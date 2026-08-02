use super::*;
use crate::protocol::computer::{ComputerErrorCode, DeliveryOutcome, DeliveryReceipt, RunStarted};
use crate::server::domain::{
    attention::InboxItemDisposition,
    execution::{RunErrorCode, RunOutcome},
};

#[derive(Deserialize)]
pub(super) struct RenewRunBody {
    fencing_token: String,
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
            fencing_token_hash: token_hash(started.fencing_token.expose()),
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
            fencing_token_hash: token_hash(receipt.fencing_token.expose()),
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
    CompleteRun::execute(
        &mut storage,
        CompleteRunInput {
            event_id: result.event_id,
            run_id: result.run_id,
            computer_id: ComputerId::from_uuid(computer_id),
            fencing_token_hash: token_hash(result.fencing_token.expose()),
            outcome,
            error_code,
            item_dispositions,
            continuation_note: result.continuation_note,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
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

pub(super) async fn claim_run(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    let mut storage = state.storage.clone();
    let candidate = ClaimNextRun::candidate(&mut storage, ComputerId::from_uuid(computer_id))
        .await
        .map_err(application_error)?;
    let Some(candidate) = candidate else {
        return Ok(Json(json!({"claimed":false})));
    };
    let run_id = RunId::from_uuid(Uuid::now_v7());
    let fencing_token = format!("{}{}", Uuid::now_v7().simple(), Uuid::now_v7().simple());
    let claim_result = ClaimRun::execute(
        &mut storage,
        ClaimRunInput {
            run_id,
            agent_id: candidate.agent_id,
            computer_id: ComputerId::from_uuid(computer_id),
            task_id: candidate.task_id,
            focus_thread_id: candidate.thread_id,
            item_ids: vec![candidate.item_id],
            fencing_token: RawFencingToken::new(fencing_token),
            lease_expires_at: OffsetDateTime::now_utc() + Duration::minutes(2),
        },
    )
    .await;
    if let Err(error) = claim_result {
        let error_code = run_claim_error_code(&error);
        let mut storage = state.storage.clone();
        let changed = ClaimNextRun::record_failure(
            &mut storage,
            candidate.item_id,
            candidate.message_id,
            candidate.channel_id,
            error_code,
        )
        .await
        .map_err(application_error)?;
        if changed {
            tracing::warn!(
                %computer_id,
                item_id = %candidate.item_id.into_uuid(),
                agent_id = %candidate.agent_id.into_uuid(),
                error_code,
                "Computer Run claim failed; the Inbox Item remains pending"
            );
        }
        return Err(application_error(error));
    }
    Ok(Json(json!({"claimed":true,"run_id":run_id})))
}

pub(super) fn run_claim_error_code(
    error: &crate::server::application::ports::ApplicationError,
) -> &'static str {
    use crate::server::application::ports::ApplicationError;
    match error {
        ApplicationError::NotFound => "run_claim_not_found",
        ApplicationError::Unauthenticated => "run_claim_unauthenticated",
        ApplicationError::PayloadTooLarge => "run_claim_payload_too_large",
        ApplicationError::PermissionDenied => "run_claim_permission_denied",
        ApplicationError::Conflict | ApplicationError::Domain(_) => "run_claim_conflict",
        ApplicationError::ContextChanged => "run_claim_context_changed",
        ApplicationError::Unavailable => "run_claim_unavailable",
        ApplicationError::Internal => "run_claim_internal",
    }
}

pub(super) async fn renew_run(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, run_id)): Path<(Uuid, Uuid)>,
    Json(body): Json<RenewRunBody>,
) -> Result<Json<Value>, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    let lease_expires_at = OffsetDateTime::now_utc() + Duration::minutes(2);
    let mut storage = state.storage.clone();
    let run = RenewRun::execute(
        &mut storage,
        RenewRunInput {
            run_id: RunId::from_uuid(run_id),
            computer_id: ComputerId::from_uuid(computer_id),
            fencing_token_hash: token_hash(&body.fencing_token),
            lease_expires_at,
        },
    )
    .await
    .map_err(application_error)?;
    let run = run.view();
    Ok(Json(json!({
        "run_id": run.id,
        "lease_expires_at": timestamp(run.lease_expires_at)
    })))
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
            fencing_token_hash: token_hash(&context.fencing_token),
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
                json!({"agent":{"id":context.agent_id,"space_id":context.space_id},"task":task,"focus_thread_id":context.focus_thread_id,"run":{"id":context.run_id,"message_snapshot_sequence":context.message_snapshot_sequence},"claimed_items":items.iter().map(|row|json!({"id":row.get::<Uuid,_>("id"),"kind":row.get::<String,_>("kind"),"strength":row.get::<String,_>("strength"),"status":row.get::<String,_>("status"),"available_at":timestamp(row.get("available_at"))})).collect::<Vec<_>>(),"session_continuity":continuity}),
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
            };
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
                    mentions: Vec::new(),
                    mention_all: false,
                    attachment_ids: Vec::new(),
                    reply_to_message_id: None,
                },
            )
            .await
            .map_err(api_to_capability)?;
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
            let mut storage = state.storage.clone();
            let task = CreateTaskFromRootMessage::execute(
                &mut storage,
                CreateTaskInput {
                    task_id: TaskId::from_uuid(Uuid::now_v7()),
                    actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
                    source: TaskSource::AgentRun(context.run_id),
                    title: title.unwrap_or_else(|| default_title.chars().take(120).collect()),
                    assignee_agent_member_id: assignee,
                    idempotency_key: key,
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            capability_value(
                &task_projection(&state.pool, task.view().id.into_uuid())
                    .await
                    .map_err(api_to_capability)?,
            )
        }
        capability::Action::TaskLinkThread { thread_id } => {
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
            capability_value(
                &task_projection(&state.pool, task_id.into_uuid())
                    .await
                    .map_err(api_to_capability)?,
            )
        }
        capability::Action::TaskUnlinkThread { thread_id } => {
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
            capability_value(
                &task_projection(&state.pool, task_id.into_uuid())
                    .await
                    .map_err(api_to_capability)?,
            )
        }
        capability::Action::TaskUpdate { title } => {
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
                    slug: Some(unique_handle(&name, channel_id.into_uuid())),
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
            Ok(json!({"channel_id":channel.id,"kind":if private{"private"}else{"public"}}))
        }
        capability::Action::AgentCreate { name, role, driver } => {
            let agent_id = MemberId::from_uuid(Uuid::now_v7());
            let mut storage = state.storage.clone();
            let agent = CreateAgentAction::execute(
                &mut storage,
                CreateAgentActionInput {
                    agent_member_id: agent_id,
                    display_name: name.clone(),
                    handle: unique_handle(&name, agent_id.into_uuid()),
                    role_text: role,
                    computer_id: ComputerId::from_uuid(computer_id),
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
            Ok(json!({"agent_id":agent.member_id,"lifecycle":"provisioning"}))
        }
        capability::Action::InboxCurrent => {
            let rows=sqlx::query("SELECT i.id,i.kind,i.strength,i.status,i.available_at,ri.disposition FROM run_items ri JOIN inbox_items i ON i.id=ri.inbox_item_id WHERE ri.run_id=$1 ORDER BY ri.delivery_seq").bind(context.run_id.into_uuid()).fetch_all(&state.pool).await.map_err(|_|capability_error(capability::ErrorCode::Internal,"Run Items could not be read",false))?;
            Ok(
                json!({"run_id":context.run_id,"items":rows.iter().map(|row|json!({"id":row.get::<Uuid,_>("id"),"kind":row.get::<String,_>("kind"),"strength":row.get::<String,_>("strength"),"status":row.get::<String,_>("status"),"available_at":timestamp(row.get("available_at")),"disposition":row.get::<Option<String>,_>("disposition")})).collect::<Vec<_>>(),"notices":[]}),
            )
        }
        capability::Action::InboxAck { item_id, .. } => {
            record_agent_item_disposition(
                state,
                computer_id,
                context,
                item_id,
                InboxItemDisposition::Handled,
                None,
            )
            .await?;
            Ok(json!({"item_id":item_id,"disposition":"handled"}))
        }
        capability::Action::InboxDefer { item_id, until } => {
            record_agent_item_disposition(
                state,
                computer_id,
                context,
                item_id,
                InboxItemDisposition::Deferred,
                Some(until),
            )
            .await?;
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
    let mut storage = state.storage.clone();
    RecordTaskOutcome::execute(
        &mut storage,
        RecordTaskOutcomeInput {
            scope: TaskOutcomeScope::AgentRun(OutcomeRunContext {
                run_id: context.run_id,
                computer_id: ComputerId::from_uuid(computer_id),
                fencing_token_hash: token_hash(&context.fencing_token),
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
            fencing_token_hash: token_hash(&context.fencing_token),
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

pub(super) async fn require_active_agent_run(
    state: &RuntimeState,
    headers: &HeaderMap,
    computer_id: Uuid,
    agent_id: Uuid,
    run_id: Uuid,
) -> Result<Uuid, ApiError> {
    let computer_token = bearer_token(headers)?;
    let fencing_token = headers
        .get("x-sumi-fencing-token")
        .and_then(|value| value.to_str().ok())
        .ok_or_else(ApiError::unauthenticated)?;
    sqlx::query_scalar(
        "SELECT runs.space_id FROM agent_runs runs \
         JOIN agents ON agents.member_id=runs.agent_id \
         JOIN computers ON computers.id=agents.computer_id \
         WHERE computers.id=$1 AND computers.token_hash=$2 AND computers.deleted_at IS NULL \
         AND agents.member_id=$3 AND runs.id=$4 AND runs.status='running' \
         AND runs.fencing_token_hash=$5 AND runs.lease_expires_at>now()",
    )
    .bind(computer_id)
    .bind(token_hash(computer_token))
    .bind(agent_id)
    .bind(run_id)
    .bind(token_hash(fencing_token))
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

#[derive(Clone, Copy)]
pub(in crate::server::adapters) struct ComputerPrincipal {
    pub(in crate::server::adapters) computer_id: ComputerId,
}

pub(in crate::server::adapters) async fn submit_run_result<P: TransactionPort + Clone>(
    port: &P,
    principal: ComputerPrincipal,
    result: RunResult,
) -> Result<StatusCode, HttpError> {
    let token_hash = hex::encode(Sha256::digest(result.fencing_token.expose().as_bytes()));
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
            fencing_token_hash: token_hash,
            outcome,
            error_code,
            item_dispositions,
            continuation_note: result.continuation_note,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await?;
    Ok(StatusCode::OK)
}

pub(super) fn run_error_code(code: ComputerErrorCode) -> RunErrorCode {
    use RunErrorCode;
    match code {
        ComputerErrorCode::InvalidCommand => RunErrorCode::InvalidCommand,
        ComputerErrorCode::AgentUnavailable => RunErrorCode::AgentUnavailable,
        ComputerErrorCode::ProcessLost => RunErrorCode::ProcessLost,
        ComputerErrorCode::SessionLost => RunErrorCode::SessionLost,
        ComputerErrorCode::SandboxUnavailable => RunErrorCode::SandboxUnavailable,
        ComputerErrorCode::DriverUnavailable => RunErrorCode::DriverUnavailable,
        ComputerErrorCode::Internal => RunErrorCode::Internal,
    }
}
use crate::server::application::ports::{
    AttachmentTransaction, CollaborationTransaction, EffectSink, ExecutionTransaction,
    IdentityTransaction, TaskTransaction,
};
