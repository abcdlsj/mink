use std::{path::Path, sync::Arc, time::Duration};

use anyhow::{Context, Result, bail, ensure};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use futures_util::{SinkExt, StreamExt};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use sqlx::SqlitePool;
use time::OffsetDateTime;
use tokio::{sync::mpsc, task::JoinSet};
use tokio_tungstenite::tungstenite::{self, client::IntoClientRequest};
use tokio_util::sync::CancellationToken;
use url::Url;
use uuid::Uuid;

const ATTENTION_PREFETCH_RUNS: usize = 1;

use crate::{
    cli::ComputerArgs,
    computer_protocol::{
        AgentMemoryReadCommand, CommandResult, ComputerCommand, ComputerFrame, MemoryFileMetadata,
        RunResultStatus, ServerFrame, SuspendMode,
    },
    config, database,
    driver::builtin_config::{self, BuiltinAuthentication},
    driver::codex::CodexDriver,
    supervisor::{RunCommand, RunResult, Supervisor},
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
    desired_lifecycle: String,
    provision_status: String,
}

#[derive(Deserialize)]
struct AgentClaimResponse {
    claimed: bool,
    run_id: Option<Uuid>,
    inbox_item_ids: Vec<Uuid>,
}

#[derive(Deserialize)]
struct AgentLeaseResponse {
    #[serde(with = "time::serde::rfc3339")]
    ownership_lease_expires_at: OffsetDateTime,
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

pub async fn run(args: ComputerArgs) -> Result<()> {
    let uses_default_computer_root = args.config.is_none();
    let mut config = config::load(args.config.as_ref())?;
    if let Some(server_url) = args.server {
        config.computer.server_url = server_url;
    }
    let computer_root = (uses_default_computer_root
        && config.computer.state_dir == config::default_computer_state_dir())
    .then(|| config.computer.state_dir.clone());
    if let Some(root) = computer_root.as_ref() {
        config.computer.state_dir = resolve_default_computer_state_dir(root).await?;
    }
    prepare_state_dir(&config.computer.state_dir).await?;
    let database_path = config.computer.state_dir.join("daemon.db");
    let mut database = database::connect_sqlite(&database_path).await?;
    crate::supervisor::recover_interrupted_runs(&database).await?;
    prepare_agent_root(&config.computer.state_dir).await?;
    let mut secrets_path = config.computer.state_dir.join("secrets.json");
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

    if let Some(root) = computer_root.as_ref()
        && config
            .computer
            .state_dir
            .parent()
            .and_then(Path::file_name)
            .is_some_and(|name| name == "pending")
    {
        let space_id = secrets
            .space_id
            .context("paired Computer has no Space id")?;
        let computer_id = secrets
            .computer_id
            .context("paired Computer has no Computer id")?;
        let final_state_dir = root
            .join(space_id.to_string())
            .join(computer_id.to_string());
        ensure!(
            !final_state_dir.exists(),
            "Computer state directory already exists: {}",
            final_state_dir.display()
        );
        database.close().await;
        prepare_state_dir(
            final_state_dir
                .parent()
                .context("invalid Computer state path")?,
        )
        .await?;
        tokio::fs::rename(&config.computer.state_dir, &final_state_dir)
            .await
            .context("failed to move paired Computer state into its ID directory")?;
        config.computer.state_dir = final_state_dir;
        secrets_path = config.computer.state_dir.join("secrets.json");
        database = database::connect_sqlite(&config.computer.state_dir.join("daemon.db")).await?;
    }

    let runtime_dir = config::runtime_dir_for(&config.computer.state_dir);
    prepare_state_dir(&runtime_dir).await?;

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
    sqlx::query("DELETE FROM run_started_outbox")
        .execute(&mut *transaction)
        .await?;
    sqlx::query("DELETE FROM run_result_outbox")
        .execute(&mut *transaction)
        .await?;
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
    let http = daemon_http_client()?;
    // Release lost runs before command replay can redeliver their persisted agent.run commands.
    release_interrupted_runs(&http, server, computer_id, secrets.token.expose(), database).await?;
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
    let cancellation = CancellationToken::new();
    let (outgoing_tx, outgoing_rx) = mpsc::channel::<tungstenite::Message>(128);
    let (command_tx, command_rx) = mpsc::channel::<ReceivedCommand>(64);
    let (completion_tx, completion_rx) = mpsc::channel::<()>(64);
    let command_processor = LocalCommandProcessor {
        database: database.clone(),
        state_dir: state_dir.to_owned(),
        supervisor,
        completion_tx,
    };
    let heartbeat_supervisor = command_processor.supervisor.clone();
    let max_claimed_runs = command_processor
        .supervisor
        .max_concurrent_runs()
        .saturating_add(ATTENTION_PREFETCH_RUNS);
    let mut tasks = JoinSet::new();
    tasks.spawn(websocket_writer_task(
        writer,
        outgoing_rx,
        cancellation.child_token(),
    ));
    tasks.spawn(websocket_reader_task(
        reader,
        outgoing_tx.clone(),
        command_tx,
        database.clone(),
        computer_id,
        cancellation.child_token(),
    ));
    tasks.spawn(command_processor_task(
        command_processor,
        command_rx,
        outgoing_tx.clone(),
        computer_id,
        cancellation.child_token(),
    ));
    tasks.spawn(result_sender_task(
        database.clone(),
        computer_id,
        outgoing_tx.clone(),
        completion_rx,
        cancellation.child_token(),
    ));
    tasks.spawn(attention_scheduler_task(
        http.clone(),
        server.clone(),
        computer_id,
        secrets.token.expose().to_owned(),
        database.clone(),
        max_claimed_runs,
        cancellation.child_token(),
    ));
    tasks.spawn(lease_renewer_task(
        http,
        server.clone(),
        computer_id,
        secrets.token.expose().to_owned(),
        database.clone(),
        cancellation.child_token(),
    ));
    tasks.spawn(heartbeat_reporter_task(
        heartbeat_supervisor,
        computer_id,
        heartbeat_seconds,
        outgoing_tx.clone(),
        cancellation.child_token(),
    ));

    let outcome = tokio::select! {
        joined = wait_for_connection_task(&mut tasks) => joined,
        signal = tokio::signal::ctrl_c() => {
            signal.context("failed to install shutdown signal handler")?;
            Ok(ConnectionOutcome::Shutdown)
        }
    };
    cancellation.cancel();
    drop(outgoing_tx);
    while let Some(joined) = tasks.join_next().await {
        match joined {
            Ok(Err(error)) => {
                tracing::warn!(computer_id = %computer_id, error = %error, "Computer task failed during shutdown")
            }
            Err(error) => {
                tracing::warn!(computer_id = %computer_id, error = %error, "Computer task panicked during shutdown")
            }
            Ok(Ok(_)) => {}
        }
    }
    outcome
}

#[derive(Debug)]
struct ReceivedCommand {
    command_id: Uuid,
    computer_seq: i64,
    command: ComputerCommand,
}

#[derive(Debug)]
enum ConnectionTaskExit {
    Disconnected,
    Shutdown,
    Deleted,
    Cancelled,
}

async fn wait_for_connection_task(
    tasks: &mut JoinSet<Result<ConnectionTaskExit>>,
) -> Result<ConnectionOutcome> {
    loop {
        let joined = tasks
            .join_next()
            .await
            .context("Computer connection has no running tasks")?;
        match joined.context("Computer connection task panicked")?? {
            ConnectionTaskExit::Disconnected => return Ok(ConnectionOutcome::Disconnected),
            ConnectionTaskExit::Shutdown => return Ok(ConnectionOutcome::Shutdown),
            ConnectionTaskExit::Deleted => return Ok(ConnectionOutcome::Deleted),
            ConnectionTaskExit::Cancelled => {}
        }
    }
}

fn daemon_http_client() -> Result<reqwest::Client> {
    reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(5))
        .timeout(Duration::from_secs(15))
        .build()
        .context("failed to build Computer HTTP client")
}

