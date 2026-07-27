#![allow(dead_code)]

use std::{
    ffi::OsStr,
    net::{IpAddr, Ipv4Addr, SocketAddr},
    path::{Path, PathBuf},
    process::Stdio,
    str::FromStr,
    sync::{Arc, Mutex},
};

use anyhow::{Context, Result, bail, ensure};
use reqwest::{Client, StatusCode, header};
use serde::Deserialize;
use sqlx::{Connection, Executor, PgConnection, postgres::PgConnectOptions};
use tokio::{
    io::{AsyncBufReadExt, BufReader},
    net::{TcpListener, TcpStream},
    process::{Child, Command},
    sync::{mpsc, watch},
    task::JoinHandle,
};
use url::Url;
use uuid::Uuid;

#[derive(Deserialize)]
pub struct SpaceResponse {
    pub id: Uuid,
    pub general_channel_id: Uuid,
}

#[derive(Deserialize)]
pub struct ComputerResponse {
    pub id: Uuid,
    pub status: String,
}

pub struct TestDatabase {
    admin_url: String,
    name: String,
    pub url: String,
}

impl TestDatabase {
    pub async fn create(prefix: &str) -> Result<Self> {
        let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
            .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
        let name = format!("{prefix}_{}", Uuid::now_v7().simple());
        let mut admin =
            PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url)?).await?;
        admin
            .execute(format!("CREATE DATABASE \"{name}\"").as_str())
            .await
            .context("create isolated PostgreSQL database")?;
        let mut url = Url::parse(&admin_url)?;
        url.set_path(&format!("/{name}"));
        Ok(Self {
            admin_url,
            name,
            url: url.to_string(),
        })
    }

    pub async fn drop(self) -> Result<()> {
        let mut admin =
            PgConnection::connect_with(&PgConnectOptions::from_str(&self.admin_url)?).await?;
        admin
            .execute(format!("DROP DATABASE \"{}\" WITH (FORCE)", self.name).as_str())
            .await
            .context("drop isolated PostgreSQL database")?;
        Ok(())
    }
}

pub struct SumiProcess {
    child: Child,
    stderr_lines: mpsc::UnboundedReceiver<String>,
    logs: Arc<Mutex<Vec<String>>>,
}

impl SumiProcess {
    pub fn spawn<I, S>(args: I, environment: &[(&str, &OsStr)]) -> Result<Self>
    where
        I: IntoIterator<Item = S>,
        S: AsRef<OsStr>,
    {
        let mut command = Command::new(env!("CARGO_BIN_EXE_sumi"));
        command
            .args(args)
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::piped())
            .kill_on_drop(true);
        for (key, value) in environment {
            command.env(key, value);
        }
        let mut child = command.spawn().context("spawn sumi process")?;
        let stderr = child.stderr.take().context("capture sumi stderr")?;
        let (sender, stderr_lines) = mpsc::unbounded_channel();
        let logs = Arc::new(Mutex::new(Vec::new()));
        let collected = logs.clone();
        tokio::spawn(async move {
            let mut lines = BufReader::new(stderr).lines();
            while let Ok(Some(line)) = lines.next_line().await {
                if let Ok(mut logs) = collected.lock() {
                    logs.push(line.clone());
                }
                let _ = sender.send(line);
            }
        });
        Ok(Self {
            child,
            stderr_lines,
            logs,
        })
    }

    pub async fn wait_for_stderr(
        &mut self,
        needle: &str,
        duration: std::time::Duration,
    ) -> Result<String> {
        tokio::time::timeout(duration, async {
            while let Some(line) = self.stderr_lines.recv().await {
                if line.contains(needle) {
                    return Ok(line);
                }
            }
            bail!("sumi process closed stderr before emitting {needle:?}")
        })
        .await
        .with_context(|| {
            format!(
                "timed out waiting for {needle:?}; logs: {}",
                self.log_text()
            )
        })?
    }

    pub fn ensure_running(&mut self) -> Result<()> {
        ensure!(
            self.child.try_wait()?.is_none(),
            "sumi process exited unexpectedly; logs: {}",
            self.log_text()
        );
        Ok(())
    }

    pub fn logs_contain(&self, needle: &str) -> bool {
        self.logs
            .lock()
            .expect("test process log lock poisoned")
            .iter()
            .any(|line| line.contains(needle))
    }

    pub async fn interrupt(&mut self) -> Result<()> {
        let pid = self.child.id().context("sumi process has no pid")?;
        // Sumi's supported Computer and Server platforms are Unix; SIGINT exercises graceful shutdown.
        let result = unsafe { libc::kill(pid as libc::pid_t, libc::SIGINT) };
        ensure!(result == 0, "send SIGINT to sumi process");
        let status = tokio::time::timeout(std::time::Duration::from_secs(15), self.child.wait())
            .await
            .with_context(|| format!("sumi process ignored SIGINT; logs: {}", self.log_text()))??;
        ensure!(
            status.success(),
            "sumi process failed during graceful shutdown: {status}; logs: {}",
            self.log_text()
        );
        Ok(())
    }

    pub async fn crash(&mut self) -> Result<()> {
        self.child.kill().await.context("force-kill sumi process")?;
        let status = self.child.wait().await?;
        ensure!(
            !status.success(),
            "force-killed sumi process exited successfully; logs: {}",
            self.log_text()
        );
        Ok(())
    }

    pub async fn wait_for_success(&mut self, duration: std::time::Duration) -> Result<()> {
        let status = tokio::time::timeout(duration, self.child.wait())
            .await
            .with_context(|| {
                format!(
                    "sumi process did not exit within {duration:?}; logs: {}",
                    self.log_text()
                )
            })??;
        ensure!(
            status.success(),
            "sumi process exited unsuccessfully: {status}; logs: {}",
            self.log_text()
        );
        Ok(())
    }

    fn log_text(&self) -> String {
        self.logs
            .lock()
            .map(|logs| logs.join("\n"))
            .unwrap_or_else(|_| "<log lock poisoned>".to_owned())
    }
}

