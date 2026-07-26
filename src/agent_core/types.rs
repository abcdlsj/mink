use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Deserialize, Serialize, Default)]
pub struct Message {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub id: String,
    pub role: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub content: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub attachments: Vec<Attachment>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub reasoning: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub tool_calls: Vec<ToolCall>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub tool_results: Vec<ToolResult>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub usage: Option<TokenUsage>,
    #[serde(skip)]
    pub cache_breakpoint: bool,
}

impl Message {
    pub fn user(content: impl Into<String>) -> Self {
        Self {
            role: "user".into(),
            content: content.into(),
            ..Default::default()
        }
    }

    pub fn assistant(content: impl Into<String>) -> Self {
        Self {
            role: "assistant".into(),
            content: content.into(),
            ..Default::default()
        }
    }

    pub fn tool(results: Vec<ToolResult>) -> Self {
        Self {
            role: "tool".into(),
            tool_results: results,
            ..Default::default()
        }
    }

    pub fn system(content: impl Into<String>) -> Self {
        Self {
            role: "system".into(),
            content: content.into(),
            ..Default::default()
        }
    }

    pub fn cacheable_system(content: impl Into<String>) -> Self {
        Self {
            role: "system".into(),
            content: content.into(),
            cache_breakpoint: true,
            ..Default::default()
        }
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ToolCall {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub args: serde_json::Value,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ToolResult {
    pub tool_call_id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub content: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub error: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct Attachment {
    #[serde(default)]
    pub kind: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub label: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub name: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub mime: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub data: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub url: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub struct TokenUsage {
    #[serde(default)]
    pub input_tokens: i32,
    #[serde(default)]
    pub output_tokens: i32,
    #[serde(default)]
    pub total_tokens: i32,
    #[serde(default)]
    pub cached_input_tokens: i32,
    #[serde(default)]
    pub cache_write_tokens: i32,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub source: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ToolDef {
    pub name: String,
    pub description: String,
    pub parameters: serde_json::Value,
}

#[derive(Clone, Debug)]
pub enum Chunk {
    Text { delta: String },
    Reasoning { delta: String },
    ToolCall { call: ToolCall },
    Done { usage: Option<TokenUsage> },
    Error { message: String },
}

#[derive(Clone, Debug)]
pub struct Response {
    pub content: String,
    pub reasoning: String,
    pub tool_calls: Vec<ToolCall>,
    pub usage: Option<TokenUsage>,
}

impl Response {
    pub fn has_tool_calls(&self) -> bool {
        !self.tool_calls.is_empty()
    }
}
