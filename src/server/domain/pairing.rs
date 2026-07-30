use time::{Duration, OffsetDateTime};

use crate::ids::{ComputerId, SpaceId};

use super::DomainError;

/// 配对 code 与 Computer Token 的有效窗口。超过该窗口的配对不能确认。
const PAIRING_WINDOW: Duration = Duration::minutes(10);

/// Computer 支持的操作系统。schema 只接受这两个取值。
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum ComputerOs {
    MacOs,
    Linux,
}

impl ComputerOs {
    pub(in crate::server) fn parse(value: &str) -> Result<Self, DomainError> {
        match value {
            "macos" => Ok(Self::MacOs),
            "linux" => Ok(Self::Linux),
            _ => Err(DomainError::InvalidPairing),
        }
    }

    pub(in crate::server) fn code(self) -> &'static str {
        match self {
            Self::MacOs => "macos",
            Self::Linux => "linux",
        }
    }
}

/// daemon 自证的本机信息。Server 不校验其真实性，只校验形式。
#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct PairingRequest {
    pub(in crate::server) token_hash: String,
    pub(in crate::server) hostname: String,
    pub(in crate::server) os: ComputerOs,
    pub(in crate::server) daemon_version: String,
}

impl PairingRequest {
    /// token 由 daemon 生成，Server 只接收其 SHA-256 十六进制散列。
    pub(in crate::server) fn new(
        token_hash: &str,
        hostname: &str,
        os: &str,
        daemon_version: &str,
    ) -> Result<Self, DomainError> {
        let hostname = hostname.trim();
        if token_hash.len() != 64
            || !token_hash.bytes().all(|byte| byte.is_ascii_hexdigit())
            || hostname.is_empty()
        {
            return Err(DomainError::InvalidPairing);
        }
        Ok(Self {
            token_hash: token_hash.to_lowercase(),
            hostname: hostname.to_owned(),
            os: ComputerOs::parse(os)?,
            daemon_version: daemon_version.to_owned(),
        })
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum PairingStatus {
    Pending,
    Confirmed,
    Expired,
}

impl PairingStatus {
    pub(in crate::server) fn parse(value: &str) -> Result<Self, DomainError> {
        match value {
            "pending" => Ok(Self::Pending),
            "confirmed" => Ok(Self::Confirmed),
            "expired" => Ok(Self::Expired),
            _ => Err(DomainError::InvalidPairing),
        }
    }

    pub(in crate::server) fn code(self) -> &'static str {
        match self {
            Self::Pending => "pending",
            Self::Confirmed => "confirmed",
            Self::Expired => "expired",
        }
    }
}

/// 一次配对尝试。确认后绑定到具体 Space 和 Computer。
#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct Pairing {
    pub(in crate::server) request: PairingRequest,
    pub(in crate::server) status: PairingStatus,
    pub(in crate::server) expires_at: OffsetDateTime,
    pub(in crate::server) computer_id: Option<ComputerId>,
    pub(in crate::server) space_id: Option<SpaceId>,
}

impl Pairing {
    /// 建立待确认配对。有效期由领域常量决定，调用方不能自定义。
    pub(in crate::server) fn open(request: PairingRequest, now: OffsetDateTime) -> Self {
        Self {
            request,
            status: PairingStatus::Pending,
            expires_at: now + PAIRING_WINDOW,
            computer_id: None,
            space_id: None,
        }
    }

    /// 过期只在读取时判定，不依赖后台任务。
    pub(in crate::server) fn has_lapsed(&self, now: OffsetDateTime) -> bool {
        self.status == PairingStatus::Pending && self.expires_at <= now
    }

    pub(in crate::server) fn lapse(&mut self) {
        if self.status == PairingStatus::Pending {
            self.status = PairingStatus::Expired;
        }
    }

