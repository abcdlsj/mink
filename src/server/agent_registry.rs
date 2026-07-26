use axum::{
    Json,
    extract::{Path, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
};
use axum_extra::extract::CookieJar;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use sqlx::{FromRow, Postgres, Transaction};
use time::OffsetDateTime;
use uuid::Uuid;

use super::{AppState, api_error::ApiError, approval, auth, idempotency, member, space};

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct CreateAgentRequest {
    pub computer_id: Uuid,
    pub name: String,
    pub handle: Option<String>,
    pub role_text: String,
    pub access_level: String,
    pub driver_kind: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct AgentResponse {
    pub member_id: Uuid,
    pub space_id: Uuid,
    pub computer_id: Uuid,
    pub name: String,
    pub handle: String,
    pub access_level: String,
    pub role_text: String,
    pub role_revision: i64,
    pub status: String,
    pub driver_kind: String,
    pub attention_config: AttentionConfig,
    pub created_at: OffsetDateTime,
    pub updated_at: OffsetDateTime,
    pub retired_at: Option<OffsetDateTime>,
    pub last_error_code: Option<String>,
    pub memory_files: Vec<MemoryFileResponse>,
}

#[derive(Serialize)]
#[serde(untagged)]
pub enum CreateAgentResponse {
    Agent(Box<AgentResponse>),
    Approval(approval::PendingApprovalResponse),
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq)]
pub struct AttentionConfig {
    pub dm_immediate: bool,
    pub mention_immediate: bool,
    pub ambient_enabled: bool,
    pub ambient_debounce_seconds: u16,
    pub ambient_max_wait_seconds: u16,
    pub max_retry_count: u8,
}

impl Default for AttentionConfig {
    fn default() -> Self {
        Self {
            dm_immediate: true,
            mention_immediate: true,
            ambient_enabled: true,
            ambient_debounce_seconds: 5,
            ambient_max_wait_seconds: 30,
            max_retry_count: 3,
        }
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct MemoryFileResponse {
    pub path: String,
    pub size: i64,
    pub sha256: String,
    pub updated_at: OffsetDateTime,
}

#[derive(Debug, Deserialize)]
pub struct ReadMemoryRequest {
    pub path: String,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct MemoryContentResponse {
    pub path: String,
    pub content: String,
    pub size: i64,
    pub sha256: String,
    #[serde(with = "time::serde::rfc3339")]
    pub updated_at: OffsetDateTime,
}

#[derive(FromRow)]
struct AgentRow {
    member_id: Uuid,
    space_id: Uuid,
    computer_id: Uuid,
    name: String,
    handle: String,
    access_level: String,
    role_text: String,
    role_revision: i64,
    status: String,
    driver_kind: String,
    attention_config_json: serde_json::Value,
    driver_config_json: serde_json::Value,
    created_at: OffsetDateTime,
    updated_at: OffsetDateTime,
    retired_at: Option<OffsetDateTime>,
    last_error_code: Option<String>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct UpdateAgentRequest {
    pub role_text: Option<String>,
    pub attention_config: Option<AttentionConfig>,
    pub lifecycle: Option<LifecycleAction>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(tag = "action", rename_all = "snake_case")]
pub enum LifecycleAction {
    Suspend { mode: SuspendMode },
    Resume,
    Retry,
    Retire,
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum SuspendMode {
    StopAfterCurrent,
    CancelNow,
}

pub async fn list(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<AgentResponse>>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    member::require_actor(&state.database, user.id, space_id).await?;
    let rows = sqlx::query_as::<_, AgentRow>(&format!(
        "{} WHERE agents.space_id = $1 ORDER BY lower(members.display_name), members.id",
        AGENT_SELECT
    ))
    .bind(space_id)
    .fetch_all(&state.database)
    .await
    .map_err(ApiError::database)?;
    let mut agents = Vec::with_capacity(rows.len());
    for row in rows {
        agents.push(agent_response(&state.database, row, false).await?);
    }
    Ok(Json(agents))
}

pub async fn get(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(agent_id): Path<Uuid>,
) -> Result<Json<AgentResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let row = find_agent(&state.database, agent_id).await?;
    let actor = member::require_actor(&state.database, user.id, row.space_id).await?;
    let include_memory = matches!(actor.access_level.as_str(), "owner" | "admin");
    Ok(Json(
        agent_response(&state.database, row, include_memory).await?,
    ))
}

pub async fn update(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(agent_id): Path<Uuid>,
    Json(mut request): Json<UpdateAgentRequest>,
) -> Result<Json<AgentResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    validate_update(&mut request)?;
    let request_hash = idempotency::request_hash(&request)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let current = find_agent_tx(&mut transaction, agent_id).await?;
    let actor = member::require_actor_tx(&mut transaction, user.id, current.space_id).await?;
    if !matches!(actor.access_level.as_str(), "owner" | "admin") {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Only an Owner or Admin can configure an Agent",
        ));
    }
    let scope = format!("agent:{agent_id}:update");
    if let Some((_status, response)) =
        idempotency::begin::<AgentResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }
    authorize_transition(&current.status, request.lifecycle.as_ref())?;
    let now = OffsetDateTime::now_utc();
    let next_role = request.role_text.as_deref().unwrap_or(&current.role_text);
    let role_changed = next_role != current.role_text;
    let next_revision = current.role_revision + i64::from(role_changed);
    let current_attention: AttentionConfig =
        serde_json::from_value(current.attention_config_json.clone())
            .map_err(|_| ApiError::Internal)?;
    let next_attention = request
        .attention_config
        .clone()
        .unwrap_or(current_attention);
    let (next_status, retired_at) = match request.lifecycle.as_ref() {
        Some(LifecycleAction::Suspend { .. }) => ("suspended", None),
        Some(LifecycleAction::Resume) => ("active", None),
        Some(LifecycleAction::Retry) => ("provisioning", None),
        Some(LifecycleAction::Retire) => ("retired", Some(now)),
        None => (current.status.as_str(), current.retired_at),
    };
    sqlx::query(
        "UPDATE agents SET role_text = $2, role_revision = $3, attention_config_json = $4, \
         status = $5, updated_at = $6, retired_at = $7, last_error_code = NULL \
         WHERE member_id = $1",
    )
    .bind(agent_id)
    .bind(next_role)
    .bind(next_revision)
    .bind(serde_json::to_value(&next_attention).map_err(|_| ApiError::Internal)?)
    .bind(next_status)
    .bind(now)
    .bind(retired_at)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if matches!(request.lifecycle, Some(LifecycleAction::Retire)) {
        sqlx::query("UPDATE members SET retired_at = $2 WHERE id = $1")
            .bind(agent_id)
            .bind(now)
            .execute(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
    }
    let command_kind = lifecycle_command(request.lifecycle.as_ref()).unwrap_or("agent.configure");
    let payload = serde_json::json!({
        "agent_id": agent_id,
        "space_id": current.space_id,
        "name": current.name,
        "handle": current.handle,
        "role_text": next_role,
        "role_revision": next_revision,
        "driver_kind": current.driver_kind,
        "driver_config": current.driver_config_json,
        "attention_config": next_attention,
        "mode": request.lifecycle.as_ref().and_then(|action| match action {
            LifecycleAction::Suspend { mode } => Some(mode),
            _ => None,
        }),
    });
    let command_id = allocate_command(
        &mut transaction,
        current.computer_id,
        command_kind,
        payload,
        now,
    )
    .await?;
    let action = if role_changed || request.attention_config.is_some() {
        "agent.configured"
    } else {
        match request.lifecycle {
            Some(LifecycleAction::Suspend { .. }) => "agent.suspended",
            Some(LifecycleAction::Resume) => "agent.resumed",
            Some(LifecycleAction::Retry) => "agent.provision_retried",
            Some(LifecycleAction::Retire) => "agent.retired",
            None => return Err(ApiError::Internal),
        }
    };
    super::audit::record(
        &mut transaction,
        super::audit::Event {
            space_id: current.space_id,
            actor_id: Some(actor.id),
            action,
            subject_type: "agent",
            subject_id: agent_id,
            metadata: Some(serde_json::json!({
                "command_id": command_id,
                "previous_role": role_summary(&current.role_text),
                "next_role": role_summary(next_role),
                "previous_status": current.status,
                "next_status": next_status,
            })),
            occurred_at: now,
        },
    )
    .await?;
    publish_agent_status(
        &mut transaction,
        current.space_id,
        agent_id,
        next_status,
        now,
    )
    .await?;
    let updated = find_agent_tx(&mut transaction, agent_id).await?;
    let response = agent_response_tx(&mut transaction, updated, true).await?;
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(response))
}

pub async fn read_memory(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(agent_id): Path<Uuid>,
    Json(request): Json<ReadMemoryRequest>,
) -> Result<(HeaderMap, Json<MemoryContentResponse>), ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let row = find_agent(&state.database, agent_id).await?;
    let actor = member::require_actor(&state.database, user.id, row.space_id).await?;
    if !matches!(actor.access_level.as_str(), "owner" | "admin") {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Only an Owner or Admin can read Agent Memory",
        ));
    }
    let path = request.path.trim();
    if path.is_empty() || path.len() > 1024 {
        return Err(ApiError::validation(
            "invalid_memory_path",
            "Memory path is invalid",
        ));
    }
    let file_exists: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM agent_memory_files WHERE agent_member_id = $1 AND path = $2)",
    )
    .bind(agent_id)
    .bind(path)
    .fetch_one(&state.database)
    .await
    .map_err(ApiError::database)?;
    if !file_exists {
        return Err(ApiError::not_found(
            "memory_file_not_found",
            "Memory file was not found",
        ));
    }
    let computer_online: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM computers WHERE id = $1 AND status = 'online')",
    )
    .bind(row.computer_id)
    .fetch_one(&state.database)
    .await
    .map_err(ApiError::database)?;
    if !computer_online {
        return Err(ApiError::conflict(
            "computer_offline",
            "Agent Memory is unavailable while its Computer is offline",
        ));
    }

    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let command_id = allocate_command(
        &mut transaction,
        row.computer_id,
        "agent.memory.read",
        serde_json::json!({ "agent_id": agent_id, "path": path }),
        OffsetDateTime::now_utc(),
    )
    .await?;
    let (sender, receiver) = tokio::sync::oneshot::channel();
    state
        .memory_read_waiters
        .lock()
        .await
        .insert(command_id, sender);
    if let Err(error) = transaction.commit().await {
        state.memory_read_waiters.lock().await.remove(&command_id);
        return Err(ApiError::database(error));
    }
    let result = tokio::time::timeout(std::time::Duration::from_secs(10), receiver).await;
    state.memory_read_waiters.lock().await.remove(&command_id);
    let (ok, result) = result
        .map_err(|_| {
            ApiError::conflict(
                "computer_unavailable",
                "Computer did not return Agent Memory in time",
            )
        })?
        .map_err(|_| {
            ApiError::conflict(
                "computer_unavailable",
                "Computer disconnected while reading Agent Memory",
            )
        })?;
    if !ok {
        return Err(ApiError::conflict(
            "memory_read_failed",
            "Computer could not read the Agent Memory file",
        ));
    }
    let response: MemoryContentResponse =
        serde_json::from_value(result).map_err(|_| ApiError::Internal)?;
    if response.path != path {
        return Err(ApiError::Internal);
    }
    let mut headers = HeaderMap::new();
    headers.insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    Ok((headers, Json(response)))
}

