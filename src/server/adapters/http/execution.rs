use super::*;
use crate::protocol::computer::{ComputerErrorCode, DeliveryOutcome, DeliveryReceipt, RunStarted};
use crate::server::domain::{
    attention::InboxItemDisposition,
    execution::{RunErrorCode, RunOutcome},
    identity::valid_display_name,
};

fn channel_slug(name: &str, id: Uuid) -> String {
    let base = name
        .chars()
        .filter(|character| character.is_ascii_alphanumeric())
        .collect::<String>()
        .to_lowercase();
    format!(
        "{}-{}",
        if base.is_empty() { "channel" } else { &base },
        &id.simple().to_string()[24..]
    )
}

/// Resolve `@display_name` tokens in an Agent message body against the target
/// Channel Members. The Server never parses message bodies for consumers, but
/// the Agent CLI sends plain Markdown, so the Server resolves mentions from the
/// body at the single write entry point.
async fn agent_mention_ids(
    pool: &PgPool,
    channel_id: Uuid,
    body: &str,
) -> Result<Vec<Uuid>, ApiError> {
    let members: Vec<(Uuid, String)> = sqlx::query_as(
        "SELECT m.id, m.display_name FROM channel_members cm \
         JOIN members m ON m.id = cm.member_id \
         WHERE cm.channel_id = $1 AND m.retired_at IS NULL",
    )
    .bind(channel_id)
    .fetch_all(pool)
    .await
    .map_err(map_sqlx)?;
    let names: Vec<(String, Uuid)> = members
        .iter()
        .map(|(id, name)| (name.to_lowercase(), *id))
        .collect();
    let chars: Vec<(usize, char)> = body.char_indices().collect();
    let mut ids: Vec<Uuid> = Vec::new();
    let mut index = 0;
    while index < chars.len() {
        let (offset, character) = chars[index];
        if character == '@' && (index == 0 || chars[index - 1].1.is_whitespace()) {
            let mut end = index + 1;
            while end < chars.len() && (chars[end].1.is_alphabetic() || chars[end].1 == '_') {
                end += 1;
            }
            let name_end = if end < chars.len() {
                chars[end].0
            } else {
                body.len()
            };
            let candidate = &body[offset + 1..name_end].to_lowercase();
            if let Some((_, id)) = names.iter().find(|(name, _)| name == candidate)
                && !ids.contains(id)
            {
                ids.push(*id);
            }
            index = end;
        } else {
            index += 1;
        }
    }
    Ok(ids)
}

pub(super) async fn run_started(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, run_id)): Path<(Uuid, Uuid)>,
    Json(started): Json<RunStarted>,
) -> Result<StatusCode, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    if started.run_id.into_uuid() != run_id {
        return Err(ApiError::invalid("Run ID does not match the request path"));
    }
    let mut storage = state.storage.clone();
    StartRun::execute(
        &mut storage,
        StartRunInput {
            run_id: started.run_id,
            computer_id: ComputerId::from_uuid(computer_id),
            now: started.observed_at,
        },
    )
    .await
    .map_err(application_error)?;
    Ok(StatusCode::OK)
}

pub(super) async fn delivery_receipt(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, run_id)): Path<(Uuid, Uuid)>,
    Json(receipt): Json<DeliveryReceipt>,
) -> Result<StatusCode, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    if receipt.run_id.into_uuid() != run_id {
        return Err(ApiError::invalid("Run ID does not match the request path"));
    }
    let mut storage = state.storage.clone();
    AcknowledgeDelivery::execute(
        &mut storage,
        AcknowledgeDeliveryInput {
            run_id: receipt.run_id,
            computer_id: ComputerId::from_uuid(computer_id),
            delivery_sequence: receipt.delivery_sequence.0,
            accepted: matches!(receipt.outcome, DeliveryOutcome::Accepted),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(StatusCode::OK)
}

pub(super) async fn apply_run_result(
    state: &RuntimeState,
    computer_id: Uuid,
    result: RunResult,
) -> Result<(), ApiError> {
    let error_code = result.error_code.map(super::run_error_code);
    let outcome = match result.status {
        RunTerminalStatus::Completed => RunOutcome::Completed,
        RunTerminalStatus::Yielded => RunOutcome::Yielded,
        RunTerminalStatus::Failed => RunOutcome::Failed,
        RunTerminalStatus::Canceled => RunOutcome::Canceled,
    };
    let item_dispositions = result
        .item_outcomes
        .into_iter()
        .map(|item| ItemDispositionInput {
            item_id: item.item_id,
            disposition: match item.disposition {
                ItemDisposition::Handled => InboxItemDisposition::Handled,
                ItemDisposition::Deferred => InboxItemDisposition::Deferred,
                ItemDisposition::Released => InboxItemDisposition::Released,
            },
        })
        .collect();
    let mut storage = state.storage.clone();
    let run = CompleteRun::execute(
        &mut storage,
        CompleteRunInput {
            event_id: result.event_id,
            run_id: result.run_id,
            computer_id: ComputerId::from_uuid(computer_id),
            outcome,
            error_code,
            item_dispositions,
            continuation_note: result.continuation_note,
            max_retry_count: AttentionPolicy::MAX_RETRY_COUNT,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    if result.status == RunTerminalStatus::Yielded {
        let run_view = run.view();
        let focus = activity_thread_reference(&state.pool, run_view.focus_thread_id).await;
        let arguments = focus
            .as_ref()
            .map(|reference| vec![("focus", reference.label.clone())])
            .unwrap_or_default();
        record_agent_activity(
            state,
            run_view.space_id.into_uuid(),
            run_view.agent_id.into_uuid(),
            "run.yield",
            agent_activity_details(
                json!({
                    "run_id": run_view.id,
                    "thread_id": run_view.focus_thread_id,
                    "channel_id": focus.as_ref().map(|reference| reference.channel_id),
                    "scope_channel_id": focus.as_ref().map(|reference| reference.channel_id),
                }),
                arguments,
                run_view.continuation_note.map(agent_activity_preview),
            ),
        )
        .await;
    }
    Ok(())
}

pub(super) async fn run_result(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, run_id)): Path<(Uuid, Uuid)>,
    Json(result): Json<RunResult>,
) -> Result<StatusCode, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    if result.run_id.into_uuid() != run_id {
        return Err(ApiError::invalid("Run ID does not match the request path"));
    }
    apply_run_result(&state, computer_id, result).await?;
    Ok(StatusCode::OK)
}

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub(crate) struct DispatchOutcome {
    pub(crate) dispatched: u32,
    pub(crate) failed: u32,
}

