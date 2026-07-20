-- +goose Up
CREATE TABLE browser_handoffs (
    token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
    human_id TEXT NOT NULL REFERENCES humans(id) ON DELETE RESTRICT,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    consumed_at INTEGER,
    CHECK (expires_at > created_at),
    CHECK (consumed_at IS NULL OR (consumed_at >= created_at AND consumed_at <= expires_at))
);

CREATE INDEX browser_handoffs_expires_at
ON browser_handoffs(expires_at);

CREATE TABLE browser_sessions (
    token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
    human_id TEXT NOT NULL REFERENCES humans(id) ON DELETE RESTRICT,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    revoked_at INTEGER,
    CHECK (expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX browser_sessions_human_active
ON browser_sessions(human_id, expires_at)
WHERE revoked_at IS NULL;

-- +goose Down
DROP INDEX browser_sessions_human_active;
DROP TABLE browser_sessions;
DROP INDEX browser_handoffs_expires_at;
DROP TABLE browser_handoffs;