const AGENT_SELECT: &str = "SELECT agents.member_id, agents.space_id, agents.computer_id, \
    members.display_name AS name, members.handle, members.access_level, agents.role_text, \
    agents.role_revision, agents.status, agents.driver_kind, agents.attention_config_json, \
    agents.driver_config_json, agents.created_at, agents.updated_at, agents.retired_at, \
    agents.last_error_code FROM agents \
    JOIN members ON members.id = agents.member_id";

pub async fn create(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(space_id): Path<Uuid>,
    Json(mut request): Json<CreateAgentRequest>,
) -> Result<(StatusCode, Json<CreateAgentResponse>), ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    validate(&mut request)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let actor = member::require_actor_tx(&mut transaction, user.id, space_id).await?;
    if actor.access_level != "owner" && actor.access_level != "admin" {
        drop(transaction);
        let response =
            approval::request_human_agent_create(&state.database, actor.id, request, key.0).await?;
        return Ok((
            StatusCode::ACCEPTED,
            Json(CreateAgentResponse::Approval(response)),
        ));
    }
    if request.access_level == "admin" && actor.access_level != "owner" {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Only the Owner can create an Agent as Admin",
        ));
    }
    let request_hash = idempotency::request_hash(&request)?;
    let computer_status: Option<String> = sqlx::query_scalar(
        "SELECT status FROM computers WHERE id = $1 AND space_id = $2 FOR UPDATE",
    )
    .bind(request.computer_id)
    .bind(space_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    match computer_status.as_deref() {
        Some("online") => {}
        Some(_) => {
            return Err(ApiError::conflict(
                "computer_offline",
                "Computer must be online",
            ));
        }
        None => {
            return Err(ApiError::not_found(
                "computer_not_found",
                "Computer was not found",
            ));
        }
    }
    let scope = format!("space:{space_id}:agent:create");
    if let Some((status, response)) =
        idempotency::begin::<AgentResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok((status, Json(CreateAgentResponse::Agent(Box::new(response)))));
    }
    let response = provision_agent_tx(&mut transaction, space_id, actor.id, request).await?;
    idempotency::finish(
        &mut transaction,
        &scope,
        key,
        StatusCode::CREATED,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok((
        StatusCode::CREATED,
        Json(CreateAgentResponse::Agent(Box::new(response))),
    ))
}

