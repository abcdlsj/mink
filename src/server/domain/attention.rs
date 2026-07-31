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

    pub(in crate::server) fn lease_for_run(
        &mut self,
        run_id: RunId,
        expires_at: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.status != InboxItemStatus::Pending {
            return Err(DomainError::InvalidTransition);
        }
        self.status = InboxItemStatus::Leased;
        self.lease_run_id = Some(run_id);
        self.lease_expires_at = Some(expires_at);
        Ok(())
    }

    pub(in crate::server) fn attach_to_active_run(
        &mut self,
        run_id: RunId,
        expires_at: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.strength != AttentionStrength::Hard {
            return Err(DomainError::AmbientItemCannotAttach);
        }
        self.lease_for_run(run_id, expires_at)
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

    /// Releases a lease the owning Run never resolved, because that Run's lease expired. Counts one
    /// failed attempt and retires the Item once it exhausts `max_retry_count`, so a source the Agent
    /// cannot process stops being retried forever.
    ///
    /// This is the only transition that raises `retry_count`: a `Released` disposition is the Agent
    /// deciding not to handle the Item, which is not a failed attempt.
    pub(in crate::server) fn reclaim_expired_lease(
        &mut self,
        run_id: RunId,
        max_retry_count: u32,
        now: OffsetDateTime,
    ) -> Result<InboxItemStatus, DomainError> {
        if self.status != InboxItemStatus::Leased || self.lease_run_id != Some(run_id) {
            return Err(DomainError::InvalidTransition);
        }
        self.lease_run_id = None;
        self.lease_expires_at = None;
        self.retry_count += 1;
        self.status = if self.retry_count > max_retry_count {
            InboxItemStatus::Dead
        } else {
            self.available_at = now;
            InboxItemStatus::Pending
        };
        Ok(self.status)
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

#[cfg(test)]
mod tests {
    use super::*;

    fn leased_item(run_id: RunId, retry_count: u32) -> InboxItem {
        InboxItem {
            id: InboxItemId::from_uuid(uuid::Uuid::from_u128(1)),
            space_id: SpaceId::from_uuid(uuid::Uuid::from_u128(2)),
            agent_id: MemberId::from_uuid(uuid::Uuid::from_u128(3)),
            message_id: None,
            thread_id: ThreadId::from_uuid(uuid::Uuid::from_u128(4)),
            task_id: None,
            kind: InboxItemKind::Mention,
            strength: AttentionStrength::Hard,
            status: InboxItemStatus::Leased,
            available_at: OffsetDateTime::UNIX_EPOCH,
            lease_run_id: Some(run_id),
            lease_expires_at: Some(OffsetDateTime::UNIX_EPOCH),
            retry_count,
            handled_at: None,
        }
    }

    #[test]
    fn reclaiming_a_lease_retries_until_the_limit_then_retires_the_item() {
        let run_id = RunId::from_uuid(uuid::Uuid::from_u128(9));
        let now = OffsetDateTime::UNIX_EPOCH + time::Duration::hours(3);

        // Below the limit the Item returns to the queue and becomes available immediately.
        let mut item = leased_item(run_id, 0);
        assert_eq!(
            item.reclaim_expired_lease(run_id, 2, now),
            Ok(InboxItemStatus::Pending)
        );
        assert_eq!(item.retry_count, 1);
        assert_eq!(item.available_at, now);
        assert_eq!(item.lease_run_id, None);
        assert_eq!(item.lease_expires_at, None);

        // Reaching the limit still retries; only exceeding it retires the Item.
        let mut item = leased_item(run_id, 1);
        assert_eq!(
            item.reclaim_expired_lease(run_id, 2, now),
            Ok(InboxItemStatus::Pending)
        );
        let mut item = leased_item(run_id, 2);
        assert_eq!(
            item.reclaim_expired_lease(run_id, 2, now),
            Ok(InboxItemStatus::Dead)
        );
        assert_eq!(item.retry_count, 3);
        assert_eq!(item.lease_run_id, None);
    }

    #[test]
    fn only_the_owning_run_can_reclaim_a_lease() {
        let owner = RunId::from_uuid(uuid::Uuid::from_u128(9));
        let stranger = RunId::from_uuid(uuid::Uuid::from_u128(10));
        let now = OffsetDateTime::UNIX_EPOCH;

        let mut item = leased_item(owner, 0);
        assert_eq!(
            item.reclaim_expired_lease(stranger, 5, now),
            Err(DomainError::InvalidTransition)
        );
        assert_eq!(item.retry_count, 0, "a foreign Run does not spend a retry");

        // A Item the Agent already resolved is no longer leased, so expiry cannot count against it.
        let mut handled = leased_item(owner, 0);
        handled
            .apply_disposition(owner, InboxItemDisposition::Handled, now)
            .expect("the owning Run resolves its Item");
        assert_eq!(
            handled.reclaim_expired_lease(owner, 5, now),
            Err(DomainError::InvalidTransition)
        );
        assert_eq!(handled.status, InboxItemStatus::Handled);
        assert_eq!(handled.retry_count, 0);
    }

    #[test]
    fn a_released_disposition_is_not_a_failed_attempt() {
        let run_id = RunId::from_uuid(uuid::Uuid::from_u128(9));
        let mut item = leased_item(run_id, 0);
        item.apply_disposition(
            run_id,
            InboxItemDisposition::Released,
            OffsetDateTime::UNIX_EPOCH,
        )
        .expect("an Agent may release an Item it chose not to handle");
        assert_eq!(item.status, InboxItemStatus::Pending);
        assert_eq!(
            item.retry_count, 0,
            "declining to handle an Item is a decision, not a failure"
        );
    }
}
