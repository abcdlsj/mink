use time::OffsetDateTime;

use crate::ids::{ChannelId, ComputerId, IdempotencyKey, MemberId, SpaceId};

use crate::server::domain::{
    access::{HumanRegistration, SessionLifetime, SpaceAccess},
    attention::{InboxItemDisposition, InboxItemStatus},
    identity::{
        AccessLevel, Agent, AgentLifecycle, Computer, ComputerLifecycle, Member, PermissionAction,
    },
};

use super::ports::{
    ApplicationError, AuthenticatedHuman, CreatedSpace, Effect, OpenedSession, PasswordPort,
    RawSessionToken, ServerTransaction, SessionTokenPort, TransactionPort,
};

/// 注册 Human 并立即建立 Browser Session。账号与 Session 在同一事务内成立。
pub(in crate::server) struct RegisterHuman;

pub(in crate::server) struct RegisterHumanInput<'a> {
    pub(in crate::server) user_id: uuid::Uuid,
    pub(in crate::server) session_id: uuid::Uuid,
    pub(in crate::server) display_name: &'a str,
    pub(in crate::server) email: &'a str,
    pub(in crate::server) password: &'a str,
    pub(in crate::server) lifetime: SessionLifetime,
    pub(in crate::server) now: OffsetDateTime,
}

impl RegisterHuman {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        passwords: &impl PasswordPort,
        tokens: &impl SessionTokenPort,
        input: RegisterHumanInput<'_>,
    ) -> Result<OpenedSession, ApplicationError> {
        let registration = HumanRegistration::new(
            input.display_name,
            input.email,
            input.password.chars().count(),
        )?;
        let password_hash = passwords.hash(input.password)?;
        let token = tokens.generate();
        port.transact(async |transaction| {
            transaction
                .insert_human(input.user_id, &registration, &password_hash, input.now)
                .await?;
            open_session(
                transaction,
                input.session_id,
                input.user_id,
                &token,
                input.lifetime,
                input.now,
            )
            .await?;
            Ok(OpenedSession {
                human: AuthenticatedHuman {
                    user_id: input.user_id,
                    display_name: registration.display_name.clone(),
                    email_normalized: registration.email_normalized.clone(),
                },
                token: token.clone(),
            })
        })
        .await
    }
}

/// 用 email 和密码建立 Browser Session。账号缺失与密码错误返回同一个错误。
pub(in crate::server) struct AuthenticateHuman;

pub(in crate::server) struct AuthenticateHumanInput<'a> {
    pub(in crate::server) session_id: uuid::Uuid,
    pub(in crate::server) email: &'a str,
    pub(in crate::server) password: &'a str,
    pub(in crate::server) lifetime: SessionLifetime,
    pub(in crate::server) now: OffsetDateTime,
}

impl AuthenticateHuman {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        passwords: &impl PasswordPort,
        tokens: &impl SessionTokenPort,
        input: AuthenticateHumanInput<'_>,
    ) -> Result<OpenedSession, ApplicationError> {
        let email_normalized = input.email.trim().to_lowercase();
        let token = tokens.generate();
        port.transact(async |transaction| {
            let credential = transaction.human_credential(&email_normalized).await?;
            // 账号不存在时同样执行到统一的失败分支，不向调用方区分两种原因。
            let Some((human, stored_hash)) = credential else {
                return Err(ApplicationError::Unauthenticated);
            };
            if !passwords.verify(input.password, &stored_hash) {
                return Err(ApplicationError::Unauthenticated);
            }
            open_session(
                transaction,
                input.session_id,
                human.user_id,
                &token,
                input.lifetime,
                input.now,
            )
            .await?;
            Ok(OpenedSession {
                human,
                token: token.clone(),
            })
        })
        .await
    }
}

async fn open_session<T: ServerTransaction>(
    transaction: &mut T,
    session_id: uuid::Uuid,
    user_id: uuid::Uuid,
    token: &RawSessionToken,
    lifetime: SessionLifetime,
    now: OffsetDateTime,
) -> Result<(), ApplicationError> {
    transaction
        .insert_browser_session(
            session_id,
            user_id,
            &token.sha256_hash(),
            lifetime.expires_at(now),
            now,
        )
        .await
}

/// 校验 Browser Session token 并返回账号事实。
pub(in crate::server) struct AuthenticateSession;

impl AuthenticateSession {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        token: &RawSessionToken,
        now: OffsetDateTime,
    ) -> Result<AuthenticatedHuman, ApplicationError> {
        let token_hash = token.sha256_hash();
        port.transact(async |transaction| {
            transaction
                .human_for_session(&token_hash, now)
                .await?
                .ok_or(ApplicationError::Unauthenticated)
        })
        .await
    }
}

/// 注销 Browser Session。重复注销成立。
pub(in crate::server) struct CloseSession;