pub(super) async fn provision_agent_tx(
    transaction: &mut Transaction<'_, Postgres>,
    space_id: Uuid,
    created_by_member_id: Uuid,
    request: CreateAgentRequest,
) -> Result<AgentResponse, ApiError> {
    let handle = unique_handle(
        transaction,
        space_id,
        request.handle.as_deref(),
        &request.name,
    )
    .await?;
    let member_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    sqlx::query(
        "INSERT INTO members (id, space_id, kind, display_name, handle, avatar_seed, access_level, created_at) \
         VALUES ($1, $2, 'agent', $3, $4, $5, $6, $7)",
    )
    .bind(member_id).bind(space_id).bind(&request.name).bind(&handle)
    .bind(member_id.to_string()).bind(&request.access_level).bind(now)
    .execute(&mut **transaction).await.map_err(ApiError::database)?;
    let general_id: Uuid = sqlx::query_scalar(
        "SELECT id FROM channels WHERE space_id = $1 AND slug = 'general' AND archived_at IS NULL",
    )
    .bind(space_id)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    sqlx::query(
        "INSERT INTO channel_members (channel_id, member_id, space_id, joined_at) VALUES ($1, $2, $3, $4)",
    )
    .bind(general_id).bind(member_id).bind(space_id).bind(now)
    .execute(&mut **transaction).await.map_err(ApiError::database)?;
    let driver_config = serde_json::json!({ "schema_version": 1 });
    let attention_config = AttentionConfig::default();
    sqlx::query(
        "INSERT INTO agents (member_id, space_id, computer_id, role_text, driver_kind, \
         driver_config_json, attention_config_json, created_by_member_id, created_at, updated_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)",
    )
    .bind(member_id)
    .bind(space_id)
    .bind(request.computer_id)
    .bind(&request.role_text)
    .bind(&request.driver_kind)
    .bind(&driver_config)
    .bind(serde_json::to_value(&attention_config).map_err(|_| ApiError::Internal)?)
    .bind(created_by_member_id)
    .bind(now)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    let computer_seq: i64 = sqlx::query_scalar(
        "UPDATE computers SET next_command_seq = next_command_seq + 1 WHERE id = $1 \
         RETURNING next_command_seq - 1",
    )
    .bind(request.computer_id)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    let command_id = Uuid::now_v7();
    let payload = serde_json::json!({
        "agent_id": member_id, "space_id": space_id, "name": request.name,
        "handle": handle, "role_text": request.role_text, "role_revision": 1,
        "driver_kind": request.driver_kind, "driver_config": driver_config,
        "attention_config": attention_config
    });
    sqlx::query(
        "INSERT INTO computer_commands (id, computer_id, computer_seq, kind, payload_json, created_at) \
         VALUES ($1, $2, $3, 'agent.provision', $4, $5)",
    )
    .bind(command_id).bind(request.computer_id).bind(computer_seq).bind(payload).bind(now)
    .execute(&mut **transaction).await.map_err(ApiError::database)?;
    super::audit::record(
        transaction,
        super::audit::Event {
            space_id,
            actor_id: Some(created_by_member_id),
            action: "agent.created",
            subject_type: "agent",
            subject_id: member_id,
            metadata: Some(serde_json::json!({
                "computer_id": request.computer_id,
                "command_id": command_id
            })),
            occurred_at: now,
        },
    )
    .await?;
    super::outbox::publish(
        transaction,
        "member.updated",
        member_id,
        serde_json::json!({ "space_id": space_id, "member_id": member_id }),
        now,
    )
    .await?;
    Ok(AgentResponse {
        member_id,
        space_id,
        computer_id: request.computer_id,
        name: request.name,
        handle,
        access_level: request.access_level,
        role_text: request.role_text,
        role_revision: 1,
        status: "provisioning".to_owned(),
        driver_kind: request.driver_kind,
        attention_config,
        created_at: now,
        updated_at: now,
        retired_at: None,
        last_error_code: None,
        memory_files: Vec::new(),
    })
}

