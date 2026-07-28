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
