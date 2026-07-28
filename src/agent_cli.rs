use anyhow::{Context, Result, ensure};
use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};
use uuid::Uuid;

use crate::{
    cli::{
        AgentArgs, AgentAttachmentCommand, AgentAuditCommand, AgentChannelCommand,
        AgentChannelMemberCommand, AgentCommand, AgentInboxCommand, AgentLifecycleCommand,
        AgentMemberCommand, AgentMessageCommand, AgentSpaceCommand, AgentTaskCommand,
        AgentThreadCommand,
    },
    local_protocol::{AgentAction, LocalRequest, LocalResponse},
};

#[derive(Debug, thiserror::Error)]
#[error("{message}")]
struct AgentCliExit {
    code: u8,
    error_code: String,
    message: String,
    retryable: bool,
}

pub fn exit_code(error: &anyhow::Error) -> u8 {
    error
        .downcast_ref::<AgentCliExit>()
        .map_or(2, |error| error.code)
}

fn classified(
    code: u8,
    error_code: impl Into<String>,
    message: impl Into<String>,
    retryable: bool,
) -> anyhow::Error {
    AgentCliExit {
        code,
        error_code: error_code.into(),
        message: message.into(),
        retryable,
    }
    .into()
}

pub async fn run(args: AgentArgs) -> Result<()> {
    let requested_json = uses_json_output(&args);
    let result = execute(args).await;
    let (response, json) = match result {
        Ok(result) => result,
        Err(error) => {
            if requested_json {
                let local = error.downcast_ref::<AgentCliExit>();
                let response = LocalResponse::failure(
                    local.map_or("invalid_arguments", |error| error.error_code.as_str()),
                    local.map_or_else(|| error.to_string(), |error| error.message.clone()),
                    local.is_some_and(|error| error.retryable),
                );
                print_json(&response)?;
            }
            return Err(error);
        }
    };
    if json {
        print_json(&response)?;
    } else if let Some(data) = &response.data {
        println!(
            "{}",
            serde_json::to_string_pretty(data).map_err(|error| {
                classified(
                    7,
                    "response_encode_failed",
                    format!("failed to encode CLI response: {error}"),
                    false,
                )
            })?
        );
    }
    if response.ok {
        Ok(())
    } else {
        let error = response
            .error
            .as_ref()
            .map(|error| error.message.as_str())
            .unwrap_or("Agent action failed");
        let code = response.error.as_ref().map(response_exit_code).unwrap_or(7);
        let error_code = response
            .error
            .as_ref()
            .map(|error| error.code.as_str())
            .unwrap_or("internal_error");
        let retryable = response.error.as_ref().is_some_and(|error| error.retryable);
        Err(classified(code, error_code, error, retryable))
    }
}

