use serde::{Deserialize, Serialize};
use serde_json::{Value, json};

use super::types::{Message, TokenUsage};

/// Append-only provider transcript plus embedding-owned metadata.
///
/// The transcript is the source of truth for what was sent to the provider.
/// Embeddings (harnesses) store their own projection/compaction state in
/// `metadata`; the core runtime never interprets it.
#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub struct Session {
    pub messages: Vec<Message>,
    #[serde(default = "default_metadata")]
    pub metadata: Value,
}

fn default_metadata() -> Value {
    json!({})
}

impl Session {
    pub fn add(&mut self, message: Message) {
        self.messages.push(message);
    }

    pub fn messages(&self) -> &[Message] {
        &self.messages
    }

    pub fn metadata(&self) -> &Value {
        &self.metadata
    }

    pub fn metadata_mut(&mut self) -> &mut Value {
        &mut self.metadata
    }

    /// Estimate the current provider context size in tokens.
    ///
    /// Prefers the provider-reported input tokens of the last inference call and adds an
    /// estimate for messages appended after it; falls back to chars/4 when no usage exists.
    pub fn estimate_tokens(&self) -> usize {
        let Some(last_usage_index) = self
            .messages
            .iter()
            .rposition(|message| message.usage.is_some())
        else {
            return super::context::estimate_messages(&self.messages);
        };
        let usage = self.messages[last_usage_index]
            .usage
            .as_ref()
            .expect("usage is present at the found index");
        let trailing = super::context::estimate_messages(&self.messages[last_usage_index + 1..]);
        usage.input_tokens.max(0) as usize + trailing
    }

    pub fn token_usage(&self) -> TokenUsage {
        self.messages
            .iter()
            .filter_map(|message| message.usage.as_ref())
            .fold(TokenUsage::default(), |mut total, usage| {
                total.input_tokens += usage.input_tokens;
                total.output_tokens += usage.output_tokens;
                total.total_tokens += usage.total_tokens;
                total.cached_input_tokens += usage.cached_input_tokens;
                total.cache_write_tokens += usage.cache_write_tokens;
                total
            })
    }

    /// Strips image attachments from all user messages.
    /// Returns true if any images were removed.
    pub fn strip_image_attachments(&mut self, add_note: bool) -> bool {
        let mut changed = false;
        for message in &mut self.messages {
            if message.role != "user" || message.attachments.is_empty() {
                continue;
            }
            let before = message.attachments.len();
            message.attachments.retain(|attachment| {
                attachment.kind != "image"
                    || (attachment.url.is_empty() && attachment.data.is_empty())
            });
            if message.attachments.len() < before {
                changed = true;
                if message.attachments.is_empty() {
                    message.attachments = Vec::new();
                }
                if add_note && !message.content.contains(IMAGE_UNSUPPORTED_NOTE) {
                    if !message.content.is_empty() {
                        message.content.push_str("\n\n");
                    }
                    message.content.push_str(IMAGE_UNSUPPORTED_NOTE);
                }
            }
        }
        changed
    }
}

const IMAGE_UNSUPPORTED_NOTE: &str = "[System note: Image payloads were omitted because the current model endpoint does not support image input. Do not infer visual details; tell the user you cannot inspect the image and ask for a text description or a vision-capable model.]";

#[cfg(test)]
mod tests {
    use super::*;
    use crate::types::{TokenUsage, ToolResult};

    #[test]
    fn token_usage_aggregates_cache_metrics() {
        let mut session = Session::default();
        for (input, cached, written) in [(2000, 1500, 300), (2500, 2200, 0)] {
            session.add(Message {
                role: "assistant".into(),
                usage: Some(TokenUsage {
                    input_tokens: input,
                    output_tokens: 100,
                    total_tokens: input + 100,
                    cached_input_tokens: cached,
                    cache_write_tokens: written,
                    source: "openai_chat_completions".into(),
                }),
                ..Default::default()
            });
        }

        let usage = session.token_usage();
        assert_eq!(usage.input_tokens, 4500);
        assert_eq!(usage.output_tokens, 200);
        assert_eq!(usage.cached_input_tokens, 3700);
        assert_eq!(usage.cache_write_tokens, 300);
    }

    #[test]
    fn metadata_round_trips_and_keeps_embedding_state() {
        let mut session = Session::default();
        session.add(Message::user("hello"));
        session.metadata_mut()["compaction"] = json!({"through": 1, "summary": "summary"});

        let restored: Session =
            serde_json::from_slice(&serde_json::to_vec(&session).unwrap()).unwrap();
        assert_eq!(restored.messages.len(), 1);
        assert_eq!(restored.metadata["compaction"]["through"], 1);
        assert_eq!(restored.metadata["compaction"]["summary"], "summary");
    }

    #[test]
    fn estimate_tokens_prefers_provider_usage_and_estimates_trailing() {
        let mut session = Session::default();
        session.add(Message {
            role: "assistant".into(),
            content: "response".into(),
            usage: Some(TokenUsage {
                input_tokens: 10_000,
                output_tokens: 10,
                total_tokens: 10_010,
                cached_input_tokens: 0,
                cache_write_tokens: 0,
                source: "openai_chat_completions".into(),
            }),
            ..Default::default()
        });
        session.add(Message {
            role: "tool".into(),
            tool_results: vec![ToolResult {
                tool_call_id: "call_1".into(),
                content: "x".repeat(400),
                error: String::new(),
            }],
            ..Default::default()
        });

        let estimated = session.estimate_tokens();
        assert!(estimated > 10_000);
        assert!(estimated < 10_200);
    }
}
