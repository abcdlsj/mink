use axum::{
    Json,
    extract::{ConnectInfo, Path, Query, State, WebSocketUpgrade, ws},
    http::{HeaderMap, StatusCode, header},
    response::Response,
};
use axum_extra::extract::CookieJar;
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;
use time::{Duration, OffsetDateTime};
use uuid::Uuid;

use super::{
    AppState, api_error::ApiError, attachment, auth, channel, idempotency, member, message,
};
use crate::local_protocol::AgentAction;

#[derive(Deserialize)]
pub struct PairingStartRequest {
    pub pairing_secret_hash: String,
    pub credential_hash: String,
    pub public_key: String,
    pub hostname: String,
    pub os: String,
    pub daemon_version: String,
}

#[derive(Serialize, Deserialize)]
pub struct PairingStartResponse {
    pub pairing_id: Uuid,
    pub code: String,
    pub browser_path: String,
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
    let pairing_secret_hash = decode_32(&request.pairing_secret_hash, "invalid_pairing_secret")?;
    let credential_hash = decode_32(&request.credential_hash, "invalid_computer_credential")?;
    let public_key = URL_SAFE_NO_PAD.decode(&request.public_key).map_err(|_| {
        ApiError::validation("invalid_public_key", "Computer public key is invalid")
    })?;
    p256::PublicKey::from_sec1_bytes(&public_key).map_err(|_| {
        ApiError::validation("invalid_public_key", "Computer public key is invalid")
    })?;
    let mut code_bytes = [0_u8; 12];
    getrandom::fill(&mut code_bytes).map_err(|_| ApiError::Internal)?;
    let code = URL_SAFE_NO_PAD.encode(code_bytes);
    let pairing_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let expires_at = now + Duration::minutes(10);
    sqlx::query(
        "INSERT INTO computer_pairings \
         (id, pairing_code_hash, pairing_secret_hash, credential_hash, public_key, hostname, os, \
          daemon_version, expires_at, created_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)",
    )
    .bind(pairing_id)
    .bind(Sha256::digest(code.as_bytes()).to_vec())
    .bind(pairing_secret_hash.to_vec())
    .bind(credential_hash.to_vec())
    .bind(public_key)
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

#[derive(Deserialize, Serialize)]
pub struct ConfirmPairingRequest {
    pub space_id: Uuid,
    pub name: String,
    pub code: String,
}

#[derive(Serialize, Deserialize)]
pub struct ComputerResponse {
    pub id: Uuid,
    pub space_id: Uuid,
    pub name: String,
    pub hostname: String,
    pub os: String,
    pub status: String,
    pub daemon_version: String,
    pub last_seen_at: Option<OffsetDateTime>,
    pub created_at: OffsetDateTime,
}

#[derive(sqlx::FromRow)]
struct ConfirmPairingRow {
    pairing_code_hash: Vec<u8>,
    credential_hash: Vec<u8>,
    public_key: Vec<u8>,
    hostname: String,
    os: String,
    daemon_version: String,
    expires_at: OffsetDateTime,
    status: String,
}

#[derive(sqlx::FromRow)]
struct PairingDetailsRow {
    pairing_code_hash: Vec<u8>,
    public_key: Vec<u8>,
    hostname: String,
    os: String,
    daemon_version: String,
    expires_at: OffsetDateTime,
    status: String,
}

#[derive(sqlx::FromRow)]
struct PairingResultRow {
    pairing_secret_hash: Vec<u8>,
    status: String,
    expires_at: OffsetDateTime,
    computer_id: Option<Uuid>,
    space_id: Option<Uuid>,
}

#[derive(Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
enum ComputerFrame {
    Hello {
        last_acked_computer_seq: i64,
    },
    Heartbeat {
        daemon_version: String,
        os: String,
        cpu_count: usize,
        memory_total_bytes: Option<u64>,
        agents_count: u32,
        active_runs: u32,
    },
    CommandAck {
        command_id: Uuid,
        computer_seq: i64,
    },
    CommandResult {
        command_id: Uuid,
        computer_seq: i64,
        ok: bool,
        result: serde_json::Value,
    },
}

#[derive(Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
enum ServerFrame {
    Welcome {
        heartbeat_interval_seconds: u64,
    },
    Command {
        command_id: Uuid,
        computer_seq: i64,
        kind: String,
        payload: serde_json::Value,
    },
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
        "SELECT pairing_code_hash, credential_hash, public_key, hostname, os, daemon_version, \
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
         (id, space_id, name, hostname, os, public_key, credential_hash, daemon_version, created_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)",
    )
    .bind(computer_id)
    .bind(request.space_id)
    .bind(&request.name)
    .bind(&pairing.hostname)
    .bind(&pairing.os)
    .bind(pairing.public_key)
    .bind(pairing.credential_hash)
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

#[derive(Serialize, Deserialize)]
pub struct PairingDetailsResponse {
    pub pairing_id: Uuid,
    pub hostname: String,
    pub os: String,
    pub daemon_version: String,
    pub public_key_fingerprint: String,
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
        "SELECT pairing_code_hash, public_key, hostname, os, daemon_version, expires_at, status \
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
    let fingerprint = Sha256::digest(&pairing.public_key);
    Ok(Json(PairingDetailsResponse {
        pairing_id,
        hostname: pairing.hostname,
        os: pairing.os,
        daemon_version: pairing.daemon_version,
        public_key_fingerprint: fingerprint
            .chunks(2)
            .map(hex::encode)
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
    let secret = bearer(&headers)?;
    let secret = decode_32(secret, "invalid_pairing_secret").map_err(|_| ApiError::Unauthorized)?;
    let secret_hash = Sha256::digest(secret);
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let pairing: Option<PairingResultRow> = sqlx::query_as(
        "SELECT pairing_secret_hash, status, expires_at, computer_id, space_id \
         FROM computer_pairings WHERE id = $1",
    )
    .bind(pairing_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let pairing = pairing
        .ok_or_else(|| ApiError::not_found("pairing_not_found", "Pairing request was not found"))?;
    if pairing.pairing_secret_hash != secret_hash.as_slice() {
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

pub async fn list(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<ComputerResponse>>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    member::require_actor_tx(&mut transaction, user.id, space_id).await?;
    let computers = sqlx::query_as::<
        _,
        (
            Uuid,
            Uuid,
            String,
            String,
            String,
            String,
            String,
            Option<OffsetDateTime>,
            OffsetDateTime,
        ),
    >(
        "SELECT id, space_id, name, hostname, os, status, daemon_version, last_seen_at, created_at \
         FROM computers WHERE space_id = $1 ORDER BY created_at DESC",
    )
    .bind(space_id)
    .fetch_all(&mut *transaction)
    .await
    .map_err(ApiError::database)?
    .into_iter()
    .map(|row| ComputerResponse {
        id: row.0,
        space_id: row.1,
        name: row.2,
        hostname: row.3,
        os: row.4,
        status: row.5,
        daemon_version: row.6,
        last_seen_at: row.7,
        created_at: row.8,
    })
    .collect();
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(computers))
}

pub async fn revoke(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(computer_id): Path<Uuid>,
) -> Result<Json<ComputerResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let space_id: Uuid = sqlx::query_scalar("SELECT space_id FROM computers WHERE id = $1")
        .bind(computer_id)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(ApiError::database)?
        .ok_or_else(|| ApiError::not_found("computer_not_found", "Computer was not found"))?;
    let actor = member::require_actor_tx(&mut transaction, user.id, space_id).await?;
    if actor.access_level != "owner" && actor.access_level != "admin" {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Only a Human Owner or Admin can revoke a Computer",
        ));
    }
    let request_hash = idempotency::request_hash(&serde_json::json!({}))?;
    let scope = format!("computer:{computer_id}:revoke");
    if let Some((_, response)) =
        idempotency::begin::<ComputerResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }
    let row = sqlx::query_as::<_, (Uuid, Uuid, String, String, String, String, String, Option<OffsetDateTime>, OffsetDateTime)>(
        "UPDATE computers SET status = 'revoked', revoked_at = COALESCE(revoked_at, now()) \
         WHERE id = $1 RETURNING id, space_id, name, hostname, os, status, daemon_version, last_seen_at, created_at",
    )
    .bind(computer_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    insert_status_event(&mut transaction, row.1, computer_id, "revoked").await?;
    let response = ComputerResponse {
        id: row.0,
        space_id: row.1,
        name: row.2,
        hostname: row.3,
        os: row.4,
        status: row.5,
        daemon_version: row.6,
        last_seen_at: row.7,
        created_at: row.8,
    };
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(response))
}

pub async fn connect(
    State(state): State<std::sync::Arc<AppState>>,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
    upgrade: WebSocketUpgrade,
) -> Result<Response, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    Ok(upgrade.on_upgrade(move |socket| computer_socket(state, computer_id, socket)))
}

pub(super) async fn authenticate_computer(
    state: &AppState,
    headers: &HeaderMap,
    computer_id: Uuid,
) -> Result<(), ApiError> {
    let credential = bearer(headers)?;
    let expected: Option<(Vec<u8>, String)> =
        sqlx::query_as("SELECT credential_hash, status FROM computers WHERE id = $1")
            .bind(computer_id)
            .fetch_optional(&state.database)
            .await
            .map_err(ApiError::database)?;
    let (expected_hash, status) = expected
        .ok_or_else(|| ApiError::not_found("computer_not_found", "Computer was not found"))?;
    if status == "revoked"
        || expected_hash.len() != 32
        || expected_hash
            .as_slice()
            .ct_eq(Sha256::digest(credential.as_bytes()).as_slice())
            .unwrap_u8()
            != 1
    {
        return Err(ApiError::Unauthorized);
    }
    Ok(())
}

#[derive(Deserialize)]
pub struct AgentActionRequest {
    agent_member_id: Uuid,
    run_id: Uuid,
    action: AgentAction,
}

pub async fn agent_action(
    State(state): State<std::sync::Arc<AppState>>,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
    Json(request): Json<AgentActionRequest>,
) -> Result<Json<serde_json::Value>, ApiError> {
    require_active_agent_run(
        &state,
        &headers,
        computer_id,
        request.agent_member_id,
        request.run_id,
    )
    .await?;
    let data = match request.action {
        AgentAction::MemberList { query } => {
            agent_member_list(&state.database, request.agent_member_id, query.as_deref()).await?
        }
        AgentAction::ChannelList => {
            agent_channel_list(&state.database, request.agent_member_id).await?
        }
        AgentAction::InboxCurrent => {
            agent_inbox_current(&state.database, request.agent_member_id, request.run_id).await?
        }
        AgentAction::InboxShow { inbox_item_id } => {
            agent_inbox_show(
                &state.database,
                request.agent_member_id,
                request.run_id,
                inbox_item_id,
            )
            .await?
        }
        AgentAction::ChannelRead {
            address,
            before,
            after,
            around,
            limit,
        } => {
            agent_channel_read(
                &state.database,
                request.agent_member_id,
                &address,
                before,
                after,
                around,
                limit,
            )
            .await?
        }
        AgentAction::ChannelCreate {
            slug,
            name,
            private,
            idempotency_key,
        } => serde_json::to_value(
            channel::create_for_agent(
                &state.database,
                request.agent_member_id,
                channel::CreateChannelRequest {
                    name,
                    slug,
                    kind: if private { "private" } else { "public" }.to_owned(),
                    topic: None,
                },
                idempotency_key,
            )
            .await?,
        )
        .map_err(|_| ApiError::Internal)?,
        AgentAction::ThreadRead {
            address,
            after,
            limit,
            include_channel,
        } => {
            agent_thread_read(
                &state.database,
                request.agent_member_id,
                &address,
                after,
                limit,
                include_channel,
            )
            .await?
        }
        AgentAction::MessageSend {
            address,
            body_markdown,
            based_on,
            handle_inbox_item_id,
            attachment_ids,
            idempotency_key,
        } => {
            agent_message_send(
                &state.database,
                request.agent_member_id,
                request.run_id,
                &address,
                body_markdown,
                based_on,
                handle_inbox_item_id,
                &attachment_ids,
                idempotency_key,
            )
            .await?
        }
        AgentAction::InboxAck {
            inbox_item_ids,
            reason,
            idempotency_key,
        } => {
            agent_inbox_ack(
                &state.database,
                request.agent_member_id,
                request.run_id,
                &inbox_item_ids,
                &reason,
                idempotency_key,
            )
            .await?
        }
        AgentAction::InboxDefer {
            inbox_item_ids,
            until,
            idempotency_key,
        } => {
            agent_inbox_defer(
                &state.database,
                request.agent_member_id,
                request.run_id,
                &inbox_item_ids,
                until,
                idempotency_key,
            )
            .await?
        }
        AgentAction::AgentCreate {
            name,
            role_text,
            computer_id,
            driver_kind,
            idempotency_key,
        } => serde_json::to_value(
            super::approval::request_agent_create(
                &state.database,
                request.agent_member_id,
                super::agent_registry::CreateAgentRequest {
                    computer_id,
                    name,
                    handle: None,
                    role_text,
                    access_level: "member".to_owned(),
                    driver_kind,
                },
                idempotency_key,
            )
            .await?,
        )
        .map_err(|_| ApiError::Internal)?,
        AgentAction::AttachmentUpload { .. }
        | AgentAction::AttachmentDownload { .. }
        | AgentAction::AttachmentInfo { .. } => {
            return Err(ApiError::validation(
                "invalid_attachment_transport",
                "Attachment actions must use the streaming Agent Attachment API",
            ));
        }
    };
    Ok(Json(data))
}

pub(super) async fn require_active_agent_run(
    state: &AppState,
    headers: &HeaderMap,
    computer_id: Uuid,
    agent_member_id: Uuid,
    run_id: Uuid,
) -> Result<Uuid, ApiError> {
    authenticate_computer(state, headers, computer_id).await?;
    sqlx::query_scalar(
        "SELECT members.space_id FROM agent_runs \
         JOIN agents ON agents.member_id = agent_runs.agent_member_id \
         JOIN members ON members.id = agents.member_id \
         WHERE agent_runs.id = $1 AND agent_runs.agent_member_id = $2 \
           AND agent_runs.computer_id = $3 AND agent_runs.status = 'running' \
           AND agents.status = 'active' AND members.retired_at IS NULL",
    )
    .bind(run_id)
    .bind(agent_member_id)
    .bind(computer_id)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| {
        ApiError::forbidden(
            "permission_denied",
            "Agent run is not active on this Computer",
        )
    })
}

