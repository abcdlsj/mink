use axum::{
    Json,
    extract::{Path, State},
    http::StatusCode,
};
use axum_extra::extract::CookieJar;
use serde::{Deserialize, Serialize};
use sqlx::{FromRow, Postgres, Transaction};
use time::OffsetDateTime;
use uuid::Uuid;

use super::{AppState, api_error::ApiError, auth, idempotency, member};

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ChannelResponse {
    pub id: Uuid,
    pub space_id: Uuid,
    pub kind: String,
    pub name: String,
    pub slug: String,
    pub topic: Option<String>,
    pub created_by_member_id: Uuid,
    pub joined: bool,
    pub archived_at: Option<OffsetDateTime>,
}

#[derive(FromRow)]
struct ChannelRow {
    id: Uuid,
    space_id: Uuid,
    kind: String,
    name: String,
    slug: String,
    topic: Option<String>,
    created_by_member_id: Uuid,
    joined: bool,
    archived_at: Option<OffsetDateTime>,
}

#[derive(FromRow)]
struct ArchivableChannelRow {
    space_id: Uuid,
    kind: String,
    slug: Option<String>,
    created_by_member_id: Uuid,
    archived_at: Option<OffsetDateTime>,
}

impl From<ChannelRow> for ChannelResponse {
    fn from(row: ChannelRow) -> Self {
        Self {
            id: row.id,
            space_id: row.space_id,
            kind: row.kind,
            name: row.name,
            slug: row.slug,
            topic: row.topic,
            created_by_member_id: row.created_by_member_id,
            joined: row.joined,
            archived_at: row.archived_at,
        }
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ChannelListResponse {
    pub channels: Vec<ChannelResponse>,
    pub can_create: bool,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct DirectMessageResponse {
    pub channel_id: Uuid,
    pub space_id: Uuid,
    pub other_member: member::MemberResponse,
    pub created_at: OffsetDateTime,
}

#[derive(FromRow)]
struct DirectMessageRow {
    channel_id: Uuid,
    space_id: Uuid,
    other_member_id: Uuid,
    other_member_kind: String,
    other_member_display_name: String,
    other_member_handle: String,
    other_member_access_level: String,
    other_member_permissions: Vec<String>,
    created_at: OffsetDateTime,
}

impl From<DirectMessageRow> for DirectMessageResponse {
    fn from(row: DirectMessageRow) -> Self {
        Self {
            channel_id: row.channel_id,
            space_id: row.space_id,
            other_member: member::MemberResponse {
                id: row.other_member_id,
                kind: row.other_member_kind,
                display_name: row.other_member_display_name,
                handle: row.other_member_handle,
                access_level: row.other_member_access_level,
                permissions: row.other_member_permissions,
            },
            created_at: row.created_at,
        }
    }
}

pub async fn list(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<ChannelListResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let actor = member::require_actor(&state.database, user.id, space_id).await?;
    let rows = sqlx::query_as::<_, ChannelRow>(
        "SELECT channels.id, channels.space_id, channels.kind, channels.name, \
                channels.slug::text AS slug, channels.topic, \
                channels.created_by_member_id, \
                channels.archived_at, \
                EXISTS(SELECT 1 FROM channel_members \
                       WHERE channel_members.channel_id = channels.id \
                         AND channel_members.member_id = $2) AS joined \
         FROM channels \
         WHERE channels.space_id = $1 AND channels.kind <> 'direct' \
           AND channels.archived_at IS NULL \
           AND (channels.kind = 'public' OR EXISTS( \
               SELECT 1 FROM channel_members \
               WHERE channel_members.channel_id = channels.id \
                 AND channel_members.member_id = $2)) \
         ORDER BY CASE channels.slug WHEN 'general' THEN 0 ELSE 1 END, lower(channels.name)",
    )
    .bind(space_id)
    .bind(actor.id)
    .fetch_all(&state.database)
    .await
    .map_err(ApiError::database)?;
    let can_create = can_create_channel(&state.database, &actor).await?;
    Ok(Json(ChannelListResponse {
        channels: rows.into_iter().map(Into::into).collect(),
        can_create,
    }))
}

pub async fn list_direct_messages(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<DirectMessageResponse>>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let actor = member::require_actor(&state.database, user.id, space_id).await?;
    let rows = sqlx::query_as::<_, DirectMessageRow>(
        "SELECT direct_channels.channel_id, direct_channels.space_id, \
                other.id AS other_member_id, other.kind AS other_member_kind, \
                other.display_name AS other_member_display_name, \
                other.handle AS other_member_handle, \
                other.access_level AS other_member_access_level, \
                COALESCE((SELECT array_agg(permission ORDER BY permission) \
                          FROM member_permissions WHERE member_id = other.id), \
                         ARRAY[]::text[]) AS other_member_permissions, \
                channels.created_at \
         FROM direct_channels \
         JOIN channels ON channels.id = direct_channels.channel_id \
         JOIN members other ON other.id = CASE \
             WHEN direct_channels.member_low_id = $2 THEN direct_channels.member_high_id \
             ELSE direct_channels.member_low_id END \
         WHERE direct_channels.space_id = $1 \
           AND $2 IN (direct_channels.member_low_id, direct_channels.member_high_id) \
           AND channels.archived_at IS NULL \
         ORDER BY channels.created_at DESC",
    )
    .bind(space_id)
    .bind(actor.id)
    .fetch_all(&state.database)
    .await
    .map_err(ApiError::database)?;
    Ok(Json(rows.into_iter().map(Into::into).collect()))
}

#[derive(Deserialize, Serialize)]
pub struct CreateDirectMessageRequest {
    pub member_id: Uuid,
}

pub async fn create_direct_message(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(space_id): Path<Uuid>,
    Json(request): Json<CreateDirectMessageRequest>,
) -> Result<(StatusCode, Json<DirectMessageResponse>), ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let request_hash = idempotency::request_hash(&request)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let actor = member::require_actor_tx(&mut transaction, user.id, space_id).await?;
    if actor.id == request.member_id {
        return Err(ApiError::validation(
            "invalid_dm_member",
            "DM requires another Member",
        ));
    }
    let target_exists: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM members \
         WHERE id = $1 AND space_id = $2 AND retired_at IS NULL)",
    )
    .bind(request.member_id)
    .bind(space_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if !target_exists {
        return Err(ApiError::not_found(
            "member_not_found",
            "Member was not found in this Space",
        ));
    }
    let (member_low_id, member_high_id) = canonical_member_pair(actor.id, request.member_id);
    let locked_members = sqlx::query_scalar::<_, Uuid>(
        "SELECT id FROM members WHERE id = ANY($1) ORDER BY id FOR UPDATE",
    )
    .bind([member_low_id, member_high_id].as_slice())
    .fetch_all(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if locked_members.len() != 2 {
        return Err(ApiError::not_found(
            "member_not_found",
            "Member was not found in this Space",
        ));
    }
    let scope = format!("space:{space_id}:dm:{member_low_id}:{member_high_id}:create");
    if let Some((status, response)) =
        idempotency::begin::<DirectMessageResponse>(&mut transaction, &scope, key, &request_hash)
            .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok((status, Json(response)));
    }
    let existing: Option<Uuid> = sqlx::query_scalar(
        "SELECT channel_id FROM direct_channels \
         WHERE space_id = $1 AND member_low_id = $2 AND member_high_id = $3",
    )
    .bind(space_id)
    .bind(member_low_id)
    .bind(member_high_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let (channel_id, status) = if let Some(channel_id) = existing {
        (channel_id, StatusCode::OK)
    } else {
        let channel_id = Uuid::now_v7();
        let now = OffsetDateTime::now_utc();
        sqlx::query(
            "INSERT INTO channels \
             (id, space_id, kind, name, slug, created_by_member_id, created_at) \
             VALUES ($1, $2, 'direct', 'DM', NULL, $3, $4)",
        )
        .bind(channel_id)
        .bind(space_id)
        .bind(actor.id)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        for participant in [member_low_id, member_high_id] {
            sqlx::query(
                "INSERT INTO channel_members (channel_id, member_id, space_id, joined_at) \
                 VALUES ($1, $2, $3, $4)",
            )
            .bind(channel_id)
            .bind(participant)
            .bind(space_id)
            .bind(now)
            .execute(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
        }
        sqlx::query(
            "INSERT INTO direct_channels \
             (channel_id, space_id, member_low_id, member_high_id) \
             VALUES ($1, $2, $3, $4)",
        )
        .bind(channel_id)
        .bind(space_id)
        .bind(member_low_id)
        .bind(member_high_id)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        record_channel_event(
            &mut transaction,
            space_id,
            actor.id,
            channel_id,
            "channel.created",
            now,
        )
        .await?;
        (channel_id, StatusCode::CREATED)
    };
    let response =
        direct_message_by_id_tx(&mut transaction, channel_id, actor.id, request.member_id).await?;
    idempotency::finish(&mut transaction, &scope, key, status, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok((status, Json(response)))
}

#[derive(Deserialize, Serialize)]
pub struct CreateChannelRequest {
    pub name: String,
    pub slug: String,
    pub kind: String,
    pub topic: Option<String>,
}

pub async fn create(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(space_id): Path<Uuid>,
    Json(request): Json<CreateChannelRequest>,
) -> Result<(StatusCode, Json<ChannelResponse>), ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let request = validate_create(request)?;
    let request_hash = idempotency::request_hash(&request)?;
    let scope = format!("space:{space_id}:channel:create");
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let actor = member::require_actor_tx(&mut transaction, user.id, space_id).await?;
    if !can_create_channel_tx(&mut transaction, &actor).await? {
        return Err(ApiError::forbidden(
            "permission_denied",
            "channel:create permission is required",
        ));
    }
    if let Some((status, response)) =
        idempotency::begin::<ChannelResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok((status, Json(response)));
    }

    let channel_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    sqlx::query(
        "INSERT INTO channels \
         (id, space_id, kind, name, slug, topic, created_by_member_id, created_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
    )
    .bind(channel_id)
    .bind(space_id)
    .bind(&request.kind)
    .bind(&request.name)
    .bind(&request.slug)
    .bind(&request.topic)
    .bind(actor.id)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(|error| {
        if unique_constraint(&error, "channels_space_slug_unique") {
            ApiError::conflict("channel_slug_taken", "This Channel slug is already in use")
        } else {
            ApiError::database(error)
        }
    })?;
    sqlx::query(
        "INSERT INTO channel_members (channel_id, member_id, space_id, joined_at) \
         VALUES ($1, $2, $3, $4)",
    )
    .bind(channel_id)
    .bind(actor.id)
    .bind(space_id)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    record_channel_event(
        &mut transaction,
        space_id,
        actor.id,
        channel_id,
        "channel.created",
        now,
    )
    .await?;

    let response = ChannelResponse {
        id: channel_id,
        space_id,
        kind: request.kind,
        name: request.name,
        slug: request.slug,
        topic: request.topic,
        created_by_member_id: actor.id,
        joined: true,
        archived_at: None,
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

pub async fn join(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(channel_id): Path<Uuid>,
) -> Result<Json<ChannelResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let channel: (Uuid, String, Option<OffsetDateTime>) =
        sqlx::query_as("SELECT space_id, kind, archived_at FROM channels WHERE id = $1 FOR UPDATE")
            .bind(channel_id)
            .fetch_optional(&mut *transaction)
            .await
            .map_err(ApiError::database)?
            .ok_or_else(|| ApiError::not_found("channel_not_found", "Channel was not found"))?;
    let actor = member::require_actor_tx(&mut transaction, user.id, channel.0).await?;
    if channel.2.is_some() {
        return Err(ApiError::conflict(
            "channel_archived",
            "Archived Channel cannot be joined",
        ));
    }
    if channel.1 != "public" {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Only public Channel can be joined directly",
        ));
    }
    let scope = format!("channel:{channel_id}:member:{}:join", actor.id);
    let request_hash = idempotency::request_hash(&channel_id)?;
    if let Some((_status, response)) =
        idempotency::begin::<ChannelResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }

