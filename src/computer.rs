use std::{path::Path, sync::Arc};

use anyhow::{Context, Result, bail, ensure};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use futures_util::{SinkExt, StreamExt};
use p256::elliptic_curve::{rand_core::OsRng, sec1::ToEncodedPoint};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use sqlx::SqlitePool;
use subtle::ConstantTimeEq;
use time::OffsetDateTime;
use tokio_tungstenite::tungstenite::{self, client::IntoClientRequest};
use url::Url;
use uuid::Uuid;

use crate::{
    cli::ComputerArgs,
    config, database,
    driver::codex::CodexDriver,
    local_protocol::{AgentAction, AgentIdentity, LocalRequest, LocalResponse},
    supervisor::{RunResult, StartRun, Supervisor},
};

#[derive(Deserialize, Serialize)]
struct ComputerSecrets {
    schema_version: u32,
    private_key: String,
    pairing_secret: String,
    computer_credential: String,
    pairing_id: Option<Uuid>,
    computer_id: Option<Uuid>,
    space_id: Option<Uuid>,
}

#[derive(Serialize)]
struct PairingStartRequest {
    pairing_secret_hash: String,
    credential_hash: String,
    public_key: String,
    hostname: String,
    os: String,
    daemon_version: String,
}

#[derive(Deserialize)]
struct PairingStartResponse {
    pairing_id: Uuid,
    browser_path: String,
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
}

pub async fn run(args: ComputerArgs) -> Result<()> {
    let mut config = config::load(args.config.as_ref())?;
    if let Some(server_url) = args.server {
        config.computer.server_url = server_url;
    }
    prepare_state_dir(&config.computer.state_dir).await?;
    let database_path = config.computer.state_dir.join("daemon.db");
    let database = database::connect_sqlite(&database_path).await?;
    crate::supervisor::recover_interrupted_runs(&database).await?;
    prepare_agent_root(&config.computer.state_dir).await?;
    let secrets_path = config.computer.state_dir.join("secrets.json");
    let mut secrets = load_or_create_secrets(&secrets_path).await?;
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
                try_open_browser(&browser_url).await;
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
    let socket_path = config.computer.state_dir.join("daemon.sock");
    let supervisor = Supervisor::new(
        database.clone(),
        config.computer.state_dir.clone(),
        socket_path.clone(),
        &config.computer,
        Arc::new(CodexDriver::new()),
    );
    resume_received_commands(&database, &config.computer.state_dir, &supervisor).await?;
    let ipc = run_local_ipc(
        socket_path.clone(),
        config.computer.state_dir.clone(),
        database.clone(),
        config.computer.server_url.clone(),
        secrets.computer_id.context("paired Computer has no id")?,
        secrets.computer_credential.clone(),
    );
    let connection = connection_loop(
        &config.computer.server_url,
        &config.computer.state_dir,
        &secrets,
        &database,
        supervisor.clone(),
    );
    let result = tokio::select! {
        result = ipc => result,
        result = connection => result,
    };
    supervisor.shutdown().await?;
    let _ = tokio::fs::remove_file(socket_path).await;
    result
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
    for relative in ["memory", "workspace", "drivers/codex", "runs", "logs"] {
        let path = home.join(relative);
        tokio::fs::create_dir_all(&path).await?;
        set_permissions(&path, 0o700).await?;
    }
    Ok(home)
}

async fn run_local_ipc(
    socket_path: std::path::PathBuf,
    state_dir: std::path::PathBuf,
    database: SqlitePool,
    server: Url,
    computer_id: Uuid,
    computer_credential: String,
) -> Result<()> {
    if socket_path.exists() {
        ensure!(
            tokio::net::UnixStream::connect(&socket_path).await.is_err(),
            "another Computer daemon is already using {}",
            socket_path.display()
        );
        tokio::fs::remove_file(&socket_path)
            .await
            .context("failed to remove stale daemon socket")?;
    }
    let listener = tokio::net::UnixListener::bind(&socket_path)
        .context("failed to bind daemon Unix socket")?;
    set_permissions(&socket_path, 0o600).await?;
    loop {
        let (stream, _) = listener.accept().await?;
        let database = database.clone();
        let state_dir = state_dir.clone();
        let server = server.clone();
        let computer_credential = computer_credential.clone();
        tokio::spawn(async move {
            if let Err(error) = handle_local_connection(
                stream,
                &state_dir,
                &database,
                &server,
                computer_id,
                &computer_credential,
            )
            .await
            {
                tracing::warn!(error = %error, "Local Agent CLI request failed");
            }
        });
    }
}

async fn handle_local_connection(
    stream: tokio::net::UnixStream,
    state_dir: &Path,
    database: &SqlitePool,
    server: &Url,
    computer_id: Uuid,
    computer_credential: &str,
) -> Result<()> {
    use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};
    let (reader, mut writer) = stream.into_split();
    let mut line = String::new();
    BufReader::new(reader)
        .take(64 * 1024 + 1)
        .read_line(&mut line)
        .await?;
    ensure!(line.len() <= 64 * 1024, "Local request is too large");
    let request: LocalRequest = serde_json::from_str(&line)?;
    let response = match request {
        LocalRequest::Whoami { run_token } => authenticate_run(database, &run_token)
            .await?
            .map(LocalResponse::success)
            .unwrap_or_else(LocalResponse::denied),
        LocalRequest::AgentAction { run_token, action } => {
            if let Some(identity) = authenticate_run(database, &run_token).await? {
                tracing::debug!(
                    run_id = %identity.run_id,
                    agent_member_id = %identity.agent_member_id,
                    action = action.name(),
                    "Agent local IPC action"
                );
                proxy_agent_action(
                    state_dir,
                    server,
                    computer_id,
                    computer_credential,
                    identity,
                    action,
                )
                .await
            } else {
                LocalResponse::denied()
            }
        }
    };
    writer.write_all(&serde_json::to_vec(&response)?).await?;
    writer.write_all(b"\n").await?;
    Ok(())
}

