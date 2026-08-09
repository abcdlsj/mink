use time::OffsetDateTime;

use crate::ids::{
    ChannelId, CommandId, ComputerId, EventId, InboxItemId, MemberId, MessageId, RunId, TaskId,
    ThreadId,
};

use crate::server::domain::{
    DomainError,
    attention::{InboxItemDisposition, InboxItemStatus},
    execution::{DeliveryOutcome, Run, RunErrorCode, RunOutcome, RunStatus, RunTrigger},
    task::TaskStatus,
};

use super::ports::{
    ApplicationError, CollaborationTransaction, Effect, EffectSink, ExecutionTransaction,
    IdentityTransaction, RunCapabilityProof, TaskTransaction, TransactionPort,
};

/// Finds Agents whose Inbox holds work and who have no live Run, so the Server can dispatch to them.
///
/// A tick rather than a pure event reaction, because availability is a future fact: an ambient
/// aggregate becomes available after its debounce, and a deferred Item after the time the Agent
/// asked for. The tick only starts work; it never judges a Run, so a slow tick delays a Run and
/// cannot fail one.
pub(in crate::server) struct FindDispatchableWork;

impl FindDispatchableWork {
    pub(in crate::server) async fn candidates<P: TransactionPort>(
        port: &mut P,
        now: OffsetDateTime,
        limit: u32,
    ) -> Result<Vec<super::ports::DispatchCandidate>, ApplicationError> {
        port.transact(async |transaction| transaction.dispatchable_work(now, limit).await)
            .await
    }

