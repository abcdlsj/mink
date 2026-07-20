-- +goose Up
ALTER TABLE agent_create_requests RENAME TO agent_create_requests_v7;

CREATE TABLE agent_create_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('system', 'human')),
    actor_id TEXT NOT NULL,
    agent_id TEXT NOT NULL UNIQUE REFERENCES agents(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    CHECK (
        (actor_kind = 'system' AND actor_id = '')
        OR (actor_kind = 'human' AND length(actor_id) = 36)
    )
);

INSERT INTO agent_create_requests(request_id, actor_kind, actor_id, agent_id, payload_fingerprint)
SELECT request_id, 'system', '', agent_id, payload_fingerprint
FROM agent_create_requests_v7;

DROP TABLE agent_create_requests_v7;

CREATE TABLE agent_placement_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    actor_kind TEXT NOT NULL CHECK (actor_kind = 'human'),
    actor_id TEXT NOT NULL REFERENCES humans(id) ON DELETE RESTRICT CHECK (length(actor_id) = 36),
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT,
    computer_id TEXT NOT NULL REFERENCES computers(id) ON DELETE RESTRICT,
    generation INTEGER NOT NULL CHECK (generation > 0),
    state TEXT NOT NULL CHECK (state IN ('pending', 'active', 'failed')),
    error_code TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (
        (state = 'failed' AND length(error_code) BETWEEN 1 AND 64)
        OR (state IN ('pending', 'active') AND error_code = '')
    )
);

ALTER TABLE audit_events RENAME TO audit_events_v7;

CREATE TABLE audit_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('system', 'human', 'agent')),
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL,
    target_kind TEXT NOT NULL,
    target_id TEXT NOT NULL,
    request_id TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('committed', 'denied')),
    reason_code TEXT NOT NULL,
    occurred_at INTEGER NOT NULL,
    context_kind TEXT NOT NULL DEFAULT '' CHECK (context_kind IN ('', 'space', 'thread', 'computer')),
    context_id TEXT NOT NULL DEFAULT '',
    CHECK ((actor_kind = 'system' AND actor_id = '') OR (actor_kind != 'system' AND length(actor_id) = 36)),
    CHECK ((outcome = 'committed' AND reason_code = '') OR (outcome = 'denied' AND length(reason_code) BETWEEN 1 AND 64)),
    CHECK (
        (context_kind = '' AND context_id = '')
        OR (context_kind IN ('space', 'thread', 'computer') AND length(context_id) = 36)
    )
);

INSERT INTO audit_events(
    sequence, id, organization_id, actor_kind, actor_id, action, target_kind,
    target_id, request_id, outcome, reason_code, occurred_at, context_kind, context_id
)
SELECT
    sequence, id, organization_id, actor_kind, actor_id, action, target_kind,
    target_id, request_id, outcome, reason_code, occurred_at, context_kind, context_id
FROM audit_events_v7
ORDER BY sequence;

DROP TABLE audit_events_v7;

CREATE INDEX audit_events_organization_sequence
ON audit_events(organization_id, sequence);

-- +goose Down
ALTER TABLE audit_events RENAME TO audit_events_v8;

CREATE TABLE audit_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('system', 'human', 'agent')),
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL,
    target_kind TEXT NOT NULL,
    target_id TEXT NOT NULL,
    request_id TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('committed', 'denied')),
    reason_code TEXT NOT NULL,
    occurred_at INTEGER NOT NULL,
    context_kind TEXT NOT NULL DEFAULT '' CHECK (context_kind IN ('', 'space', 'thread')),
    context_id TEXT NOT NULL DEFAULT '',
    CHECK ((actor_kind = 'system' AND actor_id = '') OR (actor_kind != 'system' AND length(actor_id) = 36)),
    CHECK ((outcome = 'committed' AND reason_code = '') OR (outcome = 'denied' AND length(reason_code) BETWEEN 1 AND 64)),
    CHECK (
        (context_kind = '' AND context_id = '')
        OR (context_kind IN ('space', 'thread') AND length(context_id) = 36)
    )
);

INSERT INTO audit_events(
    sequence, id, organization_id, actor_kind, actor_id, action, target_kind,
    target_id, request_id, outcome, reason_code, occurred_at, context_kind, context_id
)
SELECT
    sequence, id, organization_id, actor_kind, actor_id, action, target_kind,
    target_id, request_id, outcome, reason_code, occurred_at, context_kind, context_id
FROM audit_events_v8
ORDER BY sequence;

DROP TABLE audit_events_v8;

CREATE INDEX audit_events_organization_sequence
ON audit_events(organization_id, sequence);

DROP TABLE agent_placement_requests;

ALTER TABLE agent_create_requests RENAME TO agent_create_requests_v8;

CREATE TABLE agent_create_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    agent_id TEXT NOT NULL UNIQUE REFERENCES agents(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
);

INSERT INTO agent_create_requests(request_id, agent_id, payload_fingerprint)
SELECT request_id, agent_id, payload_fingerprint
FROM agent_create_requests_v8;

DROP TABLE agent_create_requests_v8;
