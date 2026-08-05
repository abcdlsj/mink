use crate::ids::{AgentId, CommandId, RunId, TaskId};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use time::OffsetDateTime;

use crate::computer::core::{
    home::LocalAgent,
    input::{AttentionNoticeInput, DispatchedItemInput},
    session::{ProviderSession, SessionFingerprint, SessionState},
    supervisor::{LocalRun, LocalRunState},
};

use super::{
    ApplicationError,
    ports::{
        AgentHomePort, CommandStatus, ComputerTransaction, DriverPort, StoredCommand,
        TransactionPort,
    },
    run::RunService,
};

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub(in crate::computer) enum Command {
    Provision {
        agent: LocalAgent,
    },
    Configure {
        agent: LocalAgent,
    },
    Suspend {
        agent_id: AgentId,
        cancel_current: bool,
    },
    Resume {
        agent_id: AgentId,
    },
    Restart {
        agent_id: AgentId,
    },
    Retire {
        agent_id: AgentId,
    },
    Start {
        run: Box<LocalRun>,
        fingerprint: SessionFingerprint,
    },
    Attach {
        run_id: RunId,
        sequence: u64,
        item: DispatchedItemInput,
    },
    Notice {
        run_id: RunId,
        notice: AttentionNoticeInput,
    },
    BindTask {
        run_id: RunId,
        task_id: TaskId,
        fingerprint: SessionFingerprint,
    },
    Stop {
        run_id: RunId,
    },
    ResetSession {
        session: ProviderSession,
    },
}

impl Command {
    pub(super) fn fingerprint(&self) -> String {
        let semantic = match self {
            Self::Provision { agent } => format!("provision:{agent:?}"),
            Self::Configure { agent } => format!("configure:{agent:?}"),
            Self::Suspend {
                agent_id,
                cancel_current,
            } => format!("suspend:{agent_id}:{cancel_current}"),
            Self::Resume { agent_id } => format!("resume:{agent_id}"),
            Self::Restart { agent_id } => format!("restart:{agent_id}"),
            Self::Retire { agent_id } => format!("retire:{agent_id}"),
            Self::Start { run, fingerprint } => format!(
                "start:{}:{}:{:?}:{}:{}:{}:{}:{:?}:{}",
                run.view().id,
                run.view().agent_id,
                run.view().task_id,
                run.view().focus_thread_id,
                fingerprint.workspace,
                fingerprint.role_revision,
                fingerprint.audience,
                fingerprint.driver,
                run.view().input.content_hash(),
            ),
            Self::Attach {
                run_id,
                sequence,
                item,
            } => format!("attach:{run_id}:{sequence}:{}", item.content_hash()),
            Self::Notice { run_id, notice } => format!("notice:{run_id}:{notice:?}"),
            Self::BindTask {
                run_id,
                task_id,
                fingerprint,
            } => format!(
                "bind:{run_id}:{task_id}:{:?}:{}:{}:{}",
                fingerprint.driver,
                fingerprint.workspace,
                fingerprint.role_revision,
                fingerprint.audience,
            ),
            Self::Stop { run_id } => format!("stop:{run_id}"),
            Self::ResetSession { session } => format!(
                "reset:{}:{:?}:{}",
                session.view().agent_id,
                session.view().scope,
                session.view().generation,
            ),
        };
        hex::encode(Sha256::digest(semantic.as_bytes()))
    }
}

pub(in crate::computer) struct CommandService;

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::computer) struct CommandExecution {
    pub(in crate::computer) status: CommandStatus,
    pub(in crate::computer) error: Option<ApplicationError>,
}

