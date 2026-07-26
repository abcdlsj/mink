use std::{path::Path, process::Stdio, sync::Arc, time::Duration};

use anyhow::{Context, Result, bail, ensure};
use async_trait::async_trait;
use secrecy::ExposeSecret;
use serde_json::Value;
use tokio::sync::mpsc;

use crate::agent_core::{
    engine::{Engine, Turn},
    prompt::PromptContext,
    provider::{OpenAiProvider, ProviderConfig},
    session::Session,
    tool_executor::{ToolEvent, ToolExecutor, ToolRunner},
    types::ToolDef,
    workspace::{agent_rooted_path, collect_shell_output, edit_utf8, read_utf8, write_utf8},
};

use super::codex::{sandboxed_command, validate_sandbox_backend};
use super::{Driver, DriverEnvironment, DriverEvent, DriverOutcome, DriverProcess, DriverRun};

#[allow(dead_code)]
pub struct BuiltinDriver {
    provider_config: Option<ProviderConfig>,
}

impl BuiltinDriver {
    #[allow(dead_code)]
    pub fn new() -> Self {
        Self {
            provider_config: None,
        }
    }

    #[cfg(test)]
    fn with_provider(provider_config: ProviderConfig) -> Self {
        Self {
            provider_config: Some(provider_config),
        }
    }

    fn provider_config(&self, environment: &DriverEnvironment) -> Result<ProviderConfig> {
        if let Some(config) = &self.provider_config {
            return Ok(config.clone());
        }
        let api_key = builtin_api_key(environment)?;
        let model = std::env::var("SUMI_BUILTIN_MODEL").unwrap_or_else(|_| "gpt-4o".into());
        let mut config = ProviderConfig::openai(api_key, model);
        if let Ok(url) = std::env::var("SUMI_BUILTIN_BASE_URL") {
            config = config.with_base_url(url);
        }
        Ok(config)
    }
}

#[async_trait]
impl Driver for BuiltinDriver {
    async fn validate(&self, environment: &DriverEnvironment) -> Result<()> {
        ensure!(environment.agent_home.is_dir(), "Agent Home is unavailable");
        ensure!(
            environment.workspace.is_dir(),
            "Agent workspace is unavailable"
        );
        ensure!(
            environment.agent_home.join("memory").is_dir(),
            "Agent Memory is unavailable"
        );
        ensure!(
            environment.agent_home.join("drivers/builtin").is_dir(),
            "Builtin Driver home is unavailable"
        );
        self.provider_config(environment)?;
        validate_sandbox_backend()?;
        let mut command = builtin_shell_command(environment)?;
        let status = tokio::time::timeout(
            Duration::from_secs(10),
            command
                .arg("-c")
                .arg("true")
                .stdin(Stdio::null())
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .status(),
        )
        .await
        .context("Builtin sandbox validation timed out")?
        .context("failed to validate Builtin sandbox")?;
        ensure!(status.success(), "Builtin sandbox validation failed");
        Ok(())
    }

    async fn start(&self, run: DriverRun) -> Result<DriverProcess> {
        self.validate(&run.environment).await?;
        let provider_config = self.provider_config(&run.environment)?;
        let provider = Arc::new(
            OpenAiProvider::new(provider_config).context("failed to create builtin provider")?,
        );

        let tools = Arc::new(DaemonToolRunner {
            environment: DriverEnvironment {
                codex_api_key: None,
                ..run.environment.clone()
            },
        });
        let executor = ToolExecutor::new(tools);
        let engine = Arc::new(Engine::new(
            provider,
            executor,
            PromptContext::default(),
            Some(run.prompt.clone()),
            daemon_tool_defs(),
        ));

        let turn = Turn {
            input: "Process every claimed Inbox Item now. Use the Sumi CLI and stop only after each Item is handled, acknowledged, or deferred.".into(),
            source: String::new(),
            attachments: vec![],
            blocked_tools: std::collections::HashMap::new(),
        };

        let (tool_events_tx, mut tool_events_rx) = mpsc::channel::<ToolEvent>(64);
        let (driver_events_tx, driver_events_rx) = mpsc::channel::<DriverEvent>(64);
        let bridge_events = driver_events_tx.clone();

        let task = tokio::spawn(async move {
            let mut session = Session::default();
            let result = engine.run(&turn, &mut session, &tool_events_tx, None).await;
            let _ = driver_events_tx
                .send(match result {
                    Ok(()) => DriverEvent::ProcessCompleted,
                    Err(_) => DriverEvent::ProcessFailed,
                })
                .await;
        });
        tokio::spawn(async move {
            while let Some(event) = tool_events_rx.recv().await {
                let driver_event = match event {
                    ToolEvent::Started { tool, .. } => DriverEvent::OutputReceived {
                        event_type: format!("tool.{tool}.started"),
                    },
                    ToolEvent::Finished { tool, .. } => DriverEvent::OutputReceived {
                        event_type: format!("tool.{tool}.finished"),
                    },
                    ToolEvent::Failed {
                        tool,
                        error: _,
                        input: _,
                        tool_call_id: _,
                    } => DriverEvent::OutputReceived {
                        event_type: format!("tool.{tool}.failed"),
                    },
                };
                if bridge_events.send(driver_event).await.is_err() {
                    break;
                }
            }
        });

        Ok(DriverProcess::Internal {
            task,
            events: driver_events_rx,
        })
    }

