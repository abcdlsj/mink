use std::collections::BTreeSet;

use time::OffsetDateTime;

use crate::ids::{
    ChannelId, ComputerId, IdempotencyKey, MemberId, MessageId, RunId, SpaceId, ThreadId,
};
use crate::server::domain::{
    DomainError,
    conversation::{Channel, ChannelKind, Message, MessageContent},
    execution::RunStatus,
    identity::{AccessLevel, Agent, AgentLifecycle, DriverKind, Member, PermissionAction},
};

use super::ports::{
    ApplicationError, AttachmentTransaction, CollaborationTransaction, DirectMessageView, Effect,
    EffectSink, ExecutionTransaction, IdentityTransaction, MessageDraft, PublishedMessage,
    TransactionPort,
};

pub(in crate::server) struct ArchiveChannel;

impl ArchiveChannel {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        actor_member_id: MemberId,
        channel_id: ChannelId,
        now: OffsetDateTime,
    ) -> Result<Channel, ApplicationError> {
        port.transact(async |transaction| {
            let mut channel = transaction.channel(channel_id).await?;
            let access = transaction
                .member_access_level(actor_member_id, channel.space_id)
                .await?;
            if !access.can_manage_space() {
                return Err(ApplicationError::PermissionDenied);
            }
            channel.archive(now)?;
            transaction.save_channel(channel.clone()).await?;
            transaction.emit(Effect::ChannelUpdated(channel.id));
            Ok(channel)
        })
        .await
    }
}

pub(in crate::server) struct JoinChannel;

impl JoinChannel {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        actor_member_id: MemberId,
        channel_id: ChannelId,
    ) -> Result<Channel, ApplicationError> {
        port.transact(async |transaction| {
            let mut channel = transaction.channel(channel_id).await?;
            transaction
                .member_access_level(actor_member_id, channel.space_id)
                .await?;
            channel.admit(actor_member_id)?;
            transaction.save_channel(channel.clone()).await?;
            transaction.emit(Effect::ChannelUpdated(channel.id));
            Ok(channel)
        })
        .await
    }
}

pub(in crate::server) struct SetThreadSubscription;

impl SetThreadSubscription {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        actor_member_id: MemberId,
        thread_id: ThreadId,
        following: bool,
        now: OffsetDateTime,
    ) -> Result<bool, ApplicationError> {
        port.transact(async |transaction| {
            if !transaction
                .can_read_thread(actor_member_id, thread_id)
                .await?
            {
                return Err(ApplicationError::NotFound);
            }
            transaction
                .set_thread_subscription(thread_id, actor_member_id, following, now)
                .await?;
            Ok(following)
        })
        .await
    }
}

pub(in crate::server) struct ListDirectMessages;

impl ListDirectMessages {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        member_id: MemberId,
        space_id: SpaceId,
    ) -> Result<Vec<DirectMessageView>, ApplicationError> {
        port.transact(async |transaction| {
            transaction
                .direct_messages_for_member(member_id, space_id)
                .await
        })
        .await
    }
}

pub(in crate::server) struct OpenDirectMessage;

pub(in crate::server) struct OpenDirectMessageInput {
    pub(in crate::server) channel_id: ChannelId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) other_member_id: MemberId,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct OpenedDirectMessage {
    pub(in crate::server) view: DirectMessageView,
    pub(in crate::server) created: bool,
}

impl OpenDirectMessage {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: OpenDirectMessageInput,
    ) -> Result<OpenedDirectMessage, ApplicationError> {
        if input.actor_member_id == input.other_member_id {
            return Err(ApplicationError::Conflict);
        }
        port.transact(async |transaction| {
            let other = transaction
                .space_member(input.other_member_id, input.space_id)
                .await?
                .ok_or(ApplicationError::NotFound)?;
            if let Some(existing) = transaction
                .direct_message_between(
                    input.space_id,
                    input.actor_member_id,
                    input.other_member_id,
                )
                .await?
            {
                return Ok(OpenedDirectMessage {
                    view: existing,
                    created: false,
                });
            }
            let channel = Channel::create(
                input.channel_id,
                input.space_id,
                [input.actor_member_id, input.other_member_id]
                    .into_iter()
                    .collect(),
                ChannelKind::Direct,
                None,
                None,
                input.now,
            )?;
            transaction.insert_channel(channel.clone()).await?;
            transaction.emit(Effect::ChannelCreated(channel.id));
            Ok(OpenedDirectMessage {
                view: DirectMessageView {
                    channel_id: channel.id,
                    space_id: channel.space_id,
                    other_member: other,
                    created_at: channel.created_at,
                },
                created: true,
            })
        })
        .await
    }
}

