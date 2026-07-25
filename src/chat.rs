use std::{collections::HashMap, path::PathBuf, sync::Arc};

use anyhow::{Context, Result};
use tokio::sync::mpsc;

use crate::agent_core::{
    engine::{Engine, Turn},
    prompt::PromptContext,
    provider::{OpenAiProvider, ProviderConfig},
    session::Session,
    tool_executor::{ToolEvent, ToolExecutor, ToolRunner},
    types::ToolDef,
};

pub async fn run(
    api_key: String,
    model: String,
    base_url: Option<String>,
    workspace: Option<PathBuf>,
) -> Result<()> {
    let workspace =
        workspace.unwrap_or_else(|| std::env::current_dir().unwrap_or_else(|_| PathBuf::from(".")));
    std::fs::create_dir_all(&workspace).ok();

    let mut provider_config = ProviderConfig::openai(api_key, model);
    if let Some(url) = base_url {
        provider_config = provider_config.with_base_url(url);
    }
    let provider = Arc::new(OpenAiProvider::new(provider_config)?);
    let tools = Arc::new(ChatToolRunner {
        workspace: workspace.clone(),
    });
    let executor = ToolExecutor::new(tools);

    let memory = load_memory(&workspace);
    let prompt_ctx = PromptContext {
        workspace: workspace.display().to_string(),
        memory,
        ..Default::default()
    };

    let engine = Engine::new(provider, executor, prompt_ctx, None, chat_tool_defs());
    let mut session = Session::default();
    let (events, _rx) = mpsc::channel::<ToolEvent>(64);

    println!("Sumi chat. Type /exit to quit, /compact to force compaction.\n");

    loop {
        let input = read_line("> ")?;
        let trimmed = input.trim();

        if trimmed == "/exit" {
            break;
        }
        if trimmed == "/compact" {
            if session.messages.len() < 8 {
                println!("Not enough messages to compact.");
                continue;
            }
            let summary =
                "Conversation compacted. Prefer this summary and recent messages for context.";
            session.compact(summary.to_owned(), 8);
            println!("Compacted. {} messages remain.", session.messages.len());
            continue;
        }
        if trimmed.is_empty() {
            continue;
        }

        let turn = Turn {
            input: trimmed.to_owned(),
            source: String::new(),
            attachments: vec![],
            blocked_tools: HashMap::new(),
        };

        engine.run(&turn, &mut session, &events).await?;

        if let Some(last) = session
            .messages
            .iter()
            .rev()
            .find(|m| m.role == "assistant")
        {
            println!("{}\\n", last.content);
        }

        // Auto-compact when approaching 64k tokens (≈256k chars)
        if session.estimated_tokens() > 48_000 {
            let summary =
                "Context compacted due to length. Refer to this summary and recent messages.";
            session.compact(summary.to_owned(), 8);
        }
    }

    Ok(())
}

fn load_memory(workspace: &PathBuf) -> String {
    let mut parts: Vec<String> = Vec::new();
    if let Ok(entries) = std::fs::read_dir(workspace) {
        for entry in entries.flatten() {
            let path = entry.path();
            if !path.is_file() {
                continue;
            }
            let Some(name) = path.file_name().and_then(|n| n.to_str()) else {
                continue;
            };
            if !name.ends_with(".md") {
                continue;
            }
            if let Ok(content) = std::fs::read_to_string(&path) {
                let label = name.trim_end_matches(".md");
                parts.push(format!("--- {label} ---\\n{content}"));
            }
        }
    }
    parts.join("\\n\\n")
}

fn read_line(prompt: &str) -> Result<String> {
    use std::io::{BufRead, Write};
    let mut stdout = std::io::stdout();
    write!(stdout, "{prompt}")?;
    stdout.flush()?;
    let stdin = std::io::stdin();
    let mut line = String::new();
    stdin.lock().read_line(&mut line)?;
    Ok(line)
}

fn chat_tool_defs() -> Vec<ToolDef> {
    vec![
        ToolDef {
            name: "read".into(),
            description: "Read a file from the workspace.".into(),
            parameters: serde_json::json!({
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Path relative to workspace"}
                },
                "required": ["path"]
            }),
        },
        ToolDef {
            name: "write".into(),
            description: "Write content to a file. Use to create or update files including your own memory (MEMORY.md or other .md files).".into(),
            parameters: serde_json::json!({
                "type": "object",
                "properties": {
                    "path": {"type": "string"},
                    "content": {"type": "string"}
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
                    "path": {"type": "string"},
                    "old_text": {"type": "string"},
                    "new_text": {"type": "string"}
                },
                "required": ["path", "old_text", "new_text"]
            }),
        },
        ToolDef {
            name: "bash".into(),
            description: "Run a shell command. Use for ls, grep, git, cargo, etc.".into(),
            parameters: serde_json::json!({
                "type": "object",
                "properties": {
                    "command": {"type": "string"}
                },
                "required": ["command"]
            }),
        },
    ]
}

struct ChatToolRunner {
    workspace: PathBuf,
}

#[async_trait::async_trait]
impl ToolRunner for ChatToolRunner {
    fn definitions(&self) -> Vec<ToolDef> {
        chat_tool_defs()
    }

    async fn run(&self, name: &str, args: &serde_json::Value) -> Result<String> {
        match name {
            "read" => {
                let path = args["path"].as_str().context("read requires path")?;
                let full = self.workspace.join(path);
                tokio::fs::read_to_string(&full)
                    .await
                    .map_err(|e| anyhow::anyhow!("read {path}: {e}"))
            }
            "write" => {
                let path = args["path"].as_str().context("write requires path")?;
                let content = args["content"].as_str().context("write requires content")?;
                let full = self.workspace.join(path);
                if let Some(parent) = full.parent() {
                    tokio::fs::create_dir_all(parent).await?;
                }
                tokio::fs::write(&full, content)
                    .await
                    .map_err(|e| anyhow::anyhow!("write {path}: {e}"))?;
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
                let full = self.workspace.join(path);
                let content = tokio::fs::read_to_string(&full)
                    .await
                    .map_err(|e| anyhow::anyhow!("edit {path}: {e}"))?;
                if let Some(pos) = content.find(old_text) {
                    let new_content = format!(
                        "{}{}{}",
                        &content[..pos],
                        new_text,
                        &content[pos + old_text.len()..]
                    );
                    tokio::fs::write(&full, new_content).await?;
                    Ok(format!("Edited {}", path))
                } else {
                    anyhow::bail!("old_text not found in {path}");
                }
            }
            "bash" => {
                let command = args["command"].as_str().context("bash requires command")?;
                let output = tokio::process::Command::new("sh")
                    .arg("-c")
                    .arg(command)
                    .current_dir(&self.workspace)
                    .output()
                    .await
                    .context("bash failed")?;
                let stdout = String::from_utf8_lossy(&output.stdout);
                let stderr = String::from_utf8_lossy(&output.stderr);
                let mut result = stdout.into_owned();
                if !stderr.is_empty() {
                    result.push_str("\nstderr:\n");
                    result.push_str(&stderr);
                }
                Ok(result)
            }
            _ => anyhow::bail!("unknown tool: {name}"),
        }
    }
}
