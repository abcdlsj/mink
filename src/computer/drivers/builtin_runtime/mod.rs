mod config;
mod engine;
mod provider;
mod session;
mod tool_executor;
mod types;
mod workspace;

use std::{
    collections::{BTreeMap, VecDeque},
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
    computer::{
        adapters::sandbox::SandboxAdapter,
        application::{
            ApplicationError,
            ports::{DriverCompletion, DriverTurnOutcome, ProcessEvidence, SteerOutcome},
        },
        core::{
            home::LocalAgent,
            input::{ClaimedItemInput, RunInput},
        },
    },
    config::ComputerConfig,
    ids::{AgentId, RunId},
};

use self::{
    config::BuiltinProviderConfig,
    engine::{Engine, Turn},
    provider::OpenAiProvider,
    session::Session,
    tool_executor::{ToolExecutor, ToolRunner},
    types::{Message, ToolDef},
    workspace::{agent_rooted_path, collect_shell_output, edit_utf8, read_utf8, write_utf8},
};
use super::contract::StructuredProviderClient;

const MAX_TOOL_OUTPUT_BYTES: usize = 1024 * 1024;

pub(super) struct BuiltinRuntimeClient {
    computer_home: PathBuf,
    socket_path: PathBuf,
    provider: Option<BuiltinProviderConfig>,
    sessions: BTreeMap<String, AgentId>,
    turns: BTreeMap<RunId, BuiltinTurn>,
    completions: VecDeque<DriverCompletion>,
}

struct BuiltinTurn {
    locator: String,
    task: JoinHandle<DriverTurnOutcome>,
}

impl BuiltinRuntimeClient {
    pub(super) fn new(
        computer_home: PathBuf,
        config: &ComputerConfig,
    ) -> Result<Self, ApplicationError> {
        let provider = config::load(config).map_err(|_| ApplicationError::DriverUnavailable)?;
        let socket_path = crate::config::runtime_dir_for(&computer_home).join("daemon.sock");
        Ok(Self {
            computer_home,
            socket_path,
            provider,
            sessions: BTreeMap::new(),
            turns: BTreeMap::new(),
            completions: VecDeque::new(),
        })
    }

    fn agent_home(&self, agent_id: AgentId) -> PathBuf {
        self.computer_home.join("agents").join(agent_id.to_string())
    }

    fn session_path(&self, agent_id: AgentId, locator: &str) -> PathBuf {
        self.agent_home(agent_id)
            .join("drivers/builtin/sessions")
            .join(format!("{locator}.json"))
    }

    async fn load_session(
        &self,
        agent_id: AgentId,
        locator: &str,
    ) -> Result<Session, ApplicationError> {
        let bytes = tokio::fs::read(self.session_path(agent_id, locator))
            .await
            .map_err(|_| ApplicationError::SessionLost)?;
        serde_json::from_slice(&bytes).map_err(|_| ApplicationError::SessionLost)
    }

    fn owner_for_locator(&self, locator: &str) -> Result<AgentId, ApplicationError> {
        self.sessions
            .get(locator)
            .copied()
            .ok_or(ApplicationError::SessionLost)
    }
}