    /// The dispatch transaction has already rolled back when this method is called. Keeping this
    /// write in a second transaction preserves the diagnostic Item state and outbox event.
    pub(in crate::server) async fn record_failure<P: TransactionPort>(
        port: &mut P,
        item_id: InboxItemId,
        message_id: Option<MessageId>,
        channel_id: ChannelId,
        error_code: &str,
    ) -> Result<bool, ApplicationError> {
        port.transact(async |transaction| {
            transaction
                .record_dispatch_failure(item_id, message_id, channel_id, error_code)
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
        channel_id: ChannelId,
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
        channel_id: ChannelId,
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
        channel_id: ChannelId,
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
        command_id: CommandId,
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

pub(in crate::server) struct DispatchRunInput {
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) agent_id: MemberId,
    pub(in crate::server) task_id: Option<TaskId>,
    pub(in crate::server) focus_thread_id: ThreadId,
    pub(in crate::server) trigger: RunTrigger,
    pub(in crate::server) item_ids: Vec<InboxItemId>,
}

/// Creates a Run and queues its start command for the Computer hosting the Agent. The Server chooses
/// the host from the Agent's hosting record; no Computer asks for work.
pub(in crate::server) struct DispatchRun;

impl DispatchRun {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: DispatchRunInput,
    ) -> Result<Run, ApplicationError> {
        port.transact(async |transaction| {
            match transaction.run(input.run_id).await {
                Ok(run) => {
                    let run_view = run.view();
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
                    return Err(DomainError::FocusOutsideTask.into());
                }
            }
            let mut run = Run::create(
                input.run_id,
                agent.space_id,
                input.agent_id,
                input.task_id,
                input.focus_thread_id,
                input.trigger,
            );
            let mut assigned_items = Vec::with_capacity(input.item_ids.len());
            for (index, item_id) in input.item_ids.into_iter().enumerate() {
                let mut item = transaction.inbox_item(item_id).await?;
                let item_view = item.view();
                let run_view = run.view();
                if item_view.member_id != run_view.agent_id
                    || item_view.thread_id != run_view.focus_thread_id
                    || item_view.task_id != run_view.task_id
                {
                    return Err(DomainError::ItemScopeMismatch.into());
                }
                item.assign_to_run(run_view.id)?;
                run.add_dispatched_item(item_view.id, index as u64 + 1)?;
                assigned_items.push(item);
            }
            transaction.save_run(run.clone()).await?;
            for item in assigned_items {
                let source_message_id = item.view().message_id;
                transaction.save_inbox_item(item).await?;
                if let Some(message_id) = source_message_id {
                    transaction.emit(Effect::MessageUpdated(message_id));
                }
            }
            transaction.emit(Effect::RunDispatched(run.view().id));
            let run_view = run.view();
            tracing::info!(
                run_id = %run_view.id,
                agent_id = %run_view.agent_id,
                task_id = ?run_view.task_id,
                trigger = ?run_view.trigger,
                "Run dispatched to Computer"
            );
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
            if run_view.status == RunStatus::Working || run.is_terminal() {
                return Ok(run);
            }
            run.start(input.now)?;
            let run_view = run.view();
            tracing::info!(
                run_id = %run_view.id,
                computer_id = %input.computer_id.into_uuid(),
                agent_id = %run_view.agent_id,
                task_id = ?run_view.task_id,
                "Run started on Computer"
            );
            transaction.save_run(run.clone()).await?;
            transaction.emit(Effect::RunStarted(run.view().id));
            // The assignee's first Task Run reaching `working` is what makes the Task in progress;
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
    pub(in crate::server) outcome: RunOutcome,
    pub(in crate::server) error_code: Option<RunErrorCode>,
    pub(in crate::server) item_dispositions: Vec<ItemDispositionInput>,
    pub(in crate::server) continuation_note: Option<String>,
    pub(in crate::server) max_retry_count: u32,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct RecordRunItemDispositionInput {
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) computer_id: ComputerId,
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
            if run_view.status != RunStatus::Working {
                return Err(ApplicationError::ContextChanged);
            }
            run.set_item_disposition_at(input.item_id, input.disposition, input.now)?;
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
    pub(in crate::server) event_id: EventId,
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) delivery_sequence: u64,
    pub(in crate::server) outcome: DeliveryOutcome,
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
            let item = run
                .item_for_delivery(input.delivery_sequence)
                .ok_or(ApplicationError::NotFound)?;
            let recorded = run.record_delivery_receipt(
                input.delivery_sequence,
                input.event_id,
                input.outcome,
                input.now,
            )?;
            if !recorded {
                return Ok(run);
            }
            if input.outcome != DeliveryOutcome::Accepted && item.disposition.is_none() {
                run.set_item_disposition_at(
                    item.inbox_item_id,
                    InboxItemDisposition::Released,
                    input.now,
                )?;
                let mut inbox_item = transaction.inbox_item(item.inbox_item_id).await?;
                inbox_item.apply_disposition(run_id, InboxItemDisposition::Released, input.now)?;
                transaction.save_inbox_item(inbox_item).await?;
            }
            transaction.save_run(run.clone()).await?;
            Ok(run)
        })
        .await
    }
}

pub(in crate::server) struct RequestRunCancelInput {
    pub(in crate::server) run_id: RunId,
    pub(in crate::server) requested_by: MemberId,
    pub(in crate::server) now: OffsetDateTime,
}

/// Records a Human's stop request and queues the stop command. The Run stays live until the Computer
/// reports it stopped, because only the Computer knows when the Driver actually ended.
pub(in crate::server) struct RequestRunCancel;

impl RequestRunCancel {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: RequestRunCancelInput,
    ) -> Result<Run, ApplicationError> {
        port.transact(async |transaction| {
            let mut run = transaction.run(input.run_id).await?;
            let run_view = run.view();
            if !transaction
                .can_read_thread(input.requested_by, run_view.focus_thread_id)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            if run.is_terminal() {
                return Ok(run);
            }
            run.request_cancel()?;
            transaction.save_run(run.clone()).await?;
            transaction.emit(Effect::RunCancelRequested(run.view().id));
            let _ = input.now;
            Ok(run)
        })
        .await
    }
}

pub(in crate::server) struct SyncComputerRunsInput {
    pub(in crate::server) computer_id: ComputerId,
    /// Runs the Computer still has locally. Every other non-terminal Run on this Computer is gone
    /// with the previous daemon process.
    pub(in crate::server) live_run_ids: Vec<RunId>,
    pub(in crate::server) max_retry_count: u32,
    pub(in crate::server) now: OffsetDateTime,
}

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub(in crate::server) struct SyncedComputerRuns {
    pub(in crate::server) runs_failed: u32,
    pub(in crate::server) items_released: u32,
    pub(in crate::server) items_dead: u32,
}

