use std::collections::BTreeMap;

use async_trait::async_trait;
use time::{Duration, OffsetDateTime};
use uuid::Uuid;

use crate::ids::{
    AgentId, CommandId, EventId, InboxItemId, NoticeId, RunId, SpaceId, TaskId, ThreadId,
};

use crate::computer::core::{
    home::LocalAgent,
    input::{
        AgentInput, AttentionNoticeInput, ClaimedItemInput, NoticeLocationInput, RunContextInput,
        RunInput, TaskInput, WorkInput,
    },
    scheduler::{PendingRun, RunPriority, Scheduler, WorkStrength},
    session::{
        self, DriverKind, ProviderSession, ResolveDecision, SessionFingerprint, SessionScope,
        SessionState,
    },
    supervisor::{
        DeliveryState, FencingToken, ItemDisposition, LocalRun, LocalRunState, NewRun,
        TerminalStatus,
    },
};

use super::{
    ApplicationError,
    command::{Command, CommandService},
    ports::{
        AgentHomePort, CommandStatus, ComputerTransaction, DriverCompletion, DriverPort,
        DriverTurnOutcome, LocalErrorCode, LocalEvent, OpenSessionRequest, OpenedSession,
        ProcessEvidence, SteerOutcome, StoredCommand, TransactionPort,
    },
    recovery::RecoveryService,
    run::RunService,
    scheduler::SchedulerService,
};

#[derive(Clone, Default)]
struct MemoryState {
    commands: BTreeMap<CommandId, StoredCommand>,
    runs: BTreeMap<RunId, LocalRun>,
    sessions: Vec<ProviderSession>,
    events: BTreeMap<EventId, LocalEvent>,
}

#[derive(Default)]
struct MemoryPort {
    state: MemoryState,
}

struct MemoryTransaction {
    state: MemoryState,
}

impl TransactionPort for MemoryPort {
    type Transaction = MemoryTransaction;

    async fn transact<T>(
        &mut self,
        operation: impl for<'a> AsyncFnOnce(&'a mut Self::Transaction) -> Result<T, ApplicationError>,
    ) -> Result<T, ApplicationError> {
        let mut transaction = MemoryTransaction {
            state: self.state.clone(),
        };
        match operation(&mut transaction).await {
            Ok(value) => {
                self.state = transaction.state;
                Ok(value)
            }
            Err(error) => Err(error),
        }
    }
}

impl ComputerTransaction for MemoryTransaction {
    fn command(&mut self, id: CommandId) -> Result<Option<StoredCommand>, ApplicationError> {
        Ok(self.state.commands.get(&id).cloned())
    }

    fn insert_command(&mut self, command: StoredCommand) -> Result<(), ApplicationError> {
        if self.state.commands.insert(command.id, command).is_some() {
            return Err(ApplicationError::Conflict);
        }
        Ok(())
    }

    fn save_command(&mut self, command: StoredCommand) -> Result<(), ApplicationError> {
        self.state.commands.insert(command.id, command);
        Ok(())
    }

    fn pending_commands(&mut self) -> Result<Vec<StoredCommand>, ApplicationError> {
        Ok(self
            .state
            .commands
            .values()
            .filter(|command| command.status == CommandStatus::Pending)
            .cloned()
            .collect())
    }

    fn run(&mut self, id: RunId) -> Result<Option<LocalRun>, ApplicationError> {
        Ok(self.state.runs.get(&id).cloned())
    }

    fn save_run(&mut self, run: LocalRun) -> Result<(), ApplicationError> {
        self.state.runs.insert(run.id, run);
        Ok(())
    }

    fn nonterminal_runs(&mut self) -> Result<Vec<LocalRun>, ApplicationError> {
        Ok(self
            .state
            .runs
            .values()
            .filter(|run| !run.state.is_terminal())
            .cloned()
            .collect())
    }

    fn sessions(
        &mut self,
        agent_id: AgentId,
        scope: SessionScope,
    ) -> Result<Vec<ProviderSession>, ApplicationError> {
        Ok(self
            .state
            .sessions
            .iter()
            .filter(|session| session.agent_id == agent_id && session.scope == scope)
            .cloned()
            .collect())
    }

    fn agent_sessions(
        &mut self,
        agent_id: AgentId,
    ) -> Result<Vec<ProviderSession>, ApplicationError> {
        Ok(self
            .state
            .sessions
            .iter()
            .filter(|session| session.agent_id == agent_id)
            .cloned()
            .collect())
    }

    fn save_session(&mut self, session: ProviderSession) -> Result<(), ApplicationError> {
        self.state.sessions.retain(|stored| {
            stored.agent_id != session.agent_id
                || stored.scope != session.scope
                || stored.generation != session.generation
        });
        self.state.sessions.push(session);
        Ok(())
    }

    fn delete_session(
        &mut self,
        agent_id: AgentId,
        scope: SessionScope,
        generation: u64,
    ) -> Result<(), ApplicationError> {
        self.state.sessions.retain(|session| {
            session.agent_id != agent_id
                || session.scope != scope
                || session.generation != generation
        });
        Ok(())
    }

    fn append_event(&mut self, event: LocalEvent) -> Result<(), ApplicationError> {
        self.state.events.entry(event.id()).or_insert(event);
        Ok(())
    }

    fn pending_events(&mut self) -> Result<Vec<LocalEvent>, ApplicationError> {
        Ok(self.state.events.values().cloned().collect())
    }

