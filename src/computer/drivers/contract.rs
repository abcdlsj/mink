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

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::computer) enum ProviderOpen {
    Opened(String),
    Resumed(String),
    Lost,
}

#[async_trait(?Send)]
pub(in crate::computer) trait ProviderBackend {
    async fn validate(&mut self, agent: &LocalAgent) -> Result<(), ApplicationError>;
    async fn open(&mut self, agent_id: AgentId) -> Result<ProviderOpen, ApplicationError>;
    async fn resume(
        &mut self,
        agent_id: AgentId,
        locator: &str,
    ) -> Result<ProviderOpen, ApplicationError>;
    async fn start_turn(
        &mut self,
        run_id: RunId,
        locator: &str,
        input: &RunInput,
        run_token: &str,
    ) -> Result<(), ApplicationError>;
    async fn steer(
        &mut self,
        locator: &str,
        item: &ClaimedItemInput,
    ) -> Result<SteerOutcome, ApplicationError>;
    async fn notice(&mut self, locator: &str) -> Result<(), ApplicationError>;
    async fn interrupt(&mut self, locator: &str) -> Result<(), ApplicationError>;
    async fn close(&mut self, locator: &str) -> Result<(), ApplicationError>;
    async fn process_evidence(
        &mut self,
        run_id: RunId,
    ) -> Result<ProcessEvidence, ApplicationError>;
    async fn poll_completions(&mut self) -> Result<Vec<DriverCompletion>, ApplicationError>;
}

#[async_trait(?Send)]
pub(in crate::computer) trait StructuredProviderClient {
    async fn validate(&mut self, agent: &LocalAgent) -> Result<(), ApplicationError>;
    async fn create_session(&mut self, agent_id: AgentId) -> Result<String, ApplicationError>;
    async fn resume_session(
        &mut self,
        agent_id: AgentId,
        locator: &str,
    ) -> Result<bool, ApplicationError>;
    async fn start_turn(
        &mut self,
        run_id: RunId,
        locator: &str,
        input: &RunInput,
        run_token: &str,
    ) -> Result<(), ApplicationError>;
    async fn steer(
        &mut self,
        locator: &str,
        item: &ClaimedItemInput,
    ) -> Result<SteerOutcome, ApplicationError>;
    async fn notice(&mut self, locator: &str) -> Result<(), ApplicationError>;
    async fn interrupt(&mut self, locator: &str) -> Result<(), ApplicationError>;
    async fn delete_session(&mut self, locator: &str) -> Result<(), ApplicationError>;
    async fn process_evidence(
        &mut self,
        run_id: RunId,
    ) -> Result<ProcessEvidence, ApplicationError>;
    async fn poll_completions(&mut self) -> Result<Vec<DriverCompletion>, ApplicationError>;
}
