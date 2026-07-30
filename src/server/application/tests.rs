use std::collections::{BTreeSet, HashMap, HashSet};

use sha2::{Digest, Sha256};
use time::OffsetDateTime;
use uuid::Uuid;

use crate::ids::{
    ChannelId, ComputerId, EventId, IdempotencyKey, InboxItemId, MemberId, MessageId, RunId,
    SpaceId, TaskId, ThreadId,
};
use crate::server::domain::{
    access::{HumanRegistration, SessionLifetime, SpaceAccess},
    attention::{
        AttentionStrength, InboxItem, InboxItemDisposition, InboxItemKind, InboxItemStatus,
    },
    conversation::{Channel, ChannelKind, Message, MessageContent, MessagePlacement, Thread},
    execution::{Run, RunItem, RunOutcome, RunStatus},
    identity::{
        AccessLevel, Agent, AgentLifecycle, Computer, ComputerLifecycle, DriverKind, Member,
        PermissionAction,
    },
    pairing::{Pairing, PairingStatus},
    task::{CloseReason, Task, TaskStatus},
};

use super::{
    attention::{HardItemRoute, RouteHardItem, RouteHardItemInput},
    computer::{
        AuthenticateComputer, BeginPairing, BeginPairingInput, ConfirmPairing, ConfirmPairingInput,
        ReadPairing, ReadPairingStatus,
    },
    conversation::{
        CreateAgent, CreateAgentAction, CreateAgentActionInput, CreateAgentInput, CreateChannel,
        CreateChannelAction, CreateChannelActionInput, CreateChannelInput, DeleteMessage,
        EditMessage, EditMessageInput,
    },
    execution::{
        AcknowledgeDelivery, AcknowledgeDeliveryInput, ClaimRun, ClaimRunInput, CompleteRun,
        CompleteRunInput, ItemDispositionInput, RecordRunItemDisposition,
        RecordRunItemDispositionInput, RenewRun, RenewRunInput, StartRun, StartRunInput,
    },
    identity::{
        AuthenticateHuman, AuthenticateHumanInput, AuthenticateSession, AuthorizeAgentGovernance,
        AuthorizeSpaceAccess, CloseSession, DeleteComputer, RegisterHuman, RegisterHumanInput,
        RetireAgent, SetPermission,
    },
    ports::{
        ApplicationError, AuthenticatedHuman, ComputerRecord, Effect, MessageDraft, PairedComputer,
        PairingCodePort, PasswordPort, PublishedMessage, RawPairingCode, RawSessionToken,
        ServerTransaction, SessionTokenPort, TransactionPort,
    },
    task::{
        CompleteTask, CompleteTaskInput, CreateTaskFromRootMessage, CreateTaskInput,
        FinishAgentTaskAction, FinishAgentTaskInput, FinishAgentTaskRun, LinkThreadInput,
        LinkThreadToTask, TaskAction, TaskPostTarget, TaskSource, UpdateTask, UpdateTaskInput,
    },
};

