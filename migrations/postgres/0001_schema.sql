CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id UUID PRIMARY KEY,
    email_normalized CITEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 40),
    created_at TIMESTAMPTZ NOT NULL,
    disabled_at TIMESTAMPTZ
);

CREATE TABLE sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (expires_at > created_at)
);

CREATE TABLE spaces (
    id UUID PRIMARY KEY,
    slug CITEXT NOT NULL UNIQUE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    accent TEXT NOT NULL,
    owner_member_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    UNIQUE (id, owner_member_id)
);

CREATE TABLE members (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id),
    kind TEXT NOT NULL CHECK (kind IN ('human', 'agent')),
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 40),
    handle TEXT NOT NULL CHECK (handle ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
    avatar_seed TEXT NOT NULL,
    access_level TEXT NOT NULL CHECK (access_level IN ('owner', 'admin', 'member')),
    created_at TIMESTAMPTZ NOT NULL,
    retired_at TIMESTAMPTZ,
    UNIQUE (id, space_id)
);

CREATE UNIQUE INDEX members_space_handle_unique
    ON members (space_id, lower(handle));
CREATE UNIQUE INDEX members_one_owner_per_space
    ON members (space_id)
    WHERE access_level = 'owner';

ALTER TABLE spaces
    ADD CONSTRAINT spaces_owner_member_in_space
    FOREIGN KEY (owner_member_id, id)
    REFERENCES members (id, space_id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE human_members (
    member_id UUID PRIMARY KEY,
    space_id UUID NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id),
    UNIQUE (space_id, user_id),
    FOREIGN KEY (member_id, space_id)
        REFERENCES members (id, space_id)
);

CREATE TABLE member_permissions (
    member_id UUID NOT NULL,
    space_id UUID NOT NULL,
    permission TEXT NOT NULL CHECK (permission IN ('channel:create', 'agent:create')),
    granted_by_member_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (member_id, permission),
    CONSTRAINT member_permissions_member_in_space
        FOREIGN KEY (member_id, space_id) REFERENCES members (id, space_id),
    CONSTRAINT member_permissions_granter_in_space
        FOREIGN KEY (granted_by_member_id, space_id) REFERENCES members (id, space_id)
);

CREATE TABLE channels (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id),
    kind TEXT NOT NULL CHECK (kind IN ('public', 'private', 'direct')),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    slug CITEXT,
    topic TEXT,
    created_by_member_id UUID NOT NULL,
    next_seq BIGINT NOT NULL DEFAULT 1 CHECK (next_seq > 0),
    next_thread_id BIGINT NOT NULL DEFAULT 1 CHECK (next_thread_id > 0),
    created_at TIMESTAMPTZ NOT NULL,
    archived_at TIMESTAMPTZ,
    UNIQUE (id, space_id),
    FOREIGN KEY (created_by_member_id, space_id)
        REFERENCES members (id, space_id),
    CHECK (
        (kind = 'direct' AND slug IS NULL)
        OR (kind <> 'direct' AND slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$')
    )
);

CREATE UNIQUE INDEX channels_space_slug_unique
    ON channels (space_id, lower(slug::text))
    WHERE kind <> 'direct' AND archived_at IS NULL;

CREATE TABLE channel_members (
    channel_id UUID NOT NULL,
    member_id UUID NOT NULL,
    space_id UUID NOT NULL,
    joined_at TIMESTAMPTZ NOT NULL,
    last_read_seq BIGINT NOT NULL DEFAULT 0 CHECK (last_read_seq >= 0),
    notification_level TEXT NOT NULL DEFAULT 'all'
        CHECK (notification_level IN ('all', 'mentions', 'muted')),
    PRIMARY KEY (channel_id, member_id),
    FOREIGN KEY (channel_id, space_id)
        REFERENCES channels (id, space_id),
    FOREIGN KEY (member_id, space_id)
        REFERENCES members (id, space_id)
);

CREATE TABLE audit_events (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id),
    actor_member_id UUID,
    action TEXT NOT NULL,
    subject_type TEXT NOT NULL,
    subject_id UUID NOT NULL,
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (actor_member_id, space_id)
        REFERENCES members (id, space_id)
);

