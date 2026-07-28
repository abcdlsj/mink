use std::{collections::HashMap, path::PathBuf, sync::Arc, time::Duration};

use anyhow::{Context, Result, bail, ensure};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use serde::Serialize;
use sha2::{Digest, Sha256};
use sqlx::SqlitePool;
use time::OffsetDateTime;
use tokio::sync::{Mutex, Semaphore, mpsc, oneshot};
use uuid::Uuid;

pub(crate) use crate::computer_protocol::{AgentRunCommand as StartRun, MemoryFileMetadata};
use crate::{
    config::ComputerConfig,
    driver::{
        Driver, DriverEnvironment, DriverOutcome, DriverProcess, DriverRun, DriverStopOutcome,
        ProcessExitEvidence,
    },
};

#[derive(Clone)]
pub struct Supervisor {
    inner: Arc<SupervisorInner>,
}

struct SupervisorInner {
    database: SqlitePool,
    state_dir: PathBuf,
    socket_path: PathBuf,
    codex_config_source: Option<PathBuf>,
    codex_auth_source: Option<PathBuf>,
    drivers: HashMap<String, Arc<dyn Driver>>,
    slots: Arc<Semaphore>,
    max_concurrent_runs: usize,
    active: Mutex<HashMap<Uuid, ActiveRun>>,
    timeout: Duration,
    grace_period: Duration,
}

struct ActiveRun {
    agent_id: Uuid,
    cancel: Option<oneshot::Sender<()>>,
    state: ActiveRunState,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ActiveRunState {
    Running,
    Orphaned,
}

#[derive(Clone, Copy)]
struct RunStopContext {
    run_id: Uuid,
    agent_id: Uuid,
    process_instance_id: Uuid,
    stop_epoch: i64,
}

#[derive(Clone, Copy)]
struct DriverSignalEvidence {
    sigterm_sent: bool,
    sigkill_sent: bool,
}

#[derive(Clone, Copy, Debug)]
pub struct RunCommand {
    pub command_id: Uuid,
    pub computer_seq: i64,
}

#[derive(Clone, Debug, Serialize)]
pub struct RunResult {
    pub run_id: Uuid,
    pub status: String,
    pub error_code: Option<String>,
    pub memory_files: Vec<MemoryFileMetadata>,
}

impl Supervisor {
    pub fn new(
        database: SqlitePool,
        state_dir: PathBuf,
        socket_path: PathBuf,
        config: &ComputerConfig,
        default_driver: Arc<dyn Driver>,
        builtin_provider: Option<crate::agent_core::provider::ProviderConfig>,
    ) -> Self {
        let mut drivers = HashMap::new();
        drivers.insert("codex".to_owned(), default_driver);
        drivers.insert(
            "builtin".to_owned(),
            Arc::new(crate::driver::builtin::BuiltinDriver::new(builtin_provider)),
        );
        Self {
            inner: Arc::new(SupervisorInner {
                database,
                state_dir,
                socket_path,
                codex_config_source: config.codex_config_source.clone(),
                codex_auth_source: config.codex_auth_source.clone(),
                drivers,
                slots: Arc::new(Semaphore::new(config.max_concurrent_runs)),
                max_concurrent_runs: config.max_concurrent_runs,
                active: Mutex::new(HashMap::new()),
                timeout: Duration::from_secs(config.per_agent_timeout_seconds),
                grace_period: Duration::from_secs(config.shutdown_grace_period_seconds),
            }),
        }
    }

    pub fn max_concurrent_runs(&self) -> usize {
        self.inner.max_concurrent_runs
    }

    pub async fn start(
        &self,
        run: StartRun,
        command: RunCommand,
    ) -> Result<oneshot::Receiver<RunResult>> {
        ensure!(!run.prompt.is_empty(), "Agent run prompt must not be empty");
        let mut active = self.inner.active.lock().await;
        ensure!(
            !active
                .values()
                .any(|current| current.agent_id == run.agent_id),
            "Agent already has an active run"
        );
        let existing: Option<String> =
            sqlx::query_scalar("SELECT status FROM local_agent_runs WHERE run_id = ?1")
                .bind(run.run_id.to_string())
                .fetch_optional(&self.inner.database)
                .await?;
        if let Some(status) = existing {
            bail!("Agent run already exists with status {status}");
        }
        let (cancel, cancel_rx) = oneshot::channel();
        let (result_tx, result_rx) = oneshot::channel();
        let result_event_id = Uuid::now_v7();
        sqlx::query(
            "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
             token_expires_at, status, command_id, computer_seq, result_event_id, fencing_token, \
             ownership_lease_expires_at) \
             VALUES (?1, ?2, ?3, zeroblob(32), ?4, 'queued', ?5, ?6, ?7, ?8, ?9)",
        )
        .bind(run.run_id.to_string())
        .bind(run.agent_id.to_string())
        .bind(run.space_id.to_string())
        .bind(OffsetDateTime::now_utc().to_string())
        .bind(command.command_id.to_string())
        .bind(command.computer_seq)
        .bind(result_event_id.to_string())
        .bind(&run.fencing_token)
        .bind(run.ownership_lease_expires_at.to_string())
        .execute(&self.inner.database)
        .await?;
        tracing::info!(
            run_id = %run.run_id,
            agent_member_id = %run.agent_id,
            space_id = %run.space_id,
            driver_kind = %run.driver_kind,
            status = "queued",
            "Agent run queued"
        );
        active.insert(
            run.run_id,
            ActiveRun {
                agent_id: run.agent_id,
                cancel: Some(cancel),
                state: ActiveRunState::Running,
            },
        );
        drop(active);

        let supervisor = self.clone();
        tokio::spawn(async move {
            let run_id = run.run_id;
            let agent_id = run.agent_id;
            let started = std::time::Instant::now();
            let result = match supervisor.execute(run, command, cancel_rx).await {
                Ok(result) => result,
                Err(error) => supervisor
                    .persist_terminal_result(
                        error.run_id,
                        agent_id,
                        command,
                        "failed",
                        Some(&error.code),
                    )
                    .await
                    .unwrap_or(RunResult {
                        run_id: error.run_id,
                        status: "failed".to_owned(),
                        error_code: Some(error.code),
                        memory_files: Vec::new(),
                    }),
            };
            tracing::info!(
                run_id = %run_id,
                agent_member_id = %agent_id,
                status = %result.status,
                error_code = result.error_code.as_deref().unwrap_or("none"),
                duration_ms = started.elapsed().as_millis(),
                "Agent run finished"
            );
            supervisor.inner.active.lock().await.remove(&result.run_id);
            let _ = result_tx.send(result);
        });
        Ok(result_rx)
    }

    pub async fn cancel(&self, run_id: Uuid) -> Result<()> {
        let mut active = self.inner.active.lock().await;
        let Some(active) = active.get_mut(&run_id) else {
            bail!("Agent run is not active");
        };
        let cancel = active
            .cancel
            .take()
            .context("Agent run is already canceling")?;
        let _ = cancel.send(());
        Ok(())
    }

    pub async fn cancel_agent(&self, agent_id: Uuid) -> Result<()> {
        let mut active = self.inner.active.lock().await;
        for run in active.values_mut().filter(|run| run.agent_id == agent_id) {
            if let Some(cancel) = run.cancel.take() {
                let _ = cancel.send(());
            }
        }
        Ok(())
    }

