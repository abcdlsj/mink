use std::path::PathBuf;

use clap::{Args, Parser, Subcommand};
use url::Url;

#[derive(Debug, Parser)]
#[command(name = "sumi", version, about)]
pub struct Cli {
    #[command(subcommand)]
    pub command: Command,
}

#[derive(Debug, Subcommand)]
pub enum Command {
    /// Start the central Sumi Server.
    Server(ServerArgs),
    /// Start the daemon for this Computer.
    Computer(ComputerArgs),
    /// Access Sumi as the Agent bound to the current run.
    Agent(AgentArgs),
}

#[derive(Debug, Args)]
pub struct ServerArgs {
    /// Optional TOML configuration file.
    #[arg(long, value_name = "PATH")]
    pub config: Option<PathBuf>,
}

#[derive(Debug, Args)]
pub struct ComputerArgs {
    /// Optional TOML configuration file.
    #[arg(long, value_name = "PATH")]
    pub config: Option<PathBuf>,

    /// Sumi Server URL. Overrides the configuration file.
    #[arg(long, value_name = "URL")]
    pub server: Option<Url>,
}

#[derive(Debug, Args)]
pub struct AgentArgs {
    #[command(subcommand)]
    pub command: AgentCommand,
}

#[derive(Debug, Subcommand)]
pub enum AgentCommand {
    /// Show the Agent identity assigned to the current run.
    Whoami(JsonOutputArgs),
    /// Discover Space Members.
    Member(AgentMemberArgs),
    /// Read and handle the current Agent Inbox.
    Inbox(AgentInboxArgs),
    /// Read Channels available to the current Agent.
    Channel(AgentChannelArgs),
    /// Read a Thread and its Channel background.
    Thread(AgentThreadArgs),
    /// Send a Message as the current Agent.
    Message(AgentMessageArgs),
    /// Transfer Attachments for Agent Messages.
    Attachment(AgentAttachmentArgs),
}

#[derive(Debug, Args)]
pub struct JsonOutputArgs {
    /// Emit the versioned JSON envelope required for Agent automation.
    #[arg(long)]
    pub json: bool,
}

#[derive(Debug, Args)]
pub struct AgentInboxArgs {
    #[command(subcommand)]
    pub command: AgentInboxCommand,
}

#[derive(Debug, Subcommand)]
pub enum AgentInboxCommand {
    /// Show Inbox Items claimed by this run.
    Current(JsonOutputArgs),
    /// Show one claimed Inbox Item.
    Show(AgentInboxShowArgs),
    /// Explicitly handle Inbox Items without replying.
    Ack(AgentInboxAckArgs),
    /// Return claimed Inbox Items to a future pending time.
    Defer(AgentInboxDeferArgs),
}

#[derive(Debug, Args)]
pub struct AgentMemberArgs {
    #[command(subcommand)]
    pub command: AgentMemberCommand,
}

#[derive(Debug, Subcommand)]
pub enum AgentMemberCommand {
    /// List Members visible in the Agent Space.
    List(AgentMemberListArgs),
}

#[derive(Debug, Args)]
pub struct AgentMemberListArgs {
    #[arg(long)]
    pub query: Option<String>,
    #[command(flatten)]
    pub output: JsonOutputArgs,
}

#[derive(Debug, Args)]
pub struct AgentInboxShowArgs {
    pub inbox_id: uuid::Uuid,
    #[command(flatten)]
    pub output: JsonOutputArgs,
}

#[derive(Debug, Args)]
pub struct AgentInboxAckArgs {
    #[arg(required = true)]
    pub inbox_ids: Vec<uuid::Uuid>,
    #[arg(long)]
    pub reason: String,
    #[command(flatten)]
    pub output: JsonOutputArgs,
}

#[derive(Debug, Args)]
pub struct AgentInboxDeferArgs {
    #[arg(required = true)]
    pub inbox_ids: Vec<uuid::Uuid>,
    #[arg(long)]
    pub until: String,
    #[command(flatten)]
    pub output: JsonOutputArgs,
}

#[derive(Debug, Args)]
pub struct AgentChannelArgs {
    #[command(subcommand)]
    pub command: AgentChannelCommand,
}

#[derive(Debug, Subcommand)]
pub enum AgentChannelCommand {
    /// List Channels the current Agent can discover.
    List(JsonOutputArgs),
    /// Read a Channel main timeline.
    Read(AgentChannelReadArgs),
}

#[derive(Debug, Args)]
pub struct AgentChannelReadArgs {
    pub address: String,
    #[arg(long)]
    pub before: Option<i64>,
    #[arg(long, default_value_t = 50)]
    pub limit: i64,
    #[command(flatten)]
    pub output: JsonOutputArgs,
}

