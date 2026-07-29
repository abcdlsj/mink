use sqlx::{Postgres, Transaction};
use time::OffsetDateTime;
use uuid::Uuid;

use super::api_error::ApiError;

pub(super) async fn publish(
    transaction: &mut Transaction<'_, Postgres>,
    topic: &str,
    aggregate_id: Uuid,
    payload: serde_json::Value,
    occurred_at: OffsetDateTime,
) -> Result<(), ApiError> {
    sqlx::query(
        "INSERT INTO outbox_events (id, topic, aggregate_id, payload_json, created_at) \
         VALUES ($1, $2, $3, $4, $5)",
    )
    .bind(Uuid::now_v7())
    .bind(topic)
    .bind(aggregate_id)
    .bind(payload)
    .bind(occurred_at)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(())
}
