use clap::{Args, Parser, Subcommand};

use crate::{
    ids::IdempotencyKey,
    protocol::capability::{
        Action, CloseReason, DriverKind, MessageSend, MessageTarget, Page, PostTarget,
    },
};

#[derive(Debug, Parser)]
#[command(name = "sumi agent")]
pub(crate) struct AgentCli {
    #[arg(long, global = true)]
    pub(crate) json: bool,
    #[command(subcommand)]
    pub(crate) command: Command,
}

#[derive(Debug, Subcommand)]
pub(crate) enum Command {
    Context(ContextArgs),
    Message(MessageArgs),
    Task(TaskArgs),
    Run(RunArgs),
    Inbox(InboxArgs),
    Channel(ChannelArgs),
    Agent(AgentArgs),
    Attachment(AttachmentArgs),
    Memory(MemoryArgs),
    Thread(ThreadArgs),
}

#[derive(Debug, Args)]
pub(crate) struct ContextArgs {
    #[command(subcommand)]
    command: ContextCommand,
}
#[derive(Debug, Subcommand)]
enum ContextCommand {
    Current,
}

#[derive(Debug, Args)]
pub(crate) struct MessageArgs {
    #[command(subcommand)]
    command: MessageCommand,
}
#[derive(Debug, Subcommand)]
enum MessageCommand {
    Send(MessageSendArgs),
    Read(PageArgs),
}

#[derive(Debug, Args)]
struct MessageSendArgs {
    #[arg(long, conflicts_with = "stdin")]
    body: Option<String>,
    #[arg(long, conflicts_with = "body")]
    stdin: bool,
    #[arg(long, conflicts_with = "channel")]
    thread: Option<crate::ids::ThreadId>,
    #[arg(long, conflicts_with = "thread")]
    channel: Option<crate::ids::ChannelId>,
    #[arg(long)]
    handle: Option<crate::ids::InboxItemId>,
}

#[derive(Debug, Args)]
struct PageArgs {
    #[arg(long, conflicts_with = "after")]
    before: Option<u64>,
    #[arg(long, conflicts_with = "before")]
    after: Option<u64>,
    #[arg(long, default_value_t = 50, value_parser = 1..=100)]
    limit: u16,
}

#[derive(Debug, Args)]
pub(crate) struct TaskArgs {
    #[command(subcommand)]
    command: TaskCommand,
}
#[derive(Debug, Subcommand)]
enum TaskCommand {
    Create {
        #[arg(long)]
        title: Option<String>,
        #[arg(long)]
        assign: Option<crate::ids::MemberId>,
    },
    LinkThread {
        thread_id: crate::ids::ThreadId,
    },
    UnlinkThread {
        thread_id: crate::ids::ThreadId,
    },
    Update {
        #[arg(long)]
        title: String,
    },
    SubmitReview {
        #[arg(long)]
        body_file: std::path::PathBuf,
        #[arg(long, default_value = "focus")]
        post_to: String,
    },
    Done {
        #[arg(long)]
        result_file: std::path::PathBuf,
        #[arg(long, default_value = "focus")]
        post_to: String,
    },
    Close {
        #[arg(long)]
        reason: String,
        #[arg(long)]
        note: Option<String>,
    },
}

#[derive(Debug, Args)]
pub(crate) struct RunArgs {
    #[command(subcommand)]
    command: RunCommand,
}
#[derive(Debug, Subcommand)]
enum RunCommand {
    Yield {
        #[arg(long)]
        note: Option<String>,
    },
}

#[derive(Debug, Args)]
pub(crate) struct InboxArgs {
    #[command(subcommand)]
    command: InboxCommand,
}
#[derive(Debug, Subcommand)]
enum InboxCommand {
    Current,
    Ack {
        item_id: crate::ids::InboxItemId,
        #[arg(long)]
        reason: Option<String>,
    },
    Defer {
        item_id: crate::ids::InboxItemId,
        #[arg(long)]
        until: String,
    },
}

#[derive(Debug, Args)]
pub(crate) struct ChannelArgs {
    #[command(subcommand)]
    command: ChannelCommand,
}
#[derive(Debug, Subcommand)]
enum ChannelCommand {
    Create {
        name: String,
        #[arg(long)]
        private: bool,
    },
    Read {
        channel_id: crate::ids::ChannelId,
        #[arg(long)]
        around: Option<crate::ids::MessageId>,
        #[arg(long, default_value_t = 50)]
        limit: u16,
    },
}

#[derive(Debug, Args)]
pub(crate) struct AgentArgs {
    #[command(subcommand)]
    command: AgentCommand,
}
#[derive(Debug, Subcommand)]
enum AgentCommand {
    Create {
        name: String,
        #[arg(long)]
        role_file: std::path::PathBuf,
        #[arg(long)]
        driver: String,
    },
}