    fn acknowledge_event(&mut self, event_id: EventId) -> Result<(), ApplicationError> {
        self.state.events.remove(&event_id);
        Ok(())
    }
}

#[derive(Default)]
struct MemoryHome {
    agents: BTreeMap<AgentId, LocalAgent>,
    memory: BTreeMap<(AgentId, std::path::PathBuf), Vec<u8>>,
}

#[async_trait]
impl AgentHomePort for MemoryHome {
    async fn agent(&mut self, agent_id: AgentId) -> Result<LocalAgent, ApplicationError> {
        self.agents
            .get(&agent_id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    async fn provision(&mut self, agent: LocalAgent) -> Result<(), ApplicationError> {
        self.agents.insert(agent.agent_id, agent);
        Ok(())
    }

    async fn configure(&mut self, mut agent: LocalAgent) -> Result<(), ApplicationError> {
        agent.state = self.agent(agent.agent_id).await?.state;
        self.provision(agent).await
    }

    async fn suspend(&mut self, agent_id: AgentId) -> Result<(), ApplicationError> {
        let agent = self
            .agents
            .get_mut(&agent_id)
            .ok_or(ApplicationError::NotFound)?;
        agent.state = crate::computer::core::home::LocalAgentState::Suspended;
        Ok(())
    }

    async fn retire(&mut self, agent_id: AgentId) -> Result<(), ApplicationError> {
        self.agents.remove(&agent_id);
        Ok(())
    }

    async fn workspace_fingerprint(&mut self, _: AgentId) -> Result<String, ApplicationError> {
        Ok("workspace".to_owned())
    }

    async fn read_memory(
        &mut self,
        agent_id: AgentId,
        path: &std::path::Path,
    ) -> Result<Vec<u8>, ApplicationError> {
        self.memory
            .get(&(agent_id, path.to_path_buf()))
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    async fn write_memory(
        &mut self,
        agent_id: AgentId,
        path: &std::path::Path,
        content: &[u8],
    ) -> Result<(), ApplicationError> {
        self.memory
            .insert((agent_id, path.to_path_buf()), content.to_vec());
        Ok(())
    }
}

struct FakeDriver {
    open_count: usize,
    start_count: usize,
    steer_count: usize,
    notice_count: usize,
    interrupt_count: usize,
    close_count: usize,
    resume_count: usize,
    fail_next_resume: bool,
    steer_outcome: SteerOutcome,
    process_evidence: ProcessEvidence,
    steered_content: Option<String>,
    fail_next_steer: bool,
    fail_validation: bool,
}

impl Default for FakeDriver {
    fn default() -> Self {
        Self {
            open_count: 0,
            start_count: 0,
            steer_count: 0,
            notice_count: 0,
            interrupt_count: 0,
            close_count: 0,
            resume_count: 0,
            fail_next_resume: false,
            steer_outcome: SteerOutcome::Accepted,
            process_evidence: ProcessEvidence::Controlled,
            steered_content: None,
            fail_next_steer: false,
            fail_validation: false,
        }
    }
}

#[async_trait(?Send)]
impl DriverPort for FakeDriver {
    async fn validate(&mut self, _: &LocalAgent) -> Result<(), ApplicationError> {
        if self.fail_validation {
            Err(ApplicationError::DriverUnavailable)
        } else {
            Ok(())
        }
    }

    async fn open_or_resume(
        &mut self,
        request: OpenSessionRequest,
    ) -> Result<OpenedSession, ApplicationError> {
        self.open_count += 1;
        if request.resume_locator.is_some() {
            self.resume_count += 1;
            if self.fail_next_resume {
                self.fail_next_resume = false;
                return Err(ApplicationError::SessionLost);
            }
        }
        Ok(OpenedSession {
            locator: request
                .resume_locator
                .clone()
                .unwrap_or_else(|| format!("session-{}", request.generation)),
            resumed: request.resume_locator.is_some(),
        })
    }

    async fn start_turn(
        &mut self,
        _run: &LocalRun,
        _locator: &str,
    ) -> Result<(), ApplicationError> {
        self.start_count += 1;
        Ok(())
    }

    async fn steer(
        &mut self,
        run: &LocalRun,
        sequence: u64,
    ) -> Result<SteerOutcome, ApplicationError> {
        self.steer_count += 1;
        if self.fail_next_steer {
            self.fail_next_steer = false;
            return Err(ApplicationError::DriverUnavailable);
        }
        self.steered_content = run.deliveries[&sequence].item.content.clone();
        Ok(self.steer_outcome)
    }

    async fn notice(&mut self, _run: &LocalRun) -> Result<(), ApplicationError> {
        self.notice_count += 1;
        Ok(())
    }

    async fn interrupt(&mut self, _run: &LocalRun) -> Result<(), ApplicationError> {
        self.interrupt_count += 1;
        Ok(())
    }

    async fn close_session(&mut self, _session: &ProviderSession) -> Result<(), ApplicationError> {
        self.close_count += 1;
        Ok(())
    }

    async fn process_evidence(
        &mut self,
        _run: &LocalRun,
    ) -> Result<ProcessEvidence, ApplicationError> {
        Ok(self.process_evidence)
    }

    async fn poll_completions(&mut self) -> Result<Vec<DriverCompletion>, ApplicationError> {
        Ok(Vec::new())
    }
}

#[test]
fn scheduler_rotates_agents_and_prioritizes_human_redirects() {
    let now = OffsetDateTime::now_utc();
    let agent_a = agent_id();
    let agent_b = agent_id();
    let mut scheduler = Scheduler::new(1);
    let ordinary = pending(agent_a, now, false, WorkStrength::Hard);
    let redirect = pending(
        agent_a,
        now + Duration::seconds(1),
        true,
        WorkStrength::Hard,
    );
    let other_agent = pending(agent_b, now, false, WorkStrength::Ambient);
    scheduler.enqueue(ordinary.clone());
    scheduler.enqueue(redirect.clone());
    scheduler.enqueue(other_agent.clone());

    assert_eq!(scheduler.next().unwrap().run_id, redirect.run_id);
    scheduler.release(redirect.run_id);
    assert_eq!(scheduler.next().unwrap().run_id, other_agent.run_id);
    scheduler.release(other_agent.run_id);
    assert_eq!(scheduler.next().unwrap().run_id, ordinary.run_id);
}

#[test]
fn session_resolution_reuses_only_compatible_scope_and_generation() {
    let agent_id = agent_id();
    let scope = SessionScope::Task(task_id());
    let compatible = fingerprint(1, "workspace-a");
    let session = ProviderSession {
        agent_id,
        scope,
        generation: 3,
        locator: "provider-session".to_owned(),
        fingerprint: compatible.clone(),
        state: SessionState::Ready,
        created_at: OffsetDateTime::now_utc(),
        last_resumed_at: None,
        closed_at: None,
    };

    assert!(matches!(
        session::resolve(std::slice::from_ref(&session), agent_id, scope, &compatible),
        ResolveDecision::Resume(_)
    ));
    assert!(matches!(
        session::resolve(
            std::slice::from_ref(&session),
            agent_id,
            scope,
            &fingerprint(2, "workspace-a")
        ),
        ResolveDecision::Create {
            generation: 4,
            close: Some(_)
        }
    ));
}

#[test]
fn supervisor_freezes_deliveries_at_finalizing_and_deduplicates_sequence() {
    let thread_id = thread_id();
    let first_item = item_id();
    let mut run = local_run(None, thread_id, [(first_item, None, thread_id)]);
    run.begin_start().unwrap();
    run.started(SessionScope::Thread(thread_id), 1).unwrap();
    assert!(
        !run.attach(1, claimed_item(first_item, None, thread_id))
            .unwrap()
    );
    run.begin_finalizing().unwrap();

    assert_eq!(
        run.attach(2, claimed_item(item_id(), None, thread_id)),
        Err(crate::computer::core::CoreError::RunNotAcceptingDeliveries)
    );
}

#[test]
fn debug_output_excludes_fencing_locator_and_message_content() {
    let thread_id = thread_id();
    let run = local_run(None, thread_id, []);
    let session = ProviderSession {
        agent_id: run.agent_id,
        scope: SessionScope::Thread(thread_id),
        generation: 1,
        locator: "provider-secret-locator".to_owned(),
        fingerprint: fingerprint(1, "workspace-a"),
        state: SessionState::Ready,
        created_at: OffsetDateTime::now_utc(),
        last_resumed_at: None,
        closed_at: None,
    };

    let output = format!("{run:?} {session:?}");

    assert!(!output.contains("secret-token"));
    assert!(!output.contains("provider-secret-locator"));
    assert!(!output.contains("message body"));
}

#[tokio::test]
async fn duplicate_start_command_does_not_start_a_second_driver_turn() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let thread_id = thread_id();
    let run = local_run(None, thread_id, []);
    let command = Command::Start {
        run: Box::new(run),
        fingerprint: fingerprint(1, "workspace-a"),
    };
    let command_id = command_id();

    assert_eq!(
        CommandService::execute(
            &mut store,
            &mut driver,
            &mut MemoryHome::default(),
            command_id,
            1,
            command.clone(),
        )
        .await
        .unwrap()
        .status,
        CommandStatus::Applied
    );
    CommandService::execute(
        &mut store,
        &mut driver,
        &mut MemoryHome::default(),
        command_id,
        1,
        command,
    )
    .await
    .unwrap();
    SchedulerService::dispatch(&mut store, &mut driver, 1)
        .await
        .unwrap();

    assert_eq!(driver.open_count, 1);
    assert_eq!(driver.start_count, 1);
    assert_eq!(store.state.events.len(), 1);
}

#[tokio::test]
async fn rejected_command_replays_the_stored_error() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let first = local_run(None, thread_id(), []);
    let run_id = first.id;
    CommandService::execute(
        &mut store,
        &mut driver,
        &mut MemoryHome::default(),
        command_id(),
        1,
        Command::Start {
            run: Box::new(first),
            fingerprint: fingerprint(1, "workspace-a"),
        },
    )
    .await
    .unwrap();
    let mut conflicting = local_run(None, thread_id(), []);
    conflicting.id = run_id;
    let command = Command::Start {
        run: Box::new(conflicting),
        fingerprint: fingerprint(1, "workspace-a"),
    };
    let command_id = command_id();

    let first_result = CommandService::execute(
        &mut store,
        &mut driver,
        &mut MemoryHome::default(),
        command_id,
        2,
        command.clone(),
    )
    .await
    .unwrap();
    let replay = CommandService::execute(
        &mut store,
        &mut driver,
        &mut MemoryHome::default(),
        command_id,
        2,
        command,
    )
    .await
    .unwrap();

    assert_eq!(first_result.status, CommandStatus::Rejected);
    assert_eq!(first_result.error, Some(ApplicationError::Conflict));
    assert_eq!(replay, first_result);
}

#[tokio::test]
async fn incompatible_agent_configuration_closes_every_warm_session() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let mut homes = MemoryHome::default();
    let agent = local_agent(DriverKind::Codex, 1);
    let agent_id = agent.agent_id;
    homes.provision(agent.clone()).await.unwrap();
    store.state.sessions.push(ProviderSession {
        agent_id,
        scope: SessionScope::Thread(thread_id()),
        generation: 1,
        locator: "provider-secret-locator".to_owned(),
        fingerprint: fingerprint(1, "workspace"),
        state: SessionState::Ready,
        created_at: OffsetDateTime::now_utc(),
        last_resumed_at: None,
        closed_at: None,
    });
    let mut configured = agent;
    configured.driver = DriverKind::Builtin;
    configured.role_revision = 2;

