use anyhow::Context;
use serde::{Deserialize, Serialize};
use utoipa::{OpenApi, ToSchema};
use uuid::Uuid;

#[derive(OpenApi)]
#[openapi(
    info(title = "Sumi Browser API", version = "1"),
    components(schemas(
        RegisterRequest,
        UserResponse,
        RegisterResponse,
        LoginRequest,
        LoginResponse,
        ErrorEnvelope,
        ErrorBody,
        CreateSpaceRequest,
        SpaceResponse,
        ConfirmPairingRequest,
        ComputerResponse,
        PairingDetailsResponse,
        CreateAgentRequest,
        AgentResponse,
        AgentActivityResponse,
        AttentionConfig,
        MemoryFileResponse,
        ReadMemoryRequest,
        MemoryContentResponse,
        UpdateAgentRequest,
        LifecycleAction,
        SuspendMode,
        MemberResponse,
        UpdateMemberRequest,
        CreateInvitationRequest,
        InvitationResponse,
        CreatedInvitationResponse,
        ChannelResponse,
        ChannelMembersResponse,
        ChannelListResponse,
        DirectMessageResponse,
        CreateDirectMessageRequest,
        CreateChannelRequest,
        AddChannelAgentsRequest,
        MessageAuthor,
        MessageContentResponse,
        MessageTaskSummary,
        MessageResponse,
        MessagePageResponse,
        CreateMessageRequest,
        UpdateMessageRequest,
        AttachmentResponse,
        CreateUploadRequest,
        CompleteUploadRequest,
        InboxItemResponse,
        TaskStatus,
        RunStatus,
        SessionContinuityState,
        ThreadReferenceResponse,
        RunResponse,
        SessionContinuityResponse,
        TaskResponse,
        CreateTaskRequest,
        UpdateTaskRequest,
        LinkTaskThreadRequest,
        CompleteTaskRequest,
        CloseTaskRequest,
        AgentRuntimeResponse,
        CreateThreadMessageRequest,
        ThreadReadResponse,
        ThreadSubscriptionResponse
    ))
)]
struct BrowserApi;

pub(in crate::server) fn write_browser_openapi() -> anyhow::Result<()> {
    let schema = serde_json::to_string_pretty(&BrowserApi::openapi())
        .context("failed to serialize Browser API OpenAPI schema")?;
    println!("{schema}");
    Ok(())
}

