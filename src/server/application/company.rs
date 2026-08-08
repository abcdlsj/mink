use sha2::{Digest, Sha256};
use time::OffsetDateTime;

use crate::ids::{CompanyFileId, IdempotencyKey, MemberId, SpaceId};

use crate::server::domain::company_file::CompanyFile;

use super::ports::{
    ApplicationError, CompanyFileObjectPort, CompanyFileTransaction, EffectSink,
    IdentityTransaction, TransactionPort,
};

const UPLOAD_ACTION: &str = "company_file.upload";
const DELETE_ACTION: &str = "company_file.delete";

pub(in crate::server) struct ListCompanyFiles;

pub(in crate::server) struct ListCompanyFilesInput {
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) actor_member_id: MemberId,
}

impl ListCompanyFiles {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        input: ListCompanyFilesInput,
    ) -> Result<Vec<(CompanyFile, String)>, ApplicationError> {
        port.transact(async |transaction| {
            transaction
                .member_access_level(input.actor_member_id, input.space_id)
                .await?;
            transaction.list_company_files(input.space_id).await
        })
        .await
    }
}

pub(in crate::server) struct UploadCompanyFile;

pub(in crate::server) struct UploadCompanyFileInput<'a> {
    pub(in crate::server) file_id: CompanyFileId,
    pub(in crate::server) space_id: SpaceId,
    pub(in crate::server) uploader_member_id: MemberId,
    pub(in crate::server) name: &'a str,
    pub(in crate::server) media_type: &'a str,
    pub(in crate::server) content: Vec<u8>,
    pub(in crate::server) max_bytes: u64,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) now: OffsetDateTime,
}

impl UploadCompanyFile {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        objects: &impl CompanyFileObjectPort,
        input: UploadCompanyFileInput<'_>,
    ) -> Result<CompanyFile, ApplicationError> {
        let length = u64::try_from(input.content.len()).map_err(|_| ApplicationError::Conflict)?;
        if length > input.max_bytes {
            return Err(ApplicationError::PayloadTooLarge);
        }
        let digest: [u8; 32] = Sha256::digest(&input.content).into();
        let resolved_name = port
            .transact(async |transaction| {
                transaction
                    .member_access_level(input.uploader_member_id, input.space_id)
                    .await?;
                let mut candidate = input.name.trim().to_owned();
                let mut suffix = 2;
                while transaction
                    .company_file_name_exists(input.space_id, &candidate)
                    .await?
                {
                    candidate = uniquified_name(input.name.trim(), suffix);
                    suffix += 1;
                }
                Ok(candidate)
            })
            .await?;
        let file = CompanyFile::create(
            input.file_id,
            input.space_id,
            input.uploader_member_id,
            &resolved_name,
            input.media_type,
            length,
            digest,
            input.now,
        )?;
        // Content is external to PostgreSQL; write it before publishing metadata so a listed file
        // always has bytes available. A failed transaction leaves an unreferenced object, which is
        // harmless and never listed.
        let object_key = file.object_key();
        objects.put(&object_key, input.content).await?;
        let replayed = port
            .transact(async |transaction| {
                transaction
                    .lock_idempotency(
                        input.uploader_member_id,
                        UPLOAD_ACTION,
                        input.idempotency_key,
                    )
                    .await?;
                if let Some(existing_id) = transaction
                    .resource_for_idempotency(
                        input.uploader_member_id,
                        UPLOAD_ACTION,
                        input.idempotency_key,
                    )
                    .await?
                {
                    let existing = transaction
                        .company_file(CompanyFileId::from_uuid(existing_id))
                        .await?
                        .ok_or(ApplicationError::NotFound)?;
                    return Ok(Some(existing));
                }
                transaction.insert_company_file(&file).await?;
                transaction
                    .record_company_file_write(
                        input.space_id,
                        input.uploader_member_id,
                        UPLOAD_ACTION,
                        input.idempotency_key,
                        input.file_id,
                        "company_file.created",
                        input.now,
                    )
                    .await?;
                Ok(None)
            })
            .await?;
        if let Some(existing) = replayed {
            return Ok(existing);
        }
        Ok(file)
    }
}

fn uniquified_name(name: &str, suffix: u64) -> String {
    match name.rsplit_once('.') {
        Some((stem, extension))
            if !stem.is_empty()
                && extension
                    .chars()
                    .all(|character| !character.is_whitespace()) =>
        {
            format!("{stem} ({suffix}).{extension}")
        }
        _ => format!("{name} ({suffix})"),
    }
}

pub(in crate::server) struct ReadCompanyFile;

pub(in crate::server) struct CompanyFileContent {
    pub(in crate::server) file: CompanyFile,
    pub(in crate::server) content: Vec<u8>,
}

impl ReadCompanyFile {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        objects: &impl CompanyFileObjectPort,
        file_id: CompanyFileId,
        actor_member_id: MemberId,
    ) -> Result<CompanyFileContent, ApplicationError> {
        let file = port
            .transact(async |transaction| {
                let file = transaction
                    .company_file(file_id)
                    .await?
                    .ok_or(ApplicationError::NotFound)?;
                if file.view().deleted_at.is_some() {
                    return Err(ApplicationError::NotFound);
                }
                transaction
                    .member_access_level(actor_member_id, file.view().space_id)
                    .await?;
                Ok(file)
            })
            .await?;
        let content = objects.get(&file.object_key()).await?;
        Ok(CompanyFileContent { file, content })
    }
}

pub(in crate::server) struct DeleteCompanyFile;

pub(in crate::server) struct DeleteCompanyFileInput {
    pub(in crate::server) file_id: CompanyFileId,
    pub(in crate::server) actor_member_id: MemberId,
    pub(in crate::server) idempotency_key: IdempotencyKey,
    pub(in crate::server) now: OffsetDateTime,
}

impl DeleteCompanyFile {
    pub(in crate::server) async fn execute<P: TransactionPort>(
        port: &mut P,
        objects: &impl CompanyFileObjectPort,
        input: DeleteCompanyFileInput,
    ) -> Result<(), ApplicationError> {
        let deletion = port
            .transact(async |transaction| {
                transaction
                    .lock_idempotency(input.actor_member_id, DELETE_ACTION, input.idempotency_key)
                    .await?;
                if transaction
                    .resource_for_idempotency(
                        input.actor_member_id,
                        DELETE_ACTION,
                        input.idempotency_key,
                    )
                    .await?
                    .is_some()
                {
                    return Ok(None);
                }
                let mut file = transaction
                    .company_file(input.file_id)
                    .await?
                    .ok_or(ApplicationError::NotFound)?;
                let access = transaction
                    .member_access_level(input.actor_member_id, file.view().space_id)
                    .await?;
                file.delete(input.actor_member_id, access.can_manage_space(), input.now)?;
                let space_id = file.view().space_id;
                let object_key = file.object_key();
                transaction.save_company_file(&file).await?;
                transaction
                    .record_company_file_write(
                        space_id,
                        input.actor_member_id,
                        DELETE_ACTION,
                        input.idempotency_key,
                        input.file_id,
                        "company_file.deleted",
                        input.now,
                    )
                    .await?;
                Ok(Some((space_id, object_key)))
            })
            .await?;
        if let Some((_, object_key)) = deletion {
            // The metadata transaction already committed the deletion; an orphaned blob is
            // unreachable through the API and never leaks file content.
            let _ = objects.delete(&object_key).await;
        }
        Ok(())
    }
}
