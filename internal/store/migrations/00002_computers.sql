-- +goose Up
CREATE TABLE computers (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    registration_key_hash BLOB NOT NULL UNIQUE CHECK (length(registration_key_hash) = 32),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    os TEXT NOT NULL CHECK (os IN ('macos', 'linux')),
    arch TEXT NOT NULL CHECK (arch IN ('arm64', 'amd64')),
    created_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE computers;
