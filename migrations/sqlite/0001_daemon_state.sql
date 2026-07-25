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
    last_error_code TEXT
) STRICT;

CREATE INDEX local_agent_runs_agent_status_idx
    ON local_agent_runs (agent_member_id, status);
