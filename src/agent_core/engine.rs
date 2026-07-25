use std::collections::HashMap;
use std::sync::Arc;

use anyhow::Result;
use tokio::sync::mpsc;

use crate::agent_core::{
    prompt::build_system_prompt,
    provider::Provider,
    session::Session,
    tool_executor::{ToolEvent, ToolExecutor},
    types::{Chunk, Message, Response, ToolCall, ToolDef},
};

/// Represents the context for a single agent turn.
#[derive(Clone, Debug)]
pub struct Turn {
    pub input: String,
    pub source: String,
    pub attachments: Vec<crate::agent_core::types::Attachment>,
    pub blocked_tools: HashMap<String, String>,
}

/// The core agent engine that runs the LLM → tool → LLM loop.
pub struct Engine {
    provider: Arc<dyn Provider>,
    tool_executor: ToolExecutor,
    prompt_context: crate::prompt::PromptContext,
    system_prompt: Option<String>,
    tool_defs: Vec<ToolDef>,
}

pub struct StreamSink {
    pub text: mpsc::Sender<String>,
    pub reasoning: mpsc::Sender<String>,
}

impl Engine {
    pub fn new(
        provider: Arc<dyn Provider>,
        tool_executor: ToolExecutor,
        prompt_context: crate::prompt::PromptContext,
        system_prompt: Option<String>,
        tool_defs: Vec<ToolDef>,
    ) -> Self {
        Self {
            provider,
            tool_executor,
            prompt_context,
            system_prompt,
            tool_defs,
        }
    }

    /// Run a complete agent turn. Adds the user message, then loops
    /// through step() until no more tool calls are returned.
    pub async fn run(
        &self,
        turn: &Turn,
        session: &mut Session,
        events: &mpsc::Sender<ToolEvent>,
        sink: Option<&StreamSink>,
    ) -> Result<()> {
        session.add(Message::user(turn.input.clone()));

        let mut retried_without_images = false;
        loop {
            match self.step(turn, session, events, sink).await {
                Ok(resp) => {
                    if !resp.has_tool_calls() {
                        return Ok(());
                    }
                }
                Err(e) => {
                    let err_str = e.to_string().to_lowercase();
                    if !retried_without_images
                        && err_str.contains("image_url")
                        && (err_str.contains("expected text")
                            || err_str.contains("unknown variant"))
                        && session.strip_image_attachments(true)
                    {
                        retried_without_images = true;
                        continue;
                    }
                    return Err(e);
                }
            }
        }
    }

    /// Execute a single step: stream LLM response, add to session, run tools.
    async fn step(
        &self,
        turn: &Turn,
        session: &mut Session,
        events: &mpsc::Sender<ToolEvent>,
        sink: Option<&StreamSink>,
    ) -> Result<Response> {
        let resp = self.stream(turn, session, sink).await?;
        let has_content =
            !resp.content.is_empty() || !resp.reasoning.is_empty() || !resp.tool_calls.is_empty();

        if has_content {
            session.add(Message {
                role: "assistant".into(),
                content: resp.content.clone(),
                reasoning: resp.reasoning.clone(),
                tool_calls: resp.tool_calls.clone(),
                usage: resp.usage.clone(),
                ..Default::default()
            });
        }

        if !resp.tool_calls.is_empty() {
            let results = self
                .tool_executor
                .run(&resp.tool_calls, &turn.blocked_tools, events)
                .await;
            for result in results {
                session.add(Message::tool(vec![result]));
            }
        }

        Ok(resp)
    }

    /// Stream a single LLM call.
    async fn stream(
        &self,
        turn: &Turn,
        session: &Session,
        sink: Option<&StreamSink>,
    ) -> Result<Response> {
        let messages = self.build_messages(turn, session);
        let tool_defs = self.filtered_tool_defs(turn);
        let mut chunk_rx = self.provider.chat_stream(&messages, &tool_defs).await?;

        let mut content = String::new();
        let mut reasoning = String::new();
        let mut tool_calls: Vec<ToolCall> = Vec::new();
        let mut usage = None;

        while let Some(chunk) = chunk_rx.recv().await {
            match chunk {
                Chunk::Text { delta } => {
                    content.push_str(&delta);
                    if let Some(s) = sink {
                        s.text.send(delta.clone()).await.ok();
                    }
                }
                Chunk::Reasoning { delta } => {
                    reasoning.push_str(&delta);
                    if let Some(s) = sink {
                        s.reasoning.send(delta.clone()).await.ok();
                    }
                }
                Chunk::ToolCall { call } => {
                    tool_calls.push(call);
                }
                Chunk::Done { usage: u } => {
                    usage = u;
                }
                Chunk::Error { message } => {
                    anyhow::bail!("{message}");
                }
            }
        }

        Ok(Response {
            content,
            reasoning,
            tool_calls,
            usage,
        })
    }

