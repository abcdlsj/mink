PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

CREATE TABLE schema_meta (
    version INTEGER PRIMARY KEY CHECK (version > 0),
    applied_at TEXT NOT NULL
) STRICT;

INSERT INTO schema_meta (version, applied_at)
VALUES (2, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));

CREATE TABLE local_commands (
    command_id TEXT PRIMARY KEY,
    computer_seq INTEGER NOT NULL UNIQUE CHECK (computer_seq > 0),
    fingerprint TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'applied', 'rejected')),
    error_code TEXT
) STRICT;

CREATE TABLE local_runs (
    run_id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    task_id TEXT,
    focus_thread_id TEXT NOT NULL,
    -- Local Driver phases. Finer than the Server's status because this side starts and stops the
    -- process. No phase carries a deadline: nothing here expires.
    state TEXT NOT NULL CHECK (state IN (
        'queued', 'starting', 'running', 'finalizing', 'stopping',
        'completed', 'yielded', 'failed', 'canceled'
    )),
    run_json TEXT NOT NULL
) STRICT;

CREATE UNIQUE INDEX local_runs_one_active_agent
    ON local_runs (agent_id)
    WHERE state IN ('starting', 'running', 'finalizing', 'stopping');

CREATE TABLE provider_sessions (
    agent_id TEXT NOT NULL,
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('thread', 'task')),
    scope_id TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    driver_kind TEXT NOT NULL CHECK (driver_kind IN ('codex', 'builtin')),
    provider_locator TEXT NOT NULL,
    workspace_fingerprint TEXT NOT NULL,
    role_revision INTEGER NOT NULL CHECK (role_revision >= 0),
    audience_fingerprint TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('ready', 'in_use', 'closing', 'closed', 'lost')),
    created_at TEXT NOT NULL,
    last_resumed_at TEXT,
    closed_at TEXT,
    PRIMARY KEY (agent_id, scope_kind, scope_id, generation)
) STRICT;

CREATE TABLE run_deliveries (
    run_id TEXT NOT NULL REFERENCES local_runs(run_id) ON DELETE CASCADE,
    delivery_seq INTEGER NOT NULL CHECK (delivery_seq > 0),
    inbox_item_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'accepted', 'too_late', 'unsupported')),
    disposition TEXT CHECK (disposition IN ('handled', 'deferred', 'released')),
    item_json TEXT NOT NULL,
    PRIMARY KEY (run_id, delivery_seq),
    UNIQUE (run_id, inbox_item_id)
) STRICT;

CREATE TABLE result_outbox (
    event_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES local_runs(run_id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('run_started', 'delivery', 'run_result')),
    payload_json TEXT NOT NULL
) STRICT;
