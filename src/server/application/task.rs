use time::OffsetDateTime;

use crate::ids::{ComputerId, IdempotencyKey, MemberId, MessageId, RunId, TaskId, ThreadId};

use crate::server::domain::{
    conversation::{Message, MessageContent, MessagePlacement},
    execution::RunStatus,
    task::{CloseReason, Task},
};

use super::ports::{ApplicationError, Effect, ServerTransaction, TransactionPort};

pub(in crate::server) enum TaskSource {
    HumanRoot(ThreadId),
    AgentRun(RunId),
}

pub(in crate::server) struct CreateTaskInput {
    pub(in crate::server) task_id: TaskId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) source: TaskSource,
    pub(in crate::server) title: String,
    pub(in crate::server) assignee_agent_member_id: Option<MemberId>,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct CreateTaskFromRootMessage;

impl CreateTaskFromRootMessage {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: CreateTaskInput,
    ) -> Result<Task, ApplicationError> {
        port.transact(async |transaction| {
            if let Some(task_id) = transaction
                .task_for_idempotency(input.actor_member_id, "task.create", input.idempotency_key)
                .await?
            {
                return transaction.task(task_id).await;
            }
            let (source_thread_id, running_agent) = match input.source {
                TaskSource::HumanRoot(thread_id) => (thread_id, None),
                TaskSource::AgentRun(run_id) => {
                    let run = transaction.run(run_id).await?;
                    if run.agent_id != input.actor_member_id || run.status != RunStatus::Running {
                        return Err(ApplicationError::ContextChanged);
                    }
                    (run.focus_thread_id, Some(run))
                }
            };
            let source = transaction.thread(source_thread_id).await?;
            if !transaction
                .can_read_thread(input.actor_member_id, source.id)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            if transaction.task_for_source(source.id).await?.is_some() {
                return Err(ApplicationError::Conflict);
            }
            let root = transaction.root_message(source.id).await?;

            let assignee = running_agent
                .as_ref()
                .map(|run| input.assignee_agent_member_id.unwrap_or(run.agent_id))
                .or(input.assignee_agent_member_id);
            if let Some(agent) = assignee
                && !transaction.can_assign_agent(agent, &source).await?
            {
                return Err(ApplicationError::PermissionDenied);
            }

            let task = Task::create(
                input.task_id,
                input.title,
                input.actor_member_id,
                assignee,
                &source,
                &root,
                running_agent.is_some(),
                input.now,
            )?;
            transaction.insert_task(task.clone()).await?;

            if let Some(mut run) = running_agent {
                run.bind_task(&task)?;
                for run_item in &run.items {
                    let mut item = transaction.inbox_item(run_item.inbox_item_id).await?;
                    item.bind_task(task.id)?;
                    transaction.save_inbox_item(item).await?;
                }
                transaction.save_run(run.clone()).await?;
                transaction.emit(Effect::RunTaskBound {
                    run_id: run.id,
                    task_id: task.id,
                });
            }
            transaction
                .record_task_idempotency(
                    input.actor_member_id,
                    "task.create",
                    input.idempotency_key,
                    task.id,
                )
                .await?;
            transaction
                .record_task_audit(input.actor_member_id, "task.create", task.id, input.now)
                .await?;
            transaction.emit(Effect::TaskCreated(task.id));
            Ok(task)
        })
        .await
    }
}

#[derive(Clone, Copy)]
pub(in crate::server) enum TaskPostTarget {
    Focus,
    Source,
    Thread(ThreadId),
}

/// Message a Task outcome publishes. `submit_review` may omit it; `done` requires it, because
/// `tasks.result_message_id` must point at a stored Message.
pub(in crate::server) struct OutcomeMessage {
    pub(in crate::server) message_id: MessageId,
    pub(in crate::server) body_markdown: String,
    pub(in crate::server) post_to: TaskPostTarget,
}

pub(in crate::server) enum TaskOutcome {
    SubmitReview {
        message: Option<OutcomeMessage>,
    },
    Done {
        result: OutcomeMessage,
    },
    Close {
        reason: CloseReason,
        note: Option<String>,
    },
}

