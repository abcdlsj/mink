use std::time::Duration;

use anyhow::{Context, Result, bail};
use async_trait::async_trait;
use futures_util::StreamExt;
use reqwest::Client;
use serde::{Deserialize, Serialize};
use tokio::sync::mpsc;

use crate::agent_core::types::{Chunk, Message, TokenUsage, ToolCall, ToolDef};

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ProviderConfig {
    pub provider: String,
    pub api_key: String,
    #[serde(default)]
    pub base_url: Option<String>,
    pub model: String,
    #[serde(default)]
    pub max_tokens: Option<u32>,
    #[serde(default)]
    pub temperature: Option<f32>,
}

impl ProviderConfig {
    pub fn openai(api_key: impl Into<String>, model: String) -> Self {
        Self {
            provider: "openai".into(),
            api_key: api_key.into(),
            base_url: None,
            model,
            max_tokens: None,
            temperature: None,
        }
    }

    pub fn with_base_url(mut self, url: String) -> Self {
        self.base_url = Some(url);
        self
    }

    pub fn with_max_tokens(mut self, n: u32) -> Self {
        self.max_tokens = Some(n);
        self
    }

    pub fn with_temperature(mut self, t: f32) -> Self {
        self.temperature = Some(t);
        self
    }
}

#[async_trait]
pub trait Provider: Send + Sync {
    async fn chat_stream(
        &self,
        messages: &[Message],
        tools: &[ToolDef],
    ) -> Result<mpsc::Receiver<Chunk>>;
}

pub fn create_provider(config: ProviderConfig) -> Result<Box<dyn Provider>> {
    match config.provider.as_str() {
        "openai" => Ok(Box::new(OpenAiProvider::new(config)?)),
        other => bail!("unknown provider: {other}"),
    }
}

pub struct OpenAiProvider {
    client: Client,
    config: ProviderConfig,
    chat_url: String,
}

impl OpenAiProvider {
    pub fn new(config: ProviderConfig) -> Result<Self> {
        let client = Client::builder()
            .timeout(Duration::from_secs(300))
            .connect_timeout(Duration::from_secs(10))
            .build()
            .context("failed to create HTTP client")?;
        let base = config
            .base_url
            .as_deref()
            .unwrap_or("https://api.openai.com/v1")
            .trim_end_matches('/')
            .to_owned();
        let chat_url = format!("{base}/chat/completions");
        Ok(Self {
            client,
            config,
            chat_url,
        })
    }
}

#[async_trait]
impl Provider for OpenAiProvider {
    async fn chat_stream(
        &self,
        messages: &[Message],
        tools: &[ToolDef],
    ) -> Result<mpsc::Receiver<Chunk>> {
        let body = build_openai_request(messages, tools, &self.config);
        let response = self
            .client
            .post(&self.chat_url)
            .header("Authorization", format!("Bearer {}", self.config.api_key))
            .header("Content-Type", "application/json")
            .header("Accept", "text/event-stream")
            .json(&body)
            .send()
            .await
            .context("failed to call chat completions")?;

        if !response.status().is_success() {
            let status = response.status();
            let text = response
                .text()
                .await
                .unwrap_or_else(|e| format!("(failed to read body: {e})"));
            bail!("chat API error ({status}): {text}");
        }

        let (tx, rx) = mpsc::channel(64);
        tokio::spawn(async move {
            if let Err(e) = parse_openai_stream(response, &tx).await {
                let _ = tx
                    .send(Chunk::Error {
                        message: format!("stream parse error: {e}"),
                    })
                    .await;
            }
        });
        Ok(rx)
    }
}

fn build_openai_request(
    messages: &[Message],
    tools: &[ToolDef],
    config: &ProviderConfig,
) -> serde_json::Value {
    let msgs: Vec<serde_json::Value> = messages
        .iter()
        .flat_map(|m| {
            let mut result: Vec<serde_json::Value> = Vec::new();

            if !m.tool_results.is_empty() {
                for tr in &m.tool_results {
                    let content = if !tr.error.is_empty() {
                        format!(
                            "error: {}{}",
                            tr.error,
                            if tr.content.is_empty() {
                                String::new()
                            } else {
                                format!("\n{}", tr.content)
                            }
                        )
                    } else if !tr.content.is_empty() {
                        tr.content.clone()
                    } else {
                        "(no output)".into()
                    };
                    result.push(serde_json::json!({
                        "role": "tool",
                        "tool_call_id": tr.tool_call_id,
                        "content": content
                    }));
                }
                return result;
            }

            let mut obj = serde_json::json!({ "role": m.role });
            if !m.content.is_empty() {
                obj["content"] = serde_json::Value::String(m.content.clone());
            }
            if !m.tool_calls.is_empty() {
                obj["tool_calls"] = m
                    .tool_calls
                    .iter()
                    .map(|tc| {
                        serde_json::json!({
                            "id": tc.id,
                            "type": "function",
                            "function": {
                                "name": tc.name,
                                "arguments": tc.args.to_string()
                            }
                        })
                    })
                    .collect();
                if m.content.is_empty() {
                    obj["content"] = serde_json::Value::String(" ".into());
                }
            }
            if !m.reasoning.is_empty() {
                obj["reasoning_content"] = serde_json::Value::String(m.reasoning.clone());
            }
            result.push(obj);
            result
        })
        .collect();

    let mut request = serde_json::json!({
        "model": config.model,
        "messages": msgs,
        "stream": true,
        "stream_options": { "include_usage": true }
    });

    if !tools.is_empty() {
        let tool_defs: Vec<serde_json::Value> = tools
            .iter()
            .map(|t| {
                serde_json::json!({
                    "type": "function",
                    "function": {
                        "name": t.name,
                        "description": t.description,
                        "parameters": t.parameters
                    }
                })
            })
            .collect();
        request["tools"] = tool_defs.into();
    }

    if let Some(max_tokens) = config.max_tokens {
        request["max_tokens"] = max_tokens.into();
    }
    if let Some(temperature) = config.temperature {
        request["temperature"] = temperature.into();
    }

    request
}

