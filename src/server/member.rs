use axum::{
    Json,
    extract::{Path, State},
    http::StatusCode,
};
use axum_extra::extract::CookieJar;
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use email_address::EmailAddress;
use serde::{Deserialize, Serialize};
use sqlx::{FromRow, Postgres, Transaction};
use time::{Duration, OffsetDateTime};
use uuid::Uuid;

use super::{AppState, api_error::ApiError, auth, idempotency, space};
use crate::database;

const INVITATION_TTL_DAYS: i64 = 7;
const PERMISSIONS: &[&str] = &["agent:create", "channel:create"];

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct MemberResponse {
    pub id: Uuid,
    pub kind: String,
    pub display_name: String,
    pub handle: String,
    pub access_level: String,
    pub permissions: Vec<String>,
}

#[derive(FromRow)]
struct MemberRow {
    id: Uuid,
    kind: String,
    display_name: String,
    handle: String,
    access_level: String,
    permissions: Vec<String>,
}

impl From<MemberRow> for MemberResponse {
    fn from(row: MemberRow) -> Self {
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

#[derive(FromRow)]
pub(super) struct ActorMember {
    pub id: Uuid,
    pub access_level: String,
}

pub async fn list(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<MemberResponse>>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    require_actor(&state.database, user.id, space_id).await?;

    let members = sqlx::query_as::<_, MemberRow>(
        "SELECT members.id, members.kind, members.display_name, members.handle, \
                members.access_level, \
                COALESCE((SELECT array_agg(permission ORDER BY permission) \
                          FROM member_permissions \
                          WHERE member_permissions.member_id = members.id), \
                         ARRAY[]::text[]) AS permissions \
         FROM members \
         WHERE members.space_id = $1 AND members.retired_at IS NULL \
         ORDER BY CASE members.access_level \
                    WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, \
                  lower(members.display_name), members.id",
    )
    .bind(space_id)
    .fetch_all(&state.database)
    .await
    .map_err(ApiError::database)?;

    Ok(Json(members.into_iter().map(Into::into).collect()))
}

#[derive(Deserialize, Serialize)]
pub struct UpdateMemberRequest {
    pub access_level: Option<String>,
    pub permissions: Option<Vec<String>>,
}

pub async fn update(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path((space_id, member_id)): Path<(Uuid, Uuid)>,
    Json(mut request): Json<UpdateMemberRequest>,
) -> Result<Json<MemberResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    validate_update(&mut request)?;
    let request_hash = idempotency::request_hash(&request)?;
    let scope = format!("space:{space_id}:member:{member_id}:update");
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let actor = require_actor_tx(&mut transaction, user.id, space_id).await?;

    if let Some((_status, response)) =
        idempotency::begin::<MemberResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }

    let target_access: Option<String> = sqlx::query_scalar(
        "SELECT access_level FROM members \
         WHERE id = $1 AND space_id = $2 AND retired_at IS NULL FOR UPDATE",
    )
    .bind(member_id)
    .bind(space_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let target_access = target_access.ok_or_else(|| {
        ApiError::not_found("member_not_found", "Member was not found in this Space")
    })?;

    authorize_update(&actor, member_id, &target_access, &request)?;
    let final_access = request.access_level.as_deref().unwrap_or(&target_access);
    if request.permissions.is_some() && final_access != "member" {
        return Err(ApiError::validation(
            "permissions_require_member",
            "Explicit permissions can only be assigned to a Member",
        ));
    }

    if let Some(access_level) = &request.access_level {
        sqlx::query("UPDATE members SET access_level = $3 WHERE id = $1 AND space_id = $2")
            .bind(member_id)
            .bind(space_id)
            .bind(access_level)
            .execute(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
        sqlx::query("DELETE FROM member_permissions WHERE member_id = $1 AND space_id = $2")
            .bind(member_id)
            .bind(space_id)
            .execute(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
    }
    if let Some(permissions) = &request.permissions {
        sqlx::query("DELETE FROM member_permissions WHERE member_id = $1 AND space_id = $2")
            .bind(member_id)
            .bind(space_id)
            .execute(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
        for permission in permissions {
            sqlx::query(
                "INSERT INTO member_permissions \
                 (member_id, permission, granted_by_member_id, created_at, space_id) \
                 VALUES ($1, $2, $3, $4, $5)",
            )
            .bind(member_id)
            .bind(permission)
            .bind(actor.id)
            .bind(OffsetDateTime::now_utc())
            .bind(space_id)
            .execute(&mut *transaction)
            .await
            .map_err(ApiError::database)?;
        }
    }

    record_member_update(&mut transaction, space_id, actor.id, member_id).await?;
    let response = member_by_id_tx(&mut transaction, space_id, member_id).await?;
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(response))
}

#[derive(Deserialize)]
pub struct CreateInvitationRequest {
    pub email: String,
    pub invite_token: String,
}

#[derive(Serialize)]
struct CreateInvitationFingerprint<'a> {
    email: &'a str,
    token_hash: &'a str,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct InvitationResponse {
    pub id: Uuid,
    pub space_id: Uuid,
    pub space_name: String,
    pub space_slug: String,
    pub email: String,
    #[serde(with = "time::serde::rfc3339")]
    pub expires_at: OffsetDateTime,
}

pub async fn create_invitation(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(space_id): Path<Uuid>,
    Json(request): Json<CreateInvitationRequest>,
) -> Result<(StatusCode, Json<InvitationResponse>), ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let email = request.email.trim().to_lowercase();
    if !EmailAddress::is_valid(&email) {
        return Err(ApiError::validation(
            "invalid_email",
            "Email address is invalid",
        ));
    }
    validate_invite_token(&request.invite_token)?;
    let token_hash = auth::token_hash(&request.invite_token);
    let fingerprint = CreateInvitationFingerprint {
        email: &email,
        token_hash: &token_hash,
    };
    let request_hash = idempotency::request_hash(&fingerprint)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let actor = require_actor_tx(&mut transaction, user.id, space_id).await?;
    if actor.access_level != "owner" && actor.access_level != "admin" {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Only Owner or Admin can invite a Human",
        ));
    }
    let scope = format!("space:{space_id}:human-invitation:create");
    if let Some((status, response)) =
        idempotency::begin::<InvitationResponse>(&mut transaction, &scope, key, &request_hash)
            .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok((status, Json(response)));
    }

    let already_member: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM human_members \
         JOIN users ON users.id = human_members.user_id \
         WHERE human_members.space_id = $1 AND users.email_normalized = $2)",
    )
    .bind(space_id)
    .bind(&email)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if already_member {
        return Err(ApiError::conflict(
            "already_member",
            "This Human is already a Member of the Space",
        ));
    }

    let (space_name, space_slug): (String, String) =
        sqlx::query_as("SELECT name, slug::text FROM spaces WHERE id = $1 AND deleted_at IS NULL")
            .bind(space_id)
            .fetch_optional(&mut *transaction)
            .await
            .map_err(ApiError::database)?
            .ok_or_else(|| ApiError::not_found("space_not_found", "Space was not found"))?;
    let id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let expires_at = now + Duration::days(INVITATION_TTL_DAYS);
    sqlx::query(
        "INSERT INTO human_invitations \
         (id, space_id, email_normalized, token_hash, invited_by_member_id, expires_at, created_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $7)",
    )
    .bind(id)
    .bind(space_id)
    .bind(&email)
    .bind(token_hash)
    .bind(actor.id)
    .bind(expires_at)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(|error| {
        if database::is_unique_constraint(&error, "human_invitations_token_hash_key") {
            ApiError::conflict("invite_token_conflict", "Invitation token must be regenerated")
        } else {
            ApiError::database(error)
        }
    })?;
    super::audit::record(
        &mut transaction,
        super::audit::Event {
            space_id,
            actor_id: Some(actor.id),
            action: "human_invitation.created",
            subject_type: "human_invitation",
            subject_id: id,
            metadata: Some(serde_json::json!({ "email": email })),
            occurred_at: now,
        },
    )
    .await?;

    let response = InvitationResponse {
        id,
        space_id,
        space_name,
        space_slug,
        email,
        expires_at,
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

#[derive(FromRow)]
struct InvitationRow {
    id: Uuid,
    space_id: Uuid,
    email: String,
    expires_at: OffsetDateTime,
    accepted_by_member_id: Option<Uuid>,
    accepted_at: Option<OffsetDateTime>,
    revoked_at: Option<OffsetDateTime>,
}

pub async fn invitation(
    State(state): State<std::sync::Arc<AppState>>,
    Path(invite_token): Path<String>,
) -> Result<Json<InvitationResponse>, ApiError> {
    let token_hash = invite_token_hash(&invite_token)?;
    let row = sqlx::query_as::<_, InvitationPreviewRow>(
        "SELECT human_invitations.id, human_invitations.space_id, \
                spaces.name AS space_name, spaces.slug::text AS space_slug, \
                human_invitations.email_normalized::text AS email, \
                human_invitations.expires_at, human_invitations.accepted_at, \
                human_invitations.revoked_at \
         FROM human_invitations \
         JOIN spaces ON spaces.id = human_invitations.space_id \
         WHERE human_invitations.token_hash = $1 AND spaces.deleted_at IS NULL",
    )
    .bind(token_hash)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(invitation_not_found)?;
    row.active()?;
    Ok(Json(row.into()))
}

#[derive(FromRow)]
struct InvitationPreviewRow {
    id: Uuid,
    space_id: Uuid,
    space_name: String,
    space_slug: String,
    email: String,
    expires_at: OffsetDateTime,
    accepted_at: Option<OffsetDateTime>,
    revoked_at: Option<OffsetDateTime>,
}

impl InvitationPreviewRow {
    fn active(&self) -> Result<(), ApiError> {
        if self.accepted_at.is_some() || self.revoked_at.is_some() {
            return Err(invitation_not_found());
        }
        if self.expires_at <= OffsetDateTime::now_utc() {
            return Err(ApiError::gone(
                "invitation_expired",
                "This invitation has expired",
            ));
        }
        Ok(())
    }
}

impl From<InvitationPreviewRow> for InvitationResponse {
    fn from(row: InvitationPreviewRow) -> Self {
        Self {
            id: row.id,
            space_id: row.space_id,
            space_name: row.space_name,
            space_slug: row.space_slug,
            email: row.email,
            expires_at: row.expires_at,
        }
    }
}

pub async fn accept_invitation(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(invite_token): Path<String>,
) -> Result<Json<MemberResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let token_hash = invite_token_hash(&invite_token)?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let invitation = sqlx::query_as::<_, InvitationRow>(
        "SELECT id, space_id, email_normalized::text AS email, expires_at, \
                accepted_by_member_id, accepted_at, revoked_at \
         FROM human_invitations WHERE token_hash = $1 FOR UPDATE",
    )
    .bind(token_hash)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(invitation_not_found)?;
    let scope = format!("invitation:{}:user:{}:accept", invitation.id, user.id);
    let request_hash = idempotency::request_hash(&user.id)?;
    if let Some((_status, response)) =
        idempotency::begin::<MemberResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }

    validate_acceptance(&invitation, &user.email)?;
    sqlx::query("SELECT id FROM spaces WHERE id = $1 AND deleted_at IS NULL FOR UPDATE")
        .bind(invitation.space_id)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(ApiError::database)?
        .ok_or_else(|| ApiError::not_found("space_not_found", "Space was not found"))?;
    let existing: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM human_members \
         WHERE space_id = $1 AND user_id = $2)",
    )
    .bind(invitation.space_id)
    .bind(user.id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if existing {
        return Err(ApiError::conflict(
            "already_member",
            "This Human is already a Member of the Space",
        ));
    }

    let member_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let handle = unique_handle(&mut transaction, invitation.space_id, &user.display_name).await?;
    sqlx::query(
        "INSERT INTO members \
         (id, space_id, kind, display_name, handle, avatar_seed, access_level, created_at) \
         VALUES ($1, $2, 'human', $3, $4, $5, 'member', $6)",
    )
    .bind(member_id)
    .bind(invitation.space_id)
    .bind(&user.display_name)
    .bind(&handle)
    .bind(member_id.to_string())
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    sqlx::query("INSERT INTO human_members (member_id, space_id, user_id) VALUES ($1, $2, $3)")
        .bind(member_id)
        .bind(invitation.space_id)
        .bind(user.id)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    let general_membership = sqlx::query(
        "INSERT INTO channel_members (channel_id, member_id, space_id, joined_at) \
         SELECT id, $2, $1, $3 FROM channels \
         WHERE space_id = $1 AND slug = 'general' AND archived_at IS NULL",
    )
    .bind(invitation.space_id)
    .bind(member_id)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    if general_membership.rows_affected() != 1 {
        tracing::error!(
            space_id = %invitation.space_id,
            "Space has no active general Channel"
        );
        return Err(ApiError::Internal);
    }
    sqlx::query(
        "UPDATE human_invitations SET accepted_by_member_id = $2, accepted_at = $3 \
         WHERE id = $1",
    )
    .bind(invitation.id)
    .bind(member_id)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    sqlx::query(
        "UPDATE human_invitations SET revoked_at = $4 \
         WHERE space_id = $1 AND email_normalized = $2 AND id <> $3 \
           AND accepted_at IS NULL AND revoked_at IS NULL",
    )
    .bind(invitation.space_id)
    .bind(&invitation.email)
    .bind(invitation.id)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    record_member_joined(
        &mut transaction,
        invitation.space_id,
        member_id,
        invitation.id,
        now,
    )
    .await?;

    let response = MemberResponse {
        id: member_id,
        kind: "human".to_owned(),
        display_name: user.display_name,
        handle,
        access_level: "member".to_owned(),
        permissions: Vec::new(),
    };
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(response))
}

