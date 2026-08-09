use std::collections::HashMap;
use std::sync::Arc;

use anyhow::Result;
use async_trait::async_trait;
use tokio::sync::mpsc;

use super::{
    context::{ContextStrategy, Summarizer},
    provider::Provider,
    session::Session,
    tool_executor::{ToolEvent, ToolExecutor},
    types::{Attachment, Chunk, Message, Response, ToolCall, ToolDef},
};

const MAX_TURN_ATTEMPTS: usize = 3;

/// Represents the context for a single agent turn.
#[derive(Clone, Debug)]
pub(super) struct Turn {
    pub(super) input: String,
    pub(super) attachments: Vec<Attachment>,
    pub(super) blocked_tools: HashMap<String, String>,
}

/// The core agent engine that runs the LLM → tool → LLM loop.
pub(super) struct Engine {
    provider: Arc<dyn Provider>,
    tool_executor: ToolExecutor,
    system_messages: Vec<Message>,
    tool_defs: Vec<ToolDef>,
    context: Arc<dyn ContextStrategy>,
    usage_records: std::sync::Mutex<Vec<crate::types::TokenUsage>>,
}

pub(super) struct StreamSink {
    pub(super) text: mpsc::Sender<String>,
    pub(super) reasoning: mpsc::Sender<String>,
}

impl Engine {
    pub(super) fn new(
        provider: Arc<dyn Provider>,
        tool_executor: ToolExecutor,
        system_messages: Vec<Message>,
        tool_defs: Vec<ToolDef>,
        context: Arc<dyn ContextStrategy>,
    ) -> Self {
        Self {
            provider,
            tool_executor,
            system_messages,
            tool_defs,
            context,
            usage_records: std::sync::Mutex::new(Vec::new()),
        }
    }

    pub(super) fn take_usage_records(&self) -> Vec<crate::types::TokenUsage> {
        std::mem::take(&mut *self.usage_records.lock().expect("usage record lock"))
    }

    fn clear_usage_records(&self) {
        self.usage_records
            .lock()
            .expect("usage record lock")
            .clear();
    }

    fn record_usage(&self, usage: Option<crate::types::TokenUsage>) {
        if let Some(usage) = usage {
            self.usage_records
                .lock()
                .expect("usage record lock")
                .push(usage);
        }
    }

    /// Run a complete agent turn. Adds the user message, then loops through step() until no more
    /// tool calls are returned.
    #[cfg(test)]
    pub(super) async fn run(
        &self,
        turn: &Turn,
        session: &mut Session,
        events: &mpsc::Sender<ToolEvent>,
        sink: Option<&StreamSink>,
    ) -> Result<()> {
        self.run_once(turn, session, events, sink, true).await
    }

    /// Retries transient provider failures inside the same Run and Provider Session. A retry does
    /// not append the Run input again: a failed attempt may already have appended tool messages.
    pub(super) async fn run_with_retries(
        &self,
        turn: &Turn,
        session: &mut Session,
        events: &mpsc::Sender<ToolEvent>,
        sink: Option<&StreamSink>,
    ) -> Result<()> {
        self.run_with_retries_inner(turn, session, events, sink, true)
            .await
    }

    /// Continue a Run after an input arrived through its mailbox without
    /// discarding usage records from the earlier provider calls in that Run.
    pub(super) async fn run_with_retries_preserving_usage(
        &self,
        turn: &Turn,
        session: &mut Session,
        events: &mpsc::Sender<ToolEvent>,
        sink: Option<&StreamSink>,
    ) -> Result<()> {
        self.run_with_retries_inner(turn, session, events, sink, false)
            .await
    }

    async fn run_with_retries_inner(
        &self,
        turn: &Turn,
        session: &mut Session,
        events: &mpsc::Sender<ToolEvent>,
        sink: Option<&StreamSink>,
        reset_usage: bool,
    ) -> Result<()> {
        if reset_usage {
            self.clear_usage_records();
        }
        let mut append_input = true;
        for attempt in 1..=MAX_TURN_ATTEMPTS {
            match self
                .run_once(turn, session, events, sink, append_input)
                .await
            {
                Ok(()) => return Ok(()),
                Err(error) if attempt < MAX_TURN_ATTEMPTS && is_retryable_error(&error) => {
                    tracing::warn!(
                        attempt,
                        max_attempts = MAX_TURN_ATTEMPTS,
                        failure_code = failure_code(&error),
                        "retrying transient provider failure"
                    );
                    append_input = false;
                }
                Err(error) => return Err(error),
            }
        }
        unreachable!("the final turn attempt returns from the loop")
    }