    async fn observe(
        &self,
        process: &mut DriverProcess,
        events: &mpsc::Sender<DriverEvent>,
    ) -> Result<DriverOutcome> {
        let DriverProcess::Internal { task, events: rx } = process else {
            bail!("Builtin driver requires internal process");
        };

        let mut outcome = DriverOutcome::Completed;
        while let Some(event) = rx.recv().await {
            if event == DriverEvent::ProcessFailed {
                outcome = DriverOutcome::Failed;
            }
            if event == DriverEvent::ProcessCompleted || event == DriverEvent::ProcessFailed {
                events.send(event).await.ok();
                break;
            }
            events.send(event).await.ok();
        }
        if task.await.is_err() {
            outcome = DriverOutcome::Failed;
        }
        Ok(outcome)
    }

    async fn cancel(&self, process: &mut DriverProcess, _grace_period: Duration) -> Result<()> {
        if let DriverProcess::Internal { task, .. } = process {
            task.abort();
        }
        Ok(())
    }

    async fn cleanup(&self, _environment: &DriverEnvironment) -> Result<()> {
        Ok(())
    }
}

fn daemon_tool_defs() -> Vec<ToolDef> {
    vec![
        ToolDef {
            name: "read".into(),
            description: "Read a UTF-8 file from this Agent Home. Path must start with workspace/ or memory/.".into(),
            parameters: serde_json::json!({
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Agent Home path starting with workspace/ or memory/"}
                },
                "required": ["path"]
            }),
        },
        ToolDef {
            name: "write".into(),
            description: "Write a UTF-8 file inside workspace/ or memory/.".into(),
            parameters: serde_json::json!({
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Agent Home path starting with workspace/ or memory/"},
                    "content": {"type": "string", "description": "Content to write"}
                },
                "required": ["path", "content"]
            }),
        },
        ToolDef {
            name: "edit".into(),
            description: "Make a precise edit inside workspace/ or memory/.".into(),
            parameters: serde_json::json!({
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Agent Home path starting with workspace/ or memory/"},
                    "old_text": {"type": "string", "description": "Text to replace"},
                    "new_text": {"type": "string", "description": "Replacement text"}
                },
                "required": ["path", "old_text", "new_text"]
            }),
        },
        ToolDef {
            name: "bash".into(),
            description: "Run a shell command in the workspace.".into(),
            parameters: serde_json::json!({
                "type": "object",
                "properties": {
                    "command": {"type": "string", "description": "Shell command to execute"}
                },
                "required": ["command"]
            }),
        },
    ]
}

struct DaemonToolRunner {
    environment: DriverEnvironment,
}

#[async_trait]
impl ToolRunner for DaemonToolRunner {
    fn definitions(&self) -> Vec<ToolDef> {
        daemon_tool_defs()
    }

    async fn run(&self, name: &str, args: &Value) -> Result<String> {
        match name {
            "read" => {
                let path = args["path"].as_str().context("read requires path")?;
                let (root, relative) = agent_rooted_path(&self.environment.agent_home, path)?;
                read_utf8(&root, &relative).await
            }
            "write" => {
                let path = args["path"].as_str().context("write requires path")?;
                let content = args["content"].as_str().context("write requires content")?;
                let (root, relative) = agent_rooted_path(&self.environment.agent_home, path)?;
                write_utf8(&root, &relative, content).await?;
                Ok(format!("Wrote {}", path))
            }
            "edit" => {
                let path = args["path"].as_str().context("edit requires path")?;
                let old_text = args["old_text"]
                    .as_str()
                    .context("edit requires old_text")?;
                let new_text = args["new_text"]
                    .as_str()
                    .context("edit requires new_text")?;
                let (root, relative) = agent_rooted_path(&self.environment.agent_home, path)?;
                edit_utf8(&root, &relative, old_text, new_text).await?;
                Ok(format!("Edited {}", path))
            }
            "bash" => {
                let command = args["command"].as_str().context("bash requires command")?;
                run_sandboxed_shell(&self.environment, command).await
            }
            _ => bail!("unknown tool: {name}"),
        }
    }
}