fn encode_computer_frame(frame: &ComputerFrame) -> Result<tungstenite::Message> {
    Ok(tungstenite::Message::Text(
        serde_json::to_string(frame)?.into(),
    ))
}

async fn queue_computer_frame(
    outgoing: &mpsc::Sender<tungstenite::Message>,
    frame: &ComputerFrame,
) -> Result<()> {
    outgoing
        .send(encode_computer_frame(frame)?)
        .await
        .context("Computer WebSocket writer stopped")
}

async fn websocket_writer_task<W>(
    mut writer: W,
    mut outgoing: mpsc::Receiver<tungstenite::Message>,
    cancellation: CancellationToken,
) -> Result<ConnectionTaskExit>
where
    W: futures_util::Sink<tungstenite::Message, Error = tungstenite::Error> + Unpin,
{
    loop {
        tokio::select! {
            _ = cancellation.cancelled() => {
                let _ = tokio::time::timeout(
                    Duration::from_secs(1),
                    writer.send(tungstenite::Message::Close(None)),
                ).await;
                return Ok(ConnectionTaskExit::Cancelled);
            }
            message = outgoing.recv() => {
                let Some(message) = message else {
                    return Ok(ConnectionTaskExit::Disconnected);
                };
                writer.send(message).await.context("failed to write Computer WebSocket frame")?;
            }
        }
    }
}

