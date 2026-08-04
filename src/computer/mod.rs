mod adapters;
mod application;
mod core;
mod drivers;

use std::{
    collections::HashSet,
    path::{Path, PathBuf},
};

use adapters::{
    AgentHomeAdapter, LocalIpcAdapter, SandboxAdapter, ServerConnectionAdapter, SqliteAdapter,
};
use anyhow::{Context, ensure};
use application::{
    ApplicationError,
    pipeline::RunPipelineService,
    ports::{AgentHomePort, DriverPort, TransactionPort},
    recovery::RecoveryService,
};
use backon::{BackoffBuilder, ExponentialBuilder};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use futures_util::{SinkExt, StreamExt};
use rand::{RngCore, rngs::OsRng};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use tokio_tungstenite::tungstenite::{Message as WebSocketMessage, client::IntoClientRequest};
use uuid::Uuid;

use crate::{
    ids::{AttachmentId, DaemonSessionId, EventId, IdempotencyKey, RunId},
    protocol::{
        capability,
        computer::{
            CommandSequence, ComputerFrame, ComputerHello, DaemonCapability, Heartbeat,
            ServerFrame, ServerHandshake,
        },
        version::SUPPORTED,
    },
};

#[derive(Serialize, Deserialize)]
struct ComputerSecrets {
    computer_id: Uuid,
    space_id: Uuid,
    token: String,
}

#[derive(Serialize)]
struct BeginPairing<'a> {
    token_hash: String,
    hostname: &'a str,
    os: &'static str,
    daemon_version: &'static str,
}

#[derive(Deserialize)]
struct PairingStarted {
    pairing_id: Uuid,
    code: String,
    expires_at: String,
}

#[derive(Deserialize)]
struct PairingStatus {
    status: String,
    computer_id: Option<Uuid>,
    space_id: Option<Uuid>,
}

#[derive(Serialize)]
struct AgentActionRequest {
    context: capability::RunContext,
    action: capability::Action,
    idempotency_key: Option<IdempotencyKey>,
}

