use std::collections::HashSet;

use axum::{
    Json,
    extract::{Path, State},
    http::StatusCode,
};
use axum_extra::extract::CookieJar;
use serde::{Deserialize, Serialize};
use time::OffsetDateTime;
use uuid::Uuid;

use super::{
    AppState,
    api_error::ApiError,
    auth, idempotency, member,
    message::{self, MessageResponse},
};

#[derive(Deserialize, Serialize)]
pub struct CreateThreadRequest {
    pub root_message_id: Uuid,
}

#[derive(Clone, Debug, Deserialize, Serialize, sqlx::FromRow)]
pub struct ThreadResponse {
    pub channel_id: Uuid,
    pub thread_id: i64,
    pub root_message_id: Uuid,
    pub created_by_member_id: Uuid,
    #[serde(with = "time::serde::rfc3339")]
    pub created_at: OffsetDateTime,
}

pub async fn create(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(channel_id): Path<Uuid>,
    Json(request): Json<CreateThreadRequest>,
) -> Result<(StatusCode, Json<ThreadResponse>), ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let request_hash = idempotency::request_hash(&request)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let channel: Option<(Uuid, Option<OffsetDateTime>)> =
        sqlx::query_as("SELECT space_id, archived_at FROM channels WHERE id = $1 FOR UPDATE")
            .bind(channel_id)
            .fetch_optional(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
    let (space_id, archived_at) =
        channel.ok_or_else(|| ApiError::not_found("channel_not_found", "Channel was not found"))?;
    let actor = member::require_actor_tx(&mut transaction, user.id, space_id).await?;
    super::channel_access::require_member(&mut transaction, channel_id, actor.id).await?;
    if archived_at.is_some() {
        return Err(ApiError::conflict(
            "channel_archived",
            "Archived Channel is read-only",
        ));
    }
    let scope = format!(
        "channel:{channel_id}:message:{}:thread:create",
        request.root_message_id
    );
    if let Some((status, response)) =
        idempotency::begin::<ThreadResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok((status, Json(response)));
    }
    let root_exists: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM messages \
         WHERE id = $1 AND channel_id = $2 AND thread_id IS NULL AND deleted_at IS NULL)",
    )
    .bind(request.root_message_id)
    .bind(channel_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if !root_exists {
        return Err(ApiError::validation(
            "invalid_thread_root",
            "Thread root must be an active main timeline Message in this Channel",
        ));
    }
    let existing: Option<ThreadResponse> = sqlx::query_as(
        "SELECT channel_id, thread_id, root_message_id, created_by_member_id, created_at \
         FROM threads WHERE channel_id = $1 AND root_message_id = $2",
    )
    .bind(channel_id)
    .bind(request.root_message_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if existing.is_some() {
        return Err(ApiError::conflict(
            "thread_exists",
            "This Message already has a Thread",
        ));
    }
    let thread_id: i64 = sqlx::query_scalar(
        "UPDATE channels SET next_thread_id = next_thread_id + 1 \
         WHERE id = $1 RETURNING next_thread_id - 1",
    )
    .bind(channel_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let now = OffsetDateTime::now_utc();
    sqlx::query(
        "INSERT INTO threads \
         (channel_id, space_id, thread_id, root_message_id, created_by_member_id, created_at) \
         VALUES ($1, $2, $3, $4, $5, $6)",
    )
    .bind(channel_id)
    .bind(space_id)
    .bind(thread_id)
    .bind(request.root_message_id)
    .bind(actor.id)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    subscribe(&mut transaction, channel_id, thread_id, actor.id, now).await?;
    let response = ThreadResponse {
        channel_id,
        thread_id,
        root_message_id: request.root_message_id,
        created_by_member_id: actor.id,
        created_at: now,
    };
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

#[derive(Deserialize, Serialize)]
pub struct CreateThreadMessageRequest {
    pub body_markdown: String,
    #[serde(default)]
    pub mentions: Vec<Uuid>,
    #[serde(default)]
    pub attachment_ids: Vec<Uuid>,
    pub reply_to_message_id: Option<Uuid>,
}

pub async fn reply(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path((channel_id, thread_id)): Path<(Uuid, i64)>,
    Json(mut request): Json<CreateThreadMessageRequest>,
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
    let thread: Option<(Uuid, Uuid, Option<OffsetDateTime>, String)> = sqlx::query_as(
        "SELECT threads.space_id, threads.root_message_id, channels.archived_at, channels.kind \
         FROM threads JOIN channels ON channels.id = threads.channel_id \
         WHERE threads.channel_id = $1 AND threads.thread_id = $2 FOR UPDATE OF channels",
    )
    .bind(channel_id)
    .bind(thread_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let (space_id, root_message_id, archived_at, channel_kind) =
        thread.ok_or_else(|| ApiError::not_found("thread_not_found", "Thread was not found"))?;
    let actor = member::require_actor_tx(&mut transaction, user.id, space_id).await?;
    super::channel_access::require_member(&mut transaction, channel_id, actor.id).await?;
    if archived_at.is_some() {
        return Err(ApiError::conflict(
            "channel_archived",
            "Archived Channel is read-only",
        ));
    }
    let scope = format!(
        "channel:{channel_id}:thread:{thread_id}:member:{}:message:create",
        actor.id
    );
    if let Some((status, response)) =
        idempotency::begin::<MessageResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok((status, Json(response)));
    }
    if let Some(reply_to) = request.reply_to_message_id {
        let valid_reply: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM messages WHERE id = $1 AND channel_id = $2 \
             AND (id = $3 OR thread_id = $4))",
        )
        .bind(reply_to)
        .bind(channel_id)
        .bind(root_message_id)
        .bind(thread_id)
        .fetch_one(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        if !valid_reply {
            return Err(ApiError::validation(
                "invalid_reply_target",
                "Reply target must be the Thread root or a Message in this Thread",
            ));
        }
    }
    message::validate_mentions(&mut transaction, channel_id, &request.mentions).await?;
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
         (id, channel_id, space_id, channel_seq, thread_id, reply_to_message_id, \
          author_member_id, body_markdown, idempotency_key, created_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)",
    )
    .bind(message_id)
    .bind(channel_id)
    .bind(space_id)
    .bind(seq)
    .bind(thread_id)
    .bind(request.reply_to_message_id)
    .bind(actor.id)
    .bind(&request.body_markdown)
    .bind(key.0)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    super::attachment::attach_to_message(
        &mut transaction,
        message_id,
        space_id,
        actor.id,
        &request.attachment_ids,
    )
    .await?;
    let inbox_changed = insert_thread_attention(
        &mut transaction,
        ThreadAttention {
            space_id,
            channel_id,
            thread_id,
            message_id,
            actor_id: actor.id,
            seq,
            reply_to_message_id: request.reply_to_message_id,
            mentions: &request.mentions,
            channel_kind: &channel_kind,
            now,
        },
    )
    .await?;
    if inbox_changed {
        super::outbox::publish(
            &mut transaction,
            "inbox.changed",
            message_id,
            serde_json::json!({ "space_id": space_id, "channel_id": channel_id, "thread_id": thread_id }),
            now,
        )
        .await?;
    }
    subscribe(&mut transaction, channel_id, thread_id, actor.id, now).await?;
    super::outbox::publish(
        &mut transaction,
        "message.created",
        message_id,
        serde_json::json!({
            "space_id": space_id,
            "channel_id": channel_id,
            "thread_id": thread_id,
            "message_id": message_id,
            "channel_seq": seq
        }),
        now,
    )
    .await?;
    let response = message::message_by_id(&mut transaction, message_id).await?;
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

#[derive(Deserialize, Serialize)]
pub struct ThreadReadResponse {
    pub channel_id: Uuid,
    pub thread_id: i64,
    pub snapshot_channel_seq: i64,
    pub root: MessageResponse,
    pub replies: Vec<MessageResponse>,
    pub is_following: bool,
}

pub async fn read(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path((channel_id, thread_id)): Path<(Uuid, i64)>,
) -> Result<Json<ThreadReadResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let access: Option<(Uuid, Uuid, i64, bool)> = sqlx::query_as(
        "SELECT threads.space_id, threads.root_message_id, channels.next_seq - 1, \
                EXISTS(SELECT 1 FROM thread_subscriptions subscriptions \
                    WHERE subscriptions.channel_id = threads.channel_id \
                      AND subscriptions.thread_id = threads.thread_id \
                      AND subscriptions.member_id = channel_members.member_id \
                      AND subscriptions.muted_at IS NULL) \
         FROM threads JOIN channels ON channels.id = threads.channel_id \
         JOIN channel_members ON channel_members.channel_id = threads.channel_id \
         JOIN human_members ON human_members.member_id = channel_members.member_id \
         WHERE threads.channel_id = $1 AND threads.thread_id = $2 \
           AND human_members.user_id = $3",
    )
    .bind(channel_id)
    .bind(thread_id)
    .bind(user.id)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?;
    let (_space_id, root_message_id, snapshot_channel_seq, is_following) =
        access.ok_or_else(|| {
            ApiError::forbidden("permission_denied", "Channel membership is required")
        })?;
    let root = fetch_message(&state.database, root_message_id).await?;
    let replies = fetch_replies(&state.database, channel_id, thread_id).await?;
    Ok(Json(ThreadReadResponse {
        channel_id,
        thread_id,
        snapshot_channel_seq,
        root,
        replies,
        is_following,
    }))
}

#[derive(Clone, Deserialize, Serialize)]
pub struct ThreadSubscriptionResponse {
    pub channel_id: Uuid,
    pub thread_id: i64,
    pub is_following: bool,
}

pub async fn follow(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path((channel_id, thread_id)): Path<(Uuid, i64)>,
) -> Result<Json<ThreadSubscriptionResponse>, ApiError> {
    set_subscription(state, jar, key, channel_id, thread_id, true).await
}

pub async fn unfollow(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path((channel_id, thread_id)): Path<(Uuid, i64)>,
) -> Result<Json<ThreadSubscriptionResponse>, ApiError> {
    set_subscription(state, jar, key, channel_id, thread_id, false).await
}

async fn set_subscription(
    state: std::sync::Arc<AppState>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    channel_id: Uuid,
    thread_id: i64,
    is_following: bool,
) -> Result<Json<ThreadSubscriptionResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let thread: Option<(Uuid, Option<OffsetDateTime>)> = sqlx::query_as(
        "SELECT threads.space_id, channels.archived_at FROM threads \
         JOIN channels ON channels.id = threads.channel_id \
         WHERE threads.channel_id = $1 AND threads.thread_id = $2 FOR UPDATE OF channels",
    )
    .bind(channel_id)
    .bind(thread_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let (space_id, archived_at) =
        thread.ok_or_else(|| ApiError::not_found("thread_not_found", "Thread was not found"))?;
    let actor = member::require_actor_tx(&mut transaction, user.id, space_id).await?;
    super::channel_access::require_member(&mut transaction, channel_id, actor.id).await?;
    if archived_at.is_some() {
        return Err(ApiError::conflict(
            "channel_archived",
            "Archived Channel is read-only",
        ));
    }
    let scope = format!(
        "channel:{channel_id}:thread:{thread_id}:member:{}:subscription:{}",
        actor.id,
        if is_following { "follow" } else { "unfollow" }
    );
    let request_hash = idempotency::request_hash(&is_following)?;
    if let Some((_status, response)) = idempotency::begin::<ThreadSubscriptionResponse>(
        &mut transaction,
        &scope,
        key,
        &request_hash,
    )
    .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }
    let now = OffsetDateTime::now_utc();
    sqlx::query(
        "INSERT INTO thread_subscriptions \
         (channel_id, thread_id, member_id, created_at, muted_at) VALUES ($1, $2, $3, $4, $5) \
         ON CONFLICT (channel_id, thread_id, member_id) DO UPDATE SET muted_at = EXCLUDED.muted_at",
    )
    .bind(channel_id)
    .bind(thread_id)
    .bind(actor.id)
    .bind(now)
    .bind(if is_following { None } else { Some(now) })
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let response = ThreadSubscriptionResponse {
        channel_id,
        thread_id,
        is_following,
    };
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(response))
}

