ALTER TABLE local_agent_runs ADD COLUMN run_attempt INTEGER NOT NULL DEFAULT 1
    CHECK (run_attempt > 0);
ALTER TABLE local_agent_runs ADD COLUMN process_instance_id TEXT;

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