    /// token 散列的可展示前缀。Human 用它核对 daemon 身份，不暴露完整散列。
    pub(in crate::server) fn token_fingerprint(&self) -> &str {
        &self.request.token_hash[..12]
    }

    /// 确认配对并绑定 Computer。仅 pending 且未过期的配对可以确认。
    pub(in crate::server) fn confirm(
        &mut self,
        computer_id: ComputerId,
        space_id: SpaceId,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.status != PairingStatus::Pending || self.expires_at <= now {
            return Err(DomainError::PairingLapsed);
        }
        self.status = PairingStatus::Confirmed;
        self.computer_id = Some(computer_id);
        self.space_id = Some(space_id);
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn request() -> PairingRequest {
        PairingRequest::new(&"a".repeat(64), "workstation", "linux", "0.1.0")
            .expect("request is valid")
    }

    #[test]
    fn pairing_request_requires_a_hex_token_hash_hostname_and_known_os() {
        assert_eq!(
            PairingRequest::new("short", "host", "linux", "0.1.0"),
            Err(DomainError::InvalidPairing)
        );
        assert_eq!(
            PairingRequest::new(&"z".repeat(64), "host", "linux", "0.1.0"),
            Err(DomainError::InvalidPairing)
        );
        assert_eq!(
            PairingRequest::new(&"a".repeat(64), "  ", "linux", "0.1.0"),
            Err(DomainError::InvalidPairing)
        );
        assert_eq!(
            PairingRequest::new(&"a".repeat(64), "host", "windows", "0.1.0"),
            Err(DomainError::InvalidPairing)
        );
        let normalized = PairingRequest::new(&"A".repeat(64), " host ", "macos", "0.1.0")
            .expect("request is valid");
        assert_eq!(normalized.token_hash, "a".repeat(64));
        assert_eq!(normalized.hostname, "host");
        assert_eq!(normalized.os, ComputerOs::MacOs);
    }

    #[test]
    fn a_pairing_confirms_once_inside_its_window() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let mut pairing = Pairing::open(request(), now);
        let computer_id = ComputerId::from_uuid(uuid::Uuid::from_u128(1));
        let space_id = SpaceId::from_uuid(uuid::Uuid::from_u128(2));
        pairing
            .confirm(computer_id, space_id, now)
            .expect("confirm succeeds inside the window");
        assert_eq!(pairing.status, PairingStatus::Confirmed);
        assert_eq!(pairing.computer_id, Some(computer_id));
        // 已确认的配对不能再次绑定另一个 Computer。
        assert_eq!(
            pairing.confirm(
                ComputerId::from_uuid(uuid::Uuid::from_u128(3)),
                space_id,
                now
            ),
            Err(DomainError::PairingLapsed)
        );
        assert_eq!(pairing.computer_id, Some(computer_id));
    }

    #[test]
    fn a_lapsed_pairing_cannot_confirm() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let mut pairing = Pairing::open(request(), now);
        let after = now + PAIRING_WINDOW;
        assert!(pairing.has_lapsed(after));
        assert_eq!(
            pairing.confirm(
                ComputerId::from_uuid(uuid::Uuid::from_u128(1)),
                SpaceId::from_uuid(uuid::Uuid::from_u128(2)),
                after
            ),
            Err(DomainError::PairingLapsed)
        );
        pairing.lapse();
        assert_eq!(pairing.status, PairingStatus::Expired);
        // 已过期后不再被 lapse 覆盖为其他状态。
        pairing.lapse();
        assert_eq!(pairing.status, PairingStatus::Expired);
    }

    #[test]
    fn the_token_fingerprint_does_not_expose_the_full_hash() {
        let pairing = Pairing::open(request(), OffsetDateTime::UNIX_EPOCH);
        let fingerprint = pairing.token_fingerprint();
        assert_eq!(fingerprint.len(), 12);
        assert!(pairing.request.token_hash.starts_with(fingerprint));
        assert_ne!(fingerprint, pairing.request.token_hash);
    }
}
