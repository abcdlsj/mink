use std::collections::HashMap;

use axum::{
    Json,
    extract::{Path, Query, State},
    http::StatusCode,
};
use axum_extra::extract::CookieJar;
use serde::{Deserialize, Serialize};
use sqlx::FromRow;
use time::OffsetDateTime;
use uuid::Uuid;

use super::{AppState, api_error::ApiError, attachment, auth, idempotency, member};

#[derive(Clone, Debug, Deserialize, Serialize, utoipa::ToSchema)]
pub struct MessageAuthor {
    pub id: Uuid,
    pub kind: String,
    pub display_name: String,
    pub handle: String,
}

#[derive(Clone, Debug, Deserialize, Serialize, sqlx::FromRow, utoipa::ToSchema)]
pub struct MessageTaskSummary {
    pub id: Uuid,
    pub title: String,
    pub status: String,
    pub assigned_agent_member_id: Option<Uuid>,
    pub assignee_name: Option<String>,
}

#[derive(Clone, Debug, Deserialize, Serialize, utoipa::ToSchema)]
pub struct MessageResponse {
    pub id: Uuid,
    pub channel_id: Uuid,
    pub seq: i64,
    pub author: MessageAuthor,
    pub body_markdown: String,
    pub mentions: Vec<Uuid>,
    pub attachments: Vec<attachment::AttachmentResponse>,
    #[serde(with = "time::serde::rfc3339")]
    pub created_at: OffsetDateTime,
    #[serde(with = "time::serde::rfc3339::option")]
    pub edited_at: Option<OffsetDateTime>,
    #[serde(with = "time::serde::rfc3339::option")]
    pub deleted_at: Option<OffsetDateTime>,
    pub thread_id: Option<i64>,
    pub reply_count: i64,
    pub task: Option<MessageTaskSummary>,
}

#[derive(FromRow)]
struct MessageRow {
    id: Uuid,
    channel_id: Uuid,
    seq: i64,
    author_id: Uuid,
    author_kind: String,
    author_display_name: String,
    author_handle: String,
    body_markdown: String,
    mentions: Vec<Uuid>,
    created_at: OffsetDateTime,
    edited_at: Option<OffsetDateTime>,
    deleted_at: Option<OffsetDateTime>,
    thread_id: Option<i64>,
    reply_count: i64,
}

impl From<MessageRow> for MessageResponse {
    fn from(row: MessageRow) -> Self {
        let body_markdown = if row.deleted_at.is_some() {
            "Message 已删除".to_owned()
        } else {
            row.body_markdown
        };
        Self {
            id: row.id,
            channel_id: row.channel_id,
            seq: row.seq,
            author: MessageAuthor {
                id: row.author_id,
                kind: row.author_kind,
                display_name: row.author_display_name,
                handle: row.author_handle,
            },
            body_markdown,
            mentions: row.mentions,
            attachments: Vec::new(),
            created_at: row.created_at,
            edited_at: row.edited_at,
            deleted_at: row.deleted_at,
            thread_id: row.thread_id,
            reply_count: row.reply_count,
            task: None,
        }
    }
}

#[derive(Deserialize, Serialize)]
pub struct UpdateMessageRequest {
    pub body_markdown: String,
}

