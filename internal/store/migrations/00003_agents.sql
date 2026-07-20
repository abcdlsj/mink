-- +goose Up
CREATE TABLE agents (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    name TEXT NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 32),
    description TEXT NOT NULL CHECK (length(description) <= 1000),
    driver TEXT NOT NULL CHECK (driver IN ('native', 'codex', 'claude')),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE agent_create_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    agent_id TEXT NOT NULL UNIQUE REFERENCES agents(id) ON DELETE RESTRICT,
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32)
);

-- +goose Down
DROP TABLE agent_create_requests;
DROP TABLE agents;
