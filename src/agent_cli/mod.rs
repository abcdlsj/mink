pub(crate) mod client;
pub(crate) mod commands;

use std::collections::BTreeMap;

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
        let (message, details) = classify_error(&error.message, error.code, agent_create);
        error.message = message;
        for (key, value) in details {
            error.details.entry(key).or_insert(value);
        }
    }
    let exit = if response.ok { 0 } else { 1 };
    if serde_json::to_writer(&mut *output, &response).is_err() || output.write_all(b"\n").is_err() {
        return 1;
    }
    exit
}

/// Normalizes an Agent CLI error into a one-line diagnosis and structured details the model can
/// follow directly: `reason` classifies the failure, `next_action` gives the exact next command.
pub(crate) fn classify_error(
    message: &str,
    code: ErrorCode,
    agent_create: bool,
) -> (String, BTreeMap<String, serde_json::Value>) {
    let normalized = message.to_ascii_lowercase();
    let (diagnosis, reason, next_action) = if normalized.contains("unrecognized subcommand")
        && normalized.contains("ack")
    {
        (
            "unknown `message` subcommand `ack`".to_owned(),
            "unknown_subcommand",
            Some(
                "acknowledge with `sumi agent inbox ack <item-id> --json`; defer with `sumi agent inbox defer <item-id> --until <RFC3339> --json`; yield with `sumi agent run yield --json`",
            ),
        )
    } else if normalized.contains("unexpected argument") && normalized.contains("--ack") {
        (
            "`message send` does not accept `--ack`".to_owned(),
            "unknown_argument",
            Some(
                "send with `--body`, then acknowledge the Item separately with `sumi agent inbox ack <item-id> --json`",
            ),
        )
    } else if normalized.contains("--computer-id") {
        (
            "`--computer-id` must be a UUID of an online Computer in this Space".to_owned(),
            "invalid_computer_id",
            Some(
                "run `sumi agent discover agent.create --json` and copy one of the returned `computer_id` values",
            ),
        )
    } else if normalized.contains("role-file could not be read")
        || normalized.contains("body-file could not be read")
        || normalized.contains("result-file could not be read")
    {
        (
            message.to_owned(),
            "file_not_found",
            Some(
                "the shell starts in `workspace/`: create the file there (for example `test-role.md`) with the `write` tool and pass its relative path",
            ),
        )
    } else if normalized.contains("--driver must be codex or builtin") {
        (
            "`--driver` must be `codex` or `builtin`".to_owned(),
            "invalid_driver",
            Some("rerun with `--driver codex` or `--driver builtin`"),
        )
    } else if normalized.contains("agent display name or role is invalid") {
        (
            "Agent display name or role is invalid".to_owned(),
            "invalid_display_name_or_role",
            Some(
                "display name: Unicode letters and underscores only (no digits or spaces, max 40 characters); role must be non-empty",
            ),
        )
    } else if normalized.contains("request conflicts with current state") && agent_create {
        (
            "Computer is not online or does not belong to this Space".to_owned(),
            "computer_not_available",
            Some(
                "run `sumi agent discover agent.create --json` and reuse one of its `computer_id` values",
            ),
        )
    } else if normalized.contains("request conflicts with current state") {
        (
            "request conflicts with current state".to_owned(),
            "conflict",
            Some(
                "refresh the current Thread or Channel snapshot, then retry once; if it still conflicts, yield and report",
            ),
        )
    } else if normalized.contains("--json is required") {
        (
            "--json is required for Agent automation".to_owned(),
            "missing_json_flag",
            Some("append `--json` to every `sumi agent` automation call"),
        )
    } else if normalized.contains("memory write requires --stdin") {
        (
            "memory write requires `--stdin`".to_owned(),
            "missing_stdin_flag",
            Some("run `sumi agent memory write <path> --stdin --json` and pipe the content in"),
        )
    } else if normalized.contains("--until must be an rfc3339 timestamp") {
        (
            "`--until` must be an RFC3339 timestamp".to_owned(),
            "invalid_timestamp",
            Some("pass a timestamp such as `2026-08-08T12:00:00Z`"),
        )
    } else if normalized.contains("--post-to must be focus or source") {
        (
            "`--post-to` must be `focus` or `source`".to_owned(),
            "invalid_post_target",
            Some("use `--post-to focus` or `--post-to source`"),
        )
    } else if normalized.contains("invalid close reason") {
        (
            "invalid close reason".to_owned(),
            "invalid_close_reason",
            Some("use one of: invalid, duplicate, not_needed, obsolete, other"),
        )
    } else if normalized.contains("--body or --stdin is required")
        || normalized.contains("stdin is required")
    {
        (
            "message body or stdin is required".to_owned(),
            "missing_body",
            Some("provide `--body <text>` or pass input on stdin with `--stdin`"),
        )
    } else if normalized.contains("operation must contain 1 to 100 characters") {
        (
            "discover operation must contain 1 to 100 characters".to_owned(),
            "invalid_operation",
            Some("run `sumi agent discover agent.create --json`"),
        )
    } else {
        let diagnosis = message
            .lines()
            .next()
            .unwrap_or(message)
            .trim_start_matches("error: ")
            .to_owned();
        let (reason, next_action) = match code {
            ErrorCode::Unauthenticated => (
                "unauthenticated",
                Some(
                    "run inside an active Run with SUMI_SOCKET and SUMI_DRIVER_TOKEN set, then retry",
                ),
            ),
            ErrorCode::Unavailable => (
                "unavailable",
                Some("wait briefly and retry; if it persists, yield and report"),
            ),
            ErrorCode::Internal => (
                "internal",
                Some("yield and report; retrying the same call is unlikely to help"),
            ),
            ErrorCode::ContextChanged => (
                "context_changed",
                Some(
                    "re-read the current Thread or Channel, then resubmit once; if it still conflicts, yield and report",
                ),
            ),
            ErrorCode::PermissionDenied => (
                "permission_denied",
                Some(
                    "ask a Human Owner or Admin to grant the missing permission or run the action; do not retry without it",
                ),
            ),
            ErrorCode::NotFound => (
                "not_found",
                Some(
                    "re-read the current context for valid IDs: `sumi agent context current --json`, `sumi agent thread read <thread-id> --json`",
                ),
            ),
            ErrorCode::RateLimited => (
                "rate_limited",
                Some("wait briefly and retry; if it persists, yield and report"),
            ),
            ErrorCode::Conflict => (
                "conflict",
                Some(
                    "refresh the current Thread or Channel snapshot, then retry once; if it still conflicts, yield and report",
                ),
            ),
            ErrorCode::InvalidArgument => (
                "invalid_argument",
                Some(
                    "run `sumi agent --help` or `sumi agent <command> --help` for the exact syntax",
                ),
            ),
        };
        (diagnosis, reason, next_action)
    };
    let mut details = BTreeMap::new();
    details.insert("reason".to_owned(), serde_json::json!(reason));
    if let Some(next_action) = next_action {
        details.insert("next_action".to_owned(), serde_json::json!(next_action));
    }
    (diagnosis, details)
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
