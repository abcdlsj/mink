use axum::{
    Json,
    extract::{Path, State, WebSocketUpgrade, ws},
    http::{HeaderMap, StatusCode},
    response::Response,
};
use axum_extra::extract::CookieJar;
use serde::{Deserialize, Serialize};
use time::{Duration, OffsetDateTime};
use uuid::Uuid;

use super::{AppState, api_error::ApiError, auth, idempotency, member};

use super::{
    computer_pairing::ComputerResponse,
    computer_protocol::{ComputerFrame, ServerFrame},
};

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
         FROM computers WHERE space_id = $1 AND status != 'revoked' ORDER BY created_at DESC",
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

pub async fn delete_computer(
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
            "Only a Human Owner or Admin can delete a Computer",
        ));
    }
    let request_hash = idempotency::request_hash(&serde_json::json!({}))?;
    let scope = format!("computer:{computer_id}:delete");
    if let Some((_, response)) =
        idempotency::begin::<ComputerResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }
    let now = OffsetDateTime::now_utc();
    sqlx::query(
        "UPDATE agent_runs SET status = 'canceled', finished_at = COALESCE(finished_at, $2), \
         error_code = COALESCE(error_code, 'computer_deleted') \
         WHERE computer_id = $1 AND status IN ('queued', 'running')",
    )
    .bind(computer_id)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let retired_agents: Vec<Uuid> = sqlx::query_scalar(
        "UPDATE agents SET status = 'retired', retired_at = COALESCE(retired_at, $2), \
         updated_at = $2 WHERE computer_id = $1 AND status != 'retired' RETURNING member_id",
    )
    .bind(computer_id)
    .bind(now)
    .fetch_all(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if !retired_agents.is_empty() {
        sqlx::query("UPDATE members SET retired_at = COALESCE(retired_at, $2) WHERE id = ANY($1)")
            .bind(&retired_agents)
            .bind(now)
            .execute(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
    }
    let row = sqlx::query_as::<_, (Uuid, Uuid, String, String, String, String, String, Option<OffsetDateTime>, OffsetDateTime)>(
        "UPDATE computers SET status = 'revoked', revoked_at = COALESCE(revoked_at, now()) \
         WHERE id = $1 RETURNING id, space_id, name, hostname, os, status, daemon_version, last_seen_at, created_at",
    )
    .bind(computer_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    publish_computer_status(&mut transaction, row.1, computer_id, "revoked").await?;
    for agent_id in retired_agents {
        super::outbox::publish(
            &mut transaction,
            "agent.status_changed",
            agent_id,
            serde_json::json!({ "space_id": row.1, "agent_member_id": agent_id, "status": "retired" }),
            now,
        )
        .await?;
    }
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
    super::computer_auth::require_computer(&state, &headers, computer_id).await?;
    Ok(upgrade.on_upgrade(move |socket| computer_socket(state, computer_id, socket)))
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
    super::computer_auth::require_computer(&state, &headers, computer_id).await?;
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
    super::computer_auth::require_computer(&state, &headers, computer_id).await?;
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
        "driver_kind": driver_kind,
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
    super::outbox::publish(
        &mut transaction,
        "agent.run_changed",
        run_id,
        serde_json::json!({
            "space_id": space_id,
            "agent_member_id": agent_id,
            "run_id": run_id,
            "status": "queued",
        }),
        now,
    )
    .await?;
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
    super::computer_auth::require_computer(&state, &headers, computer_id).await?;
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
    super::computer_auth::require_computer(&state, &headers, computer_id).await?;
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
    publish_run_status(
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
                if is_deleted(state, computer_id).await? {
                    send_frame(socket, &ServerFrame::Shutdown { reason: "computer_deleted".to_string() }).await?;
                    break;
                }
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

async fn is_deleted(state: &AppState, computer_id: Uuid) -> Result<bool, ApiError> {
    Ok(
        sqlx::query_scalar::<_, bool>("SELECT status = 'revoked' FROM computers WHERE id = $1")
            .bind(computer_id)
            .fetch_optional(&state.database)
            .await
            .map_err(ApiError::database)?
            .unwrap_or(true),
    )
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
            super::outbox::publish(
                &mut transaction,
                "agent.status_changed",
                agent_id,
                serde_json::json!({
                    "space_id": space_id, "agent_member_id": agent_id,
                    "status": new_status,
                    "error_code": if ok { None } else { result.get("error_code") },
                }),
                OffsetDateTime::now_utc(),
            )
            .await?;
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
    publish_run_status(transaction, run_id, agent_id, space_id, run_status).await?;
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
            super::agent_gateway::publish_inbox_update(
                transaction,
                space_id,
                admin_id,
                item_id,
                now,
            )
            .await?;
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

async fn publish_run_status(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    run_id: Uuid,
    agent_id: Uuid,
    space_id: Uuid,
    status: &str,
) -> Result<(), ApiError> {
    super::outbox::publish(
        transaction,
        "agent.run_changed",
        run_id,
        serde_json::json!({
            "space_id": space_id,
            "agent_member_id": agent_id,
            "run_id": run_id,
            "status": status,
        }),
        OffsetDateTime::now_utc(),
    )
    .await
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
        publish_computer_status(&mut transaction, space_id, computer_id, &status).await?;
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
        publish_computer_status(transaction, space_id, computer_id, "offline").await?;
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

async fn publish_computer_status(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    space_id: Uuid,
    computer_id: Uuid,
    status: &str,
) -> Result<(), ApiError> {
    super::outbox::publish(
        transaction,
        "computer.status_changed",
        computer_id,
        serde_json::json!({
            "space_id": space_id,
            "computer_id": computer_id,
            "status": status,
        }),
        OffsetDateTime::now_utc(),
    )
    .await
}
