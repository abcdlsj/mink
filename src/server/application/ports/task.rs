use super::*;

#[async_trait]
pub(in crate::server) trait TaskTransaction {
    async fn task(&mut self, id: TaskId) -> Result<Task, ApplicationError>;
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
    async fn insert_task(&mut self, task: Task) -> Result<(), ApplicationError>;
    async fn save_task(&mut self, task: Task) -> Result<(), ApplicationError>;
}
