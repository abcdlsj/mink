use std::collections::BTreeSet;

use time::OffsetDateTime;

use crate::ids::{ChannelId, MemberId, MessageId, RunId, SpaceId};
use crate::server::domain::{
    conversation::{Channel, Message, MessageContent},
    execution::RunStatus,
    identity::PermissionAction,
};

use super::ports::{ApplicationError, Effect, ServerTransaction, TransactionPort};

pub(in crate::server) struct CreateChannelActionInput {
    pub(in crate::server) channel_id: ChannelId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) audience: BTreeSet<MemberId>,
    pub(in crate::server) action_message_id: MessageId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) current_run_id: RunId,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct CreateChannelAction;

impl CreateChannelAction {
    pub(in crate::server) fn execute<P: TransactionPort>(
        port: &mut P,
        input: CreateChannelActionInput,
    ) -> Result<Channel, ApplicationError> {
        port.transact(|transaction| {
            let run = transaction.run(input.current_run_id)?;
            if run.agent_id != input.actor_member_id || run.status != RunStatus::Running {
                return Err(ApplicationError::ContextChanged);
            }
            if !transaction.has_permission(input.actor_member_id, PermissionAction::ChannelCreate)
                || !transaction.can_read_thread(input.actor_member_id, run.focus_thread_id)
            {
                return Err(ApplicationError::PermissionDenied);
            }
            let channel = Channel {
                id: input.channel_id,
                space_id: input.space_id,
                audience: input.audience,
            };
            let action = Message::action_reply(
                input.action_message_id,
                run.focus_thread_id,
                input.actor_member_id,
                MessageContent::ChannelCreated(channel.id),
                input.now,
            )?;
            transaction.insert_channel(channel.clone())?;
            transaction.insert_message(action)?;
            transaction.emit(Effect::ChannelCreated(channel.id));
            Ok(channel)
        })
    }
}