#[async_trait(?Send)]
impl StructuredProviderClient for BuiltinRuntimeClient {
    async fn validate(&mut self, agent: &LocalAgent) -> Result<(), ApplicationError> {
        if self.provider.is_none() {
            return Err(ApplicationError::DriverUnavailable);
        }
        let home = self.agent_home(agent.agent_id);
        if !home.join("workspace").is_dir()
            || !home.join("memory").is_dir()
            || !home.join("runs").is_dir()
            || !home.join("drivers/builtin").is_dir()
        {
            return Err(ApplicationError::DriverUnavailable);
        }
        SandboxAdapter::validate()?;
        let mut command = SandboxAdapter::command(
            Path::new("/bin/sh"),
            &home,
            &home.join("drivers/builtin"),
            &self.socket_path,
            "validation-token",
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
        .map_err(|_| ApplicationError::DriverUnavailable)?
        .map_err(|_| ApplicationError::DriverUnavailable)?;
        if status.success() {
            Ok(())
        } else {
            Err(ApplicationError::DriverUnavailable)
        }
    }

    async fn create_session(&mut self, agent_id: AgentId) -> Result<String, ApplicationError> {
        let locator = Uuid::now_v7().to_string();
        let directory = self.agent_home(agent_id).join("drivers/builtin/sessions");
        tokio::fs::create_dir_all(&directory)
            .await
            .map_err(|_| ApplicationError::Internal)?;
        restrict_directory(&directory).await?;
        persist_session(
            &directory.join(format!("{locator}.json")),
            &Session::default(),
        )
        .await?;
        self.sessions.insert(locator.clone(), agent_id);
        Ok(locator)
    }

    async fn resume_session(
        &mut self,
        agent_id: AgentId,
        locator: &str,
    ) -> Result<bool, ApplicationError> {
        match self.load_session(agent_id, locator).await {
            Ok(_) => {
                self.sessions.insert(locator.to_owned(), agent_id);
                Ok(true)
            }
            Err(ApplicationError::SessionLost) => Ok(false),
            Err(error) => Err(error),
        }
    }

    async fn start_turn(
        &mut self,
        run_id: RunId,
        locator: &str,
        input: &RunInput,
        run_token: &str,
    ) -> Result<(), ApplicationError> {
        let agent_id = self.owner_for_locator(locator)?;
        if input.agent.agent_id != agent_id || self.turns.contains_key(&run_id) {
            return Err(ApplicationError::Conflict);
        }
        let provider = self
            .provider
            .clone()
            .ok_or(ApplicationError::DriverUnavailable)?
            .into_provider_config()
            .with_prompt_cache_key(input.content_hash());
        let session = self.load_session(agent_id, locator).await?;
        let agent_home = self.agent_home(agent_id);
        let socket_path = self.socket_path.clone();
        let locator_owned = locator.to_owned();
        let session_path = self.session_path(agent_id, locator);
        let run_token = run_token.to_owned();
        let input_owned = input.clone();
        let task = tokio::spawn(async move {
            let tools = Arc::new(BuiltinTools {
                agent_home,
                socket_path,
                run_token,
            });
            let engine = match OpenAiProvider::new(provider) {
                Ok(provider) => Engine::new(
                    Arc::new(provider),
                    ToolExecutor::new(tools),
                    system_messages(&input_owned),
                    tool_definitions(),
                ),
                Err(_) => return DriverTurnOutcome::Failed,
            };
            let turn = match serde_json::to_string(&input_owned) {
                Ok(input) => Turn {
                    input: format!(
                        "Process this Sumi Run. The JSON is the current authoritative run_context and work_context.\n{input}"
                    ),
                    blocked_tools: Default::default(),
                },
                Err(_) => return DriverTurnOutcome::Failed,
            };
            let mut session = session;
            let (events, mut event_rx) = tokio::sync::mpsc::channel(64);
            tokio::spawn(async move {
                while let Some(event) = event_rx.recv().await {
                    match event {
                        tool_executor::ToolEvent::Started { tool } => {
                            tracing::debug!(tool, "Builtin tool started");
                        }
                        tool_executor::ToolEvent::Finished { tool } => {
                            tracing::debug!(tool, "Builtin tool finished");
                        }
                        tool_executor::ToolEvent::Failed { tool } => {
                            tracing::warn!(tool, "Builtin tool failed");
                        }
                    }
                }
            });
            let outcome = if engine.run(&turn, &mut session, &events, None).await.is_ok() {
                DriverTurnOutcome::Completed
            } else {
                DriverTurnOutcome::Failed
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
                return DriverTurnOutcome::Failed;
            }
            outcome
        });
        self.turns.insert(
            run_id,
            BuiltinTurn {
                locator: locator_owned,
                task,
            },
        );
        Ok(())
    }

    async fn steer(
        &mut self,
        locator: &str,
        _: &ClaimedItemInput,
    ) -> Result<SteerOutcome, ApplicationError> {
        self.owner_for_locator(locator)?;
        Ok(SteerOutcome::Unsupported)
    }

    async fn notice(&mut self, locator: &str) -> Result<(), ApplicationError> {
        self.owner_for_locator(locator).map(|_| ())
    }

    async fn interrupt(&mut self, locator: &str) -> Result<(), ApplicationError> {
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
            self.completions.push_back(DriverCompletion {
                run_id,
                outcome: DriverTurnOutcome::Interrupted,
            });
        }
        Ok(())
    }

    async fn delete_session(&mut self, locator: &str) -> Result<(), ApplicationError> {
        let agent_id = self.owner_for_locator(locator)?;
        self.interrupt(locator).await?;
        match tokio::fs::remove_file(self.session_path(agent_id, locator)).await {
            Ok(()) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(_) => return Err(ApplicationError::Internal),
        }
        self.sessions.remove(locator);
        Ok(())
    }

    async fn process_evidence(
        &mut self,
        run_id: RunId,
    ) -> Result<ProcessEvidence, ApplicationError> {
        Ok(if self.turns.contains_key(&run_id) {
            ProcessEvidence::Controlled
        } else {
            ProcessEvidence::Lost
        })
    }