async fn agent_member_list(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    query: Option<&str>,
) -> Result<serde_json::Value, ApiError> {
    if query.is_some_and(|query| query.chars().count() > 100) {
        return Err(ApiError::validation(
            "invalid_member_query",
            "Member query must contain at most 100 characters",
        ));
    }
    let members: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object('id', members.id, 'kind', members.kind, \
            'display_name', members.display_name, 'handle', members.handle, \
            'access_level', members.access_level) FROM members \
         WHERE members.space_id = (SELECT space_id FROM agents WHERE member_id = $1) \
           AND members.retired_at IS NULL AND ($2::text IS NULL \
             OR members.display_name ILIKE '%' || $2 || '%' OR members.handle ILIKE '%' || $2 || '%') \
         ORDER BY lower(members.display_name), members.id LIMIT 100",
    )
    .bind(agent_id)
    .bind(query.map(str::trim).filter(|query| !query.is_empty()))
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    Ok(serde_json::json!({ "members": members }))
}

async fn agent_channel_list(
    database: &sqlx::PgPool,
    agent_id: Uuid,
) -> Result<serde_json::Value, ApiError> {
    let channels: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object('id', channels.id, 'address', '#' || channels.slug::text, \
            'kind', channels.kind, 'name', channels.name, 'topic', channels.topic, \
            'joined', EXISTS(SELECT 1 FROM channel_members own \
                WHERE own.channel_id = channels.id AND own.member_id = $1)) \
         FROM channels WHERE channels.space_id = (SELECT space_id FROM agents WHERE member_id = $1) \
           AND channels.kind != 'direct' AND channels.archived_at IS NULL \
           AND (channels.kind = 'public' OR EXISTS(SELECT 1 FROM channel_members own \
                WHERE own.channel_id = channels.id AND own.member_id = $1)) \
         ORDER BY lower(channels.name)",
    )
    .bind(agent_id)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    let dms: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object('channel_id', direct_channels.channel_id, \
            'address', '@' || other.handle, 'member', jsonb_build_object('id', other.id, \
                'kind', other.kind, 'display_name', other.display_name, 'handle', other.handle)) \
         FROM direct_channels JOIN members other ON other.id = CASE \
             WHEN direct_channels.member_low_id = $1 THEN direct_channels.member_high_id \
             ELSE direct_channels.member_low_id END \
         WHERE $1 IN (direct_channels.member_low_id, direct_channels.member_high_id) \
         ORDER BY lower(other.display_name)",
    )
    .bind(agent_id)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    Ok(serde_json::json!({ "channels": channels, "direct_messages": dms }))
}

#[derive(Serialize)]
pub struct HostedAgentResponse {
    member_id: Uuid,
    status: String,
}

pub async fn list_hosted_agents(
    State(state): State<std::sync::Arc<AppState>>,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
) -> Result<Json<Vec<HostedAgentResponse>>, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    let agents: Vec<(Uuid, String)> = sqlx::query_as(
        "SELECT member_id, status FROM agents WHERE computer_id = $1 AND status != 'retired' \
         ORDER BY created_at",
    )
    .bind(computer_id)
    .fetch_all(&state.database)
    .await
    .map_err(ApiError::database)?;
    Ok(Json(
        agents
            .into_iter()
            .map(|(member_id, status)| HostedAgentResponse { member_id, status })
            .collect(),
    ))
}

#[derive(Serialize)]
pub struct AgentClaimResponse {
    claimed: bool,
    run_id: Option<Uuid>,
    inbox_item_ids: Vec<Uuid>,
}

#[derive(Deserialize, Serialize)]
pub struct AgentLeaseRequest {
    run_id: Uuid,
}

#[derive(Serialize)]
pub struct AgentLeaseResponse {
    renewed_items: u64,
    lease_expires_at: OffsetDateTime,
}

#[derive(Deserialize, Serialize)]
pub struct AgentReleaseRequest {
    run_id: Uuid,
    error_code: String,
}

#[derive(Serialize)]
pub struct AgentReleaseResponse {
    released: bool,
    retry_items: u64,
    dead_items: u64,
}

#[derive(sqlx::FromRow)]
struct ClaimableInboxItem {
    id: Uuid,
    priority: String,
    kind: String,
    source_address: Option<String>,
}