    fn build_messages(&self, _turn: &Turn, session: &Session) -> Vec<Message> {
        let system_content = self
            .system_prompt
            .clone()
            .unwrap_or_else(|| build_system_prompt(&self.prompt_context));
        let mut messages = vec![Message::system(system_content)];
        messages.extend(session.messages.clone());
        messages
    }

    fn filtered_tool_defs(&self, turn: &Turn) -> Vec<ToolDef> {
        if turn.blocked_tools.is_empty() {
            return self.tool_defs.clone();
        }
        self.tool_defs
            .iter()
            .filter(|t| !turn.blocked_tools.contains_key(&t.name))
            .cloned()
            .collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::agent_core::{tool_executor::ToolRunner, types::ToolDef};
    use async_trait::async_trait;
    use serde_json::Value;

    struct FakeProvider {
        responses: Vec<Response>,
        call_count: std::sync::Mutex<usize>,
    }

    #[async_trait]
    impl Provider for FakeProvider {
        async fn chat_stream(
            &self,
            _messages: &[Message],
            _tools: &[ToolDef],
        ) -> Result<mpsc::Receiver<Chunk>> {
            let mut count = self.call_count.lock().unwrap();
            let idx = *count;
            *count += 1;

            let (tx, rx) = mpsc::channel(8);
            if idx < self.responses.len() {
                let resp = self.responses[idx].clone();
                tokio::spawn(async move {
                    if !resp.content.is_empty() {
                        let _ = tx
                            .send(Chunk::Text {
                                delta: resp.content,
                            })
                            .await;
                    }
                    for tc in &resp.tool_calls {
                        let _ = tx.send(Chunk::ToolCall { call: tc.clone() }).await;
                    }
                    let _ = tx.send(Chunk::Done { usage: None }).await;
                });
            }
            Ok(rx)
        }
    }

    struct FakeTools;

    #[async_trait]
    impl ToolRunner for FakeTools {
        fn definitions(&self) -> Vec<ToolDef> {
            vec![ToolDef {
                name: "echo".into(),
                description: "Echo back".into(),
                parameters: serde_json::json!({}),
            }]
        }

        async fn run(&self, _name: &str, args: &Value) -> Result<String> {
            Ok(format!("echo: {}", args))
        }
    }

    #[tokio::test]
    async fn engine_returns_when_no_tool_calls() {
        let provider = Arc::new(FakeProvider {
            responses: vec![Response {
                content: "Hello!".into(),
                reasoning: String::new(),
                tool_calls: vec![],
                usage: None,
            }],
            call_count: std::sync::Mutex::new(0),
        });
        let tools = Arc::new(FakeTools);
        let executor = ToolExecutor::new(tools);
        let engine = Engine::new(provider, executor, Default::default(), None, vec![]);

        let turn = Turn {
            input: "hi".into(),
            source: String::new(),
            attachments: vec![],
            blocked_tools: HashMap::new(),
        };
        let mut session = Session::default();
        let (tx, _rx) = mpsc::channel(8);

        engine.run(&turn, &mut session, &tx, None).await.unwrap();
        assert_eq!(session.messages.len(), 2);
        assert_eq!(session.messages[0].role, "user");
        assert_eq!(session.messages[1].role, "assistant");
        assert_eq!(session.messages[1].content, "Hello!");
    }

    #[tokio::test]
    async fn engine_loops_on_tool_calls() {
        let provider = Arc::new(FakeProvider {
            responses: vec![
                Response {
                    content: String::new(),
                    reasoning: String::new(),
                    tool_calls: vec![ToolCall {
                        id: "call_1".into(),
                        name: "echo".into(),
                        args: serde_json::json!({"msg": "test"}),
                    }],
                    usage: None,
                },
                Response {
                    content: "Done after tools.".into(),
                    reasoning: String::new(),
                    tool_calls: vec![],
                    usage: None,
                },
            ],
            call_count: std::sync::Mutex::new(0),
        });
        let tools = Arc::new(FakeTools);
        let executor = ToolExecutor::new(tools);
        let engine = Engine::new(
            provider,
            executor,
            Default::default(),
            None,
            vec![ToolDef {
                name: "echo".into(),
                description: "Echo".into(),
                parameters: serde_json::json!({}),
            }],
        );

        let turn = Turn {
            input: "test".into(),
            source: String::new(),
            attachments: vec![],
            blocked_tools: HashMap::new(),
        };
        let mut session = Session::default();
        let (tx, _rx) = mpsc::channel(8);

        engine.run(&turn, &mut session, &tx, None).await.unwrap();
        assert_eq!(session.messages.len(), 4);
        assert_eq!(session.messages[0].role, "user");
        assert_eq!(session.messages[1].role, "assistant");
        assert!(!session.messages[1].tool_calls.is_empty());
        assert_eq!(session.messages[2].role, "tool");
        assert_eq!(session.messages[3].role, "assistant");
        assert_eq!(session.messages[3].content, "Done after tools.");
    }
}
