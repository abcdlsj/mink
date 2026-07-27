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

use super::{AppState, api_error::ApiError, auth, idempotency, member, validation};
use crate::database;

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
    #[serde(with = "time::serde::rfc3339::option")]
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
struct ChannelMemberRow {
    id: Uuid,
    kind: String,
    display_name: String,
    handle: String,
    access_level: String,
    permissions: Vec<String>,
}

impl From<ChannelMemberRow> for member::MemberResponse {
    fn from(row: ChannelMemberRow) -> Self {
        Self {
            id: row.id,
            kind: row.kind,
            display_name: row.display_name,
            handle: row.handle,
            access_level: row.access_level,
            permissions: row.permissions,
        }
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ChannelMembersResponse {
    pub members: Vec<member::MemberResponse>,
    pub can_manage: bool,
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
    #[serde(with = "time::serde::rfc3339")]
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
    pub agent_member_ids: Vec<Uuid>,
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
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let actor = member::require_actor_tx(&mut transaction, user.id, space_id).await?;
    let (status, response) =
        create_channel_tx(&mut transaction, space_id, &actor, request, key).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok((status, Json(response)))
}

pub(super) async fn create_for_agent(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    request: CreateChannelRequest,
    key: Uuid,
) -> Result<ChannelResponse, ApiError> {
    let request = validate_create(request)?;
    let mut transaction = database.begin().await.map_err(ApiError::database)?;
    let actor: Option<(Uuid, String)> = sqlx::query_as(
        "SELECT space_id, access_level FROM members WHERE id = $1 AND kind = 'agent' \
         AND retired_at IS NULL FOR UPDATE",
    )
    .bind(agent_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let (space_id, access_level) = actor.ok_or_else(|| {
        ApiError::forbidden("permission_denied", "Current Agent identity is not active")
    })?;
    let actor = member::ActorMember {
        id: agent_id,
        access_level,
    };
    let (_status, response) = create_channel_tx(
        &mut transaction,
        space_id,
        &actor,
        request,
        idempotency::IdempotencyKey(key),
    )
    .await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(response)
}

async fn create_channel_tx(
    transaction: &mut Transaction<'_, Postgres>,
    space_id: Uuid,
    actor: &member::ActorMember,
    request: CreateChannelRequest,
    key: idempotency::IdempotencyKey,
) -> Result<(StatusCode, ChannelResponse), ApiError> {
    if !can_create_channel_tx(transaction, actor).await? {
        return Err(ApiError::forbidden(
            "permission_denied",
            "channel:create permission is required",
        ));
    }
    let request_hash = idempotency::request_hash(&request)?;
    let scope = format!("space:{space_id}:channel:create");
    if let Some((status, response)) =
        idempotency::begin::<ChannelResponse>(transaction, &scope, key, &request_hash).await?
    {
        return Ok((status, response));
    }

    let channel_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    require_active_agents(transaction, space_id, &request.agent_member_ids).await?;
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
    .execute(&mut **transaction)
    .await
    .map_err(|error| {
        if database::is_unique_constraint(&error, "channels_space_slug_unique") {
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
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    for agent_id in request
        .agent_member_ids
        .iter()
        .copied()
        .filter(|agent_id| *agent_id != actor.id)
    {
        sqlx::query(
            "INSERT INTO channel_members (channel_id, member_id, space_id, joined_at) \
             VALUES ($1, $2, $3, $4)",
        )
        .bind(channel_id)
        .bind(agent_id)
        .bind(space_id)
        .bind(now)
        .execute(&mut **transaction)
        .await
        .map_err(ApiError::database)?;
    }
    record_channel_event(
        transaction,
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
    idempotency::finish(transaction, &scope, key, StatusCode::CREATED, &response).await?;
    Ok((StatusCode::CREATED, response))
}

pub async fn list_members(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(channel_id): Path<Uuid>,
) -> Result<Json<ChannelMembersResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let channel: (Uuid, String, Uuid, Option<OffsetDateTime>) = sqlx::query_as(
        "SELECT space_id, kind, created_by_member_id, archived_at FROM channels WHERE id = $1",
    )
    .bind(channel_id)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| ApiError::not_found("channel_not_found", "Channel was not found"))?;
    let actor = member::require_actor(&state.database, user.id, channel.0).await?;
    let joined: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM channel_members WHERE channel_id = $1 AND member_id = $2)",
    )
    .bind(channel_id)
    .bind(actor.id)
    .fetch_one(&state.database)
    .await
    .map_err(ApiError::database)?;
    if !joined {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Only Channel Members can view its membership",
        ));
    }
    let members = channel_members(&state.database, channel_id).await?;
    Ok(Json(ChannelMembersResponse {
        members,
        can_manage: can_manage_members(&actor, channel.1.as_str(), channel.2, channel.3),
    }))
}

#[derive(Deserialize, Serialize)]
pub struct AddChannelAgentsRequest {
    pub agent_member_ids: Vec<Uuid>,
}

pub async fn add_agents(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(channel_id): Path<Uuid>,
    Json(mut request): Json<AddChannelAgentsRequest>,
) -> Result<Json<ChannelMembersResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    request.agent_member_ids.sort_unstable();
    request.agent_member_ids.dedup();
    if request.agent_member_ids.is_empty() {
        return Err(ApiError::validation(
            "agents_required",
            "Select at least one Agent",
        ));
    }
    let request_hash = idempotency::request_hash(&request)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let channel: (Uuid, String, Uuid, Option<OffsetDateTime>) = sqlx::query_as(
        "SELECT space_id, kind, created_by_member_id, archived_at \
         FROM channels WHERE id = $1 FOR UPDATE",
    )
    .bind(channel_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| ApiError::not_found("channel_not_found", "Channel was not found"))?;
    let actor = member::require_actor_tx(&mut transaction, user.id, channel.0).await?;
    if !can_manage_members(&actor, channel.1.as_str(), channel.2, channel.3) {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Only Owner, Admin, or the Channel creator can add Agents",
        ));
    }
    require_active_agents(&mut transaction, channel.0, &request.agent_member_ids).await?;
    let scope = format!("channel:{channel_id}:agents:add");
    if let Some((_status, response)) =
        idempotency::begin::<ChannelMembersResponse>(&mut transaction, &scope, key, &request_hash)
            .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }
    let now = OffsetDateTime::now_utc();
    let mut added = false;
    for agent_id in &request.agent_member_ids {
        let result = sqlx::query(
            "INSERT INTO channel_members (channel_id, member_id, space_id, joined_at) \
             VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING",
        )
        .bind(channel_id)
        .bind(agent_id)
        .bind(channel.0)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
        added |= result.rows_affected() == 1;
    }
    if added {
        record_channel_event(
            &mut transaction,
            channel.0,
            actor.id,
            channel_id,
            "channel.updated",
            now,
        )
        .await?;
    }
    let response = ChannelMembersResponse {
        members: channel_members(&mut *transaction, channel_id).await?,
        can_manage: true,
    };
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(response))
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

