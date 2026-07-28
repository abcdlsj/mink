PRAGMA foreign_keys = ON;

CREATE TABLE daemon_metadata (
    key TEXT PRIMARY KEY,
    value_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE server_commands (
    command_id TEXT PRIMARY KEY,
    computer_seq INTEGER NOT NULL UNIQUE CHECK (computer_seq > 0),
    request_json TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('received', 'running', 'completed', 'failed')),
    result_json TEXT,
    received_at TEXT NOT NULL,
    completed_at TEXT
) STRICT;

CREATE TABLE local_agent_runs (
    run_id TEXT PRIMARY KEY,
    agent_member_id TEXT NOT NULL,
    space_id TEXT NOT NULL,
    run_token_hash BLOB NOT NULL CHECK (length(run_token_hash) = 32),
    token_expires_at TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'failed', 'canceled')),
    process_id INTEGER,
    started_at TEXT,
    finished_at TEXT,
    last_error_code TEXT,
    server_recovery_reported_at TEXT,
    command_id TEXT,
    computer_seq INTEGER,
    result_event_id TEXT,
    run_attempt INTEGER NOT NULL DEFAULT 1 CHECK (run_attempt > 0),
    process_instance_id TEXT,
    fencing_token TEXT NOT NULL DEFAULT '',
    ownership_lease_expires_at TEXT,
    stop_state TEXT NOT NULL DEFAULT 'none'
        CHECK (stop_state IN ('none', 'stopping', 'orphaned', 'reaped')),
    stop_epoch INTEGER NOT NULL DEFAULT 0 CHECK (stop_epoch >= 0),
    stop_requested_at TEXT,
    sigterm_sent_at TEXT,
    sigkill_sent_at TEXT,
    orphaned_at TEXT,
    reaped_at TEXT,
    exit_code INTEGER,
    exit_signal INTEGER,
    stop_error_code TEXT
) STRICT;

CREATE INDEX local_agent_runs_agent_status_idx
    ON local_agent_runs (agent_member_id, status);

CREATE UNIQUE INDEX local_agent_runs_command_idx
    ON local_agent_runs (command_id) WHERE command_id IS NOT NULL;
CREATE UNIQUE INDEX local_agent_runs_result_event_idx
    ON local_agent_runs (result_event_id) WHERE result_event_id IS NOT NULL;

CREATE TABLE run_result_outbox (
    event_id TEXT PRIMARY KEY,
    command_id TEXT NOT NULL UNIQUE REFERENCES server_commands(command_id),
    computer_seq INTEGER NOT NULL CHECK (computer_seq > 0),
    run_id TEXT NOT NULL UNIQUE REFERENCES local_agent_runs(run_id),
    payload_json TEXT NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TEXT NOT NULL,
    last_error TEXT,
    reported_at TEXT,
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX run_result_outbox_pending_idx
    ON run_result_outbox (next_attempt_at, event_id) WHERE reported_at IS NULL;

CREATE TABLE run_started_outbox (
    event_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL UNIQUE REFERENCES local_agent_runs(run_id),
    run_attempt INTEGER NOT NULL CHECK (run_attempt > 0),
    process_instance_id TEXT NOT NULL,
    daemon_observed_at TEXT NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TEXT NOT NULL,
    reported_at TEXT,
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX run_started_outbox_pending_idx
    ON run_started_outbox (next_attempt_at, event_id) WHERE reported_at IS NULL;
