use std::{collections::BTreeSet, fmt};

use serde::{Deserialize, Serialize};
use time::OffsetDateTime;

use crate::ids::{
    AgentId, ChannelId, CommandId, DaemonSessionId, EventId, InboxItemId, MemberId, MessageId,
    NoticeId, QueryId, RunId, SpaceId, TaskId, ThreadId,
};

use super::version::{ProtocolVersion, ProtocolVersionRange};

#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(transparent)]
pub(crate) struct CommandSequence(pub(crate) u64);

#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(transparent)]
pub(crate) struct DeliverySequence(pub(crate) u64);

#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum DaemonCapability {
    ActiveTurnSteer,
    ProviderSessionResume,
    ProviderSessionReset,
    Sandbox,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct ComputerHello {
    pub(crate) supported_versions: ProtocolVersionRange,
    pub(crate) daemon_version: String,
    pub(crate) capabilities: BTreeSet<DaemonCapability>,
    pub(crate) daemon_session_id: DaemonSessionId,
    pub(crate) command_watermark: CommandSequence,
    /// Runs this daemon still holds locally, plus terminal Runs whose result is still pending delivery.
    /// The Server fails every other non-terminal Run it has for this Computer, because those died with
    /// the previous daemon process. Sending the protected set rather than the lost set means a daemon
    /// that starts with empty state reports the truth by default.
    pub(crate) live_run_ids: Vec<RunId>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(tag = "type", rename_all = "snake_case", deny_unknown_fields)]
