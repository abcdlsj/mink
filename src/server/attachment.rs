use axum::{
    Json,
    body::Body,
    extract::{Path, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
};
use axum_extra::extract::CookieJar;
use futures_util::StreamExt;
use object_store::{ObjectStore, path::Path as ObjectPath};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use sqlx::{FromRow, Postgres, Transaction};
use time::OffsetDateTime;
use uuid::Uuid;

use super::{AppState, api_error::ApiError, auth, computer_registry, idempotency, member};

const UPLOAD_PART_BYTES: usize = 8 * 1024 * 1024;

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct AttachmentResponse {
    pub id: Uuid,
    pub space_id: Uuid,
    pub uploader_member_id: Uuid,
    pub original_name: String,
    pub media_type: String,
    pub size: Option<i64>,
    pub sha256: Option<String>,
    pub status: String,
    pub upload_path: Option<String>,
    pub download_path: Option<String>,
    pub created_at: OffsetDateTime,
}

#[derive(FromRow)]
struct AttachmentRow {
    id: Uuid,
    space_id: Uuid,
    uploader_member_id: Uuid,
    original_name: String,
    media_type: String,
    size: Option<i64>,
    sha256: Option<Vec<u8>>,
    status: String,
    created_at: OffsetDateTime,
}

impl From<AttachmentRow> for AttachmentResponse {
    fn from(row: AttachmentRow) -> Self {
        let ready = row.status == "ready";
        Self {
            id: row.id,
            space_id: row.space_id,
            uploader_member_id: row.uploader_member_id,
            original_name: row.original_name,
            media_type: row.media_type,
            size: row.size,
            sha256: row.sha256.map(hex::encode),
            status: row.status,
            upload_path: (!ready).then(|| format!("/api/v1/attachments/{}/content", row.id)),
            download_path: ready.then(|| format!("/api/v1/attachments/{}/download", row.id)),
            created_at: row.created_at,
        }
    }
}

#[derive(Deserialize, Serialize)]
pub struct CreateUploadRequest {
    pub space_id: Uuid,
    pub original_name: String,
    pub media_type: String,
}

pub async fn create_upload(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Json(mut request): Json<CreateUploadRequest>,
) -> Result<(StatusCode, Json<AttachmentResponse>), ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    request.original_name = request.original_name.trim().to_owned();
    request.media_type = request.media_type.trim().to_owned();
    validate_metadata(&request)?;
    let request_hash = idempotency::request_hash(&request)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let actor = member::require_actor_tx(&mut transaction, user.id, request.space_id).await?;
    let scope = format!(
        "space:{}:member:{}:attachment:create",
        request.space_id, actor.id
    );
    if let Some((status, response)) =
        idempotency::begin::<AttachmentResponse>(&mut transaction, &scope, key, &request_hash)
            .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok((status, Json(response)));
    }
    let attachment_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let object_key = object_key(request.space_id, attachment_id);
    let row: AttachmentRow = sqlx::query_as(
        "INSERT INTO attachments \
         (id, space_id, uploader_member_id, original_name, media_type, object_key, created_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $7) \
         RETURNING id, space_id, uploader_member_id, original_name, media_type, size, sha256, \
                   status, created_at",
    )
    .bind(attachment_id)
    .bind(request.space_id)
    .bind(actor.id)
    .bind(request.original_name)
    .bind(request.media_type)
    .bind(object_key.as_ref())
    .bind(now)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let response = AttachmentResponse::from(row);
    idempotency::finish(
        &mut transaction,
        &scope,
        key,
        StatusCode::CREATED,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok((StatusCode::CREATED, Json(response)))
}