    CommandService::execute(
        &mut store,
        &mut driver,
        &mut homes,
        command_id(),
        1,
        Command::Configure { agent: configured },
    )
    .await
    .unwrap();

    assert_eq!(driver.close_count, 1);
    assert_eq!(store.state.sessions[0].state, SessionState::Closed);
    assert!(store.state.sessions[0].locator.is_empty());
    assert_eq!(
        homes.agent(agent_id).await.unwrap().driver,
        DriverKind::Builtin
    );
}

#[tokio::test]
async fn failed_driver_validation_preserves_existing_profile_and_session() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver {
        fail_validation: true,
        ..FakeDriver::default()
    };
    let mut homes = MemoryHome::default();
    let agent = local_agent(DriverKind::Codex, 1);
    let agent_id = agent.agent_id;
    homes.provision(agent.clone()).await.unwrap();
    store.state.sessions.push(ProviderSession {
        agent_id,
        scope: SessionScope::Thread(thread_id()),
        generation: 1,
        locator: "provider-secret-locator".to_owned(),
        fingerprint: fingerprint(1, "workspace"),
        state: SessionState::Ready,
        created_at: OffsetDateTime::now_utc(),
        last_resumed_at: None,
        closed_at: None,
    });
    let mut configured = agent;
    configured.driver = DriverKind::Builtin;
    let command_id = command_id();

    assert_eq!(
        CommandService::execute(
            &mut store,
            &mut driver,
            &mut homes,
            command_id,
            1,
            Command::Configure { agent: configured },
        )
        .await,
        Err(ApplicationError::DriverUnavailable)
    );

    assert_eq!(
        store.state.commands[&command_id].status,
        CommandStatus::Pending
    );
    assert_eq!(store.state.sessions[0].state, SessionState::Ready);
    assert_eq!(
        homes.agent(agent_id).await.unwrap().driver,
        DriverKind::Codex
    );
}

