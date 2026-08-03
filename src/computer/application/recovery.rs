use std::collections::BTreeSet;

use crate::ids::{EventId, RunId};

use crate::computer::core::supervisor::{
    DeliveryState, ItemDisposition, LocalRun, LocalRunState, TerminalStatus,
};

use super::{
    ApplicationError,
    command::CommandService,
    ports::{
        AgentHomePort, ComputerTransaction, DriverPort, LocalErrorCode, LocalEvent,
        ProcessEvidence, TransactionPort,
    },
    run::RunService,
    scheduler::SchedulerService,
};

pub(in crate::computer) struct RecoveryService;

impl RecoveryService {
    /// Reconciles local state with the Driver processes that actually exist.
    ///
    /// A Run fails here only when this Computer can see its process is gone, never because time
    /// passed: there is no lease to expire. A daemon restart loses every Driver child process, so Runs
    /// that were mid-turn fail with `ComputerRestarted` and release their Items.
    pub(in crate::computer) async fn recover<
        P: TransactionPort,
        D: DriverPort,
        H: AgentHomePort,
    >(
        store: &mut P,
        driver: &mut D,
        homes: &mut H,
        capacity: usize,
    ) -> Result<(), ApplicationError> {
        let pending_commands = store
            .transact(async |transaction| transaction.pending_commands())
            .await?;
        for stored in pending_commands {
            match CommandService::execute(
                store,
                driver,
                homes,
                stored.id,
                stored.sequence,
                stored.command,
            )
            .await
            {
                Ok(_) | Err(ApplicationError::DriverUnavailable) => {}
                Err(error) => return Err(error),
            }
        }
        SchedulerService::dispatch(store, driver, capacity).await?;
        let runs = store
            .transact(async |transaction| transaction.nonterminal_runs())
            .await?;
        for run in runs {
            match run.view().state {
                LocalRunState::Starting | LocalRunState::Running => {
                    if driver.process_evidence(&run).await? == ProcessEvidence::Controlled {
                        if run.view().state == LocalRunState::Running {
                            Self::redeliver_pending_items(store, driver, &run).await?;
                        }
                        continue;
                    }
                    Self::fail_lost_run(store, &run, LocalErrorCode::ComputerRestarted).await?;
                }
                LocalRunState::Finalizing => {
                    // The result outbox retries until the Server acknowledges, so a queued result
                    // needs nothing here. Without one, the turn ended but the result was never
                    // recorded, which only an internal fault explains.
                    if !Self::result_queued(store, run.view().id).await? {
                        Self::fail_lost_run(store, &run, LocalErrorCode::Internal).await?;
                    }
                }
                LocalRunState::Stopping => {
                    let _ = driver.interrupt(&run).await;
                    RunService::finish(
                        store,
                        run.view().id,
                        TerminalStatus::Canceled,
                        Self::released_items(&run),
                        None,
                        None,
                    )
                    .await?;
                }
                LocalRunState::Queued => {}
                LocalRunState::Completed
                | LocalRunState::Yielded
                | LocalRunState::Failed
                | LocalRunState::Canceled => {}
            }
        }
        Ok(())
    }

    /// Fails Runs whose Driver process is gone while this daemon is still running.
    ///
    /// A dead process produces no turn completion, so without this check the Run would stay
    /// mid-flight forever: nothing else on either side judges a Run by elapsed time. The evidence is
    /// a terminated process handle, which only this Computer can observe.
    pub(in crate::computer) async fn fail_lost_drivers<P: TransactionPort, D: DriverPort>(
        store: &mut P,
        driver: &mut D,
    ) -> Result<bool, ApplicationError> {
        let runs = store
            .transact(async |transaction| transaction.nonterminal_runs())
            .await?;
        let mut failed = false;
        for run in runs {
            if !matches!(run.view().state, LocalRunState::Running) {
                continue;
            }
            if driver.process_evidence(&run).await? == ProcessEvidence::Controlled {
                continue;
            }
            Self::fail_lost_run(store, &run, LocalErrorCode::DriverLost).await?;
            failed = true;
        }
        Ok(failed)
    }

    async fn fail_lost_run<P: TransactionPort>(
        store: &mut P,
        run: &LocalRun,
        error_code: LocalErrorCode,
    ) -> Result<(), ApplicationError> {
        RunService::finish(
            store,
            run.view().id,
            TerminalStatus::Failed,
            Self::released_items(run),
            None,
            Some(error_code),
        )
        .await
        .map(|_| ())
    }

    fn released_items(run: &LocalRun) -> Vec<(crate::ids::InboxItemId, ItemDisposition)> {
        run.view()
            .deliveries
            .values()
            .map(|delivery| (delivery.item.item_id, ItemDisposition::Released))
            .collect()
    }

    async fn result_queued<P: TransactionPort>(
        store: &mut P,
        run_id: RunId,
    ) -> Result<bool, ApplicationError> {
        store
            .transact(async |transaction| {
                Ok(transaction.pending_events()?.into_iter().any(|event| {
                    matches!(event, LocalEvent::RunResult { run_id: queued, .. } if queued == run_id)
                }))
            })
            .await
    }

    async fn redeliver_pending_items<P: TransactionPort, D: DriverPort>(
        store: &mut P,
        driver: &mut D,
        run: &LocalRun,
    ) -> Result<(), ApplicationError> {
        let pending = run
            .view()
            .deliveries
            .values()
            .filter(|delivery| delivery.state == DeliveryState::Pending)
            .map(|delivery| (delivery.sequence, delivery.item.clone()))
            .collect::<Vec<_>>();
        for (sequence, item) in pending {
            RunService::attach(store, driver, run.view().id, sequence, item).await?;
        }
        Ok(())
    }

    pub(in crate::computer) async fn pending_results<P: TransactionPort>(
        store: &mut P,
    ) -> Result<Vec<LocalEvent>, ApplicationError> {
        store
            .transact(async |transaction| transaction.pending_events())
            .await
    }

    /// Runs this daemon still holds. Sent on connect so the Server can fail the Runs it believes are
    /// live here but are not, without consulting any timer.
    pub(in crate::computer) async fn live_run_ids<P: TransactionPort>(
        store: &mut P,
    ) -> Result<Vec<RunId>, ApplicationError> {
        store
            .transact(async |transaction| {
                Ok(transaction
                    .nonterminal_runs()?
                    .into_iter()
                    .map(|run| run.view().id)
                    .collect())
            })
            .await
    }

    /// Runs that must be protected during handshake reconciliation. This includes non-terminal Runs
    /// and terminal Runs whose result is still in the local outbox; the latter must reach the Server
    /// before the Server can decide whether a non-terminal Run was lost.
    pub(in crate::computer) async fn reconnect_run_ids<P: TransactionPort>(
        store: &mut P,
    ) -> Result<Vec<RunId>, ApplicationError> {
        store
            .transact(async |transaction| {
                let mut run_ids = BTreeSet::new();
                for run in transaction.nonterminal_runs()? {
                    run_ids.insert(run.view().id);
                }
                for event in transaction.pending_events()? {
                    if let LocalEvent::RunResult { run_id, .. } = event {
                        run_ids.insert(run_id);
                    }
                }
                Ok(run_ids.into_iter().collect())
            })
            .await
    }

    pub(in crate::computer) async fn acknowledge<P: TransactionPort>(
        store: &mut P,
        event_id: EventId,
    ) -> Result<(), ApplicationError> {
        store
            .transact(async |transaction| transaction.acknowledge_event(event_id))
            .await
    }
}
