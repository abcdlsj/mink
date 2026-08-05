use std::sync::Arc;

use anyhow::Result;
use async_trait::async_trait;
use serde_json::Value;
use tokio::sync::mpsc;

use super::types::{ToolCall, ToolResult};

const TOOL_SUMMARY_MAX_CHARS: usize = 800;

/// Events published during tool execution.
#[derive(Clone, Debug)]
pub(super) enum ToolEvent {
    Started {
        tool: String,
        summary: String,
    },
    Finished {
        tool: String,
        summary: String,
    },
    Failed {
        tool: String,
        summary: String,
        error_code: &'static str,
    },
}

/// Trait for executing tools by name.
#[async_trait]
pub(super) trait ToolRunner: Send + Sync {
    async fn run(&self, name: &str, args: &Value) -> Result<String>;
}

#[derive(Clone)]
pub(super) struct ToolExecutor {
    tools: Arc<dyn ToolRunner>,
}

impl ToolExecutor {
    pub(super) fn new(tools: Arc<dyn ToolRunner>) -> Self {
        Self { tools }
    }

    pub(super) async fn run(
        &self,
        calls: &[ToolCall],
        blocked_tools: &std::collections::HashMap<String, String>,
        events: &mpsc::Sender<ToolEvent>,
    ) -> Vec<ToolResult> {
        if parallel_tool_calls(calls) {
            self.run_parallel(calls, blocked_tools, events).await
        } else {
            self.run_serial(calls, blocked_tools, events).await
        }
    }

    async fn run_serial(
        &self,
        calls: &[ToolCall],
        blocked_tools: &std::collections::HashMap<String, String>,
        events: &mpsc::Sender<ToolEvent>,
    ) -> Vec<ToolResult> {
        let mut results = Vec::with_capacity(calls.len());
        for call in calls {
            results.push(self.run_one(call, blocked_tools, events).await);
        }
        results
    }

    async fn run_parallel(
        &self,
        calls: &[ToolCall],
        blocked_tools: &std::collections::HashMap<String, String>,
        events: &mpsc::Sender<ToolEvent>,
    ) -> Vec<ToolResult> {
        let mut handles = Vec::with_capacity(calls.len());
        for call in calls {
            let tools = Arc::clone(&self.tools);
            let call = call.clone();
            let blocked = blocked_tools.clone();
            let events = events.clone();
            handles.push(tokio::spawn(async move {
                run_one_tool(tools.as_ref(), &call, &blocked, &events).await
            }));
        }
        let mut results = Vec::with_capacity(calls.len());
        for handle in handles {
            match handle.await {
                Ok(result) => results.push(result),
                Err(_) => results.push(ToolResult {
                    tool_call_id: String::new(),
                    content: String::new(),
                    error: "tool execution panicked".into(),
                }),
            }
        }
        results
    }

    async fn run_one(
        &self,
        call: &ToolCall,
        blocked_tools: &std::collections::HashMap<String, String>,
        events: &mpsc::Sender<ToolEvent>,
    ) -> ToolResult {
        run_one_tool(self.tools.as_ref(), call, blocked_tools, events).await
    }
}

async fn run_one_tool(
    tools: &dyn ToolRunner,
    call: &ToolCall,
    blocked_tools: &std::collections::HashMap<String, String>,
    events: &mpsc::Sender<ToolEvent>,
) -> ToolResult {
    let summary = tool_summary(&call.name, &call.args);
    let _ = events
        .send(ToolEvent::Started {
            tool: call.name.clone(),
            summary: summary.clone(),
        })
        .await;

    if let Some(reason) = blocked_tools.get(&call.name) {
        let error = if reason.is_empty() {
            format!("{} is not available in this turn", call.name)
        } else {
            format!("{} is not available in this turn: {}", call.name, reason)
        };
        let _ = events
            .send(ToolEvent::Failed {
                tool: call.name.clone(),
                summary,
                error_code: "blocked",
            })
            .await;
        return ToolResult {
            tool_call_id: call.id.clone(),
            content: String::new(),
            error,
        };
    }

    match tools.run(&call.name, &call.args).await {
        Ok(output) => {
            let _ = events
                .send(ToolEvent::Finished {
                    tool: call.name.clone(),
                    summary,
                })
                .await;
            ToolResult {
                tool_call_id: call.id.clone(),
                content: output,
                error: String::new(),
            }
        }
        Err(error) => {
            let error = error.to_string();
            let _ = events
                .send(ToolEvent::Failed {
                    tool: call.name.clone(),
                    summary,
                    error_code: tool_error_code(&error),
                })
                .await;
            ToolResult {
                tool_call_id: call.id.clone(),
                content: String::new(),
                error,
            }
        }
    }
}

fn tool_summary(name: &str, args: &Value) -> String {
    let value = match name {
        "bash" => args.get("command").and_then(Value::as_str),
        "read" | "write" | "edit" => args.get("path").and_then(Value::as_str),
        _ => None,
    }
    .unwrap_or_default()
    .to_owned();
    if value.len() <= TOOL_SUMMARY_MAX_CHARS {
        return value;
    }
    let mut boundary = TOOL_SUMMARY_MAX_CHARS;
    while !value.is_char_boundary(boundary) {
        boundary -= 1;
    }
    format!("{}…", &value[..boundary])
}

fn tool_error_code(error: &str) -> &'static str {
    if error == "sandbox unavailable" {
        "sandbox_unavailable"
    } else if error.starts_with("failed to start sandboxed shell") {
        "spawn_failed"
    } else if error == "shell command timed out" {
        "timeout"
    } else if error.starts_with("shell exited with") {
        "shell_nonzero"
    } else if error.contains("is required") {
        "invalid_args"
    } else if error.contains("path") || error.contains("file") || error.contains("root") {
        "path_or_file"
    } else {
        "tool_error"
    }
}

/// Only parallelize when all tool calls are `read`.
fn parallel_tool_calls(calls: &[ToolCall]) -> bool {
    if calls.len() < 2 {
        return false;
    }
    calls.iter().all(|c| c.name == "read")
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;

    #[test]
    fn bash_summary_is_bounded_at_a_char_boundary() {
        let summary = tool_summary("bash", &json!({"command": "界".repeat(300)}));

        assert!(summary.chars().count() <= TOOL_SUMMARY_MAX_CHARS + 1);
        assert!(summary.ends_with('…'));
    }

    #[test]
    fn edit_summary_never_contains_edited_content() {
        let summary = tool_summary(
            "edit",
            &json!({"path": "workspace/a.md", "old_text": "old", "new_text": "new"}),
        );

        assert_eq!(summary, "workspace/a.md");
    }

    #[test]
    fn tool_error_code_is_stable_for_known_failures() {
        assert_eq!(tool_error_code("shell command timed out"), "timeout");
        assert_eq!(
            tool_error_code("shell exited with exit status: 1: output"),
            "shell_nonzero"
        );
        assert_eq!(
            tool_error_code("sandbox unavailable"),
            "sandbox_unavailable"
        );
    }
}
