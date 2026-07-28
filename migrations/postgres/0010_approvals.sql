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
