use std::sync::Arc;

use anyhow::Result;
use async_trait::async_trait;
use serde_json::Value;
use tokio::sync::mpsc;

use super::types::{ToolCall, ToolResult};

/// Events published during tool execution.
#[derive(Clone, Debug)]
pub(super) enum ToolEvent {
    Started { tool: String },
    Finished { tool: String },
    Failed { tool: String },
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
    let _ = events
        .send(ToolEvent::Started {
            tool: call.name.clone(),
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

/// Only parallelize when all tool calls are `read`.
fn parallel_tool_calls(calls: &[ToolCall]) -> bool {
    if calls.len() < 2 {
        return false;
    }
    calls.iter().all(|c| c.name == "read")
}
