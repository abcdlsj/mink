use time::OffsetDateTime;

use crate::ids::{ComputerId, MemberId};

use crate::server::domain::identity::{Agent, Computer};

use super::ports::{ApplicationError, Effect, ServerTransaction, TransactionPort};

pub(in crate::server) struct RetireAgent;

impl RetireAgent {
    pub(in crate::server) fn execute<P: TransactionPort>(
        port: &mut P,
        agent_id: MemberId,
        now: OffsetDateTime,
    ) -> Result<Agent, ApplicationError> {
        port.transact(|transaction| {
            let mut agent = transaction.agent(agent_id)?;
            agent.retire(now)?;
            transaction.save_agent(agent.clone())?;
            transaction.emit(Effect::AgentRetired(agent_id));
            Ok(agent)
        })
    }
}

pub(in crate::server) struct DeleteComputer;

impl DeleteComputer {
    pub(in crate::server) fn execute<P: TransactionPort>(
        port: &mut P,
        computer_id: ComputerId,
        now: OffsetDateTime,
    ) -> Result<Computer, ApplicationError> {
        port.transact(|transaction| {
            let mut computer = transaction.computer(computer_id)?;
            let assigned = transaction.computer_has_assigned_agents(computer_id);
            computer.delete(assigned, now)?;
            transaction.save_computer(computer.clone())?;
            transaction.emit(Effect::ComputerDeleted(computer_id));
            Ok(computer)
        })
    }
}