    async fn run_once(
        &self,
        turn: &Turn,
        session: &mut Session,
        events: &mpsc::Sender<ToolEvent>,
        sink: Option<&StreamSink>,
        append_input: bool,
    ) -> Result<()> {
        self.context
            .prepare_turn(session, "preemptive", self)
            .await?;
        if append_input {
            if turn.attachments.is_empty() {
                session.add(Message::user(turn.input.clone()));
            } else {
                session.add(Message::user_with_attachments(
                    turn.input.clone(),
                    turn.attachments.clone(),
                ));
            }
        }

        let mut retried_without_images = false;
        let mut retried_after_compaction = false;
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
                    if !retried_after_compaction && is_context_limit_error(&e) {
                        // A provider context error is authoritative. Only retry after the
                        // strategy actually changes the projected session; an under-estimated
                        // context must never receive the same request twice. Compaction errors
                        // remain visible to the caller instead of being retried.
                        let before = serde_json::to_vec(&self.build_messages(turn, session))
                            .unwrap_or_default();
                        self.context
                            .prepare_turn(session, "context_limit", self)
                            .await?;
                        let after = serde_json::to_vec(&self.build_messages(turn, session))
                            .unwrap_or_default();
                        if after == before {
                            return Err(e);
                        }
                        retried_after_compaction = true;
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
        // Persist an assistant message whenever the provider reports usage, even if the
        // response contains no visible content. This keeps one usage-bearing transcript entry
        // per provider call so harnesses can emit per-call telemetry without session baselines.
        let has_content = !resp.content.is_empty()
            || !resp.reasoning.is_empty()
            || !resp.tool_calls.is_empty()
            || resp.usage.is_some();

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
        let mut completed = false;

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
                    self.record_usage(u.clone());
                    usage = u;
                    completed = true;
                    break;
                }
                Chunk::Error { message } => {
                    anyhow::bail!("{message}");
                }
            }
        }

        if !completed {
            anyhow::bail!("provider_stream_incomplete");
        }

        Ok(Response {
            content,
            reasoning,
            tool_calls,
            usage,
        })
    }

    fn build_messages(&self, _turn: &Turn, session: &Session) -> Vec<Message> {
        let mut messages = self.system_messages.clone();
        messages.extend(self.context.project(session));
        messages
    }

    async fn collect_summary(&self, messages: &[Message]) -> Result<String> {
        let mut chunks = self.provider.chat_stream(messages, &[]).await?;
        let mut summary = String::new();
        let mut completed = false;
        while let Some(chunk) = chunks.recv().await {
            match chunk {
                Chunk::Text { delta } => summary.push_str(&delta),
                Chunk::Done { usage } => {
                    self.record_usage(usage);
                    completed = true;
                    break;
                }
                Chunk::Reasoning { .. } => {}
                Chunk::ToolCall { .. } => anyhow::bail!("context compaction returned a tool call"),
                Chunk::Error { message } => anyhow::bail!("{message}"),
            }
        }
        if !completed {
            anyhow::bail!("provider_stream_incomplete");
        }
        if summary.trim().is_empty() {
            anyhow::bail!("context compaction returned an empty summary");
        }
        Ok(summary)
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

#[async_trait]
impl Summarizer for Engine {
    async fn summarize(&self, messages: &[Message]) -> Result<String> {
        self.collect_summary(messages).await
    }
}

fn is_context_limit_error(error: &anyhow::Error) -> bool {
    error_chain_contains(
        error,
        &[
            "context length",
            "context window",
            "context_length_exceeded",
            "maximum context",
            "too many tokens",
            "token limit",
        ],
    )
}

pub(super) fn failure_code(error: &anyhow::Error) -> &'static str {
    if error_chain_contains(error, &["context compaction failed"]) {
        "context_compaction_failed"
    } else if is_context_limit_error(error) {
        "context_limit"
    } else if error_chain_contains(error, &["provider_stream_incomplete"]) {
        "provider_stream_incomplete"
    } else if error_chain_contains(error, &["provider_output_truncated"]) {
        "provider_output_truncated"
    } else if error_chain_contains(error, &["provider_stream_abnormal_finish"]) {
        "provider_stream_abnormal_finish"
    } else if error_chain_contains(error, &["failed to call chat completions"]) {
        "provider_request_failed"
    } else {
        "driver_error"
    }
}

