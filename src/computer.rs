use std::{path::Path, sync::Arc};

use anyhow::{Context, Result, bail, ensure};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use futures_util::{SinkExt, StreamExt};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use sqlx::SqlitePool;
use time::OffsetDateTime;
use tokio_tungstenite::tungstenite::{self, client::IntoClientRequest};
use url::Url;
use uuid::Uuid;

use crate::{
    cli::ComputerArgs,
    config, database,
    driver::builtin_config::{self, BuiltinAuthentication},
    driver::codex::CodexDriver,
    supervisor::{RunResult, StartRun, Supervisor},
};

mod local_ipc;

#[derive(Deserialize, Serialize)]
#[serde(transparent)]
struct ComputerToken(String);

impl ComputerToken {
    fn expose(&self) -> &str {
        &self.0
    }
}

#[derive(Deserialize, Serialize)]
struct ComputerSecrets {
    schema_version: u32,
    token: ComputerToken,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    builtin_auth: Option<BuiltinAuthentication>,
    pairing_id: Option<Uuid>,
    computer_id: Option<Uuid>,
    space_id: Option<Uuid>,
}

#[derive(Serialize)]
struct PairingStartRequest {
    token_hash: String,
    hostname: String,
    os: String,
    daemon_version: String,
}

#[derive(Deserialize)]
struct PairingStartResponse {
    pairing_id: Uuid,
    browser_path: String,
    #[serde(with = "time::serde::rfc3339")]
    expires_at: OffsetDateTime,
}

#[derive(Deserialize)]
struct HostedAgent {
    member_id: Uuid,
    status: String,
}

#[derive(Deserialize)]
struct AgentClaimResponse {
    claimed: bool,
    run_id: Option<Uuid>,
    inbox_item_ids: Vec<Uuid>,
}

#[derive(Deserialize)]
#[serde(tag = "status", rename_all = "snake_case")]
enum PairingResultResponse {
    Pending,
    Confirmed { computer_id: Uuid, space_id: Uuid },
}

enum PairingPollOutcome {
    Confirmed,
    Expired,
    Shutdown,
}

#[derive(Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
enum ComputerFrame {
    Hello {
        last_acked_computer_seq: i64,
    },
    Heartbeat {
        daemon_version: &'static str,
        os: &'static str,
        cpu_count: usize,
        memory_total_bytes: Option<u64>,
        agents_count: u32,
        active_runs: u32,
    },
    CommandAck {
        command_id: Uuid,
        computer_seq: i64,
    },
    CommandResult {
        command_id: Uuid,
        computer_seq: i64,
        ok: bool,
        result: serde_json::Value,
    },
}

#[derive(Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
enum ServerFrame {
    Welcome {
        heartbeat_interval_seconds: u64,
    },
    Command {
        command_id: Uuid,
        computer_seq: i64,
        kind: String,
        payload: serde_json::Value,
    },
    Shutdown {
        reason: String,
    },
}

