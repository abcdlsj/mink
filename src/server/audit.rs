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
