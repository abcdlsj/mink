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
    id: Uuid,
    name: String,
    slug: String,
    accent: String,
    owner_member_id: Uuid,
    current_member_id: Uuid,
    general_channel_id: Uuid,
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
    dm_immediate: bool,
    mention_immediate: bool,
    ambient_enabled: bool,
    ambient_debounce_seconds: u32,
    ambient_max_wait_seconds: u32,
    max_retry_count: u32,
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
    kind: String,
    label: String,
    updated_at: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct AgentResponse {
    member_id: Uuid,
    space_id: Uuid,
    computer_id: Option<Uuid>,
    name: String,
    handle: String,
    access_level: AgentAccessLevel,
    role_text: String,
    role_revision: u64,
    desired_lifecycle: AgentLifecycle,
    provision_status: ProvisionStatus,
    activity_status: AgentActivityStatus,
    driver_kind: DriverKind,
    attention_config: AttentionConfig,
    activity: Option<AgentActivityResponse>,
    last_error_code: Option<String>,
    memory_files: Vec<MemoryFileResponse>,
    created_at: String,
    updated_at: String,
    retired_at: Option<String>,
}
#[derive(Serialize, Deserialize, ToSchema)]
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
#[derive(Serialize, Deserialize, ToSchema)]
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
    path: String,
    size: u64,
    sha256: String,
    updated_at: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ReadMemoryRequest {
    path: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct MemoryContentResponse {
    path: String,
    size: u64,
    sha256: String,
    updated_at: String,
    content: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
/// Attention 策略是 Server 的固定策略,没有存储也没有写入路径,因此不可修改。
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
#[derive(Serialize, Deserialize, ToSchema)]
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
/// 创建响应。`token`只在首次创建时非空，重放不返回明文。
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
    id: Uuid,
    space_id: Uuid,
    name: String,
    slug: String,
    topic: Option<String>,
    kind: ChannelKind,
    created_by_member_id: Uuid,
    joined: bool,
    archived_at: Option<String>,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum ChannelKind {
    Public,
    Private,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ChannelMembersResponse {
    members: Vec<MemberResponse>,
    can_manage: bool,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ChannelListResponse {
    channels: Vec<ChannelResponse>,
    can_create: bool,
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

#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct MessageAuthor {
    id: Uuid,
    kind: MemberKind,
    display_name: String,
    handle: String,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(tag = "type", rename_all = "snake_case")]
pub(super) enum MessageContentResponse {
    Text { body_markdown: String },
    ChannelCreated { channel: ActionChannelResponse },
    AgentCreated { agent: ActionAgentResponse },
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ActionChannelResponse {
    id: Uuid,
    slug: String,
    name: String,
    available: bool,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ActionAgentResponse {
    member_id: Uuid,
    name: String,
    lifecycle: AgentLifecycle,
    available: bool,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct MessageTaskSummary {
    id: Uuid,
    title: String,
    status: TaskStatus,
    assignee_agent_member_id: Option<Uuid>,
    assignee_name: Option<String>,
    working_elsewhere: bool,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct AttentionFailureResponse {
    agent_member_id: Uuid,
    agent_handle: String,
    error_code: String,
    retrying: bool,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct MessageResponse {
    id: Uuid,
    channel_id: Uuid,
    thread_id: Uuid,
    placement: MessagePlacement,
    seq: u64,
    author: MessageAuthor,
    content: MessageContentResponse,
    mentions: Vec<Uuid>,
    attachments: Vec<AttachmentResponse>,
    reply_count: u64,
    task: Option<MessageTaskSummary>,
    attention_failures: Vec<AttentionFailureResponse>,
    created_at: String,
    edited_at: Option<String>,
    deleted_at: Option<String>,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum MessagePlacement {
    Root,
    Reply,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct MessagePageResponse {
    channel_id: Uuid,
    snapshot_channel_seq: u64,
    messages: Vec<MessageResponse>,
    has_more_before: bool,
    has_more_after: bool,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CreateMessageRequest {
    body_markdown: String,
    mentions: Vec<Uuid>,
    attachment_ids: Vec<Uuid>,
}

#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct UpdateMessageRequest {
    body_markdown: String,
}

#[derive(Serialize, Deserialize, ToSchema)]
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
#[derive(Serialize, Deserialize, ToSchema)]
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

#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum TaskStatus {
    Todo,
    InProgress,
    InReview,
    Done,
    Closed,
}
#[derive(Serialize, Deserialize, ToSchema)]
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
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum SessionContinuityState {
    Warm,
    Cold,
    ResetRequired,
    Unavailable,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ThreadReferenceResponse {
    id: Uuid,
    channel_id: Uuid,
    channel_slug: String,
    root_message_id: Uuid,
    root_message_seq: u64,
    relation: ThreadRelation,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum ThreadRelation {
    Source,
    Related,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct RunResponse {
    id: Uuid,
    task_id: Option<Uuid>,
    agent_member_id: Uuid,
    agent_name: String,
    focus: ThreadReferenceResponse,
    status: RunStatus,
    outcome: Option<RunOutcome>,
    continuation_note: Option<String>,
    error_code: Option<String>,
    started_at: Option<String>,
    finished_at: Option<String>,
}
#[derive(Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "snake_case")]
pub(super) enum RunOutcome {
    Completed,
    Yielded,
    Failed,
    Canceled,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct SessionContinuityResponse {
    state: SessionContinuityState,
    generation: Option<u64>,
    reason_code: Option<String>,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct TaskResponse {
    id: Uuid,
    space_id: Uuid,
    title: String,
    status: TaskStatus,
    creator_member_id: Uuid,
    creator_name: String,
    assignee_agent_member_id: Option<Uuid>,
    assignee_name: Option<String>,
    source_thread: ThreadReferenceResponse,
    related_threads: Vec<ThreadReferenceResponse>,
    result_message: Option<MessageResponse>,
    close_reason_code: Option<CloseReason>,
    close_reason_note: Option<String>,
    current_run: Option<RunResponse>,
    recent_runs: Vec<RunResponse>,
    session_continuity: SessionContinuityResponse,
    runtime_issue_code: Option<String>,
    created_at: String,
    updated_at: String,
    finished_at: Option<String>,
}
#[derive(Serialize, Deserialize, ToSchema)]
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
    current_run: Option<RunResponse>,
    current_task: Option<TaskResponse>,
    focus: Option<ThreadReferenceResponse>,
    another_item_waiting: bool,
    session_continuity: SessionContinuityResponse,
}

#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct CreateThreadMessageRequest {
    body_markdown: String,
    mentions: Vec<Uuid>,
    attachment_ids: Vec<Uuid>,
    reply_to_message_id: Option<Uuid>,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ThreadReadResponse {
    thread_id: Uuid,
    channel_id: Uuid,
    snapshot_channel_seq: u64,
    root: MessageResponse,
    replies: Vec<MessageResponse>,
    is_following: bool,
    task: Option<MessageTaskSummary>,
    task_relation: Option<ThreadRelation>,
}
#[derive(Serialize, Deserialize, ToSchema)]
pub(super) struct ThreadSubscriptionResponse {
    thread_id: Uuid,
    channel_id: Uuid,
    is_following: bool,
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
            "source_message_id",
            "assigned_agent_member_id",
        ] {
            assert!(!serialized.contains(forbidden), "schema leaks {forbidden}");
        }
    }
}
