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

use super::{
    AppState,
    agent_registry::{self, CreateAgentRequest},
    api_error::ApiError,
    auth, idempotency, member,
};

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct AgentCreateApprovalPayload {
    pub computer_id: Uuid,
    pub name: String,
    pub role_text: String,
    pub driver_kind: String,
    pub access_level: String,
    pub permissions: Vec<String>,
}

#[derive(Clone, Debug, Deserialize, Serialize, FromRow)]
pub struct ApprovalResponse {
    pub id: Uuid,
    pub space_id: Uuid,
    #[sqlx(rename = "type")]
    #[serde(rename = "type")]
    pub approval_type: String,
    pub requested_by_member_id: Uuid,
    pub requester_name: String,
    pub payload: serde_json::Value,
    pub status: String,
    pub resolved_by_member_id: Option<Uuid>,
    pub created_at: OffsetDateTime,
    pub resolved_at: Option<OffsetDateTime>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct PendingApprovalResponse {
    pub approval_id: Uuid,
    pub status: String,
}

const APPROVAL_SELECT: &str = "SELECT approvals.id, approvals.space_id, approvals.type, approvals.requested_by_member_id, \
     requesters.display_name AS requester_name, approvals.payload_json AS payload, \
     approvals.status, approvals.resolved_by_member_id, approvals.created_at, approvals.resolved_at \
     FROM approvals JOIN members requesters ON requesters.id = approvals.requested_by_member_id";

pub async fn list(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<ApprovalResponse>>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let actor = member::require_actor(&state.database, user.id, space_id).await?;
    if !matches!(actor.access_level.as_str(), "owner" | "admin") {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Only a Human Owner or Admin can view Approvals",
        ));
    }
    let rows = sqlx::query_as::<_, ApprovalResponse>(&format!(
        "{APPROVAL_SELECT} WHERE approvals.space_id = $1 ORDER BY approvals.created_at DESC"
    ))
    .bind(space_id)
    .fetch_all(&state.database)
    .await
    .map_err(ApiError::database)?;
    Ok(Json(rows))
}

pub(super) async fn request_agent_create(
    database: &sqlx::PgPool,
    requested_by_member_id: Uuid,
    request: CreateAgentRequest,
    key: Uuid,
) -> Result<PendingApprovalResponse, ApiError> {
    request_agent_create_for_member(database, requested_by_member_id, request, key, "agent").await
}

pub(super) async fn request_human_agent_create(
    database: &sqlx::PgPool,
    requested_by_member_id: Uuid,
    request: CreateAgentRequest,
    key: Uuid,
) -> Result<PendingApprovalResponse, ApiError> {
    request_agent_create_for_member(database, requested_by_member_id, request, key, "human").await
}

