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
