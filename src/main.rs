mod agent_cli;
mod cli;
mod computer;
mod config;
mod database;
mod driver;
mod local_protocol;
mod server;
mod supervisor;

use std::process::ExitCode;

use clap::Parser;
use cli::{Cli, Command};

#[tokio::main]
async fn main() -> ExitCode {
    init_tracing();

    let result = match Cli::parse().command {
        Command::Server(args) => server::run(args).await,
        Command::Computer(args) => computer::run(args).await,
        Command::Agent(args) => agent_cli::run(args).await,
    };

    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            tracing::error!(error = ?error, "command failed");
            ExitCode::from(1)
        }
    }
}

fn init_tracing() {
    let filter = tracing_subscriber::EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| "sumi=info,tower_http=info".into());
    tracing_subscriber::fmt()
        .with_env_filter(filter)
        .with_target(false)
        .compact()
        .init();
}