pub async fn upload_content(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(attachment_id): Path<Uuid>,
    body: Body,
) -> Result<StatusCode, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let object_key: String = sqlx::query_scalar(
        "SELECT attachments.object_key FROM attachments \
         JOIN human_members ON human_members.member_id = attachments.uploader_member_id \
         WHERE attachments.id = $1 AND human_members.user_id = $2 \
           AND attachments.status = 'uploading'",
    )
    .bind(attachment_id)
    .bind(user.id)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| {
        ApiError::forbidden(
            "attachment_upload_denied",
            "Only the uploader can write an uploading Attachment",
        )
    })?;

    stream_upload(
        state.attachment_store.as_ref(),
        &ObjectPath::from(object_key),
        body,
        state.attachment_max_bytes,
    )
    .await?;
    Ok(StatusCode::NO_CONTENT)
}

#[derive(Deserialize, Serialize)]
pub struct CompleteUploadRequest {
    pub size: u64,
    pub sha256: String,
}

pub async fn complete_upload(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(attachment_id): Path<Uuid>,
    Json(request): Json<CompleteUploadRequest>,
) -> Result<Json<AttachmentResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    if request.size > state.attachment_max_bytes {
        return Err(ApiError::validation(
            "attachment_too_large",
            "Attachment exceeds the configured size limit",
        ));
    }
    let expected_sha = decode_sha256(&request.sha256)?;
    let request_hash = idempotency::request_hash(&request)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let row: AttachmentRow = sqlx::query_as(
        "SELECT attachments.id, attachments.space_id, attachments.uploader_member_id, \
                attachments.original_name, attachments.media_type, attachments.size, \
                attachments.sha256, attachments.status, attachments.created_at \
         FROM attachments \
         JOIN human_members ON human_members.member_id = attachments.uploader_member_id \
         WHERE attachments.id = $1 AND human_members.user_id = $2 FOR UPDATE OF attachments",
    )
    .bind(attachment_id)
    .bind(user.id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| ApiError::not_found("attachment_not_found", "Attachment was not found"))?;
    let scope = format!("attachment:{attachment_id}:complete");
    if let Some((_status, response)) =
        idempotency::begin::<AttachmentResponse>(&mut transaction, &scope, key, &request_hash)
            .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }
    if row.status == "ready" {
        if row.size == i64::try_from(request.size).ok()
            && row.sha256.as_deref() == Some(expected_sha.as_slice())
        {
            let response = AttachmentResponse::from(row);
            idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
            transaction.commit().await.map_err(ApiError::database)?;
            return Ok(Json(response));
        }
        return Err(ApiError::conflict(
            "attachment_integrity_conflict",
            "Attachment is already ready with different integrity metadata",
        ));
    }
    if row.status != "uploading" {
        return Err(ApiError::conflict(
            "attachment_not_uploading",
            "Attachment upload has already finished",
        ));
    }
    let object_key: String = sqlx::query_scalar("SELECT object_key FROM attachments WHERE id = $1")
        .bind(attachment_id)
        .fetch_one(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    let (actual_size, actual_sha) = inspect_object(
        state.attachment_store.as_ref(),
        &ObjectPath::from(object_key),
        state.attachment_max_bytes,
    )
    .await?;
    if actual_size != request.size || actual_sha.as_slice() != expected_sha {
        return Err(ApiError::validation(
            "attachment_integrity_mismatch",
            "Uploaded content does not match the declared size and SHA-256",
        ));
    }
    let ready: AttachmentRow = sqlx::query_as(
        "UPDATE attachments SET size = $2, sha256 = $3, status = 'ready' WHERE id = $1 \
         RETURNING id, space_id, uploader_member_id, original_name, media_type, size, sha256, \
                   status, created_at",
    )
    .bind(attachment_id)
    .bind(i64::try_from(actual_size).map_err(|_| ApiError::Internal)?)
    .bind(actual_sha.to_vec())
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let now = OffsetDateTime::now_utc();
    sqlx::query(
        "INSERT INTO outbox_events (id, topic, aggregate_id, payload_json, created_at) \
         VALUES ($1, 'attachment.ready', $2, $3, $4)",
    )
    .bind(Uuid::now_v7())
    .bind(attachment_id)
    .bind(serde_json::json!({ "space_id": ready.space_id, "attachment_id": attachment_id }))
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let response = AttachmentResponse::from(ready);
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(response))
}