pub async fn run(args: ComputerArgs) -> Result<()> {
    let mut config = config::load(args.config.as_ref())?;
    if let Some(server_url) = args.server {
        config.computer.server_url = server_url;
    }
    prepare_state_dir(&config.computer.state_dir).await?;
    let runtime_dir = config::runtime_dir_for(&config.computer.state_dir);
    prepare_state_dir(&runtime_dir).await?;
    let database_path = config.computer.state_dir.join("daemon.db");
    let database = database::connect_sqlite(&database_path).await?;
    crate::supervisor::recover_interrupted_runs(&database).await?;
    prepare_agent_root(&config.computer.state_dir).await?;
    let secrets_path = config.computer.state_dir.join("secrets.json");
    let mut secrets = load_or_create_secrets(&secrets_path).await?;
    let builtin_provider = builtin_config::load(&config.computer)?;
    let builtin_auth = builtin_provider
        .as_ref()
        .map(|provider| provider.authentication().clone());
    sync_builtin_auth(&secrets_path, &mut secrets, builtin_auth).await?;
    if secrets.computer_id.is_none() {
        loop {
            if secrets.pairing_id.is_none() {
                let pairing = start_pairing(&config.computer.server_url, &secrets).await?;
                secrets.pairing_id = Some(pairing.pairing_id);
                write_secrets(&secrets_path, &secrets).await?;
                let browser_url = config
                    .computer
                    .server_url
                    .join(&pairing.browser_path)
                    .context("Server returned an invalid pairing Browser path")?;
                tracing::info!(url = %browser_url, expires_at = %pairing.expires_at, "Open this URL to pair the Computer");
                if config.computer.open_pairing_browser {
                    try_open_browser(&browser_url).await;
                }
            }
            match poll_pairing(&config.computer.server_url, &secrets_path, &mut secrets).await? {
                PairingPollOutcome::Confirmed => break,
                PairingPollOutcome::Expired => {
                    secrets.pairing_id = None;
                    write_secrets(&secrets_path, &secrets).await?;
                }
                PairingPollOutcome::Shutdown => return Ok(()),
            }
        }
    }

    tracing::info!(
        computer_id = %secrets.computer_id.context("paired Computer has no id")?,
        server = %config.computer.server_url,
        state_dir = %config.computer.state_dir.display(),
        "Computer daemon initialized"
    );
    let socket_path = runtime_dir.join("daemon.sock");
    let supervisor = Supervisor::new(
        database.clone(),
        config.computer.state_dir.clone(),
        socket_path.clone(),
        &config.computer,
        Arc::new(CodexDriver::new()),
        builtin_provider.map(|provider| provider.into_provider_config()),
    );
    resume_received_commands(&database, &config.computer.state_dir, &supervisor).await?;
    let ipc = local_ipc::run(
        socket_path.clone(),
        config.computer.state_dir.clone(),
        database.clone(),
        config.computer.server_url.clone(),
        secrets.computer_id.context("paired Computer has no id")?,
        secrets.token.expose().to_owned(),
    );
    let connection = connection_loop(
        &config.computer.server_url,
        &config.computer.state_dir,
        &secrets,
        &database,
        supervisor.clone(),
    );
    let result: Result<DaemonExit> = tokio::select! {
        result = ipc => result.map(|()| DaemonExit::Shutdown),
        result = connection => result,
    };
    supervisor.shutdown().await?;
    let _ = tokio::fs::remove_file(socket_path).await;
    let exit = result?;
    if exit == DaemonExit::Deleted {
        reset_deleted_identity(&database, &secrets_path).await?;
    }
    Ok(())
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum DaemonExit {
    Shutdown,
    Deleted,
}

async fn reset_deleted_identity(database: &SqlitePool, secrets_path: &Path) -> Result<()> {
    let mut transaction = database.begin().await?;
    sqlx::query("DELETE FROM server_commands")
        .execute(&mut *transaction)
        .await?;
    sqlx::query("DELETE FROM local_agent_runs")
        .execute(&mut *transaction)
        .await?;
    sqlx::query("DELETE FROM daemon_metadata")
        .execute(&mut *transaction)
        .await?;
    transaction.commit().await?;
    match tokio::fs::remove_file(secrets_path).await {
        Ok(()) => {}
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
        Err(error) => return Err(error).context("failed to remove deleted Computer identity"),
    }
    tracing::info!("Deleted Computer identity cleared; next start will begin pairing");
    Ok(())
}

async fn prepare_agent_root(state_dir: &Path) -> Result<()> {
    let agents = state_dir.join("agents");
    if !agents.exists() {
        tokio::fs::create_dir(&agents).await?;
        set_permissions(&agents, 0o700).await?;
    }
    let metadata = tokio::fs::metadata(&agents).await?;
    ensure_secure_permissions(&agents, &metadata, 0o700, "Computer Agents directory")
}

async fn prepare_agent_home(state_dir: &Path, agent_id: Uuid) -> Result<std::path::PathBuf> {
    let home = state_dir.join("agents").join(agent_id.to_string());
    tokio::fs::create_dir_all(&home).await?;
    set_permissions(&home, 0o700).await?;
    for relative in [
        "memory",
        "workspace",
        "drivers/codex",
        "drivers/builtin",
        "runs",
        "logs",
    ] {
        let path = home.join(relative);
        tokio::fs::create_dir_all(&path).await?;
        set_permissions(&path, 0o700).await?;
    }
    Ok(home)
}

async fn connection_loop(
    server: &Url,
    state_dir: &Path,
    secrets: &ComputerSecrets,
    database: &SqlitePool,
    supervisor: Supervisor,
) -> Result<DaemonExit> {
    let mut attempt = 0_u32;
    loop {
        match connect_once(server, state_dir, secrets, database, supervisor.clone()).await {
            Ok(ConnectionOutcome::Shutdown) => return Ok(DaemonExit::Shutdown),
            Ok(ConnectionOutcome::Deleted) => return Ok(DaemonExit::Deleted),
            Ok(ConnectionOutcome::Disconnected) => attempt = 0,
            Err(error) => tracing::warn!(
                computer_id = %secrets.computer_id.context("paired Computer has no id")?,
                error = %error,
                "Computer connection failed"
            ),
        }
        let delay = reconnect_delay(attempt);
        attempt = attempt.saturating_add(1);
        tracing::info!(delay_ms = delay.as_millis(), "Reconnecting Computer");
        tokio::select! {
            _ = tokio::time::sleep(delay) => {}
            signal = tokio::signal::ctrl_c() => {
                signal.context("failed to install shutdown signal handler")?;
                return Ok(DaemonExit::Shutdown);
            }
        }
    }
}

enum ConnectionOutcome {
    Disconnected,
    Shutdown,
    Deleted,
}

async fn connect_once(
    server: &Url,
    state_dir: &Path,
    secrets: &ComputerSecrets,
    database: &SqlitePool,
    supervisor: Supervisor,
) -> Result<ConnectionOutcome> {
    let computer_id = secrets.computer_id.context("paired Computer has no id")?;
    tracing::info!(computer_id = %computer_id, status = "connecting", "Computer connecting");
    let mut endpoint = server.join(&format!("/api/v1/computers/{computer_id}/connect"))?;
    match endpoint.scheme() {
        "http" => endpoint.set_scheme("ws").expect("ws is a valid URL scheme"),
        "https" => endpoint
            .set_scheme("wss")
            .expect("wss is a valid URL scheme"),
        scheme => bail!("unsupported Sumi Server URL scheme: {scheme}"),
    }
    let mut request = endpoint.as_str().into_client_request()?;
    request.headers_mut().insert(
        tungstenite::http::header::AUTHORIZATION,
        format!("Bearer {}", secrets.token.expose()).parse()?,
    );
    let (socket, _) = match tokio_tungstenite::connect_async(request).await {
        Ok(connection) => connection,
        Err(tokio_tungstenite::tungstenite::Error::Http(response))
            if response.status() == tungstenite::http::StatusCode::UNAUTHORIZED
                || response.status() == tungstenite::http::StatusCode::NOT_FOUND =>
        {
            tracing::error!(computer_id = %computer_id, "Computer Token is no longer valid; exiting daemon");
            return Ok(ConnectionOutcome::Deleted);
        }
        Err(error) => return Err(error).context("failed to connect Computer WebSocket"),
    };
    // Release lost runs before command replay can redeliver their persisted agent.run commands.
    release_interrupted_runs(server, computer_id, secrets.token.expose(), database).await?;
    let (mut writer, mut reader) = socket.split();
    let last_acked = last_acked_sequence(database).await?;
    send_ws_frame(
        &mut writer,
        &ComputerFrame::Hello {
            last_acked_computer_seq: last_acked,
        },
    )
    .await?;
    let welcome = reader
        .next()
        .await
        .context("Server closed before Computer welcome")??;
    let heartbeat_seconds = match decode_server_frame(welcome)? {
        ServerFrame::Welcome {
            heartbeat_interval_seconds,
        } if heartbeat_interval_seconds > 0 => heartbeat_interval_seconds,
        _ => bail!("Server did not send a valid Computer welcome"),
    };
    tracing::info!(computer_id = %computer_id, "Computer connected");
    let mut heartbeat = tokio::time::interval(std::time::Duration::from_secs(heartbeat_seconds));
    heartbeat.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    heartbeat.tick().await;
    let mut attention = tokio::time::interval(std::time::Duration::from_secs(1));
    attention.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    attention.tick().await;
    let mut lease_renewal = tokio::time::interval(std::time::Duration::from_secs(60));
    lease_renewal.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    lease_renewal.tick().await;
    let (completion_tx, mut completion_rx) = tokio::sync::mpsc::channel::<CommandCompletion>(64);
    let command_processor = LocalCommandProcessor {
        database: database.clone(),
        state_dir: state_dir.to_owned(),
        supervisor,
        completion_tx,
    };

    loop {
        tokio::select! {
            _ = heartbeat.tick() => {
                let (agents_count, active_runs) = command_processor.supervisor.counts().await?;
                send_ws_frame(&mut writer, &ComputerFrame::Heartbeat {
                    daemon_version: env!("CARGO_PKG_VERSION"),
                    os: platform_os()?,
                    cpu_count: std::thread::available_parallelism().map(usize::from).unwrap_or(1),
                    memory_total_bytes: None,
                    agents_count,
                    active_runs,
                }).await?;
                tracing::debug!(computer_id = %computer_id, agents_count, active_runs, "Computer heartbeat sent");
            }
            _ = attention.tick() => {
                if let Err(error) = poll_agent_inbox(server, computer_id, secrets.token.expose()).await {
                    tracing::warn!(computer_id = %computer_id, error = %error, "Agent Inbox poll failed");
                }
            }
            _ = lease_renewal.tick() => {
                if let Err(error) = renew_active_run_leases(
                    server,
                    computer_id,
                    secrets.token.expose(),
                    database,
                ).await {
                    tracing::warn!(computer_id = %computer_id, error = %error, "Agent Inbox lease renewal failed");
                }
            }
            completion = completion_rx.recv() => {
                let completion = completion.context("Computer command completion channel closed")?;
                let error_code = command_error_code(&completion.outcome).to_owned();
                let ok = completion.outcome.ok;
                send_ws_frame(&mut writer, &ComputerFrame::CommandResult {
                    command_id: completion.command_id,
                    computer_seq: completion.computer_seq,
                    ok,
                    result: completion.outcome.result,
                }).await?;
                tracing::info!(
                    computer_id = %computer_id,
                    command_id = %completion.command_id,
                    computer_seq = completion.computer_seq,
                    ok,
                    error_code,
                    "Computer command result sent"
                );
            }
            message = reader.next() => {
                let Some(message) = message else {
                    tracing::info!(computer_id = %computer_id, status = "disconnected", reason = "stream_ended", "Computer disconnected");
                    return Ok(ConnectionOutcome::Disconnected);
                };
                match message? {
                    tungstenite::Message::Text(text) => {
                        match serde_json::from_str(&text).context("Server sent an invalid Computer frame")? {
                            ServerFrame::Command { command_id, computer_seq, kind, payload } => {
                                let context = command_log_context(&payload);
                                tracing::info!(
                                    computer_id = %computer_id,
                                    command_id = %command_id,
                                    computer_seq,
                                    kind,
                                    agent_member_id = context.agent_member_id.map(|id| id.to_string()).as_deref().unwrap_or("none"),
                                    run_id = context.run_id.map(|id| id.to_string()).as_deref().unwrap_or("none"),
                                    "Computer command received"
                                );
                                persist_command(database, command_id, computer_seq, &kind, &payload).await?;
                                send_ws_frame(&mut writer, &ComputerFrame::CommandAck { command_id, computer_seq }).await?;
                                tracing::info!(computer_id = %computer_id, command_id = %command_id, computer_seq, kind, "Computer command acknowledged");
                                if let Some(outcome) = command_processor.process(command_id, computer_seq, &kind, &payload).await? {
                                    let error_code = command_error_code(&outcome).to_owned();
                                    let ok = outcome.ok;
                                    send_ws_frame(&mut writer, &ComputerFrame::CommandResult { command_id, computer_seq, ok, result: outcome.result }).await?;
                                    tracing::info!(computer_id = %computer_id, command_id = %command_id, computer_seq, kind, ok, error_code, "Computer command result sent");
                                }
                            }
                            ServerFrame::Shutdown { reason } => {
                                tracing::info!(computer_id = %computer_id, reason = %reason, "Server requested Computer shutdown");
                                return Ok(if reason == "computer_deleted" {
                                    ConnectionOutcome::Deleted
                                } else {
                                    ConnectionOutcome::Shutdown
                                });
                            }
                            ServerFrame::Welcome { .. } => {}
                        }
                    }
                    tungstenite::Message::Ping(bytes) => writer.send(tungstenite::Message::Pong(bytes)).await?,
                    tungstenite::Message::Close(_) => {
                        tracing::info!(computer_id = %computer_id, status = "disconnected", reason = "server_closed", "Computer disconnected");
                        return Ok(ConnectionOutcome::Disconnected);
                    }
                    tungstenite::Message::Binary(_) | tungstenite::Message::Pong(_) | tungstenite::Message::Frame(_) => {}
                }
            }
            signal = tokio::signal::ctrl_c() => {
                signal.context("failed to install shutdown signal handler")?;
                let _ = writer.send(tungstenite::Message::Close(None)).await;
                return Ok(ConnectionOutcome::Shutdown);
            }
        }
    }
}

async fn poll_agent_inbox(server: &Url, computer_id: Uuid, token: &str) -> Result<()> {
    let client = reqwest::Client::new();
    let agents: Vec<HostedAgent> = client
        .get(server.join(&format!("/api/v1/computers/{computer_id}/agents"))?)
        .bearer_auth(token)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    for agent in agents.into_iter().filter(|agent| agent.status == "active") {
        let claim: AgentClaimResponse = client
            .post(server.join(&format!(
                "/api/v1/computers/{computer_id}/agents/{}/inbox/claim",
                agent.member_id
            ))?)
            .bearer_auth(token)
            .json(&serde_json::json!({}))
            .send()
            .await?
            .error_for_status()?
            .json()
            .await?;
        if claim.claimed {
            tracing::info!(
                computer_id = %computer_id,
                agent_member_id = %agent.member_id,
                run_id = claim.run_id.map(|id| id.to_string()).as_deref().unwrap_or("none"),
                inbox_items_count = claim.inbox_item_ids.len(),
                "Agent Inbox claimed"
            );
        }
    }
    Ok(())
}

async fn renew_active_run_leases(
    server: &Url,
    computer_id: Uuid,
    token: &str,
    database: &SqlitePool,
) -> Result<()> {
    let runs: Vec<(String, String)> = sqlx::query_as(
        "SELECT run_id, agent_member_id FROM local_agent_runs \
         WHERE status IN ('queued', 'running') ORDER BY run_id",
    )
    .fetch_all(database)
    .await?;
    let client = reqwest::Client::new();
    for (run_id, agent_id) in runs {
        let run_id = Uuid::parse_str(&run_id)?;
        let agent_id = Uuid::parse_str(&agent_id)?;
        let response = client
            .post(server.join(&format!(
                "/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/renew"
            ))?)
            .bearer_auth(token)
            .json(&serde_json::json!({ "run_id": run_id }))
            .send()
            .await;
        match response.and_then(reqwest::Response::error_for_status) {
            Ok(_) => {
                tracing::debug!(computer_id = %computer_id, agent_member_id = %agent_id, run_id = %run_id, "Agent Inbox lease renewed")
            }
            Err(_) => {
                tracing::warn!(computer_id = %computer_id, agent_member_id = %agent_id, run_id = %run_id, error_code = "lease_renew_failed", "Agent Inbox lease renewal failed")
            }
        }
    }
    Ok(())
}

async fn release_interrupted_runs(
    server: &Url,
    computer_id: Uuid,
    token: &str,
    database: &SqlitePool,
) -> Result<()> {
    let runs: Vec<(String, String)> = sqlx::query_as(
        "SELECT run_id, agent_member_id FROM local_agent_runs \
         WHERE status = 'failed' AND last_error_code = 'process_lost' \
           AND server_recovery_reported_at IS NULL ORDER BY run_id",
    )
    .fetch_all(database)
    .await?;
    let client = reqwest::Client::new();
    for (run_id, agent_id) in runs {
        let run_id = Uuid::parse_str(&run_id)?;
        let agent_id = Uuid::parse_str(&agent_id)?;
        client
            .post(server.join(&format!(
                "/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/release"
            ))?)
            .bearer_auth(token)
            .json(&serde_json::json!({
                "run_id": run_id,
                "error_code": "process_lost",
            }))
            .send()
            .await?
            .error_for_status()?;
        tracing::info!(
            computer_id = %computer_id,
            agent_member_id = %agent_id,
            run_id = %run_id,
            error_code = "process_lost",
            "Interrupted Agent run lease released"
        );
        sqlx::query(
            "UPDATE local_agent_runs SET server_recovery_reported_at = ?2 WHERE run_id = ?1",
        )
        .bind(run_id.to_string())
        .bind(OffsetDateTime::now_utc().to_string())
        .execute(database)
        .await?;
    }
    Ok(())
}

struct LocalCommandOutcome {
    ok: bool,
    result: serde_json::Value,
}

#[derive(Debug, PartialEq, Eq)]
struct CommandLogContext {
    agent_member_id: Option<Uuid>,
    run_id: Option<Uuid>,
}

fn command_log_context(payload: &serde_json::Value) -> CommandLogContext {
    let parse_id = |name: &str| {
        payload
            .get(name)
            .and_then(serde_json::Value::as_str)
            .and_then(|value| Uuid::parse_str(value).ok())
    };
    CommandLogContext {
        agent_member_id: parse_id("agent_id").or_else(|| parse_id("agent_member_id")),
        run_id: parse_id("run_id"),
    }
}

fn command_error_code(outcome: &LocalCommandOutcome) -> &str {
    outcome
        .result
        .get("error_code")
        .and_then(serde_json::Value::as_str)
        .unwrap_or("none")
}

struct CommandCompletion {
    command_id: Uuid,
    computer_seq: i64,
    outcome: LocalCommandOutcome,
}

struct LocalCommandProcessor {
    database: SqlitePool,
    state_dir: std::path::PathBuf,
    supervisor: Supervisor,
    completion_tx: tokio::sync::mpsc::Sender<CommandCompletion>,
}

impl LocalCommandProcessor {
    async fn process(
        &self,
        command_id: Uuid,
        computer_seq: i64,
        kind: &str,
        payload: &serde_json::Value,
    ) -> Result<Option<LocalCommandOutcome>> {
        let existing: (String, Option<String>) =
            sqlx::query_as("SELECT status, result_json FROM server_commands WHERE command_id = ?1")
                .bind(command_id.to_string())
                .fetch_one(&self.database)
                .await?;
        if matches!(existing.0.as_str(), "completed" | "failed") {
            tracing::info!(command_id = %command_id, computer_seq, kind, replayed = true, status = %existing.0, "Computer command replayed from local result");
            return Ok(Some(LocalCommandOutcome {
                ok: existing.0 == "completed",
                result: serde_json::from_str(existing.1.as_deref().unwrap_or("{}"))?,
            }));
        }
        if existing.0 == "running" {
            tracing::info!(command_id = %command_id, computer_seq, kind, replayed = true, status = "running", "Computer command already running");
            return Ok(None);
        }
        if kind == "agent.memory.read" {
            let outcome = match read_memory_file(&self.state_dir, payload).await {
                Ok(file) => LocalCommandOutcome {
                    ok: true,
                    result: serde_json::to_value(file)?,
                },
                Err(_) => LocalCommandOutcome {
                    ok: false,
                    result: serde_json::json!({
                        "ok": false,
                        "error_code": COMMAND_FAILED_ERROR_CODE,
                    }),
                },
            };
            finish_local_command_with_result(
                &self.database,
                command_id,
                &outcome,
                &serde_json::json!({ "ok": outcome.ok }),
            )
            .await?;
            return Ok(Some(outcome));
        }
        if kind == "agent.run" {
            let run: StartRun = serde_json::from_value(payload.clone())
                .context("agent.run command payload is invalid")?;
            let memory_root = self
                .state_dir
                .join("agents")
                .join(run.agent_id.to_string())
                .join("memory");
            sqlx::query("UPDATE server_commands SET status = 'running' WHERE command_id = ?1")
                .bind(command_id.to_string())
                .execute(&self.database)
                .await?;
            let result = self.supervisor.start(run).await;
            match result {
                Ok(receiver) => {
                    let database = self.database.clone();
                    let completion_tx = self.completion_tx.clone();
                    tokio::spawn(async move {
                        let result = receiver.await.unwrap_or(RunResult {
                            run_id: Uuid::nil(),
                            status: "failed".to_owned(),
                            error_code: Some("supervisor_stopped".to_owned()),
                        });
                        let mut outcome = command_outcome_for_run(&result);
                        match scan_memory(&memory_root).await {
                            Ok(memory_files) => {
                                outcome.result["memory_files"] = serde_json::json!(memory_files);
                            }
                            Err(error) => {
                                tracing::warn!(run_id = %result.run_id, error = %error, "Failed to scan Agent Memory after run");
                            }
                        }
                        if let Err(error) =
                            finish_local_command(&database, command_id, &outcome).await
                        {
                            tracing::error!(command_id = %command_id, error = %error, "Failed to persist Agent run result");
                            return;
                        }
                        completion_tx
                            .send(CommandCompletion {
                                command_id,
                                computer_seq,
                                outcome,
                            })
                            .await
                            .ok();
                    });
                    return Ok(None);
                }
                Err(_) => {
                    let outcome = LocalCommandOutcome {
                        ok: false,
                        result: serde_json::json!({ "ok": false, "error_code": COMMAND_FAILED_ERROR_CODE }),
                    };
                    finish_local_command(&self.database, command_id, &outcome).await?;
                    return Ok(Some(outcome));
                }
            }
        }
        if kind == "agent.cancel" {
            let run_id = payload
                .get("run_id")
                .and_then(serde_json::Value::as_str)
                .context("agent.cancel command has no run_id")
                .and_then(|value| {
                    Uuid::parse_str(value).context("agent.cancel run_id is invalid")
                })?;
            let result = self.supervisor.cancel(run_id).await;
            let outcome = match result {
                Ok(()) => LocalCommandOutcome {
                    ok: true,
                    result: serde_json::json!({ "ok": true }),
                },
                Err(_) => LocalCommandOutcome {
                    ok: false,
                    result: serde_json::json!({ "ok": false, "error_code": COMMAND_FAILED_ERROR_CODE }),
                },
            };
            finish_local_command(&self.database, command_id, &outcome).await?;
            return Ok(Some(outcome));
        }
        let result = execute_local_command(&self.state_dir, kind, payload, &self.supervisor).await;
        let result = validate_provision_result(result, kind, payload, &self.supervisor).await;
        let outcome = match result {
            Ok(memory_files) => LocalCommandOutcome {
                ok: true,
                result: serde_json::json!({ "ok": true, "memory_files": memory_files }),
            },
            Err(_) => LocalCommandOutcome {
                ok: false,
                result: serde_json::json!({ "ok": false, "error_code": COMMAND_FAILED_ERROR_CODE }),
            },
        };
        finish_local_command(&self.database, command_id, &outcome).await?;
        Ok(Some(outcome))
    }
}

fn command_outcome_for_run(result: &RunResult) -> LocalCommandOutcome {
    LocalCommandOutcome {
        ok: result.status == "completed",
        result: serde_json::json!({
            "ok": result.status == "completed",
            "run_id": result.run_id,
            "status": result.status,
            "error_code": result.error_code,
        }),
    }
}

async fn finish_local_command(
    database: &SqlitePool,
    command_id: Uuid,
    outcome: &LocalCommandOutcome,
) -> Result<()> {
    finish_local_command_with_result(database, command_id, outcome, &outcome.result).await
}

async fn finish_local_command_with_result(
    database: &SqlitePool,
    command_id: Uuid,
    outcome: &LocalCommandOutcome,
    stored_result: &serde_json::Value,
) -> Result<()> {
    let status = if outcome.ok { "completed" } else { "failed" };
    sqlx::query(
        "UPDATE server_commands SET status = ?2, result_json = ?3, completed_at = ?4 \
         WHERE command_id = ?1",
    )
    .bind(command_id.to_string())
    .bind(status)
    .bind(serde_json::to_string(stored_result)?)
    .bind(OffsetDateTime::now_utc().to_string())
    .execute(database)
    .await?;
    Ok(())
}

const COMMAND_FAILED_ERROR_CODE: &str = "command_failed";

async fn execute_local_command(
    state_dir: &Path,
    kind: &str,
    payload: &serde_json::Value,
    supervisor: &Supervisor,
) -> Result<Vec<MemoryFileMetadata>> {
    if !matches!(
        kind,
        "agent.provision" | "agent.configure" | "agent.suspend" | "agent.resume" | "agent.retire"
    ) {
        bail!("unsupported Server command kind: {kind}");
    }
    let agent_id = payload
        .get("agent_id")
        .and_then(serde_json::Value::as_str)
        .context("Agent command has no agent_id")
        .and_then(|value| Uuid::parse_str(value).context("Agent command agent_id is invalid"))?;
    let home = if kind == "agent.provision" {
        let driver_kind = payload
            .get("driver_kind")
            .and_then(serde_json::Value::as_str)
            .context("agent.provision command has no driver_kind")?;
        ensure!(
            matches!(driver_kind, "codex" | "builtin"),
            "agent.provision command has unknown driver_kind"
        );
        let home = prepare_agent_home(state_dir, agent_id).await?;
        supervisor
            .prepare_agent_driver(agent_id, driver_kind)
            .await?;
        home
    } else {
        let home = state_dir.join("agents").join(agent_id.to_string());
        ensure!(home.is_dir(), "Agent Home is unavailable");
        home
    };
    let profile_path = home.join("profile.json");
    let mut profile = if kind == "agent.provision" {
        serde_json::json!({
            "schema_version": 1,
            "agent_id": agent_id,
            "space_id": payload.get("space_id"),
            "name": payload.get("name"),
            "handle": payload.get("handle"),
            "driver_kind": payload.get("driver_kind"),
            "driver_config": payload.get("driver_config"),
        })
    } else {
        serde_json::from_slice(&tokio::fs::read(&profile_path).await?)?
    };
    let profile = profile
        .as_object_mut()
        .context("Agent profile must be a JSON object")?;
    for field in ["role_text", "role_revision", "attention_config"] {
        if let Some(value) = payload.get(field) {
            profile.insert(field.to_owned(), value.clone());
        }
    }
    let status = match kind {
        "agent.provision" | "agent.resume" => "active",
        "agent.suspend" => "suspended",
        "agent.retire" => "retired",
        _ => profile
            .get("status")
            .and_then(serde_json::Value::as_str)
            .unwrap_or("active"),
    };
    profile.insert("status".to_owned(), serde_json::json!(status));
    write_restricted_file_atomic(&profile_path, &serde_json::to_vec_pretty(&profile)?).await?;
    let memory = home.join("memory/MEMORY.md");
    if !memory.exists() {
        write_restricted_file(&memory, b"# Memory\n").await?;
    }
    if matches!(kind, "agent.suspend" | "agent.retire")
        && (kind == "agent.retire"
            || payload.get("mode").and_then(serde_json::Value::as_str) == Some("cancel_now"))
    {
        supervisor.cancel_agent(agent_id).await?;
    }
    scan_memory(&home.join("memory")).await
}

#[derive(Serialize)]
struct MemoryFileMetadata {
    path: String,
    size: u64,
    sha256: String,
    #[serde(with = "time::serde::rfc3339")]
    updated_at: OffsetDateTime,
}

#[derive(Debug, Serialize)]
struct MemoryFileContent {
    path: String,
    content: String,
    size: u64,
    sha256: String,
    #[serde(with = "time::serde::rfc3339")]
    updated_at: OffsetDateTime,
}

async fn scan_memory(root: &Path) -> Result<Vec<MemoryFileMetadata>> {
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
            let modified = metadata.modified()?;
            files.push(MemoryFileMetadata {
                path: relative.to_owned(),
                size: metadata.len(),
                sha256: hex::encode(Sha256::digest(&bytes)),
                updated_at: OffsetDateTime::from(modified),
            });
        }
    }
    files.sort_by(|left, right| left.path.cmp(&right.path));
    Ok(files)
}

