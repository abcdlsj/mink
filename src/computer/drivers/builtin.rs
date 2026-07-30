use async_trait::async_trait;

use crate::computer::{
    application::{
        ApplicationError,
        ports::{DriverCompletion, ProcessEvidence, SteerOutcome},
    },
    core::{
        home::LocalAgent,
        input::{ClaimedItemInput, RunInput},
    },
};
use crate::ids::{AgentId, RunId};

use super::contract::{ProviderBackend, ProviderOpen, StructuredProviderClient};

pub(in crate::computer) struct BuiltinAdapter<C> {
    client: C,
}

impl<C> BuiltinAdapter<C> {
    pub(in crate::computer) fn new(client: C) -> Self {
        Self { client }
    }
}

#[async_trait(?Send)]
impl<C: StructuredProviderClient> ProviderBackend for BuiltinAdapter<C> {
    async fn validate(&mut self, agent: &LocalAgent) -> Result<(), ApplicationError> {
        self.client.validate(agent).await
    }

    async fn open(
        &mut self,
        agent_id: AgentId,
        run_token: &str,
    ) -> Result<ProviderOpen, ApplicationError> {
        self.client
            .create_session(agent_id, run_token)
            .await
            .map(ProviderOpen::Opened)
    }

    async fn resume(
        &mut self,
        agent_id: AgentId,
        locator: &str,
        run_token: &str,
    ) -> Result<ProviderOpen, ApplicationError> {
        if self
            .client
            .resume_session(agent_id, locator, run_token)
            .await?
        {
            Ok(ProviderOpen::Resumed(locator.to_owned()))
        } else {
            Ok(ProviderOpen::Lost)
        }
    }

    async fn start_turn(
        &mut self,
        run_id: RunId,
        locator: &str,
        input: &RunInput,
        run_token: &str,
    ) -> Result<(), ApplicationError> {
        self.client
            .start_turn(run_id, locator, input, run_token)
            .await
    }

    async fn steer(
        &mut self,
        locator: &str,
        item: &ClaimedItemInput,
    ) -> Result<SteerOutcome, ApplicationError> {
        self.client.steer(locator, item).await
    }

    async fn notice(&mut self, locator: &str) -> Result<(), ApplicationError> {
        self.client.notice(locator).await
    }

    async fn interrupt(&mut self, locator: &str) -> Result<(), ApplicationError> {
        self.client.interrupt(locator).await
    }

    async fn close(&mut self, locator: &str) -> Result<(), ApplicationError> {
        self.client.delete_session(locator).await
    }

    async fn process_evidence(
        &mut self,
        run_id: RunId,
    ) -> Result<ProcessEvidence, ApplicationError> {
        self.client.process_evidence(run_id).await
    }

    async fn poll_completions(&mut self) -> Result<Vec<DriverCompletion>, ApplicationError> {
        self.client.poll_completions().await
    }
}
