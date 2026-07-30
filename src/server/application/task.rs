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
}

pub(in crate::server) enum FinishAgentTaskAction {
    SubmitReview {
        message_id: MessageId,
        body: String,
        post_to: TaskPostTarget,
    },
    Done {
        message_id: MessageId,
        result: String,
        post_to: TaskPostTarget,
    },
    Close {
        reason: CloseReason,
        note: Option<String>,
    },
}

impl FinishAgentTaskAction {
    fn action_name(&self) -> &'static str {
        match self {
            Self::SubmitReview { .. } => "task.submit_review",
            Self::Done { .. } => "task.done",
            Self::Close { .. } => "task.close",
        }
    }
}

pub(in crate::server) struct FinishAgentTaskInput {
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) fencing_token_hash: String,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) message_snapshot_sequence: u64,
    pub(in crate::server) action: FinishAgentTaskAction,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct FinishAgentTaskRun;

impl FinishAgentTaskRun {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: FinishAgentTaskInput,
    ) -> Result<Task, ApplicationError> {
        port.transact(async |transaction| {
            let action_name = input.action.action_name();
            if let Some(task_id) = transaction
                .task_for_idempotency(input.actor_member_id, action_name, input.idempotency_key)
                .await?
            {
                return transaction.task(task_id).await;
            }

            let mut run = transaction.run(input.run_id).await?;
            if run.agent_id != input.actor_member_id
                || !transaction
                    .can_operate_agent(input.computer_id, run.agent_id)
                    .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            run.validate_fencing(&input.fencing_token_hash)?;
            let task_id = run.task_id.ok_or(ApplicationError::ContextChanged)?;
            let mut task = transaction.task(task_id).await?;
            if transaction
                .thread_message_sequence(run.focus_thread_id)
                .await?
                != input.message_snapshot_sequence
            {
                return Err(ApplicationError::ContextChanged);
            }

            let message = match input.action {
                FinishAgentTaskAction::SubmitReview {
                    message_id,
                    body,
                    post_to,
                } => {
                    let thread_id = post_thread(&task, &run, post_to);
                    ensure_post_allowed(transaction, input.actor_member_id, &task, thread_id)
                        .await?;
                    task.request_review(input.actor_member_id, input.now)?;
                    Some(Message {
                        id: message_id,
                        thread_id,
                        author_member_id: input.actor_member_id,
                        placement: MessagePlacement::Reply,
                        content: MessageContent::Text(body),
                        created_at: input.now,
                    })
                }
                FinishAgentTaskAction::Done {
                    message_id,
                    result,
                    post_to,
                } => {
                    let thread_id = post_thread(&task, &run, post_to);
                    ensure_post_allowed(transaction, input.actor_member_id, &task, thread_id)
                        .await?;
                    task.finish(input.actor_member_id, true, message_id, input.now)?;
                    Some(Message {
                        id: message_id,
                        thread_id,
                        author_member_id: input.actor_member_id,
                        placement: MessagePlacement::Reply,
                        content: MessageContent::Text(result),
                        created_at: input.now,
                    })
                }
                FinishAgentTaskAction::Close { reason, note } => {
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

            run.begin_finalizing(&input.fencing_token_hash)?;
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
                &input.fencing_token_hash,
                crate::server::domain::execution::RunOutcome::Completed,
                None,
                input.now,
            )?;

            if let Some(message) = message {
                let message_id = message.id;
                transaction.insert_message(message).await?;
                transaction.emit(Effect::MessageCreated(message_id));
            }
            transaction.save_task(task.clone()).await?;
            transaction.save_run(run.clone()).await?;
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
            transaction.emit(Effect::RunCompleted(run.id));
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
                _ => unreachable!("agent Task action has a stable name"),
            }
            Ok(task)
        })
        .await
    }
}

fn post_thread(
    task: &Task,
    run: &crate::server::domain::execution::Run,
    target: TaskPostTarget,
) -> ThreadId {
    match target {
        TaskPostTarget::Focus => run.focus_thread_id,
        TaskPostTarget::Source => task.source_thread_id,
    }
}

async fn ensure_post_allowed(
    transaction: &mut impl ServerTransaction,
    actor: MemberId,
    task: &Task,
    thread_id: ThreadId,
) -> Result<(), ApplicationError> {
    if !task.linked_to(thread_id) || !transaction.can_read_thread(actor, thread_id).await? {
        return Err(ApplicationError::PermissionDenied);
    }
    Ok(())
}

