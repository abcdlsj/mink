pub(in crate::computer) mod capability;
pub(in crate::computer) mod command;
pub(in crate::computer) mod pipeline;
pub(in crate::computer) mod ports;
pub(in crate::computer) mod query;
pub(in crate::computer) mod recovery;
pub(in crate::computer) mod run;
pub(in crate::computer) mod scheduler;

pub(in crate::computer) use crate::computer::core::{
    home::{LocalAgent, LocalAgentState, MemoryFile},
    input::{
        ActivityEventInput, AgentInput, AttentionNoticeInput, ChannelMemberInput,
        ContextMessageInput, DispatchedItemInput, MemoryEntryInput, NoticeLocationInput,
        RunContextInput, RunInput, TaskInput, WorkInput,
    },
    scheduler::{RunPriority, WorkStrength},
    session::{
        Continuity, ContinuityState, DriverKind, ProviderSession, ProviderSessionSnapshot,
        SessionFingerprint, SessionScope, SessionState,
    },
    supervisor::{
        Delivery, DeliveryState, ItemDisposition, LocalRun, LocalRunSnapshot, LocalRunState,
        NewRun, NoticeDelivery, TerminalStatus,
    },
};

#[cfg(test)]
mod tests;

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
pub(in crate::computer) enum ApplicationError {
    #[error(transparent)]
    Core(#[from] crate::computer::core::CoreError),
    #[error("local resource was not found")]
    NotFound,
    #[error("run capability is invalid or expired")]
    Unauthenticated,
    #[error("local state conflicts with the command")]
    Conflict,
    #[error("command was already applied")]
    AlreadyApplied,
    #[error("driver is unavailable")]
    DriverUnavailable,
    #[error("provider session cannot be resumed")]
    SessionLost,
    #[error("local persistence failed")]
    Internal,
}
