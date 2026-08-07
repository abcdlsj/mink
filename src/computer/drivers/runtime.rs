use std::{
    collections::{BTreeMap, VecDeque},
    ffi::OsString,
    path::{Path, PathBuf},
    process::Stdio,
    time::Duration,
};

use async_trait::async_trait;
use serde_json::{Value, json};
use tokio::{
    io::{AsyncBufReadExt, AsyncWriteExt, BufReader, Lines},
    process::{Child, ChildStdin, ChildStdout, Command},
};

use crate::{
    computer::{
        application::{
            ApplicationError,
            ports::{DriverCompletion, DriverTurnOutcome, ProcessEvidence, SteerOutcome},
        },
        core::{
            home::LocalAgent,
            input::{DispatchedItemInput, RunInput},
        },
    },
    config::daemon_socket_path,
    ids::{AgentId, RunId},
};

use super::{agent_home, contract::StructuredProviderClient, prompt};
use crate::computer::application::capability::CapabilityService;

const RPC_TIMEOUT: Duration = Duration::from_secs(30);
const MAX_TURN_ATTEMPTS: u8 = 3;

pub(super) struct CodexRuntimeClient {
    computer_home: PathBuf,
    executable: OsString,
    driver_secret: [u8; 32],
    processes: BTreeMap<AgentId, CodexProcess>,
    locator_owners: BTreeMap<String, AgentId>,
    run_owners: BTreeMap<RunId, AgentId>,
    run_inputs: BTreeMap<RunId, CodexRunInput>,
}

#[derive(Clone)]
struct CodexRunInput {
    locator: String,
    input: RunInput,
    attempts: u8,
}

impl CodexRuntimeClient {
    pub(super) fn new(computer_home: PathBuf, driver_secret: [u8; 32]) -> Self {
        let executable = std::env::var_os("SUMI_CODEX_COMMAND")
            .filter(|value| !value.is_empty())
            .unwrap_or_else(|| OsString::from("codex"));
        Self::with_executable(computer_home, executable, driver_secret)
    }

    fn with_executable(
        computer_home: PathBuf,
        executable: OsString,
        driver_secret: [u8; 32],
    ) -> Self {
        Self {
            computer_home,
            executable,
            driver_secret,
            processes: BTreeMap::new(),
            locator_owners: BTreeMap::new(),
            run_owners: BTreeMap::new(),
            run_inputs: BTreeMap::new(),
        }
    }

    fn agent_home(&self, agent_id: AgentId) -> PathBuf {
        agent_home(&self.computer_home, agent_id)
    }

    async fn process(&mut self, agent_id: AgentId) -> Result<&mut CodexProcess, ApplicationError> {
        self.ensure_process(agent_id).await?;
        self.processes
            .get_mut(&agent_id)
            .ok_or(ApplicationError::Internal)
    }

    fn owner_for_locator(&self, locator: &str) -> Result<AgentId, ApplicationError> {
        self.locator_owners
            .get(locator)
            .copied()
            .ok_or(ApplicationError::SessionLost)
    }

    async fn prepare_for_run(&mut self, agent_id: AgentId) -> Result<(), ApplicationError> {
        self.ensure_process(agent_id).await?;
        Ok(())
    }

    /// Starts or reuses the app-server for one Agent. The process is kept alive across Runs because
    /// its environment carries the Agent's stable Driver token; a restart is needed only when the
    /// process is gone.
    async fn ensure_process(&mut self, agent_id: AgentId) -> Result<(), ApplicationError> {
        let alive = self
            .processes
            .get_mut(&agent_id)
            .is_some_and(|process| process.is_running());
        if alive {
            return Ok(());
        }
        self.processes.remove(&agent_id);
        let agent_home = self.agent_home(agent_id);
        let socket_path = daemon_socket_path(&self.computer_home);
        let driver_token = CapabilityService::driver_token(&self.driver_secret, agent_id);
        let process = CodexProcess::spawn(
            self.executable.clone(),
            agent_home,
            Some((&socket_path, &driver_token)),
        )
        .await?;
        self.processes.insert(agent_id, process);
        Ok(())
    }

