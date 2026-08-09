use std::{
    collections::{BTreeMap, HashMap, VecDeque},
    path::{Path, PathBuf},
    process::Stdio,
    sync::Arc,
    time::Duration,
};

use anyhow::{Context, bail};
use async_trait::async_trait;
use serde_json::Value;
use tokio::task::JoinHandle;
use uuid::Uuid;

use crate::{
    config::{AgentConfig, SandboxConfig},
    engine::{Engine, Turn, failure_code},
    memory::{self, MemoryFile, PRIMARY_MEMORY_PATH},
    plugin::{AgentPlugin, PluginContext},
    provider::OpenAiProvider,
    sandbox::SandboxAdapter,
    session::Session,
    tool_executor::{ToolEvent, ToolExecutor, ToolRunner},
    types::{Attachment, Message, TokenUsage, ToolDef},
    workspace::{agent_rooted_path, collect_shell_output, edit_utf8, read_utf8, write_utf8},
};

const MAX_TOOL_OUTPUT_BYTES: usize = 1024 * 1024;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AgentError {
    Unavailable,
    SessionLost,
    Conflict,
    NotFound,
    Internal,
}

impl std::fmt::Display for AgentError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(match self {
            Self::Unavailable => "builtin agent is unavailable",
            Self::SessionLost => "builtin agent session is lost",
            Self::Conflict => "builtin agent operation conflicts with the current state",
            Self::NotFound => "builtin agent resource was not found",
            Self::Internal => "builtin agent internal error",
        })
    }
}

impl std::error::Error for AgentError {}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum TurnOutcome {
    Completed,
    Failed,
    Interrupted,
}

/// One agent turn request. Prompts are assembled by the embedding (harness);
/// the runtime only transports them and runs the tool loop.
#[derive(Clone)]
pub struct TurnRequest {
    pub system_messages: Vec<Message>,
    pub user_message: String,
    pub attachments: Vec<Attachment>,
    pub blocked_tools: HashMap<String, String>,
    /// Stable key used for provider prompt caching.
    pub prompt_cache_key: String,
    /// Extra environment variables for sandboxed shell subprocesses.
    pub sandbox_environment: BTreeMap<String, String>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Completion {
    pub run_id: Uuid,
    pub agent_id: Uuid,
    pub outcome: TurnOutcome,
    pub usage: Option<TokenUsage>,
}

pub struct AgentRuntime {
    config: AgentConfig,
    plugins: Vec<Arc<dyn AgentPlugin>>,
    sessions: BTreeMap<String, Uuid>,
    turns: BTreeMap<Uuid, BuiltinTurn>,
    completions: VecDeque<Completion>,
}

struct BuiltinTurn {
    locator: String,
    agent_id: Uuid,
    task: JoinHandle<(TurnOutcome, TokenUsage)>,
}

impl AgentRuntime {
    pub fn new(config: AgentConfig, plugins: Vec<Arc<dyn AgentPlugin>>) -> Self {
        Self {
            config,
            plugins,
            sessions: BTreeMap::new(),
            turns: BTreeMap::new(),
            completions: VecDeque::new(),
        }
    }

    pub fn config(&self) -> &AgentConfig {
        &self.config
    }

    pub fn agent_home(&self, agent_id: Uuid) -> PathBuf {
        self.config.agent_home(agent_id)
    }

    pub fn memory_root(&self, agent_id: Uuid) -> PathBuf {
        self.agent_home(agent_id).join("memory")
    }

    /// Provision the agent home layout and the initial primary memory document.
    ///
    /// Creates `workspace/`, `memory/`, `runs/`, and `drivers/builtin/`, then
    /// writes `memory/MEMORY.md` when it does not exist yet.
    pub async fn provision(
        &self,
        agent_id: Uuid,
        identity: &str,
        role: &str,
    ) -> Result<(), AgentError> {
        let home = self.agent_home(agent_id);
        for relative in ["workspace", "memory", "runs", "drivers/builtin"] {
            tokio::fs::create_dir_all(home.join(relative))
                .await
                .map_err(|_| AgentError::Internal)?;
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            tokio::fs::set_permissions(&home, std::fs::Permissions::from_mode(0o700))
                .await
                .map_err(|_| AgentError::Internal)?;
            tokio::fs::set_permissions(home.join("memory"), std::fs::Permissions::from_mode(0o700))
                .await
                .map_err(|_| AgentError::Internal)?;
        }
        if self
            .read_memory(agent_id, PRIMARY_MEMORY_PATH)
            .await
            .is_err()
        {
            let document = format!(
                "# {identity}\n\n## Role\n\n{role}\n\n## Key Knowledge\n\n- None recorded yet.\n\n## Active Context\n\n- Current focus: No active work recorded.\n"
            );
            self.write_memory(agent_id, PRIMARY_MEMORY_PATH, document.as_bytes())
                .await?;
        }
        Ok(())
    }