CREATE TABLE outbox_events (
    id UUID PRIMARY KEY,
    topic TEXT NOT NULL,
    aggregate_id UUID NOT NULL,
    payload_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0)
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX channel_members_member_id_idx ON channel_members (member_id);
CREATE INDEX audit_events_space_created_at_idx ON audit_events (space_id, created_at DESC);
CREATE INDEX outbox_events_unpublished_idx ON outbox_events (created_at)
    WHERE published_at IS NULL;

CREATE TABLE idempotency_records (
    scope TEXT NOT NULL,
    idempotency_key UUID NOT NULL,
    request_hash BYTEA NOT NULL,
    response_status SMALLINT,
    response_json JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (scope, idempotency_key),
    CHECK (
        (response_status IS NULL AND response_json IS NULL)
        OR (response_status BETWEEN 200 AND 299 AND response_json IS NOT NULL)
    ),
    CHECK (expires_at > created_at)
);

CREATE INDEX idempotency_records_expiry_idx ON idempotency_records (expires_at);

CREATE TABLE human_invitations (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id),
    email_normalized CITEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    invited_by_member_id UUID NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_by_member_id UUID,
    accepted_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (id, space_id),
    CONSTRAINT human_invitations_inviter_in_space
        FOREIGN KEY (invited_by_member_id, space_id)
        REFERENCES members (id, space_id),
    CONSTRAINT human_invitations_acceptor_in_space
        FOREIGN KEY (accepted_by_member_id, space_id)
        REFERENCES members (id, space_id),
    CHECK (expires_at > created_at),
    CHECK (
        (accepted_by_member_id IS NULL AND accepted_at IS NULL)
        OR (accepted_by_member_id IS NOT NULL AND accepted_at IS NOT NULL)
    ),
    CHECK (accepted_at IS NULL OR revoked_at IS NULL)
);

CREATE INDEX human_invitations_space_email_idx
    ON human_invitations (space_id, email_normalized, created_at DESC);
CREATE INDEX human_invitations_expiry_idx
    ON human_invitations (expires_at)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;

CREATE TABLE messages (
    id UUID PRIMARY KEY,
    channel_id UUID NOT NULL,
    space_id UUID NOT NULL,
    channel_seq BIGINT NOT NULL CHECK (channel_seq > 0),
    thread_id BIGINT,
    reply_to_message_id UUID REFERENCES messages(id),
    author_member_id UUID NOT NULL,
    body_markdown TEXT NOT NULL CHECK (char_length(body_markdown) BETWEEN 1 AND 20000),
    idempotency_key UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    edited_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    UNIQUE (channel_id, channel_seq),
    UNIQUE (id, channel_id, space_id),
    UNIQUE (author_member_id, idempotency_key),
    FOREIGN KEY (channel_id, space_id) REFERENCES channels(id, space_id),
    FOREIGN KEY (author_member_id, space_id) REFERENCES members(id, space_id),
    CHECK (thread_id IS NULL OR thread_id > 0)
);

CREATE TABLE message_mentions (
    message_id UUID NOT NULL REFERENCES messages(id),
    channel_id UUID NOT NULL,
    space_id UUID NOT NULL,
    member_id UUID NOT NULL,
    PRIMARY KEY (message_id, member_id),
    FOREIGN KEY (message_id, channel_id, space_id)
        REFERENCES messages(id, channel_id, space_id),
    FOREIGN KEY (channel_id, member_id)
        REFERENCES channel_members(channel_id, member_id)
);

CREATE TABLE inbox_items (
    id UUID PRIMARY KEY,
    member_id UUID NOT NULL,
    space_id UUID NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN (
        'direct', 'mention', 'reply', 'thread_activity', 'channel_activity', 'approval', 'system'
    )),
    priority TEXT NOT NULL CHECK (priority IN ('hard', 'ambient')),
    channel_id UUID,
    thread_id BIGINT,
    message_id UUID,
    first_seq BIGINT,
    last_seq BIGINT,
    message_count INTEGER NOT NULL DEFAULT 1 CHECK (message_count > 0),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN (
        'pending', 'leased', 'deferred', 'handled', 'dead'
    )),
    available_at TIMESTAMPTZ NOT NULL,
    lease_id UUID,
    lease_expires_at TIMESTAMPTZ,
    retry_count INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    handled_by_run_id UUID,
    handled_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (member_id, space_id) REFERENCES members(id, space_id),
    FOREIGN KEY (channel_id, space_id) REFERENCES channels(id, space_id),
    FOREIGN KEY (message_id, channel_id, space_id)
        REFERENCES messages(id, channel_id, space_id)
);