pub(super) async fn require_actor(
    database: &sqlx::PgPool,
    user_id: Uuid,
    space_id: Uuid,
) -> Result<ActorMember, ApiError> {
    sqlx::query_as::<_, ActorMember>(
        "SELECT members.id, members.access_level FROM members \
         JOIN human_members ON human_members.member_id = members.id \
         JOIN spaces ON spaces.id = members.space_id \
         WHERE human_members.user_id = $1 AND members.space_id = $2 \
           AND members.retired_at IS NULL AND spaces.deleted_at IS NULL",
    )
    .bind(user_id)
    .bind(space_id)
    .fetch_optional(database)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| ApiError::forbidden("permission_denied", "Space membership is required"))
}

pub(super) async fn require_actor_tx(
    transaction: &mut Transaction<'_, Postgres>,
    user_id: Uuid,
    space_id: Uuid,
) -> Result<ActorMember, ApiError> {
    sqlx::query_as::<_, ActorMember>(
        "SELECT members.id, members.access_level FROM members \
         JOIN human_members ON human_members.member_id = members.id \
         JOIN spaces ON spaces.id = members.space_id \
         WHERE human_members.user_id = $1 AND members.space_id = $2 \
           AND members.retired_at IS NULL AND spaces.deleted_at IS NULL",
    )
    .bind(user_id)
    .bind(space_id)
    .fetch_optional(&mut **transaction)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| ApiError::forbidden("permission_denied", "Space membership is required"))
}

