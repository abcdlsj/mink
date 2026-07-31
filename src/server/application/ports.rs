use crate::ids::{
    AttachmentId, ChannelId, ComputerId, EventId, IdempotencyKey, InboxItemId, MemberId, MessageId,
    RunId, TaskId, ThreadId,
};

use crate::server::domain::{
    attention::InboxItem,
    conversation::{Channel, Message, Thread},
    execution::Run,
    identity::{AccessLevel, Agent, Computer, Member},
    task::Task,
};

use async_trait::async_trait;
use sha2::{Digest, Sha256};
use std::fmt;

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
pub(in crate::server) enum ApplicationError {
    #[error(transparent)]
    Domain(#[from] crate::server::domain::DomainError),
    #[error("resource was not found")]
    NotFound,
    #[error("credential is missing, expired, or does not match")]
    Unauthenticated,
    #[error("request payload exceeds the configured limit")]
    PayloadTooLarge,
    #[error("actor is not allowed to perform this action")]
    PermissionDenied,
    #[error("request conflicts with current state")]
    Conflict,
    #[error("run context changed")]
    ContextChanged,
    #[error("external dependency is unavailable")]
    Unavailable,
    #[error("server adapter failed")]
    Internal,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct StoredObject {
    pub(in crate::server) length: u64,
    pub(in crate::server) sha256: [u8; 32],
}

#[async_trait]
pub(in crate::server) trait AttachmentObjectPort: Send + Sync {
    async fn put(
        &self,
        object_key: &str,
        content: Vec<u8>,
    ) -> Result<StoredObject, ApplicationError>;
    async fn get(&self, object_key: &str) -> Result<Vec<u8>, ApplicationError>;
}

pub(in crate::server) trait PasswordPort {
    fn hash(&self, password: &str) -> Result<String, ApplicationError>;

    fn verify(&self, password: &str, stored_hash: &str) -> bool;
}

pub(in crate::server) trait SessionTokenPort {
    fn generate(&self) -> RawSessionToken;
}

#[derive(Clone, Eq, PartialEq)]
pub(in crate::server) struct RawSessionToken(String);

impl RawSessionToken {
    pub(in crate::server) fn new(value: String) -> Self {
        Self(value)
    }

    pub(in crate::server) fn expose(&self) -> &str {
        &self.0
    }

    pub(in crate::server) fn sha256_hash(&self) -> String {
        hex::encode(Sha256::digest(self.0.as_bytes()))
    }
}

impl fmt::Debug for RawSessionToken {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("RawSessionToken([REDACTED])")
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct AuthenticatedHuman {
    pub(in crate::server) user_id: uuid::Uuid,
    pub(in crate::server) display_name: String,
    pub(in crate::server) email_normalized: String,
}

pub(in crate::server) struct OpenedSession {
    pub(in crate::server) human: AuthenticatedHuman,
    pub(in crate::server) token: RawSessionToken,
}

pub(in crate::server) trait PairingCodePort {
    fn generate(&self) -> RawPairingCode;
}

#[derive(Clone, Eq, PartialEq)]
pub(in crate::server) struct RawPairingCode(String);

impl RawPairingCode {
    pub(in crate::server) fn new(value: String) -> Self {
        Self(value)
    }

    pub(in crate::server) fn expose(&self) -> &str {
        &self.0
    }

    pub(in crate::server) fn sha256_hash(&self) -> String {
        hex::encode(Sha256::digest(self.0.as_bytes()))
    }
}

impl fmt::Debug for RawPairingCode {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("RawPairingCode([REDACTED])")
    }
}

pub(in crate::server) struct ComputerRecord {
    pub(in crate::server) id: ComputerId,
    pub(in crate::server) space_id: crate::ids::SpaceId,
    pub(in crate::server) name: String,
    pub(in crate::server) hostname: String,
    pub(in crate::server) os: crate::server::domain::pairing::ComputerOs,
    pub(in crate::server) daemon_version: String,
    pub(in crate::server) token_hash: String,
    pub(in crate::server) created_at: time::OffsetDateTime,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct PairedComputer {
    pub(in crate::server) id: ComputerId,
    pub(in crate::server) space_id: crate::ids::SpaceId,
    pub(in crate::server) name: String,
    pub(in crate::server) hostname: String,
    pub(in crate::server) os: crate::server::domain::pairing::ComputerOs,
    pub(in crate::server) daemon_version: Option<String>,
    pub(in crate::server) connected: bool,
    pub(in crate::server) deleted: bool,
    pub(in crate::server) last_seen_at: Option<time::OffsetDateTime>,
    pub(in crate::server) created_at: time::OffsetDateTime,
}

pub(in crate::server) trait InvitationTokenPort {
    fn generate(&self) -> RawInvitationToken;
}

#[derive(Clone, Eq, PartialEq)]
pub(in crate::server) struct RawInvitationToken(String);

impl RawInvitationToken {
    pub(in crate::server) fn new(value: String) -> Self {
        Self(value)
    }

    pub(in crate::server) fn expose(&self) -> &str {
        &self.0
    }

    pub(in crate::server) fn sha256_hash(&self) -> String {
        hex::encode(Sha256::digest(self.0.as_bytes()))
    }
}

impl fmt::Debug for RawInvitationToken {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("RawInvitationToken([REDACTED])")
    }
}

pub(in crate::server) struct InvitationView {
    pub(in crate::server) id: uuid::Uuid,
    pub(in crate::server) space_id: crate::ids::SpaceId,
    pub(in crate::server) space_name: String,
    pub(in crate::server) space_slug: String,
    pub(in crate::server) email: String,
    pub(in crate::server) expires_at: time::OffsetDateTime,
    pub(in crate::server) accepted_at: Option<time::OffsetDateTime>,
    pub(in crate::server) accepted_by_member_id: Option<MemberId>,
}

pub(in crate::server) struct HumanMemberRecord {
    pub(in crate::server) member_id: MemberId,
    pub(in crate::server) space_id: crate::ids::SpaceId,
    pub(in crate::server) user_id: uuid::Uuid,
    pub(in crate::server) display_name: String,
    pub(in crate::server) handle: String,
    pub(in crate::server) created_at: time::OffsetDateTime,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct DirectMessageView {
    pub(in crate::server) channel_id: ChannelId,
    pub(in crate::server) space_id: crate::ids::SpaceId,
    pub(in crate::server) other_member: SpaceMemberView,
    pub(in crate::server) created_at: time::OffsetDateTime,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct SpaceMemberView {
    pub(in crate::server) id: MemberId,
    pub(in crate::server) space_id: crate::ids::SpaceId,
    pub(in crate::server) kind: MemberKind,
    pub(in crate::server) display_name: String,
    pub(in crate::server) handle: String,
    pub(in crate::server) access_level: crate::server::domain::identity::AccessLevel,
    pub(in crate::server) permissions: Vec<crate::server::domain::identity::PermissionAction>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum MemberKind {
    Human,
    Agent,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct InboxItemView {
    pub(in crate::server) id: InboxItemId,
    pub(in crate::server) member_id: MemberId,
    pub(in crate::server) kind: crate::server::domain::attention::InboxItemKind,
    pub(in crate::server) strength: crate::server::domain::attention::AttentionStrength,
    pub(in crate::server) status: crate::server::domain::attention::InboxItemStatus,
    pub(in crate::server) channel_id: Option<ChannelId>,
    pub(in crate::server) channel_slug: Option<String>,
    pub(in crate::server) thread_id: Option<crate::ids::ThreadId>,
    pub(in crate::server) message_id: Option<MessageId>,
    pub(in crate::server) sender_member_id: Option<MemberId>,
    pub(in crate::server) sender_display_name: Option<String>,
    pub(in crate::server) available_at: time::OffsetDateTime,
    pub(in crate::server) created_at: time::OffsetDateTime,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct SpaceHumanMember {
    pub(in crate::server) member_id: MemberId,
    pub(in crate::server) space_id: crate::ids::SpaceId,
    pub(in crate::server) display_name: String,
    pub(in crate::server) handle: String,
}

pub(in crate::server) struct PairingView {
    pub(in crate::server) pairing_id: uuid::Uuid,
    pub(in crate::server) hostname: String,
    pub(in crate::server) os: crate::server::domain::pairing::ComputerOs,
    pub(in crate::server) daemon_version: String,
    pub(in crate::server) token_fingerprint: String,
    pub(in crate::server) status: crate::server::domain::pairing::PairingStatus,
    pub(in crate::server) expires_at: time::OffsetDateTime,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) enum Effect {
    MessageCreated(MessageId),
    MessageUpdated(MessageId),
    MessageDeleted(MessageId),
    TaskCreated(TaskId),
    RunTaskBound {
        run_id: RunId,
        task_id: TaskId,
    },
    ThreadLinked {
        task_id: TaskId,
        thread_id: ThreadId,
    },
    ThreadUnlinked {
        task_id: TaskId,
        thread_id: ThreadId,
    },
    ItemAttached {
        run_id: RunId,
        item_id: InboxItemId,
        sequence: u64,
    },
    RunNotice {
        run_id: RunId,
        item_id: InboxItemId,
        location_visible: bool,
    },
    RunClaimed {
        run_id: RunId,
        fencing_token: RawFencingToken,
    },
    RunStarted(RunId),
    RunCompleted(RunId),
    TaskCompleted {
        task_id: TaskId,
        result_message_id: MessageId,
    },
    TaskFinished(TaskId),
    SessionClose(TaskId),
    SessionReset(TaskId),
    AgentRetired {
        agent_id: MemberId,
        computer_id: ComputerId,
    },
    ComputerDeleted(ComputerId),
    TaskUpdated(TaskId),
    ChannelCreated(ChannelId),
    ChannelUpdated(ChannelId),
    AgentUpdated(MemberId),
    AgentCreated {
        agent_id: MemberId,
        computer_id: ComputerId,
    },
    PermissionChanged(MemberId),
    /// A Member's Inbox queue changed. Emitted for the owning Member, not for a resource, because
    /// the Inbox projection is read per Member.
    InboxChanged(MemberId),
    /// A Thread gained a reply or changed its reply count, so open Thread panes must refresh.
    ThreadUpdated(ThreadId),
}

pub(in crate::server) struct MessageDraft {
    pub(in crate::server) message_id: MessageId,
    pub(in crate::server) channel_id: ChannelId,
    pub(in crate::server) author_member_id: MemberId,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) thread_id: Option<ThreadId>,
    pub(in crate::server) reply_to_message_id: Option<MessageId>,
    pub(in crate::server) body_markdown: String,
    pub(in crate::server) mentions: Vec<MemberId>,
    pub(in crate::server) attachment_ids: Vec<AttachmentId>,
    pub(in crate::server) handled_item: Option<(RunId, InboxItemId)>,
    pub(in crate::server) expected_snapshot: Option<u64>,
    pub(in crate::server) now: time::OffsetDateTime,
}

pub(in crate::server) struct PublishedMessage {
    pub(in crate::server) message_id: MessageId,
    pub(in crate::server) hard_item_ids: Vec<InboxItemId>,
    /// Agents that received an Item from this Message, hard or ambient. Drives `inbox.changed`, so
    /// it covers ambient recipients that `hard_item_ids` omits.
    pub(in crate::server) notified_agent_ids: Vec<MemberId>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct CreatedSpace {
    pub(in crate::server) space_id: crate::ids::SpaceId,
    pub(in crate::server) owner_id: MemberId,
    pub(in crate::server) general_channel_id: ChannelId,
}

#[derive(Clone, Eq, PartialEq)]
pub(in crate::server) struct RawFencingToken(String);

impl RawFencingToken {
    pub(in crate::server) fn new(value: String) -> Self {
        Self(value)
    }

    pub(in crate::server) fn expose(&self) -> &str {
        &self.0
    }

    pub(in crate::server) fn sha256_hash(&self) -> String {
        hex::encode(Sha256::digest(self.0.as_bytes()))
    }
}

impl fmt::Debug for RawFencingToken {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("RawFencingToken([REDACTED])")
    }
}

#[async_trait]
pub(in crate::server) trait ServerTransaction {
    #[allow(clippy::too_many_arguments)]
    async fn create_space(
        &mut self,
        actor_user_id: uuid::Uuid,
        space_id: crate::ids::SpaceId,
        owner_id: MemberId,
        general_channel_id: ChannelId,
        name: &str,
        slug: &str,
        owner_handle: &str,
        owner_display_name: &str,
        idempotency_key: IdempotencyKey,
        now: time::OffsetDateTime,
    ) -> Result<CreatedSpace, ApplicationError>;
    async fn insert_human(
        &mut self,
        user_id: uuid::Uuid,
        registration: &crate::server::domain::access::HumanRegistration,
        password_hash: &str,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn human_credential(
        &mut self,
        email_normalized: &str,
    ) -> Result<Option<(AuthenticatedHuman, String)>, ApplicationError>;
    async fn insert_browser_session(
        &mut self,
        session_id: uuid::Uuid,
        user_id: uuid::Uuid,
        token_hash: &str,
        expires_at: time::OffsetDateTime,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn human_for_session(
        &mut self,
        token_hash: &str,
        now: time::OffsetDateTime,
    ) -> Result<Option<AuthenticatedHuman>, ApplicationError>;
    async fn delete_browser_session(&mut self, token_hash: &str) -> Result<(), ApplicationError>;
    async fn space_access(
        &mut self,
        user_id: uuid::Uuid,
        space_id: crate::ids::SpaceId,
    ) -> Result<Option<crate::server::domain::access::SpaceAccess>, ApplicationError>;
    async fn channel_access(
        &mut self,
        user_id: uuid::Uuid,
        channel_id: ChannelId,
    ) -> Result<Option<MemberId>, ApplicationError>;
    async fn space_of_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<Option<crate::ids::SpaceId>, ApplicationError>;
    async fn space_of_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Option<crate::ids::SpaceId>, ApplicationError>;
    async fn space_of_attachment(
        &mut self,
        attachment_id: AttachmentId,
    ) -> Result<Option<crate::ids::SpaceId>, ApplicationError>;

    async fn insert_pairing(
        &mut self,
        pairing_id: uuid::Uuid,
        pairing: &crate::server::domain::pairing::Pairing,
        code_hash: &str,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn save_pairing(
        &mut self,
        pairing_id: uuid::Uuid,
        pairing: &crate::server::domain::pairing::Pairing,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn pairing_by_code(
        &mut self,
        pairing_id: uuid::Uuid,
        code_hash: &str,
    ) -> Result<Option<crate::server::domain::pairing::Pairing>, ApplicationError>;
    async fn pairing_by_code_for_update(
        &mut self,
        pairing_id: uuid::Uuid,
        code_hash: &str,
    ) -> Result<Option<crate::server::domain::pairing::Pairing>, ApplicationError>;
    async fn pairing_by_token(
        &mut self,
        pairing_id: uuid::Uuid,
        token_hash: &str,
    ) -> Result<Option<crate::server::domain::pairing::Pairing>, ApplicationError>;

    async fn insert_invitation(
        &mut self,
        invitation_id: uuid::Uuid,
        invitation: &crate::server::domain::invitation::Invitation,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn save_invitation(
        &mut self,
        invitation_id: uuid::Uuid,
        invitation: &crate::server::domain::invitation::Invitation,
    ) -> Result<(), ApplicationError>;
    async fn invitation_by_token(
        &mut self,
        token_hash: &str,
    ) -> Result<Option<(uuid::Uuid, crate::server::domain::invitation::Invitation)>, ApplicationError>;
    async fn invitation_by_token_for_update(
        &mut self,
        token_hash: &str,
    ) -> Result<Option<(uuid::Uuid, crate::server::domain::invitation::Invitation)>, ApplicationError>;
    async fn space_identity(
        &mut self,
        space_id: crate::ids::SpaceId,
    ) -> Result<Option<(String, String)>, ApplicationError>;
    async fn insert_human_member(
        &mut self,
        record: &HumanMemberRecord,
    ) -> Result<(), ApplicationError>;
    async fn space_human_member(
        &mut self,
        user_id: uuid::Uuid,
        space_id: crate::ids::SpaceId,
    ) -> Result<Option<SpaceHumanMember>, ApplicationError>;

    async fn direct_messages_for_member(
        &mut self,
        member_id: MemberId,
        space_id: crate::ids::SpaceId,
    ) -> Result<Vec<DirectMessageView>, ApplicationError>;
    async fn direct_message_between(
        &mut self,
        space_id: crate::ids::SpaceId,
        first: MemberId,
        second: MemberId,
    ) -> Result<Option<DirectMessageView>, ApplicationError>;
    async fn space_member(
        &mut self,
        member_id: MemberId,
        space_id: crate::ids::SpaceId,
    ) -> Result<Option<SpaceMemberView>, ApplicationError>;
    async fn inbox_for_member(
        &mut self,
        member_id: MemberId,
    ) -> Result<Vec<InboxItemView>, ApplicationError>;
    async fn space_of_member(
        &mut self,
        member_id: MemberId,
    ) -> Result<Option<crate::ids::SpaceId>, ApplicationError>;
    async fn member(
        &mut self,
        member_id: MemberId,
    ) -> Result<crate::server::domain::identity::Member, ApplicationError>;
    async fn save_member(
        &mut self,
        member: crate::server::domain::identity::Member,
    ) -> Result<(), ApplicationError>;
    async fn save_channel(
        &mut self,
        channel: crate::server::domain::conversation::Channel,
    ) -> Result<(), ApplicationError>;
    async fn queue_agent_suspend(
        &mut self,
        agent_id: MemberId,
        computer_id: Option<ComputerId>,
        cancel_current_run: bool,
    ) -> Result<(), ApplicationError>;
    async fn queue_agent_configuration(
        &mut self,
        agent: &crate::server::domain::identity::Agent,
    ) -> Result<(), ApplicationError>;
    async fn set_thread_subscription(
        &mut self,
        thread_id: crate::ids::ThreadId,
        member_id: MemberId,
        following: bool,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;

    async fn insert_computer(&mut self, record: &ComputerRecord) -> Result<(), ApplicationError>;
    async fn paired_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Option<PairedComputer>, ApplicationError>;
    async fn space_computers(
        &mut self,
        space_id: crate::ids::SpaceId,
    ) -> Result<Vec<PairedComputer>, ApplicationError>;
    async fn computer_for_token(
        &mut self,
        computer_id: ComputerId,
        token_hash: &str,
    ) -> Result<Option<bool>, ApplicationError>;
    async fn lock_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<(), ApplicationError>;

    async fn attachment(
        &mut self,
        id: AttachmentId,
    ) -> Result<Option<crate::server::domain::attachment::Attachment>, ApplicationError>;
    async fn insert_attachment(
        &mut self,
        attachment: &crate::server::domain::attachment::Attachment,
    ) -> Result<(), ApplicationError>;
    async fn save_attachment(
        &mut self,
        attachment: &crate::server::domain::attachment::Attachment,
    ) -> Result<(), ApplicationError>;
    async fn attachment_is_visible(
        &mut self,
        id: AttachmentId,
        viewer: MemberId,
    ) -> Result<bool, ApplicationError>;
    #[allow(clippy::too_many_arguments)]
    async fn record_attachment_write(
        &mut self,
        space_id: crate::ids::SpaceId,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        attachment_id: AttachmentId,
        event_kind: &str,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;

    async fn thread(&mut self, id: ThreadId) -> Result<Thread, ApplicationError>;
    async fn root_message(&mut self, thread_id: ThreadId) -> Result<Message, ApplicationError>;
    async fn message(&mut self, id: MessageId) -> Result<Message, ApplicationError>;
    async fn channel(&mut self, id: ChannelId) -> Result<Channel, ApplicationError>;
    async fn task(&mut self, id: TaskId) -> Result<Task, ApplicationError>;
    async fn run(&mut self, id: RunId) -> Result<Run, ApplicationError>;
    async fn inbox_item(&mut self, id: InboxItemId) -> Result<InboxItem, ApplicationError>;
    async fn agent(&mut self, id: MemberId) -> Result<Agent, ApplicationError>;
    async fn computer(&mut self, id: ComputerId) -> Result<Computer, ApplicationError>;

    async fn task_for_source(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<TaskId>, ApplicationError>;
    async fn unfinished_task_for_thread(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<TaskId>, ApplicationError>;
    async fn task_for_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<Option<TaskId>, ApplicationError>;
    async fn resource_for_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<Option<uuid::Uuid>, ApplicationError>;
    async fn active_run_for_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<Option<RunId>, ApplicationError>;
    async fn computer_has_assigned_agents(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<bool, ApplicationError>;
    async fn completed_run_for_event(
        &mut self,
        event_id: EventId,
    ) -> Result<Option<RunId>, ApplicationError>;
    /// Non-terminal Runs whose ownership lease already lapsed, oldest first. Bounded by `limit` so
    /// one sweep cannot hold a transaction open across an unbounded backlog.
    async fn runs_with_expired_lease(
        &mut self,
        now: time::OffsetDateTime,
        limit: u32,
    ) -> Result<Vec<RunId>, ApplicationError>;

    async fn can_read_thread(
        &mut self,
        actor: MemberId,
        thread_id: ThreadId,
    ) -> Result<bool, ApplicationError>;
    async fn can_link_thread(
        &mut self,
        actor: MemberId,
        task: &Task,
        target: &Thread,
    ) -> Result<bool, ApplicationError>;
    async fn can_assign_agent(
        &mut self,
        agent: MemberId,
        source: &Thread,
    ) -> Result<bool, ApplicationError>;
    async fn can_govern_task(
        &mut self,
        actor: MemberId,
        task: &Task,
    ) -> Result<bool, ApplicationError>;
    async fn has_permission(
        &mut self,
        actor: MemberId,
        action: crate::server::domain::identity::PermissionAction,
    ) -> Result<bool, ApplicationError>;
    async fn can_manage_permissions(
        &mut self,
        actor: MemberId,
        target: MemberId,
    ) -> Result<bool, ApplicationError>;
    async fn can_operate_agent(
        &mut self,
        computer_id: ComputerId,
        agent_id: MemberId,
    ) -> Result<bool, ApplicationError>;
    async fn member_access_level(
        &mut self,
        member_id: MemberId,
        space_id: crate::ids::SpaceId,
    ) -> Result<AccessLevel, ApplicationError>;
    async fn computer_accepts_agent(
        &mut self,
        computer_id: ComputerId,
        space_id: crate::ids::SpaceId,
    ) -> Result<bool, ApplicationError>;
    async fn thread_message_sequence(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<u64, ApplicationError>;
    async fn publish_message(
        &mut self,
        draft: MessageDraft,
    ) -> Result<PublishedMessage, ApplicationError>;

    async fn insert_task(&mut self, task: Task) -> Result<(), ApplicationError>;
    async fn save_task(&mut self, task: Task) -> Result<(), ApplicationError>;
    async fn save_run(&mut self, run: Run) -> Result<(), ApplicationError>;
    async fn save_inbox_item(&mut self, item: InboxItem) -> Result<(), ApplicationError>;
    /// Records that an Item was retired without being handled. Carries no Message, so the notice
    /// states the failure without copying the source body. Returns the new Item's ID.
    async fn insert_dead_item_notice(
        &mut self,
        agent_id: MemberId,
        thread_id: ThreadId,
        error_code: &'static str,
        now: time::OffsetDateTime,
    ) -> Result<InboxItemId, ApplicationError>;
    async fn insert_message(&mut self, message: Message) -> Result<(), ApplicationError>;
    async fn save_message(&mut self, message: Message) -> Result<(), ApplicationError>;
    async fn grant_permission(
        &mut self,
        target: MemberId,
        action: crate::server::domain::identity::PermissionAction,
        granted_by: MemberId,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn revoke_permission(
        &mut self,
        target: MemberId,
        action: crate::server::domain::identity::PermissionAction,
    ) -> Result<(), ApplicationError>;
    async fn insert_channel(&mut self, channel: Channel) -> Result<(), ApplicationError>;
    async fn insert_agent(&mut self, member: Member, agent: Agent) -> Result<(), ApplicationError>;
    async fn save_agent(&mut self, agent: Agent) -> Result<(), ApplicationError>;
    async fn save_computer(&mut self, computer: Computer) -> Result<(), ApplicationError>;
    async fn record_completed_run_event(
        &mut self,
        event_id: EventId,
        run_id: RunId,
    ) -> Result<(), ApplicationError>;
    async fn record_task_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        task_id: TaskId,
    ) -> Result<(), ApplicationError>;
    async fn record_resource_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        resource_id: uuid::Uuid,
    ) -> Result<(), ApplicationError>;
    async fn record_task_audit(
        &mut self,
        actor: MemberId,
        action: &str,
        task_id: TaskId,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    fn emit(&mut self, effect: Effect);
}

pub(in crate::server) trait TransactionPort {
    type Transaction: ServerTransaction;

    async fn transact<T>(
        &mut self,
        operation: impl for<'a> AsyncFnOnce(&'a mut Self::Transaction) -> Result<T, ApplicationError>,
    ) -> Result<T, ApplicationError>;
}
