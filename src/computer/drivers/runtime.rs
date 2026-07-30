use std::{
    collections::{BTreeMap, VecDeque},
    ffi::OsString,
    path::PathBuf,
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
            input::{ClaimedItemInput, RunInput},
        },
    },
    ids::{AgentId, RunId},
};

use super::contract::StructuredProviderClient;

const RPC_TIMEOUT: Duration = Duration::from_secs(30);

pub(super) struct CodexRuntimeClient {
    computer_home: PathBuf,
    executable: OsString,
    processes: BTreeMap<AgentId, CodexProcess>,
    locator_owners: BTreeMap<String, AgentId>,
    run_owners: BTreeMap<RunId, AgentId>,
}

impl CodexRuntimeClient {
    pub(super) fn new(computer_home: PathBuf) -> Self {
        Self::with_executable(computer_home, OsString::from("codex"))
    }

    fn with_executable(computer_home: PathBuf, executable: OsString) -> Self {
        Self {
            computer_home,
            executable,
            processes: BTreeMap::new(),
            locator_owners: BTreeMap::new(),
            run_owners: BTreeMap::new(),
        }
    }

    fn agent_home(&self, agent_id: AgentId) -> PathBuf {
        self.computer_home.join("agents").join(agent_id.to_string())
    }

    async fn process(&mut self, agent_id: AgentId) -> Result<&mut CodexProcess, ApplicationError> {
        if !self.processes.contains_key(&agent_id) {
            let agent_home = self.agent_home(agent_id);
            let process = CodexProcess::spawn(self.executable.clone(), agent_home).await?;
            self.processes.insert(agent_id, process);
        }
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
}

struct CodexProcess {
    child: Child,
    stdin: ChildStdin,
    stdout: Lines<BufReader<ChildStdout>>,
    next_request_id: u64,
    active_turns: BTreeMap<String, String>,
    active_runs: BTreeMap<String, RunId>,
    completions: VecDeque<DriverCompletion>,
}

impl CodexProcess {
    async fn spawn(executable: OsString, agent_home: PathBuf) -> Result<Self, ApplicationError> {
        let codex_home = agent_home.join("drivers/codex");
        let workspace = agent_home.join("workspace");
        if !codex_home.is_dir() || !workspace.is_dir() {
            return Err(ApplicationError::DriverUnavailable);
        }
        let mut command = Command::new(executable);
        command
            .arg("app-server")
            .arg("--listen")
            .arg("stdio://")
            .current_dir(&workspace)
            .env_clear()
            .env("CODEX_HOME", &codex_home)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .kill_on_drop(true);
        if let Some(path) = std::env::var_os("PATH") {
            command.env("PATH", path);
        }
        if let Some(language) = std::env::var_os("LANG") {
            command.env("LANG", language);
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
        let outcome = match message
            .pointer("/params/turn/status")
            .and_then(Value::as_str)
        {
            Some("completed") => DriverTurnOutcome::Completed,
            Some("interrupted") => DriverTurnOutcome::Interrupted,
            _ => DriverTurnOutcome::Failed,
        };
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
        let agent_id = input.agent.agent_id;
        if self.owner_for_locator(locator)? != agent_id {
            return Err(ApplicationError::SessionLost);
        }
        let encoded = serde_json::to_string(input).map_err(|_| ApplicationError::Internal)?;
        let result = self
            .process(agent_id)
            .await?
            .request(
                "turn/start",
                json!({
                    "threadId": locator,
                    "input": [{
                        "type": "text",
                        "text": format!("Process this Sumi Run input. Treat each top-level field as a separate contract block.\n{encoded}")
                    }]
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

    async fn steer(
        &mut self,
        locator: &str,
        item: &ClaimedItemInput,
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
            completions.extend(process.completions.drain(..));
        }
        Ok(completions)
    }
}

pub(super) struct UnavailableRuntimeClient;

#[async_trait(?Send)]
impl StructuredProviderClient for UnavailableRuntimeClient {
    async fn validate(&mut self, _: &LocalAgent) -> Result<(), ApplicationError> {
        Err(ApplicationError::DriverUnavailable)
    }

    async fn create_session(&mut self, _: AgentId) -> Result<String, ApplicationError> {
        Err(ApplicationError::DriverUnavailable)
    }

    async fn resume_session(&mut self, _: AgentId, _: &str) -> Result<bool, ApplicationError> {
        Err(ApplicationError::DriverUnavailable)
    }

    async fn start_turn(
        &mut self,
        _: RunId,
        _: &str,
        _: &RunInput,
    ) -> Result<(), ApplicationError> {
        Err(ApplicationError::DriverUnavailable)
    }

    async fn steer(
        &mut self,
        _: &str,
        _: &ClaimedItemInput,
    ) -> Result<SteerOutcome, ApplicationError> {
        Ok(SteerOutcome::Unsupported)
    }

    async fn notice(&mut self, _: &str) -> Result<(), ApplicationError> {
        Ok(())
    }

    async fn interrupt(&mut self, _: &str) -> Result<(), ApplicationError> {
        Ok(())
    }

    async fn delete_session(&mut self, _: &str) -> Result<(), ApplicationError> {
        Ok(())
    }

    async fn process_evidence(&mut self, _: RunId) -> Result<ProcessEvidence, ApplicationError> {
        Ok(ProcessEvidence::Lost)
    }

    async fn poll_completions(&mut self) -> Result<Vec<DriverCompletion>, ApplicationError> {
        Ok(Vec::new())
    }
}

#[cfg(test)]
mod tests {
    use std::os::unix::fs::PermissionsExt;

    use uuid::Uuid;

    use super::*;
    use crate::{
        computer::core::{
            home::LocalAgentState,
            input::{AgentInput, RunContextInput, WorkInput},
            session::DriverKind,
        },
        ids::{InboxItemId, SpaceId, ThreadId},
    };

    #[tokio::test]
    async fn codex_runtime_uses_structured_session_and_turn_methods() {
        let directory = tempfile::tempdir().unwrap();
        let computer_home = directory.path().join("computer");
        let agent_id = AgentId::from_uuid(Uuid::now_v7());
        let agent_home = computer_home.join("agents").join(agent_id.to_string());
        std::fs::create_dir_all(agent_home.join("drivers/codex")).unwrap();
        std::fs::create_dir_all(agent_home.join("workspace")).unwrap();
        let executable = directory.path().join("fake-codex");
        std::fs::write(
            &executable,
            r#"#!/bin/sh
if [ "$1" = "--version" ]; then exit 0; fi
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*) printf '%s\n' '{"id":1,"result":{}}' ;;
    *'"method":"thread/start"'*) printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-1"}}}' ;;
    *'"method":"turn/start"'*) printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-1"}}}' ;;
    *'"method":"turn/steer"'*) printf '%s\n' '{"id":4,"result":{"turnId":"turn-1"}}' '{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}}' ;;
    *'"method":"thread/archive"'*) printf '%s\n' '{"id":5,"result":{}}' ;;
  esac
done
"#,
        )
        .unwrap();
        let mut permissions = std::fs::metadata(&executable).unwrap().permissions();
        permissions.set_mode(0o700);
        std::fs::set_permissions(&executable, permissions).unwrap();

