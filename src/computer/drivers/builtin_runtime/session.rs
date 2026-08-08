use serde::{Deserialize, Serialize};

use super::types::{Message, TokenUsage};

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub(super) struct Session {
    pub(super) messages: Vec<Message>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub(super) compactions: Vec<CompactionRecord>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    compaction: Option<Compaction>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(super) struct CompactionRecord {
    pub(super) reason: String,
    pub(super) through: usize,
    pub(super) source_tokens: usize,
    pub(super) summary_tokens: usize,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
struct Compaction {
    through: usize,
    summary: String,
}

impl Session {
    pub(super) fn add(&mut self, message: Message) {
        self.messages.push(message);
    }

    pub(super) fn model_messages(&self) -> Vec<Message> {
        let mut messages = Vec::new();
        if let Some(compaction) = &self.compaction {
            messages.push(Message::system(format!(
                "Previous conversation summary (provider context only):\n{}",
                compaction.summary
            )));
        }
        let start = self
            .compaction
            .as_ref()
            .map_or(0, |compaction| compaction.through.min(self.messages.len()));
        messages.extend(self.messages[start..].iter().cloned());
        messages
    }

    pub(super) fn estimated_model_tokens(&self) -> usize {
        self.model_messages()
            .iter()
            .map(|message| serde_json::to_string(message).map_or(0, |value| value.len()))
            .sum::<usize>()
            .div_ceil(4)
    }

    pub(super) fn compaction_boundary(&self, recent_messages: usize) -> usize {
        let target = self.messages.len().saturating_sub(recent_messages);
        let compacted_through = self.compacted_through();
        let start = target.max(compacted_through.saturating_add(1));
        (start..self.messages.len())
            .find(|&index| self.messages[index].role == "user")
            .or_else(|| {
                // A long tool loop can push the recent tail past every user message.
                // Falling back to the last user boundary keeps compaction moving instead
                // of stalling the whole mechanism; mid-turn history stays intact.
                (compacted_through.saturating_add(1)..self.messages.len())
                    .rev()
                    .find(|&index| self.messages[index].role == "user")
            })
            .unwrap_or(0)
    }

    pub(super) fn compaction_source(&self, through: usize) -> Vec<Message> {
        let start = self
            .compaction
            .as_ref()
            .map_or(0, |compaction| compaction.through.min(through));
        let mut source = Vec::new();
        if let Some(compaction) = &self.compaction {
            source.push(Message::system(format!(
                "Existing summary of earlier conversation:\n{}",
                compaction.summary
            )));
        }
        source.extend(
            self.messages[start..through.min(self.messages.len())]
                .iter()
                .cloned(),
        );
        source
    }

    pub(super) fn apply_compaction(&mut self, through: usize, summary: String, reason: &str) {
        let source_tokens = self.estimated_model_tokens();
        let summary_tokens = summary.len().div_ceil(4);
        let previous_through = self.compactions.last().map_or(0, |record| record.through);
        let through = through.min(self.messages.len()).max(previous_through);
        self.compactions.push(CompactionRecord {
            reason: reason.to_owned(),
            through,
            source_tokens,
            summary_tokens,
        });
        self.compaction = Some(Compaction { through, summary });
    }

    pub(super) fn compacted_through(&self) -> usize {
        self.compaction
            .as_ref()
            .map_or(0, |compaction| compaction.through.min(self.messages.len()))
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
    use super::super::types::ToolResult;
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

    #[test]
    fn compaction_keeps_append_only_history_and_projects_summary_with_recent_tail() {
        let mut session = Session::default();
        session.add(Message::user("old request"));
        session.add(Message {
            role: "assistant".into(),
            content: "old response".into(),
            ..Default::default()
        });
        session.add(Message::user("recent request"));
        session.apply_compaction(2, "The old work is complete.".into(), "preemptive");

        let encoded = serde_json::to_vec(&session).unwrap();
        let session: Session = serde_json::from_slice(&encoded).unwrap();

        assert_eq!(session.messages.len(), 3);
        let projected = session.model_messages();
        assert_eq!(projected[0].role, "system");
        assert!(projected[0].content.contains("The old work is complete."));
        assert_eq!(projected[1].content, "recent request");
    }

    #[test]
    fn compaction_boundary_must_advance_past_the_existing_summary() {
        let mut session = Session::default();
        for index in 0..7 {
            session.add(Message::user(format!("request {index}")));
            session.add(Message {
                role: "assistant".into(),
                content: format!("response {index}"),
                ..Default::default()
            });
        }
        session.apply_compaction(2, "summary".into(), "preemptive");

        assert_eq!(session.compaction_boundary(12), 4);
        session.apply_compaction(4, "new summary".into(), "context_limit");
        assert_eq!(session.compaction_boundary(12), 6);
    }

    #[test]
    fn compaction_boundary_falls_back_to_the_last_user_when_the_tail_has_none() {
        let mut session = Session::default();
        for index in 0..3 {
            session.add(Message::user(format!("request {index}")));
            session.add(Message {
                role: "assistant".into(),
                content: format!("response {index}"),
                ..Default::default()
            });
        }
        for index in 0..20 {
            session.add(Message {
                role: "tool".into(),
                tool_results: vec![ToolResult {
                    tool_call_id: format!("call_{index}"),
                    content: "output".into(),
                    error: String::new(),
                }],
                ..Default::default()
            });
        }

        // The last 12 messages are tool results with no user message; the boundary must
        // fall back to the last user message (request 2 at index 4) instead of returning 0.
        assert_eq!(session.compaction_boundary(12), 4);
    }

    #[test]
    fn old_session_without_compaction_records_loads() {
        let session: Session = serde_json::from_str(r#"{"messages":[]}"#).unwrap();
        assert!(session.compactions.is_empty());
    }

    #[test]
    fn compaction_records_are_append_only_monotonic_and_compressive() {
        let mut session = Session::default();
        session.add(Message::user("old request"));
        session.add(Message {
            role: "assistant".into(),
            content: "old response".into(),
            ..Default::default()
        });
        session.apply_compaction(1, "first summary".into(), "preemptive");
        session.add(Message::user("new request"));
        session.add(Message {
            role: "assistant".into(),
            content: "new response".into(),
            ..Default::default()
        });
        session.apply_compaction(3, "second summary".into(), "context_limit");

        let encoded = serde_json::to_vec(&session).unwrap();
        let session: Session = serde_json::from_slice(&encoded).unwrap();
        assert_eq!(session.compactions.len(), 2);
        assert_eq!(session.compactions[0].reason, "preemptive");
        assert_eq!(session.compactions[1].reason, "context_limit");
        assert!(session.compactions[1].through >= session.compactions[0].through);
        for record in &session.compactions {
            assert!(record.source_tokens > record.summary_tokens);
        }
    }
}
