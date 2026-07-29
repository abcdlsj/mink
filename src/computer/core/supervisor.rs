use std::{collections::BTreeMap, fmt};

use crate::ids::{AgentId, InboxItemId, NoticeId, RunId, TaskId, ThreadId};

use super::{
    CoreError,
    input::{AttentionNoticeInput, ClaimedItemInput, RunInput},
    scheduler::RunPriority,
    session::{SessionFingerprint, SessionScope},
};

#[derive(Clone, Eq, PartialEq)]
pub(in crate::computer) struct FencingToken(String);

impl FencingToken {
    pub(in crate::computer) fn new(value: String) -> Self {
        Self(value)
    }

    pub(in crate::computer) fn expose(&self) -> &str {
        &self.0
    }
}

impl fmt::Debug for FencingToken {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("FencingToken([REDACTED])")
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) enum LocalRunState {
    Queued,
    Starting,
    Running,
    Finalizing,
    Stopping,
    Completed,
    Yielded,
    Failed,
    Canceled,
}

impl LocalRunState {
    pub(in crate::computer) fn is_terminal(self) -> bool {
        matches!(
            self,
            Self::Completed | Self::Yielded | Self::Failed | Self::Canceled
        )
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) enum DeliveryState {
    Pending,
    Accepted,
    TooLate,
    Unsupported,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::computer) struct Delivery {
    pub(in crate::computer) sequence: u64,
    pub(in crate::computer) item: ClaimedItemInput,
    pub(in crate::computer) state: DeliveryState,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::computer) struct NoticeDelivery {
    pub(in crate::computer) notice: AttentionNoticeInput,
    pub(in crate::computer) delivered: bool,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) enum TerminalStatus {
    Completed,
    Yielded,
    Failed,
    Canceled,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) enum ItemDisposition {
    Handled,
    Deferred,
    Released,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::computer) struct LocalRun {
    pub(in crate::computer) id: RunId,
    pub(in crate::computer) agent_id: AgentId,
    pub(in crate::computer) task_id: Option<TaskId>,
    pub(in crate::computer) focus_thread_id: ThreadId,
    pub(in crate::computer) fencing_token: FencingToken,
    pub(in crate::computer) priority: RunPriority,
    pub(in crate::computer) ownership_lease_expires_at: time::OffsetDateTime,
    pub(in crate::computer) input: RunInput,
    pub(in crate::computer) state: LocalRunState,
    pub(in crate::computer) session: Option<(SessionScope, u64)>,
    pub(in crate::computer) session_fingerprint: Option<SessionFingerprint>,
    pub(in crate::computer) deliveries: BTreeMap<u64, Delivery>,
    pub(in crate::computer) notices: BTreeMap<NoticeId, NoticeDelivery>,
    pub(in crate::computer) terminal_status: Option<TerminalStatus>,
}

pub(in crate::computer) struct NewRun {
    pub(in crate::computer) id: RunId,
    pub(in crate::computer) agent_id: AgentId,
    pub(in crate::computer) task_id: Option<TaskId>,
    pub(in crate::computer) focus_thread_id: ThreadId,
    pub(in crate::computer) fencing_token: FencingToken,
    pub(in crate::computer) priority: RunPriority,
    pub(in crate::computer) ownership_lease_expires_at: time::OffsetDateTime,
    pub(in crate::computer) input: RunInput,
}

impl LocalRun {
    pub(in crate::computer) fn new(spec: NewRun) -> Result<Self, CoreError> {
        if spec.input.agent.agent_id != spec.agent_id
            || spec.input.context.focus_thread_id != spec.focus_thread_id
            || spec.input.work.task.as_ref().map(|task| task.task_id) != spec.task_id
            || spec.task_id.is_some()
                && !spec
                    .input
                    .work
                    .linked_thread_ids
                    .contains(&spec.focus_thread_id)
        {
            return Err(CoreError::InputScopeMismatch);
        }
        let initial_items = spec.input.context.claimed_items.clone();
        let mut run = Self {
            id: spec.id,
            agent_id: spec.agent_id,
            task_id: spec.task_id,
            focus_thread_id: spec.focus_thread_id,
            fencing_token: spec.fencing_token,
            priority: spec.priority,
            ownership_lease_expires_at: spec.ownership_lease_expires_at,
            input: spec.input,
            state: LocalRunState::Queued,
            session: None,
            session_fingerprint: None,
            deliveries: BTreeMap::new(),
            notices: BTreeMap::new(),
            terminal_status: None,
        };
        for (index, item) in initial_items.into_iter().enumerate() {
            run.attach(index as u64 + 1, item)?;
        }
        Ok(run)
    }

    pub(in crate::computer) fn begin_start(&mut self) -> Result<(), CoreError> {
        if self.state != LocalRunState::Queued {
            return Err(CoreError::InvalidTransition);
        }
        self.state = LocalRunState::Starting;
        Ok(())
    }

    pub(in crate::computer) fn started(
        &mut self,
        scope: SessionScope,
        generation: u64,
    ) -> Result<(), CoreError> {
        if self.state != LocalRunState::Starting {
            return Err(CoreError::InvalidTransition);
        }
        self.session = Some((scope, generation));
        self.state = LocalRunState::Running;
        Ok(())
    }