pub async fn download(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(attachment_id): Path<Uuid>,
) -> Result<Response, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let metadata: Option<(String, String, String, i64)> = sqlx::query_as(
        "SELECT attachments.object_key, attachments.original_name, attachments.media_type, \
                attachments.size \
         FROM attachments \
         JOIN message_attachments ON message_attachments.attachment_id = attachments.id \
         JOIN messages ON messages.id = message_attachments.message_id \
         JOIN channel_members ON channel_members.channel_id = messages.channel_id \
         JOIN human_members ON human_members.member_id = channel_members.member_id \
         WHERE attachments.id = $1 AND attachments.status = 'ready' \
           AND attachments.deleted_at IS NULL AND human_members.user_id = $2",
    )
    .bind(attachment_id)
    .bind(user.id)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?;
    let (object_key, original_name, media_type, size) = metadata.ok_or_else(|| {
        ApiError::forbidden(
            "attachment_download_denied",
            "Attachment is not visible from an accessible Message",
        )
    })?;
    let result = state
        .attachment_store
        .get(&ObjectPath::from(object_key))
        .await
        .map_err(storage_error)?;
    let stream = result
        .into_stream()
        .map(|chunk| chunk.map_err(|error| std::io::Error::other(error.to_string())));
    let mut response = Body::from_stream(stream).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_str(&media_type)
            .unwrap_or_else(|_| HeaderValue::from_static("application/octet-stream")),
    );
    response.headers_mut().insert(
        header::CONTENT_LENGTH,
        HeaderValue::from_str(&size.to_string()).map_err(|_| ApiError::Internal)?,
    );
    let safe_name = original_name.replace(['\r', '\n', '"'], "_");
    let disposition = format!("attachment; filename=\"{safe_name}\"");
    response.headers_mut().insert(
        header::CONTENT_DISPOSITION,
        HeaderValue::from_str(&disposition).map_err(|_| ApiError::Internal)?,
    );
    Ok(response)
}

#[derive(Deserialize, Serialize)]
pub struct AgentCreateUploadRequest {
    pub original_name: String,
    pub media_type: String,
}

pub async fn agent_create_upload(
    State(state): State<std::sync::Arc<AppState>>,
    headers: HeaderMap,
    key: idempotency::IdempotencyKey,
    Path((computer_id, agent_id, run_id)): Path<(Uuid, Uuid, Uuid)>,
    Json(mut request): Json<AgentCreateUploadRequest>,
) -> Result<(StatusCode, Json<AttachmentResponse>), ApiError> {
    let space_id = computer_registry::require_active_agent_run(
        &state,
        &headers,
        computer_id,
        agent_id,
        run_id,
    )
    .await?;
    request.original_name = request.original_name.trim().to_owned();
    request.media_type = request.media_type.trim().to_owned();
    validate_metadata(&CreateUploadRequest {
        space_id,
        original_name: request.original_name.clone(),
        media_type: request.media_type.clone(),
    })?;
    let request_hash = idempotency::request_hash(&request)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let scope = format!("space:{space_id}:member:{agent_id}:attachment:create");
    if let Some((status, response)) =
        idempotency::begin::<AttachmentResponse>(&mut transaction, &scope, key, &request_hash)
            .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok((status, Json(response)));
    }
    let attachment_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let row: AttachmentRow = sqlx::query_as(
        "INSERT INTO attachments \
         (id, space_id, uploader_member_id, original_name, media_type, object_key, created_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $7) \
         RETURNING id, space_id, uploader_member_id, original_name, media_type, size, sha256, \
                   status, created_at",
    )
    .bind(attachment_id)
    .bind(space_id)
    .bind(agent_id)
    .bind(request.original_name)
    .bind(request.media_type)
    .bind(object_key(space_id, attachment_id).as_ref())
    .bind(now)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let response = AttachmentResponse::from(row);
    idempotency::finish(
        &mut transaction,
        &scope,
        key,
        StatusCode::CREATED,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok((StatusCode::CREATED, Json(response)))
}

