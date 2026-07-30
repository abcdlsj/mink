use std::collections::BTreeSet;

use async_trait::async_trait;
use time::{Duration, OffsetDateTime};
use uuid::Uuid;

use super::{
    DriverAdapter, builtin::BuiltinAdapter, codex::CodexAdapter, contract::StructuredProviderClient,
};
use crate::{
    computer::{
        application::{
            ApplicationError,
            ports::{
                DriverCompletion, DriverPort, OpenSessionRequest, ProcessEvidence, SteerOutcome,
            },
        },
        core::{
            input::{AgentInput, ClaimedItemInput, RunContextInput, RunInput, WorkInput},
            scheduler::{RunPriority, WorkStrength},
            session::{DriverKind, SessionFingerprint, SessionScope},
            supervisor::{FencingToken, LocalRun, NewRun},
        },
    },
    ids::{AgentId, InboxItemId, RunId, ThreadId},
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
        _: &str,
    ) -> Result<(), ApplicationError> {
        self.turns_started += 1;
        self.runs.insert(run_id);
        Ok(())
    }

    async fn steer(
        &mut self,
        _: &str,
        _: &ClaimedItemInput,
    ) -> Result<SteerOutcome, ApplicationError> {
        Ok(self.steer)
    }

    async fn notice(&mut self, _: &str) -> Result<(), ApplicationError> {
        Ok(())
    }

    async fn interrupt(&mut self, _: &str) -> Result<(), ApplicationError> {
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
    let item = ClaimedItemInput {
        item_id: InboxItemId::from_uuid(Uuid::now_v7()),
        task_id: None,
        thread_id: run.focus_thread_id,
        content: Some("new input".to_owned()),
    };
    run.attach(1, item).unwrap();
    assert_eq!(
        driver.steer(&run, 1).await.unwrap(),
        SteerOutcome::Unsupported
    );
    assert_eq!(driver.codex.client.turns_started, 1);
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
        fencing_token: FencingToken::new("secret".to_owned()),
        priority: RunPriority {
            explicit_human_redirect: false,
            strength: WorkStrength::Hard,
            available_at: OffsetDateTime::now_utc(),
            has_task_continuity: false,
        },
        ownership_lease_expires_at: OffsetDateTime::now_utc() + Duration::minutes(5),
        input: RunInput {
            global_contract: "contract".to_owned(),
            agent: AgentInput {
                agent_id,
                space_id: crate::ids::SpaceId::from_uuid(Uuid::nil()),
                identity: "agent".to_owned(),
                role_revision: 1,
                role: "role".to_owned(),
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
                focus_messages: Vec::new(),
                claimed_items: Vec::new(),
            },
        },
    })
    .unwrap();
    run.session_fingerprint = Some(fingerprint(driver));
    run
}
