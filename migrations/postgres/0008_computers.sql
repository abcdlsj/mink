CREATE TABLE computers (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    hostname TEXT NOT NULL,
    os TEXT NOT NULL CHECK (os IN ('macos', 'linux')),
    public_key BYTEA NOT NULL,
    credential_hash BYTEA NOT NULL CHECK (octet_length(credential_hash) = 32),
    status TEXT NOT NULL DEFAULT 'offline' CHECK (status IN ('online', 'offline', 'revoked')),
    daemon_version TEXT NOT NULL,
    next_command_seq BIGINT NOT NULL DEFAULT 1 CHECK (next_command_seq > 0),
    last_seen_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    UNIQUE (id, space_id)
);

CREATE TABLE computer_pairings (
    id UUID PRIMARY KEY,
    pairing_code_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(pairing_code_hash) = 32),
    pairing_secret_hash BYTEA NOT NULL CHECK (octet_length(pairing_secret_hash) = 32),
    credential_hash BYTEA NOT NULL CHECK (octet_length(credential_hash) = 32),
    public_key BYTEA NOT NULL,
    hostname TEXT NOT NULL,
    os TEXT NOT NULL CHECK (os IN ('macos', 'linux')),
    daemon_version TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    space_id UUID,
    confirmed_by_member_id UUID,
    computer_id UUID UNIQUE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'confirmed', 'expired')),
    created_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (confirmed_by_member_id, space_id) REFERENCES members(id, space_id),
    FOREIGN KEY (computer_id, space_id) REFERENCES computers(id, space_id),
    CHECK (
        (status IN ('pending', 'expired') AND space_id IS NULL
            AND confirmed_by_member_id IS NULL AND computer_id IS NULL)
        OR (status = 'confirmed' AND space_id IS NOT NULL
            AND confirmed_by_member_id IS NOT NULL AND computer_id IS NOT NULL)
    )
);

CREATE INDEX computers_space_status_idx ON computers (space_id, status, created_at);
CREATE INDEX computer_pairings_pending_idx ON computer_pairings (expires_at)
    WHERE status = 'pending';

CREATE TABLE agents (
    member_id UUID PRIMARY KEY,
    space_id UUID NOT NULL,
    computer_id UUID NOT NULL,
    role_text TEXT NOT NULL CHECK (char_length(role_text) BETWEEN 1 AND 12000),
    role_revision BIGINT NOT NULL DEFAULT 1 CHECK (role_revision > 0),
    status TEXT NOT NULL DEFAULT 'provisioning'
        CHECK (status IN ('provisioning', 'active', 'suspended', 'error', 'retired')),
    driver_kind TEXT NOT NULL CHECK (driver_kind IN ('codex', 'builtin')),
    driver_config_json JSONB NOT NULL,
    attention_config_json JSONB NOT NULL,
    created_by_member_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    retired_at TIMESTAMPTZ,
    FOREIGN KEY (member_id, space_id) REFERENCES members(id, space_id),
    FOREIGN KEY (computer_id, space_id) REFERENCES computers(id, space_id),
    FOREIGN KEY (created_by_member_id, space_id) REFERENCES members(id, space_id)
);

CREATE INDEX agents_computer_status_idx ON agents (computer_id, status, created_at);

CREATE TABLE computer_commands (
    id UUID PRIMARY KEY,
    computer_id UUID NOT NULL REFERENCES computers(id),
    computer_seq BIGINT NOT NULL CHECK (computer_seq > 0),
    kind TEXT NOT NULL,
    payload_json JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'acked', 'completed', 'failed')),
    result_json JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    acked_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    UNIQUE (computer_id, computer_seq)
);

CREATE INDEX computer_commands_pending_idx
    ON computer_commands (computer_id, computer_seq) WHERE status = 'pending';