async fn read_memory_file(
    state_dir: &Path,
    payload: &serde_json::Value,
) -> Result<MemoryFileContent> {
    const MAX_MEMORY_READ_BYTES: u64 = 1024 * 1024;
    let agent_id = payload
        .get("agent_id")
        .and_then(serde_json::Value::as_str)
        .context("Memory command has no agent_id")
        .and_then(|value| Uuid::parse_str(value).context("Memory command agent_id is invalid"))?;
    let relative = payload
        .get("path")
        .and_then(serde_json::Value::as_str)
        .context("Memory command has no path")?;
    let relative_path = Path::new(relative);
    ensure!(
        !relative.is_empty()
            && relative_path
                .components()
                .all(|component| matches!(component, std::path::Component::Normal(_))),
        "Memory path is invalid"
    );
    let root = state_dir
        .join("agents")
        .join(agent_id.to_string())
        .join("memory");
    let canonical_root = tokio::fs::canonicalize(&root).await?;
    let mut candidate = root;
    for component in relative_path.components() {
        let std::path::Component::Normal(component) = component else {
            bail!("Memory path is invalid");
        };
        candidate.push(component);
        ensure!(
            !tokio::fs::symlink_metadata(&candidate)
                .await?
                .file_type()
                .is_symlink(),
            "Memory path cannot contain a symlink"
        );
    }
    let canonical = tokio::fs::canonicalize(&candidate).await?;
    ensure!(
        canonical.starts_with(&canonical_root),
        "Memory path escapes Agent Home"
    );
    let metadata = tokio::fs::metadata(&canonical).await?;
    ensure!(metadata.is_file(), "Memory path is not a regular file");
    ensure!(
        metadata.len() <= MAX_MEMORY_READ_BYTES,
        "Memory file is too large"
    );
    let bytes = tokio::fs::read(&canonical).await?;
    let content = String::from_utf8(bytes.clone()).context("Memory file is not UTF-8")?;
    Ok(MemoryFileContent {
        path: relative.to_owned(),
        content,
        size: metadata.len(),
        sha256: hex::encode(Sha256::digest(&bytes)),
        updated_at: OffsetDateTime::from(metadata.modified()?),
    })
}