pub async fn agent_upload_content(
    State(state): State<std::sync::Arc<AppState>>,
    headers: HeaderMap,
    Path((computer_id, agent_id, run_id, attachment_id)): Path<(Uuid, Uuid, Uuid, Uuid)>,
    body: Body,
) -> Result<StatusCode, ApiError> {
    computer_registry::require_active_agent_run(&state, &headers, computer_id, agent_id, run_id)
        .await?;
    let object_key: String = sqlx::query_scalar(
        "SELECT object_key FROM attachments WHERE id = $1 AND uploader_member_id = $2 \
         AND status = 'uploading' AND deleted_at IS NULL",
    )
    .bind(attachment_id)
    .bind(agent_id)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| {
        ApiError::forbidden(
            "attachment_upload_denied",
            "Only the uploader can write an uploading Attachment",
        )
    })?;
    stream_upload(
        state.attachment_store.as_ref(),
        &ObjectPath::from(object_key),
        body,
        state.attachment_max_bytes,
    )
    .await?;
    Ok(StatusCode::NO_CONTENT)
}

pub async fn agent_complete_upload(
    State(state): State<std::sync::Arc<AppState>>,
    headers: HeaderMap,
    key: idempotency::IdempotencyKey,
    Path((computer_id, agent_id, run_id, attachment_id)): Path<(Uuid, Uuid, Uuid, Uuid)>,
    Json(request): Json<CompleteUploadRequest>,
) -> Result<Json<AttachmentResponse>, ApiError> {
    computer_registry::require_active_agent_run(&state, &headers, computer_id, agent_id, run_id)
        .await?;
    if request.size > state.attachment_max_bytes {
        return Err(ApiError::validation(
            "attachment_too_large",
            "Attachment exceeds the configured size limit",
        ));
    }
    let expected_sha = decode_sha256(&request.sha256)?;
    let request_hash = idempotency::request_hash(&request)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let row: AttachmentRow = sqlx::query_as(
        "SELECT id, space_id, uploader_member_id, original_name, media_type, size, sha256, \
                status, created_at FROM attachments \
         WHERE id = $1 AND uploader_member_id = $2 FOR UPDATE",
    )
    .bind(attachment_id)
    .bind(agent_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| ApiError::not_found("attachment_not_found", "Attachment was not found"))?;
    let scope = format!("attachment:{attachment_id}:complete");
    if let Some((_status, response)) =
        idempotency::begin::<AttachmentResponse>(&mut transaction, &scope, key, &request_hash)
            .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }
    if row.status == "ready" {
        if row.size == i64::try_from(request.size).ok()
            && row.sha256.as_deref() == Some(expected_sha.as_slice())
        {
            let response = AttachmentResponse::from(row);
            idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
            transaction.commit().await.map_err(ApiError::database)?;
            return Ok(Json(response));
        }
        return Err(ApiError::conflict(
            "attachment_integrity_conflict",
            "Attachment is already ready with different integrity metadata",
        ));
    }
    if row.status != "uploading" {
        return Err(ApiError::conflict(
            "attachment_not_uploading",
            "Attachment upload has already finished",
        ));
    }
    let object_key: String = sqlx::query_scalar("SELECT object_key FROM attachments WHERE id = $1")
        .bind(attachment_id)
        .fetch_one(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    let (actual_size, actual_sha) = inspect_object(
        state.attachment_store.as_ref(),
        &ObjectPath::from(object_key),
        state.attachment_max_bytes,
    )
    .await?;
    if actual_size != request.size || actual_sha.as_slice() != expected_sha {
        return Err(ApiError::validation(
            "attachment_integrity_mismatch",
            "Uploaded content does not match the declared size and SHA-256",
        ));
    }
    let ready: AttachmentRow = sqlx::query_as(
        "UPDATE attachments SET size = $2, sha256 = $3, status = 'ready' WHERE id = $1 \
         RETURNING id, space_id, uploader_member_id, original_name, media_type, size, sha256, \
                   status, created_at",
    )
    .bind(attachment_id)
    .bind(i64::try_from(actual_size).map_err(|_| ApiError::Internal)?)
    .bind(actual_sha.to_vec())
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let response = AttachmentResponse::from(ready);
    sqlx::query(
        "INSERT INTO outbox_events (id, topic, aggregate_id, payload_json, created_at) \
         VALUES ($1, 'attachment.ready', $2, $3, $4)",
    )
    .bind(Uuid::now_v7())
    .bind(attachment_id)
    .bind(serde_json::json!({
        "space_id": response.space_id,
        "attachment_id": attachment_id
    }))
    .bind(OffsetDateTime::now_utc())
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(response))
}