pub(in crate::server) struct LinkThreadInput {
    pub(in crate::server) task_id: TaskId,
    pub(in crate::server) target_thread_id: ThreadId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) enum TaskAction {
    Rename {
        title: String,
    },
    Start {
        assignee: MemberId,
    },
    SubmitReview,
    RequestChanges,
    Close {
        reason: CloseReason,
        note: Option<String>,
    },
}

pub(in crate::server) struct UpdateTaskInput {
    pub(in crate::server) task_id: TaskId,
    pub(in crate::server) actor_member_id: MemberId,
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
            let mut task = transaction.task(input.task_id).await?;
            let source = transaction.thread(task.source_thread_id).await?;
            if !transaction
                .can_read_thread(input.actor_member_id, source.id)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            match input.action {
                TaskAction::Rename { title } => task.rename(title, input.now),
                TaskAction::Start { assignee } => {
                    if !transaction.can_assign_agent(assignee, &source).await? {
                        return Err(ApplicationError::PermissionDenied);
                    }
                    task.start(assignee, input.now)?;
                }
                TaskAction::SubmitReview => {
                    task.request_review(input.actor_member_id, input.now)?;
                }
                TaskAction::RequestChanges => {
                    task.return_from_review(input.actor_member_id, true, input.now)?;
                }
                TaskAction::Close { reason, note } => {
                    let allowed = task.creator_member_id == input.actor_member_id
                        || task.assignee_agent_member_id == Some(input.actor_member_id)
                        || transaction
                            .can_govern_task(input.actor_member_id, &task)
                            .await?;
                    if !allowed {
                        return Err(ApplicationError::PermissionDenied);
                    }
                    task.close(reason, note, input.now)?;
                }
            }
            transaction.save_task(task.clone()).await?;
            transaction.emit(Effect::TaskUpdated(task.id));
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
            if task.linked_to(target.id) {
                return Ok(task);
            }
            task.add_related_thread(&source, &target, input.actor_member_id, input.now)?;
            transaction.save_task(task.clone()).await?;
            transaction.emit(Effect::ThreadLinked {
                task_id: task.id,
                thread_id: target.id,
            });
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
            let mut task = transaction.task(input.task_id).await?;
            let target = transaction.thread(input.target_thread_id).await?;
            if !transaction
                .can_link_thread(input.actor_member_id, &task, &target)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            if !task.linked_to(target.id) {
                return Ok(task);
            }
            task.remove_related_thread(target.id, input.now)?;
            transaction.save_task(task.clone()).await?;
            transaction.emit(Effect::ThreadUnlinked {
                task_id: task.id,
                thread_id: target.id,
            });
            Ok(task)
        })
        .await
    }
}

pub(in crate::server) struct CompleteTaskInput {
    pub(in crate::server) task_id: TaskId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) result_message_id: MessageId,
    pub(in crate::server) result_thread_id: ThreadId,
    pub(in crate::server) result_markdown: String,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct CompleteTask;

impl CompleteTask {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: CompleteTaskInput,
    ) -> Result<Task, ApplicationError> {
        port.transact(async |transaction| {
            let mut task = transaction.task(input.task_id).await?;
            if task.result_message_id == Some(input.result_message_id)
                && task.status == crate::server::domain::task::TaskStatus::Done
            {
                return Ok(task);
            }
            if !task.linked_to(input.result_thread_id)
                || !transaction
                    .can_read_thread(input.actor_member_id, input.result_thread_id)
                    .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            let result = Message {
                id: input.result_message_id,
                thread_id: input.result_thread_id,
                author_member_id: input.actor_member_id,
                placement: MessagePlacement::Reply,
                content: MessageContent::Text(input.result_markdown),
                created_at: input.now,
            };
            task.finish(input.actor_member_id, true, result.id, input.now)?;
            transaction.insert_message(result).await?;
            transaction.save_task(task.clone()).await?;
            transaction.emit(Effect::TaskCompleted {
                task_id: task.id,
                result_message_id: input.result_message_id,
            });
            transaction.emit(Effect::SessionClose(task.id));
            Ok(task)
        })
        .await
    }
}
