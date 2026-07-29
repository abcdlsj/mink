use async_trait::async_trait;

use crate::computer::{
    application::{
        ApplicationError,
        ports::{ProcessEvidence, SteerOutcome},
    },
    core::{
        home::LocalAgent,
        input::{ClaimedItemInput, RunInput},
    },
};
use crate::ids::RunId;

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

    async fn open(&mut self) -> Result<ProviderOpen, ApplicationError> {
        self.client.create_session().await.map(ProviderOpen::Opened)
    }

    async fn resume(&mut self, locator: &str) -> Result<ProviderOpen, ApplicationError> {
        if self.client.resume_session(locator).await? {
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
    ) -> Result<(), ApplicationError> {
        self.client.start_turn(run_id, locator, input).await
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
}
