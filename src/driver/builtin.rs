use std::{path::Path, process::Stdio, sync::Arc, time::Duration};

use anyhow::{Context, Result, bail, ensure};
use async_trait::async_trait;
use serde_json::Value;
use tokio::sync::mpsc;

use crate::agent_core::{
    engine::{Engine, Turn},
    provider::{OpenAiProvider, ProviderConfig},
    session::Session,
    tool_executor::{ToolEvent, ToolExecutor, ToolRunner},
    types::{Message, ToolDef},
    workspace::{agent_rooted_path, collect_shell_output, edit_utf8, read_utf8, write_utf8},
};

use super::codex::{sandboxed_command, validate_sandbox_backend};
use super::{
    Driver, DriverEnvironment, DriverEvent, DriverOutcome, DriverProcess, DriverRun,
    DriverStopOutcome, ProcessExitEvidence,
};

pub struct BuiltinDriver {
    provider_config: Option<ProviderConfig>,
}

impl BuiltinDriver {
    pub fn new(provider_config: Option<ProviderConfig>) -> Self {
        Self { provider_config }
    }

    #[cfg(test)]
    fn with_provider(provider_config: ProviderConfig) -> Self {
        Self::new(Some(provider_config))
    }

    fn provider_config(&self) -> Result<ProviderConfig> {
        self.provider_config
            .clone()
            .context("Builtin provider is not configured on this Computer")
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
        self.provider_config()?;
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
        let provider_config = self
            .provider_config()?
            .with_prompt_cache_key(run.prompt.cache_key.clone());
        let provider = Arc::new(
            OpenAiProvider::new(provider_config).context("failed to create builtin provider")?,
        );

        let tools = Arc::new(DaemonToolRunner {
            environment: run.environment.clone(),
        });
        let executor = ToolExecutor::new(tools);
        let mut system_messages = Vec::new();
        if !run.prompt.global_static.is_empty() {
            system_messages.push(Message::cacheable_system(run.prompt.global_static.clone()));
        }
        if !run.prompt.agent_static.is_empty() {
            system_messages.push(Message::cacheable_system(run.prompt.agent_static.clone()));
        }
        if !run.prompt.dynamic_context.is_empty() {
            system_messages.push(Message::system(run.prompt.dynamic_context.clone()));
        }
        let engine = Arc::new(Engine::new(
            provider,
            executor,
            system_messages,
            daemon_tool_defs(),
        ));

        let turn = Turn {
            input: run.prompt.user_input.clone(),
            blocked_tools: std::collections::HashMap::new(),
        };

        let (tool_events_tx, mut tool_events_rx) = mpsc::channel::<ToolEvent>(64);
        let (driver_events_tx, driver_events_rx) = mpsc::channel::<DriverEvent>(64);
        let bridge_events = driver_events_tx.clone();

        let run_id = run.run_id;
        let (activation_tx, activation_rx) = tokio::sync::oneshot::channel();
        let task = tokio::spawn(async move {
            if activation_rx.await.is_err() {
                return;
            }
            let mut session = Session::default();
            let result = engine.run(&turn, &mut session, &tool_events_tx, None).await;
            let usage = session.token_usage();
            let cache_read_ratio = if usage.input_tokens > 0 {
                f64::from(usage.cached_input_tokens) / f64::from(usage.input_tokens)
            } else {
                0.0
            };
            tracing::info!(
                %run_id,
                input_tokens = usage.input_tokens,
                output_tokens = usage.output_tokens,
                cached_input_tokens = usage.cached_input_tokens,
                cache_write_tokens = usage.cache_write_tokens,
                cache_read_ratio,
                "Builtin LLM token usage"
            );
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
                    ToolEvent::Failed { tool } => DriverEvent::OutputReceived {
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
            activation: Some(activation_tx),
        })
    }

    async fn observe(
        &self,
        process: &mut DriverProcess,
        events: &mpsc::Sender<DriverEvent>,
    ) -> Result<DriverOutcome> {
        let DriverProcess::Internal {
            task, events: rx, ..
        } = process
        else {
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

    async fn cancel(
        &self,
        process: &mut DriverProcess,
        _grace_period: Duration,
    ) -> Result<DriverStopOutcome> {
        if let DriverProcess::Internal { task, .. } = process {
            task.abort();
            let _ = task.await;
        }
        Ok(DriverStopOutcome::Reaped {
            exit: ProcessExitEvidence::INTERNAL_TASK,
            sigterm_sent: false,
            sigkill_sent: false,
        })
    }

    async fn reap(&self, process: &mut DriverProcess) -> Result<ProcessExitEvidence> {
        if let DriverProcess::Internal { task, .. } = process {
            let _ = task.await;
        }
        Ok(ProcessExitEvidence::INTERNAL_TASK)
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
    use std::{
        fs,
        os::unix::fs::PermissionsExt,
        sync::{
            Arc,
            atomic::{AtomicUsize, Ordering},
        },
    };

    use axum::{
        Router,
        http::{HeaderMap, header},
        routing::post,
    };

    use super::*;

    fn loaded_provider_config(root: &tempfile::TempDir, base_url: &str) -> ProviderConfig {
        let settings = root.path().join("settings.json");
        let models = root.path().join("models-store.json");
        let auth = root.path().join("auth.json");
        fs::write(
            &settings,
            r#"{"defaultProvider":"local","defaultModel":"test-model"}"#,
        )
        .unwrap();
        fs::write(
            &models,
            serde_json::json!({
                "local": {
                    "models": [{
                        "id": "test-model",
                        "api": "openai-completions",
                        "baseUrl": base_url
                    }]
                }
            })
            .to_string(),
        )
        .unwrap();
        fs::write(&auth, r#"{"local":{"type":"api_key","key":"test-key"}}"#).unwrap();
        fs::set_permissions(&auth, fs::Permissions::from_mode(0o600)).unwrap();
        let config = crate::config::ComputerConfig {
            builtin_settings_source: Some(settings),
            builtin_models_source: Some(models),
            builtin_auth_source: Some(auth),
            ..crate::config::ComputerConfig::default()
        };
        crate::driver::builtin_config::load(&config)
            .unwrap()
            .unwrap()
            .into_provider_config()
    }

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
            "printf '%s|%s|%s' \"$SUMI_RUN_TOKEN\" \"$SUMI_SOCKET\" \"${OPENAI_API_KEY-unset}\"",
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
                move |headers: HeaderMap| {
                    let calls = Arc::clone(&calls);
                    async move {
                        assert_eq!(
                            headers.get(header::AUTHORIZATION).unwrap(),
                            "Bearer test-key"
                        );
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
        let driver = BuiltinDriver::with_provider(loaded_provider_config(
            &root,
            &format!("http://{address}"),
        ));
        let mut process = driver
            .start(DriverRun {
                run_id: uuid::Uuid::now_v7(),
                prompt: "system prompt".into(),
                environment: environment.clone(),
            })
            .await
            .unwrap();
        process.activate().await.unwrap();
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
                run_id: uuid::Uuid::now_v7(),
                prompt: "system prompt".into(),
                environment: environment(&root),
            })
            .await
            .unwrap();

        let outcome = driver.cancel(&mut process, Duration::ZERO).await.unwrap();
        assert_eq!(
            outcome,
            DriverStopOutcome::Reaped {
                exit: ProcessExitEvidence::INTERNAL_TASK,
                sigterm_sent: false,
                sigkill_sent: false,
            }
        );
        server.abort();
    }
}
