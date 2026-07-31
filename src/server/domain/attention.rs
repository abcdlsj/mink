use time::{Duration, OffsetDateTime};

use crate::ids::{InboxItemId, MemberId, MessageId, RunId, SpaceId, TaskId, ThreadId};

use super::DomainError;

/// Limits the Server applies when routing attention. The domain owns them so the enforced values and
/// the `attention_config` projection cannot drift apart.
pub(in crate::server) struct AttentionPolicy;

impl AttentionPolicy {
    /// Failed delivery attempts an Inbox Item survives before it is retired as `dead`.
    pub(in crate::server) const MAX_RETRY_COUNT: u32 = 5;
    /// Quiet period a Thread must reach before its aggregated ambient activity becomes available.
    /// Each new Message restarts it, so a busy Thread is read once instead of once per Message.
    pub(in crate::server) const AMBIENT_DEBOUNCE_SECONDS: u32 = 30;
    /// Longest an ambient aggregate stays unavailable after it opens.
    pub(in crate::server) const AMBIENT_MAX_WAIT_SECONDS: u32 = 300;

    fn ambient_debounce() -> Duration {
        Duration::seconds(i64::from(Self::AMBIENT_DEBOUNCE_SECONDS))
    }

    fn ambient_max_wait() -> Duration {
        Duration::seconds(i64::from(Self::AMBIENT_MAX_WAIT_SECONDS))
    }
}

/// Ambient activity for one Agent and one Thread collapsed into a single attention fact. The
/// aggregate covers a Message sequence range rather than one Message, so the Item carries no source
/// Message. The Item's own `available_at` remains the single scheduling fact; this struct adds only
/// the range and the deadline.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct AmbientAggregate {
    pub(in crate::server) first_message_seq: u64,
    pub(in crate::server) last_message_seq: u64,
    pub(in crate::server) aggregated_count: u32,
    /// Deadline fixed when the aggregate opens. No later Message moves it, which is what stops a
    /// continuously busy Thread from postponing the Agent's read forever.
    pub(in crate::server) force_at: OffsetDateTime,
}

impl AmbientAggregate {
    fn opened_at(message_seq: u64, now: OffsetDateTime) -> Self {
        Self {
            first_message_seq: message_seq,
            last_message_seq: message_seq,
            aggregated_count: 1,
            force_at: now + AttentionPolicy::ambient_max_wait(),
        }
    }

    /// When the accumulated activity becomes available to claim. The debounce restarts on each new
    /// Message, and `force_at` caps the result, so the total wait is bounded regardless of volume.
    fn debounced_available_at(&self, now: OffsetDateTime) -> OffsetDateTime {
        (now + AttentionPolicy::ambient_debounce()).min(self.force_at)
    }
}

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
    /// Present only on ambient Items, which stand for a range of Messages instead of one.
    pub(in crate::server) ambient: Option<AmbientAggregate>,
}

impl InboxItem {
    /// Opens an ambient aggregate for one Agent and Thread. The Item stays unavailable until the
    /// debounce elapses, so a Thread that keeps receiving Messages is read once, not once per
    /// Message.
    pub(in crate::server) fn open_ambient(
        id: InboxItemId,
        space_id: SpaceId,
        agent_id: MemberId,
        thread_id: ThreadId,
        kind: InboxItemKind,
        message_seq: u64,
        now: OffsetDateTime,
    ) -> Self {
        let ambient = AmbientAggregate::opened_at(message_seq, now);
        Self {
            id,
            space_id,
            agent_id,
            message_id: None,
            thread_id,
            task_id: None,
            kind,
            strength: AttentionStrength::Ambient,
            status: InboxItemStatus::Pending,
            available_at: ambient.debounced_available_at(now),
            lease_run_id: None,
            lease_expires_at: None,
            retry_count: 0,
            handled_at: None,
            ambient: Some(ambient),
        }
    }