    pub(in crate::computer) fn attach(
        &mut self,
        sequence: u64,
        item: ClaimedItemInput,
    ) -> Result<bool, CoreError> {
        if !matches!(
            self.state,
            LocalRunState::Queued | LocalRunState::Starting | LocalRunState::Running
        ) {
            return Err(CoreError::RunNotAcceptingDeliveries);
        }
        if item.task_id != self.task_id || item.thread_id != self.focus_thread_id {
            return Err(CoreError::ScopeMismatch);
        }
        if let Some(existing) = self.deliveries.get(&sequence) {
            if existing.item.item_id == item.item_id && existing.item == item {
                return Ok(false);
            }
            return Err(CoreError::ConflictingDelivery);
        }
        let expected = self
            .deliveries
            .last_key_value()
            .map_or(1, |(sequence, _)| sequence + 1);
        if sequence != expected {
            return Err(CoreError::InvalidDeliverySequence);
        }
        self.deliveries.insert(
            sequence,
            Delivery {
                sequence,
                item,
                state: DeliveryState::Pending,
            },
        );
        Ok(true)
    }

    pub(in crate::computer) fn record_delivery(
        &mut self,
        sequence: u64,
        state: DeliveryState,
    ) -> Result<(), CoreError> {
        let delivery = self
            .deliveries
            .get_mut(&sequence)
            .ok_or(CoreError::InvalidDeliverySequence)?;
        if delivery.state == DeliveryState::Pending || delivery.state == state {
            delivery.state = state;
            return Ok(());
        }
        Err(CoreError::ConflictingDelivery)
    }

    pub(in crate::computer) fn add_notice(
        &mut self,
        notice: AttentionNoticeInput,
    ) -> Result<bool, CoreError> {
        if let Some(existing) = self.notices.get(&notice.notice_id) {
            return if existing.notice == notice {
                Ok(false)
            } else {
                Err(CoreError::ConflictingNotice)
            };
        }
        self.notices.insert(
            notice.notice_id,
            NoticeDelivery {
                notice,
                delivered: false,
            },
        );
        Ok(true)
    }

    pub(in crate::computer) fn notice_is_pending(&self, notice_id: NoticeId) -> bool {
        self.notices
            .get(&notice_id)
            .is_some_and(|delivery| !delivery.delivered)
    }

    pub(in crate::computer) fn record_notice(
        &mut self,
        notice_id: NoticeId,
    ) -> Result<(), CoreError> {
        let delivery = self
            .notices
            .get_mut(&notice_id)
            .ok_or(CoreError::InvalidTransition)?;
        delivery.delivered = true;
        Ok(())
    }

    pub(in crate::computer) fn bind_task(&mut self, task_id: TaskId) -> Result<(), CoreError> {
        if self.task_id.is_some_and(|existing| existing != task_id) || self.state.is_terminal() {
            return Err(CoreError::InvalidTransition);
        }
        self.task_id = Some(task_id);
        for delivery in self.deliveries.values_mut() {
            delivery.item.task_id = Some(task_id);
        }
        Ok(())
    }

    pub(in crate::computer) fn begin_finalizing(&mut self) -> Result<(), CoreError> {
        if self.state != LocalRunState::Running {
            return Err(CoreError::InvalidTransition);
        }
        self.state = LocalRunState::Finalizing;
        Ok(())
    }

    pub(in crate::computer) fn request_stop(&mut self) -> Result<(), CoreError> {
        if !matches!(self.state, LocalRunState::Starting | LocalRunState::Running) {
            return Err(CoreError::InvalidTransition);
        }
        self.state = LocalRunState::Stopping;
        Ok(())
    }

    pub(in crate::computer) fn finish(&mut self, status: TerminalStatus) -> Result<(), CoreError> {
        let valid = match status {
            TerminalStatus::Canceled => self.state == LocalRunState::Stopping,
            _ => self.state == LocalRunState::Finalizing,
        };
        if !valid {
            return Err(CoreError::InvalidTransition);
        }
        self.state = match status {
            TerminalStatus::Completed => LocalRunState::Completed,
            TerminalStatus::Yielded => LocalRunState::Yielded,
            TerminalStatus::Failed => LocalRunState::Failed,
            TerminalStatus::Canceled => LocalRunState::Canceled,
        };
        self.terminal_status = Some(status);
        Ok(())
    }

    pub(in crate::computer) fn validate_item_outcomes(
        &self,
        outcomes: &[(InboxItemId, ItemDisposition)],
    ) -> Result<(), CoreError> {
        let mut item_ids: Vec<_> = outcomes.iter().map(|(item_id, _)| *item_id).collect();
        item_ids.sort_unstable();
        item_ids.dedup();
        let mut delivered: Vec<_> = self
            .deliveries
            .values()
            .map(|delivery| delivery.item.item_id)
            .collect();
        delivered.sort_unstable();
        if item_ids != delivered {
            return Err(CoreError::IncompleteItemDisposition);
        }
        for delivery in self.deliveries.values() {
            if matches!(
                delivery.state,
                DeliveryState::TooLate | DeliveryState::Unsupported
            ) && !outcomes.iter().any(|(item_id, disposition)| {
                *item_id == delivery.item.item_id && *disposition == ItemDisposition::Released
            }) {
                return Err(CoreError::IncompleteItemDisposition);
            }
        }
        Ok(())
    }

    pub(in crate::computer) fn renew_lease(
        &mut self,
        lease_expires_at: time::OffsetDateTime,
    ) -> Result<(), CoreError> {
        if self.state.is_terminal() || lease_expires_at <= self.ownership_lease_expires_at {
            return Err(CoreError::InvalidTransition);
        }
        self.ownership_lease_expires_at = lease_expires_at;
        Ok(())
    }
}