async fn websocket_reader_task<R>(
    mut reader: R,
    outgoing: mpsc::Sender<tungstenite::Message>,
    commands: mpsc::Sender<ReceivedCommand>,
    database: SqlitePool,
    computer_id: Uuid,
    cancellation: CancellationToken,
) -> Result<ConnectionTaskExit>
where
    R: futures_util::Stream<Item = std::result::Result<tungstenite::Message, tungstenite::Error>>
        + Unpin,
{
    loop {
        let message = tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            message = reader.next() => message,
        };
        let Some(message) = message else {
            tracing::info!(computer_id = %computer_id, status = "disconnected", reason = "stream_ended", "Computer disconnected");
            return Ok(ConnectionTaskExit::Disconnected);
        };
        match message? {
            tungstenite::Message::Text(text) => {
                match serde_json::from_str(&text)
                    .context("Server sent an invalid Computer frame")?
                {
                    ServerFrame::Command {
                        command_id,
                        computer_seq,
                        command,
                    } => {
                        let kind = command.kind();
                        let context = command_log_context(&command);
                        tracing::info!(
                            computer_id = %computer_id,
                            command_id = %command_id,
                            computer_seq,
                            kind,
                            agent_member_id = context.agent_member_id.map(|id| id.to_string()).as_deref().unwrap_or("none"),
                            run_id = context.run_id.map(|id| id.to_string()).as_deref().unwrap_or("none"),
                            "Computer command received"
                        );
                        persist_command(&database, command_id, computer_seq, &command).await?;
                        queue_computer_frame(
                            &outgoing,
                            &ComputerFrame::CommandAck {
                                command_id,
                                computer_seq,
                            },
                        )
                        .await?;
                        tracing::info!(computer_id = %computer_id, command_id = %command_id, computer_seq, kind, "Computer command acknowledged");
                        commands
                            .send(ReceivedCommand {
                                command_id,
                                computer_seq,
                                command: *command,
                            })
                            .await
                            .context("Computer command processor stopped")?;
                    }
                    ServerFrame::Shutdown { reason } => {
                        tracing::info!(computer_id = %computer_id, reason = %reason, "Server requested Computer shutdown");
                        return Ok(if reason == "computer_deleted" {
                            ConnectionTaskExit::Deleted
                        } else {
                            ConnectionTaskExit::Shutdown
                        });
                    }
                    ServerFrame::ResultReceipt { event_id } => {
                        mark_run_result_reported(&database, computer_id, &event_id).await?;
                    }
                    ServerFrame::StartedReceipt { event_id } => {
                        mark_run_started_reported(&database, computer_id, &event_id).await?;
                    }
                    ServerFrame::Welcome { .. } => {}
                }
            }
            tungstenite::Message::Ping(bytes) => {
                outgoing
                    .send(tungstenite::Message::Pong(bytes))
                    .await
                    .context("Computer WebSocket writer stopped")?;
            }
            tungstenite::Message::Close(_) => {
                tracing::info!(computer_id = %computer_id, status = "disconnected", reason = "server_closed", "Computer disconnected");
                return Ok(ConnectionTaskExit::Disconnected);
            }
            tungstenite::Message::Binary(_)
            | tungstenite::Message::Pong(_)
            | tungstenite::Message::Frame(_) => {}
        }
    }
}

async fn command_processor_task(
    processor: LocalCommandProcessor,
    mut commands: mpsc::Receiver<ReceivedCommand>,
    outgoing: mpsc::Sender<tungstenite::Message>,
    computer_id: Uuid,
    cancellation: CancellationToken,
) -> Result<ConnectionTaskExit> {
    loop {
        let command = tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            command = commands.recv() => command,
        };
        let Some(command) = command else {
            return Ok(ConnectionTaskExit::Disconnected);
        };
        let kind = command.command.kind();
        let outcome = tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            outcome = processor.process(
                command.command_id,
                command.computer_seq,
                &command.command,
            ) => outcome?,
        };
        if let Some(outcome) = outcome {
            let error_code = command_error_code(&outcome).to_owned();
            let ok = outcome.ok;
            queue_computer_frame(
                &outgoing,
                &ComputerFrame::CommandResult {
                    command_id: command.command_id,
                    computer_seq: command.computer_seq,
                    ok,
                    result: outcome.result,
                },
            )
            .await?;
            tracing::info!(
                computer_id = %computer_id,
                command_id = %command.command_id,
                computer_seq = command.computer_seq,
                kind,
                ok,
                error_code,
                "Computer command result sent"
            );
        }
    }
}

async fn result_sender_task(
    database: SqlitePool,
    computer_id: Uuid,
    outgoing: mpsc::Sender<tungstenite::Message>,
    mut completions: mpsc::Receiver<()>,
    cancellation: CancellationToken,
) -> Result<ConnectionTaskExit> {
    let mut retry = tokio::time::interval(Duration::from_secs(1));
    retry.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    retry.tick().await;
    let mut sink = Box::pin(futures_util::sink::unfold(
        outgoing,
        |outgoing, message| async move {
            outgoing
                .send(message)
                .await
                .map_err(|_| tungstenite::Error::ConnectionClosed)?;
            Ok::<_, tungstenite::Error>(outgoing)
        },
    ));
    loop {
        tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            _ = retry.tick() => {
                send_pending_run_event(&mut sink, &database, computer_id).await?;
            }
            completion = completions.recv() => {
                if completion.is_none() {
                    return Ok(ConnectionTaskExit::Disconnected);
                }
                send_pending_run_event(&mut sink, &database, computer_id).await?;
            }
        }
    }
}

