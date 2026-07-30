-- Space Invitation。Human 加入既有 Space 的唯一途径。
-- token 由 Server 生成,数据库只保存 SHA-256 散列,与 browser_sessions
-- 和 computers 的 token 处理方式一致。

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

-- 同一 Space 内同一收件人只能持有一个待接受的 Invitation。
-- 已接受和已过期的记录保留为历史事实,不参与该唯一性。
CREATE UNIQUE INDEX space_invitations_one_pending_per_email
    ON space_invitations (space_id, email_normalized) WHERE status = 'pending';

-- 治理页面按 Space 列出 Invitation,按创建时间倒序。
CREATE INDEX space_invitations_space_cursor
    ON space_invitations (space_id, created_at DESC, id DESC);

-- 接受者必须是该 Space 中的 Human Member。复合外键只能保证 Member 属于
-- 同一 Space,不能保证 kind,因此与 agents 一样用触发器补齐。
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