    async fn start_turn_once(
        &mut self,
        run_id: RunId,
        locator: &str,
        input: &RunInput,
    ) -> Result<(), ApplicationError> {
        let agent_id = input.agent.agent_id;
        if self.owner_for_locator(locator)? != agent_id {
            return Err(ApplicationError::SessionLost);
        }
        let encoded =
            serde_json::to_string(&input.model_view()).map_err(|_| ApplicationError::Internal)?;
        let result = self
            .process(agent_id)
            .await?
            .request(
                "turn/start",
                json!({
                    "threadId": locator,
                    "input": [{
                        "type": "text",
                        "text": prompt::turn_instruction(&encoded)
                    }],
                    "sandboxPolicy": {
                        "type": "dangerFullAccess"
                    }
                }),
            )
            .await?;
        let turn_id = result
            .pointer("/turn/id")
            .and_then(Value::as_str)
            .ok_or(ApplicationError::DriverUnavailable)?
            .to_owned();
        self.process(agent_id)
            .await?
            .active_turns
            .insert(locator.to_owned(), turn_id);
        self.process(agent_id)
            .await?
            .active_runs
            .insert(locator.to_owned(), run_id);
        self.run_owners.insert(run_id, agent_id);
        Ok(())
    }
}

struct CodexProcess {
    child: Child,
    stdin: ChildStdin,
    stdout: Lines<BufReader<ChildStdout>>,
    next_request_id: u64,
    active_turns: BTreeMap<String, String>,
    active_runs: BTreeMap<String, RunId>,
    failure_reasons: BTreeMap<RunId, String>,
    completions: VecDeque<DriverCompletion>,
}

impl CodexProcess {
    async fn spawn(
        executable: OsString,
        agent_home: PathBuf,
        capability: Option<(&std::path::Path, &str)>,
    ) -> Result<Self, ApplicationError> {
        let codex_home = agent_home.join("drivers/codex");
        let workspace = agent_home.join("workspace");
        if !codex_home.is_dir() || !workspace.is_dir() {
            return Err(ApplicationError::DriverUnavailable);
        }
        let mut command = Command::new(executable);
        command
            .arg("--dangerously-bypass-approvals-and-sandbox")
            .arg("app-server")
            .arg("--listen")
            .arg("stdio://")
            .arg("-c")
            .arg("shell_environment_policy.inherit=\"all\"")
            .arg("-c")
            .arg("shell_environment_policy.ignore_default_excludes=true")
            .arg("-c")
            .arg(
                "shell_environment_policy.include_only=[\"PATH\",\"HOME\",\"SUMI_SOCKET\",\"SUMI_DRIVER_TOKEN\"]",
            )
            .current_dir(&workspace)
            .env_clear()
            .env("CODEX_HOME", &codex_home)
            .env("HOME", &agent_home)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .kill_on_drop(true);
        if let Some((key, value)) = provider_environment(&codex_home).await? {
            command.env(key, value);
        }
        let mut executable_paths = Vec::new();
        if let Ok(current_executable) = std::env::current_exe()
            && let Some(parent) = current_executable.parent()
        {
            executable_paths.push(parent.to_owned());
        }
        if let Some(path) = std::env::var_os("PATH") {
            executable_paths.extend(std::env::split_paths(&path));
        }
        let executable_path = std::env::join_paths(executable_paths)
            .map_err(|_| ApplicationError::DriverUnavailable)?;
        command.env("PATH", executable_path);
        if let Some(language) = std::env::var_os("LANG") {
            command.env("LANG", language);
        }
        if let Some((socket, driver_token)) = capability {
            command
                .env("SUMI_SOCKET", socket)
                .env("SUMI_DRIVER_TOKEN", driver_token);
        }
        let mut child = command
            .spawn()
            .map_err(|_| ApplicationError::DriverUnavailable)?;
        let stdin = child.stdin.take().ok_or(ApplicationError::Internal)?;
        let stdout = child.stdout.take().ok_or(ApplicationError::Internal)?;
        let mut process = Self {
            child,
            stdin,
            stdout: BufReader::new(stdout).lines(),
            next_request_id: 1,
            active_turns: BTreeMap::new(),
            active_runs: BTreeMap::new(),
            failure_reasons: BTreeMap::new(),
            completions: VecDeque::new(),
        };
        process
            .request(
                "initialize",
                json!({
                    "clientInfo": {
                        "name": "sumi",
                        "title": "Sumi Computer",
                        "version": env!("CARGO_PKG_VERSION")
                    },
                    "capabilities": {
                        "experimentalApi": false
                    }
                }),
            )
            .await?;
        process.notify("initialized", None).await?;
        Ok(process)
    }