fn error_chain_contains(error: &anyhow::Error, markers: &[&str]) -> bool {
    error.chain().any(|cause| {
        let message = cause.to_string().to_lowercase();
        markers.iter().any(|marker| message.contains(marker))
    })
}

fn is_retryable_error(error: &anyhow::Error) -> bool {
    let text = error
        .chain()
        .map(|cause| cause.to_string().to_lowercase())
        .collect::<Vec<_>>()
        .join(" ");
    if [
        "context compaction failed",
        "context length",
        "context window",
        "context_length_exceeded",
        "maximum context",
        "too many tokens",
        "token limit",
        "unauthorized",
        "authentication",
        "invalid api key",
        "permission denied",
        "forbidden",
        "tool call",
        "invalid json",
        "unknown tool",
        "sandbox",
    ]
    .iter()
    .any(|marker| text.contains(marker))
    {
        return false;
    }

    [
        "provider_stream_incomplete",
        "provider_stream_abnormal_finish",
        "timed out",
        "timeout",
        "connection reset",
        "connection refused",
        "connection closed",
        "broken pipe",
        "network is unreachable",
        "error sending request",
        "http status server error",
        "500 internal server error",
        "502 bad gateway",
        "503 service unavailable",
        "504 gateway timeout",
    ]
    .iter()
    .any(|marker| text.contains(marker))
}

#[cfg(test)]
mod tests {
    use super::super::context::{ContextStrategy, IdentityContext, Summarizer};
    use super::super::{tool_executor::ToolRunner, types::ToolDef};
    use super::*;
    use async_trait::async_trait;
    use serde_json::Value;

    fn test_engine(
        provider: Arc<dyn Provider>,
        tool_executor: ToolExecutor,
        system_messages: Vec<Message>,
        tool_defs: Vec<ToolDef>,
    ) -> Engine {
        Engine::new(
            provider,
            tool_executor,
            system_messages,
            tool_defs,
            Arc::new(IdentityContext),
        )
    }

    #[derive(Debug)]
    struct TestCompactingContext;

    #[derive(Debug)]
    struct MetadataOnlyContext;

    #[derive(Debug)]
    struct SummaryContext;

    #[async_trait]
    impl ContextStrategy for SummaryContext {
        fn project(&self, session: &Session) -> Vec<Message> {
            session.messages().to_vec()
        }

        async fn prepare_turn(
            &self,
            session: &mut Session,
            reason: &str,
            summarizer: &dyn Summarizer,
        ) -> Result<()> {
            if reason == "preemptive" {
                let _ = summarizer.summarize(&[Message::user("history")]).await?;
                session.metadata_mut()["summary_called"] = Value::Bool(true);
            }
            Ok(())
        }
    }

    #[async_trait]
    impl ContextStrategy for MetadataOnlyContext {
        fn project(&self, session: &Session) -> Vec<Message> {
            session.messages().to_vec()
        }

        async fn prepare_turn(
            &self,
            session: &mut Session,
            reason: &str,
            _summarizer: &dyn Summarizer,
        ) -> Result<()> {
            if reason == "context_limit" {
                session.metadata_mut()["test_compacted"] = Value::Bool(true);
            }
            Ok(())
        }
    }