async fn execute(args: AgentArgs) -> Result<(LocalResponse, bool)> {
    let (action, json) = match args.command {
        AgentCommand::Whoami(output) => (None, output.json),
        AgentCommand::Member(args) => match args.command {
            AgentMemberCommand::List(args) => (
                Some(AgentAction::MemberList { query: args.query }),
                args.output.json,
            ),
        },
        AgentCommand::Inbox(args) => match args.command {
            AgentInboxCommand::Current(output) => (Some(AgentAction::InboxCurrent), output.json),
            AgentInboxCommand::Show(args) => (
                Some(AgentAction::InboxShow {
                    inbox_item_id: args.inbox_id,
                }),
                args.output.json,
            ),
            AgentInboxCommand::Ack(args) => {
                ensure!(!args.reason.trim().is_empty(), "--reason must not be empty");
                (
                    Some(AgentAction::InboxAck {
                        inbox_item_ids: args.inbox_ids,
                        reason: args.reason,
                        idempotency_key: Uuid::now_v7(),
                    }),
                    args.output.json,
                )
            }
            AgentInboxCommand::Defer(args) => {
                let until = time::OffsetDateTime::parse(
                    &args.until,
                    &time::format_description::well_known::Rfc3339,
                )
                .context("--until must be an RFC3339 timestamp")?;
                ensure!(
                    until > time::OffsetDateTime::now_utc(),
                    "--until must be in the future"
                );
                (
                    Some(AgentAction::InboxDefer {
                        inbox_item_ids: args.inbox_ids,
                        until,
                        idempotency_key: Uuid::now_v7(),
                    }),
                    args.output.json,
                )
            }
        },
        AgentCommand::Task(args) => match args.command {
            AgentTaskCommand::List(args) => (
                Some(AgentAction::TaskList {
                    status: args.status,
                }),
                args.output.json,
            ),
            AgentTaskCommand::Convert(args) => (
                Some(AgentAction::TaskConvert {
                    message_id: args.message_id,
                    title: args.title,
                    assigned_agent_id: args.assigned_agent_id,
                    idempotency_key: Uuid::now_v7(),
                }),
                args.output.json,
            ),
            AgentTaskCommand::Create(args) => (
                Some(AgentAction::TaskCreate {
                    address: args.address,
                    title: args.title,
                    body: args.body,
                    assigned_agent_id: args.assigned_agent_id,
                    idempotency_key: Uuid::now_v7(),
                }),
                args.output.json,
            ),
            AgentTaskCommand::Claim(args) => (
                Some(AgentAction::TaskClaim {
                    task_id: args.task_id,
                    idempotency_key: Uuid::now_v7(),
                }),
                args.output.json,
            ),
            AgentTaskCommand::Assign(args) => (
                Some(AgentAction::TaskAssign {
                    task_id: args.task_id,
                    agent_member_id: args.agent_member_id,
                    idempotency_key: Uuid::now_v7(),
                }),
                args.output.json,
            ),
            AgentTaskCommand::Status(args) => (
                Some(AgentAction::TaskStatus {
                    task_id: args.task_id,
                    status: args.status,
                    idempotency_key: Uuid::now_v7(),
                }),
                args.output.json,
            ),
        },
        AgentCommand::Channel(args) => match args.command {
            AgentChannelCommand::List(output) => (Some(AgentAction::ChannelList), output.json),
            AgentChannelCommand::Read(args) => {
                ensure!((1..=100).contains(&args.limit), "--limit must be 1 to 100");
                ensure!(
                    args.before.is_none_or(|value| value > 0),
                    "--before must be positive"
                );
                ensure!(
                    args.after.is_none_or(|value| value >= 0),
                    "--after must not be negative"
                );
                (
                    Some(AgentAction::ChannelRead {
                        address: args.address,
                        before: args.before,
                        after: args.after,
                        around: args.around,
                        limit: args.limit,
                    }),
                    args.output.json,
                )
            }
            AgentChannelCommand::Create(args) => (
                Some(AgentAction::ChannelCreate {
                    slug: args.slug,
                    name: args.name,
                    private: args.private,
                    idempotency_key: Uuid::now_v7(),
                }),
                args.output.json,
            ),
            AgentChannelCommand::Member(args) => match args.command {
                AgentChannelMemberCommand::Add(args) => (
                    Some(AgentAction::ChannelMemberAdd {
                        address: args.address,
                        member_id: args.member_id,
                        idempotency_key: Uuid::now_v7(),
                    }),
                    args.output.json,
                ),
                AgentChannelMemberCommand::Remove(args) => (
                    Some(AgentAction::ChannelMemberRemove {
                        address: args.address,
                        member_id: args.member_id,
                        idempotency_key: Uuid::now_v7(),
                    }),
                    args.output.json,
                ),
            },
            AgentChannelCommand::Archive(args) => (
                Some(AgentAction::ChannelArchive {
                    address: args.address,
                    idempotency_key: Uuid::now_v7(),
                }),
                args.output.json,
            ),
        },
        AgentCommand::Thread(args) => match args.command {
            AgentThreadCommand::Read(args) => {
                ensure!((1..=100).contains(&args.limit), "--limit must be 1 to 100");
                ensure!(
                    (0..=100).contains(&args.include_channel),
                    "--include-channel must be 0 to 100"
                );
                (
                    Some(AgentAction::ThreadRead {
                        address: args.address,
                        after: args.after,
                        limit: args.limit,
                        include_channel: args.include_channel,
                    }),
                    args.output.json,
                )
            }
        },
        AgentCommand::Message(args) => match args.command {
            AgentMessageCommand::Send(args) => {
                let body_markdown = if args.stdin {
                    let mut body = String::new();
                    tokio::io::stdin()
                        .take(20_001)
                        .read_to_string(&mut body)
                        .await?;
                    body
                } else {
                    args.body
                        .context("message send requires --body or --stdin")?
                };
                ensure!(
                    (1..=20_000).contains(&body_markdown.trim().chars().count()),
                    "Message must contain 1 to 20000 characters"
                );
                (
                    Some(AgentAction::MessageSend {
                        address: args.address,
                        mention_handles: mention_handles(&body_markdown),
                        body_markdown,
                        based_on: args.based_on,
                        handle_inbox_item_id: args.handle,
                        attachment_ids: args.attachment_ids,
                        idempotency_key: Uuid::now_v7(),
                    }),
                    args.output.json,
                )
            }
        },
        AgentCommand::Attachment(args) => match args.command {
            AgentAttachmentCommand::Upload(args) => {
                let path = tokio::fs::canonicalize(&args.path).await.with_context(|| {
                    format!("Attachment source {} was not found", args.path.display())
                })?;
                (
                    Some(AgentAction::AttachmentUpload {
                        path: path.to_string_lossy().into_owned(),
                        media_type: args.media_type,
                        idempotency_key: Uuid::now_v7(),
                    }),
                    args.output.json,
                )
            }
            AgentAttachmentCommand::Download(args) => {
                let output = if args.output.is_absolute() {
                    args.output
                } else {
                    std::env::current_dir()?.join(args.output)
                };
                (
                    Some(AgentAction::AttachmentDownload {
                        attachment_id: args.attachment_id,
                        output_path: output.to_string_lossy().into_owned(),
                    }),
                    args.json,
                )
            }
            AgentAttachmentCommand::Info(args) => (
                Some(AgentAction::AttachmentInfo {
                    attachment_id: args.attachment_id,
                }),
                args.output.json,
            ),
        },
        AgentCommand::Create(args) => {
            let role_text = tokio::fs::read_to_string(&args.role_file)
                .await
                .with_context(|| {
                    format!("Role file {} could not be read", args.role_file.display())
                })?;
            ensure!(
                (1..=12_000).contains(&role_text.trim().chars().count()),
                "Agent Role must contain 1 to 12000 characters"
            );
            ensure!(
                (1..=40).contains(&args.name.trim().chars().count()),
                "Agent name must contain 1 to 40 characters"
            );
            (
                Some(AgentAction::AgentCreate {
                    name: args.name,
                    role_text,
                    computer_id: args.computer,
                    driver_kind: args.driver,
                    idempotency_key: Uuid::now_v7(),
                }),
                args.output.json,
            )
        }
        AgentCommand::Space(args) => match args.command {
            AgentSpaceCommand::Update(args) => {
                ensure!(
                    args.name.is_some() || args.accent.is_some(),
                    "space update requires --name or --accent"
                );
                (
                    Some(AgentAction::SpaceUpdate {
                        name: args.name,
                        accent: args.accent,
                        idempotency_key: Uuid::now_v7(),
                    }),
                    args.output.json,
                )
            }
        },
        AgentCommand::Lifecycle(args) => match args.command {
            AgentLifecycleCommand::Suspend(args) => (
                Some(AgentAction::AgentSuspend {
                    agent_member_id: args.agent_member_id,
                    cancel_now: args.cancel_now,
                    idempotency_key: Uuid::now_v7(),
                }),
                args.output.json,
            ),
            AgentLifecycleCommand::Resume(args) => (
                Some(AgentAction::AgentResume {
                    agent_member_id: args.agent_member_id,
                    idempotency_key: Uuid::now_v7(),
                }),
                args.output.json,
            ),
        },
        AgentCommand::Audit(args) => match args.command {
            AgentAuditCommand::List(args) => {
                ensure!((1..=100).contains(&args.limit), "--limit must be 1 to 100");
                (
                    Some(AgentAction::AuditList {
                        before: args.before,
                        limit: args.limit,
                    }),
                    args.output.json,
                )
            }
        },
    };
    let run_token = std::env::var("SUMI_RUN_TOKEN").map_err(|_| {
        classified(
            3,
            "permission_denied",
            "sumi agent commands require an active Agent run",
            false,
        )
    })?;
    let request = match action {
        Some(action) => LocalRequest::AgentAction { run_token, action },
        None => LocalRequest::Whoami { run_token },
    };
    Ok((call_daemon(request).await?, json))
}

