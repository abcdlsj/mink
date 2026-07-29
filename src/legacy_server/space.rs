use axum::{
    Json,
    extract::{Path, State},
    http::StatusCode,
};
use axum_extra::extract::CookieJar;
use serde::{Deserialize, Serialize};
use time::OffsetDateTime;
use uuid::Uuid;

use super::{AppState, api_error::ApiError, auth, idempotency, validation};
use crate::database;

const RESERVED_SLUGS: &[&str] = &[
    "api",
    "app",
    "auth",
    "login",
    "logout",
    "register",
    "admin",
    "settings",
    "spaces",
    "s",
    "attachments",
    "assets",
    "health",
];
const ACCENTS: &[&str] = &["#FE7DA8", "#27CCF3", "#FFD440", "#A9D877"];

#[derive(Debug, Deserialize, Serialize, utoipa::ToSchema)]
pub struct CreateSpaceRequest {
    pub name: String,
    pub slug: String,
    pub accent: String,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct UpdateSpaceRequest {
    pub name: Option<String>,
    pub accent: Option<String>,
}

#[derive(Clone, Debug, Deserialize, Serialize, utoipa::ToSchema)]
pub struct SpaceResponse {
    pub id: Uuid,
    pub name: String,
    pub slug: String,
    pub accent: String,
    pub owner_member_id: Uuid,
    pub current_member_id: Uuid,
    pub general_channel_id: Uuid,
}

pub async fn create(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Json(request): Json<CreateSpaceRequest>,
) -> Result<(StatusCode, Json<SpaceResponse>), ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let request = validate(request)?;
    let request_hash = idempotency::request_hash(&request)?;
    let scope = format!("user:{}:space:create", user.id);
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;

    if let Some((status, response)) =
        idempotency::begin::<SpaceResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok((status, Json(response)));
    }

    let space_id = Uuid::now_v7();
    let member_id = Uuid::now_v7();
    let channel_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let handle = member_handle(&user.display_name);

    sqlx::query(
        "INSERT INTO spaces \
         (id, slug, name, accent, owner_member_id, created_at, updated_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $6)",
    )
    .bind(space_id)
    .bind(&request.slug)
    .bind(&request.name)
    .bind(&request.accent)
    .bind(member_id)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(|error| {
        if database::is_unique_constraint(&error, "spaces_slug_key") {
            ApiError::conflict("slug_taken", "This Space slug is already in use")
        } else {
            ApiError::database(error)
        }
    })?;
    sqlx::query(
        "INSERT INTO members \
         (id, space_id, kind, display_name, handle, avatar_seed, access_level, created_at) \
         VALUES ($1, $2, 'human', $3, $4, $5, 'owner', $6)",
    )
    .bind(member_id)
    .bind(space_id)
    .bind(&user.display_name)
    .bind(handle)
    .bind(member_id.to_string())
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    sqlx::query("INSERT INTO human_members (member_id, space_id, user_id) VALUES ($1, $2, $3)")
        .bind(member_id)
        .bind(space_id)
        .bind(user.id)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    sqlx::query(
        "INSERT INTO channels \
         (id, space_id, kind, name, slug, created_by_member_id, created_at) \
         VALUES ($1, $2, 'public', 'general', 'general', $3, $4)",
    )
    .bind(channel_id)
    .bind(space_id)
    .bind(member_id)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    sqlx::query(
        "INSERT INTO channel_members (channel_id, member_id, space_id, joined_at) \
         VALUES ($1, $2, $3, $4)",
    )
    .bind(channel_id)
    .bind(member_id)
    .bind(space_id)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    super::audit::record(
        &mut transaction,
        super::audit::Event {
            space_id,
            actor_id: Some(member_id),
            action: "space.created",
            subject_type: "space",
            subject_id: space_id,
            metadata: None,
            occurred_at: now,
        },
    )
    .await?;
    super::outbox::publish(
        &mut transaction,
        "channel.created",
        channel_id,
        serde_json::json!({
            "channel_id": channel_id,
            "space_id": space_id,
            "slug": "general"
        }),
        now,
    )
    .await?;

    let response = SpaceResponse {
        id: space_id,
        name: request.name,
        slug: request.slug,
        accent: request.accent,
        owner_member_id: member_id,
        current_member_id: member_id,
        general_channel_id: channel_id,
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

pub async fn by_slug(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(space_slug): Path<String>,
) -> Result<Json<SpaceResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let response = sqlx::query_as::<_, SpaceRow>(
        "SELECT spaces.id, spaces.name, spaces.slug::text AS slug, spaces.accent, \
                spaces.owner_member_id, human_members.member_id AS current_member_id, \
                channels.id AS general_channel_id \
         FROM spaces \
         JOIN human_members ON human_members.space_id = spaces.id \
         JOIN channels ON channels.space_id = spaces.id AND channels.slug = 'general' \
         WHERE spaces.slug = $1 AND human_members.user_id = $2 \
           AND spaces.deleted_at IS NULL AND channels.archived_at IS NULL",
    )
    .bind(space_slug)
    .bind(user.id)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| ApiError::not_found("space_not_found", "Space was not found"))?;

    Ok(Json(response.into()))
}

