PRAGMA foreign_keys = ON;

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
    placement_generation INTEGER NOT NULL CHECK (placement_generation > 0),
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
    launch_id TEXT NOT NULL,
    fence INTEGER NOT NULL CHECK (fence >= 0),
    response_launch_id TEXT NOT NULL DEFAULT '',
    response_fence INTEGER NOT NULL DEFAULT 0 CHECK (response_fence >= 0),
    response_expires_at INTEGER,
    created_at INTEGER NOT NULL,
    completed_at INTEGER,
    CHECK (
        (status = 'pending' AND completed_at IS NULL)
        OR (status != 'pending' AND completed_at IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS mutation_attempts_pending
ON mutation_attempts(operation, subject_id, created_at)
WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS outbox_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    outbox_event_id TEXT NOT NULL UNIQUE CHECK (length(outbox_event_id) = 36),
    request_id TEXT NOT NULL UNIQUE CHECK (length(request_id) = 36),
    agent_id TEXT NOT NULL CHECK (length(agent_id) = 36),
    placement_generation INTEGER NOT NULL CHECK (placement_generation > 0),
    run_id TEXT NOT NULL CHECK (length(run_id) = 36),
    launch_id TEXT NOT NULL CHECK (length(launch_id) = 36),
    fence INTEGER NOT NULL CHECK (fence > 0),
    outcome TEXT NOT NULL CHECK (outcome IN ('succeeded', 'failed')),
    body TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'tombstone')),
    rejection_code TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    last_attempt_at INTEGER,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    UNIQUE (run_id, launch_id, fence),
    CHECK (
        (state = 'pending' AND rejection_code = '')
        OR (state = 'tombstone' AND body = '' AND rejection_code != '')
    )
);

CREATE TABLE IF NOT EXISTS outbox_mentions (
    outbox_event_id TEXT NOT NULL REFERENCES outbox_events(outbox_event_id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    agent_id TEXT NOT NULL CHECK (length(agent_id) = 36),
    PRIMARY KEY (outbox_event_id, ordinal),
    UNIQUE (outbox_event_id, agent_id)
);
