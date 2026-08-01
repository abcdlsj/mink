use crate::ids::EventId;
use time::OffsetDateTime;

use crate::computer::core::supervisor::{
    DeliveryState, ItemDisposition, LocalRunState, TerminalStatus,
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
                LocalRunState::Running | LocalRunState::Starting => {
                    if run.view().ownership_lease_expires_at <= OffsetDateTime::now_utc() {
                        let _ = driver.interrupt(&run).await;
                        let outcomes = run
                            .view()
                            .deliveries
                            .values()
                            .map(|delivery| (delivery.item.item_id, ItemDisposition::Released))
                            .collect();
                        if run.view().state == LocalRunState::Starting {
                            let mut expired = run.clone();
                            expired.recover_starting()?;
                            store
                                .transact(async |transaction| transaction.save_run(expired))
                                .await?;
                        }
                        RunService::finish(
                            store,
                            run.view().id,
                            TerminalStatus::Failed,
                            outcomes,
                            None,
                            Some(LocalErrorCode::ProcessLost),
                        )
                        .await?;
                        continue;
                    }
                    if driver.process_evidence(&run).await? == ProcessEvidence::Lost {
                        let outcomes = run
                            .view()
                            .deliveries
                            .values()
                            .map(|delivery| (delivery.item.item_id, ItemDisposition::Released))
                            .collect();
                        if run.view().state == LocalRunState::Starting {
                            let mut failed = run.clone();
                            failed.recover_starting()?;
                            store
                                .transact(async |transaction| transaction.save_run(failed))
                                .await?;
                        }
                        RunService::finish(
                            store,
                            run.view().id,
                            TerminalStatus::Failed,
                            outcomes,
                            None,
                            Some(LocalErrorCode::ProcessLost),
                        )
                        .await?;
                        continue;
                    }
                    if run.view().state == LocalRunState::Running {
                        for delivery in run
                            .view()
                            .deliveries
                            .values()
                            .filter(|delivery| delivery.state == DeliveryState::Pending)
                        {
                            RunService::attach(
                                store,
                                driver,
                                run.view().id,
                                delivery.sequence,
                                delivery.item.clone(),
                            )
                            .await?;
                        }
                    }
                }
                LocalRunState::Finalizing => {
                    let already_queued = store
                        .transact(async |transaction| {
                            Ok(transaction.pending_events()?.into_iter().any(|event| {
                                matches!(event, LocalEvent::RunResult { run_id, .. } if run_id == run.view().id)
                            }))
                        })
                        .await?;
                    if !already_queued {
                        let outcomes = run
                            .view()
                            .deliveries
                            .values()
                            .map(|delivery| (delivery.item.item_id, ItemDisposition::Released))
                            .collect();
                        RunService::finish(
                            store,
                            run.view().id,
                            TerminalStatus::Failed,
                            outcomes,
                            None,
                            Some(LocalErrorCode::Internal),
                        )
                        .await?;
                    }
                }
                LocalRunState::Stopping => {
                    let _ = driver.interrupt(&run).await;
                    let outcomes = run
                        .view()
                        .deliveries
                        .values()
                        .map(|delivery| (delivery.item.item_id, ItemDisposition::Released))
                        .collect();
                    RunService::finish(
                        store,
                        run.view().id,
                        TerminalStatus::Canceled,
                        outcomes,
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

    pub(in crate::computer) async fn pending_results<P: TransactionPort>(
        store: &mut P,
    ) -> Result<Vec<LocalEvent>, ApplicationError> {
        store
            .transact(async |transaction| transaction.pending_events())
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