async fn parse_openai_stream(response: reqwest::Response, tx: &mpsc::Sender<Chunk>) -> Result<()> {
    let mut stream = response.bytes_stream();
    let mut buf = String::new();
    let mut current_tool_call: Option<ToolCall> = None;
    let mut done_sent = false;

    while let Some(chunk_result) = stream.next().await {
        let chunk = chunk_result.context("failed to read stream chunk")?;
        let chunk_str = String::from_utf8_lossy(&chunk);
        buf.push_str(&chunk_str);

        while let Some(newline) = buf.find('\n') {
            let line = buf[..newline].trim().to_owned();
            buf = buf[newline + 1..].to_owned();

            let line = line.trim();
            if line.is_empty() {
                if let Some(tc) = current_tool_call.take()
                    && !tc.name.is_empty()
                    && tx.send(Chunk::ToolCall { call: tc }).await.is_err()
                {
                    return Ok(());
                }
                continue;
            }
            if line == "data: [DONE]" {
                if !done_sent {
                    let _ = tx.send(Chunk::Done { usage: None }).await;
                }
                return Ok(());
            }
            let Some(data) = line.strip_prefix("data: ") else {
                continue;
            };
            let Ok(parsed) = serde_json::from_str::<serde_json::Value>(data) else {
                continue;
            };

            if let Some(usage) = parsed.get("usage").filter(|v| !v.is_null()) {
                let usage = TokenUsage {
                    input_tokens: usage["prompt_tokens"].as_i64().unwrap_or(0) as i32,
                    output_tokens: usage["completion_tokens"].as_i64().unwrap_or(0) as i32,
                    total_tokens: usage["total_tokens"].as_i64().unwrap_or(0) as i32,
                    source: String::new(),
                };
                let _ = tx.send(Chunk::Done { usage: Some(usage) }).await;
                done_sent = true;
                continue;
            }

            let Some(choices) = parsed["choices"].as_array() else {
                continue;
            };
            if choices.is_empty()
                || choices[0].get("finish_reason").and_then(|v| v.as_str()) == Some("stop")
            {
                if !done_sent {
                    let _ = tx.send(Chunk::Done { usage: None }).await;
                    done_sent = true;
                }
                continue;
            }
            let delta = &choices[0]["delta"];

            if let Some(content) = delta["content"].as_str()
                && !content.is_empty()
                && tx
                    .send(Chunk::Text {
                        delta: content.to_owned(),
                    })
                    .await
                    .is_err()
            {
                return Ok(());
            }

            if let Some(reasoning) = delta["reasoning_content"].as_str()
                && !reasoning.is_empty()
                && tx
                    .send(Chunk::Reasoning {
                        delta: reasoning.to_owned(),
                    })
                    .await
                    .is_err()
            {
                return Ok(());
            }

            if let Some(tool_calls) = delta["tool_calls"].as_array() {
                for tc_delta in tool_calls {
                    let id = tc_delta["id"].as_str().unwrap_or("");
                    let function = &tc_delta["function"];
                    let name_delta = function["name"].as_str().unwrap_or("");
                    let args_delta = function["arguments"].as_str().unwrap_or("");

                    if current_tool_call.is_none() {
                        current_tool_call = Some(ToolCall {
                            id: id.to_owned(),
                            name: String::new(),
                            args: serde_json::Value::Null,
                        });
                    }

                    if let Some(ref mut tc) = current_tool_call {
                        if !id.is_empty() {
                            tc.id = id.to_owned();
                        }
                        if !name_delta.is_empty() {
                            tc.name.push_str(name_delta);
                        }
                        if !args_delta.is_empty() {
                            if tc.args.is_null() {
                                tc.args = serde_json::Value::String(args_delta.to_owned());
                            } else if let serde_json::Value::String(ref mut s) = tc.args {
                                s.push_str(args_delta);
                            }
                        }
                    }
                }
            }
        }
    }

    if let Some(tc) = current_tool_call.take()
        && !tc.name.is_empty()
    {
        let args = if let serde_json::Value::String(ref s) = tc.args {
            serde_json::from_str(s).unwrap_or(serde_json::Value::Null)
        } else {
            serde_json::Value::Null
        };
        let _ = tx
            .send(Chunk::ToolCall {
                call: ToolCall { args, ..tc },
            })
            .await;
    }

    if !done_sent {
        let _ = tx.send(Chunk::Done { usage: None }).await;
    }
    Ok(())
}
