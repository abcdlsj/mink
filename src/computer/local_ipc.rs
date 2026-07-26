use std::path::Path;

use anyhow::{Context, Result, ensure};
use futures_util::StreamExt;
use sha2::{Digest, Sha256};
use sqlx::SqlitePool;
use subtle::ConstantTimeEq;
use time::OffsetDateTime;
use url::Url;
use uuid::Uuid;

use crate::local_protocol::{AgentAction, AgentIdentity, LocalRequest, LocalResponse};

pub(super) async fn run(
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
    super::set_permissions(&socket_path, 0o600).await?;
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

pub(super) async fn handle_local_connection(
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
                    &AgentProxy {
                        state_dir,
                        server,
                        computer_id,
                        computer_credential,
                        identity: &identity,
                    },
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

struct AgentProxy<'a> {
    state_dir: &'a Path,
    server: &'a Url,
    computer_id: Uuid,
    computer_credential: &'a str,
    identity: &'a AgentIdentity,
}

async fn proxy_agent_action(context: &AgentProxy<'_>, action: AgentAction) -> LocalResponse {
    match action {
        AgentAction::AttachmentUpload {
            path,
            media_type,
            idempotency_key,
        } => {
            return proxy_attachment_upload(context, &path, &media_type, idempotency_key).await;
        }
        AgentAction::AttachmentDownload {
            attachment_id,
            output_path,
        } => {
            return proxy_attachment_download(context, attachment_id, &output_path).await;
        }
        AgentAction::AttachmentInfo { attachment_id } => {
            return proxy_attachment_info(context, attachment_id).await;
        }
        action => proxy_json_agent_action(context, action).await,
    }
}

async fn proxy_json_agent_action(context: &AgentProxy<'_>, action: AgentAction) -> LocalResponse {
    let endpoint = match context.server.join(&format!(
        "/api/v1/computers/{}/agent-actions",
        context.computer_id
    )) {
        Ok(endpoint) => endpoint,
        Err(_) => return LocalResponse::failure("internal_error", "Server URL is invalid", false),
    };
    let body = serde_json::json!({
        "agent_member_id": context.identity.agent_member_id,
        "run_id": context.identity.run_id,
        "action": action,
    });
    let response = match reqwest::Client::new()
        .post(endpoint)
        .bearer_auth(context.computer_credential)
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
        LocalResponse::failure_with_details(
            error
                .and_then(|value| value.get("code"))
                .and_then(serde_json::Value::as_str)
                .unwrap_or("agent_action_failed"),
            error
                .and_then(|value| value.get("message"))
                .and_then(serde_json::Value::as_str)
                .unwrap_or("Agent action failed"),
            status.is_server_error(),
            error.and_then(|value| value.get("details")).cloned(),
        )
    }
}

async fn proxy_attachment_upload(
    context: &AgentProxy<'_>,
    path: &str,
    media_type: &str,
    idempotency_key: Uuid,
) -> LocalResponse {
    let (source, original_name, size, sha256) = match prepare_upload_source(
        context.state_dir,
        context.identity.agent_member_id,
        path,
    )
    .await
    {
        Ok(source) => source,
        Err(response) => return response,
    };
    let base = match agent_attachment_base(context.server, context.computer_id, context.identity) {
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
        .bearer_auth(context.computer_credential)
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
        .bearer_auth(context.computer_credential)
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
        .bearer_auth(context.computer_credential)
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

async fn proxy_attachment_info(context: &AgentProxy<'_>, attachment_id: Uuid) -> LocalResponse {
    let base = match agent_attachment_base(context.server, context.computer_id, context.identity) {
        Ok(base) => base,
        Err(response) => return response,
    };
    let endpoint = match base.join(&attachment_id.to_string()) {
        Ok(endpoint) => endpoint,
        Err(_) => return invalid_server_url(),
    };
    let response = match reqwest::Client::new()
        .get(endpoint)
        .bearer_auth(context.computer_credential)
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
    context: &AgentProxy<'_>,
    attachment_id: Uuid,
    output_path: &str,
) -> LocalResponse {
    use tokio::io::AsyncWriteExt;

    let output = match prepare_download_target(
        context.state_dir,
        context.identity.agent_member_id,
        output_path,
    )
    .await
    {
        Ok(output) => output,
        Err(response) => return response,
    };
    let base = match agent_attachment_base(context.server, context.computer_id, context.identity) {
        Ok(base) => base,
        Err(response) => return response,
    };
    let endpoint = match base.join(&format!("{attachment_id}/download")) {
        Ok(endpoint) => endpoint,
        Err(_) => return invalid_server_url(),
    };
    let response = match reqwest::Client::new()
        .get(endpoint)
        .bearer_auth(context.computer_credential)
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

pub(super) async fn prepare_upload_source(
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

pub(super) async fn prepare_download_target(
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

pub(super) async fn authenticate_run(
    database: &SqlitePool,
    run_token: &str,
) -> Result<Option<AgentIdentity>> {
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