pub(crate) enum ServerHandshake {
    Welcome {
        selected_version: ProtocolVersion,
        supported_versions: ProtocolVersionRange,
        heartbeat_interval_seconds: u32,
    },
    Rejected {
        code: HandshakeErrorCode,
        supported_versions: ProtocolVersionRange,
    },
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum HandshakeErrorCode {
    NoCommonVersion,
    ComputerDeleted,
    Unauthenticated,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct CommandEnvelope {
    pub(crate) command_id: CommandId,
    pub(crate) sequence: CommandSequence,
    pub(crate) command: Command,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(tag = "kind", content = "payload", deny_unknown_fields)]
pub(crate) enum Command {
    #[serde(rename = "agent.provision")]
    AgentProvision(AgentConfiguration),
    #[serde(rename = "agent.configure")]
    AgentConfigure(AgentConfiguration),
    #[serde(rename = "agent.suspend")]
    AgentSuspend(AgentSuspend),
    #[serde(rename = "agent.resume")]
    AgentResume(AgentResume),
    #[serde(rename = "agent.restart")]
    AgentRestart(AgentRestart),
    #[serde(rename = "agent.retire")]
    AgentRetire(AgentRetire),
    #[serde(rename = "run.start")]
    RunStart(RunStart),
    #[serde(rename = "run.task_bound")]
    RunTaskBound(RunTaskBound),
    #[serde(rename = "run.attach_item")]
    RunAttachItem(RunAttachItem),
    #[serde(rename = "run.notice")]
    RunNotice(RunNotice),
    #[serde(rename = "run.stop")]
    RunStop(RunStop),
    #[serde(rename = "session.reset")]
    SessionReset(SessionCommand),
    #[serde(rename = "session.close")]
    SessionClose(SessionCommand),
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct AgentConfiguration {
    pub(crate) agent_id: AgentId,
    pub(crate) space_id: SpaceId,
    pub(crate) name: String,
    pub(crate) role: RoleSnapshot,
    pub(crate) driver: DriverKind,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct RoleSnapshot {
    pub(crate) revision: u64,
    pub(crate) text: String,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum DriverKind {
    Codex,
    Builtin,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum SuspendMode {
    AfterCurrentRun,
    CancelCurrentRun,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct AgentSuspend {
    pub(crate) agent_id: AgentId,
    pub(crate) mode: SuspendMode,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct AgentResume {
    pub(crate) agent_id: AgentId,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct AgentRestart {
    pub(crate) agent_id: AgentId,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct AgentRetire {
    pub(crate) agent_id: AgentId,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct RunStart {
    pub(crate) run_id: RunId,
    pub(crate) agent_id: AgentId,
    pub(crate) task: Option<TaskSnapshot>,
    pub(crate) focus: FocusSnapshot,
    pub(crate) dispatched_items: Vec<InboxItemSnapshot>,
    pub(crate) space_members: Vec<SpaceMemberSnapshot>,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct SpaceMemberSnapshot {
    pub(crate) member_id: MemberId,
    pub(crate) display_name: String,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct RunTaskBound {
    pub(crate) run_id: RunId,
    pub(crate) task: TaskSnapshot,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct RunAttachItem {
    pub(crate) run_id: RunId,
    pub(crate) delivery_sequence: DeliverySequence,
    pub(crate) item: InboxItemSnapshot,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct RunNotice {
    pub(crate) run_id: RunId,
    pub(crate) notice: AttentionNotice,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct RunStop {
    pub(crate) run_id: RunId,
    pub(crate) reason: StopReason,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum StopReason {
    Suspend,
    Retire,
    HumanRequest,
    SafetyLimit,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct SessionCommand {
    pub(crate) agent_id: AgentId,
    pub(crate) scope: SessionScope,
    pub(crate) reason: SessionChangeReason,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(
    tag = "kind",
    content = "id",
    rename_all = "snake_case",
    deny_unknown_fields
)]
pub(crate) enum SessionScope {
    Thread(ThreadId),
    Task(TaskId),
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum SessionChangeReason {
    ExplicitReset,
    TaskFinished,
    AudienceChanged,
    DriverChanged,
    RoleChanged,
    WorkspaceChanged,
    AgentRetired,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct TaskSnapshot {
    pub(crate) task_id: TaskId,
    pub(crate) seq: u64,
    pub(crate) title: String,
    pub(crate) status: TaskStatus,
    pub(crate) source_thread_id: ThreadId,
    pub(crate) linked_thread_ids: Vec<ThreadId>,
    pub(crate) result_message_id: Option<MessageId>,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum TaskStatus {
    Todo,
    InProgress,
    InReview,
    Done,
    Closed,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct FocusSnapshot {
    pub(crate) thread_id: ThreadId,
    pub(crate) channel_id: ChannelId,
    pub(crate) root: MessageSnapshot,
    pub(crate) replies: Vec<MessageSnapshot>,
    pub(crate) message_sequence: u64,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct MessageSnapshot {
    pub(crate) message_id: MessageId,
    pub(crate) author_member_id: MemberId,
    pub(crate) sequence: u64,
    pub(crate) content: MessageContent,
    #[serde(with = "time::serde::rfc3339")]
    pub(crate) created_at: OffsetDateTime,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(tag = "type", rename_all = "snake_case", deny_unknown_fields)]
pub(crate) enum MessageContent {
    Text {
        markdown: String,
    },
    Action {
        action: ActionKind,
        target: ActionTarget,
    },
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum ActionKind {
    ChannelCreated,
    AgentCreated,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(
    tag = "type",
    content = "id",
    rename_all = "snake_case",
    deny_unknown_fields
)]
pub(crate) enum ActionTarget {
    Channel(ChannelId),
    Agent(AgentId),
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct InboxItemSnapshot {
    pub(crate) item_id: InboxItemId,
    pub(crate) source_kind: InboxSourceKind,
    pub(crate) strength: AttentionStrength,
    pub(crate) channel_id: ChannelId,
    pub(crate) thread_id: ThreadId,
    pub(crate) task_id: Option<TaskId>,
    pub(crate) message: Option<MessageSnapshot>,
    pub(crate) activity_events: Vec<ActivityEventSnapshot>,
    #[serde(with = "time::serde::rfc3339")]
    pub(crate) available_at: OffsetDateTime,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct ActivityEventSnapshot {
    pub(crate) sequence: u64,
    pub(crate) kind: ActivityEventKind,
    pub(crate) message_id: Option<MessageId>,
    pub(crate) member_id: Option<MemberId>,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum ActivityEventKind {
    Message,
    MemberJoined,
    MemberLeft,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum InboxSourceKind {
    Direct,
    Mention,
    Reply,
    TaskActivity,
    ThreadActivity,
    ChannelActivity,
    System,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum AttentionStrength {
    Hard,
    Ambient,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct AttentionNotice {
    pub(crate) notice_id: NoticeId,
    pub(crate) source_kind: InboxSourceKind,
    pub(crate) strength: AttentionStrength,
    pub(crate) location: NoticeLocation,
    pub(crate) explicit_human_redirect: bool,
    #[serde(with = "time::serde::rfc3339")]
    pub(crate) arrived_at: OffsetDateTime,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(tag = "visibility", rename_all = "snake_case", deny_unknown_fields)]
pub(crate) enum NoticeLocation {
    Restricted,
    Visible {
        task_id: Option<TaskId>,
        thread_id: ThreadId,
    },
}

// Server frames intentionally omit Debug because they may carry Message content.
#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(tag = "type", rename_all = "snake_case", deny_unknown_fields)]
pub(crate) enum ServerFrame {
    Command { envelope: Box<CommandEnvelope> },
    Receipt { receipt: Receipt },
    Query { query: QueryEnvelope },
    Shutdown { code: ShutdownCode },
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(tag = "type", rename_all = "snake_case", deny_unknown_fields)]
pub(crate) enum ComputerFrame {
    Heartbeat { heartbeat: Heartbeat },
    CommandAck { ack: CommandAck },
    CommandResult { result: CommandResult },
    RunStarted { started: RunStarted },
    DeliveryReceipt { receipt: DeliveryReceipt },
    RunResult { result: RunResult },
    QueryResult { result: QueryResultEnvelope },
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct QueryEnvelope {
    pub(crate) query_id: QueryId,
    pub(crate) query: Query,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(tag = "kind", content = "payload", deny_unknown_fields)]
pub(crate) enum Query {
    #[serde(rename = "session.continuity")]
    SessionContinuity(SessionContinuityQuery),
    #[serde(rename = "runtime.diagnostics")]
    RuntimeDiagnostics(RuntimeDiagnosticsQuery),
    #[serde(rename = "memory.list")]
    MemoryList(MemoryQuery),
    #[serde(rename = "memory.read")]
    MemoryRead(MemoryReadQuery),
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct SessionContinuityQuery {
    pub(crate) agent_id: AgentId,
    pub(crate) scope: SessionScope,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct RuntimeDiagnosticsQuery {
    pub(crate) agent_id: AgentId,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct MemoryQuery {
    pub(crate) agent_id: AgentId,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct MemoryReadQuery {
    pub(crate) agent_id: AgentId,
    pub(crate) path: String,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct QueryResultEnvelope {
    pub(crate) query_id: QueryId,
    pub(crate) result: QueryResult,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(tag = "kind", content = "payload", deny_unknown_fields)]
pub(crate) enum QueryResult {
    #[serde(rename = "session.continuity")]
    SessionContinuity(SessionContinuityResult),
    #[serde(rename = "runtime.diagnostics")]
    RuntimeDiagnostics(RuntimeDiagnosticsResult),
    #[serde(rename = "memory.list")]
    MemoryList(MemoryListResult),
    #[serde(rename = "memory.read")]
    MemoryRead(MemoryReadResult),
    #[serde(rename = "unavailable")]
    Unavailable { code: QueryErrorCode },
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum QueryErrorCode {
    UnknownAgent,
    UnknownPath,
    SessionLost,
    DriverUnavailable,
    Unreachable,
    Internal,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct SessionContinuityResult {
    pub(crate) state: SessionContinuityState,
    pub(crate) generation: Option<u64>,
    pub(crate) reason_code: Option<String>,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum RuntimeRunState {
    Queued,
    Starting,
    Running,
    Finalizing,
    Stopping,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct RuntimeDiagnosticsResult {
    pub(crate) local_run_id: Option<RunId>,
    pub(crate) local_run_state: Option<RuntimeRunState>,
    pub(crate) queued_runs: u32,
    pub(crate) active_runs: u32,
    pub(crate) pending_commands: u32,
    pub(crate) pending_result_events: u32,
    pub(crate) warm_sessions: u32,
    pub(crate) cold_sessions: u32,
    pub(crate) reset_required_sessions: u32,
    #[serde(with = "time::serde::rfc3339")]
    pub(crate) observed_at: OffsetDateTime,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum SessionContinuityState {
    Warm,
    Cold,
    Lost,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct MemoryListResult {
    pub(crate) files: Vec<MemoryFileProjection>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct MemoryFileProjection {
    pub(crate) path: String,
    pub(crate) size: u64,
    pub(crate) sha256: String,
    #[serde(with = "time::serde::rfc3339")]
    pub(crate) updated_at: OffsetDateTime,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct MemoryReadResult {
    pub(crate) file: MemoryFileProjection,
    pub(crate) content: String,
}

impl fmt::Debug for MemoryReadResult {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("MemoryReadResult")
            .field("file", &self.file)
            .field("content", &"[REDACTED]")
            .finish()
    }
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Heartbeat {
    pub(crate) daemon_session_id: DaemonSessionId,
    pub(crate) active_runs: u32,
    #[serde(with = "time::serde::rfc3339")]
    pub(crate) observed_at: OffsetDateTime,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct CommandAck {
    pub(crate) command_id: CommandId,
    pub(crate) sequence: CommandSequence,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct CommandResult {
    pub(crate) command_id: CommandId,
    pub(crate) sequence: CommandSequence,
    pub(crate) outcome: CommandOutcome,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(tag = "status", rename_all = "snake_case", deny_unknown_fields)]
pub(crate) enum CommandOutcome {
    Applied,
    Rejected { code: ComputerErrorCode },
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct RunStarted {
    pub(crate) event_id: EventId,
    pub(crate) run_id: RunId,
    #[serde(with = "time::serde::rfc3339")]
    pub(crate) observed_at: OffsetDateTime,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct DeliveryReceipt {
    pub(crate) event_id: EventId,
    pub(crate) run_id: RunId,
    pub(crate) delivery_sequence: DeliverySequence,
    pub(crate) outcome: DeliveryOutcome,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum DeliveryOutcome {
    Accepted,
    TooLate,
    Unsupported,
}

#[derive(Clone, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct RunResult {
    pub(crate) event_id: EventId,
    pub(crate) run_id: RunId,
    pub(crate) status: RunTerminalStatus,
    pub(crate) item_outcomes: Vec<ItemOutcome>,
    pub(crate) continuation_note: Option<String>,
    pub(crate) error_code: Option<ComputerErrorCode>,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum RunTerminalStatus {
    Completed,
    Yielded,
    Failed,
    Canceled,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct ItemOutcome {
    pub(crate) item_id: InboxItemId,
    pub(crate) disposition: ItemDisposition,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum ItemDisposition {
    Handled,
    Deferred,
    Released,
}

/// Failures the Computer observed directly. There is no code for "the Server lost contact": that is
/// Computer reachability, which the Server derives from the connection, not from a report.
#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum ComputerErrorCode {
    DriverError,
    DriverLost,
    ComputerRestarted,
    SessionUnavailable,
    AgentUnavailable,
    InvalidCommand,
    Internal,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Receipt {
    pub(crate) event_id: EventId,
    pub(crate) kind: ReceiptKind,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum ReceiptKind {
    RunStarted,
    Delivery,
    RunResult,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum ShutdownCode {
    ComputerDeleted,
    ReplacedConnection,
    ServerShutdown,
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeSet;

    use serde_json::json;
    use uuid::Uuid;

    use super::{
        AttentionNotice, AttentionStrength, Command, CommandEnvelope, CommandSequence,
        ComputerFrame, ComputerHello, DaemonCapability, InboxSourceKind, MemoryFileProjection,
        MemoryReadResult, NoticeLocation, Query, QueryEnvelope, QueryErrorCode, QueryResult,
        QueryResultEnvelope, RunStop, RuntimeDiagnosticsQuery, RuntimeDiagnosticsResult,
        RuntimeRunState, ServerFrame, SessionContinuityQuery, SessionScope, StopReason,
    };
    use crate::{
        ids::{AgentId, CommandId, DaemonSessionId, NoticeId, QueryId, RunId, TaskId},
        protocol::version::SUPPORTED,
    };
    use time::OffsetDateTime;

    #[test]
    fn hello_exchanges_version_range_and_daemon_capabilities() {
        let hello = ComputerHello {
            supported_versions: SUPPORTED,
            daemon_version: "0.1.0".to_owned(),
            capabilities: BTreeSet::from([DaemonCapability::ActiveTurnSteer]),
            daemon_session_id: DaemonSessionId::from_uuid(Uuid::now_v7()),
            command_watermark: CommandSequence(12),
            live_run_ids: Vec::new(),
        };
        let value = serde_json::to_value(hello).unwrap();

        assert_eq!(value["supported_versions"]["minimum"], 1);
        assert_eq!(value["supported_versions"]["maximum"], 1);
        assert_eq!(value["capabilities"][0], "active_turn_steer");
    }

    #[test]
    fn command_is_a_versioned_tagged_union_with_stable_kind() {
        let envelope = CommandEnvelope {
            command_id: CommandId::from_uuid(Uuid::now_v7()),
            sequence: CommandSequence(4),
            command: Command::RunStop(RunStop {
                run_id: RunId::from_uuid(Uuid::now_v7()),
                reason: StopReason::HumanRequest,
            }),
        };
        let value = serde_json::to_value(envelope).unwrap();

        assert_eq!(value["command"]["kind"], "run.stop");
        assert_eq!(value["command"]["payload"]["reason"], "human_request");
    }

    #[test]
    fn required_command_fields_and_unknown_fields_are_rejected() {
        let missing_reason = json!({
            "command_id": Uuid::now_v7(),
            "sequence": 1,
            "command": {
                "kind": "run.stop",
                "payload": { "run_id": Uuid::now_v7() }
            }
        });
        let unknown_field = json!({
            "command_id": Uuid::now_v7(),
            "sequence": 1,
            "unexpected": true,
            "command": {
                "kind": "run.stop",
                "payload": {
                    "run_id": Uuid::now_v7(),
                    "reason": "human_request"
                }
            }
        });

        assert!(serde_json::from_value::<CommandEnvelope>(missing_reason).is_err());
        assert!(serde_json::from_value::<CommandEnvelope>(unknown_field).is_err());
    }

    #[test]
    fn restricted_notice_cannot_carry_task_or_thread_ids() {
        let notice = AttentionNotice {
            notice_id: NoticeId::from_uuid(Uuid::now_v7()),
            source_kind: InboxSourceKind::Direct,
            strength: AttentionStrength::Hard,
            location: NoticeLocation::Restricted,
            explicit_human_redirect: false,
            arrived_at: OffsetDateTime::now_utc(),
        };
        let value = serde_json::to_value(notice).unwrap();

        assert_eq!(value["location"]["visibility"], "restricted");
        assert!(value["location"].get("task_id").is_none());
        assert!(value["location"].get("thread_id").is_none());
    }

    #[test]
    fn a_query_round_trips_by_kind_and_keeps_the_query_id() {
        let query_id = QueryId::from_uuid(Uuid::now_v7());
        let frame = ServerFrame::Query {
            query: QueryEnvelope {
                query_id,
                query: Query::SessionContinuity(SessionContinuityQuery {
                    agent_id: AgentId::from_uuid(Uuid::now_v7()),
                    scope: SessionScope::Task(TaskId::from_uuid(Uuid::now_v7())),
                }),
            },
        };
        let value = serde_json::to_value(&frame).unwrap();

        assert_eq!(value["type"], "query");
        assert_eq!(value["query"]["query"]["kind"], "session.continuity");
        assert_eq!(value["query"]["query"]["payload"]["scope"]["kind"], "task");
        assert!(serde_json::from_value::<ServerFrame>(value).unwrap() == frame);

        let result = ComputerFrame::QueryResult {
            result: QueryResultEnvelope {
                query_id,
                result: QueryResult::Unavailable {
                    code: QueryErrorCode::Unreachable,
                },
            },
        };
        let value = serde_json::to_value(&result).unwrap();

        assert_eq!(value["result"]["result"]["kind"], "unavailable");
        assert_eq!(value["result"]["result"]["payload"]["code"], "unreachable");
        assert!(serde_json::from_value::<ComputerFrame>(value).unwrap() == result);
    }

    #[test]
    fn a_memory_read_result_hides_the_body_from_debug_output() {
        let result = MemoryReadResult {
            file: MemoryFileProjection {
                path: "MEMORY.md".to_owned(),
                size: 9,
                sha256: "abcd".to_owned(),
                updated_at: OffsetDateTime::UNIX_EPOCH,
            },
            content: "private note".to_owned(),
        };

        let rendered = format!("{result:?}");

        assert!(rendered.contains("[REDACTED]"));
        assert!(!rendered.contains("private note"));
    }

    #[test]
    fn runtime_diagnostics_query_round_trips_without_content_fields() {
        let frame = ServerFrame::Query {
            query: QueryEnvelope {
                query_id: QueryId::from_uuid(Uuid::now_v7()),
                query: Query::RuntimeDiagnostics(RuntimeDiagnosticsQuery {
                    agent_id: AgentId::from_uuid(Uuid::now_v7()),
                }),
            },
        };
        let value = serde_json::to_value(&frame).unwrap();
        assert_eq!(value["query"]["query"]["kind"], "runtime.diagnostics");
        assert!(value.get("content").is_none());
        assert!(serde_json::from_value::<ServerFrame>(value).unwrap() == frame);

        let result = QueryResult::RuntimeDiagnostics(RuntimeDiagnosticsResult {
            local_run_id: Some(RunId::from_uuid(Uuid::now_v7())),
            local_run_state: Some(RuntimeRunState::Running),
            queued_runs: 1,
            active_runs: 2,
            pending_commands: 3,
            pending_result_events: 4,
            warm_sessions: 5,
            cold_sessions: 6,
            reset_required_sessions: 7,
            observed_at: OffsetDateTime::now_utc(),
        });
        let encoded = serde_json::to_value(&result).unwrap();
        assert_eq!(encoded["kind"], "runtime.diagnostics");
        assert_eq!(
            serde_json::from_value::<QueryResult>(encoded).unwrap(),
            result
        );
    }
}