    async fn notify(
        &mut self,
        method: &str,
        params: Option<Value>,
    ) -> Result<(), ApplicationError> {
        let mut notification = json!({"method": method});
        if let Some(params) = params {
            notification["params"] = params;
        }
        self.write_message(&notification).await
    }

    async fn request(&mut self, method: &str, params: Value) -> Result<Value, RpcFailure> {
        let request_id = self.next_request_id;
        self.next_request_id = self
            .next_request_id
            .checked_add(1)
            .ok_or(RpcFailure::Transport)?;
        self.write_message(&json!({
            "id": request_id,
            "method": method,
            "params": params
        }))
        .await
        .map_err(|_| RpcFailure::Transport)?;

        loop {
            let line = tokio::time::timeout(RPC_TIMEOUT, self.stdout.next_line())
                .await
                .map_err(|_| RpcFailure::Transport)?
                .map_err(|_| RpcFailure::Transport)?
                .ok_or(RpcFailure::Transport)?;
            let message: Value = serde_json::from_str(&line).map_err(|_| RpcFailure::Transport)?;
            self.record_notification(&message);
            if message.get("id").and_then(Value::as_u64) != Some(request_id) {
                continue;
            }
            if message.get("error").is_some() {
                return Err(RpcFailure::Remote);
            }
            return message.get("result").cloned().ok_or(RpcFailure::Transport);
        }
    }

    fn record_notification(&mut self, message: &Value) {
        if message.get("method").and_then(Value::as_str) != Some("turn/completed") {
            return;
        }
        let Some(thread_id) = message.pointer("/params/threadId").and_then(Value::as_str) else {
            return;
        };
        self.active_turns.remove(thread_id);
        let Some(run_id) = self.active_runs.remove(thread_id) else {
            return;
        };
        let status = message
            .pointer("/params/turn/status")
            .and_then(Value::as_str);
        let outcome = match status {
            Some("completed") => DriverTurnOutcome::Completed,
            Some("interrupted") => DriverTurnOutcome::Interrupted,
            _ => DriverTurnOutcome::Failed,
        };
        if outcome == DriverTurnOutcome::Failed {
            let reason = message
                .pointer("/params/turn/error")
                .or_else(|| message.pointer("/params/error"))
                .map(|value| {
                    value
                        .as_str()
                        .map_or_else(|| value.to_string(), ToOwned::to_owned)
                })
                .filter(|value| !value.is_empty());
            if let Some(reason) = reason {
                self.failure_reasons.insert(run_id, reason);
            }
        }
        self.completions
            .push_back(DriverCompletion { run_id, outcome });
    }

    async fn write_message(&mut self, message: &Value) -> Result<(), ApplicationError> {
        let mut encoded = serde_json::to_vec(message).map_err(|_| ApplicationError::Internal)?;
        encoded.push(b'\n');
        self.stdin
            .write_all(&encoded)
            .await
            .map_err(|_| ApplicationError::DriverUnavailable)?;
        self.stdin
            .flush()
            .await
            .map_err(|_| ApplicationError::DriverUnavailable)
    }

    fn is_running(&mut self) -> bool {
        matches!(self.child.try_wait(), Ok(None))
    }

    async fn poll_notifications(&mut self) -> Result<(), ApplicationError> {
        loop {
            let line = match tokio::time::timeout(Duration::from_millis(1), self.stdout.next_line())
                .await
            {
                Err(_) => break,
                Ok(Ok(Some(line))) => line,
                Ok(Ok(None)) | Ok(Err(_)) => return Err(ApplicationError::DriverUnavailable),
            };
            let message: Value =
                serde_json::from_str(&line).map_err(|_| ApplicationError::DriverUnavailable)?;
            self.record_notification(&message);
        }
        Ok(())
    }
}

