use std::collections::{BTreeSet, HashMap, HashSet};

use time::OffsetDateTime;
use uuid::Uuid;

use crate::ids::{
    ChannelId, ComputerId, EventId, IdempotencyKey, InboxItemId, MemberId, MessageId, RunId,
    SpaceId, TaskId, ThreadId,
};
use crate::server::domain::{
    attention::{
        AttentionStrength, InboxItem, InboxItemDisposition, InboxItemKind, InboxItemStatus,
    },
    conversation::{Channel, Message, MessageContent, MessagePlacement, Thread},
    execution::{Run, RunItem, RunOutcome, RunStatus},
    identity::{Agent, AgentLifecycle, Computer, ComputerLifecycle, PermissionAction},
    task::{CloseReason, Task, TaskStatus},
};

use super::{
    attention::{AttachHardItem, AttachHardItemInput},
    conversation::{CreateChannelAction, CreateChannelActionInput},
    execution::{ClaimRun, ClaimRunInput, CompleteRun, CompleteRunInput, ItemDispositionInput},
    identity::{DeleteComputer, RetireAgent},
    ports::{ApplicationError, Effect, ServerTransaction, TransactionPort},
    task::{
        CompleteTask, CompleteTaskInput, CreateTaskFromRootMessage, CreateTaskInput,
        LinkThreadInput, LinkThreadToTask, TaskAction, TaskSource, UpdateTask, UpdateTaskInput,
    },
};

#[derive(Clone, Default)]
struct MemoryState {
    threads: HashMap<ThreadId, Thread>,
    channels: HashMap<ChannelId, Channel>,
    roots: HashMap<ThreadId, Message>,
    tasks: HashMap<TaskId, Task>,
    runs: HashMap<RunId, Run>,
    items: HashMap<InboxItemId, InboxItem>,
    messages: HashMap<MessageId, Message>,
    agents: HashMap<MemberId, Agent>,
    computers: HashMap<ComputerId, Computer>,
    idempotency: HashMap<(MemberId, IdempotencyKey), TaskId>,
    completed_run_events: HashMap<EventId, RunId>,
    assignable_agents: HashSet<MemberId>,
    permissions: HashSet<(MemberId, PermissionAction)>,
    effects: Vec<Effect>,
    reject_message_insert: bool,
}

#[derive(Default)]
struct MemoryPort {
    state: MemoryState,
}

struct MemoryTransaction<'a> {
    state: &'a mut MemoryState,
}

impl TransactionPort for MemoryPort {
    type Transaction<'a> = MemoryTransaction<'a>;

