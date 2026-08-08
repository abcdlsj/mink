use super::*;

#[derive(Deserialize)]
pub(super) struct CompanyFileUploadQuery {
    name: String,
    #[serde(default = "default_company_media_type")]
    media_type: String,
}

fn default_company_media_type() -> String {
    "application/octet-stream".to_owned()
}

pub(super) async fn list_company_files(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<CompanyFileResponse>>, ApiError> {
    let member = current_member(&state, &jar, space_id).await?;
    let mut storage = state.storage.clone();
    let files = ListCompanyFiles::execute(
        &mut storage,
        ListCompanyFilesInput {
            space_id: SpaceId::from_uuid(space_id),
            actor_member_id: MemberId::from_uuid(member),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(
        files
            .iter()
            .map(|(file, uploader_name)| company_file_response(file, uploader_name))
            .collect(),
    ))
}

pub(super) async fn upload_company_file(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(space_id): Path<Uuid>,
    Query(query): Query<CompanyFileUploadQuery>,
    body: Bytes,
) -> Result<(StatusCode, Json<CompanyFileResponse>), ApiError> {
    let member = current_member(&state, &jar, space_id).await?;
    let key = idempotency_header(&headers)?;
    let mut storage = state.storage.clone();
    let file = UploadCompanyFile::execute(
        &mut storage,
        state.company_objects.as_ref(),
        UploadCompanyFileInput {
            file_id: CompanyFileId::from_uuid(Uuid::now_v7()),
            space_id: SpaceId::from_uuid(space_id),
            uploader_member_id: MemberId::from_uuid(member),
            name: &query.name,
            media_type: &query.media_type,
            content: body.to_vec(),
            max_bytes: state.attachment_max_bytes,
            idempotency_key: IdempotencyKey::from_uuid(key),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    let uploader_name = state
        .read
        .member_name(member)
        .await
        .ok()
        .flatten()
        .unwrap_or_default();
    Ok((
        StatusCode::CREATED,
        Json(company_file_response(&file, &uploader_name)),
    ))
}

pub(super) async fn download_company_file(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path((space_id, file_id)): Path<(Uuid, Uuid)>,
) -> Result<Response, ApiError> {
    let member = current_member(&state, &jar, space_id).await?;
    let mut storage = state.storage.clone();
    let downloaded = ReadCompanyFile::execute(
        &mut storage,
        state.company_objects.as_ref(),
        CompanyFileId::from_uuid(file_id),
        MemberId::from_uuid(member),
    )
    .await
    .map_err(application_error)?;
    if downloaded.file.view().space_id != SpaceId::from_uuid(space_id) {
        return Err(ApiError::not_found());
    }
    company_file_download_response(&downloaded)
}

pub(super) async fn delete_company_file(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path((space_id, file_id)): Path<(Uuid, Uuid)>,
) -> Result<StatusCode, ApiError> {
    let member = current_member(&state, &jar, space_id).await?;
    let key = idempotency_header(&headers)?;
    let mut storage = state.storage.clone();
    DeleteCompanyFile::execute(
        &mut storage,
        state.company_objects.as_ref(),
        DeleteCompanyFileInput {
            file_id: CompanyFileId::from_uuid(file_id),
            actor_member_id: MemberId::from_uuid(member),
            idempotency_key: IdempotencyKey::from_uuid(key),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(StatusCode::NO_CONTENT)
}

fn company_file_response(file: &CompanyFile, uploader_name: &str) -> CompanyFileResponse {
    let view = file.view();
    CompanyFileResponse {
        id: view.id.into_uuid(),
        space_id: view.space_id.into_uuid(),
        name: view.name.to_owned(),
        media_type: view.media_type.to_owned(),
        size: view.length,
        sha256: hex::encode(view.sha256),
        uploader_member_id: view.uploader_member_id.into_uuid(),
        uploader_name: uploader_name.to_owned(),
        download_path: format!(
            "/api/v1/spaces/{}/company/files/{}",
            view.space_id.into_uuid(),
            view.id.into_uuid()
        ),
        created_at: timestamp(view.created_at),
    }
}

fn company_file_download_response(downloaded: &CompanyFileContent) -> Result<Response, ApiError> {
    let view = downloaded.file.view();
    let disposition = if view.media_type.starts_with("image/")
        || view.media_type.starts_with("text/")
        || view.media_type == "application/pdf"
    {
        "inline"
    } else {
        "attachment"
    };
    let disposition = HeaderValue::from_str(&format!(
        "{disposition}; filename=\"{}\"",
        downloaded.file.header_safe_name()
    ))
    .map_err(|_| ApiError::invalid("Company file name cannot be represented in a header"))?;
    let media_type = HeaderValue::from_str(view.media_type)
        .map_err(|_| ApiError::invalid("Company file media type is invalid"))?;
    Ok((
        [
            (header::CONTENT_DISPOSITION, disposition),
            (header::CONTENT_TYPE, media_type),
        ],
        Bytes::from(downloaded.content.clone()),
    )
        .into_response())
}
