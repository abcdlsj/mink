-- +goose Up
CREATE TABLE knowledge_cursor_keys (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    key BLOB NOT NULL CHECK (length(key) = 32)
);

-- +goose Down
DROP TABLE knowledge_cursor_keys;
