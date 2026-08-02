use super::*;

#[async_trait]
pub(in crate::server) trait CollaborationTransaction {
    async fn channel_access(
        &mut self,
        user_id: uuid::Uuid,
        channel_id: ChannelId,
    ) -> Result<Option<MemberId>, ApplicationError>;
    async fn direct_messages_for_member(
        &mut self,
        member_id: MemberId,
        space_id: SpaceId,
    ) -> Result<Vec<DirectMessageView>, ApplicationError>;
    async fn direct_message_between(
        &mut self,
        space_id: SpaceId,
        first: MemberId,
        second: MemberId,
    ) -> Result<Option<DirectMessageView>, ApplicationError>;
    async fn space_member(
        &mut self,
        member_id: MemberId,
        space_id: SpaceId,
    ) -> Result<Option<SpaceMemberView>, ApplicationError>;
    async fn inbox_for_member(
        &mut self,
        member_id: MemberId,
        scope: InboxScope,
    ) -> Result<Vec<InboxItemView>, ApplicationError>;
    async fn inbox_item_view(
        &mut self,
        item_id: InboxItemId,
    ) -> Result<InboxItemView, ApplicationError>;
    async fn save_channel(&mut self, channel: Channel) -> Result<(), ApplicationError>;
    async fn set_thread_subscription(
        &mut self,
        thread_id: ThreadId,
        member_id: MemberId,
        following: bool,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn thread(&mut self, id: ThreadId) -> Result<Thread, ApplicationError>;
    async fn root_message(&mut self, thread_id: ThreadId) -> Result<Message, ApplicationError>;
    async fn message(&mut self, id: MessageId) -> Result<Message, ApplicationError>;
    async fn channel(&mut self, id: ChannelId) -> Result<Channel, ApplicationError>;
    async fn inbox_item(&mut self, id: InboxItemId) -> Result<InboxItem, ApplicationError>;
    async fn can_read_thread(
        &mut self,
        actor: MemberId,
        thread_id: ThreadId,
    ) -> Result<bool, ApplicationError>;
    async fn thread_message_sequence(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<u64, ApplicationError>;
    async fn publish_message(
        &mut self,
        draft: MessageDraft,
    ) -> Result<PublishedMessage, ApplicationError>;
    async fn insert_message(&mut self, message: Message) -> Result<(), ApplicationError>;
    async fn save_message(&mut self, message: Message) -> Result<(), ApplicationError>;
    async fn save_message_mentions(
        &mut self,
        message_id: MessageId,
        mentions: Vec<MemberId>,
        mention_all: bool,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn insert_channel(&mut self, channel: Channel) -> Result<(), ApplicationError>;
    async fn channel_action_audience(
        &mut self,
        focus_thread_id: ThreadId,
        space_id: SpaceId,
        private: bool,
    ) -> Result<BTreeSet<MemberId>, ApplicationError>;
    async fn channel_for_thread(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<ChannelId>, ApplicationError>;
    async fn add_channel_agents(
        &mut self,
        actor: MemberId,
        channel_id: ChannelId,
        agent_ids: Vec<MemberId>,
        idempotency_key: IdempotencyKey,
        now: time::OffsetDateTime,
    ) -> Result<Vec<MemberId>, ApplicationError>;
    async fn channel_member_visible(
        &mut self,
        channel_id: ChannelId,
        member_id: MemberId,
    ) -> Result<bool, ApplicationError>;
    async fn message_sequence_in_channel(
        &mut self,
        message_id: MessageId,
        channel_id: ChannelId,
    ) -> Result<Option<u64>, ApplicationError>;
    async fn channel_snapshot(&mut self, channel_id: ChannelId) -> Result<u64, ApplicationError>;
    async fn pending_item_for_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<bool, ApplicationError>;
}