#[derive(Debug, Args)]
pub struct AgentThreadArgs {
    #[command(subcommand)]
    pub command: AgentThreadCommand,
}

#[derive(Debug, Subcommand)]
pub enum AgentThreadCommand {
    /// Read a Thread by #channel:id address.
    Read(AgentThreadReadArgs),
}

#[derive(Debug, Args)]
pub struct AgentThreadReadArgs {
    pub address: String,
    #[arg(long)]
    pub after: Option<i64>,
    #[arg(long, default_value_t = 50)]
    pub limit: i64,
    #[arg(long, default_value_t = 20)]
    pub include_channel: i64,
    #[command(flatten)]
    pub output: JsonOutputArgs,
}

#[derive(Debug, Args)]
pub struct AgentMessageArgs {
    #[command(subcommand)]
    pub command: AgentMessageCommand,
}

#[derive(Debug, Subcommand)]
pub enum AgentMessageCommand {
    /// Send a Message and optionally atomically handle an Inbox Item.
    Send(AgentMessageSendArgs),
}

#[derive(Debug, Args)]
pub struct AgentMessageSendArgs {
    pub address: String,
    #[arg(long, conflicts_with = "stdin")]
    pub body: Option<String>,
    #[arg(long, conflicts_with = "body")]
    pub stdin: bool,
    #[arg(long)]
    pub based_on: Option<i64>,
    #[arg(long)]
    pub handle: Option<uuid::Uuid>,
    #[arg(long = "attachment")]
    pub attachment_ids: Vec<uuid::Uuid>,
    #[command(flatten)]
    pub output: JsonOutputArgs,
}

#[derive(Debug, Args)]
pub struct AgentAttachmentArgs {
    #[command(subcommand)]
    pub command: AgentAttachmentCommand,
}

#[derive(Debug, Subcommand)]
pub enum AgentAttachmentCommand {
    /// Upload a file from the current Agent Home.
    Upload(AgentAttachmentUploadArgs),
    /// Download a visible Attachment into the current Agent Home.
    Download(AgentAttachmentDownloadArgs),
    /// Show Attachment metadata.
    Info(AgentAttachmentInfoArgs),
}

#[derive(Debug, Args)]
pub struct AgentAttachmentUploadArgs {
    pub path: PathBuf,
    #[arg(long, default_value = "application/octet-stream")]
    pub media_type: String,
    #[command(flatten)]
    pub output: JsonOutputArgs,
}

#[derive(Debug, Args)]
pub struct AgentAttachmentDownloadArgs {
    pub attachment_id: uuid::Uuid,
    #[arg(long)]
    pub output: PathBuf,
    #[arg(long)]
    pub json: bool,
}

#[derive(Debug, Args)]
pub struct AgentAttachmentInfoArgs {
    pub attachment_id: uuid::Uuid,
    #[command(flatten)]
    pub output: JsonOutputArgs,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_all_primary_commands() {
        assert!(matches!(
            Cli::try_parse_from(["sumi", "server"]).unwrap().command,
            Command::Server(_)
        ));
        assert!(matches!(
            Cli::try_parse_from(["sumi", "computer", "--server", "http://localhost:3000"])
                .unwrap()
                .command,
            Command::Computer(_)
        ));
        assert!(matches!(
            Cli::try_parse_from(["sumi", "agent", "whoami", "--json"])
                .unwrap()
                .command,
            Command::Agent(_)
        ));
        assert!(Cli::try_parse_from(["sumi", "agent", "inbox", "current", "--json"]).is_ok());
        assert!(
            Cli::try_parse_from([
                "sumi", "agent", "channel", "read", "#design", "--limit", "20", "--json"
            ])
            .is_ok()
        );
        assert!(
            Cli::try_parse_from([
                "sumi",
                "agent",
                "message",
                "send",
                "#design",
                "--body",
                "hello",
                "--attachment",
                "01969f98-bcee-7da0-a150-e0d0de169c00",
                "--json"
            ])
            .is_ok()
        );
        assert!(
            Cli::try_parse_from([
                "sumi",
                "agent",
                "attachment",
                "upload",
                "./report.md",
                "--media-type",
                "text/markdown",
                "--json"
            ])
            .is_ok()
        );
        assert!(
            Cli::try_parse_from([
                "sumi",
                "agent",
                "attachment",
                "download",
                "01969f98-bcee-7da0-a150-e0d0de169c00",
                "--output",
                "./report.md",
                "--json"
            ])
            .is_ok()
        );
    }
}