impl TaskOutcome {
    fn action_name(&self) -> &'static str {
        match self {
            Self::SubmitReview { .. } => "task.submit_review",
            Self::Done { .. } => "task.done",
            Self::Close { .. } => "task.close",
        }
    }
}

/// Run ownership proof supplied when an Agent records the outcome from inside its Run. Absent for
/// Browser callers, which hold no Run and therefore complete no Run.
pub(in crate::server) struct OutcomeRunContext {
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) fencing_token_hash: String,
    pub(in crate::server) message_snapshot_sequence: u64,
}

pub(in crate::server) enum TaskOutcomeScope {
    Browser { task_id: TaskId },
    AgentRun(OutcomeRunContext),
}

pub(in crate::server) struct RecordTaskOutcomeInput {
    pub(in crate::server) scope: TaskOutcomeScope,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) outcome: TaskOutcome,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct RecordTaskOutcome;

impl RecordTaskOutcome {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: RecordTaskOutcomeInput,
    ) -> Result<Task, ApplicationError> {
        port.transact(async |transaction| {
            let action_name = input.outcome.action_name();
            if let Some(task_id) = transaction
                .task_for_idempotency(input.actor_member_id, action_name, input.idempotency_key)
                .await?
            {
                return transaction.task(task_id).await;
            }

            // An Agent proves Run ownership before the Task changes; a Browser caller holds no Run
            // and therefore reaches the same transitions without a Run to finalize.
            let mut run = match &input.scope {
                TaskOutcomeScope::Browser { .. } => None,
                TaskOutcomeScope::AgentRun(context) => {
                    let run = transaction.run(context.run_id).await?;
                    if run.agent_id != input.actor_member_id
                        || !transaction
                            .can_operate_agent(context.computer_id, run.agent_id)
                            .await?
                    {
                        return Err(ApplicationError::PermissionDenied);
                    }
                    run.validate_fencing(&context.fencing_token_hash)?;
                    if transaction
                        .thread_message_sequence(run.focus_thread_id)
                        .await?
                        != context.message_snapshot_sequence
                    {
                        return Err(ApplicationError::ContextChanged);
                    }
                    Some(run)
                }
            };
            let task_id = match (&input.scope, &run) {
                (TaskOutcomeScope::Browser { task_id }, _) => *task_id,
                (TaskOutcomeScope::AgentRun(_), Some(run)) => {
                    run.task_id.ok_or(ApplicationError::ContextChanged)?
                }
                (TaskOutcomeScope::AgentRun(_), None) => {
                    unreachable!("an Agent scope resolves its Run above")
                }
            };
            let mut task = transaction.task(task_id).await?;

            let message = match input.outcome {
                TaskOutcome::SubmitReview { message } => {
                    let message = resolve_outcome_message(
                        transaction,
                        input.actor_member_id,
                        &task,
                        run.as_ref(),
                        message,
                        input.now,
                    )
                    .await?;
                    task.request_review(input.actor_member_id, input.now)?;
                    message
                }
                TaskOutcome::Done { result } => {
                    let result_id = result.message_id;
                    let message = resolve_outcome_message(
                        transaction,
                        input.actor_member_id,
                        &task,
                        run.as_ref(),
                        Some(result),
                        input.now,
                    )
                    .await?;
                    task.finish(input.actor_member_id, true, result_id, input.now)?;
                    message
                }
                TaskOutcome::Close { reason, note } => {
                    let allowed = task.creator_member_id == input.actor_member_id
                        || task.assignee_agent_member_id == Some(input.actor_member_id)
                        || transaction
                            .can_govern_task(input.actor_member_id, &task)
                            .await?;
                    if !allowed {
                        return Err(ApplicationError::PermissionDenied);
                    }
                    task.close(reason, note, input.now)?;
                    None
                }
            };

            if let Some(run) = run.as_mut() {
                let fencing_token_hash = match &input.scope {
                    TaskOutcomeScope::AgentRun(context) => context.fencing_token_hash.clone(),
                    TaskOutcomeScope::Browser { .. } => {
                        unreachable!("only an Agent scope carries a Run")
                    }
                };
                run.begin_finalizing(&fencing_token_hash)?;
                for index in 0..run.items.len() {
                    let item_id = run.items[index].inbox_item_id;
                    let disposition = run.items[index]
                        .disposition
                        .unwrap_or(crate::server::domain::attention::InboxItemDisposition::Handled);
                    run.set_item_disposition(item_id, disposition)?;
                    let mut item = transaction.inbox_item(item_id).await?;
                    item.apply_disposition(run.id, disposition, input.now)?;
                    transaction.save_inbox_item(item).await?;
                }
                run.finish(
                    &fencing_token_hash,
                    crate::server::domain::execution::RunOutcome::Completed,
                    None,
                    None,
                    input.now,
                )?;
            }

            if let Some(message) = message {
                let message_id = message.id;
                transaction.insert_message(message).await?;
                transaction.emit(Effect::MessageCreated(message_id));
            }
            transaction.save_task(task.clone()).await?;
            if let Some(run) = run {
                transaction.save_run(run.clone()).await?;
                transaction.emit(Effect::RunCompleted(run.id));
            }
            transaction
                .record_task_idempotency(
                    input.actor_member_id,
                    action_name,
                    input.idempotency_key,
                    task.id,
                )
                .await?;
            transaction
                .record_task_audit(input.actor_member_id, action_name, task.id, input.now)
                .await?;
            match action_name {
                "task.submit_review" => {
                    transaction.emit(Effect::TaskUpdated(task.id));
                }
                "task.done" => {
                    transaction.emit(Effect::TaskCompleted {
                        task_id: task.id,
                        result_message_id: task.result_message_id.expect("done Task has Result"),
                    });
                    transaction.emit(Effect::SessionClose(task.id));
                }
                "task.close" => {
                    transaction.emit(Effect::TaskFinished(task.id));
                    transaction.emit(Effect::SessionClose(task.id));
                }
                _ => unreachable!("a Task outcome has a stable action name"),
            }
            Ok(task)
        })
        .await
    }
}