impl CloseSession {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        token: &RawSessionToken,
    ) -> Result<(), ApplicationError> {
        let token_hash = token.sha256_hash();
        port.transact(async |transaction| transaction.delete_browser_session(&token_hash).await)
            .await
    }
}

/// 把 Browser Session 解析为某个 Space 的 Member 身份。
pub(in crate::server) struct AuthorizeSpaceAccess;

impl AuthorizeSpaceAccess {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        token: &RawSessionToken,
        space_id: SpaceId,
        now: OffsetDateTime,
    ) -> Result<SpaceAccess, ApplicationError> {
        let token_hash = token.sha256_hash();
        port.transact(async |transaction| {
            let human = transaction
                .human_for_session(&token_hash, now)
                .await?
                .ok_or(ApplicationError::Unauthenticated)?;
            transaction
                .space_access(human.user_id, space_id)
                .await?
                // 非成员不区分“Space 不存在”和“无权访问”。
                .ok_or(ApplicationError::NotFound)
        })
        .await
    }
}

/// 把 Browser Session 解析为某个 Channel 的 Member 身份。要求 Channel membership。
pub(in crate::server) struct AuthorizeChannelAccess;

impl AuthorizeChannelAccess {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        token: &RawSessionToken,
        channel_id: ChannelId,
        now: OffsetDateTime,
    ) -> Result<MemberId, ApplicationError> {
        let token_hash = token.sha256_hash();
        port.transact(async |transaction| {
            let human = transaction
                .human_for_session(&token_hash, now)
                .await?
                .ok_or(ApplicationError::Unauthenticated)?;
            transaction
                .channel_access(human.user_id, channel_id)
                .await?
                .ok_or(ApplicationError::NotFound)
        })
        .await
    }
}

/// 把 Browser Session 解析为 Attachment 所属 Space 的 Member 身份。
pub(in crate::server) struct AuthorizeAttachmentAccess;

impl AuthorizeAttachmentAccess {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        token: &RawSessionToken,
        attachment_id: crate::ids::AttachmentId,
        now: OffsetDateTime,
    ) -> Result<SpaceAccess, ApplicationError> {
        let token_hash = token.sha256_hash();
        port.transact(async |transaction| {
            let human = transaction
                .human_for_session(&token_hash, now)
                .await?
                .ok_or(ApplicationError::Unauthenticated)?;
            let space_id = transaction
                .space_of_attachment(attachment_id)
                .await?
                .ok_or(ApplicationError::NotFound)?;
            transaction
                .space_access(human.user_id, space_id)
                .await?
                .ok_or(ApplicationError::NotFound)
        })
        .await
    }
}

/// 读取 Agent 相关事实的授权。只要求同 Space Member 身份，不要求治理级别。
pub(in crate::server) struct AuthorizeAgentAccess;

impl AuthorizeAgentAccess {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        token: &RawSessionToken,
        agent_id: MemberId,
        now: OffsetDateTime,
    ) -> Result<SpaceAccess, ApplicationError> {
        let token_hash = token.sha256_hash();
        port.transact(async |transaction| {
            let human = transaction
                .human_for_session(&token_hash, now)
                .await?
                .ok_or(ApplicationError::Unauthenticated)?;
            let space_id = transaction
                .space_of_agent(agent_id)
                .await?
                .ok_or(ApplicationError::NotFound)?;
            transaction
                .space_access(human.user_id, space_id)
                .await?
                .ok_or(ApplicationError::NotFound)
        })
        .await
    }
}

/// Agent 或 Computer 级请求的治理授权。资源所属 Space 由 Server 推导。
/// 把 Browser Session 解析为某个 Space 的治理身份。要求 Owner 或 Admin。
pub(in crate::server) struct AuthorizeSpaceGovernance;

impl AuthorizeSpaceGovernance {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        token: &RawSessionToken,
        space_id: SpaceId,
        now: OffsetDateTime,
    ) -> Result<SpaceAccess, ApplicationError> {
        let token_hash = token.sha256_hash();
        port.transact(async |transaction| {
            let human = transaction
                .human_for_session(&token_hash, now)
                .await?
                .ok_or(ApplicationError::Unauthenticated)?;
            let access = transaction
                .space_access(human.user_id, space_id)
                .await?
                // 非成员不区分“Space 不存在”和“无权访问”。
                .ok_or(ApplicationError::NotFound)?;
            access.require_governor()?;
            Ok(access)
        })
        .await
    }
}

pub(in crate::server) struct AuthorizeAgentGovernance;

