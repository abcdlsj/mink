use super::*;
use crate::server::domain::task::TaskStatus as DomainTaskStatus;
use serde::Serialize;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct CreateTaskRequest {
    title: Option<String>,
    assignee_agent_member_id: Option<Uuid>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct UpdateTaskBody {
    title: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct StartTaskBody {
    assignee_agent_member_id: Uuid,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct LinkThreadBody {
    thread_id: Uuid,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct CompleteTaskBody {
    result_markdown: String,
    result_thread_id: Uuid,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct CloseTaskBody {
    reason: String,
    note: Option<String>,
}

pub(super) async fn list_tasks(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<TaskResponse>>, ApiError> {
    current_member(&state, &jar, space_id).await?;
    let ids = sqlx::query_scalar::<_, Uuid>(
        "SELECT id FROM tasks WHERE space_id=$1 ORDER BY updated_at DESC,id DESC",
    )
    .bind(space_id)
    .fetch_all(&state.pool)
    .await
    .map_err(map_sqlx)?;
    let mut tasks = Vec::with_capacity(ids.len());
    for id in ids {
        tasks.push(task_projection(&state.pool, id).await?);
    }
    Ok(Json(tasks))
}

pub(super) async fn get_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(task_id): Path<Uuid>,
) -> Result<Json<TaskResponse>, ApiError> {
    let space_id = sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM tasks WHERE id=$1")
        .bind(task_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    current_member(&state, &jar, space_id).await?;
    Ok(Json(task_detail(&state, task_id).await?))
}

pub(super) async fn task_runs(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(task_id): Path<Uuid>,
) -> Result<Json<Vec<RunResponse>>, ApiError> {
    task_actor(&state, &jar, task_id).await?;
    let ids = sqlx::query_scalar::<_, Uuid>(
        "SELECT id FROM agent_runs WHERE task_id=$1 ORDER BY created_at DESC,id DESC",
    )
    .bind(task_id)
    .fetch_all(&state.pool)
    .await
    .map_err(map_sqlx)?;
    let mut runs = Vec::with_capacity(ids.len());
    for id in ids {
        runs.push(run_projection(&state.pool, id).await?);
    }
    Ok(Json(runs))
}

pub(super) async fn create_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(message_id): Path<Uuid>,
    Json(body): Json<CreateTaskRequest>,
) -> Result<(StatusCode, Json<TaskResponse>), ApiError> {
    let source = sqlx::query(
        "SELECT m.space_id,m.thread_id,m.body_markdown FROM messages m WHERE m.id=$1 AND m.placement='root'",
    )
    .bind(message_id)
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::not_found)?;
    let actor = current_member(&state, &jar, source.get("space_id")).await?;
    let title = body
        .title
        .filter(|title| !title.trim().is_empty())
        .unwrap_or_else(|| {
            source
                .get::<String, _>("body_markdown")
                .chars()
                .take(120)
                .collect()
        });
    let context = super::write_context(
        super::AuthenticationSurface::Browser,
        super::AuthenticationSurface::Browser,
        headers
            .get("Idempotency-Key")
            .and_then(|value| value.to_str().ok()),
    )
    .map_err(|_| ApiError::invalid("Idempotency-Key must be a UUID"))?;
    let task = create_task_from_root(
        &state.storage,
        super::BrowserPrincipal {
            member_id: MemberId::from_uuid(actor),
        },
        MessageId::from_uuid(message_id),
        context,
        super::CreateTaskBody {
            title,
            assignee_agent_member_id: body.assignee_agent_member_id.map(MemberId::from_uuid),
        },
    )
    .await
    .map_err(|_| ApiError {
        status: StatusCode::CONFLICT,
        code: "conflict",
        message: "Task could not be created from this Root Message",
    })?;
    Ok((
        StatusCode::CREATED,
        Json(task_detail(&state, task.0.id.into_uuid()).await?),
    ))
}

pub(super) async fn link_task_thread(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
    Json(body): Json<LinkThreadBody>,
) -> Result<Json<TaskResponse>, ApiError> {
    let actor = task_actor(&state, &jar, task_id).await?;
    let mut storage = state.storage.clone();
    LinkThreadToTask::execute(
        &mut storage,
        LinkThreadInput {
            task_id: TaskId::from_uuid(task_id),
            target_thread_id: ThreadId::from_uuid(body.thread_id),
            actor_member_id: MemberId::from_uuid(actor),
            idempotency_key: IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(task_detail(&state, task_id).await?))
}

pub(super) async fn unlink_task_thread(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path((task_id, thread_id)): Path<(Uuid, Uuid)>,
) -> Result<Json<TaskResponse>, ApiError> {
    let actor = task_actor(&state, &jar, task_id).await?;
    let mut storage = state.storage.clone();
    UnlinkThreadFromTask::execute(
        &mut storage,
        LinkThreadInput {
            task_id: TaskId::from_uuid(task_id),
            target_thread_id: ThreadId::from_uuid(thread_id),
            actor_member_id: MemberId::from_uuid(actor),
            idempotency_key: IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(task_detail(&state, task_id).await?))
}

pub(super) async fn update_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
    Json(body): Json<UpdateTaskBody>,
) -> Result<Json<TaskResponse>, ApiError> {
    if body.title.trim().is_empty() {
        return Err(ApiError::invalid("Task title is required"));
    }
    update_task_action(
        &state,
        &jar,
        &headers,
        task_id,
        TaskAction::Rename { title: body.title },
    )
    .await
}

pub(super) async fn start_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
    Json(body): Json<StartTaskBody>,
) -> Result<Json<TaskResponse>, ApiError> {
    update_task_action(
        &state,
        &jar,
        &headers,
        task_id,
        TaskAction::Start {
            assignee: MemberId::from_uuid(body.assignee_agent_member_id),
        },
    )
    .await
}

pub(super) async fn submit_task_review(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
) -> Result<Json<TaskResponse>, ApiError> {
    record_task_outcome(
        &state,
        &jar,
        &headers,
        task_id,
        TaskOutcome::SubmitReview { message: None },
    )
    .await
}

pub(super) async fn request_task_changes(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
) -> Result<Json<TaskResponse>, ApiError> {
    update_task_action(&state, &jar, &headers, task_id, TaskAction::RequestChanges).await
}

pub(super) async fn close_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
    Json(body): Json<CloseTaskBody>,
) -> Result<Json<TaskResponse>, ApiError> {
    let reason = match body.reason.as_str() {
        "invalid" => CloseReason::Invalid,
        "duplicate" => CloseReason::Duplicate,
        "not_needed" => CloseReason::NotNeeded,
        "obsolete" => CloseReason::Obsolete,
        "other" => CloseReason::Other,
        _ => return Err(ApiError::invalid("close reason is invalid")),
    };
    record_task_outcome(
        &state,
        &jar,
        &headers,
        task_id,
        TaskOutcome::Close {
            reason,
            note: body.note,
        },
    )
    .await
}

pub(super) async fn reset_task_session(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
) -> Result<Json<TaskResponse>, ApiError> {
    update_task_action(&state, &jar, &headers, task_id, TaskAction::ResetSession).await
}

pub(super) async fn complete_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
    Json(body): Json<CompleteTaskBody>,
) -> Result<Json<TaskResponse>, ApiError> {
    record_task_outcome(
        &state,
        &jar,
        &headers,
        task_id,
        TaskOutcome::Done {
            result: OutcomeMessage {
                message_id: MessageId::from_uuid(Uuid::now_v7()),
                body_markdown: body.result_markdown,
                post_to: TaskPostTarget::Thread(ThreadId::from_uuid(body.result_thread_id)),
            },
        },
    )
    .await
}

pub(super) async fn record_task_outcome(
    state: &RuntimeState,
    jar: &CookieJar,
    headers: &HeaderMap,
    task_id: Uuid,
    outcome: TaskOutcome,
) -> Result<Json<TaskResponse>, ApiError> {
    let actor = task_actor(state, jar, task_id).await?;
    let mut storage = state.storage.clone();
    RecordTaskOutcome::execute(
        &mut storage,
        RecordTaskOutcomeInput {
            scope: TaskOutcomeScope::Browser {
                task_id: TaskId::from_uuid(task_id),
            },
            actor_member_id: MemberId::from_uuid(actor),
            idempotency_key: IdempotencyKey::from_uuid(idempotency_header(headers)?),
            outcome,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(task_detail(state, task_id).await?))
}

pub(super) async fn update_task_action(
    state: &RuntimeState,
    jar: &CookieJar,
    headers: &HeaderMap,
    task_id: Uuid,
    action: TaskAction,
) -> Result<Json<TaskResponse>, ApiError> {
    let actor = task_actor(state, jar, task_id).await?;
    let mut storage = state.storage.clone();
    UpdateTask::execute(
        &mut storage,
        UpdateTaskInput {
            task_id: TaskId::from_uuid(task_id),
            actor_member_id: MemberId::from_uuid(actor),
            idempotency_key: IdempotencyKey::from_uuid(idempotency_header(headers)?),
            action,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(task_detail(state, task_id).await?))
}

pub(super) async fn task_actor(
    state: &RuntimeState,
    jar: &CookieJar,
    task_id: Uuid,
) -> Result<Uuid, ApiError> {
    let space_id = sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM tasks WHERE id=$1")
        .bind(task_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    let actor = current_member(state, jar, space_id).await?;
    let can_read: bool = sqlx::query_scalar(
        "SELECT EXISTS(\
           SELECT 1 FROM tasks task \
           JOIN threads source ON source.id=task.source_thread_id \
           JOIN channel_members membership ON membership.channel_id=source.channel_id \
           WHERE task.id=$1 AND membership.member_id=$2\
         )",
    )
    .bind(task_id)
    .bind(actor)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    if !can_read {
        return Err(ApiError::not_found());
    }
    Ok(actor)
}

pub(super) async fn task_detail(
    state: &RuntimeState,
    task_id: Uuid,
) -> Result<TaskResponse, ApiError> {
    let mut task = task_projection(&state.pool, task_id).await?;
    if let Some(agent_id) = task.assignee_agent_member_id {
        task.session_continuity = agent_continuity(
            state,
            agent_id,
            SessionScope::Task(TaskId::from_uuid(task_id)),
        )
        .await;
    }
    Ok(task)
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(super) enum AuthenticationSurface {
    Browser,
    #[cfg(test)]
    Computer,
    #[cfg(test)]
    Agent,
}

#[derive(Debug, Eq, PartialEq)]
pub(super) struct WriteContext {
    pub(super) authentication: AuthenticationSurface,
    pub(super) idempotency_key: IdempotencyKey,
}

#[derive(Clone, Copy)]
pub(super) struct BrowserPrincipal {
    pub(super) member_id: MemberId,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct CreateTaskBody {
    pub(super) title: String,
    pub(super) assignee_agent_member_id: Option<MemberId>,
}

#[derive(Serialize)]
pub(super) struct CreatedTask {
    pub(super) id: TaskId,
    source_thread_id: ThreadId,
    status: &'static str,
}

pub(super) fn write_context(
    expected: AuthenticationSurface,
    authenticated_as: AuthenticationSurface,
    idempotency_key: Option<&str>,
) -> Result<WriteContext, HttpError> {
    if expected != authenticated_as {
        return Err(ApplicationError::PermissionDenied.into());
    }
    let key = idempotency_key
        .ok_or(ApplicationError::Conflict)?
        .parse()
        .map_err(|_| ApplicationError::Conflict)?;
    Ok(WriteContext {
        authentication: authenticated_as,
        idempotency_key: key,
    })
}

pub(super) async fn create_task_from_root<P: TransactionPort + Clone>(
    port: &P,
    principal: BrowserPrincipal,
    root_message_id: MessageId,
    context: WriteContext,
    body: CreateTaskBody,
) -> Result<Json<CreatedTask>, HttpError> {
    if context.authentication != AuthenticationSurface::Browser {
        return Err(ApplicationError::PermissionDenied.into());
    }
    let mut port = port.clone();
    let task = CreateTaskFromRootMessage::execute(
        &mut port,
        CreateTaskInput {
            task_id: TaskId::from_uuid(Uuid::now_v7()),
            actor_member_id: principal.member_id,
            source: TaskSource::HumanRoot(ThreadId::from_uuid(root_message_id.into_uuid())),
            title: body.title,
            assignee_agent_member_id: body.assignee_agent_member_id,
            idempotency_key: context.idempotency_key,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await?;
    let task = task.view();
    let status = match task.status {
        DomainTaskStatus::Todo => "todo",
        DomainTaskStatus::InProgress => "in_progress",
        DomainTaskStatus::InReview => "in_review",
        DomainTaskStatus::Done => "done",
        DomainTaskStatus::Closed => "closed",
    };
    Ok(Json(CreatedTask {
        id: task.id,
        source_thread_id: task.source_thread_id,
        status,
    }))
}
use crate::server::application::ports::{
    AttachmentTransaction, CollaborationTransaction, EffectSink, ExecutionTransaction,
    IdentityTransaction, TaskTransaction,
};
