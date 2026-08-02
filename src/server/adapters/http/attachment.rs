use super::*;

#[derive(Deserialize)]
pub(super) struct CreateUploadBody {
    pub(super) space_id: Uuid,
    pub(super) original_name: String,
    pub(super) media_type: String,
}

#[derive(Deserialize)]
pub(super) struct AgentCreateUploadBody {
    pub(super) original_name: String,
    pub(super) media_type: String,
}

#[derive(Deserialize)]
pub(super) struct CompleteUploadBody {
    pub(super) size: u64,
    pub(super) sha256: String,
}

pub(super) async fn agent_create_upload(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, agent_id, run_id)): Path<(Uuid, Uuid, Uuid)>,
    Json(body): Json<AgentCreateUploadBody>,
) -> Result<(StatusCode, Json<AttachmentResponse>), ApiError> {
    let space_id =
        require_active_agent_run(&state, &headers, computer_id, agent_id, run_id).await?;
    let key = idempotency_header(&headers)?;
    let mut storage = state.storage.clone();
    let opened = OpenUpload::execute(
        &mut storage,
        OpenUploadInput {
            attachment_id: AttachmentId::from_uuid(Uuid::now_v7()),
            space_id: SpaceId::from_uuid(space_id),
            uploader_member_id: MemberId::from_uuid(agent_id),
            name: &body.original_name,
            media_type: &body.media_type,
            idempotency_key: IdempotencyKey::from_uuid(key),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    let status = if opened.created {
        StatusCode::CREATED
    } else {
        StatusCode::OK
    };
    Ok((
        status,
        Json(attachment_response(
            &opened.attachment,
            AttachmentPath::Upload,
        )),
    ))
}

pub(super) async fn agent_upload_content(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, agent_id, run_id, attachment_id)): Path<(Uuid, Uuid, Uuid, Uuid)>,
    body: Bytes,
) -> Result<StatusCode, ApiError> {
    require_active_agent_run(&state, &headers, computer_id, agent_id, run_id).await?;
    let mut storage = state.storage.clone();
    WriteUploadContent::execute(
        &mut storage,
        state.objects.as_ref(),
        WriteUploadContentInput {
            attachment_id: AttachmentId::from_uuid(attachment_id),
            uploader_member_id: MemberId::from_uuid(agent_id),
            content: body.to_vec(),
            max_bytes: state.attachment_max_bytes,
        },
    )
    .await
    .map_err(application_error)?;
    Ok(StatusCode::NO_CONTENT)
}

pub(super) async fn agent_complete_upload(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, agent_id, run_id, attachment_id)): Path<(Uuid, Uuid, Uuid, Uuid)>,
    Json(body): Json<CompleteUploadBody>,
) -> Result<Json<AttachmentResponse>, ApiError> {
    require_active_agent_run(&state, &headers, computer_id, agent_id, run_id).await?;
    let key = idempotency_header(&headers)?;
    let mut storage = state.storage.clone();
    let attachment = CompleteUpload::execute(
        &mut storage,
        state.objects.as_ref(),
        CompleteUploadInput {
            attachment_id: AttachmentId::from_uuid(attachment_id),
            uploader_member_id: MemberId::from_uuid(agent_id),
            declared: DeclaredContent {
                size: body.size,
                sha256_hex: body.sha256,
            },
            idempotency_key: IdempotencyKey::from_uuid(key),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(attachment_response(
        &attachment,
        AttachmentPath::Download,
    )))
}

pub(super) async fn agent_download_attachment(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, agent_id, run_id, attachment_id)): Path<(Uuid, Uuid, Uuid, Uuid)>,
) -> Result<Bytes, ApiError> {
    require_active_agent_run(&state, &headers, computer_id, agent_id, run_id).await?;
    let mut storage = state.storage.clone();
    let downloaded = ReadAttachment::for_uploader_or_member(
        &mut storage,
        state.objects.as_ref(),
        AttachmentId::from_uuid(attachment_id),
        MemberId::from_uuid(agent_id),
    )
    .await
    .map_err(application_error)?;
    Ok(Bytes::from(downloaded.content))
}

pub(super) fn idempotency_header(headers: &HeaderMap) -> Result<Uuid, ApiError> {
    headers
        .get("idempotency-key")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| Uuid::parse_str(value).ok())
        .ok_or_else(|| ApiError::invalid("Idempotency-Key must be a UUID"))
}