async fn write_restricted_file(path: &Path, bytes: &[u8]) -> Result<()> {
    let mut options = tokio::fs::OpenOptions::new();
    options.create(true).truncate(true).write(true).mode(0o600);
    let mut file = options.open(path).await?;
    use tokio::io::AsyncWriteExt;
    file.write_all(bytes).await?;
    file.sync_all().await?;
    set_permissions(path, 0o600).await
}

async fn write_restricted_file_atomic(path: &Path, bytes: &[u8]) -> Result<()> {
    let parent = path.parent().context("restricted file has no parent")?;
    let temporary = parent.join(format!(".profile-{}.tmp", Uuid::now_v7()));
    write_restricted_file(&temporary, bytes).await?;
    tokio::fs::rename(&temporary, path).await?;
    Ok(())
}

async fn validate_provision_result(
    result: Result<Vec<MemoryFileMetadata>>,
    kind: &str,
    payload: &serde_json::Value,
    supervisor: &Supervisor,
) -> Result<Vec<MemoryFileMetadata>> {
    let memory_files = result?;
    if kind == "agent.provision" {
        let agent_id = payload
            .get("agent_id")
            .and_then(serde_json::Value::as_str)
            .context("agent.provision command has no agent_id")
            .and_then(|value| {
                Uuid::parse_str(value).context("agent.provision agent_id is invalid")
            })?;
        let driver_kind = payload
            .get("driver_kind")
            .and_then(serde_json::Value::as_str)
            .context("agent.provision command has no driver_kind")?;
        supervisor.validate_agent(agent_id, driver_kind).await?;
    }
    Ok(memory_files)
}

