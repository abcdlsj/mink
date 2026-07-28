ALTER TABLE local_agent_runs ADD COLUMN command_id TEXT;
ALTER TABLE local_agent_runs ADD COLUMN computer_seq INTEGER;
ALTER TABLE local_agent_runs ADD COLUMN result_event_id TEXT;

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