pub(super) fn validate(request: &mut CreateAgentRequest) -> Result<(), ApiError> {
    request.name = request.name.trim().to_owned();
    request.role_text = request.role_text.trim().to_owned();
    request.handle = request
        .handle
        .take()
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty());
    if !(1..=40).contains(&request.name.chars().count()) {
        return Err(ApiError::validation(
            "invalid_agent_name",
            "Agent name must contain 1 to 40 characters",
        ));
    }
    if !(1..=12_000).contains(&request.role_text.chars().count()) {
        return Err(ApiError::validation(
            "invalid_agent_role",
            "Agent Role must contain 1 to 12000 characters",
        ));
    }
    if !matches!(request.access_level.as_str(), "member" | "admin") {
        return Err(ApiError::validation(
            "invalid_access_level",
            "Agent access level must be Member or Admin",
        ));
    }
    if !matches!(request.driver_kind.as_str(), "codex" | "builtin") {
        return Err(ApiError::validation(
            "invalid_driver",
            "Driver must be codex or builtin",
        ));
    }
    Ok(())
}

fn validate_update(request: &mut UpdateAgentRequest) -> Result<(), ApiError> {
    if let Some(role) = &mut request.role_text {
        *role = role.trim().to_owned();
        if !(1..=12_000).contains(&role.chars().count()) {
            return Err(ApiError::validation(
                "invalid_agent_role",
                "Agent Role must contain 1 to 12000 characters",
            ));
        }
    }
    if let Some(config) = &request.attention_config {
        if !config.dm_immediate || !config.mention_immediate {
            return Err(ApiError::validation(
                "invalid_attention_config",
                "DM and mention immediate attention are fixed in Sumi v1",
            ));
        }
        if !(1..=60).contains(&config.ambient_debounce_seconds)
            || !(5..=300).contains(&config.ambient_max_wait_seconds)
            || config.ambient_max_wait_seconds < config.ambient_debounce_seconds
            || config.max_retry_count == 0
        {
            return Err(ApiError::validation(
                "invalid_attention_config",
                "Agent attention configuration is outside the supported range",
            ));
        }
    }
    if matches!(
        request.lifecycle,
        Some(LifecycleAction::Retire | LifecycleAction::Retry)
    ) && (request.role_text.is_some() || request.attention_config.is_some())
    {
        return Err(ApiError::validation(
            "invalid_agent_update",
            "Retry and retire cannot be combined with Agent configuration changes",
        ));
    }
    if request.role_text.is_none()
        && request.attention_config.is_none()
        && request.lifecycle.is_none()
    {
        return Err(ApiError::validation(
            "empty_agent_update",
            "Agent update must include a configuration or lifecycle change",
        ));
    }
    Ok(())
}

