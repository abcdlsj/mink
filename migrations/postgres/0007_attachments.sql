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
