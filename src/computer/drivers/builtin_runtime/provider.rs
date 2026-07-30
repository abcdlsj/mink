use std::{collections::BTreeMap, pin::Pin};

use anyhow::{Context, Result, bail};
use async_openai::{Client, config::OpenAIConfig, error::OpenAIError};
use async_trait::async_trait;
use futures_util::{Stream, StreamExt};
use secrecy::{ExposeSecret, SecretString};
use tokio::sync::mpsc;

use super::types::{Chunk, Message, TokenUsage, ToolCall, ToolDef};

#[derive(Clone)]
pub(super) struct ProviderConfig {
    pub(super) api_key: SecretString,
    pub(super) base_url: Option<String>,
    pub(super) model: String,
    pub(super) prompt_cache_key: Option<String>,
}

impl ProviderConfig {
    pub(super) fn openai(api_key: impl Into<SecretString>, model: String) -> Self {
        Self {
            api_key: api_key.into(),
            base_url: None,
            model,
            prompt_cache_key: None,
        }
    }

    pub(super) fn with_base_url(mut self, url: String) -> Self {
        self.base_url = Some(url);
        self
    }

    pub(super) fn with_prompt_cache_key(mut self, key: String) -> Self {
        if !key.is_empty() {
            self.prompt_cache_key = Some(key);
        }
        self
    }

    fn supports_explicit_prompt_cache(&self) -> bool {
        self.base_url.is_none() && is_gpt_5_6_or_later(&self.model)
    }
}

fn is_gpt_5_6_or_later(model: &str) -> bool {
    let Some(version) = model.strip_prefix("gpt-") else {
        return false;
    };
    let version = version
        .split_once('-')
        .map_or(version, |(version, _)| version);
    let Some((major, minor)) = version.split_once('.') else {
        return false;
    };
    let (Ok(major), Ok(minor)) = (major.parse::<u32>(), minor.parse::<u32>()) else {
        return false;
    };
    major > 5 || (major == 5 && minor >= 6)
}

#[async_trait]
pub(super) trait Provider: Send + Sync {
    async fn chat_stream(
        &self,
        messages: &[Message],
        tools: &[ToolDef],
    ) -> Result<mpsc::Receiver<Chunk>>;
}

pub(super) struct OpenAiProvider {
    client: Client<OpenAIConfig>,
    config: ProviderConfig,
}