    pub async fn list_memory(&self, agent_id: Uuid) -> Result<Vec<MemoryFile>, AgentError> {
        memory::list_memory(&self.memory_root(agent_id)).await
    }

    pub async fn read_memory(&self, agent_id: Uuid, path: &str) -> Result<Vec<u8>, AgentError> {
        memory::read_memory(&self.memory_root(agent_id), path).await
    }

    pub async fn write_memory(
        &self,
        agent_id: Uuid,
        path: &str,
        content: &[u8],
    ) -> Result<(), AgentError> {
        memory::write_memory(&self.memory_root(agent_id), path, content).await
    }

    fn session_path(&self, agent_id: Uuid, locator: &str) -> PathBuf {
        self.agent_home(agent_id)
            .join("drivers/builtin/sessions")
            .join(format!("{locator}.json"))
    }

    async fn load_session(&self, agent_id: Uuid, locator: &str) -> Result<Session, AgentError> {
        let bytes = tokio::fs::read(self.session_path(agent_id, locator))
            .await
            .map_err(|_| AgentError::SessionLost)?;
        serde_json::from_slice(&bytes).map_err(|_| AgentError::SessionLost)
    }

    fn owner_for_locator(&self, locator: &str) -> Result<Uuid, AgentError> {
        self.sessions
            .get(locator)
            .copied()
            .ok_or(AgentError::SessionLost)
    }

    pub async fn validate(&self, agent_id: Uuid) -> Result<(), AgentError> {
        let home = self.agent_home(agent_id);
        if !home.join("workspace").is_dir()
            || !home.join("memory").is_dir()
            || !home.join("runs").is_dir()
            || !home.join("drivers/builtin").is_dir()
        {
            return Err(AgentError::Unavailable);
        }
        SandboxAdapter::validate()?;
        let mut command = SandboxAdapter::command(
            &SandboxAdapter::shell(),
            &home,
            &home.join("drivers/builtin"),
            &self.config.sandbox,
            &BTreeMap::new(),
        )?;
        let status = tokio::time::timeout(
            Duration::from_secs(10),
            command
                .arg("-c")
                .arg("true")
                .stdin(Stdio::null())
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .status(),
        )
        .await
        .map_err(|_| AgentError::Unavailable)?
        .map_err(|_| AgentError::Unavailable)?;
        if status.success() {
            Ok(())
        } else {
            Err(AgentError::Unavailable)
        }
    }

    pub async fn create_session(&mut self, agent_id: Uuid) -> Result<String, AgentError> {
        let locator = Uuid::now_v7().to_string();
        let directory = self.agent_home(agent_id).join("drivers/builtin/sessions");
        tokio::fs::create_dir_all(&directory)
            .await
            .map_err(|_| AgentError::Internal)?;
        restrict_directory(&directory).await?;
        persist_session(
            &directory.join(format!("{locator}.json")),
            &Session::default(),
        )
        .await?;
        self.sessions.insert(locator.clone(), agent_id);
        Ok(locator)
    }

    pub async fn resume_session(
        &mut self,
        agent_id: Uuid,
        locator: &str,
    ) -> Result<bool, AgentError> {
        match self.load_session(agent_id, locator).await {
            Ok(_) => {
                self.sessions.insert(locator.to_owned(), agent_id);
                Ok(true)
            }
            Err(AgentError::SessionLost) => Ok(false),
            Err(error) => Err(error),
        }
    }

