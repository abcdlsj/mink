use time::OffsetDateTime;

use crate::ids::{InboxItemId, MemberId, RunId, SpaceId, TaskId, ThreadId};

use super::{
    DomainError,
    attention::{InboxItem, InboxItemDisposition},
    task::Task,
};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum RunStatus {
    Queued,
    Starting,
    Running,
    Finalizing,
    Completed,
    Yielded,
    Failed,
    Stopping,
    Canceled,
}

impl RunStatus {
    #[cfg(test)]
    pub(in crate::server) fn is_active(self) -> bool {
        !matches!(
            self,
            Self::Completed | Self::Yielded | Self::Failed | Self::Canceled
        )
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum RunOutcome {
    Completed,
    Yielded,
    Failed,
    Canceled,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct RunItem {
    pub(in crate::server) inbox_item_id: InboxItemId,
    pub(in crate::server) delivery_sequence: u64,
    pub(in crate::server) disposition: Option<InboxItemDisposition>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct Run {
    pub(in crate::server) id: RunId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) agent_id: MemberId,
    pub(in crate::server) task_id: Option<TaskId>,
    pub(in crate::server) focus_thread_id: ThreadId,
    pub(in crate::server) status: RunStatus,
    pub(in crate::server) fencing_token_hash: String,
    pub(in crate::server) lease_expires_at: OffsetDateTime,
    pub(in crate::server) items: Vec<RunItem>,
    pub(in crate::server) outcome: Option<RunOutcome>,
    pub(in crate::server) continuation_note: Option<String>,
    pub(in crate::server) started_at: Option<OffsetDateTime>,
    pub(in crate::server) finished_at: Option<OffsetDateTime>,
}

impl Run {
    pub(in crate::server) fn start(
        &mut self,
        fencing_token_hash: &str,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        self.validate_fencing(fencing_token_hash)?;
        if !matches!(self.status, RunStatus::Queued | RunStatus::Starting) {
            return Err(DomainError::InvalidTransition);
        }
        self.status = RunStatus::Running;
        self.started_at.get_or_insert(now);
        Ok(())
    }

    pub(in crate::server) fn bind_task(&mut self, task: &Task) -> Result<(), DomainError> {
        if self.task_id.is_some_and(|task_id| task_id != task.id)
            || !task.linked_to(self.focus_thread_id)
        {
            return Err(DomainError::FocusOutsideTask);
        }
        self.task_id = Some(task.id);
        Ok(())
    }

    pub(in crate::server) fn renew_lease(
        &mut self,
        fencing_token_hash: &str,
        expires_at: OffsetDateTime,
    ) -> Result<(), DomainError> {
        self.validate_fencing(fencing_token_hash)?;
        if self.status != RunStatus::Running || expires_at <= self.lease_expires_at {
            return Err(DomainError::InvalidTransition);
        }
        self.lease_expires_at = expires_at;
        Ok(())
    }

    pub(in crate::server) fn attach(&mut self, item: &InboxItem) -> Result<u64, DomainError> {
        if self.status != RunStatus::Running {
            return Err(DomainError::RunNotAcceptingItems);
        }
        if item.agent_id != self.agent_id
            || item.thread_id != self.focus_thread_id
            || item.task_id != self.task_id
        {
            return Err(DomainError::ItemScopeMismatch);
        }
        if let Some(existing) = self
            .items
            .iter()
            .find(|existing| existing.inbox_item_id == item.id)
        {
            return Ok(existing.delivery_sequence);
        }
        let sequence = self
            .items
            .last()
            .map_or(1, |item| item.delivery_sequence + 1);
        self.items.push(RunItem {
            inbox_item_id: item.id,
            delivery_sequence: sequence,
            disposition: None,
        });
        Ok(sequence)
    }

    pub(in crate::server) fn begin_finalizing(
        &mut self,
        fencing_token_hash: &str,
    ) -> Result<(), DomainError> {
        self.validate_fencing(fencing_token_hash)?;
        if self.status != RunStatus::Running {
            return Err(DomainError::InvalidTransition);
        }
        self.status = RunStatus::Finalizing;
        Ok(())
    }

    pub(in crate::server) fn cancel_for_agent_retirement(&mut self, now: OffsetDateTime) {
        for item in &mut self.items {
            item.disposition
                .get_or_insert(InboxItemDisposition::Released);
        }
        self.status = RunStatus::Canceled;
        self.outcome = Some(RunOutcome::Canceled);
        self.finished_at = Some(now);
    }

    pub(in crate::server) fn set_item_disposition(
        &mut self,
        item_id: InboxItemId,
        disposition: InboxItemDisposition,
    ) -> Result<(), DomainError> {
        let item = self
            .items
            .iter_mut()
            .find(|item| item.inbox_item_id == item_id)
            .ok_or(DomainError::ItemScopeMismatch)?;
        if item
            .disposition
            .is_some_and(|existing| existing != disposition)
        {
            return Err(DomainError::IncompleteItemDisposition);
        }
        item.disposition = Some(disposition);
        Ok(())
    }

    pub(in crate::server) fn finish(
        &mut self,
        fencing_token_hash: &str,
        outcome: RunOutcome,
        continuation_note: Option<String>,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        self.validate_fencing(fencing_token_hash)?;
        if self.status != RunStatus::Finalizing {
            return Err(DomainError::InvalidTransition);
        }
        if self.items.iter().any(|item| item.disposition.is_none()) {
            return Err(DomainError::IncompleteItemDisposition);
        }
        self.status = match outcome {
            RunOutcome::Completed => RunStatus::Completed,
            RunOutcome::Yielded => RunStatus::Yielded,
            RunOutcome::Failed => RunStatus::Failed,
            RunOutcome::Canceled => RunStatus::Canceled,
        };
        self.outcome = Some(outcome);
        self.continuation_note = continuation_note;
        self.finished_at = Some(now);
        Ok(())
    }

    pub(in crate::server) fn validate_fencing(&self, token_hash: &str) -> Result<(), DomainError> {
        if self.fencing_token_hash != token_hash {
            return Err(DomainError::StaleFencingToken);
        }
        Ok(())
    }
}
