use time::OffsetDateTime;

use crate::ids::{ComputerId, MemberId};

use crate::server::domain::{
    attention::InboxItemDisposition,
    identity::{Agent, AgentLifecycle, Computer, ComputerLifecycle},
};

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
            if agent.lifecycle == AgentLifecycle::Retired {
                return Ok(agent);
            }
            let computer_id = agent.computer_id.ok_or(ApplicationError::Conflict)?;
            if let Some(run_id) = transaction.active_run_for_agent(agent_id).await? {
                let mut run = transaction.run(run_id).await?;
                for run_item in run.items.clone() {
                    let disposition = run_item
                        .disposition
                        .unwrap_or(InboxItemDisposition::Released);
                    let mut item = transaction.inbox_item(run_item.inbox_item_id).await?;
                    item.apply_disposition(run.id, disposition, now)?;
                    transaction.save_inbox_item(item).await?;
                }
                run.cancel_for_agent_retirement(now);
                transaction.save_run(run.clone()).await?;
                transaction.emit(Effect::RunCompleted(run.id));
            }
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
            if computer.lifecycle == ComputerLifecycle::Deleted {
                return Ok(computer);
            }
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