async fn attention_scheduler_task(
    client: reqwest::Client,
    server: Url,
    computer_id: Uuid,
    token: String,
    database: SqlitePool,
    max_claimed_runs: usize,
    cancellation: CancellationToken,
) -> Result<ConnectionTaskExit> {
    let mut interval = tokio::time::interval(Duration::from_secs(1));
    let mut scheduler = AttentionSchedulerState {
        database,
        max_claimed_runs,
        next_agent_index: 0,
        pending_claims: std::collections::HashSet::new(),
    };
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    interval.tick().await;
    loop {
        tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            _ = interval.tick() => {}
        }
        let result = tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            result = poll_agent_inbox(
                &client,
                &server,
                computer_id,
                &token,
                &mut scheduler,
            ) => result,
        };
        if let Err(error) = result {
            tracing::warn!(computer_id = %computer_id, error = %error, "Agent Inbox poll failed");
        }
    }
}

async fn lease_renewer_task(
    client: reqwest::Client,
    server: Url,
    computer_id: Uuid,
    token: String,
    database: SqlitePool,
    cancellation: CancellationToken,
) -> Result<ConnectionTaskExit> {
    let mut interval = tokio::time::interval(Duration::from_secs(60));
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    interval.tick().await;
    loop {
        tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            _ = interval.tick() => {}
        }
        let result = tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            result = renew_active_run_leases(&client, &server, computer_id, &token, &database) => result,
        };
        if let Err(error) = result {
            tracing::warn!(computer_id = %computer_id, error = %error, "Agent Inbox lease renewal failed");
        }
    }
}

async fn heartbeat_reporter_task(
    supervisor: Supervisor,
    computer_id: Uuid,
    heartbeat_seconds: u64,
    outgoing: mpsc::Sender<tungstenite::Message>,
    cancellation: CancellationToken,
) -> Result<ConnectionTaskExit> {
    let mut interval = tokio::time::interval(Duration::from_secs(heartbeat_seconds));
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    interval.tick().await;
    loop {
        tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            _ = interval.tick() => {}
        }
        let (agents_count, active_runs) = supervisor.counts().await?;
        queue_computer_frame(
            &outgoing,
            &ComputerFrame::Heartbeat {
                daemon_version: env!("CARGO_PKG_VERSION").to_owned(),
                os: platform_os()?.to_owned(),
                cpu_count: std::thread::available_parallelism()
                    .map(usize::from)
                    .unwrap_or(1),
                memory_total_bytes: None,
                agents_count,
                active_runs,
            },
        )
        .await?;
        tracing::debug!(computer_id = %computer_id, agents_count, active_runs, "Computer heartbeat sent");
    }
}

async fn send_pending_run_event<S>(
    writer: &mut S,
    database: &SqlitePool,
    computer_id: Uuid,
) -> Result<()>
where
    S: futures_util::Sink<tungstenite::Message, Error = tungstenite::Error> + Unpin,
{
    if send_pending_run_started(writer, database, computer_id).await? {
        return Ok(());
    }
    send_pending_run_result(writer, database, computer_id).await
}

async fn send_pending_run_started<S>(
    writer: &mut S,
    database: &SqlitePool,
    computer_id: Uuid,
) -> Result<bool>
where
    S: futures_util::Sink<tungstenite::Message, Error = tungstenite::Error> + Unpin,
{
    let now = OffsetDateTime::now_utc();
    let pending: Option<(String, String, String, i64, String, String, i64)> = sqlx::query_as(
        "SELECT outbox.event_id, outbox.run_id, runs.fencing_token, outbox.run_attempt, \
                outbox.process_instance_id, outbox.daemon_observed_at, outbox.attempt_count \
         FROM run_started_outbox outbox JOIN local_agent_runs runs ON runs.run_id = outbox.run_id \
         WHERE outbox.reported_at IS NULL AND outbox.next_attempt_at <= ?1 \
         ORDER BY next_attempt_at, event_id LIMIT 1",
    )
    .bind(now.to_string())
    .fetch_optional(database)
    .await?;
    let Some((
        event_id,
        run_id,
        fencing_token,
        run_attempt,
        process_instance_id,
        observed_at,
        attempt_count,
    )) = pending
    else {
        return Ok(false);
    };
    let run_id = Uuid::parse_str(&run_id)?;
    let process_instance_id = Uuid::parse_str(&process_instance_id)?;
    let daemon_observed_at =
        OffsetDateTime::parse(&observed_at, &time::format_description::well_known::Rfc3339)?;
    send_ws_frame(
        writer,
        &ComputerFrame::RunStarted {
            event_id: event_id.clone(),
            run_id,
            fencing_token,
            run_attempt,
            process_instance_id,
            daemon_observed_at,
        },
    )
    .await?;
    let retry_seconds = 1_i64 << attempt_count.min(5);
    sqlx::query(
        "UPDATE run_started_outbox SET attempt_count = attempt_count + 1, next_attempt_at = ?2 \
         WHERE event_id = ?1 AND reported_at IS NULL",
    )
    .bind(&event_id)
    .bind((now + time::Duration::seconds(retry_seconds)).to_string())
    .execute(database)
    .await?;
    tracing::info!(computer_id = %computer_id, run_id = %run_id, event_id, attempt = attempt_count + 1, "Agent run started event sent");
    Ok(true)
}