/// Dispatches one Run per Agent that has available work and no live Run.
///
/// Each candidate gets its own transaction, so one Agent whose dispatch fails does not stop the rest.
/// A failed dispatch leaves the Item pending; it carries no deadline, so the next pass retries it.
pub(crate) async fn dispatch_available_work(
    state: &RuntimeState,
    now: OffsetDateTime,
    limit: u32,
) -> Result<DispatchOutcome, crate::server::application::ports::ApplicationError> {
    let mut storage = state.storage.clone();
    let candidates = FindDispatchableWork::candidates(&mut storage, now, limit).await?;
    let mut outcome = DispatchOutcome::default();
    for candidate in candidates {
        let mut storage = state.storage.clone();
        let result = DispatchRun::execute(
            &mut storage,
            DispatchRunInput {
                run_id: RunId::from_uuid(Uuid::now_v7()),
                agent_id: candidate.agent_id,
                task_id: candidate.task_id,
                focus_thread_id: candidate.thread_id,
                trigger: candidate.trigger,
                item_ids: vec![candidate.item_id],
            },
        )
        .await;
        match result {
            Ok(_) => outcome.dispatched += 1,
            Err(error) => {
                outcome.failed += 1;
                let error_code = run_dispatch_error_code(&error);
                let mut storage = state.storage.clone();
                let changed = FindDispatchableWork::record_failure(
                    &mut storage,
                    candidate.item_id,
                    candidate.message_id,
                    candidate.channel_id,
                    error_code,
                )
                .await?;
                if changed {
                    tracing::warn!(
                        computer_id = %candidate.computer_id.into_uuid(),
                        item_id = %candidate.item_id.into_uuid(),
                        agent_id = %candidate.agent_id.into_uuid(),
                        error_code,
                        "Run dispatch failed; the Inbox Item remains pending"
                    );
                }
            }
        }
    }
    Ok(outcome)
}