pub async fn agent_info(
    State(state): State<std::sync::Arc<AppState>>,
    headers: HeaderMap,
    Path((computer_id, agent_id, run_id, attachment_id)): Path<(Uuid, Uuid, Uuid, Uuid)>,
) -> Result<Json<AttachmentResponse>, ApiError> {
    computer_registry::require_active_agent_run(&state, &headers, computer_id, agent_id, run_id)
        .await?;
    let row: AttachmentRow = sqlx::query_as(
        "SELECT attachments.id, attachments.space_id, attachments.uploader_member_id, \
                attachments.original_name, attachments.media_type, attachments.size, \
                attachments.sha256, attachments.status, attachments.created_at \
         FROM attachments WHERE attachments.id = $1 AND attachments.deleted_at IS NULL \
           AND (attachments.uploader_member_id = $2 OR EXISTS ( \
             SELECT 1 FROM message_attachments \
             JOIN messages ON messages.id = message_attachments.message_id \
             JOIN channel_members ON channel_members.channel_id = messages.channel_id \
             WHERE message_attachments.attachment_id = attachments.id \
               AND channel_members.member_id = $2))",
    )
    .bind(attachment_id)
    .bind(agent_id)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| ApiError::not_found("attachment_not_found", "Attachment was not found"))?;
    Ok(Json(AttachmentResponse::from(row)))
}

pub async fn agent_download(
    State(state): State<std::sync::Arc<AppState>>,
    headers: HeaderMap,
    Path((computer_id, agent_id, run_id, attachment_id)): Path<(Uuid, Uuid, Uuid, Uuid)>,
) -> Result<Response, ApiError> {
    computer_registry::require_active_agent_run(&state, &headers, computer_id, agent_id, run_id)
        .await?;
    let metadata: Option<(String, String, String, i64)> = sqlx::query_as(
        "SELECT attachments.object_key, attachments.original_name, attachments.media_type, \
                attachments.size FROM attachments \
         JOIN message_attachments ON message_attachments.attachment_id = attachments.id \
         JOIN messages ON messages.id = message_attachments.message_id \
         JOIN channel_members ON channel_members.channel_id = messages.channel_id \
         WHERE attachments.id = $1 AND attachments.status = 'ready' \
           AND attachments.deleted_at IS NULL AND channel_members.member_id = $2",
    )
    .bind(attachment_id)
    .bind(agent_id)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?;
    let (object_key, original_name, media_type, size) = metadata.ok_or_else(|| {
        ApiError::forbidden(
            "attachment_download_denied",
            "Attachment is not visible from an accessible Message",
        )
    })?;
    attachment_download_response(&state, object_key, original_name, media_type, size).await
}

