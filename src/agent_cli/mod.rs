//! Run 内 Agent capability 客户端。该模块只依赖版本化 capability 协议。

pub(crate) mod client;
pub(crate) mod commands;

use crate::protocol::capability::{Error, ErrorCode, Response};

pub(crate) async fn execute(
    cli: commands::AgentCli,
    stdin: Option<String>,
    output: &mut impl std::io::Write,
) -> u8 {
    let response = match cli.action(stdin).await {
        Ok((action, idempotency_key)) => match client::call(action, idempotency_key).await {
            Ok(response) => response,
            Err(error) => client_failure(error),
        },
        Err(message) => failure(ErrorCode::InvalidArgument, message, false),
    };
    let exit = if response.ok { 0 } else { 1 };
    if serde_json::to_writer(&mut *output, &response).is_err() || output.write_all(b"\n").is_err() {
        return 1;
    }
    exit
}

fn client_failure(error: client::ClientError) -> Response<serde_json::Value> {
    match error {
        client::ClientError::Unauthenticated => failure(
            ErrorCode::Unauthenticated,
            "Agent capability requires an active Run",
            false,
        ),
        client::ClientError::Unavailable => failure(
            ErrorCode::Unavailable,
            "Computer capability endpoint is unavailable",
            true,
        ),
        client::ClientError::Internal => failure(
            ErrorCode::Internal,
            "Computer returned an invalid capability response",
            false,
        ),
    }
}

fn failure(code: ErrorCode, message: &str, retryable: bool) -> Response<serde_json::Value> {
    Response::failure(Error {
        code,
        message: message.to_owned(),
        retryable,
        details: Default::default(),
    })
}
