use time::OffsetDateTime;

use crate::ids::{IdempotencyKey, MemberId, MessageId, RunId, TaskId, ThreadId};

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
    pub(in crate::server) fn execute<P: TransactionPort>(
        port: &mut P,
        input: CreateTaskInput,
    ) -> Result<Task, ApplicationError> {
        port.transact(|transaction| {
            if let Some(task_id) =
                transaction.task_for_idempotency(input.actor_member_id, input.idempotency_key)
            {
                return transaction.task(task_id);
            }
            let (source_thread_id, running_agent) = match input.source {
                TaskSource::HumanRoot(thread_id) => (thread_id, None),
                TaskSource::AgentRun(run_id) => {
                    let run = transaction.run(run_id)?;
                    if run.agent_id != input.actor_member_id || run.status != RunStatus::Running {
                        return Err(ApplicationError::ContextChanged);
                    }
                    (run.focus_thread_id, Some(run))
                }
            };
            let source = transaction.thread(source_thread_id)?;
            if !transaction.can_read_thread(input.actor_member_id, source.id) {
                return Err(ApplicationError::PermissionDenied);
            }
            if transaction.task_for_source(source.id).is_some() {
                return Err(ApplicationError::Conflict);
            }
            let root = transaction.root_message(source.id)?;

            let assignee = running_agent
                .as_ref()
                .map(|run| input.assignee_agent_member_id.unwrap_or(run.agent_id))
                .or(input.assignee_agent_member_id);
            if assignee.is_some_and(|agent| !transaction.can_assign_agent(agent, &source)) {
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

            if let Some(mut run) = running_agent {
                run.bind_task(&task)?;
                for run_item in &run.items {
                    let mut item = transaction.inbox_item(run_item.inbox_item_id)?;
                    item.bind_task(task.id)?;
                    transaction.save_inbox_item(item)?;
                }
                transaction.save_run(run.clone())?;
                transaction.emit(Effect::RunTaskBound {
                    run_id: run.id,
                    task_id: task.id,
                });
            }
            transaction.insert_task(task.clone())?;
            transaction.record_task_idempotency(
                input.actor_member_id,
                input.idempotency_key,
                task.id,
            )?;
            transaction.emit(Effect::TaskCreated(task.id));
            Ok(task)
        })
    }
}

pub(in crate::server) struct LinkThreadInput {
    pub(in crate::server) task_id: TaskId,
    pub(in crate::server) target_thread_id: ThreadId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) enum TaskAction {
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
    pub(in crate::server) fn execute<P: TransactionPort>(
        port: &mut P,
        input: UpdateTaskInput,
    ) -> Result<Task, ApplicationError> {
        port.transact(|transaction| {
            let mut task = transaction.task(input.task_id)?;
            let source = transaction.thread(task.source_thread_id)?;
            if !transaction.can_read_thread(input.actor_member_id, source.id) {
                return Err(ApplicationError::PermissionDenied);
            }
            match input.action {
                TaskAction::Start { assignee } => {
                    if !transaction.can_assign_agent(assignee, &source) {
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
                        || transaction.can_govern_task(input.actor_member_id, &task);
                    if !allowed {
                        return Err(ApplicationError::PermissionDenied);
                    }
                    task.close(reason, note, input.now)?;
                }
            }
            transaction.save_task(task.clone())?;
            transaction.emit(Effect::TaskUpdated(task.id));
            Ok(task)
        })
    }
}

pub(in crate::server) struct LinkThreadToTask;

impl LinkThreadToTask {
    pub(in crate::server) fn execute<P: TransactionPort>(
        port: &mut P,
        input: LinkThreadInput,
    ) -> Result<Task, ApplicationError> {
        port.transact(|transaction| {
            let mut task = transaction.task(input.task_id)?;
            let source = transaction.thread(task.source_thread_id)?;
            let target = transaction.thread(input.target_thread_id)?;
            if !transaction.can_link_thread(input.actor_member_id, &task, &target) {
                return Err(ApplicationError::PermissionDenied);
            }
            if task
                .related_threads
                .iter()
                .any(|link| !transaction.can_read_thread(input.actor_member_id, link.thread_id))
            {
                return Err(ApplicationError::PermissionDenied);
            }
            if transaction
                .unfinished_task_for_thread(target.id)
                .is_some_and(|task_id| task_id != task.id)
            {
                return Err(ApplicationError::Conflict);
            }
            if task.linked_to(target.id) {
                return Ok(task);
            }
            task.add_related_thread(&source, &target, input.actor_member_id, input.now)?;
            transaction.save_task(task.clone())?;
            transaction.emit(Effect::ThreadLinked {
                task_id: task.id,
                thread_id: target.id,
            });
            Ok(task)
        })
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
    pub(in crate::server) fn execute<P: TransactionPort>(
        port: &mut P,
        input: CompleteTaskInput,
    ) -> Result<Task, ApplicationError> {
        port.transact(|transaction| {
            let mut task = transaction.task(input.task_id)?;
            if task.result_message_id == Some(input.result_message_id)
                && task.status == crate::server::domain::task::TaskStatus::Done
            {
                return Ok(task);
            }
            if !task.linked_to(input.result_thread_id)
                || !transaction.can_read_thread(input.actor_member_id, input.result_thread_id)
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
            transaction.insert_message(result)?;
            transaction.save_task(task.clone())?;
            transaction.emit(Effect::TaskCompleted {
                task_id: task.id,
                result_message_id: input.result_message_id,
            });
            transaction.emit(Effect::SessionClose(task.id));
            Ok(task)
        })
    }
}