pub async fn claim_agent_inbox(
    State(state): State<std::sync::Arc<AppState>>,
    headers: HeaderMap,
    Path((computer_id, agent_id)): Path<(Uuid, Uuid)>,
) -> Result<Json<AgentClaimResponse>, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let agent: Option<(Uuid, i64, String, String, String, String)> = sqlx::query_as(
        "SELECT agents.space_id, agents.role_revision, agents.driver_kind, agents.role_text, \
                members.display_name, members.handle \
         FROM agents JOIN members ON members.id = agents.member_id \
         WHERE agents.member_id = $1 AND agents.computer_id = $2 AND agents.status = 'active' FOR UPDATE OF agents",
    )
    .bind(agent_id)
    .bind(computer_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let Some((space_id, role_revision, driver_kind, role_text, agent_name, agent_handle)) = agent
    else {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Agent is not active on this Computer",
        ));
    };
    let already_active: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM agent_runs WHERE agent_member_id = $1 \
         AND status IN ('queued', 'running'))",
    )
    .bind(agent_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if already_active {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(AgentClaimResponse {
            claimed: false,
            run_id: None,
            inbox_item_ids: Vec::new(),
        }));
    }
    let items: Vec<ClaimableInboxItem> = sqlx::query_as(
        "SELECT inbox_items.id, inbox_items.priority, inbox_items.kind, \
                CASE WHEN channels.kind = 'direct' THEN '@' || senders.handle \
                     WHEN inbox_items.thread_id IS NOT NULL \
                        THEN '#' || channels.slug::text || ':' || inbox_items.thread_id::text \
                     ELSE '#' || channels.slug::text END AS source_address \
         FROM inbox_items LEFT JOIN channels ON channels.id = inbox_items.channel_id \
         LEFT JOIN messages ON messages.id = inbox_items.message_id \
         LEFT JOIN members senders ON senders.id = messages.author_member_id \
         WHERE inbox_items.member_id = $1 \
           AND (inbox_items.status = 'pending' \
             OR (inbox_items.status = 'deferred' AND inbox_items.available_at <= now()) \
             OR (inbox_items.status = 'leased' AND inbox_items.lease_expires_at <= now())) \
           AND (inbox_items.available_at <= now() OR (inbox_items.status = 'pending' \
             AND inbox_items.priority = 'ambient' AND EXISTS( \
               SELECT 1 FROM inbox_items hard_items WHERE hard_items.member_id = inbox_items.member_id \
                 AND hard_items.priority = 'hard' AND hard_items.status = 'pending' \
                 AND hard_items.available_at <= now() \
                 AND hard_items.channel_id IS NOT DISTINCT FROM inbox_items.channel_id \
                 AND hard_items.thread_id IS NOT DISTINCT FROM inbox_items.thread_id))) \
         ORDER BY CASE inbox_items.priority WHEN 'hard' THEN 0 ELSE 1 END, inbox_items.created_at \
         LIMIT 10 FOR UPDATE OF inbox_items SKIP LOCKED",
    )
    .bind(agent_id)
    .fetch_all(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if items.is_empty() {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(AgentClaimResponse {
            claimed: false,
            run_id: None,
            inbox_item_ids: Vec::new(),
        }));
    }
    let run_id = Uuid::now_v7();
    let lease_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let expires_at = now + time::Duration::minutes(35);
    let item_ids = items.iter().map(|item| item.id).collect::<Vec<_>>();
    sqlx::query(
        "UPDATE inbox_items SET status = 'leased', lease_id = $2, lease_expires_at = $3 \
         WHERE id = ANY($1)",
    )
    .bind(&item_ids)
    .bind(lease_id)
    .bind(expires_at)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    sqlx::query(
        "INSERT INTO agent_runs (id, agent_member_id, computer_id, driver_kind, role_revision, \
         status, created_at) VALUES ($1, $2, $3, $4, $5, 'queued', $6)",
    )
    .bind(run_id)
    .bind(agent_id)
    .bind(computer_id)
    .bind(&driver_kind)
    .bind(role_revision)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    for item_id in &item_ids {
        sqlx::query(
            "INSERT INTO agent_run_inbox_items (run_id, inbox_item_id, lease_id) \
             VALUES ($1, $2, $3)",
        )
        .bind(run_id)
        .bind(item_id)
        .bind(lease_id)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    }
    let summaries = items
        .iter()
        .map(|item| {
            serde_json::json!({
                "id": item.id,
                "priority": item.priority,
                "kind": item.kind,
                "address": item.source_address,
            })
        })
        .collect::<Vec<_>>();
    let prompt = super::agent_prompt::build(
        &agent_name,
        &agent_handle,
        agent_id,
        role_revision,
        &role_text,
        &summaries,
    );
    let payload = serde_json::json!({
        "run_id": run_id,
        "agent_id": agent_id,
        "space_id": space_id,
        "prompt": prompt,
    });
    let command_id = Uuid::now_v7();
    let computer_seq: i64 = sqlx::query_scalar(
        "UPDATE computers SET next_command_seq = next_command_seq + 1 WHERE id = $1 \
         AND status != 'revoked' RETURNING next_command_seq - 1",
    )
    .bind(computer_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    sqlx::query(
        "INSERT INTO computer_commands (id, computer_id, computer_seq, kind, payload_json, created_at) \
         VALUES ($1, $2, $3, 'agent.run', $4, $5)",
    )
    .bind(command_id)
    .bind(computer_id)
    .bind(computer_seq)
    .bind(payload)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    sqlx::query(
        "INSERT INTO outbox_events (id, topic, aggregate_id, payload_json, created_at) \
         VALUES ($1, 'agent.run_changed', $2, $3, $4)",
    )
    .bind(Uuid::now_v7())
    .bind(run_id)
    .bind(serde_json::json!({
        "space_id": space_id,
        "agent_member_id": agent_id,
        "run_id": run_id,
        "status": "queued",
    }))
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(AgentClaimResponse {
        claimed: true,
        run_id: Some(run_id),
        inbox_item_ids: item_ids,
    }))
}

pub async fn renew_agent_inbox(
    State(state): State<std::sync::Arc<AppState>>,
    headers: HeaderMap,
    Path((computer_id, agent_id)): Path<(Uuid, Uuid)>,
    Json(request): Json<AgentLeaseRequest>,
) -> Result<Json<AgentLeaseResponse>, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    let valid_run: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM agent_runs \
         WHERE id = $1 AND agent_member_id = $2 AND computer_id = $3 \
           AND status IN ('queued', 'running'))",
    )
    .bind(request.run_id)
    .bind(agent_id)
    .bind(computer_id)
    .fetch_one(&state.database)
    .await
    .map_err(ApiError::database)?;
    if !valid_run {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Agent run is not active on this Computer",
        ));
    }
    let lease_expires_at = OffsetDateTime::now_utc() + Duration::minutes(35);
    let renewed_items = sqlx::query(
        "UPDATE inbox_items SET lease_expires_at = $2 \
         FROM agent_run_inbox_items run_items \
         WHERE run_items.run_id = $1 AND inbox_items.id = run_items.inbox_item_id \
           AND inbox_items.status = 'leased' AND inbox_items.lease_id = run_items.lease_id",
    )
    .bind(request.run_id)
    .bind(lease_expires_at)
    .execute(&state.database)
    .await
    .map_err(ApiError::database)?
    .rows_affected();
    Ok(Json(AgentLeaseResponse {
        renewed_items,
        lease_expires_at,
    }))
}

pub async fn release_agent_inbox(
    State(state): State<std::sync::Arc<AppState>>,
    headers: HeaderMap,
    Path((computer_id, agent_id)): Path<(Uuid, Uuid)>,
    Json(mut request): Json<AgentReleaseRequest>,
) -> Result<Json<AgentReleaseResponse>, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    request.error_code = request.error_code.trim().to_owned();
    if request.error_code.is_empty()
        || request.error_code.len() > 64
        || !request
            .error_code
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'_')
    {
        return Err(ApiError::validation(
            "invalid_error_code",
            "Run error code is invalid",
        ));
    }
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let run: Option<(Uuid, Uuid)> = sqlx::query_as(
        "UPDATE agent_runs SET status = 'failed', started_at = COALESCE(started_at, created_at), \
         finished_at = now(), error_code = $4 WHERE id = $1 AND agent_member_id = $2 \
         AND computer_id = $3 AND status IN ('queued', 'running') RETURNING agent_member_id, \
         (SELECT space_id FROM agents WHERE agents.member_id = agent_runs.agent_member_id)",
    )
    .bind(request.run_id)
    .bind(agent_id)
    .bind(computer_id)
    .bind(&request.error_code)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let Some((agent_id, space_id)) = run else {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(AgentReleaseResponse {
            released: false,
            retry_items: 0,
            dead_items: 0,
        }));
    };
    sqlx::query(
        "UPDATE computer_commands SET status = 'failed', result_json = $3, \
         acked_at = COALESCE(acked_at, now()), completed_at = now() \
         WHERE computer_id = $1 AND kind = 'agent.run' \
           AND payload_json->>'run_id' = $2 AND status NOT IN ('completed', 'failed')",
    )
    .bind(computer_id)
    .bind(request.run_id.to_string())
    .bind(serde_json::json!({
        "ok": false,
        "run_id": request.run_id,
        "status": "failed",
        "error_code": request.error_code,
    }))
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let (retry_items, dead_items) = release_run_inbox_items(
        &mut transaction,
        request.run_id,
        agent_id,
        space_id,
        &request.error_code,
    )
    .await?;
    insert_run_event(
        &mut transaction,
        request.run_id,
        agent_id,
        space_id,
        "failed",
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(AgentReleaseResponse {
        released: true,
        retry_items,
        dead_items,
    }))
}

async fn agent_inbox_current(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    run_id: Uuid,
) -> Result<serde_json::Value, ApiError> {
    let items: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object( \
            'id', inbox_items.id, 'kind', inbox_items.kind, 'priority', inbox_items.priority, \
            'channel_id', inbox_items.channel_id, 'channel_slug', channels.slug::text, \
            'thread_id', inbox_items.thread_id, 'message_id', inbox_items.message_id, \
            'sender_member_id', senders.id, 'sender_display_name', senders.display_name, \
            'sender_handle', senders.handle, \
            'address', CASE WHEN channels.kind = 'direct' THEN '@' || senders.handle \
                WHEN inbox_items.thread_id IS NOT NULL \
                    THEN '#' || channels.slug::text || ':' || inbox_items.thread_id::text \
                ELSE '#' || channels.slug::text END, \
            'summary', CASE WHEN messages.deleted_at IS NULL THEN left(messages.body_markdown, 160) \
                            ELSE 'Message 已删除' END, \
            'status', inbox_items.status, 'available_at', inbox_items.available_at, \
            'created_at', inbox_items.created_at) \
         FROM agent_run_inbox_items \
         JOIN inbox_items ON inbox_items.id = agent_run_inbox_items.inbox_item_id \
         LEFT JOIN channels ON channels.id = inbox_items.channel_id \
         LEFT JOIN messages ON messages.id = inbox_items.message_id \
         LEFT JOIN members senders ON senders.id = messages.author_member_id \
         WHERE agent_run_inbox_items.run_id = $1 AND inbox_items.member_id = $2 \
           AND inbox_items.status = 'leased' AND inbox_items.lease_id = agent_run_inbox_items.lease_id \
         ORDER BY CASE inbox_items.priority WHEN 'hard' THEN 0 ELSE 1 END, inbox_items.created_at",
    )
    .bind(run_id)
    .bind(agent_id)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    Ok(serde_json::json!({ "run_id": run_id, "items": items }))
}