#[derive(Debug, Args)]
pub(crate) struct AttachmentArgs {
    #[command(subcommand)]
    command: AttachmentCommand,
}
#[derive(Debug, Subcommand)]
enum AttachmentCommand {
    Upload {
        path: std::path::PathBuf,
    },
    Download {
        attachment_id: crate::ids::AttachmentId,
        #[arg(long)]
        output: std::path::PathBuf,
    },
}

#[derive(Debug, Args)]
pub(crate) struct MemoryArgs {
    #[command(subcommand)]
    command: MemoryCommand,
}
#[derive(Debug, Subcommand)]
enum MemoryCommand {
    Read {
        path: String,
    },
    Write {
        path: String,
        #[arg(long)]
        stdin: bool,
    },
}

#[derive(Debug, Args)]
pub(crate) struct ThreadArgs {
    #[command(subcommand)]
    command: ThreadCommand,
}
#[derive(Debug, Subcommand)]
enum ThreadCommand {
    Read {
        thread_id: crate::ids::ThreadId,
        #[arg(long)]
        after: Option<u64>,
        #[arg(long, default_value_t = 50)]
        limit: u16,
    },
}

impl AgentCli {
    pub(crate) async fn action(
        self,
        stdin: Option<String>,
    ) -> Result<(Action, Option<IdempotencyKey>), &'static str> {
        if !self.json {
            return Err("--json is required for Agent automation");
        }
        let (action, writes) = match self.command {
            Command::Context(ContextArgs {
                command: ContextCommand::Current,
            }) => (Action::ContextCurrent, false),
            Command::Message(MessageArgs {
                command: MessageCommand::Read(page),
            }) => (Action::MessageRead(page.into()), false),
            Command::Message(MessageArgs {
                command: MessageCommand::Send(args),
            }) => {
                let body = if args.stdin {
                    stdin.ok_or("stdin is required")?
                } else {
                    args.body.ok_or("--body or --stdin is required")?
                };
                if body.trim().is_empty() || body.chars().count() > 20_000 {
                    return Err("Message must contain 1 to 20000 characters");
                }
                let target = args
                    .thread
                    .map(MessageTarget::Thread)
                    .or_else(|| args.channel.map(MessageTarget::Channel))
                    .unwrap_or(MessageTarget::Focus);
                (
                    Action::MessageSend(MessageSend {
                        target,
                        body,
                        handle_item_id: args.handle,
                        snapshot_sequence: None,
                    }),
                    true,
                )
            }
            Command::Task(TaskArgs {
                command: TaskCommand::Create { title, assign },
            }) => (
                Action::TaskCreate {
                    title,
                    assignee: assign,
                },
                true,
            ),
            Command::Task(TaskArgs {
                command: TaskCommand::LinkThread { thread_id },
            }) => (Action::TaskLinkThread { thread_id }, true),
            Command::Task(TaskArgs {
                command: TaskCommand::UnlinkThread { thread_id },
            }) => (Action::TaskUnlinkThread { thread_id }, true),
            Command::Task(TaskArgs {
                command: TaskCommand::Update { title },
            }) => (Action::TaskUpdate { title }, true),
            Command::Task(TaskArgs {
                command: TaskCommand::SubmitReview { body_file, post_to },
            }) => {
                let body = tokio::fs::read_to_string(body_file)
                    .await
                    .map_err(|_| "--body-file could not be read")?;
                (
                    Action::TaskSubmitReview {
                        body,
                        post_to: parse_post_target(&post_to)?,
                    },
                    true,
                )
            }
            Command::Task(TaskArgs {
                command:
                    TaskCommand::Done {
                        result_file,
                        post_to,
                    },
            }) => {
                let result = tokio::fs::read_to_string(result_file)
                    .await
                    .map_err(|_| "--result-file could not be read")?;
                (
                    Action::TaskDone {
                        result,
                        post_to: parse_post_target(&post_to)?,
                    },
                    true,
                )
            }
            Command::Task(TaskArgs {
                command: TaskCommand::Close { reason, note },
            }) => (
                Action::TaskClose {
                    reason: parse_close_reason(&reason)?,
                    note,
                },
                true,
            ),
            Command::Run(RunArgs {
                command: RunCommand::Yield { note },
            }) => (Action::RunYield { note }, true),
            Command::Inbox(InboxArgs {
                command: InboxCommand::Current,
            }) => (Action::InboxCurrent, false),
            Command::Inbox(InboxArgs {
                command: InboxCommand::Ack { item_id, reason },
            }) => (Action::InboxAck { item_id, reason }, true),
            Command::Inbox(InboxArgs {
                command: InboxCommand::Defer { item_id, until },
            }) => {
                let until = time::OffsetDateTime::parse(
                    &until,
                    &time::format_description::well_known::Rfc3339,
                )
                .map_err(|_| "--until must be an RFC3339 timestamp")?;
                (Action::InboxDefer { item_id, until }, true)
            }
            Command::Channel(ChannelArgs {
                command: ChannelCommand::Create { name, private },
            }) => (Action::ChannelCreate { name, private }, true),
            Command::Channel(ChannelArgs {
                command:
                    ChannelCommand::Read {
                        channel_id,
                        around,
                        limit,
                    },
            }) => (
                Action::ChannelRead {
                    channel_id,
                    around_message_id: around,
                    limit,
                },
                false,
            ),
            Command::Agent(AgentArgs {
                command:
                    AgentCommand::Create {
                        name,
                        role_file,
                        driver,
                    },
            }) => {
                let role = tokio::fs::read_to_string(role_file)
                    .await
                    .map_err(|_| "--role-file could not be read")?;
                let driver = match driver.as_str() {
                    "codex" => DriverKind::Codex,
                    "builtin" => DriverKind::Builtin,
                    _ => return Err("--driver must be codex or builtin"),
                };
                (Action::AgentCreate { name, role, driver }, true)
            }
            Command::Attachment(AttachmentArgs {
                command: AttachmentCommand::Upload { path },
            }) => (
                Action::AttachmentUpload {
                    path: path.to_string_lossy().into_owned(),
                },
                true,
            ),
            Command::Attachment(AttachmentArgs {
                command:
                    AttachmentCommand::Download {
                        attachment_id,
                        output,
                    },
            }) => (
                Action::AttachmentDownload {
                    attachment_id,
                    output: output.to_string_lossy().into_owned(),
                },
                false,
            ),
            Command::Memory(MemoryArgs {
                command: MemoryCommand::Read { path },
            }) => (Action::MemoryRead { path }, false),
            Command::Memory(MemoryArgs {
                command: MemoryCommand::Write { path, stdin: true },
            }) => (
                Action::MemoryWrite {
                    path,
                    content: stdin.ok_or("stdin is required")?,
                },
                true,
            ),
            Command::Memory(MemoryArgs {
                command: MemoryCommand::Write { stdin: false, .. },
            }) => return Err("memory write requires --stdin"),
            Command::Thread(ThreadArgs {
                command:
                    ThreadCommand::Read {
                        thread_id,
                        after,
                        limit,
                    },
            }) => (
                Action::ThreadRead {
                    thread_id,
                    page: Page {
                        before: None,
                        after,
                        limit,
                    },
                },
                false,
            ),
        };
        Ok((
            action,
            writes.then(|| IdempotencyKey::from_uuid(uuid::Uuid::now_v7())),
        ))
    }
}