fn builtin_api_key(environment: &DriverEnvironment) -> Result<String> {
    environment
        .codex_api_key
        .as_ref()
        .map(|key| key.expose_secret().to_owned())
        .or_else(|| std::env::var("SUMI_BUILTIN_API_KEY").ok())
        .context("builtin driver requires an API key")
}

fn builtin_shell_command(environment: &DriverEnvironment) -> Result<tokio::process::Command> {
    let shell = Path::new("/bin/sh");
    ensure!(shell.is_file(), "/bin/sh is unavailable");
    let driver_home = environment.agent_home.join("drivers/builtin");
    let sandbox = sandboxed_command(shell, environment, &driver_home)?;
    let mut command = sandbox.command;
    command
        .current_dir(&sandbox.workspace)
        .env_clear()
        .env("PATH", &sandbox.path)
        .env("HOME", &sandbox.agent_home)
        .env("TMPDIR", sandbox.agent_home.join("runs"))
        .env("SUMI_SOCKET", &sandbox.socket_path)
        .env("SUMI_RUN_TOKEN", &environment.run_token)
        .kill_on_drop(true);
    #[cfg(unix)]
    command.process_group(0);
    Ok(command)
}

async fn run_sandboxed_shell(environment: &DriverEnvironment, script: &str) -> Result<String> {
    const MAX_OUTPUT_BYTES: usize = 1024 * 1024;
    let mut command = builtin_shell_command(environment)?;
    let child = command
        .arg("-c")
        .arg(script)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .context("failed to start sandboxed shell")?;
    let (status, text) =
        collect_shell_output(child, Duration::from_secs(300), MAX_OUTPUT_BYTES).await?;
    ensure!(status.success(), "shell exited with {status}: {text}");
    Ok(text)
}

