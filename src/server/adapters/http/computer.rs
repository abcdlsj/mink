use super::*;
use crate::server::adapters::websocket::computer_socket;
use crate::server::domain::identity::valid_display_name;

pub(super) async fn connect_computer(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
    upgrade: WebSocketUpgrade,
) -> Result<Response, ApiError> {
    let raw = bearer_token(&headers)?;
    let mut storage = state.storage.clone();
    let identity = AuthenticateComputer::execute(
        &mut storage,
        ComputerId::from_uuid(computer_id),
        &token_hash(raw),
    )
    .await
    .map_err(application_error)?;
    let storage = state.storage.clone();
    let pool = state.pool.clone();
    let queries = state.queries.clone();
    Ok(upgrade.on_upgrade(move |socket| {
        computer_socket(
            socket,
            storage,
            pool,
            queries,
            computer_id,
            identity.deleted,
        )
    }))
}

pub(super) async fn authenticate_computer(
    state: &RuntimeState,
    headers: &HeaderMap,
    computer_id: Uuid,
) -> Result<(), ApiError> {
    let raw = bearer_token(headers)?;
    let mut storage = state.storage.clone();
    AuthenticateComputer::require_active(
        &mut storage,
        ComputerId::from_uuid(computer_id),
        &token_hash(raw),
    )
    .await
    .map_err(application_error)?;
    Ok(())
}

pub(super) fn computer_response(computer: &PairedComputer) -> ComputerResponse {
    ComputerResponse {
        id: computer.id.into_uuid(),
        space_id: computer.space_id.into_uuid(),
        name: computer.name.clone(),
        hostname: computer.hostname.clone(),
        os: match computer.os {
            ComputerOsKind::MacOs => ComputerOs::Macos,
            ComputerOsKind::Linux => ComputerOs::Linux,
        },
        daemon_version: computer.daemon_version.clone().unwrap_or_default(),
        status: computer_status(computer),
        last_seen_at: optional_timestamp(computer.last_seen_at),
        created_at: timestamp(computer.created_at),
    }
}

pub(super) fn computer_status(computer: &PairedComputer) -> ComputerStatus {
    if computer.deleted {
        ComputerStatus::Revoked
    } else if computer.connected {
        ComputerStatus::Online
    } else {
        ComputerStatus::Offline
    }
}