    pub async fn start_turn(
        &mut self,
        run_id: Uuid,
        locator: &str,
        request: TurnRequest,
    ) -> Result<(), AgentError> {
        let agent_id = self.owner_for_locator(locator)?;
        if self.turns.contains_key(&run_id) {
            return Err(AgentError::Conflict);
        }
        let provider = self
            .config
            .provider
            .clone()
            .with_prompt_cache_key(request.prompt_cache_key.clone());
        let session = self.load_session(agent_id, locator).await?;
        let agent_home = self.agent_home(agent_id);
        let session_path = self.session_path(agent_id, locator);
        let sandbox_config = self.config.sandbox.clone();
        let context = Arc::clone(&self.config.context);
        let plugins = self.plugins.clone();
        let request_owned = request.clone();
        let task = tokio::spawn(async move {
            let tools = Arc::new(CompositeTools {
                builtin: BuiltinTools {
                    agent_home: agent_home.clone(),
                    sandbox: sandbox_config,
                    environment: request_owned.sandbox_environment.clone(),
                },
                plugins: plugins.clone(),
                context: PluginContext {
                    agent_id,
                    agent_home: agent_home.clone(),
                },
            });
            let engine = match OpenAiProvider::new(provider) {
                Ok(provider) => Engine::new(
                    Arc::new(provider),
                    ToolExecutor::new(tools),
                    system_messages(&request_owned, &plugins),
                    tool_definitions(&plugins),
                    context,
                ),
                Err(_) => return (TurnOutcome::Failed, TokenUsage::default()),
            };
            let turn = Turn {
                input: request_owned.user_message.clone(),
                attachments: request_owned.attachments.clone(),
                blocked_tools: request_owned.blocked_tools.clone(),
            };
            let mut session = session;
            let (events, mut event_rx) = tokio::sync::mpsc::channel(64);
            tokio::spawn(async move {
                while let Some(event) = event_rx.recv().await {
                    match event {
                        ToolEvent::Started { tool, summary } => {
                            tracing::debug!(tool, summary, "Builtin tool started");
                        }
                        ToolEvent::Finished { tool, summary } => {
                            tracing::debug!(tool, summary, "Builtin tool finished");
                        }
                        ToolEvent::Failed {
                            tool,
                            summary,
                            error_code,
                            error,
                        } => {
                            tracing::warn!(
                                tool,
                                summary,
                                failure_code = error_code,
                                error,
                                "Builtin tool failed"
                            );
                        }
                    }
                }
            });
            let outcome = match engine
                .run_with_retries(&turn, &mut session, &events, None)
                .await
            {
                Ok(()) => TurnOutcome::Completed,
                Err(error) => {
                    tracing::warn!(
                        %run_id,
                        failure_code = failure_code(&error),
                        "Builtin turn failed"
                    );
                    TurnOutcome::Failed
                }
            };
            let usage = session.token_usage();
            tracing::info!(
                %run_id,
                input_tokens = usage.input_tokens,
                output_tokens = usage.output_tokens,
                cached_input_tokens = usage.cached_input_tokens,
                "Builtin model usage"
            );
            if persist_session(&session_path, &session).await.is_err() {
                tracing::warn!(
                    %run_id,
                    failure_code = "session_persist_failed",
                    "Builtin turn failed"
                );
                return (TurnOutcome::Failed, usage.clone());
            }
            (outcome, usage)
        });
        self.turns.insert(
            run_id,
            BuiltinTurn {
                locator: locator.to_owned(),
                agent_id,
                task,
            },
        );
        Ok(())
    }

    pub async fn steer(&mut self, locator: &str) -> Result<(), AgentError> {
        self.owner_for_locator(locator).map(|_| ())
    }

    pub async fn notice(&mut self, locator: &str) -> Result<(), AgentError> {
        self.owner_for_locator(locator).map(|_| ())
    }

    pub async fn interrupt(&mut self, locator: &str) -> Result<(), AgentError> {
        self.owner_for_locator(locator)?;
        let run_id = self
            .turns
            .iter()
            .find_map(|(run_id, turn)| (turn.locator == locator).then_some(*run_id));
        if let Some(run_id) = run_id
            && let Some(turn) = self.turns.remove(&run_id)
        {
            turn.task.abort();
            let _ = turn.task.await;
            self.completions.push_back(Completion {
                run_id,
                agent_id: turn.agent_id,
                outcome: TurnOutcome::Interrupted,
                usage: None,
            });
        }
        Ok(())
    }

    pub async fn restart_agent(&mut self, agent_id: Uuid) -> Result<(), AgentError> {
        let run_ids = self
            .turns
            .iter()
            .filter(|(_, turn)| {
                self.sessions
                    .get(&turn.locator)
                    .is_some_and(|owner| *owner == agent_id)
            })
            .map(|(run_id, _)| *run_id)
            .collect::<Vec<_>>();
        for run_id in run_ids {
            if let Some(turn) = self.turns.remove(&run_id) {
                turn.task.abort();
                let _ = turn.task.await;
            }
        }
        self.sessions.retain(|_, owner| *owner != agent_id);
        Ok(())
    }

