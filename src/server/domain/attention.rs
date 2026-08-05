use time::{Duration, OffsetDateTime};

use crate::ids::{InboxItemId, MemberId, MessageId, RunId, SpaceId, TaskId, ThreadId};

use super::DomainError;

/// Limits the Server applies when routing attention. The domain owns them so the enforced values and
/// the `attention_config` projection cannot drift apart.
pub(in crate::server) struct AttentionPolicy;

impl AttentionPolicy {
    /// Failed delivery attempts an Inbox Item survives before it is retired as `dead`.
    pub(in crate::server) const MAX_RETRY_COUNT: u32 = 5;
    /// Quiet period a Channel must reach before its aggregated ambient activity becomes available.
    /// Each new activity event restarts it, so a busy Channel is read once instead of once per event.
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
    first_message_seq: u64,
    last_message_seq: u64,
    aggregated_count: u32,
    /// Deadline fixed when the aggregate opens. No later Message moves it, which is what stops a
    /// continuously busy Thread from postponing the Agent's read forever.
    force_at: OffsetDateTime,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct AmbientAggregateView {
    pub(in crate::server) first_message_seq: u64,
    pub(in crate::server) last_message_seq: u64,
    pub(in crate::server) aggregated_count: u32,
    pub(in crate::server) force_at: OffsetDateTime,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct AmbientAggregateSnapshot {
    pub(in crate::server) first_message_seq: u64,
    pub(in crate::server) last_message_seq: u64,
    pub(in crate::server) aggregated_count: u32,
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
    /// activity event, and `force_at` caps the result, so the total wait is bounded regardless of volume.
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
    Assigned,
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
    id: InboxItemId,
    space_id: SpaceId,
    member_id: MemberId,
    message_id: Option<MessageId>,
    thread_id: ThreadId,
    task_id: Option<TaskId>,
    kind: InboxItemKind,
    strength: AttentionStrength,
    status: InboxItemStatus,
    available_at: OffsetDateTime,
    /// The Run currently processing this Item. A plain reference, not a lease: it carries no expiry,
    /// because nothing reclaims an Item on a timer. The Item returns to the queue when the owning
    /// Computer reports the Run's outcome.
    assigned_run_id: Option<RunId>,
    retry_count: u32,
    /// Times a governor returned this Item from `dead` to the queue. Retained across those returns so
    /// a repeatedly failing source stays distinguishable from a fresh Item.
    requeue_count: u32,
    handled_at: Option<OffsetDateTime>,
    /// Present only on ambient Items, which stand for a range of Messages instead of one.
    ambient: Option<AmbientAggregate>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct InboxItemView {
    pub(in crate::server) id: InboxItemId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) member_id: MemberId,
    pub(in crate::server) message_id: Option<MessageId>,
    pub(in crate::server) thread_id: ThreadId,
    pub(in crate::server) task_id: Option<TaskId>,
    pub(in crate::server) kind: InboxItemKind,
    pub(in crate::server) strength: AttentionStrength,
    pub(in crate::server) status: InboxItemStatus,
    pub(in crate::server) available_at: OffsetDateTime,
    pub(in crate::server) assigned_run_id: Option<RunId>,
    pub(in crate::server) retry_count: u32,
    pub(in crate::server) requeue_count: u32,
    pub(in crate::server) handled_at: Option<OffsetDateTime>,
    pub(in crate::server) ambient: Option<AmbientAggregateView>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct InboxItemSnapshot {
    pub(in crate::server) id: InboxItemId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) member_id: MemberId,
    pub(in crate::server) message_id: Option<MessageId>,
    pub(in crate::server) thread_id: ThreadId,
    pub(in crate::server) task_id: Option<TaskId>,
    pub(in crate::server) kind: InboxItemKind,
    pub(in crate::server) strength: AttentionStrength,
    pub(in crate::server) status: InboxItemStatus,
    pub(in crate::server) available_at: OffsetDateTime,
    pub(in crate::server) assigned_run_id: Option<RunId>,
    pub(in crate::server) retry_count: u32,
    pub(in crate::server) requeue_count: u32,
    pub(in crate::server) handled_at: Option<OffsetDateTime>,
    pub(in crate::server) ambient: Option<AmbientAggregateSnapshot>,
}

impl InboxItem {
    #[allow(clippy::too_many_arguments)]
    pub(in crate::server) fn open_hard(
        id: InboxItemId,
        space_id: SpaceId,
        member_id: MemberId,
        message_id: Option<MessageId>,
        thread_id: ThreadId,
        task_id: Option<TaskId>,
        kind: InboxItemKind,
        now: OffsetDateTime,
    ) -> Result<Self, DomainError> {
        if !matches!(
            kind,
            InboxItemKind::Direct
                | InboxItemKind::Mention
                | InboxItemKind::Reply
                | InboxItemKind::TaskActivity
                | InboxItemKind::System
        ) {
            return Err(DomainError::InvalidPersistedState);
        }
        Ok(Self {
            id,
            space_id,
            member_id,
            message_id,
            thread_id,
            task_id,
            kind,
            strength: AttentionStrength::Hard,
            status: InboxItemStatus::Pending,
            available_at: now,
            assigned_run_id: None,
            retry_count: 0,
            requeue_count: 0,
            handled_at: None,
            ambient: None,
        })
    }

    /// Opens an ambient aggregate for one Agent and Thread. The Item stays unavailable until the
    /// debounce elapses, so a Thread that keeps receiving Messages is read once, not once per
    /// Message.
    pub(in crate::server) fn open_ambient(
        id: InboxItemId,
        space_id: SpaceId,
        member_id: MemberId,
        thread_id: ThreadId,
        kind: InboxItemKind,
        message_seq: u64,
        now: OffsetDateTime,
    ) -> Result<Self, DomainError> {
        if !matches!(
            kind,
            InboxItemKind::ThreadActivity | InboxItemKind::ChannelActivity
        ) || message_seq == 0
        {
            return Err(DomainError::InvalidPersistedState);
        }
        let ambient = AmbientAggregate::opened_at(message_seq, now);
        Ok(Self {
            id,
            space_id,
            member_id,
            message_id: None,
            thread_id,
            task_id: None,
            kind,
            strength: AttentionStrength::Ambient,
            status: InboxItemStatus::Pending,
            available_at: ambient.debounced_available_at(now),
            assigned_run_id: None,
            retry_count: 0,
            requeue_count: 0,
            handled_at: None,
            ambient: Some(ambient),
        })
    }

    pub(in crate::server) fn view(&self) -> InboxItemView {
        InboxItemView {
            id: self.id,
            space_id: self.space_id,
            member_id: self.member_id,
            message_id: self.message_id,
            thread_id: self.thread_id,
            task_id: self.task_id,
            kind: self.kind,
            strength: self.strength,
            status: self.status,
            available_at: self.available_at,
            assigned_run_id: self.assigned_run_id,
            retry_count: self.retry_count,
            requeue_count: self.requeue_count,
            handled_at: self.handled_at,
            ambient: self.ambient.map(|ambient| AmbientAggregateView {
                first_message_seq: ambient.first_message_seq,
                last_message_seq: ambient.last_message_seq,
                aggregated_count: ambient.aggregated_count,
                force_at: ambient.force_at,
            }),
        }
    }

    pub(in crate::server) fn snapshot(&self) -> InboxItemSnapshot {
        let view = self.view();
        InboxItemSnapshot {
            id: view.id,
            space_id: view.space_id,
            member_id: view.member_id,
            message_id: view.message_id,
            thread_id: view.thread_id,
            task_id: view.task_id,
            kind: view.kind,
            strength: view.strength,
            status: view.status,
            available_at: view.available_at,
            assigned_run_id: view.assigned_run_id,
            retry_count: view.retry_count,
            requeue_count: view.requeue_count,
            handled_at: view.handled_at,
            ambient: view.ambient.map(|ambient| AmbientAggregateSnapshot {
                first_message_seq: ambient.first_message_seq,
                last_message_seq: ambient.last_message_seq,
                aggregated_count: ambient.aggregated_count,
                force_at: ambient.force_at,
            }),
        }
    }

    pub(in crate::server) fn rehydrate(snapshot: InboxItemSnapshot) -> Result<Self, DomainError> {
        if (snapshot.status == InboxItemStatus::Assigned) != snapshot.assigned_run_id.is_some()
            || (snapshot.status == InboxItemStatus::Handled) != snapshot.handled_at.is_some()
        {
            return Err(DomainError::InvalidPersistedState);
        }
        let ambient = snapshot
            .ambient
            .map(|ambient| {
                if ambient.first_message_seq == 0
                    || ambient.last_message_seq < ambient.first_message_seq
                    || ambient.aggregated_count == 0
                    || u64::from(ambient.aggregated_count)
                        > ambient.last_message_seq - ambient.first_message_seq + 1
                {
                    return Err(DomainError::InvalidPersistedState);
                }
                Ok(AmbientAggregate {
                    first_message_seq: ambient.first_message_seq,
                    last_message_seq: ambient.last_message_seq,
                    aggregated_count: ambient.aggregated_count,
                    force_at: ambient.force_at,
                })
            })
            .transpose()?;
        let kind_matches_strength = matches!(
            (snapshot.kind, snapshot.strength),
            (
                InboxItemKind::Direct
                    | InboxItemKind::Mention
                    | InboxItemKind::Reply
                    | InboxItemKind::TaskActivity
                    | InboxItemKind::System,
                AttentionStrength::Hard
            ) | (
                InboxItemKind::ThreadActivity | InboxItemKind::ChannelActivity,
                AttentionStrength::Ambient
            )
        );
        if !kind_matches_strength
            || matches!(snapshot.strength, AttentionStrength::Ambient)
                != (ambient.is_some() && snapshot.message_id.is_none())
        {
            return Err(DomainError::InvalidPersistedState);
        }
        Ok(Self {
            id: snapshot.id,
            space_id: snapshot.space_id,
            member_id: snapshot.member_id,
            message_id: snapshot.message_id,
            thread_id: snapshot.thread_id,
            task_id: snapshot.task_id,
            kind: snapshot.kind,
            strength: snapshot.strength,
            status: snapshot.status,
            available_at: snapshot.available_at,
            assigned_run_id: snapshot.assigned_run_id,
            retry_count: snapshot.retry_count,
            requeue_count: snapshot.requeue_count,
            handled_at: snapshot.handled_at,
            ambient,
        })
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

    pub(in crate::server) fn assign_to_run(&mut self, run_id: RunId) -> Result<(), DomainError> {
        if self.status != InboxItemStatus::Pending {
            return Err(DomainError::InvalidTransition);
        }
        self.status = InboxItemStatus::Assigned;
        self.assigned_run_id = Some(run_id);
        Ok(())
    }

    pub(in crate::server) fn attach_to_active_run(
        &mut self,
        run_id: RunId,
    ) -> Result<(), DomainError> {
        if self.strength != AttentionStrength::Hard {
            return Err(DomainError::AmbientItemCannotAttach);
        }
        self.assign_to_run(run_id)
    }

    /// Applies the disposition the Agent reported for this Item. `Deferred` keeps the Item out of the
    /// queue until `prepare_defer` sets its `available_at`, which is how an Agent says "look at this
    /// again in twenty minutes".
    pub(in crate::server) fn apply_disposition(
        &mut self,
        run_id: RunId,
        disposition: InboxItemDisposition,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.status != InboxItemStatus::Assigned || self.assigned_run_id != Some(run_id) {
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
        self.assigned_run_id = None;
        Ok(())
    }

    /// Returns an Item to the queue because the Computer reported its Run failed. Counts one failed
    /// attempt and retires the Item once it exhausts `max_retry_count`, so a source the Agent cannot
    /// process stops being retried forever.
    ///
    /// This is the only transition that raises `retry_count`: a `Released` disposition is the Agent
    /// deciding not to handle the Item, which is not a failed attempt.
    pub(in crate::server) fn release_on_failed_run(
        &mut self,
        run_id: RunId,
        max_retry_count: u32,
        now: OffsetDateTime,
    ) -> Result<InboxItemStatus, DomainError> {
        if self.status != InboxItemStatus::Assigned || self.assigned_run_id != Some(run_id) {
            return Err(DomainError::InvalidTransition);
        }
        self.assigned_run_id = None;
        self.retry_count += 1;
        self.status = if self.retry_count > max_retry_count {
            InboxItemStatus::Dead
        } else {
            self.available_at = now;
            InboxItemStatus::Pending
        };
        Ok(self.status)
    }

    /// Marks a Human-owned Item handled on its owner's explicit read. Agent Items never use this
    /// path: their terminal state is decided by the Run that leased them, so callers must guard on
    /// owner kind before invoking. Reading an already handled Item stays idempotent.
    pub(in crate::server) fn mark_read(&mut self, now: OffsetDateTime) -> Result<(), DomainError> {
        match self.status {
            InboxItemStatus::Handled => Ok(()),
            InboxItemStatus::Pending => {
                self.status = InboxItemStatus::Handled;
                self.handled_at = Some(now);
                Ok(())
            }
            _ => Err(DomainError::InvalidTransition),
        }
    }

    /// Returns a retired Item to the queue on a governor's decision.
    ///
    /// Clearing `retry_count` is what makes the action useful: a `dead` Item has already exhausted
    /// `max_retry_count`, so keeping the count would retire it again on the next expiry. `requeue_count`
    /// records that a governor overrode the retirement, so a source that keeps failing stays visible
    /// instead of looking like a fresh Item.
    pub(in crate::server) fn requeue_from_dead(
        &mut self,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.status != InboxItemStatus::Dead {
            return Err(DomainError::InvalidTransition);
        }
        self.status = InboxItemStatus::Pending;
        self.available_at = now;
        self.retry_count = 0;
        self.requeue_count += 1;
        Ok(())
    }

    pub(in crate::server) fn prepare_defer(
        &mut self,
        run_id: RunId,
        until: OffsetDateTime,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.status != InboxItemStatus::Assigned
            || self.assigned_run_id != Some(run_id)
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

    #[test]
    fn rehydrate_accepts_a_complete_snapshot_and_rejects_a_dangling_assignment() {
        let item = InboxItem::open_hard(
            InboxItemId::from_uuid(uuid::Uuid::from_u128(21)),
            SpaceId::from_uuid(uuid::Uuid::from_u128(22)),
            MemberId::from_uuid(uuid::Uuid::from_u128(23)),
            None,
            ThreadId::from_uuid(uuid::Uuid::from_u128(24)),
            None,
            InboxItemKind::System,
            OffsetDateTime::UNIX_EPOCH,
        )
        .expect("system Item is hard");
        let snapshot = item.snapshot();
        let restored = InboxItem::rehydrate(snapshot.clone()).expect("snapshot is valid");
        assert_eq!(restored.snapshot(), snapshot);

        let mut invalid = snapshot;
        invalid.assigned_run_id = Some(RunId::from_uuid(uuid::Uuid::from_u128(25)));
        assert_eq!(
            InboxItem::rehydrate(invalid),
            Err(DomainError::InvalidPersistedState)
        );
    }

    #[test]
    fn rehydrate_rejects_a_kind_with_the_wrong_strength() {
        let item = InboxItem::open_hard(
            InboxItemId::from_uuid(uuid::Uuid::from_u128(21)),
            SpaceId::from_uuid(uuid::Uuid::from_u128(22)),
            MemberId::from_uuid(uuid::Uuid::from_u128(23)),
            None,
            ThreadId::from_uuid(uuid::Uuid::from_u128(24)),
            None,
            InboxItemKind::System,
            OffsetDateTime::UNIX_EPOCH,
        )
        .expect("system Item is hard");
        let mut invalid = item.snapshot();
        invalid.kind = InboxItemKind::ChannelActivity;
        assert_eq!(
            InboxItem::rehydrate(invalid),
            Err(DomainError::InvalidPersistedState)
        );
    }

    #[test]
    fn constructors_reject_kinds_with_the_wrong_strength() {
        assert_eq!(
            InboxItem::open_hard(
                InboxItemId::from_uuid(uuid::Uuid::from_u128(31)),
                SpaceId::from_uuid(uuid::Uuid::from_u128(32)),
                MemberId::from_uuid(uuid::Uuid::from_u128(33)),
                None,
                ThreadId::from_uuid(uuid::Uuid::from_u128(34)),
                None,
                InboxItemKind::ChannelActivity,
                OffsetDateTime::UNIX_EPOCH,
            ),
            Err(DomainError::InvalidPersistedState)
        );
        assert_eq!(
            InboxItem::open_ambient(
                InboxItemId::from_uuid(uuid::Uuid::from_u128(35)),
                SpaceId::from_uuid(uuid::Uuid::from_u128(36)),
                MemberId::from_uuid(uuid::Uuid::from_u128(37)),
                ThreadId::from_uuid(uuid::Uuid::from_u128(38)),
                InboxItemKind::Mention,
                1,
                OffsetDateTime::UNIX_EPOCH,
            ),
            Err(DomainError::InvalidPersistedState)
        );
    }

    fn assigned_item(run_id: RunId, retry_count: u32) -> InboxItem {
        InboxItem {
            id: InboxItemId::from_uuid(uuid::Uuid::from_u128(1)),
            space_id: SpaceId::from_uuid(uuid::Uuid::from_u128(2)),
            member_id: MemberId::from_uuid(uuid::Uuid::from_u128(3)),
            message_id: None,
            thread_id: ThreadId::from_uuid(uuid::Uuid::from_u128(4)),
            task_id: None,
            kind: InboxItemKind::Mention,
            strength: AttentionStrength::Hard,
            status: InboxItemStatus::Assigned,
            available_at: OffsetDateTime::UNIX_EPOCH,
            assigned_run_id: Some(run_id),
            retry_count,
            requeue_count: 0,
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
        .expect("channel activity is ambient")
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

        // An activity event arriving every debounce period restarts the quiet period each time. Without the
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
            "each activity event restarts the quiet period"
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

        // Once assigned, the aggregate is frozen: the Agent already received this range.
        let mut assigned = ambient_item(now);
        assigned
            .assign_to_run(run_id)
            .expect("a pending Item can be assigned");
        assert_eq!(
            assigned.absorb_ambient_message(11, now),
            Err(DomainError::InvalidTransition)
        );

        // A hard Item is one Message, not a range.
        let mut hard = assigned_item(run_id, 0);
        hard.status = InboxItemStatus::Pending;
        assert_eq!(
            hard.absorb_ambient_message(11, now),
            Err(DomainError::ItemIsNotAmbientAggregate)
        );
    }

    #[test]
    fn requeueing_a_dead_item_restores_its_retry_budget_and_counts_the_override() {
        let run_id = RunId::from_uuid(uuid::Uuid::from_u128(9));
        let retired_at = OffsetDateTime::UNIX_EPOCH;
        let requeued_at = retired_at + Duration::hours(1);

        let mut item = assigned_item(run_id, 2);
        assert_eq!(
            item.release_on_failed_run(run_id, 2, retired_at),
            Ok(InboxItemStatus::Dead)
        );

        item.requeue_from_dead(requeued_at)
            .expect("a governor may return a retired Item to the queue");
        assert_eq!(item.status, InboxItemStatus::Pending);
        assert_eq!(item.available_at, requeued_at);
        assert_eq!(
            item.retry_count, 0,
            "without a fresh budget the next failure would retire it again"
        );
        assert_eq!(item.requeue_count, 1);

        // A second round trip accumulates, so a source that keeps failing stays visible.
        item.assign_to_run(run_id)
            .expect("the requeued Item is assignable again");
        assert_eq!(
            item.release_on_failed_run(run_id, 0, requeued_at),
            Ok(InboxItemStatus::Dead)
        );
        item.requeue_from_dead(requeued_at).expect("requeued again");
        assert_eq!(item.requeue_count, 2);
    }

    #[test]
    fn only_a_dead_item_can_be_requeued() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let run_id = RunId::from_uuid(uuid::Uuid::from_u128(9));

        // An assigned Item belongs to a live Run; requeueing it would strip that Run's Item.
        let mut assigned = assigned_item(run_id, 0);
        assert_eq!(
            assigned.requeue_from_dead(now),
            Err(DomainError::InvalidTransition)
        );
        assert_eq!(assigned.assigned_run_id, Some(run_id));

        // A handled Item is resolved history, not a retirement to undo.
        let mut handled = assigned_item(run_id, 0);
        handled
            .apply_disposition(run_id, InboxItemDisposition::Handled, now)
            .expect("the owning Run resolves its Item");
        assert_eq!(
            handled.requeue_from_dead(now),
            Err(DomainError::InvalidTransition)
        );
        assert_eq!(handled.status, InboxItemStatus::Handled);
        assert_eq!(handled.requeue_count, 0);
    }

    #[test]
    fn an_ambient_aggregate_never_attaches_to_an_active_run() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let run_id = RunId::from_uuid(uuid::Uuid::from_u128(9));
        let mut item = ambient_item(now);
        assert_eq!(
            item.attach_to_active_run(run_id),
            Err(DomainError::AmbientItemCannotAttach),
            "ambient activity only aggregates; it does not interrupt a Run"
        );
        assert_eq!(item.status, InboxItemStatus::Pending);
    }

    #[test]
    fn releasing_on_a_failed_run_retries_until_the_limit_then_retires_the_item() {
        let run_id = RunId::from_uuid(uuid::Uuid::from_u128(9));
        let now = OffsetDateTime::UNIX_EPOCH + time::Duration::hours(3);

        // Below the limit the Item returns to the queue and becomes available immediately.
        let mut item = assigned_item(run_id, 0);
        assert_eq!(
            item.release_on_failed_run(run_id, 2, now),
            Ok(InboxItemStatus::Pending)
        );
        assert_eq!(item.retry_count, 1);
        assert_eq!(item.available_at, now);
        assert_eq!(item.assigned_run_id, None);

        // Reaching the limit still retries; only exceeding it retires the Item.
        let mut item = assigned_item(run_id, 1);
        assert_eq!(
            item.release_on_failed_run(run_id, 2, now),
            Ok(InboxItemStatus::Pending)
        );
        let mut item = assigned_item(run_id, 2);
        assert_eq!(
            item.release_on_failed_run(run_id, 2, now),
            Ok(InboxItemStatus::Dead)
        );
        assert_eq!(item.retry_count, 3);
        assert_eq!(item.assigned_run_id, None);
    }

    #[test]
    fn only_the_owning_run_can_release_an_item() {
        let owner = RunId::from_uuid(uuid::Uuid::from_u128(9));
        let stranger = RunId::from_uuid(uuid::Uuid::from_u128(10));
        let now = OffsetDateTime::UNIX_EPOCH;

        let mut item = assigned_item(owner, 0);
        assert_eq!(
            item.release_on_failed_run(stranger, 5, now),
            Err(DomainError::InvalidTransition)
        );
        assert_eq!(item.retry_count, 0, "a foreign Run does not spend a retry");

        // An Item the Agent already resolved is no longer assigned, so a failure cannot count against it.
        let mut handled = assigned_item(owner, 0);
        handled
            .apply_disposition(owner, InboxItemDisposition::Handled, now)
            .expect("the owning Run resolves its Item");
        assert_eq!(
            handled.release_on_failed_run(owner, 5, now),
            Err(DomainError::InvalidTransition)
        );
        assert_eq!(handled.status, InboxItemStatus::Handled);
        assert_eq!(handled.retry_count, 0);
    }

    /// A deferred Item is how an Agent says "look at this again later". The defer target becomes the
    /// Item's `available_at`, and reaching it is what wakes the Agent.
    #[test]
    fn a_deferred_item_becomes_available_at_the_requested_time() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let later = now + Duration::minutes(20);
        let run_id = RunId::from_uuid(uuid::Uuid::from_u128(9));

        let mut item = assigned_item(run_id, 0);
        item.prepare_defer(run_id, later, now)
            .expect("an Agent may defer to a future time");
        item.apply_disposition(run_id, InboxItemDisposition::Deferred, now)
            .expect("the deferred disposition applies");

        assert_eq!(item.status, InboxItemStatus::Deferred);
        assert_eq!(item.available_at, later);
        assert_eq!(
            item.retry_count, 0,
            "deferring is a decision, not a failed attempt"
        );

        let mut backwards = assigned_item(run_id, 0);
        assert_eq!(
            backwards.prepare_defer(run_id, now, now),
            Err(DomainError::InvalidTransition),
            "a defer target must be in the future"
        );
    }

    #[test]
    fn a_released_disposition_is_not_a_failed_attempt() {
        let run_id = RunId::from_uuid(uuid::Uuid::from_u128(9));
        let mut item = assigned_item(run_id, 0);
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
