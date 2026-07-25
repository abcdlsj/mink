ALTER TABLE agents
    ADD CONSTRAINT agents_member_computer_unique UNIQUE (member_id, computer_id);

ALTER TABLE agents
    ADD COLUMN last_error_code TEXT;

CREATE TABLE agent_memory_files (
    agent_member_id UUID NOT NULL REFERENCES agents(member_id),
    path TEXT NOT NULL CHECK (
        char_length(path) BETWEEN 1 AND 1024
        AND path !~ '(^|/)\.\.(/|$)'
        AND left(path, 1) <> '/'
    ),
    size BIGINT NOT NULL CHECK (size >= 0),
    sha256 BYTEA NOT NULL CHECK (octet_length(sha256) = 32),
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (agent_member_id, path)
);

CREATE TABLE agent_runs (
    id UUID PRIMARY KEY,
    agent_member_id UUID NOT NULL,
    computer_id UUID NOT NULL,
    driver_kind TEXT NOT NULL CHECK (driver_kind = 'codex'),
    role_revision BIGINT NOT NULL CHECK (role_revision > 0),
    status TEXT NOT NULL CHECK (status IN (
        'queued', 'running', 'completed', 'failed', 'canceled'
    )),
    created_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    exit_code INTEGER,
    error_code TEXT,
    FOREIGN KEY (agent_member_id, computer_id)
        REFERENCES agents(member_id, computer_id),
    CHECK (
        (status = 'queued' AND started_at IS NULL AND finished_at IS NULL)
        OR (status = 'running' AND started_at IS NOT NULL AND finished_at IS NULL)
        OR (status IN ('completed', 'failed', 'canceled') AND finished_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX agent_runs_one_active_idx
    ON agent_runs (agent_member_id)
    WHERE status IN ('queued', 'running');

CREATE INDEX agent_runs_computer_created_idx
    ON agent_runs (computer_id, created_at DESC);

CREATE TABLE agent_run_inbox_items (
    run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    inbox_item_id UUID NOT NULL REFERENCES inbox_items(id),
    lease_id UUID NOT NULL,
    PRIMARY KEY (run_id, inbox_item_id),
    UNIQUE (inbox_item_id, lease_id)
);

ALTER TABLE inbox_items
    ADD CONSTRAINT inbox_items_handled_run_fk
    FOREIGN KEY (handled_by_run_id) REFERENCES agent_runs(id);