pub async fn update(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(message_id): Path<Uuid>,
    Json(mut request): Json<UpdateMessageRequest>,
) -> Result<Json<MessageResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    request.body_markdown = request.body_markdown.trim().to_owned();
    if !(1..=20_000).contains(&request.body_markdown.chars().count()) {
        return Err(ApiError::validation(
            "invalid_message_body",
            "Message must contain 1 to 20000 characters",
        ));
    }
    let request_hash = idempotency::request_hash(&request)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let target: Option<(Uuid, Uuid, Uuid, Option<OffsetDateTime>)> = sqlx::query_as(
        "SELECT messages.space_id, messages.channel_id, messages.author_member_id, \
                messages.deleted_at FROM messages WHERE messages.id = $1 FOR UPDATE",
    )
    .bind(message_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let (space_id, channel_id, author_id, deleted_at) =
        target.ok_or_else(|| ApiError::not_found("message_not_found", "Message was not found"))?;
    let actor = member::require_actor_tx(&mut transaction, user.id, space_id).await?;
    super::channel_access::require_member(&mut transaction, channel_id, actor.id).await?;
    if actor.id != author_id {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Only the Message author can edit it",
        ));
    }
    if deleted_at.is_some() {
        return Err(ApiError::conflict(
            "message_deleted",
            "Deleted Message cannot be edited",
        ));
    }
    let scope = format!("message:{message_id}:update");
    if let Some((_status, response)) =
        idempotency::begin::<MessageResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }
    let now = OffsetDateTime::now_utc();
    sqlx::query("UPDATE messages SET body_markdown = $2, edited_at = $3 WHERE id = $1")
        .bind(message_id)
        .bind(request.body_markdown)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    publish_message_event(
        &mut transaction,
        message_id,
        space_id,
        channel_id,
        "message.updated",
        now,
    )
    .await?;
    let response = message_by_id(&mut transaction, message_id).await?;
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(response))
}

pub async fn delete(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(message_id): Path<Uuid>,
) -> Result<Json<MessageResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let target: Option<(Uuid, Uuid, Uuid)> = sqlx::query_as(
        "SELECT space_id, channel_id, author_member_id FROM messages WHERE id = $1 FOR UPDATE",
    )
    .bind(message_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let (space_id, channel_id, author_id) =
        target.ok_or_else(|| ApiError::not_found("message_not_found", "Message was not found"))?;
    let actor = member::require_actor_tx(&mut transaction, user.id, space_id).await?;
    super::channel_access::require_member(&mut transaction, channel_id, actor.id).await?;
    if actor.id != author_id && actor.access_level != "owner" && actor.access_level != "admin" {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Only the Message author, Owner, or Admin can delete it",
        ));
    }
    let scope = format!("message:{message_id}:delete");
    let request_hash = idempotency::request_hash(&message_id)?;
    if let Some((_status, response)) =
        idempotency::begin::<MessageResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }
    let now = OffsetDateTime::now_utc();
    sqlx::query("UPDATE messages SET deleted_at = COALESCE(deleted_at, $2) WHERE id = $1")
        .bind(message_id)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    publish_message_event(
        &mut transaction,
        message_id,
        space_id,
        channel_id,
        "message.deleted",
        now,
    )
    .await?;
    let response = message_by_id(&mut transaction, message_id).await?;
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(response))
}

#[derive(Deserialize)]
pub struct MessagePageQuery {
    before: Option<i64>,
    limit: Option<i64>,
}

#[derive(Deserialize, Serialize, utoipa::ToSchema)]
pub struct MessagePageResponse {
    pub channel_id: Uuid,
    pub snapshot_channel_seq: i64,
    pub messages: Vec<MessageResponse>,
    pub has_more_before: bool,
    pub has_more_after: bool,
}