fn validate_update(request: &mut UpdateMemberRequest) -> Result<(), ApiError> {
    if request.access_level.is_none() && request.permissions.is_none() {
        return Err(ApiError::validation(
            "empty_member_update",
            "At least one Member setting must be provided",
        ));
    }
    if let Some(access_level) = &request.access_level
        && access_level != "admin"
        && access_level != "member"
    {
        return Err(ApiError::validation(
            "invalid_access_level",
            "Access level must be admin or member",
        ));
    }
    if let Some(permissions) = &mut request.permissions {
        permissions.sort();
        permissions.dedup();
        if permissions
            .iter()
            .any(|permission| !PERMISSIONS.contains(&permission.as_str()))
        {
            return Err(ApiError::validation(
                "invalid_permission",
                "Permission must be channel:create or agent:create",
            ));
        }
    }
    Ok(())
}

fn authorize_update(
    actor: &ActorMember,
    target_id: Uuid,
    target_access: &str,
    request: &UpdateMemberRequest,
) -> Result<(), ApiError> {
    if target_access == "owner" {
        return Err(ApiError::forbidden(
            "owner_update_forbidden",
            "Owner must be changed through Owner transfer",
        ));
    }
    match actor.access_level.as_str() {
        "owner" => Ok(()),
        "admin" => {
            if request.access_level.is_some() || target_access != "member" || actor.id == target_id
            {
                Err(ApiError::forbidden(
                    "permission_denied",
                    "Admin can only update explicit permissions for a Member",
                ))
            } else {
                Ok(())
            }
        }
        _ => Err(ApiError::forbidden(
            "permission_denied",
            "Owner or Admin permission is required",
        )),
    }
}

