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

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum RunErrorCode {
    InvalidCommand,
    AgentUnavailable,
    ProcessLost,
    SessionLost,
    SandboxUnavailable,
    DriverUnavailable,
    Internal,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct RunItem {
    inbox_item_id: InboxItemId,
    delivery_sequence: u64,
    disposition: Option<InboxItemDisposition>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct RunItemView {
    pub(in crate::server) inbox_item_id: InboxItemId,
    pub(in crate::server) delivery_sequence: u64,
    pub(in crate::server) disposition: Option<InboxItemDisposition>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct RunItemSnapshot {
    pub(in crate::server) inbox_item_id: InboxItemId,
    pub(in crate::server) delivery_sequence: u64,
    pub(in crate::server) disposition: Option<InboxItemDisposition>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct Run {
    id: RunId,
    space_id: SpaceId,
    agent_id: MemberId,
    task_id: Option<TaskId>,
    focus_thread_id: ThreadId,
    status: RunStatus,
    fencing_token_hash: String,
    lease_expires_at: OffsetDateTime,
    items: Vec<RunItem>,
    outcome: Option<RunOutcome>,
    error_code: Option<RunErrorCode>,
    continuation_note: Option<String>,
    started_at: Option<OffsetDateTime>,
    finished_at: Option<OffsetDateTime>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct RunView<'a> {
    pub(in crate::server) id: RunId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) agent_id: MemberId,
    pub(in crate::server) task_id: Option<TaskId>,
    pub(in crate::server) focus_thread_id: ThreadId,
    pub(in crate::server) status: RunStatus,
    pub(in crate::server) fencing_token_hash: &'a str,
    pub(in crate::server) lease_expires_at: OffsetDateTime,
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
    pub(in crate::server) fencing_token_hash: String,
    pub(in crate::server) lease_expires_at: OffsetDateTime,
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
        fencing_token_hash: String,
        lease_expires_at: OffsetDateTime,
    ) -> Self {
        Self {
            id,
            space_id,
            agent_id,
            task_id,
            focus_thread_id,
            status: RunStatus::Queued,
            fencing_token_hash,
            lease_expires_at,
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
            fencing_token_hash: &self.fencing_token_hash,
            lease_expires_at: self.lease_expires_at,
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
            disposition: item.disposition,
        })
    }

    pub(in crate::server) fn item_for_delivery(
        &self,
        delivery_sequence: u64,
    ) -> Option<RunItemView> {
        self.items()
            .find(|item| item.delivery_sequence == delivery_sequence)
    }

    pub(in crate::server) fn add_claimed_item(
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
            disposition: None,
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
            fencing_token_hash: view.fencing_token_hash.to_owned(),
            lease_expires_at: view.lease_expires_at,
            items: self
                .items()
                .map(|item| RunItemSnapshot {
                    inbox_item_id: item.inbox_item_id,
                    delivery_sequence: item.delivery_sequence,
                    disposition: item.disposition,
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
            RunStatus::Queued
            | RunStatus::Starting
            | RunStatus::Running
            | RunStatus::Finalizing
            | RunStatus::Stopping => None,
        };
        if snapshot.fencing_token_hash.is_empty()
            || snapshot.outcome != expected_outcome
            || snapshot.finished_at.is_some() != expected_outcome.is_some()
            || (snapshot.error_code.is_some() && snapshot.outcome != Some(RunOutcome::Failed))
            || (expected_outcome.is_some()
                && snapshot.items.iter().any(|item| item.disposition.is_none()))
        {
            return Err(DomainError::InvalidPersistedState);
        }
        let mut item_ids = std::collections::BTreeSet::new();
        let mut sequences = std::collections::BTreeSet::new();
        if snapshot.items.iter().any(|item| {
            item.delivery_sequence == 0
                || !item_ids.insert(item.inbox_item_id)
                || !sequences.insert(item.delivery_sequence)
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
            fencing_token_hash: snapshot.fencing_token_hash,
            lease_expires_at: snapshot.lease_expires_at,
            items: snapshot
                .items
                .into_iter()
                .map(|item| RunItem {
                    inbox_item_id: item.inbox_item_id,
                    delivery_sequence: item.delivery_sequence,
                    disposition: item.disposition,
                })
                .collect(),
            outcome: snapshot.outcome,
            error_code: snapshot.error_code,
            continuation_note: snapshot.continuation_note,
            started_at: snapshot.started_at,
            finished_at: snapshot.finished_at,
        })
    }

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
        let task_view = task.view();
        if self.task_id.is_some_and(|task_id| task_id != task_view.id)
            || !task.linked_to(self.focus_thread_id)
        {
            return Err(DomainError::FocusOutsideTask);
        }
        self.task_id = Some(task_view.id);
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
        self.add_claimed_item(item_view.id, sequence)?;
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
        self.error_code = None;
        self.finished_at = Some(now);
    }

    /// Fails a Run whose ownership lease expired. Takes no fencing token: the point is that the
    /// owning Computer stopped proving ownership, so its token must not gate the recovery. Any later
    /// report from that Computer is rejected, because the Run is now terminal.
    ///
    /// Reachable from every non-terminal status, including `queued` and `starting`: a Computer that
    /// goes offline before starting the Driver leaves the Run just as stuck as one that dies mid-turn.
    pub(in crate::server) fn fail_expired_lease(
        &mut self,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.is_terminal() {
            return Err(DomainError::InvalidTransition);
        }
        for item in &mut self.items {
            item.disposition
                .get_or_insert(InboxItemDisposition::Released);
        }
        self.status = RunStatus::Failed;
        self.outcome = Some(RunOutcome::Failed);
        self.error_code = Some(RunErrorCode::ProcessLost);
        self.finished_at = Some(now);
        Ok(())
    }

    pub(in crate::server) fn is_terminal(&self) -> bool {
        matches!(
            self.status,
            RunStatus::Completed | RunStatus::Yielded | RunStatus::Failed | RunStatus::Canceled
        )
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
        error_code: Option<RunErrorCode>,
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
        // The database permits an error code only for a failed outcome.
        if error_code.is_some() && outcome != RunOutcome::Failed {
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

    pub(in crate::server) fn validate_fencing(&self, token_hash: &str) -> Result<(), DomainError> {
        if self.fencing_token_hash != token_hash {
            return Err(DomainError::StaleFencingToken);
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const TOKEN: &str = "token-hash";

    #[test]
    fn rehydrate_accepts_a_consistent_snapshot_and_rejects_a_live_outcome() {
        let snapshot = finalizing_run().snapshot();
        let restored = Run::rehydrate(snapshot.clone()).expect("snapshot is valid");
        assert_eq!(restored.snapshot(), snapshot);

        let mut invalid = snapshot;
        invalid.outcome = Some(RunOutcome::Completed);
        assert_eq!(
            Run::rehydrate(invalid),
            Err(DomainError::InvalidPersistedState)
        );

        let mut invalid = finalizing_run().snapshot();
        invalid.fencing_token_hash.clear();
        assert_eq!(
            Run::rehydrate(invalid),
            Err(DomainError::InvalidPersistedState)
        );
    }

    #[test]
    fn rehydrate_rejects_a_terminal_run_with_an_unresolved_item() {
        let mut run = finalizing_run();
        run.add_claimed_item(InboxItemId::from_uuid(uuid::Uuid::from_u128(5)), 1)
            .expect("item is unique");
        run.set_item_disposition(
            InboxItemId::from_uuid(uuid::Uuid::from_u128(5)),
            InboxItemDisposition::Handled,
        )
        .expect("item can be resolved");
        run.finish(
            TOKEN,
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

    fn finalizing_run() -> Run {
        Run {
            id: RunId::from_uuid(uuid::Uuid::from_u128(1)),
            space_id: SpaceId::from_uuid(uuid::Uuid::from_u128(2)),
            agent_id: MemberId::from_uuid(uuid::Uuid::from_u128(3)),
            task_id: None,
            focus_thread_id: ThreadId::from_uuid(uuid::Uuid::from_u128(4)),
            status: RunStatus::Finalizing,
            fencing_token_hash: TOKEN.to_owned(),
            lease_expires_at: OffsetDateTime::UNIX_EPOCH,
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
        let mut run = finalizing_run();
        run.finish(
            TOKEN,
            RunOutcome::Failed,
            Some(RunErrorCode::SessionLost),
            None,
            now,
        )
        .expect("a failed run accepts an error code");
        assert_eq!(run.status, RunStatus::Failed);
        assert_eq!(run.error_code, Some(RunErrorCode::SessionLost));
    }

    #[test]
    fn only_a_failed_outcome_carries_an_error_code() {
        let now = OffsetDateTime::UNIX_EPOCH;
        for outcome in [
            RunOutcome::Completed,
            RunOutcome::Yielded,
            RunOutcome::Canceled,
        ] {
            let mut run = finalizing_run();
            assert_eq!(
                run.finish(TOKEN, outcome, Some(RunErrorCode::Internal), None, now),
                Err(DomainError::InvalidTransition)
            );
            assert_eq!(run.status, RunStatus::Finalizing);
            assert_eq!(run.error_code, None);
            run.finish(TOKEN, outcome, None, None, now)
                .expect("a non-failed outcome finishes without an error code");
            assert_eq!(run.error_code, None);
        }
    }

    #[test]
    fn a_failed_run_may_omit_the_error_code() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let mut run = finalizing_run();
        run.finish(TOKEN, RunOutcome::Failed, None, None, now)
            .expect("an error code is optional");
        assert_eq!(run.error_code, None);
    }

    #[test]
    fn retirement_cancellation_leaves_no_error_code() {
        let mut run = finalizing_run();
        run.error_code = Some(RunErrorCode::ProcessLost);
        run.cancel_for_agent_retirement(OffsetDateTime::UNIX_EPOCH);
        assert_eq!(run.outcome, Some(RunOutcome::Canceled));
        assert_eq!(run.error_code, None);
    }

    #[test]
    fn an_expired_lease_fails_the_run_from_any_live_status_and_only_once() {
        let now = OffsetDateTime::UNIX_EPOCH;
        for status in [
            RunStatus::Queued,
            RunStatus::Starting,
            RunStatus::Running,
            RunStatus::Finalizing,
            RunStatus::Stopping,
        ] {
            let mut run = finalizing_run();
            run.status = status;
            run.fail_expired_lease(now)
                .expect("a live Run yields to lease expiry");
            assert_eq!(run.status, RunStatus::Failed);
            assert_eq!(run.outcome, Some(RunOutcome::Failed));
            assert_eq!(run.error_code, Some(RunErrorCode::ProcessLost));
            assert_eq!(run.finished_at, Some(now));
            // Terminal now, so a late report from the old Computer cannot reopen it.
            assert_eq!(
                run.fail_expired_lease(now),
                Err(DomainError::InvalidTransition)
            );
        }
    }

    #[test]
    fn reclaiming_an_expired_lease_preserves_a_disposition_the_agent_already_recorded() {
        let mut run = finalizing_run();
        let handled = InboxItemId::from_uuid(uuid::Uuid::from_u128(11));
        let untouched = InboxItemId::from_uuid(uuid::Uuid::from_u128(12));
        run.status = RunStatus::Running;
        run.items = vec![
            RunItem {
                inbox_item_id: handled,
                delivery_sequence: 1,
                disposition: Some(InboxItemDisposition::Handled),
            },
            RunItem {
                inbox_item_id: untouched,
                delivery_sequence: 2,
                disposition: None,
            },
        ];

        run.fail_expired_lease(OffsetDateTime::UNIX_EPOCH)
            .expect("a running Run yields to lease expiry");

        assert_eq!(
            run.items[0].disposition,
            Some(InboxItemDisposition::Handled),
            "work the Agent reported as handled is not undone by expiry"
        );
        assert_eq!(
            run.items[1].disposition,
            Some(InboxItemDisposition::Released)
        );
    }
}
