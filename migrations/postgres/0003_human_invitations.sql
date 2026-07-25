ALTER TABLE member_permissions
    ADD COLUMN space_id UUID;

UPDATE member_permissions
SET space_id = members.space_id
FROM members
WHERE members.id = member_permissions.member_id;

ALTER TABLE member_permissions
    ALTER COLUMN space_id SET NOT NULL,
    DROP CONSTRAINT member_permissions_member_id_fkey,
    DROP CONSTRAINT member_permissions_granted_by_member_id_fkey,
    ADD CONSTRAINT member_permissions_member_in_space
        FOREIGN KEY (member_id, space_id) REFERENCES members (id, space_id),
    ADD CONSTRAINT member_permissions_granter_in_space
        FOREIGN KEY (granted_by_member_id, space_id) REFERENCES members (id, space_id);

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