async fn send_pending_run_result<S>(
    writer: &mut S,
    database: &SqlitePool,
    computer_id: Uuid,
) -> Result<()>
where
    S: futures_util::Sink<tungstenite::Message, Error = tungstenite::Error> + Unpin,
{
    let now = OffsetDateTime::now_utc();
    let pending: Option<(String, String, String, i64, String, i64)> = sqlx::query_as(
        "SELECT outbox.event_id, runs.fencing_token, outbox.command_id, outbox.computer_seq, \
                outbox.payload_json, outbox.attempt_count FROM run_result_outbox outbox \
         JOIN local_agent_runs runs ON runs.run_id = outbox.run_id \
         WHERE outbox.reported_at IS NULL AND outbox.next_attempt_at <= ?1 \
         ORDER BY next_attempt_at, event_id LIMIT 1",
    )
    .bind(now.to_string())
    .fetch_optional(database)
    .await?;
    let Some((event_id, fencing_token, command_id, computer_seq, payload, attempt_count)) = pending
    else {
        return Ok(());
    };
    let command_id = Uuid::parse_str(&command_id)?;
    let result: CommandResult = serde_json::from_str(&payload)?;
    let ok = matches!(result.status, Some(RunResultStatus::Completed));
    send_ws_frame(
        writer,
        &ComputerFrame::RunResult {
            event_id: event_id.clone(),
            fencing_token,
            command_id,
            computer_seq,
            ok,
            result,
        },
    )
    .await?;
    let retry_seconds = 1_i64 << attempt_count.min(5);
    sqlx::query(
        "UPDATE run_result_outbox SET attempt_count = attempt_count + 1, \
         next_attempt_at = ?2, last_error = NULL \
         WHERE event_id = ?1 AND reported_at IS NULL",
    )
    .bind(&event_id)
    .bind((now + time::Duration::seconds(retry_seconds)).to_string())
    .execute(database)
    .await?;
    tracing::info!(computer_id = %computer_id, command_id = %command_id, event_id, attempt = attempt_count + 1, "Agent run result sent");
    Ok(())
}

async fn mark_run_result_reported(
    database: &SqlitePool,
    computer_id: Uuid,
    event_id: &str,
) -> Result<()> {
    let updated = sqlx::query(
        "UPDATE run_result_outbox SET reported_at = COALESCE(reported_at, ?2), last_error = NULL \
         WHERE event_id = ?1",
    )
    .bind(event_id)
    .bind(OffsetDateTime::now_utc().to_string())
    .execute(database)
    .await?;
    ensure!(
        updated.rows_affected() == 1,
        "Server receipted an unknown Run result event"
    );
    tracing::info!(computer_id = %computer_id, event_id, "Agent run result receipted");
    Ok(())
}

async fn mark_run_started_reported(
    database: &SqlitePool,
    computer_id: Uuid,
    event_id: &str,
) -> Result<()> {
    let updated = sqlx::query(
        "UPDATE run_started_outbox SET reported_at = COALESCE(reported_at, ?2) WHERE event_id = ?1",
    )
    .bind(event_id)
    .bind(OffsetDateTime::now_utc().to_string())
    .execute(database)
    .await?;
    ensure!(
        updated.rows_affected() == 1,
        "Server receipted an unknown Run started event"
    );
    tracing::info!(computer_id = %computer_id, event_id, "Agent run started event receipted");
    Ok(())
}

struct AttentionSchedulerState {
    database: SqlitePool,
    max_claimed_runs: usize,
    next_agent_index: usize,
    pending_claims: std::collections::HashSet<Uuid>,
}

async fn poll_agent_inbox(
    client: &reqwest::Client,
    server: &Url,
    computer_id: Uuid,
    token: &str,
    scheduler: &mut AttentionSchedulerState,
) -> Result<()> {
    let local_runs: Vec<(String, String)> =
        sqlx::query_as("SELECT run_id, status FROM local_agent_runs")
            .fetch_all(&scheduler.database)
            .await?;
    let local_run_ids = local_runs
        .iter()
        .filter_map(|(run_id, _)| Uuid::parse_str(run_id).ok())
        .collect::<std::collections::HashSet<_>>();
    scheduler
        .pending_claims
        .retain(|run_id| !local_run_ids.contains(run_id));
    let active_runs = local_runs
        .iter()
        .filter(|(_, status)| !matches!(status.as_str(), "completed" | "failed" | "canceled"))
        .count();
    let mut claim_budget = scheduler
        .max_claimed_runs
        .saturating_sub(active_runs.saturating_add(scheduler.pending_claims.len()));
    if claim_budget == 0 {
        return Ok(());
    }
    let agents: Vec<HostedAgent> = client
        .get(server.join(&format!("/api/v1/computers/{computer_id}/agents"))?)
        .bearer_auth(token)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let agents = agents
        .into_iter()
        .filter(|agent| agent.desired_lifecycle == "active" && agent.provision_status == "ready")
        .collect::<Vec<_>>();
    if agents.is_empty() {
        scheduler.next_agent_index = 0;
        return Ok(());
    }
    let start = scheduler.next_agent_index % agents.len();
    let mut visited = 0;
    while visited < agents.len() && claim_budget > 0 {
        let index = (start + visited) % agents.len();
        let agent = &agents[index];
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
            let run_id = claim.run_id.context("claimed Agent Run has no run id")?;
            scheduler.pending_claims.insert(run_id);
            claim_budget -= 1;
            tracing::info!(
                computer_id = %computer_id,
                agent_member_id = %agent.member_id,
                run_id = %run_id,
                inbox_items_count = claim.inbox_item_ids.len(),
                "Agent Inbox claimed"
            );
        }
        visited += 1;
    }
    scheduler.next_agent_index = (start + visited) % agents.len();
    Ok(())
}