#[tokio::test]
async fn retire_closes_idle_sessions_before_removing_agent_home() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let mut homes = MemoryHome::default();
    let agent = local_agent(DriverKind::Codex, 1);
    let agent_id = agent.agent_id;
    homes.provision(agent).await.unwrap();
    store.state.sessions.push(ProviderSession {
        agent_id,
        scope: SessionScope::Thread(thread_id()),
        generation: 1,
        locator: "provider-secret-locator".to_owned(),
        fingerprint: fingerprint(1, "workspace"),
        state: SessionState::Ready,
        created_at: OffsetDateTime::now_utc(),
        last_resumed_at: None,
        closed_at: None,
    });

    CommandService::execute(
        &mut store,
        &mut driver,
        &mut homes,
        command_id(),
        1,
        Command::Retire { agent_id },
    )
    .await
    .unwrap();

    assert_eq!(driver.close_count, 1);
    assert_eq!(store.state.sessions[0].state, SessionState::Closed);
    assert_eq!(homes.agent(agent_id).await, Err(ApplicationError::NotFound));
}

#[tokio::test]
async fn application_scheduler_enforces_capacity_and_releases_terminal_run_slot() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let first = local_run(None, thread_id(), []);
    let second = local_run(None, thread_id(), []);
    let first_id = first.id;
    let second_id = second.id;
    for (sequence, run) in [(1, first), (2, second)] {
        CommandService::execute(
            &mut store,
            &mut driver,
            &mut MemoryHome::default(),
            command_id(),
            sequence,
            Command::Start {
                run: Box::new(run),
                fingerprint: fingerprint(1, "workspace-a"),
            },
        )
        .await
        .unwrap();
    }

    SchedulerService::dispatch(&mut store, &mut driver, 1)
        .await
        .unwrap();
    let states = [
        store.state.runs[&first_id].state,
        store.state.runs[&second_id].state,
    ];
    assert_eq!(
        states
            .iter()
            .filter(|state| **state == LocalRunState::Running)
            .count(),
        1
    );
    assert_eq!(
        states
            .iter()
            .filter(|state| **state == LocalRunState::Queued)
            .count(),
        1
    );
    let running_id = if store.state.runs[&first_id].state == LocalRunState::Running {
        first_id
    } else {
        second_id
    };
    RunService::finish(
        &mut store,
        running_id,
        TerminalStatus::Completed,
        Vec::new(),
        None,
        None,
    )
    .await
    .unwrap();
    SchedulerService::dispatch(&mut store, &mut driver, 1)
        .await
        .unwrap();

    assert_eq!(driver.start_count, 2);
    assert_eq!(
        store
            .state
            .runs
            .values()
            .filter(|run| run.state == LocalRunState::Running)
            .count(),
        1
    );
}