pub async fn list(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(channel_id): Path<Uuid>,
    Query(query): Query<MessagePageQuery>,
) -> Result<Json<MessagePageResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let limit = query.limit.unwrap_or(50);
    if !(1..=100).contains(&limit) || query.before.is_some_and(|before| before <= 0) {
        return Err(ApiError::validation(
            "invalid_pagination",
            "Message limit must be 1 to 100 and before must be positive",
        ));
    }
    let access: Option<(i64,)> = sqlx::query_as(
        "SELECT channels.next_seq - 1 FROM channels \
         JOIN channel_members ON channel_members.channel_id = channels.id \
         JOIN human_members ON human_members.member_id = channel_members.member_id \
         WHERE channels.id = $1 AND human_members.user_id = $2",
    )
    .bind(channel_id)
    .bind(user.id)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?;
    let snapshot_channel_seq = access
        .ok_or_else(|| ApiError::forbidden("permission_denied", "Channel membership is required"))?
        .0;
    let before = query.before.unwrap_or(i64::MAX);
    let rows = sqlx::query_as::<_, MessageRow>(
        "SELECT messages.id, messages.channel_id, messages.channel_seq AS seq, \
                members.id AS author_id, members.kind AS author_kind, \
                members.display_name AS author_display_name, members.handle AS author_handle, \
                messages.body_markdown, \
                COALESCE((SELECT array_agg(member_id ORDER BY member_id) FROM message_mentions \
                          WHERE message_mentions.message_id = messages.id), \
                         ARRAY[]::uuid[]) AS mentions, \
                messages.created_at, messages.edited_at, messages.deleted_at \
                , threads.thread_id, COALESCE((SELECT count(*) FROM messages replies \
                    WHERE replies.channel_id = messages.channel_id \
                      AND replies.thread_id = threads.thread_id), 0) AS reply_count \
         FROM messages JOIN members ON members.id = messages.author_member_id \
         LEFT JOIN threads ON threads.channel_id = messages.channel_id \
                          AND threads.root_message_id = messages.id \
         WHERE messages.channel_id = $1 AND messages.thread_id IS NULL \
           AND messages.channel_seq < $2 \
         ORDER BY messages.channel_seq DESC LIMIT $3",
    )
    .bind(channel_id)
    .bind(before)
    .bind(limit + 1)
    .fetch_all(&state.database)
    .await
    .map_err(ApiError::database)?;
    let has_more_before = rows.len() as i64 > limit;
    let mut messages = rows
        .into_iter()
        .take(limit as usize)
        .map(MessageResponse::from)
        .collect::<Vec<_>>();
    messages.reverse();
    for message in &mut messages {
        message.attachments =
            attachment::attachments_for_message_pool(&state.database, message.id).await?;
    }
    hydrate_task_summaries_pool(&state.database, &mut messages).await?;
    let has_more_after = query
        .before
        .is_some_and(|cursor| cursor <= snapshot_channel_seq);
    Ok(Json(MessagePageResponse {
        channel_id,
        snapshot_channel_seq,
        messages,
        has_more_before,
        has_more_after,
    }))
}

#[derive(Deserialize, Serialize, utoipa::ToSchema)]
pub struct CreateMessageRequest {
    pub body_markdown: String,
    #[serde(default)]
    pub mentions: Vec<Uuid>,
    #[serde(default)]
    pub attachment_ids: Vec<Uuid>,
}

pub async fn create(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(channel_id): Path<Uuid>,
    Json(mut request): Json<CreateMessageRequest>,
) -> Result<(StatusCode, Json<MessageResponse>), ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    request.body_markdown = request.body_markdown.trim().to_owned();
    request.mentions.sort();
    request.mentions.dedup();
    request.attachment_ids.sort();
    request.attachment_ids.dedup();
    if !(1..=20_000).contains(&request.body_markdown.chars().count()) {
        return Err(ApiError::validation(
            "invalid_message_body",
            "Message must contain 1 to 20000 characters",
        ));
    }
    let request_hash = idempotency::request_hash(&request)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let channel: Option<(Uuid, Option<OffsetDateTime>, String)> =
        sqlx::query_as("SELECT space_id, archived_at, kind FROM channels WHERE id = $1 FOR UPDATE")
            .bind(channel_id)
            .fetch_optional(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
    let (space_id, archived_at, channel_kind) =
        channel.ok_or_else(|| ApiError::not_found("channel_not_found", "Channel was not found"))?;
    let actor = member::require_actor_tx(&mut transaction, user.id, space_id).await?;
    let is_member: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM channel_members \
         WHERE channel_id = $1 AND member_id = $2)",
    )
    .bind(channel_id)
    .bind(actor.id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if !is_member {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Channel membership is required",
        ));
    }
    if archived_at.is_some() {
        return Err(ApiError::conflict(
            "channel_archived",
            "Archived Channel is read-only",
        ));
    }
    let scope = format!("channel:{channel_id}:member:{}:message:create", actor.id);
    if let Some((status, response)) =
        idempotency::begin::<MessageResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok((status, Json(response)));
    }

    let published = super::message_publish::publish(
        &mut transaction,
        super::message_publish::PublishMessage {
            space_id,
            channel_id,
            channel_kind: &channel_kind,
            author_member_id: actor.id,
            body_markdown: &request.body_markdown,
            mention_member_ids: &request.mentions,
            attachment_ids: &request.attachment_ids,
            thread_id: None,
            thread_root_message_id: None,
            reply_to_message_id: None,
            idempotency_key: key.0,
        },
    )
    .await?;
    let response = message_by_id(&mut transaction, published.id).await?;
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

