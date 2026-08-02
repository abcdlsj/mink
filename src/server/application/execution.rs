use time::OffsetDateTime;

use crate::ids::{ComputerId, EventId, InboxItemId, MemberId, MessageId, RunId, TaskId, ThreadId};

use crate::server::domain::{
    attention::{InboxItemDisposition, InboxItemStatus},
    execution::{Run, RunErrorCode, RunOutcome, RunStatus},
    task::TaskStatus,
};

use super::ports::{
    ApplicationError, CollaborationTransaction, Effect, EffectSink, ExecutionTransaction,
    IdentityTransaction, RawFencingToken, RunCapabilityProof, TaskTransaction, TransactionPort,
};

pub(in crate::server) struct ClaimNextRun;

impl ClaimNextRun {
    pub(in crate::server) async fn candidate<P: TransactionPort>(
        port: &mut P,
        computer_id: ComputerId,
    ) -> Result<Option<super::ports::ClaimCandidate>, ApplicationError> {
        port.transact(async |transaction| transaction.next_claim_candidate(computer_id).await)
            .await
    }

    /// The claim transaction has already rolled back when this method is called. Keeping this
    /// write in a second transaction preserves the diagnostic Item state and outbox event.
    pub(in crate::server) async fn record_failure<P: TransactionPort>(
        port: &mut P,
        item_id: InboxItemId,
        message_id: Option<crate::ids::MessageId>,
        channel_id: crate::ids::ChannelId,
        error_code: &str,
    ) -> Result<bool, ApplicationError> {
        port.transact(async |transaction| {
            transaction
                .record_claim_failure(item_id, message_id, channel_id, error_code)
                .await
        })
        .await
    }
}

pub(in crate::server) struct AuthorizeRunCapability;

impl AuthorizeRunCapability {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        proof: RunCapabilityProof,
    ) -> Result<bool, ApplicationError> {
        port.transact(async |transaction| transaction.authorize_run_capability(&proof).await)
            .await
    }
}

pub(in crate::server) struct ReadAgentChannel;
impl ReadAgentChannel {
    pub(in crate::server) async fn membership<P: TransactionPort>(
        port: &mut P,
        channel_id: crate::ids::ChannelId,
        agent_id: MemberId,
    ) -> Result<bool, ApplicationError> {
        port.transact(async |transaction| {
            transaction
                .channel_member_visible(channel_id, agent_id)
                .await
        })
        .await
    }
    pub(in crate::server) async fn around_sequence<P: TransactionPort>(
        port: &mut P,
        message_id: MessageId,
        channel_id: crate::ids::ChannelId,
    ) -> Result<Option<u64>, ApplicationError> {
        port.transact(async |transaction| {
            transaction
                .message_sequence_in_channel(message_id, channel_id)
                .await
        })
        .await
    }
    pub(in crate::server) async fn snapshot<P: TransactionPort>(
        port: &mut P,
        channel_id: crate::ids::ChannelId,
    ) -> Result<u64, ApplicationError> {
        port.transact(async |transaction| transaction.channel_snapshot(channel_id).await)
            .await
    }
}

pub(in crate::server) struct ReadCurrentAgentRun;
impl ReadCurrentAgentRun {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        agent_id: MemberId,
        viewer_id: MemberId,
    ) -> Result<(Option<RunId>, bool), ApplicationError> {
        port.transact(async |transaction| {
            Ok((
                transaction
                    .active_run_for_visible_agent(agent_id, viewer_id)
                    .await?,
                transaction.pending_item_for_agent(agent_id).await?,
            ))
        })
        .await
    }
}

pub(in crate::server) struct ApplyCommandResult;
impl ApplyCommandResult {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        computer_id: ComputerId,
        command_id: crate::ids::CommandId,
        sequence: u64,
        applied: bool,
    ) -> Result<(), ApplicationError> {
        port.transact(async |transaction| {
            let Some(agent_id) = transaction
                .agent_provision_command_target(computer_id, command_id, sequence)
                .await?
            else {
                return Ok(());
            };
            let mut agent = transaction.agent(agent_id).await?;
            if agent.apply_provision_result(computer_id, applied) {
                transaction.save_agent(agent).await?;
            }
            Ok(())
        })
        .await
    }
}

