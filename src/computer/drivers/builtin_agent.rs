use std::{
    collections::{BTreeMap, HashMap},
    path::PathBuf,
};

use async_trait::async_trait;
use sumi_builtin_agent::{
    AgentConfig, AgentError, AgentRuntime, CompactionConfig, Completion, ProviderConfig,
    SandboxConfig, TurnOutcome, TurnRequest,
};

use crate::{
    computer::{
        application::{
            ApplicationError,
            capability::CapabilityService,
            ports::{DriverCompletion, DriverTurnOutcome, ProcessEvidence, SteerOutcome},
        },
        core::{
            home::LocalAgent,
            input::{DispatchedItemInput, RunInput},
        },
    },
    config::{ComputerConfig, daemon_socket_path},
    ids::{AgentId, RunId},
};

use super::{contract::StructuredProviderClient, prompt};

pub(in crate::computer) struct BuiltinRuntimeClient {
    driver_secret: [u8; 32],
    runtime: Option<AgentRuntime>,
}

impl BuiltinRuntimeClient {
    pub(in crate::computer) fn new(
        computer_home: PathBuf,
        config: &ComputerConfig,
        driver_secret: [u8; 32],
    ) -> Result<Self, ApplicationError> {
        let Some(builtin) = &config.builtin else {
            return Ok(Self {
                driver_secret,
                runtime: None,
            });
        };
        let socket_path = daemon_socket_path(&computer_home);
        let agent_config = AgentConfig {
            computer_home,
            provider: ProviderConfig::openai(builtin.token.clone_secret(), builtin.model.clone())
                .with_base_url(
                    builtin
                        .api_base
                        .to_string()
                        .trim_end_matches('/')
                        .to_owned(),
                ),
            sandbox: SandboxConfig {
                socket_path: Some(socket_path),
                runtime_executable: None,
                environment: BTreeMap::new(),
            },
            compaction: CompactionConfig {
                trigger_tokens: builtin.compaction_trigger_tokens(),
                keep_recent_tokens: builtin.compaction_keep_recent_tokens(),
            },
        };
        Ok(Self {
            driver_secret,
            runtime: Some(AgentRuntime::new(agent_config, Vec::new())),
        })
    }

    fn runtime(&self) -> Result<&AgentRuntime, ApplicationError> {
        self.runtime
            .as_ref()
            .ok_or(ApplicationError::DriverUnavailable)
    }

    fn runtime_mut(&mut self) -> Result<&mut AgentRuntime, ApplicationError> {
        self.runtime
            .as_mut()
            .ok_or(ApplicationError::DriverUnavailable)
    }

    fn map_error(error: AgentError) -> ApplicationError {
        match error {
            AgentError::Unavailable => ApplicationError::DriverUnavailable,
            AgentError::SessionLost => ApplicationError::SessionLost,
            AgentError::Conflict => ApplicationError::Conflict,
            AgentError::NotFound => ApplicationError::NotFound,
            AgentError::Internal => ApplicationError::Internal,
        }
    }

    fn turn_request(input: &RunInput, driver_secret: &[u8]) -> TurnRequest {
        let driver_token = CapabilityService::driver_token(driver_secret, input.agent.agent_id);
        TurnRequest {
            product_contract: prompt::product_contract(),
            driver_contract: prompt::driver_contract(),
            identity: input.agent.identity.clone(),
            role: input.agent.role.clone(),
            input: input.model_view(),
            content_hash: input.content_hash(),
            attachments: Vec::new(),
            blocked_tools: HashMap::new(),
            sandbox_environment: BTreeMap::from([("SUMI_DRIVER_TOKEN".to_owned(), driver_token)]),
        }
    }
}

#[async_trait(?Send)]
impl StructuredProviderClient for BuiltinRuntimeClient {
    async fn validate(&mut self, agent: &LocalAgent) -> Result<(), ApplicationError> {
        self.runtime()?
            .validate(agent.agent_id.into_uuid())
            .await
            .map_err(Self::map_error)
    }

    async fn create_session(&mut self, agent_id: AgentId) -> Result<String, ApplicationError> {
        self.runtime_mut()?
            .create_session(agent_id.into_uuid())
            .await
            .map_err(Self::map_error)
    }

    async fn resume_session(
        &mut self,
        agent_id: AgentId,
        locator: &str,
    ) -> Result<bool, ApplicationError> {
        self.runtime_mut()?
            .resume_session(agent_id.into_uuid(), locator)
            .await
            .map_err(Self::map_error)
    }

    async fn start_turn(
        &mut self,
        run_id: RunId,
        locator: &str,
        input: &RunInput,
    ) -> Result<(), ApplicationError> {
        let request = Self::turn_request(input, &self.driver_secret);
        self.runtime_mut()?
            .start_turn(run_id.into_uuid(), locator, request)
            .await
            .map_err(Self::map_error)
    }

    async fn steer(
        &mut self,
        locator: &str,
        _: &DispatchedItemInput,
    ) -> Result<SteerOutcome, ApplicationError> {
        self.runtime_mut()?
            .steer(locator)
            .await
            .map_err(Self::map_error)?;
        Ok(SteerOutcome::Unsupported)
    }

    async fn notice(&mut self, locator: &str) -> Result<(), ApplicationError> {
        self.runtime_mut()?
            .notice(locator)
            .await
            .map_err(Self::map_error)
    }

    async fn interrupt(&mut self, locator: &str) -> Result<(), ApplicationError> {
        self.runtime_mut()?
            .interrupt(locator)
            .await
            .map_err(Self::map_error)
    }

    async fn restart_agent(&mut self, agent_id: AgentId) -> Result<(), ApplicationError> {
        self.runtime_mut()?
            .restart_agent(agent_id.into_uuid())
            .await
            .map_err(Self::map_error)
    }