async fn proxy_agent_action(
    state_dir: &Path,
    server: &Url,
    computer_id: Uuid,
    computer_credential: &str,
    identity: AgentIdentity,
    action: AgentAction,
) -> LocalResponse {
    match action {
        AgentAction::AttachmentUpload {
            path,
            media_type,
            idempotency_key,
        } => {
            return proxy_attachment_upload(
                state_dir,
                server,
                computer_id,
                computer_credential,
                identity,
                &path,
                &media_type,
                idempotency_key,
            )
            .await;
        }
        AgentAction::AttachmentDownload {
            attachment_id,
            output_path,
        } => {
            return proxy_attachment_download(
                state_dir,
                server,
                computer_id,
                computer_credential,
                identity,
                attachment_id,
                &output_path,
            )
            .await;
        }
        AgentAction::AttachmentInfo { attachment_id } => {
            return proxy_attachment_info(
                server,
                computer_id,
                computer_credential,
                identity,
                attachment_id,
            )
            .await;
        }
        action => {
            proxy_json_agent_action(server, computer_id, computer_credential, identity, action)
                .await
        }
    }
}

async fn proxy_json_agent_action(
    server: &Url,
    computer_id: Uuid,
    computer_credential: &str,
    identity: AgentIdentity,
    action: AgentAction,
) -> LocalResponse {
    let endpoint = match server.join(&format!("/api/v1/computers/{computer_id}/agent-actions")) {
        Ok(endpoint) => endpoint,
        Err(_) => return LocalResponse::failure("internal_error", "Server URL is invalid", false),
    };
    let body = serde_json::json!({
        "agent_member_id": identity.agent_member_id,
        "run_id": identity.run_id,
        "action": action,
    });
    let response = match reqwest::Client::new()
        .post(endpoint)
        .bearer_auth(computer_credential)
        .json(&body)
        .send()
        .await
    {
        Ok(response) => response,
        Err(_) => {
            return LocalResponse::failure(
                "server_unavailable",
                "Sumi Server is temporarily unavailable",
                true,
            );
        }
    };
    let status = response.status();
    let payload = match response.json::<serde_json::Value>().await {
        Ok(payload) => payload,
        Err(_) => {
            return LocalResponse::failure(
                "invalid_server_response",
                "Sumi Server returned an invalid response",
                true,
            );
        }
    };
    if status.is_success() {
        LocalResponse::upstream(payload)
    } else {
        let error = payload.get("error");
        LocalResponse::failure(
            error
                .and_then(|value| value.get("code"))
                .and_then(serde_json::Value::as_str)
                .unwrap_or("agent_action_failed"),
            error
                .and_then(|value| value.get("message"))
                .and_then(serde_json::Value::as_str)
                .unwrap_or("Agent action failed"),
            status.is_server_error(),
        )
    }
}

#[allow(clippy::too_many_arguments)]
async fn proxy_attachment_upload(
    state_dir: &Path,
    server: &Url,
    computer_id: Uuid,
    computer_credential: &str,
    identity: AgentIdentity,
    path: &str,
    media_type: &str,
    idempotency_key: Uuid,
) -> LocalResponse {
    let (source, original_name, size, sha256) =
        match prepare_upload_source(state_dir, identity.agent_member_id, path).await {
            Ok(source) => source,
            Err(response) => return response,
        };
    let base = match agent_attachment_base(server, computer_id, &identity) {
        Ok(base) => base,
        Err(response) => return response,
    };
    let create_endpoint = match base.join("uploads") {
        Ok(endpoint) => endpoint,
        Err(_) => return invalid_server_url(),
    };
    let client = reqwest::Client::new();
    let response = match client
        .post(create_endpoint)
        .bearer_auth(computer_credential)
        .header("idempotency-key", idempotency_key.to_string())
        .json(&serde_json::json!({
            "original_name": original_name,
            "media_type": media_type,
        }))
        .send()
        .await
    {
        Ok(response) => response,
        Err(_) => return server_unavailable(),
    };
    let created = match decode_json_response(response).await {
        Ok(payload) => payload,
        Err(response) => return response,
    };
    let attachment_id = match created
        .get("id")
        .and_then(serde_json::Value::as_str)
        .and_then(|value| Uuid::parse_str(value).ok())
    {
        Some(attachment_id) => attachment_id,
        None => {
            return LocalResponse::failure(
                "invalid_server_response",
                "Sumi Server returned invalid Attachment metadata",
                true,
            );
        }
    };
    let content_endpoint = match base.join(&format!("{attachment_id}/content")) {
        Ok(endpoint) => endpoint,
        Err(_) => return invalid_server_url(),
    };
    let file = match tokio::fs::File::open(&source).await {
        Ok(file) => file,
        Err(_) => {
            return LocalResponse::failure(
                "attachment_read_failed",
                "Attachment source could not be opened",
                false,
            );
        }
    };
    let response = match client
        .put(content_endpoint)
        .bearer_auth(computer_credential)
        .body(reqwest::Body::wrap_stream(
            tokio_util::io::ReaderStream::new(file),
        ))
        .send()
        .await
    {
        Ok(response) => response,
        Err(_) => return server_unavailable(),
    };
    if !response.status().is_success() {
        return decode_error_response(response).await;
    }
    let complete_endpoint = match base.join(&format!("{attachment_id}/complete")) {
        Ok(endpoint) => endpoint,
        Err(_) => return invalid_server_url(),
    };
    let response = match client
        .post(complete_endpoint)
        .bearer_auth(computer_credential)
        .header("idempotency-key", idempotency_key.to_string())
        .json(&serde_json::json!({ "size": size, "sha256": sha256 }))
        .send()
        .await
    {
        Ok(response) => response,
        Err(_) => return server_unavailable(),
    };
    match decode_json_response(response).await {
        Ok(payload) => LocalResponse::upstream(payload),
        Err(response) => response,
    }
}