    let inserted = sqlx::query(
        "INSERT INTO channel_members (channel_id, member_id, space_id, joined_at) \
         VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING",
    )
    .bind(channel_id)
    .bind(actor.id)
    .bind(channel.0)
    .bind(OffsetDateTime::now_utc())
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if inserted.rows_affected() == 1 {
        record_channel_event(
            &mut transaction,
            channel.0,
            actor.id,
            channel_id,
            "channel.joined",
            OffsetDateTime::now_utc(),
        )
        .await?;
    }
    let response = channel_by_id_tx(&mut transaction, channel_id, actor.id).await?;
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(response))
}

pub async fn archive(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(channel_id): Path<Uuid>,
) -> Result<Json<ChannelResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let channel: Option<ArchivableChannelRow> = sqlx::query_as(
        "SELECT space_id, kind, slug::text, created_by_member_id, archived_at \
             FROM channels WHERE id = $1 FOR UPDATE",
    )
    .bind(channel_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let channel =
        channel.ok_or_else(|| ApiError::not_found("channel_not_found", "Channel was not found"))?;
    let actor = member::require_actor_tx(&mut transaction, user.id, channel.space_id).await?;
    if channel.kind == "direct" || channel.slug.as_deref() == Some("general") {
        return Err(ApiError::validation(
            "channel_not_archivable",
            "general and direct Channels cannot be archived here",
        ));
    }
    if actor.access_level != "owner"
        && actor.access_level != "admin"
        && actor.id != channel.created_by_member_id
    {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Only Owner, Admin, or the Channel creator can archive this Channel",
        ));
    }
    let scope = format!("channel:{channel_id}:archive");
    let request_hash = idempotency::request_hash(&channel_id)?;
    if let Some((_status, response)) =
        idempotency::begin::<ChannelResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }
    if channel.archived_at.is_none() {
        let now = OffsetDateTime::now_utc();
        sqlx::query("UPDATE channels SET archived_at = $2 WHERE id = $1")
            .bind(channel_id)
            .bind(now)
            .execute(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
        record_channel_event(
            &mut transaction,
            channel.space_id,
            actor.id,
            channel_id,
            "channel.updated",
            now,
        )
        .await?;
    }
    let response = channel_by_id_tx(&mut transaction, channel_id, actor.id).await?;
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(response))
}