pub(crate) async fn run(args: crate::cli::ComputerArgs) -> anyhow::Result<()> {
    let mut config = crate::config::load(args.config.as_ref())?.computer;
    if let Some(server) = args.server {
        config.server_url = server;
    }
    let (computer_home, secrets) = match find_paired_computer(&config.state_dir).await? {
        Some(paired) => paired,
        None => pair(&config.state_dir, &config.server_url).await?,
    };
    ensure_private_directory(&computer_home).await?;
    let database_path = computer_home.join("daemon.db");
    let mut storage = SqliteAdapter::open(&database_path)
        .await
        .map_err(|error| anyhow::anyhow!(error))?;
    let runtime_dir = crate::config::runtime_dir_for(&computer_home);
    tokio::fs::create_dir_all(&runtime_dir)
        .await
        .context("failed to create Computer runtime directory")?;
    tracing::info!(
        computer_id = %secrets.computer_id,
        space_id = %secrets.space_id,
        server = %config.server_url,
        "Computer local baseline is ready"
    );
    SandboxAdapter::validate().map_err(|error| anyhow::anyhow!(error))?;
    let mut driver_secret = [0_u8; 32];
    OsRng.fill_bytes(&mut driver_secret);
    let mut capability_store = SqliteAdapter::open(&database_path)
        .await
        .map_err(|error| anyhow::anyhow!(error))?;
    let ipc = LocalIpcAdapter::bind(&runtime_dir.join("daemon.sock"))
        .await
        .map_err(|error| anyhow::anyhow!(error))?;
    let capability_endpoint = config.server_url.join(&format!(
        "api/v1/computers/{}/agent-actions",
        secrets.computer_id
    ))?;
    let capability_server_url = config.server_url.clone();
    let capability_computer_id = secrets.computer_id;
    let capability_computer_home = computer_home.clone();
    let capability_token = secrets.token.clone();
    let capability_client = reqwest::Client::new();
    let mut capability_homes = AgentHomeAdapter::new(
        computer_home.clone(),
        config.codex_config_source.clone(),
        config.codex_auth_source.clone(),
    );
    let (yield_interrupt_tx, mut yield_interrupt_rx) = tokio::sync::mpsc::unbounded_channel();
    tokio::spawn(async move {
        loop {
            let endpoint = capability_endpoint.clone();
            let token = capability_token.clone();
            let client = capability_client.clone();
            let server_url = capability_server_url.clone();
            let computer_home = capability_computer_home.clone();
            let result = ipc
                .serve_capability(
                    &mut capability_store,
                    &mut capability_homes,
                    driver_secret,
                    |run_id| {
                        let _ = yield_interrupt_tx.send(run_id);
                    },
                    move |context, action, idempotency_key| async move {
                        match action {
                            capability::Action::AttachmentUpload { path } => {
                                return proxy_attachment_upload(
                                    &client,
                                    &server_url,
                                    capability_computer_id,
                                    &token,
                                    &computer_home,
                                    &context,
                                    &path,
                                    idempotency_key,
                                )
                                .await;
                            }
                            capability::Action::AttachmentDownload {
                                attachment_id,
                                output,
                            } => {
                                return proxy_attachment_download(
                                    &client,
                                    &server_url,
                                    capability_computer_id,
                                    &token,
                                    &computer_home,
                                    &context,
                                    attachment_id,
                                    &output,
                                )
                                .await;
                            }
                            action => {
                                let response = client
                                    .post(endpoint)
                                    .bearer_auth(token)
                                    .json(&AgentActionRequest {
                                        context,
                                        action,
                                        idempotency_key,
                                    })
                                    .send()
                                    .await;
                                match response {
                                    Ok(response) => response
                                        .json()
                                        .await
                                        .unwrap_or_else(|_| capability_failure()),
                                    Err(_) => capability_failure(),
                                }
                            }
                        }
                    },
                )
                .await;
            if result.is_err() {
                tracing::warn!("local Agent capability request failed");
            }
        }
    });
    let mut homes = AgentHomeAdapter::new(
        computer_home.clone(),
        config.codex_config_source.clone(),
        config.codex_auth_source.clone(),
    );
    let mut driver = drivers::runtime(&computer_home, &config, driver_secret)
        .map_err(|error| anyhow::anyhow!(error))?;
    RunPipelineService::recover(
        &mut storage,
        &mut driver,
        &mut homes,
        config.max_concurrent_runs,
    )
    .await
    .map_err(|error| anyhow::anyhow!(error))?;
    let mut reconnect = ExponentialBuilder::default()
        .with_jitter()
        .with_min_delay(std::time::Duration::from_secs(1))
        .with_max_delay(std::time::Duration::from_secs(30))
        .without_max_times()
        .build();
    loop {
        match connect(
            &config.server_url,
            &secrets,
            &mut storage,
            &mut driver,
            &mut homes,
            config.max_concurrent_runs,
            &mut yield_interrupt_rx,
        )
        .await
        {
            Ok(()) => return Ok(()),
            Err(error) => {
                let delay = reconnect
                    .next()
                    .expect("an unlimited reconnect backoff always yields a delay");
                tracing::warn!(%error, ?delay, "Computer connection lost; retrying");
                tokio::time::sleep(delay).await;
            }
        }
    }
}