fn mention_handles(body: &str) -> Vec<String> {
    let mut handles = body
        .split_whitespace()
        .filter_map(|word| word.strip_prefix('@'))
        .filter_map(|word| {
            let handle = word
                .chars()
                .take_while(|character| character.is_ascii_alphanumeric() || *character == '-')
                .collect::<String>()
                .to_ascii_lowercase();
            (!handle.is_empty()
                && handle.len() <= 32
                && !handle.starts_with('-')
                && !handle.ends_with('-')
                && !handle.contains("--"))
            .then_some(handle)
        })
        .collect::<Vec<_>>();
    handles.sort();
    handles.dedup();
    handles
}

fn uses_json_output(args: &AgentArgs) -> bool {
    match &args.command {
        AgentCommand::Whoami(output) => output.json,
        AgentCommand::Member(args) => match &args.command {
            AgentMemberCommand::List(args) => args.output.json,
        },
        AgentCommand::Inbox(args) => match &args.command {
            AgentInboxCommand::Current(output) => output.json,
            AgentInboxCommand::Show(args) => args.output.json,
            AgentInboxCommand::Ack(args) => args.output.json,
            AgentInboxCommand::Defer(args) => args.output.json,
        },
        AgentCommand::Task(args) => match &args.command {
            AgentTaskCommand::List(args) => args.output.json,
            AgentTaskCommand::Convert(args) => args.output.json,
            AgentTaskCommand::Create(args) => args.output.json,
            AgentTaskCommand::Claim(args) => args.output.json,
            AgentTaskCommand::Assign(args) => args.output.json,
            AgentTaskCommand::Status(args) => args.output.json,
        },
        AgentCommand::Channel(args) => match &args.command {
            AgentChannelCommand::List(output) => output.json,
            AgentChannelCommand::Read(args) => args.output.json,
            AgentChannelCommand::Create(args) => args.output.json,
            AgentChannelCommand::Member(args) => match &args.command {
                AgentChannelMemberCommand::Add(args) | AgentChannelMemberCommand::Remove(args) => {
                    args.output.json
                }
            },
            AgentChannelCommand::Archive(args) => args.output.json,
        },
        AgentCommand::Thread(args) => match &args.command {
            AgentThreadCommand::Read(args) => args.output.json,
        },
        AgentCommand::Message(args) => match &args.command {
            AgentMessageCommand::Send(args) => args.output.json,
        },
        AgentCommand::Attachment(args) => match &args.command {
            AgentAttachmentCommand::Upload(args) => args.output.json,
            AgentAttachmentCommand::Download(args) => args.json,
            AgentAttachmentCommand::Info(args) => args.output.json,
        },
        AgentCommand::Create(args) => args.output.json,
        AgentCommand::Space(args) => match &args.command {
            AgentSpaceCommand::Update(args) => args.output.json,
        },
        AgentCommand::Lifecycle(args) => match &args.command {
            AgentLifecycleCommand::Suspend(args) => args.output.json,
            AgentLifecycleCommand::Resume(args) => args.output.json,
        },
        AgentCommand::Audit(args) => match &args.command {
            AgentAuditCommand::List(args) => args.output.json,
        },
    }
}

