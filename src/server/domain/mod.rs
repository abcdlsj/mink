pub(in crate::server) mod access;
pub(in crate::server) mod attachment;
pub(in crate::server) mod attention;
pub(in crate::server) mod conversation;
pub(in crate::server) mod execution;
pub(in crate::server) mod identity;
pub(in crate::server) mod invitation;
pub(in crate::server) mod pairing;
pub(in crate::server) mod task;

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
pub(in crate::server) enum DomainError {
    #[error("invalid state transition")]
    InvalidTransition,
    #[error("persisted domain state is invalid")]
    InvalidPersistedState,
    #[error("message is not a root message")]
    SourceIsNotRoot,
    #[error("source message and thread do not match")]
    SourceMismatch,
    #[error("task requires an assignee")]
    AssigneeRequired,
    #[error("task review must be decided by another visible member")]
    InvalidReviewer,
    #[error("thread is not linked to the task")]
    FocusOutsideTask,
    #[error("thread audience differs from the task audience")]
    IncompatibleAudience,
    #[error("source thread cannot be added or removed as a related thread")]
    SourceThreadImmutable,
    #[error("inbox item cannot be attached to this run")]
    ItemScopeMismatch,
    #[error("ambient inbox item cannot attach to an active run")]
    AmbientItemCannotAttach,
    #[error("inbox item does not aggregate ambient activity")]
    ItemIsNotAmbientAggregate,
    #[error("run no longer accepts inbox items")]
    RunNotAcceptingItems,
    #[error("run item disposition is incomplete")]
    IncompleteItemDisposition,
    #[error("action messages must be replies")]
    ActionMustBeReply,
    #[error("message cannot be edited or deleted in its current state")]
    InvalidMessageMutation,
    #[error("channel kind and slug do not form a valid channel")]
    InvalidChannel,
    #[error("channel slug does not meet the required form")]
    InvalidChannelSlug,
    #[error("agent is already retired")]
    AgentRetired,
    #[error("agent role text is required")]
    InvalidRole,
    #[error("channel cannot be joined without an invitation")]
    ChannelNotJoinable,
    #[error("computer still has assigned agents")]
    ComputerHasAgents,
    #[error("credential does not meet the required form")]
    InvalidCredential,
    #[error("space owner or admin access is required")]
    GovernorRequired,
    #[error("computer pairing request is not well formed")]
    InvalidPairing,
    #[error("computer pairing is no longer pending")]
    PairingLapsed,
    #[error("attachment name or media type is missing")]
    InvalidAttachment,
    #[error("attachment belongs to another uploader")]
    AttachmentNotOwned,
    #[error("attachment upload is not open")]
    AttachmentNotOpen,
    #[error("attachment content is not ready")]
    AttachmentNotReady,
    #[error("attachment size or digest does not match the stored content")]
    AttachmentContentMismatch,
    #[error("space invitation is not well formed")]
    InvalidInvitation,
    #[error("space invitation is no longer pending")]
    InvitationLapsed,
    #[error("space invitation was issued to another email")]
    InvitationEmailMismatch,
}
