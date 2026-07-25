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
