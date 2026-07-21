-- +goose Up
CREATE TABLE computer_pairings (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    request_id TEXT NOT NULL UNIQUE CHECK (length(request_id) = 36),
    human_id TEXT NOT NULL REFERENCES humans(id) ON DELETE RESTRICT,
    token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    consumed_at INTEGER,
    consume_request_id TEXT UNIQUE CHECK (consume_request_id IS NULL OR length(consume_request_id) = 36),
    consume_fingerprint BLOB CHECK (consume_fingerprint IS NULL OR length(consume_fingerprint) = 32),
    computer_id TEXT UNIQUE REFERENCES computers(id) ON DELETE RESTRICT,
    CHECK (expires_at > created_at),
    CHECK (
        (consumed_at IS NULL AND consume_request_id IS NULL AND consume_fingerprint IS NULL AND computer_id IS NULL)
        OR (consumed_at IS NOT NULL AND consume_request_id IS NOT NULL AND consume_fingerprint IS NOT NULL AND computer_id IS NOT NULL)
    )
);

CREATE INDEX computer_pairings_human_created
ON computer_pairings(human_id, created_at);

-- +goose Down
DROP INDEX computer_pairings_human_created;
DROP TABLE computer_pairings;