pub(super) async fn change_member_for_agent_admin(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    address: &str,
    member_id: Uuid,
    add: bool,
    key: Uuid,
) -> Result<serde_json::Value, ApiError> {
    let slug = agent_channel_slug(address)?;
    let mut transaction = database.begin().await.map_err(ApiError::database)?;
    let channel: Option<(Uuid, Uuid, String, Uuid, Option<OffsetDateTime>, String)> =
        sqlx::query_as(
            "SELECT channels.id, channels.space_id, channels.kind, \
                    channels.created_by_member_id, channels.archived_at, members.access_level \
             FROM channels JOIN channel_members own ON own.channel_id = channels.id \
             JOIN members ON members.id = own.member_id \
             JOIN agents ON agents.member_id = members.id \
             WHERE channels.slug = $1 AND own.member_id = $2 \
               AND members.retired_at IS NULL AND agents.status = 'active' FOR UPDATE OF channels",
        )
        .bind(slug)
        .bind(agent_id)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    let (channel_id, space_id, kind, creator_id, archived_at, access_level) =
        channel.ok_or_else(|| {
            ApiError::forbidden(
                "permission_denied",
                "Agent must be an explicit Member of this Channel",
            )
        })?;
    if access_level != "admin" && agent_id != creator_id {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Agent Admin access or Channel creator ownership is required",
        ));
    }
    if kind == "direct" {
        return Err(ApiError::validation(
            "direct_membership_fixed",
            "Direct Channel membership cannot be changed",
        ));
    }
    if archived_at.is_some() {
        return Err(ApiError::conflict(
            "channel_archived",
            "Archived Channel membership cannot be changed",
        ));
    }
    let target_active: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM members WHERE id = $1 AND space_id = $2 \
         AND retired_at IS NULL)",
    )
    .bind(member_id)
    .bind(space_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if !target_active {
        return Err(ApiError::not_found(
            "member_not_found",
            "Active Member was not found in this Space",
        ));
    }
    if !add && member_id == agent_id {
        return Err(ApiError::validation(
            "self_membership_required",
            "Agent cannot remove itself from a Channel through this action",
        ));
    }
    if !add && member_id == creator_id {
        return Err(ApiError::validation(
            "channel_creator_required",
            "Channel creator cannot be removed from the Channel",
        ));
    }
    let request = serde_json::json!({
        "address": address,
        "member_id": member_id,
        "operation": if add { "add" } else { "remove" },
    });
    let request_hash = idempotency::request_hash(&request)?;
    let scope = format!(
        "channel:{channel_id}:member:{member_id}:{}",
        if add { "add" } else { "remove" }
    );
    let key = idempotency::IdempotencyKey(key);
    if let Some((_status, response)) =
        idempotency::begin::<serde_json::Value>(&mut transaction, &scope, key, &request_hash)
            .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(response);
    }
    let changed = if add {
        sqlx::query(
            "INSERT INTO channel_members (channel_id, member_id, space_id, joined_at) \
             VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING",
        )
        .bind(channel_id)
        .bind(member_id)
        .bind(space_id)
        .bind(OffsetDateTime::now_utc())
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?
        .rows_affected()
            == 1
    } else {
        sqlx::query("DELETE FROM channel_members WHERE channel_id = $1 AND member_id = $2")
            .bind(channel_id)
            .bind(member_id)
            .execute(&mut *transaction)
            .await
            .map_err(ApiError::database)?
            .rows_affected()
            == 1
    };
    let now = OffsetDateTime::now_utc();
    if changed {
        record_channel_event(
            &mut transaction,
            space_id,
            agent_id,
            channel_id,
            "channel.updated",
            now,
        )
        .await?;
    }
    let response = serde_json::json!({
        "channel_id": channel_id,
        "address": format!("#{slug}"),
        "member_id": member_id,
        "membership": if add { "present" } else { "absent" },
        "changed": changed,
    });
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(response)
}