#[tokio::test]
async fn second_run_resumes_task_session_and_resume_loss_creates_new_generation() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let agent_id = agent_id();
    let task_id = task_id();
    let thread_id = thread_id();
    let first = LocalRun::new(NewRun {
        id: run_id(),
        agent_id,
        task_id: Some(task_id),
        focus_thread_id: thread_id,
        fencing_token: FencingToken::new("first-token".to_owned()),
        priority: default_priority(true),
        ownership_lease_expires_at: OffsetDateTime::now_utc() + Duration::minutes(5),
        input: test_input(agent_id, Some(task_id), thread_id, []),
    })
    .unwrap();
    let first_id = first.id;
    store.state.runs.insert(first_id, first);
    RunService::start(
        &mut store,
        &mut driver,
        first_id,
        fingerprint(1, "workspace-a"),
    )
    .await
    .unwrap();
    RunService::finish(
        &mut store,
        first_id,
        TerminalStatus::Completed,
        Vec::new(),
        None,
        None,
    )
    .await
    .unwrap();

    let second = LocalRun::new(NewRun {
        id: run_id(),
        agent_id,
        task_id: Some(task_id),
        focus_thread_id: thread_id,
        fencing_token: FencingToken::new("second-token".to_owned()),
        priority: default_priority(true),
        ownership_lease_expires_at: OffsetDateTime::now_utc() + Duration::minutes(5),
        input: test_input(agent_id, Some(task_id), thread_id, []),
    })
    .unwrap();
    let second_id = second.id;
    store.state.runs.insert(second_id, second);
    driver.fail_next_resume = true;
    RunService::start(
        &mut store,
        &mut driver,
        second_id,
        fingerprint(1, "workspace-a"),
    )
    .await
    .unwrap();

    assert_eq!(driver.resume_count, 1);
    assert!(store.state.sessions.iter().any(|session| {
        session.scope == SessionScope::Task(task_id)
            && session.generation == 1
            && session.state == SessionState::Lost
    }));
    assert!(store.state.sessions.iter().any(|session| {
        session.scope == SessionScope::Task(task_id)
            && session.generation == 2
            && session.state == SessionState::InUse
    }));
}

#[tokio::test]
async fn task_binding_promotes_the_active_thread_session_without_changing_generation() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let thread_id = thread_id();
    let run = local_run(None, thread_id, []);
    let run_id = run.id;
    store.state.runs.insert(run_id, run);
    let session_fingerprint = fingerprint(1, "workspace-a");
    RunService::start(&mut store, &mut driver, run_id, session_fingerprint.clone())
        .await
        .unwrap();
    let task_id = task_id();

    RunService::bind_task(&mut store, run_id, task_id, session_fingerprint.clone())
        .await
        .unwrap();
    RunService::bind_task(&mut store, run_id, task_id, session_fingerprint)
        .await
        .unwrap();

    assert_eq!(store.state.runs[&run_id].task_id, Some(task_id));
    assert_eq!(store.state.sessions.len(), 1);
    assert_eq!(store.state.sessions[0].scope, SessionScope::Task(task_id));
    assert_eq!(store.state.sessions[0].generation, 1);
}

#[tokio::test]
async fn pending_command_is_replayed_after_restart() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let run = local_run(None, thread_id(), []);
    let run_id = run.id;
    let command_id = command_id();
    let command = Command::Start {
        run: Box::new(run),
        fingerprint: fingerprint(1, "workspace-a"),
    };
    store.state.commands.insert(
        command_id,
        StoredCommand {
            id: command_id,
            sequence: 7,
            fingerprint: command.fingerprint(),
            command,
            status: CommandStatus::Pending,
            error: None,
        },
    );

    RecoveryService::recover(&mut store, &mut driver, &mut MemoryHome::default(), 1)
        .await
        .unwrap();

    assert_eq!(
        store.state.commands[&command_id].status,
        CommandStatus::Applied
    );
    assert_eq!(store.state.runs[&run_id].state, LocalRunState::Running);
    assert_eq!(driver.start_count, 1);
}

#[tokio::test]
async fn duplicate_notice_is_delivered_once() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let thread_id = thread_id();
    let run = local_run(None, thread_id, []);
    let run_id = run.id;
    store.state.runs.insert(run_id, run);
    RunService::start(
        &mut store,
        &mut driver,
        run_id,
        fingerprint(1, "workspace-a"),
    )
    .await
    .unwrap();
    let notice_id = notice_id();
    let notice = attention_notice(notice_id, thread_id);

    RunService::notice(&mut store, &mut driver, run_id, notice.clone())
        .await
        .unwrap();
    RunService::notice(&mut store, &mut driver, run_id, notice)
        .await
        .unwrap();

    assert_eq!(driver.notice_count, 1);
}