    pub async fn shutdown(&self) -> Result<()> {
        {
            let mut active = self.inner.active.lock().await;
            for run in active.values_mut() {
                if let Some(cancel) = run.cancel.take() {
                    let _ = cancel.send(());
                }
            }
        }
        tokio::time::timeout(
            self.inner.grace_period.saturating_mul(2) + Duration::from_secs(2),
            async {
                loop {
                    let active = self.inner.active.lock().await;
                    if active.is_empty()
                        || active
                            .values()
                            .all(|run| run.state == ActiveRunState::Orphaned)
                    {
                        return;
                    }
                    drop(active);
                    tokio::time::sleep(Duration::from_millis(25)).await;
                }
            },
        )
        .await
        .context("Agent runs did not stop during daemon shutdown")?;
        Ok(())
    }

    pub async fn counts(&self) -> Result<(u32, u32)> {
        let mut agents = 0_u32;
        let mut entries = tokio::fs::read_dir(self.inner.state_dir.join("agents")).await?;
        while let Some(entry) = entries.next_entry().await? {
            if entry.file_type().await?.is_dir() && entry.path().join("profile.json").is_file() {
                agents = agents.saturating_add(1);
            }
        }
        let active = self.inner.active.lock().await.len();
        Ok((agents, active.try_into().unwrap_or(u32::MAX)))
    }

    pub async fn validate_agent(&self, agent_id: Uuid, driver_kind: &str) -> Result<()> {
        let environment = self.environment(agent_id)?;
        let driver = self
            .inner
            .drivers
            .get(driver_kind)
            .with_context(|| format!("unknown Driver: {driver_kind}"))?;
        driver.validate(&environment).await
    }

    pub async fn prepare_agent_driver(&self, agent_id: Uuid, driver_kind: &str) -> Result<()> {
        match driver_kind {
            "codex" => {
                let codex_home = self
                    .inner
                    .state_dir
                    .join("agents")
                    .join(agent_id.to_string())
                    .join("drivers/codex");
                if let Some(source) = &self.inner.codex_config_source {
                    crate::driver::codex::install_sanitized_config(source, &codex_home).await?;
                }
                if let Some(source) = &self.inner.codex_auth_source {
                    crate::driver::codex::install_local_auth(source, &codex_home).await?;
                }
            }
            "builtin" => {}
            _ => bail!("unknown Driver: {driver_kind}"),
        }
        Ok(())
    }

    async fn execute(
        &self,
        run: StartRun,
        command: RunCommand,
        mut cancel_rx: oneshot::Receiver<()>,
    ) -> std::result::Result<RunResult, RunFailure> {
        let permit = tokio::select! {
            permit = self.inner.slots.clone().acquire_owned() => permit.map_err(|_| RunFailure::new(run.run_id, "supervisor_stopped"))?,
            _ = &mut cancel_rx => return self.persist_terminal_result(
                run.run_id,
                run.agent_id,
                command,
                "canceled",
                None,
            ).await,
        };
        let _permit = permit;
        tracing::info!(
            run_id = %run.run_id,
            agent_member_id = %run.agent_id,
            driver_kind = %run.driver_kind,
            status = "starting",
            "Agent run acquired execution slot"
        );
        let driver = self
            .inner
            .drivers
            .get(&run.driver_kind)
            .ok_or_else(|| RunFailure::new(run.run_id, "unknown_driver"))?;
        self.ensure_agent_active(run.agent_id)
            .await
            .map_err(|_| RunFailure::new(run.run_id, "agent_not_active"))?;
        let environment = self
            .environment(run.agent_id)
            .map_err(|_| RunFailure::new(run.run_id, "invalid_agent_home"))?;
        driver
            .validate(&environment)
            .await
            .map_err(|_| RunFailure::new(run.run_id, "driver_unavailable"))?;
        let mut process = driver
            .start(DriverRun {
                run_id: run.run_id,
                prompt: run.prompt,
                environment: environment.clone(),
            })
            .await
            .map_err(|_| RunFailure::new(run.run_id, "driver_start_failed"))?;
        tracing::info!(
            run_id = %run.run_id,
            agent_member_id = %run.agent_id,
            driver_kind = %run.driver_kind,
            process_id = process.pid(),
            status = "starting",
            "Agent Driver started and is waiting for Server receipt"
        );
        let process_instance_id = Uuid::now_v7();
        let started_event_id = Uuid::now_v7();
        let daemon_observed_at = OffsetDateTime::now_utc();
        let mut transaction = self
            .inner
            .database
            .begin()
            .await
            .map_err(|_| RunFailure::new(run.run_id, "local_state_failed"))?;
        sqlx::query(
            "UPDATE local_agent_runs SET process_id = ?2, process_instance_id = ?3 \
             WHERE run_id = ?1 AND status = 'queued'",
        )
        .bind(run.run_id.to_string())
        .bind(process.pid().map(i64::from))
        .bind(process_instance_id.to_string())
        .execute(&mut *transaction)
        .await
        .map_err(|_| RunFailure::new(run.run_id, "local_state_failed"))?;
        sqlx::query(
            "INSERT INTO run_started_outbox (event_id, run_id, run_attempt, process_instance_id, \
             daemon_observed_at, next_attempt_at, created_at) VALUES (?1, ?2, 1, ?3, ?4, ?5, ?5)",
        )
        .bind(started_event_id.to_string())
        .bind(run.run_id.to_string())
        .bind(process_instance_id.to_string())
        .bind(
            daemon_observed_at
                .format(&time::format_description::well_known::Rfc3339)
                .map_err(|_| RunFailure::new(run.run_id, "local_state_failed"))?,
        )
        .bind(daemon_observed_at.to_string())
        .execute(&mut *transaction)
        .await
        .map_err(|_| RunFailure::new(run.run_id, "local_state_failed"))?;
        transaction
            .commit()
            .await
            .map_err(|_| RunFailure::new(run.run_id, "local_state_failed"))?;

        loop {
            let reported: bool = sqlx::query_scalar(
                "SELECT reported_at IS NOT NULL FROM run_started_outbox WHERE event_id = ?1",
            )
            .bind(started_event_id.to_string())
            .fetch_one(&self.inner.database)
            .await
            .map_err(|_| RunFailure::new(run.run_id, "local_state_failed"))?;
            if reported {
                break;
            }
            tokio::select! {
                _ = tokio::time::sleep(Duration::from_millis(25)) => {}
                _ = &mut cancel_rx => {
                    self.stop_driver(
                        driver.as_ref(),
                        &mut process,
                        run.run_id,
                        run.agent_id,
                        process_instance_id,
                    ).await?;
                    return self.persist_terminal_result(run.run_id, run.agent_id, command, "canceled", None).await;
                }
            }
        }

        let token_hash = Sha256::digest(environment.run_token.as_bytes()).to_vec();
        let activated_at = OffsetDateTime::now_utc();
        sqlx::query(
            "UPDATE local_agent_runs SET status = 'running', run_token_hash = ?2, started_at = ?3, \
             token_expires_at = ?4 WHERE run_id = ?1 AND status = 'queued'",
        )
        .bind(run.run_id.to_string())
        .bind(token_hash)
        .bind(activated_at.to_string())
        .bind(
            (activated_at
                + time::Duration::seconds(
                    self.inner.timeout.as_secs() as i64 + self.inner.grace_period.as_secs() as i64,
                ))
            .to_string(),
        )
        .execute(&self.inner.database)
        .await
        .map_err(|_| RunFailure::new(run.run_id, "local_state_failed"))?;
        process
            .activate()
            .await
            .map_err(|_| RunFailure::new(run.run_id, "driver_start_failed"))?;
        tracing::info!(
            run_id = %run.run_id,
            agent_member_id = %run.agent_id,
            process_instance_id = %process_instance_id,
            status = "running",
            "Agent Driver activated after Server receipt"
        );

        let (event_tx, mut event_rx) = mpsc::channel(64);
        event_tx
            .send(crate::driver::DriverEvent::ProcessStarted)
            .await
            .ok();
        let event_run_id = run.run_id;
        let event_agent_id = run.agent_id;
        let event_task = tokio::spawn(async move {
            while let Some(event) = event_rx.recv().await {
                tracing::info!(
                    run_id = %event_run_id,
                    agent_member_id = %event_agent_id,
                    event_type = driver_event_name(&event),
                    "Agent Driver event"
                );
            }
        });
        let (outcome, must_cancel) = {
            let observe = driver.observe(&mut process, &event_tx);
            tokio::pin!(observe);
            tokio::select! {
                status = tokio::time::timeout(self.inner.timeout, &mut observe) => match status {
                    Ok(Ok(DriverOutcome::Completed)) => (("completed", None), false),
                    Ok(Ok(DriverOutcome::Failed)) => (("failed", Some("driver_failed")), false),
                    Ok(Err(_)) => (("failed", Some("driver_protocol_failed")), true),
                    Err(_) => (("failed", Some("run_timeout")), true),
                },
                _ = &mut cancel_rx => (("canceled", None), true),
            }
        };
        if must_cancel {
            self.stop_driver(
                driver.as_ref(),
                &mut process,
                run.run_id,
                run.agent_id,
                process_instance_id,
            )
            .await?;
        } else {
            self.persist_observed_reap_until_recorded(run.run_id, process_instance_id)
                .await;
        }
        let final_event = match outcome {
            ("completed", _) => crate::driver::DriverEvent::ProcessCompleted,
            ("canceled", _) => crate::driver::DriverEvent::ProcessCanceled,
            _ => crate::driver::DriverEvent::ProcessFailed,
        };
        event_tx.send(final_event).await.ok();
        drop(event_tx);
        let _ = event_task.await;
        driver
            .cleanup(&environment)
            .await
            .map_err(|_| RunFailure::new(run.run_id, "driver_cleanup_failed"))?;
        self.persist_terminal_result(run.run_id, run.agent_id, command, outcome.0, outcome.1)
            .await
    }

