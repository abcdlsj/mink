use crate::ids::{InboxItemId, MemberId, SpaceId};

use crate::server::domain::{
    attention::{AttentionStrength, InboxItemStatus},
    execution::RunStatus,
};

use super::ports::{
    ApplicationError, Effect, InboxItemView, InboxScope, MemberKind, ServerTransaction,
    TransactionPort,
};

pub(in crate::server) struct ReadMemberInbox;

impl ReadMemberInbox {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        actor_id: MemberId,
        target_id: MemberId,
        space_id: SpaceId,
        scope: InboxScope,
    ) -> Result<Vec<InboxItemView>, ApplicationError> {
        port.transact(async |transaction| {
            if actor_id != target_id {
                let target = transaction
                    .space_member(target_id, space_id)
                    .await?
                    .ok_or(ApplicationError::NotFound)?;
                if target.kind != MemberKind::Agent {
                    return Err(ApplicationError::PermissionDenied);
                }
                let access = transaction.member_access_level(actor_id, space_id).await?;
                if !access.can_manage_space() {
                    return Err(ApplicationError::PermissionDenied);
                }
            }
            transaction.inbox_for_member(target_id, scope).await
        })
        .await
    }
}

pub(in crate::server) struct RequeueDeadItemInput {
    pub(in crate::server) item_id: InboxItemId,
    pub(in crate::server) actor_id: MemberId,
    pub(in crate::server) now: time::OffsetDateTime,
}

/// Returns a retired Item to the queue. The Item belongs to an Agent, so this authorizes the actor as
/// a governor of that Agent's Space rather than as the Item's owner.
pub(in crate::server) struct RequeueDeadItem;

impl RequeueDeadItem {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: RequeueDeadItemInput,
    ) -> Result<InboxItemView, ApplicationError> {
        port.transact(async |transaction| {
            let mut item = transaction.inbox_item(input.item_id).await?;
            let access = transaction
                .member_access_level(input.actor_id, item.space_id)
                .await?;
            if !access.can_manage_space() {
                return Err(ApplicationError::PermissionDenied);
            }
            item.requeue_from_dead(input.now)?;
            let agent_id = item.agent_id;
            let source_message_id = item.message_id;
            transaction.save_inbox_item(item).await?;
            transaction
                .record_inbox_item_audit(
                    input.actor_id,
                    "inbox_item.requeued",
                    input.item_id,
                    input.now,
                )
                .await?;
            // The Item is claimable again, so both the Agent's queue and the source Message's run
            // state projection are stale.
            transaction.emit(Effect::InboxChanged(agent_id));
            if let Some(message_id) = source_message_id {
                transaction.emit(Effect::MessageUpdated(message_id));
            }
            transaction.inbox_item_view(input.item_id).await
        })
        .await
    }
}

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
