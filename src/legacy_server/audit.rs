use sqlx::{Postgres, Transaction};
use time::OffsetDateTime;
use uuid::Uuid;

use super::api_error::ApiError;

pub(super) struct Event<'a> {
    pub space_id: Uuid,
    pub actor_id: Option<Uuid>,
    pub action: &'a str,
    pub subject_type: &'a str,
    pub subject_id: Uuid,
    pub metadata: Option<serde_json::Value>,
    pub occurred_at: OffsetDateTime,
}

pub(super) async fn record(
    transaction: &mut Transaction<'_, Postgres>,
    event: Event<'_>,
) -> Result<(), ApiError> {
    let metadata = event.metadata.unwrap_or_else(|| serde_json::json!({}));
    sqlx::query(
        "INSERT INTO audit_events (id, space_id, actor_member_id, action, subject_type, \
         subject_id, metadata_json, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
    )
    .bind(Uuid::now_v7())
    .bind(event.space_id)
    .bind(event.actor_id)
    .bind(event.action)
    .bind(event.subject_type)
    .bind(event.subject_id)
    .bind(metadata)
    .bind(event.occurred_at)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(())
}

pub(super) async fn list_for_agent_admin(
    database: &sqlx::PgPool,
    agent_id: Uuid,
    before: Option<Uuid>,
    limit: i64,
) -> Result<serde_json::Value, ApiError> {
    if !(1..=100).contains(&limit) {
        return Err(ApiError::validation(
            "invalid_pagination",
            "Audit limit must be 1 to 100",
        ));
    }
    let actor: Option<(Uuid, String)> = sqlx::query_as(
        "SELECT members.space_id, members.access_level FROM members \
         JOIN agents ON agents.member_id = members.id WHERE members.id = $1 \
           AND members.kind = 'agent' AND members.retired_at IS NULL \
           AND agents.desired_lifecycle = 'active' AND agents.provision_status = 'ready'",
    )
    .bind(agent_id)
    .fetch_optional(database)
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
    let events: Vec<serde_json::Value> = sqlx::query_scalar(
        "SELECT jsonb_build_object( \
             'id', audit_events.id, 'action', audit_events.action, \
             'subject_type', audit_events.subject_type, 'subject_id', audit_events.subject_id, \
             'actor', CASE WHEN actors.id IS NULL THEN NULL ELSE jsonb_build_object( \
                 'id', actors.id, 'kind', actors.kind, 'display_name', actors.display_name, \
                 'handle', actors.handle) END, 'created_at', audit_events.created_at) \
         FROM audit_events LEFT JOIN members actors ON actors.id = audit_events.actor_member_id \
         WHERE audit_events.space_id = $1 AND ($2::uuid IS NULL OR audit_events.id < $2) \
           AND NOT (audit_events.subject_type = 'channel' AND EXISTS ( \
               SELECT 1 FROM channels hidden WHERE hidden.id = audit_events.subject_id \
                 AND hidden.kind = 'private' AND NOT EXISTS (SELECT 1 FROM channel_members own \
                     WHERE own.channel_id = hidden.id AND own.member_id = $3))) \
         ORDER BY audit_events.id DESC LIMIT $4",
    )
    .bind(space_id)
    .bind(before)
    .bind(agent_id)
    .bind(limit + 1)
    .fetch_all(database)
    .await
    .map_err(ApiError::database)?;
    let has_more = events.len() > limit as usize;
    let mut events = events;
    events.truncate(limit as usize);
    let next_before = events.last().and_then(|event| event.get("id")).cloned();
    Ok(serde_json::json!({
        "events": events,
        "has_more": has_more,
        "next_before": next_before,
    }))
}
