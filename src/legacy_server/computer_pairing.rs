use axum::{
    Json,
    extract::{ConnectInfo, Path, Query, State},
    http::{HeaderMap, StatusCode},
};
use axum_extra::extract::CookieJar;
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;
use time::{Duration, OffsetDateTime};
use uuid::Uuid;

use super::{AppState, api_error::ApiError, auth, idempotency, member};

#[derive(Deserialize)]
pub struct PairingStartRequest {
    pub token_hash: String,
    pub hostname: String,
    pub os: String,
    pub daemon_version: String,
}

#[derive(Serialize, Deserialize)]
pub struct PairingStartResponse {
    pub pairing_id: Uuid,
    pub code: String,
    pub browser_path: String,
    #[serde(with = "time::serde::rfc3339")]
    pub expires_at: OffsetDateTime,
}

pub async fn start(
    State(state): State<std::sync::Arc<AppState>>,
    ConnectInfo(address): ConnectInfo<std::net::SocketAddr>,
    Json(request): Json<PairingStartRequest>,
) -> Result<(StatusCode, Json<PairingStartResponse>), ApiError> {
    state
        .auth_rate_limits
        .check_pairing_ip(address.ip().to_string())?;
    if !matches!(request.os.as_str(), "macos" | "linux")
        || request.hostname.trim().is_empty()
        || request.hostname.chars().count() > 255
    {
        return Err(ApiError::validation(
            "invalid_computer_metadata",
            "Computer hostname and OS are invalid",
        ));
    }
    let token_hash =
        super::computer_auth::decode_hash(&request.token_hash, "invalid_computer_token_hash")?;
    let mut code_bytes = [0_u8; 12];
    getrandom::fill(&mut code_bytes).map_err(|_| ApiError::Internal)?;
    let code = URL_SAFE_NO_PAD.encode(code_bytes);
    let pairing_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let expires_at = now + Duration::minutes(10);
    sqlx::query(
        "INSERT INTO computer_pairings \
         (id, pairing_code_hash, token_hash, hostname, os, daemon_version, expires_at, created_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
    )
    .bind(pairing_id)
    .bind(Sha256::digest(code.as_bytes()).to_vec())
    .bind(token_hash.to_vec())
    .bind(request.hostname.trim())
    .bind(request.os)
    .bind(request.daemon_version)
    .bind(expires_at)
    .bind(now)
    .execute(&state.database)
    .await
    .map_err(ApiError::database)?;
    let browser_path = format!("/pair-computer/{pairing_id}?code={code}");
    Ok((
        StatusCode::CREATED,
        Json(PairingStartResponse {
            pairing_id,
            code,
            browser_path,
            expires_at,
        }),
    ))
}

#[derive(Deserialize, Serialize, utoipa::ToSchema)]
pub struct ConfirmPairingRequest {
    pub space_id: Uuid,
    pub name: String,
    pub code: String,
}

#[derive(Serialize, Deserialize, utoipa::ToSchema)]
pub struct ComputerResponse {
    pub id: Uuid,
    pub space_id: Uuid,
    pub name: String,
    pub hostname: String,
    pub os: String,
    pub status: String,
    pub daemon_version: String,
    #[serde(with = "time::serde::rfc3339::option")]
    pub last_seen_at: Option<OffsetDateTime>,
    #[serde(with = "time::serde::rfc3339")]
    pub created_at: OffsetDateTime,
}

#[derive(sqlx::FromRow)]
struct ConfirmPairingRow {
    pairing_code_hash: Vec<u8>,
    token_hash: Vec<u8>,
    hostname: String,
    os: String,
    daemon_version: String,
    expires_at: OffsetDateTime,
    status: String,
}

#[derive(sqlx::FromRow)]
struct PairingDetailsRow {
    pairing_code_hash: Vec<u8>,
    token_hash: Vec<u8>,
    hostname: String,
    os: String,
    daemon_version: String,
    expires_at: OffsetDateTime,
    status: String,
}

#[derive(sqlx::FromRow)]
struct PairingResultRow {
    token_hash: Vec<u8>,
    status: String,
    expires_at: OffsetDateTime,
    computer_id: Option<Uuid>,
    space_id: Option<Uuid>,
}