fn parse_post_target(value: &str) -> Result<PostTarget, &'static str> {
    match value {
        "focus" => Ok(PostTarget::Focus),
        "source" => Ok(PostTarget::Source),
        _ => Err("--post-to must be focus or source"),
    }
}

fn parse_close_reason(value: &str) -> Result<CloseReason, &'static str> {
    match value {
        "invalid" => Ok(CloseReason::Invalid),
        "duplicate" => Ok(CloseReason::Duplicate),
        "not_needed" => Ok(CloseReason::NotNeeded),
        "obsolete" => Ok(CloseReason::Obsolete),
        "other" => Ok(CloseReason::Other),
        _ => Err("invalid close reason"),
    }
}

impl From<PageArgs> for Page {
    fn from(value: PageArgs) -> Self {
        Self {
            before: value.before,
            after: value.after,
            limit: value.limit,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn task_create_has_no_source_or_context_parameters() {
        AgentCli::try_parse_from([
            "sumi-agent",
            "task",
            "create",
            "--title",
            "design",
            "--json",
        ])
        .unwrap();
        assert!(
            AgentCli::try_parse_from(["sumi-agent", "task", "create", "--task", "bad", "--json"])
                .is_err()
        );
    }

    #[tokio::test]
    async fn json_validation_error_is_one_envelope() {
        let cli = AgentCli::try_parse_from(["sumi-agent", "context", "current"]).unwrap();
        let mut output = Vec::new();
        let exit = crate::agent_cli::execute(cli, None, &mut output).await;
        assert_ne!(exit, 0);
        assert_eq!(output.iter().filter(|byte| **byte == b'\n').count(), 1);
        let value: serde_json::Value = serde_json::from_slice(&output).unwrap();
        assert_eq!(value["ok"], false);
        assert_eq!(value["error"]["code"], "invalid_argument");
    }
}
