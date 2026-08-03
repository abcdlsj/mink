use super::*;

#[async_trait]
pub(in crate::server) trait ExecutionTransaction {
    async fn run(&mut self, id: RunId) -> Result<Run, ApplicationError>;
    async fn active_run_for_agent(
        &mut self,
        agent_id: MemberId,
    ) -> Result<Option<RunId>, ApplicationError>;
    async fn completed_run_for_event(
        &mut self,
        event_id: EventId,
    ) -> Result<Option<RunId>, ApplicationError>;
    async fn nonterminal_runs_for_computer(
        &mut self,
        computer_id: ComputerId,
    ) -> Result<Vec<RunId>, ApplicationError>;
    async fn save_run(&mut self, run: Run) -> Result<(), ApplicationError>;
    async fn dispatchable_work(
        &mut self,
        now: time::OffsetDateTime,
        limit: u32,
    ) -> Result<Vec<DispatchCandidate>, ApplicationError>;
    async fn record_dispatch_failure(
        &mut self,
        item_id: InboxItemId,
        message_id: Option<MessageId>,
        channel_id: ChannelId,
        error_code: &str,
    ) -> Result<bool, ApplicationError>;
    async fn authorize_run_capability(
        &mut self,
        proof: &RunCapabilityProof,
    ) -> Result<bool, ApplicationError>;
    async fn active_run_for_visible_agent(
        &mut self,
        agent_id: MemberId,
        viewer_id: MemberId,
    ) -> Result<Option<RunId>, ApplicationError>;
    async fn agent_provision_command_target(
        &mut self,
        computer_id: ComputerId,
        command_id: CommandId,
        sequence: u64,
    ) -> Result<Option<MemberId>, ApplicationError>;
}
