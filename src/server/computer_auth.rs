use axum::http::{HeaderMap, header};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;
use uuid::Uuid;

use super::{AppState, api_error::ApiError};

pub(super) fn decode_hash(value: &str, code: &'static str) -> Result<[u8; 32], ApiError> {
    URL_SAFE_NO_PAD
        .decode(value)
        .ok()
        .and_then(|bytes| bytes.try_into().ok())
        .ok_or_else(|| ApiError::validation(code, "Expected base64url-encoded 32 bytes"))
}

pub(super) fn bearer(headers: &HeaderMap) -> Result<&str, ApiError> {
    headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.strip_prefix("Bearer "))
        .filter(|value| !value.is_empty())
        .ok_or(ApiError::Unauthorized)
}

pub(super) async fn require_computer(
    state: &AppState,
    headers: &HeaderMap,
    computer_id: Uuid,
) -> Result<(), ApiError> {
    let token = bearer(headers)?;
    let expected: Option<(Vec<u8>, String)> =
        sqlx::query_as("SELECT token_hash, status FROM computers WHERE id = $1")
            .bind(computer_id)
            .fetch_optional(&state.database)
            .await
            .map_err(ApiError::database)?;
    let (expected_hash, status) = expected
        .ok_or_else(|| ApiError::not_found("computer_not_found", "Computer was not found"))?;
    if status == "revoked"
        || expected_hash.len() != 32
        || expected_hash
            .as_slice()
            .ct_eq(Sha256::digest(token.as_bytes()).as_slice())
            .unwrap_u8()
            != 1
    {
        return Err(ApiError::Unauthorized);
    }
    Ok(())
}

pub(super) async fn require_active_run(
    state: &AppState,
    headers: &HeaderMap,
    computer_id: Uuid,
    agent_id: Uuid,
    run_id: Uuid,
) -> Result<Uuid, ApiError> {
    require_computer(state, headers, computer_id).await?;
    sqlx::query_scalar(
        "SELECT members.space_id FROM agent_runs \
         JOIN agents ON agents.member_id = agent_runs.agent_member_id \
         JOIN members ON members.id = agents.member_id \
         WHERE agent_runs.id = $1 AND agent_runs.agent_member_id = $2 \
           AND agent_runs.computer_id = $3 AND agent_runs.status = 'running' \
           AND agents.status IN ('active', 'suspended') AND members.retired_at IS NULL",
    )
    .bind(run_id)
    .bind(agent_id)
    .bind(computer_id)
    .fetch_optional(&state.database)
    .await
    .map_err(ApiError::database)?
    .ok_or_else(|| {
        ApiError::forbidden(
            "permission_denied",
            "Agent run is not active on this Computer",
        )
    })
}
