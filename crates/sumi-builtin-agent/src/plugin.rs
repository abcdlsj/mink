use std::path::PathBuf;

use anyhow::Result;
use async_trait::async_trait;
use serde_json::Value;
use uuid::Uuid;

use crate::types::ToolDef;

/// Context passed to a plugin tool call for the current agent turn.
#[derive(Clone, Debug)]
pub struct PluginContext {
    pub agent_id: Uuid,
    pub agent_home: PathBuf,
}

/// A conversation-channel or tool plugin for the builtin agent runtime.
///
/// Plugins can extend the system prompt, register extra tools, and execute
/// those tools inside the agent loop. Tool names must be unique across all
/// plugins and the builtin tools (`read`, `write`, `edit`, `bash`).
#[async_trait]
pub trait AgentPlugin: Send + Sync {
    fn name(&self) -> &str;

    /// Extra system-prompt text appended to the dynamic agent contract.
    fn contract(&self) -> String {
        String::new()
    }

    fn tools(&self) -> Vec<ToolDef> {
        Vec::new()
    }

    async fn run_tool(
        &self,
        _context: &PluginContext,
        name: &str,
        _args: &Value,
    ) -> Result<String> {
        anyhow::bail!("plugin {} does not provide tool {name}", self.name())
    }
}
