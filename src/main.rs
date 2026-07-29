#[path = "agent_cli.rs"]
mod legacy_agent_cli;
// 新 Agent CLI 在最终运行时切换前只通过自身流程测试验证。
#[allow(dead_code)]
#[path = "agent_cli/mod.rs"]
mod agent_cli;
mod agent_config;
mod agent_core;
mod cli;
// 新运行时在最终切换任务前必须保持未接入状态。
#[allow(dead_code)]
mod computer;
mod computer_protocol;
mod config;
mod database;
mod driver;
#[allow(dead_code)]
mod ids;
mod legacy_computer;
mod legacy_server;
mod local_protocol;
mod prompt;
#[allow(dead_code)]
mod protocol;
#[allow(dead_code)]
mod server;
mod supervisor;

use std::process::ExitCode;

use clap::{Parser, error::ErrorKind};
use cli::{Cli, Command, SchemaCommand};

#[tokio::main]
async fn main() -> ExitCode {
    init_tracing();

    let cli = match Cli::try_parse() {
        Ok(cli) => cli,
        Err(error) => return handle_parse_error(error),
    };
    let (result, agent_command) = match cli.command {
        Command::Server(args) => (legacy_server::run(args).await, false),
        Command::Computer(args) => (legacy_computer::run(args).await, false),
        Command::Agent(args) => (legacy_agent_cli::run(args).await, true),
        Command::Schema(args) => {
            let result = match args.command {
                SchemaCommand::BrowserOpenapi => server::write_browser_openapi(),
            };
            (result, false)
        }
    };

    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            tracing::error!(error = ?error, "command failed");
            ExitCode::from(if agent_command {
                legacy_agent_cli::exit_code(&error)
            } else {
                1
            })
        }
    }
}

fn handle_parse_error(error: clap::Error) -> ExitCode {
    if matches!(
        error.kind(),
        ErrorKind::DisplayHelp | ErrorKind::DisplayVersion
    ) {
        let _ = error.print();
        return ExitCode::SUCCESS;
    }
    let agent_json = std::env::args_os().nth(1).is_some_and(|arg| arg == "agent")
        && std::env::args_os().any(|arg| arg == "--json");
    if agent_json {
        let response =
            local_protocol::LocalResponse::failure("invalid_arguments", error.to_string(), false);
        if let Ok(response) = serde_json::to_string(&response) {
            println!("{response}");
        }
    } else {
        let _ = error.print();
    }
    ExitCode::from(2)
}

fn init_tracing() {
    let filter = tracing_subscriber::EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| "sumi=info,tower_http=info".into());
    tracing_subscriber::fmt()
        .with_env_filter(filter)
        .with_target(false)
        .with_writer(std::io::stderr)
        .compact()
        .init();
}