fn print_json(response: &LocalResponse) -> Result<()> {
    println!(
        "{}",
        serde_json::to_string(response).map_err(|error| {
            classified(
                7,
                "response_encode_failed",
                format!("failed to encode CLI response: {error}"),
                false,
            )
        })?
    );
    Ok(())
}

fn response_exit_code(error: &crate::local_protocol::LocalError) -> u8 {
    if error.retryable {
        return 6;
    }
    match error.code.as_str() {
        "permission_denied" | "unauthorized" => 3,
        "not_found" => 4,
        "context_changed"
        | "conflict"
        | "inbox_lease_lost"
        | "channel_archived"
        | "attachment_output_exists" => 5,
        code if code.ends_with("_not_found") => 4,
        code if code.starts_with("invalid_") => 2,
        _ => 7,
    }
}

async fn call_daemon(request: LocalRequest) -> Result<LocalResponse> {
    let socket = std::env::var_os("SUMI_SOCKET").ok_or_else(|| {
        classified(
            3,
            "permission_denied",
            "sumi agent commands require an active Agent run",
            false,
        )
    })?;
    let mut stream = tokio::net::UnixStream::connect(socket)
        .await
        .map_err(|error| {
            classified(
                6,
                "daemon_unavailable",
                format!("daemon IPC endpoint is unavailable: {error}"),
                true,
            )
        })?;
    let request = serde_json::to_vec(&request).map_err(|error| {
        classified(
            7,
            "request_encode_failed",
            format!("failed to encode daemon request: {error}"),
            false,
        )
    })?;
    stream.write_all(&request).await.map_err(|error| {
        classified(
            6,
            "daemon_unavailable",
            format!("failed to write daemon request: {error}"),
            true,
        )
    })?;
    stream.write_all(b"\n").await.map_err(|error| {
        classified(
            6,
            "daemon_unavailable",
            format!("failed to write daemon request: {error}"),
            true,
        )
    })?;
    let mut response = String::new();
    BufReader::new(stream)
        .read_line(&mut response)
        .await
        .map_err(|error| {
            classified(
                6,
                "daemon_unavailable",
                format!("failed to read daemon response: {error}"),
                true,
            )
        })?;
    serde_json::from_str(&response).map_err(|error| {
        classified(
            7,
            "invalid_daemon_response",
            format!("daemon returned an invalid response: {error}"),
            false,
        )
    })
}

#[cfg(test)]
mod tests {
    use super::mention_handles;

    #[test]
    fn message_mentions_are_parsed_normalized_and_deduplicated() {
        assert_eq!(
            mention_handles(
                "@Lin review this with @ada-lovelace, not email@example.com or @bad--handle"
            ),
            vec!["ada-lovelace", "lin"]
        );
    }
}