    /// Folds one more ambient Message into this open aggregate. Returns the Item's new
    /// `available_at`, which never exceeds the `force_at` set when the aggregate opened.
    pub(in crate::server) fn absorb_ambient_message(
        &mut self,
        message_seq: u64,
        now: OffsetDateTime,
    ) -> Result<OffsetDateTime, DomainError> {
        if self.status != InboxItemStatus::Pending {
            return Err(DomainError::InvalidTransition);
        }
        let ambient = self
            .ambient
            .as_mut()
            .ok_or(DomainError::ItemIsNotAmbientAggregate)?;
        if message_seq <= ambient.last_message_seq {
            return Err(DomainError::InvalidTransition);
        }
        ambient.last_message_seq = message_seq;
        ambient.aggregated_count += 1;
        self.available_at = ambient.debounced_available_at(now);
        Ok(self.available_at)
    }

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
            ambient: None,
        }
    }

    fn ambient_item(now: OffsetDateTime) -> InboxItem {
        InboxItem::open_ambient(
            InboxItemId::from_uuid(uuid::Uuid::from_u128(1)),
            SpaceId::from_uuid(uuid::Uuid::from_u128(2)),
            MemberId::from_uuid(uuid::Uuid::from_u128(3)),
            ThreadId::from_uuid(uuid::Uuid::from_u128(4)),
            InboxItemKind::ChannelActivity,
            10,
            now,
        )
    }

    #[test]
    fn new_ambient_messages_cannot_postpone_the_force_deadline() {
        let opened_at = OffsetDateTime::UNIX_EPOCH;
        let mut item = ambient_item(opened_at);
        let force_at = item.ambient.expect("an ambient Item aggregates").force_at;
        assert_eq!(
            force_at,
            opened_at + Duration::seconds(i64::from(AttentionPolicy::AMBIENT_MAX_WAIT_SECONDS))
        );

        // A Message arriving every debounce period restarts the quiet period each time. Without the
        // cap this walks available_at forward forever; with it, the Item is claimable by force_at.
        let step = Duration::seconds(i64::from(AttentionPolicy::AMBIENT_DEBOUNCE_SECONDS));
        let mut sent_at = opened_at;
        for sequence in 11..=40 {
            sent_at += step;
            let available_at = item
                .absorb_ambient_message(sequence, sent_at)
                .expect("a pending aggregate absorbs later Messages");
            assert!(
                available_at <= force_at,
                "sequence {sequence} pushed availability past the deadline"
            );
        }
        let ambient = item.ambient.expect("an ambient Item aggregates");
        assert_eq!(ambient.force_at, force_at, "the deadline never moves");
        assert_eq!(
            (ambient.first_message_seq, ambient.last_message_seq),
            (10, 40)
        );
        assert_eq!(ambient.aggregated_count, 31);
        assert_eq!(
            item.available_at, force_at,
            "past the deadline the aggregate is available immediately"
        );
    }

    #[test]
    fn an_ambient_aggregate_debounces_while_it_stays_below_the_deadline() {
        let opened_at = OffsetDateTime::UNIX_EPOCH;
        let mut item = ambient_item(opened_at);
        let debounce = Duration::seconds(i64::from(AttentionPolicy::AMBIENT_DEBOUNCE_SECONDS));
        assert_eq!(item.available_at, opened_at + debounce);

        let second = opened_at + Duration::seconds(5);
        assert_eq!(
            item.absorb_ambient_message(11, second),
            Ok(second + debounce),
            "each Message restarts the quiet period"
        );
    }

    #[test]
    fn only_a_pending_aggregate_absorbs_later_messages() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let run_id = RunId::from_uuid(uuid::Uuid::from_u128(9));

        // A replayed or out-of-order sequence must not inflate the count.
        let mut item = ambient_item(now);
        assert_eq!(
            item.absorb_ambient_message(10, now),
            Err(DomainError::InvalidTransition)
        );
        assert_eq!(item.ambient.expect("aggregates").aggregated_count, 1);

        // Once claimed, the aggregate is frozen: the Agent already received this range.
        let mut leased = ambient_item(now);
        leased
            .lease_for_run(run_id, now + Duration::minutes(2))
            .expect("a pending Item can be leased");
        assert_eq!(
            leased.absorb_ambient_message(11, now),
            Err(DomainError::InvalidTransition)
        );

        // A hard Item is one Message, not a range.
        let mut hard = leased_item(run_id, 0);
        hard.status = InboxItemStatus::Pending;
        assert_eq!(
            hard.absorb_ambient_message(11, now),
            Err(DomainError::ItemIsNotAmbientAggregate)
        );
    }

    #[test]
    fn an_ambient_aggregate_never_attaches_to_an_active_run() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let run_id = RunId::from_uuid(uuid::Uuid::from_u128(9));
        let mut item = ambient_item(now);
        assert_eq!(
            item.attach_to_active_run(run_id, now + Duration::minutes(2)),
            Err(DomainError::AmbientItemCannotAttach),
            "ambient activity only aggregates; it does not interrupt a Run"
        );
        assert_eq!(item.status, InboxItemStatus::Pending);
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
