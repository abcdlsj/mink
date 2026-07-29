use crate::ids::{
    ChannelId, ComputerId, EventId, IdempotencyKey, InboxItemId, MemberId, MessageId, RunId,
    TaskId, ThreadId,
};

use crate::server::domain::{
    attention::InboxItem,
    conversation::{Channel, Message, Thread},
    execution::Run,
    identity::{Agent, Computer},
    task::Task,
};

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
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::server) enum Effect {
    TaskCreated(TaskId),
    RunTaskBound {
        run_id: RunId,
        task_id: TaskId,
    },
    ThreadLinked {
        task_id: TaskId,
        thread_id: ThreadId,
    },
    ItemAttached {
        run_id: RunId,
        item_id: InboxItemId,
        sequence: u64,
    },
    RunClaimed(RunId),
    RunCompleted(RunId),
    TaskCompleted {
        task_id: TaskId,
        result_message_id: MessageId,
    },
    SessionClose(TaskId),
    AgentRetired(MemberId),
    ComputerDeleted(ComputerId),
    TaskUpdated(TaskId),
    ChannelCreated(ChannelId),
}

pub(in crate::server) trait ServerTransaction {
    fn thread(&mut self, id: ThreadId) -> Result<Thread, ApplicationError>;
    fn root_message(&mut self, thread_id: ThreadId) -> Result<Message, ApplicationError>;
    fn task(&mut self, id: TaskId) -> Result<Task, ApplicationError>;
    fn run(&mut self, id: RunId) -> Result<Run, ApplicationError>;
    fn inbox_item(&mut self, id: InboxItemId) -> Result<InboxItem, ApplicationError>;
    fn agent(&mut self, id: MemberId) -> Result<Agent, ApplicationError>;
    fn computer(&mut self, id: ComputerId) -> Result<Computer, ApplicationError>;

    fn task_for_source(&mut self, thread_id: ThreadId) -> Option<TaskId>;
    fn unfinished_task_for_thread(&mut self, thread_id: ThreadId) -> Option<TaskId>;
    fn task_for_idempotency(&mut self, actor: MemberId, key: IdempotencyKey) -> Option<TaskId>;
    fn active_run_for_agent(&mut self, agent_id: MemberId) -> Option<RunId>;
    fn computer_has_assigned_agents(&mut self, computer_id: ComputerId) -> bool;
    fn completed_run_for_event(&mut self, event_id: EventId) -> Option<RunId>;

    fn can_read_thread(&mut self, actor: MemberId, thread_id: ThreadId) -> bool;
    fn can_link_thread(&mut self, actor: MemberId, task: &Task, target: &Thread) -> bool;
    fn can_assign_agent(&mut self, agent: MemberId, source: &Thread) -> bool;
    fn can_govern_task(&mut self, actor: MemberId, task: &Task) -> bool;
    fn has_permission(
        &mut self,
        actor: MemberId,
        action: crate::server::domain::identity::PermissionAction,
    ) -> bool;

    fn insert_task(&mut self, task: Task) -> Result<(), ApplicationError>;
    fn save_task(&mut self, task: Task) -> Result<(), ApplicationError>;
    fn save_run(&mut self, run: Run) -> Result<(), ApplicationError>;
    fn save_inbox_item(&mut self, item: InboxItem) -> Result<(), ApplicationError>;
    fn insert_message(&mut self, message: Message) -> Result<(), ApplicationError>;
    fn insert_channel(&mut self, channel: Channel) -> Result<(), ApplicationError>;
    fn save_agent(&mut self, agent: Agent) -> Result<(), ApplicationError>;
    fn save_computer(&mut self, computer: Computer) -> Result<(), ApplicationError>;
    fn record_completed_run_event(
        &mut self,
        event_id: EventId,
        run_id: RunId,
    ) -> Result<(), ApplicationError>;
    fn record_task_idempotency(
        &mut self,
        actor: MemberId,
        key: IdempotencyKey,
        task_id: TaskId,
    ) -> Result<(), ApplicationError>;
    fn emit(&mut self, effect: Effect);
}

pub(in crate::server) trait TransactionPort {
    type Transaction<'a>: ServerTransaction
    where
        Self: 'a;

    fn transact<T>(
        &mut self,
        operation: impl FnOnce(&mut Self::Transaction<'_>) -> Result<T, ApplicationError>,
    ) -> Result<T, ApplicationError>;
}
