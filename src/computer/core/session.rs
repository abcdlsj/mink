use std::fmt;

use serde::{Deserialize, Deserializer, Serialize};
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

#[derive(Clone, Eq, PartialEq, Serialize)]
pub(in crate::computer) struct ProviderSession {
    agent_id: AgentId,
    scope: SessionScope,
    generation: u64,
    locator: String,
    fingerprint: SessionFingerprint,
    state: SessionState,
    created_at: OffsetDateTime,
    last_resumed_at: Option<OffsetDateTime>,
    closed_at: Option<OffsetDateTime>,
}

#[derive(Clone, Eq, PartialEq, Deserialize)]
pub(in crate::computer) struct ProviderSessionSnapshot {
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

impl fmt::Debug for ProviderSessionSnapshot {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ProviderSessionSnapshot")
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

impl<'de> Deserialize<'de> for ProviderSession {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let snapshot = ProviderSessionSnapshot::deserialize(deserializer)?;
        Self::rehydrate(snapshot).map_err(serde::de::Error::custom)
    }
}

pub(in crate::computer) struct ProviderSessionView<'a> {
    pub(in crate::computer) agent_id: AgentId,
    pub(in crate::computer) scope: SessionScope,
    pub(in crate::computer) generation: u64,
    pub(in crate::computer) locator: &'a str,
    pub(in crate::computer) fingerprint: &'a SessionFingerprint,
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

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) enum ContinuityState {
    Warm,
    Cold,
    Lost,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) struct Continuity {
    pub(in crate::computer) state: ContinuityState,
    pub(in crate::computer) generation: Option<u64>,
}

pub(in crate::computer) fn continuity(
    sessions: &[ProviderSession],
    agent_id: AgentId,
    scope: SessionScope,
) -> Continuity {
    let latest = sessions
        .iter()
        .filter(|session| session.agent_id == agent_id && session.scope == scope)
        .max_by_key(|session| session.generation);
    let Some(session) = latest else {
        return Continuity {
            state: ContinuityState::Cold,
            generation: None,
        };
    };
    let state = match session.state {
        SessionState::Ready | SessionState::InUse => ContinuityState::Warm,
        SessionState::Closing | SessionState::Closed => ContinuityState::Cold,
        SessionState::Lost => ContinuityState::Lost,
    };
    Continuity {
        state,
        generation: Some(session.generation),
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
    pub(in crate::computer) fn create(
        agent_id: AgentId,
        scope: SessionScope,
        generation: u64,
        locator: String,
        fingerprint: SessionFingerprint,
        created_at: OffsetDateTime,
    ) -> Result<Self, CoreError> {
        Self::rehydrate(ProviderSessionSnapshot {
            agent_id,
            scope,
            generation,
            locator,
            fingerprint,
            state: SessionState::Ready,
            created_at,
            last_resumed_at: None,
            closed_at: None,
        })
    }

    pub(in crate::computer) fn rehydrate(
        snapshot: ProviderSessionSnapshot,
    ) -> Result<Self, CoreError> {
        if snapshot.generation == 0
            || snapshot
                .last_resumed_at
                .is_some_and(|value| value < snapshot.created_at)
            || snapshot
                .closed_at
                .is_some_and(|value| value < snapshot.created_at)
            || snapshot.closed_at.is_some_and(|value| {
                snapshot
                    .last_resumed_at
                    .is_some_and(|resumed| value < resumed)
            })
        {
            return Err(CoreError::InvalidSessionState);
        }
        match snapshot.state {
            SessionState::Ready | SessionState::InUse | SessionState::Closing => {
                if snapshot.locator.is_empty() || snapshot.closed_at.is_some() {
                    return Err(CoreError::InvalidSessionState);
                }
            }
            SessionState::Closed | SessionState::Lost => {
                if !snapshot.locator.is_empty() || snapshot.closed_at.is_none() {
                    return Err(CoreError::InvalidSessionState);
                }
            }
        }
        Ok(Self {
            agent_id: snapshot.agent_id,
            scope: snapshot.scope,
            generation: snapshot.generation,
            locator: snapshot.locator,
            fingerprint: snapshot.fingerprint,
            state: snapshot.state,
            created_at: snapshot.created_at,
            last_resumed_at: snapshot.last_resumed_at,
            closed_at: snapshot.closed_at,
        })
    }

    pub(in crate::computer) fn view(&self) -> ProviderSessionView<'_> {
        ProviderSessionView {
            agent_id: self.agent_id,
            scope: self.scope,
            generation: self.generation,
            locator: &self.locator,
            fingerprint: &self.fingerprint,
            state: self.state,
            created_at: self.created_at,
            last_resumed_at: self.last_resumed_at,
            closed_at: self.closed_at,
        }
    }

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
