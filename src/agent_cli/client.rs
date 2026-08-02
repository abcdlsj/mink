use std::path::Path;

use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};

use crate::{
    ids::IdempotencyKey,
    protocol::capability::{self, Response},
};

pub(crate) async fn call(
    action: capability::Action,
    idempotency_key: Option<IdempotencyKey>,
) -> Result<Response<serde_json::Value>, ClientError> {
    let socket = std::env::var_os("SUMI_SOCKET").ok_or(ClientError::Unauthenticated)?;
    let token = std::env::var("SUMI_RUN_TOKEN").map_err(|_| ClientError::Unauthenticated)?;
    call_with(Path::new(&socket), token, action, idempotency_key).await
}

pub(crate) async fn call_with(
    socket: &Path,
    run_token: String,
    action: capability::Action,
    idempotency_key: Option<IdempotencyKey>,
) -> Result<Response<serde_json::Value>, ClientError> {
    let mut stream = tokio::net::UnixStream::connect(socket)
        .await
        .map_err(|_| ClientError::Unavailable)?;
    let request = capability::Request {
        schema_version: capability::SCHEMA_VERSION,
        run_token,
        idempotency_key,
        action,
    };
    let mut frame = serde_json::to_vec(&request).map_err(|_| ClientError::Internal)?;
    frame.push(b'\n');
    stream
        .write_all(&frame)
        .await
        .map_err(|_| ClientError::Unavailable)?;
    let mut response = Vec::new();
    BufReader::new(stream)
        .read_until(b'\n', &mut response)
        .await
        .map_err(|_| ClientError::Unavailable)?;
    serde_json::from_slice(&response).map_err(|_| ClientError::Internal)
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, thiserror::Error)]
pub(crate) enum ClientError {
    #[error("Agent capability requires an active Run")]
    Unauthenticated,
    #[error("Computer capability endpoint is unavailable")]
    Unavailable,
    #[error("Computer returned an invalid capability response")]
    Internal,
}
