mod agent_cli;
mod cli;
mod computer;
mod config;
mod ids;
mod protocol;
mod release;
mod server;

use std::process::ExitCode;

use clap::{Parser, error::ErrorKind};
use cli::{Cli, Command, ReleaseCommand, SchemaCommand};

#[tokio::main]
async fn main() -> ExitCode {
    init_tracing();

    let cli = match Cli::try_parse() {
        Ok(cli) => cli,
        Err(error) => return handle_parse_error(error).await,
    };
    let (result, exit_code) = match cli.command {
        Command::Server(args) => (server::run(args).await, None),
        Command::Computer(args) => (computer::run(args).await, None),
        Command::Updater(args) => (computer::update::run_updater(args).await, None),
        Command::Release(args) => {
            let result = match args.command {
                ReleaseCommand::Computer(args) => release::computer(args).await,
            };
            (result, None)
        }
        Command::Agent(args) => {
            let stdin = if args.requires_stdin() {
                use tokio::io::AsyncReadExt;
                let mut input = String::new();
                if let Err(error) = tokio::io::stdin().read_to_string(&mut input).await {
                    tracing::error!(%error, "failed to read Agent command stdin");
                    return ExitCode::from(1);
                }
                Some(input)
            } else {
                None
            };
            let mut output = std::io::stdout().lock();
            let code = agent_cli::execute(args, stdin, &mut output).await;
            (Ok(()), Some(code))
        }
        Command::Schema(args) => {
            let result = match args.command {
                SchemaCommand::BrowserOpenapi => server::write_browser_openapi(),
            };
            (result, None)
        }
    };

    if let Some(code) = exit_code {
        return ExitCode::from(code);
    }

    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            tracing::error!(%error, "command failed");
            ExitCode::from(1)
        }
    }
}

async fn handle_parse_error(error: clap::Error) -> ExitCode {
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
        let error_text = error.to_string();
        let (message, details) = agent_cli::classify_error(
            &error_text,
            protocol::capability::ErrorCode::InvalidArgument,
            false,
        );
        let mut details = details;
        if error_text.contains("--computer-id")
            && let Ok(response) = agent_cli::client::call(
                protocol::capability::Action::Discover {
                    operation: "agent.create".to_owned(),
                },
                None,
            )
            .await
            && response.ok
        {
            let available = response.data.and_then(|data| {
                data.pointer("/input/fields")
                    .and_then(|fields| fields.as_array())
                    .and_then(|fields| {
                        fields.iter().find(|field| {
                            field.get("name").and_then(serde_json::Value::as_str)
                                == Some("computer_id")
                        })
                    })
                    .and_then(|field| field.get("available"))
                    .cloned()
            });
            if let Some(available) = available {
                details.insert("available_computers".to_owned(), available);
            }
        }
        let response = protocol::capability::Response::<serde_json::Value>::failure(
            protocol::capability::Error {
                code: protocol::capability::ErrorCode::InvalidArgument,
                message,
                retryable: false,
                details,
            },
        );
        if let Ok(response) = serde_json::to_string(&response) {
            println!("{response}");
        }
    } else {
        let _ = error.print();
    }
    ExitCode::from(2)
}

fn init_tracing() {
    use std::io::IsTerminal;

    let filter = tracing_subscriber::EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| "sumi=info,tower_http=info".into());
    tracing_subscriber::fmt()
        .with_env_filter(filter)
        .with_target(true)
        .with_ansi(std::io::stderr().is_terminal())
        .with_writer(std::io::stderr)
        .init();
}
