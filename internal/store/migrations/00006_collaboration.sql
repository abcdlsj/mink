-- +goose Up
CREATE TABLE spaces (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('dm', 'group')),
    name TEXT NOT NULL CHECK (length(name) <= 100),
    dm_key TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    archived_at INTEGER,
    CHECK (
        (kind = 'dm' AND name = '' AND dm_key IS NOT NULL AND archived_at IS NULL)
        OR (kind = 'group' AND length(name) BETWEEN 1 AND 100 AND dm_key IS NULL)
    )
);

CREATE UNIQUE INDEX spaces_dm_key ON spaces(organization_id, dm_key) WHERE dm_key IS NOT NULL;

CREATE TABLE space_memberships (
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    principal_kind TEXT NOT NULL CHECK (principal_kind IN ('human', 'agent')),
    principal_id TEXT NOT NULL CHECK (length(principal_id) = 36),
    joined_at INTEGER NOT NULL,
    PRIMARY KEY (space_id, principal_kind, principal_id)
);

CREATE TABLE message_target_sequences (
    target_kind TEXT NOT NULL CHECK (target_kind IN ('space', 'thread')),
    target_id TEXT NOT NULL CHECK (length(target_id) = 36),
    next_sequence INTEGER NOT NULL CHECK (next_sequence >= 1),
    PRIMARY KEY (target_kind, target_id)
);

CREATE TABLE messages (
    id TEXT NOT NULL UNIQUE CHECK (length(id) = 36),
    request_id TEXT NOT NULL UNIQUE CHECK (length(request_id) = 36),
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('space', 'thread')),
    target_id TEXT NOT NULL CHECK (length(target_id) = 36),
    target_sequence INTEGER NOT NULL CHECK (target_sequence >= 1),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('human', 'agent')),
    author_id TEXT NOT NULL CHECK (length(author_id) = 36),
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 400000),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (target_kind, target_id, target_sequence),
    CHECK (
        (target_kind = 'space' AND target_id = space_id)
        OR target_kind = 'thread'
    )
);

CREATE TABLE threads (
    id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE RESTRICT,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    created_at INTEGER NOT NULL
);

CREATE TABLE collaboration_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
    operation TEXT NOT NULL CHECK (operation IN ('space.create.dm', 'space.create.group', 'space.member.add', 'space.member.remove', 'space.archive', 'space.unarchive', 'message.send')),
    payload_fingerprint BLOB NOT NULL CHECK (length(payload_fingerprint) = 32),
    result_id TEXT NOT NULL CHECK (length(result_id) = 36),
    committed_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE collaboration_requests;
DROP TABLE threads;
DROP TABLE messages;
DROP TABLE message_target_sequences;
DROP TABLE space_memberships;
DROP INDEX spaces_dm_key;
DROP TABLE spaces;