fn authorize_transition(status: &str, action: Option<&LifecycleAction>) -> Result<(), ApiError> {
    let allowed = match action {
        None => status != "retired",
        Some(LifecycleAction::Suspend { .. }) => status == "active",
        Some(LifecycleAction::Resume) => status == "suspended",
        Some(LifecycleAction::Retry) => status == "error",
        Some(LifecycleAction::Retire) => matches!(status, "active" | "suspended" | "error"),
    };
    if allowed {
        Ok(())
    } else {
        Err(ApiError::conflict(
            "invalid_agent_transition",
            "Agent lifecycle transition is not allowed from its current status",
        ))
    }
}

fn lifecycle_command(action: Option<&LifecycleAction>) -> Option<&'static str> {
    match action {
        Some(LifecycleAction::Suspend { .. }) => Some("agent.suspend"),
        Some(LifecycleAction::Resume) => Some("agent.resume"),
        Some(LifecycleAction::Retry) => Some("agent.provision"),
        Some(LifecycleAction::Retire) => Some("agent.retire"),
        None => None,
    }
}

fn role_summary(role: &str) -> String {
    let digest = Sha256::digest(role.as_bytes());
    format!(
        "sha256:{};chars:{}",
        hex::encode(digest),
        role.chars().count()
    )
}

