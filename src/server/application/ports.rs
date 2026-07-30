use crate::ids::{
    ChannelId, ComputerId, EventId, IdempotencyKey, InboxItemId, MemberId, MessageId, RunId,
    TaskId, ThreadId,
};

use crate::server::domain::{
    attention::InboxItem,
    conversation::{Channel, Message, Thread},
    execution::Run,
    identity::{Agent, Computer, Member},
    task::Task,
};

use async_trait::async_trait;
use sha2::{Digest, Sha256};
use std::fmt;

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
pub(in crate::server) enum ApplicationError {
    #[error(transparent)]
    Domain(#[from] crate::server::domain::DomainError),
    #[error("resource was not found")]
    NotFound,
    #[error("actor is not allowed to perform this action")]
    PermissionDenied,
    #[error("request conflicts with current state")]
    Conflict,
    #[error("run context changed")]
    ContextChanged,
    #[error("external dependency is unavailable")]
    Unavailable,
    #[error("server adapter failed")]
    Internal,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) struct StoredObject {
    pub(in crate::server) length: u64,
    pub(in crate::server) sha256: [u8; 32],
}

#[async_trait]
pub(in crate::server) trait AttachmentObjectPort: Send + Sync {
    async fn put(
        &self,
        object_key: &str,
        content: Vec<u8>,
    ) -> Result<StoredObject, ApplicationError>;
    async fn get(&self, object_key: &str) -> Result<Vec<u8>, ApplicationError>;
    async fn delete(&self, object_key: &str) -> Result<(), ApplicationError>;
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) enum Effect {
    MessageCreated(MessageId),
    TaskCreated(TaskId),
    RunTaskBound {
        run_id: RunId,
        task_id: TaskId,
    },
    ThreadLinked {
        task_id: TaskId,
        thread_id: ThreadId,
    },
    ThreadUnlinked {
        task_id: TaskId,
        thread_id: ThreadId,
    },
    ItemAttached {
        run_id: RunId,
        item_id: InboxItemId,
        sequence: u64,
    },
    RunNotice {
        run_id: RunId,
        item_id: InboxItemId,
        location_visible: bool,
    },
    RunClaimed {
        run_id: RunId,
        fencing_token: RawFencingToken,
    },
    RunStarted(RunId),
    RunCompleted(RunId),
    TaskCompleted {
        task_id: TaskId,
        result_message_id: MessageId,
    },
    TaskFinished(TaskId),
    SessionClose(TaskId),
    AgentRetired {
        agent_id: MemberId,
        computer_id: ComputerId,
    },
    ComputerDeleted(ComputerId),
    TaskUpdated(TaskId),
    ChannelCreated(ChannelId),
    AgentCreated {
        agent_id: MemberId,
        computer_id: ComputerId,
    },
}

#[derive(Clone, Eq, PartialEq)]
pub(in crate::server) struct RawFencingToken(String);

impl RawFencingToken {
    pub(in crate::server) fn new(value: String) -> Self {
        Self(value)
    }

    pub(in crate::server) fn expose(&self) -> &str {
        &self.0
    }

    pub(in crate::server) fn sha256_hash(&self) -> String {
        hex::encode(Sha256::digest(self.0.as_bytes()))
    }
}

impl fmt::Debug for RawFencingToken {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("RawFencingToken([REDACTED])")
    }
}

#[async_trait]
pub(in crate::server) trait ServerTransaction {
    async fn thread(&mut self, id: ThreadId) -> Result<Thread, ApplicationError>;
    async fn root_message(&mut self, thread_id: ThreadId) -> Result<Message, ApplicationError>;
    async fn task(&mut self, id: TaskId) -> Result<Task, ApplicationError>;
    async fn run(&mut self, id: RunId) -> Result<Run, ApplicationError>;
    async fn inbox_item(&mut self, id: InboxItemId) -> Result<InboxItem, ApplicationError>;
    async fn agent(&mut self, id: MemberId) -> Result<Agent, ApplicationError>;
    async fn computer(&mut self, id: ComputerId) -> Result<Computer, ApplicationError>;

    async fn task_for_source(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<TaskId>, ApplicationError>;
    async fn unfinished_task_for_thread(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<Option<TaskId>, ApplicationError>;
    async fn task_for_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<Option<TaskId>, ApplicationError>;
    async fn active_run_for_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<Option<RunId>, ApplicationError>;
    async fn computer_has_assigned_agents(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<bool, ApplicationError>;
    async fn completed_run_for_event(
        &mut self,
        event_id: EventId,
    ) -> Result<Option<RunId>, ApplicationError>;

    async fn can_read_thread(
        &mut self,
        actor: MemberId,
        thread_id: ThreadId,
    ) -> Result<bool, ApplicationError>;
    async fn can_link_thread(
        &mut self,
        actor: MemberId,
        task: &Task,
        target: &Thread,
    ) -> Result<bool, ApplicationError>;
    async fn can_assign_agent(
        &mut self,
        agent: MemberId,
        source: &Thread,
    ) -> Result<bool, ApplicationError>;
    async fn can_govern_task(
        &mut self,
        actor: MemberId,
        task: &Task,
    ) -> Result<bool, ApplicationError>;
    async fn has_permission(
        &mut self,
        actor: MemberId,
        action: crate::server::domain::identity::PermissionAction,
    ) -> Result<bool, ApplicationError>;
    async fn can_operate_agent(
        &mut self,
        computer_id: ComputerId,
        agent_id: MemberId,
    ) -> Result<bool, ApplicationError>;
    async fn thread_message_sequence(
        &mut self,
        thread_id: ThreadId,
    ) -> Result<u64, ApplicationError>;

    async fn insert_task(&mut self, task: Task) -> Result<(), ApplicationError>;
    async fn save_task(&mut self, task: Task) -> Result<(), ApplicationError>;
    async fn save_run(&mut self, run: Run) -> Result<(), ApplicationError>;
    async fn save_inbox_item(&mut self, item: InboxItem) -> Result<(), ApplicationError>;
    async fn insert_message(&mut self, message: Message) -> Result<(), ApplicationError>;
    async fn insert_channel(&mut self, channel: Channel) -> Result<(), ApplicationError>;
    async fn insert_agent(&mut self, member: Member, agent: Agent) -> Result<(), ApplicationError>;
    async fn save_agent(&mut self, agent: Agent) -> Result<(), ApplicationError>;
    async fn save_computer(&mut self, computer: Computer) -> Result<(), ApplicationError>;
    async fn record_completed_run_event(
        &mut self,
        event_id: EventId,
        run_id: RunId,
    ) -> Result<(), ApplicationError>;
    async fn record_task_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        task_id: TaskId,
    ) -> Result<(), ApplicationError>;
    async fn record_task_audit(
        &mut self,
        actor: MemberId,
        action: &str,
        task_id: TaskId,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    fn emit(&mut self, effect: Effect);
}

pub(in crate::server) trait TransactionPort {
    type Transaction: ServerTransaction;

    async fn transact<T>(
        &mut self,
        operation: impl for<'a> AsyncFnOnce(&'a mut Self::Transaction) -> Result<T, ApplicationError>,
    ) -> Result<T, ApplicationError>;
}
