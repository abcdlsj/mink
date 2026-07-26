use std::{collections::HashMap, path::PathBuf, sync::Arc, time::Duration};

use anyhow::{Context, Result, bail, ensure};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use sqlx::SqlitePool;
use time::OffsetDateTime;
use tokio::sync::{Mutex, Semaphore, mpsc, oneshot};
use uuid::Uuid;

use crate::{
    config::ComputerConfig,
    driver::{Driver, DriverEnvironment, DriverOutcome, DriverRun},
    prompt::AgentRunPrompt,
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
    active: Mutex<HashMap<Uuid, ActiveRun>>,
    timeout: Duration,
    grace_period: Duration,
}

struct ActiveRun {
    agent_id: Uuid,
    cancel: Option<oneshot::Sender<()>>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct StartRun {
    pub run_id: Uuid,
    pub agent_id: Uuid,
    pub space_id: Uuid,
    pub prompt: AgentRunPrompt,
    pub driver_kind: String,
}

#[derive(Clone, Debug, Serialize)]
pub struct RunResult {
    pub run_id: Uuid,
    pub status: String,
    pub error_code: Option<String>,
}

impl Supervisor {
    pub fn new(
        database: SqlitePool,
        state_dir: PathBuf,
        socket_path: PathBuf,
        config: &ComputerConfig,
        default_driver: Arc<dyn Driver>,
    ) -> Self {
        let mut drivers = HashMap::new();
        drivers.insert("codex".to_owned(), default_driver);
        drivers.insert(
            "builtin".to_owned(),
            Arc::new(crate::driver::builtin::BuiltinDriver::new()),
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
                active: Mutex::new(HashMap::new()),
                timeout: Duration::from_secs(config.per_agent_timeout_seconds),
                grace_period: Duration::from_secs(config.shutdown_grace_period_seconds),
            }),
        }
    }

    pub async fn start(&self, run: StartRun) -> Result<oneshot::Receiver<RunResult>> {
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
        sqlx::query(
            "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
             token_expires_at, status) VALUES (?1, ?2, ?3, zeroblob(32), ?4, 'queued')",
        )
        .bind(run.run_id.to_string())
        .bind(run.agent_id.to_string())
        .bind(run.space_id.to_string())
        .bind(OffsetDateTime::now_utc().to_string())
        .execute(&self.inner.database)
        .await?;
        active.insert(
            run.run_id,
            ActiveRun {
                agent_id: run.agent_id,
                cancel: Some(cancel),
            },
        );
        drop(active);

        let supervisor = self.clone();
        tokio::spawn(async move {
            let result = match supervisor.execute(run, cancel_rx).await {
                Ok(result) => result,
                Err(error) => supervisor
                    .finish_without_process(error.run_id, "failed", Some(&error.code))
                    .await
                    .unwrap_or(RunResult {
                        run_id: error.run_id,
                        status: "failed".to_owned(),
                        error_code: Some(error.code),
                    }),
            };
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
        tokio::time::timeout(self.inner.grace_period + Duration::from_secs(2), async {
            loop {
                if self.inner.active.lock().await.is_empty() {
                    return;
                }
                tokio::time::sleep(Duration::from_millis(25)).await;
            }
        })
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
        mut cancel_rx: oneshot::Receiver<()>,
    ) -> std::result::Result<RunResult, RunFailure> {
        let permit = tokio::select! {
            permit = self.inner.slots.clone().acquire_owned() => permit.map_err(|_| RunFailure::new(run.run_id, "supervisor_stopped"))?,
            _ = &mut cancel_rx => return self.finish_without_process(run.run_id, "canceled", None).await,
        };
        let _permit = permit;
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
        let token_hash = Sha256::digest(environment.run_token.as_bytes()).to_vec();
        sqlx::query(
            "UPDATE local_agent_runs SET status = 'running', run_token_hash = ?2, started_at = ?3, \
             token_expires_at = ?4 \
             WHERE run_id = ?1 AND status = 'queued'",
        )
        .bind(run.run_id.to_string())
        .bind(token_hash)
        .bind(OffsetDateTime::now_utc().to_string())
        .bind(
            (OffsetDateTime::now_utc()
                + time::Duration::seconds(
                    self.inner.timeout.as_secs() as i64 + self.inner.grace_period.as_secs() as i64,
                ))
            .to_string(),
        )
        .execute(&self.inner.database)
        .await
        .map_err(|_| RunFailure::new(run.run_id, "local_state_failed"))?;
        let mut process = driver
            .start(DriverRun {
                run_id: run.run_id,
                prompt: run.prompt,
                environment: environment.clone(),
            })
            .await
            .map_err(|_| RunFailure::new(run.run_id, "driver_start_failed"))?;
        sqlx::query("UPDATE local_agent_runs SET process_id = ?2 WHERE run_id = ?1")
            .bind(run.run_id.to_string())
            .bind(process.pid().map(i64::from))
            .execute(&self.inner.database)
            .await
            .map_err(|_| RunFailure::new(run.run_id, "local_state_failed"))?;

        let (event_tx, mut event_rx) = mpsc::channel(64);
        event_tx
            .send(crate::driver::DriverEvent::ProcessStarted)
            .await
            .ok();
        let event_run_id = run.run_id;
        let event_task = tokio::spawn(async move {
            while let Some(event) = event_rx.recv().await {
                tracing::debug!(run_id = %event_run_id, event = ?event, "Driver event");
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
            driver
                .cancel(&mut process, self.inner.grace_period)
                .await
                .map_err(|_| RunFailure::new(run.run_id, "driver_cancel_failed"))?;
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
        self.finish_without_process(run.run_id, outcome.0, outcome.1)
            .await
    }

    async fn finish_without_process(
        &self,
        run_id: Uuid,
        status: &str,
        error_code: Option<&str>,
    ) -> std::result::Result<RunResult, RunFailure> {
        sqlx::query(
            "UPDATE local_agent_runs SET status = ?2, process_id = NULL, finished_at = ?3, \
             last_error_code = ?4 WHERE run_id = ?1",
        )
        .bind(run_id.to_string())
        .bind(status)
        .bind(OffsetDateTime::now_utc().to_string())
        .bind(error_code)
        .execute(&self.inner.database)
        .await
        .map_err(|_| RunFailure::new(run_id, "local_state_failed"))?;
        Ok(RunResult {
            run_id,
            status: status.to_owned(),
            error_code: error_code.map(str::to_owned),
        })
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
            codex_api_key: None,
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
            profile.get("status").and_then(serde_json::Value::as_str) == Some("active"),
            "Agent is not active"
        );
        Ok(())
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

pub async fn recover_interrupted_runs(database: &SqlitePool) -> Result<()> {
    let now = OffsetDateTime::now_utc().to_string();
    sqlx::query(
        "UPDATE local_agent_runs SET status = 'failed', process_id = NULL, finished_at = ?1, \
         last_error_code = 'process_lost', server_recovery_reported_at = NULL \
         WHERE status IN ('queued', 'running')",
    )
    .bind(&now)
    .execute(database)
    .await?;
    sqlx::query(
        "UPDATE server_commands SET status = 'failed', result_json = \
         '{\"ok\":false,\"error_code\":\"process_lost\"}', completed_at = ?1 \
         WHERE status = 'running'",
    )
    .bind(&now)
    .execute(database)
    .await?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::{os::unix::fs::PermissionsExt, sync::Arc};

    use crate::{database, driver::codex::CodexDriver};

    use super::*;

    async fn fixture(
        max_runs: usize,
        timeout_seconds: u64,
    ) -> (tempfile::TempDir, tempfile::TempDir, Supervisor, Uuid, Uuid) {
        let root = tempfile::tempdir().unwrap();
        let tools = tempfile::tempdir().unwrap();
        let state = root.path().join("computer");
        std::fs::create_dir_all(state.join("agents")).unwrap();
        let first = Uuid::now_v7();
        let second = Uuid::now_v7();
        for agent_id in [first, second] {
            let home = state.join("agents").join(agent_id.to_string());
            for relative in ["workspace", "memory", "runs", "drivers/codex"] {
                std::fs::create_dir_all(home.join(relative)).unwrap();
            }
            std::fs::write(home.join("profile.json"), r#"{"status":"active"}"#).unwrap();
        }
        std::fs::write(state.join("daemon.sock"), "").unwrap();
        let executable = tools.path().join("fake-codex");
        std::fs::write(
            &executable,
            "#!/bin/sh\ntest \"${1:-}\" = \"--version\" && exit 0\nIFS= read -r prompt\nprintf '%s\\n' '{\"type\":\"thread.started\"}'\ncase \"$prompt\" in hold) sleep 1 ;; timeout) sleep 5 ;; esac\nprintf '%s\\n' '{\"type\":\"turn.completed\"}'\n",
        )
        .unwrap();
        std::fs::set_permissions(&executable, std::fs::Permissions::from_mode(0o700)).unwrap();
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
            Arc::new(CodexDriver::with_executable(executable)),
        );
        (root, tools, supervisor, first, second)
    }

    fn run(agent_id: Uuid, prompt: &str) -> StartRun {
        StartRun {
            run_id: Uuid::now_v7(),
            agent_id,
            space_id: Uuid::now_v7(),
            prompt: prompt.into(),
            driver_kind: "codex".to_owned(),
        }
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
        let (_root, _tools, supervisor, first, second) = fixture(1, 10).await;
        let first_run = run(first, "hold");
        let first_id = first_run.run_id;
        let first_result = supervisor.start(first_run).await.unwrap();
        let second_run = run(second, "quick");
        let second_id = second_run.run_id;
        let second_result = supervisor.start(second_run).await.unwrap();
        assert!(supervisor.start(run(first, "quick")).await.is_err());
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
        let (_root, _tools, supervisor, first, second) = fixture(2, 1).await;
        let canceled = run(first, "timeout");
        let canceled_id = canceled.run_id;
        let canceled_result = supervisor.start(canceled).await.unwrap();
        while status(&supervisor, canceled_id).await != "running" {
            tokio::task::yield_now().await;
        }
        supervisor.cancel(canceled_id).await.unwrap();
        assert_eq!(canceled_result.await.unwrap().status, "canceled");
        assert_eq!(status(&supervisor, canceled_id).await, "canceled");

        let timed_out = run(second, "timeout");
        let timed_out_id = timed_out.run_id;
        let result = supervisor.start(timed_out).await.unwrap().await.unwrap();
        assert_eq!(result.status, "failed");
        assert_eq!(result.error_code.as_deref(), Some("run_timeout"));
        assert_eq!(status(&supervisor, timed_out_id).await, "failed");
    }

    #[tokio::test]
    async fn shutdown_cancels_all_active_runs_before_returning() {
        let (_root, _tools, supervisor, first, second) = fixture(2, 10).await;
        let first_run = run(first, "timeout");
        let first_id = first_run.run_id;
        let first_result = supervisor.start(first_run).await.unwrap();
        let second_run = run(second, "timeout");
        let second_id = second_run.run_id;
        let second_result = supervisor.start(second_run).await.unwrap();
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
    }
}
