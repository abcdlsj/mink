-- +goose Up
CREATE TABLE agent_runtime_sessions (
    token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE RESTRICT CHECK (length(agent_id) = 36),
    computer_id TEXT NOT NULL REFERENCES computers(id) ON DELETE RESTRICT CHECK (length(computer_id) = 36),
    placement_generation INTEGER NOT NULL CHECK (placement_generation > 0),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    revoked_at INTEGER,
    CHECK (expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE UNIQUE INDEX agent_runtime_sessions_agent_current
ON agent_runtime_sessions(agent_id)
WHERE revoked_at IS NULL;

CREATE INDEX agent_runtime_sessions_binding_active
ON agent_runtime_sessions(agent_id, computer_id, placement_generation, expires_at)
WHERE revoked_at IS NULL;

-- +goose Down
DROP INDEX agent_runtime_sessions_binding_active;
DROP INDEX agent_runtime_sessions_agent_current;
DROP TABLE agent_runtime_sessions;
