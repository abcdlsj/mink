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
    member_id UUID NOT NULL REFERENCES members(id),
    permission TEXT NOT NULL CHECK (permission IN ('channel:create', 'agent:create')),
    granted_by_member_id UUID NOT NULL REFERENCES members(id),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (member_id, permission)
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