/// Reconciles the Server's view with what a reconnecting Computer actually holds.
///
/// This replaces timer-based reclamation. A Run is failed only because the Computer that owned it says
/// it is gone, never because time passed: an offline Computer leaves its Runs untouched until it comes
/// back and reports.
pub(in crate::server) struct SyncComputerRuns;

impl SyncComputerRuns {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: SyncComputerRunsInput,
    ) -> Result<SyncedComputerRuns, ApplicationError> {
        let orphaned = port
            .transact(async |transaction| {
                transaction
                    .nonterminal_runs_for_computer(input.computer_id)
                    .await
            })
            .await?
            .into_iter()
            .filter(|run_id| !input.live_run_ids.contains(run_id))
            .collect::<Vec<_>>();
        let mut synced = SyncedComputerRuns::default();
        for run_id in orphaned {
            // One transaction per Run: a Run that cannot be reconciled must not block the others.
            let outcome = port
                .transact(async |transaction| {
                    let mut run = transaction.run(run_id).await?;
                    if run.is_terminal() {
                        return Ok(None);
                    }
                    let mut released = 0;
                    let mut dead = 0;
                    // An explicit Agent decision is Handled or Deferred. A missing or `Released`
                    // disposition is the Computer's automatic settlement for an unresolved Item,
                    // so it spends a failed-run retry.
                    for run_item in run.items().collect::<Vec<_>>() {
                        if matches!(
                            run_item.disposition,
                            None | Some(InboxItemDisposition::Released)
                        ) {
                            run.set_item_disposition_at(
                                run_item.inbox_item_id,
                                InboxItemDisposition::Released,
                                input.now,
                            )?;
                            let mut item = transaction.inbox_item(run_item.inbox_item_id).await?;
                            let item_view = item.view();
                            if item_view.status != InboxItemStatus::Assigned
                                || item_view.assigned_run_id != Some(run_id)
                            {
                                continue;
                            }
                            let retired = matches!(
                                item.release_on_failed_run(
                                    run_id,
                                    input.max_retry_count,
                                    input.now,
                                )?,
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
                            continue;
                        }
                        let disposition = run_item
                            .disposition
                            .expect("non-released disposition was checked above");
                        let mut item = transaction.inbox_item(run_item.inbox_item_id).await?;
                        let item_view = item.view();
                        if item_view.status == InboxItemStatus::Assigned
                            && item_view.assigned_run_id == Some(run_id)
                        {
                            let agent_id = item_view.member_id;
                            item.apply_disposition(run_id, disposition, input.now)?;
                            transaction.save_inbox_item(item).await?;
                            transaction.emit(Effect::InboxChanged(agent_id));
                        }
                    }
                    run.finish(
                        RunOutcome::Failed,
                        Some(RunErrorCode::ComputerRestarted),
                        None,
                        input.now,
                    )?;
                    transaction.save_run(run.clone()).await?;
                    transaction
                        .settle_run_commands(run_id, input.computer_id)
                        .await?;
                    transaction.emit(Effect::RunCompleted(run_id));
                    Ok(Some((released, dead)))
                })
                .await?;
            if let Some((released, dead)) = outcome {
                synced.runs_failed += 1;
                synced.items_released += released;
                synced.items_dead += dead;
                tracing::warn!(
                    %run_id,
                    computer_id = %input.computer_id.into_uuid(),
                    error_code = "computer_restarted",
                    items_released = released,
                    items_dead = dead,
                    "Run failed because the Computer restarted without it"
                );
            }
        }
        Ok(synced)
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
            if let Some(run_id) = transaction.completed_run_for_event(input.event_id).await? {
                if run_id != run_view.id {
                    return Err(ApplicationError::Conflict);
                }
                return Ok(run);
            }
            // A reconnect can reconcile a Run before its terminal result arrives. The Computer is
            // authenticated above, so a later result for that Run is an idempotent acknowledgement.
            if run.is_terminal() {
                return Ok(run);
            }
            let failed = input.outcome == RunOutcome::Failed;
            for item_input in input.item_dispositions {
                // On a failed Run a `Released` disposition is the Computer's automatic settlement
                // for an Item the Agent never resolved, not an Agent decision. It is counted as a
                // failed attempt below, so it must not be applied as an explicit release here.
                if failed && item_input.disposition == InboxItemDisposition::Released {
                    continue;
                }
                // A terminal report's default `Released` never overrides an explicit disposition the
                // Server already recorded from the same Run (message send, ack, defer). The Computer
                // may have failed to mirror that record locally, so keeping the explicit disposition
                // keeps the terminal result acceptable instead of rejecting it without a receipt.
                let effective_disposition = match (
                    run.items()
                        .find(|run_item| run_item.inbox_item_id == item_input.item_id)
                        .and_then(|run_item| run_item.disposition),
                    item_input.disposition,
                ) {
                    (Some(existing), InboxItemDisposition::Released)
                        if existing != InboxItemDisposition::Released =>
                    {
                        existing
                    }
                    (_, disposition) => disposition,
                };
                run.set_item_disposition_at(item_input.item_id, effective_disposition, input.now)?;
                let mut item = transaction.inbox_item(item_input.item_id).await?;
                let item_view = item.view();
                if item_view.status == InboxItemStatus::Assigned {
                    item.apply_disposition(run_id, effective_disposition, input.now)?;
                    transaction.save_inbox_item(item).await?;
                } else if !matches!(
                    (item_view.status, effective_disposition),
                    (InboxItemStatus::Handled, InboxItemDisposition::Handled)
                        | (InboxItemStatus::Deferred, InboxItemDisposition::Deferred)
                        | (InboxItemStatus::Pending, InboxItemDisposition::Released)
                ) {
                    return Err(ApplicationError::Conflict);
                }
            }
            // A failed Run leaves Items the Agent never resolved. They return to the queue here,
            // spending one retry, because this report is the only signal that the attempt failed.
            if failed {
                for run_item in run.items().collect::<Vec<_>>() {
                    if matches!(
                        run_item.disposition,
                        Some(InboxItemDisposition::Handled | InboxItemDisposition::Deferred)
                    ) {
                        continue;
                    }
                    let mut item = transaction.inbox_item(run_item.inbox_item_id).await?;
                    let item_view = item.view();
                    if item_view.status != InboxItemStatus::Assigned
                        || item_view.assigned_run_id != Some(run_id)
                    {
                        run.set_item_disposition_at(
                            run_item.inbox_item_id,
                            InboxItemDisposition::Released,
                            input.now,
                        )?;
                        continue;
                    }
                    let retired = matches!(
                        item.release_on_failed_run(run_id, input.max_retry_count, input.now)?,
                        InboxItemStatus::Dead
                    );
                    let item_view = item.view();
                    let agent_id = item_view.member_id;
                    let thread_id = item_view.thread_id;
                    transaction.save_inbox_item(item).await?;
                    if retired {
                        transaction
                            .insert_dead_item_notice(
                                agent_id,
                                thread_id,
                                "inbox_item_dead",
                                input.now,
                            )
                            .await?;
                    }
                    transaction.emit(Effect::InboxChanged(agent_id));
                    run.set_item_disposition_at(
                        run_item.inbox_item_id,
                        InboxItemDisposition::Released,
                        input.now,
                    )?;
                }
            }
            run.finish(
                input.outcome,
                input.error_code,
                input.continuation_note,
                input.now,
            )?;
            transaction.save_run(run.clone()).await?;
            transaction
                .record_completed_run_event(input.event_id, run_id)
                .await?;
            transaction
                .settle_run_commands(run_id, input.computer_id)
                .await?;
            transaction.emit(Effect::RunCompleted(run_id));
            let run_view = run.view();
            tracing::info!(
                run_id = %run_view.id,
                computer_id = %input.computer_id.into_uuid(),
                agent_id = %run_view.agent_id,
                outcome = ?run_view.outcome,
                error_code = ?run_view.error_code,
                "Run reached a terminal outcome"
            );
            Ok(run)
        })
        .await
    }
}
