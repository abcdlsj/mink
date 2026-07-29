use crate::computer::core::{
    scheduler::{PendingRun, Scheduler},
    supervisor::LocalRunState,
};

use super::{
    ApplicationError,
    ports::{ComputerTransaction, DriverPort, TransactionPort},
    run::RunService,
};

pub(in crate::computer) struct SchedulerService;

impl SchedulerService {
    pub(in crate::computer) async fn dispatch<P: TransactionPort, D: DriverPort>(
        store: &mut P,
        driver: &mut D,
        capacity: usize,
    ) -> Result<(), ApplicationError> {
        let runs = store
            .transact(async |transaction| transaction.nonterminal_runs())
            .await?;
        let mut scheduler = Scheduler::new(capacity);
        for run in runs {
            if run.state == LocalRunState::Queued {
                scheduler.enqueue(PendingRun {
                    run_id: run.id,
                    agent_id: run.agent_id,
                    explicit_human_redirect: run.priority.explicit_human_redirect,
                    strength: run.priority.strength,
                    available_at: run.priority.available_at,
                    has_task_continuity: run.priority.has_task_continuity,
                });
            } else if matches!(
                run.state,
                LocalRunState::Starting | LocalRunState::Running | LocalRunState::Stopping
            ) {
                scheduler.occupy(run.agent_id, run.id);
            }
        }
        while let Some(pending) = scheduler.next() {
            let fingerprint = store
                .transact(async |transaction| {
                    let run = transaction
                        .run(pending.run_id)?
                        .ok_or(ApplicationError::NotFound)?;
                    run.session_fingerprint
                        .clone()
                        .ok_or(ApplicationError::Conflict)
                })
                .await?;
            RunService::start(store, driver, pending.run_id, fingerprint).await?;
        }
        Ok(())
    }
}