async fn publish_message_event(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    message_id: Uuid,
    space_id: Uuid,
    channel_id: Uuid,
    topic: &str,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    super::outbox::publish(
        transaction,
        topic,
        message_id,
        serde_json::json!({
            "space_id": space_id,
            "channel_id": channel_id,
            "message_id": message_id
        }),
        now,
    )
    .await
}

pub(super) async fn message_by_id(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    message_id: Uuid,
) -> Result<MessageResponse, ApiError> {
    let mut response: MessageResponse = sqlx::query_as::<_, MessageRow>(
        "SELECT messages.id, messages.channel_id, messages.channel_seq AS seq, \
                members.id AS author_id, members.kind AS author_kind, \
                members.display_name AS author_display_name, members.handle AS author_handle, \
                messages.body_markdown, \
                COALESCE((SELECT array_agg(member_id ORDER BY member_id) FROM message_mentions \
                          WHERE message_mentions.message_id = messages.id), \
                         ARRAY[]::uuid[]) AS mentions, \
                messages.created_at, messages.edited_at, messages.deleted_at \
                , threads.thread_id, COALESCE((SELECT count(*) FROM messages replies \
                    WHERE replies.channel_id = messages.channel_id \
                      AND replies.thread_id = threads.thread_id), 0) AS reply_count \
         FROM messages JOIN members ON members.id = messages.author_member_id \
         LEFT JOIN threads ON threads.channel_id = messages.channel_id \
                          AND threads.root_message_id = messages.id \
         WHERE messages.id = $1",
    )
    .bind(message_id)
    .fetch_one(&mut **transaction)
    .await
    .map(Into::into)
    .map_err(ApiError::database)?;
    response.attachments = attachment::attachments_for_message(transaction, message_id).await?;
    response.task = sqlx::query_as::<_, MessageTaskSummary>(
        "SELECT tasks.id, tasks.title, tasks.status, tasks.assigned_agent_member_id, \
                assignees.display_name AS assignee_name \
         FROM tasks LEFT JOIN members assignees ON assignees.id = tasks.assigned_agent_member_id \
         WHERE tasks.source_message_id = $1",
    )
    .bind(message_id)
    .fetch_optional(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(response)
}

pub(super) async fn hydrate_task_summaries_pool(
    database: &sqlx::PgPool,
    messages: &mut [MessageResponse],
) -> Result<(), ApiError> {
    if messages.is_empty() {
        return Ok(());
    }
    #[derive(sqlx::FromRow)]
    struct TaskProjectionRow {
        source_message_id: Uuid,
        id: Uuid,
        title: String,
        status: String,
        assigned_agent_member_id: Option<Uuid>,
        assignee_name: Option<String>,
    }
    let message_ids = messages
        .iter()
        .map(|message| message.id)
        .collect::<Vec<_>>();
    let summaries = sqlx::query_as::<_, TaskProjectionRow>(
        "SELECT tasks.source_message_id, tasks.id, tasks.title, tasks.status, \
                tasks.assigned_agent_member_id, assignees.display_name AS assignee_name \
         FROM tasks LEFT JOIN members assignees ON assignees.id = tasks.assigned_agent_member_id \
         WHERE tasks.source_message_id = ANY($1)",
    )
    .bind(&message_ids)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?
    .into_iter()
    .map(|row| {
        (
            row.source_message_id,
            MessageTaskSummary {
                id: row.id,
                title: row.title,
                status: row.status,
                assigned_agent_member_id: row.assigned_agent_member_id,
                assignee_name: row.assignee_name,
            },
        )
    })
    .collect::<HashMap<_, _>>();
    for message in messages {
        message.task = summaries.get(&message.id).cloned();
    }
    Ok(())
}

#[cfg(test)]
#[path = "message_tests.rs"]
mod tests;
