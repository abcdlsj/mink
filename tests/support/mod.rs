use std::{
    ffi::OsStr,
    net::{IpAddr, Ipv4Addr, SocketAddr},
    path::{Path, PathBuf},
    process::Stdio,
    str::FromStr,
    sync::{Arc, Mutex},
};

use anyhow::{Context, Result, bail, ensure};
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
            "[computer]\nserver_url = '{server}'\nstate_dir = '{}'\nshutdown_grace_period_seconds = 1\n",
            state_dir.display()
        ),
    )?;
    Ok(())
}

pub fn empty_path(root: &Path) -> Result<PathBuf> {
    let path = root.join("empty-path");
    std::fs::create_dir(&path)?;
    Ok(path)
}
