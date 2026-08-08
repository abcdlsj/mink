pub(crate) mod client;
pub(crate) mod commands;

use crate::protocol::capability::{Error, ErrorCode, Response};

pub(crate) async fn execute(
    cli: commands::AgentCli,
    stdin: Option<String>,
    output: &mut impl std::io::Write,
) -> u8 {
    let agent_create = cli.is_agent_create();
    let mut response = match cli.action(stdin).await {
        Ok((action, idempotency_key)) => match client::call(action, idempotency_key).await {
            Ok(response) => response,
            Err(error) => client_failure(error),
        },
        Err(message) => failure(ErrorCode::InvalidArgument, message, false),
    };
    if let Some(error) = response.error.as_mut() {
        error.message = hint_for_error(&error.message, agent_create);
    }
    let exit = if response.ok { 0 } else { 1 };
    if serde_json::to_writer(&mut *output, &response).is_err() || output.write_all(b"\n").is_err() {
        return 1;
    }
    exit
}

/// Appends a concrete next-step hint to every Agent CLI error the model can see.
pub(crate) fn hint_for_error(message: &str, agent_create: bool) -> String {
    if message.contains("Hint:") {
        return message.to_owned();
    }
    let normalized = message.to_ascii_lowercase();
    let hint = if normalized.contains("unrecognized subcommand") && normalized.contains("ack") {
        "use `sumi agent inbox ack <item-id> --json` to acknowledge; \
         `sumi agent inbox defer <item-id> --until <RFC3339> --json` to defer; \
         `sumi agent run yield --json` to yield"
    } else if normalized.contains("unexpected argument") && normalized.contains("--ack") {
        "`message send` has no `--ack` flag: send with `--body`, then acknowledge the Item \
         separately with `sumi agent inbox ack <item-id> --json`"
    } else if normalized.contains("--computer-id") {
        "`--computer-id` must be a UUID of an online Computer in this Space: run \
         `sumi agent discover agent.create --json` and copy one of the returned `computer_id` values"
    } else if normalized.contains("role-file could not be read")
        || normalized.contains("body-file could not be read")
        || normalized.contains("result-file could not be read")
    {
        "the shell starts in `workspace/`: create the file there (for example `test-role.md`) and \
         pass its relative path; the `write` tool accepts `workspace/...` and `memory/...` paths"
    } else if normalized.contains("--driver must be codex or builtin") {
        "use `--driver codex` or `--driver builtin`"
    } else if normalized.contains("agent display name or role is invalid") {
        "display name: Unicode letters and underscores only, no digits or spaces, max 40 \
         characters; role must be non-empty"
    } else if normalized.contains("request conflicts with current state") && agent_create {
        "the Computer is not online or does not belong to this Space: run \
         `sumi agent discover agent.create --json` and reuse one of its `computer_id` values"
    } else if normalized.contains("request conflicts with current state") {
        "refresh the current Thread or Channel snapshot, then retry once; if it still conflicts, \
         yield and report"
    } else if normalized.contains("--json is required") {
        "append `--json` to every `sumi agent` automation call"
    } else if normalized.contains("memory write requires --stdin") {
        "pipe the content in and add `--stdin`: `sumi agent memory write <path> --stdin --json`"
    } else if normalized.contains("--until must be an rfc3339 timestamp") {
        "pass an RFC3339 timestamp such as `2026-08-08T12:00:00Z`"
    } else if normalized.contains("--post-to must be focus or source") {
        "use `--post-to focus` or `--post-to source`"
    } else if normalized.contains("invalid close reason") {
        "use one of: invalid, duplicate, not_needed, obsolete, other"
    } else if normalized.contains("--body or --stdin is required")
        || normalized.contains("stdin is required")
    {
        "provide `--body <text>` or pass input on stdin with `--stdin`"
    } else if normalized.contains("operation must contain 1 to 100 characters") {
        "run `sumi agent discover agent.create --json`"
    } else if normalized.contains("permission denied") || normalized.contains("permission_denied") {
        "ask a Human Owner or Admin to grant the missing permission, or ask them to run the \
         action; do not retry the same write without it"
    } else if normalized.contains("context changed") || normalized.contains("context_changed") {
        "re-read the current Thread or Channel, then resubmit the write once; if it still \
         conflicts, yield and report"
    } else if normalized.contains("not found") || normalized.contains("not_found") {
        "re-read the current context for valid IDs: `sumi agent context current --json`, \
         `sumi agent thread read <thread-id> --json`, `sumi agent channel members <channel-id> --json`"
    } else if normalized.contains("rate limited")
        || normalized.contains("rate_limited")
        || normalized.contains("unavailable")
    {
        "wait briefly and retry; if the error persists, yield and report"
    } else {
        "check the exact syntax with `sumi agent --help` or `sumi agent <command> --help`; if the \
         error persists, refresh with `sumi agent context current --json` and retry, or yield and report"
    };
    format!("{message}\nHint: {hint}")
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
