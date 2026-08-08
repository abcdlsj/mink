use clap::{Args, Subcommand};

use crate::{
    ids::{
        AttachmentId, ChannelId, ComputerId, IdempotencyKey, InboxItemId, MemberId, MessageId,
        ThreadId,
    },
    protocol::capability::{
        Action, CloseReason, DriverKind, MessageSend, MessageTarget, Page, PostTarget,
        TaskReference,
    },
};

#[derive(Debug, Args)]
pub(crate) struct AgentCli {
    #[arg(long, global = true)]
    pub(crate) json: bool,
    #[command(subcommand)]
    pub(crate) command: Command,
}

#[derive(Debug, Subcommand)]
pub(crate) enum Command {
    Discover { operation: String },
    Context(ContextArgs),
    Space(SpaceArgs),
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
pub(crate) struct SpaceArgs {
    #[command(subcommand)]
    command: SpaceCommand,
}

#[derive(Debug, Subcommand)]
enum SpaceCommand {
    Members,
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
    thread: Option<ThreadId>,
    #[arg(long, conflicts_with = "thread")]
    channel: Option<ChannelId>,
    #[arg(long, conflicts_with_all = ["thread", "channel"])]
    to: Option<MemberId>,
    #[arg(long)]
    attachment: Vec<AttachmentId>,
    #[arg(long)]
    handle: Option<InboxItemId>,
}

#[derive(Debug, Args)]
struct PageArgs {
    #[arg(long, conflicts_with = "after")]
    before: Option<u64>,
    #[arg(long, conflicts_with = "before")]
    after: Option<u64>,
    #[arg(
        long,
        default_value_t = 50,
        value_parser = clap::value_parser!(u16).range(1..=100)
    )]
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
        assign: Option<MemberId>,
    },
    Open,
    Claim {
        reference: String,
    },
    LinkThread {
        thread_id: ThreadId,
    },
    UnlinkThread {
        thread_id: ThreadId,
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
        item_id: InboxItemId,
        #[arg(long)]
        reason: Option<String>,
    },
    Defer {
        item_id: InboxItemId,
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
    /// Create a Channel with an explicit #slug and optional human-readable topic.
    Create {
        #[arg(
            value_name = "SLUG",
            help = "Channel #slug: 1-32 lowercase ASCII letters or numbers separated by single hyphens"
        )]
        slug: String,
        #[arg(
            long,
            value_name = "TEXT",
            help = "Optional human-readable Channel description; Unicode is allowed"
        )]
        topic: Option<String>,
        #[arg(long)]
        private: bool,
    },
    Read {
        channel_id: ChannelId,
        #[arg(long)]
        around: Option<MessageId>,
        #[arg(long, default_value_t = 50)]
        limit: u16,
    },
    Members {
        channel_id: ChannelId,
    },
    Leave {
        channel_id: ChannelId,
    },
    Invite {
        channel_id: ChannelId,
        member_id: MemberId,
    },
    Remove {
        channel_id: ChannelId,
        member_id: MemberId,
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
        #[arg(long)]
        computer_id: ComputerId,
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
        attachment_id: AttachmentId,
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
        thread_id: ThreadId,
        #[arg(long)]
        after: Option<u64>,
        #[arg(long, default_value_t = 50)]
        limit: u16,
    },
}

impl AgentCli {
    pub(crate) fn requires_stdin(&self) -> bool {
        matches!(
            &self.command,
            Command::Message(MessageArgs {
                command: MessageCommand::Send(MessageSendArgs { stdin: true, .. }),
            }) | Command::Memory(MemoryArgs {
                command: MemoryCommand::Write { stdin: true, .. },
            })
        )
    }

    pub(crate) fn is_agent_create(&self) -> bool {
        matches!(
            &self.command,
            Command::Agent(AgentArgs {
                command: AgentCommand::Create { .. }
            })
        )
    }