async fn resume_received_commands(
    database: &SqlitePool,
    state_dir: &Path,
    supervisor: &Supervisor,
) -> Result<()> {
    let commands: Vec<(String, String)> = sqlx::query_as(
        "SELECT command_id, request_json FROM server_commands \
         WHERE status = 'received' ORDER BY computer_seq",
    )
    .fetch_all(database)
    .await?;
    for (command_id, request_json) in commands {
        let command_id = Uuid::parse_str(&command_id)?;
        let request: serde_json::Value = serde_json::from_str(&request_json)?;
        let kind = request
            .get("kind")
            .and_then(serde_json::Value::as_str)
            .context("persisted command has no kind")?;
        let payload = request
            .get("payload")
            .context("persisted command has no payload")?;
        let result = execute_local_command(state_dir, kind, payload, supervisor).await;
        let result = validate_provision_result(result, kind, payload, supervisor).await;
        let outcome = match result {
            Ok(memory_files) => LocalCommandOutcome {
                ok: true,
                result: serde_json::json!({ "ok": true, "memory_files": memory_files }),
            },
            Err(_) => LocalCommandOutcome {
                ok: false,
                result: serde_json::json!({
                    "ok": false,
                    "error_code": COMMAND_FAILED_ERROR_CODE,
                }),
            },
        };
        finish_local_command(database, command_id, &outcome).await?;
    }
    Ok(())
}