#[allow(clippy::too_many_arguments)]
async fn proxy_attachment_upload(
    client: &reqwest::Client,
    server_url: &url::Url,
    computer_id: Uuid,
    token: &str,
    computer_home: &Path,
    context: &capability::RunContext,
    path: &str,
    idempotency_key: Option<IdempotencyKey>,
) -> capability::Response<serde_json::Value> {
    let Some(idempotency_key) = idempotency_key else {
        return capability_error_response(
            capability::ErrorCode::InvalidArgument,
            "idempotency key is required",
            false,
        );
    };
    let homes = AgentHomeAdapter::new(computer_home.to_path_buf(), None, None);
    let (name, content) = match homes
        .read_attachment_source(context.agent_id, Path::new(path))
        .await
    {
        Ok(source) => source,
        Err(error) => return local_attachment_error(error),
    };
    let base = match server_url.join(&format!(
        "api/v1/computers/{computer_id}/agents/{}/runs/{}/attachments/",
        context.agent_id, context.run_id
    )) {
        Ok(base) => base,
        Err(_) => return capability_failure(),
    };
    let created = match client
        .post(
            base.join("uploads")
                .expect("relative Attachment URL is valid"),
        )
        .bearer_auth(token)
        .header("idempotency-key", idempotency_key.to_string())
        .json(&serde_json::json!({
            "original_name": name,
            "media_type": "application/octet-stream"
        }))
        .send()
        .await
    {
        Ok(response) => match attachment_json(response).await {
            Ok(value) => value,
            Err(error) => return error,
        },
        Err(_) => return capability_failure(),
    };
    let Some(attachment_id) = created
        .get("id")
        .and_then(serde_json::Value::as_str)
        .and_then(|value| Uuid::parse_str(value).ok())
    else {
        return capability_failure();
    };
    let content_url = base
        .join(&format!("{attachment_id}/content"))
        .expect("relative Attachment URL is valid");
    let uploaded = client
        .put(content_url)
        .bearer_auth(token)
        .body(content.clone())
        .send()
        .await;
    match uploaded {
        Ok(response) if response.status().is_success() => {}
        Ok(response) => return attachment_http_error(response).await,
        Err(_) => return capability_failure(),
    }
    let digest = hex::encode(Sha256::digest(&content));
    match client
        .post(
            base.join(&format!("{attachment_id}/complete"))
                .expect("relative Attachment URL is valid"),
        )
        .bearer_auth(token)
        .header("idempotency-key", idempotency_key.to_string())
        .json(&serde_json::json!({"size":content.len(),"sha256":digest}))
        .send()
        .await
    {
        Ok(response) => match attachment_json(response).await {
            Ok(value) => capability::Response::success(value),
            Err(error) => error,
        },
        Err(_) => capability_failure(),
    }
}

#[allow(clippy::too_many_arguments)]
async fn proxy_attachment_download(
    client: &reqwest::Client,
    server_url: &url::Url,
    computer_id: Uuid,
    token: &str,
    computer_home: &Path,
    context: &capability::RunContext,
    attachment_id: AttachmentId,
    output: &str,
) -> capability::Response<serde_json::Value> {
    let endpoint = match server_url.join(&format!(
        "api/v1/computers/{computer_id}/agents/{}/runs/{}/attachments/{attachment_id}/download",
        context.agent_id, context.run_id
    )) {
        Ok(endpoint) => endpoint,
        Err(_) => return capability_failure(),
    };
    let response = match client.get(endpoint).bearer_auth(token).send().await {
        Ok(response) => response,
        Err(_) => return capability_failure(),
    };
    if !response.status().is_success() {
        return attachment_http_error(response).await;
    }
    let content = match response.bytes().await {
        Ok(content) => content,
        Err(_) => return capability_failure(),
    };
    let homes = AgentHomeAdapter::new(computer_home.to_path_buf(), None, None);
    if let Err(error) = homes
        .write_attachment_output(context.agent_id, Path::new(output), &content)
        .await
    {
        return local_attachment_error(error);
    }
    capability::Response::success(serde_json::json!({
        "attachment_id": attachment_id,
        "output": output,
        "size": content.len(),
        "sha256": hex::encode(Sha256::digest(&content))
    }))
}

async fn attachment_json(
    response: reqwest::Response,
) -> Result<serde_json::Value, capability::Response<serde_json::Value>> {
    if !response.status().is_success() {
        return Err(attachment_http_error(response).await);
    }
    response.json().await.map_err(|_| capability_failure())
}

async fn attachment_http_error(
    response: reqwest::Response,
) -> capability::Response<serde_json::Value> {
    let retryable = response.status().is_server_error();
    let payload = response.json::<serde_json::Value>().await.ok();
    let code = payload
        .as_ref()
        .and_then(|value| value.pointer("/error/code"))
        .and_then(serde_json::Value::as_str)
        .map(capability_error_code)
        .unwrap_or(capability::ErrorCode::Internal);
    capability_error_response(code, "Attachment operation failed", retryable)
}

fn capability_error_code(code: &str) -> capability::ErrorCode {
    match code {
        "invalid_argument" => capability::ErrorCode::InvalidArgument,
        "unauthenticated" => capability::ErrorCode::Unauthenticated,
        "permission_denied" => capability::ErrorCode::PermissionDenied,
        "not_found" => capability::ErrorCode::NotFound,
        "conflict" => capability::ErrorCode::Conflict,
        "context_changed" => capability::ErrorCode::ContextChanged,
        "rate_limited" => capability::ErrorCode::RateLimited,
        "unavailable" => capability::ErrorCode::Unavailable,
        _ => capability::ErrorCode::Internal,
    }
}

