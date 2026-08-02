use crate::ids::{InboxItemId, MemberId, SpaceId};

use crate::server::domain::{
    attention::{AttentionStrength, InboxItemStatus},
    execution::RunStatus,
};

use super::ports::{
    ApplicationError, CollaborationTransaction, Effect, EffectSink, ExecutionTransaction,
    IdentityTransaction, InboxItemView, InboxScope, MemberKind, TransactionPort,
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
            let item_view = item.view();
            let access = transaction
                .member_access_level(input.actor_id, item_view.space_id)
                .await?;
            if !access.can_manage_space() {
                return Err(ApplicationError::PermissionDenied);
            }
            item.requeue_from_dead(input.now)?;
            let member_id = item_view.member_id;
            let source_message_id = item_view.message_id;
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
            transaction.emit(Effect::InboxChanged(member_id));
            if let Some(message_id) = source_message_id {
                transaction.emit(Effect::MessageUpdated(message_id));
            }
            transaction.inbox_item_view(input.item_id).await
        })
        .await
    }
}

pub(in crate::server) struct MarkInboxItemReadInput {
    pub(in crate::server) item_id: InboxItemId,
    pub(in crate::server) actor_id: MemberId,
    pub(in crate::server) now: time::OffsetDateTime,
}

/// Marks a Human-owned Item handled on its owner's explicit read. Agent Items never enter this
/// path: their terminal state belongs to the Run that leased them, so this command rejects them.
/// Reading an already handled Item is idempotent and returns the current projection.
pub(in crate::server) struct MarkInboxItemRead;

impl MarkInboxItemRead {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: MarkInboxItemReadInput,
    ) -> Result<InboxItemView, ApplicationError> {
        port.transact(async |transaction| {
            let mut item = transaction.inbox_item(input.item_id).await?;
            let item_view = item.view();
            if input.actor_id != item_view.member_id {
                return Err(ApplicationError::PermissionDenied);
            }
            let owner = transaction
                .space_member(item_view.member_id, item_view.space_id)
                .await?
                .ok_or(ApplicationError::NotFound)?;
            if owner.kind != MemberKind::Human {
                return Err(ApplicationError::PermissionDenied);
            }
            item.mark_read(input.now)?;
            transaction.save_inbox_item(item).await?;
            transaction.emit(Effect::InboxChanged(item_view.member_id));
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
            let item_view = item.view();
            if item_view.strength != AttentionStrength::Hard
                || item_view.status != InboxItemStatus::Pending
            {
                return Ok(HardItemRoute::Pending);
            }
            let Some(run_id) = transaction
                .active_run_for_agent(item_view.member_id)
                .await?
            else {
                return Ok(HardItemRoute::Pending);
            };
            let mut run = transaction.run(run_id).await?;
            let run_view = run.view();
            let run_id = run_view.id;
            let lease_expires_at = run_view.lease_expires_at;
            if run_view.status != RunStatus::Running {
                return Ok(HardItemRoute::Pending);
            }
            if run_view.task_id == item_view.task_id
                && run_view.focus_thread_id == item_view.thread_id
            {
                let sequence = run.attach(&item)?;
                item.attach_to_active_run(run_id, lease_expires_at)?;
                transaction.save_run(run.clone()).await?;
                transaction.save_inbox_item(item).await?;
                transaction.emit(Effect::ItemAttached {
                    run_id,
                    item_id: input.item_id,
                    sequence,
                });
                return Ok(HardItemRoute::Attached { sequence });
            }
            let location_visible = transaction
                .can_read_thread(run_view.agent_id, item_view.thread_id)
                .await?;
            transaction.emit(Effect::RunNotice {
                run_id,
                item_id: item_view.id,
                location_visible,
            });
            Ok(HardItemRoute::Notice)
        })
        .await
    }
}
