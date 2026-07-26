use std::{collections::BTreeMap, time::Duration};

use anyhow::{Context, Result, bail};
use async_trait::async_trait;
use futures_util::StreamExt;
use reqwest::Client;
use tokio::sync::mpsc;

use crate::agent_core::types::{Chunk, Message, TokenUsage, ToolCall, ToolDef};

#[derive(Clone)]
pub struct ProviderConfig {
    pub provider: String,
    pub api_key: String,
    pub base_url: Option<String>,
    pub model: String,
    pub max_tokens: Option<u32>,
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
    let mut buffer = Vec::new();
    let mut state = OpenAiStreamState::default();

    while let Some(chunk_result) = stream.next().await {
        let chunk = chunk_result.context("failed to read stream chunk")?;
        buffer.extend_from_slice(&chunk);
        while let Some(newline) = buffer.iter().position(|byte| *byte == b'\n') {
            let line = String::from_utf8(buffer.drain(..=newline).collect())
                .context("SSE stream contains invalid UTF-8")?;
            if handle_sse_line(&line, &mut state, tx).await? {
                return Ok(());
            }
        }
    }

    if !buffer.is_empty() {
        let line = String::from_utf8(buffer).context("SSE stream contains invalid UTF-8")?;
        let _ = handle_sse_line(&line, &mut state, tx).await?;
    }
    flush_tool_calls(&mut state, tx).await?;
    send_done_once(&mut state, None, tx).await;
    Ok(())
}

#[derive(Default)]
struct OpenAiStreamState {
    tool_calls: BTreeMap<usize, PendingToolCall>,
    done_sent: bool,
}

#[derive(Default)]
struct PendingToolCall {
    id: String,
    name: String,
    arguments: String,
}

async fn handle_sse_line(
    line: &str,
    state: &mut OpenAiStreamState,
    tx: &mpsc::Sender<Chunk>,
) -> Result<bool> {
    let line = line.trim();
    if line.is_empty() || line.starts_with(':') {
        return Ok(false);
    }
    let Some(data) = line.strip_prefix("data:").map(str::trim_start) else {
        return Ok(false);
    };
    if data == "[DONE]" {
        flush_tool_calls(state, tx).await?;
        send_done_once(state, None, tx).await;
        return Ok(true);
    }

    let parsed: serde_json::Value =
        serde_json::from_str(data).context("SSE data is not valid JSON")?;
    if let Some(choice) = parsed
        .get("choices")
        .and_then(serde_json::Value::as_array)
        .and_then(|choices| choices.first())
    {
        let delta = &choice["delta"];
        if let Some(content) = delta["content"].as_str().filter(|value| !value.is_empty()) {
            let _ = tx
                .send(Chunk::Text {
                    delta: content.to_owned(),
                })
                .await;
        }
        if let Some(reasoning) = delta["reasoning_content"]
            .as_str()
            .filter(|value| !value.is_empty())
        {
            let _ = tx
                .send(Chunk::Reasoning {
                    delta: reasoning.to_owned(),
                })
                .await;
        }
        if let Some(tool_calls) = delta["tool_calls"].as_array() {
            for tool_call in tool_calls {
                let index = tool_call["index"].as_u64().unwrap_or(0) as usize;
                let pending = state.tool_calls.entry(index).or_default();
                if let Some(id) = tool_call["id"].as_str().filter(|value| !value.is_empty()) {
                    pending.id = id.to_owned();
                }
                if let Some(name) = tool_call["function"]["name"]
                    .as_str()
                    .filter(|value| !value.is_empty())
                {
                    pending.name.push_str(name);
                }
                if let Some(arguments) = tool_call["function"]["arguments"]
                    .as_str()
                    .filter(|value| !value.is_empty())
                {
                    pending.arguments.push_str(arguments);
                }
            }
        }

        if let Some(reason) = choice["finish_reason"].as_str() {
            if reason == "tool_calls" {
                flush_tool_calls(state, tx).await?;
            }
            send_done_once(state, None, tx).await;
        }
    }

    if let Some(usage) = parsed.get("usage").filter(|value| !value.is_null()) {
        let usage = TokenUsage {
            input_tokens: usage["prompt_tokens"].as_i64().unwrap_or(0) as i32,
            output_tokens: usage["completion_tokens"].as_i64().unwrap_or(0) as i32,
            total_tokens: usage["total_tokens"].as_i64().unwrap_or(0) as i32,
            source: String::new(),
        };
        let _ = tx.send(Chunk::Done { usage: Some(usage) }).await;
        state.done_sent = true;
    }
    Ok(false)
}

async fn flush_tool_calls(state: &mut OpenAiStreamState, tx: &mpsc::Sender<Chunk>) -> Result<()> {
    for (_, pending) in std::mem::take(&mut state.tool_calls) {
        ensure_tool_call(&pending)?;
        let args = if pending.arguments.trim().is_empty() {
            serde_json::json!({})
        } else {
            serde_json::from_str(&pending.arguments)
                .with_context(|| format!("tool call {} has invalid JSON arguments", pending.id))?
        };
        let _ = tx
            .send(Chunk::ToolCall {
                call: ToolCall {
                    id: pending.id,
                    name: pending.name,
                    args,
                },
            })
            .await;
    }
    Ok(())
}

fn ensure_tool_call(call: &PendingToolCall) -> Result<()> {
    if call.id.is_empty() {
        bail!("streamed tool call has no id");
    }
    if call.name.is_empty() {
        bail!("streamed tool call {} has no name", call.id);
    }
    Ok(())
}

async fn send_done_once(
    state: &mut OpenAiStreamState,
    usage: Option<TokenUsage>,
    tx: &mpsc::Sender<Chunk>,
) {
    if !state.done_sent {
        let _ = tx.send(Chunk::Done { usage }).await;
        state.done_sent = true;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn streamed_tool_calls_are_assembled_by_index_across_sse_events() {
        let (tx, mut rx) = mpsc::channel(16);
        let mut state = OpenAiStreamState::default();
        let lines = [
            r#"data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"re","arguments":"{\"path\":"}}]},"finish_reason":null}]}"#,
            "",
            r#"data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"ad","arguments":"\"MEMORY.md\"}"}},{"index":1,"id":"call-2","function":{"name":"write","arguments":"{\"path\":\"notes/x\",\"content\":\"ok\"}"}}]},"finish_reason":null}]}"#,
            r#"data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}"#,
            "data: [DONE]",
        ];
        for line in lines {
            if handle_sse_line(line, &mut state, &tx).await.unwrap() {
                break;
            }
        }
        drop(tx);

        let mut calls = Vec::new();
        while let Some(chunk) = rx.recv().await {
            if let Chunk::ToolCall { call } = chunk {
                calls.push(call);
            }
        }
        assert_eq!(calls.len(), 2);
        assert_eq!(calls[0].name, "read");
        assert_eq!(calls[0].args, serde_json::json!({"path": "MEMORY.md"}));
        assert_eq!(calls[1].name, "write");
        assert_eq!(calls[1].args["content"], "ok");
    }

    #[tokio::test]
    async fn invalid_streamed_tool_arguments_fail_before_execution() {
        let (tx, _rx) = mpsc::channel(4);
        let mut state = OpenAiStreamState::default();
        handle_sse_line(
            r#"data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"read","arguments":"{"}}]},"finish_reason":null}]}"#,
            &mut state,
            &tx,
        )
        .await
        .unwrap();
        let error = handle_sse_line(
            r#"data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}"#,
            &mut state,
            &tx,
        )
        .await
        .unwrap_err();
        assert!(error.to_string().contains("invalid JSON arguments"));
    }
}