CREATE INDEX messages_channel_timeline_idx
    ON messages (channel_id, channel_seq DESC);
CREATE INDEX inbox_items_member_pending_idx
    ON inbox_items (member_id, priority, available_at, created_at)
    WHERE status IN ('pending', 'deferred');

CREATE TABLE threads (
    channel_id UUID NOT NULL,
    space_id UUID NOT NULL,
    thread_id BIGINT NOT NULL CHECK (thread_id > 0),
    root_message_id UUID NOT NULL,
    created_by_member_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (channel_id, thread_id),
    UNIQUE (channel_id, root_message_id),
    FOREIGN KEY (channel_id, space_id) REFERENCES channels(id, space_id),
    FOREIGN KEY (root_message_id, channel_id, space_id)
        REFERENCES messages(id, channel_id, space_id),
    FOREIGN KEY (created_by_member_id, space_id)
        REFERENCES members(id, space_id)
);

CREATE TABLE thread_subscriptions (
    channel_id UUID NOT NULL,
    thread_id BIGINT NOT NULL,
    member_id UUID NOT NULL,
    last_read_seq BIGINT NOT NULL DEFAULT 0 CHECK (last_read_seq >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    muted_at TIMESTAMPTZ,
    PRIMARY KEY (channel_id, thread_id, member_id),
    FOREIGN KEY (channel_id, thread_id) REFERENCES threads(channel_id, thread_id),
    FOREIGN KEY (channel_id, member_id) REFERENCES channel_members(channel_id, member_id)
);

ALTER TABLE messages
    ADD CONSTRAINT messages_thread_in_channel
    FOREIGN KEY (channel_id, thread_id) REFERENCES threads(channel_id, thread_id);

CREATE TABLE direct_channels (
    channel_id UUID PRIMARY KEY,
    space_id UUID NOT NULL,
    member_low_id UUID NOT NULL,
    member_high_id UUID NOT NULL,
    UNIQUE (space_id, member_low_id, member_high_id),
    FOREIGN KEY (channel_id, space_id) REFERENCES channels(id, space_id),
    FOREIGN KEY (member_low_id, space_id) REFERENCES members(id, space_id),
    FOREIGN KEY (member_high_id, space_id) REFERENCES members(id, space_id),
    FOREIGN KEY (channel_id, member_low_id)
        REFERENCES channel_members(channel_id, member_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (channel_id, member_high_id)
        REFERENCES channel_members(channel_id, member_id)
        DEFERRABLE INITIALLY DEFERRED,
    CHECK (member_low_id < member_high_id)
);

CREATE FUNCTION enforce_direct_channel_member() RETURNS trigger AS $$
DECLARE
    direct_pair direct_channels%ROWTYPE;
BEGIN
    SELECT * INTO direct_pair FROM direct_channels WHERE channel_id = NEW.channel_id;
    IF FOUND AND NEW.member_id <> direct_pair.member_low_id
             AND NEW.member_id <> direct_pair.member_high_id THEN
        RAISE EXCEPTION 'direct Channel only accepts its two participants'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER channel_members_enforce_direct_pair
BEFORE INSERT OR UPDATE ON channel_members
FOR EACH ROW EXECUTE FUNCTION enforce_direct_channel_member();

CREATE FUNCTION validate_direct_channel_pair() RETURNS trigger AS $$
DECLARE
    channel_kind TEXT;
    participant_count BIGINT;
BEGIN
    SELECT kind INTO channel_kind FROM channels WHERE id = NEW.channel_id;
    IF channel_kind <> 'direct' THEN
        RAISE EXCEPTION 'direct_channels row requires a direct Channel'
            USING ERRCODE = '23514';
    END IF;

    SELECT count(*) INTO participant_count
    FROM channel_members
    WHERE channel_id = NEW.channel_id
      AND member_id IN (NEW.member_low_id, NEW.member_high_id);
    IF participant_count <> 2 OR
       (SELECT count(*) FROM channel_members WHERE channel_id = NEW.channel_id) <> 2 THEN
        RAISE EXCEPTION 'direct Channel must contain exactly its two participants'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER direct_channels_validate_pair
BEFORE INSERT OR UPDATE ON direct_channels
FOR EACH ROW EXECUTE FUNCTION validate_direct_channel_pair();

CREATE TABLE attachments (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL,
    uploader_member_id UUID NOT NULL,
    original_name TEXT NOT NULL CHECK (char_length(original_name) BETWEEN 1 AND 255),
    media_type TEXT NOT NULL CHECK (char_length(media_type) BETWEEN 1 AND 255),
    size BIGINT CHECK (size BETWEEN 0 AND 104857600),
    sha256 BYTEA CHECK (sha256 IS NULL OR octet_length(sha256) = 32),
    object_key TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'uploading' CHECK (status IN ('uploading', 'ready', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    UNIQUE (id, space_id),
    FOREIGN KEY (uploader_member_id, space_id) REFERENCES members(id, space_id),
    CHECK (
        (status = 'uploading' AND size IS NULL AND sha256 IS NULL AND deleted_at IS NULL)
        OR (status = 'ready' AND size IS NOT NULL AND sha256 IS NOT NULL AND deleted_at IS NULL)
        OR (status = 'deleted' AND size IS NOT NULL AND sha256 IS NOT NULL AND deleted_at IS NOT NULL)
    )
);

CREATE TABLE message_attachments (
    message_id UUID NOT NULL,
    attachment_id UUID NOT NULL UNIQUE,
    channel_id UUID NOT NULL,
    space_id UUID NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (message_id, attachment_id),
    UNIQUE (message_id, position),
    FOREIGN KEY (message_id, channel_id, space_id) REFERENCES messages(id, channel_id, space_id),
    FOREIGN KEY (attachment_id, space_id) REFERENCES attachments(id, space_id)
);

CREATE INDEX attachments_uploader_status_idx
    ON attachments (uploader_member_id, status, created_at DESC);

CREATE TABLE computers (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    hostname TEXT NOT NULL,
    os TEXT NOT NULL CHECK (os IN ('macos', 'linux')),
    token_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
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
    token_hash BYTEA NOT NULL CHECK (octet_length(token_hash) = 32),
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
    desired_lifecycle TEXT NOT NULL DEFAULT 'active'
        CHECK (desired_lifecycle IN ('active', 'suspended', 'retired')),
    provision_status TEXT NOT NULL DEFAULT 'provisioning'
        CHECK (provision_status IN ('provisioning', 'ready', 'error')),
    driver_kind TEXT NOT NULL CHECK (driver_kind IN ('codex', 'builtin')),
    driver_config_json JSONB NOT NULL,
    attention_config_json JSONB NOT NULL,
    created_by_member_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    retired_at TIMESTAMPTZ,
    last_error_code TEXT,
    CONSTRAINT agents_member_computer_unique UNIQUE (member_id, computer_id),
    FOREIGN KEY (member_id, space_id) REFERENCES members(id, space_id),
    FOREIGN KEY (computer_id, space_id) REFERENCES computers(id, space_id),
    FOREIGN KEY (created_by_member_id, space_id) REFERENCES members(id, space_id)
);

CREATE INDEX agents_computer_lifecycle_idx
    ON agents (computer_id, desired_lifecycle, provision_status, created_at);

CREATE TABLE computer_commands (
    id UUID PRIMARY KEY,
    computer_id UUID NOT NULL REFERENCES computers(id),
    computer_seq BIGINT NOT NULL CHECK (computer_seq > 0),
    kind TEXT NOT NULL,
    payload_json JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'acked', 'completed', 'failed')),
    result_json JSONB,
    result_event_id TEXT CHECK (char_length(result_event_id) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL,
    acked_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    UNIQUE (computer_id, computer_seq)
);

CREATE INDEX computer_commands_pending_idx
    ON computer_commands (computer_id, computer_seq) WHERE status = 'pending';
CREATE UNIQUE INDEX computer_commands_result_event_idx
    ON computer_commands (result_event_id) WHERE result_event_id IS NOT NULL;

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
    driver_kind TEXT NOT NULL CHECK (driver_kind IN ('codex', 'builtin')),
    role_revision BIGINT NOT NULL CHECK (role_revision > 0),
    status TEXT NOT NULL CHECK (status IN (
        'queued', 'running', 'completed', 'failed', 'canceled'
    )),
    created_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    exit_code INTEGER,
    error_code TEXT,
    run_attempt INTEGER CHECK (run_attempt IS NULL OR run_attempt > 0),
    process_instance_id UUID,
    started_event_id TEXT CHECK (char_length(started_event_id) BETWEEN 1 AND 128),
    fencing_token TEXT,
    ownership_lease_expires_at TIMESTAMPTZ,
    last_renewed_at TIMESTAMPTZ,
    FOREIGN KEY (agent_member_id, computer_id)
        REFERENCES agents(member_id, computer_id),
    CHECK (
        (status = 'queued' AND started_at IS NULL AND finished_at IS NULL)
        OR (status = 'running' AND started_at IS NOT NULL AND finished_at IS NULL)
        OR (status IN ('completed', 'failed', 'canceled') AND finished_at IS NOT NULL)
    ),
    CONSTRAINT agent_runs_active_ownership_check CHECK (
        status NOT IN ('queued', 'running')
        OR (
            fencing_token IS NOT NULL
            AND char_length(fencing_token) BETWEEN 1 AND 128
            AND ownership_lease_expires_at IS NOT NULL
            AND last_renewed_at IS NOT NULL
        )
    )
);

CREATE UNIQUE INDEX agent_runs_one_active_idx
    ON agent_runs (agent_member_id)
    WHERE status IN ('queued', 'running');

CREATE INDEX agent_runs_computer_created_idx
    ON agent_runs (computer_id, created_at DESC);
CREATE UNIQUE INDEX agent_runs_started_event_idx
    ON agent_runs (started_event_id) WHERE started_event_id IS NOT NULL;
CREATE INDEX agent_runs_expired_ownership_idx
    ON agent_runs (ownership_lease_expires_at)
    WHERE status IN ('queued', 'running');

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

CREATE TABLE approvals (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id),
    type TEXT NOT NULL CHECK (type = 'agent.create'),
    requested_by_member_id UUID NOT NULL,
    payload_json JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN (
        'pending', 'approved', 'rejected', 'canceled'
    )),
    resolved_by_member_id UUID,
    created_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    UNIQUE (id, space_id),
    FOREIGN KEY (requested_by_member_id, space_id) REFERENCES members(id, space_id),
    FOREIGN KEY (resolved_by_member_id, space_id) REFERENCES members(id, space_id),
    CHECK (
        (status = 'pending' AND resolved_by_member_id IS NULL AND resolved_at IS NULL)
        OR (status <> 'pending' AND resolved_by_member_id IS NOT NULL AND resolved_at IS NOT NULL)
    )
);

