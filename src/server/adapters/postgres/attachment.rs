use super::*;

impl PostgresTransaction {
    pub(super) async fn attachment_is_visible(
        &mut self,
        id: AttachmentId,
        viewer: MemberId,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM message_attachments links \
             JOIN messages ON messages.id=links.message_id \
             JOIN channel_members members ON members.channel_id=messages.channel_id \
             WHERE links.attachment_id=$1 AND members.member_id=$2)",
        )
        .bind(id.into_uuid())
        .bind(viewer.into_uuid())
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    #[allow(clippy::too_many_arguments)]
    pub(super) async fn record_attachment_write(
        &mut self,
        space_id: SpaceId,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        attachment_id: AttachmentId,
        event_kind: &str,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        let attachment_uuid = attachment_id.into_uuid();
        sqlx::query(
            "INSERT INTO idempotency_records\
             (actor_member_id,action,idempotency_key,response_code,resource_id,result_hash,created_at) \
             VALUES($1,$2,$3,'ok',$4,$5,$6)",
        )
        .bind(actor.into_uuid())
        .bind(action)
        .bind(key.into_uuid())
        .bind(attachment_uuid)
        .bind(Sha256::digest(attachment_uuid.as_bytes()).as_slice())
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        sqlx::query(
            "INSERT INTO audit_events\
             (id,space_id,actor_member_id,action,subject_type,subject_id,created_at) \
             VALUES($1,$2,$3,$4,'attachment',$5,$6)",
        )
        .bind(Uuid::now_v7())
        .bind(space_id.into_uuid())
        .bind(actor.into_uuid())
        .bind(action)
        .bind(attachment_uuid)
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        sqlx::query(
            "INSERT INTO outbox_events(id,space_id,kind,payload_json,created_at) \
             VALUES($1,$2,$3,$4,$5)",
        )
        .bind(Uuid::now_v7())
        .bind(space_id.into_uuid())
        .bind(event_kind)
        .bind(json!({"attachment_id": attachment_uuid}))
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn save_attachment(
        &mut self,
        attachment: &Attachment,
    ) -> Result<(), ApplicationError> {
        let attachment = attachment.snapshot();
        let length = attachment
            .length
            .map(i64::try_from)
            .transpose()
            .map_err(|_| ApplicationError::PayloadTooLarge)?;
        sqlx::query(
            "UPDATE attachments SET name=$2,media_type=$3,length=$4,sha256=$5,status=$6,\
             ready_at=$7,deleted_at=$8 WHERE id=$1",
        )
        .bind(attachment.id.into_uuid())
        .bind(&attachment.name)
        .bind(&attachment.media_type)
        .bind(length)
        .bind(attachment.sha256.map(|digest| digest.to_vec()))
        .bind(attachment.status.code())
        .bind(attachment.ready_at)
        .bind(attachment.deleted_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn attachment(
        &mut self,
        id: AttachmentId,
    ) -> Result<Option<Attachment>, ApplicationError> {
        let row = sqlx::query(
            "SELECT id,space_id,uploader_member_id,name,media_type,length,sha256,object_key,\
             status,created_at,ready_at,deleted_at FROM attachments WHERE id=$1 FOR UPDATE",
        )
        .bind(id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        row.map(|row| attachment_from_row(&row)).transpose()
    }

    pub(super) async fn insert_attachment(
        &mut self,
        attachment: &Attachment,
    ) -> Result<(), ApplicationError> {
        let attachment = attachment.snapshot();
        sqlx::query(
            "INSERT INTO attachments\
             (id,space_id,uploader_member_id,name,media_type,object_key,status,created_at) \
             VALUES($1,$2,$3,$4,$5,$6,$7,$8)",
        )
        .bind(attachment.id.into_uuid())
        .bind(attachment.space_id.into_uuid())
        .bind(attachment.uploader_member_id.into_uuid())
        .bind(&attachment.name)
        .bind(&attachment.media_type)
        .bind(&attachment.object_key)
        .bind(attachment.status.code())
        .bind(attachment.created_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }
}
#[async_trait]
impl AttachmentTransaction for PostgresTransaction {
    async fn space_of_attachment(
        &mut self,
        attachment_id: AttachmentId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        self.space_of_attachment(attachment_id).await
    }
    async fn attachment(
        &mut self,
        id: AttachmentId,
    ) -> Result<Option<Attachment>, ApplicationError> {
        self.attachment(id).await
    }
    async fn insert_attachment(&mut self, attachment: &Attachment) -> Result<(), ApplicationError> {
        self.insert_attachment(attachment).await
    }
    async fn save_attachment(&mut self, attachment: &Attachment) -> Result<(), ApplicationError> {
        self.save_attachment(attachment).await
    }
    async fn attachment_is_visible(
        &mut self,
        id: AttachmentId,
        viewer: MemberId,
    ) -> Result<bool, ApplicationError> {
        self.attachment_is_visible(id, viewer).await
    }
    async fn record_attachment_write(
        &mut self,
        space_id: SpaceId,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        attachment_id: AttachmentId,
        event_kind: &str,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.record_attachment_write(space_id, actor, action, key, attachment_id, event_kind, now)
            .await
    }
}
