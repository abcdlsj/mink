use std::collections::BTreeSet;

use time::OffsetDateTime;

use crate::ids::{ChannelId, MemberId, MessageId, SpaceId, ThreadId};

use super::DomainError;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum MessagePlacement {
    Root,
    Reply,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) enum MessageContent {
    Text(String),
    ChannelCreated(ChannelId),
    AgentCreated(MemberId),
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::server) enum ChannelKind {
    Public,
    Private,
    Direct,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct Channel {
    pub(in crate::server) id: ChannelId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) audience: BTreeSet<MemberId>,
    pub(in crate::server) kind: ChannelKind,
    pub(in crate::server) slug: Option<String>,
    pub(in crate::server) topic: Option<String>,
    pub(in crate::server) created_at: OffsetDateTime,
}

impl Channel {
    pub(in crate::server) fn create(
        id: ChannelId,
        space_id: SpaceId,
        audience: BTreeSet<MemberId>,
        kind: ChannelKind,
        slug: Option<String>,
        topic: Option<String>,
        created_at: OffsetDateTime,
    ) -> Result<Self, DomainError> {
        let valid_slug = match kind {
            ChannelKind::Direct => slug.is_none(),
            ChannelKind::Public | ChannelKind::Private => {
                slug.as_ref().is_some_and(|value| !value.trim().is_empty())
            }
        };
        if audience.is_empty() || !valid_slug {
            return Err(DomainError::InvalidChannel);
        }
        Ok(Self {
            id,
            space_id,
            audience,
            kind,
            slug,
            topic,
            created_at,
        })
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct Message {
    pub(in crate::server) id: MessageId,
    pub(in crate::server) thread_id: ThreadId,
    pub(in crate::server) author_member_id: MemberId,
    pub(in crate::server) placement: MessagePlacement,
    pub(in crate::server) content: MessageContent,
    pub(in crate::server) created_at: OffsetDateTime,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct Thread {
    pub(in crate::server) id: ThreadId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) channel_id: ChannelId,
    pub(in crate::server) root_message_id: MessageId,
    pub(in crate::server) audience: BTreeSet<MemberId>,
}

impl Thread {
    pub(in crate::server) fn validate_source(&self, message: &Message) -> Result<(), DomainError> {
        if message.placement != MessagePlacement::Root {
            return Err(DomainError::SourceIsNotRoot);
        }
        if message.id != self.root_message_id || message.thread_id != self.id {
            return Err(DomainError::SourceMismatch);
        }
        Ok(())
    }

    pub(in crate::server) fn has_same_audience(&self, other: &Self) -> bool {
        self.audience == other.audience
    }
}

impl Message {
    pub(in crate::server) fn action_reply(
        id: MessageId,
        thread_id: ThreadId,
        author_member_id: MemberId,
        content: MessageContent,
        created_at: OffsetDateTime,
    ) -> Result<Self, DomainError> {
        if matches!(content, MessageContent::Text(_)) {
            return Err(DomainError::ActionMustBeReply);
        }
        Ok(Self {
            id,
            thread_id,
            author_member_id,
            placement: MessagePlacement::Reply,
            content,
            created_at,
        })
    }
}
