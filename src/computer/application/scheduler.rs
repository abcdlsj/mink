use crate::computer::core::{
    home::LocalAgentState,
    scheduler::{PendingRun, Scheduler},
    supervisor::LocalRunState,
};

use super::{
    ApplicationError,
    ports::{AgentHomePort, ComputerTransaction, DriverPort, TransactionPort},
    run::RunService,
};

pub(in crate::computer) struct SchedulerService;

impl SchedulerService {
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
        let runs = store
            .transact(async |transaction| transaction.nonterminal_runs())
            .await?;
        let mut scheduler = Scheduler::new(capacity);
        for run in runs {
            if run.view().state == LocalRunState::Queued {
                let ready = match homes.agent(run.view().agent_id).await {
                    Ok(agent) => agent.state == LocalAgentState::Active,
                    Err(ApplicationError::NotFound | ApplicationError::DriverUnavailable) => false,
                    Err(error) => return Err(error),
                };
                if !ready {
                    continue;
                }
                scheduler.enqueue(PendingRun {
                    run_id: run.view().id,
                    agent_id: run.view().agent_id,
                    explicit_human_redirect: run.view().priority.explicit_human_redirect,
                    strength: run.view().priority.strength,
                    available_at: run.view().priority.available_at,
                    has_task_continuity: run.view().priority.has_task_continuity,
                });
            } else if matches!(
                run.view().state,
                LocalRunState::Starting | LocalRunState::Running | LocalRunState::Stopping
            ) {
                scheduler.occupy(run.view().agent_id, run.view().id);
            }
        }
        while let Some(pending) = scheduler.next() {
            let fingerprint = match store
                .transact(async |transaction| {
                    let run = transaction
                        .run(pending.run_id)?
                        .ok_or(ApplicationError::NotFound)?;
                    run.view()
                        .session_fingerprint
                        .cloned()
                        .ok_or(ApplicationError::Conflict)
                })
                .await
            {
                Ok(fingerprint) => fingerprint,
                Err(ApplicationError::NotFound) => continue,
                Err(ApplicationError::Conflict) => {
                    tracing::warn!(
                        run_id = %pending.run_id,
                        "queued local Run has no usable session fingerprint"
                    );
                    RunService::stop(store, driver, pending.run_id).await?;
                    continue;
                }
                Err(error) => return Err(error),
            };
            match RunService::start(store, driver, pending.run_id, fingerprint).await {
                Ok(()) => {}
                Err(ApplicationError::Conflict) => {
                    tracing::warn!(
                        run_id = %pending.run_id,
                        "queued local Run conflicted while starting"
                    );
                    RunService::stop(store, driver, pending.run_id).await?;
                }
                Err(ApplicationError::NotFound) => {}
                Err(error) => return Err(error),
            }
        }
        Ok(())
    }
}