    pub async fn delete_session(&mut self, locator: &str) -> Result<(), AgentError> {
        let agent_id = self.owner_for_locator(locator)?;
        self.interrupt(locator).await?;
        match tokio::fs::remove_file(self.session_path(agent_id, locator)).await {
            Ok(()) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(_) => return Err(AgentError::Internal),
        }
        self.sessions.remove(locator);
        Ok(())
    }

    /// Returns the latest non-empty assistant reply from the persisted session.
    ///
    /// Channel plugins read this after a completed turn to deliver the final
    /// text to the external conversation.
    pub async fn latest_reply(&self, locator: &str) -> Result<Option<String>, AgentError> {
        let agent_id = self.owner_for_locator(locator)?;
        let session = self.load_session(agent_id, locator).await?;
        Ok(session
            .messages
            .iter()
            .rev()
            .find(|message| message.role == "assistant" && !message.content.trim().is_empty())
            .map(|message| message.content.clone()))
    }

    pub fn process_evidence(&self, run_id: Uuid) -> bool {
        self.turns.contains_key(&run_id)
    }

    pub async fn poll_completions(&mut self) -> Result<Vec<Completion>, AgentError> {
        let finished = self
            .turns
            .iter()
            .filter_map(|(run_id, turn)| turn.task.is_finished().then_some(*run_id))
            .collect::<Vec<_>>();
        for run_id in finished {
            let turn = self.turns.remove(&run_id).ok_or(AgentError::Internal)?;
            let (outcome, usage) = turn
                .task
                .await
                .unwrap_or((TurnOutcome::Failed, TokenUsage::default()));
            self.completions.push_back(Completion {
                run_id,
                agent_id: turn.agent_id,
                outcome,
                usage: Some(usage),
            });
        }
        Ok(self.completions.drain(..).collect())
    }
}

fn system_messages(request: &TurnRequest, plugins: &[Arc<dyn AgentPlugin>]) -> Vec<Message> {
    let plugin_contract = plugins
        .iter()
        .map(|plugin| plugin.contract())
        .filter(|contract| !contract.trim().is_empty())
        .collect::<Vec<_>>()
        .join("\n\n");
    let mut messages = request.system_messages.clone();
    if !plugin_contract.is_empty() {
        messages.push(Message::system(plugin_contract));
    }
    messages
}

fn tool_definitions(plugins: &[Arc<dyn AgentPlugin>]) -> Vec<ToolDef> {
    let mut definitions = vec![
        tool_definition(
            "read",
            "Read a UTF-8 file. Paths: `workspace/<path>` or `memory/<path>`; bare Memory paths like `MEMORY.md` and `notes/<topic>.md` default to memory/.",
            &["path"],
        ),
        tool_definition(
            "write",
            "Write a UTF-8 file. Paths: `workspace/<path>` or `memory/<path>`; bare Memory paths like `MEMORY.md` and `notes/<topic>.md` default to memory/.",
            &["path", "content"],
        ),
        tool_definition(
            "edit",
            "Replace one exact text occurrence. Paths: `workspace/<path>` or `memory/<path>`; bare Memory paths like `MEMORY.md` and `notes/<topic>.md` default to memory/.",
            &["path", "old_text", "new_text"],
        ),
        tool_definition(
            "bash",
            "Run a sandboxed shell command from the Agent Home root. Paths are `workspace/<path>` or `memory/<path>`; shell writes are allowed under workspace/, runs/ ($TMPDIR), and /tmp.",
            &["command"],
        ),
    ];
    for plugin in plugins {
        definitions.extend(plugin.tools());
    }
    definitions
}

fn tool_definition(name: &str, description: &str, required: &[&str]) -> ToolDef {
    let properties = required
        .iter()
        .map(|field| ((*field).to_owned(), serde_json::json!({"type": "string"})))
        .collect::<serde_json::Map<_, _>>();
    ToolDef {
        name: name.to_owned(),
        description: description.to_owned(),
        parameters: serde_json::json!({
            "type": "object",
            "properties": properties,
            "required": required
        }),
    }
}

struct CompositeTools {
    builtin: BuiltinTools,
    plugins: Vec<Arc<dyn AgentPlugin>>,
    context: PluginContext,
}

#[async_trait]
impl ToolRunner for CompositeTools {
    async fn run(&self, name: &str, args: &Value) -> anyhow::Result<String> {
        for plugin in &self.plugins {
            if plugin.tools().iter().any(|tool| tool.name == name) {
                return plugin.run_tool(&self.context, name, args).await;
            }
        }
        self.builtin.run(name, args).await
    }
}

struct BuiltinTools {
    agent_home: PathBuf,
    sandbox: SandboxConfig,
    environment: BTreeMap<String, String>,
}

#[async_trait]
impl ToolRunner for BuiltinTools {
    async fn run(&self, name: &str, args: &Value) -> anyhow::Result<String> {
        match name {
            "read" => {
                let path = required_string(args, "path")?;
                let (root, relative) = agent_rooted_path(&self.agent_home, path)?;
                read_utf8(&root, &relative).await
            }
            "write" => {
                let path = required_string(args, "path")?;
                let content = required_string(args, "content")?;
                let (root, relative) = agent_rooted_path(&self.agent_home, path)?;
                write_utf8(&root, &relative, content).await?;
                Ok(format!("Wrote {path}"))
            }
            "edit" => {
                let path = required_string(args, "path")?;
                let old_text = required_string(args, "old_text")?;
                let new_text = required_string(args, "new_text")?;
                let (root, relative) = agent_rooted_path(&self.agent_home, path)?;
                edit_utf8(&root, &relative, old_text, new_text).await?;
                Ok(format!("Edited {path}"))
            }
            "bash" => self.run_shell(required_string(args, "command")?).await,
            _ => bail!("unknown tool"),
        }
    }
}

impl BuiltinTools {
    async fn run_shell(&self, script: &str) -> anyhow::Result<String> {
        let mut command = SandboxAdapter::command(
            &SandboxAdapter::shell(),
            &self.agent_home,
            &self.agent_home.join("drivers/builtin"),
            &self.sandbox,
            &self.environment,
        )
        .map_err(|_| anyhow::anyhow!("sandbox unavailable"))?;
        #[cfg(unix)]
        command.process_group(0);
        let child = command
            .arg("-c")
            .arg(script)
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .kill_on_drop(true)
            .spawn()
            .context("failed to start sandboxed shell")?;
        let (status, output) =
            collect_shell_output(child, Duration::from_secs(120), MAX_TOOL_OUTPUT_BYTES).await?;
        if status.success() {
            Ok(output)
        } else {
            bail!("shell exited with {status}: {output}")
        }
    }
}

fn required_string<'a>(args: &'a Value, name: &str) -> anyhow::Result<&'a str> {
    args.get(name)
        .and_then(Value::as_str)
        .with_context(|| format!("{name} is required"))
}

