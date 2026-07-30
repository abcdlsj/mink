use time::OffsetDateTime;

use crate::ids::{InboxItemId, MemberId, MessageId, RunId, SpaceId, TaskId, ThreadId};

use super::DomainError;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum InboxItemKind {
    Direct,
    Mention,
    Reply,
    TaskActivity,
    ThreadActivity,
    ChannelActivity,
    System,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum AttentionStrength {
    Hard,
    Ambient,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum InboxItemStatus {
    Pending,
    Leased,
    Deferred,
    Handled,
    Dead,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum InboxItemDisposition {
    Handled,
    Deferred,
    Released,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct InboxItem {
    pub(in crate::server) id: InboxItemId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) agent_id: MemberId,
    pub(in crate::server) message_id: Option<MessageId>,
    pub(in crate::server) thread_id: ThreadId,
    pub(in crate::server) task_id: Option<TaskId>,
    pub(in crate::server) kind: InboxItemKind,
    pub(in crate::server) strength: AttentionStrength,
    pub(in crate::server) status: InboxItemStatus,
    pub(in crate::server) available_at: OffsetDateTime,
    pub(in crate::server) lease_run_id: Option<RunId>,
    pub(in crate::server) lease_expires_at: Option<OffsetDateTime>,
    pub(in crate::server) retry_count: u32,
    pub(in crate::server) handled_at: Option<OffsetDateTime>,
}

impl InboxItem {
    pub(in crate::server) fn bind_task(&mut self, task_id: TaskId) -> Result<(), DomainError> {
        if self.task_id.is_some_and(|existing| existing != task_id) {
            return Err(DomainError::ItemScopeMismatch);
        }
        self.task_id = Some(task_id);
        Ok(())
    }

    pub(in crate::server) fn lease(
        &mut self,
        run_id: RunId,
        expires_at: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.strength != AttentionStrength::Hard {
            return Err(DomainError::AmbientItemCannotAttach);
        }
        if self.status != InboxItemStatus::Pending {
            return Err(DomainError::InvalidTransition);
        }
        self.status = InboxItemStatus::Leased;
        self.lease_run_id = Some(run_id);
        self.lease_expires_at = Some(expires_at);
        Ok(())
    }

    pub(in crate::server) fn apply_disposition(
        &mut self,
        run_id: RunId,
        disposition: InboxItemDisposition,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.status != InboxItemStatus::Leased || self.lease_run_id != Some(run_id) {
            return Err(DomainError::InvalidTransition);
        }
        match disposition {
            InboxItemDisposition::Handled => {
                self.status = InboxItemStatus::Handled;
                self.handled_at = Some(now);
            }
            InboxItemDisposition::Deferred => self.status = InboxItemStatus::Deferred,
            InboxItemDisposition::Released => self.status = InboxItemStatus::Pending,
        }
        self.lease_run_id = None;
        self.lease_expires_at = None;
        Ok(())
    }

    pub(in crate::server) fn renew_lease(
        &mut self,
        run_id: RunId,
        expires_at: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.status != InboxItemStatus::Leased
            || self.lease_run_id != Some(run_id)
            || self
                .lease_expires_at
                .is_none_or(|current| expires_at <= current)
        {
            return Err(DomainError::InvalidTransition);
        }
        self.lease_expires_at = Some(expires_at);
        Ok(())
    }

    pub(in crate::server) fn prepare_defer(
        &mut self,
        run_id: RunId,
        until: OffsetDateTime,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.status != InboxItemStatus::Leased
            || self.lease_run_id != Some(run_id)
            || until <= now
        {
            return Err(DomainError::InvalidTransition);
        }
        self.available_at = until;
        Ok(())
    }
}