async fn agent_inbox_show(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    run_id: Uuid,
    item_id: Uuid,
) -> Result<serde_json::Value, ApiError> {
    sqlx::query_scalar(
        "SELECT jsonb_build_object('id', inbox_items.id, 'kind', inbox_items.kind, \
            'priority', inbox_items.priority, 'channel_id', inbox_items.channel_id, \
            'address', CASE WHEN channels.kind = 'direct' THEN '@' || senders.handle \
                WHEN inbox_items.thread_id IS NOT NULL \
                    THEN '#' || channels.slug::text || ':' || inbox_items.thread_id::text \
                ELSE '#' || channels.slug::text END, 'thread_id', inbox_items.thread_id, \
            'message_id', inbox_items.message_id, 'sender', jsonb_build_object('id', senders.id, \
                'kind', senders.kind, 'display_name', senders.display_name, 'handle', senders.handle), \
            'summary', CASE WHEN messages.deleted_at IS NULL THEN left(messages.body_markdown, 160) \
                ELSE 'Message 已删除' END, 'status', inbox_items.status, \
            'available_at', inbox_items.available_at, 'created_at', inbox_items.created_at) \
         FROM agent_run_inbox_items JOIN inbox_items \
           ON inbox_items.id = agent_run_inbox_items.inbox_item_id \
         LEFT JOIN channels ON channels.id = inbox_items.channel_id \
         LEFT JOIN messages ON messages.id = inbox_items.message_id \
         LEFT JOIN members senders ON senders.id = messages.author_member_id \
         WHERE agent_run_inbox_items.run_id = $1 AND inbox_items.id = $2 \
           AND inbox_items.member_id = $3 AND inbox_items.status = 'leased' \
           AND inbox_items.lease_id = agent_run_inbox_items.lease_id",
    )
    .bind(run_id)
    .bind(item_id)
    .bind(agent_id)
    .fetch_optional(database)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| ApiError::not_found("inbox_item_not_found", "Inbox Item is not claimed by this run"))
}

async fn agent_channel_read(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    address: &str,
    before: Option<i64>,
    after: Option<i64>,
    around: Option<Uuid>,
    limit: i64,
) -> Result<serde_json::Value, ApiError> {
    let cursor_count = usize::from(before.is_some())
        + usize::from(after.is_some())
        + usize::from(around.is_some());
    if !(1..=100).contains(&limit)
        || before.is_some_and(|value| value <= 0)
        || after.is_some_and(|value| value < 0)
        || cursor_count > 1
    {
        return Err(ApiError::validation(
            "invalid_pagination",
            "Message limit must be 1 to 100 and before, after, and around are mutually exclusive",
        ));
    }
    let (channel_id, display_address) = resolve_agent_address(database, agent_id, address).await?;
    let snapshot: i64 = sqlx::query_scalar("SELECT next_seq - 1 FROM channels WHERE id = $1")
        .bind(channel_id)
        .fetch_one(database)
        .await
        .map_err(ApiError::database)?;
    let around_seq: Option<i64> = if let Some(message_id) = around {
        Some(
            sqlx::query_scalar::<_, i64>(
                "SELECT channel_seq FROM messages WHERE id = $1 AND channel_id = $2 \
                 AND thread_id IS NULL",
            )
            .bind(message_id)
            .bind(channel_id)
            .fetch_optional(database)
            .await
            .map_err(ApiError::database)?
            .ok_or_else(|| {
                ApiError::not_found(
                    "message_not_found",
                    "Around Message was not found on this Channel main timeline",
                )
            })?,
        )
    } else {
        None
    };
    let mut messages: Vec<serde_json::Value> = sqlx::query_scalar(
        "WITH candidates AS (SELECT messages.id, messages.channel_seq, \
            messages.author_member_id, messages.body_markdown, messages.created_at, \
            messages.edited_at, messages.deleted_at \
         FROM messages WHERE messages.channel_id = $1 AND messages.thread_id IS NULL \
           AND messages.channel_seq <= $2 \
           AND ($3::bigint IS NULL OR messages.channel_seq < $3) \
           AND ($4::bigint IS NULL OR messages.channel_seq > $4) \
         ORDER BY CASE WHEN $5::bigint IS NULL THEN NULL \
                       ELSE abs(messages.channel_seq - $5) END, \
                  CASE WHEN $4::bigint IS NOT NULL THEN messages.channel_seq END ASC, \
                  CASE WHEN $4::bigint IS NULL AND $5::bigint IS NULL \
                       THEN messages.channel_seq END DESC, \
                  messages.channel_seq \
         LIMIT $6) \
         SELECT jsonb_build_object( \
            'id', messages.id, 'seq', messages.channel_seq, \
            'author', jsonb_build_object('id', members.id, 'kind', members.kind, \
                'display_name', members.display_name, 'handle', members.handle), \
            'address', $7::text, \
            'body_markdown', CASE WHEN messages.deleted_at IS NULL THEN messages.body_markdown \
                                  ELSE 'Message 已删除' END, \
            'created_at', messages.created_at, 'edited_at', messages.edited_at) \
         FROM candidates messages JOIN members ON members.id = messages.author_member_id",
    )
    .bind(channel_id)
    .bind(snapshot)
    .bind(before)
    .bind(after)
    .bind(around_seq)
    .bind(limit)
    .bind(&display_address)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    messages.sort_by_key(|message| message.get("seq").and_then(serde_json::Value::as_i64));
    let first_seq = messages
        .first()
        .and_then(|message| message.get("seq"))
        .and_then(serde_json::Value::as_i64);
    let last_seq = messages
        .last()
        .and_then(|message| message.get("seq"))
        .and_then(serde_json::Value::as_i64);
    let first_boundary = first_seq.unwrap_or_else(|| {
        after
            .map(|value| value.saturating_add(1))
            .or(before)
            .unwrap_or_else(|| snapshot.saturating_add(1))
    });
    let last_boundary = last_seq.unwrap_or_else(|| {
        after
            .or_else(|| before.map(|value| value.saturating_sub(1)))
            .unwrap_or(0)
    });
    let has_more_before: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM messages WHERE channel_id = $1 AND thread_id IS NULL \
         AND channel_seq < $2)",
    )
    .bind(channel_id)
    .bind(first_boundary)
    .fetch_one(database)
    .await
    .map_err(ApiError::database)?;
    let has_more_after: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM messages WHERE channel_id = $1 AND thread_id IS NULL \
         AND channel_seq > $2 AND channel_seq <= $3)",
    )
    .bind(channel_id)
    .bind(last_boundary)
    .bind(snapshot)
    .fetch_one(database)
    .await
    .map_err(ApiError::database)?;
    enrich_agent_messages(database, &mut messages).await?;
    Ok(serde_json::json!({
        "address": display_address,
        "channel_id": channel_id,
        "thread_id": null,
        "snapshot_channel_seq": snapshot,
        "messages": messages,
        "has_more_before": has_more_before,
        "has_more_after": has_more_after,
    }))
}

async fn agent_thread_read(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    address: &str,
    after: Option<i64>,
    limit: i64,
    include_channel: i64,
) -> Result<serde_json::Value, ApiError> {
    if !(1..=100).contains(&limit)
        || !(0..=100).contains(&include_channel)
        || after.is_some_and(|value| value < 0)
    {
        return Err(ApiError::validation(
            "invalid_pagination",
            "Thread pagination is invalid",
        ));
    }
    let raw = address.strip_prefix('#').ok_or_else(|| {
        ApiError::validation("invalid_address", "Thread address must use #channel:id")
    })?;
    let (slug, thread_id) = raw
        .rsplit_once(':')
        .and_then(|(slug, id)| id.parse::<i64>().ok().map(|id| (slug, id)))
        .filter(|(slug, id)| !slug.is_empty() && *id > 0)
        .ok_or_else(|| {
            ApiError::validation("invalid_address", "Thread address must use #channel:id")
        })?;
    let access: Option<(Uuid, Uuid, i64)> = sqlx::query_as(
        "SELECT threads.channel_id, threads.root_message_id, channels.next_seq - 1 \
         FROM threads JOIN channels ON channels.id = threads.channel_id \
         JOIN channel_members ON channel_members.channel_id = threads.channel_id \
         WHERE channels.slug = $1 AND threads.thread_id = $2 \
           AND channel_members.member_id = $3",
    )
    .bind(slug)
    .bind(thread_id)
    .bind(agent_id)
    .fetch_optional(database)
    .await
    .map_err(ApiError::database)?;
    let (channel_id, root_message_id, snapshot) = access.ok_or_else(|| {
        ApiError::forbidden(
            "permission_denied",
            "Agent is not a member of this Thread Channel",
        )
    })?;
    let root = agent_message_json_pool(database, root_message_id, address).await?;
    let replies: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object('id', messages.id, 'seq', messages.channel_seq, \
            'author', jsonb_build_object('id', members.id, 'kind', members.kind, \
                'display_name', members.display_name, 'handle', members.handle), \
            'address', $5::text, 'body_markdown', CASE WHEN messages.deleted_at IS NULL \
                THEN messages.body_markdown ELSE 'Message 已删除' END, \
            'created_at', messages.created_at, 'edited_at', messages.edited_at) \
         FROM messages JOIN members ON members.id = messages.author_member_id \
         WHERE messages.channel_id = $1 AND messages.thread_id = $2 \
           AND messages.channel_seq > $3 ORDER BY messages.channel_seq LIMIT $4",
    )
    .bind(channel_id)
    .bind(thread_id)
    .bind(after.unwrap_or(0))
    .bind(limit + 1)
    .bind(address)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    let has_more_after = replies.len() as i64 > limit;
    let mut replies = replies.into_iter().take(limit as usize).collect::<Vec<_>>();
    let root_seq = root
        .get("seq")
        .and_then(serde_json::Value::as_i64)
        .ok_or(ApiError::Internal)?;
    let background: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object('id', messages.id, 'seq', messages.channel_seq, \
            'author', jsonb_build_object('id', members.id, 'kind', members.kind, \
                'display_name', members.display_name, 'handle', members.handle), \
            'address', '#' || channels.slug::text, 'body_markdown', messages.body_markdown, \
            'created_at', messages.created_at, 'edited_at', messages.edited_at) \
         FROM messages JOIN members ON members.id = messages.author_member_id \
         JOIN channels ON channels.id = messages.channel_id \
         WHERE messages.channel_id = $1 AND messages.thread_id IS NULL \
           AND messages.channel_seq < $2 ORDER BY messages.channel_seq DESC LIMIT $3",
    )
    .bind(channel_id)
    .bind(root_seq)
    .bind(include_channel)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    enrich_agent_messages(database, &mut replies).await?;
    let mut background = background.into_iter().rev().collect::<Vec<_>>();
    enrich_agent_messages(database, &mut background).await?;
    Ok(serde_json::json!({
        "address": address,
        "channel_id": channel_id,
        "thread_id": thread_id,
        "snapshot_channel_seq": snapshot,
        "root": root,
        "replies": replies,
        "channel_background": background,
        "has_more_before": false,
        "has_more_after": has_more_after,
    }))
}

