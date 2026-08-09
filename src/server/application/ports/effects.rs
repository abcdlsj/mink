use super::*;

#[async_trait]
pub(in crate::server) trait EffectSink {
    async fn queue_agent_suspend(
        &mut self,
        agent_id: MemberId,
        computer_id: Option<ComputerId>,
        cancel_current_run: bool,
    ) -> Result<(), ApplicationError>;
    async fn queue_agent_resume(
        &mut self,
        agent_id: MemberId,
        computer_id: Option<ComputerId>,
    ) -> Result<(), ApplicationError>;
    async fn queue_agent_restart(
        &mut self,
        agent_id: MemberId,
        computer_id: Option<ComputerId>,
    ) -> Result<(), ApplicationError>;
    async fn queue_agent_configuration(&mut self, agent: &Agent) -> Result<(), ApplicationError>;
    async fn lock_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<(), ApplicationError>;
    async fn resource_for_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
    ) -> Result<Option<uuid::Uuid>, ApplicationError>;
    async fn insert_inbox_item(&mut self, item: InboxItem) -> Result<(), ApplicationError>;
    async fn save_inbox_item(&mut self, item: InboxItem) -> Result<(), ApplicationError>;
    async fn insert_dead_item_notice(
        &mut self,
        agent_id: MemberId,
        thread_id: ThreadId,
        error_code: &'static str,
        now: time::OffsetDateTime,
    ) -> Result<InboxItemId, ApplicationError>;
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
    async fn record_resource_idempotency(
        &mut self,
        actor: MemberId,
        action: &str,
        key: IdempotencyKey,
        resource_id: uuid::Uuid,
    ) -> Result<(), ApplicationError>;
    async fn record_task_audit(
        &mut self,
        actor: MemberId,
        action: &str,
        task_id: TaskId,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    async fn record_inbox_item_audit(
        &mut self,
        actor: MemberId,
        action: &str,
        item_id: InboxItemId,
        now: time::OffsetDateTime,
    ) -> Result<(), ApplicationError>;
    fn emit(&mut self, effect: Effect);
}
