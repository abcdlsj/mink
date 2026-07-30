use time::OffsetDateTime;

use crate::ids::{ComputerId, IdempotencyKey, MemberId};

use crate::server::domain::{
    attention::{InboxItemDisposition, InboxItemStatus},
    identity::{Agent, AgentLifecycle, Computer, ComputerLifecycle, PermissionAction},
};

use super::ports::{ApplicationError, Effect, ServerTransaction, TransactionPort};

pub(in crate::server) struct SetPermission;

impl SetPermission {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        actor_id: MemberId,
        target_id: MemberId,
        action: PermissionAction,
        enabled: bool,
        idempotency_key: IdempotencyKey,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        let operation = if enabled {
            "permission.grant"
        } else {
            "permission.revoke"
        };
        port.transact(async |transaction| {
            if transaction
                .resource_for_idempotency(actor_id, operation, idempotency_key)
                .await?
                .is_some()
            {
                return Ok(());
            }
            if !transaction
                .can_manage_permissions(actor_id, target_id)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            if enabled {
                transaction
                    .grant_permission(target_id, action, actor_id, now)
                    .await?;
            } else {
                transaction.revoke_permission(target_id, action).await?;
            }
            transaction
                .record_resource_idempotency(
                    actor_id,
                    operation,
                    idempotency_key,
                    target_id.into_uuid(),
                )
                .await?;
            transaction.emit(Effect::PermissionChanged(target_id));
            Ok(())
        })
        .await
    }
}

pub(in crate::server) struct RetireAgent;

impl RetireAgent {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        actor_id: MemberId,
        agent_id: MemberId,
        idempotency_key: IdempotencyKey,
        now: OffsetDateTime,
    ) -> Result<Agent, ApplicationError> {
        port.transact(async |transaction| {
            if let Some(agent_id) = transaction
                .resource_for_idempotency(actor_id, "agent.retire", idempotency_key)
                .await?
            {
                return transaction.agent(MemberId::from_uuid(agent_id)).await;
            }
            let mut agent = transaction.agent(agent_id).await?;
            let computer_id = agent.computer_id;
            if agent.lifecycle != AgentLifecycle::Retired {
                let assigned_computer = computer_id.ok_or(ApplicationError::Conflict)?;
                if let Some(run_id) = transaction.active_run_for_agent(agent_id).await? {
                    let mut run = transaction.run(run_id).await?;
                    for run_item in run.items.clone() {
                        let disposition = run_item
                            .disposition
                            .unwrap_or(InboxItemDisposition::Released);
                        let mut item = transaction.inbox_item(run_item.inbox_item_id).await?;
                        if item.status == InboxItemStatus::Leased {
                            item.apply_disposition(run.id, disposition, now)?;
                            transaction.save_inbox_item(item).await?;
                        } else if disposition != InboxItemDisposition::Released {
                            return Err(ApplicationError::Conflict);
                        }
                    }
                    run.cancel_for_agent_retirement(now);
                    transaction.save_run(run.clone()).await?;
                    transaction.emit(Effect::RunCompleted(run.id));
                }
                agent.retire(now)?;
                transaction.save_agent(agent.clone()).await?;
                transaction.emit(Effect::AgentRetired {
                    agent_id,
                    computer_id: assigned_computer,
                });
            }
            transaction
                .record_resource_idempotency(
                    actor_id,
                    "agent.retire",
                    idempotency_key,
                    agent_id.into_uuid(),
                )
                .await?;
            Ok(agent)
        })
        .await
    }
}

pub(in crate::server) struct DeleteComputer;

impl DeleteComputer {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        actor_id: MemberId,
        computer_id: ComputerId,
        idempotency_key: IdempotencyKey,
        now: OffsetDateTime,
    ) -> Result<Computer, ApplicationError> {
        port.transact(async |transaction| {
            if let Some(computer_id) = transaction
                .resource_for_idempotency(actor_id, "computer.delete", idempotency_key)
                .await?
            {
                return transaction
                    .computer(ComputerId::from_uuid(computer_id))
                    .await;
            }
            let mut computer = transaction.computer(computer_id).await?;
            if computer.lifecycle != ComputerLifecycle::Deleted {
                let assigned = transaction
                    .computer_has_assigned_agents(computer_id)
                    .await?;
                computer.delete(assigned, now)?;
                transaction.save_computer(computer.clone()).await?;
                transaction.emit(Effect::ComputerDeleted(computer_id));
            }
            transaction
                .record_resource_idempotency(
                    actor_id,
                    "computer.delete",
                    idempotency_key,
                    computer_id.into_uuid(),
                )
                .await?;
            Ok(computer)
        })
        .await
    }
}