/// Records a Human's request to stop a Run. Returns immediately: the Run stays live until the Computer
/// reports that the Driver actually stopped, because only the Computer can know that.
pub(super) async fn cancel_run(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path((agent_id, run_id)): Path<(Uuid, Uuid)>,
) -> Result<StatusCode, ApiError> {
    let viewer_id = agent_space_member(&state, &jar, agent_id).await?;
    let mut storage = state.storage.clone();
    RequestRunCancel::execute(
        &mut storage,
        RequestRunCancelInput {
            run_id: RunId::from_uuid(run_id),
            requested_by: MemberId::from_uuid(viewer_id),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(StatusCode::ACCEPTED)
}

pub(super) fn run_dispatch_error_code(
    error: &crate::server::application::ports::ApplicationError,
) -> &'static str {
    use crate::server::application::ports::ApplicationError;
    match error {
        ApplicationError::NotFound => "run_dispatch_not_found",
        ApplicationError::Unauthenticated => "run_dispatch_unauthenticated",
        ApplicationError::PayloadTooLarge => "run_dispatch_payload_too_large",
        ApplicationError::PermissionDenied => "run_dispatch_permission_denied",
        ApplicationError::Conflict | ApplicationError::Domain(_) => "run_dispatch_conflict",
        ApplicationError::ContextChanged => "run_dispatch_context_changed",
        ApplicationError::Unavailable => "run_dispatch_unavailable",
        ApplicationError::Internal => "run_dispatch_internal",
    }
}

pub(super) async fn agent_action(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
    Json(request): Json<AgentActionRequest>,
) -> Json<capability::Response<Value>> {
    match execute_agent_action(&state, &headers, computer_id, request).await {
        Ok(value) => Json(capability::Response::success(value)),
        Err(error) => Json(capability::Response::failure(error)),
    }
}

pub(super) async fn execute_agent_action(
    state: &RuntimeState,
    headers: &HeaderMap,
    computer_id: Uuid,
    request: AgentActionRequest,
) -> Result<Value, capability::Error> {
    let raw = bearer_token(headers).map_err(|_| {
        capability_error(
            capability::ErrorCode::Unauthenticated,
            "Computer authentication failed",
            false,
        )
    })?;
    let authenticated:bool=sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM computers WHERE id=$1 AND token_hash=$2 AND deleted_at IS NULL)").bind(computer_id).bind(token_hash(raw)).fetch_one(&state.pool).await.map_err(|_|capability_error(capability::ErrorCode::Internal,"Computer authentication failed",false))?;
    if !authenticated {
        return Err(capability_error(
            capability::ErrorCode::Unauthenticated,
            "Computer authentication failed",
            false,
        ));
    }
    let context = &request.context;
    if let Some(key) = request.idempotency_key
        && matches!(
            &request.action,
            capability::Action::TaskSubmitReview { .. }
                | capability::Action::TaskDone { .. }
                | capability::Action::TaskClose { .. }
        )
    {
        let replayed = sqlx::query_scalar::<_, Uuid>(
            "SELECT records.resource_id FROM idempotency_records records \
             JOIN tasks ON tasks.id=records.resource_id \
             JOIN agents ON agents.member_id=records.actor_member_id \
             WHERE records.actor_member_id=$1 AND records.action=$2 \
             AND records.idempotency_key=$3 AND tasks.id=$4 AND tasks.space_id=$5 \
             AND agents.computer_id=$6",
        )
        .bind(context.agent_id.into_uuid())
        .bind(request.action.name())
        .bind(key.into_uuid())
        .bind(context.task_id.map(TaskId::into_uuid))
        .bind(context.space_id.into_uuid())
        .bind(computer_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(|_| {
            capability_error(
                capability::ErrorCode::Internal,
                "Task action replay could not be checked",
                false,
            )
        })?;
        if let Some(task_id) = replayed {
            return capability_value(
                &task_projection(&state.pool, task_id)
                    .await
                    .map_err(api_to_capability)?,
            );
        }
    }
    if let (Some(key), capability::Action::ChannelLeave { channel_id }) =
        (request.idempotency_key, &request.action)
    {
        let replayed = sqlx::query_scalar::<_, Uuid>(
            "SELECT records.resource_id FROM idempotency_records records \
             JOIN agents ON agents.member_id=records.actor_member_id \
             JOIN agent_runs runs ON runs.id=$5 \
             WHERE records.actor_member_id=$1 AND records.action='channel.leave' \
             AND records.idempotency_key=$2 AND agents.space_id=$3 AND agents.computer_id=$4 \
             AND runs.agent_id=records.actor_member_id AND runs.space_id=$3 \
             AND runs.task_id IS NOT DISTINCT FROM $6 AND runs.focus_thread_id=$7 \
             AND runs.status='working'",
        )
        .bind(context.agent_id.into_uuid())
        .bind(key.into_uuid())
        .bind(context.space_id.into_uuid())
        .bind(computer_id)
        .bind(context.run_id.into_uuid())
        .bind(context.task_id.map(TaskId::into_uuid))
        .bind(context.focus_thread_id.into_uuid())
        .fetch_optional(&state.pool)
        .await
        .map_err(|_| {
            capability_error(
                capability::ErrorCode::Internal,
                "Channel leave replay could not be checked",
                false,
            )
        })?;
        if let Some(replayed_channel_id) = replayed {
            if replayed_channel_id != channel_id.into_uuid() {
                return Err(capability_error(
                    capability::ErrorCode::Conflict,
                    "Idempotency key belongs to another Channel",
                    false,
                ));
            }
            return Ok(json!({
                "channel_id": replayed_channel_id,
                "member_id": context.agent_id,
            }));
        }
    }
    let mut storage = state.storage.clone();
    let valid = AuthorizeRunCapability::execute(
        &mut storage,
        RunCapabilityProof {
            computer_id: ComputerId::from_uuid(computer_id),
            run_id: context.run_id,
            agent_id: MemberId::from_uuid(context.agent_id.into_uuid()),
            space_id: context.space_id,
            task_id: context.task_id,
            focus_thread_id: context.focus_thread_id,
        },
    )
    .await
    .map_err(app_to_capability)?;
    if !valid {
        return Err(capability_error(
            capability::ErrorCode::ContextChanged,
            "Run context is no longer active",
            false,
        ));
    }
    match request.action {
        capability::Action::Discover { operation } => {
            discover_operation(state, context, &operation).await
        }
        capability::Action::ContextCurrent => {
            let task = match context.task_id {
                Some(id) => Some(
                    task_projection(&state.pool, id.into_uuid())
                        .await
                        .map_err(api_to_capability)?,
                ),
                None => None,
            };
            let items=sqlx::query("SELECT i.id,i.kind,i.strength,i.status,i.available_at FROM run_items ri JOIN inbox_items i ON i.id=ri.inbox_item_id WHERE ri.run_id=$1 ORDER BY ri.delivery_seq").bind(context.run_id.into_uuid()).fetch_all(&state.pool).await.map_err(|_|capability_error(capability::ErrorCode::Internal,"Run Items could not be read",false))?;
            let scope = match context.task_id {
                Some(task_id) => SessionScope::Task(task_id),
                None => SessionScope::Thread(context.focus_thread_id),
            };
            let continuity = agent_continuity(state, context.agent_id.into_uuid(), scope).await;
            Ok(
                json!({"agent":{"id":context.agent_id,"space_id":context.space_id},"task":task,"focus_thread_id":context.focus_thread_id,"run":{"id":context.run_id,"message_snapshot_sequence":context.message_snapshot_sequence},"dispatched_items":items.iter().map(|row|json!({"id":row.get::<Uuid,_>("id"),"kind":row.get::<String,_>("kind"),"strength":row.get::<String,_>("strength"),"status":row.get::<String,_>("status"),"available_at":timestamp(row.get("available_at"))})).collect::<Vec<_>>(),"session_continuity":continuity}),
            )
        }
        capability::Action::MessageRead(page) => {
            agent_read_thread(state, context.focus_thread_id.into_uuid(), page).await
        }
        capability::Action::ThreadRead { thread_id, page } => {
            agent_read_thread(state, thread_id.into_uuid(), page).await
        }
        capability::Action::ChannelRead {
            channel_id,
            around_message_id,
            limit,
        } => {
            if limit == 0 || limit > 100 {
                return Err(capability_error(
                    capability::ErrorCode::InvalidArgument,
                    "limit must be between 1 and 100",
                    false,
                ));
            }
            agent_read_channel(
                state,
                context.agent_id.into_uuid(),
                channel_id.into_uuid(),
                around_message_id.map(MessageId::into_uuid),
                limit,
            )
            .await
        }
        capability::Action::MessageSend(send) => {
            let target = activity_message_target(state, context, &send.target).await;
            let message_preview = agent_activity_preview(&send.body);
            let attachment_count = send.attachment_ids.len();
            let handles_item = send.handle_item_id.is_some();
            let expected_snapshot = send.snapshot_sequence.or_else(|| {
                matches!(&send.target, capability::MessageTarget::Focus)
                    .then_some(context.message_snapshot_sequence)
            });
            let (channel_id, thread_id) = match send.target {
                capability::MessageTarget::Focus => {
                    let mut storage = state.storage.clone();
                    storage
                        .transact(async |transaction| {
                            transaction
                                .channel_for_thread(context.focus_thread_id)
                                .await
                        })
                        .await
                        .map_err(app_to_capability)?
                        .map(|channel| {
                            (
                                channel.into_uuid(),
                                Some(context.focus_thread_id.into_uuid()),
                            )
                        })
                        .ok_or_else(|| {
                            capability_error(
                                capability::ErrorCode::NotFound,
                                "Focus Thread was not found",
                                false,
                            )
                        })?
                }
                capability::MessageTarget::Thread(thread_id) => {
                    let mut storage = state.storage.clone();
                    storage
                        .transact(async |transaction| {
                            transaction.channel_for_thread(thread_id).await
                        })
                        .await
                        .map_err(app_to_capability)?
                        .map(|channel| (channel.into_uuid(), Some(thread_id.into_uuid())))
                        .ok_or_else(|| {
                            capability_error(
                                capability::ErrorCode::NotFound,
                                "Thread was not found",
                                false,
                            )
                        })?
                }
                capability::MessageTarget::Channel(channel_id) => (channel_id.into_uuid(), None),
                capability::MessageTarget::Member(member_id) => {
                    let opened = OpenDirectMessage::execute(
                        &mut storage,
                        OpenDirectMessageInput {
                            channel_id: ChannelId::from_uuid(Uuid::now_v7()),
                            space_id: context.space_id,
                            actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
                            other_member_id: member_id,
                            now: time::OffsetDateTime::now_utc(),
                        },
                    )
                    .await
                    .map_err(app_to_capability)?;
                    (opened.view.channel_id.into_uuid(), None)
                }
            };
            let mentions = agent_mention_ids(&state.pool, channel_id, &send.body)
                .await
                .map_err(api_to_capability)?;
            let message_id = insert_message(
                state,
                channel_id,
                context.agent_id.into_uuid(),
                MessageWriteContext {
                    idempotency_key: request.idempotency_key.ok_or_else(|| {
                        capability_error(
                            capability::ErrorCode::InvalidArgument,
                            "Idempotency key is required",
                            false,
                        )
                    })?,
                    thread_id,
                    handled_item: send
                        .handle_item_id
                        .map(|item_id| (context.run_id.into_uuid(), item_id.into_uuid())),
                    expected_snapshot,
                },
                CreateMessageBody {
                    body_markdown: send.body,
                    mentions,
                    mention_all: false,
                    attachment_ids: send
                        .attachment_ids
                        .into_iter()
                        .map(AttachmentId::into_uuid)
                        .collect(),
                    reply_to_message_id: None,
                },
            )
            .await
            .map_err(api_to_capability)?;
            record_agent_activity(
                state,
                context.space_id.into_uuid(),
                context.agent_id.into_uuid(),
                "message.send",
                agent_activity_details(
                    json!({
                        "run_id": context.run_id,
                        "channel_id": channel_id,
                        "thread_id": thread_id.unwrap_or(message_id),
                        "message_id": message_id,
                        "scope_channel_id": channel_id,
                    }),
                    vec![
                        ("target", target),
                        ("attachment_count", attachment_count.to_string()),
                        ("handle_item", handles_item.to_string()),
                    ],
                    Some(message_preview),
                ),
            )
            .await;
            Ok(
                json!({"message_id":message_id,"thread_id":thread_id.unwrap_or(message_id),"channel_id":channel_id}),
            )
        }
        capability::Action::TaskCreate { title, assignee } => {
            let key = request.idempotency_key.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::InvalidArgument,
                    "idempotency key is required",
                    false,
                )
            })?;
            let default_title: String =
                sqlx::query_scalar("SELECT body_markdown FROM messages WHERE id=$1")
                    .bind(context.focus_thread_id.into_uuid())
                    .fetch_one(&state.pool)
                    .await
                    .map_err(|_| {
                        capability_error(
                            capability::ErrorCode::NotFound,
                            "Focus Root Message was not found",
                            false,
                        )
                    })?;
            let title = title.unwrap_or_else(|| default_title.chars().take(120).collect());
            let assignee_label = match assignee.as_ref() {
                Some(member_id) => activity_member_label(state, *member_id)
                    .await
                    .unwrap_or_else(|| "Assigned Agent".to_owned()),
                None => "Unassigned".to_owned(),
            };
            let focus = activity_thread_reference(&state.pool, context.focus_thread_id).await;
            let mut storage = state.storage.clone();
            let task = CreateTaskFromRootMessage::execute(
                &mut storage,
                CreateTaskInput {
                    task_id: TaskId::from_uuid(Uuid::now_v7()),
                    actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
                    source: TaskSource::AgentRun(context.run_id),
                    title: title.clone(),
                    assignee_agent_member_id: assignee,
                    idempotency_key: key,
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            let task_id = task.view().id;
            record_agent_activity(
                state,
                context.space_id.into_uuid(),
                context.agent_id.into_uuid(),
                "task.create",
                agent_activity_details(
                    json!({
                        "run_id": context.run_id,
                        "task_id": task_id,
                        "thread_id": context.focus_thread_id,
                        "scope_channel_id": focus.as_ref().map(|reference| reference.channel_id),
                    }),
                    vec![("title", title), ("assignee", assignee_label)],
                    None,
                ),
            )
            .await;
            capability_value(
                &task_projection(&state.pool, task_id.into_uuid())
                    .await
                    .map_err(api_to_capability)?,
            )
        }
        capability::Action::TaskLinkThread { thread_id } => {
            let target = activity_thread_reference(&state.pool, thread_id).await;
            let key = request.idempotency_key.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::InvalidArgument,
                    "Idempotency key is required",
                    false,
                )
            })?;
            let task_id = context.task_id.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::Conflict,
                    "Run is not bound to a Task",
                    false,
                )
            })?;
            let mut storage = state.storage.clone();
            LinkThreadToTask::execute(
                &mut storage,
                LinkThreadInput {
                    task_id,
                    target_thread_id: thread_id,
                    actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
                    idempotency_key: key,
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            record_agent_activity(
                state,
                context.space_id.into_uuid(),
                context.agent_id.into_uuid(),
                "task.link_thread",
                agent_activity_details(
                    json!({
                        "run_id": context.run_id,
                        "task_id": task_id,
                        "thread_id": thread_id,
                        "scope_channel_id": target.as_ref().map(|reference| reference.channel_id),
                    }),
                    vec![(
                        "thread",
                        target
                            .as_ref()
                            .map(|reference| reference.label.clone())
                            .unwrap_or_else(|| "Thread".to_owned()),
                    )],
                    None,
                ),
            )
            .await;
            capability_value(
                &task_projection(&state.pool, task_id.into_uuid())
                    .await
                    .map_err(api_to_capability)?,
            )
        }
        capability::Action::TaskUnlinkThread { thread_id } => {
            let target = activity_thread_reference(&state.pool, thread_id).await;
            let key = request.idempotency_key.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::InvalidArgument,
                    "Idempotency key is required",
                    false,
                )
            })?;
            let task_id = context.task_id.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::Conflict,
                    "Run is not bound to a Task",
                    false,
                )
            })?;
            let mut storage = state.storage.clone();
            UnlinkThreadFromTask::execute(
                &mut storage,
                LinkThreadInput {
                    task_id,
                    target_thread_id: thread_id,
                    actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
                    idempotency_key: key,
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            record_agent_activity(
                state,
                context.space_id.into_uuid(),
                context.agent_id.into_uuid(),
                "task.unlink_thread",
                agent_activity_details(
                    json!({
                        "run_id": context.run_id,
                        "task_id": task_id,
                        "thread_id": thread_id,
                        "scope_channel_id": target.as_ref().map(|reference| reference.channel_id),
                    }),
                    vec![(
                        "thread",
                        target
                            .as_ref()
                            .map(|reference| reference.label.clone())
                            .unwrap_or_else(|| "Thread".to_owned()),
                    )],
                    None,
                ),
            )
            .await;
            capability_value(
                &task_projection(&state.pool, task_id.into_uuid())
                    .await
                    .map_err(api_to_capability)?,
            )
        }
        capability::Action::TaskUpdate { title } => {
            let activity_title = title.clone();
            let focus = activity_thread_reference(&state.pool, context.focus_thread_id).await;
            let key = request.idempotency_key.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::InvalidArgument,
                    "Idempotency key is required",
                    false,
                )
            })?;
            let task_id = context.task_id.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::Conflict,
                    "Run is not bound to a Task",
                    false,
                )
            })?;
            let mut storage = state.storage.clone();
            UpdateTask::execute(
                &mut storage,
                UpdateTaskInput {
                    task_id,
                    actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
                    idempotency_key: key,
                    action: TaskAction::Rename { title },
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            record_agent_activity(
                state,
                context.space_id.into_uuid(),
                context.agent_id.into_uuid(),
                "task.update",
                agent_activity_details(
                    json!({
                        "run_id": context.run_id,
                        "task_id": task_id,
                        "thread_id": context.focus_thread_id,
                        "scope_channel_id": focus.as_ref().map(|reference| reference.channel_id),
                    }),
                    vec![("title", activity_title)],
                    None,
                ),
            )
            .await;
            capability_value(
                &task_projection(&state.pool, task_id.into_uuid())
                    .await
                    .map_err(api_to_capability)?,
            )
        }
        capability::Action::TaskSubmitReview { body, post_to } => {
            finish_agent_task(
                state,
                computer_id,
                context,
                request.idempotency_key,
                TaskOutcome::SubmitReview {
                    message: Some(OutcomeMessage {
                        message_id: MessageId::from_uuid(Uuid::now_v7()),
                        body_markdown: body,
                        post_to: task_post_target(post_to),
                    }),
                },
            )
            .await
        }
        capability::Action::TaskDone { result, post_to } => {
            finish_agent_task(
                state,
                computer_id,
                context,
                request.idempotency_key,
                TaskOutcome::Done {
                    result: OutcomeMessage {
                        message_id: MessageId::from_uuid(Uuid::now_v7()),
                        body_markdown: result,
                        post_to: task_post_target(post_to),
                    },
                },
            )
            .await
        }
        capability::Action::TaskClose { reason, note } => {
            let reason = match reason {
                capability::CloseReason::Invalid => CloseReason::Invalid,
                capability::CloseReason::Duplicate => CloseReason::Duplicate,
                capability::CloseReason::NotNeeded => CloseReason::NotNeeded,
                capability::CloseReason::Obsolete => CloseReason::Obsolete,
                capability::CloseReason::Other => CloseReason::Other,
            };
            finish_agent_task(
                state,
                computer_id,
                context,
                request.idempotency_key,
                TaskOutcome::Close { reason, note },
            )
            .await
        }
        capability::Action::ChannelCreate { name, private } => {
            let activity_name = name.clone();
            let focus = activity_thread_reference(&state.pool, context.focus_thread_id).await;
            let channel_id = ChannelId::from_uuid(Uuid::now_v7());
            let mut storage = state.storage.clone();
            let channel = CreateChannelAction::execute(
                &mut storage,
                CreateChannelActionInput {
                    channel_id,
                    audience: Default::default(),
                    kind: if private {
                        ChannelKind::Private
                    } else {
                        ChannelKind::Public
                    },
                    slug: Some(channel_slug(&name, channel_id.into_uuid())),
                    topic: Some(name),
                    action_message_id: MessageId::from_uuid(Uuid::now_v7()),
                    actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
                    idempotency_key: request.idempotency_key.ok_or_else(|| {
                        capability_error(
                            capability::ErrorCode::InvalidArgument,
                            "Idempotency key is required",
                            false,
                        )
                    })?,
                    current_run_id: context.run_id,
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            record_agent_activity(
                state,
                context.space_id.into_uuid(),
                context.agent_id.into_uuid(),
                "channel.create",
                agent_activity_details(
                    json!({
                        "run_id": context.run_id,
                        "channel_id": channel.id,
                        "thread_id": context.focus_thread_id,
                        "scope_channel_id": focus.as_ref().map(|reference| reference.channel_id),
                    }),
                    vec![("name", activity_name), ("private", private.to_string())],
                    None,
                ),
            )
            .await;
            Ok(json!({"channel_id":channel.id,"kind":if private{"private"}else{"public"}}))
        }
        capability::Action::ChannelLeave { channel_id } => {
            let channel_label = activity_channel_label(state, channel_id)
                .await
                .unwrap_or_else(|| "Channel".to_owned());
            let key = request.idempotency_key.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::InvalidArgument,
                    "Idempotency key is required",
                    false,
                )
            })?;
            let mut storage = state.storage.clone();
            LeaveChannel::execute(
                &mut storage,
                MemberId::from_uuid(context.agent_id.into_uuid()),
                channel_id,
                key,
                OffsetDateTime::now_utc(),
            )
            .await
            .map_err(app_to_capability)?;
            record_agent_activity(
                state,
                context.space_id.into_uuid(),
                context.agent_id.into_uuid(),
                "channel.leave",
                agent_activity_details(
                    json!({
                        "run_id": context.run_id,
                        "channel_id": channel_id,
                        "scope_channel_id": channel_id,
                    }),
                    vec![("channel", channel_label)],
                    None,
                ),
            )
            .await;
            Ok(json!({"channel_id": channel_id, "member_id": context.agent_id}))
        }
        capability::Action::AgentCreate {
            name,
            role,
            driver,
            computer_id: target_computer_id,
        } => {
            if !valid_display_name(&name) || name.chars().count() > 40 || role.trim().is_empty() {
                return Err(capability_error(
                    capability::ErrorCode::InvalidArgument,
                    "Agent display name or role is invalid",
                    false,
                ));
            }
            let activity_name = name.clone();
            let activity_role = bounded_activity_text(&role).0;
            let activity_driver = match driver {
                capability::DriverKind::Codex => "codex",
                capability::DriverKind::Builtin => "builtin",
            };
            let activity_computer = activity_computer_label(state, target_computer_id)
                .await
                .unwrap_or_else(|| "Computer".to_owned());
            let focus = activity_thread_reference(&state.pool, context.focus_thread_id).await;
            let agent_id = MemberId::from_uuid(Uuid::now_v7());
            let mut storage = state.storage.clone();
            let agent = CreateAgentAction::execute(
                &mut storage,
                CreateAgentActionInput {
                    agent_member_id: agent_id,
                    display_name: name.clone(),
                    role_text: role,
                    computer_id: target_computer_id,
                    driver_kind: match driver {
                        capability::DriverKind::Codex => DriverKind::Codex,
                        capability::DriverKind::Builtin => DriverKind::Builtin,
                    },
                    action_message_id: MessageId::from_uuid(Uuid::now_v7()),
                    actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
                    idempotency_key: request.idempotency_key.ok_or_else(|| {
                        capability_error(
                            capability::ErrorCode::InvalidArgument,
                            "Idempotency key is required",
                            false,
                        )
                    })?,
                    current_run_id: context.run_id,
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            record_agent_activity(
                state,
                context.space_id.into_uuid(),
                context.agent_id.into_uuid(),
                "agent.create",
                agent_activity_details(
                    json!({
                        "run_id": context.run_id,
                        "target_member_id": agent.member_id,
                        "thread_id": context.focus_thread_id,
                        "scope_channel_id": focus.as_ref().map(|reference| reference.channel_id),
                    }),
                    vec![
                        ("name", activity_name),
                        ("role", activity_role),
                        ("driver", activity_driver.to_owned()),
                        ("computer", activity_computer),
                    ],
                    None,
                ),
            )
            .await;
            Ok(json!({"agent_id":agent.member_id,"lifecycle":"provisioning"}))
        }
        capability::Action::InboxCurrent => {
            let rows=sqlx::query("SELECT i.id,i.kind,i.strength,i.status,i.available_at,ri.disposition FROM run_items ri JOIN inbox_items i ON i.id=ri.inbox_item_id WHERE ri.run_id=$1 ORDER BY ri.delivery_seq").bind(context.run_id.into_uuid()).fetch_all(&state.pool).await.map_err(|_|capability_error(capability::ErrorCode::Internal,"Run Items could not be read",false))?;
            Ok(
                json!({"run_id":context.run_id,"items":rows.iter().map(|row|json!({"id":row.get::<Uuid,_>("id"),"kind":row.get::<String,_>("kind"),"strength":row.get::<String,_>("strength"),"status":row.get::<String,_>("status"),"available_at":timestamp(row.get("available_at")),"disposition":row.get::<Option<String>,_>("disposition")})).collect::<Vec<_>>(),"notices":[]}),
            )
        }
        capability::Action::InboxAck { item_id, reason } => {
            let source = activity_inbox_thread_reference(&state.pool, item_id).await;
            let mut activity_arguments = source
                .as_ref()
                .map(|reference| vec![("source", reference.label.clone())])
                .unwrap_or_default();
            activity_arguments.push(("disposition", "handled".to_owned()));
            if let Some(reason) = reason {
                activity_arguments.push(("reason", reason));
            }
            record_agent_item_disposition(
                state,
                computer_id,
                context,
                item_id,
                InboxItemDisposition::Handled,
                None,
            )
            .await?;
            record_agent_activity(
                state,
                context.space_id.into_uuid(),
                context.agent_id.into_uuid(),
                "inbox.ack",
                agent_activity_details(
                    json!({
                        "run_id": context.run_id,
                        "item_id": item_id,
                        "thread_id": source.as_ref().map(|reference| reference.thread_id),
                        "scope_channel_id": source.as_ref().map(|reference| reference.channel_id),
                    }),
                    activity_arguments,
                    None,
                ),
            )
            .await;
            Ok(json!({"item_id":item_id,"disposition":"handled"}))
        }
        capability::Action::InboxDefer { item_id, until } => {
            let source = activity_inbox_thread_reference(&state.pool, item_id).await;
            let mut activity_arguments = source
                .as_ref()
                .map(|reference| vec![("source", reference.label.clone())])
                .unwrap_or_default();
            activity_arguments.extend([
                ("disposition", "deferred".to_owned()),
                ("until", timestamp(until)),
            ]);
            record_agent_item_disposition(
                state,
                computer_id,
                context,
                item_id,
                InboxItemDisposition::Deferred,
                Some(until),
            )
            .await?;
            record_agent_activity(
                state,
                context.space_id.into_uuid(),
                context.agent_id.into_uuid(),
                "inbox.defer",
                agent_activity_details(
                    json!({
                        "run_id": context.run_id,
                        "item_id": item_id,
                        "thread_id": source.as_ref().map(|reference| reference.thread_id),
                        "scope_channel_id": source.as_ref().map(|reference| reference.channel_id),
                    }),
                    activity_arguments,
                    None,
                ),
            )
            .await;
            Ok(json!({"item_id":item_id,"disposition":"deferred","available_at":timestamp(until)}))
        }
        _ => Err(capability_error(
            capability::ErrorCode::Unavailable,
            "Agent action is not connected in this runtime",
            false,
        )),
    }
}

