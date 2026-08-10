use std::path::PathBuf;

use clap::{Args, Parser, Subcommand};
use url::Url;

use crate::agent_cli::commands::AgentCli;

#[derive(Debug, Parser)]
#[command(name = "sumi", version, about)]
pub(crate) struct Cli {
    #[command(subcommand)]
    pub(crate) command: Command,
}

#[derive(Debug, Subcommand)]
pub(crate) enum Command {
    /// Start the central Sumi Server.
    Server(ServerArgs),
    /// Start the daemon for this Computer.
    Computer(ComputerArgs),
    /// Apply a staged Computer update.
    #[command(hide = true)]
    Updater(UpdaterArgs),
    /// Build release metadata for deployment.
    Release(ReleaseArgs),
    /// Access Sumi as the Agent bound to the current Run.
    Agent(AgentCli),
    /// Export schemas used by development tooling.
    #[command(hide = true)]
    Schema(SchemaArgs),
}

#[derive(Debug, Args)]
pub(crate) struct SchemaArgs {
    #[command(subcommand)]
    pub(crate) command: SchemaCommand,
}

#[derive(Debug, Subcommand)]
pub(crate) enum SchemaCommand {
    /// Write the Browser API OpenAPI document to stdout.
    BrowserOpenapi,
}

#[derive(Debug, Args)]
pub(crate) struct ServerArgs {
    /// TOML configuration file. Defaults to $HOME/.sumi/config.toml when present.
    #[arg(long, value_name = "PATH")]
    pub(crate) config: Option<PathBuf>,
}

#[derive(Clone, Debug, Args)]
pub(crate) struct ComputerArgs {
    /// TOML configuration file. Defaults to $HOME/.sumi/config.toml when present.
    #[arg(long, value_name = "PATH")]
    pub(crate) config: Option<PathBuf>,

    /// Sumi Server URL. Overrides the configuration file.
    #[arg(long, value_name = "URL")]
    pub(crate) server: Option<Url>,
}

#[derive(Debug, Args)]
pub(crate) struct UpdaterArgs {
    #[arg(long)]
    pub(crate) parent_pid: u32,
    #[arg(long)]
    pub(crate) current_exe: PathBuf,
    #[arg(long)]
    pub(crate) candidate: PathBuf,
    #[arg(long)]
    pub(crate) version: String,
    #[arg(long)]
    pub(crate) computer_home: PathBuf,
    #[arg(long, default_value_t = 30)]
    pub(crate) ready_timeout_seconds: u64,
    #[arg(long)]
    pub(crate) config: Option<PathBuf>,
    #[arg(long)]
    pub(crate) server: Option<Url>,
}

#[derive(Debug, Args)]
pub(crate) struct ReleaseArgs {
    #[command(subcommand)]
    pub(crate) command: ReleaseCommand,
}

#[derive(Debug, Subcommand)]
pub(crate) enum ReleaseCommand {
    /// Package one Computer daemon release.
    Computer(ComputerReleaseArgs),
}

#[derive(Debug, Args)]
pub(crate) struct ComputerReleaseArgs {
    #[arg(long)]
    pub(crate) artifact: PathBuf,
    #[arg(long)]
    pub(crate) version: String,
    #[arg(long)]
    pub(crate) protocol_version: u16,
    #[arg(long)]
    pub(crate) target: Option<String>,
    #[arg(long)]
    pub(crate) output_dir: PathBuf,
}