#[tokio::test]
async fn repeated_delivery_steers_once_and_preserves_too_late_result() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver {
        steer_outcome: SteerOutcome::TooLate,
        ..FakeDriver::default()
    };
    let thread_id = thread_id();
    let run = local_run(None, thread_id, []);
    let run_id = run.id;
    store.state.runs.insert(run_id, run);
    RunService::start(
        &mut store,
        &mut driver,
        run_id,
        fingerprint(1, "workspace-a"),
    )
    .await
    .unwrap();
    let item_id = item_id();
    let item = claimed_item(item_id, None, thread_id);

    assert_eq!(
        RunService::attach(&mut store, &mut driver, run_id, 1, item.clone())
            .await
            .unwrap(),
        DeliveryState::TooLate
    );
    assert_eq!(
        RunService::attach(&mut store, &mut driver, run_id, 1, item)
            .await
            .unwrap(),
        DeliveryState::TooLate
    );
    assert_eq!(driver.steer_count, 1);
    assert_eq!(driver.steered_content.as_deref(), Some("steering body"));
    assert_eq!(
        store
            .state
            .events
            .values()
            .filter(|event| matches!(event, LocalEvent::Delivery { .. }))
            .count(),
        1
    );
    assert_eq!(
        RunService::finish(
            &mut store,
            run_id,
            TerminalStatus::Completed,
            vec![(item_id, ItemDisposition::Handled)],
            None,
            None,
        )
        .await,
        Err(ApplicationError::Core(
            crate::computer::core::CoreError::IncompleteItemDisposition
        ))
    );
    RunService::finish(
        &mut store,
        run_id,
        TerminalStatus::Completed,
        vec![(item_id, ItemDisposition::Released)],
        None,
        None,
    )
    .await
    .unwrap();
}

#[tokio::test]
async fn retryable_driver_error_keeps_command_pending_until_replay_succeeds() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver {
        fail_next_steer: true,
        ..FakeDriver::default()
    };
    let thread_id = thread_id();
    let run = local_run(None, thread_id, []);
    let run_id = run.id;
    store.state.runs.insert(run_id, run);
    RunService::start(
        &mut store,
        &mut driver,
        run_id,
        fingerprint(1, "workspace-a"),
    )
    .await
    .unwrap();
    let command = Command::Attach {
        run_id,
        sequence: 1,
        item: claimed_item(item_id(), None, thread_id),
    };
    let command_id = command_id();

    assert_eq!(
        CommandService::execute(
            &mut store,
            &mut driver,
            &mut MemoryHome::default(),
            command_id,
            8,
            command.clone(),
        )
        .await,
        Err(ApplicationError::DriverUnavailable)
    );
    assert_eq!(
        store.state.commands[&command_id].status,
        CommandStatus::Pending
    );
    let replay = CommandService::execute(
        &mut store,
        &mut driver,
        &mut MemoryHome::default(),
        command_id,
        8,
        command,
    )
    .await
    .unwrap();

    assert_eq!(replay.status, CommandStatus::Applied);
    assert_eq!(driver.steer_count, 2);
}

#[tokio::test]
async fn yield_writes_terminal_state_and_result_outbox_atomically() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let thread_id = thread_id();
    let claimed = item_id();
    let run = local_run(None, thread_id, [(claimed, None, thread_id)]);
    let run_id = run.id;
    store.state.runs.insert(run_id, run);
    RunService::start(
        &mut store,
        &mut driver,
        run_id,
        fingerprint(1, "workspace-a"),
    )
    .await
    .unwrap();

    let event_id = RunService::finish(
        &mut store,
        run_id,
        TerminalStatus::Yielded,
        vec![(claimed, ItemDisposition::Released)],
        Some("等待另一个 Focus".to_owned()),
        None,
    )
    .await
    .unwrap();

    assert_eq!(store.state.runs[&run_id].state, LocalRunState::Yielded);
    assert!(matches!(
        store.state.events[&event_id],
        LocalEvent::RunResult {
            status: TerminalStatus::Yielded,
            ..
        }
    ));
    assert_eq!(store.state.sessions[0].state, SessionState::Ready);
    RecoveryService::acknowledge(&mut store, event_id)
        .await
        .unwrap();
    assert!(!store.state.events.contains_key(&event_id));
}

#[tokio::test]
async fn completed_driver_turn_without_items_completes_run_once() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let thread_id = thread_id();
    let run = local_run(None, thread_id, []);
    let run_id = run.id;
    store.state.runs.insert(run_id, run);
    RunService::start(
        &mut store,
        &mut driver,
        run_id,
        fingerprint(1, "workspace-a"),
    )
    .await
    .unwrap();

    let event_id = RunService::finish_driver_turn(&mut store, run_id, DriverTurnOutcome::Completed)
        .await
        .unwrap()
        .unwrap();

    assert_eq!(store.state.runs[&run_id].state, LocalRunState::Completed);
    assert!(matches!(
        store.state.events[&event_id],
        LocalEvent::RunResult {
            status: TerminalStatus::Completed,
            ref item_outcomes,
            error_code: None,
            ..
        } if item_outcomes.is_empty()
    ));
    assert_eq!(
        RunService::finish_driver_turn(&mut store, run_id, DriverTurnOutcome::Completed,)
            .await
            .unwrap(),
        None
    );
    assert_eq!(
        store
            .state
            .events
            .values()
            .filter(|event| matches!(event, LocalEvent::RunResult { .. }))
            .count(),
        1
    );
}