pub(super) fn task_post_target(target: capability::PostTarget) -> TaskPostTarget {
    match target {
        capability::PostTarget::Focus => TaskPostTarget::Focus,
        capability::PostTarget::Source => TaskPostTarget::Source,
    }
}

pub(super) async fn finish_agent_task(
    state: &RuntimeState,
    computer_id: Uuid,
    context: &capability::RunContext,
    idempotency_key: Option<IdempotencyKey>,
    outcome: TaskOutcome,
) -> Result<Value, capability::Error> {
    let task_id = context.task_id.ok_or_else(|| {
        capability_error(
            capability::ErrorCode::Conflict,
            "Run is not bound to a Task",
            false,
        )
    })?;
    let idempotency_key = idempotency_key.ok_or_else(|| {
        capability_error(
            capability::ErrorCode::InvalidArgument,
            "idempotency key is required",
            false,
        )
    })?;
    let (activity_kind, activity_arguments, activity_preview, activity_post_target) = match &outcome
    {
        TaskOutcome::SubmitReview { message } => {
            let post_to = message.as_ref().map(|message| message.post_to);
            (
                "task.submit_review",
                post_to
                    .map(|target| vec![("post_to", activity_task_post_target(target).to_owned())])
                    .unwrap_or_default(),
                message
                    .as_ref()
                    .map(|message| agent_activity_preview(&message.body_markdown)),
                post_to,
            )
        }
        TaskOutcome::Done { result } => (
            "task.done",
            vec![(
                "post_to",
                activity_task_post_target(result.post_to).to_owned(),
            )],
            Some(agent_activity_preview(&result.body_markdown)),
            Some(result.post_to),
        ),
        TaskOutcome::Close { reason, note } => {
            let reason = match reason {
                CloseReason::Invalid => "invalid",
                CloseReason::Duplicate => "duplicate",
                CloseReason::NotNeeded => "not_needed",
                CloseReason::Obsolete => "obsolete",
                CloseReason::Other => "other",
            };
            (
                "task.close",
                vec![("reason", reason.to_owned())],
                note.as_deref().map(agent_activity_preview),
                None,
            )
        }
    };
    let activity_thread_id = match activity_post_target {
        Some(TaskPostTarget::Focus) | None => context.focus_thread_id,
        Some(TaskPostTarget::Source) => activity_task_source_thread(state, task_id)
            .await
            .unwrap_or(context.focus_thread_id),
        Some(TaskPostTarget::Thread(thread_id)) => thread_id,
    };
    let activity_thread = activity_thread_reference(&state.pool, activity_thread_id).await;
    let mut storage = state.storage.clone();
    RecordTaskOutcome::execute(
        &mut storage,
        RecordTaskOutcomeInput {
            scope: TaskOutcomeScope::AgentRun(OutcomeRunContext {
                run_id: context.run_id,
                computer_id: ComputerId::from_uuid(computer_id),
                message_snapshot_sequence: context.message_snapshot_sequence,
            }),
            actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
            idempotency_key,
            outcome,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(app_to_capability)?;
    record_agent_activity(
        state,
        context.space_id.into_uuid(),
        context.agent_id.into_uuid(),
        activity_kind,
        agent_activity_details(
            json!({
                "run_id": context.run_id,
                "task_id": task_id,
                "thread_id": activity_thread_id,
                "scope_channel_id": activity_thread.as_ref().map(|reference| reference.channel_id),
            }),
            activity_arguments,
            activity_preview,
        ),
    )
    .await;
    capability_value(
        &task_projection(&state.pool, task_id.into_uuid())
            .await
            .map_err(api_to_capability)?,
    )
}

pub(super) async fn record_agent_item_disposition(
    state: &RuntimeState,
    computer_id: Uuid,
    context: &capability::RunContext,
    item_id: InboxItemId,
    disposition: InboxItemDisposition,
    defer_until: Option<OffsetDateTime>,
) -> Result<(), capability::Error> {
    let mut storage = state.storage.clone();
    RecordRunItemDisposition::execute(
        &mut storage,
        RecordRunItemDispositionInput {
            run_id: context.run_id,
            computer_id: ComputerId::from_uuid(computer_id),
            item_id,
            disposition,
            defer_until,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(app_to_capability)?;
    Ok(())
}

async fn discover_operation(
    state: &RuntimeState,
    context: &capability::RunContext,
    operation: &str,
) -> Result<Value, capability::Error> {
    if operation != "agent.create" {
        return Err(capability_error(
            capability::ErrorCode::NotFound,
            "Discovery operation is not available",
            false,
        ));
    }

    let computers = sqlx::query(
        "SELECT id,name,hostname,os,connection_status FROM computers \
         WHERE space_id=$1 AND deleted_at IS NULL AND connection_status='online' ORDER BY name,id",
    )
    .bind(context.space_id.into_uuid())
    .fetch_all(&state.pool)
    .await
    .map_err(|_| {
        capability_error(
            capability::ErrorCode::Internal,
            "Discovery options could not be read",
            false,
        )
    })?;
    let permission_granted = sqlx::query_scalar::<_, bool>(
        "SELECT EXISTS(SELECT 1 FROM member_permissions \
         WHERE member_id=$1 AND space_id=$2 AND action_code='agent.create')",
    )
    .bind(context.agent_id.into_uuid())
    .bind(context.space_id.into_uuid())
    .fetch_one(&state.pool)
    .await
    .map_err(|_| {
        capability_error(
            capability::ErrorCode::Internal,
            "Discovery permission state could not be read",
            false,
        )
    })?;

    Ok(json!({
        "operation": "agent.create",
        "description": "Create an Agent in the current Space",
        "input": {
            "fields": [
                {
                    "name": "name",
                    "value_type": "display_name",
                    "required": true
                },
                {
                    "name": "role_file",
                    "value_type": "agent_role_file",
                    "required": true
                },
                {
                    "name": "computer_id",
                    "value_type": "computer_id",
                    "required": true,
                    "available": computers.iter().map(|row| json!({
                        "value": row.get::<Uuid, _>("id"),
                        "label": row.get::<String, _>("name"),
                        "hostname": row.get::<String, _>("hostname"),
                        "os": row.get::<String, _>("os"),
                        "status": row.get::<String, _>("connection_status"),
                        "available": row.get::<&str, _>("connection_status") == "online"
                    })).collect::<Vec<_>>()
                },
                {
                    "name": "driver",
                    "value_type": "driver_kind",
                    "required": true,
                    "available": [
                        { "value": "codex", "label": "Codex" },
                        { "value": "builtin", "label": "Builtin" }
                    ]
                }
            ]
        },
        "permission": {
            "action": "agent.create",
            "granted": permission_granted
        }
    }))
}

pub(super) async fn agent_read_thread(
    state: &RuntimeState,
    thread_id: Uuid,
    page: capability::Page,
) -> Result<Value, capability::Error> {
    let limit = i64::from(page.limit);
    let rows=sqlx::query("SELECT id,channel_seq,author_member_id,content_kind,body_markdown,created_at FROM messages WHERE thread_id=$1 AND ($2::bigint IS NULL OR channel_seq<$2) AND ($3::bigint IS NULL OR channel_seq>$3) ORDER BY channel_seq LIMIT $4").bind(thread_id).bind(page.before.map(|v|v as i64)).bind(page.after.map(|v|v as i64)).bind(limit).fetch_all(&state.pool).await.map_err(|_|capability_error(capability::ErrorCode::Internal,"Messages could not be read",false))?;
    Ok(
        json!({"thread_id":thread_id,"messages":rows.iter().map(|row|json!({"id":row.get::<Uuid,_>("id"),"seq":row.get::<i64,_>("channel_seq"),"author_member_id":row.get::<Uuid,_>("author_member_id"),"content":{"type":"text","body_markdown":row.get::<Option<String>,_>("body_markdown").unwrap_or_default()},"created_at":timestamp(row.get("created_at"))})).collect::<Vec<_>>()}),
    )
}

pub(super) async fn agent_read_channel(
    state: &RuntimeState,
    agent_id: Uuid,
    channel_id: Uuid,
    around_message_id: Option<Uuid>,
    limit: u16,
) -> Result<Value, capability::Error> {
    let mut storage = state.storage.clone();
    let member = ReadAgentChannel::membership(
        &mut storage,
        ChannelId::from_uuid(channel_id),
        MemberId::from_uuid(agent_id),
    )
    .await
    .map_err(app_to_capability)?;
    if !member {
        return Err(capability_error(
            capability::ErrorCode::PermissionDenied,
            "Channel is not visible to the Agent",
            false,
        ));
    }
    let around_sequence = match around_message_id {
        Some(message_id) => Some(
            ReadAgentChannel::around_sequence(
                &mut storage,
                MessageId::from_uuid(message_id),
                ChannelId::from_uuid(channel_id),
            )
            .await
            .map_err(app_to_capability)?
            .ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::NotFound,
                    "Around Message was not found in the Channel",
                    false,
                )
            })?,
        ),
        None => None,
    };
    let rows = if let Some(sequence) = around_sequence {
        sqlx::query(
            "SELECT * FROM messages WHERE channel_id=$1 AND deleted_at IS NULL \
             ORDER BY abs(channel_seq-$2),channel_seq LIMIT $3",
        )
        .bind(channel_id)
        .bind(sequence as i64)
        .bind(i64::from(limit))
        .fetch_all(&state.pool)
        .await
    } else {
        sqlx::query(
            "SELECT * FROM messages WHERE channel_id=$1 AND deleted_at IS NULL \
             ORDER BY channel_seq DESC LIMIT $2",
        )
        .bind(channel_id)
        .bind(i64::from(limit))
        .fetch_all(&state.pool)
        .await
    }
    .map_err(|_| {
        capability_error(
            capability::ErrorCode::Internal,
            "Channel Messages could not be read",
            false,
        )
    })?;
    let mut rows = rows;
    rows.sort_by_key(|row| row.get::<i64, _>("channel_seq"));
    let mut messages = Vec::with_capacity(rows.len());
    for row in &rows {
        messages.push(
            message_row(&state.pool, row)
                .await
                .map_err(api_to_capability)?,
        );
    }
    let snapshot = ReadAgentChannel::snapshot(&mut storage, ChannelId::from_uuid(channel_id))
        .await
        .map_err(app_to_capability)?;
    Ok(json!({
        "channel_id": channel_id,
        "messages": messages,
        "snapshot_channel_seq": snapshot
    }))
}

pub(super) fn capability_error(
    code: capability::ErrorCode,
    message: &str,
    retryable: bool,
) -> capability::Error {
    capability::Error {
        code,
        message: message.into(),
        retryable,
        details: Default::default(),
    }
}

pub(super) fn api_to_capability(error: ApiError) -> capability::Error {
    let code = if error.code == "context_changed" {
        capability::ErrorCode::ContextChanged
    } else {
        match error.status {
            StatusCode::NOT_FOUND => capability::ErrorCode::NotFound,
            StatusCode::FORBIDDEN => capability::ErrorCode::PermissionDenied,
            StatusCode::CONFLICT => capability::ErrorCode::Conflict,
            _ => capability::ErrorCode::Internal,
        }
    };
    capability_error(code, error.message, false)
}

pub(super) fn app_to_capability(
    error: crate::server::application::ports::ApplicationError,
) -> capability::Error {
    let api = application_error(error);
    api_to_capability(api)
}

/// Authorizes a Run-scoped call. The Computer's own token plus a `working` Run hosted by that
/// Computer is the whole proof: there is no per-Run credential and no deadline to check.
pub(super) async fn require_active_agent_run(
    state: &RuntimeState,
    headers: &HeaderMap,
    computer_id: Uuid,
    agent_id: Uuid,
    run_id: Uuid,
) -> Result<Uuid, ApiError> {
    let computer_token = bearer_token(headers)?;
    sqlx::query_scalar(
        "SELECT runs.space_id FROM agent_runs runs \
         JOIN agents ON agents.member_id=runs.agent_id \
         JOIN computers ON computers.id=agents.computer_id \
         WHERE computers.id=$1 AND computers.token_hash=$2 AND computers.deleted_at IS NULL \
         AND agents.member_id=$3 AND runs.id=$4 AND runs.status='working'",
    )
    .bind(computer_id)
    .bind(token_hash(computer_token))
    .bind(agent_id)
    .bind(run_id)
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::unauthenticated)
}