    async fn stop_driver(
        &self,
        driver: &dyn Driver,
        process: &mut DriverProcess,
        run_id: Uuid,
        agent_id: Uuid,
        process_instance_id: Uuid,
    ) -> std::result::Result<(), RunFailure> {
        let stop_epoch = loop {
            match self.persist_stopping(run_id, process_instance_id).await {
                Ok(stop_epoch) => break stop_epoch,
                Err(error) => {
                    tracing::warn!(run_id = %run_id, process_instance_id = %process_instance_id, error = %error, "Failed to persist Driver stopping state; retrying");
                    tokio::time::sleep(Duration::from_millis(100)).await;
                }
            }
        };
        let context = RunStopContext {
            run_id,
            agent_id,
            process_instance_id,
            stop_epoch,
        };
        tracing::info!(
            run_id = %run_id,
            agent_member_id = %agent_id,
            process_instance_id = %process_instance_id,
            stop_epoch,
            "Agent Driver stop persisted"
        );

        let stop = driver.cancel(process, self.inner.grace_period).await;
        match stop {
            Ok(DriverStopOutcome::Reaped {
                exit,
                sigterm_sent,
                sigkill_sent,
            }) => {
                self.persist_reaped_until_recorded(
                    context,
                    exit,
                    DriverSignalEvidence {
                        sigterm_sent,
                        sigkill_sent,
                    },
                )
                .await;
                tracing::info!(
                    run_id = %run_id,
                    agent_member_id = %agent_id,
                    process_instance_id = %process_instance_id,
                    stop_epoch,
                    exit_code = exit.exit_code,
                    exit_signal = exit.exit_signal,
                    "Agent Driver reaped"
                );
                Ok(())
            }
            Ok(DriverStopOutcome::Orphaned {
                sigterm_sent,
                sigkill_sent,
            }) => {
                self.persist_orphaned_until_recorded(
                    context,
                    DriverSignalEvidence {
                        sigterm_sent,
                        sigkill_sent,
                    },
                    None,
                )
                .await;
                self.mark_active_orphaned(run_id).await;
                tracing::warn!(
                    run_id = %run_id,
                    agent_member_id = %agent_id,
                    process_instance_id = %process_instance_id,
                    stop_epoch,
                    "Agent Driver is orphaned; background reap continues"
                );
                self.reap_orphaned(
                    driver,
                    process,
                    context,
                    DriverSignalEvidence {
                        sigterm_sent,
                        sigkill_sent,
                    },
                )
                .await
            }
            Err(error) => {
                tracing::warn!(
                    run_id = %run_id,
                    agent_member_id = %agent_id,
                    process_instance_id = %process_instance_id,
                    stop_epoch,
                    error = %error,
                    "Agent Driver cancellation failed; background reap continues"
                );
                self.persist_orphaned_until_recorded(
                    context,
                    DriverSignalEvidence {
                        sigterm_sent: false,
                        sigkill_sent: false,
                    },
                    Some("driver_cancel_failed"),
                )
                .await;
                self.mark_active_orphaned(run_id).await;
                self.reap_orphaned(
                    driver,
                    process,
                    context,
                    DriverSignalEvidence {
                        sigterm_sent: false,
                        sigkill_sent: false,
                    },
                )
                .await
            }
        }
    }

    async fn reap_orphaned(
        &self,
        driver: &dyn Driver,
        process: &mut DriverProcess,
        context: RunStopContext,
        signals: DriverSignalEvidence,
    ) -> std::result::Result<(), RunFailure> {
        let exit = loop {
            match driver.reap(process).await {
                Ok(exit) => break exit,
                Err(error) => {
                    tracing::warn!(run_id = %context.run_id, process_instance_id = %context.process_instance_id, stop_epoch = context.stop_epoch, error = %error, "Failed to reap orphaned Agent Driver; retrying");
                    tokio::time::sleep(Duration::from_millis(250)).await;
                }
            }
        };
        self.persist_reaped_until_recorded(context, exit, signals)
            .await;
        tracing::info!(
            run_id = %context.run_id,
            agent_member_id = %context.agent_id,
            process_instance_id = %context.process_instance_id,
            stop_epoch = context.stop_epoch,
            exit_code = exit.exit_code,
            exit_signal = exit.exit_signal,
            "Orphaned Agent Driver reaped"
        );
        Ok(())
    }

    async fn mark_active_orphaned(&self, run_id: Uuid) {
        if let Some(active) = self.inner.active.lock().await.get_mut(&run_id) {
            active.state = ActiveRunState::Orphaned;
        }
    }