async fn request_agent_create_for_member(
    database: &sqlx::PgPool,
    requested_by_member_id: Uuid,
    mut request: CreateAgentRequest,
    key: Uuid,
    requester_kind: &str,
) -> Result<PendingApprovalResponse, ApiError> {
    request.access_level = "member".to_owned();
    request.handle = None;
    agent_registry::validate(&mut request)?;
    let payload = AgentCreateApprovalPayload {
        computer_id: request.computer_id,
        name: request.name,
        role_text: request.role_text,
        driver_kind: request.driver_kind,
        access_level: request.access_level,
        permissions: Vec::new(),
    };
    let request_hash = idempotency::request_hash(&payload)?;
    let mut transaction = database.begin().await.map_err(ApiError::database)?;
    let requester: Option<(Uuid, String, String, Option<String>)> = sqlx::query_as(
        "SELECT members.space_id, members.access_level, members.kind, agents.status \
         FROM members LEFT JOIN agents ON agents.member_id = members.id \
         WHERE members.id = $1 AND members.retired_at IS NULL FOR UPDATE OF members",
    )
    .bind(requested_by_member_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let (space_id, access_level, member_kind, agent_status) = requester
        .ok_or_else(|| ApiError::forbidden("permission_denied", "Current Member is not active"))?;
    if member_kind != requester_kind
        || (requester_kind == "agent" && agent_status.as_deref() != Some("active"))
    {
        return Err(ApiError::forbidden(
            "permission_denied",
            format!("Current {requester_kind} is not active"),
        ));
    }
    let allowed = matches!(access_level.as_str(), "owner" | "admin")
        || sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM member_permissions \
             WHERE member_id = $1 AND permission = 'agent:create')",
        )
        .bind(requested_by_member_id)
        .fetch_one(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    if !allowed {
        return Err(ApiError::forbidden(
            "permission_denied",
            "agent:create permission is required",
        ));
    }
    let scope = format!("member:{requested_by_member_id}:agent:create:approval");
    let key = idempotency::IdempotencyKey(key);
    if let Some((_status, response)) =
        idempotency::begin::<PendingApprovalResponse>(&mut transaction, &scope, key, &request_hash)
            .await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(response);
    }
    require_online_computer(&mut transaction, payload.computer_id, space_id).await?;
    let approval_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    sqlx::query(
        "INSERT INTO approvals \
         (id, space_id, type, requested_by_member_id, payload_json, status, created_at) \
         VALUES ($1, $2, 'agent.create', $3, $4, 'pending', $5)",
    )
    .bind(approval_id)
    .bind(space_id)
    .bind(requested_by_member_id)
    .bind(serde_json::to_value(&payload).map_err(|_| ApiError::Internal)?)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    let approvers: Vec<Uuid> = sqlx::query_scalar(
        "SELECT members.id FROM members \
         JOIN human_members ON human_members.member_id = members.id \
         WHERE members.space_id = $1 AND members.access_level IN ('owner', 'admin') \
           AND members.retired_at IS NULL",
    )
    .bind(space_id)
    .fetch_all(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    for approver_id in approvers {
        sqlx::query(
            "INSERT INTO inbox_items \
             (id, member_id, space_id, kind, priority, approval_id, status, available_at, created_at) \
             VALUES ($1, $2, $3, 'approval', 'hard', $4, 'pending', $5, $5)",
        )
        .bind(Uuid::now_v7())
        .bind(approver_id)
        .bind(space_id)
        .bind(approval_id)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(ApiError::database)?;
    }
    insert_audit(
        &mut transaction,
        space_id,
        requested_by_member_id,
        "approval.created",
        approval_id,
        serde_json::json!({ "type": "agent.create", "computer_id": payload.computer_id }),
        now,
    )
    .await?;
    insert_event(
        &mut transaction,
        "approval.created",
        approval_id,
        space_id,
        serde_json::json!({ "approval_id": approval_id, "status": "pending" }),
        now,
    )
    .await?;
    let response = PendingApprovalResponse {
        approval_id,
        status: "pending".to_owned(),
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
    Ok(response)
}

pub async fn approve(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(approval_id): Path<Uuid>,
) -> Result<Json<ApprovalResponse>, ApiError> {
    resolve(state, jar, key, approval_id, true).await
}

pub async fn reject(
    State(state): State<std::sync::Arc<AppState>>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    Path(approval_id): Path<Uuid>,
) -> Result<Json<ApprovalResponse>, ApiError> {
    resolve(state, jar, key, approval_id, false).await
}

async fn resolve(
    state: std::sync::Arc<AppState>,
    jar: CookieJar,
    key: idempotency::IdempotencyKey,
    approval_id: Uuid,
    approved: bool,
) -> Result<Json<ApprovalResponse>, ApiError> {
    let user = auth::current_user(&state, &jar).await?;
    let mut transaction = state.database.begin().await.map_err(ApiError::database)?;
    let current = find_tx(&mut transaction, approval_id).await?;
    let actor = member::require_actor_tx(&mut transaction, user.id, current.space_id).await?;
    if !matches!(actor.access_level.as_str(), "owner" | "admin") {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Only a Human Owner or Admin can resolve an Approval",
        ));
    }
    if actor.id == current.requested_by_member_id {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Approval requesters cannot resolve their own request",
        ));
    }
    let scope = format!(
        "approval:{approval_id}:{}",
        if approved { "approve" } else { "reject" }
    );
    let request_hash = idempotency::request_hash(&approved)?;
    if let Some((_status, response)) =
        idempotency::begin::<ApprovalResponse>(&mut transaction, &scope, key, &request_hash).await?
    {
        transaction.commit().await.map_err(ApiError::database)?;
        return Ok(Json(response));
    }
    if current.status != "pending" {
        return Err(ApiError::conflict(
            "approval_resolved",
            "Approval has already been resolved",
        ));
    }
    let payload: AgentCreateApprovalPayload =
        serde_json::from_value(current.payload.clone()).map_err(|_| ApiError::Internal)?;
    let now = OffsetDateTime::now_utc();
    if approved {
        require_online_computer(&mut transaction, payload.computer_id, current.space_id).await?;
        agent_registry::provision_agent_tx(
            &mut transaction,
            current.space_id,
            current.requested_by_member_id,
            CreateAgentRequest {
                computer_id: payload.computer_id,
                name: payload.name,
                handle: None,
                role_text: payload.role_text,
                access_level: payload.access_level,
                driver_kind: payload.driver_kind,
            },
        )
        .await?;
    }
    let status = if approved { "approved" } else { "rejected" };
    sqlx::query(
        "UPDATE approvals SET status = $2, resolved_by_member_id = $3, resolved_at = $4 \
         WHERE id = $1",
    )
    .bind(approval_id)
    .bind(status)
    .bind(actor.id)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    sqlx::query(
        "UPDATE inbox_items SET status = 'handled', handled_at = $2 \
         WHERE approval_id = $1 AND status IN ('pending', 'deferred')",
    )
    .bind(approval_id)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(ApiError::database)?;
    insert_audit(
        &mut transaction,
        current.space_id,
        actor.id,
        "approval.resolved",
        approval_id,
        serde_json::json!({ "status": status }),
        now,
    )
    .await?;
    insert_event(
        &mut transaction,
        "approval.resolved",
        approval_id,
        current.space_id,
        serde_json::json!({ "approval_id": approval_id, "status": status }),
        now,
    )
    .await?;
    let response = find_tx(&mut transaction, approval_id).await?;
    idempotency::finish(&mut transaction, &scope, key, StatusCode::OK, &response).await?;
    transaction.commit().await.map_err(ApiError::database)?;
    Ok(Json(response))
}

