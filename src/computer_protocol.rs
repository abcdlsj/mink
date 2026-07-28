use serde::{Deserialize, Serialize};
use time::OffsetDateTime;
use uuid::Uuid;

use crate::{
    agent_config::{AttentionConfig, SuspendMode},
    prompt::AgentRunPrompt,
};

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(tag = "type", rename_all = "snake_case", deny_unknown_fields)]
pub(crate) enum ComputerFrame {
    Hello {
        last_acked_computer_seq: i64,
    },
    Heartbeat {
        daemon_version: String,
        os: String,
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
        result: CommandResult,
    },
    RunStarted {
        event_id: String,
        run_id: Uuid,
        fencing_token: String,
        run_attempt: i64,
        process_instance_id: Uuid,
        #[serde(with = "time::serde::rfc3339")]
        daemon_observed_at: OffsetDateTime,
    },
    RunResult {
        event_id: String,
        fencing_token: String,
        command_id: Uuid,
        computer_seq: i64,
        ok: bool,
        result: CommandResult,
    },
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(tag = "type", rename_all = "snake_case", deny_unknown_fields)]
pub(crate) enum ServerFrame {
    Welcome {
        heartbeat_interval_seconds: u64,
    },
    Command {
        command_id: Uuid,
        computer_seq: i64,
        #[serde(flatten)]
        command: Box<ComputerCommand>,
    },
    ResultReceipt {
        event_id: String,
    },
    StartedReceipt {
        event_id: String,
    },
    Shutdown {
        reason: String,
    },
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(tag = "kind", content = "payload", deny_unknown_fields)]
pub(crate) enum ComputerCommand {
    #[serde(rename = "agent.provision")]
    Provision(AgentConfiguration),
    #[serde(rename = "agent.configure")]
    Configure(AgentConfiguration),
    #[serde(rename = "agent.suspend")]
    Suspend(AgentConfiguration),
    #[serde(rename = "agent.resume")]
    Resume(AgentConfiguration),
    #[serde(rename = "agent.retire")]
    Retire(AgentConfiguration),
    #[serde(rename = "agent.run")]
    Run(AgentRunCommand),
    #[serde(rename = "agent.cancel")]
    Cancel(AgentCancelCommand),
    #[serde(rename = "agent.memory.read")]
    MemoryRead(AgentMemoryReadCommand),
}

impl ComputerCommand {
    pub(crate) fn kind(&self) -> &'static str {
        match self {
            Self::Provision(_) => "agent.provision",
            Self::Configure(_) => "agent.configure",
            Self::Suspend(_) => "agent.suspend",
            Self::Resume(_) => "agent.resume",
            Self::Retire(_) => "agent.retire",
            Self::Run(_) => "agent.run",
            Self::Cancel(_) => "agent.cancel",
            Self::MemoryRead(_) => "agent.memory.read",
        }
    }

    pub(crate) fn payload_json(&self) -> serde_json::Result<serde_json::Value> {
        let value = serde_json::to_value(self)?;
        Ok(value
            .get("payload")
            .cloned()
            .expect("ComputerCommand always has a payload"))
    }

    pub(crate) fn from_parts(kind: &str, payload: serde_json::Value) -> serde_json::Result<Self> {
        serde_json::from_value(serde_json::json!({ "kind": kind, "payload": payload }))
    }

    pub(crate) fn agent_id(&self) -> Option<Uuid> {
        match self {
            Self::Provision(command)
            | Self::Configure(command)
            | Self::Suspend(command)
            | Self::Resume(command)
            | Self::Retire(command) => Some(command.agent_id),
            Self::Run(command) => Some(command.agent_id),
            Self::MemoryRead(command) => Some(command.agent_id),
            Self::Cancel(_) => None,
        }
    }

    pub(crate) fn run_id(&self) -> Option<Uuid> {
        match self {
            Self::Run(command) => Some(command.run_id),
            Self::Cancel(command) => Some(command.run_id),
            _ => None,
        }
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct AgentConfiguration {
    pub(crate) agent_id: Uuid,
    pub(crate) space_id: Uuid,
    pub(crate) name: String,
    pub(crate) handle: String,
    pub(crate) role_text: String,
    pub(crate) role_revision: i64,
    pub(crate) driver_kind: String,
    pub(crate) driver_config: DriverConfig,
    pub(crate) attention_config: AttentionConfig,
    pub(crate) mode: Option<SuspendMode>,
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct DriverConfig {
    pub(crate) schema_version: u32,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct AgentRunCommand {
    pub(crate) run_id: Uuid,
    pub(crate) agent_id: Uuid,
    pub(crate) space_id: Uuid,
    pub(crate) driver_kind: String,
    pub(crate) fencing_token: String,
    #[serde(with = "time::serde::rfc3339")]
    pub(crate) ownership_lease_expires_at: OffsetDateTime,
    pub(crate) prompt: AgentRunPrompt,
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct AgentCancelCommand {
    pub(crate) run_id: Uuid,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct AgentMemoryReadCommand {
    pub(crate) agent_id: Uuid,
    pub(crate) path: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct CommandResult {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) ok: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) run_id: Option<Uuid>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) status: Option<RunResultStatus>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) error_code: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) memory_files: Option<Vec<MemoryFileMetadata>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) path: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) content: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) size: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) sha256: Option<String>,
    #[serde(
        default,
        with = "time::serde::rfc3339::option",
        skip_serializing_if = "Option::is_none"
    )]
    pub(crate) updated_at: Option<OffsetDateTime>,
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum RunResultStatus {
    Completed,
    Failed,
    Canceled,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct MemoryFileMetadata {
    pub(crate) path: String,
    pub(crate) size: u64,
    pub(crate) sha256: String,
    #[serde(with = "time::serde::rfc3339")]
    pub(crate) updated_at: OffsetDateTime,
}
