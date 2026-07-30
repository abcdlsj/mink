use time::OffsetDateTime;

use crate::ids::{ComputerId, MemberId, SpaceId};

use super::DomainError;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum DriverKind {
    Codex,
    Builtin,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct Member {
    pub(in crate::server) id: MemberId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) display_name: String,
    pub(in crate::server) handle: String,
    pub(in crate::server) access_level: AccessLevel,
    pub(in crate::server) created_at: OffsetDateTime,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum AccessLevel {
    Owner,
    Admin,
    Member,
}

impl AccessLevel {
    pub(in crate::server) fn can_manage_space(self) -> bool {
        matches!(self, Self::Owner | Self::Admin)
    }

    pub(in crate::server) fn can_grant(self, requested: Self) -> bool {
        match requested {
            Self::Owner => false,
            Self::Admin => self == Self::Owner,
            Self::Member => self.can_manage_space(),
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub(in crate::server) enum PermissionAction {
    ChannelCreate,
    AgentCreate,
}

impl PermissionAction {
    pub(in crate::server) fn code(self) -> &'static str {
        match self {
            Self::ChannelCreate => "channel.create",
            Self::AgentCreate => "agent.create",
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum AgentLifecycle {
    Provisioning,
    Active,
    Suspended,
    Retired,
    Error,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct Agent {
    pub(in crate::server) member_id: MemberId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) computer_id: Option<ComputerId>,
    pub(in crate::server) role_text: String,
    pub(in crate::server) role_revision: u64,
    pub(in crate::server) lifecycle: AgentLifecycle,
    pub(in crate::server) driver_kind: DriverKind,
    pub(in crate::server) retired_at: Option<OffsetDateTime>,
}

impl Agent {
    pub(in crate::server) fn retire(&mut self, now: OffsetDateTime) -> Result<(), DomainError> {
        if self.lifecycle == AgentLifecycle::Retired {
            return Err(DomainError::AgentRetired);
        }
        self.lifecycle = AgentLifecycle::Retired;
        self.computer_id = None;
        self.retired_at = Some(now);
        Ok(())
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum ComputerLifecycle {
    Online,
    Offline,
    Deleted,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct Computer {
    pub(in crate::server) id: ComputerId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) lifecycle: ComputerLifecycle,
    pub(in crate::server) token_hash: Option<String>,
    pub(in crate::server) deleted_at: Option<OffsetDateTime>,
}

impl Computer {
    pub(in crate::server) fn delete(
        &mut self,
        has_assigned_agents: bool,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if has_assigned_agents {
            return Err(DomainError::ComputerHasAgents);
        }
        if self.lifecycle == ComputerLifecycle::Deleted {
            return Err(DomainError::InvalidTransition);
        }
        self.lifecycle = ComputerLifecycle::Deleted;
        self.token_hash = None;
        self.deleted_at = Some(now);
        Ok(())
    }
}