fn validate_create(mut request: CreateChannelRequest) -> Result<CreateChannelRequest, ApiError> {
    request.name = request.name.trim().to_owned();
    request.slug = request.slug.trim().to_owned();
    request.topic = request
        .topic
        .map(|topic| topic.trim().to_owned())
        .filter(|topic| !topic.is_empty());
    if !(1..=80).contains(&request.name.chars().count()) {
        return Err(ApiError::validation(
            "invalid_channel_name",
            "Channel name must contain 1 to 80 characters",
        ));
    }
    if !valid_slug(&request.slug) {
        return Err(ApiError::validation(
            "invalid_channel_slug",
            "Channel slug must contain 1 to 32 lowercase letters, numbers, or single hyphens",
        ));
    }
    if request.kind != "public" && request.kind != "private" {
        return Err(ApiError::validation(
            "invalid_channel_kind",
            "Channel kind must be public or private",
        ));
    }
    if request
        .topic
        .as_ref()
        .is_some_and(|topic| topic.chars().count() > 200)
    {
        return Err(ApiError::validation(
            "invalid_channel_topic",
            "Channel topic must contain at most 200 characters",
        ));
    }
    Ok(request)
}

fn valid_slug(slug: &str) -> bool {
    (1..=32).contains(&slug.len())
        && !slug.starts_with('-')
        && !slug.ends_with('-')
        && !slug.contains("--")
        && slug
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
}

