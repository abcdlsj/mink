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
    /// Optional TOML configuration file.
    #[arg(long, value_name = "PATH")]
    pub(crate) config: Option<PathBuf>,
}

#[derive(Debug, Args)]
pub(crate) struct ComputerArgs {
    /// Optional TOML configuration file.
    #[arg(long, value_name = "PATH")]
    pub(crate) config: Option<PathBuf>,

    /// Sumi Server URL. Overrides the configuration file.
    #[arg(long, value_name = "URL")]
    pub(crate) server: Option<Url>,
}