async fn renew_active_run_leases(
    client: &reqwest::Client,
    server: &Url,
    computer_id: Uuid,
    token: &str,
    database: &SqlitePool,
) -> Result<()> {
    let runs: Vec<(String, String, String)> = sqlx::query_as(
        "SELECT run_id, agent_member_id, fencing_token FROM local_agent_runs \
         WHERE status IN ('queued', 'running') ORDER BY run_id",
    )
    .fetch_all(database)
    .await?;
    for (run_id, agent_id, fencing_token) in runs {
        let run_id = Uuid::parse_str(&run_id)?;
        let agent_id = Uuid::parse_str(&agent_id)?;
        let response = client
            .post(server.join(&format!(
                "/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/renew"
            ))?)
            .bearer_auth(token)
            .json(&serde_json::json!({ "run_id": run_id, "fencing_token": fencing_token }))
            .send()
            .await;
        match response {
            Ok(response) if response.status().is_success() => {
                let response: AgentLeaseResponse = response.json().await?;
                sqlx::query(
                    "UPDATE local_agent_runs SET ownership_lease_expires_at = ?2 WHERE run_id = ?1",
                )
                .bind(run_id.to_string())
                .bind(response.ownership_lease_expires_at.to_string())
                .execute(database)
                .await?;
                tracing::info!(computer_id = %computer_id, agent_member_id = %agent_id, run_id = %run_id, "Agent run ownership lease renewed")
            }
            Ok(_) | Err(_) => {
                tracing::warn!(computer_id = %computer_id, agent_member_id = %agent_id, run_id = %run_id, error_code = "lease_renew_failed", "Agent Inbox lease renewal failed")
            }
        }
    }
    Ok(())
}