ALTER TABLE inbox_items ADD COLUMN approval_id UUID REFERENCES approvals(id);

CREATE INDEX approvals_space_status_created_idx
    ON approvals (space_id, status, created_at DESC);

CREATE INDEX inbox_items_approval_idx
    ON inbox_items (approval_id)
    WHERE approval_id IS NOT NULL;

CREATE UNIQUE INDEX inbox_items_one_pending_channel_ambient_idx
    ON inbox_items (member_id, channel_id)
    WHERE kind = 'channel_activity' AND thread_id IS NULL AND status = 'pending';

CREATE UNIQUE INDEX inbox_items_one_pending_thread_ambient_idx
    ON inbox_items (member_id, channel_id, thread_id)
    WHERE kind = 'thread_activity' AND thread_id IS NOT NULL AND status = 'pending';

CREATE TABLE tasks (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id),
    source_message_id UUID NOT NULL UNIQUE,
    channel_id UUID NOT NULL,
    created_by_member_id UUID NOT NULL,
    assigned_agent_member_id UUID REFERENCES agents(member_id),
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    status TEXT NOT NULL CHECK (status IN ('open', 'in_progress', 'done', 'canceled')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (source_message_id, channel_id, space_id)
        REFERENCES messages(id, channel_id, space_id),
    FOREIGN KEY (created_by_member_id, space_id)
        REFERENCES members(id, space_id),
    FOREIGN KEY (assigned_agent_member_id, space_id)
        REFERENCES members(id, space_id),
    CHECK (status <> 'in_progress' OR assigned_agent_member_id IS NOT NULL)
);

CREATE INDEX tasks_space_status_updated_idx
    ON tasks (space_id, status, updated_at DESC);