#[tokio::test]
async fn completed_driver_turn_releases_unhandled_items_and_fails_run() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let thread_id = thread_id();
    let claimed = item_id();
    let run = local_run(None, thread_id, [(claimed, None, thread_id)]);
    let run_id = run.id;
    store.state.runs.insert(run_id, run);
    RunService::start(
        &mut store,
        &mut driver,
        run_id,
        fingerprint(1, "workspace-a"),
    )
    .await
    .unwrap();

    let event_id = RunService::finish_driver_turn(&mut store, run_id, DriverTurnOutcome::Completed)
        .await
        .unwrap()
        .unwrap();

    assert_eq!(store.state.runs[&run_id].state, LocalRunState::Failed);
    assert!(matches!(
        store.state.events[&event_id],
        LocalEvent::RunResult {
            status: TerminalStatus::Failed,
            ref item_outcomes,
            ..
        } if item_outcomes == &vec![(claimed, ItemDisposition::Released)]
    ));
}

#[tokio::test]
async fn completed_driver_turn_preserves_explicit_item_disposition() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let thread_id = thread_id();
    let claimed = item_id();
    let run = local_run(None, thread_id, [(claimed, None, thread_id)]);
    let run_id = run.id;
    store.state.runs.insert(run_id, run);
    RunService::start(
        &mut store,
        &mut driver,
        run_id,
        fingerprint(1, "workspace-a"),
    )
    .await
    .unwrap();
    RunService::record_item_disposition(&mut store, run_id, claimed, ItemDisposition::Handled)
        .await
        .unwrap();

    let event_id = RunService::finish_driver_turn(&mut store, run_id, DriverTurnOutcome::Completed)
        .await
        .unwrap()
        .unwrap();

    assert_eq!(store.state.runs[&run_id].state, LocalRunState::Completed);
    assert!(matches!(
        store.state.events[&event_id],
        LocalEvent::RunResult {
            status: TerminalStatus::Completed,
            ref item_outcomes,
            ..
        } if item_outcomes == &vec![(claimed, ItemDisposition::Handled)]
    ));
}

#[tokio::test]
async fn yield_atomically_preserves_dispositions_releases_remaining_and_wins_completion_race() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let thread_id = thread_id();
    let handled = item_id();
    let remaining = item_id();
    let run = local_run(
        None,
        thread_id,
        [(handled, None, thread_id), (remaining, None, thread_id)],
    );
    let run_id = run.id;
    store.state.runs.insert(run_id, run);
    RunService::start(
        &mut store,
        &mut driver,
        run_id,
        fingerprint(1, "workspace-a"),
    )
    .await
    .unwrap();
    RunService::record_item_disposition(&mut store, run_id, handled, ItemDisposition::Handled)
        .await
        .unwrap();

    let event_id = RunService::yield_run(
        &mut store,
        run_id,
        Some("continue after higher-priority work".to_owned()),
    )
    .await
    .unwrap();

    assert_eq!(store.state.runs[&run_id].state, LocalRunState::Yielded);
    assert!(matches!(
        store.state.events[&event_id],
        LocalEvent::RunResult {
            status: TerminalStatus::Yielded,
            ref item_outcomes,
            continuation_note: Some(ref note),
            ..
        } if item_outcomes == &vec![
            (handled, ItemDisposition::Handled),
            (remaining, ItemDisposition::Released),
        ] && note == "continue after higher-priority work"
    ));
    assert_eq!(
        RunService::finish_driver_turn(&mut store, run_id, DriverTurnOutcome::Interrupted)
            .await
            .unwrap(),
        None
    );
    assert_eq!(
        store
            .state
            .events
            .values()
            .filter(|event| matches!(event, LocalEvent::RunResult { .. }))
            .count(),
        1
    );
}

#[tokio::test]
async fn restart_marks_uncontrolled_process_failed_and_keeps_result_for_retry() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver {
        process_evidence: ProcessEvidence::Lost,
        ..FakeDriver::default()
    };
    let thread_id = thread_id();
    let claimed = item_id();
    let mut run = local_run(None, thread_id, [(claimed, None, thread_id)]);
    run.begin_start().unwrap();
    run.started(SessionScope::Thread(thread_id), 1).unwrap();
    let run_id = run.id;
    store.state.runs.insert(run_id, run);

    RecoveryService::recover(&mut store, &mut driver, &mut MemoryHome::default(), 1)
        .await
        .unwrap();

    assert_eq!(store.state.runs[&run_id].state, LocalRunState::Failed);
    assert_eq!(driver.steer_count, 0);
    assert!(
        RecoveryService::pending_results(&mut store)
            .await
            .unwrap()
            .iter()
            .any(|event| matches!(
                event,
                LocalEvent::RunResult {
                    status: TerminalStatus::Failed,
                    error_code: Some(LocalErrorCode::ProcessLost),
                    ..
                }
            ))
    );
}

#[tokio::test]
async fn restart_stops_a_run_whose_ownership_lease_expired() {
    let mut store = MemoryPort::default();
    let mut driver = FakeDriver::default();
    let thread_id = thread_id();
    let claimed = item_id();
    let mut run = local_run(None, thread_id, [(claimed, None, thread_id)]);
    run.begin_start().unwrap();
    run.started(SessionScope::Thread(thread_id), 1).unwrap();
    run.ownership_lease_expires_at = OffsetDateTime::now_utc() - Duration::seconds(1);
    let run_id = run.id;
    store.state.runs.insert(run_id, run);

    RecoveryService::recover(&mut store, &mut driver, &mut MemoryHome::default(), 1)
        .await
        .unwrap();

    assert_eq!(driver.interrupt_count, 1);
    assert_eq!(store.state.runs[&run_id].state, LocalRunState::Failed);
}