async fn release_interrupted_runs(
    client: &reqwest::Client,
    server: &Url,
    computer_id: Uuid,
    token: &str,
    database: &SqlitePool,
) -> Result<()> {
    let runs: Vec<(String, String, String)> = sqlx::query_as(
        "SELECT run_id, agent_member_id, fencing_token FROM local_agent_runs \
         WHERE status = 'failed' AND last_error_code = 'process_lost' \
           AND server_recovery_reported_at IS NULL ORDER BY run_id",
    )
    .fetch_all(database)
    .await?;
    for (run_id, agent_id, fencing_token) in runs {
        let run_id = Uuid::parse_str(&run_id)?;
        let agent_id = Uuid::parse_str(&agent_id)?;
        client
            .post(server.join(&format!(
                "/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/release"
            ))?)
            .bearer_auth(token)
            .json(&serde_json::json!({
                "run_id": run_id,
                "fencing_token": fencing_token,
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
    result: CommandResult,
}

#[derive(Debug, PartialEq, Eq)]
struct CommandLogContext {
    agent_member_id: Option<Uuid>,
    run_id: Option<Uuid>,
}

fn command_log_context(command: &ComputerCommand) -> CommandLogContext {
    CommandLogContext {
        agent_member_id: command.agent_id(),
        run_id: command.run_id(),
    }
}

fn command_error_code(outcome: &LocalCommandOutcome) -> &str {
    outcome.result.error_code.as_deref().unwrap_or("none")
}

struct LocalCommandProcessor {
    database: SqlitePool,
    state_dir: std::path::PathBuf,
    supervisor: Supervisor,
    completion_tx: tokio::sync::mpsc::Sender<()>,
}

impl LocalCommandProcessor {
    async fn process(
        &self,
        command_id: Uuid,
        computer_seq: i64,
        command: &ComputerCommand,
    ) -> Result<Option<LocalCommandOutcome>> {
        let kind = command.kind();
        let existing: (String, Option<String>) =
            sqlx::query_as("SELECT status, result_json FROM server_commands WHERE command_id = ?1")
                .bind(command_id.to_string())
                .fetch_one(&self.database)
                .await?;
        if matches!(existing.0.as_str(), "completed" | "failed") {
            tracing::info!(command_id = %command_id, computer_seq, kind, replayed = true, status = %existing.0, "Computer command replayed from local result");
            if matches!(command, ComputerCommand::Run(_)) {
                return Ok(None);
            }
            return Ok(Some(LocalCommandOutcome {
                ok: existing.0 == "completed",
                result: serde_json::from_str(existing.1.as_deref().unwrap_or("{}"))?,
            }));
        }
        if existing.0 == "running" {
            tracing::info!(command_id = %command_id, computer_seq, kind, replayed = true, status = "running", "Computer command already running");
            return Ok(None);
        }
        if let ComputerCommand::MemoryRead(payload) = command {
            let outcome = match read_memory_file(&self.state_dir, payload).await {
                Ok(file) => LocalCommandOutcome {
                    ok: true,
                    result: file,
                },
                Err(_) => LocalCommandOutcome {
                    ok: false,
                    result: failed_command_result(),
                },
            };
            finish_local_command_with_result(
                &self.database,
                command_id,
                &outcome,
                &CommandResult {
                    ok: Some(outcome.ok),
                    ..CommandResult::default()
                },
            )
            .await?;
            return Ok(Some(outcome));
        }
        if let ComputerCommand::Run(run) = command {
            sqlx::query("UPDATE server_commands SET status = 'running' WHERE command_id = ?1")
                .bind(command_id.to_string())
                .execute(&self.database)
                .await?;
            let result = self
                .supervisor
                .start(
                    run.clone(),
                    RunCommand {
                        command_id,
                        computer_seq,
                    },
                )
                .await;
            match result {
                Ok(receiver) => {
                    let completion_tx = self.completion_tx.clone();
                    tokio::spawn(async move {
                        let _result = receiver.await.unwrap_or(RunResult {
                            run_id: Uuid::nil(),
                            status: "failed".to_owned(),
                            error_code: Some("supervisor_stopped".to_owned()),
                            memory_files: Vec::new(),
                        });
                        completion_tx.send(()).await.ok();
                    });
                    return Ok(None);
                }
                Err(_) => {
                    let outcome = LocalCommandOutcome {
                        ok: false,
                        result: failed_command_result(),
                    };
                    finish_local_command(&self.database, command_id, &outcome).await?;
                    return Ok(Some(outcome));
                }
            }
        }
        if let ComputerCommand::Cancel(payload) = command {
            let result = self.supervisor.cancel(payload.run_id).await;
            let outcome = match result {
                Ok(()) => LocalCommandOutcome {
                    ok: true,
                    result: successful_command_result(None),
                },
                Err(_) => LocalCommandOutcome {
                    ok: false,
                    result: failed_command_result(),
                },
            };
            finish_local_command(&self.database, command_id, &outcome).await?;
            return Ok(Some(outcome));
        }
        let result = execute_local_command(&self.state_dir, command, &self.supervisor).await;
        let result = validate_provision_result(result, command, &self.supervisor).await;
        let outcome = match result {
            Ok(memory_files) => LocalCommandOutcome {
                ok: true,
                result: successful_command_result(Some(memory_files)),
            },
            Err(_) => LocalCommandOutcome {
                ok: false,
                result: failed_command_result(),
            },
        };
        finish_local_command(&self.database, command_id, &outcome).await?;
        Ok(Some(outcome))
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
    stored_result: &CommandResult,
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

fn successful_command_result(memory_files: Option<Vec<MemoryFileMetadata>>) -> CommandResult {
    CommandResult {
        ok: Some(true),
        memory_files,
        ..CommandResult::default()
    }
}

fn failed_command_result() -> CommandResult {
    CommandResult {
        ok: Some(false),
        error_code: Some(COMMAND_FAILED_ERROR_CODE.to_owned()),
        ..CommandResult::default()
    }
}

async fn execute_local_command(
    state_dir: &Path,
    command: &ComputerCommand,
    supervisor: &Supervisor,
) -> Result<Vec<MemoryFileMetadata>> {
    let (configuration, lifecycle, provision) = match command {
        ComputerCommand::Provision(configuration) => (configuration, "active", true),
        ComputerCommand::Configure(configuration) => (configuration, "unchanged", false),
        ComputerCommand::Suspend(configuration) => (configuration, "suspended", false),
        ComputerCommand::Resume(configuration) => (configuration, "active", false),
        ComputerCommand::Retire(configuration) => (configuration, "retired", false),
        _ => bail!("unsupported local command: {}", command.kind()),
    };
    let agent_id = configuration.agent_id;
    let home = if provision {
        ensure!(
            matches!(configuration.driver_kind.as_str(), "codex" | "builtin"),
            "agent.provision command has unknown driver_kind"
        );
        let home = prepare_agent_home(state_dir, agent_id).await?;
        supervisor
            .prepare_agent_driver(agent_id, &configuration.driver_kind)
            .await?;
        home
    } else {
        let home = state_dir.join("agents").join(agent_id.to_string());
        ensure!(home.is_dir(), "Agent Home is unavailable");
        home
    };
    let profile_path = home.join("profile.json");
    let mut profile = if provision {
        serde_json::json!({
            "schema_version": 1,
            "agent_id": agent_id,
            "space_id": configuration.space_id,
            "name": configuration.name,
            "handle": configuration.handle,
            "driver_kind": configuration.driver_kind,
            "driver_config": configuration.driver_config,
        })
    } else {
        serde_json::from_slice(&tokio::fs::read(&profile_path).await?)?
    };
    let profile = profile
        .as_object_mut()
        .context("Agent profile must be a JSON object")?;
    profile.insert(
        "role_text".to_owned(),
        serde_json::to_value(&configuration.role_text)?,
    );
    profile.insert(
        "role_revision".to_owned(),
        serde_json::to_value(configuration.role_revision)?,
    );
    profile.insert(
        "attention_config".to_owned(),
        serde_json::to_value(&configuration.attention_config)?,
    );
    let desired_lifecycle = if lifecycle == "unchanged" {
        profile
            .get("desired_lifecycle")
            .and_then(serde_json::Value::as_str)
            .unwrap_or("active")
    } else {
        lifecycle
    };
    profile.insert(
        "desired_lifecycle".to_owned(),
        serde_json::json!(desired_lifecycle),
    );
    profile.insert("provision_status".to_owned(), serde_json::json!("ready"));
    write_restricted_file_atomic(&profile_path, &serde_json::to_vec_pretty(&profile)?).await?;
    let memory = home.join("memory/MEMORY.md");
    if !memory.exists() {
        write_restricted_file(&memory, b"# Memory\n").await?;
    }
    if matches!(
        command,
        ComputerCommand::Suspend(_) | ComputerCommand::Retire(_)
    ) && (matches!(command, ComputerCommand::Retire(_))
        || matches!(configuration.mode, Some(SuspendMode::CancelNow)))
    {
        supervisor.cancel_agent(agent_id).await?;
    }
    scan_memory(&home.join("memory")).await
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
    command: &AgentMemoryReadCommand,
) -> Result<CommandResult> {
    const MAX_MEMORY_READ_BYTES: u64 = 1024 * 1024;
    let agent_id = command.agent_id;
    let relative = command.path.as_str();
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
    Ok(CommandResult {
        path: Some(relative.to_owned()),
        content: Some(content),
        size: Some(metadata.len()),
        sha256: Some(hex::encode(Sha256::digest(&bytes))),
        updated_at: Some(OffsetDateTime::from(metadata.modified()?)),
        ..CommandResult::default()
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
    command: &ComputerCommand,
    supervisor: &Supervisor,
) -> Result<Vec<MemoryFileMetadata>> {
    let memory_files = result?;
    if let ComputerCommand::Provision(configuration) = command {
        supervisor
            .validate_agent(configuration.agent_id, &configuration.driver_kind)
            .await?;
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
        let command: ComputerCommand = serde_json::from_str(&request_json)
            .context("persisted command has invalid protocol payload")?;
        let result = execute_local_command(state_dir, &command, supervisor).await;
        let result = validate_provision_result(result, &command, supervisor).await;
        let outcome = match result {
            Ok(memory_files) => LocalCommandOutcome {
                ok: true,
                result: successful_command_result(Some(memory_files)),
            },
            Err(_) => LocalCommandOutcome {
                ok: false,
                result: failed_command_result(),
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
    command: &ComputerCommand,
) -> Result<()> {
    ensure!(computer_seq > 0, "Server command sequence must be positive");
    let request = serde_json::to_value(command)?;
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

async fn resolve_default_computer_state_dir(root: &Path) -> Result<std::path::PathBuf> {
    prepare_state_dir(root).await?;
    let mut candidates = Vec::new();
    let mut spaces = tokio::fs::read_dir(root).await?;
    while let Some(space_entry) = spaces.next_entry().await? {
        if !space_entry.file_type().await?.is_dir() {
            continue;
        }
        let space_name = space_entry.file_name();
        if space_name == "pending" {
            let mut pending = tokio::fs::read_dir(space_entry.path()).await?;
            while let Some(entry) = pending.next_entry().await? {
                if entry.file_type().await?.is_dir() && entry.path().join("secrets.json").exists() {
                    candidates.push(entry.path());
                }
            }
            continue;
        }
        let Ok(space_id) = Uuid::parse_str(&space_name.to_string_lossy()) else {
            continue;
        };
        let mut computers = tokio::fs::read_dir(space_entry.path()).await?;
        while let Some(entry) = computers.next_entry().await? {
            if !entry.file_type().await?.is_dir() {
                continue;
            }
            let Ok(computer_id) = Uuid::parse_str(&entry.file_name().to_string_lossy()) else {
                continue;
            };
            let secrets_path = entry.path().join("secrets.json");
            if !secrets_path.exists() {
                continue;
            }
            let bytes = tokio::fs::read(&secrets_path).await?;
            let secrets: ComputerSecrets =
                serde_json::from_slice(&bytes).context("Computer secrets are invalid")?;
            if secrets.space_id == Some(space_id) && secrets.computer_id == Some(computer_id) {
                candidates.push(entry.path());
            }
        }
    }
    ensure!(
        candidates.len() <= 1,
        "multiple local Computer identities found under {}",
        root.display()
    );
    Ok(candidates
        .pop()
        .unwrap_or_else(|| root.join("pending").join(Uuid::now_v7().to_string())))
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
