use std::{net::SocketAddr, sync::Arc};

use argon2::{Argon2, PasswordHash, PasswordHasher, PasswordVerifier, password_hash::SaltString};
use axum::{
    Json,
    extract::{ConnectInfo, State},
    http::StatusCode,
};
use axum_extra::extract::{CookieJar, cookie::Cookie};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use email_address::EmailAddress;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use sqlx::{FromRow, Postgres, Transaction};
use time::{Duration, OffsetDateTime};
use uuid::Uuid;

use super::{AppState, api_error::ApiError, idempotency};

const SESSION_COOKIE: &str = "sumi_session";

#[derive(Debug, Deserialize)]
pub struct RegisterRequest {
    pub display_name: String,
    pub email: String,
    pub password: String,
}

#[derive(Serialize)]
struct RegisterFingerprint<'a> {
    display_name: &'a str,
    email: &'a str,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct UserResponse {
    pub id: Uuid,
    pub display_name: String,
    pub email: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct RegisterResponse {
    pub user: UserResponse,
    pub next: String,
}

#[derive(Debug, Deserialize)]
pub struct LoginRequest {
    pub email: String,
    pub password: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct LoginResponse {
    pub user: UserResponse,
}

#[derive(FromRow)]
struct LoginUser {
    id: Uuid,
    display_name: String,
    email: String,
    password_hash: String,
}

#[derive(Clone, Debug, FromRow)]
pub struct CurrentUser {
    pub id: Uuid,
    pub display_name: String,
    pub email: String,
}

pub async fn register(
    State(state): State<Arc<AppState>>,
    ConnectInfo(address): ConnectInfo<SocketAddr>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Json(request): Json<RegisterRequest>,
) -> Result<(StatusCode, CookieJar, Json<RegisterResponse>), ApiError> {
    state.auth_rate_limits.check_ip(address.ip().to_string())?;
    let (display_name, email) = validate_registration(&request)?;
    state.auth_rate_limits.check_email(&email)?;
    let fingerprint = RegisterFingerprint {
        display_name: &display_name,
        email: &email,
    };
    let request_hash = idempotency::request_hash(&fingerprint)?;
    let scope = format!("auth:register:{}", token_hash(&email));
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;

    if let Some((status, response)) =
        idempotency::begin::<RegisterResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        let password_hash: String =
            sqlx::query_scalar("SELECT password_hash FROM users WHERE id = $1")
                .bind(response.user.id)
                .fetch_one(&mut *transaction)
                .await
                .map_err(ApiError::database)?;
        if !verify_password(request.password, password_hash).await? {
            return Err(ApiError::conflict(
                "idempotency_conflict",
                "Idempotency-Key was already used with a different request",
            ));
        }
        let cookie = create_session(&mut transaction, response.user.id, &state).await?;
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok((status, jar.add(cookie), Json(response)));
    }

    let password_hash = hash_password(request.password).await?;
    let user_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let insert = sqlx::query(
        "INSERT INTO users (id, email_normalized, password_hash, display_name, created_at) \
         VALUES ($1, $2, $3, $4, $5)",
    )
    .bind(user_id)
    .bind(&email)
    .bind(password_hash)
    .bind(&display_name)
    .bind(now)
    .execute(&mut *transaction)
    .await;
    if let Err(error) = insert {
        if is_unique_constraint(&error, "users_email_normalized_key") {
            return Err(ApiError::conflict(
                "email_taken",
                "An account already uses this email",
            ));
        }
        return Err(ApiError::database(error));
    }

    let response = RegisterResponse {
        user: UserResponse {
            id: user_id,
            display_name,
            email,
        },
        next: "/spaces/new".to_owned(),
    };
    let cookie = create_session(&mut transaction, user_id, &state).await?;
    idempotency::finish(
        &mut transaction,
        &scope,
        key,
        StatusCode::CREATED,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;

    Ok((StatusCode::CREATED, jar.add(cookie), Json(response)))
}

pub async fn login(
    State(state): State<Arc<AppState>>,
    ConnectInfo(address): ConnectInfo<SocketAddr>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Json(request): Json<LoginRequest>,
) -> Result<(StatusCode, CookieJar, Json<LoginResponse>), ApiError> {
    state.auth_rate_limits.check_ip(address.ip().to_string())?;
    let email = request.email.trim().to_lowercase();
    if !EmailAddress::is_valid(&email) {
        return Err(ApiError::InvalidCredentials);
    }
    state.auth_rate_limits.check_email(&email)?;

    let user = sqlx::query_as::<_, LoginUser>(
        "SELECT id, display_name, email_normalized::text AS email, password_hash \
         FROM users WHERE email_normalized = $1 AND disabled_at IS NULL",
    )
    .bind(&email)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?;
    let Some(user) = user else {
        hash_password(request.password).await?;
        return Err(ApiError::InvalidCredentials);
    };
    if !verify_password(request.password, user.password_hash).await? {
        return Err(ApiError::InvalidCredentials);
    }

    let scope = format!("auth:login:{}", token_hash(&email));
    let request_hash = idempotency::request_hash(&email)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    if let Some((status, response)) =
        idempotency::begin::<LoginResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        if response.user.id != user.id {
            return Err(ApiError::Internal);
        }
        let cookie = create_session(&mut transaction, user.id, &state).await?;
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok((status, jar.add(cookie), Json(response)));
    }

    let response = LoginResponse {
        user: UserResponse {
            id: user.id,
            display_name: user.display_name,
            email: user.email,
        },
    };
    let cookie = create_session(&mut transaction, user.id, &state).await?;
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok((StatusCode::OK, jar.add(cookie), Json(response)))
}

