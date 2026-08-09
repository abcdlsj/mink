use std::collections::BTreeSet;

use async_trait::async_trait;
use time::OffsetDateTime;
use uuid::Uuid;

use super::{
    DriverAdapter, builtin::BuiltinAdapter, codex::CodexAdapter, contract::StructuredProviderClient,
};
use crate::{
    computer::{
        application::{
            ApplicationError,
            ports::{
                DriverCompletion, DriverPort, DriverTurnOutcome, OpenSessionRequest,
                ProcessEvidence, SteerOutcome,
            },
        },
        core::{
            input::{AgentInput, DispatchedItemInput, RunContextInput, RunInput, WorkInput},
            scheduler::{RunPriority, WorkStrength},
            session::{DriverKind, SessionFingerprint, SessionScope},
            supervisor::{LocalRun, NewRun},
        },
    },
    ids::{AgentId, ChannelId, InboxItemId, RunId, SpaceId, ThreadId},
};

struct FakeClient {
    prefix: &'static str,
    sessions: BTreeSet<String>,
    steer: SteerOutcome,
    turns_started: usize,
    runs: BTreeSet<RunId>,
}

impl FakeClient {
    fn new(prefix: &'static str, steer: SteerOutcome) -> Self {
        Self {
            prefix,
            sessions: BTreeSet::new(),
            steer,
            turns_started: 0,
            runs: BTreeSet::new(),
        }
    }
}

#[async_trait(?Send)]
impl StructuredProviderClient for FakeClient {
    async fn validate(
        &mut self,
        _: &crate::computer::core::home::LocalAgent,
    ) -> Result<(), ApplicationError> {
        Ok(())
    }

    async fn create_session(&mut self, _: AgentId) -> Result<String, ApplicationError> {
        let locator = format!("{}-{}", self.prefix, self.sessions.len() + 1);
        self.sessions.insert(locator.clone());
        Ok(locator)
    }

    async fn resume_session(
        &mut self,
        _: AgentId,
        locator: &str,
    ) -> Result<bool, ApplicationError> {
        Ok(self.sessions.contains(locator))
    }

    async fn start_turn(
        &mut self,
        run_id: RunId,
        _: &str,
        _: &RunInput,
    ) -> Result<(), ApplicationError> {
        self.turns_started += 1;
        self.runs.insert(run_id);
        Ok(())
    }

    async fn steer(
        &mut self,
        _: &str,
        _: u64,
        _: &DispatchedItemInput,
    ) -> Result<SteerOutcome, ApplicationError> {
        Ok(self.steer)
    }

    async fn notice(
        &mut self,
        _: &str,
        _: &crate::computer::core::input::AttentionNoticeInput,
    ) -> Result<(), ApplicationError> {
        Ok(())
    }

    async fn interrupt(&mut self, _: &str) -> Result<(), ApplicationError> {
        Ok(())
    }

    async fn restart_agent(&mut self, _: AgentId) -> Result<(), ApplicationError> {
        Ok(())
    }

    async fn delete_session(&mut self, locator: &str) -> Result<(), ApplicationError> {
        self.sessions.remove(locator);
        Ok(())
    }

    async fn process_evidence(
        &mut self,
        run_id: RunId,
    ) -> Result<ProcessEvidence, ApplicationError> {
        Ok(if self.runs.contains(&run_id) {
            ProcessEvidence::Controlled
        } else {
            ProcessEvidence::Lost
        })
    }

    async fn poll_completions(&mut self) -> Result<Vec<DriverCompletion>, ApplicationError> {
        Ok(Vec::new())
    }
}

#[tokio::test]
async fn codex_session_resumes_and_lost_locator_is_reported() {
    let codex = CodexAdapter::new(FakeClient::new("codex", SteerOutcome::Unsupported));
    let builtin = BuiltinAdapter::new(FakeClient::new("builtin", SteerOutcome::Accepted));
    let mut driver = DriverAdapter::new(codex, builtin);
    let request = open_request(DriverKind::Codex, None);
    let opened = driver.open_or_resume(request).await.unwrap();
    assert!(!opened.resumed);
    let resumed = driver
        .open_or_resume(open_request(
            DriverKind::Codex,
            Some(opened.locator.clone()),
        ))
        .await
        .unwrap();
    assert!(resumed.resumed);
    assert_eq!(resumed.locator, opened.locator);
    assert_eq!(
        driver
            .open_or_resume(open_request(DriverKind::Codex, Some("missing".to_owned())))
            .await,
        Err(ApplicationError::SessionLost)
    );
}