    #[async_trait]
    impl ContextStrategy for TestCompactingContext {
        fn project(&self, session: &Session) -> Vec<Message> {
            if session.metadata()["test_compacted"].as_bool() == Some(true) {
                let mut projected = vec![Message::system("compacted")];
                projected.extend(session.messages().iter().cloned());
                projected
            } else {
                session.messages().to_vec()
            }
        }

        async fn prepare_turn(
            &self,
            session: &mut Session,
            reason: &str,
            _summarizer: &dyn Summarizer,
        ) -> Result<()> {
            if reason == "context_limit" {
                session.metadata_mut()["test_compacted"] = Value::Bool(true);
            }
            Ok(())
        }
    }

    fn test_engine_with_context(
        provider: Arc<dyn Provider>,
        context: Arc<dyn ContextStrategy>,
    ) -> Engine {
        Engine::new(
            provider,
            ToolExecutor::new(Arc::new(FakeTools)),
            vec![Message::system("test system")],
            vec![],
            context,
        )
    }

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
                    let _ = tx
                        .send(Chunk::Done {
                            usage: resp.usage.clone(),
                        })
                        .await;
                });
            }
            Ok(rx)
        }
    }

    struct ContextLimitProvider {
        call_count: std::sync::Mutex<usize>,
    }

    struct IncompleteProvider;

    struct RetryProvider {
        failures: usize,
        call_count: std::sync::Mutex<usize>,
        error: &'static str,
    }

    #[async_trait]
    impl Provider for RetryProvider {
        async fn chat_stream(
            &self,
            _messages: &[Message],
            _tools: &[ToolDef],
        ) -> Result<mpsc::Receiver<Chunk>> {
            let mut call_count = self.call_count.lock().unwrap();
            let call = *call_count;
            *call_count += 1;
            drop(call_count);

            let (tx, rx) = mpsc::channel(4);
            if call < self.failures {
                let error = self.error.to_owned();
                tokio::spawn(async move {
                    let _ = tx.send(Chunk::Error { message: error }).await;
                });
            } else {
                tokio::spawn(async move {
                    let _ = tx
                        .send(Chunk::Text {
                            delta: "completed".into(),
                        })
                        .await;
                    let _ = tx.send(Chunk::Done { usage: None }).await;
                });
            }
            Ok(rx)
        }
    }

    #[async_trait]
    impl Provider for IncompleteProvider {
        async fn chat_stream(
            &self,
            _messages: &[Message],
            _tools: &[ToolDef],
        ) -> Result<mpsc::Receiver<Chunk>> {
            let (tx, rx) = mpsc::channel(1);
            drop(tx);
            Ok(rx)
        }
    }

    #[async_trait]
    impl Provider for ContextLimitProvider {
        async fn chat_stream(
            &self,
            _messages: &[Message],
            _tools: &[ToolDef],
        ) -> Result<mpsc::Receiver<Chunk>> {
            let call = {
                let mut count = self.call_count.lock().unwrap();
                let call = *count;
                *count += 1;
                call
            };
            let (tx, rx) = mpsc::channel(4);
            tokio::spawn(async move {
                match call {
                    0 => {
                        let _ = tx
                            .send(Chunk::Error {
                                message: "maximum context length exceeded".into(),
                            })
                            .await;
                    }
                    1 => {
                        let _ = tx
                            .send(Chunk::Text {
                                delta: "compacted history".into(),
                            })
                            .await;
                        let _ = tx.send(Chunk::Done { usage: None }).await;
                    }
                    _ => {
                        let _ = tx
                            .send(Chunk::Text {
                                delta: "completed after compaction".into(),
                            })
                            .await;
                        let _ = tx.send(Chunk::Done { usage: None }).await;
                    }
                }
            });
            Ok(rx)
        }
    }

    struct FakeTools;

    #[async_trait]
    impl ToolRunner for FakeTools {
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
        let engine = test_engine(
            provider,
            executor,
            vec![Message::system("test system")],
            vec![],
        );

        let turn = Turn {
            input: "hi".into(),
            attachments: Vec::new(),
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
        let engine = test_engine(
            provider,
            executor,
            vec![Message::system("test system")],
            vec![ToolDef {
                name: "echo".into(),
                description: "Echo".into(),
                parameters: serde_json::json!({}),
            }],
        );

        let turn = Turn {
            input: "test".into(),
            attachments: Vec::new(),
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

    #[tokio::test]
    async fn engine_records_usage_for_each_provider_call() {
        let provider = Arc::new(FakeProvider {
            responses: vec![
                Response {
                    content: "summary".into(),
                    reasoning: String::new(),
                    tool_calls: vec![],
                    usage: Some(crate::types::TokenUsage {
                        input_tokens: 50,
                        output_tokens: 5,
                        total_tokens: 55,
                        ..Default::default()
                    }),
                },
                Response {
                    content: String::new(),
                    reasoning: String::new(),
                    tool_calls: vec![ToolCall {
                        id: "call_1".into(),
                        name: "echo".into(),
                        args: serde_json::json!({"msg": "test"}),
                    }],
                    usage: Some(crate::types::TokenUsage {
                        input_tokens: 100,
                        output_tokens: 10,
                        total_tokens: 110,
                        ..Default::default()
                    }),
                },
                Response {
                    content: "done".into(),
                    reasoning: String::new(),
                    tool_calls: vec![],
                    usage: Some(crate::types::TokenUsage {
                        input_tokens: 140,
                        output_tokens: 20,
                        total_tokens: 160,
                        ..Default::default()
                    }),
                },
            ],
            call_count: std::sync::Mutex::new(0),
        });
        let engine = Engine::new(
            provider,
            ToolExecutor::new(Arc::new(FakeTools)),
            vec![Message::system("test system")],
            vec![ToolDef {
                name: "echo".into(),
                description: "Echo".into(),
                parameters: serde_json::json!({}),
            }],
            Arc::new(SummaryContext),
        );
        let mut session = Session::default();
        let (events, _event_rx) = mpsc::channel(8);

        engine
            .run_with_retries(
                &Turn {
                    input: "test".into(),
                    attachments: Vec::new(),
                    blocked_tools: HashMap::new(),
                },
                &mut session,
                &events,
                None,
            )
            .await
            .unwrap();

        let usages = engine.take_usage_records();
        assert_eq!(usages.len(), 3);
        assert_eq!(usages[0].input_tokens, 50);
        assert_eq!(usages[1].input_tokens, 100);
        assert_eq!(usages[2].input_tokens, 140);
    }

    #[tokio::test]
    async fn context_limit_error_prepares_and_retries_the_same_turn_once() {
        let provider = Arc::new(ContextLimitProvider {
            call_count: std::sync::Mutex::new(0),
        });
        let engine = test_engine_with_context(provider.clone(), Arc::new(TestCompactingContext));
        let mut session = Session::default();
        for index in 0..7 {
            session.add(Message::user(format!("request {index}")));
            session.add(Message {
                role: "assistant".into(),
                content: format!("response {index}"),
                ..Default::default()
            });
        }
        let (events, _event_rx) = mpsc::channel(8);

        engine
            .run(
                &Turn {
                    input: "continue".into(),
                    attachments: Vec::new(),
                    blocked_tools: HashMap::new(),
                },
                &mut session,
                &events,
                None,
            )
            .await
            .unwrap();

        assert_eq!(*provider.call_count.lock().unwrap(), 2);
        assert_eq!(
            session.messages.last().unwrap().content,
            "compacted history"
        );
    }

    #[tokio::test]
    async fn context_limit_error_is_not_retried_when_compaction_does_not_change_context() {
        let provider = Arc::new(ContextLimitProvider {
            call_count: std::sync::Mutex::new(0),
        });
        let engine = test_engine_with_context(provider.clone(), Arc::new(MetadataOnlyContext));
        let mut session = Session::default();
        let (events, _event_rx) = mpsc::channel(8);

        let error = engine
            .run(
                &Turn {
                    input: "continue".into(),
                    attachments: Vec::new(),
                    blocked_tools: HashMap::new(),
                },
                &mut session,
                &events,
                None,
            )
            .await
            .unwrap_err();

        assert!(is_context_limit_error(&error));
        assert_eq!(*provider.call_count.lock().unwrap(), 1);
    }

    #[tokio::test]
    async fn engine_rejects_a_stream_that_closes_without_done() {
        let engine = test_engine(
            Arc::new(IncompleteProvider),
            ToolExecutor::new(Arc::new(FakeTools)),
            vec![Message::system("test system")],
            vec![],
        );
        let mut session = Session::default();
        let (events, _event_rx) = mpsc::channel(1);

        let error = engine
            .run(
                &Turn {
                    input: "continue".into(),
                    attachments: Vec::new(),
                    blocked_tools: HashMap::new(),
                },
                &mut session,
                &events,
                None,
            )
            .await
            .unwrap_err();

        assert_eq!(failure_code(&error), "provider_stream_incomplete");
    }

    #[tokio::test]
    async fn engine_retries_transient_provider_failures_without_duplicate_run_input() {
        let provider = Arc::new(RetryProvider {
            failures: 2,
            call_count: std::sync::Mutex::new(0),
            error: "503 Service Unavailable",
        });
        let engine = test_engine(
            provider.clone(),
            ToolExecutor::new(Arc::new(FakeTools)),
            vec![Message::system("test system")],
            vec![],
        );
        let mut session = Session::default();
        let (events, _event_rx) = mpsc::channel(1);

        engine
            .run_with_retries(
                &Turn {
                    input: "continue".into(),
                    attachments: Vec::new(),
                    blocked_tools: HashMap::new(),
                },
                &mut session,
                &events,
                None,
            )
            .await
            .unwrap();

        assert_eq!(*provider.call_count.lock().unwrap(), 3);
        assert_eq!(
            session
                .messages
                .iter()
                .filter(|message| message.role == "user")
                .count(),
            1
        );
        assert_eq!(session.messages.last().unwrap().content, "completed");
    }

    #[tokio::test]
    async fn engine_stops_after_three_transient_attempts() {
        let provider = Arc::new(RetryProvider {
            failures: 3,
            call_count: std::sync::Mutex::new(0),
            error: "provider_stream_abnormal_finish",
        });
        let engine = test_engine(
            provider.clone(),
            ToolExecutor::new(Arc::new(FakeTools)),
            vec![Message::system("test system")],
            vec![],
        );
        let mut session = Session::default();
        let (events, _event_rx) = mpsc::channel(1);

        let error = engine
            .run_with_retries(
                &Turn {
                    input: "continue".into(),
                    attachments: Vec::new(),
                    blocked_tools: HashMap::new(),
                },
                &mut session,
                &events,
                None,
            )
            .await
            .unwrap_err();

        assert_eq!(*provider.call_count.lock().unwrap(), 3);
        assert_eq!(failure_code(&error), "provider_stream_abnormal_finish");
    }

    #[tokio::test]
    async fn engine_does_not_retry_non_transient_provider_failures() {
        let provider = Arc::new(RetryProvider {
            failures: 3,
            call_count: std::sync::Mutex::new(0),
            error: "401 Unauthorized: invalid api key",
        });
        let engine = test_engine(
            provider.clone(),
            ToolExecutor::new(Arc::new(FakeTools)),
            vec![Message::system("test system")],
            vec![],
        );
        let mut session = Session::default();
        let (events, _event_rx) = mpsc::channel(1);

        let error = engine
            .run_with_retries(
                &Turn {
                    input: "continue".into(),
                    attachments: Vec::new(),
                    blocked_tools: HashMap::new(),
                },
                &mut session,
                &events,
                None,
            )
            .await
            .unwrap_err();

        assert_eq!(*provider.call_count.lock().unwrap(), 1);
        assert!(error.to_string().contains("invalid api key"));
    }

    #[test]
    fn context_limit_is_detected_inside_provider_error_chain() {
        let error =
            anyhow::anyhow!("context_length_exceeded").context("failed to call chat completions");

        assert!(is_context_limit_error(&error));
        assert_eq!(failure_code(&error), "context_limit");
    }

    #[test]
    fn compaction_failure_takes_precedence_over_its_provider_cause() {
        let error = anyhow::anyhow!("context_length_exceeded").context("context compaction failed");

        assert_eq!(failure_code(&error), "context_compaction_failed");
    }
}
