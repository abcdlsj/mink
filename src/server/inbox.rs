use axum::{
    Json,
    extract::{Path, State},
};
use axum_extra::extract::CookieJar;
use serde::{Deserialize, Serialize};
use sqlx::FromRow;
use time::OffsetDateTime;
use uuid::Uuid;

use super::{AppState, api_error::ApiError, auth, idempotency};

#[derive(Clone, Deserialize, Serialize, FromRow)]
pub struct InboxItemResponse {
    pub id: Uuid,
    pub member_id: Uuid,
    pub kind: String,
    pub priority: String,
    pub channel_id: Option<Uuid>,
    pub channel_slug: Option<String>,
    pub thread_id: Option<i64>,
    pub message_id: Option<Uuid>,
    pub approval_id: Option<Uuid>,
    pub sender_member_id: Option<Uuid>,
    pub sender_display_name: Option<String>,
    pub summary: Option<String>,
    pub status: String,
    pub available_at: OffsetDateTime,
    pub created_at: OffsetDateTime,
}

pub async fn list(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(member_id): Path<Uuid>,
) -> Result<Json<Vec<InboxItemResponse>>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    require_own_human_member(&state.database, user.id, member_id).await?;
    let rows = sqlx::query_as(
        "SELECT inbox_items.id, inbox_items.member_id, inbox_items.kind, inbox_items.priority, \
                inbox_items.channel_id, channels.slug::text AS channel_slug, inbox_items.thread_id, \
                inbox_items.message_id, inbox_items.approval_id, senders.id AS sender_member_id, \
                senders.display_name AS sender_display_name, \
                CASE WHEN messages.deleted_at IS NULL THEN left(messages.body_markdown, 160) \
                     ELSE 'Message 已删除' END AS summary, \
                inbox_items.status, inbox_items.available_at, inbox_items.created_at \
         FROM inbox_items \
         LEFT JOIN channels ON channels.id = inbox_items.channel_id \
         LEFT JOIN messages ON messages.id = inbox_items.message_id \
         LEFT JOIN members senders ON senders.id = messages.author_member_id \
         WHERE inbox_items.member_id = $1 \
           AND (inbox_items.status = 'pending' OR \
                (inbox_items.status = 'deferred' AND inbox_items.available_at <= now())) \
         ORDER BY CASE inbox_items.priority WHEN 'hard' THEN 0 ELSE 1 END, \
                  inbox_items.created_at DESC",
    )
    .bind(member_id)
    .fetch_all(&state.database)
    .await
    .map_err(ApiError::database)?;
    Ok(Json(rows))
}

pub async fn ack(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(item_id): Path<Uuid>,
) -> Result<Json<InboxItemResponse>, ApiError> {
    update_item(state, jar, key, item_id, None).await
}

#[derive(Deserialize, Serialize)]
pub struct DeferRequest {
    pub until: OffsetDateTime,
}

pub async fn defer(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(item_id): Path<Uuid>,
    Json(request): Json<DeferRequest>,
) -> Result<Json<InboxItemResponse>, ApiError> {
    if request.until <= OffsetDateTime::now_utc() {
        return Err(ApiError::validation(
            "invalid_defer_time",
            "Inbox defer time must be in the future",
        ));
    }
    update_item(state, jar, key, item_id, Some(request)).await
}

async fn update_item(
    state: std::sync::Arc<AppState>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    item_id: Uuid,
    defer: Option<DeferRequest>,
) -> Result<Json<InboxItemResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let member_id: Uuid = sqlx::query_scalar(
        "SELECT inbox_items.member_id FROM inbox_items \
         JOIN human_members ON human_members.member_id = inbox_items.member_id \
         WHERE inbox_items.id = $1 AND human_members.user_id = $2 FOR UPDATE OF inbox_items",
    )
    .bind(item_id)
    .bind(user.id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| ApiError::not_found("inbox_item_not_found", "Inbox Item was not found"))?;
    let scope = format!(
        "inbox:{item_id}:{}",
        if defer.is_some() { "defer" } else { "ack" }
    );
    let request_hash = idempotency::request_hash(&defer)?;
    if let Some((_status, response)) =
        idempotency::begin::<InboxItemResponse>(&mut transaction, &scope, key, &request_hash)
            .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }
    if let Some(request) = defer {
        sqlx::query(
            "UPDATE inbox_items SET status = 'deferred', available_at = $2, lease_id = NULL, \
             lease_expires_at = NULL WHERE id = $1 AND status IN ('pending', 'deferred')",
        )
        .bind(item_id)
        .bind(request.until)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    } else {
        sqlx::query(
            "UPDATE inbox_items SET status = 'handled', handled_at = $2, lease_id = NULL, \
             lease_expires_at = NULL WHERE id = $1 AND status IN ('pending', 'deferred')",
        )
        .bind(item_id)
        .bind(OffsetDateTime::now_utc())
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    }
    let response = item_by_id(&mut transaction, item_id).await?;
    let space_id: Uuid = sqlx::query_scalar("SELECT space_id FROM inbox_items WHERE id = $1")
        .bind(item_id)
        .fetch_one(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    sqlx::query(
        "INSERT INTO outbox_events (id, topic, aggregate_id, payload_json, created_at) \
         VALUES ($1, 'inbox.changed', $2, $3, $4)",
    )
    .bind(Uuid::now_v7())
    .bind(item_id)
    .bind(serde_json::json!({ "space_id": space_id, "member_id": member_id, "item_id": item_id }))
    .bind(OffsetDateTime::now_utc())
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    idempotency::finish(
        &mut transaction,
        &scope,
        key,
        axum::http::StatusCode::OK,
        &response,
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(response))
}

async fn item_by_id(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    item_id: Uuid,
) -> Result<InboxItemResponse, ApiError> {
    sqlx::query_as(
        "SELECT inbox_items.id, inbox_items.member_id, inbox_items.kind, inbox_items.priority, \
                inbox_items.channel_id, channels.slug::text AS channel_slug, inbox_items.thread_id, \
                inbox_items.message_id, inbox_items.approval_id, senders.id AS sender_member_id, \
                senders.display_name AS sender_display_name, \
                CASE WHEN messages.deleted_at IS NULL THEN left(messages.body_markdown, 160) \
                     ELSE 'Message 已删除' END AS summary, \
                inbox_items.status, inbox_items.available_at, inbox_items.created_at \
         FROM inbox_items LEFT JOIN channels ON channels.id = inbox_items.channel_id \
         LEFT JOIN messages ON messages.id = inbox_items.message_id \
         LEFT JOIN members senders ON senders.id = messages.author_member_id \
         WHERE inbox_items.id = $1",
    )
    .bind(item_id)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)
}

async fn require_own_human_member(
    database: &sqlx::PgPool,
    user_id: Uuid,
    member_id: Uuid,
) -> Result<(), ApiError> {
    let owns: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM human_members WHERE member_id = $1 AND user_id = $2)",
    )
    .bind(member_id)
    .bind(user_id)
    .fetch_one(database)
    .await
    .map_err(ApiError::database)?;
    if owns {
        Ok(())
    } else {
        Err(ApiError::forbidden(
            "permission_denied",
            "Human Inbox is private to its Member",
        ))
    }
}
