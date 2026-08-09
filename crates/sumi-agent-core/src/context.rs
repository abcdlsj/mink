//! Generic context utilities and the projection strategy hook.
//!
//! The runtime owns the transcript and the provider loop but not the context
//! policy: embeddings implement `ContextStrategy` to decide how the transcript
//! is projected into provider messages and whether/when it is compacted. The
//! shared algorithms (token estimation, cut points, file-operation extraction)
//! live here so harnesses do not duplicate them.

use anyhow::Result;
use async_trait::async_trait;
use serde_json::Value;

use crate::{session::Session, types::Message};

/// Projects and prepares the transcript for one provider call.
#[async_trait]
pub trait ContextStrategy: Send + Sync + std::fmt::Debug {
    /// Messages appended to the per-turn system messages before the transcript.
    fn project(&self, session: &Session) -> Vec<Message>;

    /// Called before a turn and again before a context-limit retry. The embedding
    /// decides whether to compact the session and may persist its state in
    /// `session.metadata`. `reason` is `preemptive` or `context_limit`.
    async fn prepare_turn(
        &self,
        session: &mut Session,
        reason: &str,
        summarizer: &dyn Summarizer,
    ) -> Result<()>;
}

/// Runs a completion with no tools, used by embeddings to generate summaries.
#[async_trait]
pub trait Summarizer: Send + Sync {
    async fn summarize(&self, messages: &[Message]) -> Result<String>;
}

/// Default strategy: raw transcript, never compacts.
#[derive(Clone, Copy, Debug, Default)]
pub struct IdentityContext;

#[async_trait]
impl ContextStrategy for IdentityContext {
    fn project(&self, session: &Session) -> Vec<Message> {
        session.messages().to_vec()
    }

    async fn prepare_turn(
        &self,
        _session: &mut Session,
        _reason: &str,
        _summarizer: &dyn Summarizer,
    ) -> Result<()> {
        Ok(())
    }
}

/// Where a token-budget compaction should cut the transcript.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct CutPoint {
    /// Index of the first message kept after compaction.
    pub first_kept: usize,
    /// User message starting the turn that is split, if the cut is mid-turn.
    pub turn_start: Option<usize>,
    /// Estimated tokens of the kept tail.
    pub kept_tokens: usize,
}

/// Rough token estimate using the chars/4 heuristic.
pub fn estimate_messages(messages: &[Message]) -> usize {
    messages
        .iter()
        .map(|message| {
            serde_json::to_string(message)
                .map_or(0, |value| value.len())
                .div_ceil(4)
        })
        .sum()
}

/// Find the cut point that keeps approximately `keep_recent_tokens` of the tail.
///
/// Valid cut points are user and assistant messages; tool results are never cut.
/// When the budget is exceeded inside a tool run, the closest assistant/user
/// message before it is chosen so the tool results stay in the kept tail.
pub fn find_cut_point(
    messages: &[Message],
    compacted_through: usize,
    keep_recent_tokens: usize,
) -> CutPoint {
    let start = compacted_through.saturating_add(1);
    if start >= messages.len() {
        return CutPoint::default();
    }
    let valid_cuts = (start..messages.len())
        .filter(|&index| messages[index].role != "tool")
        .collect::<Vec<_>>();
    let Some(&first_valid) = valid_cuts.first() else {
        return CutPoint::default();
    };

    let mut accumulated = 0;
    let mut cut = first_valid;
    for index in (start..messages.len()).rev() {
        accumulated += estimate_messages(&messages[index..=index]);
        if accumulated >= keep_recent_tokens {
            cut = valid_cuts
                .iter()
                .copied()
                .find(|&candidate| candidate >= index)
                .or_else(|| {
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
    let kept_tokens = estimate_messages(&messages[first_kept..]);
    let turn_start = (messages[first_kept].role == "assistant")
        .then(|| {
            (start..first_kept)
                .rev()
                .find(|&index| messages[index].role == "user")
        })
        .flatten();
    CutPoint {
        first_kept,
        turn_start,
        kept_tokens,
    }
}

/// Extract read/modified file paths from tool calls and format an appendix.
pub fn file_operations_appendix(messages: &[Message]) -> String {
    let mut reads = Vec::new();
    let mut writes = Vec::new();
    for message in messages {
        for call in &message.tool_calls {
            let Some(path) = call.args.get("path").and_then(Value::as_str) else {
                continue;
            };
            match call.name.as_str() {
                "read" => {
                    if !reads.contains(&path.to_owned()) {
                        reads.push(path.to_owned());
                    }
                }
                "write" | "edit" if !writes.contains(&path.to_owned()) => {
                    writes.push(path.to_owned());
                }
                _ => {}
            }
        }
    }
    let mut appendix = String::new();
    if !reads.is_empty() {
        appendix.push_str("\n\nFiles read:\n");
        for path in reads {
            appendix.push_str(&format!("- {path}\n"));
        }
    }
    if !writes.is_empty() {
        appendix.push_str("\nFiles modified:\n");
        for path in writes {
            appendix.push_str(&format!("- {path}\n"));
        }
    }
    appendix
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::types::ToolCall;

    #[test]
    fn cut_point_never_cuts_tool_results_and_honors_the_budget() {
        let mut messages = Vec::new();
        for index in 0..3 {
            messages.push(Message::user(format!("request {index}")));
            messages.push(Message {
                role: "assistant".into(),
                content: format!("response {index}"),
                ..Default::default()
            });
        }
        for index in 0..20 {
            messages.push(Message {
                role: "tool".into(),
                tool_results: vec![crate::types::ToolResult {
                    tool_call_id: format!("call_{index}"),
                    content: "output".into(),
                    error: String::new(),
                }],
                ..Default::default()
            });
        }

        let cut = find_cut_point(&messages, 0, 10);
        assert!(cut.first_kept > 0);
        assert_ne!(messages[cut.first_kept].role, "tool");
        assert!(cut.kept_tokens >= 10);
    }

    #[test]
    fn cut_point_detects_split_turn_and_keeps_tool_results() {
        let mut messages = vec![
            Message::user("request 0"),
            Message {
                role: "assistant".into(),
                content: "response 0".into(),
                ..Default::default()
            },
            Message::user("request 1"),
            Message {
                role: "assistant".into(),
                content: "response 1".into(),
                ..Default::default()
            },
            Message {
                role: "assistant".into(),
                content: String::new(),
                tool_calls: vec![ToolCall {
                    id: "call_1".into(),
                    name: "read".into(),
                    args: serde_json::json!({"path": "workspace/a.txt"}),
                }],
                ..Default::default()
            },
        ];
        for index in 0..12 {
            messages.push(Message {
                role: "tool".into(),
                tool_results: vec![crate::types::ToolResult {
                    tool_call_id: format!("call_{index}"),
                    content: "output".into(),
                    error: String::new(),
                }],
                ..Default::default()
            });
        }

        let cut = find_cut_point(&messages, 0, 1);
        assert_eq!(messages[cut.first_kept].role, "assistant");
        assert_eq!(cut.turn_start, Some(2));
        assert_eq!(cut.first_kept, 4);
    }
}