#[derive(Clone, Copy)]
enum RpcFailure {
    Remote,
    Transport,
}

impl From<RpcFailure> for ApplicationError {
    fn from(_: RpcFailure) -> Self {
        ApplicationError::DriverUnavailable
    }
}

#[async_trait(?Send)]
impl StructuredProviderClient for CodexRuntimeClient {
    async fn validate(&mut self, agent: &LocalAgent) -> Result<(), ApplicationError> {
        let agent_home = self.agent_home(agent.agent_id);
        if !agent_home.join("workspace").is_dir() || !agent_home.join("drivers/codex").is_dir() {
            return Err(ApplicationError::DriverUnavailable);
        }
        let status = Command::new(&self.executable)
            .arg("--version")
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .status()
            .await
            .map_err(|_| ApplicationError::DriverUnavailable)?;
        if status.success() {
            Ok(())
        } else {
            Err(ApplicationError::DriverUnavailable)
        }
    }

    async fn create_session(&mut self, agent_id: AgentId) -> Result<String, ApplicationError> {
        self.prepare_for_run(agent_id).await?;
        let workspace = self.agent_home(agent_id).join("workspace");
        let result = self
            .process(agent_id)
            .await?
            .request(
                "thread/start",
                json!({
                    "cwd": workspace,
                    "approvalPolicy": "never",
                    "sandbox": "workspace-write",
                    "ephemeral": false,
                    "serviceName": "sumi"
                }),
            )
            .await?;
        let locator = result
            .pointer("/thread/id")
            .and_then(Value::as_str)
            .ok_or(ApplicationError::DriverUnavailable)?
            .to_owned();
        self.locator_owners.insert(locator.clone(), agent_id);
        Ok(locator)
    }

    async fn resume_session(
        &mut self,
        agent_id: AgentId,
        locator: &str,
    ) -> Result<bool, ApplicationError> {
        self.prepare_for_run(agent_id).await?;
        let result = self
            .process(agent_id)
            .await?
            .request("thread/resume", json!({"threadId": locator}))
            .await;
        match result {
            Ok(response)
                if response.pointer("/thread/id").and_then(Value::as_str) == Some(locator) =>
            {
                self.locator_owners.insert(locator.to_owned(), agent_id);
                Ok(true)
            }
            Ok(_) | Err(RpcFailure::Remote) => Ok(false),
            Err(RpcFailure::Transport) => Err(ApplicationError::DriverUnavailable),
        }
    }

    async fn start_turn(
        &mut self,
        run_id: RunId,
        locator: &str,
        input: &RunInput,
    ) -> Result<(), ApplicationError> {
        self.start_turn_once(run_id, locator, input).await?;
        self.run_inputs.insert(
            run_id,
            CodexRunInput {
                locator: locator.to_owned(),
                input: input.clone(),
                attempts: 1,
            },
        );
        Ok(())
    }

    async fn steer(
        &mut self,
        locator: &str,
        item: &DispatchedItemInput,
    ) -> Result<SteerOutcome, ApplicationError> {
        let agent_id = self.owner_for_locator(locator)?;
        let Some(turn_id) = self
            .process(agent_id)
            .await?
            .active_turns
            .get(locator)
            .cloned()
        else {
            return Ok(SteerOutcome::TooLate);
        };
        let encoded = serde_json::to_string(item).map_err(|_| ApplicationError::Internal)?;
        match self
            .process(agent_id)
            .await?
            .request(
                "turn/steer",
                json!({
                    "threadId": locator,
                    "expectedTurnId": turn_id,
                    "input": [{"type": "text", "text": format!("New Sumi Inbox Item:\n{encoded}")}]
                }),
            )
            .await
        {
            Ok(_) => Ok(SteerOutcome::Accepted),
            Err(RpcFailure::Remote) => Ok(SteerOutcome::TooLate),
            Err(RpcFailure::Transport) => Err(ApplicationError::DriverUnavailable),
        }
    }

    async fn notice(&mut self, _: &str) -> Result<(), ApplicationError> {
        Ok(())
    }

