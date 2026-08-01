use std::fmt;

use async_trait::async_trait;
use serde::{Deserialize, Serialize};

use crate::ids::{AgentId, CommandId, EventId, InboxItemId, RunId};

use crate::computer::core::{
    home::{LocalAgent, MemoryFile},
    session::{ProviderSession, SessionFingerprint, SessionScope},
    supervisor::{DeliveryState, FencingToken, ItemDisposition, LocalRun, TerminalStatus},
};

use super::ApplicationError;
use super::command::Command;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) enum CommandStatus {
    Pending,
    Applied,
    Rejected,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::computer) struct StoredCommand {
    pub(in crate::computer) id: CommandId,
    pub(in crate::computer) sequence: u64,
    pub(in crate::computer) fingerprint: String,
    pub(in crate::computer) command: Command,
    pub(in crate::computer) status: CommandStatus,
    pub(in crate::computer) error: Option<ApplicationError>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::computer) enum LocalEvent {
    RunStarted {
        event_id: EventId,
        run_id: RunId,
        fencing_token: FencingToken,
    },
    Delivery {
        event_id: EventId,
        run_id: RunId,
        sequence: u64,
        outcome: DeliveryState,
        fencing_token: FencingToken,
    },
    RunResult {
        event_id: EventId,
        run_id: RunId,
        status: TerminalStatus,
        item_outcomes: Vec<(InboxItemId, ItemDisposition)>,
        continuation_note: Option<String>,
        error_code: Option<LocalErrorCode>,
        fencing_token: FencingToken,
    },
}

impl LocalEvent {
    pub(in crate::computer) fn id(&self) -> EventId {
        match self {
            Self::RunStarted { event_id, .. }
            | Self::Delivery { event_id, .. }
            | Self::RunResult { event_id, .. } => *event_id,
        }
    }
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) enum LocalErrorCode {
    ProcessLost,
    SessionLost,
    DriverUnavailable,
    Internal,
}

pub(in crate::computer) trait ComputerTransaction {
    fn command(&mut self, id: CommandId) -> Result<Option<StoredCommand>, ApplicationError>;
    fn insert_command(&mut self, command: StoredCommand) -> Result<(), ApplicationError>;
    fn save_command(&mut self, command: StoredCommand) -> Result<(), ApplicationError>;
    fn pending_commands(&mut self) -> Result<Vec<StoredCommand>, ApplicationError>;
    fn run(&mut self, id: RunId) -> Result<Option<LocalRun>, ApplicationError>;
    fn save_run(&mut self, run: LocalRun) -> Result<(), ApplicationError>;
    fn nonterminal_runs(&mut self) -> Result<Vec<LocalRun>, ApplicationError>;
    fn sessions(
        &mut self,
        agent_id: AgentId,
        scope: SessionScope,
    ) -> Result<Vec<ProviderSession>, ApplicationError>;
    fn agent_sessions(
        &mut self,
        agent_id: AgentId,
    ) -> Result<Vec<ProviderSession>, ApplicationError>;
    fn save_session(&mut self, session: ProviderSession) -> Result<(), ApplicationError>;
    fn delete_session(
        &mut self,
        agent_id: AgentId,
        scope: SessionScope,
        generation: u64,
    ) -> Result<(), ApplicationError>;
    fn append_event(&mut self, event: LocalEvent) -> Result<(), ApplicationError>;
    fn pending_events(&mut self) -> Result<Vec<LocalEvent>, ApplicationError>;
    fn acknowledge_event(&mut self, event_id: EventId) -> Result<(), ApplicationError>;
}