pub(in crate::server) struct ClaimRunInput {
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) agent_id: MemberId,
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) task_id: Option<TaskId>,
    pub(in crate::server) focus_thread_id: ThreadId,
    pub(in crate::server) item_ids: Vec<InboxItemId>,
    pub(in crate::server) fencing_token: RawFencingToken,
    pub(in crate::server) lease_expires_at: OffsetDateTime,
}

pub(in crate::server) struct ClaimRun;

impl ClaimRun {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: ClaimRunInput,
    ) -> Result<Run, ApplicationError> {
        port.transact(async |transaction| {
            match transaction.run(input.run_id).await {
                Ok(run) => {
                    let run_view = run.view();
                    if !transaction
                        .can_operate_agent(input.computer_id, run_view.agent_id)
                        .await?
                    {
                        return Err(ApplicationError::PermissionDenied);
                    }
                    if run_view.agent_id == input.agent_id
                        && run_view.focus_thread_id == input.focus_thread_id
                        && run_view.task_id == input.task_id
                    {
                        return Ok(run);
                    }
                    return Err(ApplicationError::Conflict);
                }
                Err(ApplicationError::NotFound) => {}
                Err(error) => return Err(error),
            }
            if !transaction
                .can_operate_agent(input.computer_id, input.agent_id)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            let agent = transaction.agent(input.agent_id).await?;
            if transaction
                .active_run_for_agent(input.agent_id)
                .await?
                .is_some()
            {
                return Err(ApplicationError::Conflict);
            }
            if !transaction
                .can_read_thread(input.agent_id, input.focus_thread_id)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            if let Some(task_id) = input.task_id {
                let task = transaction.task(task_id).await?;
                if !task.linked_to(input.focus_thread_id) {
                    return Err(crate::server::domain::DomainError::FocusOutsideTask.into());
                }
            }
            let mut run = Run::create(
                input.run_id,
                agent.space_id,
                input.agent_id,
                input.task_id,
                input.focus_thread_id,
                input.fencing_token.sha256_hash(),
                input.lease_expires_at,
            );
            let mut leased_items = Vec::with_capacity(input.item_ids.len());
            for (index, item_id) in input.item_ids.into_iter().enumerate() {
                let mut item = transaction.inbox_item(item_id).await?;
                let item_view = item.view();
                let run_view = run.view();
                if item_view.member_id != run_view.agent_id
                    || item_view.thread_id != run_view.focus_thread_id
                    || item_view.task_id != run_view.task_id
                {
                    return Err(crate::server::domain::DomainError::ItemScopeMismatch.into());
                }
                item.lease_for_run(run_view.id, run_view.lease_expires_at)?;
                run.add_claimed_item(item_view.id, index as u64 + 1)?;
                leased_items.push(item);
            }
            transaction.save_run(run.clone()).await?;
            for item in leased_items {
                let source_message_id = item.view().message_id;
                transaction.save_inbox_item(item).await?;
                if let Some(message_id) = source_message_id {
                    transaction.emit(Effect::MessageUpdated(message_id));
                }
            }
            transaction.emit(Effect::RunClaimed {
                run_id: run.view().id,
                fencing_token: input.fencing_token,
            });
            Ok(run)
        })
        .await
    }
}

pub(in crate::server) struct ItemDispositionInput {
    pub(in crate::server) item_id: InboxItemId,
    pub(in crate::server) disposition: InboxItemDisposition,
}

pub(in crate::server) struct StartRunInput {
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) fencing_token_hash: String,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct StartRun;

impl StartRun {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: StartRunInput,
    ) -> Result<Run, ApplicationError> {
        port.transact(async |transaction| {
            let mut run = transaction.run(input.run_id).await?;
            let run_view = run.view();
            if !transaction
                .can_operate_agent(input.computer_id, run_view.agent_id)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            if run_view.status == RunStatus::Running {
                run.validate_fencing(&input.fencing_token_hash)?;
                return Ok(run);
            }
            run.start(&input.fencing_token_hash, input.now)?;
            transaction.save_run(run.clone()).await?;
            transaction.emit(Effect::RunStarted(run.view().id));
            // The assignee's first Task Run reaching `running` is what makes the Task in progress;
            // a Run by any other Agent leaves the Task status alone.
            let run_view = run.view();
            if let Some(task_id) = run_view.task_id {
                let mut task = transaction.task(task_id).await?;
                let task_view = task.view();
                if task_view.status == TaskStatus::Todo
                    && task_view.assignee_agent_member_id == Some(run_view.agent_id)
                {
                    task.start(run_view.agent_id, input.now)?;
                    transaction.save_task(task.clone()).await?;
                    transaction.emit(Effect::TaskUpdated(task.view().id));
                }
            }
            Ok(run)
        })
        .await
    }
}