impl CommandService {
    pub(in crate::computer) async fn execute<
        P: TransactionPort,
        D: DriverPort,
        H: AgentHomePort,
    >(
        store: &mut P,
        driver: &mut D,
        homes: &mut H,
        command_id: CommandId,
        sequence: u64,
        command: Command,
    ) -> Result<CommandExecution, ApplicationError> {
        let fingerprint = command.fingerprint();
        let accepted = store
            .transact(async |transaction| {
                if let Some(stored) = transaction.command(command_id)? {
                    if stored.sequence != sequence
                        || stored.fingerprint != fingerprint
                        || stored.command != command
                    {
                        return Err(ApplicationError::Conflict);
                    }
                    return Ok(Some(CommandExecution {
                        status: stored.status,
                        error: stored.error,
                    }));
                }
                transaction.insert_command(StoredCommand {
                    id: command_id,
                    sequence,
                    fingerprint: fingerprint.clone(),
                    command: command.clone(),
                    status: CommandStatus::Pending,
                    error: None,
                })?;
                Ok(None)
            })
            .await?;
        if let Some(execution) = accepted
            && execution.status != CommandStatus::Pending
        {
            return Ok(execution);
        }

        let result = Self::apply(store, driver, homes, command).await;
        if result.as_ref().is_err_and(|error| {
            matches!(
                error,
                ApplicationError::DriverUnavailable | ApplicationError::Internal
            )
        }) {
            return Err(result.expect_err("retryable error was checked"));
        }
        let already_applied = result == Err(ApplicationError::AlreadyApplied);
        let execution = if result.is_ok() || already_applied {
            CommandExecution {
                status: CommandStatus::Applied,
                error: None,
            }
        } else {
            CommandExecution {
                status: CommandStatus::Rejected,
                error: result.err(),
            }
        };
        store
            .transact(async |transaction| {
                let mut stored = transaction
                    .command(command_id)?
                    .ok_or(ApplicationError::NotFound)?;
                stored.status = execution.status;
                stored.error = execution.error.clone();
                transaction.save_command(stored)
            })
            .await?;
        Ok(execution)
    }