async fn subscribe(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    channel_id: Uuid,
    thread_id: i64,
    member_id: Uuid,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    sqlx::query(
        "INSERT INTO thread_subscriptions \
         (channel_id, thread_id, member_id, created_at) VALUES ($1, $2, $3, $4) \
         ON CONFLICT (channel_id, thread_id, member_id) DO UPDATE SET muted_at = NULL",
    )
    .bind(channel_id)
    .bind(thread_id)
    .bind(member_id)
    .bind(now)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(())
}

pub(super) struct ThreadAttention<'a> {
    pub space_id: Uuid,
    pub channel_id: Uuid,
    pub thread_id: i64,
    pub message_id: Uuid,
    pub actor_id: Uuid,
    pub seq: i64,
    pub reply_to_message_id: Option<Uuid>,
    pub mentions: &'a [Uuid],
    pub channel_kind: &'a str,
    pub now: OffsetDateTime,
}

pub(super) async fn insert_thread_attention(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    input: ThreadAttention<'_>,
) -> Result<bool, ApiError> {
    let ThreadAttention {
        space_id,
        channel_id,
        thread_id,
        message_id,
        actor_id,
        seq,
        reply_to_message_id,
        mentions,
        channel_kind,
        now,
    } = input;
    let mut hard_recipients = HashSet::new();
    for mentioned_member_id in mentions {
        sqlx::query(
            "INSERT INTO message_mentions \
             (message_id, channel_id, space_id, member_id) VALUES ($1, $2, $3, $4)",
        )
        .bind(message_id)
        .bind(channel_id)
        .bind(space_id)
        .bind(mentioned_member_id)
        .execute(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
        subscribe(
            transaction,
            channel_id,
            thread_id,
            *mentioned_member_id,
            now,
        )
        .await?;
        if channel_kind != "direct" && *mentioned_member_id != actor_id {
            hard_recipients.insert(*mentioned_member_id);
            sqlx::query(
                "INSERT INTO inbox_items \
                 (id, member_id, space_id, kind, priority, channel_id, thread_id, message_id, \
                  first_seq, last_seq, available_at, created_at) \
                 VALUES ($1, $2, $3, 'mention', 'hard', $4, $5, $6, $7, $7, $8, $8)",
            )
            .bind(Uuid::now_v7())
            .bind(mentioned_member_id)
            .bind(space_id)
            .bind(channel_id)
            .bind(thread_id)
            .bind(message_id)
            .bind(seq)
            .bind(now)
            .execute(&mut **transaction)
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
        .bind(actor_id)
        .fetch_one(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
        sqlx::query(
            "INSERT INTO inbox_items \
             (id, member_id, space_id, kind, priority, channel_id, thread_id, message_id, \
              first_seq, last_seq, available_at, created_at) \
             VALUES ($1, $2, $3, 'direct', 'hard', $4, $5, $6, $7, $7, $8, $8)",
        )
        .bind(Uuid::now_v7())
        .bind(recipient_id)
        .bind(space_id)
        .bind(channel_id)
        .bind(thread_id)
        .bind(message_id)
        .bind(seq)
        .bind(now)
        .execute(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
        return Ok(true);
    }
    if let Some(reply_to_message_id) = reply_to_message_id {
        let reply_recipient: Uuid =
            sqlx::query_scalar("SELECT author_member_id FROM messages WHERE id = $1")
                .bind(reply_to_message_id)
                .fetch_one(&mut **transaction)
                .await
                .map_err(ApiError::database)?;
        if reply_recipient != actor_id && hard_recipients.insert(reply_recipient) {
            sqlx::query(
                "INSERT INTO inbox_items \
                 (id, member_id, space_id, kind, priority, channel_id, thread_id, message_id, \
                  first_seq, last_seq, available_at, created_at) \
                 VALUES ($1, $2, $3, 'reply', 'hard', $4, $5, $6, $7, $7, $8, $8)",
            )
            .bind(Uuid::now_v7())
            .bind(reply_recipient)
            .bind(space_id)
            .bind(channel_id)
            .bind(thread_id)
            .bind(message_id)
            .bind(seq)
            .bind(now)
            .execute(&mut **transaction)
            .await
            .map_err(ApiError::database)?;
        }
    }
    let hard_recipients = hard_recipients.into_iter().collect::<Vec<_>>();
    let recipients: Vec<(Uuid, i64)> = sqlx::query_as(
        "SELECT subscriptions.member_id, \
                COALESCE((agents.attention_config_json->>'ambient_debounce_seconds')::bigint, 5) \
         FROM thread_subscriptions subscriptions \
         LEFT JOIN agents ON agents.member_id = subscriptions.member_id \
         WHERE subscriptions.channel_id = $1 AND subscriptions.thread_id = $2 \
           AND subscriptions.muted_at IS NULL AND subscriptions.member_id <> $3 \
           AND NOT (subscriptions.member_id = ANY($4)) \
           AND (agents.member_id IS NULL OR (agents.desired_lifecycle IN ('active', 'suspended') \
             AND agents.provision_status = 'ready' \
             AND COALESCE((agents.attention_config_json->>'ambient_enabled')::boolean, false)))",
    )
    .bind(channel_id)
    .bind(thread_id)
    .bind(actor_id)
    .bind(&hard_recipients)
    .fetch_all(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    for (member_id, debounce_seconds) in &recipients {
        sqlx::query(
            "INSERT INTO inbox_items \
             (id, member_id, space_id, kind, priority, channel_id, thread_id, message_id, \
              first_seq, last_seq, message_count, status, available_at, created_at) \
             VALUES ($1, $2, $3, 'thread_activity', 'ambient', $4, $5, $6, $7, $7, 1, \
                     'pending', $8, $9) \
             ON CONFLICT (member_id, channel_id, thread_id) \
               WHERE kind = 'thread_activity' AND thread_id IS NOT NULL AND status = 'pending' \
             DO UPDATE SET message_id = EXCLUDED.message_id, last_seq = EXCLUDED.last_seq, \
                           message_count = inbox_items.message_count + 1",
        )
        .bind(Uuid::now_v7())
        .bind(member_id)
        .bind(space_id)
        .bind(channel_id)
        .bind(thread_id)
        .bind(message_id)
        .bind(seq)
        .bind(now + time::Duration::seconds(*debounce_seconds))
        .bind(now)
        .execute(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
    }
    Ok(!hard_recipients.is_empty() || !recipients.is_empty())
}

async fn fetch_message(
    database: &sqlx::PgPool,
    message_id: Uuid,
) -> Result<MessageResponse, ApiError> {
    let mut message: MessageResponse = sqlx::query_as::<_, MessageWireRow>(MESSAGE_SELECT)
        .bind(message_id)
        .fetch_one(database)
        .await
        .map(Into::into)
        .map_err(ApiError::database)?;
    message.attachments =
        super::attachment::attachments_for_message_pool(database, message_id).await?;
    message::hydrate_task_summaries_pool(database, std::slice::from_mut(&mut message)).await?;
    Ok(message)
}

async fn fetch_replies(
    database: &sqlx::PgPool,
    channel_id: Uuid,
    thread_id: i64,
) -> Result<Vec<MessageResponse>, ApiError> {
    let query = format!(
        "{MESSAGE_SELECT} AND messages.channel_id = $2 AND messages.thread_id = $3 ORDER BY messages.channel_seq"
    );
    let mut messages: Vec<MessageResponse> = sqlx::query_as::<_, MessageWireRow>(&query)
        .bind(Uuid::nil())
        .bind(channel_id)
        .bind(thread_id)
        .fetch_all(database)
        .await
        .map(|rows| rows.into_iter().map(Into::into).collect())
        .map_err(ApiError::database)?;
    for message in &mut messages {
        message.attachments =
            super::attachment::attachments_for_message_pool(database, message.id).await?;
    }
    Ok(messages)
}

const MESSAGE_SELECT: &str = "SELECT messages.id, messages.channel_id, messages.channel_seq AS seq, \
            members.id AS author_id, members.kind AS author_kind, \
            members.display_name AS author_display_name, members.handle AS author_handle, \
            messages.body_markdown, \
            COALESCE((SELECT array_agg(member_id ORDER BY member_id) FROM message_mentions \
                      WHERE message_mentions.message_id = messages.id), ARRAY[]::uuid[]) AS mentions, \
            messages.created_at, messages.edited_at, messages.deleted_at, \
            COALESCE(messages.thread_id, threads_for_root.thread_id) AS thread_id, \
            COALESCE((SELECT count(*) FROM messages replies \
                WHERE replies.channel_id = messages.channel_id \
                  AND replies.thread_id = threads_for_root.thread_id), 0) AS reply_count \
     FROM messages JOIN members ON members.id = messages.author_member_id \
     LEFT JOIN threads threads_for_root ON threads_for_root.channel_id = messages.channel_id \
                                        AND threads_for_root.root_message_id = messages.id \
     WHERE (messages.id = $1 OR $1 = '00000000-0000-0000-0000-000000000000'::uuid)";

#[derive(sqlx::FromRow)]
struct MessageWireRow {
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

impl From<MessageWireRow> for MessageResponse {
    fn from(row: MessageWireRow) -> Self {
        let body_markdown = if row.deleted_at.is_some() {
            "Message 已删除".to_owned()
        } else {
            row.body_markdown
        };
        Self {
            id: row.id,
            channel_id: row.channel_id,
            seq: row.seq,
            author: message::MessageAuthor {
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