fn local_attachment_error(error: ApplicationError) -> capability::Response<serde_json::Value> {
    let code = match error {
        ApplicationError::NotFound => capability::ErrorCode::NotFound,
        ApplicationError::Conflict | ApplicationError::Core(_) => capability::ErrorCode::Conflict,
        _ => capability::ErrorCode::Internal,
    };
    capability_error_response(code, "Attachment file operation failed", false)
}

fn capability_error_response(
    code: capability::ErrorCode,
    message: &str,
    retryable: bool,
) -> capability::Response<serde_json::Value> {
    capability::Response::failure(capability::Error {
        code,
        message: message.into(),
        retryable,
        details: Default::default(),
    })
}

fn capability_failure() -> capability::Response<serde_json::Value> {
    capability::Response::failure(capability::Error {
        code: capability::ErrorCode::Unavailable,
        message: "Server Agent capability transport is unavailable".into(),
        retryable: true,
        details: Default::default(),
    })
}

async fn connect<P, D, H>(
    server: &url::Url,
    secrets: &ComputerSecrets,
    storage: &mut P,
    driver: &mut D,
    homes: &mut H,
    max_concurrent_runs: usize,
    yield_interrupts: &mut tokio::sync::mpsc::UnboundedReceiver<RunId>,
) -> anyhow::Result<()>
where
    P: TransactionPort,
    D: DriverPort,
    H: AgentHomePort,
{
    let mut endpoint = server.join(&format!("api/v1/computers/{}/connect", secrets.computer_id))?;
    endpoint
        .set_scheme(match endpoint.scheme() {
            "https" => "wss",
            _ => "ws",
        })
        .map_err(|_| anyhow::anyhow!("invalid Sumi Server URL"))?;
    let mut request = endpoint.as_str().into_client_request()?;
    request.headers_mut().insert(
        "Authorization",
        format!("Bearer {}", secrets.token).parse()?,
    );
    let (socket, _) = tokio_tungstenite::connect_async(request)
        .await
        .context("failed to connect Computer WebSocket")?;
    let (mut writer, mut reader) = socket.split();
    let daemon_session_id = DaemonSessionId::from_uuid(Uuid::now_v7());
    let hello = ComputerHello {
        supported_versions: SUPPORTED,
        daemon_version: env!("CARGO_PKG_VERSION").into(),
        capabilities: std::collections::BTreeSet::from([
            DaemonCapability::ProviderSessionResume,
            DaemonCapability::ProviderSessionReset,
            DaemonCapability::Sandbox,
        ]),
        daemon_session_id,
        command_watermark: CommandSequence(0),
        // Runs this daemon still holds. The Server fails the rest of its non-terminal Runs for this
        // Computer, which is how a restart is reported instead of inferred from a timer.
        live_run_ids: RecoveryService::reconnect_run_ids(storage)
            .await
            .map_err(|error| anyhow::anyhow!(error))?,
    };
    writer
        .send(WebSocketMessage::Text(
            serde_json::to_string(&hello)?.into(),
        ))
        .await?;
    let handshake = reader
        .next()
        .await
        .context("Server closed during handshake")??;
    let WebSocketMessage::Text(handshake) = handshake else {
        anyhow::bail!("Server returned a non-text Computer handshake");
    };
    match serde_json::from_str::<ServerHandshake>(&handshake)? {
        ServerHandshake::Welcome { .. } => {}
        ServerHandshake::Rejected { code, .. } => {
            anyhow::bail!("Server rejected Computer protocol: {code:?}")
        }
    }
    let mut sent_events = HashSet::new();
    send_next_pending_event(storage, &mut writer, &mut sent_events).await?;
    let adapter = ServerConnectionAdapter::new(drivers::prompt::global_contract());
    let mut heartbeat = tokio::time::interval(std::time::Duration::from_secs(15));
    let mut driver_observation = tokio::time::interval(std::time::Duration::from_millis(250));
    let mut lost_driver_check = tokio::time::interval(std::time::Duration::from_secs(1));
    loop {
        tokio::select! {
            _ = tokio::signal::ctrl_c() => return Ok(()),
            Some(run_id) = yield_interrupts.recv() => {
                RunPipelineService::interrupt_yielded(storage, driver, run_id, max_concurrent_runs)
                    .await
                    .map_err(|error| anyhow::anyhow!(error))?;
                send_next_pending_event(storage, &mut writer, &mut sent_events).await?;
            }
            _ = heartbeat.tick() => {
                let active_runs=RecoveryService::live_run_ids(storage).await.map_err(|error|anyhow::anyhow!(error))?.len() as u32;
                let frame=ComputerFrame::Heartbeat{heartbeat:Heartbeat{daemon_session_id,active_runs,observed_at:time::OffsetDateTime::now_utc()}};
                writer.send(WebSocketMessage::Text(serde_json::to_string(&frame)?.into())).await?;
            }
            _ = driver_observation.tick() => {
                let completions = driver
                    .poll_completions()
                    .await
                    .map_err(|error| anyhow::anyhow!(error))?;
                let changed = RunPipelineService::finish_driver_turns(
                    storage,
                    driver,
                    completions,
                    max_concurrent_runs,
                )
                .await
                .map_err(|error| anyhow::anyhow!(error))?;
                if changed {
                    send_next_pending_event(storage, &mut writer, &mut sent_events).await?;
                }
            }
            _ = lost_driver_check.tick() => {
                if RunPipelineService::fail_lost_drivers(storage, driver, max_concurrent_runs)
                    .await
                    .map_err(|error| anyhow::anyhow!(error))?
                {
                    send_next_pending_event(storage, &mut writer, &mut sent_events).await?;
                }
            }
            incoming=reader.next()=>{
                let incoming=incoming.context("Computer WebSocket closed")??;
                let WebSocketMessage::Text(encoded)=incoming else{continue;};
                match serde_json::from_str::<ServerFrame>(&encoded)?{
                    ServerFrame::Command{envelope}=>{
                        for frame in adapter.receive(storage,driver,homes,*envelope).await.map_err(|error|anyhow::anyhow!(error))?{
                            writer.send(WebSocketMessage::Text(serde_json::to_string(&frame)?.into())).await?;
                        }
                        RunPipelineService::dispatch(
                            storage,
                            driver,
                            max_concurrent_runs,
                        )
                        .await
                        .map_err(|error| anyhow::anyhow!(error))?;
                        send_next_pending_event(storage, &mut writer, &mut sent_events).await?;
                    }
                    ServerFrame::Receipt{receipt}=>{
                        RecoveryService::acknowledge(storage,receipt.event_id).await.map_err(|error|anyhow::anyhow!(error))?;
                        sent_events.remove(&receipt.event_id);
                        send_next_pending_event(storage, &mut writer, &mut sent_events).await?;
                    },
                    ServerFrame::Query{query}=>{
                        let frame=ServerConnectionAdapter::answer_query(storage,homes,query).await;
                        writer.send(WebSocketMessage::Text(serde_json::to_string(&frame)?.into())).await?;
                    }
                    ServerFrame::Shutdown{code}=>anyhow::bail!("Server stopped Computer connection: {code:?}"),
                }
            }
        }
    }
}

