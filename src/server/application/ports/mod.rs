use crate::ids::{
    AttachmentId, ChannelId, CommandId, ComputerId, EventId, IdempotencyKey, InboxItemId, MemberId,
    MessageId, RunId, SpaceId, TaskId, ThreadId,
};

use crate::server::domain::{
    DomainError,
    access::{HumanRegistration, SpaceAccess},
    attachment::Attachment,
    attention::{AttentionStrength, InboxItem, InboxItemKind, InboxItemStatus},
    conversation::{Channel, Message, Thread},
    execution::{Run, RunTrigger},
    identity::{AccessLevel, Agent, Computer, Member, PermissionAction},
    invitation::Invitation,
    pairing::{ComputerOs, Pairing, PairingStatus},
    task::Task,
};

use async_trait::async_trait;
use sha2::{Digest, Sha256};
use std::collections::BTreeSet;
use std::fmt;

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
pub(in crate::server) enum ApplicationError {
    #[error(transparent)]
    Domain(#[from] DomainError),
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
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) name: String,
    pub(in crate::server) hostname: String,
    pub(in crate::server) os: ComputerOs,
    pub(in crate::server) daemon_version: String,
    pub(in crate::server) token_hash: String,
    pub(in crate::server) created_at: time::OffsetDateTime,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct PairedComputer {
    pub(in crate::server) id: ComputerId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) name: String,
    pub(in crate::server) hostname: String,
    pub(in crate::server) os: ComputerOs,
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
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) space_name: String,
    pub(in crate::server) space_slug: String,
    pub(in crate::server) email: String,
    pub(in crate::server) expires_at: time::OffsetDateTime,
    pub(in crate::server) accepted_at: Option<time::OffsetDateTime>,
    pub(in crate::server) accepted_by_member_id: Option<MemberId>,
}

pub(in crate::server) struct HumanMemberRecord {
    pub(in crate::server) member_id: MemberId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) user_id: uuid::Uuid,
    pub(in crate::server) display_name: String,
    pub(in crate::server) created_at: time::OffsetDateTime,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct DirectMessageView {
    pub(in crate::server) channel_id: ChannelId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) other_member: SpaceMemberView,
    pub(in crate::server) created_at: time::OffsetDateTime,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct SpaceMemberView {
    pub(in crate::server) id: MemberId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) kind: MemberKind,
    pub(in crate::server) display_name: String,
    pub(in crate::server) access_level: AccessLevel,
    pub(in crate::server) permissions: Vec<PermissionAction>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum MemberKind {
    Human,
    Agent,
}

/// Which Items an Inbox read returns. The queue is the default; retired Items are a separate view so a
/// governor can find what to requeue without turning history into part of the queue.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum InboxScope {
    Queue,
    Dead,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum InboxActivityEventKind {
    Message,
    MemberJoined,
    MemberLeft,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct InboxActivityEventView {
    pub(in crate::server) sequence: u64,
    pub(in crate::server) kind: InboxActivityEventKind,
    pub(in crate::server) message_id: Option<MessageId>,
    pub(in crate::server) member_id: Option<MemberId>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct InboxItemView {
    pub(in crate::server) id: InboxItemId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) member_id: MemberId,
    pub(in crate::server) kind: InboxItemKind,
    pub(in crate::server) strength: AttentionStrength,
    pub(in crate::server) status: InboxItemStatus,
    pub(in crate::server) channel_id: Option<ChannelId>,
    pub(in crate::server) channel_slug: Option<String>,
    pub(in crate::server) thread_id: Option<ThreadId>,
    pub(in crate::server) message_id: Option<MessageId>,
    pub(in crate::server) sender_member_id: Option<MemberId>,
    pub(in crate::server) sender_display_name: Option<String>,
    pub(in crate::server) message_preview: Option<String>,
    pub(in crate::server) activity_events: Vec<InboxActivityEventView>,
    pub(in crate::server) available_at: time::OffsetDateTime,
    pub(in crate::server) created_at: time::OffsetDateTime,
    pub(in crate::server) retry_count: u32,
    pub(in crate::server) requeue_count: u32,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct SpaceHumanMember {
    pub(in crate::server) member_id: MemberId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) display_name: String,
}

pub(in crate::server) struct PairingView {
    pub(in crate::server) pairing_id: uuid::Uuid,
    pub(in crate::server) hostname: String,
    pub(in crate::server) os: ComputerOs,
    pub(in crate::server) daemon_version: String,
    pub(in crate::server) token_fingerprint: String,
    pub(in crate::server) status: PairingStatus,
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
    RunDispatched(RunId),
    RunCancelRequested(RunId),
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
    /// Whether this Message explicitly addresses every Agent in its Channel.
    pub(in crate::server) mention_all: bool,
    pub(in crate::server) attachment_ids: Vec<AttachmentId>,
    pub(in crate::server) handled_item: Option<(RunId, InboxItemId)>,
    pub(in crate::server) now: time::OffsetDateTime,
}

pub(in crate::server) struct PublishedMessage {
    pub(in crate::server) message_id: MessageId,
    /// Hard Items only for Agent recipients. `RouteHardItem` decides whether they attach to an
    /// active Run or become a notice; Human Items have no Run to route to.
    pub(in crate::server) hard_item_ids: Vec<InboxItemId>,
    /// Members that received an Item from this Message, hard or ambient. Drives `inbox.changed`,
    /// so it covers ambient recipients that `hard_item_ids` omits.
    pub(in crate::server) notified_member_ids: Vec<MemberId>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct CreatedSpace {
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) owner_id: MemberId,
    pub(in crate::server) general_channel_id: ChannelId,
}

/// Work the dispatcher found for one Agent: a pending Item and the Computer that hosts the Agent. The
/// dispatcher returns identifiers only; every state transition stays in `DispatchRun`.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct DispatchCandidate {
    pub(in crate::server) item_id: InboxItemId,
    pub(in crate::server) agent_id: MemberId,
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) task_id: Option<TaskId>,
    pub(in crate::server) thread_id: ThreadId,
    pub(in crate::server) message_id: Option<MessageId>,
    pub(in crate::server) channel_id: ChannelId,
    pub(in crate::server) trigger: RunTrigger,
}

/// Proves a capability call belongs to a live Run on the Computer making the call. Carries no token
/// and no deadline: the Run being `working` on this Computer is the whole proof.
pub(in crate::server) struct RunCapabilityProof {
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) agent_id: MemberId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) task_id: Option<TaskId>,
    pub(in crate::server) focus_thread_id: ThreadId,
}

mod attachment;
mod collaboration;
mod effects;
mod execution;
mod identity;
mod task;
mod transaction;

pub(in crate::server) use attachment::AttachmentTransaction;
pub(in crate::server) use collaboration::CollaborationTransaction;
pub(in crate::server) use effects::EffectSink;
pub(in crate::server) use execution::ExecutionTransaction;
pub(in crate::server) use identity::IdentityTransaction;
pub(in crate::server) use task::TaskTransaction;
pub(in crate::server) use transaction::TransactionPort;

pub(in crate::server) trait ServerTransaction:
    IdentityTransaction
    + CollaborationTransaction
    + TaskTransaction
    + ExecutionTransaction
    + AttachmentTransaction
    + EffectSink
{
}

impl<T> ServerTransaction for T where
    T: IdentityTransaction
        + CollaborationTransaction
        + TaskTransaction
        + ExecutionTransaction
        + AttachmentTransaction
        + EffectSink
{
}
