pub(in crate::computer) mod command;
pub(in crate::computer) mod ports;
pub(in crate::computer) mod recovery;
pub(in crate::computer) mod run;
pub(in crate::computer) mod scheduler;

#[cfg(test)]
mod tests;

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
pub(in crate::computer) enum ApplicationError {
    #[error(transparent)]
    Core(#[from] crate::computer::core::CoreError),
    #[error("local resource was not found")]
    NotFound,
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