#[tokio::test]
async fn human_channel_and_agent_creation_use_access_and_computer_transaction_rules() {
    let mut port = MemoryPort::default();
    let space_id = space(900);
    let owner_id = member(901);
    let computer_id = computer(902);
    port.state.members.insert(
        owner_id,
        Member {
            id: owner_id,
            space_id,
            display_name: "Owner".into(),
            handle: "owner".into(),
            access_level: AccessLevel::Owner,
            created_at: OffsetDateTime::now_utc(),
        },
    );
    port.state.computers.insert(
        computer_id,
        Computer {
            id: computer_id,
            space_id,
            lifecycle: ComputerLifecycle::Online,
            token_hash: Some("hash".into()),
            deleted_at: None,
        },
    );

    let channel_id = channel(903);
    CreateChannel::execute(
        &mut port,
        CreateChannelInput {
            channel_id,
            space_id,
            audience: BTreeSet::from([owner_id]),
            kind: ChannelKind::Private,
            slug: Some("private".into()),
            topic: None,
            actor_member_id: owner_id,
            idempotency_key: idempotency(209),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .unwrap();
    let agent_id = member(904);
    CreateAgent::execute(
        &mut port,
        CreateAgentInput {
            agent_member_id: agent_id,
            space_id,
            display_name: "Agent".into(),
            handle: "agent".into(),
            access_level: AccessLevel::Admin,
            role_text: "Review changes".into(),
            computer_id,
            driver_kind: DriverKind::Builtin,
            actor_member_id: owner_id,
            idempotency_key: idempotency(210),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .unwrap();

    assert!(port.state.channels.contains_key(&channel_id));
    assert_eq!(
        port.state.members[&agent_id].access_level,
        AccessLevel::Admin
    );
    assert_eq!(
        port.state.effects,
        vec![
            Effect::ChannelCreated(channel_id),
            Effect::AgentCreated {
                agent_id,
                computer_id,
            },
        ]
    );
}

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
    members: HashMap<MemberId, Member>,
    computers: HashMap<ComputerId, Computer>,
    idempotency: HashMap<(MemberId, String, IdempotencyKey), TaskId>,
    resource_idempotency: HashMap<(MemberId, String, IdempotencyKey), uuid::Uuid>,
    task_audits: Vec<(MemberId, String, TaskId)>,
    completed_run_events: HashMap<EventId, RunId>,
    assignable_agents: HashSet<MemberId>,
    permissions: HashSet<(MemberId, PermissionAction)>,
    computer_assignments: HashSet<(ComputerId, MemberId)>,
    effects: Vec<Effect>,
    reject_message_insert: bool,
    humans: HashMap<String, (AuthenticatedHuman, String)>,
    sessions: HashMap<String, (uuid::Uuid, OffsetDateTime)>,
    space_members: HashMap<(uuid::Uuid, SpaceId), MemberId>,
    channel_members: HashMap<(uuid::Uuid, ChannelId), MemberId>,
    agent_spaces: HashMap<MemberId, SpaceId>,
    computer_spaces: HashMap<ComputerId, SpaceId>,
    pairings: HashMap<uuid::Uuid, (Pairing, String)>,
    paired_computers: Vec<PairedComputer>,
    computer_tokens: HashMap<String, ComputerId>,
    idempotency_locks: Vec<(MemberId, String, IdempotencyKey)>,
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
        let result = operation(&mut transaction).await?;
        self.state = transaction.state;
        Ok(result)
    }
}

#[async_trait::async_trait]
impl ServerTransaction for MemoryTransaction {
    async fn create_space(
        &mut self,
        _actor_user_id: uuid::Uuid,
        _space_id: crate::ids::SpaceId,
        _owner_id: MemberId,
        _general_channel_id: ChannelId,
        _name: &str,
        _slug: &str,
        _owner_handle: &str,
        _owner_display_name: &str,
        _idempotency_key: IdempotencyKey,
        _now: time::OffsetDateTime,
    ) -> Result<super::ports::CreatedSpace, ApplicationError> {
        Err(ApplicationError::Internal)
    }

    async fn insert_human(
        &mut self,
        user_id: uuid::Uuid,
        registration: &HumanRegistration,
        password_hash: &str,
        _now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        if self
            .state
            .humans
            .contains_key(&registration.email_normalized)
        {
            return Err(ApplicationError::Conflict);
        }
        self.state.humans.insert(
            registration.email_normalized.clone(),
            (
                AuthenticatedHuman {
                    user_id,
                    display_name: registration.display_name.clone(),
                    email_normalized: registration.email_normalized.clone(),
                },
                password_hash.to_owned(),
            ),
        );
        Ok(())
    }

    async fn human_credential(
        &mut self,
        email_normalized: &str,
    ) -> Result<Option<(AuthenticatedHuman, String)>, ApplicationError> {
        Ok(self.state.humans.get(email_normalized).cloned())
    }

    async fn insert_browser_session(
        &mut self,
        _session_id: uuid::Uuid,
        user_id: uuid::Uuid,
        token_hash: &str,
        expires_at: OffsetDateTime,
        _now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.state
            .sessions
            .insert(token_hash.to_owned(), (user_id, expires_at));
        Ok(())
    }

    async fn human_for_session(
        &mut self,
        token_hash: &str,
        now: OffsetDateTime,
    ) -> Result<Option<AuthenticatedHuman>, ApplicationError> {
        let Some((user_id, expires_at)) = self.state.sessions.get(token_hash).copied() else {
            return Ok(None);
        };
        if expires_at <= now {
            return Ok(None);
        }
        Ok(self
            .state
            .humans
            .values()
            .find(|(human, _)| human.user_id == user_id)
            .map(|(human, _)| human.clone()))
    }

    async fn delete_browser_session(&mut self, token_hash: &str) -> Result<(), ApplicationError> {
        self.state.sessions.remove(token_hash);
        Ok(())
    }

    async fn space_access(
        &mut self,
        user_id: uuid::Uuid,
        space_id: SpaceId,
    ) -> Result<Option<SpaceAccess>, ApplicationError> {
        let Some(member_id) = self.state.space_members.get(&(user_id, space_id)).copied() else {
            return Ok(None);
        };
        let access_level = self
            .state
            .members
            .get(&member_id)
            .ok_or(ApplicationError::NotFound)?
            .access_level;
        Ok(Some(SpaceAccess {
            member_id,
            space_id,
            access_level,
        }))
    }

    async fn channel_access(
        &mut self,
        user_id: uuid::Uuid,
        channel_id: ChannelId,
    ) -> Result<Option<MemberId>, ApplicationError> {
        Ok(self
            .state
            .channel_members
            .get(&(user_id, channel_id))
            .copied())
    }

    async fn space_of_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        Ok(self.state.agent_spaces.get(&agent_id).copied())
    }

    async fn space_of_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        Ok(self.state.computer_spaces.get(&computer_id).copied())
    }

    async fn insert_pairing(
        &mut self,
        pairing_id: uuid::Uuid,
        pairing: &Pairing,
        code_hash: &str,
        _now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.state
            .pairings
            .insert(pairing_id, (pairing.clone(), code_hash.to_owned()));
        Ok(())
    }

    async fn save_pairing(
        &mut self,
        pairing_id: uuid::Uuid,
        pairing: &Pairing,
        _now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        let code_hash = self
            .state
            .pairings
            .get(&pairing_id)
            .map(|(_, code_hash)| code_hash.clone())
            .ok_or(ApplicationError::NotFound)?;
        self.state
            .pairings
            .insert(pairing_id, (pairing.clone(), code_hash));
        Ok(())
    }

    async fn pairing_by_code(
        &mut self,
        pairing_id: uuid::Uuid,
        code_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError> {
        Ok(self
            .state
            .pairings
            .get(&pairing_id)
            .filter(|(_, stored)| stored == code_hash)
            .map(|(pairing, _)| pairing.clone()))
    }

    async fn pairing_by_code_for_update(
        &mut self,
        pairing_id: uuid::Uuid,
        code_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError> {
        self.pairing_by_code(pairing_id, code_hash).await
    }

    async fn pairing_by_token(
        &mut self,
        pairing_id: uuid::Uuid,
        token_hash: &str,
    ) -> Result<Option<Pairing>, ApplicationError> {
        Ok(self
            .state
            .pairings
            .get(&pairing_id)
            .filter(|(pairing, _)| pairing.request.token_hash == token_hash)
            .map(|(pairing, _)| pairing.clone()))
    }

    async fn insert_computer(&mut self, record: &ComputerRecord) -> Result<(), ApplicationError> {
        if self.state.computer_tokens.contains_key(&record.token_hash) {
            return Err(ApplicationError::Conflict);
        }
        self.state
            .computer_tokens
            .insert(record.token_hash.clone(), record.id);
        self.state.paired_computers.push(PairedComputer {
            id: record.id,
            space_id: record.space_id,
            name: record.name.clone(),
            hostname: record.hostname.clone(),
            os: record.os,
            daemon_version: Some(record.daemon_version.clone()),
            connected: false,
            deleted: false,
            last_seen_at: None,
            created_at: record.created_at,
        });
        Ok(())
    }

    async fn paired_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Option<PairedComputer>, ApplicationError> {
        Ok(self
            .state
            .paired_computers
            .iter()
            .find(|computer| computer.id == computer_id)
            .cloned())
    }

    async fn space_computers(
        &mut self,
        space_id: SpaceId,
    ) -> Result<Vec<PairedComputer>, ApplicationError> {
        Ok(self
            .state
            .paired_computers
            .iter()
            .filter(|computer| computer.space_id == space_id)
            .cloned()
            .collect())
    }

    async fn computer_for_token(
        &mut self,
        computer_id: ComputerId,
        token_hash: &str,
    ) -> Result<Option<bool>, ApplicationError> {
        let Some(stored) = self.state.computer_tokens.get(token_hash).copied() else {
            return Ok(None);
        };
        if stored != computer_id {
            return Ok(None);
        }
        Ok(self
            .state
            .paired_computers
            .iter()
            .find(|computer| computer.id == computer_id)
            .map(|computer| computer.deleted))
    }

    async fn lock_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<(), ApplicationError> {
        self.state
            .idempotency_locks
            .push((actor, action.to_owned(), key));
        Ok(())
    }

    async fn thread(&mut self, id: ThreadId) -> Result<Thread, ApplicationError> {
        self.state
            .threads
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    async fn root_message(&mut self, thread_id: ThreadId) -> Result<Message, ApplicationError> {
        self.state
            .roots
            .get(&thread_id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    async fn message(&mut self, id: MessageId) -> Result<Message, ApplicationError> {
        self.state
            .messages
            .get(&id)
            .or_else(|| self.state.roots.values().find(|message| message.id == id))
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    async fn channel(&mut self, id: ChannelId) -> Result<Channel, ApplicationError> {
        self.state
            .channels
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    async fn task(&mut self, id: TaskId) -> Result<Task, ApplicationError> {
        self.state
            .tasks
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    async fn run(&mut self, id: RunId) -> Result<Run, ApplicationError> {
        self.state
            .runs
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    async fn inbox_item(&mut self, id: InboxItemId) -> Result<InboxItem, ApplicationError> {
        self.state
            .items
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    async fn agent(&mut self, id: MemberId) -> Result<Agent, ApplicationError> {
        self.state
            .agents
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    async fn computer(&mut self, id: ComputerId) -> Result<Computer, ApplicationError> {
        self.state
            .computers
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }

    async fn task_for_source(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<TaskId>, ApplicationError> {
        Ok(self
            .state
            .tasks
            .values()
            .find(|task| task.source_thread_id == thread_id)
            .map(|task| task.id))
    }

    async fn unfinished_task_for_thread(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<TaskId>, ApplicationError> {
        Ok(self
            .state
            .tasks
            .values()
            .find(|task| !task.status.is_finished() && task.linked_to(thread_id))
            .map(|task| task.id))
    }

    async fn task_for_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<Option<TaskId>, ApplicationError> {
        Ok(self
            .state
            .idempotency
            .get(&(actor, action.to_owned(), key))
            .copied())
    }

    async fn resource_for_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<Option<uuid::Uuid>, ApplicationError> {
        Ok(self
            .state
            .resource_idempotency
            .get(&(actor, action.to_owned(), key))
            .copied())
    }

    async fn active_run_for_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<Option<RunId>, ApplicationError> {
        Ok(self
            .state
            .runs
            .values()
            .find(|run| run.agent_id == agent_id && run.status.is_active())
            .map(|run| run.id))
    }

    async fn computer_has_assigned_agents(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<bool, ApplicationError> {
        Ok(self
            .state
            .agents
            .values()
            .any(|agent| agent.computer_id == Some(computer_id)))
    }

    async fn completed_run_for_event(
        &mut self,
        event_id: EventId,
    ) -> Result<Option<RunId>, ApplicationError> {
        Ok(self.state.completed_run_events.get(&event_id).copied())
    }

    async fn can_read_thread(
        &mut self,
        actor: MemberId,
        thread_id: ThreadId,
    ) -> Result<bool, ApplicationError> {
        Ok(self
            .state
            .threads
            .get(&thread_id)
            .is_some_and(|thread| thread.audience.contains(&actor)))
    }

    async fn can_link_thread(
        &mut self,
        actor: MemberId,
        task: &Task,
        target: &Thread,
    ) -> Result<bool, ApplicationError> {
        Ok(self.can_read_thread(actor, task.source_thread_id).await?
            && target.audience.contains(&actor))
    }

    async fn can_assign_agent(
        &mut self,
        agent: MemberId,
        source: &Thread,
    ) -> Result<bool, ApplicationError> {
        Ok(self.state.assignable_agents.contains(&agent) && source.audience.contains(&agent))
    }

    async fn can_govern_task(
        &mut self,
        _actor: MemberId,
        _task: &Task,
    ) -> Result<bool, ApplicationError> {
        Ok(false)
    }

    async fn has_permission(
        &mut self,
        actor: MemberId,
        action: PermissionAction,
    ) -> Result<bool, ApplicationError> {
        Ok(self.state.permissions.contains(&(actor, action)))
    }

    async fn can_manage_permissions(
        &mut self,
        actor: MemberId,
        target: MemberId,
    ) -> Result<bool, ApplicationError> {
        let Some(actor) = self.state.members.get(&actor) else {
            return Ok(false);
        };
        let Some(target) = self.state.members.get(&target) else {
            return Ok(false);
        };
        Ok(actor.space_id == target.space_id && actor.access_level.can_manage_space())
    }

    async fn can_operate_agent(
        &mut self,
        computer_id: ComputerId,
        agent_id: MemberId,
    ) -> Result<bool, ApplicationError> {
        Ok(self
            .state
            .computer_assignments
            .contains(&(computer_id, agent_id)))
    }

    async fn member_access_level(
        &mut self,
        member_id: MemberId,
        space_id: SpaceId,
    ) -> Result<AccessLevel, ApplicationError> {
        self.state
            .members
            .get(&member_id)
            .filter(|member| member.space_id == space_id)
            .map(|member| member.access_level)
            .ok_or(ApplicationError::NotFound)
    }

    async fn computer_accepts_agent(
        &mut self,
        computer_id: ComputerId,
        space_id: SpaceId,
    ) -> Result<bool, ApplicationError> {
        Ok(self
            .state
            .computers
            .get(&computer_id)
            .is_some_and(|computer| {
                computer.space_id == space_id && computer.lifecycle == ComputerLifecycle::Online
            }))
    }

    async fn thread_message_sequence(
        &mut self,
        _thread_id: ThreadId,
    ) -> Result<u64, ApplicationError> {
        Ok(0)
    }

    async fn publish_message(
        &mut self,
        draft: MessageDraft,
    ) -> Result<PublishedMessage, ApplicationError> {
        let thread_id = draft
            .thread_id
            .unwrap_or_else(|| ThreadId::from_uuid(draft.message_id.into_uuid()));
        self.insert_message(Message {
            id: draft.message_id,
            thread_id,
            author_member_id: draft.author_member_id,
            placement: if draft.thread_id.is_some() {
                MessagePlacement::Reply
            } else {
                MessagePlacement::Root
            },
            content: MessageContent::Text(draft.body_markdown),
            created_at: draft.now,
            edited_at: None,
            deleted_at: None,
        })
        .await?;
        Ok(PublishedMessage {
            message_id: draft.message_id,
            hard_item_ids: Vec::new(),
        })
    }

    async fn insert_task(&mut self, task: Task) -> Result<(), ApplicationError> {
        if self.state.tasks.insert(task.id, task).is_some() {
            return Err(ApplicationError::Conflict);
        }
        Ok(())
    }

    async fn save_task(&mut self, task: Task) -> Result<(), ApplicationError> {
        self.state.tasks.insert(task.id, task);
        Ok(())
    }

    async fn save_run(&mut self, run: Run) -> Result<(), ApplicationError> {
        self.state.runs.insert(run.id, run);
        Ok(())
    }

    async fn save_inbox_item(&mut self, item: InboxItem) -> Result<(), ApplicationError> {
        self.state.items.insert(item.id, item);
        Ok(())
    }

    async fn insert_message(&mut self, message: Message) -> Result<(), ApplicationError> {
        if self.state.reject_message_insert {
            return Err(ApplicationError::Conflict);
        }
        self.state.messages.insert(message.id, message);
        Ok(())
    }

    async fn save_message(&mut self, message: Message) -> Result<(), ApplicationError> {
        if let std::collections::hash_map::Entry::Occupied(mut entry) =
            self.state.messages.entry(message.id)
        {
            entry.insert(message);
            return Ok(());
        }
        if let Some(root) = self
            .state
            .roots
            .values_mut()
            .find(|root| root.id == message.id)
        {
            *root = message;
            return Ok(());
        }
        Err(ApplicationError::NotFound)
    }

    async fn grant_permission(
        &mut self,
        target: MemberId,
        action: PermissionAction,
        _granted_by: MemberId,
        _now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.state.permissions.insert((target, action));
        Ok(())
    }

    async fn revoke_permission(
        &mut self,
        target: MemberId,
        action: PermissionAction,
    ) -> Result<(), ApplicationError> {
        self.state.permissions.remove(&(target, action));
        Ok(())
    }

    async fn insert_channel(&mut self, channel: Channel) -> Result<(), ApplicationError> {
        if self.state.channels.insert(channel.id, channel).is_some() {
            return Err(ApplicationError::Conflict);
        }
        Ok(())
    }

    async fn insert_agent(&mut self, member: Member, agent: Agent) -> Result<(), ApplicationError> {
        if self.state.members.contains_key(&member.id)
            || self.state.agents.contains_key(&agent.member_id)
        {
            return Err(ApplicationError::Conflict);
        }
        self.state.members.insert(member.id, member);
        self.state.agents.insert(agent.member_id, agent);
        Ok(())
    }

    async fn save_agent(&mut self, agent: Agent) -> Result<(), ApplicationError> {
        self.state.agents.insert(agent.member_id, agent);
        Ok(())
    }

    async fn save_computer(&mut self, computer: Computer) -> Result<(), ApplicationError> {
        self.state.computers.insert(computer.id, computer);
        Ok(())
    }

    async fn record_completed_run_event(
        &mut self,
        event_id: EventId,
        run_id: RunId,
    ) -> Result<(), ApplicationError> {
        self.state.completed_run_events.insert(event_id, run_id);
        Ok(())
    }

    async fn record_task_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        task_id: TaskId,
    ) -> Result<(), ApplicationError> {
        self.state
            .idempotency
            .insert((actor, action.to_owned(), key), task_id);
        Ok(())
    }

    async fn record_resource_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        resource_id: uuid::Uuid,
    ) -> Result<(), ApplicationError> {
        self.state
            .resource_idempotency
            .insert((actor, action.to_owned(), key), resource_id);
        Ok(())
    }

    async fn record_task_audit(
        &mut self,
        actor: MemberId,
        action: &str,
        task_id: TaskId,
        _now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.state
            .task_audits
            .push((actor, action.to_owned(), task_id));
        Ok(())
    }

    fn emit(&mut self, effect: Effect) {
        self.state.effects.push(effect);
    }
}

#[tokio::test]
async fn agent_task_creation_atomically_binds_run_items_and_retries_idempotently() {
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
    .await
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
    .await
    .unwrap();
    assert_eq!(retried.id, task_id);
    assert_eq!(port.state.tasks.len(), 1);
}

#[tokio::test]
async fn reply_cannot_create_task_and_transaction_leaves_no_effects() {
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
    .await
    .unwrap_err();

    assert!(matches!(
        error,
        ApplicationError::Domain(crate::server::domain::DomainError::SourceIsNotRoot)
    ));
    assert!(port.state.tasks.is_empty());
    assert!(port.state.effects.is_empty());
}

#[tokio::test]
async fn linking_rejects_incompatible_audience_and_another_unfinished_task() {
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
            idempotency_key: idempotency(202),
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
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
            idempotency_key: idempotency(208),
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap_err();
    assert_eq!(occupied_error, ApplicationError::Conflict);
}

#[tokio::test]
async fn claim_rejects_parallel_active_run_and_task_focus_outside_links() {
    let agent = member(30);
    let focus = thread(31);
    let other_focus = thread(32);
    let mut port = MemoryPort::default();
    port.state
        .computer_assignments
        .insert((computer(999), agent));
    port.state.agents.insert(
        agent,
        Agent {
            member_id: agent,
            space_id: space(1),
            computer_id: Some(computer(999)),
            role_text: "run".into(),
            role_revision: 1,
            lifecycle: AgentLifecycle::Active,
            driver_kind: DriverKind::Codex,
            retired_at: None,
        },
    );
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

    let mut unauthorized_input = claim_input(run(35), agent, Some(task(33)), focus);
    unauthorized_input.computer_id = computer(998);
    let unauthorized = ClaimRun::execute(&mut port, unauthorized_input)
        .await
        .unwrap_err();
    assert_eq!(unauthorized, ApplicationError::PermissionDenied);

    let conflict = ClaimRun::execute(
        &mut port,
        claim_input(run(35), agent, Some(task(33)), focus),
    )
    .await
    .unwrap_err();
    assert_eq!(conflict, ApplicationError::Conflict);

    port.state.runs.clear();
    let focus_error = ClaimRun::execute(
        &mut port,
        claim_input(run(36), agent, Some(task(33)), other_focus),
    )
    .await
    .unwrap_err();
    assert!(matches!(
        focus_error,
        ApplicationError::Domain(crate::server::domain::DomainError::FocusOutsideTask)
    ));
}

#[tokio::test]
async fn ambient_item_can_be_leased_by_a_new_run() {
    let agent = member(300);
    let focus = thread(301);
    let item_id = item(302);
    let run_id = run(303);
    let mut port = MemoryPort::default();
    port.state
        .computer_assignments
        .insert((computer(999), agent));
    port.state.agents.insert(
        agent,
        Agent {
            member_id: agent,
            space_id: space(1),
            computer_id: Some(computer(999)),
            role_text: "observe".into(),
            role_revision: 1,
            lifecycle: AgentLifecycle::Active,
            driver_kind: DriverKind::Codex,
            retired_at: None,
        },
    );
    insert_thread(&mut port, focus, &[agent]);
    let mut ambient = inbox(item_id, agent, focus, None, InboxItemStatus::Pending);
    ambient.kind = InboxItemKind::ChannelActivity;
    ambient.strength = AttentionStrength::Ambient;
    port.state.items.insert(item_id, ambient);
    let mut input = claim_input(run_id, agent, None, focus);
    input.item_ids.push(item_id);

    let claimed = ClaimRun::execute(&mut port, input).await.unwrap();

    assert_eq!(claimed.items[0].inbox_item_id, item_id);
    assert_eq!(port.state.items[&item_id].status, InboxItemStatus::Leased);
    assert_eq!(port.state.items[&item_id].lease_run_id, Some(run_id));
}

#[tokio::test]
async fn run_started_requires_assignment_and_fencing_and_is_idempotent() {
    let agent = member(37);
    let focus = thread(38);
    let run_id = run(39);
    let token_hash = hex::encode(Sha256::digest(b"token"));
    let mut queued = running_run(run_id, agent, focus, None, Vec::new());
    queued.status = RunStatus::Queued;
    queued.started_at = None;
    queued.fencing_token_hash = token_hash.clone();
    let mut port = MemoryPort::default();
    port.state.runs.insert(run_id, queued);
    port.state
        .computer_assignments
        .insert((computer(999), agent));

    let input = || StartRunInput {
        run_id,
        computer_id: computer(999),
        fencing_token_hash: token_hash.clone(),
        now: OffsetDateTime::UNIX_EPOCH,
    };
    let started = StartRun::execute(&mut port, input()).await.unwrap();
    assert_eq!(started.status, RunStatus::Running);
    assert_eq!(started.started_at, Some(OffsetDateTime::UNIX_EPOCH));
    assert!(matches!(port.state.effects.last(), Some(Effect::RunStarted(id)) if *id == run_id));
    assert_eq!(
        StartRun::execute(&mut port, input()).await.unwrap(),
        started
    );
    let stale = StartRun::execute(
        &mut port,
        StartRunInput {
            fencing_token_hash: hex::encode(Sha256::digest(b"stale")),
            ..input()
        },
    )
    .await
    .unwrap_err();
    assert!(matches!(
        stale,
        ApplicationError::Domain(crate::server::domain::DomainError::StaleFencingToken)
    ));

    let error = StartRun::execute(
        &mut port,
        StartRunInput {
            computer_id: computer(998),
            ..input()
        },
    )
    .await
    .unwrap_err();
    assert_eq!(error, ApplicationError::PermissionDenied);
}

#[tokio::test]
async fn run_renewal_updates_run_and_item_lease_with_assignment_and_fencing() {
    let agent = member(137);
    let focus = thread(138);
    let run_id = run(139);
    let item_id = item(140);
    let mut port = MemoryPort::default();
    port.state
        .computer_assignments
        .insert((computer(999), agent));
    let mut leased_item = inbox(item_id, agent, focus, None, InboxItemStatus::Leased);
    leased_item.lease_run_id = Some(run_id);
    port.state.items.insert(item_id, leased_item);
    port.state.runs.insert(
        run_id,
        running_run(run_id, agent, focus, None, vec![item_id]),
    );
    let renewed_until = OffsetDateTime::UNIX_EPOCH + time::Duration::hours(1);

    let renewed = RenewRun::execute(
        &mut port,
        RenewRunInput {
            run_id,
            computer_id: computer(999),
            fencing_token_hash: hex::encode(Sha256::digest(b"token")),
            lease_expires_at: renewed_until,
        },
    )
    .await
    .unwrap();

    assert_eq!(renewed.lease_expires_at, renewed_until);
    assert_eq!(
        port.state.items[&item_id].lease_expires_at,
        Some(renewed_until)
    );
    assert!(matches!(
        RenewRun::execute(
            &mut port,
            RenewRunInput {
                run_id,
                computer_id: computer(999),
                fencing_token_hash: "stale".into(),
                lease_expires_at: renewed_until + time::Duration::hours(1),
            },
        )
        .await,
        Err(ApplicationError::Domain(
            crate::server::domain::DomainError::StaleFencingToken
        ))
    ));
}

#[tokio::test]
async fn run_item_disposition_is_recorded_without_releasing_lease_early() {
    let agent = member(141);
    let focus = thread(142);
    let run_id = run(143);
    let item_id = item(144);
    let mut port = MemoryPort::default();
    port.state
        .computer_assignments
        .insert((computer(999), agent));
    let mut leased_item = inbox(item_id, agent, focus, None, InboxItemStatus::Leased);
    leased_item.lease_run_id = Some(run_id);
    port.state.items.insert(item_id, leased_item);
    port.state.runs.insert(
        run_id,
        running_run(run_id, agent, focus, None, vec![item_id]),
    );
    let defer_until = OffsetDateTime::UNIX_EPOCH + time::Duration::hours(1);

    let updated = RecordRunItemDisposition::execute(
        &mut port,
        RecordRunItemDispositionInput {
            run_id,
            computer_id: computer(999),
            fencing_token_hash: hex::encode(Sha256::digest(b"token")),
            item_id,
            disposition: InboxItemDisposition::Deferred,
            defer_until: Some(defer_until),
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap();

    assert_eq!(
        updated.items[0].disposition,
        Some(InboxItemDisposition::Deferred)
    );
    assert_eq!(port.state.items[&item_id].status, InboxItemStatus::Leased);
    assert_eq!(port.state.items[&item_id].available_at, defer_until);
}

#[tokio::test]
async fn hard_item_created_after_finalizing_stays_pending_without_effect() {
    let agent = member(53);
    let focus = thread(54);
    let run_id = run(55);
    let item_id = item(56);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent]);
    let mut current = running_run(run_id, agent, focus, None, Vec::new());
    current.status = RunStatus::Finalizing;
    port.state.runs.insert(run_id, current);
    port.state.items.insert(
        item_id,
        inbox(item_id, agent, focus, None, InboxItemStatus::Pending),
    );

    let route = RouteHardItem::execute(&mut port, RouteHardItemInput { item_id })
        .await
        .unwrap();

    assert_eq!(route, HardItemRoute::Pending);
    assert_eq!(port.state.items[&item_id].status, InboxItemStatus::Pending);
    assert!(port.state.effects.is_empty());
}

#[tokio::test]
async fn hard_item_routes_to_same_focus_once_and_uses_run_lease() {
    let agent = member(44);
    let focus = thread(45);
    let run_id = run(46);
    let item_id = item(47);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent]);
    let current = running_run(run_id, agent, focus, None, Vec::new());
    let lease_expires_at = current.lease_expires_at;
    port.state.runs.insert(run_id, current);
    port.state.items.insert(
        item_id,
        inbox(item_id, agent, focus, None, InboxItemStatus::Pending),
    );

    let route = RouteHardItem::execute(&mut port, RouteHardItemInput { item_id })
        .await
        .unwrap();
    assert_eq!(route, HardItemRoute::Attached { sequence: 1 });
    assert_eq!(port.state.items[&item_id].status, InboxItemStatus::Leased);
    assert_eq!(
        port.state.items[&item_id].lease_expires_at,
        Some(lease_expires_at)
    );
    assert!(matches!(
        port.state.effects.as_slice(),
        [Effect::ItemAttached {
            run_id: effect_run,
            item_id: effect_item,
            sequence: 1
        }] if *effect_run == run_id && *effect_item == item_id
    ));

    let duplicate = RouteHardItem::execute(&mut port, RouteHardItemInput { item_id })
        .await
        .unwrap();
    assert_eq!(duplicate, HardItemRoute::Pending);
    assert_eq!(port.state.effects.len(), 1);
}

#[tokio::test]
async fn different_focus_hard_item_stays_pending_and_emits_notice() {
    let agent = member(48);
    let focus = thread(49);
    let waiting = thread(50);
    let run_id = run(51);
    let item_id = item(52);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent]);
    insert_thread(&mut port, waiting, &[agent]);
    port.state
        .runs
        .insert(run_id, running_run(run_id, agent, focus, None, Vec::new()));
    port.state.items.insert(
        item_id,
        inbox(item_id, agent, waiting, None, InboxItemStatus::Pending),
    );

    let route = RouteHardItem::execute(&mut port, RouteHardItemInput { item_id })
        .await
        .unwrap();

    assert_eq!(route, HardItemRoute::Notice);
    assert_eq!(port.state.items[&item_id].status, InboxItemStatus::Pending);
    assert!(matches!(
        port.state.effects.as_slice(),
        [Effect::RunNotice {
            run_id: effect_run,
            item_id: effect_item,
            location_visible: true
        }] if *effect_run == run_id && *effect_item == item_id
    ));
}

#[tokio::test]
async fn run_completion_checks_fencing_and_does_not_complete_task() {
    let agent = member(50);
    let focus = thread(51);
    let task_id = task(52);
    let run_id = run(53);
    let item_id = item(54);
    let mut port = MemoryPort::default();
    port.state
        .computer_assignments
        .insert((computer(999), agent));
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

    let stale = CompleteRun::execute(&mut port, complete_run_input(run_id, "stale", item_id))
        .await
        .unwrap_err();
    assert!(matches!(
        stale,
        ApplicationError::Domain(crate::server::domain::DomainError::StaleFencingToken)
    ));

    let completed = CompleteRun::execute(&mut port, complete_run_input(run_id, "token", item_id))
        .await
        .unwrap();
    assert_eq!(completed.status, RunStatus::Completed);
    let retried = CompleteRun::execute(&mut port, complete_run_input(run_id, "token", item_id))
        .await
        .unwrap();
    assert_eq!(retried.status, RunStatus::Completed);
    assert_eq!(port.state.items[&item_id].status, InboxItemStatus::Handled);
    assert_eq!(port.state.tasks[&task_id].status, TaskStatus::InProgress);
}

#[tokio::test]
async fn result_message_failure_rolls_back_task_completion() {
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
            idempotency_key: idempotency(203),
            result_message_id: result_id,
            result_thread_id: focus,
            result_markdown: "完成".into(),
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap_err();
    assert_eq!(error, ApplicationError::Conflict);
    assert_eq!(port.state.tasks[&task_id].status, TaskStatus::InProgress);
    assert!(!port.state.messages.contains_key(&result_id));
    assert!(port.state.effects.is_empty());
}

#[tokio::test]
async fn agent_task_done_atomically_finishes_run_items_and_replays() {
    let agent = member(160);
    let focus = thread(161);
    let task_id = task(162);
    let run_id = run(163);
    let handled_item = item(164);
    let deferred_item = item(165);
    let result_id = message(166);
    let key = idempotency(167);
    let computer_id = computer(999);
    let now = OffsetDateTime::UNIX_EPOCH;
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent]);
    port.state.computer_assignments.insert((computer_id, agent));
    port.state.tasks.insert(
        task_id,
        make_task(task_id, focus, agent, TaskStatus::InProgress),
    );
    for item_id in [handled_item, deferred_item] {
        let mut inbox_item = inbox(
            item_id,
            agent,
            focus,
            Some(task_id),
            InboxItemStatus::Leased,
        );
        inbox_item.lease_run_id = Some(run_id);
        port.state.items.insert(item_id, inbox_item);
    }
    let mut current_run = running_run(
        run_id,
        agent,
        focus,
        Some(task_id),
        vec![handled_item, deferred_item],
    );
    current_run.items[1].disposition = Some(InboxItemDisposition::Deferred);
    port.state.runs.insert(run_id, current_run);

    let input = || FinishAgentTaskInput {
        run_id,
        computer_id,
        actor_member_id: agent,
        fencing_token_hash: hex::encode(Sha256::digest(b"token")),
        idempotency_key: key,
        message_snapshot_sequence: 0,
        action: FinishAgentTaskAction::Done {
            message_id: result_id,
            result: "完成".into(),
            post_to: TaskPostTarget::Focus,
        },
        now,
    };
    let completed = FinishAgentTaskRun::execute(&mut port, input())
        .await
        .unwrap();

    assert_eq!(completed.status, TaskStatus::Done);
    assert_eq!(port.state.runs[&run_id].status, RunStatus::Completed);
    assert_eq!(
        port.state.items[&handled_item].status,
        InboxItemStatus::Handled
    );
    assert_eq!(
        port.state.items[&deferred_item].status,
        InboxItemStatus::Deferred
    );
    assert!(port.state.messages.contains_key(&result_id));
    assert_eq!(
        port.state.task_audits,
        vec![(agent, "task.done".into(), task_id)]
    );

    let replayed = FinishAgentTaskRun::execute(&mut port, input())
        .await
        .unwrap();
    assert_eq!(replayed, completed);
    assert_eq!(port.state.messages.len(), 1);
    assert_eq!(port.state.task_audits.len(), 1);
}

#[tokio::test]
async fn computer_delete_requires_explicit_agent_retirement() {
    let computer_id = computer(70);
    let agent_id = member(71);
    let focus = thread(72);
    let run_id = run(73);
    let item_id = item(74);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent_id]);
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
            driver_kind: DriverKind::Codex,
            retired_at: None,
        },
    );
    let mut leased_item = inbox(item_id, agent_id, focus, None, InboxItemStatus::Leased);
    leased_item.lease_run_id = Some(run_id);
    port.state.items.insert(item_id, leased_item);
    port.state.runs.insert(
        run_id,
        running_run(run_id, agent_id, focus, None, vec![item_id]),
    );

    let blocked = DeleteComputer::execute(
        &mut port,
        agent_id,
        computer_id,
        idempotency(211),
        OffsetDateTime::UNIX_EPOCH,
    )
    .await
    .unwrap_err();
    assert!(matches!(
        blocked,
        ApplicationError::Domain(crate::server::domain::DomainError::ComputerHasAgents)
    ));
    assert_eq!(
        port.state.computers[&computer_id].lifecycle,
        ComputerLifecycle::Offline
    );

    RetireAgent::execute(
        &mut port,
        agent_id,
        agent_id,
        idempotency(212),
        OffsetDateTime::UNIX_EPOCH,
    )
    .await
    .unwrap();
    assert_eq!(port.state.runs[&run_id].status, RunStatus::Canceled);
    assert_eq!(port.state.items[&item_id].status, InboxItemStatus::Pending);
    let deleted = DeleteComputer::execute(
        &mut port,
        agent_id,
        computer_id,
        idempotency(213),
        OffsetDateTime::UNIX_EPOCH,
    )
    .await
    .unwrap();
    assert_eq!(deleted.lifecycle, ComputerLifecycle::Deleted);
    assert!(deleted.token_hash.is_none());
}

#[tokio::test]
async fn task_review_requires_another_visible_member_and_closed_is_terminal() {
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
            idempotency_key: idempotency(204),
            action: TaskAction::SubmitReview,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap();
    let self_review = UpdateTask::execute(
        &mut port,
        UpdateTaskInput {
            task_id,
            actor_member_id: assignee,
            idempotency_key: idempotency(205),
            action: TaskAction::RequestChanges,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
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
            idempotency_key: idempotency(206),
            action: TaskAction::RequestChanges,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap();
    assert_eq!(returned.status, TaskStatus::InProgress);

    let closed = UpdateTask::execute(
        &mut port,
        UpdateTaskInput {
            task_id,
            actor_member_id: assignee,
            idempotency_key: idempotency(207),
            action: TaskAction::Close {
                reason: CloseReason::Obsolete,
                note: None,
            },
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap();
    assert_eq!(closed.status, TaskStatus::Closed);
    assert!(closed.finished_at.is_some());
}

#[tokio::test]
async fn agent_channel_action_and_action_message_commit_together() {
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
            audience: [agent].into_iter().collect(),
            kind: ChannelKind::Private,
            slug: Some("new-channel".into()),
            topic: None,
            action_message_id: message(104),
            actor_member_id: agent,
            idempotency_key: idempotency(214),
            current_run_id: run_id,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap_err();
    assert_eq!(error, ApplicationError::Conflict);
    assert!(!port.state.channels.contains_key(&channel_id));

    port.state.reject_message_insert = false;
    CreateChannelAction::execute(
        &mut port,
        CreateChannelActionInput {
            channel_id,
            audience: [agent].into_iter().collect(),
            kind: ChannelKind::Private,
            slug: Some("new-channel".into()),
            topic: None,
            action_message_id: message(104),
            actor_member_id: agent,
            idempotency_key: idempotency(214),
            current_run_id: run_id,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap();
    assert!(port.state.channels.contains_key(&channel_id));
    assert!(matches!(
        port.state.messages[&message(104)].content,
        MessageContent::ChannelCreated(id) if id == channel_id
    ));
}

#[tokio::test]
async fn agent_creation_and_action_message_share_permission_and_transaction() {
    let actor = member(110);
    let created_agent = member(111);
    let focus = thread(112);
    let run_id = run(113);
    let computer_id = computer(114);
    let action_message_id = message(115);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[actor]);
    port.state
        .runs
        .insert(run_id, running_run(run_id, actor, focus, None, Vec::new()));

    let input = || CreateAgentActionInput {
        agent_member_id: created_agent,
        display_name: "Implementer".into(),
        handle: "implementer".into(),
        role_text: "Implement code".into(),
        computer_id,
        driver_kind: DriverKind::Codex,
        action_message_id,
        actor_member_id: actor,
        idempotency_key: idempotency(215),
        current_run_id: run_id,
        now: OffsetDateTime::UNIX_EPOCH,
    };
    let denied = CreateAgentAction::execute(&mut port, input())
        .await
        .unwrap_err();
    assert_eq!(denied, ApplicationError::PermissionDenied);
    assert!(!port.state.agents.contains_key(&created_agent));

    port.state
        .permissions
        .insert((actor, PermissionAction::AgentCreate));
    port.state.reject_message_insert = true;
    let rolled_back = CreateAgentAction::execute(&mut port, input())
        .await
        .unwrap_err();
    assert_eq!(rolled_back, ApplicationError::Conflict);
    assert!(!port.state.agents.contains_key(&created_agent));

    port.state.reject_message_insert = false;
    CreateAgentAction::execute(&mut port, input())
        .await
        .unwrap();
    assert!(port.state.agents.contains_key(&created_agent));
    assert!(matches!(
        port.state.messages[&action_message_id].content,
        MessageContent::AgentCreated(id) if id == created_agent
    ));
}

#[tokio::test]
async fn message_edit_and_delete_are_authorized_idempotent_text_mutations() {
    let mut port = MemoryPort::default();
    let author = member(1600);
    let thread_id = thread(1601);
    port.state.members.insert(
        author,
        Member {
            id: author,
            space_id: space(1),
            display_name: "Author".into(),
            handle: "author".into(),
            access_level: AccessLevel::Member,
            created_at: OffsetDateTime::UNIX_EPOCH,
        },
    );
    insert_thread(&mut port, thread_id, &[author]);
    let message_id = message(thread_id.into_uuid().as_u128());
    let edit = EditMessageInput {
        message_id,
        actor_member_id: author,
        body_markdown: "edited".into(),
        idempotency_key: idempotency(1602),
        now: OffsetDateTime::UNIX_EPOCH,
    };
    let edited = EditMessage::execute(&mut port, edit).await.unwrap();
    assert_eq!(edited.content, MessageContent::Text("edited".into()));

    DeleteMessage::execute(
        &mut port,
        message_id,
        author,
        idempotency(1603),
        OffsetDateTime::UNIX_EPOCH,
    )
    .await
    .unwrap();
    let replayed = DeleteMessage::execute(
        &mut port,
        message_id,
        author,
        idempotency(1603),
        OffsetDateTime::UNIX_EPOCH,
    )
    .await
    .unwrap();
    assert_eq!(replayed.deleted_at, Some(OffsetDateTime::UNIX_EPOCH));
    assert_eq!(
        port.state.effects,
        vec![
            Effect::MessageUpdated(message_id),
            Effect::MessageDeleted(message_id)
        ]
    );
}

#[tokio::test]
async fn permission_changes_require_a_space_governor_and_are_idempotent() {
    let mut port = MemoryPort::default();
    let owner = member(1610);
    let target = member(1611);
    for (id, access_level) in [(owner, AccessLevel::Owner), (target, AccessLevel::Member)] {
        port.state.members.insert(
            id,
            Member {
                id,
                space_id: space(1),
                display_name: id.to_string(),
                handle: id.to_string(),
                access_level,
                created_at: OffsetDateTime::UNIX_EPOCH,
            },
        );
    }
    SetPermission::execute(
        &mut port,
        owner,
        target,
        PermissionAction::ChannelCreate,
        true,
        idempotency(1612),
        OffsetDateTime::UNIX_EPOCH,
    )
    .await
    .unwrap();
    SetPermission::execute(
        &mut port,
        owner,
        target,
        PermissionAction::ChannelCreate,
        true,
        idempotency(1612),
        OffsetDateTime::UNIX_EPOCH,
    )
    .await
    .unwrap();
    assert!(
        port.state
            .permissions
            .contains(&(target, PermissionAction::ChannelCreate))
    );
    assert_eq!(port.state.effects, vec![Effect::PermissionChanged(target)]);
}

#[tokio::test]
async fn rejected_delivery_releases_the_item_once() {
    let mut port = MemoryPort::default();
    let run_id = run(53);
    let item_id = item(1620);
    let agent_id = member(1621);
    let focus = thread(1622);
    port.state.runs.insert(
        run_id,
        running_run(run_id, agent_id, focus, None, vec![item_id]),
    );
    port.state.items.insert(
        item_id,
        inbox(item_id, agent_id, focus, None, InboxItemStatus::Leased),
    );
    port.state
        .computer_assignments
        .insert((computer(999), agent_id));
    let input = || AcknowledgeDeliveryInput {
        run_id,
        computer_id: computer(999),
        fencing_token_hash: hex::encode(Sha256::digest(b"token")),
        delivery_sequence: 1,
        accepted: false,
        now: OffsetDateTime::UNIX_EPOCH,
    };
    AcknowledgeDelivery::execute(&mut port, input())
        .await
        .unwrap();
    AcknowledgeDelivery::execute(&mut port, input())
        .await
        .unwrap();
    assert_eq!(port.state.items[&item_id].status, InboxItemStatus::Pending);
    assert_eq!(
        port.state.runs[&run_id].items[0].disposition,
        Some(InboxItemDisposition::Released)
    );
    CompleteRun::execute(
        &mut port,
        CompleteRunInput {
            event_id: event(1623),
            run_id,
            computer_id: computer(999),
            fencing_token_hash: hex::encode(Sha256::digest(b"token")),
            outcome: RunOutcome::Completed,
            item_dispositions: vec![ItemDispositionInput {
                item_id,
                disposition: InboxItemDisposition::Released,
            }],
            continuation_note: None,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap();
    assert_eq!(port.state.runs[&run_id].status, RunStatus::Completed);
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
            edited_at: None,
            deleted_at: None,
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
        fencing_token_hash: hex::encode(Sha256::digest(b"token")),
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
        computer_id: computer(999),
        agent_id: agent,
        task_id,
        focus_thread_id: focus,
        item_ids: Vec::new(),
        fencing_token: super::ports::RawFencingToken::new("token".into()),
        lease_expires_at: OffsetDateTime::UNIX_EPOCH,
    }
}

fn complete_run_input(run_id: RunId, token: &str, item_id: InboxItemId) -> CompleteRunInput {
    CompleteRunInput {
        event_id: event(80),
        run_id,
        computer_id: computer(999),
        fencing_token_hash: hex::encode(Sha256::digest(token.as_bytes())),
        outcome: RunOutcome::Completed,
        item_dispositions: vec![ItemDispositionInput {
            item_id,
            disposition: InboxItemDisposition::Handled,
        }],
        continuation_note: None,
        now: OffsetDateTime::UNIX_EPOCH,
    }
}

/// 测试用密码端口：散列是可预测的前缀拼接，校验只比较该形式。
struct StubPasswords;

impl PasswordPort for StubPasswords {
    fn hash(&self, password: &str) -> Result<String, ApplicationError> {
        Ok(format!("hashed:{password}"))
    }

    fn verify(&self, password: &str, stored_hash: &str) -> bool {
        stored_hash == format!("hashed:{password}")
    }
}

/// 测试用 token 端口：每次调用返回递增 token，便于断言 Session 隔离。
struct StubTokens {
    next: std::cell::Cell<u32>,
}

impl Default for StubTokens {
    fn default() -> Self {
        Self {
            next: std::cell::Cell::new(1),
        }
    }
}

impl SessionTokenPort for StubTokens {
    fn generate(&self) -> RawSessionToken {
        let value = self.next.get();
        self.next.set(value + 1);
        RawSessionToken::new(format!("token-{value}"))
    }
}

fn hour_lifetime() -> SessionLifetime {
    SessionLifetime::from_hours(1).expect("one hour is a valid lifetime")
}

#[tokio::test]
async fn registration_establishes_a_session_and_rejects_a_duplicate_email() {
    let mut port = MemoryPort::default();
    let passwords = StubPasswords;
    let tokens = StubTokens::default();
    let now = OffsetDateTime::UNIX_EPOCH;
    let user_id = Uuid::from_u128(4001);
    let session = RegisterHuman::execute(
        &mut port,
        &passwords,
        &tokens,
        RegisterHumanInput {
            user_id,
            session_id: Uuid::from_u128(4002),
            display_name: " Casey ",
            email: " Casey@Example.COM ",
            password: "correct horse battery",
            lifetime: hour_lifetime(),
            now,
        },
    )
    .await
    .expect("registration succeeds");
    assert_eq!(session.human.user_id, user_id);
    assert_eq!(session.human.email_normalized, "casey@example.com");
    assert_eq!(session.human.display_name, "Casey");

    // Session 立即可用，且解析回同一个账号。
    let authenticated = AuthenticateSession::execute(&mut port, &session.token, now)
        .await
        .expect("session resolves");
    assert_eq!(authenticated, session.human);

    // 同一个规范化 email 不能注册两次。
    let duplicate = RegisterHuman::execute(
        &mut port,
        &passwords,
        &tokens,
        RegisterHumanInput {
            user_id: Uuid::from_u128(4003),
            session_id: Uuid::from_u128(4004),
            display_name: "Casey Again",
            email: "casey@example.com",
            password: "another long password",
            lifetime: hour_lifetime(),
            now,
        },
    )
    .await;
    assert_eq!(duplicate.err(), Some(ApplicationError::Conflict));
}

#[tokio::test]
async fn registration_rejects_a_short_password_without_writing_the_account() {
    let mut port = MemoryPort::default();
    let result = RegisterHuman::execute(
        &mut port,
        &StubPasswords,
        &StubTokens::default(),
        RegisterHumanInput {
            user_id: Uuid::from_u128(4010),
            session_id: Uuid::from_u128(4011),
            display_name: "Casey",
            email: "casey@example.com",
            password: "short",
            lifetime: hour_lifetime(),
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await;
    assert_eq!(
        result.err(),
        Some(ApplicationError::Domain(
            crate::server::domain::DomainError::InvalidCredential
        ))
    );
    assert!(port.state.humans.is_empty());
    assert!(port.state.sessions.is_empty());
}

#[tokio::test]
async fn authentication_hides_whether_the_account_exists_and_closing_a_session_is_repeatable() {
    let mut port = MemoryPort::default();
    let passwords = StubPasswords;
    let tokens = StubTokens::default();
    let now = OffsetDateTime::UNIX_EPOCH;
    let registered = RegisterHuman::execute(
        &mut port,
        &passwords,
        &tokens,
        RegisterHumanInput {
            user_id: Uuid::from_u128(4020),
            session_id: Uuid::from_u128(4021),
            display_name: "Casey",
            email: "casey@example.com",
            password: "correct horse battery",
            lifetime: hour_lifetime(),
            now,
        },
    )
    .await
    .expect("registration succeeds");

    // 未知账号和错误密码返回同一个错误码。
    for (email, password) in [
        ("missing@example.com", "correct horse battery"),
        ("casey@example.com", "wrong password entirely"),
    ] {
        let result = AuthenticateHuman::execute(
            &mut port,
            &passwords,
            &tokens,
            AuthenticateHumanInput {
                session_id: Uuid::from_u128(4022),
                email,
                password,
                lifetime: hour_lifetime(),
                now,
            },
        )
        .await;
        assert_eq!(result.err(), Some(ApplicationError::Unauthenticated));
    }

    let logged_in = AuthenticateHuman::execute(
        &mut port,
        &passwords,
        &tokens,
        AuthenticateHumanInput {
            session_id: Uuid::from_u128(4023),
            email: " CASEY@example.com ",
            password: "correct horse battery",
            lifetime: hour_lifetime(),
            now,
        },
    )
    .await
    .expect("login succeeds");
    assert_ne!(logged_in.token.expose(), registered.token.expose());

    // 注销只作用于本 Session，且可以重复执行。
    CloseSession::execute(&mut port, &logged_in.token)
        .await
        .expect("close succeeds");
    CloseSession::execute(&mut port, &logged_in.token)
        .await
        .expect("closing an absent session still succeeds");
    assert_eq!(
        AuthenticateSession::execute(&mut port, &logged_in.token, now)
            .await
            .err(),
        Some(ApplicationError::Unauthenticated)
    );
    AuthenticateSession::execute(&mut port, &registered.token, now)
        .await
        .expect("the other session remains valid");
}

#[tokio::test]
async fn an_expired_session_no_longer_authenticates() {
    let mut port = MemoryPort::default();
    let now = OffsetDateTime::UNIX_EPOCH;
    let session = RegisterHuman::execute(
        &mut port,
        &StubPasswords,
        &StubTokens::default(),
        RegisterHumanInput {
            user_id: Uuid::from_u128(4030),
            session_id: Uuid::from_u128(4031),
            display_name: "Casey",
            email: "casey@example.com",
            password: "correct horse battery",
            lifetime: hour_lifetime(),
            now,
        },
    )
    .await
    .expect("registration succeeds");
    let after_expiry = now + time::Duration::hours(1);
    assert_eq!(
        AuthenticateSession::execute(&mut port, &session.token, after_expiry)
            .await
            .err(),
        Some(ApplicationError::Unauthenticated)
    );
}

#[tokio::test]
async fn space_authorization_separates_non_members_from_members_and_governors() {
    let mut port = MemoryPort::default();
    let now = OffsetDateTime::UNIX_EPOCH;
    let space_id = space(4100);
    let admin_id = member(4101);
    let plain_id = member(4102);
    let agent_id = member(4103);
    for (id, level) in [
        (admin_id, AccessLevel::Admin),
        (plain_id, AccessLevel::Member),
    ] {
        port.state.members.insert(
            id,
            Member {
                id,
                space_id,
                display_name: "Member".into(),
                handle: "member".into(),
                access_level: level,
                created_at: now,
            },
        );
    }
    port.state.agent_spaces.insert(agent_id, space_id);

    let tokens = StubTokens::default();
    let mut sessions = Vec::new();
    for (index, member_id) in [admin_id, plain_id].into_iter().enumerate() {
        let session = RegisterHuman::execute(
            &mut port,
            &StubPasswords,
            &tokens,
            RegisterHumanInput {
                user_id: Uuid::from_u128(4110 + index as u128),
                session_id: Uuid::from_u128(4120 + index as u128),
                display_name: "Human",
                email: &format!("human{index}@example.com"),
                password: "correct horse battery",
                lifetime: hour_lifetime(),
                now,
            },
        )
        .await
        .expect("registration succeeds");
        port.state
            .space_members
            .insert((session.human.user_id, space_id), member_id);
        sessions.push(session.token);
    }
    let (admin_token, plain_token) = (sessions[0].clone(), sessions[1].clone());

    let access = AuthorizeSpaceAccess::execute(&mut port, &plain_token, space_id, now)
        .await
        .expect("member resolves");
    assert_eq!(access.member_id, plain_id);

    // 治理动作要求 Owner/Admin，普通 Member 得到领域级拒绝。
    assert_eq!(
        AuthorizeAgentGovernance::execute(&mut port, &plain_token, agent_id, now)
            .await
            .err(),
        Some(ApplicationError::Domain(
            crate::server::domain::DomainError::GovernorRequired
        ))
    );
    let governed = AuthorizeAgentGovernance::execute(&mut port, &admin_token, agent_id, now)
        .await
        .expect("admin governs the agent");
    assert_eq!(governed.member_id, admin_id);

    // 非成员访问与 Space 不存在返回同一个错误码。
    let outsider = RegisterHuman::execute(
        &mut port,
        &StubPasswords,
        &tokens,
        RegisterHumanInput {
            user_id: Uuid::from_u128(4130),
            session_id: Uuid::from_u128(4131),
            display_name: "Outsider",
            email: "outsider@example.com",
            password: "correct horse battery",
            lifetime: hour_lifetime(),
            now,
        },
    )
    .await
    .expect("registration succeeds");
    assert_eq!(
        AuthorizeSpaceAccess::execute(&mut port, &outsider.token, space_id, now)
            .await
            .err(),
        Some(ApplicationError::NotFound)
    );
    assert_eq!(
        AuthorizeSpaceAccess::execute(&mut port, &admin_token, space(4199), now)
            .await
            .err(),
        Some(ApplicationError::NotFound)
    );
    assert_eq!(
        AuthorizeAgentGovernance::execute(&mut port, &admin_token, member(4198), now)
            .await
            .err(),
        Some(ApplicationError::NotFound)
    );
}

/// 测试用配对 code 端口：返回固定 code，便于构造错误 code 的对照请求。
struct StubPairingCodes;

impl PairingCodePort for StubPairingCodes {
    fn generate(&self) -> RawPairingCode {
        RawPairingCode::new("424242".into())
    }
}

const DAEMON_TOKEN_HASH: &str = "1a2b3c4d5e6f70819203a4b5c6d7e8f9\
0a1b2c3d4e5f60718293a4b5c6d7e8f9";

async fn begin_test_pairing(
    port: &mut MemoryPort,
    pairing_id: Uuid,
    now: OffsetDateTime,
) -> super::computer::StartedPairing {
    BeginPairing::execute(
        port,
        &StubPairingCodes,
        BeginPairingInput {
            pairing_id,
            token_hash: DAEMON_TOKEN_HASH,
            hostname: "workstation",
            os: "linux",
            daemon_version: "0.1.0",
            now,
        },
    )
    .await
    .expect("pairing opens")
}

#[tokio::test]
async fn confirming_a_pairing_creates_one_computer_and_replays_the_same_result() {
    let mut port = MemoryPort::default();
    let now = OffsetDateTime::UNIX_EPOCH;
    let pairing_id = Uuid::from_u128(5001);
    let started = begin_test_pairing(&mut port, pairing_id, now).await;
    let code_hash = RawPairingCode::new(started.code.clone()).sha256_hash();

    let actor_id = member(5002);
    let space_id = space(5003);
    let key = idempotency(5004);
    let confirmed = ConfirmPairing::execute(
        &mut port,
        ConfirmPairingInput {
            actor_id,
            pairing_id,
            computer_id: computer(5005),
            space_id,
            code_hash: &code_hash,
            name: "  Workstation  ",
            idempotency_key: key,
            now,
        },
    )
    .await
    .expect("confirm succeeds");
    assert_eq!(confirmed.id, computer(5005));
    assert_eq!(confirmed.name, "Workstation");
    assert_eq!(confirmed.hostname, "workstation");
    assert!(!confirmed.connected);

    // 同一 key 重放返回既有 Computer，不创建第二个。
    let replayed = ConfirmPairing::execute(
        &mut port,
        ConfirmPairingInput {
            actor_id,
            pairing_id,
            computer_id: computer(5099),
            space_id,
            code_hash: &code_hash,
            name: "Workstation",
            idempotency_key: key,
            now,
        },
    )
    .await
    .expect("replay succeeds");
    assert_eq!(replayed, confirmed);
    assert_eq!(port.state.paired_computers.len(), 1);
    // 重放路径取过 idempotency 锁，说明并发确认在同一键上串行。
    assert!(
        port.state
            .idempotency_locks
            .iter()
            .filter(|(locked_actor, action, locked_key)| {
                *locked_actor == actor_id
                    && action == "computer.pairing.confirm"
                    && *locked_key == key
            })
            .count()
            >= 2
    );
    assert_eq!(
        port.state.pairings[&pairing_id].0.status,
        PairingStatus::Confirmed
    );
}

#[tokio::test]
async fn a_wrong_code_and_a_second_confirmation_cannot_create_another_computer() {
    let mut port = MemoryPort::default();
    let now = OffsetDateTime::UNIX_EPOCH;
    let pairing_id = Uuid::from_u128(5010);
    let started = begin_test_pairing(&mut port, pairing_id, now).await;
    let code_hash = RawPairingCode::new(started.code).sha256_hash();
    let wrong_hash = RawPairingCode::new("000000".into()).sha256_hash();
    let space_id = space(5011);

    assert_eq!(
        ConfirmPairing::execute(
            &mut port,
            ConfirmPairingInput {
                actor_id: member(5012),
                pairing_id,
                computer_id: computer(5013),
                space_id,
                code_hash: &wrong_hash,
                name: "Workstation",
                idempotency_key: idempotency(5014),
                now,
            },
        )
        .await
        .err(),
        Some(ApplicationError::NotFound)
    );
    assert!(port.state.paired_computers.is_empty());

    ConfirmPairing::execute(
        &mut port,
        ConfirmPairingInput {
            actor_id: member(5012),
            pairing_id,
            computer_id: computer(5015),
            space_id,
            code_hash: &code_hash,
            name: "Workstation",
            idempotency_key: idempotency(5016),
            now,
        },
    )
    .await
    .expect("first confirm succeeds");

    // 另一个 idempotency key 也不能重复确认同一个配对。
    assert_eq!(
        ConfirmPairing::execute(
            &mut port,
            ConfirmPairingInput {
                actor_id: member(5012),
                pairing_id,
                computer_id: computer(5017),
                space_id,
                code_hash: &code_hash,
                name: "Workstation",
                idempotency_key: idempotency(5018),
                now,
            },
        )
        .await
        .err(),
        Some(ApplicationError::Domain(
            crate::server::domain::DomainError::PairingLapsed
        ))
    );
    assert_eq!(port.state.paired_computers.len(), 1);
}

#[tokio::test]
async fn a_lapsed_pairing_is_recorded_as_expired_on_read_and_rejects_confirmation() {
    let mut port = MemoryPort::default();
    let now = OffsetDateTime::UNIX_EPOCH;
    let pairing_id = Uuid::from_u128(5020);
    let started = begin_test_pairing(&mut port, pairing_id, now).await;
    let code_hash = RawPairingCode::new(started.code).sha256_hash();
    let after = started.expires_at;

    let view = ReadPairing::execute(&mut port, pairing_id, &code_hash, after)
        .await
        .expect("read succeeds");
    assert_eq!(view.status, PairingStatus::Expired);
    // 过期在读取时落库，daemon 轮询看到同一状态。
    assert_eq!(
        port.state.pairings[&pairing_id].0.status,
        PairingStatus::Expired
    );
    let progress = ReadPairingStatus::execute(&mut port, pairing_id, DAEMON_TOKEN_HASH, after)
        .await
        .expect("status succeeds");
    assert_eq!(progress.status, PairingStatus::Expired);
    assert_eq!(progress.computer_id, None);

    assert_eq!(
        ConfirmPairing::execute(
            &mut port,
            ConfirmPairingInput {
                actor_id: member(5021),
                pairing_id,
                computer_id: computer(5022),
                space_id: space(5023),
                code_hash: &code_hash,
                name: "Workstation",
                idempotency_key: idempotency(5024),
                now: after,
            },
        )
        .await
        .err(),
        Some(ApplicationError::Domain(
            crate::server::domain::DomainError::PairingLapsed
        ))
    );
    assert!(port.state.paired_computers.is_empty());
    assert_eq!(
        port.state.pairings[&pairing_id].0.status,
        PairingStatus::Expired
    );
}

#[tokio::test]
async fn pairing_details_never_expose_the_full_token_hash_and_status_requires_the_daemon_token() {
    let mut port = MemoryPort::default();
    let now = OffsetDateTime::UNIX_EPOCH;
    let pairing_id = Uuid::from_u128(5030);
    let started = begin_test_pairing(&mut port, pairing_id, now).await;
    let code_hash = RawPairingCode::new(started.code).sha256_hash();

    let view = ReadPairing::execute(&mut port, pairing_id, &code_hash, now)
        .await
        .expect("read succeeds");
    assert_eq!(view.token_fingerprint.len(), 12);
    assert!(DAEMON_TOKEN_HASH.starts_with(&view.token_fingerprint));
    assert_ne!(view.token_fingerprint, DAEMON_TOKEN_HASH);

    // 未知 daemon token 得到认证失败，不泄露配对是否存在。
    assert_eq!(
        ReadPairingStatus::execute(&mut port, pairing_id, &"b".repeat(64), now)
            .await
            .err(),
        Some(ApplicationError::Unauthenticated)
    );
    assert_eq!(
        ReadPairingStatus::execute(&mut port, Uuid::from_u128(5039), DAEMON_TOKEN_HASH, now)
            .await
            .err(),
        Some(ApplicationError::Unauthenticated)
    );
}

#[tokio::test]
async fn a_deleted_computer_authenticates_for_handshake_but_not_for_the_computer_api() {
    let mut port = MemoryPort::default();
    let now = OffsetDateTime::UNIX_EPOCH;
    let computer_id = computer(5040);
    let record = ComputerRecord {
        id: computer_id,
        space_id: space(5041),
        name: "Workstation".into(),
        hostname: "workstation".into(),
        os: crate::server::domain::pairing::ComputerOs::Linux,
        daemon_version: "0.1.0".into(),
        token_hash: DAEMON_TOKEN_HASH.into(),
        created_at: now,
    };
    port.transact(async |transaction| transaction.insert_computer(&record).await)
        .await
        .expect("insert succeeds");

    let identity = AuthenticateComputer::execute(&mut port, computer_id, DAEMON_TOKEN_HASH)
        .await
        .expect("token authenticates");
    assert!(!identity.deleted);
    AuthenticateComputer::require_active(&mut port, computer_id, DAEMON_TOKEN_HASH)
        .await
        .expect("an active computer may call the Computer API");

    // 错误 token 与错误 Computer 都不能认证。
    assert_eq!(
        AuthenticateComputer::execute(&mut port, computer_id, &"c".repeat(64))
            .await
            .err(),
        Some(ApplicationError::Unauthenticated)
    );
    assert_eq!(
        AuthenticateComputer::execute(&mut port, computer(5049), DAEMON_TOKEN_HASH)
            .await
            .err(),
        Some(ApplicationError::Unauthenticated)
    );

    // 删除后握手仍能认证，但 Computer API 被拒绝。
    port.state.paired_computers[0].deleted = true;
    assert!(
        AuthenticateComputer::execute(&mut port, computer_id, DAEMON_TOKEN_HASH)
            .await
            .expect("handshake still authenticates")
            .deleted
    );
    assert_eq!(
        AuthenticateComputer::require_active(&mut port, computer_id, DAEMON_TOKEN_HASH)
            .await
            .err(),
        Some(ApplicationError::Unauthenticated)
    );
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
