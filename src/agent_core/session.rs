use crate::agent_core::types::{Message, TokenUsage, ToolResult};

#[derive(Clone, Debug, Default)]
pub struct Session {
    pub messages: Vec<Message>,
}

impl Session {
    pub fn add(&mut self, message: Message) {
        self.messages.push(message);
    }

    pub fn add_usage(&mut self, usage: Option<TokenUsage>) {
        if let Some(ref usage) = usage
            && let Some(last) = self.messages.last_mut()
        {
            last.usage = Some(usage.clone());
        }
    }

    pub fn estimated_tokens(&self) -> usize {
        self.messages
            .iter()
            .map(|m| (m.content.len() + m.reasoning.len()) / 4)
            .sum()
    }

    pub fn needs_compact(&self, max_tokens: usize) -> bool {
        self.estimated_tokens() > max_tokens
    }

    pub fn compact(&mut self, summary: String, keep: usize) {
        let keep_at = self.messages.len().saturating_sub(keep);
        let tail = self.messages.split_off(keep_at);
        self.messages = vec![Message::system(format!("[Context Summary]\n{summary}"))];
        self.messages.extend(tail);
    }

    /// Strips image attachments from all user messages.
    /// Returns true if any images were removed.
    pub fn strip_image_attachments(&mut self, add_note: bool) -> bool {
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

/// Creates a tool message from a tool result.
pub fn new_tool_message(result: ToolResult) -> Message {
    Message::tool(vec![result])
}