async fn resolve_agent_address(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    address: &str,
) -> Result<(Uuid, String), ApiError> {
    if let Some(slug) = address.strip_prefix('#') {
        if slug.is_empty() || slug.contains(':') {
            return Err(ApiError::validation(
                "invalid_address",
                "This command requires a Channel main timeline address",
            ));
        }
        return sqlx::query_as(
            "SELECT channels.id, '#' || channels.slug::text FROM channels \
             JOIN channel_members ON channel_members.channel_id = channels.id \
             WHERE channels.slug = $1 AND channel_members.member_id = $2 \
               AND channels.archived_at IS NULL",
        )
        .bind(slug)
        .bind(agent_id)
        .fetch_optional(database)
        .await
        .map_err(ApiError::database)?
        .ok_or_else(|| {
            ApiError::forbidden("permission_denied", "Agent is not a member of this Channel")
        });
    }
    if let Some(handle) = address.strip_prefix('@') {
        return sqlx::query_as(
            "SELECT direct_channels.channel_id, '@' || target.handle FROM direct_channels \
             JOIN members target ON target.id = CASE \
                 WHEN direct_channels.member_low_id = $1 THEN direct_channels.member_high_id \
                 ELSE direct_channels.member_low_id END \
             WHERE $1 IN (direct_channels.member_low_id, direct_channels.member_high_id) \
               AND lower(target.handle) = lower($2)",
        )
        .bind(agent_id)
        .bind(handle)
        .fetch_optional(database)
        .await
        .map_err(ApiError::database)?
        .ok_or_else(|| ApiError::not_found("dm_not_found", "DM was not found"));
    }
    Err(ApiError::validation(
        "invalid_address",
        "Address must use #channel or @member",
    ))
}