    pub(crate) async fn action(
        self,
        stdin: Option<String>,
    ) -> Result<(Action, Option<IdempotencyKey>), &'static str> {
        if !self.json {
            return Err("--json is required for Agent automation");
        }
        let (action, writes) = match self.command {
            Command::Discover { operation } => {
                if operation.trim().is_empty() || operation.chars().count() > 100 {
                    return Err("operation must contain 1 to 100 characters");
                }
                (Action::Discover { operation }, false)
            }
            Command::Context(ContextArgs {
                command: ContextCommand::Current,
            }) => (Action::ContextCurrent, false),
            Command::Space(SpaceArgs {
                command: SpaceCommand::Members,
            }) => (Action::SpaceMembers, false),
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
                    .or_else(|| args.to.map(MessageTarget::Member))
                    .unwrap_or(MessageTarget::Focus);
                (
                    Action::MessageSend(MessageSend {
                        target,
                        body,
                        attachment_ids: args.attachment,
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
                command: TaskCommand::Open,
            }) => (Action::TaskOpen, false),
            Command::Task(TaskArgs {
                command: TaskCommand::Claim { reference },
            }) => (
                Action::TaskStart {
                    task: parse_task_reference(&reference)?,
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
                command:
                    ChannelCommand::Create {
                        slug,
                        topic,
                        private,
                    },
            }) => (
                Action::ChannelCreate {
                    slug,
                    topic,
                    private,
                },
                true,
            ),
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
            Command::Channel(ChannelArgs {
                command: ChannelCommand::Members { channel_id },
            }) => (Action::ChannelMembers { channel_id }, false),
            Command::Channel(ChannelArgs {
                command: ChannelCommand::Leave { channel_id },
            }) => (Action::ChannelLeave { channel_id }, true),
            Command::Channel(ChannelArgs {
                command:
                    ChannelCommand::Invite {
                        channel_id,
                        member_id,
                    },
            }) => (
                Action::ChannelInvite {
                    channel_id,
                    member_id,
                },
                true,
            ),
            Command::Channel(ChannelArgs {
                command:
                    ChannelCommand::Remove {
                        channel_id,
                        member_id,
                    },
            }) => (
                Action::ChannelRemove {
                    channel_id,
                    member_id,
                },
                true,
            ),
            Command::Agent(AgentArgs {
                command:
                    AgentCommand::Create {
                        name,
                        role_file,
                        driver,
                        computer_id,
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
                (
                    Action::AgentCreate {
                        name,
                        role,
                        driver,
                        computer_id,
                    },
                    true,
                )
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

fn parse_task_reference(value: &str) -> Result<TaskReference, &'static str> {
    let value = value.strip_prefix('!').unwrap_or(value).trim();
    if value.is_empty() {
        return Err("task reference must be !<seq> or a task UUID");
    }
    if let Ok(seq) = value.parse::<u64>() {
        if seq == 0 {
            return Err("task seq must be positive");
        }
        return Ok(TaskReference::Seq(seq));
    }
    value
        .parse()
        .map(TaskReference::Id)
        .map_err(|_| "task reference must be !<seq> or a task UUID")
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
    use clap::Parser;

    use super::*;

    #[derive(Debug, Parser)]
    struct TestAgentCli {
        #[command(flatten)]
        agent: AgentCli,
    }

    fn parse(args: impl IntoIterator<Item = &'static str>) -> Result<AgentCli, clap::Error> {
        TestAgentCli::try_parse_from(args).map(|parsed| parsed.agent)
    }

    #[test]
    fn task_create_has_no_source_or_context_parameters() {
        parse([
            "sumi-agent",
            "task",
            "create",
            "--title",
            "design",
            "--json",
        ])
        .unwrap();
        assert!(parse(["sumi-agent", "task", "create", "--task", "bad", "--json"]).is_err());
    }

    #[tokio::test]
    async fn task_open_and_claim_map_to_read_and_write_actions() {
        let open = parse(["sumi-agent", "task", "open", "--json"]).unwrap();
        let (action, key) = open.action(None).await.unwrap();
        assert_eq!(action, Action::TaskOpen);
        assert!(key.is_none());

        let claim = parse(["sumi-agent", "task", "claim", "!7", "--json"]).unwrap();
        let (action, key) = claim.action(None).await.unwrap();
        assert_eq!(
            action,
            Action::TaskStart {
                task: TaskReference::Seq(7)
            }
        );
        assert!(key.is_some());

        let task_id = uuid::Uuid::from_u128(99);
        let claim = TestAgentCli::try_parse_from(vec![
            "sumi-agent".to_owned(),
            "task".to_owned(),
            "claim".to_owned(),
            task_id.to_string(),
            "--json".to_owned(),
        ])
        .unwrap()
        .agent;
        let (action, _) = claim.action(None).await.unwrap();
        assert_eq!(
            action,
            Action::TaskStart {
                task: TaskReference::Id(crate::ids::TaskId::from_uuid(task_id))
            }
        );
        assert!(
            parse(["sumi-agent", "task", "claim", "bad", "--json"])
                .unwrap()
                .action(None)
                .await
                .is_err()
        );
    }

    #[test]
    fn message_send_accepts_repeated_attachment_ids() {
        let first = AttachmentId::from_uuid(uuid::Uuid::from_u128(7));
        let second = AttachmentId::from_uuid(uuid::Uuid::from_u128(8));
        let first_str = first.to_string();
        let second_str = second.to_string();
        let cli = TestAgentCli::try_parse_from([
            "sumi-agent",
            "message",
            "send",
            "--body",
            "text",
            "--attachment",
            &first_str,
            "--attachment",
            &second_str,
            "--json",
        ])
        .unwrap()
        .agent;
        let Command::Message(MessageArgs {
            command: MessageCommand::Send(args),
        }) = cli.command
        else {
            panic!("expected message send");
        };
        assert_eq!(args.attachment, vec![first, second]);
    }

    #[tokio::test]
    async fn message_send_accepts_member_target_without_a_dm_subcommand() {
        let target = MemberId::from_uuid(uuid::Uuid::from_u128(9));
        let target_str = target.to_string();
        let cli = TestAgentCli::try_parse_from([
            "sumi-agent",
            "message",
            "send",
            "--to",
            &target_str,
            "--body",
            "private note",
            "--json",
        ])
        .unwrap()
        .agent;
        let (action, _) = cli.action(None).await.unwrap();
        assert_eq!(action.name(), "message.send");
        assert!(matches!(
            action,
            Action::MessageSend(MessageSend {
                target: MessageTarget::Member(id),
                ..
            }) if id == target
        ));
    }

    #[tokio::test]
    async fn channel_leave_uses_the_agent_capability_without_a_dm_command() {
        let channel = ChannelId::from_uuid(uuid::Uuid::from_u128(10));
        let channel_str = channel.to_string();
        let cli = TestAgentCli::try_parse_from([
            "sumi-agent",
            "channel",
            "leave",
            &channel_str,
            "--json",
        ])
        .unwrap()
        .agent;
        let (action, _) = cli.action(None).await.unwrap();
        assert_eq!(action.name(), "channel.leave");
        assert_eq!(
            action,
            Action::ChannelLeave {
                channel_id: channel
            }
        );
    }

    #[tokio::test]
    async fn channel_membership_commands_map_to_permissioned_actions() {
        let channel = ChannelId::from_uuid(uuid::Uuid::from_u128(12));
        let member = MemberId::from_uuid(uuid::Uuid::from_u128(13));
        let channel_str = channel.to_string();
        let member_str = member.to_string();
        let invite = TestAgentCli::try_parse_from([
            "sumi-agent",
            "channel",
            "invite",
            &channel_str,
            &member_str,
            "--json",
        ])
        .unwrap()
        .agent;
        let (invite_action, invite_key) = invite.action(None).await.unwrap();
        assert_eq!(invite_action.name(), "channel.invite");
        assert!(invite_key.is_some());
        assert_eq!(
            invite_action,
            Action::ChannelInvite {
                channel_id: channel,
                member_id: member,
            }
        );

        let remove = TestAgentCli::try_parse_from([
            "sumi-agent",
            "channel",
            "remove",
            &channel_str,
            &member_str,
            "--json",
        ])
        .unwrap()
        .agent;
        let (remove_action, remove_key) = remove.action(None).await.unwrap();
        assert_eq!(remove_action.name(), "channel.remove");
        assert!(remove_key.is_some());
        assert_eq!(
            remove_action,
            Action::ChannelRemove {
                channel_id: channel,
                member_id: member,
            }
        );
    }

    #[tokio::test]
    async fn member_queries_map_to_read_actions_without_idempotency_keys() {
        let channel = ChannelId::from_uuid(uuid::Uuid::from_u128(14));
        let space = TestAgentCli::try_parse_from(["sumi-agent", "space", "members", "--json"])
            .unwrap()
            .agent;
        let (space_action, space_key) = space.action(None).await.unwrap();
        assert_eq!(space_action, Action::SpaceMembers);
        assert!(space_key.is_none());

        let channel_str = channel.to_string();
        let channel = TestAgentCli::try_parse_from([
            "sumi-agent",
            "channel",
            "members",
            &channel_str,
            "--json",
        ])
        .unwrap()
        .agent;
        let (channel_action, channel_key) = channel.action(None).await.unwrap();
        assert_eq!(
            channel_action,
            Action::ChannelMembers {
                channel_id: ChannelId::from_uuid(uuid::Uuid::from_u128(14)),
            }
        );
        assert!(channel_key.is_none());
    }

    #[tokio::test]
    async fn channel_create_keeps_slug_and_unicode_topic_separate() {
        let cli = TestAgentCli::try_parse_from([
            "sumi-agent",
            "channel",
            "create",
            "product-discussion",
            "--topic",
            "产品讨论",
            "--private",
            "--json",
        ])
        .unwrap()
        .agent;
        let (action, _) = cli.action(None).await.unwrap();
        assert_eq!(
            action,
            Action::ChannelCreate {
                slug: "product-discussion".into(),
                topic: Some("产品讨论".into()),
                private: true,
            }
        );
    }

    #[tokio::test]
    async fn discover_maps_to_an_extensible_read_action() {
        let cli =
            TestAgentCli::try_parse_from(["sumi-agent", "discover", "agent.create", "--json"])
                .unwrap()
                .agent;
        let (action, idempotency_key) = cli.action(None).await.unwrap();
        assert_eq!(idempotency_key, None);
        assert_eq!(
            action,
            Action::Discover {
                operation: "agent.create".into()
            }
        );
    }

    #[tokio::test]
    async fn agent_create_submits_the_discovered_computer() {
        let computer_id = ComputerId::from_uuid(uuid::Uuid::from_u128(11));
        let role_file = tempfile::NamedTempFile::new().unwrap();
        std::fs::write(role_file.path(), "Build and verify").unwrap();
        let cli = TestAgentCli::try_parse_from(vec![
            "sumi-agent".to_owned(),
            "agent".to_owned(),
            "create".to_owned(),
            "Coder".to_owned(),
            "--role-file".to_owned(),
            role_file.path().to_string_lossy().into_owned(),
            "--computer-id".to_owned(),
            computer_id.to_string(),
            "--driver".to_owned(),
            "codex".to_owned(),
            "--json".to_owned(),
        ])
        .unwrap()
        .agent;
        let (action, _) = cli.action(None).await.unwrap();
        assert!(matches!(
            action,
            Action::AgentCreate {
                computer_id: selected,
                driver: DriverKind::Codex,
                ..
            } if selected == computer_id
        ));
    }

    #[tokio::test]
    async fn json_validation_error_is_one_envelope() {
        let cli = parse(["sumi-agent", "context", "current"]).unwrap();
        let mut output = Vec::new();
        let exit = crate::agent_cli::execute(cli, None, &mut output).await;
        assert_ne!(exit, 0);
        assert_eq!(output.iter().filter(|byte| **byte == b'\n').count(), 1);
        let value: serde_json::Value = serde_json::from_slice(&output).unwrap();
        assert_eq!(value["ok"], false);
        assert_eq!(value["error"]["code"], "invalid_argument");
    }
}