fn decode_server_frame(message: tungstenite::Message) -> Result<ServerFrame> {
    match message {
        tungstenite::Message::Text(text) => {
            serde_json::from_str(&text).context("Server sent an invalid Computer frame")
        }
        _ => bail!("Server did not send a text Computer frame"),
    }
}

async fn send_ws_frame<S>(writer: &mut S, frame: &ComputerFrame) -> Result<()>
where
    S: futures_util::Sink<tungstenite::Message, Error = tungstenite::Error> + Unpin,
{
    writer
        .send(tungstenite::Message::Text(
            serde_json::to_string(frame)?.into(),
        ))
        .await?;
    Ok(())
}

async fn last_acked_sequence(database: &SqlitePool) -> Result<i64> {
    Ok(sqlx::query_scalar::<_, Option<i64>>(
        "SELECT max(computer_seq) FROM server_commands WHERE status IN ('received', 'running', 'completed', 'failed')",
    )
    .fetch_one(database)
    .await?
    .unwrap_or(0))
}

async fn persist_command(
    database: &SqlitePool,
    command_id: Uuid,
    computer_seq: i64,
    kind: &str,
    payload: &serde_json::Value,
) -> Result<()> {
    ensure!(computer_seq > 0, "Server command sequence must be positive");
    let request = serde_json::json!({ "kind": kind, "payload": payload });
    let inserted = sqlx::query(
        "INSERT INTO server_commands \
         (command_id, computer_seq, request_json, status, received_at) \
         VALUES (?1, ?2, ?3, 'received', ?4) ON CONFLICT(command_id) DO NOTHING",
    )
    .bind(command_id.to_string())
    .bind(computer_seq)
    .bind(serde_json::to_string(&request)?)
    .bind(OffsetDateTime::now_utc().to_string())
    .execute(database)
    .await?;
    if inserted.rows_affected() == 0 {
        let existing: (i64, String) = sqlx::query_as(
            "SELECT computer_seq, request_json FROM server_commands WHERE command_id = ?1",
        )
        .bind(command_id.to_string())
        .fetch_one(database)
        .await?;
        ensure!(
            existing.0 == computer_seq && existing.1 == serde_json::to_string(&request)?,
            "Server reused a command ID with different content"
        );
    }
    Ok(())
}

