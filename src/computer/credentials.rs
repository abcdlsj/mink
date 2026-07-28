use std::path::Path;

use anyhow::{Context, Result, bail, ensure};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use sqlx::SqlitePool;
use time::OffsetDateTime;
use url::Url;
use uuid::Uuid;

use crate::driver::builtin_config::BuiltinAuthentication;

#[derive(Deserialize, Serialize)]
#[serde(transparent)]
pub(super) struct ComputerToken(String);

impl ComputerToken {
    pub(super) fn expose(&self) -> &str {
        &self.0
    }
}

#[derive(Deserialize, Serialize)]
pub(super) struct ComputerSecrets {
    pub(super) schema_version: u32,
    pub(super) token: ComputerToken,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub(super) builtin_auth: Option<BuiltinAuthentication>,
    pub(super) pairing_id: Option<Uuid>,
    pub(super) computer_id: Option<Uuid>,
    pub(super) space_id: Option<Uuid>,
}

#[derive(Serialize)]
pub(super) struct PairingStartRequest {
    token_hash: String,
    hostname: String,
    os: String,
    daemon_version: String,
}

#[derive(Deserialize)]
pub(super) struct PairingStartResponse {
    pub(super) pairing_id: Uuid,
    pub(super) browser_path: String,
    #[serde(with = "time::serde::rfc3339")]
    pub(super) expires_at: OffsetDateTime,
}

#[derive(Deserialize)]
#[serde(tag = "status", rename_all = "snake_case")]
pub(super) enum PairingResultResponse {
    Pending,
    Confirmed { computer_id: Uuid, space_id: Uuid },
}

pub(super) enum PairingPollOutcome {
    Confirmed,
    Expired,
    Shutdown,
}

pub(super) async fn reset_deleted_identity(
    database: &SqlitePool,
    secrets_path: &Path,
) -> Result<()> {
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

pub(super) async fn start_pairing(
    server: &Url,
    secrets: &ComputerSecrets,
) -> Result<PairingStartResponse> {
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

pub(super) async fn try_open_browser(url: &Url) {
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

pub(super) async fn poll_pairing(
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

pub(super) async fn prepare_state_dir(path: &Path) -> Result<()> {
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

pub(super) async fn resolve_default_computer_state_dir(root: &Path) -> Result<std::path::PathBuf> {
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
            let secrets = decode_secrets(&bytes)?;
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

pub(super) async fn load_or_create_secrets(path: &Path) -> Result<ComputerSecrets> {
    if path.exists() {
        let metadata = tokio::fs::metadata(path).await?;
        ensure_secure_permissions(path, &metadata, 0o600, "Computer secrets file")?;
        let bytes = tokio::fs::read(path)
            .await
            .context("failed to read Computer secrets")?;
        return decode_secrets(&bytes);
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

pub(super) fn decode_secrets(bytes: &[u8]) -> Result<ComputerSecrets> {
    let secrets: ComputerSecrets =
        serde_json::from_slice(bytes).context("Computer secrets are invalid")?;
    ensure!(
        secrets.schema_version == 1,
        "unsupported Computer secrets schema"
    );
    Ok(secrets)
}

pub(super) async fn sync_builtin_auth(
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

pub(super) async fn write_secrets(path: &Path, secrets: &ComputerSecrets) -> Result<()> {
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

pub(super) fn hostname() -> String {
    hostname::get()
        .ok()
        .and_then(|value| value.into_string().ok())
        .filter(|value| !value.trim().is_empty())
        .unwrap_or_else(|| "local-computer".to_owned())
}

pub(super) fn platform_os() -> Result<&'static str> {
    match std::env::consts::OS {
        "macos" => Ok("macos"),
        "linux" => Ok("linux"),
        other => bail!("unsupported Computer operating system: {other}"),
    }
}

#[cfg(unix)]
pub(super) fn ensure_secure_permissions(
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
pub(super) async fn set_permissions(path: &Path, mode: u32) -> Result<()> {
    use std::os::unix::fs::PermissionsExt;
    tokio::fs::set_permissions(path, std::fs::Permissions::from_mode(mode))
        .await
        .with_context(|| format!("failed to set permissions on {}", path.display()))
}
