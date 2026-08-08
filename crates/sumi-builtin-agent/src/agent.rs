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
    plugin::{AgentPlugin, PluginContext},
    prompt,
    provider::OpenAiProvider,
    sandbox::SandboxAdapter,
    session::Session,
    tool_executor::{ToolEvent, ToolExecutor, ToolRunner},
    types::{Attachment, Message, ToolDef},
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

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum TurnOutcome {
    Completed,
    Failed,
    Interrupted,
}

/// One agent turn request. `input` is the model-facing JSON view of the run;
/// the runtime serializes it into the turn instruction.
#[derive(Clone)]
pub struct TurnRequest {
    pub product_contract: String,
    pub driver_contract: String,
    pub identity: String,
    pub role: String,
    pub input: Value,
    pub content_hash: String,
    pub attachments: Vec<Attachment>,
    pub blocked_tools: HashMap<String, String>,
    /// Extra environment variables for sandboxed shell subprocesses.
    pub sandbox_environment: BTreeMap<String, String>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Completion {
    pub run_id: Uuid,
    pub outcome: TurnOutcome,
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
    task: JoinHandle<TurnOutcome>,
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
        let provider = self.config.provider.clone().with_prompt_cache_key(format!(
            "{}-{}",
            request.content_hash,
            prompt::stable_hash(&request.product_contract, &request.driver_contract)
        ));
        let session = self.load_session(agent_id, locator).await?;
        let agent_home = self.agent_home(agent_id);
        let session_path = self.session_path(agent_id, locator);
        let sandbox_config = self.config.sandbox.clone();
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
                ),
                Err(_) => return TurnOutcome::Failed,
            };
            let turn = match serde_json::to_string(&request_owned.input) {
                Ok(input) => Turn {
                    input: prompt::turn_instruction(&input),
                    attachments: request_owned.attachments.clone(),
                    blocked_tools: request_owned.blocked_tools.clone(),
                },
                Err(_) => return TurnOutcome::Failed,
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
            let compacted_before = session.compacted_through();
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
            let compacted_after = session.compacted_through();
            if compacted_after > compacted_before {
                tracing::info!(
                    %run_id,
                    compacted_messages = compacted_after - compacted_before,
                    retained_messages = session.messages.len() - compacted_after,
                    "Builtin provider context compacted"
                );
            }
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
                return TurnOutcome::Failed;
            }
            outcome
        });
        self.turns.insert(
            run_id,
            BuiltinTurn {
                locator: locator.to_owned(),
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
                outcome: TurnOutcome::Interrupted,
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
            let outcome = turn.task.await.unwrap_or(TurnOutcome::Failed);
            self.completions.push_back(Completion { run_id, outcome });
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
    let (stable, dynamic) = prompt::system_messages(
        &request.product_contract,
        &request.driver_contract,
        &request.identity,
        &request.role,
        &plugin_contract,
    );
    vec![
        Message::cacheable_system(stable),
        Message::system(dynamic),
        Message::system(builtin_tool_contract()),
    ]
}

fn tool_definitions(plugins: &[Arc<dyn AgentPlugin>]) -> Vec<ToolDef> {
    let mut definitions = vec![
        tool_definition(
            "read",
            "Read a UTF-8 file from workspace/ or memory/. Use paths like `workspace/<path>` or `memory/<path>` (for example `memory/MEMORY.md`).",
            &["path"],
        ),
        tool_definition(
            "write",
            "Write a UTF-8 file inside workspace/ or memory/. Use paths like `workspace/<path>` or `memory/<path>` (for example `memory/notes/<topic>.md`).",
            &["path", "content"],
        ),
        tool_definition(
            "edit",
            "Replace one exact text occurrence inside workspace/ or memory/. Use paths like `workspace/<path>` or `memory/<path>`.",
            &["path", "old_text", "new_text"],
        ),
        tool_definition(
            "bash",
            "Run a sandboxed shell command from the Agent Home root. Paths are `workspace/<path>` or `memory/<path>`; shell writes are allowed only under workspace/ and $TMPDIR (runs/), never /tmp.",
            &["command"],
        ),
    ];
    for plugin in plugins {
        definitions.extend(plugin.tools());
    }
    definitions
}

fn builtin_tool_contract() -> String {
    concat!(
        "Builtin `read`, `write`, and `edit` paths start with `workspace/` or `memory/`: for example `memory/MEMORY.md`, `memory/notes/<topic>.md`, or `workspace/role.md`.\n",
        "The bash shell starts at the Agent Home root, so the same `workspace/...` and `memory/...` paths work in shell commands and CLI file arguments.\n",
        "Shell writes are allowed only under `workspace/` and `$TMPDIR` (the `runs/` directory); `/tmp` and other absolute paths are denied.\n",
    )
    .into()
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
    use crate::{config::SandboxConfig, provider::ProviderConfig, types::ToolDef};

    fn agent_config(api_base: &str) -> AgentConfig {
        AgentConfig {
            computer_home: std::env::temp_dir().join("unused"),
            provider: ProviderConfig::openai("provider-secret", "test-model".into())
                .with_base_url(api_base.to_owned()),
            sandbox: SandboxConfig::default(),
        }
    }

    fn run_request(agent_id: Uuid) -> TurnRequest {
        TurnRequest {
            product_contract: "Portable agent contract".to_owned(),
            driver_contract: "Use tools".to_owned(),
            identity: "Builtin".to_owned(),
            role: "Complete the current Run".to_owned(),
            input: serde_json::json!({
                "agent": { "identity": "Builtin", "role": "Complete the current Run" },
                "reference": { "agent_id": agent_id },
                "run_context": { "focus_messages": [] }
            }),
            content_hash: "hash".to_owned(),
            attachments: Vec::new(),
            blocked_tools: HashMap::new(),
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
        assert_eq!(
            completion,
            Completion {
                run_id,
                outcome: TurnOutcome::Completed,
            }
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
                outcome: TurnOutcome::Interrupted,
            }]
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