pub(in crate::server) struct PublishMessage;

impl PublishMessage {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        draft: MessageDraft,
    ) -> Result<PublishedMessage, ApplicationError> {
        if draft.body_markdown.trim().is_empty() {
            return Err(ApplicationError::Conflict);
        }
        port.transact(async |transaction| {
            if let Some(message_id) = transaction
                .resource_for_idempotency(
                    draft.author_member_id,
                    "message.create",
                    draft.idempotency_key,
                )
                .await?
            {
                return Ok(PublishedMessage {
                    message_id: MessageId::from_uuid(message_id),
                    hard_item_ids: Vec::new(),
                    notified_member_ids: Vec::new(),
                });
            }
            if !draft.attachment_ids.is_empty() {
                let channel = transaction.channel(draft.channel_id).await?;
                for attachment_id in &draft.attachment_ids {
                    let attachment = transaction
                        .attachment(*attachment_id)
                        .await?
                        .ok_or(ApplicationError::NotFound)?;
                    if attachment.view().space_id != channel.space_id {
                        return Err(ApplicationError::NotFound);
                    }
                    attachment
                        .require_ready()
                        .map_err(|_| ApplicationError::Domain(DomainError::AttachmentNotReady))?;
                }
            }
            let actor = draft.author_member_id;
            let key = draft.idempotency_key;
            // A Root Message creates its Thread, so only a reply changes an existing one.
            let replied_thread_id = draft.thread_id;
            let published = transaction.publish_message(draft).await?;
            transaction
                .record_resource_idempotency(
                    actor,
                    "message.create",
                    key,
                    published.message_id.into_uuid(),
                )
                .await?;
            transaction.emit(Effect::MessageCreated(published.message_id));
            if let Some(thread_id) = replied_thread_id {
                transaction.emit(Effect::ThreadUpdated(thread_id));
            }
            for member_id in &published.notified_member_ids {
                transaction.emit(Effect::InboxChanged(*member_id));
            }
            Ok(published)
        })
        .await
    }
}

pub(in crate::server) struct EditMessageInput {
    pub(in crate::server) message_id: MessageId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) body_markdown: String,
    pub(in crate::server) mentions: Vec<MemberId>,
    pub(in crate::server) mention_all: bool,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct EditMessage;

impl EditMessage {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: EditMessageInput,
    ) -> Result<Message, ApplicationError> {
        port.transact(async |transaction| {
            if let Some(message_id) = transaction
                .resource_for_idempotency(
                    input.actor_member_id,
                    "message.edit",
                    input.idempotency_key,
                )
                .await?
            {
                return transaction.message(MessageId::from_uuid(message_id)).await;
            }
            let mut message = transaction.message(input.message_id).await?;
            let thread = transaction.thread(message.thread_id).await?;
            let access = transaction
                .member_access_level(input.actor_member_id, thread.space_id)
                .await?;
            if input.actor_member_id != message.author_member_id && !access.can_manage_space() {
                return Err(ApplicationError::PermissionDenied);
            }
            message.edit_text(input.body_markdown, input.now)?;
            transaction.save_message(message.clone()).await?;
            transaction
                .save_message_mentions(message.id, input.mentions, input.mention_all, input.now)
                .await?;
            transaction
                .record_resource_idempotency(
                    input.actor_member_id,
                    "message.edit",
                    input.idempotency_key,
                    message.id.into_uuid(),
                )
                .await?;
            transaction.emit(Effect::MessageUpdated(message.id));
            Ok(message)
        })
        .await
    }
}

pub(in crate::server) struct DeleteMessage;

