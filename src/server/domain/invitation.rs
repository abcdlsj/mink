use time::{Duration, OffsetDateTime};

use crate::ids::{MemberId, SpaceId};

use super::{DomainError, access::normalize_email};

/// Invitation 链接的有效窗口。该常量是有效期的唯一来源,请求不能自定义窗口。
const INVITATION_WINDOW: Duration = Duration::days(7);

/// Server 生成 token 后交给领域层的邀请意图。领域层只见散列,不见明文。
#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct InvitationDraft {
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) email_normalized: String,
    pub(in crate::server) token_hash: String,
    pub(in crate::server) created_by_member_id: MemberId,
}

impl InvitationDraft {
    /// token 由 Server 生成,领域层只接收其 SHA-256 十六进制散列。
    /// 客户端提供的 token 不可验证熵源,因此不进入该构造。
    pub(in crate::server) fn new(
        space_id: SpaceId,
        email: &str,
        token_hash: &str,
        created_by_member_id: MemberId,
    ) -> Result<Self, DomainError> {
        let email_normalized = normalize_email(email);
        if email_normalized.is_empty()
            || token_hash.len() != 64
            || !token_hash.bytes().all(|byte| byte.is_ascii_hexdigit())
        {
            return Err(DomainError::InvalidInvitation);
        }
        Ok(Self {
            space_id,
            email_normalized,
            token_hash: token_hash.to_lowercase(),
            created_by_member_id,
        })
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum InvitationStatus {
    Pending,
    Accepted,
    Expired,
}

impl InvitationStatus {
    pub(in crate::server) fn parse(value: &str) -> Result<Self, DomainError> {
        match value {
            "pending" => Ok(Self::Pending),
            "accepted" => Ok(Self::Accepted),
            "expired" => Ok(Self::Expired),
            _ => Err(DomainError::InvalidInvitation),
        }
    }

    pub(in crate::server) fn code(self) -> &'static str {
        match self {
            Self::Pending => "pending",
            Self::Accepted => "accepted",
            Self::Expired => "expired",
        }
    }
}

/// 一次邀请。接受后绑定到该 Space 中新建的 Human Member。
#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct Invitation {
    pub(in crate::server) draft: InvitationDraft,
    pub(in crate::server) status: InvitationStatus,
    pub(in crate::server) expires_at: OffsetDateTime,
    pub(in crate::server) accepted_by_member_id: Option<MemberId>,
    pub(in crate::server) accepted_at: Option<OffsetDateTime>,
}

impl Invitation {
    /// 建立待接受邀请。有效期由领域常量决定,调用方不能自定义窗口。
    pub(in crate::server) fn open(draft: InvitationDraft, now: OffsetDateTime) -> Self {
        Self {
            draft,
            status: InvitationStatus::Pending,
            expires_at: now + INVITATION_WINDOW,
            accepted_by_member_id: None,
            accepted_at: None,
        }
    }

    /// 过期只在读取时判定,不依赖后台任务。
    pub(in crate::server) fn has_lapsed(&self, now: OffsetDateTime) -> bool {
        self.status == InvitationStatus::Pending && self.expires_at <= now
    }

    pub(in crate::server) fn lapse(&mut self) {
        if self.status == InvitationStatus::Pending {
            self.status = InvitationStatus::Expired;
        }
    }

    /// 收件人校验。Invitation 指向一个具体 email,不是任何持有链接的人。
    /// 比较在规范化形式上进行,调用方传入原始或已规范化的 email 都得到同一结果。
    pub(in crate::server) fn recipient_matches(&self, user_email: &str) -> bool {
        self.draft.email_normalized == normalize_email(user_email)
    }