#[tokio::test]
async fn builtin_session_resumes_without_crossing_driver_boundary() {
    let codex = CodexAdapter::new(FakeClient::new("codex", SteerOutcome::Unsupported));
    let builtin = BuiltinAdapter::new(FakeClient::new("builtin", SteerOutcome::Accepted));
    let mut driver = DriverAdapter::new(codex, builtin);
    let opened = driver
        .open_or_resume(open_request(DriverKind::Builtin, None))
        .await
        .unwrap();
    assert!(opened.locator.starts_with("builtin-"));
    let resumed = driver
        .open_or_resume(open_request(
            DriverKind::Builtin,
            Some(opened.locator.clone()),
        ))
        .await
        .unwrap();
    assert!(resumed.resumed);
    assert_eq!(resumed.locator, opened.locator);
}

#[tokio::test]
async fn unsupported_steer_does_not_start_a_second_turn() {
    let codex = CodexAdapter::new(FakeClient::new("codex", SteerOutcome::Unsupported));
    let builtin = BuiltinAdapter::new(FakeClient::new("builtin", SteerOutcome::Accepted));
    let mut driver = DriverAdapter::new(codex, builtin);
    let opened = driver
        .open_or_resume(open_request(DriverKind::Codex, None))
        .await
        .unwrap();
    let mut run = test_run(DriverKind::Codex);
    driver.start_turn(&run, &opened.locator).await.unwrap();
    let item = DispatchedItemInput {
        item_id: InboxItemId::from_uuid(Uuid::now_v7()),
        source_kind: "mention".to_owned(),
        strength: WorkStrength::Hard,
        task_id: None,
        channel_id: crate::ids::ChannelId::from_uuid(Uuid::nil()),
        thread_id: run.view().focus_thread_id,
        message_id: None,
        content: Some("new input".to_owned()),
        activity_events: Vec::new(),
    };
    run.attach(1, item).unwrap();
    assert_eq!(
        driver.steer(&run, 1).await.unwrap(),
        SteerOutcome::Unsupported
    );
    assert_eq!(driver.codex.client.turns_started, 1);
}

#[tokio::test]
async fn waiting_for_one_completion_preserves_other_runs() {
    let codex = CodexAdapter::new(FakeClient::new("codex", SteerOutcome::Unsupported));
    let builtin = BuiltinAdapter::new(FakeClient::new("builtin", SteerOutcome::Accepted));
    let mut driver = DriverAdapter::new(codex, builtin);
    let target = RunId::from_uuid(Uuid::now_v7());
    let other = RunId::from_uuid(Uuid::now_v7());
    driver.completions.extend([
        DriverCompletion {
            run_id: other,
            outcome: DriverTurnOutcome::Completed,
        },
        DriverCompletion {
            run_id: target,
            outcome: DriverTurnOutcome::Interrupted,
        },
    ]);

    assert_eq!(
        driver
            .wait_for_completion(target, std::time::Duration::ZERO)
            .await
            .unwrap(),
        Some(DriverTurnOutcome::Interrupted)
    );
    assert_eq!(
        driver.poll_completions().await.unwrap(),
        vec![DriverCompletion {
            run_id: other,
            outcome: DriverTurnOutcome::Completed,
        }]
    );
}

fn open_request(driver: DriverKind, resume_locator: Option<String>) -> OpenSessionRequest {
    OpenSessionRequest {
        agent_id: AgentId::from_uuid(Uuid::now_v7()),
        scope: SessionScope::Thread(ThreadId::from_uuid(Uuid::now_v7())),
        generation: 1,
        fingerprint: fingerprint(driver),
        resume_locator,
    }
}

fn fingerprint(driver: DriverKind) -> SessionFingerprint {
    SessionFingerprint {
        driver,
        workspace: "workspace".to_owned(),
        role_revision: 1,
        audience: "audience".to_owned(),
    }
}

fn test_run(driver: DriverKind) -> LocalRun {
    let agent_id = AgentId::from_uuid(Uuid::now_v7());
    let thread_id = ThreadId::from_uuid(Uuid::now_v7());
    let mut run = LocalRun::new(NewRun {
        id: RunId::from_uuid(Uuid::now_v7()),
        agent_id,
        task_id: None,
        focus_thread_id: thread_id,
        priority: RunPriority {
            explicit_human_redirect: false,
            strength: WorkStrength::Hard,
            available_at: OffsetDateTime::now_utc(),
            has_task_continuity: false,
        },
        input: RunInput {
            product_contract: "contract".to_owned(),
            agent: AgentInput {
                agent_id,
                space_id: SpaceId::from_uuid(Uuid::nil()),
                identity: "agent".to_owned(),
                role_revision: 1,
                role: "role".to_owned(),
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
                focus_messages: Vec::new(),
                channel_id: ChannelId::from_uuid(Uuid::nil()),
                channel_snapshot_sequence: 1,
                channel_activity: Vec::new(),
                dispatched_items: Vec::new(),
            },
            channel_members: Vec::new(),
        },
    })
    .unwrap();
    run.set_session_fingerprint(fingerprint(driver));
    run
}