async fn send_next_pending_event<P>(
    storage: &mut P,
    writer: &mut (impl SinkExt<WebSocketMessage, Error = tokio_tungstenite::tungstenite::Error> + Unpin),
    sent_events: &mut HashSet<EventId>,
) -> anyhow::Result<()>
where
    P: TransactionPort,
{
    let Some(event) = RecoveryService::pending_results(storage)
        .await
        .map_err(|error| anyhow::anyhow!(error))?
        .into_iter()
        .find(|event| !sent_events.contains(&event.id()))
    else {
        return Ok(());
    };
    let event_id = event.id();
    let frame = ServerConnectionAdapter::event_frame(event);
    writer
        .send(WebSocketMessage::Text(
            serde_json::to_string(&frame)?.into(),
        ))
        .await?;
    sent_events.insert(event_id);
    Ok(())
}

async fn find_paired_computer(root: &Path) -> anyhow::Result<Option<(PathBuf, ComputerSecrets)>> {
    let mut spaces = match tokio::fs::read_dir(root).await {
        Ok(entries) => entries,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error).context("failed to read Computer state root"),
    };
    let mut found = Vec::new();
    while let Some(space) = spaces.next_entry().await? {
        if !space.file_type().await?.is_dir() || space.file_name() == "pairing" {
            continue;
        }
        let mut computers = tokio::fs::read_dir(space.path()).await?;
        while let Some(computer) = computers.next_entry().await? {
            if !computer.file_type().await?.is_dir() {
                continue;
            }
            let secrets_path = computer.path().join("secrets.json");
            if let Ok(encoded) = tokio::fs::read(&secrets_path).await {
                let secrets = serde_json::from_slice(&encoded)
                    .with_context(|| format!("invalid {}", secrets_path.display()))?;
                found.push((computer.path(), secrets));
            }
        }
    }
    ensure!(
        found.len() <= 1,
        "multiple paired Computers exist under {}; set computer.state_dir to one Computer Home",
        root.display()
    );
    Ok(found.pop())
}

