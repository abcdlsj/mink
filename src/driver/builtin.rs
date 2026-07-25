use std::{sync::Arc, time::Duration};

use anyhow::{Context, Result, bail};
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
};

use super::{Driver, DriverEnvironment, DriverEvent, DriverOutcome, DriverProcess, DriverRun};

#[allow(dead_code)]
pub struct BuiltinDriver;

impl BuiltinDriver {
    #[allow(dead_code)]
    pub fn new() -> Self {
        Self
    }
}

#[async_trait]
impl Driver for BuiltinDriver {
    async fn validate(&self, _environment: &DriverEnvironment) -> Result<()> {
        Ok(())
    }

    async fn start(&self, run: DriverRun) -> Result<DriverProcess> {
        let api_key = run
            .environment
            .codex_api_key
            .as_ref()
            .map(|k| k.expose_secret().to_owned())
            .or_else(|| std::env::var("SUMI_BUILTIN_API_KEY").ok())
            .context("builtin driver requires an API key")?;
        let model = std::env::var("SUMI_BUILTIN_MODEL").unwrap_or_else(|_| "gpt-4o".into());
        let base_url = std::env::var("SUMI_BUILTIN_BASE_URL").ok();

        let mut provider_config = ProviderConfig::openai(api_key, model);
        if let Some(url) = base_url {
            provider_config = provider_config.with_base_url(url);
        }
        let provider = Arc::new(
            OpenAiProvider::new(provider_config).context("failed to create builtin provider")?,
        );

        let tools = Arc::new(DaemonToolRunner {
            socket_path: run.environment.socket_path.clone(),
            run_token: run.environment.run_token.clone(),
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
            input: run.prompt,
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
        // Ensure the task completes
        let _ = task.await;
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
            description: "Read a file from the workspace.".into(),
            parameters: serde_json::json!({
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Path to the file to read"}
                },
                "required": ["path"]
            }),
        },
        ToolDef {
            name: "write".into(),
            description: "Write content to a file.".into(),
            parameters: serde_json::json!({
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Path to write"},
                    "content": {"type": "string", "description": "Content to write"}
                },
                "required": ["path", "content"]
            }),
        },
        ToolDef {
            name: "edit".into(),
            description: "Make precise edits to a file.".into(),
            parameters: serde_json::json!({
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Path to the file"},
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
    #[allow(dead_code)]
    socket_path: std::path::PathBuf,
    #[allow(dead_code)]
    run_token: String,
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
                tokio::fs::read_to_string(path)
                    .await
                    .map_err(|e| anyhow::anyhow!("read failed: {e}"))
            }
            "write" => {
                let path = args["path"].as_str().context("write requires path")?;
                let content = args["content"].as_str().context("write requires content")?;
                tokio::fs::write(path, content)
                    .await
                    .map_err(|e| anyhow::anyhow!("write failed: {e}"))?;
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
                let content = tokio::fs::read_to_string(path).await?;
                if let Some(pos) = content.find(old_text) {
                    let new_content = format!(
                        "{}{}{}",
                        &content[..pos],
                        new_text,
                        &content[pos + old_text.len()..]
                    );
                    tokio::fs::write(path, new_content).await?;
                    Ok(format!("Edited {}", path))
                } else {
                    bail!("old_text not found in {path}");
                }
            }
            "bash" => {
                let command = args["command"].as_str().context("bash requires command")?;
                let output = tokio::process::Command::new("sh")
                    .arg("-c")
                    .arg(command)
                    .output()
                    .await
                    .context("bash command failed")?;
                let stdout = String::from_utf8_lossy(&output.stdout);
                Ok(stdout.into_owned())
            }
            _ => bail!("unknown tool: {name}"),
        }
    }
}
