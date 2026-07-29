use time::OffsetDateTime;

use crate::ids::{ComputerId, MemberId};

use crate::server::domain::identity::{Agent, Computer};

use super::ports::{ApplicationError, Effect, ServerTransaction, TransactionPort};

pub(in crate::server) struct RetireAgent;

impl RetireAgent {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        agent_id: MemberId,
        now: OffsetDateTime,
    ) -> Result<Agent, ApplicationError> {
        port.transact(async |transaction| {
            let mut agent = transaction.agent(agent_id).await?;
            let computer_id = agent.computer_id.ok_or(ApplicationError::Conflict)?;
            agent.retire(now)?;
            transaction.save_agent(agent.clone()).await?;
            transaction.emit(Effect::AgentRetired {
                agent_id,
                computer_id,
            });
            Ok(agent)
        })
        .await
    }
}

pub(in crate::server) struct DeleteComputer;

impl DeleteComputer {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        computer_id: ComputerId,
        now: OffsetDateTime,
    ) -> Result<Computer, ApplicationError> {
        port.transact(async |transaction| {
            let mut computer = transaction.computer(computer_id).await?;
            let assigned = transaction
                .computer_has_assigned_agents(computer_id)
                .await?;
            computer.delete(assigned, now)?;
            transaction.save_computer(computer.clone()).await?;
            transaction.emit(Effect::ComputerDeleted(computer_id));
            Ok(computer)
        })
        .await
    }
}
