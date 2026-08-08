use super::*;

impl PostgresTransaction {
    pub(super) async fn company_file(
        &mut self,
        id: CompanyFileId,
    ) -> Result<Option<CompanyFile>, ApplicationError> {
        let row = sqlx::query(
            "SELECT id,space_id,name,media_type,length,sha256,uploader_member_id,created_at,deleted_at \
             FROM company_files WHERE id=$1 FOR UPDATE",
        )
        .bind(id.into_uuid())
        .fetch_optional(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        row.map(|row| company_file_from_row(&row)).transpose()
    }

    pub(super) async fn list_company_files(
        &mut self,
        space_id: SpaceId,
    ) -> Result<Vec<(CompanyFile, String)>, ApplicationError> {
        let rows = sqlx::query(
            "SELECT f.id,f.space_id,f.name,f.media_type,f.length,f.sha256,\
             f.uploader_member_id,f.created_at,f.deleted_at,m.display_name AS uploader_name \
             FROM company_files f JOIN members m ON m.id=f.uploader_member_id \
             WHERE f.space_id=$1 AND f.deleted_at IS NULL \
             ORDER BY f.created_at DESC,f.id DESC",
        )
        .bind(space_id.into_uuid())
        .fetch_all(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        rows.iter()
            .map(|row| {
                Ok((
                    company_file_from_row(row)?,
                    row.get::<String, _>("uploader_name"),
                ))
            })
            .collect()
    }

    pub(super) async fn company_file_name_exists(
        &mut self,
        space_id: SpaceId,
        name: &str,
    ) -> Result<bool, ApplicationError> {
        sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM company_files \
             WHERE space_id=$1 AND name=$2 AND deleted_at IS NULL)",
        )
        .bind(space_id.into_uuid())
        .bind(name)
        .fetch_one(&mut *self.connection)
        .await
        .map_err(map_sqlx)
    }

    pub(super) async fn insert_company_file(
        &mut self,
        file: &CompanyFile,
    ) -> Result<(), ApplicationError> {
        let file = file.snapshot();
        sqlx::query(
            "INSERT INTO company_files\
             (id,space_id,name,media_type,length,sha256,uploader_member_id,created_at,deleted_at) \
             VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULL)",
        )
        .bind(file.id.into_uuid())
        .bind(file.space_id.into_uuid())
        .bind(&file.name)
        .bind(&file.media_type)
        .bind(i64::try_from(file.length).map_err(|_| ApplicationError::Conflict)?)
        .bind(file.sha256.to_vec())
        .bind(file.uploader_member_id.into_uuid())
        .bind(file.created_at)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }

    pub(super) async fn save_company_file(
        &mut self,
        file: &CompanyFile,
    ) -> Result<(), ApplicationError> {
        let file = file.snapshot();
        sqlx::query("UPDATE company_files SET deleted_at=$2 WHERE id=$1")
            .bind(file.id.into_uuid())
            .bind(file.deleted_at)
            .execute(&mut *self.connection)
            .await
            .map_err(map_sqlx)?;
        Ok(())
    }

    #[allow(clippy::too_many_arguments)]
    pub(super) async fn record_company_file_write(
        &mut self,
        space_id: SpaceId,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        file_id: CompanyFileId,
        event_kind: &str,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        let file_uuid = file_id.into_uuid();
        sqlx::query(
            "INSERT INTO idempotency_records\
             (actor_member_id,action,idempotency_key,response_code,resource_id,result_hash,created_at) \
             VALUES($1,$2,$3,'ok',$4,$5,$6)",
        )
        .bind(actor.into_uuid())
        .bind(action)
        .bind(key.into_uuid())
        .bind(file_uuid)
        .bind(Sha256::digest(file_uuid.as_bytes()).as_slice())
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        sqlx::query(
            "INSERT INTO audit_events\
             (id,space_id,actor_member_id,action,subject_type,subject_id,created_at) \
             VALUES($1,$2,$3,$4,'company_file',$5,$6)",
        )
        .bind(Uuid::now_v7())
        .bind(space_id.into_uuid())
        .bind(actor.into_uuid())
        .bind(action)
        .bind(file_uuid)
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
        .bind(json!({"company_file_id": file_uuid}))
        .bind(now)
        .execute(&mut *self.connection)
        .await
        .map_err(map_sqlx)?;
        Ok(())
    }
}

fn company_file_from_row(row: &sqlx::postgres::PgRow) -> Result<CompanyFile, ApplicationError> {
    CompanyFile::rehydrate(CompanyFileSnapshot {
        id: CompanyFileId::from_uuid(row.get("id")),
        space_id: SpaceId::from_uuid(row.get("space_id")),
        name: row.get("name"),
        media_type: row.get("media_type"),
        length: u64::try_from(row.get::<i64, _>("length"))
            .map_err(|_| ApplicationError::Internal)?,
        sha256: row
            .get::<Vec<u8>, _>("sha256")
            .try_into()
            .map_err(|_| ApplicationError::Internal)?,
        uploader_member_id: MemberId::from_uuid(row.get("uploader_member_id")),
        created_at: row.get("created_at"),
        deleted_at: row.get("deleted_at"),
    })
    .map_err(ApplicationError::from)
}

#[async_trait]
impl CompanyFileTransaction for PostgresTransaction {
    async fn company_file(
        &mut self,
        id: CompanyFileId,
    ) -> Result<Option<CompanyFile>, ApplicationError> {
        self.company_file(id).await
    }
    async fn list_company_files(
        &mut self,
        space_id: SpaceId,
    ) -> Result<Vec<(CompanyFile, String)>, ApplicationError> {
        self.list_company_files(space_id).await
    }
    async fn company_file_name_exists(
        &mut self,
        space_id: SpaceId,
        name: &str,
    ) -> Result<bool, ApplicationError> {
        self.company_file_name_exists(space_id, name).await
    }
    async fn insert_company_file(&mut self, file: &CompanyFile) -> Result<(), ApplicationError> {
        self.insert_company_file(file).await
    }
    async fn save_company_file(&mut self, file: &CompanyFile) -> Result<(), ApplicationError> {
        self.save_company_file(file).await
    }
    async fn record_company_file_write(
        &mut self,
        space_id: SpaceId,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        file_id: CompanyFileId,
        event_kind: &str,
        now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.record_company_file_write(space_id, actor, action, key, file_id, event_kind, now)
            .await
    }
}