impl OpenAiProvider {
    pub(super) fn new(config: ProviderConfig) -> Result<Self> {
        let base = config
            .base_url
            .as_deref()
            .unwrap_or("https://api.openai.com/v1")
            .trim_end_matches('/')
            .to_owned();
        let openai = OpenAIConfig::new()
            .with_api_key(config.api_key.expose_secret())
            .with_api_base(base);
        Ok(Self {
            client: Client::with_config(openai),
            config,
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
        type ProviderStream =
            Pin<Box<dyn Stream<Item = std::result::Result<serde_json::Value, OpenAIError>> + Send>>;
        let mut response: ProviderStream = self
            .client
            .chat()
            .create_stream_byot(body)
            .await
            .context("failed to call chat completions")?;

        let (tx, rx) = mpsc::channel(64);
        tokio::spawn(async move {
            let mut state = OpenAiStreamState::default();
            while let Some(chunk) = response.next().await {
                let result = match chunk {
                    Ok(chunk) => handle_stream_chunk(chunk, &mut state, &tx).await,
                    Err(error) => Err(anyhow::Error::new(error)),
                };
                if let Err(error) = result {
                    let _ = tx
                        .send(Chunk::Error {
                            message: error.to_string(),
                        })
                        .await;
                    return;
                }
            }
            if let Err(error) = flush_tool_calls(&mut state, &tx).await {
                let _ = tx
                    .send(Chunk::Error {
                        message: error.to_string(),
                    })
                    .await;
                return;
            }
            let _ = tx.send(Chunk::Done { usage: state.usage }).await;
        });
        Ok(rx)
    }
}

fn build_openai_request(
    messages: &[Message],
    tools: &[ToolDef],
    config: &ProviderConfig,
) -> serde_json::Value {
    let explicit_prompt_cache = config.supports_explicit_prompt_cache();
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
                obj["content"] = if explicit_prompt_cache && m.cache_breakpoint {
                    serde_json::json!([{
                        "type": "text",
                        "text": m.content,
                        "prompt_cache_breakpoint": { "mode": "explicit" }
                    }])
                } else {
                    serde_json::Value::String(m.content.clone())
                };
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

    if explicit_prompt_cache && let Some(key) = &config.prompt_cache_key {
        request["prompt_cache_key"] = key.clone().into();
        request["prompt_cache_options"] = serde_json::json!({
            "mode": "implicit",
            "ttl": "30m"
        });
    }

    request
}

#[derive(Default)]
struct OpenAiStreamState {
    tool_calls: BTreeMap<usize, PendingToolCall>,
    usage: Option<TokenUsage>,
}

#[derive(Default)]
struct PendingToolCall {
    id: String,
    name: String,
    arguments: String,
}

async fn handle_stream_chunk(
    parsed: serde_json::Value,
    state: &mut OpenAiStreamState,
    tx: &mpsc::Sender<Chunk>,
) -> Result<()> {
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

        if choice["finish_reason"].as_str() == Some("tool_calls") {
            flush_tool_calls(state, tx).await?;
        }
    }

    if let Some(usage) = parsed.get("usage").filter(|value| !value.is_null()) {
        state.usage = Some(TokenUsage {
            input_tokens: usage["prompt_tokens"].as_i64().unwrap_or(0) as i32,
            output_tokens: usage["completion_tokens"].as_i64().unwrap_or(0) as i32,
            total_tokens: usage["total_tokens"].as_i64().unwrap_or(0) as i32,
            cached_input_tokens: usage["prompt_tokens_details"]["cached_tokens"]
                .as_i64()
                .unwrap_or(0) as i32,
            cache_write_tokens: usage["prompt_tokens_details"]["cache_write_tokens"]
                .as_i64()
                .unwrap_or(0) as i32,
            source: "openai_chat_completions".to_owned(),
        });
    }
    Ok(())
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn official_gpt_5_6_request_marks_only_stable_prompt_prefixes() {
        let config = ProviderConfig::openai("test-key", "gpt-5.6".into())
            .with_prompt_cache_key("sumi-v1-agent".into());
        let messages = vec![
            Message::cacheable_system("global"),
            Message::cacheable_system("agent"),
            Message::system("dynamic"),
            Message::user("run"),
        ];

        let request = build_openai_request(&messages, &[], &config);

        assert_eq!(request["prompt_cache_key"], "sumi-v1-agent");
        assert_eq!(request["prompt_cache_options"]["mode"], "implicit");
        assert_eq!(request["prompt_cache_options"]["ttl"], "30m");
        assert_eq!(
            request["messages"][0]["content"][0]["prompt_cache_breakpoint"]["mode"],
            "explicit"
        );
        assert_eq!(
            request["messages"][1]["content"][0]["prompt_cache_breakpoint"]["mode"],
            "explicit"
        );
        assert_eq!(request["messages"][2]["content"], "dynamic");
        assert_eq!(request["messages"][3]["content"], "run");
    }

    #[test]
    fn older_models_and_compatible_endpoints_do_not_get_explicit_cache_fields() {
        for config in [
            ProviderConfig::openai("test-key", "gpt-4o".into())
                .with_prompt_cache_key("cache".into()),
            ProviderConfig::openai("test-key", "gpt-5.6".into())
                .with_base_url("https://compatible.example/v1".into())
                .with_prompt_cache_key("cache".into()),
        ] {
            let request =
                build_openai_request(&[Message::cacheable_system("stable")], &[], &config);

            assert!(request.get("prompt_cache_key").is_none());
            assert!(request.get("prompt_cache_options").is_none());
            assert_eq!(request["messages"][0]["content"], "stable");
        }
    }

    #[tokio::test]
    async fn usage_includes_prompt_cache_reads_and_writes() {
        let (tx, _rx) = mpsc::channel(4);
        let mut state = OpenAiStreamState::default();
        handle_stream_chunk(
            serde_json::json!({"usage":{"prompt_tokens":2006,"completion_tokens":300,"total_tokens":2306,"prompt_tokens_details":{"cached_tokens":1920,"cache_write_tokens":64}}}),
            &mut state,
            &tx,
        )
        .await
        .unwrap();

        let usage = state.usage.expect("usage");
        assert_eq!(usage.input_tokens, 2006);
        assert_eq!(usage.cached_input_tokens, 1920);
        assert_eq!(usage.cache_write_tokens, 64);
        assert_eq!(usage.source, "openai_chat_completions");
    }

    #[tokio::test]
    async fn streamed_tool_calls_are_assembled_by_index_across_sse_events() {
        let (tx, mut rx) = mpsc::channel(16);
        let mut state = OpenAiStreamState::default();
        let chunks = [
            serde_json::json!({"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"re","arguments":"{\"path\":"}}]},"finish_reason":null}]}),
            serde_json::json!({"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"ad","arguments":"\"MEMORY.md\"}"}},{"index":1,"id":"call-2","function":{"name":"write","arguments":"{\"path\":\"notes/x\",\"content\":\"ok\"}"}}]},"finish_reason":null}]}),
            serde_json::json!({"choices":[{"delta":{},"finish_reason":"tool_calls"}]}),
        ];
        for chunk in chunks {
            handle_stream_chunk(chunk, &mut state, &tx).await.unwrap();
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
        handle_stream_chunk(
            serde_json::json!({"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"read","arguments":"{"}}]},"finish_reason":null}]}),
            &mut state,
            &tx,
        )
        .await
        .unwrap();
        let error = handle_stream_chunk(
            serde_json::json!({"choices":[{"delta":{},"finish_reason":"tool_calls"}]}),
            &mut state,
            &tx,
        )
        .await
        .unwrap_err();
        assert!(error.to_string().contains("invalid JSON arguments"));
    }
}
