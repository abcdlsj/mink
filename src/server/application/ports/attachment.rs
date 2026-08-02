use super::*;

#[async_trait]
pub(in crate::server) trait AttachmentTransaction {
    async fn space_of_attachment(
        &mut self,
        attachment_id: AttachmentId,
    ) -> Result<Option<SpaceId>, ApplicationError>;
    async fn attachment(
        &mut self,
        id: AttachmentId,
    ) -> Result<Option<Attachment>, ApplicationError>;
    async fn insert_attachment(&mut self, attachment: &Attachment) -> Result<(), ApplicationError>;
    async fn save_attachment(&mut self, attachment: &Attachment) -> Result<(), ApplicationError>;
    async fn attachment_is_visible(
        &mut self,
        id: AttachmentId,
        viewer: MemberId,
    ) -> Result<bool, ApplicationError>;
    #[allow(clippy::too_many_arguments)]
    async fn record_attachment_write(
        &mut self,
        space_id: SpaceId,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        attachment_id: AttachmentId,
        event_kind: &str,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
}
