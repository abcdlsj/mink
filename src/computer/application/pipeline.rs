use super::{
    ApplicationError,
    ports::{AgentHomePort, DriverCompletion, DriverPort, TransactionPort},
    recovery::RecoveryService,
    run::RunService,
    scheduler::SchedulerService,
};

/// Owns the local Run lifecycle after an external observation changes capacity.
///
/// Run state transitions and Driver calls remain separate operations, but callers no longer
/// decide independently whether a released slot must be dispatched. The pending event outbox is
/// still read and sent by the connection adapter after this pipeline returns.
pub(in crate::computer) struct RunPipelineService;

impl RunPipelineService {
    pub(in crate::computer) async fn finish_driver_turns<
        P: TransactionPort,
        D: DriverPort,
        H: AgentHomePort,
    >(
        store: &mut P,
        driver: &mut D,
        completions: impl IntoIterator<Item = DriverCompletion>,
        homes: &mut H,
        capacity: usize,
    ) -> Result<bool, ApplicationError> {
        let mut changed = false;
        for completion in completions {
            changed |= RunService::finish_driver_turn(store, completion.run_id, completion.outcome)
                .await?
                .is_some();
        }
        if changed {
            Self::dispatch(store, driver, homes, capacity).await?;
        }
        Ok(changed)
    }

    pub(in crate::computer) async fn fail_lost_drivers<
        P: TransactionPort,
        D: DriverPort,
        H: AgentHomePort,
    >(
        store: &mut P,
        driver: &mut D,
        homes: &mut H,
        capacity: usize,
    ) -> Result<bool, ApplicationError> {
        let changed = RecoveryService::fail_lost_drivers(store, driver).await?;
        if changed {
            Self::dispatch(store, driver, homes, capacity).await?;
        }
        Ok(changed)
    }

    pub(in crate::computer) async fn interrupt_yielded<
        P: TransactionPort,
        D: DriverPort,
        H: AgentHomePort,
    >(
        store: &mut P,
        driver: &mut D,
        run_id: crate::ids::RunId,
        homes: &mut H,
        capacity: usize,
    ) -> Result<(), ApplicationError> {
        if let Err(error) = RunService::interrupt_terminal(store, driver, run_id).await {
            tracing::warn!(%run_id, %error, "yielded Driver interrupt failed");
        }
        Self::dispatch(store, driver, homes, capacity).await
    }

    pub(in crate::computer) async fn dispatch<
        P: TransactionPort,
        D: DriverPort,
        H: AgentHomePort,
    >(
        store: &mut P,
        driver: &mut D,
        homes: &mut H,
        capacity: usize,
    ) -> Result<(), ApplicationError> {
        SchedulerService::dispatch(store, driver, homes, capacity).await
    }

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
        RecoveryService::recover(store, driver, homes, capacity).await
    }
}