pub async fn confirm(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(pairing_id): Path<Uuid>,
    Json(mut request): Json<ConfirmPairingRequest>,
) -> Result<(StatusCode, Json<ComputerResponse>), ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    request.name = request.name.trim().to_owned();
    if !(1..=80).contains(&request.name.chars().count()) {
        return Err(ApiError::validation(
            "invalid_computer_name",
            "Computer name must contain 1 to 80 characters",
        ));
    }
    let request_hash = idempotency::request_hash(&request)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let actor = member::require_actor_tx(&mut transaction, user.id, request.space_id).await?;
    if actor.access_level != "owner" && actor.access_level != "admin" {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Only a Human Owner or Admin can confirm a Computer",
        ));
    }
    let scope = format!("computer-pairing:{pairing_id}:confirm");
    if let Some((status, response)) =
        idempotency::begin::<ComputerResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok((status, Json(response)));
    }
    let pairing: Option<ConfirmPairingRow> = sqlx::query_as(
        "SELECT pairing_code_hash, token_hash, hostname, os, daemon_version, \
                expires_at, status \
             FROM computer_pairings WHERE id = $1 FOR UPDATE",
    )
    .bind(pairing_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let pairing = pairing
        .ok_or_else(|| ApiError::not_found("pairing_not_found", "Pairing request was not found"))?;
    if pairing.status != "pending" || pairing.expires_at <= OffsetDateTime::now_utc() {
        return Err(ApiError::gone(
            "pairing_expired",
            "Pairing request has expired",
        ));
    }
    if pairing
        .pairing_code_hash
        .as_slice()
        .ct_eq(Sha256::digest(request.code.as_bytes()).as_slice())
        .unwrap_u8()
        != 1
    {
        return Err(ApiError::forbidden(
            "invalid_pairing_code",
            "Pairing code is invalid",
        ));
    }
    let computer_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    sqlx::query(
        "INSERT INTO computers \
         (id, space_id, name, hostname, os, token_hash, daemon_version, created_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
    )
    .bind(computer_id)
    .bind(request.space_id)
    .bind(&request.name)
    .bind(&pairing.hostname)
    .bind(&pairing.os)
    .bind(pairing.token_hash)
    .bind(&pairing.daemon_version)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    sqlx::query(
        "UPDATE computer_pairings SET status = 'confirmed', space_id = $2, \
         confirmed_by_member_id = $3, computer_id = $4 WHERE id = $1",
    )
    .bind(pairing_id)
    .bind(request.space_id)
    .bind(actor.id)
    .bind(computer_id)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let response = ComputerResponse {
        id: computer_id,
        space_id: request.space_id,
        name: request.name,
        hostname: pairing.hostname,
        os: pairing.os,
        status: "offline".to_owned(),
        daemon_version: pairing.daemon_version,
        last_seen_at: None,
        created_at: now,
    };
    idempotency::finish(
        &mut transaction,
        &scope,
        key,
        StatusCode::CREATED,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok((StatusCode::CREATED, Json(response)))
}

#[derive(Serialize, Deserialize)]
#[serde(tag = "status", rename_all = "snake_case")]
pub enum PairingResultResponse {
    Pending,
    Confirmed { computer_id: Uuid, space_id: Uuid },
}

#[derive(Serialize, Deserialize, utoipa::ToSchema)]
pub struct PairingDetailsResponse {
    pub pairing_id: Uuid,
    pub hostname: String,
    pub os: String,
    pub daemon_version: String,
    pub token_fingerprint: String,
    #[serde(with = "time::serde::rfc3339")]
    pub expires_at: OffsetDateTime,
    pub status: String,
}

#[derive(Deserialize)]
pub struct PairingDetailsQuery {
    code: String,
}

pub async fn details(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(pairing_id): Path<Uuid>,
    Query(query): Query<PairingDetailsQuery>,
) -> Result<Json<PairingDetailsResponse>, ApiError> {
    auth::current_user(&state, &jar).await?;
    let pairing: Option<PairingDetailsRow> = sqlx::query_as(
        "SELECT pairing_code_hash, token_hash, hostname, os, daemon_version, expires_at, status \
             FROM computer_pairings WHERE id = $1",
    )
    .bind(pairing_id)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?;
    let pairing = pairing
        .ok_or_else(|| ApiError::not_found("pairing_not_found", "Pairing request was not found"))?;
    if pairing
        .pairing_code_hash
        .as_slice()
        .ct_eq(Sha256::digest(query.code.as_bytes()).as_slice())
        .unwrap_u8()
        != 1
    {
        return Err(ApiError::forbidden(
            "invalid_pairing_code",
            "Pairing code is invalid",
        ));
    }
    Ok(Json(PairingDetailsResponse {
        pairing_id,
        hostname: pairing.hostname,
        os: pairing.os,
        daemon_version: pairing.daemon_version,
        token_fingerprint: pairing
            .token_hash
            .iter()
            .take(6)
            .map(|byte| format!("{byte:02x}"))
            .collect::<Vec<_>>()
            .join(":"),
        expires_at: pairing.expires_at,
        status: pairing.status,
    }))
}

pub async fn result(
    State(state): State<std::sync::Arc<AppState>>,
    headers: HeaderMap,
    Path(pairing_id): Path<Uuid>,
) -> Result<Json<PairingResultResponse>, ApiError> {
    let token = super::computer_auth::bearer(&headers)?;
    let token_hash = Sha256::digest(token.as_bytes());
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let pairing: Option<PairingResultRow> = sqlx::query_as(
        "SELECT token_hash, status, expires_at, computer_id, space_id \
         FROM computer_pairings WHERE id = $1",
    )
    .bind(pairing_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let pairing = pairing
        .ok_or_else(|| ApiError::not_found("pairing_not_found", "Pairing request was not found"))?;
    if pairing
        .token_hash
        .as_slice()
        .ct_eq(token_hash.as_slice())
        .unwrap_u8()
        != 1
    {
        return Err(ApiError::Unauthorized);
    }
    if pairing.status == "pending" && pairing.expires_at > OffsetDateTime::now_utc() {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(PairingResultResponse::Pending));
    }
    if pairing.status == "pending" {
        sqlx::query("UPDATE computer_pairings SET status = 'expired' WHERE id = $1")
            .bind(pairing_id)
            .execute(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
        transaction.commit().await.map_err(ApiError::database)?;
        return Err(ApiError::gone(
            "pairing_expired",
            "Pairing request expired before confirmation",
        ));
    }
    let (computer_id, space_id) = pairing.computer_id.zip(pairing.space_id).ok_or_else(|| {
        ApiError::gone(
            "pairing_expired",
            "Pairing request expired before confirmation",
        )
    })?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(PairingResultResponse::Confirmed {
        computer_id,
        space_id,
    }))
}
