use anyhow::{Context, Result, bail, ensure};
use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};
use uuid::Uuid;

use crate::{
    cli::{
        AgentArgs, AgentAttachmentCommand, AgentChannelCommand, AgentCommand, AgentInboxCommand,
        AgentMemberCommand, AgentMessageCommand, AgentThreadCommand,
    },
    local_protocol::{AgentAction, LocalRequest, LocalResponse},
};

pub async fn run(args: AgentArgs) -> Result<()> {
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
        AgentCommand::Channel(args) => match args.command {
            AgentChannelCommand::List(output) => (Some(AgentAction::ChannelList), output.json),
            AgentChannelCommand::Read(args) => {
                ensure!((1..=100).contains(&args.limit), "--limit must be 1 to 100");
                (
                    Some(AgentAction::ChannelRead {
                        address: args.address,
                        before: args.before,
                        limit: args.limit,
                    }),
                    args.output.json,
                )
            }
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
    };
    let run_token = std::env::var("SUMI_RUN_TOKEN")
        .context("sumi agent commands require an active Agent run")?;
    let request = match action {
        Some(action) => LocalRequest::AgentAction { run_token, action },
        None => LocalRequest::Whoami { run_token },
    };
    let response = call_daemon(request).await?;
    if json {
        println!("{}", serde_json::to_string(&response)?);
    } else if let Some(data) = &response.data {
        println!("{}", serde_json::to_string_pretty(data)?);
    }
    if response.ok {
        Ok(())
    } else {
        let error = response
            .error
            .as_ref()
            .map(|error| error.message.as_str())
            .unwrap_or("Agent action failed");
        bail!(error.to_owned())
    }
}

async fn call_daemon(request: LocalRequest) -> Result<LocalResponse> {
    let socket = std::env::var_os("SUMI_SOCKET")
        .context("sumi agent commands require an active Agent run")?;
    let mut stream = tokio::net::UnixStream::connect(socket)
        .await
        .context("the configured daemon IPC endpoint is unavailable")?;
    stream.write_all(&serde_json::to_vec(&request)?).await?;
    stream.write_all(b"\n").await?;
    let mut response = String::new();
    BufReader::new(stream).read_line(&mut response).await?;
    serde_json::from_str(&response).context("daemon returned an invalid local response")
}