#[tokio::test]
async fn stop_cancels_a_queued_run_without_starting_or_interrupting_driver() {
    let thread_id = thread_id();
    let run = local_run(None, thread_id, []);
    let run_id = run.id;
    let mut store = MemoryPort::default();
    store.state.runs.insert(run_id, run);
    let mut driver = FakeDriver::default();

    RunService::stop(&mut store, &mut driver, run_id)
        .await
        .unwrap();

    assert_eq!(store.state.runs[&run_id].state, LocalRunState::Canceled);
    assert_eq!(driver.start_count, 0);
    assert_eq!(driver.interrupt_count, 0);
    assert!(
        store.state.events.values().any(
            |event| matches!(event, LocalEvent::RunResult { run_id: id, .. } if *id == run_id)
        )
    );
}

fn pending(
    agent_id: AgentId,
    available_at: OffsetDateTime,
    explicit_human_redirect: bool,
    strength: WorkStrength,
) -> PendingRun {
    PendingRun {
        run_id: run_id(),
        agent_id,
        explicit_human_redirect,
        strength,
        available_at,
        has_task_continuity: false,
    }
}

fn local_run<const N: usize>(
    task_id: Option<TaskId>,
    thread_id: ThreadId,
    items: [(InboxItemId, Option<TaskId>, ThreadId); N],
) -> LocalRun {
    let agent_id = agent_id();
    LocalRun::new(NewRun {
        id: run_id(),
        agent_id,
        task_id,
        focus_thread_id: thread_id,
        fencing_token: FencingToken::new("secret-token".to_owned()),
        priority: default_priority(task_id.is_some()),
        ownership_lease_expires_at: OffsetDateTime::now_utc() + Duration::minutes(5),
        input: test_input(agent_id, task_id, thread_id, items),
    })
    .unwrap()
}

fn test_input<const N: usize>(
    agent_id: AgentId,
    task_id: Option<TaskId>,
    thread_id: ThreadId,
    items: [(InboxItemId, Option<TaskId>, ThreadId); N],
) -> RunInput {
    RunInput {
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
            task: task_id.map(|task_id| TaskInput {
                task_id,
                title: "task".to_owned(),
                status: "in_progress".to_owned(),
            }),
            linked_thread_ids: vec![thread_id],
            public_result_message_id: None,
        },
        context: RunContextInput {
            focus_thread_id: thread_id,
            message_snapshot_sequence: 1,
            focus_messages: vec!["message body".to_owned()],
            claimed_items: items
                .into_iter()
                .map(|(item_id, task_id, thread_id)| claimed_item(item_id, task_id, thread_id))
                .collect(),
        },
    }
}

fn claimed_item(
    item_id: InboxItemId,
    task_id: Option<TaskId>,
    thread_id: ThreadId,
) -> ClaimedItemInput {
    ClaimedItemInput {
        item_id,
        task_id,
        thread_id,
        content: Some("steering body".to_owned()),
    }
}

fn attention_notice(notice_id: NoticeId, thread_id: ThreadId) -> AttentionNoticeInput {
    AttentionNoticeInput {
        notice_id,
        source_kind: "mention".to_owned(),
        location: NoticeLocationInput::Visible {
            task_id: None,
            thread_id,
        },
        explicit_human_redirect: false,
        arrived_at: OffsetDateTime::now_utc(),
    }
}

fn default_priority(has_task_continuity: bool) -> RunPriority {
    RunPriority {
        explicit_human_redirect: false,
        strength: WorkStrength::Hard,
        available_at: OffsetDateTime::now_utc(),
        has_task_continuity,
    }
}

fn fingerprint(role_revision: u64, workspace: &str) -> SessionFingerprint {
    SessionFingerprint {
        driver: DriverKind::Codex,
        workspace: workspace.to_owned(),
        role_revision,
        audience: "audience".to_owned(),
    }
}

fn local_agent(driver: DriverKind, role_revision: u64) -> LocalAgent {
    LocalAgent {
        agent_id: agent_id(),
        space_id: SpaceId::from_uuid(Uuid::now_v7()),
        name: "agent".to_owned(),
        handle: "agent".to_owned(),
        role_revision,
        role: "role".to_owned(),
        driver,
        state: crate::computer::core::home::LocalAgentState::Active,
    }
}

fn agent_id() -> AgentId {
    AgentId::from_uuid(Uuid::now_v7())
}

fn command_id() -> CommandId {
    CommandId::from_uuid(Uuid::now_v7())
}

fn item_id() -> InboxItemId {
    InboxItemId::from_uuid(Uuid::now_v7())
}

fn run_id() -> RunId {
    RunId::from_uuid(Uuid::now_v7())
}

fn task_id() -> TaskId {
    TaskId::from_uuid(Uuid::now_v7())
}

fn thread_id() -> ThreadId {
    ThreadId::from_uuid(Uuid::now_v7())
}

fn notice_id() -> NoticeId {
    NoticeId::from_uuid(Uuid::now_v7())
}
