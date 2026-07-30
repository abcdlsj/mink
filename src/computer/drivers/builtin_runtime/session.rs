use serde::{Deserialize, Serialize};

use super::types::{Message, TokenUsage};

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub(super) struct Session {
    pub(super) messages: Vec<Message>,
}

impl Session {
    pub(super) fn add(&mut self, message: Message) {
        self.messages.push(message);
    }

    pub(super) fn token_usage(&self) -> TokenUsage {
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
    pub(super) fn strip_image_attachments(&mut self, add_note: bool) -> bool {
        let mut changed = false;
        for m in &mut self.messages {
            if m.role != "user" || m.attachments.is_empty() {
                continue;
            }
            let before = m.attachments.len();
            m.attachments
                .retain(|a| a.kind != "image" || (a.url.is_empty() && a.data.is_empty()));
            if m.attachments.len() < before {
                changed = true;
                if m.attachments.is_empty() {
                    m.attachments = Vec::new();
                }
                if add_note && !m.content.contains(IMAGE_UNSUPPORTED_NOTE) {
                    if !m.content.is_empty() {
                        m.content.push_str("\n\n");
                    }
                    m.content.push_str(IMAGE_UNSUPPORTED_NOTE);
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
}
