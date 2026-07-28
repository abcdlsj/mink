use serde::{Deserialize, Serialize};
use uuid::Uuid;

#[derive(Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub(super) enum ComputerFrame {
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
        result: serde_json::Value,
    },
    RunStarted {
        event_id: String,
        run_id: Uuid,
        fencing_token: String,
        run_attempt: i32,
        process_instance_id: Uuid,
        #[serde(with = "time::serde::rfc3339")]
        daemon_observed_at: time::OffsetDateTime,
    },
    RunResult {
        event_id: String,
        fencing_token: String,
        command_id: Uuid,
        computer_seq: i64,
        ok: bool,
        result: serde_json::Value,
    },
}

#[derive(Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub(super) enum ServerFrame {
    Welcome {
        heartbeat_interval_seconds: u64,
    },
    Command {
        command_id: Uuid,
        computer_seq: i64,
        kind: String,
        payload: serde_json::Value,
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
