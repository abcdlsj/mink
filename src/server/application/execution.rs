use time::OffsetDateTime;

use crate::ids::{EventId, InboxItemId, MemberId, RunId, SpaceId, TaskId, ThreadId};

use crate::server::domain::{
    attention::InboxItemDisposition,
    execution::{Run, RunItem, RunOutcome, RunStatus},
};

use super::ports::{ApplicationError, Effect, ServerTransaction, TransactionPort};

pub(in crate::server) struct ClaimRunInput {
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) agent_id: MemberId,
    pub(in crate::server) task_id: Option<TaskId>,
    pub(in crate::server) focus_thread_id: ThreadId,
    pub(in crate::server) item_ids: Vec<InboxItemId>,
    pub(in crate::server) fencing_token_hash: String,
    pub(in crate::server) lease_expires_at: OffsetDateTime,
}

pub(in crate::server) struct ClaimRun;

impl ClaimRun {
    pub(in crate::server) fn execute<P: TransactionPort>(
        port: &mut P,
        input: ClaimRunInput,
    ) -> Result<Run, ApplicationError> {
        port.transact(|transaction| {
            match transaction.run(input.run_id) {
                Ok(run)
                    if run.agent_id == input.agent_id
                        && run.focus_thread_id == input.focus_thread_id
                        && run.task_id == input.task_id =>
                {
                    return Ok(run);
                }
                Ok(_) => return Err(ApplicationError::Conflict),
                Err(ApplicationError::NotFound) => {}
                Err(error) => return Err(error),
            }
            if transaction.active_run_for_agent(input.agent_id).is_some() {
                return Err(ApplicationError::Conflict);
            }
            if !transaction.can_read_thread(input.agent_id, input.focus_thread_id) {
                return Err(ApplicationError::PermissionDenied);
            }
            if let Some(task_id) = input.task_id {
                let task = transaction.task(task_id)?;
                if !task.linked_to(input.focus_thread_id) {
                    return Err(crate::server::domain::DomainError::FocusOutsideTask.into());
                }
            }
            let mut run = Run {
                id: input.run_id,
                space_id: input.space_id,
                agent_id: input.agent_id,
                task_id: input.task_id,
                focus_thread_id: input.focus_thread_id,
                status: RunStatus::Queued,
                fencing_token_hash: input.fencing_token_hash,
                lease_expires_at: input.lease_expires_at,
                items: Vec::new(),
                outcome: None,
                continuation_note: None,
                started_at: None,
                finished_at: None,
            };
            for (index, item_id) in input.item_ids.into_iter().enumerate() {
                let mut item = transaction.inbox_item(item_id)?;
                if item.agent_id != run.agent_id
                    || item.thread_id != run.focus_thread_id
                    || item.task_id != run.task_id
                {
                    return Err(crate::server::domain::DomainError::ItemScopeMismatch.into());
                }
                item.lease(run.id, run.lease_expires_at)?;
                run.items.push(RunItem {
                    inbox_item_id: item.id,
                    delivery_sequence: index as u64 + 1,
                    disposition: None,
                });
                transaction.save_inbox_item(item)?;
            }
            transaction.save_run(run.clone())?;
            transaction.emit(Effect::RunClaimed(run.id));
            Ok(run)
        })
    }
}

pub(in crate::server) struct ItemDispositionInput {
    pub(in crate::server) item_id: InboxItemId,
    pub(in crate::server) disposition: InboxItemDisposition,
}

pub(in crate::server) struct CompleteRunInput {
    pub(in crate::server) event_id: EventId,
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) fencing_token_hash: String,
    pub(in crate::server) outcome: RunOutcome,
    pub(in crate::server) item_dispositions: Vec<ItemDispositionInput>,
    pub(in crate::server) continuation_note: Option<String>,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct CompleteRun;

impl CompleteRun {
    pub(in crate::server) fn execute<P: TransactionPort>(
        port: &mut P,
        input: CompleteRunInput,
    ) -> Result<Run, ApplicationError> {
        port.transact(|transaction| {
            if let Some(run_id) = transaction.completed_run_for_event(input.event_id) {
                return transaction.run(run_id);
            }
            let mut run = transaction.run(input.run_id)?;
            run.begin_finalizing(&input.fencing_token_hash)?;
            for item_input in input.item_dispositions {
                run.set_item_disposition(item_input.item_id, item_input.disposition)?;
                let mut item = transaction.inbox_item(item_input.item_id)?;
                item.apply_disposition(run.id, item_input.disposition, input.now)?;
                transaction.save_inbox_item(item)?;
            }
            run.finish(
                &input.fencing_token_hash,
                input.outcome,
                input.continuation_note,
                input.now,
            )?;
            transaction.save_run(run.clone())?;
            transaction.record_completed_run_event(input.event_id, run.id)?;
            transaction.emit(Effect::RunCompleted(run.id));
            Ok(run)
        })
    }
}