    fn transact<T>(
        &mut self,
        operation: impl FnOnce(&mut Self::Transaction<'_>) -> Result<T, ApplicationError>,
    ) -> Result<T, ApplicationError> {
        let mut staged = self.state.clone();
        let result = operation(&mut MemoryTransaction { state: &mut staged })?;
        self.state = staged;
        Ok(result)
    }
}

impl ServerTransaction for MemoryTransaction<'_> {
    fn thread(&mut self, id: ThreadId) -> Result<Thread, ApplicationError> {
        self.state
            .threads
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    fn root_message(&mut self, thread_id: ThreadId) -> Result<Message, ApplicationError> {
        self.state
            .roots
            .get(&thread_id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    fn task(&mut self, id: TaskId) -> Result<Task, ApplicationError> {
        self.state
            .tasks
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    fn run(&mut self, id: RunId) -> Result<Run, ApplicationError> {
        self.state
            .runs
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    fn inbox_item(&mut self, id: InboxItemId) -> Result<InboxItem, ApplicationError> {
        self.state
            .items
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    fn agent(&mut self, id: MemberId) -> Result<Agent, ApplicationError> {
        self.state
            .agents
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    fn computer(&mut self, id: ComputerId) -> Result<Computer, ApplicationError> {
        self.state
            .computers
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    fn task_for_source(&mut self, thread_id: ThreadId) -> Option<TaskId> {
        self.state
            .tasks
            .values()
            .find(|task| task.source_thread_id == thread_id)
            .map(|task| task.id)
    }

    fn unfinished_task_for_thread(&mut self, thread_id: ThreadId) -> Option<TaskId> {
        self.state
            .tasks
            .values()
            .find(|task| !task.status.is_finished() && task.linked_to(thread_id))
            .map(|task| task.id)
    }

    fn task_for_idempotency(&mut self, actor: MemberId, key: IdempotencyKey) -> Option<TaskId> {
        self.state.idempotency.get(&(actor, key)).copied()
    }

    fn active_run_for_agent(&mut self, agent_id: MemberId) -> Option<RunId> {
        self.state
            .runs
            .values()
            .find(|run| run.agent_id == agent_id && run.status.is_active())
            .map(|run| run.id)
    }

    fn computer_has_assigned_agents(&mut self, computer_id: ComputerId) -> bool {
        self.state
            .agents
            .values()
            .any(|agent| agent.computer_id == Some(computer_id))
    }

    fn completed_run_for_event(&mut self, event_id: EventId) -> Option<RunId> {
        self.state.completed_run_events.get(&event_id).copied()
    }

    fn can_read_thread(&mut self, actor: MemberId, thread_id: ThreadId) -> bool {
        self.state
            .threads
            .get(&thread_id)
            .is_some_and(|thread| thread.audience.contains(&actor))
    }

    fn can_link_thread(&mut self, actor: MemberId, task: &Task, target: &Thread) -> bool {
        self.can_read_thread(actor, task.source_thread_id) && target.audience.contains(&actor)
    }

    fn can_assign_agent(&mut self, agent: MemberId, source: &Thread) -> bool {
        self.state.assignable_agents.contains(&agent) && source.audience.contains(&agent)
    }

    fn can_govern_task(&mut self, _actor: MemberId, _task: &Task) -> bool {
        false
    }

    fn has_permission(&mut self, actor: MemberId, action: PermissionAction) -> bool {
        self.state.permissions.contains(&(actor, action))
    }

    fn insert_task(&mut self, task: Task) -> Result<(), ApplicationError> {
        if self.state.tasks.insert(task.id, task).is_some() {
            return Err(ApplicationError::Conflict);
        }
        Ok(())
    }

    fn save_task(&mut self, task: Task) -> Result<(), ApplicationError> {
        self.state.tasks.insert(task.id, task);
        Ok(())
    }

    fn save_run(&mut self, run: Run) -> Result<(), ApplicationError> {
        self.state.runs.insert(run.id, run);
        Ok(())
    }

    fn save_inbox_item(&mut self, item: InboxItem) -> Result<(), ApplicationError> {
        self.state.items.insert(item.id, item);
        Ok(())
    }

    fn insert_message(&mut self, message: Message) -> Result<(), ApplicationError> {
        if self.state.reject_message_insert {
            return Err(ApplicationError::Conflict);
        }
        self.state.messages.insert(message.id, message);
        Ok(())
    }

    fn insert_channel(&mut self, channel: Channel) -> Result<(), ApplicationError> {
        if self.state.channels.insert(channel.id, channel).is_some() {
            return Err(ApplicationError::Conflict);
        }
        Ok(())
    }

    fn save_agent(&mut self, agent: Agent) -> Result<(), ApplicationError> {
        self.state.agents.insert(agent.member_id, agent);
        Ok(())
    }

    fn save_computer(&mut self, computer: Computer) -> Result<(), ApplicationError> {
        self.state.computers.insert(computer.id, computer);
        Ok(())
    }

    fn record_completed_run_event(
        &mut self,
        event_id: EventId,
        run_id: RunId,
    ) -> Result<(), ApplicationError> {
        self.state.completed_run_events.insert(event_id, run_id);
        Ok(())
    }

    fn record_task_idempotency(
        &mut self,
        actor: MemberId,
        key: IdempotencyKey,
        task_id: TaskId,
    ) -> Result<(), ApplicationError> {
        self.state.idempotency.insert((actor, key), task_id);
        Ok(())
    }

    fn emit(&mut self, effect: Effect) {
        self.state.effects.push(effect);
    }
}

#[test]
fn agent_task_creation_atomically_binds_run_items_and_retries_idempotently() {
    let now = OffsetDateTime::UNIX_EPOCH;
    let agent = member(1);
    let thread_id = thread(2);
    let item_id = item(3);
    let run_id = run(4);
    let task_id = task(5);
    let key = idempotency(6);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, thread_id, &[agent]);
    port.state.assignable_agents.insert(agent);
    port.state.items.insert(
        item_id,
        inbox(item_id, agent, thread_id, None, InboxItemStatus::Leased),
    );
    port.state.runs.insert(
        run_id,
        running_run(run_id, agent, thread_id, None, vec![item_id]),
    );

    let created = CreateTaskFromRootMessage::execute(
        &mut port,
        CreateTaskInput {
            task_id,
            actor_member_id: agent,
            source: TaskSource::AgentRun(run_id),
            title: "重建领域层".into(),
            assignee_agent_member_id: None,
            idempotency_key: key,
            now,
        },
    )
    .unwrap();

    assert_eq!(created.status, TaskStatus::InProgress);
    assert_eq!(port.state.runs[&run_id].task_id, Some(task_id));
    assert_eq!(port.state.items[&item_id].task_id, Some(task_id));

    let retried = CreateTaskFromRootMessage::execute(
        &mut port,
        CreateTaskInput {
            task_id: task(99),
            actor_member_id: agent,
            source: TaskSource::AgentRun(run_id),
            title: "不会覆盖".into(),
            assignee_agent_member_id: None,
            idempotency_key: key,
            now,
        },
    )
    .unwrap();
    assert_eq!(retried.id, task_id);
    assert_eq!(port.state.tasks.len(), 1);
}

#[test]
fn reply_cannot_create_task_and_transaction_leaves_no_effects() {
    let actor = member(10);
    let thread_id = thread(11);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, thread_id, &[actor]);
    port.state.roots.get_mut(&thread_id).unwrap().placement = MessagePlacement::Reply;

    let error = CreateTaskFromRootMessage::execute(
        &mut port,
        CreateTaskInput {
            task_id: task(12),
            actor_member_id: actor,
            source: TaskSource::HumanRoot(thread_id),
            title: "非法来源".into(),
            assignee_agent_member_id: None,
            idempotency_key: idempotency(13),
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .unwrap_err();

    assert!(matches!(
        error,
        ApplicationError::Domain(crate::server::domain::DomainError::SourceIsNotRoot)
    ));
    assert!(port.state.tasks.is_empty());
    assert!(port.state.effects.is_empty());
}

#[test]
fn linking_rejects_incompatible_audience_and_another_unfinished_task() {
    let actor = member(20);
    let other = member(21);
    let source = thread(22);
    let incompatible = thread(23);
    let occupied = thread(24);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, source, &[actor]);
    insert_thread(&mut port, incompatible, &[actor, other]);
    insert_thread(&mut port, occupied, &[actor]);
    let first = make_task(task(25), source, actor, TaskStatus::Todo);
    let mut occupying = make_task(task(26), occupied, actor, TaskStatus::Todo);
    occupying.source_thread_id = occupied;
    port.state.tasks.insert(first.id, first);
    port.state.tasks.insert(occupying.id, occupying);

    let incompatible_error = LinkThreadToTask::execute(
        &mut port,
        LinkThreadInput {
            task_id: task(25),
            target_thread_id: incompatible,
            actor_member_id: actor,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .unwrap_err();
    assert!(matches!(
        incompatible_error,
        ApplicationError::Domain(crate::server::domain::DomainError::IncompatibleAudience)
    ));

    let occupied_error = LinkThreadToTask::execute(
        &mut port,
        LinkThreadInput {
            task_id: task(25),
            target_thread_id: occupied,
            actor_member_id: actor,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .unwrap_err();
    assert_eq!(occupied_error, ApplicationError::Conflict);
}

#[test]
fn claim_rejects_parallel_active_run_and_task_focus_outside_links() {
    let agent = member(30);
    let focus = thread(31);
    let other_focus = thread(32);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent]);
    insert_thread(&mut port, other_focus, &[agent]);
    port.state.tasks.insert(
        task(33),
        make_task(task(33), focus, agent, TaskStatus::InProgress),
    );
    port.state.runs.insert(
        run(34),
        running_run(run(34), agent, focus, Some(task(33)), Vec::new()),
    );

    let conflict = ClaimRun::execute(
        &mut port,
        claim_input(run(35), agent, Some(task(33)), focus),
    )
    .unwrap_err();
    assert_eq!(conflict, ApplicationError::Conflict);

    port.state.runs.clear();
    let focus_error = ClaimRun::execute(
        &mut port,
        claim_input(run(36), agent, Some(task(33)), other_focus),
    )
    .unwrap_err();
    assert!(matches!(
        focus_error,
        ApplicationError::Domain(crate::server::domain::DomainError::FocusOutsideTask)
    ));
}

#[test]
fn attach_loses_finalizing_race_without_leasing_item() {
    let agent = member(40);
    let focus = thread(41);
    let run_id = run(42);
    let item_id = item(43);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent]);
    let mut current = running_run(run_id, agent, focus, None, Vec::new());
    current.status = RunStatus::Finalizing;
    port.state.runs.insert(run_id, current);
    port.state.items.insert(
        item_id,
        inbox(item_id, agent, focus, None, InboxItemStatus::Pending),
    );

    let error = AttachHardItem::execute(
        &mut port,
        AttachHardItemInput {
            run_id,
            item_id,
            lease_expires_at: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .unwrap_err();
    assert!(matches!(
        error,
        ApplicationError::Domain(crate::server::domain::DomainError::RunNotAcceptingItems)
    ));
    assert_eq!(port.state.items[&item_id].status, InboxItemStatus::Pending);
}

#[test]
fn run_completion_checks_fencing_and_does_not_complete_task() {
    let agent = member(50);
    let focus = thread(51);
    let task_id = task(52);
    let run_id = run(53);
    let item_id = item(54);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent]);
    port.state.tasks.insert(
        task_id,
        make_task(task_id, focus, agent, TaskStatus::InProgress),
    );
    port.state.items.insert(
        item_id,
        inbox(
            item_id,
            agent,
            focus,
            Some(task_id),
            InboxItemStatus::Leased,
        ),
    );
    port.state.runs.insert(
        run_id,
        running_run(run_id, agent, focus, Some(task_id), vec![item_id]),
    );

    let stale =
        CompleteRun::execute(&mut port, complete_run_input(run_id, "stale", item_id)).unwrap_err();
    assert!(matches!(
        stale,
        ApplicationError::Domain(crate::server::domain::DomainError::StaleFencingToken)
    ));

    let completed =
        CompleteRun::execute(&mut port, complete_run_input(run_id, "token", item_id)).unwrap();
    assert_eq!(completed.status, RunStatus::Completed);
    let retried =
        CompleteRun::execute(&mut port, complete_run_input(run_id, "token", item_id)).unwrap();
    assert_eq!(retried.status, RunStatus::Completed);
    assert_eq!(port.state.items[&item_id].status, InboxItemStatus::Handled);
    assert_eq!(port.state.tasks[&task_id].status, TaskStatus::InProgress);
}

#[test]
fn result_message_failure_rolls_back_task_completion() {
    let assignee = member(60);
    let focus = thread(61);
    let task_id = task(62);
    let result_id = message(63);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[assignee]);
    port.state.tasks.insert(
        task_id,
        make_task(task_id, focus, assignee, TaskStatus::InProgress),
    );
    port.state.reject_message_insert = true;

    let error = CompleteTask::execute(
        &mut port,
        CompleteTaskInput {
            task_id,
            actor_member_id: assignee,
            result_message_id: result_id,
            result_thread_id: focus,
            result_markdown: "完成".into(),
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .unwrap_err();
    assert_eq!(error, ApplicationError::Conflict);
    assert_eq!(port.state.tasks[&task_id].status, TaskStatus::InProgress);
    assert!(!port.state.messages.contains_key(&result_id));
    assert!(port.state.effects.is_empty());
}

#[test]
fn computer_delete_requires_explicit_agent_retirement() {
    let computer_id = computer(70);
    let agent_id = member(71);
    let mut port = MemoryPort::default();
    port.state.computers.insert(
        computer_id,
        Computer {
            id: computer_id,
            space_id: space(1),
            lifecycle: ComputerLifecycle::Offline,
            token_hash: Some("hash".into()),
            deleted_at: None,
        },
    );
    port.state.agents.insert(
        agent_id,
        Agent {
            member_id: agent_id,
            space_id: space(1),
            computer_id: Some(computer_id),
            role_text: "实现".into(),
            role_revision: 1,
            lifecycle: AgentLifecycle::Active,
            retired_at: None,
        },
    );

    let blocked =
        DeleteComputer::execute(&mut port, computer_id, OffsetDateTime::UNIX_EPOCH).unwrap_err();
    assert!(matches!(
        blocked,
        ApplicationError::Domain(crate::server::domain::DomainError::ComputerHasAgents)
    ));
    assert_eq!(
        port.state.computers[&computer_id].lifecycle,
        ComputerLifecycle::Offline
    );

    RetireAgent::execute(&mut port, agent_id, OffsetDateTime::UNIX_EPOCH).unwrap();
    let deleted =
        DeleteComputer::execute(&mut port, computer_id, OffsetDateTime::UNIX_EPOCH).unwrap();
    assert_eq!(deleted.lifecycle, ComputerLifecycle::Deleted);
    assert!(deleted.token_hash.is_none());
}

#[test]
fn task_review_requires_another_visible_member_and_closed_is_terminal() {
    let assignee = member(90);
    let reviewer = member(91);
    let focus = thread(92);
    let task_id = task(93);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[assignee, reviewer]);
    port.state.assignable_agents.insert(assignee);
    port.state.tasks.insert(
        task_id,
        make_task(task_id, focus, assignee, TaskStatus::InProgress),
    );

    UpdateTask::execute(
        &mut port,
        UpdateTaskInput {
            task_id,
            actor_member_id: assignee,
            action: TaskAction::SubmitReview,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .unwrap();
    let self_review = UpdateTask::execute(
        &mut port,
        UpdateTaskInput {
            task_id,
            actor_member_id: assignee,
            action: TaskAction::RequestChanges,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .unwrap_err();
    assert!(matches!(
        self_review,
        ApplicationError::Domain(crate::server::domain::DomainError::InvalidReviewer)
    ));
    let returned = UpdateTask::execute(
        &mut port,
        UpdateTaskInput {
            task_id,
            actor_member_id: reviewer,
            action: TaskAction::RequestChanges,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .unwrap();
    assert_eq!(returned.status, TaskStatus::InProgress);

    let closed = UpdateTask::execute(
        &mut port,
        UpdateTaskInput {
            task_id,
            actor_member_id: assignee,
            action: TaskAction::Close {
                reason: CloseReason::Obsolete,
                note: None,
            },
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .unwrap();
    assert_eq!(closed.status, TaskStatus::Closed);
    assert!(closed.finished_at.is_some());
}

#[test]
fn agent_channel_action_and_action_message_commit_together() {
    let agent = member(100);
    let focus = thread(101);
    let run_id = run(102);
    let channel_id = channel(103);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent]);
    port.state
        .runs
        .insert(run_id, running_run(run_id, agent, focus, None, Vec::new()));
    port.state
        .permissions
        .insert((agent, PermissionAction::ChannelCreate));
    port.state.reject_message_insert = true;

    let error = CreateChannelAction::execute(
        &mut port,
        CreateChannelActionInput {
            channel_id,
            space_id: space(1),
            audience: [agent].into_iter().collect(),
            action_message_id: message(104),
            actor_member_id: agent,
            current_run_id: run_id,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .unwrap_err();
    assert_eq!(error, ApplicationError::Conflict);
    assert!(!port.state.channels.contains_key(&channel_id));

    port.state.reject_message_insert = false;
    CreateChannelAction::execute(
        &mut port,
        CreateChannelActionInput {
            channel_id,
            space_id: space(1),
            audience: [agent].into_iter().collect(),
            action_message_id: message(104),
            actor_member_id: agent,
            current_run_id: run_id,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .unwrap();
    assert!(port.state.channels.contains_key(&channel_id));
    assert!(matches!(
        port.state.messages[&message(104)].content,
        MessageContent::ChannelCreated(id) if id == channel_id
    ));
}

fn insert_thread(port: &mut MemoryPort, id: ThreadId, members: &[MemberId]) {
    let root_id = message(id.into_uuid().as_u128());
    let audience = members.iter().copied().collect::<BTreeSet<_>>();
    port.state.threads.insert(
        id,
        Thread {
            id,
            space_id: space(1),
            channel_id: channel(id.into_uuid().as_u128()),
            root_message_id: root_id,
            audience,
        },
    );
    port.state.roots.insert(
        id,
        Message {
            id: root_id,
            thread_id: id,
            author_member_id: members[0],
            placement: MessagePlacement::Root,
            content: MessageContent::Text("来源".into()),
            created_at: OffsetDateTime::UNIX_EPOCH,
        },
    );
}

fn make_task(id: TaskId, source: ThreadId, assignee: MemberId, status: TaskStatus) -> Task {
    Task {
        id,
        space_id: space(1),
        title: "任务".into(),
        status,
        source_thread_id: source,
        creator_member_id: assignee,
        assignee_agent_member_id: (status != TaskStatus::Todo).then_some(assignee),
        result_message_id: None,
        close_reason: None,
        close_reason_note: None,
        related_threads: Vec::new(),
        created_at: OffsetDateTime::UNIX_EPOCH,
        updated_at: OffsetDateTime::UNIX_EPOCH,
        finished_at: None,
    }
}

fn running_run(
    id: RunId,
    agent: MemberId,
    focus: ThreadId,
    task_id: Option<TaskId>,
    items: Vec<InboxItemId>,
) -> Run {
    Run {
        id,
        space_id: space(1),
        agent_id: agent,
        task_id,
        focus_thread_id: focus,
        status: RunStatus::Running,
        fencing_token_hash: "token".into(),
        lease_expires_at: OffsetDateTime::UNIX_EPOCH,
        items: items
            .into_iter()
            .enumerate()
            .map(|(index, item_id)| RunItem {
                inbox_item_id: item_id,
                delivery_sequence: index as u64 + 1,
                disposition: None,
            })
            .collect(),
        outcome: None,
        continuation_note: None,
        started_at: Some(OffsetDateTime::UNIX_EPOCH),
        finished_at: None,
    }
}

fn inbox(
    id: InboxItemId,
    agent: MemberId,
    focus: ThreadId,
    task_id: Option<TaskId>,
    status: InboxItemStatus,
) -> InboxItem {
    InboxItem {
        id,
        space_id: space(1),
        agent_id: agent,
        message_id: None,
        thread_id: focus,
        task_id,
        kind: InboxItemKind::Mention,
        strength: AttentionStrength::Hard,
        status,
        available_at: OffsetDateTime::UNIX_EPOCH,
        lease_run_id: (status == InboxItemStatus::Leased)
            .then_some(run(if id.into_uuid().as_u128() == 3 { 4 } else { 53 })),
        lease_expires_at: (status == InboxItemStatus::Leased).then_some(OffsetDateTime::UNIX_EPOCH),
        retry_count: 0,
        handled_at: None,
    }
}

fn claim_input(
    run_id: RunId,
    agent: MemberId,
    task_id: Option<TaskId>,
    focus: ThreadId,
) -> ClaimRunInput {
    ClaimRunInput {
        run_id,
        space_id: space(1),
        agent_id: agent,
        task_id,
        focus_thread_id: focus,
        item_ids: Vec::new(),
        fencing_token_hash: "token".into(),
        lease_expires_at: OffsetDateTime::UNIX_EPOCH,
    }
}

fn complete_run_input(run_id: RunId, token: &str, item_id: InboxItemId) -> CompleteRunInput {
    CompleteRunInput {
        event_id: event(80),
        run_id,
        fencing_token_hash: token.into(),
        outcome: RunOutcome::Completed,
        item_dispositions: vec![ItemDispositionInput {
            item_id,
            disposition: InboxItemDisposition::Handled,
        }],
        continuation_note: None,
        now: OffsetDateTime::UNIX_EPOCH,
    }
}

macro_rules! id_fn {
    ($name:ident, $type:ty) => {
        fn $name(value: u128) -> $type {
            <$type>::from_uuid(Uuid::from_u128(value))
        }
    };
}

id_fn!(space, SpaceId);
id_fn!(channel, ChannelId);
id_fn!(computer, ComputerId);
id_fn!(event, EventId);
id_fn!(member, MemberId);
id_fn!(message, MessageId);
id_fn!(thread, ThreadId);
id_fn!(task, TaskId);
id_fn!(run, RunId);
id_fn!(item, InboxItemId);
id_fn!(idempotency, IdempotencyKey);