async fn proxy_attachment_info(
    server: &Url,
    computer_id: Uuid,
    computer_credential: &str,
    identity: AgentIdentity,
    attachment_id: Uuid,
) -> LocalResponse {
    let base = match agent_attachment_base(server, computer_id, &identity) {
        Ok(base) => base,
        Err(response) => return response,
    };
    let endpoint = match base.join(&attachment_id.to_string()) {
        Ok(endpoint) => endpoint,
        Err(_) => return invalid_server_url(),
    };
    let response = match reqwest::Client::new()
        .get(endpoint)
        .bearer_auth(computer_credential)
        .send()
        .await
    {
        Ok(response) => response,
        Err(_) => return server_unavailable(),
    };
    match decode_json_response(response).await {
        Ok(payload) => LocalResponse::upstream(payload),
        Err(response) => response,
    }
}

async fn proxy_attachment_download(
    state_dir: &Path,
    server: &Url,
    computer_id: Uuid,
    computer_credential: &str,
    identity: AgentIdentity,
    attachment_id: Uuid,
    output_path: &str,
) -> LocalResponse {
    use tokio::io::AsyncWriteExt;

    let output =
        match prepare_download_target(state_dir, identity.agent_member_id, output_path).await {
            Ok(output) => output,
            Err(response) => return response,
        };
    let base = match agent_attachment_base(server, computer_id, &identity) {
        Ok(base) => base,
        Err(response) => return response,
    };
    let endpoint = match base.join(&format!("{attachment_id}/download")) {
        Ok(endpoint) => endpoint,
        Err(_) => return invalid_server_url(),
    };
    let response = match reqwest::Client::new()
        .get(endpoint)
        .bearer_auth(computer_credential)
        .send()
        .await
    {
        Ok(response) => response,
        Err(_) => return server_unavailable(),
    };
    if !response.status().is_success() {
        return decode_error_response(response).await;
    }
    let mut file = match tokio::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&output)
        .await
    {
        Ok(file) => file,
        Err(_) => {
            return LocalResponse::failure(
                "attachment_output_exists",
                "Attachment output must not already exist",
                false,
            );
        }
    };
    let mut stream = response.bytes_stream();
    while let Some(chunk) = stream.next().await {
        let chunk = match chunk {
            Ok(chunk) => chunk,
            Err(_) => {
                drop(file);
                let _ = tokio::fs::remove_file(&output).await;
                return server_unavailable();
            }
        };
        if file.write_all(&chunk).await.is_err() {
            drop(file);
            let _ = tokio::fs::remove_file(&output).await;
            return LocalResponse::failure(
                "attachment_write_failed",
                "Attachment output could not be written",
                false,
            );
        }
    }
    if file.sync_all().await.is_err() {
        drop(file);
        let _ = tokio::fs::remove_file(&output).await;
        return LocalResponse::failure(
            "attachment_write_failed",
            "Attachment output could not be persisted",
            false,
        );
    }
    LocalResponse::success(serde_json::json!({
        "attachment_id": attachment_id,
        "output_path": output,
    }))
}

async fn prepare_upload_source(
    state_dir: &Path,
    agent_id: Uuid,
    path: &str,
) -> Result<(std::path::PathBuf, String, u64, String), LocalResponse> {
    let home = canonical_agent_home(state_dir, agent_id).await?;
    let source = tokio::fs::canonicalize(path).await.map_err(|_| {
        LocalResponse::failure(
            "attachment_not_found",
            "Attachment source was not found",
            false,
        )
    })?;
    if !source.starts_with(&home) {
        return Err(LocalResponse::failure(
            "permission_denied",
            "Attachment source must be inside the current Agent Home",
            false,
        ));
    }
    let metadata = tokio::fs::metadata(&source).await.map_err(|_| {
        LocalResponse::failure(
            "attachment_read_failed",
            "Attachment source metadata is unavailable",
            false,
        )
    })?;
    if !metadata.is_file() {
        return Err(LocalResponse::failure(
            "invalid_attachment_source",
            "Attachment source must be a regular file",
            false,
        ));
    }
    let original_name = source
        .file_name()
        .and_then(|name| name.to_str())
        .filter(|name| !name.is_empty())
        .ok_or_else(|| {
            LocalResponse::failure(
                "invalid_attachment_name",
                "Attachment source filename must be valid UTF-8",
                false,
            )
        })?
        .to_owned();
    let sha256 = hash_file(&source).await.map_err(|_| {
        LocalResponse::failure(
            "attachment_read_failed",
            "Attachment source could not be read",
            false,
        )
    })?;
    Ok((source, original_name, metadata.len(), sha256))
}

