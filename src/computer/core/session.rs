use std::fmt;

use serde::{Deserialize, Serialize};
use time::OffsetDateTime;

use crate::ids::{AgentId, TaskId, ThreadId};

use super::CoreError;

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) enum DriverKind {
    Codex,
    Builtin,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub(in crate::computer) enum SessionScope {
    Thread(ThreadId),
    Task(TaskId),
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct SessionFingerprint {
    pub(in crate::computer) driver: DriverKind,
    pub(in crate::computer) workspace: String,
    pub(in crate::computer) role_revision: u64,
    pub(in crate::computer) audience: String,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) enum SessionState {
    Ready,
    InUse,
    Closing,
    Closed,
    Lost,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct ProviderSession {
    pub(in crate::computer) agent_id: AgentId,
    pub(in crate::computer) scope: SessionScope,
    pub(in crate::computer) generation: u64,
    pub(in crate::computer) locator: String,
    pub(in crate::computer) fingerprint: SessionFingerprint,
    pub(in crate::computer) state: SessionState,
    pub(in crate::computer) created_at: OffsetDateTime,
    pub(in crate::computer) last_resumed_at: Option<OffsetDateTime>,
    pub(in crate::computer) closed_at: Option<OffsetDateTime>,
}

impl fmt::Debug for ProviderSession {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ProviderSession")
            .field("agent_id", &self.agent_id)
            .field("scope", &self.scope)
            .field("generation", &self.generation)
            .field("locator", &"[REDACTED]")
            .field("fingerprint", &self.fingerprint)
            .field("state", &self.state)
            .field("created_at", &self.created_at)
            .field("last_resumed_at", &self.last_resumed_at)
            .field("closed_at", &self.closed_at)
            .finish()
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::computer) enum ResolveDecision {
    Resume(ProviderSession),
    Create {
        generation: u64,
        close: Option<ProviderSession>,
    },
}

pub(in crate::computer) fn resolve(
    sessions: &[ProviderSession],
    agent_id: AgentId,
    scope: SessionScope,
    fingerprint: &SessionFingerprint,
) -> ResolveDecision {
    let latest = sessions
        .iter()
        .filter(|session| session.agent_id == agent_id && session.scope == scope)
        .max_by_key(|session| session.generation);

    match latest {
        Some(session)
            if session.state == SessionState::Ready && session.fingerprint == *fingerprint =>
        {
            ResolveDecision::Resume(session.clone())
        }
        Some(session) => ResolveDecision::Create {
            generation: session.generation + 1,
            close: matches!(session.state, SessionState::Ready | SessionState::InUse)
                .then(|| session.clone()),
        },
        None => ResolveDecision::Create {
            generation: 1,
            close: None,
        },
    }
}

impl ProviderSession {
    pub(in crate::computer) fn begin_use(&mut self, now: OffsetDateTime) -> Result<(), CoreError> {
        if self.state != SessionState::Ready {
            return Err(CoreError::SessionUnavailable);
        }
        self.state = SessionState::InUse;
        self.last_resumed_at = Some(now);
        Ok(())
    }

    pub(in crate::computer) fn release(&mut self) -> Result<(), CoreError> {
        if self.state != SessionState::InUse {
            return Err(CoreError::InvalidTransition);
        }
        self.state = SessionState::Ready;
        Ok(())
    }

    pub(in crate::computer) fn promote(
        &mut self,
        focus: ThreadId,
        task_id: TaskId,
        fingerprint: &SessionFingerprint,
    ) -> Result<(), CoreError> {
        if self.scope != SessionScope::Thread(focus)
            || self.fingerprint != *fingerprint
            || !matches!(self.state, SessionState::Ready | SessionState::InUse)
        {
            return Err(CoreError::InvalidSessionPromotion);
        }
        self.scope = SessionScope::Task(task_id);
        Ok(())
    }

    pub(in crate::computer) fn mark_closing(&mut self) {
        if matches!(self.state, SessionState::Ready | SessionState::InUse) {
            self.state = SessionState::Closing;
        }
    }

    pub(in crate::computer) fn close(&mut self, succeeded: bool, now: OffsetDateTime) {
        self.state = if succeeded {
            SessionState::Closed
        } else {
            SessionState::Lost
        };
        self.locator.clear();
        self.closed_at = Some(now);
    }
}