    async fn persist_orphaned_until_recorded(
        &self,
        context: RunStopContext,
        signals: DriverSignalEvidence,
        error_code: Option<&str>,
    ) {
        loop {
            match self.persist_orphaned(context, signals, error_code).await {
                Ok(()) => return,
                Err(error) => {
                    tracing::warn!(run_id = %context.run_id, process_instance_id = %context.process_instance_id, stop_epoch = context.stop_epoch, error = %error, "Failed to persist orphaned Driver state; retrying");
                    tokio::time::sleep(Duration::from_millis(100)).await;
                }
            }
        }
    }

    async fn persist_reaped_until_recorded(
        &self,
        context: RunStopContext,
        exit: ProcessExitEvidence,
        signals: DriverSignalEvidence,
    ) {
        loop {
            match self.persist_reaped(context, exit, signals).await {
                Ok(()) => return,
                Err(error) => {
                    tracing::warn!(run_id = %context.run_id, process_instance_id = %context.process_instance_id, stop_epoch = context.stop_epoch, error = %error, "Failed to persist Driver reap evidence; retrying");
                    tokio::time::sleep(Duration::from_millis(100)).await;
                }
            }
        }
    }

    async fn persist_stopping(&self, run_id: Uuid, process_instance_id: Uuid) -> Result<i64> {
        let now = OffsetDateTime::now_utc().to_string();
        let stop_epoch: i64 = sqlx::query_scalar(
            "UPDATE local_agent_runs SET stop_state = 'stopping', stop_epoch = stop_epoch + 1, \
             stop_requested_at = ?3, sigterm_sent_at = NULL, \
             sigkill_sent_at = NULL, orphaned_at = NULL, reaped_at = NULL, exit_code = NULL, \
             exit_signal = NULL, stop_error_code = NULL \
             WHERE run_id = ?1 AND process_instance_id = ?2 \
               AND status IN ('queued', 'running') AND stop_state = 'none' RETURNING stop_epoch",
        )
        .bind(run_id.to_string())
        .bind(process_instance_id.to_string())
        .bind(now)
        .fetch_one(&self.inner.database)
        .await?;
        Ok(stop_epoch)
    }

    async fn persist_orphaned(
        &self,
        context: RunStopContext,
        signals: DriverSignalEvidence,
        error_code: Option<&str>,
    ) -> Result<()> {
        let now = OffsetDateTime::now_utc().to_string();
        let updated = sqlx::query(
            "UPDATE local_agent_runs SET stop_state = 'orphaned', \
             sigterm_sent_at = CASE WHEN ?4 THEN ?6 ELSE NULL END, \
             sigkill_sent_at = CASE WHEN ?5 THEN ?6 ELSE NULL END, \
             orphaned_at = ?6, stop_error_code = ?7 \
             WHERE run_id = ?1 AND process_instance_id = ?2 AND stop_epoch = ?3 \
               AND stop_state = 'stopping'",
        )
        .bind(context.run_id.to_string())
        .bind(context.process_instance_id.to_string())
        .bind(context.stop_epoch)
        .bind(signals.sigterm_sent)
        .bind(signals.sigkill_sent)
        .bind(now)
        .bind(error_code)
        .execute(&self.inner.database)
        .await?;
        ensure!(updated.rows_affected() == 1, "stale Driver orphan callback");
        Ok(())
    }

    async fn persist_reaped(
        &self,
        context: RunStopContext,
        exit: ProcessExitEvidence,
        signals: DriverSignalEvidence,
    ) -> Result<()> {
        let now = OffsetDateTime::now_utc().to_string();
        let updated = sqlx::query(
            "UPDATE local_agent_runs SET stop_state = 'reaped', \
             sigterm_sent_at = CASE WHEN ?6 THEN COALESCE(sigterm_sent_at, ?4) ELSE sigterm_sent_at END, \
             sigkill_sent_at = CASE WHEN ?7 THEN COALESCE(sigkill_sent_at, ?4) ELSE sigkill_sent_at END, \
             reaped_at = ?4, exit_code = ?5, exit_signal = ?8 \
             WHERE run_id = ?1 AND process_instance_id = ?2 AND stop_epoch = ?3 \
               AND stop_state IN ('stopping', 'orphaned')",
        )
        .bind(context.run_id.to_string())
        .bind(context.process_instance_id.to_string())
        .bind(context.stop_epoch)
        .bind(now)
        .bind(exit.exit_code)
        .bind(signals.sigterm_sent)
        .bind(signals.sigkill_sent)
        .bind(exit.exit_signal)
        .execute(&self.inner.database)
        .await?;
        ensure!(updated.rows_affected() == 1, "stale Driver reap callback");
        Ok(())
    }

    async fn persist_observed_reap(&self, run_id: Uuid, process_instance_id: Uuid) -> Result<()> {
        let now = OffsetDateTime::now_utc().to_string();
        let updated = sqlx::query(
            "UPDATE local_agent_runs SET stop_state = 'reaped', reaped_at = ?3 \
             WHERE run_id = ?1 AND process_instance_id = ?2 AND status = 'running' \
               AND stop_state = 'none'",
        )
        .bind(run_id.to_string())
        .bind(process_instance_id.to_string())
        .bind(now)
        .execute(&self.inner.database)
        .await?;
        ensure!(updated.rows_affected() == 1, "stale Driver exit callback");
        Ok(())
    }

    async fn persist_observed_reap_until_recorded(&self, run_id: Uuid, process_instance_id: Uuid) {
        loop {
            match self
                .persist_observed_reap(run_id, process_instance_id)
                .await
            {
                Ok(()) => return,
                Err(error) => {
                    tracing::warn!(run_id = %run_id, process_instance_id = %process_instance_id, error = %error, "Failed to persist Driver exit evidence; retrying");
                    tokio::time::sleep(Duration::from_millis(100)).await;
                }
            }
        }
    }

    async fn persist_terminal_result(
        &self,
        run_id: Uuid,
        agent_id: Uuid,
        command: RunCommand,
        status: &str,
        error_code: Option<&str>,
    ) -> std::result::Result<RunResult, RunFailure> {
        let memory_files = match scan_memory(
            &self
                .inner
                .state_dir
                .join("agents")
                .join(agent_id.to_string())
                .join("memory"),
        )
        .await
        {
            Ok(files) => files,
            Err(error) => {
                tracing::warn!(run_id = %run_id, error = %error, "Failed to scan Agent Memory after run");
                Vec::new()
            }
        };
        let result = RunResult {
            run_id,
            status: status.to_owned(),
            error_code: error_code.map(str::to_owned),
            memory_files,
        };
        let payload = serde_json::json!({
            "ok": status == "completed",
            "run_id": run_id,
            "status": status,
            "error_code": error_code,
            "memory_files": &result.memory_files,
        });
        let now = OffsetDateTime::now_utc().to_string();
        let mut transaction = self
            .inner
            .database
            .begin()
            .await
            .map_err(|_| RunFailure::new(run_id, "local_state_failed"))?;
        sqlx::query(
            "UPDATE local_agent_runs SET status = ?2, process_id = NULL, finished_at = ?3, \
             last_error_code = ?4 WHERE run_id = ?1",
        )
        .bind(run_id.to_string())
        .bind(status)
        .bind(&now)
        .bind(error_code)
        .execute(&mut *transaction)
        .await
        .map_err(|_| RunFailure::new(run_id, "local_state_failed"))?;
        sqlx::query(
            "UPDATE server_commands SET status = ?2, result_json = ?3, completed_at = ?4 \
             WHERE command_id = ?1",
        )
        .bind(command.command_id.to_string())
        .bind(if status == "completed" {
            "completed"
        } else {
            "failed"
        })
        .bind(
            serde_json::to_string(&payload)
                .map_err(|_| RunFailure::new(run_id, "local_state_failed"))?,
        )
        .bind(&now)
        .execute(&mut *transaction)
        .await
        .map_err(|_| RunFailure::new(run_id, "local_state_failed"))?;
        sqlx::query(
            "INSERT INTO run_result_outbox (event_id, command_id, computer_seq, run_id, \
             payload_json, next_attempt_at, created_at) \
             SELECT result_event_id, command_id, computer_seq, run_id, ?2, ?3, ?3 \
             FROM local_agent_runs WHERE run_id = ?1 \
             ON CONFLICT(event_id) DO NOTHING",
        )
        .bind(run_id.to_string())
        .bind(
            serde_json::to_string(&payload)
                .map_err(|_| RunFailure::new(run_id, "local_state_failed"))?,
        )
        .bind(&now)
        .execute(&mut *transaction)
        .await
        .map_err(|_| RunFailure::new(run_id, "local_state_failed"))?;
        transaction
            .commit()
            .await
            .map_err(|_| RunFailure::new(run_id, "local_state_failed"))?;
        tracing::info!(run_id = %run_id, command_id = %command.command_id, "Agent run result persisted to outbox");
        Ok(result)
    }