#[allow(clippy::too_many_arguments)]
async fn agent_message_send(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    run_id: Uuid,
    address: &str,
    body_markdown: String,
    based_on: Option<i64>,
    handle_item_id: Option<Uuid>,
    attachment_ids: &[Uuid],
    idempotency_key: Uuid,
) -> Result<serde_json::Value, ApiError> {
    let body_markdown = body_markdown.trim();
    if !(1..=20_000).contains(&body_markdown.chars().count()) {
        return Err(ApiError::validation(
            "invalid_message_body",
            "Message must contain 1 to 20000 characters",
        ));
    }
    if based_on.is_some_and(|snapshot| snapshot < 0) {
        return Err(ApiError::validation(
            "invalid_context_snapshot",
            "Context snapshot sequence cannot be negative",
        ));
    }
    let (channel_id, display_address, thread_id) =
        resolve_agent_message_target(database, agent_id, address).await?;
    let request_hash = idempotency::request_hash(&serde_json::json!({
        "run_id": run_id,
        "address": address,
        "body_markdown": body_markdown,
        "based_on": based_on,
        "handle_inbox_item_id": handle_item_id,
        "attachment_ids": attachment_ids,
    }))?;
    let mut transaction = database.begin().await.map_err(ApiError::database)?;
    let channel: Option<(Uuid, String, Option<OffsetDateTime>, i64)> = sqlx::query_as(
        "SELECT channels.space_id, channels.kind, channels.archived_at, channels.next_seq - 1 \
         FROM channels JOIN channel_members ON channel_members.channel_id = channels.id \
         WHERE channels.id = $1 AND channel_members.member_id = $2 FOR UPDATE OF channels",
    )
    .bind(channel_id)
    .bind(agent_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let (space_id, channel_kind, archived_at, current_seq) = channel.ok_or_else(|| {
        ApiError::forbidden("permission_denied", "Channel membership is required")
    })?;
    if archived_at.is_some() {
        return Err(ApiError::conflict(
            "channel_archived",
            "Archived Channel is read-only",
        ));
    }
    let scope = format!("agent:{agent_id}:message:send");
    if let Some((_status, response)) = idempotency::begin::<serde_json::Value>(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(idempotency_key),
        &request_hash,
    )
    .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(response);
    }
    let handled_priority = if let Some(item_id) = handle_item_id {
        let priority: Option<String> = sqlx::query_scalar(
            "SELECT inbox_items.priority FROM agent_run_inbox_items \
             JOIN inbox_items ON inbox_items.id = agent_run_inbox_items.inbox_item_id \
             WHERE agent_run_inbox_items.run_id = $1 \
               AND agent_run_inbox_items.inbox_item_id = $2 \
               AND inbox_items.member_id = $3 AND inbox_items.status = 'leased' \
               AND inbox_items.lease_id = agent_run_inbox_items.lease_id \
               AND inbox_items.lease_expires_at > now()",
        )
        .bind(run_id)
        .bind(item_id)
        .bind(agent_id)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        let Some(priority) = priority else {
            return Err(ApiError::conflict(
                "inbox_lease_lost",
                "Inbox Item is not leased by this run",
            ));
        };
        Some(priority)
    } else {
        None
    };
    if handled_priority.as_deref() == Some("hard")
        && let Some(snapshot) = based_on
        && snapshot != current_seq
    {
        let details = context_change_details(
            &mut transaction,
            channel_id,
            &display_address,
            snapshot,
            current_seq,
        )
        .await?;
        return Err(ApiError::conflict_with_details(
            "context_changed",
            format!("Channel context changed; latest sequence is {current_seq}"),
            details,
        ));
    }
    let seq: i64 = sqlx::query_scalar(
        "UPDATE channels SET next_seq = next_seq + 1 WHERE id = $1 RETURNING next_seq - 1",
    )
    .bind(channel_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let message_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    sqlx::query(
        "INSERT INTO messages (id, channel_id, space_id, channel_seq, thread_id, author_member_id, \
         body_markdown, idempotency_key, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)",
    )
    .bind(message_id)
    .bind(channel_id)
    .bind(space_id)
    .bind(seq)
    .bind(thread_id)
    .bind(agent_id)
    .bind(body_markdown)
    .bind(idempotency_key)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    attachment::attach_to_message(
        &mut transaction,
        message_id,
        space_id,
        agent_id,
        attachment_ids,
    )
    .await?;
    let mut thread_inbox_changed = false;
    if let Some(thread_id) = thread_id {
        sqlx::query(
            "INSERT INTO thread_subscriptions (channel_id, thread_id, member_id, created_at) \
             VALUES ($1, $2, $3, $4) ON CONFLICT (channel_id, thread_id, member_id) \
             DO UPDATE SET muted_at = NULL",
        )
        .bind(channel_id)
        .bind(thread_id)
        .bind(agent_id)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        if channel_kind != "direct" {
            thread_inbox_changed = super::thread::insert_thread_attention(
                &mut transaction,
                space_id,
                channel_id,
                thread_id,
                message_id,
                agent_id,
                seq,
                None,
                &[],
                &channel_kind,
                now,
            )
            .await?;
        }
    }
    if let Some(item_id) = handle_item_id {
        let updated = sqlx::query(
            "UPDATE inbox_items SET status = 'handled', handled_by_run_id = $2, handled_at = $3, \
             lease_id = NULL, lease_expires_at = NULL WHERE id = $1 AND member_id = $4 \
             AND status = 'leased'",
        )
        .bind(item_id)
        .bind(run_id)
        .bind(now)
        .bind(agent_id)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        if updated.rows_affected() != 1 {
            return Err(ApiError::conflict(
                "inbox_lease_lost",
                "Inbox Item lease changed before Message send",
            ));
        }
        insert_agent_inbox_event(&mut transaction, space_id, agent_id, item_id, now).await?;
    }
    if channel_kind == "direct" {
        let recipient_id: Uuid = sqlx::query_scalar(
            "SELECT member_id FROM channel_members WHERE channel_id = $1 AND member_id <> $2",
        )
        .bind(channel_id)
        .bind(agent_id)
        .fetch_one(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        let recipient_item_id = Uuid::now_v7();
        sqlx::query(
            "INSERT INTO inbox_items (id, member_id, space_id, kind, priority, channel_id, \
             message_id, first_seq, last_seq, available_at, created_at) \
             VALUES ($1, $2, $3, 'direct', 'hard', $4, $5, $6, $6, $7, $7)",
        )
        .bind(recipient_item_id)
        .bind(recipient_id)
        .bind(space_id)
        .bind(channel_id)
        .bind(message_id)
        .bind(seq)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        insert_agent_inbox_event(
            &mut transaction,
            space_id,
            recipient_id,
            recipient_item_id,
            now,
        )
        .await?;
    }
    if thread_inbox_changed {
        sqlx::query(
            "INSERT INTO outbox_events (id, topic, aggregate_id, payload_json, created_at) \
             VALUES ($1, 'inbox.changed', $2, $3, $4)",
        )
        .bind(Uuid::now_v7())
        .bind(message_id)
        .bind(serde_json::json!({
            "space_id": space_id,
            "channel_id": channel_id,
            "thread_id": thread_id,
        }))
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    }
    if channel_kind != "direct"
        && thread_id.is_none()
        && message::insert_channel_ambient_inbox(
            &mut transaction,
            space_id,
            channel_id,
            message_id,
            agent_id,
            seq,
            &[],
            now,
        )
        .await?
    {
        sqlx::query(
            "INSERT INTO outbox_events (id, topic, aggregate_id, payload_json, created_at) \
             VALUES ($1, 'inbox.changed', $2, $3, $4)",
        )
        .bind(Uuid::now_v7())
        .bind(message_id)
        .bind(serde_json::json!({ "space_id": space_id, "channel_id": channel_id }))
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    }
    sqlx::query(
        "INSERT INTO outbox_events (id, topic, aggregate_id, payload_json, created_at) \
         VALUES ($1, 'message.created', $2, $3, $4)",
    )
    .bind(Uuid::now_v7())
    .bind(message_id)
    .bind(serde_json::json!({
        "space_id": space_id,
        "channel_id": channel_id,
        "thread_id": thread_id,
        "message_id": message_id,
        "channel_seq": seq,
    }))
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let response = agent_message_json(&mut transaction, message_id, &display_address).await?;
    idempotency::finish(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(idempotency_key),
        StatusCode::OK,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(response)
}

async fn context_change_details(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    channel_id: Uuid,
    display_address: &str,
    snapshot: i64,
    current_seq: i64,
) -> Result<serde_json::Value, ApiError> {
    let mut changes: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object( \
            'id', messages.id, 'seq', messages.channel_seq, \
            'address', CASE WHEN channels.kind = 'direct' THEN $4::text \
                WHEN messages.thread_id IS NULL THEN '#' || channels.slug::text \
                ELSE '#' || channels.slug::text || ':' || messages.thread_id::text END, \
            'thread_id', messages.thread_id, \
            'author', jsonb_build_object('id', members.id, 'kind', members.kind, \
                'display_name', members.display_name, 'handle', members.handle)) \
         FROM messages JOIN channels ON channels.id = messages.channel_id \
         JOIN members ON members.id = messages.author_member_id \
         WHERE messages.channel_id = $1 AND messages.channel_seq > $2 \
           AND messages.channel_seq <= $3 ORDER BY messages.channel_seq LIMIT 11",
    )
    .bind(channel_id)
    .bind(snapshot)
    .bind(current_seq)
    .bind(display_address)
    .fetch_all(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    let has_more = changes.len() > 10;
    changes.truncate(10);
    Ok(serde_json::json!({
        "snapshot_channel_seq": snapshot,
        "latest_channel_seq": current_seq,
        "changes": changes,
        "has_more": has_more,
    }))
}

async fn resolve_agent_message_target(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    address: &str,
) -> Result<(Uuid, String, Option<i64>), ApiError> {
    if let Some(raw) = address.strip_prefix('#')
        && let Some((slug, thread_id)) = raw
            .rsplit_once(':')
            .and_then(|(slug, id)| id.parse::<i64>().ok().map(|id| (slug, id)))
    {
        if slug.is_empty() || thread_id <= 0 {
            return Err(ApiError::validation(
                "invalid_address",
                "Thread address must use #channel:id",
            ));
        }
        let channel_id: Uuid = sqlx::query_scalar(
            "SELECT channels.id FROM channels \
             JOIN channel_members ON channel_members.channel_id = channels.id \
             JOIN threads ON threads.channel_id = channels.id AND threads.thread_id = $2 \
             WHERE channels.slug = $1 AND channel_members.member_id = $3 \
               AND channels.archived_at IS NULL",
        )
        .bind(slug)
        .bind(thread_id)
        .bind(agent_id)
        .fetch_optional(database)
        .await
        .map_err(ApiError::database)?
        .ok_or_else(|| {
            ApiError::forbidden(
                "permission_denied",
                "Agent is not a member of this Thread Channel",
            )
        })?;
        return Ok((channel_id, address.to_owned(), Some(thread_id)));
    }
    let (channel_id, display_address) = resolve_agent_address(database, agent_id, address).await?;
    Ok((channel_id, display_address, None))
}

async fn agent_inbox_ack(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    run_id: Uuid,
    item_ids: &[Uuid],
    reason: &str,
    idempotency_key: Uuid,
) -> Result<serde_json::Value, ApiError> {
    if item_ids.is_empty() || reason.trim().is_empty() || reason.chars().count() > 500 {
        return Err(ApiError::validation(
            "invalid_inbox_ack",
            "Inbox ack requires Items and a reason of at most 500 characters",
        ));
    }
    let mut transaction = database.begin().await.map_err(ApiError::database)?;
    let scope = format!("agent:{agent_id}:inbox:ack");
    let request_hash = idempotency::request_hash(&serde_json::json!({
        "run_id": run_id,
        "inbox_item_ids": item_ids,
        "reason": reason,
    }))?;
    if let Some((_status, response)) = idempotency::begin::<serde_json::Value>(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(idempotency_key),
        &request_hash,
    )
    .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(response);
    }
    let rows: Vec<(Uuid, Uuid)> = sqlx::query_as(
        "UPDATE inbox_items SET status = 'handled', handled_by_run_id = $2, handled_at = now(), \
         lease_id = NULL, lease_expires_at = NULL \
         WHERE id = ANY($1) AND member_id = $3 AND status = 'leased' \
           AND EXISTS(SELECT 1 FROM agent_run_inbox_items links WHERE links.run_id = $2 \
             AND links.inbox_item_id = inbox_items.id AND links.lease_id = inbox_items.lease_id) \
         RETURNING id, space_id",
    )
    .bind(item_ids)
    .bind(run_id)
    .bind(agent_id)
    .fetch_all(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if rows.len() != item_ids.len() {
        return Err(ApiError::conflict(
            "inbox_lease_lost",
            "One or more Inbox Items are not leased by this run",
        ));
    }
    let now = OffsetDateTime::now_utc();
    for (item_id, space_id) in &rows {
        insert_agent_inbox_event(&mut transaction, *space_id, agent_id, *item_id, now).await?;
    }
    let response = serde_json::json!({
        "handled_inbox_item_ids": rows.into_iter().map(|row| row.0).collect::<Vec<_>>(),
        "reason_recorded": true,
    });
    idempotency::finish(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(idempotency_key),
        StatusCode::OK,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(response)
}

async fn agent_inbox_defer(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    run_id: Uuid,
    item_ids: &[Uuid],
    until: OffsetDateTime,
    idempotency_key: Uuid,
) -> Result<serde_json::Value, ApiError> {
    if item_ids.is_empty() || until <= OffsetDateTime::now_utc() {
        return Err(ApiError::validation(
            "invalid_inbox_defer",
            "Inbox defer requires Items and a future time",
        ));
    }
    let mut transaction = database.begin().await.map_err(ApiError::database)?;
    let scope = format!("agent:{agent_id}:inbox:defer");
    let request_hash = idempotency::request_hash(&serde_json::json!({
        "run_id": run_id,
        "inbox_item_ids": item_ids,
        "until": until,
    }))?;
    if let Some((_status, response)) = idempotency::begin::<serde_json::Value>(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(idempotency_key),
        &request_hash,
    )
    .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(response);
    }
    let rows: Vec<Uuid> = sqlx::query_scalar(
        "UPDATE inbox_items SET status = 'deferred', available_at = $4, \
         lease_id = NULL, lease_expires_at = NULL \
         WHERE id = ANY($1) AND member_id = $2 AND status = 'leased' \
           AND EXISTS(SELECT 1 FROM agent_run_inbox_items links WHERE links.run_id = $3 \
             AND links.inbox_item_id = inbox_items.id AND links.lease_id = inbox_items.lease_id) \
         RETURNING id",
    )
    .bind(item_ids)
    .bind(agent_id)
    .bind(run_id)
    .bind(until)
    .fetch_all(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if rows.len() != item_ids.len() {
        return Err(ApiError::conflict(
            "inbox_lease_lost",
            "One or more Inbox Items are not leased by this run",
        ));
    }
    let response = serde_json::json!({ "deferred_inbox_item_ids": rows, "available_at": until });
    idempotency::finish(
        &mut transaction,
        &scope,
        idempotency::IdempotencyKey(idempotency_key),
        StatusCode::OK,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(response)
}

async fn insert_agent_inbox_event(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    space_id: Uuid,
    member_id: Uuid,
    item_id: Uuid,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    sqlx::query(
        "INSERT INTO outbox_events (id, topic, aggregate_id, payload_json, created_at) \
         VALUES ($1, 'inbox.changed', $2, $3, $4)",
    )
    .bind(Uuid::now_v7())
    .bind(item_id)
    .bind(serde_json::json!({
        "space_id": space_id,
        "member_id": member_id,
        "item_id": item_id,
    }))
    .bind(now)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(())
}

async fn agent_message_json(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    message_id: Uuid,
    address: &str,
) -> Result<serde_json::Value, ApiError> {
    let mut message: serde_json::Value = sqlx::query_scalar(
        "SELECT jsonb_build_object( \
            'id', messages.id, 'channel_id', messages.channel_id, 'seq', messages.channel_seq, \
            'address', $2::text, 'author', jsonb_build_object('id', members.id, \
                'kind', members.kind, 'display_name', members.display_name, 'handle', members.handle), \
            'body_markdown', messages.body_markdown, 'created_at', messages.created_at, \
            'edited_at', messages.edited_at) \
         FROM messages JOIN members ON members.id = messages.author_member_id \
         WHERE messages.id = $1",
    )
    .bind(message_id)
    .bind(address)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    let attachments = attachment::attachments_for_message(transaction, message_id).await?;
    message.as_object_mut().ok_or(ApiError::Internal)?.insert(
        "attachments".to_owned(),
        serde_json::to_value(attachments).map_err(|_| ApiError::Internal)?,
    );
    Ok(message)
}

async fn agent_message_json_pool(
    database: &sqlx::PgPool,
    message_id: Uuid,
    address: &str,
) -> Result<serde_json::Value, ApiError> {
    let message: serde_json::Value = sqlx::query_scalar(
        "SELECT jsonb_build_object( \
            'id', messages.id, 'channel_id', messages.channel_id, 'seq', messages.channel_seq, \
            'address', $2::text, 'author', jsonb_build_object('id', members.id, \
                'kind', members.kind, 'display_name', members.display_name, 'handle', members.handle), \
            'body_markdown', CASE WHEN messages.deleted_at IS NULL THEN messages.body_markdown \
                ELSE 'Message 已删除' END, 'created_at', messages.created_at, \
            'edited_at', messages.edited_at) \
         FROM messages JOIN members ON members.id = messages.author_member_id \
         WHERE messages.id = $1",
    )
    .bind(message_id)
    .bind(address)
    .fetch_one(database)
    .await
    .map_err(ApiError::database)?;
    let mut messages = vec![message];
    enrich_agent_messages(database, &mut messages).await?;
    messages.pop().ok_or(ApiError::Internal)
}

async fn enrich_agent_messages(
    database: &sqlx::PgPool,
    messages: &mut [serde_json::Value],
) -> Result<(), ApiError> {
    for message in messages {
        let message_id = message
            .get("id")
            .and_then(serde_json::Value::as_str)
            .and_then(|value| Uuid::parse_str(value).ok())
            .ok_or(ApiError::Internal)?;
        let attachments = attachment::attachments_for_message_pool(database, message_id).await?;
        message.as_object_mut().ok_or(ApiError::Internal)?.insert(
            "attachments".to_owned(),
            serde_json::to_value(attachments).map_err(|_| ApiError::Internal)?,
        );
    }
    Ok(())
}

async fn computer_socket(
    state: std::sync::Arc<AppState>,
    computer_id: Uuid,
    mut socket: ws::WebSocket,
) {
    let result = run_computer_socket(&state, computer_id, &mut socket).await;
    if let Err(error) = result {
        tracing::warn!(computer_id = %computer_id, error = ?error, "Computer connection closed");
    }
}

async fn run_computer_socket(
    state: &AppState,
    computer_id: Uuid,
    socket: &mut ws::WebSocket,
) -> Result<(), ApiError> {
    let hello = receive_frame(socket).await?;
    let last_acked = match hello {
        ComputerFrame::Hello {
            last_acked_computer_seq,
        } if last_acked_computer_seq >= 0 => last_acked_computer_seq,
        _ => {
            return Err(ApiError::validation(
                "invalid_computer_hello",
                "Expected Computer hello",
            ));
        }
    };
    sqlx::query(
        "UPDATE computer_commands SET status = 'acked', acked_at = COALESCE(acked_at, now()) \
         WHERE computer_id = $1 AND computer_seq <= $2 AND status = 'pending'",
    )
    .bind(computer_id)
    .bind(last_acked)
    .execute(&state.database)
    .await
    .map_err(ApiError::database)?;
    sqlx::query(
        "UPDATE agent_runs SET status = 'running', started_at = COALESCE(started_at, now()) \
         WHERE computer_id = $1 AND status = 'queued' AND id IN ( \
           SELECT (payload_json->>'run_id')::uuid FROM computer_commands \
           WHERE computer_id = $1 AND computer_seq <= $2 AND kind = 'agent.run' \
             AND status IN ('acked', 'completed'))",
    )
    .bind(computer_id)
    .bind(last_acked)
    .execute(&state.database)
    .await
    .map_err(ApiError::database)?;
    update_online(state, computer_id, None, None).await?;
    send_frame(
        socket,
        &ServerFrame::Welcome {
            heartbeat_interval_seconds: 10,
        },
    )
    .await?;
    replay_commands(state, computer_id, socket).await?;
    let mut command_poll = tokio::time::interval(std::time::Duration::from_secs(1));
    command_poll.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    command_poll.tick().await;

    loop {
        let message = tokio::select! {
            _ = command_poll.tick() => {
                replay_commands(state, computer_id, socket).await?;
                continue;
            }
            message = socket.recv() => message,
        };
        let Some(message) = message else {
            break;
        };
        match message.map_err(|_| ApiError::Internal)? {
            ws::Message::Text(text) => {
                let frame: ComputerFrame = serde_json::from_str(&text).map_err(|_| {
                    ApiError::validation("invalid_computer_frame", "Computer frame is invalid")
                })?;
                match frame {
                    ComputerFrame::Heartbeat {
                        daemon_version,
                        os,
                        cpu_count,
                        memory_total_bytes,
                        agents_count,
                        active_runs,
                    } => {
                        if cpu_count == 0 || !matches!(os.as_str(), "macos" | "linux") {
                            return Err(ApiError::validation(
                                "invalid_heartbeat",
                                "Computer heartbeat is invalid",
                            ));
                        }
                        let _metrics = (memory_total_bytes, agents_count, active_runs);
                        update_online(state, computer_id, Some(&daemon_version), Some(&os)).await?;
                    }
                    ComputerFrame::CommandAck {
                        command_id,
                        computer_seq,
                    } => {
                        apply_command_ack(state, computer_id, command_id, computer_seq).await?;
                    }
                    ComputerFrame::CommandResult {
                        command_id,
                        computer_seq,
                        ok,
                        result,
                    } => {
                        apply_command_result(
                            state,
                            computer_id,
                            command_id,
                            computer_seq,
                            ok,
                            &result,
                        )
                        .await?;
                    }
                    ComputerFrame::Hello { .. } => {
                        return Err(ApiError::validation(
                            "duplicate_computer_hello",
                            "Computer hello was already received",
                        ));
                    }
                }
            }
            ws::Message::Ping(bytes) => socket
                .send(ws::Message::Pong(bytes))
                .await
                .map_err(|_| ApiError::Internal)?,
            ws::Message::Close(_) => break,
            ws::Message::Binary(_) | ws::Message::Pong(_) => {}
        }
    }
    Ok(())
}

async fn apply_command_ack(
    state: &AppState,
    computer_id: Uuid,
    command_id: Uuid,
    computer_seq: i64,
) -> Result<(), ApiError> {
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let command: Option<(String, serde_json::Value)> = sqlx::query_as(
        "UPDATE computer_commands SET status = 'acked', acked_at = COALESCE(acked_at, now()) \
         WHERE id = $1 AND computer_id = $2 AND computer_seq = $3 \
           AND status IN ('pending', 'acked') RETURNING kind, payload_json",
    )
    .bind(command_id)
    .bind(computer_id)
    .bind(computer_seq)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if let Some((kind, payload)) = command
        && kind == "agent.run"
        && let Some(run_id) = payload
            .get("run_id")
            .and_then(serde_json::Value::as_str)
            .and_then(|value| Uuid::parse_str(value).ok())
    {
        sqlx::query(
            "UPDATE agent_runs SET status = 'running', started_at = COALESCE(started_at, now()) \
             WHERE id = $1 AND computer_id = $2 AND status = 'queued'",
        )
        .bind(run_id)
        .bind(computer_id)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    }
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(())
}

async fn apply_command_result(
    state: &AppState,
    computer_id: Uuid,
    command_id: Uuid,
    computer_seq: i64,
    ok: bool,
    result: &serde_json::Value,
) -> Result<(), ApiError> {
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let command: Option<(String, serde_json::Value, String)> = sqlx::query_as(
        "SELECT kind, payload_json, status FROM computer_commands \
         WHERE id = $1 AND computer_id = $2 AND computer_seq = $3 FOR UPDATE",
    )
    .bind(command_id)
    .bind(computer_id)
    .bind(computer_seq)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let Some((kind, payload, command_status)) = command else {
        return Ok(());
    };
    if matches!(command_status.as_str(), "completed" | "failed") {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(());
    }
    let status = if ok { "completed" } else { "failed" };
    let stored_result = if kind == "agent.memory.read" {
        serde_json::json!({
            "ok": ok,
            "error_code": result.get("error_code"),
        })
    } else {
        result.clone()
    };
    sqlx::query(
        "UPDATE computer_commands SET status = $4, result_json = $5, completed_at = now(), \
         acked_at = COALESCE(acked_at, now()) \
         WHERE id = $1 AND computer_id = $2 AND computer_seq = $3",
    )
    .bind(command_id)
    .bind(computer_id)
    .bind(computer_seq)
    .bind(status)
    .bind(&stored_result)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if kind == "agent.run" {
        apply_run_result(&mut transaction, computer_id, &payload, ok, result).await?;
    }
    if !kind.starts_with("agent.") {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(());
    }
    let Some(agent_id) = payload
        .get("agent_id")
        .and_then(serde_json::Value::as_str)
        .and_then(|value| Uuid::parse_str(value).ok())
    else {
        return Err(ApiError::Internal);
    };
    let successful_status = match kind.as_str() {
        "agent.provision" => Some("active"),
        "agent.suspend" => Some("suspended"),
        "agent.resume" => Some("active"),
        "agent.retire" => Some("retired"),
        "agent.configure" | "agent.run" | "agent.cancel" => None,
        _ => None,
    };
    let new_status = if kind == "agent.retire" {
        Some("retired")
    } else if ok {
        successful_status
    } else if matches!(
        kind.as_str(),
        "agent.provision" | "agent.configure" | "agent.suspend" | "agent.resume" | "agent.retire"
    ) {
        Some("error")
    } else {
        None
    };
    let space_id: Option<Uuid> = if let Some(new_status) = new_status {
        sqlx::query_scalar(
            "UPDATE agents SET status = $2, updated_at = now(), last_error_code = $4 \
             WHERE member_id = $1 AND computer_id = $3 RETURNING space_id",
        )
        .bind(agent_id)
        .bind(new_status)
        .bind(computer_id)
        .bind(if ok || kind == "agent.retire" {
            None
        } else {
            result.get("error_code").and_then(serde_json::Value::as_str)
        })
        .fetch_optional(&mut *transaction)
        .await
        .map_err(ApiError::database)?
    } else {
        sqlx::query_scalar("SELECT space_id FROM agents WHERE member_id = $1 AND computer_id = $2")
            .bind(agent_id)
            .bind(computer_id)
            .fetch_optional(&mut *transaction)
            .await
            .map_err(ApiError::database)?
    };
    if let Some(space_id) = space_id {
        if let Some(new_status) = new_status {
            sqlx::query(
                "INSERT INTO outbox_events (id, topic, aggregate_id, payload_json, created_at) \
                 VALUES ($1, 'agent.status_changed', $2, $3, now())",
            )
            .bind(Uuid::now_v7())
            .bind(agent_id)
            .bind(serde_json::json!({
                "space_id": space_id, "agent_member_id": agent_id,
                "status": new_status,
                "error_code": if ok { None } else { result.get("error_code") },
            }))
            .execute(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
        }
        if result.get("memory_files").is_some() {
            replace_memory_files(&mut transaction, agent_id, result).await?;
        }
    }
    transaction.commit().await.map_err(ApiError::database)?;
    if kind == "agent.memory.read"
        && let Some(waiter) = state.memory_read_waiters.lock().await.remove(&command_id)
    {
        let _ = waiter.send((ok, result.clone()));
    }
    Ok(())
}

async fn apply_run_result(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    computer_id: Uuid,
    payload: &serde_json::Value,
    ok: bool,
    result: &serde_json::Value,
) -> Result<(), ApiError> {
    let run_id = payload
        .get("run_id")
        .and_then(serde_json::Value::as_str)
        .and_then(|value| Uuid::parse_str(value).ok())
        .ok_or(ApiError::Internal)?;
    let local_status = result
        .get("status")
        .and_then(serde_json::Value::as_str)
        .unwrap_or(if ok { "completed" } else { "failed" });
    let run_status = match local_status {
        "completed" => "completed",
        "canceled" => "canceled",
        _ => "failed",
    };
    let updated: Option<(Uuid, Uuid)> = sqlx::query_as(
        "UPDATE agent_runs SET status = $2, started_at = COALESCE(started_at, created_at), \
         finished_at = now(), error_code = $3 WHERE id = $1 AND computer_id = $4 \
         AND status IN ('queued', 'running') RETURNING agent_member_id, \
         (SELECT space_id FROM agents WHERE agents.member_id = agent_runs.agent_member_id)",
    )
    .bind(run_id)
    .bind(run_status)
    .bind(result.get("error_code").and_then(serde_json::Value::as_str))
    .bind(computer_id)
    .fetch_optional(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    let Some((agent_id, space_id)) = updated else {
        return Ok(());
    };
    let error_code = if run_status == "completed" {
        "run_exited_without_handling"
    } else {
        result
            .get("error_code")
            .and_then(serde_json::Value::as_str)
            .unwrap_or("driver_failed")
    };
    release_run_inbox_items(transaction, run_id, agent_id, space_id, error_code).await?;
    insert_run_event(transaction, run_id, agent_id, space_id, run_status).await?;
    Ok(())
}

async fn release_run_inbox_items(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    run_id: Uuid,
    agent_id: Uuid,
    space_id: Uuid,
    error_code: &str,
) -> Result<(u64, u64), ApiError> {
    let released: Vec<(Uuid, String)> = sqlx::query_as(
        "UPDATE inbox_items SET status = CASE \
             WHEN retry_count + 1 >= COALESCE((SELECT (attention_config_json->>'max_retry_count')::int \
                FROM agents WHERE member_id = inbox_items.member_id), 3) THEN 'dead' \
             ELSE 'pending' END, retry_count = retry_count + 1, \
             available_at = now() + make_interval(secs => LEAST(60, 2 ^ LEAST(retry_count, 5))), \
             lease_id = NULL, lease_expires_at = NULL, last_error = $2 \
         WHERE id IN (SELECT inbox_item_id FROM agent_run_inbox_items WHERE run_id = $1) \
           AND status = 'leased' RETURNING id, status",
    )
    .bind(run_id)
    .bind(error_code)
    .fetch_all(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    let dead_items = released
        .iter()
        .filter(|(_, status)| status == "dead")
        .count() as u64;
    if dead_items > 0 {
        let admins: Vec<Uuid> = sqlx::query_scalar(
            "SELECT members.id FROM members JOIN human_members ON human_members.member_id = members.id \
             WHERE members.space_id = $1 AND members.access_level IN ('owner', 'admin') \
               AND members.retired_at IS NULL",
        )
        .bind(space_id)
        .fetch_all(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
        let now = OffsetDateTime::now_utc();
        for admin_id in admins {
            let item_id = Uuid::now_v7();
            sqlx::query(
                "INSERT INTO inbox_items (id, member_id, space_id, kind, priority, message_count, \
                 status, available_at, last_error, created_at) \
                 VALUES ($1, $2, $3, 'system', 'hard', $4, 'pending', $5, $6, $5)",
            )
            .bind(item_id)
            .bind(admin_id)
            .bind(space_id)
            .bind(i32::try_from(dead_items).unwrap_or(i32::MAX))
            .bind(now)
            .bind(error_code)
            .execute(&mut **transaction)
            .await
            .map_err(ApiError::database)?;
            insert_agent_inbox_event(transaction, space_id, admin_id, item_id, now).await?;
        }
        tracing::warn!(
            agent_member_id = %agent_id,
            run_id = %run_id,
            dead_items,
            error_code,
            "Agent Inbox Items reached retry limit"
        );
    }
    let retry_items = released.len() as u64 - dead_items;
    Ok((retry_items, dead_items))
}

async fn insert_run_event(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    run_id: Uuid,
    agent_id: Uuid,
    space_id: Uuid,
    status: &str,
) -> Result<(), ApiError> {
    sqlx::query(
        "INSERT INTO outbox_events (id, topic, aggregate_id, payload_json, created_at) \
         VALUES ($1, 'agent.run_changed', $2, $3, now())",
    )
    .bind(Uuid::now_v7())
    .bind(run_id)
    .bind(serde_json::json!({
        "space_id": space_id,
        "agent_member_id": agent_id,
        "run_id": run_id,
        "status": status,
    }))
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(())
}

async fn replace_memory_files(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    agent_id: Uuid,
    result: &serde_json::Value,
) -> Result<(), ApiError> {
    let Some(files) = result
        .get("memory_files")
        .and_then(serde_json::Value::as_array)
    else {
        return Ok(());
    };
    sqlx::query("DELETE FROM agent_memory_files WHERE agent_member_id = $1")
        .bind(agent_id)
        .execute(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
    for file in files {
        let path = file
            .get("path")
            .and_then(serde_json::Value::as_str)
            .ok_or(ApiError::Internal)?;
        let size = file
            .get("size")
            .and_then(serde_json::Value::as_u64)
            .and_then(|value| i64::try_from(value).ok())
            .ok_or(ApiError::Internal)?;
        let sha256 = file
            .get("sha256")
            .and_then(serde_json::Value::as_str)
            .and_then(|value| hex::decode(value).ok())
            .filter(|value| value.len() == 32)
            .ok_or(ApiError::Internal)?;
        let updated_at = file
            .get("updated_at")
            .and_then(serde_json::Value::as_str)
            .and_then(|value| {
                OffsetDateTime::parse(value, &time::format_description::well_known::Rfc3339).ok()
            })
            .ok_or(ApiError::Internal)?;
        sqlx::query(
            "INSERT INTO agent_memory_files (agent_member_id, path, size, sha256, updated_at) \
             VALUES ($1, $2, $3, $4, $5)",
        )
        .bind(agent_id)
        .bind(path)
        .bind(size)
        .bind(sha256)
        .bind(updated_at)
        .execute(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
    }
    Ok(())
}

async fn receive_frame(socket: &mut ws::WebSocket) -> Result<ComputerFrame, ApiError> {
    match socket.recv().await {
        Some(Ok(ws::Message::Text(text))) => serde_json::from_str(&text).map_err(|_| {
            ApiError::validation("invalid_computer_frame", "Computer frame is invalid")
        }),
        _ => Err(ApiError::validation(
            "missing_computer_hello",
            "Computer closed before hello",
        )),
    }
}

async fn send_frame(socket: &mut ws::WebSocket, frame: &ServerFrame) -> Result<(), ApiError> {
    socket
        .send(ws::Message::Text(
            serde_json::to_string(frame)
                .map_err(|_| ApiError::Internal)?
                .into(),
        ))
        .await
        .map_err(|_| ApiError::Internal)
}

async fn replay_commands(
    state: &AppState,
    computer_id: Uuid,
    socket: &mut ws::WebSocket,
) -> Result<(), ApiError> {
    let commands = sqlx::query_as::<_, (Uuid, i64, String, serde_json::Value)>(
        "SELECT id, computer_seq, kind, payload_json FROM computer_commands \
         WHERE computer_id = $1 AND status IN ('pending', 'acked') ORDER BY computer_seq",
    )
    .bind(computer_id)
    .fetch_all(&state.database)
    .await
    .map_err(ApiError::database)?;
    for (command_id, computer_seq, kind, payload) in commands {
        send_frame(
            socket,
            &ServerFrame::Command {
                command_id,
                computer_seq,
                kind,
                payload,
            },
        )
        .await?;
    }
    Ok(())
}

async fn update_online(
    state: &AppState,
    computer_id: Uuid,
    daemon_version: Option<&str>,
    os: Option<&str>,
) -> Result<(), ApiError> {
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let previous_status: Option<String> =
        sqlx::query_scalar("SELECT status FROM computers WHERE id = $1 FOR UPDATE")
            .bind(computer_id)
            .fetch_optional(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
    let updated: Option<(Uuid, String)> = sqlx::query_as(
        "UPDATE computers SET status = 'online', last_seen_at = now(), \
         daemon_version = COALESCE($2, daemon_version) \
         WHERE id = $1 AND status != 'revoked' AND ($3::text IS NULL OR os = $3) \
         RETURNING space_id, status",
    )
    .bind(computer_id)
    .bind(daemon_version)
    .bind(os)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let Some((space_id, status)) = updated else {
        return Err(ApiError::Unauthorized);
    };
    if previous_status.as_deref() != Some("online") {
        insert_status_event(&mut transaction, space_id, computer_id, &status).await?;
    }
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(())
}

async fn mark_stale_offline(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
) -> Result<(), ApiError> {
    let stale: Vec<(Uuid, Uuid)> = sqlx::query_as(
        "UPDATE computers SET status = 'offline' WHERE status = 'online' \
         AND last_seen_at < now() - interval '30 seconds' RETURNING id, space_id",
    )
    .fetch_all(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    for (computer_id, space_id) in stale {
        insert_status_event(transaction, space_id, computer_id, "offline").await?;
    }
    Ok(())
}

pub async fn monitor_offline(database: sqlx::PgPool) {
    let mut interval = tokio::time::interval(std::time::Duration::from_secs(5));
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    loop {
        interval.tick().await;
        let result = async {
            let mut transaction = database.begin().await.map_err(ApiError::database)?;
            mark_stale_offline(&mut transaction).await?;
            transaction.commit().await.map_err(ApiError::database)
        }
        .await;
        if let Err(error) = result {
            tracing::error!(error = ?error, "Computer offline monitor failed");
        }
    }
}

async fn insert_status_event(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    space_id: Uuid,
    computer_id: Uuid,
    status: &str,
) -> Result<(), ApiError> {
    sqlx::query(
        "INSERT INTO outbox_events (id, topic, aggregate_id, payload_json, created_at) \
         VALUES ($1, 'computer.status_changed', $2, $3, now())",
    )
    .bind(Uuid::now_v7())
    .bind(computer_id)
    .bind(serde_json::json!({
        "space_id": space_id,
        "computer_id": computer_id,
        "status": status,
    }))
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(())
}

fn decode_32(value: &str, code: &'static str) -> Result<[u8; 32], ApiError> {
    URL_SAFE_NO_PAD
        .decode(value)
        .ok()
        .and_then(|bytes| bytes.try_into().ok())
        .ok_or_else(|| ApiError::validation(code, "Expected base64url-encoded 32 bytes"))
}

fn bearer(headers: &HeaderMap) -> Result<&str, ApiError> {
    headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.strip_prefix("Bearer "))
        .filter(|value| !value.is_empty())
        .ok_or(ApiError::Unauthorized)
}