pub struct TcpCutProxy {
    address: SocketAddr,
    enabled: watch::Sender<bool>,
    task: JoinHandle<()>,
}

impl TcpCutProxy {
    pub async fn start(upstream: SocketAddr) -> Result<Self> {
        let listener = TcpListener::bind((Ipv4Addr::LOCALHOST, 0)).await?;
        let address = listener.local_addr()?;
        let (enabled, mut state) = watch::channel(true);
        let task = tokio::spawn(async move {
            loop {
                tokio::select! {
                    accepted = listener.accept() => {
                        let Ok((inbound, _)) = accepted else { break };
                        if !*state.borrow() {
                            drop(inbound);
                            continue;
                        }
                        let connection_state = state.clone();
                        tokio::spawn(proxy_connection(inbound, upstream, connection_state));
                    }
                    changed = state.changed() => {
                        if changed.is_err() {
                            break;
                        }
                    }
                }
            }
        });
        Ok(Self {
            address,
            enabled,
            task,
        })
    }

    pub fn url(&self) -> Result<Url> {
        Ok(Url::parse(&format!("http://{}", self.address))?)
    }

    pub fn set_enabled(&self, enabled: bool) {
        self.enabled.send_replace(enabled);
    }
}

impl Drop for TcpCutProxy {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn proxy_connection(
    mut inbound: TcpStream,
    upstream: SocketAddr,
    mut state: watch::Receiver<bool>,
) {
    let Ok(mut outbound) = TcpStream::connect(upstream).await else {
        return;
    };
    let transfer = tokio::io::copy_bidirectional(&mut inbound, &mut outbound);
    tokio::pin!(transfer);
    loop {
        tokio::select! {
            _ = &mut transfer => return,
            changed = state.changed() => {
                if changed.is_err() || !*state.borrow() {
                    return;
                }
            }
        }
    }
}

pub fn reserve_local_port() -> Result<u16> {
    let listener = std::net::TcpListener::bind((IpAddr::V4(Ipv4Addr::LOCALHOST), 0))?;
    Ok(listener.local_addr()?.port())
}

pub async fn wait_for_health(server: &Url) -> Result<()> {
    let endpoint = server.join("/api/v1/health")?;
    tokio::time::timeout(std::time::Duration::from_secs(15), async {
        loop {
            if let Ok(response) = reqwest::get(endpoint.clone()).await
                && response.status().is_success()
            {
                return;
            }
            tokio::time::sleep(std::time::Duration::from_millis(100)).await;
        }
    })
    .await
    .context("Sumi Server did not become healthy")
}

pub fn write_server_config(
    path: &Path,
    bind: SocketAddr,
    database_url: &str,
    attachment_dir: &Path,
    web_dist: &Path,
) -> Result<()> {
    std::fs::write(
        path,
        format!(
            "[server]\nbind = '{bind}'\ndatabase_url = '{database_url}'\nattachment_dir = '{}'\nweb_dist = '{}'\n",
            attachment_dir.display(),
            web_dist.display()
        ),
    )?;
    Ok(())
}

pub fn write_computer_config(path: &Path, server: &Url, state_dir: &Path) -> Result<()> {
    std::fs::write(
        path,
        format!(
            "[computer]\nserver_url = '{server}'\nstate_dir = '{}'\nopen_pairing_browser = false\nshutdown_grace_period_seconds = 1\n",
            state_dir.display()
        ),
    )?;
    Ok(())
}

pub fn write_builtin_computer_config(
    path: &Path,
    server: &Url,
    state_dir: &Path,
    provider_base_url: &Url,
) -> Result<()> {
    use std::os::unix::fs::PermissionsExt;

    let settings = state_dir
        .parent()
        .context("Computer state has no parent")?
        .join("builtin-settings.json");
    let models = state_dir
        .parent()
        .context("Computer state has no parent")?
        .join("builtin-models.json");
    let auth = state_dir
        .parent()
        .context("Computer state has no parent")?
        .join("builtin-auth.json");
    std::fs::write(
        &settings,
        serde_json::to_vec(&serde_json::json!({
            "defaultProvider": "local-test",
            "defaultModel": "sumi-test-model"
        }))?,
    )?;
    std::fs::write(
        &models,
        serde_json::to_vec(&serde_json::json!({
            "local-test": {
                "models": [{
                    "id": "sumi-test-model",
                    "api": "openai-completions",
                    "baseUrl": provider_base_url
                }]
            }
        }))?,
    )?;
    std::fs::write(
        &auth,
        serde_json::to_vec(&serde_json::json!({
            "local-test": { "type": "api_key", "key": "test-only-provider-key" }
        }))?,
    )?;
    std::fs::set_permissions(&auth, std::fs::Permissions::from_mode(0o600))?;
    std::fs::write(
        path,
        format!(
            "[computer]\nserver_url = '{server}'\nstate_dir = '{}'\nopen_pairing_browser = false\nbuiltin_settings_source = '{}'\nbuiltin_models_source = '{}'\nbuiltin_auth_source = '{}'\nmax_concurrent_runs = 1\nper_agent_timeout_seconds = 60\nshutdown_grace_period_seconds = 1\n",
            state_dir.display(),
            settings.display(),
            models.display(),
            auth.display(),
        ),
    )?;
    Ok(())
}

pub fn spawn_server(config: &Path) -> Result<SumiProcess> {
    SumiProcess::spawn(
        [
            OsStr::new("server"),
            OsStr::new("--config"),
            config.as_os_str(),
        ],
        &[],
    )
}

pub fn spawn_computer(config: &Path) -> Result<SumiProcess> {
    SumiProcess::spawn(
        [
            OsStr::new("computer"),
            OsStr::new("--config"),
            config.as_os_str(),
        ],
        &[],
    )
}

pub async fn register_human(client: &Client, server: &Url) -> Result<String> {
    let response = client
        .post(server.join("/api/v1/auth/register")?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .json(&serde_json::json!({
            "display_name": "Process Test Owner",
            "email": format!("process-{}@example.test", Uuid::now_v7()),
            "password": "correct horse battery staple"
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "register Human: {}",
        response.status()
    );
    Ok(response
        .headers()
        .get(header::SET_COOKIE)
        .context("registration did not set a Session cookie")?
        .to_str()?
        .split(';')
        .next()
        .context("Session cookie is empty")?
        .to_owned())
}

pub async fn create_space(client: &Client, server: &Url, cookie: &str) -> Result<SpaceResponse> {
    let response = client
        .post(server.join("/api/v1/spaces")?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({
            "name": "Process Test Lab",
            "slug": format!("process-{}", &Uuid::now_v7().simple().to_string()[..8]),
            "accent": "#5065D8"
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "create Space: {}",
        response.status()
    );
    Ok(response.json().await?)
}

pub async fn pairing_url_from_daemon(daemon: &mut SumiProcess) -> Result<Url> {
    let line = daemon
        .wait_for_stderr(
            "Open this URL to pair the Computer",
            std::time::Duration::from_secs(15),
        )
        .await?;
    let raw = line
        .split_once("url=")
        .context("pairing log did not include a URL")?
        .1
        .split_whitespace()
        .next()
        .context("pairing URL is empty")?;
    Ok(Url::parse(raw.trim_matches('"'))?)
}

pub async fn confirm_pairing(
    client: &Client,
    server: &Url,
    cookie: &str,
    space_id: Uuid,
    pairing_url: &Url,
) -> Result<ComputerResponse> {
    let pairing_id = pairing_url
        .path_segments()
        .and_then(|mut segments| segments.nth(1))
        .context("pairing URL has no pairing id")?
        .parse::<Uuid>()?;
    let code = pairing_url
        .query_pairs()
        .find_map(|(key, value)| (key == "code").then(|| value.into_owned()))
        .context("pairing URL has no code")?;
    let response = client
        .post(server.join(&format!("/api/v1/computer-pairings/{pairing_id}/confirm"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({
            "space_id": space_id,
            "name": "Process Test Computer",
            "code": code
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "confirm pairing: {}",
        response.status()
    );
    Ok(response.json().await?)
}

pub async fn wait_for_computer_status(
    client: &Client,
    server: &Url,
    cookie: &str,
    space_id: Uuid,
    expected: &str,
) -> Result<ComputerResponse> {
    tokio::time::timeout(std::time::Duration::from_secs(20), async {
        loop {
            let response = client
                .get(server.join(&format!("/api/v1/spaces/{space_id}/computers"))?)
                .header(header::COOKIE, cookie)
                .send()
                .await?;
            if response.status().is_success() {
                let computers: Vec<ComputerResponse> = response.json().await?;
                if let Some(computer) = computers
                    .into_iter()
                    .find(|computer| computer.status == expected)
                {
                    return Ok(computer);
                }
            }
            tokio::time::sleep(std::time::Duration::from_millis(100)).await;
        }
    })
    .await
    .with_context(|| format!("Computer did not become {expected}"))?
}

pub fn empty_path(root: &Path) -> Result<PathBuf> {
    let path = root.join("empty-path");
    std::fs::create_dir(&path)?;
    Ok(path)
}
