use time::OffsetDateTime;

use crate::ids::{EventId, InboxItemId, MemberId, RunId, SpaceId, TaskId, ThreadId};

use super::{
    DomainError,
    attention::{InboxItem, InboxItemDisposition},
    task::Task,
};

/// A Run is one bounded execution. It carries no ownership credential and no deadline: the Trigger
/// names the Agent, the Agent belongs to one Computer, and that Computer's Driver executes. No second
/// candidate executor exists, so nothing has to prove its right to run.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum RunStatus {
    Dispatched,
    Working,
    Completed,
    Yielded,
    Failed,
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

/// Every variant names a failure the Computer observed directly and reported. The Server never infers
/// a failure, so no code here stands for "the Server stopped hearing from the Computer": that is a
/// Computer reachability fact, not a Run outcome.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum RunErrorCode {
    DriverError,
    DriverLost,
    ComputerRestarted,
    SessionUnavailable,
    AgentUnavailable,
    InvalidCommand,
    UnhandledItems,
    Internal,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum DeliveryOutcome {
    Accepted,
    TooLate,
    Unsupported,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct RunItem {
    inbox_item_id: InboxItemId,
    delivery_sequence: u64,
    delivery_outcome: Option<DeliveryOutcome>,
    delivery_event_id: Option<EventId>,
    delivery_receipt_at: Option<OffsetDateTime>,
    disposition: Option<InboxItemDisposition>,
    disposition_at: Option<OffsetDateTime>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct RunItemView {
    pub(in crate::server) inbox_item_id: InboxItemId,
    pub(in crate::server) delivery_sequence: u64,
    pub(in crate::server) delivery_outcome: Option<DeliveryOutcome>,
    pub(in crate::server) delivery_event_id: Option<EventId>,
    pub(in crate::server) delivery_receipt_at: Option<OffsetDateTime>,
    pub(in crate::server) disposition: Option<InboxItemDisposition>,
    pub(in crate::server) disposition_at: Option<OffsetDateTime>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct RunItemSnapshot {
    pub(in crate::server) inbox_item_id: InboxItemId,
    pub(in crate::server) delivery_sequence: u64,
    pub(in crate::server) delivery_outcome: Option<DeliveryOutcome>,
    pub(in crate::server) delivery_event_id: Option<EventId>,
    pub(in crate::server) delivery_receipt_at: Option<OffsetDateTime>,
    pub(in crate::server) disposition: Option<InboxItemDisposition>,
    pub(in crate::server) disposition_at: Option<OffsetDateTime>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct Run {
    id: RunId,
    space_id: SpaceId,
    agent_id: MemberId,
    task_id: Option<TaskId>,
    focus_thread_id: ThreadId,
    status: RunStatus,
    trigger: RunTrigger,
    cancel_requested: bool,
    items: Vec<RunItem>,
    outcome: Option<RunOutcome>,
    error_code: Option<RunErrorCode>,
    continuation_note: Option<String>,
    started_at: Option<OffsetDateTime>,
    finished_at: Option<OffsetDateTime>,
}

/// Records why the Run exists. Run behaviour does not branch on the kind: it selects the Inbox Item
/// strength upstream, so a new kind adds a value here and changes nothing else.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum RunTrigger {
    Mention,
    DirectMessage,
    TaskActivity,
    ThreadActivity,
    ChannelActivity,
    Schedule,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct RunView<'a> {
    pub(in crate::server) id: RunId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) agent_id: MemberId,
    pub(in crate::server) task_id: Option<TaskId>,
    pub(in crate::server) focus_thread_id: ThreadId,
    pub(in crate::server) status: RunStatus,
    pub(in crate::server) trigger: RunTrigger,
    pub(in crate::server) cancel_requested: bool,
    pub(in crate::server) outcome: Option<RunOutcome>,
    pub(in crate::server) error_code: Option<RunErrorCode>,
    pub(in crate::server) continuation_note: Option<&'a str>,
    pub(in crate::server) started_at: Option<OffsetDateTime>,
    pub(in crate::server) finished_at: Option<OffsetDateTime>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct RunSnapshot {
    pub(in crate::server) id: RunId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) agent_id: MemberId,
    pub(in crate::server) task_id: Option<TaskId>,
    pub(in crate::server) focus_thread_id: ThreadId,
    pub(in crate::server) status: RunStatus,
    pub(in crate::server) trigger: RunTrigger,
    pub(in crate::server) cancel_requested: bool,
    pub(in crate::server) items: Vec<RunItemSnapshot>,
    pub(in crate::server) outcome: Option<RunOutcome>,
    pub(in crate::server) error_code: Option<RunErrorCode>,
    pub(in crate::server) continuation_note: Option<String>,
    pub(in crate::server) started_at: Option<OffsetDateTime>,
    pub(in crate::server) finished_at: Option<OffsetDateTime>,
}

impl Run {
    pub(in crate::server) fn create(
        id: RunId,
        space_id: SpaceId,
        agent_id: MemberId,
        task_id: Option<TaskId>,
        focus_thread_id: ThreadId,
        trigger: RunTrigger,
    ) -> Self {
        Self {
            id,
            space_id,
            agent_id,
            task_id,
            focus_thread_id,
            status: RunStatus::Dispatched,
            trigger,
            cancel_requested: false,
            items: Vec::new(),
            outcome: None,
            error_code: None,
            continuation_note: None,
            started_at: None,
            finished_at: None,
        }
    }

    pub(in crate::server) fn view(&self) -> RunView<'_> {
        RunView {
            id: self.id,
            space_id: self.space_id,
            agent_id: self.agent_id,
            task_id: self.task_id,
            focus_thread_id: self.focus_thread_id,
            status: self.status,
            trigger: self.trigger,
            cancel_requested: self.cancel_requested,
            outcome: self.outcome,
            error_code: self.error_code,
            continuation_note: self.continuation_note.as_deref(),
            started_at: self.started_at,
            finished_at: self.finished_at,
        }
    }

    pub(in crate::server) fn items(&self) -> impl ExactSizeIterator<Item = RunItemView> + '_ {
        self.items.iter().map(|item| RunItemView {
            inbox_item_id: item.inbox_item_id,
            delivery_sequence: item.delivery_sequence,
            delivery_outcome: item.delivery_outcome,
            delivery_event_id: item.delivery_event_id,
            delivery_receipt_at: item.delivery_receipt_at,
            disposition: item.disposition,
            disposition_at: item.disposition_at,
        })
    }

    pub(in crate::server) fn item_for_delivery(
        &self,
        delivery_sequence: u64,
    ) -> Option<RunItemView> {
        self.items()
            .find(|item| item.delivery_sequence == delivery_sequence)
    }

    pub(in crate::server) fn add_dispatched_item(
        &mut self,
        inbox_item_id: InboxItemId,
        delivery_sequence: u64,
    ) -> Result<(), DomainError> {
        if delivery_sequence == 0
            || self.items.iter().any(|item| {
                item.inbox_item_id == inbox_item_id || item.delivery_sequence == delivery_sequence
            })
        {
            return Err(DomainError::ItemScopeMismatch);
        }
        self.items.push(RunItem {
            inbox_item_id,
            delivery_sequence,
            delivery_outcome: None,
            delivery_event_id: None,
            delivery_receipt_at: None,
            disposition: None,
            disposition_at: None,
        });
        Ok(())
    }

    pub(in crate::server) fn snapshot(&self) -> RunSnapshot {
        let view = self.view();
        RunSnapshot {
            id: view.id,
            space_id: view.space_id,
            agent_id: view.agent_id,
            task_id: view.task_id,
            focus_thread_id: view.focus_thread_id,
            status: view.status,
            trigger: view.trigger,
            cancel_requested: view.cancel_requested,
            items: self
                .items()
                .map(|item| RunItemSnapshot {
                    inbox_item_id: item.inbox_item_id,
                    delivery_sequence: item.delivery_sequence,
                    delivery_outcome: item.delivery_outcome,
                    delivery_event_id: item.delivery_event_id,
                    delivery_receipt_at: item.delivery_receipt_at,
                    disposition: item.disposition,
                    disposition_at: item.disposition_at,
                })
                .collect(),
            outcome: view.outcome,
            error_code: view.error_code,
            continuation_note: view.continuation_note.map(str::to_owned),
            started_at: view.started_at,
            finished_at: view.finished_at,
        }
    }

    pub(in crate::server) fn rehydrate(snapshot: RunSnapshot) -> Result<Self, DomainError> {
        let expected_outcome = match snapshot.status {
            RunStatus::Completed => Some(RunOutcome::Completed),
            RunStatus::Yielded => Some(RunOutcome::Yielded),
            RunStatus::Failed => Some(RunOutcome::Failed),
            RunStatus::Canceled => Some(RunOutcome::Canceled),
            RunStatus::Dispatched | RunStatus::Working => None,
        };
        if snapshot.outcome != expected_outcome
            || snapshot.finished_at.is_some() != expected_outcome.is_some()
            || (snapshot.error_code.is_some() && snapshot.outcome != Some(RunOutcome::Failed))
            || (snapshot.outcome == Some(RunOutcome::Failed) && snapshot.error_code.is_none())
            || (expected_outcome.is_some()
                && snapshot.items.iter().any(|item| item.disposition.is_none()))
            || snapshot
                .items
                .iter()
                .any(|item| item.disposition.is_some() != item.disposition_at.is_some())
        {
            return Err(DomainError::InvalidPersistedState);
        }
        let mut item_ids = std::collections::BTreeSet::new();
        let mut sequences = std::collections::BTreeSet::new();
        if snapshot.items.iter().any(|item| {
            item.delivery_sequence == 0
                || !item_ids.insert(item.inbox_item_id)
                || !sequences.insert(item.delivery_sequence)
                || (item.delivery_outcome.is_some()
                    != (item.delivery_event_id.is_some() && item.delivery_receipt_at.is_some()))
        }) {
            return Err(DomainError::InvalidPersistedState);
        }
        Ok(Self {
            id: snapshot.id,
            space_id: snapshot.space_id,
            agent_id: snapshot.agent_id,
            task_id: snapshot.task_id,
            focus_thread_id: snapshot.focus_thread_id,
            status: snapshot.status,
            trigger: snapshot.trigger,
            cancel_requested: snapshot.cancel_requested,
            items: snapshot
                .items
                .into_iter()
                .map(|item| RunItem {
                    inbox_item_id: item.inbox_item_id,
                    delivery_sequence: item.delivery_sequence,
                    delivery_outcome: item.delivery_outcome,
                    delivery_event_id: item.delivery_event_id,
                    delivery_receipt_at: item.delivery_receipt_at,
                    disposition: item.disposition,
                    disposition_at: item.disposition_at,
                })
                .collect(),
            outcome: snapshot.outcome,
            error_code: snapshot.error_code,
            continuation_note: snapshot.continuation_note,
            started_at: snapshot.started_at,
            finished_at: snapshot.finished_at,
        })
    }

    /// Reported by the Computer once the Driver is processing. Idempotent from `working` so a replayed
    /// report is harmless.
    pub(in crate::server) fn start(&mut self, now: OffsetDateTime) -> Result<(), DomainError> {
        if !matches!(self.status, RunStatus::Dispatched | RunStatus::Working) {
            return Err(DomainError::InvalidTransition);
        }
        self.status = RunStatus::Working;
        self.started_at.get_or_insert(now);
        Ok(())
    }

    pub(in crate::server) fn bind_task(&mut self, task: &Task) -> Result<(), DomainError> {
        let task_view = task.view();
        if self.task_id.is_some_and(|task_id| task_id != task_view.id)
            || !task.linked_to(self.focus_thread_id)
        {
            return Err(DomainError::FocusOutsideTask);
        }
        self.task_id = Some(task_view.id);
        Ok(())
    }

    pub(in crate::server) fn attach(&mut self, item: &InboxItem) -> Result<u64, DomainError> {
        if self.status != RunStatus::Working {
            return Err(DomainError::RunNotAcceptingItems);
        }
        let item_view = item.view();
        if item_view.member_id != self.agent_id
            || item_view.thread_id != self.focus_thread_id
            || item_view.task_id != self.task_id
        {
            return Err(DomainError::ItemScopeMismatch);
        }
        if let Some(existing) = self
            .items
            .iter()
            .find(|existing| existing.inbox_item_id == item_view.id)
        {
            return Ok(existing.delivery_sequence);
        }
        let sequence = self
            .items
            .last()
            .map_or(1, |item| item.delivery_sequence + 1);
        self.add_dispatched_item(item_view.id, sequence)?;
        Ok(sequence)
    }

    /// Marks a Human's stop request. The Run stays live: only the Computer's report moves it to
    /// `canceled`, because only the Computer knows when the Driver actually stopped.
    pub(in crate::server) fn request_cancel(&mut self) -> Result<(), DomainError> {
        if self.is_terminal() {
            return Err(DomainError::InvalidTransition);
        }
        self.cancel_requested = true;
        Ok(())
    }

    pub(in crate::server) fn cancel_for_agent_retirement(&mut self, now: OffsetDateTime) {
        for item in &mut self.items {
            if item.disposition.is_none() {
                item.disposition = Some(InboxItemDisposition::Released);
                item.disposition_at = Some(now);
            }
        }
        self.status = RunStatus::Canceled;
        self.outcome = Some(RunOutcome::Canceled);
        self.error_code = None;
        self.finished_at = Some(now);
    }

    pub(in crate::server) fn is_terminal(&self) -> bool {
        matches!(
            self.status,
            RunStatus::Completed | RunStatus::Yielded | RunStatus::Failed | RunStatus::Canceled
        )
    }

    pub(in crate::server) fn set_item_disposition_at(
        &mut self,
        item_id: InboxItemId,
        disposition: InboxItemDisposition,
        at: OffsetDateTime,
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
        item.disposition_at.get_or_insert(at);
        Ok(())
    }

    pub(in crate::server) fn record_delivery_receipt(
        &mut self,
        delivery_sequence: u64,
        event_id: EventId,
        outcome: DeliveryOutcome,
        at: OffsetDateTime,
    ) -> Result<bool, DomainError> {
        let item_index = self
            .items
            .iter()
            .position(|item| item.delivery_sequence == delivery_sequence)
            .ok_or(DomainError::ItemScopeMismatch)?;
        if self
            .items
            .iter()
            .enumerate()
            .any(|(index, item)| index != item_index && item.delivery_event_id == Some(event_id))
        {
            return Err(DomainError::InvalidTransition);
        }
        let item = &mut self.items[item_index];
        if let Some(existing_event_id) = item.delivery_event_id {
            if existing_event_id == event_id
                && item.delivery_outcome == Some(outcome)
                && item.delivery_receipt_at.is_some()
            {
                return Ok(false);
            }
            return Err(DomainError::InvalidTransition);
        }
        item.delivery_outcome = Some(outcome);
        item.delivery_event_id = Some(event_id);
        item.delivery_receipt_at = Some(at);
        Ok(true)
    }

    /// Applies the Computer's terminal report. Accepted from `dispatched` too: a Computer that fails
    /// while opening the Session reports the failure without ever reaching `working`.
    pub(in crate::server) fn finish(
        &mut self,
        outcome: RunOutcome,
        error_code: Option<RunErrorCode>,
        continuation_note: Option<String>,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.is_terminal() {
            return Err(DomainError::InvalidTransition);
        }
        if self.items.iter().any(|item| item.disposition.is_none()) {
            return Err(DomainError::IncompleteItemDisposition);
        }
        // The database permits an error code only for a failed outcome.
        if (error_code.is_some() && outcome != RunOutcome::Failed)
            || (outcome == RunOutcome::Failed && error_code.is_none())
        {
            return Err(DomainError::InvalidTransition);
        }
        self.status = match outcome {
            RunOutcome::Completed => RunStatus::Completed,
            RunOutcome::Yielded => RunStatus::Yielded,
            RunOutcome::Failed => RunStatus::Failed,
            RunOutcome::Canceled => RunStatus::Canceled,
        };
        self.outcome = Some(outcome);
        self.error_code = error_code;
        self.continuation_note = continuation_note;
        self.finished_at = Some(now);
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rehydrate_accepts_a_consistent_snapshot_and_rejects_a_live_outcome() {
        let snapshot = working_run().snapshot();
        let restored = Run::rehydrate(snapshot.clone()).expect("snapshot is valid");
        assert_eq!(restored.snapshot(), snapshot);

        let mut invalid = snapshot;
        invalid.outcome = Some(RunOutcome::Completed);
        assert_eq!(
            Run::rehydrate(invalid),
            Err(DomainError::InvalidPersistedState)
        );
    }

    #[test]
    fn rehydrate_rejects_a_terminal_run_with_an_unresolved_item() {
        let mut run = working_run();
        run.add_dispatched_item(InboxItemId::from_uuid(uuid::Uuid::from_u128(5)), 1)
            .expect("item is unique");
        run.set_item_disposition_at(
            InboxItemId::from_uuid(uuid::Uuid::from_u128(5)),
            InboxItemDisposition::Handled,
            OffsetDateTime::UNIX_EPOCH,
        )
        .expect("item can be resolved");
        run.finish(
            RunOutcome::Completed,
            None,
            None,
            OffsetDateTime::UNIX_EPOCH,
        )
        .expect("resolved Run can finish");

        let mut invalid = run.snapshot();
        invalid.items[0].disposition = None;
        assert_eq!(
            Run::rehydrate(invalid),
            Err(DomainError::InvalidPersistedState)
        );
    }

    fn working_run() -> Run {
        Run {
            id: RunId::from_uuid(uuid::Uuid::from_u128(1)),
            space_id: SpaceId::from_uuid(uuid::Uuid::from_u128(2)),
            agent_id: MemberId::from_uuid(uuid::Uuid::from_u128(3)),
            task_id: None,
            focus_thread_id: ThreadId::from_uuid(uuid::Uuid::from_u128(4)),
            status: RunStatus::Working,
            trigger: RunTrigger::Mention,
            cancel_requested: false,
            items: Vec::new(),
            outcome: None,
            error_code: None,
            continuation_note: None,
            started_at: Some(OffsetDateTime::UNIX_EPOCH),
            finished_at: None,
        }
    }

    #[test]
    fn a_failed_run_records_the_reported_error_code() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let mut run = working_run();
        run.finish(
            RunOutcome::Failed,
            Some(RunErrorCode::DriverError),
            None,
            now,
        )
        .expect("a failed run accepts an error code");
        assert_eq!(run.status, RunStatus::Failed);
        assert_eq!(run.error_code, Some(RunErrorCode::DriverError));
    }

    #[test]
    fn a_failed_run_requires_an_error_code() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let mut run = working_run();
        assert_eq!(
            run.finish(RunOutcome::Failed, None, None, now),
            Err(DomainError::InvalidTransition)
        );
        assert_eq!(run.status, RunStatus::Working);
    }

    #[test]
    fn a_delivery_receipt_is_recorded_once_and_replays_idempotently() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let item_id = InboxItemId::from_uuid(uuid::Uuid::from_u128(5));
        let event_id = EventId::from_uuid(uuid::Uuid::from_u128(6));
        let mut run = working_run();
        run.add_dispatched_item(item_id, 1).expect("item is unique");

        assert!(
            run.record_delivery_receipt(1, event_id, DeliveryOutcome::Unsupported, now)
                .expect("first receipt applies")
        );
        assert!(
            !run.record_delivery_receipt(1, event_id, DeliveryOutcome::Unsupported, now)
                .expect("same receipt replays idempotently")
        );
        assert_eq!(
            run.record_delivery_receipt(
                1,
                EventId::from_uuid(uuid::Uuid::from_u128(7)),
                DeliveryOutcome::TooLate,
                now,
            ),
            Err(DomainError::InvalidTransition)
        );
    }

    #[test]
    fn only_a_failed_outcome_carries_an_error_code() {
        let now = OffsetDateTime::UNIX_EPOCH;
        for outcome in [
            RunOutcome::Completed,
            RunOutcome::Yielded,
            RunOutcome::Canceled,
        ] {
            let mut run = working_run();
            assert_eq!(
                run.finish(outcome, Some(RunErrorCode::Internal), None, now),
                Err(DomainError::InvalidTransition)
            );
            assert_eq!(run.status, RunStatus::Working);
            assert_eq!(run.error_code, None);
            run.finish(outcome, None, None, now)
                .expect("a non-failed outcome finishes without an error code");
            assert_eq!(run.error_code, None);
        }
    }

    #[test]
    fn retirement_cancellation_leaves_no_error_code() {
        let mut run = working_run();
        run.error_code = Some(RunErrorCode::DriverLost);
        run.cancel_for_agent_retirement(OffsetDateTime::UNIX_EPOCH);
        assert_eq!(run.outcome, Some(RunOutcome::Canceled));
        assert_eq!(run.error_code, None);
    }

    /// A Run that never reached `working` still has to accept a terminal report: opening the Provider
    /// Session can fail before the Driver ever starts.
    #[test]
    fn a_dispatched_run_accepts_a_terminal_report_without_starting() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let mut run = working_run();
        run.status = RunStatus::Dispatched;
        run.started_at = None;
        run.finish(
            RunOutcome::Failed,
            Some(RunErrorCode::SessionUnavailable),
            None,
            now,
        )
        .expect("a dispatched Run can fail before starting");
        assert_eq!(run.status, RunStatus::Failed);
        assert_eq!(run.started_at, None);
    }

    #[test]
    fn a_terminal_run_rejects_a_second_report() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let mut run = working_run();
        run.finish(RunOutcome::Completed, None, None, now)
            .expect("first report applies");
        assert_eq!(
            run.finish(RunOutcome::Failed, None, None, now),
            Err(DomainError::InvalidTransition)
        );
        assert_eq!(run.outcome, Some(RunOutcome::Completed));
    }

    /// A replayed `run_started` must not be an error: at-least-once delivery means the Computer can
    /// report the same start twice.
    #[test]
    fn a_repeated_start_report_is_idempotent() {
        let first = OffsetDateTime::UNIX_EPOCH;
        let later = first + time::Duration::seconds(30);
        let mut run = working_run();
        run.status = RunStatus::Dispatched;
        run.started_at = None;
        run.start(first).expect("first start applies");
        run.start(later).expect("a replayed start is accepted");
        assert_eq!(run.started_at, Some(first));
    }

    #[test]
    fn a_cancel_request_leaves_the_run_live_until_the_computer_confirms() {
        let mut run = working_run();
        run.request_cancel()
            .expect("a live Run accepts the request");
        assert!(run.cancel_requested);
        assert_eq!(run.status, RunStatus::Working);

        run.finish(RunOutcome::Canceled, None, None, OffsetDateTime::UNIX_EPOCH)
            .expect("the Computer confirms the stop");
        assert_eq!(run.status, RunStatus::Canceled);
    }
}
