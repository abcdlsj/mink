use std::collections::{BTreeSet, HashMap, HashSet};

use time::OffsetDateTime;
use uuid::Uuid;

use crate::ids::{
    AttachmentId, ChannelId, CommandId, ComputerId, EventId, IdempotencyKey, InboxItemId, MemberId,
    MessageId, RunId, SpaceId, TaskId, ThreadId,
};
use crate::server::domain::{
    DomainError,
    access::{HumanRegistration, SessionLifetime, SpaceAccess},
    attachment::{Attachment, AttachmentStatus, ContentDigest, DeclaredContent},
    attention::{
        InboxItem, InboxItemDisposition, InboxItemKind, InboxItemSnapshot, InboxItemStatus,
    },
    conversation::{Channel, ChannelKind, Message, MessageContent, MessagePlacement, Thread},
    execution::{
        Run, RunErrorCode, RunItemSnapshot, RunOutcome, RunSnapshot, RunStatus, RunTrigger,
    },
    identity::{
        AccessLevel, Agent, AgentLifecycle, Computer, ComputerLifecycle, DriverKind, Member,
        PermissionAction,
    },
    invitation::{Invitation, InvitationStatus},
    pairing::{ComputerOs, Pairing, PairingStatus},
    task::{CloseReason, Task, TaskSnapshot, TaskStatus},
};