#[cfg(test)]
mod tests {
    use std::sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
    };

    use axum::{Router, http::header, routing::post};

    use super::*;

    fn environment(root: &tempfile::TempDir) -> DriverEnvironment {
        let state_dir = root.path().join("computer");
        let agents_root = state_dir.join("agents");
        let agent_home = agents_root.join("current");
        for relative in [
            "workspace",
            "memory",
            "runs",
            "drivers/builtin",
            "drivers/codex",
        ] {
            std::fs::create_dir_all(agent_home.join(relative)).unwrap();
        }
        let socket_path = state_dir.join("daemon.sock");
        std::fs::write(&socket_path, "").unwrap();
        DriverEnvironment {
            state_dir,
            agent_home: agent_home.clone(),
            agents_root,
            workspace: agent_home.join("workspace"),
            codex_home: agent_home.join("drivers/codex"),
            socket_path,
            run_token: "run-token".into(),
            path: std::env::var("PATH").unwrap(),
            codex_api_key: None,
        }
    }

    #[tokio::test]
    async fn builtin_file_tools_are_scoped_to_workspace_and_memory() {
        let root = tempfile::tempdir().unwrap();
        let environment = environment(&root);
        let runner = DaemonToolRunner { environment };

        runner
            .run(
                "write",
                &serde_json::json!({"path": "memory/MEMORY.md", "content": "durable"}),
            )
            .await
            .unwrap();
        assert_eq!(
            runner
                .run("read", &serde_json::json!({"path": "memory/MEMORY.md"}))
                .await
                .unwrap(),
            "durable"
        );
        assert!(
            runner
                .run("read", &serde_json::json!({"path": "../secrets.json"}))
                .await
                .is_err()
        );
        assert!(
            runner
                .run(
                    "read",
                    &serde_json::json!({"path": "drivers/codex/auth.json"}),
                )
                .await
                .is_err()
        );
    }

    #[tokio::test]
    async fn builtin_shell_gets_capability_but_cannot_read_computer_state() {
        if validate_sandbox_backend().is_err() {
            return;
        }
        let root = tempfile::tempdir().unwrap();
        let environment = environment(&root);
        let secret = environment.state_dir.join("secrets.json");
        std::fs::write(&secret, "computer-secret").unwrap();

        let visible = run_sandboxed_shell(
            &environment,
            "printf '%s|%s|%s' \"$SUMI_RUN_TOKEN\" \"$SUMI_SOCKET\" \"${SUMI_BUILTIN_API_KEY-unset}\"",
        )
        .await
        .unwrap();
        assert!(visible.starts_with("run-token|"));
        assert!(visible.ends_with("|unset"));
        assert!(
            run_sandboxed_shell(&environment, &format!("cat '{}'", secret.display()))
                .await
                .is_err()
        );
    }

    #[tokio::test]
    async fn builtin_runs_the_sse_tool_loop_end_to_end() {
        if validate_sandbox_backend().is_err() {
            return;
        }
        let calls = Arc::new(AtomicUsize::new(0));
        let app = Router::new().route(
            "/chat/completions",
            post({
                let calls = Arc::clone(&calls);
                move || {
                    let calls = Arc::clone(&calls);
                    async move {
                        let body = if calls.fetch_add(1, Ordering::SeqCst) == 0 {
                            let first = serde_json::json!({
                                "choices": [{
                                    "delta": {"tool_calls": [{
                                        "index": 0,
                                        "id": "call-1",
                                        "function": {
                                            "name": "wr",
                                            "arguments": "{\"path\":\"memory/MEMORY.md\","
                                        }
                                    }]},
                                    "finish_reason": null
                                }]
                            });
                            let second = serde_json::json!({
                                "choices": [{
                                    "delta": {"tool_calls": [{
                                        "index": 0,
                                        "function": {
                                            "name": "ite",
                                            "arguments": "\"content\":\"durable\"}"
                                        }
                                    }]},
                                    "finish_reason": null
                                }]
                            });
                            let finish = serde_json::json!({
                                "choices": [{"delta": {}, "finish_reason": "tool_calls"}],
                                "usage": null
                            });
                            format!(
                                "data: {first}\n\ndata: {second}\n\ndata: {finish}\n\ndata: [DONE]\n\n"
                            )
                        } else {
                            let answer = serde_json::json!({
                                "choices": [{
                                    "delta": {"content": "done"},
                                    "finish_reason": "stop"
                                }],
                                "usage": null
                            });
                            format!("data: {answer}\n\ndata: [DONE]\n\n")
                        };
                        ([(header::CONTENT_TYPE, "text/event-stream")], body)
                    }
                }
            }),
        );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
        let root = tempfile::tempdir().unwrap();
        let environment = environment(&root);
        let driver = BuiltinDriver::with_provider(
            ProviderConfig::openai("test-key", "test-model".into())
                .with_base_url(format!("http://{address}")),
        );
        let mut process = driver
            .start(DriverRun {
                prompt: "system prompt".into(),
                environment: environment.clone(),
            })
            .await
            .unwrap();
        let (events, _receiver) = mpsc::channel(16);

        let outcome = driver.observe(&mut process, &events).await.unwrap();

        assert_eq!(outcome, DriverOutcome::Completed);
        assert_eq!(calls.load(Ordering::SeqCst), 2);
        assert_eq!(
            std::fs::read_to_string(environment.agent_home.join("memory/MEMORY.md")).unwrap(),
            "durable"
        );
        server.abort();
    }

    #[tokio::test]
    async fn builtin_cancel_aborts_an_in_flight_provider_request() {
        if validate_sandbox_backend().is_err() {
            return;
        }
        let app = Router::new().route(
            "/chat/completions",
            post(|| async {
                std::future::pending::<()>().await;
                ([(header::CONTENT_TYPE, "text/event-stream")], "")
            }),
        );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
        let root = tempfile::tempdir().unwrap();
        let driver = BuiltinDriver::with_provider(
            ProviderConfig::openai("test-key", "test-model".into())
                .with_base_url(format!("http://{address}")),
        );
        let mut process = driver
            .start(DriverRun {
                prompt: "system prompt".into(),
                environment: environment(&root),
            })
            .await
            .unwrap();

        driver.cancel(&mut process, Duration::ZERO).await.unwrap();
        let DriverProcess::Internal { task, .. } = &mut process else {
            panic!("Builtin must return an internal process");
        };
        let error = tokio::time::timeout(Duration::from_secs(1), task)
            .await
            .expect("canceled Builtin task should stop")
            .expect_err("canceled Builtin task should not complete normally");
        assert!(error.is_cancelled());
        server.abort();
    }
}