        let agent = LocalAgent {
            agent_id,
            space_id: SpaceId::from_uuid(Uuid::now_v7()),
            name: "Codex".to_owned(),
            handle: "codex".to_owned(),
            role_revision: 1,
            role: "Implement the current Sumi Run".to_owned(),
            driver: DriverKind::Codex,
            state: LocalAgentState::Active,
        };
        let mut client =
            CodexRuntimeClient::with_executable(computer_home, executable.into_os_string());
        client.validate(&agent).await.unwrap();
        let locator = client.create_session(agent_id).await.unwrap();
        assert_eq!(locator, "thread-1");

        let thread_id = ThreadId::from_uuid(Uuid::now_v7());
        let input = RunInput {
            global_contract: "Use Sumi capabilities for collaboration facts".to_owned(),
            agent: AgentInput {
                agent_id,
                space_id: agent.space_id,
                identity: agent.name,
                role_revision: agent.role_revision,
                role: agent.role,
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
        };
        let run_id = RunId::from_uuid(Uuid::now_v7());
        client.start_turn(run_id, &locator, &input).await.unwrap();
        assert_eq!(
            client
                .steer(
                    &locator,
                    &ClaimedItemInput {
                        item_id: InboxItemId::from_uuid(Uuid::now_v7()),
                        task_id: None,
                        thread_id,
                        content: Some("new item".to_owned()),
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
