pub mod builtin;
pub mod codex;

use std::{path::PathBuf, time::Duration};

use anyhow::Result;
use async_trait::async_trait;
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use tokio::process::Child;

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
    pub codex_api_key: Option<SecretString>,
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
    },
    #[allow(dead_code)]
    Internal {
        task: tokio::task::JoinHandle<()>,
        events: tokio::sync::mpsc::Receiver<DriverEvent>,
    },
}

impl DriverProcess {
    pub fn pid(&self) -> Option<u32> {
        match self {
            DriverProcess::External { child, .. } => child.id(),
            DriverProcess::Internal { .. } => None,
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DriverOutcome {
    Completed,
    Failed,
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
    async fn cancel(&self, process: &mut DriverProcess, grace_period: Duration) -> Result<()>;
    async fn cleanup(&self, environment: &DriverEnvironment) -> Result<()>;
}