    async fn apply<P: TransactionPort, D: DriverPort, H: AgentHomePort>(
        store: &mut P,
        driver: &mut D,
        homes: &mut H,
        command: Command,
    ) -> Result<(), ApplicationError> {
        match command {
            Command::Provision { agent } => {
                homes.provision(agent.clone()).await?;
                driver.validate(&agent).await
            }
            Command::Configure { agent } => {
                driver.validate(&agent).await?;
                let existing = homes.agent(agent.agent_id).await?;
                if existing.driver != agent.driver
                    || existing.role_revision != agent.role_revision
                    || existing.role != agent.role
                {
                    let sessions = store
                        .transact(async |transaction| transaction.agent_sessions(agent.agent_id))
                        .await?;
                    for mut session in sessions {
                        if matches!(
                            session.view().state,
                            SessionState::Closed | SessionState::Lost
                        ) {
                            continue;
                        }
                        session.mark_closing();
                        let closed = driver.close_session(&session).await.is_ok();
                        session.close(closed, OffsetDateTime::now_utc());
                        store
                            .transact(async |transaction| transaction.save_session(session))
                            .await?;
                    }
                }
                homes.configure(agent).await
            }
            Command::Suspend {
                agent_id,
                cancel_current,
            } => {
                let runs = store
                    .transact(async |transaction| {
                        Ok(transaction
                            .nonterminal_runs()?
                            .into_iter()
                            .filter(|run| run.view().agent_id == agent_id)
                            .collect::<Vec<_>>())
                    })
                    .await?;
                for run in runs {
                    if run.view().state == LocalRunState::Queued || cancel_current {
                        RunService::stop(store, driver, run.view().id).await?;
                    }
                }
                homes.suspend(agent_id).await
            }
            Command::Resume { agent_id } => {
                let agent = homes.agent(agent_id).await?;
                if agent.state == crate::computer::core::home::LocalAgentState::Retired {
                    return Err(ApplicationError::Conflict);
                }
                driver.validate(&agent).await?;
                homes.resume(agent_id).await
            }
            Command::Restart { agent_id } => {
                let runs = store
                    .transact(async |transaction| {
                        Ok(transaction
                            .nonterminal_runs()?
                            .into_iter()
                            .filter(|run| run.view().agent_id == agent_id)
                            .collect::<Vec<_>>())
                    })
                    .await?;
                for run in runs {
                    super::run::RunService::restart(store, driver, run).await?;
                }
                let sessions = store
                    .transact(async |transaction| transaction.agent_sessions(agent_id))
                    .await?;
                for mut session in sessions {
                    if matches!(
                        session.view().state,
                        SessionState::Closed | SessionState::Lost
                    ) {
                        continue;
                    }
                    session.mark_closing();
                    let closed = driver.close_session(&session).await.is_ok();
                    session.close(closed, OffsetDateTime::now_utc());
                    store
                        .transact(async |transaction| transaction.save_session(session))
                        .await?;
                }
                driver.restart_agent(agent_id).await?;
                homes.agent(agent_id).await.map(|_| ())
            }
            Command::Retire { agent_id } => {
                let (runs, sessions) = store
                    .transact(async |transaction| {
                        let runs = transaction
                            .nonterminal_runs()?
                            .into_iter()
                            .filter(|run| run.view().agent_id == agent_id)
                            .collect::<Vec<_>>();
                        let sessions = transaction.agent_sessions(agent_id)?;
                        Ok((runs, sessions))
                    })
                    .await?;
                for run in runs {
                    RunService::stop(store, driver, run.view().id).await?;
                }
                for mut session in sessions {
                    if matches!(
                        session.view().state,
                        SessionState::Closed | SessionState::Lost
                    ) {
                        continue;
                    }
                    session.mark_closing();
                    let closed = driver.close_session(&session).await.is_ok();
                    session.close(closed, OffsetDateTime::now_utc());
                    store
                        .transact(async |transaction| transaction.save_session(session))
                        .await?;
                }
                homes.retire(agent_id).await
            }
            Command::Start { run, fingerprint } => {
                let mut run = *run;
                let run_id = run.view().id;
                run.set_session_fingerprint(fingerprint);
                store
                    .transact(async |transaction| {
                        if let Some(existing) = transaction.run(run_id)? {
                            if existing != run {
                                return Err(ApplicationError::Conflict);
                            }
                        } else {
                            let has_active_run =
                                transaction.nonterminal_runs()?.into_iter().any(|existing| {
                                    existing.view().agent_id == run.view().agent_id
                                        && matches!(
                                            existing.view().state,
                                            LocalRunState::Starting
                                                | LocalRunState::Running
                                                | LocalRunState::Finalizing
                                                | LocalRunState::Stopping
                                        )
                                });
                            if has_active_run {
                                return Err(ApplicationError::Conflict);
                            }
                            transaction.save_run(run)?;
                        }
                        Ok(())
                    })
                    .await
            }
            Command::Attach {
                run_id,
                sequence,
                item,
            } => {
                RunService::attach(store, driver, run_id, sequence, item).await?;
                Ok(())
            }
            Command::Notice { run_id, notice } => {
                RunService::notice(store, driver, run_id, notice).await
            }
            Command::BindTask {
                run_id,
                task_id,
                fingerprint,
            } => RunService::bind_task(store, run_id, task_id, fingerprint).await,
            Command::Stop { run_id } => {
                RunService::stop(store, driver, run_id).await?;
                Ok(())
            }
            Command::ResetSession { mut session } => {
                if matches!(
                    session.view().state,
                    SessionState::Closed | SessionState::Lost
                ) {
                    return Ok(());
                }
                session.mark_closing();
                let succeeded = driver.close_session(&session).await.is_ok();
                session.close(succeeded, OffsetDateTime::now_utc());
                store
                    .transact(async |transaction| transaction.save_session(session))
                    .await
            }
        }
    }
}
