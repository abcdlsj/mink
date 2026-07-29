pub(in crate::computer) mod home;
pub(in crate::computer) mod input;
pub(in crate::computer) mod scheduler;
pub(in crate::computer) mod session;
pub(in crate::computer) mod supervisor;

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
pub(in crate::computer) enum CoreError {
    #[error("invalid local state transition")]
    InvalidTransition,
    #[error("run delivery sequence is not contiguous")]
    InvalidDeliverySequence,
    #[error("run delivery conflicts with an existing delivery")]
    ConflictingDelivery,
    #[error("run is not accepting deliveries")]
    RunNotAcceptingDeliveries,
    #[error("run scope does not match the delivery")]
    ScopeMismatch,
    #[error("run input does not match the local run scope")]
    InputScopeMismatch,
    #[error("attention notice conflicts with an existing notice")]
    ConflictingNotice,
    #[error("provider session cannot be promoted")]
    InvalidSessionPromotion,
    #[error("provider session is not available")]
    SessionUnavailable,
    #[error("run result does not account for every delivery")]
    IncompleteItemDisposition,
}
