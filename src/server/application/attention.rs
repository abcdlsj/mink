use crate::ids::InboxItemId;

use crate::server::domain::{
    attention::{AttentionStrength, InboxItemStatus},
    execution::RunStatus,
};

use super::ports::{ApplicationError, Effect, ServerTransaction, TransactionPort};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum HardItemRoute {
    Pending,
    Attached { sequence: u64 },
    Notice,
}

pub(in crate::server) struct RouteHardItemInput {
    pub(in crate::server) item_id: InboxItemId,
}

pub(in crate::server) struct RouteHardItem;

impl RouteHardItem {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: RouteHardItemInput,
    ) -> Result<HardItemRoute, ApplicationError> {
        port.transact(async |transaction| {
            let mut item = transaction.inbox_item(input.item_id).await?;
            if item.strength != AttentionStrength::Hard || item.status != InboxItemStatus::Pending {
                return Ok(HardItemRoute::Pending);
            }
            let Some(run_id) = transaction.active_run_for_agent(item.agent_id).await? else {
                return Ok(HardItemRoute::Pending);
            };
            let mut run = transaction.run(run_id).await?;
            if run.status != RunStatus::Running {
                return Ok(HardItemRoute::Pending);
            }
            if run.task_id == item.task_id && run.focus_thread_id == item.thread_id {
                let sequence = run.attach(&item)?;
                item.attach_to_active_run(run.id, run.lease_expires_at)?;
                transaction.save_run(run.clone()).await?;
                transaction.save_inbox_item(item).await?;
                transaction.emit(Effect::ItemAttached {
                    run_id: run.id,
                    item_id: input.item_id,
                    sequence,
                });
                return Ok(HardItemRoute::Attached { sequence });
            }
            let location_visible = transaction
                .can_read_thread(run.agent_id, item.thread_id)
                .await?;
            transaction.emit(Effect::RunNotice {
                run_id: run.id,
                item_id: item.id,
                location_visible,
            });
            Ok(HardItemRoute::Notice)
        })
        .await
    }
}
