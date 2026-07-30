use time::OffsetDateTime;

use crate::ids::{AttachmentId, IdempotencyKey, MemberId, SpaceId};

use crate::server::domain::attachment::{Attachment, ContentDigest, DeclaredContent};

use super::ports::{ApplicationError, AttachmentObjectPort, ServerTransaction, TransactionPort};

const CREATE_ACTION: &str = "attachment.upload.create";
const COMPLETE_ACTION: &str = "attachment.upload.complete";

pub(in crate::server) struct OpenUpload;

pub(in crate::server) struct OpenUploadInput<'a> {
    pub(in crate::server) attachment_id: AttachmentId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) uploader_member_id: MemberId,
    pub(in crate::server) name: &'a str,
    pub(in crate::server) media_type: &'a str,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) now: OffsetDateTime,
}

pub(in crate::server) struct OpenedUpload {
    pub(in crate::server) attachment: Attachment,
    pub(in crate::server) created: bool,
}

impl OpenUpload {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: OpenUploadInput<'_>,
    ) -> Result<OpenedUpload, ApplicationError> {
        let attachment = Attachment::open(
            input.attachment_id,
            input.space_id,
            input.uploader_member_id,
            input.name,
            input.media_type,
            input.now,
        )?;
        port.transact(async |transaction| {
            transaction
                .lock_idempotency(
                    input.uploader_member_id,
                    CREATE_ACTION,
                    input.idempotency_key,
                )
                .await?;
            if let Some(existing) = transaction
                .resource_for_idempotency(
                    input.uploader_member_id,
                    CREATE_ACTION,
                    input.idempotency_key,
                )
                .await?
            {
                let attachment = transaction
                    .attachment(AttachmentId::from_uuid(existing))
                    .await?
                    .ok_or(ApplicationError::NotFound)?;
                return Ok(OpenedUpload {
                    attachment,
                    created: false,
                });
            }
            transaction.insert_attachment(&attachment).await?;
            transaction
                .record_attachment_write(
                    input.space_id,
                    input.uploader_member_id,
                    CREATE_ACTION,
                    input.idempotency_key,
                    input.attachment_id,
                    "attachment.created",
                    input.now,
                )
                .await?;
            Ok(OpenedUpload {
                attachment: attachment.clone(),
                created: true,
            })
        })
        .await
    }
}

pub(in crate::server) struct WriteUploadContent;

pub(in crate::server) struct WriteUploadContentInput {
    pub(in crate::server) attachment_id: AttachmentId,
    pub(in crate::server) uploader_member_id: MemberId,
    pub(in crate::server) content: Vec<u8>,
    pub(in crate::server) max_bytes: u64,
}

impl WriteUploadContent {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        objects: &impl AttachmentObjectPort,
        input: WriteUploadContentInput,
    ) -> Result<SpaceId, ApplicationError> {
        if input.content.len() as u64 > input.max_bytes {
            return Err(ApplicationError::PayloadTooLarge);
        }
        let attachment = port
            .transact(async |transaction| {
                let attachment = transaction
                    .attachment(input.attachment_id)
                    .await?
                    .ok_or(ApplicationError::NotFound)?;
                attachment.require_uploader(input.uploader_member_id)?;
                attachment.require_open()?;
                Ok(attachment)
            })
            .await?;
        // Object content is external to PostgreSQL and this write is idempotent.
        objects.put(&attachment.object_key, input.content).await?;
        Ok(attachment.space_id)
    }
}

pub(in crate::server) struct CompleteUpload;

pub(in crate::server) struct CompleteUploadInput {
    pub(in crate::server) attachment_id: AttachmentId,
    pub(in crate::server) uploader_member_id: MemberId,
    pub(in crate::server) declared: DeclaredContent,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) now: OffsetDateTime,
}

impl CompleteUpload {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        objects: &impl AttachmentObjectPort,
        input: CompleteUploadInput,
    ) -> Result<Attachment, ApplicationError> {
        let attachment = port
            .transact(async |transaction| {
                let attachment = transaction
                    .attachment(input.attachment_id)
                    .await?
                    .ok_or(ApplicationError::NotFound)?;
                attachment.require_uploader(input.uploader_member_id)?;
                Ok(attachment)
            })
            .await?;
        let stored = objects.get(&attachment.object_key).await?;
        let actual = ContentDigest {
            length: stored.len() as u64,
            sha256: <[u8; 32]>::from(<sha2::Sha256 as sha2::Digest>::digest(&stored)),
        };
        port.transact(async |transaction| {
            transaction
                .lock_idempotency(
                    input.uploader_member_id,
                    COMPLETE_ACTION,
                    input.idempotency_key,
                )
                .await?;
            if transaction
                .resource_for_idempotency(
                    input.uploader_member_id,
                    COMPLETE_ACTION,
                    input.idempotency_key,
                )
                .await?
                .is_some()
            {
                return transaction
                    .attachment(input.attachment_id)
                    .await?
                    .ok_or(ApplicationError::NotFound);
            }
            let mut attachment = transaction
                .attachment(input.attachment_id)
                .await?
                .ok_or(ApplicationError::NotFound)?;
            attachment.require_uploader(input.uploader_member_id)?;
            attachment.complete(&input.declared, actual, input.now)?;
            transaction.save_attachment(&attachment).await?;
            transaction
                .record_attachment_write(
                    attachment.space_id,
                    input.uploader_member_id,
                    COMPLETE_ACTION,
                    input.idempotency_key,
                    input.attachment_id,
                    "attachment.ready",
                    input.now,
                )
                .await?;
            Ok(attachment)
        })
        .await
    }
}

pub(in crate::server) struct ReadAttachment;

pub(in crate::server) struct AttachmentContent {
    pub(in crate::server) attachment: Attachment,
    pub(in crate::server) content: Vec<u8>,
}

impl ReadAttachment {
    pub(in crate::server) async fn for_member<P: TransactionPort>(
        port: &mut P,
        objects: &impl AttachmentObjectPort,
        attachment_id: AttachmentId,
        viewer: MemberId,
    ) -> Result<AttachmentContent, ApplicationError> {
        Self::read(port, objects, attachment_id, viewer, false).await
    }

    pub(in crate::server) async fn for_uploader_or_member<P: TransactionPort>(
        port: &mut P,
        objects: &impl AttachmentObjectPort,
        attachment_id: AttachmentId,
        viewer: MemberId,
    ) -> Result<AttachmentContent, ApplicationError> {
        Self::read(port, objects, attachment_id, viewer, true).await
    }

    async fn read<P: TransactionPort>(
        port: &mut P,
        objects: &impl AttachmentObjectPort,
        attachment_id: AttachmentId,
        viewer: MemberId,
        allow_uploader: bool,
    ) -> Result<AttachmentContent, ApplicationError> {
        let attachment = port
            .transact(async |transaction| {
                let attachment = transaction
                    .attachment(attachment_id)
                    .await?
                    .ok_or(ApplicationError::NotFound)?;
                attachment.require_ready()?;
                if allow_uploader && attachment.uploader_member_id == viewer {
                    return Ok(attachment);
                }
                if transaction
                    .attachment_is_visible(attachment_id, viewer)
                    .await?
                {
                    Ok(attachment)
                } else {
                    Err(ApplicationError::PermissionDenied)
                }
            })
            .await?;
        let content = objects.get(&attachment.object_key).await?;
        Ok(AttachmentContent {
            attachment,
            content,
        })
    }
}
