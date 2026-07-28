use uuid::Uuid;

use crate::prompt::{self, AgentRunPrompt, PromptContext};

pub fn build(
    agent_name: &str,
    agent_handle: &str,
    agent_id: Uuid,
    role_revision: i64,
    role_text: &str,
    summaries: &[serde_json::Value],
) -> AgentRunPrompt {
    let inbox_summary = serde_json::to_string_pretty(summaries).unwrap_or_else(|_| "[]".to_owned());

    prompt::build_agent_run_prompt(&PromptContext {
        agent_name: agent_name.to_owned(),
        agent_handle: agent_handle.to_owned(),
        agent_id: agent_id.to_string(),
        role_revision,
        role_text: role_text.to_owned(),
        inbox_summary,
    })
}