async fn persist_session(path: &Path, session: &Session) -> Result<(), AgentError> {
    let encoded = serde_json::to_vec(session).map_err(|_| AgentError::Internal)?;
    let temporary = path.with_extension(format!("{}.tmp", Uuid::now_v7()));
    let mut options = tokio::fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        options.mode(0o600);
    }
    let mut file = options
        .open(&temporary)
        .await
        .map_err(|_| AgentError::Internal)?;
    use tokio::io::AsyncWriteExt;
    file.write_all(&encoded)
        .await
        .map_err(|_| AgentError::Internal)?;
    file.sync_all().await.map_err(|_| AgentError::Internal)?;
    drop(file);
    tokio::fs::rename(temporary, path)
        .await
        .map_err(|_| AgentError::Internal)
}

async fn restrict_directory(path: &Path) -> Result<(), AgentError> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        tokio::fs::set_permissions(path, std::fs::Permissions::from_mode(0o700))
            .await
            .map_err(|_| AgentError::Internal)?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::fs;

    use tokio::io::{AsyncReadExt, AsyncWriteExt};

    use super::*;
    use crate::{
        config::SandboxConfig,
        context::IdentityContext,
        provider::ProviderConfig,
        types::{Message, ToolDef},
    };

    fn agent_config(api_base: &str) -> AgentConfig {
        AgentConfig {
            computer_home: std::env::temp_dir().join("unused"),
            provider: ProviderConfig::openai("provider-secret", "test-model".into())
                .with_base_url(api_base.to_owned()),
            sandbox: SandboxConfig::default(),
            context: std::sync::Arc::new(IdentityContext),
        }
    }

    fn run_request(agent_id: Uuid) -> TurnRequest {
        TurnRequest {
            system_messages: vec![Message::system("test system")],
            user_message: format!("Process run for {agent_id}."),
            attachments: Vec::new(),
            blocked_tools: HashMap::new(),
            prompt_cache_key: "test-cache-key".to_owned(),
            sandbox_environment: BTreeMap::new(),
        }
    }

    async fn serve_scripted(script: Vec<&'static str>) -> String {
        let listener = tokio::net::TcpListener::bind(("127.0.0.1", 0))
            .await
            .unwrap();
        let address = listener.local_addr().unwrap();
        tokio::spawn(async move {
            for body in script {
                let (mut stream, _) = listener.accept().await.unwrap();
                let mut request = vec![0_u8; 8192];
                let read = stream.read(&mut request).await.unwrap();
                let request = String::from_utf8_lossy(&request[..read]);
                assert!(request.contains("authorization: Bearer provider-secret"));
                let response = format!(
                    "HTTP/1.1 200 OK\r\ncontent-type: text/event-stream\r\ncontent-length: {}\r\nconnection: close\r\n\r\n{}",
                    body.len(),
                    body
                );
                stream.write_all(response.as_bytes()).await.unwrap();
            }
        });
        format!("http://{address}/v1")
    }

    #[tokio::test]
    async fn agent_persists_and_resumes_provider_session_without_exposing_secret() {
        let api_base = serve_scripted(vec![concat!(
            "data: {\"choices\":[{\"delta\":{\"content\":\"completed\"},\"finish_reason\":\"stop\"}]}\n\n",
            "data: [DONE]\n\n"
        )])
        .await;

        let directory = tempfile::tempdir().unwrap();
        let computer_home = directory.path().join("computer");
        let agent_id = Uuid::now_v7();
        let agent_home = computer_home.join("agents").join(agent_id.to_string());
        for relative in ["workspace", "memory", "runs", "drivers/builtin"] {
            fs::create_dir_all(agent_home.join(relative)).unwrap();
        }
        let mut config = agent_config(&api_base);
        config.computer_home = computer_home.clone();
        let mut runtime = AgentRuntime::new(config, Vec::new());
        let locator = runtime.create_session(agent_id).await.unwrap();
        let run_id = Uuid::now_v7();

        runtime
            .start_turn(run_id, &locator, run_request(agent_id))
            .await
            .unwrap();
        let completion = loop {
            let completions = runtime.poll_completions().await.unwrap();
            if let Some(completion) = completions.into_iter().next() {
                break completion;
            }
            tokio::task::yield_now().await;
        };
        assert!(matches!(
            completion,
            Completion {
                run_id,
                agent_id,
                outcome: TurnOutcome::Completed,
                usage: Some(_),
            }
        ));
        assert_eq!(
            runtime.latest_reply(&locator).await.unwrap().as_deref(),
            Some("completed")
        );
        runtime.steer(&locator).await.unwrap();
        runtime.notice(&locator).await.unwrap();

        let session_path = runtime.session_path(agent_id, &locator);
        let stored = fs::read_to_string(&session_path).unwrap();
        assert!(stored.contains("completed"));
        assert!(!stored.contains("provider-secret"));
        let mut resumed = AgentRuntime::new(
            AgentConfig {
                computer_home,
                provider: agent_config(&api_base).provider,
                sandbox: SandboxConfig::default(),
                context: std::sync::Arc::new(IdentityContext),
            },
            Vec::new(),
        );
        assert!(resumed.resume_session(agent_id, &locator).await.unwrap());
        resumed.delete_session(&locator).await.unwrap();
        assert!(!session_path.exists());
    }

    #[tokio::test]
    async fn agent_interrupt_reports_interrupted_completion() {
        let directory = tempfile::tempdir().unwrap();
        let computer_home = directory.path().join("computer");
        let agent_id = Uuid::now_v7();
        let agent_home = computer_home.join("agents").join(agent_id.to_string());
        for relative in ["workspace", "memory", "runs", "drivers/builtin"] {
            fs::create_dir_all(agent_home.join(relative)).unwrap();
        }
        let mut config = agent_config("http://127.0.0.1:9/v1");
        config.computer_home = computer_home;
        let mut runtime = AgentRuntime::new(config, Vec::new());
        let locator = runtime.create_session(agent_id).await.unwrap();
        let run_id = Uuid::now_v7();
        runtime
            .start_turn(run_id, &locator, run_request(agent_id))
            .await
            .unwrap();

        runtime.interrupt(&locator).await.unwrap();

        assert_eq!(
            runtime.poll_completions().await.unwrap(),
            vec![Completion {
                run_id,
                agent_id,
                outcome: TurnOutcome::Interrupted,
                usage: None,
            }]
        );
    }

    #[tokio::test]
    async fn provision_creates_agent_home_and_primary_memory_without_overwriting() {
        let directory = tempfile::tempdir().unwrap();
        let mut config = agent_config("http://127.0.0.1:9/v1");
        config.computer_home = directory.path().join("computer");
        let runtime = AgentRuntime::new(config, Vec::new());
        let agent_id = Uuid::now_v7();

        runtime
            .provision(agent_id, "Telegram Agent", "Help the user")
            .await
            .unwrap();

        let home = runtime.agent_home(agent_id);
        for relative in ["workspace", "memory", "runs", "drivers/builtin"] {
            assert!(home.join(relative).is_dir());
        }
        assert_eq!(
            String::from_utf8(
                runtime
                    .read_memory(agent_id, PRIMARY_MEMORY_PATH)
                    .await
                    .unwrap()
            )
            .unwrap(),
            "# Telegram Agent\n\n## Role\n\nHelp the user\n\n## Key Knowledge\n\n- None recorded yet.\n\n## Active Context\n\n- Current focus: No active work recorded.\n"
        );
        runtime
            .write_memory(agent_id, PRIMARY_MEMORY_PATH, b"# agent-maintained")
            .await
            .unwrap();
        runtime
            .provision(agent_id, "Telegram Agent", "Help the user")
            .await
            .unwrap();
        assert_eq!(
            runtime
                .read_memory(agent_id, PRIMARY_MEMORY_PATH)
                .await
                .unwrap(),
            b"# agent-maintained"
        );
    }

    struct EchoPlugin;

    #[async_trait]
    impl AgentPlugin for EchoPlugin {
        fn name(&self) -> &str {
            "echo"
        }

        fn contract(&self) -> String {
            "Plugin contract: echo returns the value argument.".into()
        }

        fn tools(&self) -> Vec<ToolDef> {
            vec![ToolDef {
                name: "echo".into(),
                description: "Echo a value".into(),
                parameters: serde_json::json!({"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}),
            }]
        }

        async fn run_tool(
            &self,
            _context: &PluginContext,
            name: &str,
            args: &Value,
        ) -> anyhow::Result<String> {
            assert_eq!(name, "echo");
            Ok(format!("echoed {}", args["value"]))
        }
    }

    #[tokio::test]
    async fn plugin_tools_run_inside_the_agent_loop() {
        let api_base = serve_scripted(vec![
            concat!(
                "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"value\\\":\\\"hi\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n",
                "data: [DONE]\n\n"
            ),
            concat!(
                "data: {\"choices\":[{\"delta\":{\"content\":\"echoed hi\"},\"finish_reason\":\"stop\"}]}\n\n",
                "data: [DONE]\n\n"
            ),
        ])
        .await;

        let directory = tempfile::tempdir().unwrap();
        let computer_home = directory.path().join("computer");
        let agent_id = Uuid::now_v7();
        let agent_home = computer_home.join("agents").join(agent_id.to_string());
        for relative in ["workspace", "memory", "runs", "drivers/builtin"] {
            fs::create_dir_all(agent_home.join(relative)).unwrap();
        }
        let mut config = agent_config(&api_base);
        config.computer_home = computer_home;
        let mut runtime = AgentRuntime::new(config, vec![Arc::new(EchoPlugin)]);
        let locator = runtime.create_session(agent_id).await.unwrap();
        let run_id = Uuid::now_v7();

        runtime
            .start_turn(run_id, &locator, run_request(agent_id))
            .await
            .unwrap();
        let outcome = loop {
            let completions = runtime.poll_completions().await.unwrap();
            if let Some(completion) = completions.into_iter().next() {
                break completion.outcome;
            }
            tokio::task::yield_now().await;
        };
        assert_eq!(outcome, TurnOutcome::Completed);

        let session_path = runtime.session_path(agent_id, &locator);
        let stored = fs::read_to_string(session_path).unwrap();
        assert!(stored.contains("echoed hi"));
    }
}