    async fn interrupt(&mut self, locator: &str) -> Result<(), ApplicationError> {
        let agent_id = self.owner_for_locator(locator)?;
        let Some(turn_id) = self
            .process(agent_id)
            .await?
            .active_turns
            .get(locator)
            .cloned()
        else {
            return Ok(());
        };
        let result = self
            .process(agent_id)
            .await?
            .request(
                "turn/interrupt",
                json!({"threadId": locator, "turnId": turn_id}),
            )
            .await;
        match result {
            Ok(_) | Err(RpcFailure::Remote) => {
                self.process(agent_id).await?.active_turns.remove(locator);
                Ok(())
            }
            Err(RpcFailure::Transport) => Err(ApplicationError::DriverUnavailable),
        }
    }

    async fn restart_agent(&mut self, agent_id: AgentId) -> Result<(), ApplicationError> {
        self.locator_owners.retain(|_, owner| *owner != agent_id);
        self.run_owners.retain(|_, owner| *owner != agent_id);
        self.run_inputs
            .retain(|_, input| input.input.agent.agent_id != agent_id);
        if let Some(mut process) = self.processes.remove(&agent_id) {
            let _ = process.child.kill().await;
            let _ = process.child.wait().await;
        }
        Ok(())
    }

    async fn delete_session(&mut self, locator: &str) -> Result<(), ApplicationError> {
        let agent_id = self.owner_for_locator(locator)?;
        let result = self
            .process(agent_id)
            .await?
            .request("thread/archive", json!({"threadId": locator}))
            .await;
        match result {
            Ok(_) | Err(RpcFailure::Remote) => {
                self.locator_owners.remove(locator);
                Ok(())
            }
            Err(RpcFailure::Transport) => Err(ApplicationError::DriverUnavailable),
        }
    }

    async fn process_evidence(
        &mut self,
        run_id: RunId,
    ) -> Result<ProcessEvidence, ApplicationError> {
        let Some(agent_id) = self.run_owners.get(&run_id).copied() else {
            return Ok(ProcessEvidence::Lost);
        };
        Ok(self
            .processes
            .get_mut(&agent_id)
            .map_or(ProcessEvidence::Lost, |process| {
                if process.is_running() {
                    ProcessEvidence::Controlled
                } else {
                    ProcessEvidence::Lost
                }
            }))
    }

    async fn poll_completions(&mut self) -> Result<Vec<DriverCompletion>, ApplicationError> {
        let mut completions = Vec::new();
        for process in self.processes.values_mut() {
            process.poll_notifications().await?;
            for completion in process.completions.drain(..) {
                let reason = process.failure_reasons.remove(&completion.run_id);
                completions.push((completion, reason));
            }
        }
        let mut terminal = Vec::new();
        for (completion, reason) in completions {
            let retry = completion.outcome == DriverTurnOutcome::Failed
                && reason.as_deref().is_some_and(is_retryable_codex_failure)
                && self
                    .run_inputs
                    .get(&completion.run_id)
                    .is_some_and(|run| run.attempts < MAX_TURN_ATTEMPTS);
            if retry {
                let mut run = self
                    .run_inputs
                    .get(&completion.run_id)
                    .cloned()
                    .ok_or(ApplicationError::Internal)?;
                run.attempts += 1;
                if self
                    .start_turn_once(completion.run_id, &run.locator, &run.input)
                    .await
                    .is_ok()
                {
                    self.run_inputs.insert(completion.run_id, run);
                    continue;
                }
            }
            self.run_inputs.remove(&completion.run_id);
            self.run_owners.remove(&completion.run_id);
            terminal.push(completion);
        }
        Ok(terminal)
    }
}

fn is_retryable_codex_failure(reason: &str) -> bool {
    let reason = reason.to_lowercase();
    if [
        "context",
        "unauthorized",
        "authentication",
        "invalid api key",
        "permission",
        "forbidden",
        "tool",
        "invalid argument",
    ]
    .iter()
    .any(|marker| reason.contains(marker))
    {
        return false;
    }
    [
        "timed out",
        "timeout",
        "connection reset",
        "connection closed",
        "broken pipe",
        "server error",
        "service unavailable",
        "bad gateway",
        "gateway timeout",
        "http 500",
        "http 502",
        "http 503",
        "http 504",
        "\"status\":500",
        "\"status\":502",
        "\"status\":503",
        "\"status\":504",
    ]
    .iter()
    .any(|marker| reason.contains(marker))
}

