use crate::ids::AgentId;

use crate::computer::core::session::continuity;

use super::{
    ApplicationError, Continuity, MemoryFile, SessionScope,
    ports::{AgentHomePort, ComputerTransaction, TransactionPort},
};

pub(in crate::computer) struct QueryService;

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