async fn prepare_download_target(
    state_dir: &Path,
    agent_id: Uuid,
    path: &str,
) -> Result<std::path::PathBuf, LocalResponse> {
    let home = canonical_agent_home(state_dir, agent_id).await?;
    let requested = Path::new(path);
    let requested = if requested.is_absolute() {
        requested.to_owned()
    } else {
        home.join("workspace").join(requested)
    };
    let file_name = requested.file_name().ok_or_else(|| {
        LocalResponse::failure(
            "invalid_attachment_output",
            "Attachment output must include a filename",
            false,
        )
    })?;
    let parent = requested.parent().ok_or_else(|| {
        LocalResponse::failure(
            "invalid_attachment_output",
            "Attachment output parent is invalid",
            false,
        )
    })?;
    let parent = tokio::fs::canonicalize(parent).await.map_err(|_| {
        LocalResponse::failure(
            "attachment_output_parent_missing",
            "Attachment output parent does not exist",
            false,
        )
    })?;
    if !parent.starts_with(&home) {
        return Err(LocalResponse::failure(
            "permission_denied",
            "Attachment output must be inside the current Agent Home",
            false,
        ));
    }
    let output = parent.join(file_name);
    if output.exists() {
        return Err(LocalResponse::failure(
            "attachment_output_exists",
            "Attachment output must not already exist",
            false,
        ));
    }
    Ok(output)
}

async fn canonical_agent_home(
    state_dir: &Path,
    agent_id: Uuid,
) -> Result<std::path::PathBuf, LocalResponse> {
    tokio::fs::canonicalize(state_dir.join("agents").join(agent_id.to_string()))
        .await
        .map_err(|_| {
            LocalResponse::failure(
                "agent_home_unavailable",
                "Current Agent Home is unavailable",
                false,
            )
        })
}

async fn hash_file(path: &Path) -> std::io::Result<String> {
    use tokio::io::AsyncReadExt;

    let mut file = tokio::fs::File::open(path).await?;
    let mut digest = Sha256::new();
    let mut buffer = vec![0_u8; 64 * 1024];
    loop {
        let read = file.read(&mut buffer).await?;
        if read == 0 {
            break;
        }
        digest.update(&buffer[..read]);
    }
    Ok(hex::encode(digest.finalize()))
}

fn agent_attachment_base(
    server: &Url,
    computer_id: Uuid,
    identity: &AgentIdentity,
) -> Result<Url, LocalResponse> {
    server
        .join(&format!(
            "/api/v1/computers/{computer_id}/agents/{}/runs/{}/attachments/",
            identity.agent_member_id, identity.run_id
        ))
        .map_err(|_| invalid_server_url())
}

async fn decode_json_response(
    response: reqwest::Response,
) -> Result<serde_json::Value, LocalResponse> {
    if !response.status().is_success() {
        return Err(decode_error_response(response).await);
    }
    response.json::<serde_json::Value>().await.map_err(|_| {
        LocalResponse::failure(
            "invalid_server_response",
            "Sumi Server returned an invalid response",
            true,
        )
    })
}

async fn decode_error_response(response: reqwest::Response) -> LocalResponse {
    let retryable = response.status().is_server_error();
    let payload = response.json::<serde_json::Value>().await.ok();
    let error = payload.as_ref().and_then(|payload| payload.get("error"));
    LocalResponse::failure(
        error
            .and_then(|value| value.get("code"))
            .and_then(serde_json::Value::as_str)
            .unwrap_or("agent_action_failed"),
        error
            .and_then(|value| value.get("message"))
            .and_then(serde_json::Value::as_str)
            .unwrap_or("Agent action failed"),
        retryable,
    )
}

fn invalid_server_url() -> LocalResponse {
    LocalResponse::failure("internal_error", "Server URL is invalid", false)
}

fn server_unavailable() -> LocalResponse {
    LocalResponse::failure(
        "server_unavailable",
        "Sumi Server is temporarily unavailable",
        true,
    )
}

async fn authenticate_run(database: &SqlitePool, run_token: &str) -> Result<Option<AgentIdentity>> {
    let token_hash = Sha256::digest(run_token.as_bytes());
    let rows: Vec<(String, String, String, Vec<u8>)> = sqlx::query_as(
        "SELECT run_id, agent_member_id, space_id, run_token_hash FROM local_agent_runs \
         WHERE status = 'running' AND token_expires_at > ?1",
    )
    .bind(OffsetDateTime::now_utc().to_string())
    .fetch_all(database)
    .await?;
    for (run_id, agent_member_id, space_id, expected_hash) in rows {
        if expected_hash
            .as_slice()
            .ct_eq(token_hash.as_slice())
            .unwrap_u8()
            == 1
        {
            return Ok(Some(AgentIdentity {
                run_id: Uuid::parse_str(&run_id)?,
                agent_member_id: Uuid::parse_str(&agent_member_id)?,
                space_id: Uuid::parse_str(&space_id)?,
            }));
        }
    }
    Ok(None)
}

async fn connection_loop(
    server: &Url,
    state_dir: &Path,
    secrets: &ComputerSecrets,
    database: &SqlitePool,
    supervisor: Supervisor,
) -> Result<()> {
    let mut attempt = 0_u32;
    loop {
        match connect_once(server, state_dir, secrets, database, supervisor.clone()).await {
            Ok(ConnectionOutcome::Shutdown) => return Ok(()),
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
                return Ok(());
            }
        }
    }
}

enum ConnectionOutcome {
    Disconnected,
    Shutdown,
}