pub(super) fn capability_value(value: &impl serde::Serialize) -> Result<Value, capability::Error> {
    serde_json::to_value(value).map_err(|_| {
        capability_error(
            capability::ErrorCode::Internal,
            "projection could not be encoded",
            false,
        )
    })
}

const AGENT_ACTIVITY_PREVIEW_CHARS: usize = 280;

pub(in crate::server::adapters) struct AgentActivityPreview {
    pub(in crate::server::adapters) text: String,
    pub(in crate::server::adapters) truncated: bool,
}

pub(in crate::server::adapters) fn agent_activity_preview(value: &str) -> AgentActivityPreview {
    let (text, truncated) = bounded_activity_text(value);
    AgentActivityPreview { text, truncated }
}

pub(in crate::server::adapters) fn agent_activity_details(
    mut payload: Value,
    arguments: Vec<(&str, String)>,
    message: Option<AgentActivityPreview>,
) -> Value {
    let Some(object) = payload.as_object_mut() else {
        return payload;
    };
    object.retain(|_, value| !value.is_null());
    object.insert(
        "arguments".to_owned(),
        Value::Array(
            arguments
                .into_iter()
                .map(|(name, value)| {
                    let (value, _) = bounded_activity_text(&value);
                    json!({"name": name, "value": value})
                })
                .collect(),
        ),
    );
    if let Some(message) = message {
        object.insert("message_preview".to_owned(), json!(message.text));
        object.insert("message_truncated".to_owned(), json!(message.truncated));
    }
    payload
}