pub async fn list(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
) -> Result<Json<Vec<SpaceResponse>>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let spaces = sqlx::query_as::<_, SpaceRow>(
        "SELECT spaces.id, spaces.name, spaces.slug::text AS slug, spaces.accent, \
                spaces.owner_member_id, human_members.member_id AS current_member_id, \
                channels.id AS general_channel_id \
         FROM spaces \
         JOIN human_members ON human_members.space_id = spaces.id \
         JOIN channels ON channels.space_id = spaces.id AND channels.slug = 'general' \
         WHERE human_members.user_id = $1 AND spaces.deleted_at IS NULL \
           AND channels.archived_at IS NULL ORDER BY spaces.name, spaces.id",
    )
    .bind(user.id)
    .fetch_all(&state.database)
    .await
    .map_err(ApiError::database)?
    .into_iter()
    .map(Into::into)
    .collect();
    Ok(Json(spaces))
}

pub(super) async fn update_for_agent_admin(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    mut request: UpdateSpaceRequest,
    key: Uuid,
) -> Result<serde_json::Value, ApiError> {
    if let Some(name) = &mut request.name {
        *name = name.trim().to_owned();
        if !(1..=60).contains(&name.chars().count()) {
            return Err(ApiError::validation(
                "invalid_space_name",
                "Space name must contain 1 to 60 characters",
            ));
        }
    }
    if request
        .accent
        .as_deref()
        .is_some_and(|accent| !ACCENTS.contains(&accent))
    {
        return Err(ApiError::validation(
            "invalid_space_accent",
            "Space accent is not one of the supported presets",
        ));
    }
    if request.name.is_none() && request.accent.is_none() {
        return Err(ApiError::validation(
            "empty_space_update",
            "Space update must include name or accent",
        ));
    }
    let request_hash = idempotency::request_hash(&request)?;
    let mut transaction = database.begin().await.map_err(ApiError::database)?;
    let actor: Option<(Uuid, String)> = sqlx::query_as(
        "SELECT members.space_id, members.access_level FROM members \
         JOIN agents ON agents.member_id = members.id WHERE members.id = $1 \
           AND members.kind = 'agent' AND members.retired_at IS NULL \
           AND agents.desired_lifecycle = 'active' AND agents.provision_status = 'ready' \
           FOR UPDATE OF members",
    )
    .bind(agent_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let (space_id, access_level) = actor.ok_or_else(|| {
        ApiError::forbidden("permission_denied", "Current Agent identity is not active")
    })?;
    if access_level != "admin" {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Agent Admin access is required",
        ));
    }
    let scope = format!("space:{space_id}:update");
    let key = idempotency::IdempotencyKey(key);
    if let Some((_status, response)) =
        idempotency::begin::<serde_json::Value>(&mut transaction, &scope, key, &request_hash)
            .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(response);
    }
    let now = OffsetDateTime::now_utc();
    let response: serde_json::Value = sqlx::query_scalar(
        "UPDATE spaces SET name = COALESCE($2, name), accent = COALESCE($3, accent), \
         updated_at = $4 WHERE id = $1 AND deleted_at IS NULL \
         RETURNING jsonb_build_object('id', id, 'name', name, 'slug', slug::text, \
             'accent', accent, 'updated_at', updated_at)",
    )
    .bind(space_id)
    .bind(&request.name)
    .bind(&request.accent)
    .bind(now)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    super::audit::record(
        &mut transaction,
        super::audit::Event {
            space_id,
            actor_id: Some(agent_id),
            action: "space.updated",
            subject_type: "space",
            subject_id: space_id,
            metadata: None,
            occurred_at: now,
        },
    )
    .await?;
    super::outbox::publish(
        &mut transaction,
        "space.updated",
        space_id,
        serde_json::json!({ "space_id": space_id }),
        now,
    )
    .await?;
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(response)
}

#[derive(sqlx::FromRow)]
struct SpaceRow {
    id: Uuid,
    name: String,
    slug: String,
    accent: String,
    owner_member_id: Uuid,
    current_member_id: Uuid,
    general_channel_id: Uuid,
}

impl From<SpaceRow> for SpaceResponse {
    fn from(row: SpaceRow) -> Self {
        Self {
            id: row.id,
            name: row.name,
            slug: row.slug,
            accent: row.accent,
            owner_member_id: row.owner_member_id,
            current_member_id: row.current_member_id,
            general_channel_id: row.general_channel_id,
        }
    }
}

fn validate(mut request: CreateSpaceRequest) -> Result<CreateSpaceRequest, ApiError> {
    request.name = request.name.trim().to_owned();
    if !(1..=60).contains(&request.name.chars().count()) {
        return Err(ApiError::validation(
            "invalid_space_name",
            "Space name must contain 1 to 60 characters",
        ));
    }
    if !validation::is_slug(&request.slug, 3, 32) || RESERVED_SLUGS.contains(&request.slug.as_str())
    {
        return Err(ApiError::validation(
            "invalid_space_slug",
            "Space slug must contain 3 to 32 lowercase letters, numbers, or single hyphens",
        ));
    }
    if !ACCENTS.contains(&request.accent.as_str()) {
        return Err(ApiError::validation(
            "invalid_space_accent",
            "Space accent is not one of the supported presets",
        ));
    }
    Ok(request)
}

pub(super) fn member_handle(display_name: &str) -> String {
    let handle = slug::slugify(display_name);
    let handle = handle.trim_matches('-');
    if handle.is_empty() {
        return "member".to_owned();
    }
    let mut shortened = handle.chars().take(32).collect::<String>();
    while shortened.ends_with('-') {
        shortened.pop();
    }
    shortened
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn handle_falls_back_for_non_transliterated_names() {
        assert_eq!(member_handle("Alice Zhang"), "alice-zhang");
        assert!(!member_handle("小墨").is_empty());
    }
}