impl AuthorizeAgentGovernance {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        token: &RawSessionToken,
        agent_id: MemberId,
        now: OffsetDateTime,
    ) -> Result<SpaceAccess, ApplicationError> {
        let token_hash = token.sha256_hash();
        port.transact(async |transaction| {
            let human = transaction
                .human_for_session(&token_hash, now)
                .await?
                .ok_or(ApplicationError::Unauthenticated)?;
            let space_id = transaction
                .space_of_agent(agent_id)
                .await?
                .ok_or(ApplicationError::NotFound)?;
            let access = transaction
                .space_access(human.user_id, space_id)
                .await?
                .ok_or(ApplicationError::NotFound)?;
            access.require_governor()?;
            Ok(access)
        })
        .await
    }
}

pub(in crate::server) struct AuthorizeComputerGovernance;

impl AuthorizeComputerGovernance {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        token: &RawSessionToken,
        computer_id: ComputerId,
        now: OffsetDateTime,
    ) -> Result<SpaceAccess, ApplicationError> {
        let token_hash = token.sha256_hash();
        port.transact(async |transaction| {
            let human = transaction
                .human_for_session(&token_hash, now)
                .await?
                .ok_or(ApplicationError::Unauthenticated)?;
            let space_id = transaction
                .space_of_computer(computer_id)
                .await?
                .ok_or(ApplicationError::NotFound)?;
            let access = transaction
                .space_access(human.user_id, space_id)
                .await?
                .ok_or(ApplicationError::NotFound)?;
            access.require_governor()?;
            Ok(access)
        })
        .await
    }
}

pub(in crate::server) struct CreateSpace;

pub(in crate::server) struct CreateSpaceInput<'a> {
    pub(in crate::server) actor_user_id: uuid::Uuid,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) owner_id: MemberId,
    pub(in crate::server) general_channel_id: ChannelId,
    pub(in crate::server) name: &'a str,
    pub(in crate::server) slug: &'a str,
    pub(in crate::server) owner_handle: &'a str,
    pub(in crate::server) owner_display_name: &'a str,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) now: OffsetDateTime,
}

impl CreateSpace {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: CreateSpaceInput<'_>,
    ) -> Result<CreatedSpace, ApplicationError> {
        if input.name.trim().is_empty() || input.slug.trim().is_empty() {
            return Err(ApplicationError::Conflict);
        }
        port.transact(async |transaction| {
            transaction
                .create_space(
                    input.actor_user_id,
                    input.space_id,
                    input.owner_id,
                    input.general_channel_id,
                    input.name,
                    input.slug,
                    input.owner_handle,
                    input.owner_display_name,
                    input.idempotency_key,
                    input.now,
                )
                .await
        })
        .await
    }
}

pub(in crate::server) struct SetPermission;

impl SetPermission {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        actor_id: MemberId,
        target_id: MemberId,
        action: PermissionAction,
        enabled: bool,
        idempotency_key: IdempotencyKey,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        let operation = if enabled {
            "permission.grant"
        } else {
            "permission.revoke"
        };
        port.transact(async |transaction| {
            if transaction
                .resource_for_idempotency(actor_id, operation, idempotency_key)
                .await?
                .is_some()
            {
                return Ok(());
            }
            if !transaction
                .can_manage_permissions(actor_id, target_id)
                .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            if enabled {
                transaction
                    .grant_permission(target_id, action, actor_id, now)
                    .await?;
            } else {
                transaction.revoke_permission(target_id, action).await?;
            }
            transaction
                .record_resource_idempotency(
                    actor_id,
                    operation,
                    idempotency_key,
                    target_id.into_uuid(),
                )
                .await?;
            transaction.emit(Effect::PermissionChanged(target_id));
            Ok(())
        })
        .await
    }
}

/// Agent 的生命周期动作。与协议的 suspend mode 分离:mode 决定 Computer 如何
/// 停止当前 Run,这里只决定 Server 侧的目标状态。
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum AgentLifecycleAction {
    Suspend { cancel_current_run: bool },
    Resume,
    RetryProvisioning,
}

/// 改写 Agent 的 Role 或生命周期。两者都是治理动作。
pub(in crate::server) struct UpdateAgent;

pub(in crate::server) struct UpdateAgentInput<'a> {
    pub(in crate::server) actor_id: MemberId,
    pub(in crate::server) agent_id: MemberId,
    pub(in crate::server) role_text: Option<&'a str>,
    pub(in crate::server) lifecycle: Option<AgentLifecycleAction>,
}