fn reconnect_delay(attempt: u32) -> std::time::Duration {
    let base_ms = 1_000_u64
        .saturating_mul(1_u64 << attempt.min(5))
        .min(30_000);
    let mut random = [0_u8; 2];
    let _ = getrandom::fill(&mut random);
    let jitter = u16::from_le_bytes(random) as u64 % (base_ms / 4 + 1);
    std::time::Duration::from_millis(base_ms + jitter)
}

async fn start_pairing(server: &Url, secrets: &ComputerSecrets) -> Result<PairingStartResponse> {
    let endpoint = server.join("/api/v1/computer-pairings/start")?;
    let response = reqwest::Client::new()
        .post(endpoint)
        .json(&PairingStartRequest {
            token_hash: URL_SAFE_NO_PAD.encode(Sha256::digest(secrets.token.expose().as_bytes())),
            hostname: hostname(),
            os: platform_os()?.to_owned(),
            daemon_version: env!("CARGO_PKG_VERSION").to_owned(),
        })
        .send()
        .await
        .context("failed to start Computer pairing")?;
    ensure!(
        response.status().is_success(),
        "Server rejected Computer pairing start: {}",
        response.status()
    );
    response
        .json()
        .await
        .context("Server returned an invalid Computer pairing response")
}

async fn try_open_browser(url: &Url) {
    let (program, argument) = match std::env::consts::OS {
        "macos" => ("open", url.as_str()),
        "linux" => ("xdg-open", url.as_str()),
        _ => return,
    };
    if let Err(error) = tokio::process::Command::new(program)
        .arg(argument)
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .spawn()
    {
        tracing::debug!(error = %error, "Could not open pairing URL automatically");
    }
}

