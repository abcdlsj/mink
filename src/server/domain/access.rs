use time::OffsetDateTime;

use crate::ids::{MemberId, SpaceId};

use super::{DomainError, identity::AccessLevel};

/// Human 凭据的最小长度。短于该长度的密码不建立账号。
const MINIMUM_PASSWORD_LENGTH: usize = 12;

/// email 的规范化形式。`users.email_normalized`是账号唯一键,Invitation 的收件人
/// 必须按同一规则规范化才能与之比较,因此该函数是两处的唯一来源。
pub(in crate::server) fn normalize_email(email: &str) -> String {
    email.trim().to_lowercase()
}

/// 注册请求经过规范化后的 Human 身份。规范化后的 email 是账号唯一键。
#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct HumanRegistration {
    pub(in crate::server) display_name: String,
    pub(in crate::server) email_normalized: String,
}

impl HumanRegistration {
    pub(in crate::server) fn new(
        display_name: &str,
        email: &str,
        password_length: usize,
    ) -> Result<Self, DomainError> {
        let display_name = display_name.trim();
        let email_normalized = normalize_email(email);
        if display_name.is_empty()
            || email_normalized.is_empty()
            || password_length < MINIMUM_PASSWORD_LENGTH
        {
            return Err(DomainError::InvalidCredential);
        }
        Ok(Self {
            display_name: display_name.to_owned(),
            email_normalized,
        })
    }
}

/// 已认证 Human 在某个 Space 中的 Member 身份和访问级别。
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct SpaceAccess {
    pub(in crate::server) member_id: MemberId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) access_level: AccessLevel,
}

impl SpaceAccess {
    /// Space 治理动作要求 Owner 或 Admin。该判断只属于 [`AccessLevel`]。
    pub(in crate::server) fn require_governor(self) -> Result<MemberId, DomainError> {
        if self.access_level.can_manage_space() {
            Ok(self.member_id)
        } else {
            Err(DomainError::GovernorRequired)
        }
    }
}

/// Browser Session 的过期时间由建立时刻和配置 TTL 决定。
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) struct SessionLifetime {
    hours: i64,
}

impl SessionLifetime {
    pub(in crate::server) fn from_hours(hours: i64) -> Result<Self, DomainError> {
        if hours <= 0 {
            return Err(DomainError::InvalidCredential);
        }
        Ok(Self { hours })
    }

    pub(in crate::server) fn expires_at(self, now: OffsetDateTime) -> OffsetDateTime {
        now + time::Duration::hours(self.hours)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn registration_rejects_blank_identity_and_short_password() {
        assert_eq!(
            HumanRegistration::new("  ", "user@example.com", 16),
            Err(DomainError::InvalidCredential)
        );
        assert_eq!(
            HumanRegistration::new("User", "   ", 16),
            Err(DomainError::InvalidCredential)
        );
        assert_eq!(
            HumanRegistration::new("User", "user@example.com", MINIMUM_PASSWORD_LENGTH - 1),
            Err(DomainError::InvalidCredential)
        );
    }

    #[test]
    fn registration_normalizes_email_case_and_surrounding_space() {
        let registration =
            HumanRegistration::new("  User  ", "  User@Example.COM ", MINIMUM_PASSWORD_LENGTH)
                .expect("registration is valid");
        assert_eq!(registration.display_name, "User");
        assert_eq!(registration.email_normalized, "user@example.com");
    }

    #[test]
    fn only_owner_and_admin_pass_the_governor_check() {
        let space_id = SpaceId::from_uuid(uuid::Uuid::from_u128(1));
        let member_id = MemberId::from_uuid(uuid::Uuid::from_u128(2));
        for level in [AccessLevel::Owner, AccessLevel::Admin] {
            let access = SpaceAccess {
                member_id,
                space_id,
                access_level: level,
            };
            assert_eq!(access.require_governor(), Ok(member_id));
        }
        let access = SpaceAccess {
            member_id,
            space_id,
            access_level: AccessLevel::Member,
        };
        assert_eq!(
            access.require_governor(),
            Err(DomainError::GovernorRequired)
        );
    }

    #[test]
    fn session_lifetime_requires_a_positive_window() {
        assert_eq!(
            SessionLifetime::from_hours(0),
            Err(DomainError::InvalidCredential)
        );
        let lifetime = SessionLifetime::from_hours(12).expect("12 hours is valid");
        let now = OffsetDateTime::UNIX_EPOCH;
        assert_eq!(lifetime.expires_at(now), now + time::Duration::hours(12));
    }
}