impl DeleteMessage {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        message_id: MessageId,
        actor_member_id: MemberId,
        idempotency_key: IdempotencyKey,
        now: OffsetDateTime,
    ) -> Result<Message, ApplicationError> {
        port.transact(async |transaction| {
            if let Some(message_id) = transaction
                .resource_for_idempotency(actor_member_id, "message.delete", idempotency_key)
                .await?
            {
                return transaction.message(MessageId::from_uuid(message_id)).await;
            }
            let mut message = transaction.message(message_id).await?;
            let thread = transaction.thread(message.thread_id).await?;
            let access = transaction
                .member_access_level(actor_member_id, thread.space_id)
                .await?;
            if actor_member_id != message.author_member_id && !access.can_manage_space() {
                return Err(ApplicationError::PermissionDenied);
            }
            message.soft_delete(now)?;
            transaction.save_message(message.clone()).await?;
            transaction
                .record_resource_idempotency(
                    actor_member_id,
                    "message.delete",
                    idempotency_key,
                    message.id.into_uuid(),
                )
                .await?;
            transaction.emit(Effect::MessageDeleted(message.id));
            Ok(message)
        })
        .await
    }
}

pub(in crate::server) struct CreateChannelActionInput {
    pub(in crate::server) channel_id: ChannelId,
    pub(in crate::server) audience: BTreeSet<MemberId>,
    pub(in crate::server) kind: ChannelKind,
    pub(in crate::server) slug: Option<String>,
    pub(in crate::server) topic: Option<String>,
    pub(in crate::server) action_message_id: MessageId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) current_run_id: RunId,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct CreateAgentActionInput {
    pub(in crate::server) agent_member_id: MemberId,
    pub(in crate::server) display_name: String,
    pub(in crate::server) role_text: String,
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) driver_kind: DriverKind,
    pub(in crate::server) action_message_id: MessageId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) current_run_id: RunId,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct CreateChannelInput {
    pub(in crate::server) channel_id: ChannelId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) audience: BTreeSet<MemberId>,
    pub(in crate::server) kind: ChannelKind,
    pub(in crate::server) slug: Option<String>,
    pub(in crate::server) topic: Option<String>,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct CreateAgentInput {
    pub(in crate::server) agent_member_id: MemberId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) display_name: String,
    pub(in crate::server) access_level: AccessLevel,
    pub(in crate::server) role_text: String,
    pub(in crate::server) computer_id: ComputerId,
    pub(in crate::server) driver_kind: DriverKind,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct CreateChannel;

impl CreateChannel {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: CreateChannelInput,
    ) -> Result<Channel, ApplicationError> {
        port.transact(async |transaction| {
            if let Some(channel_id) = transaction
                .resource_for_idempotency(
                    input.actor_member_id,
                    "channel.create",
                    input.idempotency_key,
                )
                .await?
            {
                return transaction.channel(ChannelId::from_uuid(channel_id)).await;
            }
            let access = transaction
                .member_access_level(input.actor_member_id, input.space_id)
                .await?;
            if !access.can_manage_space() || !input.audience.contains(&input.actor_member_id) {
                return Err(ApplicationError::PermissionDenied);
            }
            let channel = Channel::create(
                input.channel_id,
                input.space_id,
                input.audience,
                input.kind,
                input.slug,
                input.topic,
                input.now,
            )?;
            transaction.insert_channel(channel.clone()).await?;
            transaction
                .record_resource_idempotency(
                    input.actor_member_id,
                    "channel.create",
                    input.idempotency_key,
                    channel.id.into_uuid(),
                )
                .await?;
            transaction.emit(Effect::ChannelCreated(channel.id));
            Ok(channel)
        })
        .await
    }
}

pub(in crate::server) struct CreateAgent;

impl CreateAgent {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: CreateAgentInput,
    ) -> Result<Agent, ApplicationError> {
        port.transact(async |transaction| {
            if let Some(agent_id) = transaction
                .resource_for_idempotency(
                    input.actor_member_id,
                    "agent.create",
                    input.idempotency_key,
                )
                .await?
            {
                return transaction.agent(MemberId::from_uuid(agent_id)).await;
            }
            let actor_access = transaction
                .member_access_level(input.actor_member_id, input.space_id)
                .await?;
            if !actor_access.can_grant(input.access_level) {
                return Err(ApplicationError::PermissionDenied);
            }
            if !transaction
                .computer_accepts_agent(input.computer_id, input.space_id)
                .await?
            {
                return Err(ApplicationError::Conflict);
            }
            let member = Member {
                id: input.agent_member_id,
                space_id: input.space_id,
                display_name: input.display_name,
                access_level: input.access_level,
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
            transaction.insert_agent(member, agent.clone()).await?;
            transaction
                .record_resource_idempotency(
                    input.actor_member_id,
                    "agent.create",
                    input.idempotency_key,
                    agent.member_id.into_uuid(),
                )
                .await?;
            transaction.emit(Effect::AgentCreated {
                agent_id: agent.member_id,
                computer_id: input.computer_id,
            });
            Ok(agent)
        })
        .await
    }
}