pub(in crate::server) struct CompleteRunInput {
    pub(in crate::server) event_id: EventId,
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) fencing_token_hash: String,
    pub(in crate::server) outcome: RunOutcome,
    pub(in crate::server) error_code: Option<RunErrorCode>,
    pub(in crate::server) item_dispositions: Vec<ItemDispositionInput>,
    pub(in crate::server) continuation_note: Option<String>,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct RenewRunInput {
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) fencing_token_hash: String,
    pub(in crate::server) lease_expires_at: OffsetDateTime,
}

pub(in crate::server) struct RecordRunItemDispositionInput {
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) fencing_token_hash: String,
    pub(in crate::server) item_id: InboxItemId,
    pub(in crate::server) disposition: InboxItemDisposition,
    pub(in crate::server) defer_until: Option<OffsetDateTime>,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct RecordRunItemDisposition;

impl RecordRunItemDisposition {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: RecordRunItemDispositionInput,
    ) -> Result<Run, ApplicationError> {
        port.transact(async |transaction| {
            let mut run = transaction.run(input.run_id).await?;
            let run_view = run.view();
            let run_id = run_view.id;
            if !transaction
                .can_operate_agent(input.computer_id, run_view.agent_id)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            run.validate_fencing(&input.fencing_token_hash)?;
            if run_view.status != RunStatus::Running {
                return Err(ApplicationError::ContextChanged);
            }
            run.set_item_disposition(input.item_id, input.disposition)?;
            if let Some(until) = input.defer_until {
                let mut item = transaction.inbox_item(input.item_id).await?;
                item.prepare_defer(run_id, until, input.now)?;
                transaction.save_inbox_item(item).await?;
            }
            transaction.save_run(run.clone()).await?;
            Ok(run)
        })
        .await
    }
}

pub(in crate::server) struct AcknowledgeDeliveryInput {
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) fencing_token_hash: String,
    pub(in crate::server) delivery_sequence: u64,
    pub(in crate::server) accepted: bool,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct AcknowledgeDelivery;

impl AcknowledgeDelivery {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: AcknowledgeDeliveryInput,
    ) -> Result<Run, ApplicationError> {
        port.transact(async |transaction| {
            let mut run = transaction.run(input.run_id).await?;
            let run_view = run.view();
            let run_id = run_view.id;
            if !transaction
                .can_operate_agent(input.computer_id, run_view.agent_id)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            run.validate_fencing(&input.fencing_token_hash)?;
            let item = run
                .item_for_delivery(input.delivery_sequence)
                .ok_or(ApplicationError::NotFound)?;
            if input.accepted || item.disposition == Some(InboxItemDisposition::Released) {
                return Ok(run);
            }
            run.set_item_disposition(item.inbox_item_id, InboxItemDisposition::Released)?;
            let mut inbox_item = transaction.inbox_item(item.inbox_item_id).await?;
            inbox_item.apply_disposition(run_id, InboxItemDisposition::Released, input.now)?;
            transaction.save_inbox_item(inbox_item).await?;
            transaction.save_run(run.clone()).await?;
            Ok(run)
        })
        .await
    }
}

pub(in crate::server) struct RenewRun;

impl RenewRun {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: RenewRunInput,
    ) -> Result<Run, ApplicationError> {
        port.transact(async |transaction| {
            let mut run = transaction.run(input.run_id).await?;
            let run_view = run.view();
            let run_id = run_view.id;
            if !transaction
                .can_operate_agent(input.computer_id, run_view.agent_id)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            run.renew_lease(&input.fencing_token_hash, input.lease_expires_at)?;
            for run_item in run.items() {
                let mut item = transaction.inbox_item(run_item.inbox_item_id).await?;
                item.renew_lease(run_id, input.lease_expires_at)?;
                transaction.save_inbox_item(item).await?;
            }
            transaction.save_run(run.clone()).await?;
            Ok(run)
        })
        .await
    }
}