fn bounded_activity_text(value: &str) -> (String, bool) {
    let mut characters = value.chars();
    let mut preview: String = characters
        .by_ref()
        .take(AGENT_ACTIVITY_PREVIEW_CHARS)
        .collect();
    if characters.next().is_none() {
        return (preview, false);
    }
    preview.pop();
    preview.push('…');
    (preview, true)
}

struct ActivityThreadReference {
    thread_id: ThreadId,
    channel_id: Uuid,
    label: String,
}

async fn activity_thread_reference(
    pool: &PgPool,
    thread_id: ThreadId,
) -> Option<ActivityThreadReference> {
    let row = sqlx::query(
        "SELECT messages.channel_id,messages.channel_seq,channels.slug \
         FROM messages JOIN channels ON channels.id=messages.channel_id \
         WHERE messages.id=$1 AND messages.placement='root'",
    )
    .bind(thread_id.into_uuid())
    .fetch_optional(pool)
    .await
    .ok()??;
    let sequence: i64 = row.get("channel_seq");
    let slug: Option<String> = row.get("slug");
    Some(ActivityThreadReference {
        thread_id,
        channel_id: row.get("channel_id"),
        label: activity_thread_label(slug.as_deref(), sequence),
    })
}

pub(in crate::server::adapters) fn activity_thread_label(
    slug: Option<&str>,
    sequence: i64,
) -> String {
    slug.map_or_else(
        || format!("DM · message {sequence}"),
        |slug| format!("#{slug}:{sequence}"),
    )
}