pub(in crate::server) struct CreateAgentAction;

impl CreateAgentAction {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: CreateAgentActionInput,
    ) -> Result<Agent, ApplicationError> {
        port.transact(async |transaction| {
            if let Some(agent_id) = transaction
                .resource_for_idempotency(
                    input.actor_member_id,
                    "agent.create",
                    input.idempotency_key,
                )
                .await?
            {
                return transaction.agent(MemberId::from_uuid(agent_id)).await;
            }
            let run = transaction.run(input.current_run_id).await?;
            let run_view = run.view();
            if run_view.agent_id != input.actor_member_id || run_view.status != RunStatus::Running {
                return Err(ApplicationError::ContextChanged);
            }
            if !transaction
                .has_permission(input.actor_member_id, PermissionAction::AgentCreate)
                .await?
                || !transaction
                    .can_read_thread(input.actor_member_id, run_view.focus_thread_id)
                    .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            let member = Member {
                id: input.agent_member_id,
                space_id: run_view.space_id,
                display_name: input.display_name,
                access_level: AccessLevel::Member,
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
                run_view.focus_thread_id,
                input.actor_member_id,
                MessageContent::AgentCreated(agent.member_id),
                input.now,
            )?;
            transaction.insert_agent(member, agent.clone()).await?;
            transaction.insert_message(action).await?;
            transaction
                .record_resource_idempotency(
                    input.actor_member_id,
                    "agent.create",
                    input.idempotency_key,
                    agent.member_id.into_uuid(),
                )
                .await?;
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

pub(in crate::server) struct AddChannelAgents;

impl AddChannelAgents {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        actor: MemberId,
        channel_id: ChannelId,
        agent_ids: Vec<MemberId>,
        idempotency_key: IdempotencyKey,
        now: OffsetDateTime,
    ) -> Result<Vec<MemberId>, ApplicationError> {
        port.transact(async |transaction| {
            transaction
                .add_channel_agents(actor, channel_id, agent_ids, idempotency_key, now)
                .await
        })
        .await
    }
}

impl CreateChannelAction {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: CreateChannelActionInput,
    ) -> Result<Channel, ApplicationError> {
        port.transact(async |transaction| {
            if let Some(channel_id) = transaction
                .resource_for_idempotency(
                    input.actor_member_id,
                    "channel.create",
                    input.idempotency_key,
                )
                .await?
            {
                return transaction.channel(ChannelId::from_uuid(channel_id)).await;
            }
            let run = transaction.run(input.current_run_id).await?;
            let run_view = run.view();
            if run_view.agent_id != input.actor_member_id || run_view.status != RunStatus::Running {
                return Err(ApplicationError::ContextChanged);
            }
            if !transaction
                .has_permission(input.actor_member_id, PermissionAction::ChannelCreate)
                .await?
                || !transaction
                    .can_read_thread(input.actor_member_id, run_view.focus_thread_id)
                    .await?
            {
                return Err(ApplicationError::PermissionDenied);
            }
            let audience = if input.audience.is_empty() {
                transaction
                    .channel_action_audience(
                        run_view.focus_thread_id,
                        run_view.space_id,
                        input.kind == ChannelKind::Private,
                    )
                    .await?
            } else {
                input.audience
            };
            let channel = Channel::create(
                input.channel_id,
                run_view.space_id,
                audience,
                input.kind,
                input.slug,
                input.topic,
                input.now,
            )?;
            let action = Message::action_reply(
                input.action_message_id,
                run_view.focus_thread_id,
                input.actor_member_id,
                MessageContent::ChannelCreated(channel.id),
                input.now,
            )?;
            transaction.insert_channel(channel.clone()).await?;
            transaction.insert_message(action).await?;
            transaction
                .record_resource_idempotency(
                    input.actor_member_id,
                    "channel.create",
                    input.idempotency_key,
                    channel.id.into_uuid(),
                )
                .await?;
            transaction.emit(Effect::ChannelCreated(channel.id));
            Ok(channel)
        })
        .await
    }
}
