use std::collections::HashSet;

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

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct MessageAuthor {
    pub id: Uuid,
    pub kind: String,
    pub display_name: String,
    pub handle: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct MessageResponse {
    pub id: Uuid,
    pub channel_id: Uuid,
    pub seq: i64,
    pub author: MessageAuthor,
    pub body_markdown: String,
    pub mentions: Vec<Uuid>,
    pub attachments: Vec<attachment::AttachmentResponse>,
    pub created_at: OffsetDateTime,
    pub edited_at: Option<OffsetDateTime>,
    pub deleted_at: Option<OffsetDateTime>,
    pub thread_id: Option<i64>,
    pub reply_count: i64,
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
    require_channel_member(&mut transaction, channel_id, actor.id).await?;
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
    record_message_event(
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
    require_channel_member(&mut transaction, channel_id, actor.id).await?;
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
    record_message_event(
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

#[derive(Deserialize, Serialize)]
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

#[derive(Deserialize, Serialize)]
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

    validate_mentions(&mut transaction, channel_id, &request.mentions).await?;
    let seq: i64 = sqlx::query_scalar(
        "UPDATE channels SET next_seq = next_seq + 1 WHERE id = $1 RETURNING next_seq - 1",
    )
    .bind(channel_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let message_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    sqlx::query(
        "INSERT INTO messages \
         (id, channel_id, space_id, channel_seq, author_member_id, body_markdown, \
          idempotency_key, created_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
    )
    .bind(message_id)
    .bind(channel_id)
    .bind(space_id)
    .bind(seq)
    .bind(actor.id)
    .bind(&request.body_markdown)
    .bind(key.0)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    attachment::attach_to_message(
        &mut transaction,
        message_id,
        space_id,
        actor.id,
        &request.attachment_ids,
    )
    .await?;
    for mentioned_member_id in &request.mentions {
        sqlx::query(
            "INSERT INTO message_mentions \
             (message_id, channel_id, space_id, member_id) VALUES ($1, $2, $3, $4)",
        )
        .bind(message_id)
        .bind(channel_id)
        .bind(space_id)
        .bind(mentioned_member_id)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        if channel_kind != "direct" && *mentioned_member_id != actor.id {
            sqlx::query(
                "INSERT INTO inbox_items \
                 (id, member_id, kind, priority, channel_id, message_id, first_seq, last_seq, \
                  available_at, created_at, space_id) \
                 VALUES ($1, $2, 'mention', 'hard', $3, $4, $5, $5, $6, $6, $7)",
            )
            .bind(Uuid::now_v7())
            .bind(mentioned_member_id)
            .bind(channel_id)
            .bind(message_id)
            .bind(seq)
            .bind(now)
            .bind(space_id)
            .execute(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
        }
    }
    if channel_kind == "direct" {
        let recipient_id: Uuid = sqlx::query_scalar(
            "SELECT member_id FROM channel_members \
             WHERE channel_id = $1 AND member_id <> $2",
        )
        .bind(channel_id)
        .bind(actor.id)
        .fetch_one(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        sqlx::query(
            "INSERT INTO inbox_items \
             (id, member_id, space_id, kind, priority, channel_id, message_id, \
              first_seq, last_seq, available_at, created_at) \
             VALUES ($1, $2, $3, 'direct', 'hard', $4, $5, $6, $6, $7, $7)",
        )
        .bind(Uuid::now_v7())
        .bind(recipient_id)
        .bind(space_id)
        .bind(channel_id)
        .bind(message_id)
        .bind(seq)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    }
    let ambient_changed = if channel_kind == "direct" {
        false
    } else {
        insert_channel_ambient_inbox(
            &mut transaction,
            ChannelAmbientInbox {
                space_id,
                channel_id,
                message_id,
                actor_id: actor.id,
                seq,
                hard_recipients: &request.mentions,
                now,
            },
        )
        .await?
    };
    if channel_kind == "direct"
        || request
            .mentions
            .iter()
            .any(|member_id| *member_id != actor.id)
        || ambient_changed
    {
        sqlx::query(
            "INSERT INTO outbox_events \
             (id, topic, aggregate_id, payload_json, created_at) \
             VALUES ($1, 'inbox.changed', $2, $3, $4)",
        )
        .bind(Uuid::now_v7())
        .bind(message_id)
        .bind(serde_json::json!({ "space_id": space_id, "channel_id": channel_id }))
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    }
    sqlx::query(
        "INSERT INTO outbox_events \
         (id, topic, aggregate_id, payload_json, created_at) \
         VALUES ($1, 'message.created', $2, $3, $4)",
    )
    .bind(Uuid::now_v7())
    .bind(message_id)
    .bind(serde_json::json!({
        "space_id": space_id,
        "channel_id": channel_id,
        "message_id": message_id,
        "channel_seq": seq
    }))
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let response = message_by_id(&mut transaction, message_id).await?;
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

pub(super) struct ChannelAmbientInbox<'a> {
    pub space_id: Uuid,
    pub channel_id: Uuid,
    pub message_id: Uuid,
    pub actor_id: Uuid,
    pub seq: i64,
    pub hard_recipients: &'a [Uuid],
    pub now: OffsetDateTime,
}

pub(super) async fn insert_channel_ambient_inbox(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    input: ChannelAmbientInbox<'_>,
) -> Result<bool, ApiError> {
    let ChannelAmbientInbox {
        space_id,
        channel_id,
        message_id,
        actor_id,
        seq,
        hard_recipients,
        now,
    } = input;
    let recipients: Vec<(Uuid, i64)> = sqlx::query_as(
        "SELECT agents.member_id, \
                (agents.attention_config_json->>'ambient_debounce_seconds')::bigint \
         FROM agents JOIN channel_members ON channel_members.member_id = agents.member_id \
         WHERE channel_members.channel_id = $1 AND agents.member_id <> $2 \
           AND agents.status IN ('active', 'suspended') \
           AND COALESCE((agents.attention_config_json->>'ambient_enabled')::boolean, false) \
           AND NOT (agents.member_id = ANY($3))",
    )
    .bind(channel_id)
    .bind(actor_id)
    .bind(hard_recipients)
    .fetch_all(&mut **transaction)
    .await
    .map_err(ApiError::database)?;

    for (member_id, debounce_seconds) in &recipients {
        let available_at = now + time::Duration::seconds(*debounce_seconds);
        sqlx::query(
            "INSERT INTO inbox_items \
             (id, member_id, space_id, kind, priority, channel_id, message_id, first_seq, \
              last_seq, message_count, status, available_at, created_at) \
             VALUES ($1, $2, $3, 'channel_activity', 'ambient', $4, $5, $6, $6, 1, \
                     'pending', $7, $8) \
             ON CONFLICT (member_id, channel_id) \
               WHERE kind = 'channel_activity' AND thread_id IS NULL AND status = 'pending' \
             DO UPDATE SET message_id = EXCLUDED.message_id, last_seq = EXCLUDED.last_seq, \
                           message_count = inbox_items.message_count + 1",
        )
        .bind(Uuid::now_v7())
        .bind(member_id)
        .bind(space_id)
        .bind(channel_id)
        .bind(message_id)
        .bind(seq)
        .bind(available_at)
        .bind(now)
        .execute(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
    }
    Ok(!recipients.is_empty())
}

pub(super) async fn validate_mentions(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    channel_id: Uuid,
    mentions: &[Uuid],
) -> Result<(), ApiError> {
    if mentions.is_empty() {
        return Ok(());
    }
    let valid = sqlx::query_scalar::<_, Uuid>(
        "SELECT member_id FROM channel_members \
         WHERE channel_id = $1 AND member_id = ANY($2)",
    )
    .bind(channel_id)
    .bind(mentions)
    .fetch_all(&mut **transaction)
    .await
    .map_err(ApiError::database)?
    .into_iter()
    .collect::<HashSet<_>>();
    if mentions.iter().any(|member_id| !valid.contains(member_id)) {
        return Err(ApiError::validation(
            "invalid_mention",
            "Mentioned Member must belong to the Channel",
        ));
    }
    Ok(())
}

async fn require_channel_member(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    channel_id: Uuid,
    member_id: Uuid,
) -> Result<(), ApiError> {
    let exists: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM channel_members WHERE channel_id = $1 AND member_id = $2)",
    )
    .bind(channel_id)
    .bind(member_id)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    if exists {
        Ok(())
    } else {
        Err(ApiError::forbidden(
            "permission_denied",
            "Channel membership is required",
        ))
    }
}

async fn record_message_event(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    message_id: Uuid,
    space_id: Uuid,
    channel_id: Uuid,
    topic: &str,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    sqlx::query(
        "INSERT INTO outbox_events (id, topic, aggregate_id, payload_json, created_at) \
         VALUES ($1, $2, $3, $4, $5)",
    )
    .bind(Uuid::now_v7())
    .bind(topic)
    .bind(message_id)
    .bind(serde_json::json!({
        "space_id": space_id,
        "channel_id": channel_id,
        "message_id": message_id
    }))
    .bind(now)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(())
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
    Ok(response)
}