async fn allocate_command(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    computer_id: Uuid,
    kind: &str,
    payload: serde_json::Value,
    now: OffsetDateTime,
) -> Result<Uuid, ApiError> {
    let computer_seq: i64 = sqlx::query_scalar(
        "UPDATE computers SET next_command_seq = next_command_seq + 1 WHERE id = $1 \
         AND status != 'revoked' RETURNING next_command_seq - 1",
    )
    .bind(computer_id)
    .fetch_optional(&mut **transaction)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| ApiError::conflict("computer_revoked", "Agent Computer has been revoked"))?;
    let command_id = Uuid::now_v7();
    sqlx::query(
        "INSERT INTO computer_commands \
         (id, computer_id, computer_seq, kind, payload_json, created_at) \
         VALUES ($1, $2, $3, $4, $5, $6)",
    )
    .bind(command_id)
    .bind(computer_id)
    .bind(computer_seq)
    .bind(kind)
    .bind(payload)
    .bind(now)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(command_id)
}

async fn publish_agent_status(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    space_id: Uuid,
    agent_id: Uuid,
    status: &str,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    super::outbox::publish(
        transaction,
        "agent.status_changed",
        agent_id,
        serde_json::json!({
            "space_id": space_id,
            "agent_member_id": agent_id,
            "status": status,
        }),
        now,
    )
    .await
}

async fn find_agent(pool: &sqlx::PgPool, agent_id: Uuid) -> Result<AgentRow, ApiError> {
    sqlx::query_as::<_, AgentRow>(&format!("{} WHERE agents.member_id = $1", AGENT_SELECT))
        .bind(agent_id)
        .fetch_optional(pool)
        .await
        .map_err(ApiError::database)?
        .ok_or_else(|| ApiError::not_found("agent_not_found", "Agent was not found"))
}