/// Resolves the target Thread and authorizes the post. `Focus` requires a Run, so a Browser caller
/// addresses the Thread explicitly or falls back to the Source Thread.
async fn resolve_outcome_message(
    transaction: &mut impl ServerTransaction,
    actor: MemberId,
    task: &Task,
    run: Option<&crate::server::domain::execution::Run>,
    message: Option<OutcomeMessage>,
    now: OffsetDateTime,
) -> Result<Option<Message>, ApplicationError> {
    let Some(message) = message else {
        return Ok(None);
    };
    let thread_id = match message.post_to {
        TaskPostTarget::Focus => match run {
            Some(run) => run.focus_thread_id,
            None => task.source_thread_id,
        },
        TaskPostTarget::Source => task.source_thread_id,
        TaskPostTarget::Thread(thread_id) => thread_id,
    };
    if !task.linked_to(thread_id) || !transaction.can_read_thread(actor, thread_id).await? {
        return Err(ApplicationError::PermissionDenied);
    }
    Ok(Some(Message {
        id: message.message_id,
        thread_id,
        author_member_id: actor,
        placement: MessagePlacement::Reply,
        content: MessageContent::Text(message.body_markdown),
        created_at: now,
        edited_at: None,
        deleted_at: None,
    }))
}

pub(in crate::server) struct LinkThreadInput {
    pub(in crate::server) task_id: TaskId,
    pub(in crate::server) target_thread_id: ThreadId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) now: OffsetDateTime,
}

/// Task changes that publish no Message and reach no terminal state. Terminal outcomes belong to
/// [`RecordTaskOutcome`], so `done`, `submit_review` and `close` are absent here.
pub(in crate::server) enum TaskAction {
    Rename { title: String },
    Start { assignee: MemberId },
    RequestChanges,
    ResetSession,
}

impl TaskAction {
    fn name(&self) -> &'static str {
        match self {
            Self::Rename { .. } => "task.rename",
            Self::Start { .. } => "task.start",
            Self::RequestChanges => "task.request_changes",
            Self::ResetSession => "task.reset_session",
        }
    }
}

pub(in crate::server) struct UpdateTaskInput {
    pub(in crate::server) task_id: TaskId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) action: TaskAction,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct UpdateTask;

