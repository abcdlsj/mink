use sqlx::{Postgres, Transaction};
use uuid::Uuid;

use super::api_error::ApiError;

pub(super) async fn require_member(
    transaction: &mut Transaction<'_, Postgres>,
    channel_id: Uuid,
    member_id: Uuid,
) -> Result<(), ApiError> {
    let exists: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM channel_members WHERE channel_id = $1 AND member_id = $2)",
    )
    .bind(channel_id)
    .bind(member_id)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)?;

    if !exists {
        return Err(ApiError::forbidden(
            "permission_denied",
            "Channel membership is required",
        ));
    }
    Ok(())
}
