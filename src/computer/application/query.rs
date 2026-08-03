use crate::ids::{AgentId, RunId};

use crate::computer::core::{
    session::{SessionState, continuity},
    supervisor::{LocalRun, LocalRunState},
};

use super::{
    ApplicationError, Continuity, MemoryFile, SessionScope,
    ports::{AgentHomePort, ComputerTransaction, TransactionPort},
};

pub(in crate::computer) struct QueryService;

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::computer) struct RuntimeDiagnostics {
    pub(in crate::computer) local_run_id: Option<RunId>,
    pub(in crate::computer) local_run_state: Option<LocalRunState>,
    pub(in crate::computer) queued_runs: u32,
    pub(in crate::computer) active_runs: u32,
    pub(in crate::computer) pending_commands: u32,
    pub(in crate::computer) pending_result_events: u32,
    pub(in crate::computer) warm_sessions: u32,
    pub(in crate::computer) cold_sessions: u32,
    pub(in crate::computer) reset_required_sessions: u32,
}

impl QueryService {
    pub(in crate::computer) async fn session_continuity<P: TransactionPort>(
        store: &mut P,
        agent_id: AgentId,
        scope: SessionScope,
    ) -> Result<Continuity, ApplicationError> {
        let sessions = store
            .transact(async |transaction| transaction.agent_sessions(agent_id))
            .await?;
        Ok(continuity(&sessions, agent_id, scope))
    }

    pub(in crate::computer) async fn runtime_diagnostics<P: TransactionPort>(
        store: &mut P,
        agent_id: AgentId,
    ) -> Result<RuntimeDiagnostics, ApplicationError> {
        store
            .transact(async |transaction| {
                let runs = transaction.nonterminal_runs()?;
                let local_run = runs
                    .iter()
                    .find(|run| run.view().agent_id == agent_id)
                    .map(LocalRun::view);
                let queued_runs = runs
                    .iter()
                    .filter(|run| run.view().state == LocalRunState::Queued)
                    .count();
                let active_runs = runs
                    .iter()
                    .filter(|run| run.view().state != LocalRunState::Queued)
                    .count();
                let sessions = transaction.agent_sessions(agent_id)?;
                let (warm_sessions, cold_sessions, reset_required_sessions) = sessions
                    .into_iter()
                    .fold((0_u32, 0_u32, 0_u32), |mut counts, session| {
                        match session.view().state {
                            SessionState::Ready | SessionState::InUse => counts.0 += 1,
                            SessionState::Closing | SessionState::Closed => counts.1 += 1,
                            SessionState::Lost => counts.2 += 1,
                        }
                        counts
                    });
                Ok(RuntimeDiagnostics {
                    local_run_id: local_run.as_ref().map(|run| run.id),
                    local_run_state: local_run.as_ref().map(|run| run.state),
                    queued_runs: count_u32(queued_runs)?,
                    active_runs: count_u32(active_runs)?,
                    pending_commands: count_u32(transaction.pending_commands()?.len())?,
                    pending_result_events: count_u32(transaction.pending_events()?.len())?,
                    warm_sessions,
                    cold_sessions,
                    reset_required_sessions,
                })
            })
            .await
    }

    pub(in crate::computer) async fn memory_files<H: AgentHomePort>(
        homes: &mut H,
        agent_id: AgentId,
    ) -> Result<Vec<MemoryFile>, ApplicationError> {
        homes.list_memory(agent_id).await
    }

    pub(in crate::computer) async fn memory_content<H: AgentHomePort>(
        homes: &mut H,
        agent_id: AgentId,
        path: &str,
    ) -> Result<(MemoryFile, String), ApplicationError> {
        super::capability::CapabilityService::validate_agent_path(path)?;
        let content = homes
            .read_memory(agent_id, std::path::Path::new(path))
            .await?;
        let file = homes
            .list_memory(agent_id)
            .await?
            .into_iter()
            .find(|file| file.path == path)
            .ok_or(ApplicationError::NotFound)?;
        let content = String::from_utf8(content).map_err(|_| ApplicationError::Conflict)?;
        Ok((file, content))
    }
}

fn count_u32(value: usize) -> Result<u32, ApplicationError> {
    value.try_into().map_err(|_| ApplicationError::Internal)
}
