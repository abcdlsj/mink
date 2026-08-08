use crate::ids::{CompanyFileId, IdempotencyKey, MemberId, SpaceId};
use crate::server::domain::company_file::CompanyFile;
use time::OffsetDateTime;

use super::ApplicationError;

#[async_trait::async_trait]
pub(in crate::server) trait CompanyFileTransaction {
    async fn company_file(
        &mut self,
        id: CompanyFileId,
    ) -> Result<Option<CompanyFile>, ApplicationError>;
    async fn list_company_files(
        &mut self,
        space_id: SpaceId,
    ) -> Result<Vec<(CompanyFile, String)>, ApplicationError>;
    async fn company_file_name_exists(
        &mut self,
        space_id: SpaceId,
        name: &str,
    ) -> Result<bool, ApplicationError>;
    async fn insert_company_file(&mut self, file: &CompanyFile) -> Result<(), ApplicationError>;
    async fn save_company_file(&mut self, file: &CompanyFile) -> Result<(), ApplicationError>;
    #[allow(clippy::too_many_arguments)]
    async fn record_company_file_write(
        &mut self,
        space_id: SpaceId,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        file_id: CompanyFileId,
        event_kind: &str,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError>;
}

#[async_trait::async_trait]
pub(in crate::server) trait CompanyFileObjectPort: Send + Sync {
    async fn put(
        &self,
        object_key: &str,
        content: Vec<u8>,
    ) -> Result<super::StoredObject, ApplicationError>;
    async fn get(&self, object_key: &str) -> Result<Vec<u8>, ApplicationError>;
    async fn delete(&self, object_key: &str) -> Result<(), ApplicationError>;
}