async fn attachment_download_response(
    state: &AppState,
    object_key: String,
    original_name: String,
    media_type: String,
    size: i64,
) -> Result<Response, ApiError> {
    let result = state
        .attachment_store
        .get(&ObjectPath::from(object_key))
        .await
        .map_err(storage_error)?;
    let stream = result
        .into_stream()
        .map(|chunk| chunk.map_err(|error| std::io::Error::other(error.to_string())));
    let mut response = Body::from_stream(stream).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_str(&media_type)
            .unwrap_or_else(|_| HeaderValue::from_static("application/octet-stream")),
    );
    response.headers_mut().insert(
        header::CONTENT_LENGTH,
        HeaderValue::from_str(&size.to_string()).map_err(|_| ApiError::Internal)?,
    );
    let safe_name = original_name.replace(['\r', '\n', '"'], "_");
    response.headers_mut().insert(
        header::CONTENT_DISPOSITION,
        HeaderValue::from_str(&format!("attachment; filename=\"{safe_name}\""))
            .map_err(|_| ApiError::Internal)?,
    );
    Ok(response)
}

pub(super) async fn attach_to_message(
    transaction: &mut Transaction<'_, Postgres>,
    message_id: Uuid,
    space_id: Uuid,
    author_member_id: Uuid,
    attachment_ids: &[Uuid],
) -> Result<(), ApiError> {
    for (position, attachment_id) in attachment_ids.iter().enumerate() {
        let inserted = sqlx::query(
            "INSERT INTO message_attachments \
             (message_id, attachment_id, channel_id, space_id, position) \
             SELECT $1, attachments.id, messages.channel_id, $2, $3 \
             FROM attachments JOIN messages ON messages.id = $1 AND messages.space_id = $2 \
             WHERE attachments.id = $4 AND attachments.space_id = $2 \
               AND attachments.uploader_member_id = $5 AND attachments.status = 'ready' \
               AND attachments.deleted_at IS NULL",
        )
        .bind(message_id)
        .bind(space_id)
        .bind(i32::try_from(position).map_err(|_| ApiError::Internal)?)
        .bind(attachment_id)
        .bind(author_member_id)
        .execute(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
        if inserted.rows_affected() != 1 {
            return Err(ApiError::validation(
                "invalid_attachment",
                "Attachment must be ready, unlinked, in this Space, and uploaded by the author",
            ));
        }
    }
    Ok(())
}

pub(super) async fn attachments_for_message(
    transaction: &mut Transaction<'_, Postgres>,
    message_id: Uuid,
) -> Result<Vec<AttachmentResponse>, ApiError> {
    let rows = sqlx::query_as::<_, AttachmentRow>(
        "SELECT attachments.id, attachments.space_id, attachments.uploader_member_id, \
                attachments.original_name, attachments.media_type, attachments.size, \
                attachments.sha256, attachments.status, attachments.created_at \
         FROM message_attachments \
         JOIN attachments ON attachments.id = message_attachments.attachment_id \
         WHERE message_attachments.message_id = $1 ORDER BY message_attachments.position",
    )
    .bind(message_id)
    .fetch_all(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(rows.into_iter().map(Into::into).collect())
}

pub(super) async fn attachments_for_message_pool(
    database: &sqlx::PgPool,
    message_id: Uuid,
) -> Result<Vec<AttachmentResponse>, ApiError> {
    let rows = sqlx::query_as::<_, AttachmentRow>(
        "SELECT attachments.id, attachments.space_id, attachments.uploader_member_id, \
                attachments.original_name, attachments.media_type, attachments.size, \
                attachments.sha256, attachments.status, attachments.created_at \
         FROM message_attachments \
         JOIN attachments ON attachments.id = message_attachments.attachment_id \
         WHERE message_attachments.message_id = $1 ORDER BY message_attachments.position",
    )
    .bind(message_id)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    Ok(rows.into_iter().map(Into::into).collect())
}

fn validate_metadata(request: &CreateUploadRequest) -> Result<(), ApiError> {
    if !(1..=255).contains(&request.original_name.chars().count()) {
        return Err(ApiError::validation(
            "invalid_attachment_name",
            "Attachment name must contain 1 to 255 characters",
        ));
    }
    if !(1..=255).contains(&request.media_type.chars().count())
        || request.media_type.contains(['\r', '\n'])
    {
        return Err(ApiError::validation(
            "invalid_media_type",
            "Attachment media type is invalid",
        ));
    }
    Ok(())
}

async fn stream_upload(
    store: &dyn ObjectStore,
    location: &ObjectPath,
    body: Body,
    max_bytes: u64,
) -> Result<(), ApiError> {
    let mut stream = body.into_data_stream();
    let mut upload = store.put_multipart(location).await.map_err(storage_error)?;
    let mut buffer = Vec::with_capacity(UPLOAD_PART_BYTES);
    let mut total = 0_u64;
    while let Some(chunk) = stream.next().await {
        let chunk = chunk.map_err(|_| {
            ApiError::validation("invalid_upload_body", "Attachment upload body is invalid")
        })?;
        total = total.checked_add(chunk.len() as u64).ok_or_else(|| {
            ApiError::validation("attachment_too_large", "Attachment is too large")
        })?;
        if total > max_bytes {
            let _ = upload.abort().await;
            return Err(ApiError::validation(
                "attachment_too_large",
                "Attachment exceeds the configured size limit",
            ));
        }
        buffer.extend_from_slice(&chunk);
        while buffer.len() >= UPLOAD_PART_BYTES {
            let remainder = buffer.split_off(UPLOAD_PART_BYTES);
            upload
                .put_part(std::mem::replace(&mut buffer, remainder).into())
                .await
                .map_err(storage_error)?;
        }
    }
    if total == 0 {
        upload.abort().await.map_err(storage_error)?;
        store
            .put(location, Vec::<u8>::new().into())
            .await
            .map_err(storage_error)?;
    } else {
        if !buffer.is_empty() {
            upload
                .put_part(buffer.into())
                .await
                .map_err(storage_error)?;
        }
        upload.complete().await.map_err(storage_error)?;
    }
    Ok(())
}

async fn inspect_object(
    store: &dyn ObjectStore,
    location: &ObjectPath,
    max_bytes: u64,
) -> Result<(u64, [u8; 32]), ApiError> {
    let result = store.get(location).await.map_err(|error| match error {
        object_store::Error::NotFound { .. } => ApiError::conflict(
            "attachment_content_missing",
            "Attachment content must be uploaded before completion",
        ),
        other => storage_error(other),
    })?;
    let mut stream = result.into_stream();
    let mut size = 0_u64;
    let mut digest = Sha256::new();
    while let Some(chunk) = stream.next().await {
        let chunk = chunk.map_err(storage_error)?;
        size = size
            .checked_add(chunk.len() as u64)
            .ok_or(ApiError::Internal)?;
        if size > max_bytes {
            return Err(ApiError::validation(
                "attachment_too_large",
                "Attachment exceeds the configured size limit",
            ));
        }
        digest.update(&chunk);
    }
    Ok((size, digest.finalize().into()))
}

fn decode_sha256(value: &str) -> Result<[u8; 32], ApiError> {
    let bytes = hex::decode(value).map_err(|_| {
        ApiError::validation(
            "invalid_attachment_sha256",
            "Attachment SHA-256 must be 64 hexadecimal characters",
        )
    })?;
    bytes.try_into().map_err(|_| {
        ApiError::validation(
            "invalid_attachment_sha256",
            "Attachment SHA-256 must be 64 hexadecimal characters",
        )
    })
}

fn object_key(space_id: Uuid, attachment_id: Uuid) -> ObjectPath {
    ObjectPath::from(format!("{space_id}/{attachment_id}"))
}

fn storage_error(error: object_store::Error) -> ApiError {
    tracing::error!(error = %error, "Attachment storage operation failed");
    ApiError::Internal
}
