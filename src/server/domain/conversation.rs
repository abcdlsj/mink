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
    pub(in crate::server) archived_at: Option<OffsetDateTime>,
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
        // DM 的 audience 恰好是两个 Member。Server 按这两个 Member 定位既有 DM，
        // 多于或少于两人都会使该定位失效。
        let valid_audience = match kind {
            ChannelKind::Direct => audience.len() == 2,
            ChannelKind::Public | ChannelKind::Private => !audience.is_empty(),
        };
        if !valid_audience || !valid_slug {
            return Err(DomainError::InvalidChannel);
        }
        Ok(Self {
            id,
            space_id,
            audience,
            kind,
            slug,
            topic,
            archived_at: None,
            created_at,
        })
    }

    /// 归档 Channel。归档不删除 Message、Thread 或 Task，只停止新的协作。
    /// 见 [协作模型](../../../docs/design/02-collaboration.md)。
    pub(in crate::server) fn archive(&mut self, now: OffsetDateTime) -> Result<(), DomainError> {
        // DM 没有 slug 也没有治理者，归档语义对它不成立。
        if self.kind == ChannelKind::Direct {
            return Err(DomainError::InvalidChannel);
        }
        if self.archived_at.is_some() {
            return Err(DomainError::InvalidTransition);
        }
        self.archived_at = Some(now);
        Ok(())
    }

    /// Member 自行加入。只有 public Channel 允许，private 需要被加入。
    /// 已在 audience 中时保持成立，使重试幂等。
    pub(in crate::server) fn admit(&mut self, member_id: MemberId) -> Result<(), DomainError> {
        if self.kind != ChannelKind::Public {
            return Err(DomainError::ChannelNotJoinable);
        }
        if self.archived_at.is_some() {
            return Err(DomainError::InvalidTransition);
        }
        self.audience.insert(member_id);
        Ok(())
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
    pub(in crate::server) edited_at: Option<OffsetDateTime>,
    pub(in crate::server) deleted_at: Option<OffsetDateTime>,
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
            edited_at: None,
            deleted_at: None,
        })
    }

    pub(in crate::server) fn edit_text(
        &mut self,
        body: String,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if body.trim().is_empty()
            || self.deleted_at.is_some()
            || !matches!(self.content, MessageContent::Text(_))
        {
            return Err(DomainError::InvalidMessageMutation);
        }
        self.content = MessageContent::Text(body);
        self.edited_at = Some(now);
        Ok(())
    }

    pub(in crate::server) fn soft_delete(
        &mut self,
        now: OffsetDateTime,
    ) -> Result<(), DomainError> {
        if self.deleted_at.is_some() {
            return Ok(());
        }
        if !matches!(self.content, MessageContent::Text(_)) {
            return Err(DomainError::InvalidMessageMutation);
        }
        self.content = MessageContent::Text(String::new());
        self.deleted_at = Some(now);
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn member(value: u128) -> MemberId {
        MemberId::from_uuid(uuid::Uuid::from_u128(value))
    }

    fn channel(kind: ChannelKind, audience: &[MemberId]) -> Result<Channel, DomainError> {
        Channel::create(
            ChannelId::from_uuid(uuid::Uuid::from_u128(1)),
            SpaceId::from_uuid(uuid::Uuid::from_u128(2)),
            audience.iter().copied().collect(),
            kind,
            match kind {
                ChannelKind::Direct => None,
                _ => Some("design".into()),
            },
            None,
            OffsetDateTime::UNIX_EPOCH,
        )
    }

    #[test]
    fn a_direct_channel_holds_exactly_two_members() {
        assert!(channel(ChannelKind::Direct, &[member(3), member(4)]).is_ok());
        // 一人或三人都会让「按双方定位既有 DM」失效。
        assert_eq!(
            channel(ChannelKind::Direct, &[member(3)]),
            Err(DomainError::InvalidChannel)
        );
        assert_eq!(
            channel(ChannelKind::Direct, &[member(3), member(4), member(5)]),
            Err(DomainError::InvalidChannel)
        );
        // 非 DM 不受两人限制。
        assert!(channel(ChannelKind::Public, &[member(3)]).is_ok());
    }

    #[test]
    fn only_a_public_channel_admits_members_on_their_own() {
        let mut public = channel(ChannelKind::Public, &[member(3)]).expect("public channel");
        public.admit(member(4)).expect("public admits");
        assert!(public.audience.contains(&member(4)));
        // 重复加入成立，使重试幂等。
        public.admit(member(4)).expect("repeat admits");
        assert_eq!(public.audience.len(), 2);

        let mut private = channel(ChannelKind::Private, &[member(3)]).expect("private channel");
        assert_eq!(
            private.admit(member(4)),
            Err(DomainError::ChannelNotJoinable)
        );
        let mut direct =
            channel(ChannelKind::Direct, &[member(3), member(4)]).expect("direct channel");
        assert_eq!(
            direct.admit(member(5)),
            Err(DomainError::ChannelNotJoinable)
        );
    }

    #[test]
    fn archiving_is_one_way_and_closes_the_channel_to_new_members() {
        let now = OffsetDateTime::UNIX_EPOCH;
        let mut public = channel(ChannelKind::Public, &[member(3)]).expect("public channel");
        public.archive(now).expect("archives once");
        assert_eq!(public.archived_at, Some(now));
        assert_eq!(public.archive(now), Err(DomainError::InvalidTransition));
        // 归档后不再接受新成员。
        assert_eq!(public.admit(member(4)), Err(DomainError::InvalidTransition));

        // DM 没有治理者，归档语义对它不成立。
        let mut direct =
            channel(ChannelKind::Direct, &[member(3), member(4)]).expect("direct channel");
        assert_eq!(direct.archive(now), Err(DomainError::InvalidChannel));
    }
}