async fn provider_environment(
    codex_home: &Path,
) -> Result<Option<(String, String)>, ApplicationError> {
    let Ok(encoded) = tokio::fs::read_to_string(codex_home.join("config.toml")).await else {
        return Ok(None);
    };
    let Ok(config) = toml::from_str::<toml::Table>(&encoded) else {
        return Ok(None);
    };
    let Some(provider_name) = config.get("model_provider").and_then(toml::Value::as_str) else {
        return Ok(None);
    };
    let Some(env_key) = config
        .get("model_providers")
        .and_then(toml::Value::as_table)
        .and_then(|providers| providers.get(provider_name))
        .and_then(toml::Value::as_table)
        .and_then(|provider| provider.get("env_key"))
        .and_then(toml::Value::as_str)
        .filter(|key| !key.is_empty() && !key.contains(['=', '\0']))
    else {
        return Ok(None);
    };

    let from_auth = tokio::fs::read(codex_home.join("auth.json"))
        .await
        .ok()
        .and_then(|encoded| serde_json::from_slice::<serde_json::Value>(&encoded).ok())
        .and_then(|auth| {
            auth.get(env_key)
                .and_then(serde_json::Value::as_str)
                .map(str::to_owned)
        });
    let value = from_auth.or_else(|| std::env::var(env_key).ok());
    Ok(value
        .filter(|value| !value.is_empty())
        .map(|value| (env_key.to_owned(), value)))
}

#[cfg(test)]
mod tests {
    use std::os::unix::fs::PermissionsExt;

    use uuid::Uuid;

    use super::*;
    use crate::{
        computer::core::{
            home::LocalAgentState,
            input::{AgentInput, ContextMessageInput, RunContextInput, WorkInput},
            scheduler::WorkStrength,
            session::DriverKind,
        },
        ids::{ChannelId, InboxItemId, MemberId, MessageId, SpaceId, ThreadId},
    };

    #[tokio::test]
    async fn codex_provider_environment_uses_the_declared_key_from_agent_auth() {
        let directory = tempfile::tempdir().unwrap();
        std::fs::write(
            directory.path().join("config.toml"),
            "model_provider = \"local\"\n\n[model_providers.local]\nenv_key = \"LOCAL_API_KEY\"\n",
        )
        .unwrap();
        std::fs::write(
            directory.path().join("auth.json"),
            r#"{"LOCAL_API_KEY":"provider-secret"}"#,
        )
        .unwrap();

        let environment = provider_environment(directory.path()).await.unwrap();

        assert_eq!(
            environment
                .as_ref()
                .map(|(key, value)| (key.as_str(), value.as_str())),
            Some(("LOCAL_API_KEY", "provider-secret"))
        );
    }

    #[tokio::test]
    async fn codex_runtime_uses_structured_session_and_turn_methods() {
        let directory = tempfile::tempdir().unwrap();
        let computer_home = directory.path().join("computer");
        let agent_id = AgentId::from_uuid(Uuid::now_v7());
        let agent_home = agent_home(&computer_home, agent_id);
        std::fs::create_dir_all(agent_home.join("drivers/codex")).unwrap();
        std::fs::create_dir_all(agent_home.join("workspace")).unwrap();
        let executable = directory.path().join("fake-codex");
        let spawn_log = directory.path().join("spawns.log");
        std::fs::write(
            &executable,
            format!(
                r#"#!/bin/sh
if [ "$1" = "--version" ]; then exit 0; fi
printf '%s\n' "$SUMI_DRIVER_TOKEN" >> "{}"
[ -n "$SUMI_DRIVER_TOKEN" ] || exit 9
[ -n "$SUMI_SOCKET" ] || exit 10
while IFS= read -r line; do
  id="${{line#*\"id\":}}"; id="${{id%%,*}}"
  case "$line" in
    *'"method":"initialize"'*) printf '%s\n' '{{"id":'"$id"',"result":{{}}}}' ;;
    *'"method":"thread/start"'*)
      [ "$1" = "--dangerously-bypass-approvals-and-sandbox" ] || exit 11
      printf '%s\n' '{{"id":'"$id"',"result":{{"thread":{{"id":"thread-1"}}}}}}'
      ;;
    *'"method":"thread/resume"'*) printf '%s\n' '{{"id":'"$id"',"result":{{"thread":{{"id":"thread-1"}}}}}}' ;;
    *'"method":"turn/start"'*) printf '%s\n' '{{"id":'"$id"',"result":{{"turn":{{"id":"turn-1"}}}}}}' ;;
    *'"method":"turn/steer"'*) printf '%s\n' '{{"id":'"$id"',"result":{{"turnId":"turn-1"}}}}' '{{"method":"turn/completed","params":{{"threadId":"thread-1","turn":{{"id":"turn-1","status":"completed"}}}}}}' ;;
    *'"method":"thread/archive"'*) printf '%s\n' '{{"id":'"$id"',"result":{{}}}}' ;;
  esac