    fn environment(&self, agent_id: Uuid) -> Result<DriverEnvironment> {
        let agent_home = self
            .inner
            .state_dir
            .join("agents")
            .join(agent_id.to_string());
        ensure!(agent_home.is_dir(), "Agent Home is unavailable");
        let mut token = [0_u8; 32];
        getrandom::fill(&mut token)?;
        let executable_directory = std::env::current_exe()?
            .parent()
            .context("current executable has no parent directory")?
            .to_owned();
        let mut paths = vec![executable_directory];
        paths.extend(std::env::split_paths(
            &std::env::var_os("PATH").context("PATH is unavailable")?,
        ));
        Ok(DriverEnvironment {
            state_dir: self.inner.state_dir.clone(),
            agents_root: self.inner.state_dir.join("agents"),
            workspace: agent_home.join("workspace"),
            codex_home: agent_home.join("drivers/codex"),
            agent_home,
            socket_path: self.inner.socket_path.clone(),
            run_token: URL_SAFE_NO_PAD.encode(token),
            path: std::env::join_paths(paths)?
                .into_string()
                .map_err(|_| anyhow::anyhow!("Driver PATH contains non-UTF-8 data"))?,
        })
    }

    async fn ensure_agent_active(&self, agent_id: Uuid) -> Result<()> {
        let profile = tokio::fs::read(
            self.inner
                .state_dir
                .join("agents")
                .join(agent_id.to_string())
                .join("profile.json"),
        )
        .await?;
        let profile: serde_json::Value = serde_json::from_slice(&profile)?;
        ensure!(
            profile
                .get("desired_lifecycle")
                .and_then(serde_json::Value::as_str)
                == Some("active")
                && profile
                    .get("provision_status")
                    .and_then(serde_json::Value::as_str)
                    == Some("ready"),
            "Agent is not active and ready"
        );
        Ok(())
    }
}

fn driver_event_name(event: &crate::driver::DriverEvent) -> &str {
    match event {
        crate::driver::DriverEvent::ProcessStarted => "process_started",
        crate::driver::DriverEvent::OutputReceived { event_type } => event_type,
        crate::driver::DriverEvent::CommandStarted => "command_started",
        crate::driver::DriverEvent::CommandFinished => "command_finished",
        crate::driver::DriverEvent::ProcessCompleted => "process_completed",
        crate::driver::DriverEvent::ProcessFailed => "process_failed",
        crate::driver::DriverEvent::ProcessCanceled => "process_canceled",
    }
}

struct RunFailure {
    run_id: Uuid,
    code: String,
}

impl RunFailure {
    fn new(run_id: Uuid, code: &str) -> Self {
        Self {
            run_id,
            code: code.to_owned(),
        }
    }
}

async fn scan_memory(root: &std::path::Path) -> Result<Vec<MemoryFileMetadata>> {
    let mut pending = vec![root.to_owned()];
    let mut files = Vec::new();
    while let Some(directory) = pending.pop() {
        let mut entries = tokio::fs::read_dir(&directory).await?;
        while let Some(entry) = entries.next_entry().await? {
            let file_type = entry.file_type().await?;
            if file_type.is_symlink() {
                continue;
            }
            if file_type.is_dir() {
                pending.push(entry.path());
                continue;
            }
            if !file_type.is_file() {
                continue;
            }
            let bytes = tokio::fs::read(entry.path()).await?;
            let metadata = entry.metadata().await?;
            let path = entry.path();
            let relative = path
                .strip_prefix(root)?
                .to_str()
                .context("Memory path is not UTF-8")?;
            files.push(MemoryFileMetadata {
                path: relative.to_owned(),
                size: metadata.len(),
                sha256: hex::encode(Sha256::digest(&bytes)),
                updated_at: OffsetDateTime::from(metadata.modified()?),
            });
        }
    }
    files.sort_by(|left, right| left.path.cmp(&right.path));
    Ok(files)
}

pub async fn recover_interrupted_runs(database: &SqlitePool) -> Result<()> {
    let now = OffsetDateTime::now_utc().to_string();
    let mut transaction = database.begin().await?;
    let interrupted: Vec<(String, String, i64, String)> = sqlx::query_as(
        "SELECT runs.run_id, commands.command_id, commands.computer_seq, runs.result_event_id \
         FROM local_agent_runs runs JOIN server_commands commands \
           ON commands.command_id = runs.command_id \
         WHERE runs.status IN ('queued', 'running')",
    )
    .fetch_all(&mut *transaction)
    .await?;
    for (run_id, command_id, computer_seq, event_id) in interrupted {
        let payload = serde_json::json!({
            "ok": false,
            "run_id": run_id,
            "status": "failed",
            "error_code": "process_lost",
            "memory_files": [],
        });
        let payload = serde_json::to_string(&payload)?;
        sqlx::query(
            "UPDATE local_agent_runs SET status = 'failed', process_id = NULL, finished_at = ?2, \
             last_error_code = 'process_lost', server_recovery_reported_at = NULL, \
             command_id = ?3, computer_seq = ?4, result_event_id = ?5 WHERE run_id = ?1",
        )
        .bind(&run_id)
        .bind(&now)
        .bind(&command_id)
        .bind(computer_seq)
        .bind(&event_id)
        .execute(&mut *transaction)
        .await?;
        sqlx::query(
            "UPDATE server_commands SET status = 'failed', result_json = ?2, completed_at = ?3 \
             WHERE command_id = ?1",
        )
        .bind(&command_id)
        .bind(&payload)
        .bind(&now)
        .execute(&mut *transaction)
        .await?;
        sqlx::query(
            "INSERT INTO run_result_outbox (event_id, command_id, computer_seq, run_id, \
             payload_json, next_attempt_at, created_at) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?6) \
             ON CONFLICT(event_id) DO NOTHING",
        )
        .bind(&event_id)
        .bind(&command_id)
        .bind(computer_seq)
        .bind(&run_id)
        .bind(&payload)
        .bind(&now)
        .execute(&mut *transaction)
        .await?;
    }
    transaction.commit().await?;
    reconcile_run_result_outbox(database).await?;
    Ok(())
}

