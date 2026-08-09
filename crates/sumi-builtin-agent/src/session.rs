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
    #[serde(default)]
    pub(super) split_turn: bool,
    #[serde(default)]
    pub(super) kept_tokens: usize,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(super) struct CompactionBoundary {
    pub(super) first_kept: usize,
    pub(super) turn_start: Option<usize>,
    pub(super) kept_tokens: usize,
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
        // Prefer the provider-reported input tokens of the last inference call and add an
        // estimate for messages appended after it, mirroring how pi accounts context.
        let Some(last_usage_index) = self
            .messages
            .iter()
            .rposition(|message| message.usage.is_some())
        else {
            return self
                .model_messages()
                .iter()
                .map(estimate_message)
                .sum::<usize>();
        };
        let usage = self.messages[last_usage_index]
            .usage
            .as_ref()
            .expect("usage is present at the found index");
        let trailing = self.messages[last_usage_index + 1..]
            .iter()
            .map(estimate_message)
            .sum::<usize>();
        usage.input_tokens.max(0) as usize + trailing
    }

    pub(super) fn compaction_boundary(&self, keep_recent_tokens: usize) -> CompactionBoundary {
        let compacted_through = self.compacted_through();
        let start = compacted_through.saturating_add(1);
        if start >= self.messages.len() {
            return CompactionBoundary::default();
        }
        // Valid cut points are user and assistant messages; never tool results.
        let valid_cuts = (start..self.messages.len())
            .filter(|&index| self.messages[index].role != "tool")
            .collect::<Vec<_>>();
        let Some(&first_valid) = valid_cuts.first() else {
            return CompactionBoundary::default();
        };
        // Walk backwards from the newest message, accumulating estimated tokens, and cut at
        // the nearest valid boundary once the recent-token budget is met.
        let mut accumulated = 0;
        let mut cut = first_valid;
        for index in (start..self.messages.len()).rev() {
            accumulated += estimate_message(&self.messages[index]);
            if accumulated >= keep_recent_tokens {
                cut = valid_cuts
                    .iter()
                    .copied()
                    .find(|&candidate| candidate >= index)
                    .or_else(|| {
                        // The budget was exceeded inside a tool run; cut at the closest
                        // assistant/user message before it so the tool results stay kept.
                        valid_cuts
                            .iter()
                            .copied()
                            .rev()
                            .find(|&candidate| candidate < index)
                    })
                    .unwrap_or(first_valid)
                    .max(start);
                break;
            }
        }
        let first_kept = cut.max(start);
        let kept_tokens = self.messages[first_kept..]
            .iter()
            .map(estimate_message)
            .sum();
        let turn_start = (self.messages[first_kept].role == "assistant")
            .then(|| {
                (start..first_kept)
                    .rev()
                    .find(|&index| self.messages[index].role == "user")
            })
            .flatten();
        CompactionBoundary {
            first_kept,
            turn_start,
            kept_tokens,
        }
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

    pub(super) fn slice_messages(&self, start: usize, end: usize) -> Vec<Message> {
        self.messages[start.min(self.messages.len())..end.min(self.messages.len())].to_vec()
    }

    pub(super) fn apply_compaction(
        &mut self,
        boundary: CompactionBoundary,
        summary: String,
        reason: &str,
    ) {
        let source_tokens = self.estimated_model_tokens();
        let summary_tokens = summary.len().div_ceil(4);
        let previous_through = self.compactions.last().map_or(0, |record| record.through);
        let through = boundary
            .first_kept
            .min(self.messages.len())
            .max(previous_through);
        self.compactions.push(CompactionRecord {
            reason: reason.to_owned(),
            through,
            source_tokens,
            summary_tokens,
            split_turn: boundary.turn_start.is_some(),
            kept_tokens: boundary.kept_tokens,
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

fn estimate_message(message: &Message) -> usize {
    serde_json::to_string(message)
        .map_or(0, |value| value.len())
        .div_ceil(4)
}

#[cfg(test)]
mod tests {
    use super::super::types::{ToolCall, ToolResult};
    use super::*;

    fn boundary(first_kept: usize) -> CompactionBoundary {
        CompactionBoundary {
            first_kept,
            turn_start: None,
            kept_tokens: 0,
        }
    }

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
        session.apply_compaction(
            boundary(2),
            "The old work is complete.".into(),
            "preemptive",
        );

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
        session.apply_compaction(boundary(2), "summary".into(), "preemptive");

        let first = session.compaction_boundary(1_000).first_kept;
        assert!(first > 2);
        session.apply_compaction(boundary(first), "new summary".into(), "context_limit");
        assert!(session.compaction_boundary(1_000).first_kept > first);
    }

    #[test]
    fn compaction_boundary_uses_token_budget_and_never_cuts_tool_results() {
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

        let boundary = session.compaction_boundary(10);
        assert!(boundary.first_kept > 0);
        assert_ne!(session.messages[boundary.first_kept].role, "tool");
        assert!(boundary.kept_tokens >= 10);
    }

    #[test]
    fn compaction_boundary_detects_split_turn_and_keeps_tool_results() {
        let mut session = Session::default();
        session.add(Message::user("request 0"));
        session.add(Message {
            role: "assistant".into(),
            content: "response 0".into(),
            ..Default::default()
        });
        session.add(Message::user("request 1"));
        session.add(Message {
            role: "assistant".into(),
            content: "response 1".into(),
            ..Default::default()
        });
        session.add(Message {
            role: "assistant".into(),
            content: String::new(),
            tool_calls: vec![ToolCall {
                id: "call_1".into(),
                name: "read".into(),
                args: serde_json::json!({"path": "workspace/a.txt"}),
            }],
            ..Default::default()
        });
        for index in 0..12 {
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

        // A tiny budget forces the cut into the last turn at an assistant message; the
        // turn prefix (user + earlier assistant) is reported for a separate summary.
        let boundary = session.compaction_boundary(1);
        assert_eq!(session.messages[boundary.first_kept].role, "assistant");
        assert_eq!(boundary.turn_start, Some(2));
        assert_eq!(boundary.first_kept, 4);
    }

    #[test]
    fn estimated_model_tokens_prefers_provider_usage_and_estimates_trailing() {
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

        let estimated = session.estimated_model_tokens();
        assert!(estimated > 10_000);
        assert!(estimated < 10_200);
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
        session.apply_compaction(boundary(1), "first summary".into(), "preemptive");
        session.add(Message::user("new request"));
        session.add(Message {
            role: "assistant".into(),
            content: "new response".into(),
            ..Default::default()
        });
        session.apply_compaction(boundary(3), "second summary".into(), "context_limit");

        let encoded = serde_json::to_vec(&session).unwrap();
        let session: Session = serde_json::from_slice(&encoded).unwrap();
        assert_eq!(session.compactions.len(), 2);
        assert_eq!(session.compactions[0].reason, "preemptive");
        assert_eq!(session.compactions[1].reason, "context_limit");
        assert!(session.compactions[1].through >= session.compactions[0].through);
        for record in &session.compactions {
            assert!(record.source_tokens > record.summary_tokens);
        }
        assert!(!session.compactions[0].split_turn);
    }
}