done
"#,
                spawn_log.display()
            ),
        )
        .unwrap();
        let mut permissions = std::fs::metadata(&executable).unwrap().permissions();
        permissions.set_mode(0o700);
        std::fs::set_permissions(&executable, permissions).unwrap();

        let agent = LocalAgent {
            agent_id,
            space_id: SpaceId::from_uuid(Uuid::now_v7()),
            name: "Codex".to_owned(),
            role_revision: 1,
            role: "Implement the current Sumi Run".to_owned(),
            driver: DriverKind::Codex,
            state: LocalAgentState::Active,
        };
        let driver_secret = [7_u8; 32];
        let mut client = CodexRuntimeClient::with_executable(
            computer_home,
            executable.into_os_string(),
            driver_secret,
        );
        client.validate(&agent).await.unwrap();
        let locator = client.create_session(agent_id).await.unwrap();
        assert_eq!(locator, "thread-1");
        assert!(client.resume_session(agent_id, &locator).await.unwrap());
        assert_eq!(
            std::fs::read_to_string(&spawn_log).unwrap().lines().count(),
            1,
            "app-server must stay resident across Runs of the same Agent"
        );

        let thread_id = ThreadId::from_uuid(Uuid::now_v7());
        let input = RunInput {
            global_contract: "Use Sumi capabilities for collaboration facts".to_owned(),
            agent: AgentInput {
                agent_id,
                space_id: agent.space_id,
                identity: agent.name,
                role_revision: agent.role_revision,
                role: agent.role,
                memory: Vec::new(),
            },
            work: WorkInput {
                task: None,
                linked_thread_ids: vec![thread_id],
                public_result_message_id: None,
            },
            context: RunContextInput {
                focus_thread_id: thread_id,
                message_snapshot_sequence: 1,
                focus_messages: vec![ContextMessageInput {
                    message_id: MessageId::from_uuid(Uuid::now_v7()),
                    author_member_id: MemberId::from_uuid(Uuid::now_v7()),
                    body: "message".to_owned(),
                }],
                dispatched_items: Vec::new(),
            },
            space_members: Vec::new(),
        };
        let run_id = RunId::from_uuid(Uuid::now_v7());
        client.start_turn(run_id, &locator, &input).await.unwrap();
        assert_eq!(
            client
                .steer(
                    &locator,
                    &DispatchedItemInput {
                        item_id: InboxItemId::from_uuid(Uuid::now_v7()),
                        source_kind: "mention".to_owned(),
                        strength: WorkStrength::Hard,
                        task_id: None,
                        channel_id: ChannelId::from_uuid(Uuid::nil()),
                        thread_id,
                        message_id: None,
                        content: Some("new item".to_owned()),
                        activity_events: Vec::new(),
                    },
                )
                .await
                .unwrap(),
            SteerOutcome::Accepted
        );
        assert_eq!(
            client.process_evidence(run_id).await.unwrap(),
            ProcessEvidence::Controlled
        );
        assert_eq!(
            client.poll_completions().await.unwrap(),
            vec![DriverCompletion {
                run_id,
                outcome: DriverTurnOutcome::Completed,
            }]
        );
        client.interrupt(&locator).await.unwrap();
        client.delete_session(&locator).await.unwrap();
    }
}
