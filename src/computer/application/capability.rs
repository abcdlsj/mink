use subtle::ConstantTimeEq;

use crate::ids::{AgentId, RunId, SpaceId, TaskId, ThreadId};

use super::{
    ApplicationError,
    ports::{ComputerTransaction, TransactionPort},
};
use crate::computer::core::supervisor::LocalRunState;
use crate::{computer::core::supervisor::ItemDisposition, protocol::capability::Action};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) enum ScopeRequirement {
    CurrentRun,
    BoundTask,
}

#[derive(Clone, Eq, PartialEq)]
pub(in crate::computer) struct AuthorizedCapability {
    pub(in crate::computer) agent_id: AgentId,
    pub(in crate::computer) space_id: SpaceId,
    pub(in crate::computer) task_id: Option<TaskId>,
    pub(in crate::computer) focus_thread_id: ThreadId,
    pub(in crate::computer) run_id: RunId,
    pub(in crate::computer) fencing_token: String,
    pub(in crate::computer) message_snapshot_sequence: u64,
}

impl std::fmt::Debug for AuthorizedCapability {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("AuthorizedCapability")
            .field("agent_id", &self.agent_id)
            .field("space_id", &self.space_id)
            .field("task_id", &self.task_id)
            .field("focus_thread_id", &self.focus_thread_id)
            .field("run_id", &self.run_id)
            .field("fencing_token", &"[REDACTED]")
            .field("message_snapshot_sequence", &self.message_snapshot_sequence)
            .finish()
    }
}

pub(in crate::computer) struct CapabilityService;

impl CapabilityService {
    pub(in crate::computer) async fn record_success<P: TransactionPort>(
        store: &mut P,
        run_id: RunId,
        action: &Action,
    ) -> Result<(), ApplicationError> {
        let disposition = match action {
            Action::MessageSend(send) => send
                .handle_item_id
                .map(|item_id| (item_id, ItemDisposition::Handled)),
            Action::InboxAck { item_id, .. } => Some((*item_id, ItemDisposition::Handled)),
            Action::InboxDefer { item_id, .. } => Some((*item_id, ItemDisposition::Deferred)),
            _ => None,
        };
        if let Some((item_id, disposition)) = disposition {
            super::run::RunService::record_item_disposition(store, run_id, item_id, disposition)
                .await?;
        }
        Ok(())
    }

    pub(in crate::computer) fn validate_agent_path(path: &str) -> Result<(), ApplicationError> {
        let path = std::path::Path::new(path);
        if path.as_os_str().is_empty()
            || path.is_absolute()
            || path
                .components()
                .any(|component| !matches!(component, std::path::Component::Normal(_)))
        {
            return Err(ApplicationError::Conflict);
        }
        Ok(())
    }

    pub(in crate::computer) async fn authorize<P: TransactionPort>(
        store: &mut P,
        run_token: &str,
        requirement: ScopeRequirement,
    ) -> Result<AuthorizedCapability, ApplicationError> {
        let run = store
            .transact(async |transaction| {
                let matches = transaction
                    .nonterminal_runs()?
                    .into_iter()
                    .filter(|run| {
                        bool::from(
                            run.view()
                                .fencing_token
                                .expose()
                                .as_bytes()
                                .ct_eq(run_token.as_bytes()),
                        )
                    })
                    .collect::<Vec<_>>();
                match matches.as_slice() {
                    [run] => Ok(run.clone()),
                    _ => Err(ApplicationError::Unauthenticated),
                }
            })
            .await?;
        if run.view().state != LocalRunState::Running
            || run.view().ownership_lease_expires_at <= time::OffsetDateTime::now_utc()
        {
            return Err(ApplicationError::Unauthenticated);
        }
        if requirement == ScopeRequirement::BoundTask && run.view().task_id.is_none() {
            return Err(ApplicationError::Conflict);
        }
        Ok(AuthorizedCapability {
            agent_id: run.view().agent_id,
            space_id: run.view().input.agent.space_id,
            task_id: run.view().task_id,
            focus_thread_id: run.view().focus_thread_id,
            run_id: run.view().id,
            fencing_token: run.view().fencing_token.expose().to_owned(),
            message_snapshot_sequence: run.view().input.context.message_snapshot_sequence,
        })
    }
}