    /// 接受邀请并绑定新建 Human Member。
    ///
    /// 不变量:仅 pending 且未过期的邀请可以接受;接受者的 email 必须与
    /// 收件人一致;同一邀请不能接受两次。
    pub(in crate::server) fn accept(
        &mut self,
        member_id: MemberId,
        user_email: &str,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.status != InvitationStatus::Pending || self.expires_at <= now {
            return Err(DomainError::InvitationLapsed);
        }
        if !self.recipient_matches(user_email) {
            return Err(DomainError::InvitationEmailMismatch);
        }
        self.status = InvitationStatus::Accepted;
        self.accepted_by_member_id = Some(member_id);
        self.accepted_at = Some(now);
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const RECIPIENT: &str = "invitee@example.com";

    fn space_id() -> SpaceId {
        SpaceId::from_uuid(uuid::Uuid::from_u128(1))
    }

    fn member_id(value: u128) -> MemberId {
        MemberId::from_uuid(uuid::Uuid::from_u128(value))
    }

    fn draft() -> InvitationDraft {
        InvitationDraft::new(space_id(), RECIPIENT, &"a".repeat(64), member_id(2))
            .expect("draft is valid")
    }

    #[test]
    fn a_draft_requires_a_recipient_and_a_hex_token_hash() {
        assert_eq!(
            InvitationDraft::new(space_id(), "  ", &"a".repeat(64), member_id(2)),
            Err(DomainError::InvalidInvitation)
        );
        assert_eq!(
            InvitationDraft::new(space_id(), RECIPIENT, "short", member_id(2)),
            Err(DomainError::InvalidInvitation)
        );
        assert_eq!(
            InvitationDraft::new(space_id(), RECIPIENT, &"z".repeat(64), member_id(2)),
            Err(DomainError::InvalidInvitation)
        );
    }

    #[test]
    fn a_draft_normalizes_the_recipient_email_and_token_hash_case() {
        let draft = InvitationDraft::new(
            space_id(),
            " Invitee@Example.COM ",
            &"A".repeat(64),
            member_id(2),
        )
        .expect("draft is valid");
        assert_eq!(draft.email_normalized, RECIPIENT);
        assert_eq!(draft.token_hash, "a".repeat(64));
    }

    #[test]
    fn an_invitation_accepts_once_inside_its_window() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let mut invitation = Invitation::open(draft(), now);
        assert_eq!(invitation.expires_at, now + INVITATION_WINDOW);
        invitation
            .accept(member_id(3), RECIPIENT, now)
            .expect("accept succeeds inside the window");
        assert_eq!(invitation.status, InvitationStatus::Accepted);
        assert_eq!(invitation.accepted_by_member_id, Some(member_id(3)));
        assert_eq!(invitation.accepted_at, Some(now));
        // 已接受的邀请不能再绑定另一个 Member。
        assert_eq!(
            invitation.accept(member_id(4), RECIPIENT, now),
            Err(DomainError::InvitationLapsed)
        );
        assert_eq!(invitation.accepted_by_member_id, Some(member_id(3)));
    }

    #[test]
    fn a_lapsed_invitation_cannot_be_accepted() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let mut invitation = Invitation::open(draft(), now);
        let after = now + INVITATION_WINDOW;
        assert!(!invitation.has_lapsed(now));
        assert!(invitation.has_lapsed(after));
        assert_eq!(
            invitation.accept(member_id(3), RECIPIENT, after),
            Err(DomainError::InvitationLapsed)
        );
        invitation.lapse();
        assert_eq!(invitation.status, InvitationStatus::Expired);
        // 已过期后不再被 lapse 覆盖为其他状态。
        invitation.lapse();
        assert_eq!(invitation.status, InvitationStatus::Expired);
        // 过期后即使收件人正确也不能接受。
        assert_eq!(
            invitation.accept(member_id(3), RECIPIENT, now),
            Err(DomainError::InvitationLapsed)
        );
        assert_eq!(invitation.accepted_by_member_id, None);
    }

    #[test]
    fn only_the_named_recipient_can_accept() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let mut invitation = Invitation::open(draft(), now);
        assert_eq!(
            invitation.accept(member_id(3), "other@example.com", now),
            Err(DomainError::InvitationEmailMismatch)
        );
        assert_eq!(invitation.status, InvitationStatus::Pending);
        // 大小写和首尾空白差异不构成不匹配。
        invitation
            .accept(member_id(3), " Invitee@Example.COM ", now)
            .expect("normalized recipient matches");
        assert_eq!(invitation.status, InvitationStatus::Accepted);
    }

    #[test]
    fn status_codes_round_trip_and_reject_unknown_values() {
        for status in [
            InvitationStatus::Pending,
            InvitationStatus::Accepted,
            InvitationStatus::Expired,
        ] {
            assert_eq!(InvitationStatus::parse(status.code()), Ok(status));
        }
        assert_eq!(
            InvitationStatus::parse("revoked"),
            Err(DomainError::InvalidInvitation)
        );
    }
}