async fn find_tx(
    transaction: &mut Transaction<'_, Postgres>,
    approval_id: Uuid,
) -> Result<ApprovalResponse, ApiError> {
    sqlx::query_as::<_, ApprovalResponse>(&format!(
        "{APPROVAL_SELECT} WHERE approvals.id = $1 FOR UPDATE OF approvals"
    ))
    .bind(approval_id)
    .fetch_optional(&mut **transaction)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| ApiError::not_found("approval_not_found", "Approval was not found"))
}

async fn require_online_computer(
    transaction: &mut Transaction<'_, Postgres>,
    computer_id: Uuid,
    space_id: Uuid,
) -> Result<(), ApiError> {
    let status: Option<String> = sqlx::query_scalar(
        "SELECT status FROM computers WHERE id = $1 AND space_id = $2 FOR UPDATE",
    )
    .bind(computer_id)
    .bind(space_id)
    .fetch_optional(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    match status.as_deref() {
        Some("online") => Ok(()),
        Some(_) => Err(ApiError::conflict(
            "computer_offline",
            "Computer must be online",
        )),
        None => Err(ApiError::not_found(
            "computer_not_found",
            "Computer was not found",
        )),
    }
}

async fn insert_audit(
    transaction: &mut Transaction<'_, Postgres>,
    space_id: Uuid,
    actor_member_id: Uuid,
    action: &str,
    subject_id: Uuid,
    metadata: serde_json::Value,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    sqlx::query(
        "INSERT INTO audit_events \
         (id, space_id, actor_member_id, action, subject_type, subject_id, metadata_json, created_at) \
         VALUES ($1, $2, $3, $4, 'approval', $5, $6, $7)",
    )
    .bind(Uuid::now_v7())
    .bind(space_id)
    .bind(actor_member_id)
    .bind(action)
    .bind(subject_id)
    .bind(metadata)
    .bind(now)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(())
}

async fn insert_event(
    transaction: &mut Transaction<'_, Postgres>,
    topic: &str,
    aggregate_id: Uuid,
    space_id: Uuid,
    mut payload: serde_json::Value,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    payload["space_id"] = serde_json::Value::String(space_id.to_string());
    sqlx::query(
        "INSERT INTO outbox_events (id, topic, aggregate_id, payload_json, created_at) \
         VALUES ($1, $2, $3, $4, $5)",
    )
    .bind(Uuid::now_v7())
    .bind(topic)
    .bind(aggregate_id)
    .bind(payload)
    .bind(now)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(())
}
