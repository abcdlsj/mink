use std::{path::Path, sync::Arc};

use anyhow::{Context, Result, bail, ensure};
use futures_util::StreamExt;
use sqlx::SqlitePool;
use tokio::{sync::mpsc, task::JoinSet};
use tokio_tungstenite::tungstenite::{self, client::IntoClientRequest};
use tokio_util::sync::CancellationToken;
use url::Url;

use crate::{
    cli::ComputerArgs,
    computer_protocol::{ComputerFrame, ServerFrame},
    config, database,
    driver::builtin_config,
    driver::codex::CodexDriver,
    supervisor::Supervisor,
};

#[cfg(test)]
use {
    crate::computer_protocol::{CommandResult, ComputerCommand},
    futures_util::SinkExt,
    sha2::{Digest, Sha256},
    std::time::Duration,
    time::OffsetDateTime,
    uuid::Uuid,
};

mod local_ipc;

mod attention_scheduler;
mod command_executor;
mod connection;
mod credentials;
mod lease_recovery;

use attention_scheduler::*;
use command_executor::*;
use connection::*;
use credentials::*;
use lease_recovery::*;

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

#[cfg(test)]
mod tests;