async fn poll_pairing(
    server: &Url,
    secrets_path: &Path,
    secrets: &mut ComputerSecrets,
) -> Result<PairingPollOutcome> {
    let pairing_id = secrets
        .pairing_id
        .context("Computer pairing id is missing")?;
    let endpoint = server.join(&format!("/api/v1/computer-pairings/{pairing_id}/result"))?;
    let client = reqwest::Client::new();
    loop {
        let response = client
            .get(endpoint.clone())
            .bearer_auth(secrets.token.expose())
            .send()
            .await
            .context("failed to poll Computer pairing result")?;
        if response.status() == reqwest::StatusCode::GONE {
            return Ok(PairingPollOutcome::Expired);
        }
        ensure!(
            response.status().is_success(),
            "Server rejected Computer pairing result: {}",
            response.status()
        );
        match response.json::<PairingResultResponse>().await? {
            PairingResultResponse::Pending => {
                tokio::select! {
                    _ = tokio::time::sleep(std::time::Duration::from_secs(2)) => {}
                    signal = tokio::signal::ctrl_c() => {
                        signal.context("failed to install shutdown signal handler")?;
                        return Ok(PairingPollOutcome::Shutdown);
                    }
                }
            }
            PairingResultResponse::Confirmed {
                computer_id,
                space_id,
            } => {
                secrets.pairing_id = None;
                secrets.computer_id = Some(computer_id);
                secrets.space_id = Some(space_id);
                write_secrets(secrets_path, secrets).await?;
                return Ok(PairingPollOutcome::Confirmed);
            }
        }
    }
}

async fn prepare_state_dir(path: &Path) -> Result<()> {
    if !path.exists() {
        tokio::fs::create_dir_all(path)
            .await
            .context("failed to create Computer state directory")?;
        set_permissions(path, 0o700).await?;
    }
    let metadata = tokio::fs::metadata(path)
        .await
        .context("failed to inspect Computer state directory")?;
    ensure!(metadata.is_dir(), "Computer state path is not a directory");
    ensure_secure_permissions(path, &metadata, 0o700, "Computer state directory")
}

async fn load_or_create_secrets(path: &Path) -> Result<ComputerSecrets> {
    if path.exists() {
        let metadata = tokio::fs::metadata(path).await?;
        ensure_secure_permissions(path, &metadata, 0o600, "Computer secrets file")?;
        let bytes = tokio::fs::read(path)
            .await
            .context("failed to read Computer secrets")?;
        return serde_json::from_slice(&bytes).context("Computer secrets are invalid");
    }
    let mut token = [0_u8; 32];
    getrandom::fill(&mut token).context("failed to generate Computer Token")?;
    let secrets = ComputerSecrets {
        schema_version: 1,
        token: ComputerToken(URL_SAFE_NO_PAD.encode(token)),
        builtin_auth: None,
        pairing_id: None,
        computer_id: None,
        space_id: None,
    };
    write_secrets(path, &secrets).await?;
    Ok(secrets)
}

async fn sync_builtin_auth(
    path: &Path,
    secrets: &mut ComputerSecrets,
    authentication: Option<BuiltinAuthentication>,
) -> Result<()> {
    if secrets.builtin_auth != authentication {
        secrets.builtin_auth = authentication;
        write_secrets(path, secrets).await?;
    }
    Ok(())
}

async fn write_secrets(path: &Path, secrets: &ComputerSecrets) -> Result<()> {
    let parent = path
        .parent()
        .context("Computer secrets path has no parent")?;
    let temp_path = parent.join(format!(".secrets-{}.tmp", Uuid::now_v7()));
    let bytes = serde_json::to_vec(secrets)?;
    let mut options = tokio::fs::OpenOptions::new();
    options.create_new(true).write(true);
    #[cfg(unix)]
    {
        options.mode(0o600);
    }
    let mut file = options
        .open(&temp_path)
        .await
        .context("failed to create temporary Computer secrets")?;
    use tokio::io::AsyncWriteExt;
    file.write_all(&bytes).await?;
    file.sync_all().await?;
    drop(file);
    tokio::fs::rename(&temp_path, path)
        .await
        .context("failed to replace Computer secrets atomically")?;
    set_permissions(path, 0o600).await?;
    let directory = tokio::fs::File::open(parent).await?;
    directory.sync_all().await?;
    Ok(())
}

fn hostname() -> String {
    hostname::get()
        .ok()
        .and_then(|value| value.into_string().ok())
        .filter(|value| !value.trim().is_empty())
        .unwrap_or_else(|| "local-computer".to_owned())
}

fn platform_os() -> Result<&'static str> {
    match std::env::consts::OS {
        "macos" => Ok("macos"),
        "linux" => Ok("linux"),
        other => bail!("unsupported Computer operating system: {other}"),
    }
}

#[cfg(unix)]
fn ensure_secure_permissions(
    path: &Path,
    metadata: &std::fs::Metadata,
    expected: u32,
    label: &str,
) -> Result<()> {
    use std::os::unix::fs::PermissionsExt;
    let mode = metadata.permissions().mode() & 0o777;
    ensure!(
        mode == expected,
        "{label} {} must have mode {:04o}, found {:04o}; run chmod {:04o} {}",
        path.display(),
        expected,
        mode,
        expected,
        path.display()
    );
    Ok(())
}

#[cfg(unix)]
async fn set_permissions(path: &Path, mode: u32) -> Result<()> {
    use std::os::unix::fs::PermissionsExt;
    tokio::fs::set_permissions(path, std::fs::Permissions::from_mode(mode))
        .await
        .with_context(|| format!("failed to set permissions on {}", path.display()))
}

#[cfg(test)]
mod tests;