impl UpdateTask {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: UpdateTaskInput,
    ) -> Result<Task, ApplicationError> {
        port.transact(async |transaction| {
            let action_name = input.action.name();
            if let Some(task_id) = transaction
                .task_for_idempotency(input.actor_member_id, action_name, input.idempotency_key)
                .await?
            {
                return transaction.task(task_id).await;
            }
            let mut task = transaction.task(input.task_id).await?;
            let source = transaction.thread(task.source_thread_id).await?;
            if !transaction
                .can_read_thread(input.actor_member_id, source.id)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            let task_changed = match input.action {
                TaskAction::Rename { title } => {
                    task.rename(title, input.now);
                    true
                }
                TaskAction::Start { assignee } => {
                    if !transaction.can_assign_agent(assignee, &source).await? {
                        return Err(ApplicationError::PermissionDenied);
                    }
                    task.start(assignee, input.now)?;
                    true
                }
                TaskAction::RequestChanges => {
                    task.return_from_review(input.actor_member_id, true, input.now)?;
                    true
                }
                TaskAction::ResetSession => {
                    if task.status.is_finished()
                        || !transaction
                            .can_govern_task(input.actor_member_id, &task)
                            .await?
                    {
                        return Err(ApplicationError::PermissionDenied);
                    }
                    transaction.emit(Effect::SessionReset(task.id));
                    false
                }
            };
            if task_changed {
                transaction.save_task(task.clone()).await?;
                transaction.emit(Effect::TaskUpdated(task.id));
            }
            transaction
                .record_task_idempotency(
                    input.actor_member_id,
                    action_name,
                    input.idempotency_key,
                    task.id,
                )
                .await?;
            Ok(task)
        })
        .await
    }
}

pub(in crate::server) struct LinkThreadToTask;

impl LinkThreadToTask {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: LinkThreadInput,
    ) -> Result<Task, ApplicationError> {
        port.transact(async |transaction| {
            if let Some(task_id) = transaction
                .task_for_idempotency(
                    input.actor_member_id,
                    "task.link_thread",
                    input.idempotency_key,
                )
                .await?
            {
                return transaction.task(task_id).await;
            }
            let mut task = transaction.task(input.task_id).await?;
            let source = transaction.thread(task.source_thread_id).await?;
            let target = transaction.thread(input.target_thread_id).await?;
            if !transaction
                .can_link_thread(input.actor_member_id, &task, &target)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            for link in &task.related_threads {
                if !transaction
                    .can_read_thread(input.actor_member_id, link.thread_id)
                    .await?
                {
                    return Err(ApplicationError::PermissionDenied);
                }
            }
            if transaction
                .unfinished_task_for_thread(target.id)
                .await?
                .is_some_and(|task_id| task_id != task.id)
            {
                return Err(ApplicationError::Conflict);
            }
            if !task.linked_to(target.id) {
                task.add_related_thread(&source, &target, input.actor_member_id, input.now)?;
                transaction.save_task(task.clone()).await?;
                transaction.emit(Effect::ThreadLinked {
                    task_id: task.id,
                    thread_id: target.id,
                });
            }
            transaction
                .record_task_idempotency(
                    input.actor_member_id,
                    "task.link_thread",
                    input.idempotency_key,
                    task.id,
                )
                .await?;
            Ok(task)
        })
        .await
    }
}

pub(in crate::server) struct UnlinkThreadFromTask;

impl UnlinkThreadFromTask {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: LinkThreadInput,
    ) -> Result<Task, ApplicationError> {
        port.transact(async |transaction| {
            if let Some(task_id) = transaction
                .task_for_idempotency(
                    input.actor_member_id,
                    "task.unlink_thread",
                    input.idempotency_key,
                )
                .await?
            {
                return transaction.task(task_id).await;
            }
            let mut task = transaction.task(input.task_id).await?;
            let target = transaction.thread(input.target_thread_id).await?;
            if !transaction
                .can_link_thread(input.actor_member_id, &task, &target)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            if task.linked_to(target.id) {
                task.remove_related_thread(target.id, input.now)?;
                transaction.save_task(task.clone()).await?;
                transaction.emit(Effect::ThreadUnlinked {
                    task_id: task.id,
                    thread_id: target.id,
                });
            }
            transaction
                .record_task_idempotency(
                    input.actor_member_id,
                    "task.unlink_thread",
                    input.idempotency_key,
                    task.id,
                )
                .await?;
            Ok(task)
        })
        .await
    }
}