async fn pair(root: &Path, server: &url::Url) -> anyhow::Result<(PathBuf, ComputerSecrets)> {
    let mut token_bytes = [0_u8; 32];
    OsRng.fill_bytes(&mut token_bytes);
    let token = URL_SAFE_NO_PAD.encode(token_bytes);
    let hostname = hostname::get()
        .context("failed to read Computer hostname")?
        .to_string_lossy()
        .into_owned();
    let client = reqwest::Client::new();
    let endpoint = server.join("api/v1/computer-pairings")?;
    let started = client
        .post(endpoint)
        .json(&BeginPairing {
            token_hash: hex::encode(Sha256::digest(token.as_bytes())),
            hostname: &hostname,
            os: if cfg!(target_os = "macos") {
                "macos"
            } else {
                "linux"
            },
            daemon_version: env!("CARGO_PKG_VERSION"),
        })
        .send()
        .await
        .context("failed to start Computer pairing")?
        .error_for_status()
        .context("Server rejected Computer pairing")?
        .json::<PairingStarted>()
        .await
        .context("invalid Computer pairing response")?;
    let pairing_url = server.join(&format!(
        "pair-computer/{}/?code={}",
        started.pairing_id, started.code
    ))?;
    tracing::info!(url=%pairing_url, expires_at=%started.expires_at, "Confirm this Computer in Sumi");

    let status_url = server.join(&format!(
        "api/v1/computer-pairings/{}/status",
        started.pairing_id
    ))?;
    loop {
        tokio::select! {
            _ = tokio::signal::ctrl_c() => anyhow::bail!("Computer pairing canceled"),
            _ = tokio::time::sleep(std::time::Duration::from_secs(2)) => {}
        }
        let status = client
            .get(status_url.clone())
            .bearer_auth(&token)
            .send()
            .await
            .context("failed to read Computer pairing status")?
            .error_for_status()
            .context("Server rejected Computer pairing status")?
            .json::<PairingStatus>()
            .await
            .context("invalid Computer pairing status")?;
        match status.status.as_str() {
            "pending" => continue,
            "expired" => anyhow::bail!("Computer pairing expired"),
            "confirmed" => {
                let computer_id = status
                    .computer_id
                    .context("confirmed pairing has no Computer")?;
                let space_id = status.space_id.context("confirmed pairing has no Space")?;
                let home = root
                    .join(space_id.to_string())
                    .join(computer_id.to_string());
                ensure_private_directory(&home).await?;
                let secrets = ComputerSecrets {
                    computer_id,
                    space_id,
                    token,
                };
                write_secrets(&home.join("secrets.json"), &secrets).await?;
                return Ok((home, secrets));
            }
            _ => anyhow::bail!("Server returned an unknown Computer pairing status"),
        }
    }
}

async fn ensure_private_directory(path: &Path) -> anyhow::Result<()> {
    tokio::fs::create_dir_all(path)
        .await
        .with_context(|| format!("failed to create {}", path.display()))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        tokio::fs::set_permissions(path, std::fs::Permissions::from_mode(0o700)).await?;
    }
    Ok(())
}

async fn write_secrets(path: &Path, secrets: &ComputerSecrets) -> anyhow::Result<()> {
    let encoded = serde_json::to_vec_pretty(secrets)?;
    let mut options = tokio::fs::OpenOptions::new();
    options.create_new(true).write(true);
    let mut file = options
        .open(path)
        .await
        .with_context(|| format!("failed to create {}", path.display()))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        file.set_permissions(std::fs::Permissions::from_mode(0o600))
            .await?;
    }
    use tokio::io::AsyncWriteExt;
    file.write_all(&encoded).await?;
    file.sync_all().await?;
    Ok(())
}