async fn can_create_channel(
    database: &sqlx::PgPool,
    actor: &member::ActorMember,
) -> Result<bool, ApiError> {
    if actor.access_level == "owner" || actor.access_level == "admin" {
        return Ok(true);
    }
    sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM member_permissions \
         WHERE member_id = $1 AND permission = 'channel:create')",
    )
    .bind(actor.id)
    .fetch_one(database)
    .await
    .map_err(ApiError::database)
}

async fn can_create_channel_tx(
    transaction: &mut Transaction<'_, Postgres>,
    actor: &member::ActorMember,
) -> Result<bool, ApiError> {
    if actor.access_level == "owner" || actor.access_level == "admin" {
        return Ok(true);
    }
    sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM member_permissions \
         WHERE member_id = $1 AND permission = 'channel:create')",
    )
    .bind(actor.id)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)
}

async fn channel_by_id_tx(
    transaction: &mut Transaction<'_, Postgres>,
    channel_id: Uuid,
    member_id: Uuid,
) -> Result<ChannelResponse, ApiError> {
    sqlx::query_as::<_, ChannelRow>(
        "SELECT channels.id, channels.space_id, channels.kind, channels.name, \
                channels.slug::text AS slug, channels.topic, \
                channels.created_by_member_id, \
                channels.archived_at, \
                EXISTS(SELECT 1 FROM channel_members \
                       WHERE channel_members.channel_id = channels.id \
                         AND channel_members.member_id = $2) AS joined \
         FROM channels WHERE channels.id = $1",
    )
    .bind(channel_id)
    .bind(member_id)
    .fetch_one(&mut **transaction)
    .await
    .map(Into::into)
    .map_err(ApiError::database)
}