pub(super) async fn create_upload(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Json(body): Json<CreateUploadBody>,
) -> Result<(StatusCode, Json<AttachmentResponse>), ApiError> {
    let member = current_member(&state, &jar, body.space_id).await?;
    let key = idempotency_header(&headers)?;
    let mut storage = state.storage.clone();
    let opened = OpenUpload::execute(
        &mut storage,
        OpenUploadInput {
            attachment_id: AttachmentId::from_uuid(Uuid::now_v7()),
            space_id: SpaceId::from_uuid(body.space_id),
            uploader_member_id: MemberId::from_uuid(member),
            name: &body.original_name,
            media_type: &body.media_type,
            idempotency_key: IdempotencyKey::from_uuid(key),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    let status = if opened.created {
        StatusCode::CREATED
    } else {
        StatusCode::OK
    };
    Ok((
        status,
        Json(attachment_response(
            &opened.attachment,
            AttachmentPath::Upload,
        )),
    ))
}

pub(super) async fn upload_content(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(attachment_id): Path<Uuid>,
    body: Bytes,
) -> Result<StatusCode, ApiError> {
    let member = attachment_space_member(&state, &jar, attachment_id).await?;
    let mut storage = state.storage.clone();
    WriteUploadContent::execute(
        &mut storage,
        state.objects.as_ref(),
        WriteUploadContentInput {
            attachment_id: AttachmentId::from_uuid(attachment_id),
            uploader_member_id: MemberId::from_uuid(member),
            content: body.to_vec(),
            max_bytes: state.attachment_max_bytes,
        },
    )
    .await
    .map_err(application_error)?;
    Ok(StatusCode::NO_CONTENT)
}

pub(super) async fn complete_upload(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(attachment_id): Path<Uuid>,
    Json(body): Json<CompleteUploadBody>,
) -> Result<Json<AttachmentResponse>, ApiError> {
    let member = attachment_space_member(&state, &jar, attachment_id).await?;
    let key = idempotency_header(&headers)?;
    let mut storage = state.storage.clone();
    let attachment = CompleteUpload::execute(
        &mut storage,
        state.objects.as_ref(),
        CompleteUploadInput {
            attachment_id: AttachmentId::from_uuid(attachment_id),
            uploader_member_id: MemberId::from_uuid(member),
            declared: DeclaredContent {
                size: body.size,
                sha256_hex: body.sha256,
            },
            idempotency_key: IdempotencyKey::from_uuid(key),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(attachment_response(
        &attachment,
        AttachmentPath::Download,
    )))
}

pub(super) async fn download_attachment(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(attachment_id): Path<Uuid>,
) -> Result<Response, ApiError> {
    let member = attachment_space_member(&state, &jar, attachment_id).await?;
    let mut storage = state.storage.clone();
    let downloaded = ReadAttachment::for_member(
        &mut storage,
        state.objects.as_ref(),
        AttachmentId::from_uuid(attachment_id),
        MemberId::from_uuid(member),
    )
    .await
    .map_err(application_error)?;
    attachment_download_response(&downloaded)
}

pub(super) fn attachment_download_response(
    downloaded: &AttachmentContent,
) -> Result<Response, ApiError> {
    let disposition = HeaderValue::from_str(&format!(
        "attachment; filename=\"{}\"",
        downloaded.attachment.header_safe_name()
    ))
    .map_err(|_| ApiError::invalid("Attachment name cannot be represented in a header"))?;
    let media_type = HeaderValue::from_str(downloaded.attachment.view().media_type)
        .map_err(|_| ApiError::invalid("Attachment media type is invalid"))?;
    Ok((
        [
            (header::CONTENT_DISPOSITION, disposition),
            (header::CONTENT_TYPE, media_type),
        ],
        Bytes::from(downloaded.content.clone()),
    )
        .into_response())
}

pub(super) enum AttachmentPath {
    Upload,
    Download,
}

pub(super) fn attachment_response(
    attachment: &Attachment,
    path: AttachmentPath,
) -> AttachmentResponse {
    let attachment = attachment.view();
    let id = attachment.id.into_uuid();
    let (upload_path, download_path) = match path {
        AttachmentPath::Upload => (Some(format!("/api/v1/attachments/{id}/content")), None),
        AttachmentPath::Download => (None, Some(format!("/api/v1/attachments/{id}/download"))),
    };
    AttachmentResponse {
        id,
        space_id: attachment.space_id.into_uuid(),
        uploader_member_id: attachment.uploader_member_id.into_uuid(),
        original_name: attachment.name.to_owned(),
        media_type: attachment.media_type.to_owned(),
        size: attachment.length,
        sha256: attachment.sha256.map(hex::encode),
        status: match attachment.status {
            AttachmentStatusKind::Uploading => AttachmentStatus::Uploading,
            AttachmentStatusKind::Ready => AttachmentStatus::Ready,
            AttachmentStatusKind::Deleted => AttachmentStatus::Deleted,
        },
        upload_path,
        download_path,
        created_at: timestamp(attachment.created_at),
    }
}

pub(super) async fn attachment_space_member(
    state: &RuntimeState,
    jar: &CookieJar,
    attachment_id: Uuid,
) -> Result<Uuid, ApiError> {
    let token = require_session_token(jar)?;
    let mut storage = state.storage.clone();
    let access = AuthorizeAttachmentAccess::execute(
        &mut storage,
        &token,
        AttachmentId::from_uuid(attachment_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(access.member_id.into_uuid())
}
