PRAGMA foreign_keys = ON;

CREATE TABLE state_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO state_metadata(key, value) VALUES('schema_version', 'next-greenfield-1');

CREATE TABLE credential_delivery_keys (
	key_id TEXT PRIMARY KEY CHECK (length(key_id) = 36),
	private_key BLOB NOT NULL CHECK (length(private_key) = 32),
	public_key BLOB NOT NULL CHECK (length(public_key) = 32),
	active INTEGER NOT NULL CHECK (active IN (0, 1)),
	created_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX credential_delivery_one_active_key
ON credential_delivery_keys(active) WHERE active = 1;

CREATE TABLE credential_bindings (
	handle TEXT PRIMARY KEY CHECK (length(handle) BETWEEN 16 AND 255),
	delivery_id TEXT NOT NULL UNIQUE CHECK (length(delivery_id) = 36),
	agent_id TEXT NOT NULL CHECK (length(agent_id) = 36),
	computer_id TEXT NOT NULL CHECK (length(computer_id) = 36),
	credential_kind TEXT NOT NULL CHECK (credential_kind IN ('openai', 'anthropic', 'codex_adapter', 'claude_adapter')),
	key_id TEXT NOT NULL REFERENCES credential_delivery_keys(key_id) ON DELETE RESTRICT,
	created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS pairing_attempt (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    server_url TEXT NOT NULL,
    pairing_token TEXT NOT NULL,
    request_id TEXT NOT NULL CHECK (length(request_id) = 36),
    registration_key TEXT NOT NULL,
    name TEXT NOT NULL,
    os TEXT NOT NULL,
    arch TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS computer_identity (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    server_url TEXT NOT NULL,
    computer_id TEXT NOT NULL CHECK (length(computer_id) = 36),
    registration_key TEXT NOT NULL,
    paired_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS runtime_sessions (
    agent_id TEXT PRIMARY KEY CHECK (length(agent_id) = 36),
    computer_id TEXT NOT NULL CHECK (length(computer_id) = 36),
    placement_desired_revision INTEGER NOT NULL CHECK (placement_desired_revision > 0),
    token TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS mutation_attempts (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    operation TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    payload_hash BLOB NOT NULL CHECK (length(payload_hash) = 32),
    status TEXT NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed')),
    run_id TEXT NOT NULL,
	attempt INTEGER NOT NULL CHECK (attempt >= 0),
    fence INTEGER NOT NULL CHECK (fence >= 0),
	response_attempt INTEGER NOT NULL DEFAULT 0 CHECK (response_attempt >= 0),
    response_fence INTEGER NOT NULL DEFAULT 0 CHECK (response_fence >= 0),
    response_expires_at INTEGER,
    created_at INTEGER NOT NULL,
    completed_at INTEGER,
    CHECK (
        (status = 'pending' AND completed_at IS NULL)
        OR (status != 'pending' AND completed_at IS NOT NULL)
    )
);

DROP INDEX IF EXISTS mutation_attempts_pending;

CREATE UNIQUE INDEX IF NOT EXISTS mutation_attempts_one_pending
ON mutation_attempts(operation, subject_id)
WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS outbox_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    outbox_event_id TEXT NOT NULL UNIQUE CHECK (length(outbox_event_id) = 36),
    request_id TEXT NOT NULL UNIQUE CHECK (length(request_id) = 36),
    agent_id TEXT NOT NULL CHECK (length(agent_id) = 36),
    placement_desired_revision INTEGER NOT NULL CHECK (placement_desired_revision > 0),
    run_id TEXT NOT NULL CHECK (length(run_id) = 36),
	attempt INTEGER NOT NULL CHECK (attempt > 0),
    fence INTEGER NOT NULL CHECK (fence > 0),
    outcome TEXT NOT NULL CHECK (outcome IN ('succeeded', 'failed')),
	error_code TEXT NOT NULL DEFAULT '' CHECK (length(error_code) <= 255),
    body TEXT NOT NULL,
	usage_input_units INTEGER NOT NULL DEFAULT 0 CHECK (usage_input_units >= 0),
	usage_output_units INTEGER NOT NULL DEFAULT 0 CHECK (usage_output_units >= 0),
    state TEXT NOT NULL CHECK (state IN ('pending', 'tombstone')),
    rejection_code TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    last_attempt_at INTEGER,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
	UNIQUE (run_id, attempt, fence),
    CHECK (
		(state = 'pending' AND rejection_code = '' AND ((outcome = 'succeeded' AND error_code = '') OR (outcome = 'failed' AND length(error_code) > 0)))
        OR (state = 'tombstone' AND body = '' AND rejection_code != '')
    )
);

CREATE TABLE IF NOT EXISTS run_journals (
	agent_id TEXT NOT NULL CHECK (length(agent_id) = 36),
	placement_desired_revision INTEGER NOT NULL CHECK (placement_desired_revision > 0),
	run_id TEXT NOT NULL CHECK (length(run_id) = 36),
	attempt INTEGER NOT NULL CHECK (attempt > 0),
	fence INTEGER NOT NULL CHECK (fence > 0),
	state TEXT NOT NULL CHECK (state IN ('running', 'completed', 'cancelled', 'failed')),
	started_at INTEGER NOT NULL,
	finished_at INTEGER,
	PRIMARY KEY (agent_id, run_id, attempt, fence),
	CHECK ((state = 'running' AND finished_at IS NULL) OR (state != 'running' AND finished_at IS NOT NULL))
);

CREATE TABLE tool_results (
	agent_id TEXT NOT NULL CHECK (length(agent_id) = 36),
	computer_id TEXT NOT NULL CHECK (length(computer_id) = 36),
	placement_desired_revision INTEGER NOT NULL CHECK (placement_desired_revision > 0),
	run_id TEXT NOT NULL CHECK (length(run_id) = 36),
	attempt INTEGER NOT NULL CHECK (attempt > 0),
	fence INTEGER NOT NULL CHECK (fence > 0),
	call_id TEXT NOT NULL CHECK (length(call_id) BETWEEN 1 AND 255),
	request_hash BLOB NOT NULL CHECK (length(request_hash) = 32),
	result_json BLOB NOT NULL CHECK (length(result_json) BETWEEN 1 AND 1048576),
	created_at INTEGER NOT NULL,
	PRIMARY KEY (run_id, attempt, fence, call_id)
);

CREATE TABLE IF NOT EXISTS outbox_mentions (
    outbox_event_id TEXT NOT NULL REFERENCES outbox_events(outbox_event_id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    agent_id TEXT NOT NULL CHECK (length(agent_id) = 36),
    PRIMARY KEY (outbox_event_id, ordinal),
    UNIQUE (outbox_event_id, agent_id)
);