fn validate_invite_token(token: &str) -> Result<(), ApiError> {
    let decoded = URL_SAFE_NO_PAD.decode(token).map_err(|_| {
        ApiError::validation(
            "invalid_invite_token",
            "Invitation token must contain 32 random bytes",
        )
    })?;
    if decoded.len() != 32 {
        return Err(ApiError::validation(
            "invalid_invite_token",
            "Invitation token must contain 32 random bytes",
        ));
    }
    Ok(())
}

fn invite_token_hash(token: &str) -> Result<String, ApiError> {
    validate_invite_token(token).map_err(|_| invitation_not_found())?;
    Ok(auth::token_hash(token))
}

fn invitation_not_found() -> ApiError {
    ApiError::not_found("invitation_not_found", "Invitation was not found")
}

fn validate_acceptance(invitation: &InvitationRow, email: &str) -> Result<(), ApiError> {
    if invitation.accepted_by_member_id.is_some() || invitation.accepted_at.is_some() {
        return Err(ApiError::conflict(
            "invitation_used",
            "This invitation has already been accepted",
        ));
    }
    if invitation.revoked_at.is_some() {
        return Err(invitation_not_found());
    }
    if invitation.expires_at <= OffsetDateTime::now_utc() {
        return Err(ApiError::gone(
            "invitation_expired",
            "This invitation has expired",
        ));
    }
    if invitation.email != email {
        return Err(ApiError::forbidden(
            "invitation_email_mismatch",
            "Sign in with the email address that received this invitation",
        ));
    }
    Ok(())
}

async fn unique_handle(
    transaction: &mut Transaction<'_, Postgres>,
    space_id: Uuid,
    display_name: &str,
) -> Result<String, ApiError> {
    let base = space::member_handle(display_name);
    let exists: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM members WHERE space_id = $1 AND lower(handle) = lower($2))",
    )
    .bind(space_id)
    .bind(&base)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    if !exists {
        return Ok(base);
    }
    let prefix = base.chars().take(25).collect::<String>();
    Ok(format!(
        "{}-{}",
        prefix.trim_end_matches('-'),
        &Uuid::now_v7().simple().to_string()[..6]
    ))
}

async fn member_by_id_tx(
    transaction: &mut Transaction<'_, Postgres>,
    space_id: Uuid,
    member_id: Uuid,
) -> Result<MemberResponse, ApiError> {
    sqlx::query_as::<_, MemberRow>(
        "SELECT members.id, members.kind, members.display_name, members.handle, \
                members.access_level, \
                COALESCE((SELECT array_agg(permission ORDER BY permission) \
                          FROM member_permissions \
                          WHERE member_permissions.member_id = members.id), \
                         ARRAY[]::text[]) AS permissions \
         FROM members WHERE members.id = $1 AND members.space_id = $2",
    )
    .bind(member_id)
    .bind(space_id)
    .fetch_one(&mut **transaction)
    .await
    .map(Into::into)
    .map_err(ApiError::database)
}

async fn record_member_update(
    transaction: &mut Transaction<'_, Postgres>,
    space_id: Uuid,
    actor_id: Uuid,
    member_id: Uuid,
) -> Result<(), ApiError> {
    let now = OffsetDateTime::now_utc();
    super::audit::record(
        transaction,
        super::audit::Event {
            space_id,
            actor_id: Some(actor_id),
            action: "member.updated",
            subject_type: "member",
            subject_id: member_id,
            metadata: None,
            occurred_at: now,
        },
    )
    .await?;
    publish_member_update(transaction, space_id, member_id, now).await
}

async fn record_member_joined(
    transaction: &mut Transaction<'_, Postgres>,
    space_id: Uuid,
    member_id: Uuid,
    invitation_id: Uuid,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    super::audit::record(
        transaction,
        super::audit::Event {
            space_id,
            actor_id: Some(member_id),
            action: "member.joined",
            subject_type: "member",
            subject_id: member_id,
            metadata: Some(serde_json::json!({ "invitation_id": invitation_id })),
            occurred_at: now,
        },
    )
    .await?;
    publish_member_update(transaction, space_id, member_id, now).await
}

async fn publish_member_update(
    transaction: &mut Transaction<'_, Postgres>,
    space_id: Uuid,
    member_id: Uuid,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    super::outbox::publish(
        transaction,
        "member.updated",
        member_id,
        serde_json::json!({ "space_id": space_id, "member_id": member_id }),
        now,
    )
    .await
}