pub(super) async fn archive_for_agent_admin(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    address: &str,
    key: Uuid,
) -> Result<serde_json::Value, ApiError> {
    let slug = agent_channel_slug(address)?;
    let mut transaction = database.begin().await.map_err(ApiError::database)?;
    let channel: Option<(Uuid, Uuid, String, Uuid, Option<OffsetDateTime>, String)> =
        sqlx::query_as(
            "SELECT channels.id, channels.space_id, channels.kind, channels.created_by_member_id, \
                channels.archived_at, members.access_level FROM channels \
         JOIN channel_members own ON own.channel_id = channels.id \
         JOIN members ON members.id = own.member_id \
         JOIN agents ON agents.member_id = members.id \
         WHERE channels.slug = $1 AND own.member_id = $2 \
           AND members.retired_at IS NULL AND agents.status = 'active' FOR UPDATE OF channels",
        )
        .bind(slug)
        .bind(agent_id)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    let (channel_id, space_id, kind, creator_id, archived_at, access_level) =
        channel.ok_or_else(|| {
            ApiError::forbidden(
                "permission_denied",
                "Agent must be an explicit Member of this Channel",
            )
        })?;
    if access_level != "admin" && agent_id != creator_id {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Agent Admin access or Channel creator ownership is required",
        ));
    }
    if kind == "direct" || slug == "general" {
        return Err(ApiError::validation(
            "channel_not_archivable",
            "general and direct Channels cannot be archived here",
        ));
    }
    let request_hash = idempotency::request_hash(&address)?;
    let scope = format!("channel:{channel_id}:archive");
    let key = idempotency::IdempotencyKey(key);
    if let Some((_status, response)) =
        idempotency::begin::<serde_json::Value>(&mut transaction, &scope, key, &request_hash)
            .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(response);
    }
    let now = OffsetDateTime::now_utc();
    if archived_at.is_none() {
        sqlx::query("UPDATE channels SET archived_at = $2 WHERE id = $1")
            .bind(channel_id)
            .bind(now)
            .execute(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
        record_channel_event(
            &mut transaction,
            space_id,
            agent_id,
            channel_id,
            "channel.updated",
            now,
        )
        .await?;
    }
    let response = serde_json::json!({
        "channel_id": channel_id,
        "address": format!("#{slug}"),
        "archived": true,
    });
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(response)
}