async fn direct_message_by_id_tx(
    transaction: &mut Transaction<'_, Postgres>,
    channel_id: Uuid,
    actor_id: Uuid,
    other_member_id: Uuid,
) -> Result<DirectMessageResponse, ApiError> {
    sqlx::query_as::<_, DirectMessageRow>(
        "SELECT direct_channels.channel_id, direct_channels.space_id, \
                other.id AS other_member_id, other.kind AS other_member_kind, \
                other.display_name AS other_member_display_name, \
                other.handle AS other_member_handle, \
                other.access_level AS other_member_access_level, \
                COALESCE((SELECT array_agg(permission ORDER BY permission) \
                          FROM member_permissions WHERE member_id = other.id), \
                         ARRAY[]::text[]) AS other_member_permissions, \
                channels.created_at \
         FROM direct_channels JOIN channels ON channels.id = direct_channels.channel_id \
         JOIN members other ON other.id = $3 \
         WHERE direct_channels.channel_id = $1 \
           AND $2 IN (direct_channels.member_low_id, direct_channels.member_high_id) \
           AND $3 IN (direct_channels.member_low_id, direct_channels.member_high_id)",
    )
    .bind(channel_id)
    .bind(actor_id)
    .bind(other_member_id)
    .fetch_one(&mut **transaction)
    .await
    .map(Into::into)
    .map_err(ApiError::database)
}

fn canonical_member_pair(left: Uuid, right: Uuid) -> (Uuid, Uuid) {
    if left.as_bytes() < right.as_bytes() {
        (left, right)
    } else {
        (right, left)
    }
}

async fn record_channel_event(
    transaction: &mut Transaction<'_, Postgres>,
    space_id: Uuid,
    actor_id: Uuid,
    channel_id: Uuid,
    action: &str,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    sqlx::query(
        "INSERT INTO audit_events \
         (id, space_id, actor_member_id, action, subject_type, subject_id, created_at) \
         VALUES ($1, $2, $3, $4, 'channel', $5, $6)",
    )
    .bind(Uuid::now_v7())
    .bind(space_id)
    .bind(actor_id)
    .bind(action)
    .bind(channel_id)
    .bind(now)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    sqlx::query(
        "INSERT INTO outbox_events \
         (id, topic, aggregate_id, payload_json, created_at) \
         VALUES ($1, $2, $3, $4, $5)",
    )
    .bind(Uuid::now_v7())
    .bind(action)
    .bind(channel_id)
    .bind(serde_json::json!({ "space_id": space_id, "channel_id": channel_id }))
    .bind(now)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(())
}

fn unique_constraint(error: &sqlx::Error, constraint: &str) -> bool {
    error
        .as_database_error()
        .and_then(|database| database.constraint())
        == Some(constraint)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn channel_slug_matches_address_contract() {
        assert!(valid_slug("design"));
        assert!(valid_slug("x"));
        assert!(!valid_slug("Design"));
        assert!(!valid_slug("two--parts"));
    }
}