#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct RegisterRequest {
    display_name: String,
    email: String,
    password: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct UserResponse {
    pub(super) id: Uuid,
    pub(super) display_name: String,
    pub(super) email: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct RegisterResponse {
    pub(super) user: UserResponse,
    pub(super) next: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct LoginRequest {
    email: String,
    password: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct LoginResponse {
    pub(super) user: UserResponse,
}

#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ErrorEnvelope {
    error: ErrorBody,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ErrorBody {
    code: String,
    message: String,
    retryable: bool,
}

#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CreateSpaceRequest {
    name: String,
    slug: String,
    accent: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct SpaceResponse {
    pub(super) id: Uuid,
    pub(super) name: String,
    pub(super) slug: String,
    pub(super) accent: String,
    pub(super) owner_member_id: Uuid,
    pub(super) current_member_id: Uuid,
    pub(super) general_channel_id: Uuid,
}

#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ConfirmPairingRequest {
    code: String,
    name: String,
    space_id: Uuid,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ComputerResponse {
    pub(super) id: Uuid,
    pub(super) space_id: Uuid,
    pub(super) name: String,
    pub(super) hostname: String,
    pub(super) os: ComputerOs,
    pub(super) daemon_version: String,
    pub(super) status: ComputerStatus,
    pub(super) last_seen_at: Option<String>,
    pub(super) created_at: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum ComputerOs {
    Macos,
    Linux,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum ComputerStatus {
    Online,
    Offline,
    Revoked,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct PairingDetailsResponse {
    pairing_id: Uuid,
    hostname: String,
    os: String,
    daemon_version: String,
    token_fingerprint: String,
    status: PairingStatus,
    expires_at: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum PairingStatus {
    Pending,
    Confirmed,
    Expired,
}

#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct AttentionConfig {
    pub(super) dm_immediate: bool,
    pub(super) mention_immediate: bool,
    pub(super) ambient_enabled: bool,
    pub(super) ambient_debounce_seconds: u32,
    pub(super) ambient_max_wait_seconds: u32,
    pub(super) max_retry_count: u32,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CreateAgentRequest {
    computer_id: Uuid,
    name: String,
    handle: Option<String>,
    role_text: String,
    driver_kind: DriverKind,
    access_level: AgentAccessLevel,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum DriverKind {
    Codex,
    Builtin,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum AgentAccessLevel {
    Member,
    Admin,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct AgentActivityResponse {
    pub(super) kind: String,
    pub(super) label: String,
    pub(super) status: AgentActivityStatus,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct AgentResponse {
    pub(super) member_id: Uuid,
    pub(super) space_id: Uuid,
    pub(super) computer_id: Option<Uuid>,
    pub(super) name: String,
    pub(super) handle: String,
    pub(super) access_level: AgentAccessLevel,
    pub(super) role_text: String,
    pub(super) role_revision: u64,
    pub(super) desired_lifecycle: AgentLifecycle,
    pub(super) provision_status: ProvisionStatus,
    pub(super) activity_status: AgentActivityStatus,
    pub(super) driver_kind: DriverKind,
    pub(super) attention_config: AttentionConfig,
    pub(super) activity: Option<AgentActivityResponse>,
    pub(super) last_error_code: Option<String>,
    pub(super) memory_files: Vec<MemoryFileResponse>,
    pub(super) created_at: String,
    pub(super) updated_at: String,
    pub(super) retired_at: Option<String>,
}
#[derive(Clone, Copy, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum AgentLifecycle {
    Active,
    Suspended,
    Retired,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum ProvisionStatus {
    Provisioning,
    Ready,
    Error,
}
#[derive(Clone, Copy, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum AgentActivityStatus {
    Idle,
    Queued,
    Starting,
    Running,
    Finalizing,
    Stopping,
    Unreachable,
    Suspended,
    Error,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct MemoryFileResponse {
    pub(super) path: String,
    pub(super) size: u64,
    pub(super) sha256: String,
    pub(super) updated_at: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ReadMemoryRequest {
    path: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct MemoryContentResponse {
    pub(super) path: String,
    pub(super) size: u64,
    pub(super) sha256: String,
    pub(super) updated_at: String,
    pub(super) content: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct UpdateAgentRequest {
    role_text: Option<String>,
    lifecycle: Option<LifecycleAction>,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(tag = "action", rename_all = "snake_case")]
pub(super) enum LifecycleAction {
    Suspend { mode: SuspendMode },
    Resume,
    Retry,
    Retire,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum SuspendMode {
    StopAfterCurrent,
    CancelNow,
}

#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct MemberResponse {
    pub(super) id: Uuid,
    pub(super) kind: MemberKind,
    pub(super) display_name: String,
    pub(super) handle: String,
    pub(super) access_level: AccessLevel,
    pub(super) permissions: Vec<String>,
}
#[derive(Clone, Copy, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum MemberKind {
    Human,
    Agent,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum AccessLevel {
    Owner,
    Admin,
    Member,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct UpdateMemberRequest {
    access_level: Option<AccessLevel>,
    permissions: Option<Vec<String>>,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CreateInvitationRequest {
    email: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct InvitationResponse {
    pub(super) id: Uuid,
    pub(super) space_id: Uuid,
    pub(super) space_name: String,
    pub(super) space_slug: String,
    pub(super) email: String,
    pub(super) expires_at: String,
    pub(super) accepted_at: Option<String>,
    pub(super) accepted_by_member_id: Option<Uuid>,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CreatedInvitationResponse {
    pub(super) id: Uuid,
    pub(super) space_id: Uuid,
    pub(super) space_name: String,
    pub(super) space_slug: String,
    pub(super) email: String,
    pub(super) expires_at: String,
    pub(super) accepted_at: Option<String>,
    pub(super) accepted_by_member_id: Option<Uuid>,
    pub(super) token: Option<String>,
}

#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ChannelResponse {
    pub(super) id: Uuid,
    pub(super) space_id: Uuid,
    pub(super) name: String,
    pub(super) slug: String,
    pub(super) topic: Option<String>,
    pub(super) kind: ChannelKind,
    pub(super) created_by_member_id: Uuid,
    pub(super) joined: bool,
    pub(super) archived_at: Option<String>,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum ChannelKind {
    Public,
    Private,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ChannelMembersResponse {
    pub(super) members: Vec<MemberResponse>,
    pub(super) can_manage: bool,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ChannelListResponse {
    pub(super) channels: Vec<ChannelResponse>,
    pub(super) can_create: bool,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct DirectMessageResponse {
    pub(super) channel_id: Uuid,
    pub(super) space_id: Uuid,
    pub(super) other_member: MemberResponse,
    pub(super) created_at: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CreateDirectMessageRequest {
    member_id: Uuid,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CreateChannelRequest {
    name: String,
    slug: String,
    kind: ChannelKind,
    topic: Option<String>,
    agent_member_ids: Vec<Uuid>,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct AddChannelAgentsRequest {
    agent_member_ids: Vec<Uuid>,
}

#[derive(Clone, Serialize, Deserialize, ToSchema)]
pub(super) struct MessageAuthor {
    pub(super) id: Uuid,
    pub(super) kind: MemberKind,
    pub(super) display_name: String,
    pub(super) handle: String,
}
#[derive(Clone, Serialize, Deserialize, ToSchema)]
#[serde(tag = "type", rename_all = "snake_case")]
pub(super) enum MessageContentResponse {
    Text { body_markdown: String },
    ChannelCreated { channel: ActionChannelResponse },
    AgentCreated { agent: ActionAgentResponse },
}
#[derive(Clone, Serialize, Deserialize, ToSchema)]
pub(super) struct ActionChannelResponse {
    pub(super) id: Uuid,
    pub(super) slug: String,
    pub(super) name: String,
    pub(super) available: bool,
}
#[derive(Clone, Serialize, Deserialize, ToSchema)]
pub(super) struct ActionAgentResponse {
    pub(super) member_id: Uuid,
    pub(super) name: String,
    pub(super) lifecycle: AgentLifecycle,
    pub(super) available: bool,
}
#[derive(Clone, Serialize, Deserialize, ToSchema)]
pub(super) struct MessageTaskSummary {
    pub(super) id: Uuid,
    pub(super) title: String,
    pub(super) status: TaskStatus,
    pub(super) assignee_agent_member_id: Option<Uuid>,
    pub(super) assignee_name: Option<String>,
    pub(super) working_elsewhere: bool,
}
#[derive(Clone, Serialize, Deserialize, ToSchema)]
pub(super) struct AttentionFailureResponse {
    pub(super) agent_member_id: Uuid,
    pub(super) agent_handle: String,
    pub(super) error_code: String,
    pub(super) retrying: bool,
}
#[derive(Clone, Serialize, Deserialize, ToSchema)]
pub(super) struct MessageResponse {
    pub(super) id: Uuid,
    pub(super) channel_id: Uuid,
    pub(super) thread_id: Uuid,
    pub(super) placement: MessagePlacement,
    pub(super) seq: u64,
    pub(super) author: MessageAuthor,
    pub(super) content: MessageContentResponse,
    pub(super) mentions: Vec<Uuid>,
    pub(super) mention_all: bool,
    pub(super) attachments: Vec<AttachmentResponse>,
    pub(super) reply_count: u64,
    pub(super) task: Option<MessageTaskSummary>,
    pub(super) attention_failures: Vec<AttentionFailureResponse>,
    pub(super) created_at: String,
    pub(super) edited_at: Option<String>,
    pub(super) deleted_at: Option<String>,
    pub(super) context_citations: Vec<ContextCitationResponse>,
}
#[derive(Clone, Serialize, Deserialize, ToSchema)]
pub(super) struct ContextCitationResponse {
    pub(super) answer_start: u64,
    pub(super) answer_end: u64,
    pub(super) source_message_id: Uuid,
    pub(super) source_start: u64,
    pub(super) source_end: u64,
    pub(super) source_channel_id: Uuid,
    pub(super) source_thread_id: Uuid,
    pub(super) source_author: MessageAuthor,
    pub(super) source_excerpt: String,
}
#[derive(Clone, Copy, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum MessagePlacement {
    Root,
    Reply,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct MessagePageResponse {
    pub(super) channel_id: Uuid,
    pub(super) snapshot_channel_seq: u64,
    pub(super) messages: Vec<MessageResponse>,
    pub(super) has_more_before: bool,
    pub(super) has_more_after: bool,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CreateMessageRequest {
    body_markdown: String,
    mentions: Vec<Uuid>,
    mention_all: bool,
    attachment_ids: Vec<Uuid>,
}

#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct UpdateMessageRequest {
    body_markdown: String,
    mentions: Vec<Uuid>,
    mention_all: bool,
}

#[derive(Clone, Serialize, Deserialize, ToSchema)]
pub(super) struct AttachmentResponse {
    pub(super) id: Uuid,
    pub(super) space_id: Uuid,
    pub(super) uploader_member_id: Uuid,
    pub(super) original_name: String,
    pub(super) media_type: String,
    pub(super) size: Option<u64>,
    pub(super) sha256: Option<String>,
    pub(super) status: AttachmentStatus,
    pub(super) upload_path: Option<String>,
    pub(super) download_path: Option<String>,
    pub(super) created_at: String,
}
#[derive(Clone, Copy, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum AttachmentStatus {
    Uploading,
    Ready,
    Deleted,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CreateUploadRequest {
    space_id: Uuid,
    original_name: String,
    media_type: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CompleteUploadRequest {
    size: u64,
    sha256: String,
}

#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct InboxItemResponse {
    pub(super) id: Uuid,
    pub(super) member_id: Uuid,
    pub(super) kind: InboxKind,
    pub(super) priority: InboxPriority,
    pub(super) channel_id: Option<Uuid>,
    pub(super) channel_slug: Option<String>,
    pub(super) thread_id: Option<Uuid>,
    pub(super) message_id: Option<Uuid>,
    pub(super) sender_member_id: Option<Uuid>,
    pub(super) sender_display_name: Option<String>,
    pub(super) summary: String,
    pub(super) status: InboxStatus,
    pub(super) available_at: String,
    pub(super) created_at: String,
    /// Failed delivery attempts so far. Reaching `max_retry_count` retires the Item as `dead`.
    pub(super) retry_count: u32,
    /// Times a governor returned this Item from `dead` to the queue.
    pub(super) requeue_count: u32,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum InboxKind {
    Direct,
    Mention,
    Reply,
    TaskActivity,
    ThreadActivity,
    ChannelActivity,
    System,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum InboxPriority {
    Hard,
    Ambient,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum InboxStatus {
    Pending,
    Leased,
    Deferred,
    Handled,
    Dead,
}

#[derive(Clone, Copy, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum TaskStatus {
    Todo,
    InProgress,
    InReview,
    Done,
    Closed,
}
#[derive(Clone, Copy, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum RunStatus {
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
#[derive(Clone, Copy, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum SessionContinuityState {
    Warm,
    Cold,
    ResetRequired,
    Unavailable,
}
#[derive(Clone, Serialize, Deserialize, ToSchema)]
pub(super) struct ThreadReferenceResponse {
    pub(super) id: Uuid,
    pub(super) channel_id: Uuid,
    pub(super) channel_slug: String,
    pub(super) root_message_id: Uuid,
    pub(super) root_message_seq: u64,
    pub(super) relation: ThreadRelation,
}
#[derive(Clone, Copy, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum ThreadRelation {
    Source,
    Related,
}
#[derive(Clone, Serialize, Deserialize, ToSchema)]
pub(super) struct RunResponse {
    pub(super) id: Uuid,
    pub(super) task_id: Option<Uuid>,
    pub(super) agent_member_id: Uuid,
    pub(super) agent_name: String,
    pub(super) focus: ThreadReferenceResponse,
    pub(super) status: RunStatus,
    pub(super) outcome: Option<RunOutcome>,
    pub(super) continuation_note: Option<String>,
    pub(super) error_code: Option<String>,
    pub(super) started_at: Option<String>,
    pub(super) finished_at: Option<String>,
}
#[derive(Clone, Copy, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum RunOutcome {
    Completed,
    Yielded,
    Failed,
    Canceled,
}
#[derive(Clone, Serialize, Deserialize, ToSchema)]
pub(super) struct SessionContinuityResponse {
    pub(super) state: SessionContinuityState,
    pub(super) generation: Option<u64>,
    pub(super) reason_code: Option<String>,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct TaskResponse {
    pub(super) id: Uuid,
    pub(super) space_id: Uuid,
    pub(super) title: String,
    pub(super) status: TaskStatus,
    pub(super) creator_member_id: Uuid,
    pub(super) creator_name: String,
    pub(super) assignee_agent_member_id: Option<Uuid>,
    pub(super) assignee_name: Option<String>,
    pub(super) source_thread: ThreadReferenceResponse,
    pub(super) related_threads: Vec<ThreadReferenceResponse>,
    pub(super) result_message: Option<MessageResponse>,
    pub(super) close_reason_code: Option<CloseReason>,
    pub(super) close_reason_note: Option<String>,
    pub(super) current_run: Option<RunResponse>,
    pub(super) recent_runs: Vec<RunResponse>,
    pub(super) session_continuity: SessionContinuityResponse,
    pub(super) runtime_issue_code: Option<String>,
    pub(super) created_at: String,
    pub(super) updated_at: String,
    pub(super) finished_at: Option<String>,
}
#[derive(Clone, Copy, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum CloseReason {
    Invalid,
    Duplicate,
    NotNeeded,
    Obsolete,
    Other,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CreateTaskRequest {
    title: Option<String>,
    assignee_agent_member_id: Option<Uuid>,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct UpdateTaskRequest {
    title: Option<String>,
    assignee_agent_member_id: Option<Uuid>,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct LinkTaskThreadRequest {
    thread_id: Uuid,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CompleteTaskRequest {
    result_thread_id: Uuid,
    result_markdown: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CloseTaskRequest {
    reason: CloseReason,
    note: Option<String>,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct AgentRuntimeResponse {
    pub(super) current_run: Option<RunResponse>,
    pub(super) current_task: Option<TaskResponse>,
    pub(super) focus: Option<ThreadReferenceResponse>,
    pub(super) another_item_waiting: bool,
    pub(super) session_continuity: SessionContinuityResponse,
}

#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CreateThreadMessageRequest {
    body_markdown: String,
    mentions: Vec<Uuid>,
    mention_all: bool,
    attachment_ids: Vec<Uuid>,
    reply_to_message_id: Option<Uuid>,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ThreadReadResponse {
    pub(super) thread_id: Uuid,
    pub(super) channel_id: Uuid,
    pub(super) snapshot_channel_seq: u64,
    pub(super) root: MessageResponse,
    pub(super) replies: Vec<MessageResponse>,
    pub(super) is_following: bool,
    pub(super) task: Option<MessageTaskSummary>,
    pub(super) task_relation: Option<ThreadRelation>,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ThreadSubscriptionResponse {
    pub(super) thread_id: Uuid,
    pub(super) channel_id: Uuid,
    pub(super) is_following: bool,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn browser_schema_uses_current_task_run_and_message_models() {
        let document = serde_json::to_value(BrowserApi::openapi()).expect("OpenAPI document");
        assert_eq!(
            document["components"]["schemas"]["TaskStatus"]["enum"],
            serde_json::json!(["todo", "in_progress", "in_review", "done", "closed"])
        );
        assert_eq!(
            document["components"]["schemas"]["SessionContinuityState"]["enum"],
            serde_json::json!(["warm", "cold", "reset_required", "unavailable"])
        );
        let message = &document["components"]["schemas"]["MessageContentResponse"];
        assert!(
            message["oneOf"].is_array(),
            "Message content must be tagged"
        );
        assert!(
            document["components"]["schemas"]["ThreadReadResponse"]["properties"]["task_relation"]
                .is_object(),
            "Thread projection must distinguish Source and Related"
        );
        let serialized = serde_json::to_string(&document).expect("schema JSON");
        for forbidden in [
            "provider_session_locator",
            "provider_transcript",
            "hidden_reasoning",
            "assigned_agent_member_id",
        ] {
            assert!(!serialized.contains(forbidden), "schema leaks {forbidden}");
        }
    }
}