fn agent_channel_slug(address: &str) -> Result<&str, ApiError> {
    let slug = address.strip_prefix('#').unwrap_or(address);
    if !validation::is_slug(slug, 1, 32) {
        return Err(ApiError::validation(
            "invalid_address",
            "Channel address must use #channel",
        ));
    }
    Ok(slug)
}

fn validate_create(mut request: CreateChannelRequest) -> Result<CreateChannelRequest, ApiError> {
    request.name = request.name.trim().to_owned();
    request.slug = request.slug.trim().to_owned();
    request.topic = request
        .topic
        .map(|topic| topic.trim().to_owned())
        .filter(|topic| !topic.is_empty());
    request.agent_member_ids.sort_unstable();
    request.agent_member_ids.dedup();
    if !(1..=80).contains(&request.name.chars().count()) {
        return Err(ApiError::validation(
            "invalid_channel_name",
            "Channel name must contain 1 to 80 characters",
        ));
    }
    if !validation::is_slug(&request.slug, 1, 32) {
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

fn can_manage_members(
    actor: &member::ActorMember,
    channel_kind: &str,
    creator_id: Uuid,
    archived_at: Option<OffsetDateTime>,
) -> bool {
    channel_kind != "direct"
        && archived_at.is_none()
        && (actor.access_level == "owner"
            || actor.access_level == "admin"
            || actor.id == creator_id)
}

async fn require_active_agents(
    transaction: &mut Transaction<'_, Postgres>,
    space_id: Uuid,
    agent_ids: &[Uuid],
) -> Result<(), ApiError> {
    if agent_ids.is_empty() {
        return Ok(());
    }
    let count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM members \
         WHERE id = ANY($1) AND space_id = $2 AND kind = 'agent' AND retired_at IS NULL",
    )
    .bind(agent_ids)
    .bind(space_id)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    if count != agent_ids.len() as i64 {
        return Err(ApiError::validation(
            "invalid_channel_agents",
            "Every selected Agent must be active in this Space",
        ));
    }
    Ok(())
}

async fn channel_members<'e, E>(
    executor: E,
    channel_id: Uuid,
) -> Result<Vec<member::MemberResponse>, ApiError>
where
    E: sqlx::Executor<'e, Database = Postgres>,
{
    let rows = sqlx::query_as::<_, ChannelMemberRow>(
        "SELECT members.id, members.kind, members.display_name, members.handle, \
                members.access_level, \
                COALESCE((SELECT array_agg(permission ORDER BY permission) \
                          FROM member_permissions WHERE member_id = members.id), \
                         ARRAY[]::text[]) AS permissions \
         FROM channel_members JOIN members ON members.id = channel_members.member_id \
         WHERE channel_members.channel_id = $1 AND members.retired_at IS NULL \
         ORDER BY CASE members.kind WHEN 'human' THEN 0 ELSE 1 END, lower(members.display_name)",
    )
    .bind(channel_id)
    .fetch_all(executor)
    .await
    .map_err(ApiError::database)?;
    Ok(rows.into_iter().map(Into::into).collect())
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
    super::audit::record(
        transaction,
        super::audit::Event {
            space_id,
            actor_id: Some(actor_id),
            action,
            subject_type: "channel",
            subject_id: channel_id,
            metadata: None,
            occurred_at: now,
        },
    )
    .await?;
    super::outbox::publish(
        transaction,
        action,
        channel_id,
        serde_json::json!({ "space_id": space_id, "channel_id": channel_id }),
        now,
    )
    .await
}
