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

impl Member {
    /// 改写 Access Level。调用方的级别决定它能授予哪一档,由
    /// [`AccessLevel::can_grant`] 判定。Owner 不能通过该路径产生:
    /// Space 只有一个 Owner,由创建 Space 确定。
    pub(in crate::server) fn set_access_level(
        &mut self,
        actor: AccessLevel,
        requested: AccessLevel,
    ) -> Result<(), DomainError> {
        if !actor.can_grant(requested) {
            return Err(DomainError::GovernorRequired);
        }
        // 现任 Owner 的级别不能被改写,否则 Space 会失去治理者。
        if self.access_level == AccessLevel::Owner {
            return Err(DomainError::GovernorRequired);
        }
        self.access_level = requested;
        Ok(())
    }
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

    /// 改写 Role。每次改写推进 revision，使 Computer 能判断本地缓存是否过期。
    /// 空 Role 不成立：Role 是 Agent 的行为说明，没有它 Driver 无法启动。
    pub(in crate::server) fn revise_role(&mut self, role_text: &str) -> Result<(), DomainError> {
        if self.lifecycle == AgentLifecycle::Retired {
            return Err(DomainError::AgentRetired);
        }
        let role_text = role_text.trim();
        if role_text.is_empty() {
            return Err(DomainError::InvalidRole);
        }
        if role_text == self.role_text {
            return Ok(());
        }
        self.role_text = role_text.to_owned();
        self.role_revision += 1;
        Ok(())
    }

    /// 暂停 Agent。只有 active 可以暂停，其他状态不构成暂停请求。
    pub(in crate::server) fn suspend(&mut self) -> Result<(), DomainError> {
        if self.lifecycle == AgentLifecycle::Retired {
            return Err(DomainError::AgentRetired);
        }
        if self.lifecycle != AgentLifecycle::Active {
            return Err(DomainError::InvalidTransition);
        }
        self.lifecycle = AgentLifecycle::Suspended;
        Ok(())
    }

    /// 恢复 Agent。只有 suspended 可以恢复。
    pub(in crate::server) fn resume(&mut self) -> Result<(), DomainError> {
        if self.lifecycle == AgentLifecycle::Retired {
            return Err(DomainError::AgentRetired);
        }
        if self.lifecycle != AgentLifecycle::Suspended {
            return Err(DomainError::InvalidTransition);
        }
        self.lifecycle = AgentLifecycle::Active;
        Ok(())
    }

    /// 重试配置失败的 Agent。error 回到 provisioning 重新走配置流程。
    pub(in crate::server) fn retry_provisioning(&mut self) -> Result<(), DomainError> {
        if self.lifecycle == AgentLifecycle::Retired {
            return Err(DomainError::AgentRetired);
        }
        if self.lifecycle != AgentLifecycle::Error {
            return Err(DomainError::InvalidTransition);
        }
        self.lifecycle = AgentLifecycle::Provisioning;
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

#[cfg(test)]
mod tests {
    use super::*;

    fn agent(lifecycle: AgentLifecycle) -> Agent {
        Agent {
            member_id: MemberId::from_uuid(uuid::Uuid::from_u128(1)),
            space_id: SpaceId::from_uuid(uuid::Uuid::from_u128(2)),
            computer_id: Some(ComputerId::from_uuid(uuid::Uuid::from_u128(3))),
            role_text: "review boundaries".into(),
            role_revision: 4,
            lifecycle,
            driver_kind: DriverKind::Codex,
            retired_at: None,
        }
    }

    fn member(access_level: AccessLevel) -> Member {
        Member {
            id: MemberId::from_uuid(uuid::Uuid::from_u128(5)),
            space_id: SpaceId::from_uuid(uuid::Uuid::from_u128(2)),
            display_name: "Member".into(),
            handle: "member".into(),
            access_level,
            created_at: OffsetDateTime::UNIX_EPOCH,
        }
    }

    #[test]
    fn revising_a_role_advances_the_revision_only_when_the_text_changes() {
        let mut subject = agent(AgentLifecycle::Active);
        subject
            .revise_role("  review boundaries  ")
            .expect("the same text is accepted");
        // 相同 Role 不推进 revision，Computer 无需重新拉取配置。
        assert_eq!(subject.role_revision, 4);
        subject.revise_role("write tests").expect("new text");
        assert_eq!(subject.role_revision, 5);
        assert_eq!(subject.role_text, "write tests");
        assert_eq!(
            subject.revise_role("   "),
            Err(DomainError::InvalidRole),
            "空 Role 不成立"
        );
    }

    #[test]
    fn lifecycle_actions_only_apply_from_their_source_state() {
        let mut active = agent(AgentLifecycle::Active);
        active.suspend().expect("active suspends");
        assert_eq!(active.lifecycle, AgentLifecycle::Suspended);
        assert_eq!(active.suspend(), Err(DomainError::InvalidTransition));
        active.resume().expect("suspended resumes");
        assert_eq!(active.lifecycle, AgentLifecycle::Active);
        assert_eq!(active.resume(), Err(DomainError::InvalidTransition));

        let mut failed = agent(AgentLifecycle::Error);
        failed.retry_provisioning().expect("error retries");
        assert_eq!(failed.lifecycle, AgentLifecycle::Provisioning);
        assert_eq!(
            failed.retry_provisioning(),
            Err(DomainError::InvalidTransition)
        );
    }

    #[test]
    fn a_retired_agent_rejects_every_change() {
        let mut retired = agent(AgentLifecycle::Retired);
        assert_eq!(retired.revise_role("new"), Err(DomainError::AgentRetired));
        assert_eq!(retired.suspend(), Err(DomainError::AgentRetired));
        assert_eq!(retired.resume(), Err(DomainError::AgentRetired));
        assert_eq!(retired.retry_provisioning(), Err(DomainError::AgentRetired));
    }

    #[test]
    fn access_level_changes_respect_the_actor_and_protect_the_owner() {
        let mut target = member(AccessLevel::Member);
        // Admin 不能授予 Admin。
        assert_eq!(
            target.set_access_level(AccessLevel::Admin, AccessLevel::Admin),
            Err(DomainError::GovernorRequired)
        );
        target
            .set_access_level(AccessLevel::Owner, AccessLevel::Admin)
            .expect("owner promotes to admin");
        assert_eq!(target.access_level, AccessLevel::Admin);
        // Owner 不能通过该路径授予。
        assert_eq!(
            target.set_access_level(AccessLevel::Owner, AccessLevel::Owner),
            Err(DomainError::GovernorRequired)
        );
        // 现任 Owner 的级别不可改写，否则 Space 会失去治理者。
        let mut owner = member(AccessLevel::Owner);
        assert_eq!(
            owner.set_access_level(AccessLevel::Owner, AccessLevel::Member),
            Err(DomainError::GovernorRequired)
        );
        assert_eq!(owner.access_level, AccessLevel::Owner);
    }
}