pub(super) async fn computer_agents(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    let rows = sqlx::query(
        "SELECT a.member_id,a.space_id,a.role_text,a.role_revision,a.lifecycle,a.driver_kind,\
                a.driver_config_json,m.display_name,m.access_level \
         FROM agents a JOIN members m ON m.id=a.member_id \
         WHERE a.computer_id=$1 AND a.lifecycle<>'retired' ORDER BY a.created_at,a.member_id",
    )
    .bind(computer_id)
    .fetch_all(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok(Json(Value::Array(
        rows.iter()
            .map(|row| {
                json!({
                    "member_id": row.get::<Uuid,_>("member_id"),
                    "space_id": row.get::<Uuid,_>("space_id"),
                    "display_name": row.get::<String,_>("display_name"),
                    "access_level": row.get::<String,_>("access_level"),
                    "role_text": row.get::<String,_>("role_text"),
                    "role_revision": row.get::<i64,_>("role_revision"),
                    "lifecycle": row.get::<String,_>("lifecycle"),
                    "driver_kind": row.get::<String,_>("driver_kind"),
                    "driver_config": row.get::<Value,_>("driver_config_json"),
                })
            })
            .collect(),
    )))
}

pub(super) async fn list_computers(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<ComputerResponse>>, ApiError> {
    current_member(&state, &jar, space_id).await?;
    let mut storage = state.storage.clone();
    let computers = ListSpaceComputers::execute(&mut storage, SpaceId::from_uuid(space_id))
        .await
        .map_err(application_error)?;
    Ok(Json(computers.iter().map(computer_response).collect()))
}

pub(super) async fn list_agents(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<AgentResponse>>, ApiError> {
    current_member(&state, &jar, space_id).await?;
    let rows = sqlx::query(
        &format!(
            "SELECT a.*,m.display_name,m.access_level,c.connection_status,\
             c.deleted_at AS computer_deleted_at,{ACTIVITY_COLUMNS} \
             FROM agents a JOIN members m ON m.id=a.member_id LEFT JOIN computers c ON c.id=a.computer_id \
             {ACTIVITY_JOINS} WHERE a.space_id=$1 ORDER BY a.created_at"
        ),
    )
    .bind(space_id)
    .fetch_all(&state.pool)
    .await
    .map_err(map_sqlx)?;
    let mut agents = Vec::with_capacity(rows.len());
    for row in &rows {
        agents.push(agent_row(row)?);
    }
    Ok(Json(agents))
}

pub(super) async fn get_agent(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(agent_id): Path<Uuid>,
) -> Result<Json<AgentResponse>, ApiError> {
    let row = sqlx::query(
        &format!(
            "SELECT a.*,m.display_name,m.access_level,c.connection_status,\
             c.deleted_at AS computer_deleted_at,{ACTIVITY_COLUMNS} \
             FROM agents a JOIN members m ON m.id=a.member_id LEFT JOIN computers c ON c.id=a.computer_id \
             {ACTIVITY_JOINS} WHERE a.member_id=$1"
        ),
    )
    .bind(agent_id)
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::not_found)?;
    current_member(&state, &jar, row.get("space_id")).await?;
    let mut agent = agent_row(&row)?;
    agent.memory_files = memory_files(&state, row.get("computer_id"), agent_id).await;
    Ok(Json(agent))
}

pub(super) async fn current_agent_run(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(agent_id): Path<Uuid>,
) -> Result<Json<AgentRuntimeResponse>, ApiError> {
    let viewer_id = agent_space_member(&state, &jar, agent_id).await?;
    let mut storage = state.storage.clone();
    let (run_id, another_item_waiting) = ReadCurrentAgentRun::execute(
        &mut storage,
        MemberId::from_uuid(agent_id),
        MemberId::from_uuid(viewer_id),
    )
    .await
    .map_err(application_error)?;
    let Some(run_id) = run_id else {
        return Ok(Json(AgentRuntimeResponse {
            current_run: None,
            current_task: None,
            focus: None,
            another_item_waiting: false,
            session_continuity: unavailable_continuity(),
        }));
    };
    let run = run_projection(&state.pool, run_id.into_uuid()).await?;
    let current_task = match run.task_id {
        Some(task_id) => Some(task_projection(&state.pool, task_id).await?),
        None => None,
    };
    let scope = match run.task_id {
        Some(task_id) => SessionScope::Task(TaskId::from_uuid(task_id)),
        None => SessionScope::Thread(ThreadId::from_uuid(run.focus.id)),
    };
    let session_continuity = agent_continuity(&state, agent_id, scope).await;
    Ok(Json(AgentRuntimeResponse {
        focus: Some(run.focus.clone()),
        current_run: Some(run),
        current_task,
        another_item_waiting,
        session_continuity,
    }))
}

pub(super) async fn retire_agent(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(agent_id): Path<Uuid>,
) -> Result<Json<AgentResponse>, ApiError> {
    let key = IdempotencyKey::from_uuid(idempotency_header(&headers)?);
    let actor_id = require_agent_governor(&state, &jar, agent_id).await?;
    let mut storage = state.storage.clone();
    RetireAgent::execute(
        &mut storage,
        MemberId::from_uuid(actor_id),
        MemberId::from_uuid(agent_id),
        key,
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    let row = sqlx::query(&format!(
        "SELECT a.*,m.display_name,m.access_level,c.connection_status,\
             c.deleted_at AS computer_deleted_at,{ACTIVITY_COLUMNS} \
             FROM agents a JOIN members m ON m.id=a.member_id \
             LEFT JOIN computers c ON c.id=a.computer_id {ACTIVITY_JOINS} WHERE a.member_id=$1"
    ))
    .bind(agent_id)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok(Json(agent_row(&row)?))
}

pub(super) async fn delete_computer(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
) -> Result<Json<ComputerResponse>, ApiError> {
    let key = IdempotencyKey::from_uuid(idempotency_header(&headers)?);
    let actor_id = require_computer_governor(&state, &jar, computer_id).await?;
    let mut storage = state.storage.clone();
    DeleteComputer::execute(
        &mut storage,
        MemberId::from_uuid(actor_id),
        ComputerId::from_uuid(computer_id),
        key,
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    let computer = ReadPairedComputer::execute(&mut storage, ComputerId::from_uuid(computer_id))
        .await
        .map_err(application_error)?;
    Ok(Json(computer_response(&computer)))
}

pub(super) async fn create_agent(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(space_id): Path<Uuid>,
    Json(body): Json<CreateAgentBody>,
) -> Result<(StatusCode, Json<AgentResponse>), ApiError> {
    let actor_id = current_member(&state, &jar, space_id).await?;
    let requested_access = match body.access_level.as_str() {
        "member" => AccessLevel::Member,
        "admin" => AccessLevel::Admin,
        _ => return Err(ApiError::invalid("Agent configuration is invalid")),
    };
    let name = body.name.trim();
    let role = body.role_text.trim();
    if !valid_display_name(name)
        || name.chars().count() > 40
        || role.is_empty()
        || role.chars().count() > 12_000
        || !matches!(body.driver_kind.as_str(), "codex" | "builtin")
        || !matches!(body.access_level.as_str(), "member" | "admin")
    {
        return Err(ApiError::invalid("Agent configuration is invalid"));
    }
    let agent_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let mut storage = state.storage.clone();
    CreateAgent::execute(
        &mut storage,
        CreateAgentInput {
            agent_member_id: MemberId::from_uuid(agent_id),
            space_id: SpaceId::from_uuid(space_id),
            display_name: name.to_owned(),
            access_level: requested_access,
            role_text: role.to_owned(),
            computer_id: ComputerId::from_uuid(body.computer_id),
            driver_kind: if body.driver_kind == "codex" {
                DriverKind::Codex
            } else {
                DriverKind::Builtin
            },
            actor_member_id: MemberId::from_uuid(actor_id),
            idempotency_key: IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            now,
        },
    )
    .await
    .map_err(application_error)?;

    let row = sqlx::query(
        &format!(
            "SELECT a.*,m.display_name,m.access_level,c.connection_status,\
             c.deleted_at AS computer_deleted_at,{ACTIVITY_COLUMNS} \
             FROM agents a JOIN members m ON m.id=a.member_id JOIN computers c ON c.id=a.computer_id \
             {ACTIVITY_JOINS} WHERE a.member_id=$1"
        ),
    )
    .bind(agent_id)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok((StatusCode::CREATED, Json(agent_row(&row)?)))
}

pub(super) const ACTIVITY_COLUMNS: &str = "\
    active_run.status AS run_status,\
    active_run.task_id AS run_task_id,\
    active_task.title AS run_task_title,\
    focus_channel.slug AS run_focus_slug,\
    focus_thread.channel_seq AS run_focus_seq,\
    COALESCE(\
        (SELECT r.error_code FROM agent_runs r \
         WHERE r.agent_id=a.member_id AND r.outcome_code='failed' AND r.error_code IS NOT NULL \
         ORDER BY r.finished_at DESC NULLS LAST,r.id DESC LIMIT 1),\
        (SELECT i.last_error_code FROM inbox_items i \
         WHERE i.member_id=a.member_id AND i.status='pending' AND i.last_error_code IS NOT NULL \
         ORDER BY i.created_at DESC,i.id DESC LIMIT 1)\
    ) AS last_error_code";

pub(super) const ACTIVITY_JOINS: &str = "\
    LEFT JOIN LATERAL (\
        SELECT r.status,r.task_id,r.focus_thread_id FROM agent_runs r \
        WHERE r.agent_id=a.member_id \
          AND r.status NOT IN ('completed','yielded','failed','canceled') \
        ORDER BY r.created_at DESC LIMIT 1\
    ) active_run ON true \
    LEFT JOIN tasks active_task ON active_task.id=active_run.task_id \
    LEFT JOIN messages focus_thread ON focus_thread.id=active_run.focus_thread_id \
        AND focus_thread.placement='root' \
    LEFT JOIN channels focus_channel ON focus_channel.id=focus_thread.channel_id";

/// Projects the limits the domain enforces, so the published policy cannot drift from the applied one.
pub(super) fn attention_policy() -> AttentionConfig {
    AttentionConfig {
        dm_immediate: true,
        mention_immediate: true,
        ambient_enabled: true,
        ambient_debounce_seconds: AttentionPolicy::AMBIENT_DEBOUNCE_SECONDS,
        ambient_max_wait_seconds: AttentionPolicy::AMBIENT_MAX_WAIT_SECONDS,
        max_retry_count: AttentionPolicy::MAX_RETRY_COUNT,
    }
}

pub(super) fn focus_address(row: &sqlx::postgres::PgRow) -> Option<String> {
    let slug: Option<String> = row.get("run_focus_slug");
    let seq: Option<i64> = row.get("run_focus_seq");
    Some(format!("#{}:{}", slug?, seq?))
}

pub(super) fn agent_activity(
    row: &sqlx::postgres::PgRow,
    activity_status: AgentActivityStatus,
) -> Option<AgentActivityResponse> {
    let run_status = row.get::<Option<String>, _>("run_status")?;
    let address = focus_address(row);
    let task_title: Option<String> = row.get("run_task_title");
    let label = match (run_status.as_str(), address.as_deref()) {
        ("stopping", Some(address)) => format!("Stopping work on {address}"),
        ("stopping", None) => "Stopping the current Run".to_owned(),
        ("finalizing", Some(address)) => format!("Finishing work on {address}"),
        ("finalizing", None) => "Finishing the current Run".to_owned(),
        ("queued", Some(address)) => format!("Queued for {address}"),
        ("queued", None) => "Queued for a Run".to_owned(),
        (_, Some(address)) => match task_title.as_deref() {
            Some(title) => format!("Working on {address} for {title}"),
            None => format!("Working on {address}"),
        },
        (_, None) => "Working on a Run".to_owned(),
    };
    Some(AgentActivityResponse {
        kind: run_status,
        label,
        status: activity_status,
    })
}

pub(super) fn run_activity_status(status: Option<&str>) -> AgentActivityStatus {
    match status {
        Some("dispatched") => AgentActivityStatus::Dispatched,
        Some("working") => AgentActivityStatus::Working,
        _ => AgentActivityStatus::Idle,
    }
}

pub(super) fn agent_row(row: &sqlx::postgres::PgRow) -> Result<AgentResponse, ApiError> {
    let lifecycle: &str = row.get("lifecycle");
    let connection: Option<String> = row.get("connection_status");
    let run_status: Option<String> = row.get("run_status");
    let retired = lifecycle == "retired";
    let desired_lifecycle = match lifecycle {
        "suspended" => AgentLifecycle::Suspended,
        "retired" => AgentLifecycle::Retired,
        _ => AgentLifecycle::Active,
    };
    let provision_status = match lifecycle {
        "provisioning" => ProvisionStatus::Provisioning,
        "error" => ProvisionStatus::Error,
        _ => ProvisionStatus::Ready,
    };
    // Reachability is reported alongside activity, not instead of it. A Run keeps its status while its
    // Computer is offline, because nothing about the Run changed: we simply cannot hear about it.
    let computer_reachable = connection.as_deref() == Some("online");
    let activity_status = match lifecycle {
        "error" => AgentActivityStatus::Error,
        "suspended" => AgentActivityStatus::Suspended,
        _ => run_activity_status(run_status.as_deref()),
    };
    Ok(AgentResponse {
        member_id: row.get("member_id"),
        space_id: row.get("space_id"),
        computer_id: row.get("computer_id"),
        name: row.get("display_name"),
        access_level: match row.get::<&str, _>("access_level") {
            "admin" => AgentAccessLevel::Admin,
            "member" => AgentAccessLevel::Member,
            _ => return Err(ApiError::internal()),
        },
        role_text: row.get("role_text"),
        role_revision: u64::try_from(row.get::<i64, _>("role_revision"))
            .map_err(|_| ApiError::internal())?,
        desired_lifecycle,
        provision_status,
        activity_status,
        computer_reachable,
        driver_kind: match row.get::<&str, _>("driver_kind") {
            "codex" => DriverKindCode::Codex,
            "builtin" => DriverKindCode::Builtin,
            _ => return Err(ApiError::internal()),
        },
        attention_config: attention_policy(),
        activity: agent_activity(row, activity_status),
        last_error_code: row.get("last_error_code"),
        memory_files: Vec::new(),
        created_at: timestamp(row.get("created_at")),
        updated_at: timestamp(row.get("created_at")),
        retired_at: if retired {
            optional_timestamp(row.get("retired_at"))
        } else {
            None
        },
    })
}

pub(super) async fn agent_space_member(
    state: &RuntimeState,
    jar: &CookieJar,
    agent_id: Uuid,
) -> Result<Uuid, ApiError> {
    let token = require_session_token(jar)?;
    let mut storage = state.storage.clone();
    let access = AuthorizeAgentAccess::execute(
        &mut storage,
        &token,
        MemberId::from_uuid(agent_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(access.member_id.into_uuid())
}

pub(super) async fn require_agent_governor(
    state: &RuntimeState,
    jar: &CookieJar,
    agent_id: Uuid,
) -> Result<Uuid, ApiError> {
    let token = require_session_token(jar)?;
    let mut storage = state.storage.clone();
    let access = AuthorizeAgentGovernance::execute(
        &mut storage,
        &token,
        MemberId::from_uuid(agent_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(access.member_id.into_uuid())
}

pub(super) async fn require_computer_governor(
    state: &RuntimeState,
    jar: &CookieJar,
    computer_id: Uuid,
) -> Result<Uuid, ApiError> {
    let token = require_session_token(jar)?;
    let mut storage = state.storage.clone();
    let access = AuthorizeComputerGovernance::execute(
        &mut storage,
        &token,
        ComputerId::from_uuid(computer_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(access.member_id.into_uuid())
}

pub(super) async fn agent_continuity(
    state: &RuntimeState,
    agent_id: Uuid,
    scope: SessionScope,
) -> SessionContinuityResponse {
    let computer_id =
        sqlx::query_scalar::<_, Option<Uuid>>("SELECT computer_id FROM agents WHERE member_id=$1")
            .bind(agent_id)
            .fetch_optional(&state.pool)
            .await
            .ok()
            .flatten()
            .flatten();
    let Some(computer_id) = computer_id else {
        return continuity_response(QueryResult::Unavailable {
            code: QueryErrorCode::Unreachable,
        });
    };
    let result = state
        .queries
        .ask(
            computer_id,
            ComputerQuery::SessionContinuity(SessionContinuityQuery {
                agent_id: AgentId::from_uuid(agent_id),
                scope,
            }),
        )
        .await;
    continuity_response(result)
}

pub(super) fn continuity_response(result: QueryResult) -> SessionContinuityResponse {
    match result {
        QueryResult::SessionContinuity(continuity) => SessionContinuityResponse {
            state: match continuity.state {
                SessionContinuityState::Warm => ContinuityStateCode::Warm,
                SessionContinuityState::Cold => ContinuityStateCode::Cold,
                SessionContinuityState::Lost => ContinuityStateCode::ResetRequired,
            },
            generation: continuity.generation,
            reason_code: continuity.reason_code,
        },
        QueryResult::Unavailable { code } => SessionContinuityResponse {
            state: ContinuityStateCode::Unavailable,
            generation: None,
            reason_code: Some(query_error_code(code).to_owned()),
        },
        _ => unavailable_continuity(),
    }
}

pub(super) fn unavailable_continuity() -> SessionContinuityResponse {
    SessionContinuityResponse {
        state: ContinuityStateCode::Unavailable,
        generation: None,
        reason_code: None,
    }
}

pub(super) fn query_error_code(code: QueryErrorCode) -> &'static str {
    match code {
        QueryErrorCode::UnknownAgent => "unknown_agent",
        QueryErrorCode::UnknownPath => "unknown_path",
        QueryErrorCode::SessionLost => "session_lost",
        QueryErrorCode::DriverUnavailable => "driver_unavailable",
        QueryErrorCode::Unreachable => "unreachable",
        QueryErrorCode::Internal => "internal",
    }
}

pub(super) async fn update_agent(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(agent_id): Path<Uuid>,
    Json(body): Json<UpdateAgentBody>,
) -> Result<Json<AgentResponse>, ApiError> {
    let actor_id = require_agent_governor(&state, &jar, agent_id).await?;
    let lifecycle = body.lifecycle.as_ref().map(lifecycle_action).transpose()?;
    let mut storage = state.storage.clone();
    UpdateAgent::execute(
        &mut storage,
        UpdateAgentInput {
            actor_id: MemberId::from_uuid(actor_id),
            agent_id: MemberId::from_uuid(agent_id),
            role_text: body.role_text.as_deref(),
            lifecycle,
        },
    )
    .await
    .map_err(application_error)?;
    read_agent_projection(&state, agent_id).await
}

pub(super) fn lifecycle_action(
    body: &LifecycleActionBody,
) -> Result<AgentLifecycleAction, ApiError> {
    match body.action.as_str() {
        "suspend" => Ok(AgentLifecycleAction::Suspend {
            cancel_current_run: body.mode.as_deref() == Some("cancel_now"),
        }),
        "resume" => Ok(AgentLifecycleAction::Resume),
        "retry" => Ok(AgentLifecycleAction::RetryProvisioning),
        _ => Err(ApiError::invalid(
            "lifecycle action must be suspend, resume, or retry",
        )),
    }
}

pub(super) async fn read_agent_projection(
    state: &RuntimeState,
    agent_id: Uuid,
) -> Result<Json<AgentResponse>, ApiError> {
    let row = sqlx::query(&format!(
        "SELECT a.*,m.display_name,m.access_level,c.connection_status,\
         c.deleted_at AS computer_deleted_at,{ACTIVITY_COLUMNS} \
         FROM agents a JOIN members m ON m.id=a.member_id \
         LEFT JOIN computers c ON c.id=a.computer_id {ACTIVITY_JOINS} WHERE a.member_id=$1"
    ))
    .bind(agent_id)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    let mut agent = agent_row(&row)?;
    agent.memory_files = memory_files(state, row.get("computer_id"), agent_id).await;
    Ok(Json(agent))
}

pub(super) async fn memory_files(
    state: &RuntimeState,
    computer_id: Option<Uuid>,
    agent_id: Uuid,
) -> Vec<MemoryFileResponse> {
    let Some(computer_id) = computer_id else {
        return Vec::new();
    };
    let result = state
        .queries
        .ask(
            computer_id,
            ComputerQuery::MemoryList(MemoryQuery {
                agent_id: AgentId::from_uuid(agent_id),
            }),
        )
        .await;
    match result {
        QueryResult::MemoryList(list) => list
            .files
            .iter()
            .map(|file| MemoryFileResponse {
                path: file.path.clone(),
                size: file.size,
                sha256: file.sha256.clone(),
                updated_at: timestamp(file.updated_at),
            })
            .collect(),
        _ => Vec::new(),
    }
}

pub(super) async fn read_agent_memory(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(agent_id): Path<Uuid>,
    Json(body): Json<ReadMemoryBody>,
) -> Result<Response, ApiError> {
    require_agent_governor(&state, &jar, agent_id).await?;
    let computer_id =
        sqlx::query_scalar::<_, Option<Uuid>>("SELECT computer_id FROM agents WHERE member_id=$1")
            .bind(agent_id)
            .fetch_optional(&state.pool)
            .await
            .map_err(map_sqlx)?
            .flatten()
            .ok_or_else(ApiError::computer_unreachable)?;
    let result = state
        .queries
        .ask(
            computer_id,
            ComputerQuery::MemoryRead(MemoryReadQuery {
                agent_id: AgentId::from_uuid(agent_id),
                path: body.path,
            }),
        )
        .await;
    let QueryResult::MemoryRead(read) = result else {
        return Err(match result {
            QueryResult::Unavailable {
                code: QueryErrorCode::UnknownPath,
            } => ApiError::not_found(),
            _ => ApiError::computer_unreachable(),
        });
    };
    let mut response = Json(MemoryContentResponse {
        path: read.file.path,
        size: read.file.size,
        sha256: read.file.sha256,
        updated_at: timestamp(read.file.updated_at),
        content: read.content,
    })
    .into_response();
    response
        .headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    Ok(response)
}
