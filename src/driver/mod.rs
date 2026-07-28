pub mod builtin;
pub mod builtin_config;
pub mod codex;

use std::{path::PathBuf, time::Duration};

use anyhow::{Context, Result};
use async_trait::async_trait;
use serde::{Deserialize, Serialize};
use tokio::{io::AsyncWriteExt, process::Child};

use crate::prompt::AgentRunPrompt;

#[derive(Clone)]
pub struct DriverEnvironment {
    pub state_dir: PathBuf,
    pub agent_home: PathBuf,
    pub agents_root: PathBuf,
    pub workspace: PathBuf,
    pub codex_home: PathBuf,
    pub socket_path: PathBuf,
    pub run_token: String,
    pub path: String,
}

#[derive(Clone)]
pub struct DriverRun {
    pub run_id: uuid::Uuid,
    pub prompt: AgentRunPrompt,
    pub environment: DriverEnvironment,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum DriverEvent {
    ProcessStarted,
    OutputReceived { event_type: String },
    CommandStarted,
    CommandFinished,
    ProcessCompleted,
    ProcessFailed,
    ProcessCanceled,
}

pub enum DriverProcess {
    External {
        child: Child,
        stdout: tokio::process::ChildStdout,
        stdin: Option<tokio::process::ChildStdin>,
        prompt: Option<Vec<u8>>,
    },
    Internal {
        task: tokio::task::JoinHandle<()>,
        events: tokio::sync::mpsc::Receiver<DriverEvent>,
        activation: Option<tokio::sync::oneshot::Sender<()>>,
    },
}

impl DriverProcess {
    pub fn pid(&self) -> Option<u32> {
        match self {
            DriverProcess::External { child, .. } => child.id(),
            DriverProcess::Internal { .. } => None,
        }
    }

    pub async fn activate(&mut self) -> Result<()> {
        match self {
            DriverProcess::External { stdin, prompt, .. } => {
                let mut stdin = stdin.take().context("Driver stdin is unavailable")?;
                let prompt = prompt.take().context("Driver prompt is unavailable")?;
                stdin.write_all(&prompt).await?;
                drop(stdin);
            }
            DriverProcess::Internal { activation, .. } => {
                activation
                    .take()
                    .context("Driver activation is unavailable")?
                    .send(())
                    .map_err(|_| anyhow::anyhow!("Driver stopped before activation"))?;
            }
        }
        Ok(())
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DriverOutcome {
    Completed,
    Failed,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ProcessExitEvidence {
    pub exit_code: Option<i32>,
    pub exit_signal: Option<i32>,
}

impl ProcessExitEvidence {
    pub const INTERNAL_TASK: Self = Self {
        exit_code: None,
        exit_signal: None,
    };

    pub fn from_status(status: std::process::ExitStatus) -> Self {
        #[cfg(unix)]
        {
            use std::os::unix::process::ExitStatusExt;
            Self {
                exit_code: status.code(),
                exit_signal: status.signal(),
            }
        }
        #[cfg(not(unix))]
        {
            Self {
                exit_code: status.code(),
                exit_signal: None,
            }
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DriverStopOutcome {
    Reaped {
        exit: ProcessExitEvidence,
        sigterm_sent: bool,
        sigkill_sent: bool,
    },
    Orphaned {
        sigterm_sent: bool,
        sigkill_sent: bool,
    },
}

#[async_trait]
pub trait Driver: Send + Sync {
    async fn validate(&self, environment: &DriverEnvironment) -> Result<()>;
    async fn start(&self, run: DriverRun) -> Result<DriverProcess>;
    async fn observe(
        &self,
        process: &mut DriverProcess,
        events: &tokio::sync::mpsc::Sender<DriverEvent>,
    ) -> Result<DriverOutcome>;
    async fn cancel(
        &self,
        process: &mut DriverProcess,
        grace_period: Duration,
    ) -> Result<DriverStopOutcome>;
    async fn reap(&self, process: &mut DriverProcess) -> Result<ProcessExitEvidence>;
    async fn cleanup(&self, environment: &DriverEnvironment) -> Result<()>;
}
