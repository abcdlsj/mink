use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Deserialize, Serialize, Default)]
pub(super) struct Message {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub(super) id: String,
    pub(super) role: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub(super) content: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub(super) attachments: Vec<Attachment>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub(super) reasoning: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub(super) tool_calls: Vec<ToolCall>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub(super) tool_results: Vec<ToolResult>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub(super) usage: Option<TokenUsage>,
    #[serde(skip)]
    pub(super) cache_breakpoint: bool,
}

impl Message {
    pub(super) fn user(content: impl Into<String>) -> Self {
        Self {
            role: "user".into(),
            content: content.into(),
            ..Default::default()
        }
    }

    pub(super) fn tool(results: Vec<ToolResult>) -> Self {
        Self {
            role: "tool".into(),
            tool_results: results,
            ..Default::default()
        }
    }

    pub(super) fn system(content: impl Into<String>) -> Self {
        Self {
            role: "system".into(),
            content: content.into(),
            ..Default::default()
        }
    }

    pub(super) fn cacheable_system(content: impl Into<String>) -> Self {
        Self {
            role: "system".into(),
            content: content.into(),
            cache_breakpoint: true,
            ..Default::default()
        }
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(super) struct ToolCall {
    pub(super) id: String,
    pub(super) name: String,
    #[serde(default)]
    pub(super) args: serde_json::Value,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(super) struct ToolResult {
    pub(super) tool_call_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub(super) content: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub(super) error: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(super) struct Attachment {
    #[serde(default)]
    pub(super) kind: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub(super) label: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub(super) name: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub(super) mime: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub(super) data: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub(super) url: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub(super) struct TokenUsage {
    #[serde(default)]
    pub(super) input_tokens: i32,
    #[serde(default)]
    pub(super) output_tokens: i32,
    #[serde(default)]
    pub(super) total_tokens: i32,
    #[serde(default)]
    pub(super) cached_input_tokens: i32,
    #[serde(default)]
    pub(super) cache_write_tokens: i32,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub(super) source: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(super) struct ToolDef {
    pub(super) name: String,
    pub(super) description: String,
    pub(super) parameters: serde_json::Value,
}

#[derive(Clone, Debug)]
pub(super) enum Chunk {
    Text { delta: String },
    Reasoning { delta: String },
    ToolCall { call: ToolCall },
    Done { usage: Option<TokenUsage> },
    Error { message: String },
}

#[derive(Clone, Debug)]
pub(super) struct Response {
    pub(super) content: String,
    pub(super) reasoning: String,
    pub(super) tool_calls: Vec<ToolCall>,
    pub(super) usage: Option<TokenUsage>,
}

impl Response {
    pub(super) fn has_tool_calls(&self) -> bool {
        !self.tool_calls.is_empty()
    }
}