async fn reconcile_run_result_outbox(database: &SqlitePool) -> Result<()> {
    let now = OffsetDateTime::now_utc().to_string();
    let mut transaction = database.begin().await?;
    let missing: Vec<(String, String, i64, String, String)> = sqlx::query_as(
        "SELECT runs.run_id, commands.command_id, commands.computer_seq, \
         runs.result_event_id, commands.result_json \
         FROM local_agent_runs runs JOIN server_commands commands \
           ON commands.command_id = runs.command_id \
         LEFT JOIN run_result_outbox outbox ON outbox.run_id = runs.run_id \
         WHERE runs.status IN ('completed', 'failed', 'canceled') \
           AND commands.result_json IS NOT NULL AND outbox.run_id IS NULL",
    )
    .fetch_all(&mut *transaction)
    .await?;
    for (run_id, command_id, computer_seq, event_id, payload) in missing {
        sqlx::query(
            "UPDATE local_agent_runs SET command_id = ?2, computer_seq = ?3, result_event_id = ?4 \
             WHERE run_id = ?1",
        )
        .bind(&run_id)
        .bind(&command_id)
        .bind(computer_seq)
        .bind(&event_id)
        .execute(&mut *transaction)
        .await?;
        sqlx::query(
            "INSERT INTO run_result_outbox (event_id, command_id, computer_seq, run_id, \
             payload_json, next_attempt_at, created_at) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?6)",
        )
        .bind(&event_id)
        .bind(&command_id)
        .bind(computer_seq)
        .bind(&run_id)
        .bind(&payload)
        .bind(&now)
        .execute(&mut *transaction)
        .await?;
    }
    transaction.commit().await?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::sync::{
        Arc,
        atomic::{AtomicI64, AtomicUsize, Ordering},
    };

    use async_trait::async_trait;
    use tokio::sync::{Notify, mpsc};

    use crate::{
        database,
        driver::{DriverEvent, DriverProcess},
    };

    use super::*;

    struct TestDriver;

    struct OrphaningDriver {
        allow_reap: Arc<Notify>,
        reap_attempts: Arc<AtomicUsize>,
    }

    #[async_trait]
    impl Driver for TestDriver {
        async fn validate(&self, _environment: &DriverEnvironment) -> Result<()> {
            Ok(())
        }

        async fn start(&self, run: DriverRun) -> Result<DriverProcess> {
            let delay = match run.prompt.render().as_str() {
                "hold" => Duration::from_secs(1),
                "timeout" => Duration::from_secs(5),
                _ => Duration::ZERO,
            };
            let (events_tx, events) = mpsc::channel(1);
            let (activation_tx, activation_rx) = oneshot::channel();
            let task = tokio::spawn(async move {
                if activation_rx.await.is_err() {
                    return;
                }
                events_tx
                    .send(DriverEvent::OutputReceived {
                        event_type: "test_started".to_owned(),
                    })
                    .await
                    .ok();
                tokio::time::sleep(delay).await;
            });
            Ok(DriverProcess::Internal {
                task,
                events,
                activation: Some(activation_tx),
            })
        }

        async fn observe(
            &self,
            process: &mut DriverProcess,
            events: &mpsc::Sender<DriverEvent>,
        ) -> Result<DriverOutcome> {
            let DriverProcess::Internal {
                task,
                events: source,
                ..
            } = process
            else {
                bail!("Test Driver requires an internal task");
            };
            while let Ok(event) = source.try_recv() {
                events.send(event).await.ok();
            }
            task.await.context("Test Driver task failed")?;
            Ok(DriverOutcome::Completed)
        }

        async fn cancel(
            &self,
            process: &mut DriverProcess,
            _grace_period: Duration,
        ) -> Result<DriverStopOutcome> {
            let DriverProcess::Internal { task, .. } = process else {
                bail!("Test Driver requires an internal task");
            };
            task.abort();
            let _ = task.await;
            Ok(DriverStopOutcome::Reaped {
                exit: ProcessExitEvidence::INTERNAL_TASK,
                sigterm_sent: false,
                sigkill_sent: false,
            })
        }

        async fn reap(&self, process: &mut DriverProcess) -> Result<ProcessExitEvidence> {
            let DriverProcess::Internal { task, .. } = process else {
                bail!("Test Driver requires an internal task");
            };
            let _ = task.await;
            Ok(ProcessExitEvidence::INTERNAL_TASK)
        }

        async fn cleanup(&self, _environment: &DriverEnvironment) -> Result<()> {
            Ok(())
        }
    }

    #[async_trait]
    impl Driver for OrphaningDriver {
        async fn validate(&self, _environment: &DriverEnvironment) -> Result<()> {
            Ok(())
        }

        async fn start(&self, _run: DriverRun) -> Result<DriverProcess> {
            let (events_tx, events) = mpsc::channel(1);
            let (activation_tx, activation_rx) = oneshot::channel();
            let task = tokio::spawn(async move {
                if activation_rx.await.is_err() {
                    return;
                }
                let _events_tx = events_tx;
                std::future::pending::<()>().await;
            });
            Ok(DriverProcess::Internal {
                task,
                events,
                activation: Some(activation_tx),
            })
        }

        async fn observe(
            &self,
            process: &mut DriverProcess,
            _events: &mpsc::Sender<DriverEvent>,
        ) -> Result<DriverOutcome> {
            let DriverProcess::Internal { task, .. } = process else {
                bail!("Orphaning Driver requires an internal task");
            };
            let _ = task.await;
            Ok(DriverOutcome::Completed)
        }

        async fn cancel(
            &self,
            _process: &mut DriverProcess,
            _grace_period: Duration,
        ) -> Result<DriverStopOutcome> {
            Ok(DriverStopOutcome::Orphaned {
                sigterm_sent: false,
                sigkill_sent: false,
            })
        }

        async fn reap(&self, process: &mut DriverProcess) -> Result<ProcessExitEvidence> {
            if self.reap_attempts.fetch_add(1, Ordering::SeqCst) == 0 {
                bail!("injected reap failure");
            }
            self.allow_reap.notified().await;
            let DriverProcess::Internal { task, .. } = process else {
                bail!("Orphaning Driver requires an internal task");
            };
            task.abort();
            let _ = task.await;
            Ok(ProcessExitEvidence {
                exit_code: None,
                exit_signal: Some(libc::SIGKILL),
            })
        }

        async fn cleanup(&self, _environment: &DriverEnvironment) -> Result<()> {
            Ok(())
        }
    }

    async fn fixture(
        max_runs: usize,
        timeout_seconds: u64,
    ) -> (tempfile::TempDir, Supervisor, Uuid, Uuid) {
        fixture_with_driver(
            max_runs,
            timeout_seconds,
            Arc::new(TestDriver) as Arc<dyn Driver>,
        )
        .await
    }

    async fn fixture_with_driver(
        max_runs: usize,
        timeout_seconds: u64,
        driver: Arc<dyn Driver>,
    ) -> (tempfile::TempDir, Supervisor, Uuid, Uuid) {
        let root = tempfile::tempdir().unwrap();
        let state = root.path().join("computer");
        std::fs::create_dir_all(state.join("agents")).unwrap();
        let first = Uuid::now_v7();
        let second = Uuid::now_v7();
        for agent_id in [first, second] {
            let home = state.join("agents").join(agent_id.to_string());
            for relative in ["workspace", "memory", "runs", "drivers/codex"] {
                std::fs::create_dir_all(home.join(relative)).unwrap();
            }
            std::fs::write(
                home.join("profile.json"),
                r#"{"desired_lifecycle":"active","provision_status":"ready"}"#,
            )
            .unwrap();
        }
        std::fs::write(state.join("daemon.sock"), "").unwrap();
        let database = database::connect_sqlite(&state.join("daemon.db"))
            .await
            .unwrap();
        let config = ComputerConfig {
            state_dir: state.clone(),
            max_concurrent_runs: max_runs,
            per_agent_timeout_seconds: timeout_seconds,
            shutdown_grace_period_seconds: 1,
            ..ComputerConfig::default()
        };
        let supervisor = Supervisor::new(
            database,
            state.clone(),
            state.join("daemon.sock"),
            &config,
            driver,
            None,
        );
        (root, supervisor, first, second)
    }

    fn run(agent_id: Uuid, prompt: &str) -> StartRun {
        StartRun {
            run_id: Uuid::now_v7(),
            agent_id,
            space_id: Uuid::now_v7(),
            prompt: prompt.into(),
            driver_kind: "codex".to_owned(),
            fencing_token: Uuid::now_v7().to_string(),
            ownership_lease_expires_at: OffsetDateTime::now_utc() + time::Duration::minutes(35),
        }
    }

    async fn start(supervisor: &Supervisor, run: StartRun) -> Result<oneshot::Receiver<RunResult>> {
        static NEXT_SEQUENCE: AtomicI64 = AtomicI64::new(1);
        let command = RunCommand {
            command_id: Uuid::now_v7(),
            computer_seq: NEXT_SEQUENCE.fetch_add(1, Ordering::Relaxed),
        };
        let request = serde_json::json!({
            "kind": "agent.run",
            "payload": {
                "run_id": run.run_id,
                "agent_id": run.agent_id,
                "space_id": run.space_id,
            },
        });
        sqlx::query(
            "INSERT INTO server_commands (command_id, computer_seq, request_json, status, \
             received_at) VALUES (?1, ?2, ?3, 'running', ?4)",
        )
        .bind(command.command_id.to_string())
        .bind(command.computer_seq)
        .bind(serde_json::to_string(&request)?)
        .bind(OffsetDateTime::now_utc().to_string())
        .execute(&supervisor.inner.database)
        .await?;
        let database = supervisor.inner.database.clone();
        let run_id = run.run_id;
        tokio::spawn(async move {
            loop {
                let updated = sqlx::query(
                    "UPDATE run_started_outbox SET reported_at = ?2 \
                     WHERE run_id = ?1 AND reported_at IS NULL",
                )
                .bind(run_id.to_string())
                .bind(OffsetDateTime::now_utc().to_string())
                .execute(&database)
                .await
                .unwrap();
                if updated.rows_affected() == 1 {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        });
        supervisor.start(run, command).await
    }

    async fn status(supervisor: &Supervisor, run_id: Uuid) -> String {
        sqlx::query_scalar("SELECT status FROM local_agent_runs WHERE run_id = ?1")
            .bind(run_id.to_string())
            .fetch_one(&supervisor.inner.database)
            .await
            .unwrap()
    }

    #[tokio::test]
    async fn concurrency_limit_queues_other_agents_and_rejects_same_agent_overlap() {
        let (_root, supervisor, first, second) = fixture(1, 10).await;
        let first_run = run(first, "hold");
        let first_id = first_run.run_id;
        let first_result = start(&supervisor, first_run).await.unwrap();
        let second_run = run(second, "quick");
        let second_id = second_run.run_id;
        let second_result = start(&supervisor, second_run).await.unwrap();
        assert!(start(&supervisor, run(first, "quick")).await.is_err());
        tokio::time::timeout(Duration::from_secs(10), async {
            while status(&supervisor, first_id).await != "running" {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        assert_eq!(status(&supervisor, first_id).await, "running");
        assert_eq!(status(&supervisor, second_id).await, "queued");
        assert_eq!(first_result.await.unwrap().status, "completed");
        assert_eq!(second_result.await.unwrap().status, "completed");
    }

    #[tokio::test]
    async fn cancel_and_timeout_stop_processes_and_persist_terminal_state() {
        let (_root, supervisor, first, second) = fixture(2, 1).await;
        let canceled = run(first, "timeout");
        let canceled_id = canceled.run_id;
        let canceled_result = start(&supervisor, canceled).await.unwrap();
        tokio::time::timeout(Duration::from_secs(10), async {
            while status(&supervisor, canceled_id).await != "running" {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .expect("canceled run did not enter running state");
        supervisor.cancel(canceled_id).await.unwrap();
        assert_eq!(canceled_result.await.unwrap().status, "canceled");
        assert_eq!(status(&supervisor, canceled_id).await, "canceled");

        let timed_out = run(second, "timeout");
        let timed_out_id = timed_out.run_id;
        let result = start(&supervisor, timed_out).await.unwrap().await.unwrap();
        assert_eq!(result.status, "failed");
        assert_eq!(result.error_code.as_deref(), Some("run_timeout"));
        assert_eq!(status(&supervisor, timed_out_id).await, "failed");
        let outbox: Vec<(String, String)> = sqlx::query_as(
            "SELECT runs.status, outbox.payload_json FROM local_agent_runs runs \
             JOIN run_result_outbox outbox ON outbox.run_id = runs.run_id \
             WHERE runs.run_id IN (?1, ?2) ORDER BY runs.run_id",
        )
        .bind(canceled_id.to_string())
        .bind(timed_out_id.to_string())
        .fetch_all(&supervisor.inner.database)
        .await
        .unwrap();
        assert_eq!(outbox.len(), 2);
        for (status, payload) in outbox {
            let payload: serde_json::Value = serde_json::from_str(&payload).unwrap();
            assert_eq!(payload["status"], status);
            assert!(payload["memory_files"].is_array());
        }
    }

    #[tokio::test]
    async fn shutdown_cancels_all_active_runs_before_returning() {
        let (_root, supervisor, first, second) = fixture(2, 10).await;
        let first_run = run(first, "timeout");
        let first_id = first_run.run_id;
        let first_result = start(&supervisor, first_run).await.unwrap();
        let second_run = run(second, "timeout");
        let second_id = second_run.run_id;
        let second_result = start(&supervisor, second_run).await.unwrap();
        tokio::time::timeout(Duration::from_secs(10), async {
            while status(&supervisor, first_id).await != "running"
                || status(&supervisor, second_id).await != "running"
            {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        supervisor.shutdown().await.unwrap();
        assert_eq!(first_result.await.unwrap().status, "canceled");
        assert_eq!(second_result.await.unwrap().status, "canceled");
        assert_eq!(supervisor.counts().await.unwrap().1, 0);
        let pending_results: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM run_result_outbox \
             WHERE run_id IN (?1, ?2) AND reported_at IS NULL",
        )
        .bind(first_id.to_string())
        .bind(second_id.to_string())
        .fetch_one(&supervisor.inner.database)
        .await
        .unwrap();
        assert_eq!(pending_results, 2);
    }

    #[tokio::test]
    async fn orphaned_run_stays_non_terminal_until_background_reap_provides_exit_evidence() {
        let allow_reap = Arc::new(Notify::new());
        let reap_attempts = Arc::new(AtomicUsize::new(0));
        let driver = Arc::new(OrphaningDriver {
            allow_reap: allow_reap.clone(),
            reap_attempts: reap_attempts.clone(),
        });
        let (_root, supervisor, first, _second) = fixture_with_driver(1, 10, driver).await;
        let orphaned = run(first, "hold");
        let run_id = orphaned.run_id;
        let result = start(&supervisor, orphaned).await.unwrap();
        tokio::time::timeout(Duration::from_secs(10), async {
            while status(&supervisor, run_id).await != "running" {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();

        tokio::time::timeout(Duration::from_secs(1), supervisor.shutdown())
            .await
            .expect("shutdown did not accept orphaned Run")
            .unwrap();
        tokio::time::timeout(Duration::from_secs(10), async {
            loop {
                let stop_state: String =
                    sqlx::query_scalar("SELECT stop_state FROM local_agent_runs WHERE run_id = ?1")
                        .bind(run_id.to_string())
                        .fetch_one(&supervisor.inner.database)
                        .await
                        .unwrap();
                if stop_state == "orphaned" {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();

        assert_eq!(status(&supervisor, run_id).await, "running");
        let outbox_count: i64 =
            sqlx::query_scalar("SELECT count(*) FROM run_result_outbox WHERE run_id = ?1")
                .bind(run_id.to_string())
                .fetch_one(&supervisor.inner.database)
                .await
                .unwrap();
        assert_eq!(outbox_count, 0);
        assert_eq!(supervisor.counts().await.unwrap().1, 1);
        tokio::time::timeout(Duration::from_secs(2), async {
            while reap_attempts.load(Ordering::SeqCst) < 2 {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .expect("background reaper did not retry after injected failure");

        allow_reap.notify_one();
        assert_eq!(result.await.unwrap().status, "canceled");
        let evidence: (String, i64, Option<String>, Option<String>, Option<i64>) = sqlx::query_as(
            "SELECT stop_state, stop_epoch, orphaned_at, reaped_at, exit_signal \
                 FROM local_agent_runs WHERE run_id = ?1",
        )
        .bind(run_id.to_string())
        .fetch_one(&supervisor.inner.database)
        .await
        .unwrap();
        assert_eq!(evidence.0, "reaped");
        assert_eq!(evidence.1, 1);
        assert!(evidence.2.is_some());
        assert!(evidence.3.is_some());
        assert_eq!(evidence.4, Some(i64::from(libc::SIGKILL)));
        assert_eq!(supervisor.counts().await.unwrap().1, 0);
    }

    #[tokio::test]
    async fn restart_persists_process_lost_result_with_terminal_run_atomically() {
        let (_root, supervisor, first, _second) = fixture(1, 10).await;
        let run = run(first, "quick");
        let run_id = run.run_id;
        let command = RunCommand {
            command_id: Uuid::now_v7(),
            computer_seq: 1,
        };
        let request = serde_json::json!({
            "kind": "agent.run",
            "payload": {
                "run_id": run_id,
                "agent_id": first,
                "space_id": run.space_id,
            },
        });
        sqlx::query(
            "INSERT INTO server_commands (command_id, computer_seq, request_json, status, \
             received_at) VALUES (?1, ?2, ?3, 'running', ?4)",
        )
        .bind(command.command_id.to_string())
        .bind(command.computer_seq)
        .bind(serde_json::to_string(&request).unwrap())
        .bind(OffsetDateTime::now_utc().to_string())
        .execute(&supervisor.inner.database)
        .await
        .unwrap();
        sqlx::query(
            "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
             token_expires_at, status, command_id, computer_seq, result_event_id) \
             VALUES (?1, ?2, ?3, zeroblob(32), ?4, 'running', ?5, ?6, ?7)",
        )
        .bind(run_id.to_string())
        .bind(first.to_string())
        .bind(run.space_id.to_string())
        .bind(OffsetDateTime::now_utc().to_string())
        .bind(command.command_id.to_string())
        .bind(command.computer_seq)
        .bind(Uuid::now_v7().to_string())
        .execute(&supervisor.inner.database)
        .await
        .unwrap();
        let completed_run_id = Uuid::now_v7();
        let completed_command = RunCommand {
            command_id: Uuid::now_v7(),
            computer_seq: 2,
        };
        let completed_event_id = Uuid::now_v7().to_string();
        let completed_payload = serde_json::json!({
            "ok": true,
            "run_id": completed_run_id,
            "status": "completed",
            "memory_files": [],
        });
        sqlx::query(
            "INSERT INTO server_commands (command_id, computer_seq, request_json, status, \
             result_json, received_at, completed_at) VALUES (?1, ?2, ?3, 'completed', ?4, ?5, ?5)",
        )
        .bind(completed_command.command_id.to_string())
        .bind(completed_command.computer_seq)
        .bind(
            serde_json::json!({
                "kind": "agent.run",
                "payload": {
                    "run_id": completed_run_id,
                    "agent_id": first,
                    "space_id": run.space_id,
                },
            })
            .to_string(),
        )
        .bind(completed_payload.to_string())
        .bind(OffsetDateTime::now_utc().to_string())
        .execute(&supervisor.inner.database)
        .await
        .unwrap();
        sqlx::query(
            "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
             token_expires_at, status, finished_at, command_id, computer_seq, result_event_id) \
             VALUES (?1, ?2, ?3, zeroblob(32), ?4, 'completed', ?4, ?5, ?6, ?7)",
        )
        .bind(completed_run_id.to_string())
        .bind(first.to_string())
        .bind(run.space_id.to_string())
        .bind(OffsetDateTime::now_utc().to_string())
        .bind(completed_command.command_id.to_string())
        .bind(completed_command.computer_seq)
        .bind(&completed_event_id)
        .execute(&supervisor.inner.database)
        .await
        .unwrap();

        recover_interrupted_runs(&supervisor.inner.database)
            .await
            .unwrap();

        let persisted: (String, String, String, i64) = sqlx::query_as(
            "SELECT runs.status, commands.status, outbox.payload_json, outbox.attempt_count \
             FROM local_agent_runs runs JOIN server_commands commands \
               ON commands.command_id = runs.command_id \
             JOIN run_result_outbox outbox ON outbox.run_id = runs.run_id \
             WHERE runs.run_id = ?1",
        )
        .bind(run_id.to_string())
        .fetch_one(&supervisor.inner.database)
        .await
        .unwrap();
        assert_eq!(persisted.0, "failed");
        assert_eq!(persisted.1, "failed");
        assert_eq!(persisted.3, 0);
        let payload: serde_json::Value = serde_json::from_str(&persisted.2).unwrap();
        assert_eq!(payload["error_code"], "process_lost");
        let recovered_completed: (String, Option<String>) = sqlx::query_as(
            "SELECT payload_json, reported_at FROM run_result_outbox WHERE run_id = ?1",
        )
        .bind(completed_run_id.to_string())
        .fetch_one(&supervisor.inner.database)
        .await
        .unwrap();
        assert_eq!(
            serde_json::from_str::<serde_json::Value>(&recovered_completed.0).unwrap(),
            completed_payload
        );
        assert!(recovered_completed.1.is_none());
    }
}