async fn find_agent_tx(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    agent_id: Uuid,
) -> Result<AgentRow, ApiError> {
    sqlx::query_as::<_, AgentRow>(&format!(
        "{} WHERE agents.member_id = $1 FOR UPDATE OF agents, members",
        AGENT_SELECT
    ))
    .bind(agent_id)
    .fetch_optional(&mut **transaction)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| ApiError::not_found("agent_not_found", "Agent was not found"))
}

async fn agent_response(
    pool: &sqlx::PgPool,
    row: AgentRow,
    include_memory: bool,
) -> Result<AgentResponse, ApiError> {
    let memory_files = if include_memory {
        memory_files(pool, row.member_id).await?
    } else {
        Vec::new()
    };
    row.into_response(memory_files)
}

async fn agent_response_tx(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    row: AgentRow,
    include_memory: bool,
) -> Result<AgentResponse, ApiError> {
    let memory_files = if include_memory {
        let rows: Vec<(String, i64, Vec<u8>, OffsetDateTime)> = sqlx::query_as(
            "SELECT path, size, sha256, updated_at FROM agent_memory_files \
             WHERE agent_member_id = $1 ORDER BY path",
        )
        .bind(row.member_id)
        .fetch_all(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
        memory_responses(rows)
    } else {
        Vec::new()
    };
    row.into_response(memory_files)
}

async fn memory_files(
    pool: &sqlx::PgPool,
    agent_id: Uuid,
) -> Result<Vec<MemoryFileResponse>, ApiError> {
    let rows: Vec<(String, i64, Vec<u8>, OffsetDateTime)> = sqlx::query_as(
        "SELECT path, size, sha256, updated_at FROM agent_memory_files \
         WHERE agent_member_id = $1 ORDER BY path",
    )
    .bind(agent_id)
    .fetch_all(pool)
    .await
    .map_err(ApiError::database)?;
    Ok(memory_responses(rows))
}

fn memory_responses(rows: Vec<(String, i64, Vec<u8>, OffsetDateTime)>) -> Vec<MemoryFileResponse> {
    rows.into_iter()
        .map(|(path, size, sha256, updated_at)| MemoryFileResponse {
            path,
            size,
            sha256: hex::encode(sha256),
            updated_at,
        })
        .collect()
}

impl AgentRow {
    fn into_response(
        self,
        memory_files: Vec<MemoryFileResponse>,
    ) -> Result<AgentResponse, ApiError> {
        let attention_config =
            serde_json::from_value(self.attention_config_json).map_err(|_| ApiError::Internal)?;
        Ok(AgentResponse {
            member_id: self.member_id,
            space_id: self.space_id,
            computer_id: self.computer_id,
            name: self.name,
            handle: self.handle,
            access_level: self.access_level,
            role_text: self.role_text,
            role_revision: self.role_revision,
            status: self.status,
            driver_kind: self.driver_kind,
            attention_config,
            created_at: self.created_at,
            updated_at: self.updated_at,
            retired_at: self.retired_at,
            last_error_code: self.last_error_code,
            memory_files,
        })
    }
}

async fn unique_handle(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    space_id: Uuid,
    requested: Option<&str>,
    display_name: &str,
) -> Result<String, ApiError> {
    let base = requested.map_or_else(|| space::member_handle(display_name), space::member_handle);
    if requested.is_some_and(|requested| requested != base) || base.len() > 32 {
        return Err(ApiError::validation(
            "invalid_agent_handle",
            "Agent handle is invalid",
        ));
    }
    let exists: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM members WHERE space_id = $1 AND lower(handle) = lower($2))",
    )
    .bind(space_id)
    .bind(&base)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    if !exists {
        return Ok(base);
    }
    let prefix = base.chars().take(25).collect::<String>();
    Ok(format!(
        "{}-{}",
        prefix.trim_end_matches('-'),
        &Uuid::now_v7().simple().to_string()[..6]
    ))
}
