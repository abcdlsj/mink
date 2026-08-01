CREATE TABLE schema_meta (
    version INTEGER PRIMARY KEY CHECK (version > 0),
    applied_at TIMESTAMPTZ NOT NULL
);
INSERT INTO schema_meta (version, applied_at) VALUES (1, now());

CREATE TABLE users (
    id UUID PRIMARY KEY,
    email_normalized TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    disabled_at TIMESTAMPTZ
);

CREATE TABLE browser_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (expires_at > created_at)
);

CREATE TABLE spaces (
    id UUID PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    owner_member_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    UNIQUE (id, owner_member_id)
);

CREATE TABLE members (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('human', 'agent')),
    display_name TEXT NOT NULL,
    handle TEXT NOT NULL,
    access_level TEXT NOT NULL CHECK (access_level IN ('owner', 'admin', 'member')),
    created_at TIMESTAMPTZ NOT NULL,
    retired_at TIMESTAMPTZ,
    UNIQUE (id, space_id),
    CHECK (lower(handle) <> 'all')
);
CREATE UNIQUE INDEX members_space_handle_unique ON members (space_id, lower(handle));
CREATE UNIQUE INDEX members_one_owner_per_space ON members (space_id) WHERE access_level = 'owner';
ALTER TABLE spaces ADD CONSTRAINT spaces_owner_member_in_space
    FOREIGN KEY (owner_member_id, id) REFERENCES members(id, space_id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE human_members (
    member_id UUID PRIMARY KEY,
    space_id UUID NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    UNIQUE (space_id, user_id),
    FOREIGN KEY (member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT
);

CREATE TABLE space_invitations (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    email_normalized TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'expired')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_by_member_id UUID NOT NULL,
    accepted_by_member_id UUID,
    created_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    UNIQUE (id, space_id),
    FOREIGN KEY (created_by_member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (accepted_by_member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT,
    CHECK (expires_at > created_at),
    CHECK (
        (status = 'pending' AND accepted_by_member_id IS NULL AND accepted_at IS NULL)
        OR (status = 'accepted' AND accepted_by_member_id IS NOT NULL AND accepted_at IS NOT NULL)
        OR (status = 'expired' AND accepted_by_member_id IS NULL AND accepted_at IS NULL)
    )
);
CREATE UNIQUE INDEX space_invitations_one_pending_per_email
    ON space_invitations (space_id, email_normalized) WHERE status = 'pending';
CREATE INDEX space_invitations_space_cursor
    ON space_invitations (space_id, created_at DESC, id DESC);

CREATE TABLE member_permissions (
    member_id UUID NOT NULL,
    space_id UUID NOT NULL,
    action_code TEXT NOT NULL CHECK (action_code IN ('channel.create', 'agent.create')),
    granted_by_member_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (member_id, action_code),
    FOREIGN KEY (member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (granted_by_member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT
);

CREATE TABLE computers (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    name TEXT NOT NULL,
    hostname TEXT NOT NULL,
    os TEXT NOT NULL CHECK (os IN ('macos', 'linux')),
    token_hash TEXT UNIQUE,
    connection_status TEXT NOT NULL CHECK (connection_status IN ('offline', 'online')),
    daemon_version TEXT,
    next_command_seq BIGINT NOT NULL DEFAULT 1 CHECK (next_command_seq > 0),
    last_seen_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    UNIQUE (id, space_id),
    CHECK (deleted_at IS NULL OR token_hash IS NULL)
);

CREATE TABLE computer_pairings (
    id UUID PRIMARY KEY,
    code_hash TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    hostname TEXT NOT NULL,
    os TEXT NOT NULL CHECK (os IN ('macos', 'linux')),
    daemon_version TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'confirmed', 'expired')),
    expires_at TIMESTAMPTZ NOT NULL,
    computer_id UUID UNIQUE REFERENCES computers(id) ON DELETE RESTRICT,
    space_id UUID REFERENCES spaces(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL,
    confirmed_at TIMESTAMPTZ,
    CHECK (
        (status = 'pending' AND computer_id IS NULL AND space_id IS NULL AND confirmed_at IS NULL)
        OR (status = 'confirmed' AND computer_id IS NOT NULL AND space_id IS NOT NULL AND confirmed_at IS NOT NULL)
        OR status = 'expired'
    )
);

CREATE TABLE agents (
    member_id UUID PRIMARY KEY,
    space_id UUID NOT NULL,
    computer_id UUID,
    role_text TEXT NOT NULL,
    role_revision BIGINT NOT NULL CHECK (role_revision > 0),
    lifecycle TEXT NOT NULL CHECK (lifecycle IN ('provisioning', 'active', 'suspended', 'retired', 'error')),
    driver_kind TEXT NOT NULL CHECK (driver_kind IN ('codex', 'builtin')),
    driver_config_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL,
    retired_at TIMESTAMPTZ,
    FOREIGN KEY (member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (computer_id, space_id) REFERENCES computers(id, space_id) ON DELETE RESTRICT,
    CHECK (
        (lifecycle = 'retired' AND computer_id IS NULL AND retired_at IS NOT NULL)
        OR (lifecycle <> 'retired' AND computer_id IS NOT NULL AND retired_at IS NULL)
    )
);

CREATE TABLE channels (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('public', 'private', 'direct')),
    slug TEXT,
    topic TEXT,
    next_seq BIGINT NOT NULL DEFAULT 1 CHECK (next_seq > 0),
    created_at TIMESTAMPTZ NOT NULL,
    archived_at TIMESTAMPTZ,
    UNIQUE (id, space_id),
    CHECK ((kind = 'direct' AND slug IS NULL) OR (kind <> 'direct' AND slug IS NOT NULL))
);
CREATE UNIQUE INDEX channels_space_slug_unique
    ON channels (space_id, lower(slug)) WHERE kind <> 'direct';

CREATE TABLE channel_members (
    channel_id UUID NOT NULL,
    space_id UUID NOT NULL,
    member_id UUID NOT NULL,
    joined_at TIMESTAMPTZ NOT NULL,
    last_read_seq BIGINT NOT NULL DEFAULT 0 CHECK (last_read_seq >= 0),
    PRIMARY KEY (channel_id, member_id),
    FOREIGN KEY (channel_id, space_id) REFERENCES channels(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT
);

CREATE TABLE messages (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL,
    channel_id UUID NOT NULL,
    thread_id UUID NOT NULL,
    channel_seq BIGINT NOT NULL CHECK (channel_seq > 0),
    placement TEXT NOT NULL CHECK (placement IN ('root', 'reply')),
    content_kind TEXT NOT NULL CHECK (content_kind IN ('text', 'channel_created', 'agent_created')),
    reply_to_message_id UUID,
    author_member_id UUID NOT NULL,
    body_markdown TEXT,
    mention_all BOOLEAN NOT NULL DEFAULT FALSE,
    action_channel_id UUID,
    action_agent_member_id UUID,
    created_at TIMESTAMPTZ NOT NULL,
    edited_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    UNIQUE (id, space_id),
    UNIQUE (channel_id, channel_seq),
    FOREIGN KEY (channel_id, space_id) REFERENCES channels(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (author_member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (reply_to_message_id) REFERENCES messages(id) ON DELETE RESTRICT,
    FOREIGN KEY (action_channel_id) REFERENCES channels(id) ON DELETE RESTRICT,
    FOREIGN KEY (action_agent_member_id) REFERENCES members(id) ON DELETE RESTRICT,
    CHECK (
        (content_kind = 'text' AND body_markdown IS NOT NULL AND action_channel_id IS NULL AND action_agent_member_id IS NULL)
        OR (content_kind = 'channel_created' AND placement = 'reply' AND body_markdown IS NULL AND action_channel_id IS NOT NULL AND action_agent_member_id IS NULL)
        OR (content_kind = 'agent_created' AND placement = 'reply' AND body_markdown IS NULL AND action_channel_id IS NULL AND action_agent_member_id IS NOT NULL)
    ),
    CHECK ((placement = 'root' AND id = thread_id AND reply_to_message_id IS NULL) OR placement = 'reply')
);

-- Structured mention targets are authoritative for Message projections. The Message body is never
-- parsed by consumers to determine who was addressed.
CREATE TABLE message_mentions (
    message_id UUID NOT NULL,
    space_id UUID NOT NULL,
    member_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (message_id, member_id),
    FOREIGN KEY (message_id, space_id) REFERENCES messages(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT
);

CREATE TABLE threads (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL,
    channel_id UUID NOT NULL,
    root_message_id UUID NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (id, space_id),
    FOREIGN KEY (channel_id, space_id) REFERENCES channels(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (root_message_id) REFERENCES messages(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
    CHECK (id = root_message_id)
);

CREATE TABLE thread_subscriptions (
    thread_id UUID NOT NULL,
    space_id UUID NOT NULL,
    member_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (thread_id, member_id),
    FOREIGN KEY (thread_id, space_id) REFERENCES threads(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT
);
CREATE INDEX thread_subscriptions_by_thread ON thread_subscriptions (thread_id);

ALTER TABLE messages ADD CONSTRAINT messages_thread_in_space
    FOREIGN KEY (thread_id, space_id) REFERENCES threads(id, space_id)
    ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE attachments (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    uploader_member_id UUID NOT NULL,
    name TEXT NOT NULL,
    media_type TEXT NOT NULL,
    length BIGINT,
    sha256 BYTEA,
    object_key TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('uploading', 'ready', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL,
    ready_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    UNIQUE (id, space_id),
    FOREIGN KEY (uploader_member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT,
    CHECK (sha256 IS NULL OR octet_length(sha256) = 32),
    CHECK (
        (status = 'uploading' AND length IS NULL AND sha256 IS NULL AND ready_at IS NULL AND deleted_at IS NULL)
        OR (status = 'ready' AND length >= 0 AND sha256 IS NOT NULL AND ready_at IS NOT NULL AND deleted_at IS NULL)
        OR (status = 'deleted' AND length >= 0 AND sha256 IS NOT NULL AND ready_at IS NOT NULL AND deleted_at IS NOT NULL)
    )
);

CREATE TABLE message_attachments (
    message_id UUID NOT NULL,
    attachment_id UUID NOT NULL UNIQUE,
    space_id UUID NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (message_id, attachment_id),
    UNIQUE (message_id, position),
    FOREIGN KEY (message_id, space_id) REFERENCES messages(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (attachment_id, space_id) REFERENCES attachments(id, space_id) ON DELETE RESTRICT
);

CREATE TABLE tasks (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    title TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('todo', 'in_progress', 'in_review', 'done', 'closed')),
    source_thread_id UUID NOT NULL UNIQUE,
    creator_member_id UUID NOT NULL,
    assignee_agent_member_id UUID,
    result_message_id UUID,
    close_reason_code TEXT CHECK (close_reason_code IN ('invalid', 'duplicate', 'not_needed', 'obsolete', 'other')),
    close_reason_note TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    UNIQUE (id, space_id),
    FOREIGN KEY (source_thread_id, space_id) REFERENCES threads(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (creator_member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (assignee_agent_member_id) REFERENCES agents(member_id) ON DELETE RESTRICT,
    FOREIGN KEY (result_message_id, space_id) REFERENCES messages(id, space_id) ON DELETE RESTRICT,
    CHECK (
        (status = 'done' AND result_message_id IS NOT NULL AND close_reason_code IS NULL AND close_reason_note IS NULL AND finished_at IS NOT NULL)
        OR (status = 'closed' AND result_message_id IS NULL AND close_reason_code IS NOT NULL AND finished_at IS NOT NULL)
        OR (status IN ('todo', 'in_progress', 'in_review') AND result_message_id IS NULL AND close_reason_code IS NULL AND close_reason_note IS NULL AND finished_at IS NULL)
    ),
    CHECK (status NOT IN ('in_progress', 'in_review') OR assignee_agent_member_id IS NOT NULL)
);

CREATE TABLE task_threads (
    task_id UUID NOT NULL,
    thread_id UUID NOT NULL,
    space_id UUID NOT NULL,
    linked_by_member_id UUID NOT NULL,
    linked_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (task_id, thread_id),
    FOREIGN KEY (task_id, space_id) REFERENCES tasks(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (thread_id, space_id) REFERENCES threads(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (linked_by_member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT
);

CREATE TABLE agent_runs (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    agent_id UUID NOT NULL REFERENCES agents(member_id) ON DELETE RESTRICT,
    task_id UUID,
    focus_thread_id UUID NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'starting', 'running', 'finalizing', 'completed', 'yielded', 'failed', 'stopping', 'canceled')),
    fencing_token_hash TEXT NOT NULL,
    lease_expires_at TIMESTAMPTZ NOT NULL,
    outcome_code TEXT CHECK (outcome_code IN ('completed', 'yielded', 'failed', 'canceled')),
    error_code TEXT CHECK (
        error_code IN (
            'invalid_command',
            'agent_unavailable',
            'process_lost',
            'session_lost',
            'sandbox_unavailable',
            'driver_unavailable',
            'internal'
        )
    ),
    continuation_note TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    UNIQUE (id, space_id),
    FOREIGN KEY (task_id, space_id) REFERENCES tasks(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (focus_thread_id, space_id) REFERENCES threads(id, space_id) ON DELETE RESTRICT,
    CHECK ((status IN ('completed', 'yielded', 'failed', 'canceled')) = (finished_at IS NOT NULL)),
    CHECK ((status IN ('completed', 'yielded', 'failed', 'canceled')) = (outcome_code IS NOT NULL)),
    CHECK (error_code IS NULL OR outcome_code = 'failed')
);
CREATE UNIQUE INDEX agent_runs_one_active_per_agent
    ON agent_runs(agent_id) WHERE status NOT IN ('completed', 'yielded', 'failed', 'canceled');

CREATE TABLE inbox_items (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    agent_id UUID NOT NULL REFERENCES agents(member_id) ON DELETE RESTRICT,
    message_id UUID,
    thread_id UUID NOT NULL,
    task_id UUID,
    kind TEXT NOT NULL CHECK (kind IN ('direct', 'mention', 'reply', 'task_activity', 'thread_activity', 'channel_activity', 'system')),
    strength TEXT NOT NULL CHECK (strength IN ('hard', 'ambient')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'leased', 'deferred', 'handled', 'dead')),
    available_at TIMESTAMPTZ NOT NULL,
    lease_run_id UUID,
    lease_expires_at TIMESTAMPTZ,
    retry_count INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    requeue_count INTEGER NOT NULL DEFAULT 0 CHECK (requeue_count >= 0),
    handled_at TIMESTAMPTZ,
    last_error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    first_message_seq BIGINT CHECK (first_message_seq > 0),
    last_message_seq BIGINT CHECK (last_message_seq > 0),
    aggregated_count INTEGER CHECK (aggregated_count > 0),
    force_at TIMESTAMPTZ,
    UNIQUE (id, space_id),
    FOREIGN KEY (message_id, space_id) REFERENCES messages(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (thread_id, space_id) REFERENCES threads(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (task_id, space_id) REFERENCES tasks(id, space_id) ON DELETE RESTRICT,
    FOREIGN KEY (lease_run_id, space_id) REFERENCES agent_runs(id, space_id) ON DELETE RESTRICT,
    CHECK ((status = 'leased') = (lease_run_id IS NOT NULL AND lease_expires_at IS NOT NULL)),
    CHECK (status <> 'handled' OR handled_at IS NOT NULL),
    -- The four aggregate columns describe one Message range, so they are present or absent together.
    CHECK (num_nonnulls(first_message_seq, last_message_seq, aggregated_count, force_at) IN (0, 4)),
    -- Only an ambient Item aggregates. It stands for a Message range, so it names no single Message.
    CHECK (first_message_seq IS NULL OR (strength = 'ambient' AND message_id IS NULL)),
    CHECK (last_message_seq IS NULL OR last_message_seq >= first_message_seq),
    -- The count cannot exceed the sequences the range spans.
    CHECK (aggregated_count IS NULL OR aggregated_count <= last_message_seq - first_message_seq + 1)
);
-- One open ambient aggregate per Agent and Thread. Concurrent publishers therefore serialize on this
-- index instead of each inserting a competing aggregate row.
--
-- `retry_count = 0` restricts the index to aggregates that were never claimed. A retried aggregate
-- covers a range the Agent already received, so it accepts no further Messages and must not block the
-- next aggregate for that Thread.
CREATE UNIQUE INDEX inbox_items_open_ambient_aggregate
    ON inbox_items(agent_id, thread_id)
    WHERE strength = 'ambient' AND status = 'pending' AND retry_count = 0;

CREATE TABLE run_items (
    run_id UUID NOT NULL,
    inbox_item_id UUID NOT NULL,
    delivery_seq BIGINT NOT NULL CHECK (delivery_seq > 0),
    attached_at TIMESTAMPTZ NOT NULL,
    disposition TEXT CHECK (disposition IN ('handled', 'deferred', 'released')),
    PRIMARY KEY (run_id, inbox_item_id),
    UNIQUE (run_id, delivery_seq),
    FOREIGN KEY (run_id) REFERENCES agent_runs(id) ON DELETE RESTRICT,
    FOREIGN KEY (inbox_item_id) REFERENCES inbox_items(id) ON DELETE RESTRICT
);

CREATE TABLE run_result_events (
    event_id UUID PRIMARY KEY,
    run_id UUID NOT NULL UNIQUE REFERENCES agent_runs(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE computer_commands (
    id UUID PRIMARY KEY,
    computer_id UUID NOT NULL REFERENCES computers(id) ON DELETE RESTRICT,
    computer_seq BIGINT NOT NULL CHECK (computer_seq > 0),
    kind TEXT NOT NULL,
    payload_json JSONB NOT NULL,
    acked_at TIMESTAMPTZ,
    result_event_id UUID UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (computer_id, computer_seq)
);

CREATE TABLE outbox_events (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL,
    payload_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ
);
CREATE INDEX outbox_events_pending ON outbox_events(created_at) WHERE published_at IS NULL;

CREATE TABLE idempotency_records (
    actor_member_id UUID NOT NULL REFERENCES members(id) ON DELETE RESTRICT,
    action TEXT NOT NULL,
    idempotency_key UUID NOT NULL,
    response_code TEXT NOT NULL,
    resource_id UUID NOT NULL,
    result_hash BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (actor_member_id, action, idempotency_key)
);

CREATE TABLE audit_events (
    id UUID PRIMARY KEY,
    space_id UUID NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    actor_member_id UUID,
    action TEXT NOT NULL,
    subject_type TEXT NOT NULL,
    subject_id UUID NOT NULL,
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (actor_member_id, space_id) REFERENCES members(id, space_id) ON DELETE RESTRICT
);

CREATE FUNCTION enforce_space_owner() RETURNS trigger AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM members
        WHERE id = NEW.owner_member_id AND space_id = NEW.id
          AND kind = 'human' AND access_level = 'owner'
    ) THEN
        RAISE EXCEPTION 'Space owner must be its Human Owner Member' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER spaces_enforce_human_owner
AFTER INSERT OR UPDATE OF owner_member_id ON spaces
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_space_owner();

CREATE FUNCTION enforce_agent_identity_and_assignment() RETURNS trigger AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM members WHERE id = NEW.member_id AND space_id = NEW.space_id AND kind = 'agent'
    ) THEN
        RAISE EXCEPTION 'Agent must reference an Agent Member' USING ERRCODE = '23514';
    END IF;
    IF NEW.computer_id IS NOT NULL AND EXISTS (
        SELECT 1 FROM computers WHERE id = NEW.computer_id AND deleted_at IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'Agent cannot be assigned to a deleted Computer' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER agents_enforce_identity_and_assignment
BEFORE INSERT OR UPDATE OF member_id, space_id, computer_id ON agents
FOR EACH ROW EXECUTE FUNCTION enforce_agent_identity_and_assignment();

CREATE FUNCTION enforce_reply_thread() RETURNS trigger AS $$
DECLARE
    target_thread UUID;
BEGIN
    IF NEW.placement = 'reply' AND NEW.reply_to_message_id IS NOT NULL THEN
        SELECT thread_id INTO target_thread FROM messages WHERE id = NEW.reply_to_message_id;
        IF target_thread IS NULL OR target_thread <> NEW.thread_id THEN
            RAISE EXCEPTION 'Reply target must belong to the same Thread' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER messages_enforce_reply_thread
BEFORE INSERT OR UPDATE OF placement, reply_to_message_id, thread_id ON messages
FOR EACH ROW EXECUTE FUNCTION enforce_reply_thread();

CREATE FUNCTION enforce_ready_attachment() RETURNS trigger AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM attachments
        WHERE id = NEW.attachment_id AND space_id = NEW.space_id AND status = 'ready'
    ) THEN
        RAISE EXCEPTION 'Message can only reference a ready Attachment in the same Space'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER message_attachments_enforce_ready
BEFORE INSERT OR UPDATE OF attachment_id, space_id ON message_attachments
FOR EACH ROW EXECUTE FUNCTION enforce_ready_attachment();

CREATE FUNCTION enforce_task_thread_availability() RETURNS trigger AS $$
DECLARE
    source_thread UUID;
BEGIN
    PERFORM 1 FROM threads WHERE id = NEW.thread_id FOR UPDATE;
    SELECT tasks.source_thread_id INTO source_thread FROM tasks WHERE id = NEW.task_id;
    IF source_thread = NEW.thread_id THEN
        RAISE EXCEPTION 'Source Thread cannot be stored as a Related Thread' USING ERRCODE = '23514';
    END IF;
    IF EXISTS (
        SELECT 1 FROM tasks
        WHERE source_thread_id = NEW.thread_id AND id <> NEW.task_id
          AND status NOT IN ('done', 'closed')
    ) OR EXISTS (
        SELECT 1 FROM task_threads other
        JOIN tasks ON tasks.id = other.task_id
        WHERE other.thread_id = NEW.thread_id AND other.task_id <> NEW.task_id
          AND tasks.status NOT IN ('done', 'closed')
    ) THEN
        RAISE EXCEPTION 'Thread already belongs to another unfinished Task' USING ERRCODE = '23505';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER task_threads_enforce_availability
BEFORE INSERT OR UPDATE OF task_id, thread_id ON task_threads
FOR EACH ROW EXECUTE FUNCTION enforce_task_thread_availability();

CREATE FUNCTION enforce_task_source_availability() RETURNS trigger AS $$
BEGIN
    PERFORM 1 FROM threads WHERE id = NEW.source_thread_id FOR UPDATE;
    IF NEW.status NOT IN ('done', 'closed') AND EXISTS (
        SELECT 1 FROM task_threads links
        JOIN tasks ON tasks.id = links.task_id
        WHERE links.thread_id = NEW.source_thread_id AND links.task_id <> NEW.id
          AND tasks.status NOT IN ('done', 'closed')
    ) THEN
        RAISE EXCEPTION 'Source Thread belongs to another unfinished Task' USING ERRCODE = '23505';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER tasks_enforce_source_availability
BEFORE INSERT OR UPDATE OF source_thread_id, status ON tasks
FOR EACH ROW EXECUTE FUNCTION enforce_task_source_availability();

CREATE FUNCTION enforce_invitation_members() RETURNS trigger AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM members
        WHERE id = NEW.created_by_member_id AND space_id = NEW.space_id AND kind = 'human'
    ) THEN
        RAISE EXCEPTION 'Invitation creator must be a Human Member' USING ERRCODE = '23514';
    END IF;
    IF NEW.accepted_by_member_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM members
        WHERE id = NEW.accepted_by_member_id AND space_id = NEW.space_id AND kind = 'human'
    ) THEN
        RAISE EXCEPTION 'Invitation can only be accepted by a Human Member' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER space_invitations_enforce_members
BEFORE INSERT OR UPDATE OF space_id, created_by_member_id, accepted_by_member_id ON space_invitations
FOR EACH ROW EXECUTE FUNCTION enforce_invitation_members();

CREATE FUNCTION enforce_run_focus() RETURNS trigger AS $$
BEGIN
    IF NEW.task_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM tasks
        WHERE id = NEW.task_id AND source_thread_id = NEW.focus_thread_id
        UNION ALL
        SELECT 1 FROM task_threads
        WHERE task_id = NEW.task_id AND thread_id = NEW.focus_thread_id
    ) THEN
        RAISE EXCEPTION 'Run Focus must belong to its Task' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER agent_runs_enforce_focus
BEFORE INSERT OR UPDATE OF task_id, focus_thread_id ON agent_runs
FOR EACH ROW EXECUTE FUNCTION enforce_run_focus();

CREATE INDEX messages_channel_cursor ON messages(channel_id, channel_seq DESC);
CREATE INDEX tasks_space_cursor ON tasks(space_id, updated_at DESC, id DESC);
CREATE INDEX agent_runs_task_cursor ON agent_runs(task_id, created_at DESC, id DESC);
CREATE INDEX inbox_items_pending ON inbox_items(agent_id, available_at, id)
    WHERE status IN ('pending', 'deferred');
