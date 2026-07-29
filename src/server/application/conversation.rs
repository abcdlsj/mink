use std::collections::BTreeSet;

use time::OffsetDateTime;

use crate::ids::{ChannelId, ComputerId, MemberId, MessageId, RunId};
use crate::server::domain::{
    conversation::{Channel, ChannelKind, Message, MessageContent},
    execution::RunStatus,
    identity::{Agent, AgentLifecycle, DriverKind, Member, PermissionAction},
};

use super::ports::{ApplicationError, Effect, ServerTransaction, TransactionPort};

pub(in crate::server) struct CreateChannelActionInput {
    pub(in crate::server) channel_id: ChannelId,
    pub(in crate::server) audience: BTreeSet<MemberId>,
    pub(in crate::server) kind: ChannelKind,
    pub(in crate::server) slug: Option<String>,
    pub(in crate::server) topic: Option<String>,
    pub(in crate::server) action_message_id: MessageId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) current_run_id: RunId,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct CreateAgentActionInput {
    pub(in crate::server) agent_member_id: MemberId,
    pub(in crate::server) display_name: String,
    pub(in crate::server) handle: String,
    pub(in crate::server) role_text: String,
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) driver_kind: DriverKind,
    pub(in crate::server) action_message_id: MessageId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) current_run_id: RunId,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct CreateAgentAction;

impl CreateAgentAction {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: CreateAgentActionInput,
    ) -> Result<Agent, ApplicationError> {
        port.transact(async |transaction| {
            let run = transaction.run(input.current_run_id).await?;
            if run.agent_id != input.actor_member_id || run.status != RunStatus::Running {
                return Err(ApplicationError::ContextChanged);
            }
            if !transaction
                .has_permission(input.actor_member_id, PermissionAction::AgentCreate)
                .await?
                || !transaction
                    .can_read_thread(input.actor_member_id, run.focus_thread_id)
                    .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            let member = Member {
                id: input.agent_member_id,
                space_id: run.space_id,
                display_name: input.display_name,
                handle: input.handle,
                created_at: input.now,
            };
            let agent = Agent {
                member_id: member.id,
                space_id: member.space_id,
                computer_id: Some(input.computer_id),
                role_text: input.role_text,
                role_revision: 1,
                lifecycle: AgentLifecycle::Provisioning,
                driver_kind: input.driver_kind,
                retired_at: None,
            };
            let action = Message::action_reply(
                input.action_message_id,
                run.focus_thread_id,
                input.actor_member_id,
                MessageContent::AgentCreated(agent.member_id),
                input.now,
            )?;
            transaction.insert_agent(member, agent.clone()).await?;
            transaction.insert_message(action).await?;
            transaction.emit(Effect::AgentCreated {
                agent_id: agent.member_id,
                computer_id: input.computer_id,
            });
            Ok(agent)
        })
        .await
    }
}

pub(in crate::server) struct CreateChannelAction;

impl CreateChannelAction {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: CreateChannelActionInput,
    ) -> Result<Channel, ApplicationError> {
        port.transact(async |transaction| {
            let run = transaction.run(input.current_run_id).await?;
            if run.agent_id != input.actor_member_id || run.status != RunStatus::Running {
                return Err(ApplicationError::ContextChanged);
            }
            if !transaction
                .has_permission(input.actor_member_id, PermissionAction::ChannelCreate)
                .await?
                || !transaction
                    .can_read_thread(input.actor_member_id, run.focus_thread_id)
                    .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            let channel = Channel::create(
                input.channel_id,
                run.space_id,
                input.audience,
                input.kind,
                input.slug,
                input.topic,
                input.now,
            )?;
            let action = Message::action_reply(
                input.action_message_id,
                run.focus_thread_id,
                input.actor_member_id,
                MessageContent::ChannelCreated(channel.id),
                input.now,
            )?;
            transaction.insert_channel(channel.clone()).await?;
            transaction.insert_message(action).await?;
            transaction.emit(Effect::ChannelCreated(channel.id));
            Ok(channel)
        })
        .await
    }
}