impl UpdateAgent {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: UpdateAgentInput<'_>,
    ) -> Result<Agent, ApplicationError> {
        port.transact(async |transaction| {
            let mut agent = transaction.agent(input.agent_id).await?;
            let access = transaction
                .member_access_level(input.actor_id, agent.space_id)
                .await?;
            if !access.can_manage_space() {
                return Err(ApplicationError::PermissionDenied);
            }
            let revision_before = agent.role_revision;
            if let Some(role_text) = input.role_text {
                agent.revise_role(role_text)?;
            }
            if let Some(action) = input.lifecycle {
                match action {
                    AgentLifecycleAction::Suspend { cancel_current_run } => {
                        agent.suspend()?;
                        transaction
                            .queue_agent_suspend(
                                input.agent_id,
                                agent.computer_id,
                                cancel_current_run,
                            )
                            .await?;
                    }
                    AgentLifecycleAction::Resume => agent.resume()?,
                    AgentLifecycleAction::RetryProvisioning => agent.retry_provisioning()?,
                }
            }
            transaction.save_agent(agent.clone()).await?;
            // Role 改写后 Computer 的本地快照过期，必须重新下发配置。
            if agent.role_revision != revision_before {
                transaction.queue_agent_configuration(&agent).await?;
            }
            transaction.emit(Effect::AgentUpdated(agent.member_id));
            Ok(agent)
        })
        .await
    }
}

/// 改写 Member 的 Access Level。Owner 的级别不可改写。
pub(in crate::server) struct UpdateMemberAccess;

impl UpdateMemberAccess {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        actor_id: MemberId,
        target_id: MemberId,
        space_id: SpaceId,
        requested: AccessLevel,
    ) -> Result<Member, ApplicationError> {
        port.transact(async |transaction| {
            let actor_access = transaction.member_access_level(actor_id, space_id).await?;
            let mut target = transaction.member(target_id).await?;
            // 跨 Space 的 Member 不区分「不存在」和「无权访问」。
            if target.space_id != space_id {
                return Err(ApplicationError::NotFound);
            }
            target.set_access_level(actor_access, requested)?;
            transaction.save_member(target.clone()).await?;
            transaction.emit(Effect::PermissionChanged(target.id));
            Ok(target)
        })
        .await
    }
}

pub(in crate::server) struct RetireAgent;

impl RetireAgent {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        actor_id: MemberId,
        agent_id: MemberId,
        idempotency_key: IdempotencyKey,
        now: OffsetDateTime,
    ) -> Result<Agent, ApplicationError> {
        port.transact(async |transaction| {
            if let Some(agent_id) = transaction
                .resource_for_idempotency(actor_id, "agent.retire", idempotency_key)
                .await?
            {
                return transaction.agent(MemberId::from_uuid(agent_id)).await;
            }
            let mut agent = transaction.agent(agent_id).await?;
            let computer_id = agent.computer_id;
            if agent.lifecycle != AgentLifecycle::Retired {
                let assigned_computer = computer_id.ok_or(ApplicationError::Conflict)?;
                if let Some(run_id) = transaction.active_run_for_agent(agent_id).await? {
                    let mut run = transaction.run(run_id).await?;
                    for run_item in run.items.clone() {
                        let disposition = run_item
                            .disposition
                            .unwrap_or(InboxItemDisposition::Released);
                        let mut item = transaction.inbox_item(run_item.inbox_item_id).await?;
                        if item.status == InboxItemStatus::Leased {
                            item.apply_disposition(run.id, disposition, now)?;
                            transaction.save_inbox_item(item).await?;
                        } else if disposition != InboxItemDisposition::Released {
                            return Err(ApplicationError::Conflict);
                        }
                    }
                    run.cancel_for_agent_retirement(now);
                    transaction.save_run(run.clone()).await?;
                    transaction.emit(Effect::RunCompleted(run.id));
                }
                agent.retire(now)?;
                transaction.save_agent(agent.clone()).await?;
                transaction.emit(Effect::AgentRetired {
                    agent_id,
                    computer_id: assigned_computer,
                });
            }
            transaction
                .record_resource_idempotency(
                    actor_id,
                    "agent.retire",
                    idempotency_key,
                    agent_id.into_uuid(),
                )
                .await?;
            Ok(agent)
        })
        .await
    }
}

pub(in crate::server) struct DeleteComputer;

impl DeleteComputer {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        actor_id: MemberId,
        computer_id: ComputerId,
        idempotency_key: IdempotencyKey,
        now: OffsetDateTime,
    ) -> Result<Computer, ApplicationError> {
        port.transact(async |transaction| {
            if let Some(computer_id) = transaction
                .resource_for_idempotency(actor_id, "computer.delete", idempotency_key)
                .await?
            {
                return transaction
                    .computer(ComputerId::from_uuid(computer_id))
                    .await;
            }
            let mut computer = transaction.computer(computer_id).await?;
            if computer.lifecycle != ComputerLifecycle::Deleted {
                let assigned = transaction
                    .computer_has_assigned_agents(computer_id)
                    .await?;
                computer.delete(assigned, now)?;
                transaction.save_computer(computer.clone()).await?;
                transaction.emit(Effect::ComputerDeleted(computer_id));
            }
            transaction
                .record_resource_idempotency(
                    actor_id,
                    "computer.delete",
                    idempotency_key,
                    computer_id.into_uuid(),
                )
                .await?;
            Ok(computer)
        })
        .await
    }
}