async fn connect_once(
    server: &Url,
    state_dir: &Path,
    secrets: &ComputerSecrets,
    database: &SqlitePool,
    supervisor: Supervisor,
) -> Result<ConnectionOutcome> {
    let computer_id = secrets.computer_id.context("paired Computer has no id")?;
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
        format!("Bearer {}", secrets.computer_credential).parse()?,
    );
    let (socket, _) = tokio_tungstenite::connect_async(request)
        .await
        .context("failed to connect Computer WebSocket")?;
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
            }
            _ = attention.tick() => {
                if let Err(error) = poll_agent_inbox(server, computer_id, &secrets.computer_credential).await {
                    tracing::warn!(computer_id = %computer_id, error = %error, "Agent Inbox poll failed");
                }
            }
            completion = completion_rx.recv() => {
                let completion = completion.context("Computer command completion channel closed")?;
                send_ws_frame(&mut writer, &ComputerFrame::CommandResult {
                    command_id: completion.command_id,
                    computer_seq: completion.computer_seq,
                    ok: completion.outcome.ok,
                    result: completion.outcome.result,
                }).await?;
            }
            message = reader.next() => {
                let Some(message) = message else { return Ok(ConnectionOutcome::Disconnected); };
                match message? {
                    tungstenite::Message::Text(text) => {
                        if let ServerFrame::Command { command_id, computer_seq, kind, payload } =
                            serde_json::from_str(&text).context("Server sent an invalid Computer frame")?
                        {
                            persist_command(database, command_id, computer_seq, &kind, &payload).await?;
                            send_ws_frame(&mut writer, &ComputerFrame::CommandAck {
                                command_id,
                                computer_seq,
                            }).await?;
                            if let Some(outcome) = command_processor
                                .process(command_id, computer_seq, &kind, &payload)
                                .await?
                            {
                                send_ws_frame(&mut writer, &ComputerFrame::CommandResult {
                                    command_id,
                                    computer_seq,
                                    ok: outcome.ok,
                                    result: outcome.result,
                                }).await?;
                            }
                        }
                    }
                    tungstenite::Message::Ping(bytes) => writer.send(tungstenite::Message::Pong(bytes)).await?,
                    tungstenite::Message::Close(_) => return Ok(ConnectionOutcome::Disconnected),
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

async fn poll_agent_inbox(server: &Url, computer_id: Uuid, credential: &str) -> Result<()> {
    let client = reqwest::Client::new();
    let agents: Vec<HostedAgent> = client
        .get(server.join(&format!("/api/v1/computers/{computer_id}/agents"))?)
        .bearer_auth(credential)
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
            .bearer_auth(credential)
            .json(&serde_json::json!({}))
            .send()
            .await?
            .error_for_status()?
            .json()
            .await?;
        let _claimed = claim.claimed;
    }
    Ok(())
}

struct LocalCommandOutcome {
    ok: bool,
    result: serde_json::Value,
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
            return Ok(Some(LocalCommandOutcome {
                ok: existing.0 == "completed",
                result: serde_json::from_str(existing.1.as_deref().unwrap_or("{}"))?,
            }));
        }
        if existing.0 == "running" {
            return Ok(None);
        }
        if kind == "agent.memory.read" {
            let outcome = match read_memory_file(&self.state_dir, payload).await {
                Ok(file) => LocalCommandOutcome {
                    ok: true,
                    result: serde_json::to_value(file)?,
                },
                Err(error) => LocalCommandOutcome {
                    ok: false,
                    result: serde_json::json!({
                        "ok": false,
                        "error_code": local_error_code(&error),
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
                        let mut outcome = run_outcome(&result);
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
                Err(error) => {
                    let outcome = LocalCommandOutcome {
                        ok: false,
                        result: serde_json::json!({ "ok": false, "error_code": local_error_code(&error) }),
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
                Err(error) => LocalCommandOutcome {
                    ok: false,
                    result: serde_json::json!({ "ok": false, "error_code": local_error_code(&error) }),
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
            Err(error) => LocalCommandOutcome {
                ok: false,
                result: serde_json::json!({ "ok": false, "error_code": local_error_code(&error) }),
            },
        };
        finish_local_command(&self.database, command_id, &outcome).await?;
        Ok(Some(outcome))
    }
}

fn run_outcome(result: &RunResult) -> LocalCommandOutcome {
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

fn local_error_code(_error: &anyhow::Error) -> &'static str {
    "command_failed"
}

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
        let home = prepare_agent_home(state_dir, agent_id).await?;
        supervisor.prepare_agent_driver(agent_id).await?;
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
        supervisor.validate_agent(agent_id).await?;
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
            Err(error) => LocalCommandOutcome {
                ok: false,
                result: serde_json::json!({
                    "ok": false,
                    "error_code": local_error_code(&error),
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
    let private_key = decode_private_key(&secrets.private_key)?;
    let public_key = private_key.public_key().to_encoded_point(false);
    let pairing_secret = URL_SAFE_NO_PAD
        .decode(&secrets.pairing_secret)
        .context("Computer pairing secret is invalid")?;
    let endpoint = server.join("/api/v1/computer-pairings/start")?;
    let response = reqwest::Client::new()
        .post(endpoint)
        .json(&PairingStartRequest {
            pairing_secret_hash: URL_SAFE_NO_PAD.encode(Sha256::digest(&pairing_secret)),
            credential_hash: URL_SAFE_NO_PAD
                .encode(Sha256::digest(secrets.computer_credential.as_bytes())),
            public_key: URL_SAFE_NO_PAD.encode(public_key.as_bytes()),
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
            .bearer_auth(&secrets.pairing_secret)
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
    let private_key = p256::SecretKey::random(&mut OsRng);
    let mut pairing_secret = [0_u8; 32];
    getrandom::fill(&mut pairing_secret).context("failed to generate Computer pairing secret")?;
    let mut computer_credential = [0_u8; 32];
    getrandom::fill(&mut computer_credential).context("failed to generate Computer credential")?;
    let secrets = ComputerSecrets {
        schema_version: 1,
        private_key: URL_SAFE_NO_PAD.encode(private_key.to_bytes()),
        pairing_secret: URL_SAFE_NO_PAD.encode(pairing_secret),
        computer_credential: URL_SAFE_NO_PAD.encode(computer_credential),
        pairing_id: None,
        computer_id: None,
        space_id: None,
    };
    write_secrets(path, &secrets).await?;
    Ok(secrets)
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

fn decode_private_key(value: &str) -> Result<p256::SecretKey> {
    let bytes = URL_SAFE_NO_PAD
        .decode(value)
        .context("Computer private key is invalid")?;
    p256::SecretKey::from_slice(&bytes).context("Computer private key is invalid")
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
mod tests {
    use super::*;

    #[test]
    fn memory_metadata_uses_rfc3339_on_the_computer_protocol() {
        let metadata = MemoryFileMetadata {
            path: "MEMORY.md".to_owned(),
            size: 9,
            sha256: "00".repeat(32),
            updated_at: OffsetDateTime::UNIX_EPOCH,
        };

        let value = serde_json::to_value(metadata).unwrap();

        assert_eq!(value["updated_at"], "1970-01-01T00:00:00Z");
    }

    #[tokio::test]
    async fn secrets_are_created_with_restricted_permissions_and_reused() {
        let root = tempfile::tempdir().unwrap();
        let state = root.path().join("computer");
        prepare_state_dir(&state).await.unwrap();
        let path = state.join("secrets.json");
        let first = load_or_create_secrets(&path).await.unwrap();
        let second = load_or_create_secrets(&path).await.unwrap();
        assert_eq!(first.private_key, second.private_key);
        assert_eq!(first.pairing_secret, second.pairing_secret);
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                std::fs::metadata(&state).unwrap().permissions().mode() & 0o777,
                0o700
            );
            assert_eq!(
                std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
                0o600
            );
        }
    }

    #[tokio::test]
    async fn insecure_existing_state_directory_is_rejected() {
        let root = tempfile::tempdir().unwrap();
        let state = root.path().join("computer");
        tokio::fs::create_dir(&state).await.unwrap();
        set_permissions(&state, 0o755).await.unwrap();
        let error = prepare_state_dir(&state).await.unwrap_err();
        assert!(error.to_string().contains("chmod 0700"));
    }

    #[tokio::test]
    async fn duplicate_commands_reuse_sqlite_state_and_conflicting_payloads_fail() {
        let root = tempfile::tempdir().unwrap();
        let database = database::connect_sqlite(&root.path().join("daemon.db"))
            .await
            .unwrap();
        let command_id = Uuid::now_v7();
        let payload = serde_json::json!({ "agent_id": Uuid::now_v7() });
        persist_command(&database, command_id, 1, "agent.provision", &payload)
            .await
            .unwrap();
        persist_command(&database, command_id, 1, "agent.provision", &payload)
            .await
            .unwrap();
        assert_eq!(last_acked_sequence(&database).await.unwrap(), 1);
        let count: i64 = sqlx::query_scalar("SELECT count(*) FROM server_commands")
            .fetch_one(&database)
            .await
            .unwrap();
        assert_eq!(count, 1);

        let error = persist_command(
            &database,
            command_id,
            1,
            "agent.provision",
            &serde_json::json!({ "agent_id": Uuid::now_v7() }),
        )
        .await
        .unwrap_err();
        assert!(error.to_string().contains("different content"));
    }

    #[tokio::test]
    async fn restart_recovers_runs_and_received_provision_commands() {
        let root = tempfile::tempdir().unwrap();
        let state = root.path().join("computer");
        prepare_state_dir(&state).await.unwrap();
        prepare_agent_root(&state).await.unwrap();
        let database = database::connect_sqlite(&state.join("daemon.db"))
            .await
            .unwrap();
        let run_id = Uuid::now_v7();
        let agent_id = Uuid::now_v7();
        let space_id = Uuid::now_v7();
        sqlx::query(
            "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
             token_expires_at, status, started_at) VALUES (?1, ?2, ?3, ?4, ?5, 'running', ?6)",
        )
        .bind(run_id.to_string())
        .bind(agent_id.to_string())
        .bind(space_id.to_string())
        .bind(Sha256::digest(b"token").to_vec())
        .bind((OffsetDateTime::now_utc() + time::Duration::hours(1)).to_string())
        .bind(OffsetDateTime::now_utc().to_string())
        .execute(&database)
        .await
        .unwrap();
        let command_id = Uuid::now_v7();
        persist_command(
            &database,
            command_id,
            1,
            "agent.provision",
            &serde_json::json!({ "agent_id": agent_id }),
        )
        .await
        .unwrap();

        crate::supervisor::recover_interrupted_runs(&database)
            .await
            .unwrap();
        let supervisor = Supervisor::new(
            database.clone(),
            state.clone(),
            state.join("daemon.sock"),
            &crate::config::ComputerConfig::default(),
            Arc::new(CodexDriver::new()),
        );
        resume_received_commands(&database, &state, &supervisor)
            .await
            .unwrap();

        let recovered: (String, Option<String>) = sqlx::query_as(
            "SELECT status, last_error_code FROM local_agent_runs WHERE run_id = ?1",
        )
        .bind(run_id.to_string())
        .fetch_one(&database)
        .await
        .unwrap();
        assert_eq!(
            recovered,
            ("failed".to_owned(), Some("process_lost".to_owned()))
        );
        let command_status: String =
            sqlx::query_scalar("SELECT status FROM server_commands WHERE command_id = ?1")
                .bind(command_id.to_string())
                .fetch_one(&database)
                .await
                .unwrap();
        assert_eq!(command_status, "completed");
        let home = state.join("agents").join(agent_id.to_string());
        let profile: serde_json::Value =
            serde_json::from_slice(&tokio::fs::read(home.join("profile.json")).await.unwrap())
                .unwrap();
        assert_eq!(profile["agent_id"], agent_id.to_string());
        assert_eq!(profile["status"], "active");
        assert!(profile.get("computer_credential").is_none());
        assert_eq!(
            tokio::fs::read_to_string(home.join("memory/MEMORY.md"))
                .await
                .unwrap(),
            "# Memory\n"
        );
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            for relative in ["", "memory", "workspace", "drivers/codex", "runs", "logs"] {
                let path = state
                    .join("agents")
                    .join(agent_id.to_string())
                    .join(relative);
                assert_eq!(
                    std::fs::metadata(path).unwrap().permissions().mode() & 0o777,
                    0o700
                );
            }
        }
    }

    #[tokio::test]
    async fn lifecycle_commands_update_profile_and_report_memory_metadata() {
        let root = tempfile::tempdir().unwrap();
        let state = root.path().join("computer");
        prepare_state_dir(&state).await.unwrap();
        prepare_agent_root(&state).await.unwrap();
        let database = database::connect_sqlite(&state.join("daemon.db"))
            .await
            .unwrap();
        let agent_id = Uuid::now_v7();
        let supervisor = Supervisor::new(
            database,
            state.clone(),
            state.join("daemon.sock"),
            &crate::config::ComputerConfig::default(),
            Arc::new(CodexDriver::new()),
        );
        let provision = serde_json::json!({
            "agent_id": agent_id,
            "space_id": Uuid::now_v7(),
            "name": "Lin",
            "handle": "lin",
            "role_text": "Review boundaries.",
            "role_revision": 1,
            "driver_kind": "codex",
            "driver_config": { "schema_version": 1 },
            "attention_config": {
                "dm_immediate": true,
                "mention_immediate": true,
                "ambient_enabled": true,
                "ambient_debounce_seconds": 5,
                "ambient_max_wait_seconds": 30,
                "max_retry_count": 3
            }
        });
        let files = execute_local_command(&state, "agent.provision", &provision, &supervisor)
            .await
            .unwrap();
        assert_eq!(files.len(), 1);
        assert_eq!(files[0].path, "MEMORY.md");
        assert_eq!(files[0].sha256, hex::encode(Sha256::digest(b"# Memory\n")));

        execute_local_command(
            &state,
            "agent.configure",
            &serde_json::json!({
                "agent_id": agent_id,
                "role_text": "Enforce the current specification.",
                "role_revision": 2,
                "attention_config": provision["attention_config"]
            }),
            &supervisor,
        )
        .await
        .unwrap();
        execute_local_command(
            &state,
            "agent.suspend",
            &serde_json::json!({ "agent_id": agent_id, "mode": "cancel_now" }),
            &supervisor,
        )
        .await
        .unwrap();
        let profile_path = state
            .join("agents")
            .join(agent_id.to_string())
            .join("profile.json");
        let suspended: serde_json::Value =
            serde_json::from_slice(&tokio::fs::read(&profile_path).await.unwrap()).unwrap();
        assert_eq!(suspended["status"], "suspended");
        assert_eq!(suspended["role_revision"], 2);

        execute_local_command(
            &state,
            "agent.resume",
            &serde_json::json!({ "agent_id": agent_id }),
            &supervisor,
        )
        .await
        .unwrap();
        let resumed: serde_json::Value =
            serde_json::from_slice(&tokio::fs::read(&profile_path).await.unwrap()).unwrap();
        assert_eq!(resumed["status"], "active");

        execute_local_command(
            &state,
            "agent.retire",
            &serde_json::json!({ "agent_id": agent_id }),
            &supervisor,
        )
        .await
        .unwrap();
        let retired: serde_json::Value =
            serde_json::from_slice(&tokio::fs::read(profile_path).await.unwrap()).unwrap();
        assert_eq!(retired["status"], "retired");
    }

    #[tokio::test]
    async fn memory_read_is_scoped_to_agent_memory_and_rejects_symlinks() {
        let root = tempfile::tempdir().unwrap();
        let state = root.path().join("computer");
        let agent_id = Uuid::now_v7();
        let memory = state
            .join("agents")
            .join(agent_id.to_string())
            .join("memory");
        tokio::fs::create_dir_all(memory.join("notes"))
            .await
            .unwrap();
        tokio::fs::write(memory.join("notes/current.md"), b"Current facts.\n")
            .await
            .unwrap();
        let content = read_memory_file(
            &state,
            &serde_json::json!({ "agent_id": agent_id, "path": "notes/current.md" }),
        )
        .await
        .unwrap();
        assert_eq!(content.path, "notes/current.md");
        assert_eq!(content.content, "Current facts.\n");

        let escaped = read_memory_file(
            &state,
            &serde_json::json!({ "agent_id": agent_id, "path": "../profile.json" }),
        )
        .await
        .unwrap_err();
        assert!(escaped.to_string().contains("invalid"));

        #[cfg(unix)]
        {
            std::os::unix::fs::symlink(memory.join("notes/current.md"), memory.join("linked.md"))
                .unwrap();
            let linked = read_memory_file(
                &state,
                &serde_json::json!({ "agent_id": agent_id, "path": "linked.md" }),
            )
            .await
            .unwrap_err();
            assert!(linked.to_string().contains("symlink"));
        }
    }

    #[tokio::test]
    async fn local_run_capability_is_scoped_and_expires() {
        let root = tempfile::tempdir().unwrap();
        let database = database::connect_sqlite(&root.path().join("daemon.db"))
            .await
            .unwrap();
        let run_id = Uuid::now_v7();
        let agent_id = Uuid::now_v7();
        let space_id = Uuid::now_v7();
        sqlx::query(
            "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
             token_expires_at, status) VALUES (?1, ?2, ?3, ?4, ?5, 'running')",
        )
        .bind(run_id.to_string())
        .bind(agent_id.to_string())
        .bind(space_id.to_string())
        .bind(Sha256::digest(b"correct").to_vec())
        .bind((OffsetDateTime::now_utc() + time::Duration::minutes(5)).to_string())
        .execute(&database)
        .await
        .unwrap();
        let identity = authenticate_run(&database, "correct")
            .await
            .unwrap()
            .unwrap();
        assert_eq!(identity.agent_member_id, agent_id);
        assert!(
            authenticate_run(&database, "wrong")
                .await
                .unwrap()
                .is_none()
        );
        sqlx::query("UPDATE local_agent_runs SET token_expires_at = ?2 WHERE run_id = ?1")
            .bind(run_id.to_string())
            .bind((OffsetDateTime::now_utc() - time::Duration::seconds(1)).to_string())
            .execute(&database)
            .await
            .unwrap();
        assert!(
            authenticate_run(&database, "correct")
                .await
                .unwrap()
                .is_none()
        );
    }

    #[tokio::test]
    async fn local_socket_protocol_returns_scoped_agent_identity() {
        use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};

        let root = tempfile::tempdir().unwrap();
        let database = database::connect_sqlite(&root.path().join("daemon.db"))
            .await
            .unwrap();
        let run_id = Uuid::now_v7();
        let agent_id = Uuid::now_v7();
        let space_id = Uuid::now_v7();
        sqlx::query(
            "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
             token_expires_at, status) VALUES (?1, ?2, ?3, ?4, ?5, 'running')",
        )
        .bind(run_id.to_string())
        .bind(agent_id.to_string())
        .bind(space_id.to_string())
        .bind(Sha256::digest(b"socket-token").to_vec())
        .bind((OffsetDateTime::now_utc() + time::Duration::minutes(5)).to_string())
        .execute(&database)
        .await
        .unwrap();
        let (client, server) = tokio::net::UnixStream::pair().unwrap();
        let task = tokio::spawn({
            let database = database.clone();
            let state_dir = root.path().to_owned();
            async move {
                handle_local_connection(
                    server,
                    &state_dir,
                    &database,
                    &Url::parse("http://127.0.0.1:1").unwrap(),
                    Uuid::now_v7(),
                    "credential",
                )
                .await
            }
        });
        let (reader, mut writer) = client.into_split();
        let request = LocalRequest::Whoami {
            run_token: "socket-token".to_owned(),
        };
        writer
            .write_all(format!("{}\n", serde_json::to_string(&request).unwrap()).as_bytes())
            .await
            .unwrap();
        let mut response = String::new();
        BufReader::new(reader)
            .read_line(&mut response)
            .await
            .unwrap();
        let response: LocalResponse = serde_json::from_str(&response).unwrap();
        assert!(response.ok);
        let identity: AgentIdentity = serde_json::from_value(response.data.unwrap()).unwrap();
        assert_eq!(identity.run_id, run_id);
        assert_eq!(identity.agent_member_id, agent_id);
        assert_eq!(identity.space_id, space_id);
        task.await.unwrap().unwrap();
    }

    #[tokio::test]
    async fn attachment_paths_cannot_escape_agent_home_or_overwrite_files() {
        let root = tempfile::tempdir().unwrap();
        prepare_agent_root(root.path()).await.unwrap();
        let agent_id = Uuid::now_v7();
        let home = prepare_agent_home(root.path(), agent_id).await.unwrap();
        let source = home.join("workspace/report.txt");
        tokio::fs::write(&source, b"report").await.unwrap();
        let prepared = prepare_upload_source(root.path(), agent_id, source.to_str().unwrap())
            .await
            .unwrap();
        assert_eq!(prepared.1, "report.txt");
        assert_eq!(prepared.2, 6);
        assert_eq!(prepared.3, hex::encode(Sha256::digest(b"report")));

        let outside = root.path().join("outside.txt");
        tokio::fs::write(&outside, b"private").await.unwrap();
        let denied = prepare_upload_source(root.path(), agent_id, outside.to_str().unwrap())
            .await
            .err()
            .unwrap();
        assert_eq!(denied.error.unwrap().code, "permission_denied");

        let output = home.join("workspace/download.txt");
        let expected_output = tokio::fs::canonicalize(home.join("workspace"))
            .await
            .unwrap()
            .join("download.txt");
        assert_eq!(
            prepare_download_target(root.path(), agent_id, output.to_str().unwrap())
                .await
                .unwrap(),
            expected_output
        );
        tokio::fs::write(&output, b"existing").await.unwrap();
        let existing = prepare_download_target(root.path(), agent_id, output.to_str().unwrap())
            .await
            .err()
            .unwrap();
        assert_eq!(existing.error.unwrap().code, "attachment_output_exists");

        let denied = prepare_download_target(
            root.path(),
            agent_id,
            root.path().join("escape.txt").to_str().unwrap(),
        )
        .await
        .err()
        .unwrap();
        assert_eq!(denied.error.unwrap().code, "permission_denied");
    }

    #[test]
    fn reconnect_backoff_is_bounded_and_jittered_above_base() {
        for attempt in 0..10 {
            let delay = reconnect_delay(attempt).as_millis() as u64;
            let base = 1_000_u64
                .saturating_mul(1_u64 << attempt.min(5))
                .min(30_000);
            assert!(delay >= base && delay <= base + base / 4);
        }
    }
}