use super::{
    attachment::{
        CompleteUpload as CompleteAttachmentUpload,
        CompleteUploadInput as CompleteAttachmentUploadInput, OpenUpload, OpenUploadInput,
        ReadAttachment, WriteUploadContent, WriteUploadContentInput,
    },
    attention::{
        HardItemRoute, ReadMemberInbox, RequeueDeadItem, RequeueDeadItemInput, RouteHardItem,
        RouteHardItemInput,
    },
    computer::{
        AuthenticateComputer, BeginPairing, BeginPairingInput, ConfirmPairing, ConfirmPairingInput,
        ReadPairing, ReadPairingStatus,
    },
    conversation::{
        AddChannelAgents, CreateAgent, CreateAgentAction, CreateAgentActionInput, CreateAgentInput,
        CreateChannel, CreateChannelAction, CreateChannelActionInput, CreateChannelInput,
        DeleteMessage, EditMessage, EditMessageInput, InviteChannelMember, LeaveChannel,
        OpenDirectMessage, OpenDirectMessageInput, PublishMessage, RemoveChannelMember,
    },
    execution::{
        AcknowledgeDelivery, AcknowledgeDeliveryInput, ApplyCommandResult, CompleteRun,
        CompleteRunInput, DispatchRun, DispatchRunInput, FindDispatchableWork,
        ItemDispositionInput, RecordRunItemDisposition, RecordRunItemDispositionInput, StartRun,
        StartRunInput, SyncComputerRuns, SyncComputerRunsInput, SyncedComputerRuns,
    },
    identity::{
        AuthenticateHuman, AuthenticateHumanInput, AuthenticateSession, AuthorizeAgentGovernance,
        AuthorizeSpaceAccess, CloseSession, DeleteComputer, RegisterHuman, RegisterHumanInput,
        RetireAgent, SetPermission,
    },
    invitation::{
        AcceptInvitation, AcceptInvitationInput, InviteHuman, InviteHumanInput, ReadInvitation,
    },
    ports::{
        ApplicationError, AttachmentObjectPort, AttachmentTransaction, AuthenticatedHuman,
        CollaborationTransaction, ComputerRecord, DirectMessageView, DispatchCandidate, Effect,
        EffectSink, ExecutionTransaction, HumanMemberRecord, IdentityTransaction, InboxItemView,
        InboxScope, InvitationTokenPort, MemberKind, MessageDraft, PairedComputer, PairingCodePort,
        PasswordPort, PublishedMessage, RawInvitationToken, RawPairingCode, RawSessionToken,
        RunCapabilityProof, SessionTokenPort, SpaceHumanMember, SpaceMemberView, StoredObject,
        TaskTransaction, TransactionPort,
    },
    task::{
        CreateTaskFromRootMessage, CreateTaskInput, LinkThreadInput, LinkThreadToTask,
        OutcomeMessage, OutcomeRunContext, RecordTaskOutcome, RecordTaskOutcomeInput, TaskAction,
        TaskOutcome, TaskOutcomeScope, TaskPostTarget, TaskSource, UpdateTask, UpdateTaskInput,
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

#[tokio::test]
async fn dispatch_failure_is_recorded_idempotently_in_a_separate_transaction() {
    let mut port = MemoryPort::default();
    let item_id = item(920);
    let message_id = message(921);
    let channel_id = channel(922);

    assert_eq!(
        FindDispatchableWork::candidates(&mut port, OffsetDateTime::UNIX_EPOCH, 10)
            .await
            .expect("candidate query succeeds"),
        Vec::new()
    );
    assert_eq!(port.transaction_count, 1);

    assert!(
        FindDispatchableWork::record_failure(
            &mut port,
            item_id,
            Some(message_id),
            channel_id,
            "run_dispatch_conflict",
        )
        .await
        .expect("first failure changes the projection")
    );
    assert_eq!(port.transaction_count, 2);
    assert!(
        !FindDispatchableWork::record_failure(
            &mut port,
            item_id,
            Some(message_id),
            channel_id,
            "run_dispatch_conflict",
        )
        .await
        .expect("repeated failure is idempotent")
    );
    assert_eq!(port.transaction_count, 3);
    assert_eq!(
        port.state.claim_failures[&item_id],
        (
            Some(message_id),
            channel_id,
            "run_dispatch_conflict".to_owned()
        )
    );
}

#[tokio::test]
async fn adding_channel_agents_replays_the_recorded_membership_result() {
    let mut port = MemoryPort::default();
    let actor = member(930);
    let channel_id = channel(931);
    let first = member(932);
    let second = member(933);
    let key = idempotency(934);

    let added = AddChannelAgents::execute(
        &mut port,
        actor,
        channel_id,
        vec![first, second],
        key,
        OffsetDateTime::UNIX_EPOCH,
    )
    .await
    .expect("members are added");
    let replayed = AddChannelAgents::execute(
        &mut port,
        actor,
        channel_id,
        vec![second],
        key,
        OffsetDateTime::UNIX_EPOCH,
    )
    .await
    .expect("same key replays the first result");

    assert_eq!(added, vec![first, second]);
    assert_eq!(replayed, added);
    assert_eq!(
        port.state.agent_channel_members,
        HashSet::from([(channel_id, first), (channel_id, second)])
    );
}

#[tokio::test]
async fn an_agent_can_leave_a_channel_and_replay_the_same_request() {
    let mut port = MemoryPort::default();
    let agent_id = member(935);
    let channel_id = channel(936);
    let key = idempotency(937);
    port.state
        .agent_channel_members
        .insert((channel_id, agent_id));

    LeaveChannel::execute(
        &mut port,
        agent_id,
        channel_id,
        key,
        OffsetDateTime::UNIX_EPOCH,
    )
    .await
    .expect("agent leaves the channel");
    LeaveChannel::execute(
        &mut port,
        agent_id,
        channel_id,
        key,
        OffsetDateTime::UNIX_EPOCH,
    )
    .await
    .expect("same request replays");

    assert!(
        !port
            .state
            .agent_channel_members
            .contains(&(channel_id, agent_id))
    );
    assert!(
        port.state
            .channel_agent_leaves
            .contains(&(agent_id, channel_id, key))
    );
}

#[tokio::test]
async fn permissioned_channel_member_actions_are_idempotent() {
    let mut port = MemoryPort::default();
    let space_id = space(950);
    let actor = member(951);
    let target = member(952);
    let channel_id = channel(953);
    let now = OffsetDateTime::UNIX_EPOCH;
    for (id, name) in [(actor, "Operator"), (target, "Reviewer")] {
        port.state.members.insert(
            id,
            Member {
                id,
                space_id,
                display_name: name.into(),
                access_level: AccessLevel::Member,
                created_at: now,
            },
        );
    }
    port.state.channels.insert(
        channel_id,
        Channel::create(
            channel_id,
            space_id,
            BTreeSet::from([actor]),
            ChannelKind::Public,
            Some("work".into()),
            None,
            now,
        )
        .unwrap(),
    );
    port.state.permissions.extend([
        (actor, PermissionAction::ChannelInvite),
        (actor, PermissionAction::ChannelRemove),
    ]);

    assert!(
        InviteChannelMember::execute(&mut port, actor, channel_id, target, idempotency(954), now,)
            .await
            .unwrap()
    );
    assert!(
        !InviteChannelMember::execute(&mut port, actor, channel_id, target, idempotency(954), now,)
            .await
            .unwrap()
    );
    assert!(port.state.channels[&channel_id].audience.contains(&target));

    assert!(
        RemoveChannelMember::execute(&mut port, actor, channel_id, target, idempotency(955), now,)
            .await
            .unwrap()
    );
    assert!(
        !RemoveChannelMember::execute(&mut port, actor, channel_id, target, idempotency(955), now,)
            .await
            .unwrap()
    );
    assert!(!port.state.channels[&channel_id].audience.contains(&target));
}

#[tokio::test]
async fn channel_member_actions_require_the_declared_permission() {
    let mut port = MemoryPort::default();
    let space_id = space(956);
    let actor = member(957);
    let target = member(958);
    let channel_id = channel(959);
    let now = OffsetDateTime::UNIX_EPOCH;
    for (id, name) in [(actor, "Operator"), (target, "Reviewer")] {
        port.state.members.insert(
            id,
            Member {
                id,
                space_id,
                display_name: name.into(),
                access_level: AccessLevel::Member,
                created_at: now,
            },
        );
    }
    port.state.channels.insert(
        channel_id,
        Channel::create(
            channel_id,
            space_id,
            BTreeSet::from([actor]),
            ChannelKind::Public,
            Some("work".into()),
            None,
            now,
        )
        .unwrap(),
    );

    assert_eq!(
        InviteChannelMember::execute(&mut port, actor, channel_id, target, idempotency(960), now,)
            .await,
        Err(ApplicationError::PermissionDenied)
    );
    assert!(!port.state.channels[&channel_id].audience.contains(&target));

    port.state
        .members
        .get_mut(&actor)
        .expect("actor was inserted above")
        .access_level = AccessLevel::Admin;
    assert_eq!(
        InviteChannelMember::execute(&mut port, actor, channel_id, target, idempotency(961), now,)
            .await,
        Err(ApplicationError::PermissionDenied)
    );
}

#[tokio::test]
async fn command_result_updates_only_its_target_agent_through_the_domain() {
    let mut port = MemoryPort::default();
    let computer_id = computer(940);
    let target_id = member(941);
    let other_id = member(942);
    let suspended_id = member(943);
    let failed_id = member(947);
    for (agent_id, lifecycle) in [
        (target_id, AgentLifecycle::Provisioning),
        (other_id, AgentLifecycle::Provisioning),
        (suspended_id, AgentLifecycle::Suspended),
        (failed_id, AgentLifecycle::Provisioning),
    ] {
        port.state.agents.insert(
            agent_id,
            Agent {
                member_id: agent_id,
                space_id: space(944),
                computer_id: Some(computer_id),
                role_text: "test".into(),
                role_revision: 1,
                lifecycle,
                driver_kind: DriverKind::Codex,
                retired_at: None,
            },
        );
    }
    let applied_command = CommandId::from_uuid(Uuid::from_u128(945));
    let suspended_command = CommandId::from_uuid(Uuid::from_u128(946));
    let failed_command = CommandId::from_uuid(Uuid::from_u128(948));
    port.state
        .agent_provision_commands
        .insert((computer_id, applied_command, 1), Some(target_id));
    port.state
        .agent_provision_commands
        .insert((computer_id, suspended_command, 2), Some(suspended_id));
    port.state
        .agent_provision_commands
        .insert((computer_id, failed_command, 3), Some(failed_id));

    ApplyCommandResult::execute(&mut port, computer_id, applied_command, 1, true)
        .await
        .expect("provision succeeds");
    ApplyCommandResult::execute(&mut port, computer_id, applied_command, 1, true)
        .await
        .expect("duplicate result is idempotent");
    ApplyCommandResult::execute(&mut port, computer_id, suspended_command, 2, false)
        .await
        .expect("an inapplicable lifecycle is a no-op");
    ApplyCommandResult::execute(&mut port, computer_id, failed_command, 3, false)
        .await
        .expect("failed provision is recorded");

    assert_eq!(
        port.state.agents[&target_id].lifecycle,
        AgentLifecycle::Active
    );
    assert_eq!(
        port.state.agents[&other_id].lifecycle,
        AgentLifecycle::Provisioning
    );
    assert_eq!(
        port.state.agents[&suspended_id].lifecycle,
        AgentLifecycle::Suspended
    );
    assert_eq!(
        port.state.agents[&failed_id].lifecycle,
        AgentLifecycle::Error
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
    inbox_item_audits: Vec<(MemberId, String, InboxItemId)>,
    completed_run_events: HashMap<EventId, RunId>,
    assignable_agents: HashSet<MemberId>,
    permissions: HashSet<(MemberId, PermissionAction)>,
    computer_assignments: HashSet<(ComputerId, MemberId)>,
    effects: Vec<Effect>,
    /// Agents the memory port reports as having received an Item from a published Message.
    notified_members: Vec<MemberId>,
    dead_item_notices: Vec<(MemberId, &'static str)>,
    reject_message_insert: bool,
    humans: HashMap<String, (AuthenticatedHuman, String)>,
    sessions: HashMap<String, (uuid::Uuid, OffsetDateTime)>,
    space_members: HashMap<(uuid::Uuid, SpaceId), MemberId>,
    channel_members: HashMap<(uuid::Uuid, ChannelId), MemberId>,
    agent_spaces: HashMap<MemberId, SpaceId>,
    computer_spaces: HashMap<ComputerId, SpaceId>,
    pairings: HashMap<uuid::Uuid, (Pairing, String)>,
    direct_messages: Vec<DirectMessageView>,
    thread_subscriptions: HashSet<(ThreadId, MemberId)>,
    suspend_commands: Vec<(MemberId, bool)>,
    resume_commands: Vec<MemberId>,
    restart_commands: Vec<MemberId>,
    configured_agents: Vec<MemberId>,
    invitations: HashMap<uuid::Uuid, Invitation>,
    spaces: HashMap<SpaceId, (String, String)>,
    human_members: HashMap<(uuid::Uuid, SpaceId), SpaceHumanMember>,
    paired_computers: Vec<PairedComputer>,
    computer_tokens: HashMap<String, ComputerId>,
    idempotency_locks: Vec<(MemberId, String, IdempotencyKey)>,
    attachments: HashMap<AttachmentId, Attachment>,
    visible_attachments: HashSet<(AttachmentId, MemberId)>,
    attachment_writes: Vec<(String, AttachmentId, String)>,
    claim_failures: HashMap<InboxItemId, (Option<MessageId>, ChannelId, String)>,
    channel_agent_additions: HashMap<(MemberId, ChannelId, IdempotencyKey), Vec<MemberId>>,
    channel_agent_leaves: HashSet<(MemberId, ChannelId, IdempotencyKey)>,
    agent_channel_members: HashSet<(ChannelId, MemberId)>,
    channel_member_memberships: HashSet<(ChannelId, MemberId)>,
    agent_provision_commands: HashMap<(ComputerId, CommandId, u64), Option<MemberId>>,
}

#[derive(Default)]
struct MemoryPort {
    state: MemoryState,
    transaction_count: u32,
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
        self.transaction_count += 1;
        let mut transaction = MemoryTransaction {
            state: self.state.clone(),
        };
        let result = operation(&mut transaction).await?;
        self.state = transaction.state;
        Ok(result)
    }
}

#[async_trait::async_trait]
#[async_trait::async_trait]
impl IdentityTransaction for MemoryTransaction {
    async fn create_space(
        &mut self,
        _actor_user_id: uuid::Uuid,
        _space_id: SpaceId,
        _owner_id: MemberId,
        _general_channel_id: ChannelId,
        _name: &str,
        _slug: &str,
        _accent: &str,
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
    async fn insert_invitation(
        &mut self,
        invitation_id: uuid::Uuid,
        invitation: &Invitation,
        _now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.state
            .invitations
            .insert(invitation_id, invitation.clone());
        Ok(())
    }
    async fn save_invitation(
        &mut self,
        invitation_id: uuid::Uuid,
        invitation: &Invitation,
    ) -> Result<(), ApplicationError> {
        if !self.state.invitations.contains_key(&invitation_id) {
            return Err(ApplicationError::NotFound);
        }
        self.state
            .invitations
            .insert(invitation_id, invitation.clone());
        Ok(())
    }
    async fn invitation_by_token(
        &mut self,
        token_hash: &str,
    ) -> Result<Option<(uuid::Uuid, Invitation)>, ApplicationError> {
        Ok(self
            .state
            .invitations
            .iter()
            .find(|(_, invitation)| invitation.draft.token_hash == token_hash)
            .map(|(id, invitation)| (*id, invitation.clone())))
    }
    async fn invitation_by_token_for_update(
        &mut self,
        token_hash: &str,
    ) -> Result<Option<(uuid::Uuid, Invitation)>, ApplicationError> {
        self.invitation_by_token(token_hash).await
    }
    async fn space_identity(
        &mut self,
        space_id: SpaceId,
    ) -> Result<Option<(String, String)>, ApplicationError> {
        Ok(self.state.spaces.get(&space_id).cloned())
    }
    async fn insert_human_member(
        &mut self,
        record: &HumanMemberRecord,
    ) -> Result<(), ApplicationError> {
        if self
            .state
            .human_members
            .contains_key(&(record.user_id, record.space_id))
        {
            return Err(ApplicationError::Conflict);
        }
        self.state.human_members.insert(
            (record.user_id, record.space_id),
            SpaceHumanMember {
                member_id: record.member_id,
                space_id: record.space_id,
                display_name: record.display_name.clone(),
            },
        );
        Ok(())
    }
    async fn space_human_member(
        &mut self,
        user_id: uuid::Uuid,
        space_id: SpaceId,
    ) -> Result<Option<SpaceHumanMember>, ApplicationError> {
        Ok(self.state.human_members.get(&(user_id, space_id)).cloned())
    }
    async fn space_of_member(
        &mut self,
        member_id: MemberId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        Ok(self
            .state
            .members
            .get(&member_id)
            .map(|member| member.space_id))
    }
    async fn member(&mut self, member_id: MemberId) -> Result<Member, ApplicationError> {
        self.state
            .members
            .get(&member_id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }
    async fn save_member(&mut self, member: Member) -> Result<(), ApplicationError> {
        if !self.state.members.contains_key(&member.id) {
            return Err(ApplicationError::NotFound);
        }
        self.state.members.insert(member.id, member);
        Ok(())
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
}

#[async_trait::async_trait]
impl CollaborationTransaction for MemoryTransaction {
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
    async fn direct_messages_for_member(
        &mut self,
        member_id: MemberId,
        space_id: SpaceId,
    ) -> Result<Vec<DirectMessageView>, ApplicationError> {
        Ok(self
            .state
            .direct_messages
            .iter()
            .filter(|dm| {
                dm.space_id == space_id
                    && self
                        .state
                        .channels
                        .get(&dm.channel_id)
                        .is_some_and(|channel| channel.audience.contains(&member_id))
            })
            .cloned()
            .collect())
    }
    async fn direct_message_between(
        &mut self,
        space_id: SpaceId,
        first: MemberId,
        second: MemberId,
    ) -> Result<Option<DirectMessageView>, ApplicationError> {
        Ok(self
            .state
            .direct_messages
            .iter()
            .find(|dm| {
                dm.space_id == space_id
                    && self
                        .state
                        .channels
                        .get(&dm.channel_id)
                        .is_some_and(|channel| {
                            channel.audience.contains(&first) && channel.audience.contains(&second)
                        })
            })
            .cloned())
    }
    async fn space_member(
        &mut self,
        member_id: MemberId,
        space_id: SpaceId,
    ) -> Result<Option<SpaceMemberView>, ApplicationError> {
        Ok(self
            .state
            .members
            .get(&member_id)
            .filter(|member| member.space_id == space_id)
            .map(|member| SpaceMemberView {
                id: member.id,
                space_id,
                kind: if self.state.agents.contains_key(&member.id) {
                    MemberKind::Agent
                } else {
                    MemberKind::Human
                },
                display_name: member.display_name.clone(),
                access_level: member.access_level,
                permissions: self
                    .state
                    .permissions
                    .iter()
                    .filter(|(id, _)| *id == member.id)
                    .map(|(_, action)| *action)
                    .collect(),
            }))
    }
    async fn inbox_for_member(
        &mut self,
        member_id: MemberId,
        scope: InboxScope,
    ) -> Result<Vec<InboxItemView>, ApplicationError> {
        Ok(self
            .state
            .items
            .values()
            .filter(|item| {
                let item = item.view();
                item.member_id == member_id
                    && match scope {
                        InboxScope::Queue => matches!(
                            item.status,
                            InboxItemStatus::Pending
                                | InboxItemStatus::Assigned
                                | InboxItemStatus::Deferred
                        ),
                        InboxScope::Dead => item.status == InboxItemStatus::Dead,
                    }
            })
            .map(inbox_view)
            .collect())
    }
    async fn inbox_item_view(
        &mut self,
        item_id: InboxItemId,
    ) -> Result<InboxItemView, ApplicationError> {
        self.state
            .items
            .get(&item_id)
            .map(inbox_view)
            .ok_or(ApplicationError::NotFound)
    }
    async fn save_channel(&mut self, channel: Channel) -> Result<(), ApplicationError> {
        if !self.state.channels.contains_key(&channel.id) {
            return Err(ApplicationError::NotFound);
        }
        self.state.channels.insert(channel.id, channel);
        Ok(())
    }
    async fn set_thread_subscription(
        &mut self,
        thread_id: ThreadId,
        member_id: MemberId,
        following: bool,
        _now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        if following {
            self.state
                .thread_subscriptions
                .insert((thread_id, member_id));
        } else {
            self.state
                .thread_subscriptions
                .remove(&(thread_id, member_id));
        }
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
    async fn inbox_item(&mut self, id: InboxItemId) -> Result<InboxItem, ApplicationError> {
        self.state
            .items
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
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
            notified_member_ids: self.state.notified_members.clone(),
        })
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
    async fn save_message_mentions(
        &mut self,
        _message_id: MessageId,
        _mentions: Vec<MemberId>,
        _mention_all: bool,
        _now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        Ok(())
    }
    async fn insert_channel(&mut self, channel: Channel) -> Result<(), ApplicationError> {
        if self.state.channels.insert(channel.id, channel).is_some() {
            return Err(ApplicationError::Conflict);
        }
        Ok(())
    }
    async fn channel_action_audience(
        &mut self,
        focus_thread_id: ThreadId,
        space_id: SpaceId,
        private: bool,
    ) -> Result<BTreeSet<MemberId>, ApplicationError> {
        if private {
            return Ok(self
                .state
                .channel_members
                .iter()
                .filter_map(|((_, channel), member)| {
                    (self
                        .state
                        .threads
                        .get(&focus_thread_id)
                        .map(|t| t.channel_id)
                        == Some(*channel))
                    .then_some(*member)
                })
                .collect());
        }
        Ok(self
            .state
            .members
            .values()
            .filter(|m| m.space_id == space_id)
            .map(|m| m.id)
            .collect())
    }
    async fn channel_for_thread(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<ChannelId>, ApplicationError> {
        Ok(self.state.threads.get(&thread_id).map(|t| t.channel_id))
    }
    async fn join_channel(
        &mut self,
        actor: MemberId,
        channel_id: ChannelId,
        _now: OffsetDateTime,
    ) -> Result<bool, ApplicationError> {
        let channel = self
            .state
            .channels
            .get_mut(&channel_id)
            .ok_or(ApplicationError::NotFound)?;
        Ok(channel.audience.insert(actor))
    }
    async fn add_channel_agents(
        &mut self,
        actor: MemberId,
        channel_id: ChannelId,
        agent_ids: Vec<MemberId>,
        key: IdempotencyKey,
        _now: OffsetDateTime,
    ) -> Result<Vec<MemberId>, ApplicationError> {
        let idempotency = (actor, channel_id, key);
        if let Some(existing) = self.state.channel_agent_additions.get(&idempotency) {
            return Ok(existing.clone());
        }
        let mut added = Vec::new();
        for agent_id in agent_ids {
            if self
                .state
                .agent_channel_members
                .insert((channel_id, agent_id))
            {
                added.push(agent_id);
            }
        }
        self.state
            .channel_agent_additions
            .insert(idempotency, added.clone());
        Ok(added)
    }
    async fn remove_channel_agent(
        &mut self,
        _actor: MemberId,
        channel_id: ChannelId,
        agent_id: MemberId,
        _now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        if self
            .state
            .agent_channel_members
            .remove(&(channel_id, agent_id))
        {
            Ok(())
        } else {
            Err(ApplicationError::NotFound)
        }
    }
    async fn invite_channel_member(
        &mut self,
        actor: MemberId,
        channel_id: ChannelId,
        member_id: MemberId,
        idempotency_key: IdempotencyKey,
        _now: OffsetDateTime,
    ) -> Result<bool, ApplicationError> {
        let idempotency = (actor, "channel.invite".to_owned(), idempotency_key);
        if let Some(resource_id) = self.state.resource_idempotency.get(&idempotency) {
            if *resource_id != channel_id.into_uuid() {
                return Err(ApplicationError::Conflict);
            }
            return Ok(false);
        }
        let channel = self
            .state
            .channels
            .get(&channel_id)
            .ok_or(ApplicationError::NotFound)?;
        if channel.kind == ChannelKind::Direct {
            return Err(ApplicationError::Conflict);
        }
        if !channel.audience.contains(&actor)
            && !self
                .state
                .channel_member_memberships
                .contains(&(channel_id, actor))
        {
            return Err(ApplicationError::NotFound);
        }
        let target = self
            .state
            .members
            .get(&member_id)
            .filter(|member| member.space_id == channel.space_id)
            .ok_or(ApplicationError::NotFound)?;
        if self
            .state
            .agents
            .get(&member_id)
            .is_some_and(|agent| agent.lifecycle == AgentLifecycle::Retired)
        {
            return Err(ApplicationError::NotFound);
        }
        let _ = target;
        let inserted = self
            .state
            .channel_member_memberships
            .insert((channel_id, member_id));
        self.state
            .channels
            .get_mut(&channel_id)
            .expect("channel was checked above")
            .audience
            .insert(member_id);
        self.state
            .resource_idempotency
            .insert(idempotency, channel_id.into_uuid());
        Ok(inserted)
    }
    async fn remove_channel_member(
        &mut self,
        actor: MemberId,
        channel_id: ChannelId,
        member_id: MemberId,
        idempotency_key: IdempotencyKey,
        _now: OffsetDateTime,
    ) -> Result<bool, ApplicationError> {
        let idempotency = (actor, "channel.remove".to_owned(), idempotency_key);
        if let Some(resource_id) = self.state.resource_idempotency.get(&idempotency) {
            if *resource_id != channel_id.into_uuid() {
                return Err(ApplicationError::Conflict);
            }
            return Ok(false);
        }
        let channel = self
            .state
            .channels
            .get(&channel_id)
            .ok_or(ApplicationError::NotFound)?;
        if channel.kind == ChannelKind::Direct {
            return Err(ApplicationError::Conflict);
        }
        if !channel.audience.contains(&actor)
            && !self
                .state
                .channel_member_memberships
                .contains(&(channel_id, actor))
        {
            return Err(ApplicationError::NotFound);
        }
        let target = self
            .state
            .members
            .get(&member_id)
            .filter(|member| member.space_id == channel.space_id)
            .ok_or(ApplicationError::NotFound)?;
        if self
            .state
            .agents
            .get(&member_id)
            .is_some_and(|agent| agent.lifecycle == AgentLifecycle::Retired)
        {
            return Err(ApplicationError::NotFound);
        }
        let _ = target;
        let was_member = channel.audience.contains(&member_id)
            || self
                .state
                .channel_member_memberships
                .contains(&(channel_id, member_id));
        if !was_member {
            return Err(ApplicationError::NotFound);
        }
        self.state
            .channel_member_memberships
            .remove(&(channel_id, member_id));
        self.state
            .channels
            .get_mut(&channel_id)
            .expect("channel was checked above")
            .audience
            .remove(&member_id);
        self.state
            .resource_idempotency
            .insert(idempotency, channel_id.into_uuid());
        Ok(true)
    }
    async fn leave_channel(
        &mut self,
        agent_id: MemberId,
        channel_id: ChannelId,
        idempotency_key: IdempotencyKey,
        _now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        if self
            .state
            .channel_agent_leaves
            .contains(&(agent_id, channel_id, idempotency_key))
        {
            return Ok(());
        }
        if self
            .state
            .agent_channel_members
            .remove(&(channel_id, agent_id))
        {
            self.state
                .channel_agent_leaves
                .insert((agent_id, channel_id, idempotency_key));
            Ok(())
        } else {
            Err(ApplicationError::NotFound)
        }
    }
    async fn channel_member_visible(
        &mut self,
        channel_id: ChannelId,
        member_id: MemberId,
    ) -> Result<bool, ApplicationError> {
        Ok(self.state.channel_members.values().any(|m| {
            *m == member_id
                && self
                    .state
                    .channel_members
                    .keys()
                    .any(|(_, c)| *c == channel_id)
        }))
    }
    async fn message_sequence_in_channel(
        &mut self,
        _message_id: MessageId,
        _channel_id: ChannelId,
    ) -> Result<Option<u64>, ApplicationError> {
        Ok(None)
    }
    async fn channel_snapshot(&mut self, _channel_id: ChannelId) -> Result<u64, ApplicationError> {
        Ok(0)
    }
    async fn pending_item_for_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<bool, ApplicationError> {
        Ok(self.state.items.values().any(|i| {
            let v = i.view();
            v.member_id == agent_id && v.status == InboxItemStatus::Pending
        }))
    }
}

#[async_trait::async_trait]
impl TaskTransaction for MemoryTransaction {
    async fn task(&mut self, id: TaskId) -> Result<Task, ApplicationError> {
        self.state
            .tasks
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
            .find(|task| task.view().source_thread_id == thread_id)
            .map(|task| task.view().id))
    }
    async fn unfinished_task_for_thread(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<TaskId>, ApplicationError> {
        Ok(self
            .state
            .tasks
            .values()
            .find(|task| !task.view().status.is_finished() && task.linked_to(thread_id))
            .map(|task| task.view().id))
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
    async fn can_link_thread(
        &mut self,
        actor: MemberId,
        task: &Task,
        target: &Thread,
    ) -> Result<bool, ApplicationError> {
        Ok(self
            .can_read_thread(actor, task.view().source_thread_id)
            .await?
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
    async fn insert_task(&mut self, task: Task) -> Result<Task, ApplicationError> {
        if self
            .state
            .tasks
            .insert(task.view().id, task.clone())
            .is_some()
        {
            return Err(ApplicationError::Conflict);
        }
        Ok(task)
    }
    async fn save_task(&mut self, task: Task) -> Result<(), ApplicationError> {
        self.state.tasks.insert(task.view().id, task);
        Ok(())
    }
}

#[async_trait::async_trait]
impl ExecutionTransaction for MemoryTransaction {
    async fn run(&mut self, id: RunId) -> Result<Run, ApplicationError> {
        self.state
            .runs
            .get(&id)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }
    async fn active_run_for_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<Option<RunId>, ApplicationError> {
        Ok(self
            .state
            .runs
            .values()
            .find(|run| run.view().agent_id == agent_id && run.view().status.is_active())
            .map(|run| run.view().id))
    }
    async fn completed_run_for_event(
        &mut self,
        event_id: EventId,
    ) -> Result<Option<RunId>, ApplicationError> {
        Ok(self.state.completed_run_events.get(&event_id).copied())
    }
    async fn nonterminal_runs_for_computer(
        &mut self,
        _computer_id: ComputerId,
    ) -> Result<Vec<RunId>, ApplicationError> {
        Ok(self
            .state
            .runs
            .values()
            .filter(|run| !run.is_terminal())
            .map(|run| run.view().id)
            .collect())
    }
    async fn save_run(&mut self, run: Run) -> Result<(), ApplicationError> {
        self.state.runs.insert(run.view().id, run);
        Ok(())
    }
    async fn observed_thread_sequence(
        &mut self,
        _run_id: RunId,
    ) -> Result<Option<u64>, ApplicationError> {
        Ok(None)
    }
    async fn record_observed_thread_sequence(
        &mut self,
        _run_id: RunId,
        _sequence: u64,
    ) -> Result<(), ApplicationError> {
        Ok(())
    }
    async fn dispatchable_work(
        &mut self,
        _now: OffsetDateTime,
        _limit: u32,
    ) -> Result<Vec<DispatchCandidate>, ApplicationError> {
        Ok(Vec::new())
    }
    async fn record_dispatch_failure(
        &mut self,
        item_id: InboxItemId,
        message_id: Option<MessageId>,
        channel_id: ChannelId,
        error_code: &str,
    ) -> Result<bool, ApplicationError> {
        let failure = (message_id, channel_id, error_code.to_owned());
        if self.state.claim_failures.get(&item_id) == Some(&failure) {
            return Ok(false);
        }
        self.state.claim_failures.insert(item_id, failure);
        Ok(true)
    }
    async fn authorize_run_capability(
        &mut self,
        proof: &RunCapabilityProof,
    ) -> Result<bool, ApplicationError> {
        let Some(run) = self.state.runs.get(&proof.run_id) else {
            return Ok(false);
        };
        let view = run.view();
        Ok(view.agent_id == proof.agent_id
            && view.space_id == proof.space_id
            && view.task_id == proof.task_id
            && view.focus_thread_id == proof.focus_thread_id
            && view.status == RunStatus::Working
            && self
                .state
                .computer_assignments
                .contains(&(proof.computer_id, proof.agent_id)))
    }
    async fn active_run_for_visible_agent(
        &mut self,
        agent_id: MemberId,
        _viewer_id: MemberId,
    ) -> Result<Option<RunId>, ApplicationError> {
        Ok(self
            .state
            .runs
            .values()
            .find(|r| {
                let v = r.view();
                v.agent_id == agent_id && v.status == RunStatus::Working
            })
            .map(|r| r.view().id))
    }
    async fn agent_provision_command_target(
        &mut self,
        computer_id: ComputerId,
        command_id: CommandId,
        sequence: u64,
    ) -> Result<Option<MemberId>, ApplicationError> {
        self.state
            .agent_provision_commands
            .get(&(computer_id, command_id, sequence))
            .copied()
            .ok_or(ApplicationError::NotFound)
    }
}

#[async_trait::async_trait]
impl AttachmentTransaction for MemoryTransaction {
    async fn space_of_attachment(
        &mut self,
        attachment_id: AttachmentId,
    ) -> Result<Option<SpaceId>, ApplicationError> {
        Ok(self
            .state
            .attachments
            .get(&attachment_id)
            .map(|attachment| attachment.view().space_id))
    }
    async fn attachment(
        &mut self,
        id: AttachmentId,
    ) -> Result<Option<Attachment>, ApplicationError> {
        Ok(self.state.attachments.get(&id).cloned())
    }
    async fn insert_attachment(&mut self, attachment: &Attachment) -> Result<(), ApplicationError> {
        if self.state.attachments.contains_key(&attachment.view().id) {
            return Err(ApplicationError::Conflict);
        }
        self.state
            .attachments
            .insert(attachment.view().id, attachment.clone());
        Ok(())
    }
    async fn save_attachment(&mut self, attachment: &Attachment) -> Result<(), ApplicationError> {
        self.state
            .attachments
            .insert(attachment.view().id, attachment.clone());
        Ok(())
    }
    async fn attachment_is_visible(
        &mut self,
        id: AttachmentId,
        viewer: MemberId,
    ) -> Result<bool, ApplicationError> {
        Ok(self.state.visible_attachments.contains(&(id, viewer)))
    }
    async fn record_attachment_write(
        &mut self,
        _space_id: SpaceId,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        attachment_id: AttachmentId,
        event_kind: &str,
        _now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.state
            .resource_idempotency
            .insert((actor, action.to_owned(), key), attachment_id.into_uuid());
        self.state.attachment_writes.push((
            action.to_owned(),
            attachment_id,
            event_kind.to_owned(),
        ));
        Ok(())
    }
}

#[async_trait::async_trait]
impl EffectSink for MemoryTransaction {
    async fn queue_agent_suspend(
        &mut self,
        agent_id: MemberId,
        computer_id: Option<ComputerId>,
        cancel_current_run: bool,
    ) -> Result<(), ApplicationError> {
        if computer_id.is_some() {
            self.state
                .suspend_commands
                .push((agent_id, cancel_current_run));
        }
        Ok(())
    }
    async fn queue_agent_resume(
        &mut self,
        agent_id: MemberId,
        computer_id: Option<ComputerId>,
    ) -> Result<(), ApplicationError> {
        if computer_id.is_some() {
            self.state.resume_commands.push(agent_id);
        }
        Ok(())
    }
    async fn queue_agent_restart(
        &mut self,
        agent_id: MemberId,
        computer_id: Option<ComputerId>,
    ) -> Result<(), ApplicationError> {
        if computer_id.is_some() {
            self.state.restart_commands.push(agent_id);
        }
        Ok(())
    }
    async fn queue_agent_configuration(&mut self, agent: &Agent) -> Result<(), ApplicationError> {
        if agent.computer_id.is_some() {
            self.state.configured_agents.push(agent.member_id);
        }
        Ok(())
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
    async fn save_inbox_item(&mut self, item: InboxItem) -> Result<(), ApplicationError> {
        self.state.items.insert(item.view().id, item);
        Ok(())
    }
    async fn insert_dead_item_notice(
        &mut self,
        agent_id: MemberId,
        thread_id: ThreadId,
        error_code: &'static str,
        now: OffsetDateTime,
    ) -> Result<InboxItemId, ApplicationError> {
        let item_id = InboxItemId::from_uuid(uuid::Uuid::now_v7());
        let notice = InboxItem::open_hard(
            item_id,
            space(1),
            agent_id,
            None,
            thread_id,
            None,
            InboxItemKind::System,
            now,
        )?;
        self.state.items.insert(item_id, notice);
        self.state.dead_item_notices.push((agent_id, error_code));
        Ok(item_id)
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
    async fn record_inbox_item_audit(
        &mut self,
        actor: MemberId,
        action: &str,
        item_id: InboxItemId,
        _now: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        self.state
            .inbox_item_audits
            .push((actor, action.to_owned(), item_id));
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
        inbox(item_id, agent, thread_id, None, InboxItemStatus::Assigned),
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

    assert_eq!(created.view().status, TaskStatus::InProgress);
    assert_eq!(port.state.runs[&run_id].view().task_id, Some(task_id));
    assert_eq!(port.state.items[&item_id].view().task_id, Some(task_id));

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
    assert_eq!(retried.view().id, task_id);
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
        ApplicationError::Domain(DomainError::SourceIsNotRoot)
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
    let occupying = make_task(task(26), occupied, actor, TaskStatus::Todo);
    port.state.tasks.insert(first.view().id, first);
    port.state.tasks.insert(occupying.view().id, occupying);

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
        ApplicationError::Domain(DomainError::IncompatibleAudience)
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

    let conflict = DispatchRun::execute(
        &mut port,
        dispatch_input(run(35), agent, Some(task(33)), focus),
    )
    .await
    .unwrap_err();
    assert_eq!(conflict, ApplicationError::Conflict);

    port.state.runs.clear();
    let focus_error = DispatchRun::execute(
        &mut port,
        dispatch_input(run(36), agent, Some(task(33)), other_focus),
    )
    .await
    .unwrap_err();
    assert!(matches!(
        focus_error,
        ApplicationError::Domain(DomainError::FocusOutsideTask)
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
    let ambient = InboxItem::open_ambient(
        item_id,
        space(1),
        agent,
        focus,
        InboxItemKind::ChannelActivity,
        1,
        OffsetDateTime::UNIX_EPOCH,
    )
    .expect("channel activity is ambient");
    port.state.items.insert(item_id, ambient);
    let mut input = dispatch_input(run_id, agent, None, focus);
    input.item_ids.push(item_id);

    let claimed = DispatchRun::execute(&mut port, input).await.unwrap();

    assert_eq!(claimed.items().next().unwrap().inbox_item_id, item_id);
    assert_eq!(
        port.state.items[&item_id].view().status,
        InboxItemStatus::Assigned
    );
    assert_eq!(
        port.state.items[&item_id].view().assigned_run_id,
        Some(run_id)
    );
}

#[tokio::test]
async fn run_started_requires_assignment_and_is_idempotent() {
    let agent = member(37);
    let focus = thread(38);
    let run_id = run(39);
    let mut dispatched = running_run(run_id, agent, focus, None, Vec::new());
    update_test_run(&mut dispatched, |snapshot| {
        snapshot.status = RunStatus::Dispatched;
        snapshot.started_at = None;
    });
    let mut port = MemoryPort::default();
    port.state.runs.insert(run_id, dispatched);
    port.state
        .computer_assignments
        .insert((computer(999), agent));

    let input = || StartRunInput {
        run_id,
        computer_id: computer(999),
        now: OffsetDateTime::UNIX_EPOCH,
    };
    let started = StartRun::execute(&mut port, input()).await.unwrap();
    assert_eq!(started.view().status, RunStatus::Working);
    assert_eq!(started.view().started_at, Some(OffsetDateTime::UNIX_EPOCH));
    assert!(matches!(port.state.effects.last(), Some(Effect::RunStarted(id)) if *id == run_id));
    assert_eq!(
        StartRun::execute(&mut port, input()).await.unwrap(),
        started
    );
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

    let terminal_id = run(40);
    let mut terminal = running_run(terminal_id, agent, focus, None, Vec::new());
    update_test_run(&mut terminal, |snapshot| {
        snapshot.status = RunStatus::Failed;
        snapshot.outcome = Some(RunOutcome::Failed);
        snapshot.error_code = Some(RunErrorCode::ComputerRestarted);
        snapshot.finished_at = Some(OffsetDateTime::UNIX_EPOCH);
    });
    port.state.runs.insert(terminal_id, terminal.clone());
    let repeated_terminal = StartRun::execute(
        &mut port,
        StartRunInput {
            run_id: terminal_id,
            computer_id: computer(999),
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap();
    assert_eq!(repeated_terminal, terminal);
    assert_eq!(port.state.effects.len(), 1);
}

#[tokio::test]
async fn the_assignees_first_task_run_moves_the_task_from_todo_to_in_progress() {
    let assignee = member(240);
    let other_agent = member(241);
    let focus = thread(242);
    let task_id = task(243);
    let queued = |run_id, agent| {
        let mut queued = running_run(run_id, agent, focus, Some(task_id), Vec::new());
        update_test_run(&mut queued, |snapshot| {
            snapshot.status = RunStatus::Dispatched;
            snapshot.started_at = None;
        });
        queued
    };
    let start = |run_id| StartRunInput {
        run_id,
        computer_id: computer(999),
        now: OffsetDateTime::UNIX_EPOCH,
    };

    // A Run by an Agent that does not own the Task leaves the status alone.
    let foreign_run = run(244);
    let mut port = MemoryPort::default();
    let mut todo = make_task(task_id, focus, assignee, TaskStatus::Todo);
    update_test_task(&mut todo, |snapshot| {
        snapshot.assignee_agent_member_id = Some(assignee);
    });
    port.state.tasks.insert(task_id, todo.clone());
    port.state
        .runs
        .insert(foreign_run, queued(foreign_run, other_agent));
    port.state
        .computer_assignments
        .insert((computer(999), other_agent));
    StartRun::execute(&mut port, start(foreign_run))
        .await
        .unwrap();
    assert_eq!(port.state.tasks[&task_id].view().status, TaskStatus::Todo);

    // The assignee's Run advances it, and a replay does not transition twice.
    let owned_run = run(245);
    let mut port = MemoryPort::default();
    port.state.tasks.insert(task_id, todo);
    port.state
        .runs
        .insert(owned_run, queued(owned_run, assignee));
    port.state
        .computer_assignments
        .insert((computer(999), assignee));
    StartRun::execute(&mut port, start(owned_run))
        .await
        .unwrap();
    assert_eq!(
        port.state.tasks[&task_id].view().status,
        TaskStatus::InProgress
    );
    assert!(
        port.state
            .effects
            .iter()
            .any(|effect| matches!(effect, Effect::TaskUpdated(id) if *id == task_id))
    );
    port.state.effects.clear();
    StartRun::execute(&mut port, start(owned_run))
        .await
        .unwrap();
    assert_eq!(
        port.state.tasks[&task_id].view().status,
        TaskStatus::InProgress
    );
    assert!(port.state.effects.is_empty());
}

#[tokio::test]
async fn publishing_a_reply_refreshes_the_thread_and_each_notified_inbox() {
    let author = member(250);
    let first_agent = member(251);
    let second_agent = member(252);
    let root = thread(253);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, root, &[author, first_agent, second_agent]);
    port.state.notified_members = vec![first_agent, second_agent];

    PublishMessage::execute(
        &mut port,
        MessageDraft {
            message_id: message(254),
            channel_id: channel(1),
            author_member_id: author,
            idempotency_key: idempotency(255),
            body_markdown: "回复".into(),
            thread_id: Some(root),
            reply_to_message_id: None,
            mentions: Vec::new(),
            mention_all: false,
            attachment_ids: Vec::new(),
            handled_item: None,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap();

    assert!(
        port.state
            .effects
            .iter()
            .any(|effect| matches!(effect, Effect::ThreadUpdated(id) if *id == root))
    );
    let notified = port
        .state
        .effects
        .iter()
        .filter_map(|effect| match effect {
            Effect::InboxChanged(member_id) => Some(*member_id),
            _ => None,
        })
        .collect::<Vec<_>>();
    assert_eq!(notified, vec![first_agent, second_agent]);
}

/// A reconnecting Computer reports which Runs it still holds. Anything else the Server believes is
/// live on that Computer died with the previous daemon, so it fails and its Items return to the queue.
/// Nothing here consults a clock: the Computer's report is the only trigger.
#[tokio::test]
async fn syncing_a_reconnected_computer_fails_runs_it_no_longer_holds() {
    let agent = member(260);
    let focus = thread(261);
    let lost_run = run(262);
    let live_run = run(263);
    let retryable = item(264);
    let exhausted = item(265);
    let now = OffsetDateTime::UNIX_EPOCH + time::Duration::hours(5);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent]);

    port.state.runs.insert(
        lost_run,
        running_run(lost_run, agent, focus, None, vec![retryable, exhausted]),
    );
    // A Run the Computer still holds must survive the sync untouched.
    port.state.runs.insert(
        live_run,
        running_run(live_run, member(266), focus, None, Vec::new()),
    );
    for (item_id, retry_count) in [(retryable, 0), (exhausted, 2)] {
        let mut assigned = inbox(item_id, agent, focus, None, InboxItemStatus::Assigned);
        update_test_item(&mut assigned, |snapshot| {
            snapshot.assigned_run_id = Some(lost_run);
            snapshot.retry_count = retry_count;
        });
        port.state.items.insert(item_id, assigned);
    }

    let synced = SyncComputerRuns::execute(
        &mut port,
        SyncComputerRunsInput {
            computer_id: computer(1),
            live_run_ids: vec![live_run],
            max_retry_count: 2,
            now,
        },
    )
    .await
    .unwrap();

    assert_eq!(
        synced,
        SyncedComputerRuns {
            runs_failed: 1,
            items_released: 1,
            items_dead: 1,
        }
    );
    assert_eq!(port.state.runs[&lost_run].view().status, RunStatus::Failed);
    assert_eq!(
        port.state.runs[&lost_run].view().error_code,
        Some(RunErrorCode::ComputerRestarted)
    );
    assert_eq!(
        port.state.runs[&live_run].view().status,
        RunStatus::Working,
        "a Run the Computer still holds is untouched"
    );
    assert_eq!(
        port.state.items[&retryable].view().status,
        InboxItemStatus::Pending
    );
    assert_eq!(
        port.state.items[&exhausted].view().status,
        InboxItemStatus::Dead
    );
    assert_eq!(
        port.state.dead_item_notices,
        vec![(agent, "inbox_item_dead")],
        "a retired Item reports itself without copying the source Message"
    );

    // Idempotent: the Run is terminal, so a second sync changes nothing.
    let repeated = SyncComputerRuns::execute(
        &mut port,
        SyncComputerRunsInput {
            computer_id: computer(1),
            live_run_ids: vec![live_run],
            max_retry_count: 2,
            now,
        },
    )
    .await
    .unwrap();
    assert_eq!(repeated, SyncedComputerRuns::default());
    assert_eq!(port.state.items[&retryable].view().retry_count, 1);
}

#[tokio::test]
async fn syncing_a_reconnected_computer_applies_existing_item_dispositions() {
    let agent = member(267);
    let focus = thread(268);
    let lost_run = run(269);
    let handled = item(270);
    let unresolved = item(271);
    let released = item(272);
    let now = OffsetDateTime::UNIX_EPOCH + time::Duration::hours(5);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent]);

    let mut current = running_run(
        lost_run,
        agent,
        focus,
        None,
        vec![handled, unresolved, released],
    );
    update_test_run(&mut current, |snapshot| {
        snapshot.items[0].disposition = Some(InboxItemDisposition::Handled);
        snapshot.items[2].disposition = Some(InboxItemDisposition::Released);
    });
    port.state.runs.insert(lost_run, current);
    for item_id in [handled, unresolved, released] {
        let mut assigned = inbox(item_id, agent, focus, None, InboxItemStatus::Assigned);
        update_test_item(&mut assigned, |snapshot| {
            snapshot.assigned_run_id = Some(lost_run);
        });
        port.state.items.insert(item_id, assigned);
    }

    let synced = SyncComputerRuns::execute(
        &mut port,
        SyncComputerRunsInput {
            computer_id: computer(1),
            live_run_ids: Vec::new(),
            max_retry_count: 2,
            now,
        },
    )
    .await
    .unwrap();

    assert_eq!(synced.runs_failed, 1);
    assert_eq!(synced.items_released, 2);
    assert_eq!(synced.items_dead, 0);
    assert_eq!(port.state.runs[&lost_run].view().status, RunStatus::Failed);
    let run_items = port.state.runs[&lost_run].items().collect::<Vec<_>>();
    assert_eq!(
        run_items[0].disposition,
        Some(InboxItemDisposition::Handled)
    );
    assert_eq!(
        run_items[1].disposition,
        Some(InboxItemDisposition::Released)
    );
    assert_eq!(
        run_items[2].disposition,
        Some(InboxItemDisposition::Released)
    );
    assert_eq!(
        port.state.items[&handled].view().status,
        InboxItemStatus::Handled
    );
    assert_eq!(port.state.items[&handled].view().assigned_run_id, None);
    assert_eq!(
        port.state.items[&unresolved].view().status,
        InboxItemStatus::Pending
    );
    assert_eq!(port.state.items[&unresolved].view().retry_count, 1);
    assert_eq!(
        port.state.items[&released].view().status,
        InboxItemStatus::Pending
    );
    assert_eq!(port.state.items[&released].view().retry_count, 1);
}

#[tokio::test]
async fn publishing_a_message_requires_ready_attachments_from_the_same_space() {
    let author = member(250);
    let now = OffsetDateTime::UNIX_EPOCH;
    let mut port = MemoryPort::default();
    port.state.channels.insert(
        channel(1),
        Channel::create(
            channel(1),
            space(1),
            [author].into_iter().collect(),
            ChannelKind::Private,
            Some("general".into()),
            None,
            now,
        )
        .unwrap(),
    );
    let mut ready = Attachment::open(
        attachment(500),
        space(1),
        author,
        "ready.txt",
        "text/plain",
        now,
    )
    .unwrap();
    ready
        .complete(
            &DeclaredContent {
                size: 4,
                sha256_hex: hex::encode([0u8; 32]),
            },
            ContentDigest {
                length: 4,
                sha256: [0u8; 32],
            },
            now,
        )
        .unwrap();
    let uploading = Attachment::open(
        attachment(501),
        space(1),
        author,
        "pending.txt",
        "text/plain",
        now,
    )
    .unwrap();
    let mut other_space = Attachment::open(
        attachment(502),
        space(2),
        author,
        "foreign.txt",
        "text/plain",
        now,
    )
    .unwrap();
    other_space
        .complete(
            &DeclaredContent {
                size: 4,
                sha256_hex: hex::encode([0u8; 32]),
            },
            ContentDigest {
                length: 4,
                sha256: [0u8; 32],
            },
            now,
        )
        .unwrap();
    port.state
        .attachments
        .insert(ready.view().id, ready.clone());
    port.state
        .attachments
        .insert(uploading.view().id, uploading.clone());
    port.state
        .attachments
        .insert(other_space.view().id, other_space.clone());

    let draft = |attachment_ids: Vec<AttachmentId>, key: u128| MessageDraft {
        message_id: message(600),
        channel_id: channel(1),
        author_member_id: author,
        idempotency_key: idempotency(key),
        thread_id: None,
        reply_to_message_id: None,
        body_markdown: "正文".into(),
        mentions: Vec::new(),
        mention_all: false,
        attachment_ids,
        handled_item: None,
        now,
    };

    PublishMessage::execute(&mut port, draft(vec![ready.view().id], 601))
        .await
        .unwrap();
    assert!(matches!(
        PublishMessage::execute(&mut port, draft(vec![uploading.view().id], 602)).await,
        Err(ApplicationError::Conflict)
    ));
    assert!(matches!(
        PublishMessage::execute(&mut port, draft(vec![other_space.view().id], 603)).await,
        Err(ApplicationError::NotFound)
    ));
    assert!(matches!(
        PublishMessage::execute(&mut port, draft(vec![attachment(999)], 604)).await,
        Err(ApplicationError::NotFound)
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
    let mut leased_item = inbox(item_id, agent, focus, None, InboxItemStatus::Assigned);
    update_test_item(&mut leased_item, |snapshot| {
        snapshot.assigned_run_id = Some(run_id)
    });
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
            item_id,
            disposition: InboxItemDisposition::Deferred,
            defer_until: Some(defer_until),
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap();

    assert_eq!(
        updated.items().next().unwrap().disposition,
        Some(InboxItemDisposition::Deferred)
    );
    assert_eq!(
        port.state.items[&item_id].view().status,
        InboxItemStatus::Assigned
    );
    assert_eq!(port.state.items[&item_id].view().available_at, defer_until);
}

/// A Run in a terminal state is not executing, so a newly arrived hard Item must stay pending and wait
/// for the next Run instead of attaching to work nobody is doing.
#[tokio::test]
async fn hard_item_created_after_a_terminal_run_stays_pending_without_effect() {
    let agent = member(53);
    let focus = thread(54);
    let run_id = run(55);
    let item_id = item(56);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent]);
    let mut current = running_run(run_id, agent, focus, None, Vec::new());
    current
        .finish(
            RunOutcome::Completed,
            None,
            None,
            OffsetDateTime::UNIX_EPOCH,
        )
        .expect("the test Run completes");
    port.state.runs.insert(run_id, current);
    port.state.items.insert(
        item_id,
        inbox(item_id, agent, focus, None, InboxItemStatus::Pending),
    );

    let route = RouteHardItem::execute(&mut port, RouteHardItemInput { item_id })
        .await
        .unwrap();

    assert_eq!(route, HardItemRoute::Pending);
    assert_eq!(
        port.state.items[&item_id].view().status,
        InboxItemStatus::Pending
    );
    assert!(port.state.effects.is_empty());
}

#[tokio::test]
async fn hard_item_routes_to_same_focus_once_and_assigns_to_the_run() {
    let agent = member(44);
    let focus = thread(45);
    let run_id = run(46);
    let item_id = item(47);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent]);
    port.state
        .runs
        .insert(run_id, running_run(run_id, agent, focus, None, Vec::new()));
    port.state.items.insert(
        item_id,
        inbox(item_id, agent, focus, None, InboxItemStatus::Pending),
    );

    let route = RouteHardItem::execute(&mut port, RouteHardItemInput { item_id })
        .await
        .unwrap();
    assert_eq!(route, HardItemRoute::Attached { sequence: 1 });
    assert_eq!(
        port.state.items[&item_id].view().status,
        InboxItemStatus::Assigned
    );
    assert_eq!(
        port.state.items[&item_id].view().assigned_run_id,
        Some(run_id)
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
    assert_eq!(
        port.state.items[&item_id].view().status,
        InboxItemStatus::Pending
    );
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
            InboxItemStatus::Assigned,
        ),
    );
    port.state.runs.insert(
        run_id,
        running_run(run_id, agent, focus, Some(task_id), vec![item_id]),
    );

    let completed = CompleteRun::execute(&mut port, complete_run_input(run_id, item_id))
        .await
        .unwrap();
    assert_eq!(completed.view().status, RunStatus::Completed);
    let retried = CompleteRun::execute(&mut port, complete_run_input(run_id, item_id))
        .await
        .unwrap();
    assert_eq!(retried.view().status, RunStatus::Completed);
    assert_eq!(
        port.state.items[&item_id].view().status,
        InboxItemStatus::Handled
    );
    assert_eq!(
        port.state.tasks[&task_id].view().status,
        TaskStatus::InProgress
    );
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

    let error = RecordTaskOutcome::execute(
        &mut port,
        RecordTaskOutcomeInput {
            scope: TaskOutcomeScope::Browser { task_id },
            actor_member_id: assignee,
            idempotency_key: idempotency(203),
            outcome: TaskOutcome::Done {
                result: OutcomeMessage {
                    message_id: result_id,
                    body_markdown: "完成".into(),
                    post_to: TaskPostTarget::Thread(focus),
                },
            },
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap_err();
    assert_eq!(error, ApplicationError::Conflict);
    assert_eq!(
        port.state.tasks[&task_id].view().status,
        TaskStatus::InProgress
    );
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
            InboxItemStatus::Assigned,
        );
        update_test_item(&mut inbox_item, |snapshot| {
            snapshot.assigned_run_id = Some(run_id)
        });
        port.state.items.insert(item_id, inbox_item);
    }
    let mut current_run = running_run(
        run_id,
        agent,
        focus,
        Some(task_id),
        vec![handled_item, deferred_item],
    );
    update_test_run(&mut current_run, |snapshot| {
        snapshot.items[1].disposition = Some(InboxItemDisposition::Deferred)
    });
    port.state.runs.insert(run_id, current_run);

    let input = || RecordTaskOutcomeInput {
        scope: TaskOutcomeScope::AgentRun(OutcomeRunContext {
            run_id,
            computer_id,
            message_snapshot_sequence: 0,
        }),
        actor_member_id: agent,
        idempotency_key: key,
        outcome: TaskOutcome::Done {
            result: OutcomeMessage {
                message_id: result_id,
                body_markdown: "完成".into(),
                post_to: TaskPostTarget::Focus,
            },
        },
        now,
    };
    let completed = RecordTaskOutcome::execute(&mut port, input())
        .await
        .unwrap();

    assert_eq!(completed.view().status, TaskStatus::Done);
    assert_eq!(port.state.runs[&run_id].view().status, RunStatus::Completed);
    assert_eq!(
        port.state.items[&handled_item].view().status,
        InboxItemStatus::Handled
    );
    assert_eq!(
        port.state.items[&deferred_item].view().status,
        InboxItemStatus::Deferred
    );
    assert!(port.state.messages.contains_key(&result_id));
    assert_eq!(
        port.state.task_audits,
        vec![(agent, "task.done".into(), task_id)]
    );

    let replayed = RecordTaskOutcome::execute(&mut port, input())
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
    let mut leased_item = inbox(item_id, agent_id, focus, None, InboxItemStatus::Assigned);
    update_test_item(&mut leased_item, |snapshot| {
        snapshot.assigned_run_id = Some(run_id)
    });
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
        ApplicationError::Domain(DomainError::ComputerHasAgents)
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
    assert_eq!(port.state.runs[&run_id].view().status, RunStatus::Canceled);
    assert_eq!(
        port.state.items[&item_id].view().status,
        InboxItemStatus::Pending
    );
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

    RecordTaskOutcome::execute(
        &mut port,
        RecordTaskOutcomeInput {
            scope: TaskOutcomeScope::Browser { task_id },
            actor_member_id: assignee,
            idempotency_key: idempotency(204),
            outcome: TaskOutcome::SubmitReview { message: None },
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
        ApplicationError::Domain(DomainError::InvalidReviewer)
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
    assert_eq!(returned.view().status, TaskStatus::InProgress);

    let closed = RecordTaskOutcome::execute(
        &mut port,
        RecordTaskOutcomeInput {
            scope: TaskOutcomeScope::Browser { task_id },
            actor_member_id: assignee,
            idempotency_key: idempotency(207),
            outcome: TaskOutcome::Close {
                reason: CloseReason::Obsolete,
                note: None,
            },
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap();
    assert_eq!(closed.view().status, TaskStatus::Closed);
    assert!(closed.view().finished_at.is_some());
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
    port.state.computers.insert(
        computer_id,
        Computer {
            id: computer_id,
            space_id: space(1),
            lifecycle: ComputerLifecycle::Online,
            token_hash: Some("hash".into()),
            deleted_at: None,
        },
    );
    insert_thread(&mut port, focus, &[actor]);
    port.state
        .runs
        .insert(run_id, running_run(run_id, actor, focus, None, Vec::new()));

    let input = || CreateAgentActionInput {
        agent_member_id: created_agent,
        display_name: "Implementer".into(),
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
        mentions: Vec::new(),
        mention_all: false,
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
        inbox(item_id, agent_id, focus, None, InboxItemStatus::Assigned),
    );
    port.state
        .computer_assignments
        .insert((computer(999), agent_id));
    let input = || AcknowledgeDeliveryInput {
        run_id,
        computer_id: computer(999),
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
    assert_eq!(
        port.state.items[&item_id].view().status,
        InboxItemStatus::Pending
    );
    assert_eq!(
        port.state.runs[&run_id].items().next().unwrap().disposition,
        Some(InboxItemDisposition::Released)
    );
    CompleteRun::execute(
        &mut port,
        CompleteRunInput {
            max_retry_count: 5,
            event_id: event(1623),
            run_id,
            computer_id: computer(999),
            outcome: RunOutcome::Completed,
            error_code: None,
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
    assert_eq!(port.state.runs[&run_id].view().status, RunStatus::Completed);
}

#[tokio::test]
async fn run_result_accepts_a_matching_disposition_already_applied_during_recovery() {
    let run_id = run(1624);
    let item_id = item(1625);
    let agent_id = member(1626);
    let focus = thread(1627);
    let mut port = MemoryPort::default();
    port.state
        .computer_assignments
        .insert((computer(999), agent_id));
    let mut current = running_run(run_id, agent_id, focus, None, vec![item_id]);
    update_test_run(&mut current, |snapshot| {
        snapshot.items[0].disposition = Some(InboxItemDisposition::Handled);
    });
    port.state.runs.insert(run_id, current);
    let mut handled = inbox(item_id, agent_id, focus, None, InboxItemStatus::Assigned);
    update_test_item(&mut handled, |snapshot| {
        snapshot.status = InboxItemStatus::Handled;
        snapshot.assigned_run_id = None;
        snapshot.handled_at = Some(OffsetDateTime::UNIX_EPOCH);
    });
    port.state.items.insert(item_id, handled);

    CompleteRun::execute(
        &mut port,
        CompleteRunInput {
            max_retry_count: 5,
            event_id: event(1628),
            run_id,
            computer_id: computer(999),
            outcome: RunOutcome::Completed,
            error_code: None,
            item_dispositions: vec![ItemDispositionInput {
                item_id,
                disposition: InboxItemDisposition::Handled,
            }],
            continuation_note: None,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap();

    assert_eq!(port.state.runs[&run_id].view().status, RunStatus::Completed);
    assert_eq!(
        port.state.items[&item_id].view().status,
        InboxItemStatus::Handled
    );
}

#[tokio::test]
async fn terminal_released_result_keeps_server_recorded_explicit_disposition() {
    let run_id = run(1650);
    let item_id = item(1651);
    let agent_id = member(1652);
    let focus = thread(1653);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent_id]);
    port.state
        .computer_assignments
        .insert((computer(999), agent_id));
    // The Server already recorded an explicit Handled disposition from the Agent's message send,
    // but the Computer failed to mirror it locally and reports the default Released at yield.
    let mut current = running_run(run_id, agent_id, focus, None, vec![item_id]);
    update_test_run(&mut current, |snapshot| {
        snapshot.items[0].disposition = Some(InboxItemDisposition::Handled);
    });
    port.state.runs.insert(run_id, current);
    let mut assigned = inbox(item_id, agent_id, focus, None, InboxItemStatus::Assigned);
    update_test_item(&mut assigned, |snapshot| {
        snapshot.assigned_run_id = Some(run_id);
    });
    port.state.items.insert(item_id, assigned);

    CompleteRun::execute(
        &mut port,
        CompleteRunInput {
            max_retry_count: 5,
            event_id: event(1654),
            run_id,
            computer_id: computer(999),
            outcome: RunOutcome::Yielded,
            error_code: None,
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

    assert_eq!(port.state.runs[&run_id].view().status, RunStatus::Yielded);
    assert_eq!(
        port.state.runs[&run_id].items().next().unwrap().disposition,
        Some(InboxItemDisposition::Handled)
    );
    let handled = port.state.items[&item_id].view();
    assert_eq!(handled.status, InboxItemStatus::Handled);
}

#[tokio::test]
async fn late_result_for_a_terminal_run_is_idempotent_after_recovery() {
    let run_id = run(1629);
    let item_id = item(1630);
    let agent_id = member(1631);
    let focus = thread(1632);
    let mut port = MemoryPort::default();
    port.state
        .computer_assignments
        .insert((computer(999), agent_id));
    port.state.runs.insert(
        run_id,
        running_run(run_id, agent_id, focus, None, vec![item_id]),
    );
    let mut assigned = inbox(item_id, agent_id, focus, None, InboxItemStatus::Assigned);
    update_test_item(&mut assigned, |snapshot| {
        snapshot.assigned_run_id = Some(run_id);
    });
    port.state.items.insert(item_id, assigned);

    let recovered = CompleteRun::execute(
        &mut port,
        CompleteRunInput {
            max_retry_count: 5,
            event_id: event(1633),
            run_id,
            computer_id: computer(999),
            outcome: RunOutcome::Failed,
            error_code: Some(RunErrorCode::ComputerRestarted),
            item_dispositions: Vec::new(),
            continuation_note: None,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap();
    assert_eq!(recovered.view().status, RunStatus::Failed);

    let late = CompleteRun::execute(
        &mut port,
        CompleteRunInput {
            max_retry_count: 5,
            event_id: event(1634),
            run_id,
            computer_id: computer(999),
            outcome: RunOutcome::Completed,
            error_code: None,
            item_dispositions: vec![ItemDispositionInput {
                item_id,
                disposition: InboxItemDisposition::Released,
            }],
            continuation_note: None,
            now: OffsetDateTime::UNIX_EPOCH + time::Duration::seconds(1),
        },
    )
    .await
    .unwrap();

    assert_eq!(late.view().status, RunStatus::Failed);
    assert_eq!(
        late.view().error_code,
        Some(RunErrorCode::ComputerRestarted)
    );
    assert_eq!(
        port.state.items[&item_id].view().status,
        InboxItemStatus::Pending
    );
    assert_eq!(port.state.items[&item_id].view().retry_count, 1);
    assert_eq!(port.state.completed_run_events.len(), 1);
}

#[tokio::test]
async fn failed_run_without_reported_disposition_releases_item_and_counts_retry() {
    let run_id = run(1635);
    let item_id = item(1636);
    let agent_id = member(1637);
    let focus = thread(1638);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent_id]);
    port.state
        .computer_assignments
        .insert((computer(999), agent_id));
    port.state.runs.insert(
        run_id,
        running_run(run_id, agent_id, focus, None, vec![item_id]),
    );
    let mut assigned = inbox(item_id, agent_id, focus, None, InboxItemStatus::Assigned);
    update_test_item(&mut assigned, |snapshot| {
        snapshot.assigned_run_id = Some(run_id);
    });
    port.state.items.insert(item_id, assigned);

    CompleteRun::execute(
        &mut port,
        CompleteRunInput {
            max_retry_count: 5,
            event_id: event(1639),
            run_id,
            computer_id: computer(999),
            outcome: RunOutcome::Failed,
            error_code: Some(RunErrorCode::DriverError),
            item_dispositions: Vec::new(),
            continuation_note: None,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .unwrap();

    let item = port.state.items[&item_id].view();
    assert_eq!(item.status, InboxItemStatus::Pending);
    assert_eq!(item.retry_count, 1);
    assert_eq!(item.assigned_run_id, None);
    assert_eq!(port.state.runs[&run_id].view().status, RunStatus::Failed);
    assert_eq!(
        port.state.runs[&run_id].view().error_code,
        Some(RunErrorCode::DriverError)
    );
}

#[tokio::test]
async fn failed_run_counts_an_automatic_release_as_a_retry() {
    let run_id = run(1640);
    let item_id = item(1641);
    let agent_id = member(1642);
    let focus = thread(1643);
    let mut port = MemoryPort::default();
    insert_thread(&mut port, focus, &[agent_id]);
    port.state
        .computer_assignments
        .insert((computer(999), agent_id));
    port.state.runs.insert(
        run_id,
        running_run(run_id, agent_id, focus, None, vec![item_id]),
    );
    let mut assigned = inbox(item_id, agent_id, focus, None, InboxItemStatus::Assigned);
    update_test_item(&mut assigned, |snapshot| {
        snapshot.assigned_run_id = Some(run_id);
    });
    port.state.items.insert(item_id, assigned);

    CompleteRun::execute(
        &mut port,
        CompleteRunInput {
            max_retry_count: 5,
            event_id: event(1644),
            run_id,
            computer_id: computer(999),
            outcome: RunOutcome::Failed,
            error_code: Some(RunErrorCode::DriverError),
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

    let item = port.state.items[&item_id].view();
    assert_eq!(item.status, InboxItemStatus::Pending);
    assert_eq!(item.retry_count, 1);
}

fn insert_thread(port: &mut MemoryPort, id: ThreadId, members: &[MemberId]) {
    let root_id = MessageId::from_uuid(id.into_uuid());
    let audience = members.iter().copied().collect::<BTreeSet<_>>();
    port.state.threads.insert(
        id,
        Thread {
            id,
            space_id: space(1),
            channel_id: channel(id.into_uuid().as_u128()),
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
    Task::rehydrate(TaskSnapshot {
        id,
        seq: 1,
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
    })
    .expect("test Task state is valid")
}

fn running_run(
    id: RunId,
    agent: MemberId,
    focus: ThreadId,
    task_id: Option<TaskId>,
    items: Vec<InboxItemId>,
) -> Run {
    Run::rehydrate(RunSnapshot {
        id,
        space_id: space(1),
        agent_id: agent,
        task_id,
        focus_thread_id: focus,
        status: RunStatus::Working,
        trigger: RunTrigger::Mention,
        cancel_requested: false,
        items: items
            .into_iter()
            .enumerate()
            .map(|(index, item_id)| RunItemSnapshot {
                inbox_item_id: item_id,
                delivery_sequence: index as u64 + 1,
                disposition: None,
            })
            .collect(),
        outcome: None,
        error_code: None,
        continuation_note: None,
        started_at: Some(OffsetDateTime::UNIX_EPOCH),
        finished_at: None,
    })
    .expect("test Run state is valid")
}

fn inbox_view(item: &InboxItem) -> InboxItemView {
    let item = item.view();
    InboxItemView {
        id: item.id,
        space_id: item.space_id,
        member_id: item.member_id,
        kind: item.kind,
        strength: item.strength,
        status: item.status,
        channel_id: None,
        channel_slug: None,
        thread_id: Some(item.thread_id),
        message_id: item.message_id,
        sender_member_id: None,
        sender_display_name: None,
        message_preview: None,
        activity_events: Vec::new(),
        available_at: item.available_at,
        created_at: item.available_at,
        retry_count: item.retry_count,
        requeue_count: item.requeue_count,
    }
}

fn inbox(
    id: InboxItemId,
    agent: MemberId,
    focus: ThreadId,
    task_id: Option<TaskId>,
    status: InboxItemStatus,
) -> InboxItem {
    let mut item = InboxItem::open_hard(
        id,
        space(1),
        agent,
        None,
        focus,
        task_id,
        InboxItemKind::Mention,
        OffsetDateTime::UNIX_EPOCH,
    )
    .expect("mention is hard");
    if status == InboxItemStatus::Assigned {
        item.assign_to_run(run(if id.into_uuid().as_u128() == 3 { 4 } else { 53 }))
            .expect("test Item can be assigned");
    } else if status != InboxItemStatus::Pending {
        let mut snapshot = item.snapshot();
        snapshot.status = status;
        snapshot.handled_at =
            (status == InboxItemStatus::Handled).then_some(OffsetDateTime::UNIX_EPOCH);
        item = InboxItem::rehydrate(snapshot).expect("test Item state is valid");
    }
    item
}

fn update_test_run(run: &mut Run, update: impl FnOnce(&mut RunSnapshot)) {
    let mut snapshot = run.snapshot();
    update(&mut snapshot);
    *run = Run::rehydrate(snapshot).expect("updated test Run state is valid");
}

fn update_test_item(item: &mut InboxItem, update: impl FnOnce(&mut InboxItemSnapshot)) {
    let mut snapshot = item.snapshot();
    update(&mut snapshot);
    *item = InboxItem::rehydrate(snapshot).expect("updated test Item state is valid");
}

fn update_test_task(task: &mut Task, update: impl FnOnce(&mut TaskSnapshot)) {
    let mut snapshot = task.snapshot();
    update(&mut snapshot);
    *task = Task::rehydrate(snapshot).expect("updated test Task state is valid");
}

fn dispatch_input(
    run_id: RunId,
    agent: MemberId,
    task_id: Option<TaskId>,
    focus: ThreadId,
) -> DispatchRunInput {
    DispatchRunInput {
        run_id,
        agent_id: agent,
        task_id,
        focus_thread_id: focus,
        trigger: RunTrigger::Mention,
        item_ids: Vec::new(),
    }
}

fn complete_run_input(run_id: RunId, item_id: InboxItemId) -> CompleteRunInput {
    CompleteRunInput {
        max_retry_count: 5,
        event_id: event(80),
        run_id,
        computer_id: computer(999),
        outcome: RunOutcome::Completed,
        error_code: None,
        item_dispositions: vec![ItemDispositionInput {
            item_id,
            disposition: InboxItemDisposition::Handled,
        }],
        continuation_note: None,
        now: OffsetDateTime::UNIX_EPOCH,
    }
}

struct StubPasswords;

impl PasswordPort for StubPasswords {
    fn hash(&self, password: &str) -> Result<String, ApplicationError> {
        Ok(format!("hashed:{password}"))
    }

    fn verify(&self, password: &str, stored_hash: &str) -> bool {
        stored_hash == format!("hashed:{password}")
    }
}

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

    let authenticated = AuthenticateSession::execute(&mut port, &session.token, now)
        .await
        .expect("session resolves");
    assert_eq!(authenticated, session.human);

    let duplicate = RegisterHuman::execute(
        &mut port,
        &passwords,
        &tokens,
        RegisterHumanInput {
            user_id: Uuid::from_u128(4003),
            session_id: Uuid::from_u128(4004),
            display_name: "Casey_Again",
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
        Some(ApplicationError::Domain(DomainError::InvalidCredential))
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

    assert_eq!(
        AuthorizeAgentGovernance::execute(&mut port, &plain_token, agent_id, now)
            .await
            .err(),
        Some(ApplicationError::Domain(DomainError::GovernorRequired))
    );
    let governed = AuthorizeAgentGovernance::execute(&mut port, &admin_token, agent_id, now)
        .await
        .expect("admin governs the agent");
    assert_eq!(governed.member_id, admin_id);

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

#[derive(Default)]
struct MemoryObjects {
    objects: std::sync::Mutex<HashMap<String, Vec<u8>>>,
    puts: std::sync::atomic::AtomicUsize,
}

#[async_trait::async_trait]
impl AttachmentObjectPort for MemoryObjects {
    async fn put(
        &self,
        object_key: &str,
        content: Vec<u8>,
    ) -> Result<StoredObject, ApplicationError> {
        use sha2::Digest;
        self.puts.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
        let stored = StoredObject {
            length: content.len() as u64,
            sha256: sha2::Sha256::digest(&content).into(),
        };
        self.objects
            .lock()
            .expect("object lock")
            .insert(object_key.to_owned(), content);
        Ok(stored)
    }

    async fn get(&self, object_key: &str) -> Result<Vec<u8>, ApplicationError> {
        self.objects
            .lock()
            .expect("object lock")
            .get(object_key)
            .cloned()
            .ok_or(ApplicationError::NotFound)
    }
}

fn declared(content: &[u8]) -> DeclaredContent {
    use sha2::Digest;
    DeclaredContent {
        size: content.len() as u64,
        sha256_hex: hex::encode(sha2::Sha256::digest(content)),
    }
}

async fn open_test_upload(
    port: &mut MemoryPort,
    attachment_id: AttachmentId,
    space_id: SpaceId,
    uploader: MemberId,
    key: IdempotencyKey,
) -> Attachment {
    OpenUpload::execute(
        port,
        OpenUploadInput {
            attachment_id,
            space_id,
            uploader_member_id: uploader,
            name: "report.pdf",
            media_type: "application/pdf",
            idempotency_key: key,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .expect("upload opens")
    .attachment
}

#[tokio::test]
async fn an_upload_completes_once_and_replays_without_a_second_ready_event() {
    let mut port = MemoryPort::default();
    let objects = MemoryObjects::default();
    let attachment_id = AttachmentId::from_uuid(Uuid::from_u128(6001));
    let space_id = space(6002);
    let uploader = member(6003);
    open_test_upload(
        &mut port,
        attachment_id,
        space_id,
        uploader,
        idempotency(6004),
    )
    .await;

    let content = b"report bytes".to_vec();
    WriteUploadContent::execute(
        &mut port,
        &objects,
        WriteUploadContentInput {
            attachment_id,
            uploader_member_id: uploader,
            content: content.clone(),
            max_bytes: 1024,
        },
    )
    .await
    .expect("content is written");

    let complete_key = idempotency(6005);
    let completed = CompleteAttachmentUpload::execute(
        &mut port,
        &objects,
        CompleteAttachmentUploadInput {
            attachment_id,
            uploader_member_id: uploader,
            declared: declared(&content),
            idempotency_key: complete_key,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .expect("upload completes");
    assert_eq!(completed.view().status, AttachmentStatus::Ready);
    assert_eq!(completed.view().length, Some(content.len() as u64));

    let replayed = CompleteAttachmentUpload::execute(
        &mut port,
        &objects,
        CompleteAttachmentUploadInput {
            attachment_id,
            uploader_member_id: uploader,
            declared: declared(&content),
            idempotency_key: complete_key,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .expect("replay succeeds");
    assert_eq!(replayed, completed);
    assert_eq!(
        port.state
            .attachment_writes
            .iter()
            .filter(|(_, _, kind)| kind == "attachment.ready")
            .count(),
        1
    );
}

#[tokio::test]
async fn opening_an_upload_twice_with_the_same_key_returns_the_first_attachment() {
    let mut port = MemoryPort::default();
    let space_id = space(6010);
    let uploader = member(6011);
    let key = idempotency(6012);
    let first = open_test_upload(
        &mut port,
        AttachmentId::from_uuid(Uuid::from_u128(6013)),
        space_id,
        uploader,
        key,
    )
    .await;
    let replayed = OpenUpload::execute(
        &mut port,
        OpenUploadInput {
            attachment_id: AttachmentId::from_uuid(Uuid::from_u128(6019)),
            space_id,
            uploader_member_id: uploader,
            name: "report.pdf",
            media_type: "application/pdf",
            idempotency_key: key,
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .expect("replay succeeds");
    assert!(!replayed.created);
    assert_eq!(replayed.attachment, first);
    assert_eq!(port.state.attachments.len(), 1);
}

#[tokio::test]
async fn a_mismatched_declaration_keeps_the_upload_open_and_writes_no_content_twice() {
    let mut port = MemoryPort::default();
    let objects = MemoryObjects::default();
    let attachment_id = AttachmentId::from_uuid(Uuid::from_u128(6020));
    let uploader = member(6021);
    open_test_upload(
        &mut port,
        attachment_id,
        space(6022),
        uploader,
        idempotency(6023),
    )
    .await;
    let content = b"report bytes".to_vec();
    WriteUploadContent::execute(
        &mut port,
        &objects,
        WriteUploadContentInput {
            attachment_id,
            uploader_member_id: uploader,
            content: content.clone(),
            max_bytes: 1024,
        },
    )
    .await
    .expect("content is written");

    let mut wrong = declared(&content);
    wrong.size += 1;
    assert_eq!(
        CompleteAttachmentUpload::execute(
            &mut port,
            &objects,
            CompleteAttachmentUploadInput {
                attachment_id,
                uploader_member_id: uploader,
                declared: wrong,
                idempotency_key: idempotency(6024),
                now: OffsetDateTime::UNIX_EPOCH,
            },
        )
        .await
        .err(),
        Some(ApplicationError::Domain(
            DomainError::AttachmentContentMismatch
        ))
    );
    assert_eq!(
        port.state.attachments[&attachment_id].view().status,
        AttachmentStatus::Uploading
    );

    WriteUploadContent::execute(
        &mut port,
        &objects,
        WriteUploadContentInput {
            attachment_id,
            uploader_member_id: uploader,
            content: content.clone(),
            max_bytes: 1024,
        },
    )
    .await
    .expect("content is rewritten");
    assert_eq!(objects.puts.load(std::sync::atomic::Ordering::Relaxed), 2);
    CompleteAttachmentUpload::execute(
        &mut port,
        &objects,
        CompleteAttachmentUploadInput {
            attachment_id,
            uploader_member_id: uploader,
            declared: declared(&content),
            idempotency_key: idempotency(6025),
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .expect("upload completes");
}

#[tokio::test]
async fn only_the_uploader_writes_content_and_an_oversized_body_is_rejected() {
    let mut port = MemoryPort::default();
    let objects = MemoryObjects::default();
    let attachment_id = AttachmentId::from_uuid(Uuid::from_u128(6030));
    let uploader = member(6031);
    open_test_upload(
        &mut port,
        attachment_id,
        space(6032),
        uploader,
        idempotency(6033),
    )
    .await;

    assert_eq!(
        WriteUploadContent::execute(
            &mut port,
            &objects,
            WriteUploadContentInput {
                attachment_id,
                uploader_member_id: member(6039),
                content: b"other".to_vec(),
                max_bytes: 1024,
            },
        )
        .await
        .err(),
        Some(ApplicationError::Domain(DomainError::AttachmentNotOwned))
    );
    assert_eq!(
        WriteUploadContent::execute(
            &mut port,
            &objects,
            WriteUploadContentInput {
                attachment_id,
                uploader_member_id: uploader,
                content: vec![0; 1025],
                max_bytes: 1024,
            },
        )
        .await
        .err(),
        Some(ApplicationError::PayloadTooLarge)
    );
    assert_eq!(objects.puts.load(std::sync::atomic::Ordering::Relaxed), 0);
}

#[tokio::test]
async fn downloads_require_ready_content_and_a_linked_message_for_non_uploaders() {
    let mut port = MemoryPort::default();
    let objects = MemoryObjects::default();
    let attachment_id = AttachmentId::from_uuid(Uuid::from_u128(6040));
    let uploader = member(6041);
    let viewer = member(6042);
    open_test_upload(
        &mut port,
        attachment_id,
        space(6043),
        uploader,
        idempotency(6044),
    )
    .await;

    assert_eq!(
        ReadAttachment::for_member(&mut port, &objects, attachment_id, viewer)
            .await
            .err(),
        Some(ApplicationError::Domain(DomainError::AttachmentNotReady))
    );

    let content = b"report bytes".to_vec();
    WriteUploadContent::execute(
        &mut port,
        &objects,
        WriteUploadContentInput {
            attachment_id,
            uploader_member_id: uploader,
            content: content.clone(),
            max_bytes: 1024,
        },
    )
    .await
    .expect("content is written");
    CompleteAttachmentUpload::execute(
        &mut port,
        &objects,
        CompleteAttachmentUploadInput {
            attachment_id,
            uploader_member_id: uploader,
            declared: declared(&content),
            idempotency_key: idempotency(6045),
            now: OffsetDateTime::UNIX_EPOCH,
        },
    )
    .await
    .expect("upload completes");

    assert_eq!(
        ReadAttachment::for_member(&mut port, &objects, attachment_id, viewer)
            .await
            .err(),
        Some(ApplicationError::PermissionDenied)
    );
    assert_eq!(
        ReadAttachment::for_uploader_or_member(&mut port, &objects, attachment_id, uploader)
            .await
            .expect("uploader reads its own attachment")
            .content,
        content
    );
    assert_eq!(
        ReadAttachment::for_member(&mut port, &objects, attachment_id, uploader)
            .await
            .err(),
        Some(ApplicationError::PermissionDenied)
    );

    port.state
        .visible_attachments
        .insert((attachment_id, viewer));
    assert_eq!(
        ReadAttachment::for_member(&mut port, &objects, attachment_id, viewer)
            .await
            .expect("linked message grants access")
            .content,
        content
    );
}

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
        Some(ApplicationError::Domain(DomainError::PairingLapsed))
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
        Some(ApplicationError::Domain(DomainError::PairingLapsed))
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
        os: ComputerOs::Linux,
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

fn space_member_fixture(
    port: &mut MemoryPort,
    id: MemberId,
    space_id: SpaceId,
    access_level: AccessLevel,
) {
    port.state.members.insert(
        id,
        Member {
            id,
            space_id,
            display_name: "Member".into(),
            access_level,
            created_at: OffsetDateTime::UNIX_EPOCH,
        },
    );
}

#[tokio::test]
async fn a_member_reads_only_their_own_inbox_unless_the_target_is_an_agent() {
    let mut port = MemoryPort::default();
    let space_id = space(1);
    let owner = member(7001);
    let other_human = member(7002);
    let agent_id = member(7003);
    space_member_fixture(&mut port, owner, space_id, AccessLevel::Owner);
    space_member_fixture(&mut port, other_human, space_id, AccessLevel::Member);
    space_member_fixture(&mut port, agent_id, space_id, AccessLevel::Member);
    port.state.agents.insert(
        agent_id,
        Agent {
            member_id: agent_id,
            space_id,
            computer_id: Some(computer(7004)),
            role_text: "assist".into(),
            role_revision: 1,
            lifecycle: AgentLifecycle::Active,
            driver_kind: DriverKind::Codex,
            retired_at: None,
        },
    );
    for (index, holder) in [owner, other_human, agent_id].into_iter().enumerate() {
        let item_id = item(7100 + index as u128);
        port.state.items.insert(
            item_id,
            inbox(
                item_id,
                holder,
                thread(7200 + index as u128),
                None,
                InboxItemStatus::Pending,
            ),
        );
    }

    let own = ReadMemberInbox::execute(&mut port, owner, owner, space_id, InboxScope::Queue)
        .await
        .expect("a Member reads their own Inbox");
    assert_eq!(own.len(), 1);
    assert_eq!(own[0].member_id, owner);

    let agent_inbox =
        ReadMemberInbox::execute(&mut port, owner, agent_id, space_id, InboxScope::Queue)
            .await
            .expect("a governor reads an Agent Inbox");
    assert_eq!(agent_inbox.len(), 1);

    assert_eq!(
        ReadMemberInbox::execute(&mut port, owner, other_human, space_id, InboxScope::Queue)
            .await
            .err(),
        Some(ApplicationError::PermissionDenied)
    );

    assert_eq!(
        ReadMemberInbox::execute(
            &mut port,
            other_human,
            agent_id,
            space_id,
            InboxScope::Queue
        )
        .await
        .err(),
        Some(ApplicationError::PermissionDenied)
    );

    let outsider = member(7005);
    space_member_fixture(&mut port, outsider, space(2), AccessLevel::Owner);
    assert_eq!(
        ReadMemberInbox::execute(&mut port, owner, outsider, space_id, InboxScope::Queue)
            .await
            .err(),
        Some(ApplicationError::NotFound)
    );
}

#[tokio::test]
async fn only_a_governor_requeues_a_dead_item_and_the_queue_hides_it_until_then() {
    let mut port = MemoryPort::default();
    let now = OffsetDateTime::UNIX_EPOCH;
    let space_id = space(1);
    let owner = member(7401);
    let plain = member(7402);
    let agent_id = member(7403);
    space_member_fixture(&mut port, owner, space_id, AccessLevel::Owner);
    space_member_fixture(&mut port, plain, space_id, AccessLevel::Member);
    space_member_fixture(&mut port, agent_id, space_id, AccessLevel::Member);
    port.state.agents.insert(
        agent_id,
        Agent {
            member_id: agent_id,
            space_id,
            computer_id: Some(computer(7406)),
            role_text: "assist".into(),
            role_revision: 1,
            lifecycle: AgentLifecycle::Active,
            driver_kind: DriverKind::Codex,
            retired_at: None,
        },
    );

    let item_id = item(7404);
    let mut dead = inbox(item_id, agent_id, thread(7405), None, InboxItemStatus::Dead);
    update_test_item(&mut dead, |snapshot| snapshot.retry_count = 5);
    port.state.items.insert(item_id, dead);

    // A retired Item is history, so the queue omits it while a governance read finds it.
    assert!(
        ReadMemberInbox::execute(&mut port, owner, agent_id, space_id, InboxScope::Queue)
            .await
            .expect("a governor reads the Agent queue")
            .is_empty()
    );
    assert_eq!(
        ReadMemberInbox::execute(&mut port, owner, agent_id, space_id, InboxScope::Dead)
            .await
            .expect("a governor reads retired Items")
            .len(),
        1
    );

    // Governing the Space is required; ordinary membership is not enough.
    assert_eq!(
        RequeueDeadItem::execute(
            &mut port,
            RequeueDeadItemInput {
                item_id,
                actor_id: plain,
                now,
            },
        )
        .await
        .err(),
        Some(ApplicationError::PermissionDenied)
    );
    assert_eq!(
        port.state.items[&item_id].view().status,
        InboxItemStatus::Dead
    );
    assert!(port.state.inbox_item_audits.is_empty());

    let view = RequeueDeadItem::execute(
        &mut port,
        RequeueDeadItemInput {
            item_id,
            actor_id: owner,
            now,
        },
    )
    .await
    .expect("a governor returns the Item to the queue");
    assert_eq!(view.status, InboxItemStatus::Pending);
    assert_eq!((view.retry_count, view.requeue_count), (0, 1));
    assert_eq!(
        port.state.inbox_item_audits,
        vec![(owner, "inbox_item.requeued".to_owned(), item_id)]
    );
    assert!(port.state.effects.contains(&Effect::InboxChanged(agent_id)));

    // The Item is back in the queue, so requeueing it again is not a valid transition.
    assert!(matches!(
        RequeueDeadItem::execute(
            &mut port,
            RequeueDeadItemInput {
                item_id,
                actor_id: owner,
                now,
            },
        )
        .await,
        Err(ApplicationError::Domain(_))
    ));
    assert_eq!(port.state.items[&item_id].view().requeue_count, 1);
}

#[tokio::test]
async fn opening_a_direct_message_twice_reuses_one_channel() {
    let mut port = MemoryPort::default();
    let now = OffsetDateTime::UNIX_EPOCH;
    let space_id = space(1);
    let actor = member(7301);
    let other = member(7302);
    space_member_fixture(&mut port, actor, space_id, AccessLevel::Member);
    space_member_fixture(&mut port, other, space_id, AccessLevel::Member);

    let opened = OpenDirectMessage::execute(
        &mut port,
        OpenDirectMessageInput {
            channel_id: channel(7303),
            space_id,
            actor_member_id: actor,
            other_member_id: other,
            now,
        },
    )
    .await
    .expect("opening a DM succeeds");
    assert!(opened.created);
    assert_eq!(opened.view.other_member.id, other);
    port.state.direct_messages.push(opened.view.clone());

    let reopened = OpenDirectMessage::execute(
        &mut port,
        OpenDirectMessageInput {
            channel_id: channel(7399),
            space_id,
            actor_member_id: other,
            other_member_id: actor,
            now,
        },
    )
    .await
    .expect("reopening returns the existing DM");
    assert!(!reopened.created);
    assert_eq!(reopened.view.channel_id, channel(7303));
    assert_eq!(port.state.channels.len(), 1);

    assert_eq!(
        OpenDirectMessage::execute(
            &mut port,
            OpenDirectMessageInput {
                channel_id: channel(7398),
                space_id,
                actor_member_id: actor,
                other_member_id: actor,
                now,
            },
        )
        .await
        .err(),
        Some(ApplicationError::Conflict)
    );

    let outsider = member(7304);
    space_member_fixture(&mut port, outsider, space(2), AccessLevel::Member);
    assert_eq!(
        OpenDirectMessage::execute(
            &mut port,
            OpenDirectMessageInput {
                channel_id: channel(7397),
                space_id,
                actor_member_id: actor,
                other_member_id: outsider,
                now,
            },
        )
        .await
        .err(),
        Some(ApplicationError::NotFound)
    );
}

struct StubInvitationTokens;

impl InvitationTokenPort for StubInvitationTokens {
    fn generate(&self) -> RawInvitationToken {
        RawInvitationToken::new("invitation-token".into())
    }
}

const RECIPIENT: &str = "invitee@example.com";

async fn space_with_governor(port: &mut MemoryPort, space_id: SpaceId, actor_id: MemberId) {
    port.state
        .spaces
        .insert(space_id, ("Design".into(), "design".into()));
    port.state.members.insert(
        actor_id,
        Member {
            id: actor_id,
            space_id,
            display_name: "Owner".into(),
            access_level: AccessLevel::Owner,
            created_at: OffsetDateTime::UNIX_EPOCH,
        },
    );
}

#[tokio::test]
async fn creating_an_invitation_returns_the_token_once_and_replays_without_it() {
    let mut port = MemoryPort::default();
    let now = OffsetDateTime::UNIX_EPOCH;
    let space_id = space(6001);
    let actor_id = member(6002);
    let key = idempotency(6003);
    space_with_governor(&mut port, space_id, actor_id).await;

    let issued = InviteHuman::execute(
        &mut port,
        &StubInvitationTokens,
        InviteHumanInput {
            invitation_id: Uuid::from_u128(6004),
            space_id,
            actor_id,
            email: " Invitee@Example.COM ",
            idempotency_key: key,
            now,
        },
    )
    .await
    .expect("creating an invitation succeeds");
    assert_eq!(issued.view.email, RECIPIENT);
    assert_eq!(issued.view.space_name, "Design");
    assert_eq!(issued.view.space_slug, "design");
    assert_eq!(
        issued.token.as_ref().map(RawInvitationToken::expose),
        Some("invitation-token")
    );

    let replayed = InviteHuman::execute(
        &mut port,
        &StubInvitationTokens,
        InviteHumanInput {
            invitation_id: Uuid::from_u128(6099),
            space_id,
            actor_id,
            email: RECIPIENT,
            idempotency_key: key,
            now,
        },
    )
    .await
    .expect("replay succeeds");
    assert!(replayed.token.is_none());
    assert_eq!(port.state.invitations.len(), 1);
    assert!(port.state.idempotency_locks.contains(&(
        actor_id,
        "space.invitation.create".into(),
        key
    )));
}

#[tokio::test]
async fn only_the_named_recipient_accepts_and_becomes_a_member() {
    let mut port = MemoryPort::default();
    let now = OffsetDateTime::UNIX_EPOCH;
    let space_id = space(6101);
    let actor_id = member(6102);
    space_with_governor(&mut port, space_id, actor_id).await;
    InviteHuman::execute(
        &mut port,
        &StubInvitationTokens,
        InviteHumanInput {
            invitation_id: Uuid::from_u128(6103),
            space_id,
            actor_id,
            email: RECIPIENT,
            idempotency_key: idempotency(6104),
            now,
        },
    )
    .await
    .expect("creating an invitation succeeds");
    let token = StubInvitationTokens.generate();
    let user_id = Uuid::from_u128(6105);

    assert_eq!(
        AcceptInvitation::execute(
            &mut port,
            AcceptInvitationInput {
                token: &token,
                member_id: member(6106),
                user_id: Uuid::from_u128(6107),
                user_email: "other@example.com",
                display_name: "Other",
                now,
            },
        )
        .await
        .err(),
        Some(ApplicationError::Domain(
            DomainError::InvitationEmailMismatch
        ))
    );
    assert!(port.state.human_members.is_empty());

    let accepted = AcceptInvitation::execute(
        &mut port,
        AcceptInvitationInput {
            token: &token,
            member_id: member(6108),
            user_id,
            user_email: " Invitee@Example.COM ",
            display_name: "  Invitee  ",
            now,
        },
    )
    .await
    .expect("the named recipient accepts");
    assert_eq!(accepted.member_id, member(6108));
    assert_eq!(accepted.space_id, space_id);
    assert_eq!(accepted.display_name, "Invitee");

    let replayed = AcceptInvitation::execute(
        &mut port,
        AcceptInvitationInput {
            token: &token,
            member_id: member(6199),
            user_id,
            user_email: RECIPIENT,
            display_name: "Invitee",
            now,
        },
    )
    .await
    .expect("retrying the same acceptance succeeds");
    assert_eq!(replayed, accepted);
    assert_eq!(port.state.human_members.len(), 1);
}

#[tokio::test]
async fn a_lapsed_invitation_is_persisted_as_expired_and_cannot_be_accepted() {
    let mut port = MemoryPort::default();
    let now = OffsetDateTime::UNIX_EPOCH;
    let space_id = space(6201);
    let actor_id = member(6202);
    space_with_governor(&mut port, space_id, actor_id).await;
    let invitation_id = Uuid::from_u128(6203);
    InviteHuman::execute(
        &mut port,
        &StubInvitationTokens,
        InviteHumanInput {
            invitation_id,
            space_id,
            actor_id,
            email: RECIPIENT,
            idempotency_key: idempotency(6204),
            now,
        },
    )
    .await
    .expect("creating an invitation succeeds");
    let token = StubInvitationTokens.generate();
    let after = now + time::Duration::days(8);

    ReadInvitation::execute(&mut port, &token, after)
        .await
        .expect("reading a lapsed invitation still projects it");
    assert_eq!(
        port.state.invitations[&invitation_id].status,
        InvitationStatus::Expired
    );
    assert_eq!(
        AcceptInvitation::execute(
            &mut port,
            AcceptInvitationInput {
                token: &token,
                member_id: member(6205),
                user_id: Uuid::from_u128(6206),
                user_email: RECIPIENT,
                display_name: "Invitee",
                now: after,
            },
        )
        .await
        .err(),
        Some(ApplicationError::Domain(DomainError::InvitationLapsed))
    );
    assert!(port.state.human_members.is_empty());
    assert_eq!(
        ReadInvitation::execute(&mut port, &RawInvitationToken::new("unknown".into()), now)
            .await
            .err(),
        Some(ApplicationError::NotFound)
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
id_fn!(attachment, AttachmentId);