    async fn delete_session(&mut self, locator: &str) -> Result<(), ApplicationError> {
        self.runtime_mut()?
            .delete_session(locator)
            .await
            .map_err(Self::map_error)
    }

    async fn process_evidence(
        &mut self,
        run_id: RunId,
    ) -> Result<ProcessEvidence, ApplicationError> {
        Ok(if self.runtime()?.process_evidence(run_id.into_uuid()) {
            ProcessEvidence::Controlled
        } else {
            ProcessEvidence::Lost
        })
    }

    async fn poll_completions(&mut self) -> Result<Vec<DriverCompletion>, ApplicationError> {
        let completions = self
            .runtime_mut()?
            .poll_completions()
            .await
            .map_err(Self::map_error)?;
        Ok(completions
            .into_iter()
            .map(|Completion { run_id, outcome }| DriverCompletion {
                run_id: RunId::from_uuid(run_id),
                outcome: match outcome {
                    TurnOutcome::Completed => DriverTurnOutcome::Completed,
                    TurnOutcome::Failed => DriverTurnOutcome::Failed,
                    TurnOutcome::Interrupted => DriverTurnOutcome::Interrupted,
                },
            })
            .collect())
    }
}

#[cfg(test)]
mod tests {
    use std::fs;

    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    use uuid::Uuid;

    use super::*;
    use crate::{
        computer::core::{
            input::{AgentInput, ContextMessageInput, RunContextInput, WorkInput},
            scheduler::WorkStrength,
        },
        config::{BuiltinOpenAiConfig, ConfigSecret},
        ids::{ChannelId, InboxItemId, MemberId, MessageId, SpaceId, ThreadId},
    };

    #[tokio::test]
    async fn builtin_runtime_adapts_sessions_turns_and_evidence() {
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
        let agent_home = super::super::agent_home(&computer_home, agent_id);
        for relative in ["workspace", "memory", "runs", "drivers/builtin"] {
            fs::create_dir_all(agent_home.join(relative)).unwrap();
        }
        let config = builtin_config(&format!("http://{address}/v1"));
        let mut client =
            BuiltinRuntimeClient::new(computer_home.clone(), &config, [7_u8; 32]).unwrap();
        let locator = client.create_session(agent_id).await.unwrap();
        let input = run_input(agent_id);
        let run_id = RunId::from_uuid(Uuid::now_v7());

        client.start_turn(run_id, &locator, &input).await.unwrap();
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
                    &DispatchedItemInput {
                        item_id: InboxItemId::from_uuid(Uuid::now_v7()),
                        source_kind: "mention".to_owned(),
                        strength: WorkStrength::Hard,
                        task_id: None,
                        channel_id: ChannelId::from_uuid(Uuid::nil()),
                        thread_id: input.context.focus_thread_id,
                        message_id: None,
                        content: Some("new item".to_owned()),
                        activity_events: Vec::new(),
                    },
                )
                .await
                .unwrap(),
            SteerOutcome::Unsupported
        );
        assert_eq!(
            client.process_evidence(run_id).await.unwrap(),
            ProcessEvidence::Lost
        );
        provider_task.await.unwrap();

        let session_path = client
            .runtime()
            .unwrap()
            .agent_home(agent_id.into_uuid())
            .join("drivers/builtin/sessions")
            .join(format!("{locator}.json"));
        let stored = fs::read_to_string(&session_path).unwrap();
        assert!(stored.contains("completed"));
        assert!(!stored.contains("provider-secret"));
        let mut resumed = BuiltinRuntimeClient::new(computer_home, &config, [7_u8; 32]).unwrap();
        assert!(resumed.resume_session(agent_id, &locator).await.unwrap());
        resumed.delete_session(&locator).await.unwrap();
        assert!(!session_path.exists());
    }

    #[tokio::test]
    async fn builtin_runtime_interrupt_reports_interrupted_completion() {
        let directory = tempfile::tempdir().unwrap();
        let computer_home = directory.path().join("computer");
        let agent_id = AgentId::from_uuid(Uuid::now_v7());
        let agent_home = super::super::agent_home(&computer_home, agent_id);
        for relative in ["workspace", "memory", "runs", "drivers/builtin"] {
            fs::create_dir_all(agent_home.join(relative)).unwrap();
        }
        let config = builtin_config("http://127.0.0.1:9/v1");
        let mut client = BuiltinRuntimeClient::new(computer_home, &config, [7_u8; 32]).unwrap();
        let locator = client.create_session(agent_id).await.unwrap();
        let run_id = RunId::from_uuid(Uuid::now_v7());
        client
            .start_turn(run_id, &locator, &run_input(agent_id))
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

    fn builtin_config(api_base: &str) -> ComputerConfig {
        ComputerConfig {
            builtin: Some(BuiltinOpenAiConfig {
                api_base: url::Url::parse(api_base).unwrap(),
                token: ConfigSecret::from("provider-secret"),
                model: "test-model".to_owned(),
                context_window_tokens: 128_000,
                compaction_trigger_ratio: 0.75,
                compaction_keep_recent_tokens: 20_000,
            }),
            ..ComputerConfig::default()
        }
    }

    fn run_input(agent_id: AgentId) -> RunInput {
        let thread_id = ThreadId::from_uuid(Uuid::now_v7());
        RunInput {
            product_contract: "Use Sumi capabilities for collaboration facts".to_owned(),
            agent: AgentInput {
                agent_id,
                space_id: SpaceId::from_uuid(Uuid::now_v7()),
                identity: "Builtin".to_owned(),
                role_revision: 1,
                role: "Complete the current Run".to_owned(),
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
            channel_members: Vec::new(),
        }
    }
}
