use axum::{
    extract::FromRequestParts,
    http::{StatusCode, request::Parts},
};
use serde::{Serialize, de::DeserializeOwned};
use sha2::{Digest, Sha256};
use sqlx::{Postgres, Transaction};
use time::{Duration, OffsetDateTime};
use uuid::{Uuid, Version};

use super::api_error::ApiError;

#[derive(Clone, Copy, Debug)]
pub struct IdempotencyKey(pub Uuid);

impl IdempotencyKey {
    fn validate(self) -> Result<Self, ApiError> {
        if self.0.get_version() != Some(Version::SortRand) {
            return Err(ApiError::validation(
                "invalid_idempotency_key",
                "Idempotency-Key must be UUIDv7",
            ));
        }
        Ok(self)
    }
}

impl<S> FromRequestParts<S> for IdempotencyKey
where
    S: Send + Sync,
{
    type Rejection = ApiError;

    async fn from_request_parts(parts: &mut Parts, _state: &S) -> Result<Self, Self::Rejection> {
        let value = parts
            .headers
            .get("idempotency-key")
            .ok_or_else(|| {
                ApiError::validation(
                    "idempotency_key_required",
                    "Idempotency-Key header is required",
                )
            })?
            .to_str()
            .map_err(|_| {
                ApiError::validation("invalid_idempotency_key", "Idempotency-Key must be UUIDv7")
            })?;
        let key = Uuid::parse_str(value).map_err(|_| {
            ApiError::validation("invalid_idempotency_key", "Idempotency-Key must be UUIDv7")
        })?;
        Self(key).validate()
    }
}

pub fn request_hash<T: Serialize>(request: &T) -> Result<Vec<u8>, ApiError> {
    let encoded = serde_json::to_vec(request).map_err(|error| {
        tracing::error!(error = %error, "failed to serialize idempotency request");
        ApiError::Internal
    })?;
    Ok(Sha256::digest(encoded).to_vec())
}

pub async fn begin<T: DeserializeOwned>(
    transaction: &mut Transaction<'_, Postgres>,
    scope: &str,
    key: IdempotencyKey,
    hash: &[u8],
) -> Result<Option<(StatusCode, T)>, ApiError> {
    key.validate()?;
    let now = OffsetDateTime::now_utc();
    let inserted = sqlx::query(
        "INSERT INTO idempotency_records \
         (scope, idempotency_key, request_hash, created_at, expires_at) \
         VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING",
    )
    .bind(scope)
    .bind(key.0)
    .bind(hash)
    .bind(now)
    .bind(now + Duration::hours(24))
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;

    if inserted.rows_affected() == 1 {
        return Ok(None);
    }

    let record: (Vec<u8>, Option<i16>, Option<serde_json::Value>) = sqlx::query_as(
        "SELECT request_hash, response_status, response_json \
         FROM idempotency_records WHERE scope = $1 AND idempotency_key = $2",
    )
    .bind(scope)
    .bind(key.0)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ApiError::database)?;

    if record.0 != hash {
        return Err(ApiError::conflict(
            "idempotency_conflict",
            "Idempotency-Key was already used with a different request",
        ));
    }

    let status = record.1.ok_or(ApiError::Internal)?;
    let status = StatusCode::from_u16(status as u16).map_err(|_| ApiError::Internal)?;
    let response =
        serde_json::from_value(record.2.ok_or(ApiError::Internal)?).map_err(|error| {
            tracing::error!(error = %error, "invalid cached idempotency response");
            ApiError::Internal
        })?;
    Ok(Some((status, response)))
}

pub async fn finish<T: Serialize>(
    transaction: &mut Transaction<'_, Postgres>,
    scope: &str,
    key: IdempotencyKey,
    status: StatusCode,
    response: &T,
) -> Result<(), ApiError> {
    let response = serde_json::to_value(response).map_err(|error| {
        tracing::error!(error = %error, "failed to serialize idempotency response");
        ApiError::Internal
    })?;
    sqlx::query(
        "UPDATE idempotency_records SET response_status = $3, response_json = $4 \
         WHERE scope = $1 AND idempotency_key = $2",
    )
    .bind(scope)
    .bind(key.0)
    .bind(status.as_u16() as i16)
    .bind(response)
    .execute(&mut **transaction)
    .await
    .map_err(ApiError::database)?;
    Ok(())
}