#[async_trait]
pub(in crate::computer) trait AgentHomePort: Send {
    async fn agent(&mut self, agent_id: AgentId) -> Result<LocalAgent, ApplicationError>;
    async fn provision(&mut self, agent: LocalAgent) -> Result<(), ApplicationError>;
    async fn configure(&mut self, agent: LocalAgent) -> Result<(), ApplicationError>;
    async fn suspend(&mut self, agent_id: AgentId) -> Result<(), ApplicationError>;
    async fn retire(&mut self, agent_id: AgentId) -> Result<(), ApplicationError>;
    async fn workspace_fingerprint(
        &mut self,
        agent_id: AgentId,
    ) -> Result<String, ApplicationError>;
    async fn list_memory(&mut self, agent_id: AgentId)
    -> Result<Vec<MemoryFile>, ApplicationError>;
    async fn read_memory(
        &mut self,
        agent_id: AgentId,
        path: &std::path::Path,
    ) -> Result<Vec<u8>, ApplicationError>;
    async fn write_memory(
        &mut self,
        agent_id: AgentId,
        path: &std::path::Path,
        content: &[u8],
    ) -> Result<(), ApplicationError>;
}

pub(in crate::computer) trait TransactionPort {
    type Transaction: ComputerTransaction;

    async fn transact<T>(
        &mut self,
        operation: impl for<'a> AsyncFnOnce(&'a mut Self::Transaction) -> Result<T, ApplicationError>,
    ) -> Result<T, ApplicationError>;
}

#[derive(Clone, Eq, PartialEq)]
pub(in crate::computer) struct OpenSessionRequest {
    pub(in crate::computer) agent_id: AgentId,
    pub(in crate::computer) scope: SessionScope,
    pub(in crate::computer) generation: u64,
    pub(in crate::computer) fingerprint: SessionFingerprint,
    pub(in crate::computer) resume_locator: Option<String>,
    pub(in crate::computer) run_token: FencingToken,
}

impl fmt::Debug for OpenSessionRequest {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("OpenSessionRequest")
            .field("agent_id", &self.agent_id)
            .field("scope", &self.scope)
            .field("generation", &self.generation)
            .field("fingerprint", &self.fingerprint)
            .field(
                "resume_locator",
                &self.resume_locator.as_ref().map(|_| "[REDACTED]"),
            )
            .field("run_token", &"[REDACTED]")
            .finish()
    }
}

#[derive(Clone, Eq, PartialEq)]
pub(in crate::computer) struct OpenedSession {
    pub(in crate::computer) locator: String,
    pub(in crate::computer) resumed: bool,
}

impl fmt::Debug for OpenedSession {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("OpenedSession")
            .field("locator", &"[REDACTED]")
            .field("resumed", &self.resumed)
            .finish()
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) enum SteerOutcome {
    Accepted,
    TooLate,
    Unsupported,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) enum ProcessEvidence {
    Controlled,
    Lost,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) enum DriverTurnOutcome {
    Completed,
    Failed,
    Interrupted,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) struct DriverCompletion {
    pub(in crate::computer) run_id: RunId,
    pub(in crate::computer) outcome: DriverTurnOutcome,
}

#[async_trait(?Send)]
pub(in crate::computer) trait DriverPort {
    async fn validate(&mut self, agent: &LocalAgent) -> Result<(), ApplicationError>;
    async fn open_or_resume(
        &mut self,
        request: OpenSessionRequest,
    ) -> Result<OpenedSession, ApplicationError>;
    async fn start_turn(&mut self, run: &LocalRun, locator: &str) -> Result<(), ApplicationError>;
    async fn steer(
        &mut self,
        run: &LocalRun,
        sequence: u64,
    ) -> Result<SteerOutcome, ApplicationError>;
    async fn notice(&mut self, run: &LocalRun) -> Result<(), ApplicationError>;
    async fn interrupt(&mut self, run: &LocalRun) -> Result<(), ApplicationError>;
    async fn close_session(&mut self, session: &ProviderSession) -> Result<(), ApplicationError>;
    async fn process_evidence(
        &mut self,
        run: &LocalRun,
    ) -> Result<ProcessEvidence, ApplicationError>;
    async fn poll_completions(&mut self) -> Result<Vec<DriverCompletion>, ApplicationError>;
}