pub async fn logout(
    State(state): State<Arc<AppState>>,
    jar: CookieJar,
    _key: idempotency::IdempotencyKey,
) -> Result<(StatusCode, CookieJar), ApiError> {
    if let Some(cookie) = jar.get(SESSION_COOKIE) {
        sqlx::query("DELETE FROM sessions WHERE token_hash = $1")
            .bind(token_hash(cookie.value()))
            .execute(&state.database)
            .await
            .map_err(ApiError::database)?;
    }
    let removal = Cookie::build(SESSION_COOKIE).path("/").build();
    Ok((StatusCode::NO_CONTENT, jar.remove(removal)))
}

pub async fn me(
    State(state): State<Arc<AppState>>,
    jar: CookieJar,
) -> Result<Json<UserResponse>, ApiError> {
    let user = current_user(&state, &jar).await?;
    Ok(Json(UserResponse {
        id: user.id,
        display_name: user.display_name,
        email: user.email,
    }))
}

pub async fn current_user(state: &AppState, jar: &CookieJar) -> Result<CurrentUser, ApiError> {
    let token = jar
        .get(SESSION_COOKIE)
        .map(|cookie| cookie.value())
        .ok_or(ApiError::Unauthorized)?;
    let now = OffsetDateTime::now_utc();
    let user = sqlx::query_as::<_, CurrentUser>(
        "SELECT users.id, users.display_name, users.email_normalized::text AS email \
         FROM sessions JOIN users ON users.id = sessions.user_id \
         WHERE sessions.token_hash = $1 AND sessions.expires_at > $2 \
           AND users.disabled_at IS NULL",
    )
    .bind(token_hash(token))
    .bind(now)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?
    .ok_or(ApiError::Unauthorized)?;

    sqlx::query("UPDATE sessions SET last_seen_at = $2 WHERE token_hash = $1")
        .bind(token_hash(token))
        .bind(now)
        .execute(&state.database)
        .await
        .map_err(ApiError::database)?;
    Ok(user)
}

async fn create_session(
    transaction: &mut Transaction<'_, Postgres>,
    user_id: Uuid,
    state: &AppState,
) -> Result<Cookie<'static>, ApiError> {
    let mut token_bytes = [0_u8; 32];
    getrandom::fill(&mut token_bytes).map_err(|error| {
        tracing::error!(error = %error, "failed to generate Session token");
        ApiError::Internal
    })?;
    let token = URL_SAFE_NO_PAD.encode(token_bytes);
    let now = OffsetDateTime::now_utc();
    let ttl = Duration::hours(state.session_ttl_hours);
    sqlx::query(
        "INSERT INTO sessions \
         (id, user_id, token_hash, expires_at, last_seen_at, created_at) \
         VALUES ($1, $2, $3, $4, $5, $5)",
    )
    .bind(Uuid::now_v7())
    .bind(user_id)
    .bind(token_hash(&token))
    .bind(now + ttl)
    .bind(now)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;

    Ok(Cookie::build((SESSION_COOKIE, token))
        .path("/")
        .http_only(true)
        .same_site(axum_extra::extract::cookie::SameSite::Lax)
        .secure(state.secure_cookies)
        .max_age(ttl)
        .build())
}

fn validate_registration(request: &RegisterRequest) -> Result<(String, String), ApiError> {
    let display_name = request.display_name.trim().to_owned();
    if !(1..=40).contains(&display_name.chars().count()) {
        return Err(ApiError::validation(
            "invalid_display_name",
            "Display name must contain 1 to 40 characters",
        ));
    }
    let email = request.email.trim().to_lowercase();
    if !EmailAddress::is_valid(&email) {
        return Err(ApiError::validation(
            "invalid_email",
            "Email address is invalid",
        ));
    }
    if request.password.chars().count() < 10 {
        return Err(ApiError::validation(
            "invalid_password",
            "Password must contain at least 10 characters",
        ));
    }
    Ok((display_name, email))
}

async fn hash_password(password: String) -> Result<String, ApiError> {
    tokio::task::spawn_blocking(move || {
        let mut salt_bytes = [0_u8; 16];
        getrandom::fill(&mut salt_bytes).map_err(|_| ())?;
        let salt = SaltString::encode_b64(&salt_bytes).map_err(|_| ())?;
        Argon2::default()
            .hash_password(password.as_bytes(), &salt)
            .map(|hash| hash.to_string())
            .map_err(|_| ())
    })
    .await
    .map_err(|error| {
        tracing::error!(error = %error, "password hashing task failed");
        ApiError::Internal
    })?
    .map_err(|()| ApiError::Internal)
}

async fn verify_password(password: String, encoded: String) -> Result<bool, ApiError> {
    tokio::task::spawn_blocking(move || {
        let Ok(hash) = PasswordHash::new(&encoded) else {
            return false;
        };
        Argon2::default()
            .verify_password(password.as_bytes(), &hash)
            .is_ok()
    })
    .await
    .map_err(|error| {
        tracing::error!(error = %error, "password verification task failed");
        ApiError::Internal
    })
}

pub(super) fn token_hash(token: &str) -> String {
    URL_SAFE_NO_PAD.encode(Sha256::digest(token.as_bytes()))
}

fn is_unique_constraint(error: &sqlx::Error, constraint: &str) -> bool {
    error
        .as_database_error()
        .and_then(|database| database.constraint())
        == Some(constraint)
}