async fn activity_channel_label(state: &RuntimeState, channel_id: ChannelId) -> Option<String> {
    sqlx::query_scalar("SELECT COALESCE(topic,slug,'Direct message') FROM channels WHERE id=$1")
        .bind(channel_id.into_uuid())
        .fetch_optional(&state.pool)
        .await
        .ok()
        .flatten()
}

async fn activity_member_label(state: &RuntimeState, member_id: MemberId) -> Option<String> {
    sqlx::query_scalar("SELECT display_name FROM members WHERE id=$1")
        .bind(member_id.into_uuid())
        .fetch_optional(&state.pool)
        .await
        .ok()
        .flatten()
}

async fn activity_computer_label(state: &RuntimeState, computer_id: ComputerId) -> Option<String> {
    sqlx::query_scalar("SELECT name FROM computers WHERE id=$1")
        .bind(computer_id.into_uuid())
        .fetch_optional(&state.pool)
        .await
        .ok()
        .flatten()
}

async fn activity_inbox_thread_reference(
    pool: &PgPool,
    item_id: InboxItemId,
) -> Option<ActivityThreadReference> {
    let thread_id: Uuid = sqlx::query_scalar("SELECT thread_id FROM inbox_items WHERE id=$1")
        .bind(item_id.into_uuid())
        .fetch_optional(pool)
        .await
        .ok()??;
    activity_thread_reference(pool, ThreadId::from_uuid(thread_id)).await
}