pub(in crate::server) struct ReclaimExpiredLeasesInput {
    pub(in crate::server) now: OffsetDateTime,
    pub(in crate::server) limit: u32,
    pub(in crate::server) max_retry_count: u32,
}

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub(in crate::server) struct ReclaimedLeases {
    pub(in crate::server) runs_failed: u32,
    pub(in crate::server) items_released: u32,
    pub(in crate::server) items_dead: u32,
}

/// Recovers Runs abandoned by a Computer that stopped renewing its lease. Without this, one offline
/// Computer leaves a non-terminal Run that the partial unique index counts as active, which blocks
/// every later Run for that Agent.
pub(in crate::server) struct ReclaimExpiredLeases;

impl ReclaimExpiredLeases {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: ReclaimExpiredLeasesInput,
    ) -> Result<ReclaimedLeases, ApplicationError> {
        let expired = port
            .transact(async |transaction| {
                transaction
                    .runs_with_expired_lease(input.now, input.limit)
                    .await
            })
            .await?;
        let mut reclaimed = ReclaimedLeases::default();
        for run_id in expired {
            // One transaction per Run: a Run that cannot be recovered must not block the others.
            let outcome = port
                .transact(async |transaction| {
                    let mut run = transaction.run(run_id).await?;
                    let run_view = run.view();
                    let run_id = run_view.id;
                    if run.is_terminal() || run_view.lease_expires_at > input.now {
                        return Ok(None);
                    }
                    let mut released = 0;
                    let mut dead = 0;
                    for run_item in run.items() {
                        let mut item = transaction.inbox_item(run_item.inbox_item_id).await?;
                        let item_view = item.view();
                        if item_view.status != InboxItemStatus::Leased
                            || item_view.lease_run_id != Some(run_id)
                        {
                            continue;
                        }
                        let retired = matches!(
                            item.reclaim_expired_lease(run_id, input.max_retry_count, input.now)?,
                            InboxItemStatus::Dead
                        );
                        let item_view = item.view();
                        let agent_id = item_view.member_id;
                        let thread_id = item_view.thread_id;
                        transaction.save_inbox_item(item).await?;
                        if retired {
                            dead += 1;
                            transaction
                                .insert_dead_item_notice(
                                    agent_id,
                                    thread_id,
                                    "inbox_item_dead",
                                    input.now,
                                )
                                .await?;
                        } else {
                            released += 1;
                        }
                        transaction.emit(Effect::InboxChanged(agent_id));
                    }
                    run.fail_expired_lease(input.now)?;
                    transaction.save_run(run.clone()).await?;
                    transaction.emit(Effect::RunCompleted(run_id));
                    Ok(Some((released, dead)))
                })
                .await?;
            if let Some((released, dead)) = outcome {
                reclaimed.runs_failed += 1;
                reclaimed.items_released += released;
                reclaimed.items_dead += dead;
            }
        }
        Ok(reclaimed)
    }
}

pub(in crate::server) struct CompleteRun;

impl CompleteRun {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: CompleteRunInput,
    ) -> Result<Run, ApplicationError> {
        port.transact(async |transaction| {
            let mut run = transaction.run(input.run_id).await?;
            let run_view = run.view();
            let run_id = run_view.id;
            if !transaction
                .can_operate_agent(input.computer_id, run_view.agent_id)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            run.validate_fencing(&input.fencing_token_hash)?;
            if let Some(run_id) = transaction.completed_run_for_event(input.event_id).await? {
                if run_id != run_view.id {
                    return Err(ApplicationError::Conflict);
                }
                return Ok(run);
            }
            run.begin_finalizing(&input.fencing_token_hash)?;
            for item_input in input.item_dispositions {
                run.set_item_disposition(item_input.item_id, item_input.disposition)?;
                let mut item = transaction.inbox_item(item_input.item_id).await?;
                if item.view().status == InboxItemStatus::Leased {
                    item.apply_disposition(run_id, item_input.disposition, input.now)?;
                    transaction.save_inbox_item(item).await?;
                } else if item_input.disposition != InboxItemDisposition::Released {
                    return Err(ApplicationError::Conflict);
                }
            }
            run.finish(
                &input.fencing_token_hash,
                input.outcome,
                input.error_code,
                input.continuation_note,
                input.now,
            )?;
            transaction.save_run(run.clone()).await?;
            transaction
                .record_completed_run_event(input.event_id, run_id)
                .await?;
            transaction.emit(Effect::RunCompleted(run_id));
            Ok(run)
        })
        .await
    }
}