    async fn poll_completions(&mut self) -> Result<Vec<DriverCompletion>, ApplicationError> {
        let finished = self
            .turns
            .iter()
            .filter_map(|(run_id, turn)| turn.task.is_finished().then_some(*run_id))
            .collect::<Vec<_>>();
        for run_id in finished {
            let turn = self
                .turns
                .remove(&run_id)
                .ok_or(ApplicationError::Internal)?;
            let outcome = turn.task.await.unwrap_or(DriverTurnOutcome::Failed);
            self.completions
                .push_back(DriverCompletion { run_id, outcome });
        }
        Ok(self.completions.drain(..).collect())
    }
}

fn system_messages(input: &RunInput) -> Vec<Message> {
    vec![
        Message::cacheable_system(input.global_contract.clone()),
        Message::system(format!(
            "Agent identity: {}\nRole revision: {}\nRole: {}\nMemory entry: {}",
            input.agent.identity,
            input.agent.role_revision,
            input.agent.role,
            input.agent.memory_entry
        )),
    ]
}

fn tool_definitions() -> Vec<ToolDef> {
    vec![
        tool_definition(
            "read",
            "Read a UTF-8 file from workspace/ or memory/.",
            &["path"],
        ),
        tool_definition(
            "write",
            "Write a UTF-8 file inside workspace/ or memory/.",
            &["path", "content"],
        ),
        tool_definition(
            "edit",
            "Replace one exact text occurrence inside workspace/ or memory/.",
            &["path", "old_text", "new_text"],
        ),
        tool_definition(
            "bash",
            "Run a sandboxed shell command in the Agent workspace.",
            &["command"],
        ),
    ]
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

struct BuiltinTools {
    agent_home: PathBuf,
    socket_path: PathBuf,
    run_token: String,
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
            Path::new("/bin/sh"),
            &self.agent_home,
            &self.agent_home.join("drivers/builtin"),
            &self.socket_path,
            &self.run_token,
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

async fn persist_session(path: &Path, session: &Session) -> Result<(), ApplicationError> {
    let encoded = serde_json::to_vec(session).map_err(|_| ApplicationError::Internal)?;
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
        .map_err(|_| ApplicationError::Internal)?;
    use tokio::io::AsyncWriteExt;
    file.write_all(&encoded)
        .await
        .map_err(|_| ApplicationError::Internal)?;
    file.sync_all()
        .await
        .map_err(|_| ApplicationError::Internal)?;
    drop(file);
    tokio::fs::rename(temporary, path)
        .await
        .map_err(|_| ApplicationError::Internal)
}

async fn restrict_directory(path: &Path) -> Result<(), ApplicationError> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        tokio::fs::set_permissions(path, std::fs::Permissions::from_mode(0o700))
            .await
            .map_err(|_| ApplicationError::Internal)?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::{fs, os::unix::fs::PermissionsExt};

    use tokio::io::{AsyncReadExt, AsyncWriteExt};

    use super::*;
    use crate::{
        computer::core::input::{AgentInput, RunContextInput, WorkInput},
        ids::{SpaceId, ThreadId},
    };

    #[tokio::test]
    async fn builtin_persists_and_resumes_provider_session_without_exposing_secret() {
        let listener = tokio::net::TcpListener::bind(("127.0.0.1", 0))
            .await
            .unwrap();
        let address = listener.local_addr().unwrap();
        let provider_task = tokio::spawn(async move {
            let (mut stream, _) = listener.accept().await.unwrap();
            let mut request = vec![0_u8; 8192];
            let read = stream.read(&mut request).await.unwrap();
            let request = String::from_utf8_lossy(&request[..read]);
            assert!(request.contains("authorization: Bearer provider-secret"));
            let body = concat!(
                "data: {\"choices\":[{\"delta\":{\"content\":\"completed\"},\"finish_reason\":\"stop\"}]}\n\n",
                "data: [DONE]\n\n"
            );
            let response = format!(
                "HTTP/1.1 200 OK\r\ncontent-type: text/event-stream\r\ncontent-length: {}\r\nconnection: close\r\n\r\n{}",
                body.len(),
                body
            );
            stream.write_all(response.as_bytes()).await.unwrap();
        });

        let directory = tempfile::tempdir().unwrap();
        let computer_home = directory.path().join("computer");
        let agent_id = AgentId::from_uuid(Uuid::now_v7());
        let agent_home = computer_home.join("agents").join(agent_id.to_string());
        for relative in ["workspace", "memory", "runs", "drivers/builtin"] {
            fs::create_dir_all(agent_home.join(relative)).unwrap();
        }
        let config = builtin_config(directory.path(), &format!("http://{address}/v1"));
        let mut client = BuiltinRuntimeClient::new(computer_home.clone(), &config).unwrap();
        let locator = client.create_session(agent_id).await.unwrap();
        let input = run_input(agent_id);
        let run_id = RunId::from_uuid(Uuid::now_v7());

        client
            .start_turn(run_id, &locator, &input, "run-secret")
            .await
            .unwrap();
        let completion = loop {
            let completions = client.poll_completions().await.unwrap();
            if let Some(completion) = completions.into_iter().next() {
                break completion;
            }
            tokio::task::yield_now().await;
        };
        assert_eq!(
            completion,
            DriverCompletion {
                run_id,
                outcome: DriverTurnOutcome::Completed,
            }
        );
        assert_eq!(
            client
                .steer(
                    &locator,
                    &ClaimedItemInput {
                        item_id: crate::ids::InboxItemId::from_uuid(Uuid::now_v7()),
                        task_id: None,
                        thread_id: input.context.focus_thread_id,
                        content: Some("new item".to_owned()),
                    },
                )
                .await
                .unwrap(),
            SteerOutcome::Unsupported
        );
        provider_task.await.unwrap();

        let session_path = client.session_path(agent_id, &locator);
        let stored = fs::read_to_string(&session_path).unwrap();
        assert!(stored.contains("completed"));
        assert!(!stored.contains("provider-secret"));
        let mut resumed = BuiltinRuntimeClient::new(computer_home, &config).unwrap();
        assert!(resumed.resume_session(agent_id, &locator).await.unwrap());
        resumed.delete_session(&locator).await.unwrap();
        assert!(!session_path.exists());
    }

    #[tokio::test]
    async fn builtin_interrupt_reports_interrupted_completion() {
        let directory = tempfile::tempdir().unwrap();
        let computer_home = directory.path().join("computer");
        let agent_id = AgentId::from_uuid(Uuid::now_v7());
        let agent_home = computer_home.join("agents").join(agent_id.to_string());
        for relative in ["workspace", "memory", "runs", "drivers/builtin"] {
            fs::create_dir_all(agent_home.join(relative)).unwrap();
        }
        let config = builtin_config(directory.path(), "http://127.0.0.1:9/v1");
        let mut client = BuiltinRuntimeClient::new(computer_home, &config).unwrap();
        let locator = client.create_session(agent_id).await.unwrap();
        let run_id = RunId::from_uuid(Uuid::now_v7());
        client
            .start_turn(run_id, &locator, &run_input(agent_id), "run-secret")
            .await
            .unwrap();

        client.interrupt(&locator).await.unwrap();

        assert_eq!(
            client.poll_completions().await.unwrap(),
            vec![DriverCompletion {
                run_id,
                outcome: DriverTurnOutcome::Interrupted,
            }]
        );
    }

    fn builtin_config(root: &Path, base_url: &str) -> ComputerConfig {
        let settings = root.join(format!("settings-{}.json", Uuid::now_v7()));
        let models = root.join(format!("models-{}.json", Uuid::now_v7()));
        let auth = root.join(format!("auth-{}.json", Uuid::now_v7()));
        fs::write(
            &settings,
            r#"{"defaultProvider":"local","defaultModel":"test-model"}"#,
        )
        .unwrap();
        fs::write(
            &models,
            format!(
                r#"{{"local":{{"models":[{{"id":"test-model","api":"openai-completions","baseUrl":"{base_url}"}}]}}}}"#
            ),
        )
        .unwrap();
        fs::write(
            &auth,
            r#"{"local":{"type":"api_key","key":"provider-secret"}}"#,
        )
        .unwrap();
        fs::set_permissions(&auth, fs::Permissions::from_mode(0o600)).unwrap();
        ComputerConfig {
            builtin_settings_source: Some(settings),
            builtin_models_source: Some(models),
            builtin_auth_source: Some(auth),
            ..ComputerConfig::default()
        }
    }

    fn run_input(agent_id: AgentId) -> RunInput {
        let thread_id = ThreadId::from_uuid(Uuid::now_v7());
        RunInput {
            global_contract: "Use Sumi capabilities for collaboration facts".to_owned(),
            agent: AgentInput {
                agent_id,
                space_id: SpaceId::from_uuid(Uuid::now_v7()),
                identity: "Builtin".to_owned(),
                role_revision: 1,
                role: "Complete the current Run".to_owned(),
                memory_entry: "memory/".to_owned(),
            },
            work: WorkInput {
                task: None,
                linked_thread_ids: vec![thread_id],
                public_result_message_id: None,
            },
            context: RunContextInput {
                focus_thread_id: thread_id,
                message_snapshot_sequence: 1,
                focus_messages: vec!["message".to_owned()],
                claimed_items: Vec::new(),
            },
        }
    }
}