async fn activity_task_source_thread(state: &RuntimeState, task_id: TaskId) -> Option<ThreadId> {
    sqlx::query_scalar::<_, Uuid>("SELECT source_thread_id FROM tasks WHERE id=$1")
        .bind(task_id.into_uuid())
        .fetch_optional(&state.pool)
        .await
        .ok()
        .flatten()
        .map(ThreadId::from_uuid)
}

fn activity_task_post_target(target: TaskPostTarget) -> &'static str {
    match target {
        TaskPostTarget::Focus => "focus",
        TaskPostTarget::Source => "source",
        TaskPostTarget::Thread(_) => "thread",
    }
}

async fn activity_message_target(
    state: &RuntimeState,
    context: &capability::RunContext,
    target: &capability::MessageTarget,
) -> String {
    match target {
        capability::MessageTarget::Focus => {
            activity_thread_reference(&state.pool, context.focus_thread_id)
                .await
                .map(|reference| reference.label)
                .unwrap_or_else(|| "Current Focus".to_owned())
        }
        capability::MessageTarget::Thread(thread_id) => {
            activity_thread_reference(&state.pool, *thread_id)
                .await
                .map(|reference| reference.label)
                .unwrap_or_else(|| "Thread".to_owned())
        }
        capability::MessageTarget::Channel(channel_id) => {
            activity_channel_label(state, *channel_id)
                .await
                .map(|name| format!("#{name}"))
                .unwrap_or_else(|| "Channel".to_owned())
        }
        capability::MessageTarget::Member(member_id) => activity_member_label(state, *member_id)
            .await
            .map(|name| format!("DM with {name}"))
            .unwrap_or_else(|| "Direct message".to_owned()),
    }
}

/// Records an ephemeral `agent.activity` event for the Browser feed. The feed is best-effort, so a
/// failed insert never fails the Agent action that produced the activity.
pub(super) async fn record_agent_activity(
    state: &RuntimeState,
    space_id: Uuid,
    agent_member_id: Uuid,
    kind: &str,
    payload: serde_json::Value,
) {
    state
        .storage
        .record_agent_activity(
            SpaceId::from_uuid(space_id),
            MemberId::from_uuid(agent_member_id),
            kind,
            payload,
        )
        .await;
}

#[derive(Clone, Copy)]
pub(in crate::server::adapters) struct ComputerPrincipal {
    pub(in crate::server::adapters) computer_id: ComputerId,
}

pub(in crate::server::adapters) async fn submit_run_result<P: TransactionPort + Clone>(
    port: &P,
    principal: ComputerPrincipal,
    result: RunResult,
) -> Result<StatusCode, HttpError> {
    let outcome = match result.status {
        RunTerminalStatus::Completed => RunOutcome::Completed,
        RunTerminalStatus::Yielded => RunOutcome::Yielded,
        RunTerminalStatus::Failed => RunOutcome::Failed,
        RunTerminalStatus::Canceled => RunOutcome::Canceled,
    };
    let error_code = result.error_code.map(run_error_code);
    let item_dispositions = result
        .item_outcomes
        .into_iter()
        .map(|item| ItemDispositionInput {
            item_id: item.item_id,
            disposition: match item.disposition {
                ItemDisposition::Handled => InboxItemDisposition::Handled,
                ItemDisposition::Deferred => InboxItemDisposition::Deferred,
                ItemDisposition::Released => InboxItemDisposition::Released,
            },
        })
        .collect();
    let mut port = port.clone();
    CompleteRun::execute(
        &mut port,
        CompleteRunInput {
            event_id: result.event_id,
            run_id: result.run_id,
            computer_id: principal.computer_id,
            outcome,
            error_code,
            item_dispositions,
            continuation_note: result.continuation_note,
            max_retry_count: AttentionPolicy::MAX_RETRY_COUNT,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await?;
    Ok(StatusCode::OK)
}

pub(in crate::server::adapters) fn run_error_code(code: ComputerErrorCode) -> RunErrorCode {
    use RunErrorCode;
    match code {
        ComputerErrorCode::DriverError => RunErrorCode::DriverError,
        ComputerErrorCode::DriverLost => RunErrorCode::DriverLost,
        ComputerErrorCode::ComputerRestarted => RunErrorCode::ComputerRestarted,
        ComputerErrorCode::SessionUnavailable => RunErrorCode::SessionUnavailable,
        ComputerErrorCode::AgentUnavailable => RunErrorCode::AgentUnavailable,
        ComputerErrorCode::InvalidCommand => RunErrorCode::InvalidCommand,
        ComputerErrorCode::Internal => RunErrorCode::Internal,
    }
}
use crate::server::application::ports::{
    AttachmentTransaction, CollaborationTransaction, EffectSink, ExecutionTransaction,
    IdentityTransaction, TaskTransaction,
};
