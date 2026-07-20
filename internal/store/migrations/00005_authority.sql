-- +goose Up
CREATE TABLE organizations (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    bootstrap_human_id TEXT NOT NULL CHECK (length(bootstrap_human_id) = 36),
    created_at INTEGER NOT NULL
);

CREATE TABLE humans (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    role TEXT NOT NULL CHECK (role IN ('owner', 'member')),
    status TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
    credential_hash BLOB NOT NULL UNIQUE CHECK (length(credential_hash) = 32),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (organization_id, name)
);

CREATE TABLE human_create_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    human_id TEXT NOT NULL UNIQUE REFERENCES humans(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
);

CREATE TABLE human_status_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    human_id TEXT NOT NULL REFERENCES humans(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
);

CREATE TABLE grants (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('human', 'agent')),
    subject_id TEXT NOT NULL CHECK (length(subject_id) = 36),
    issuer_kind TEXT NOT NULL CHECK (issuer_kind IN ('system', 'human', 'agent')),
    issuer_id TEXT NOT NULL,
    capability TEXT NOT NULL,
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('organization', 'agent', 'computer', 'space')),
    scope_id TEXT NOT NULL CHECK (length(scope_id) = 36),
    parent_grant_id TEXT NOT NULL DEFAULT '',
    expires_at INTEGER,
    revoked_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK ((issuer_kind = 'system' AND issuer_id = '') OR (issuer_kind != 'system' AND length(issuer_id) = 36)),
    CHECK (expires_at IS NULL OR expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX grants_subject_capability_scope
ON grants(organization_id, subject_kind, subject_id, capability, scope_kind, scope_id);

CREATE TABLE grant_issue_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    grant_id TEXT NOT NULL UNIQUE REFERENCES grants(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
);

CREATE TABLE grant_revoke_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    grant_id TEXT NOT NULL REFERENCES grants(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
);

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
    CHECK ((actor_kind = 'system' AND actor_id = '') OR (actor_kind != 'system' AND length(actor_id) = 36)),
    CHECK ((outcome = 'committed' AND reason_code = '') OR (outcome = 'denied' AND length(reason_code) BETWEEN 1 AND 64))
);

CREATE INDEX audit_events_organization_sequence
ON audit_events(organization_id, sequence);

-- +goose Down
DROP INDEX audit_events_organization_sequence;
DROP TABLE audit_events;
DROP TABLE grant_revoke_requests;
DROP TABLE grant_issue_requests;
DROP INDEX grants_subject_capability_scope;
DROP TABLE grants;
DROP TABLE human_status_requests;
DROP TABLE human_create_requests;
DROP TABLE humans;
DROP TABLE organizations;
