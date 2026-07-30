mod builtin;
mod builtin_runtime;
mod codex;
mod contract;
mod runtime;

#[cfg(test)]
mod tests;

use std::collections::BTreeMap;

use async_trait::async_trait;

use crate::{
    computer::{
        application::{
            ApplicationError,
            ports::{
                DriverCompletion, DriverPort, OpenSessionRequest, OpenedSession, ProcessEvidence,
                SteerOutcome,
            },
        },
        core::{
            home::LocalAgent,
            session::{DriverKind, ProviderSession},
            supervisor::LocalRun,
        },
    },
    ids::RunId,
};

use self::{
    builtin_runtime::BuiltinRuntimeClient,
    contract::{ProviderBackend, ProviderOpen},
    runtime::CodexRuntimeClient,
};

pub(in crate::computer) fn runtime(
    computer_home: &std::path::Path,
    config: &crate::config::ComputerConfig,
) -> Result<impl DriverPort, ApplicationError> {
    Ok(DriverAdapter::new(
        codex::CodexAdapter::new(CodexRuntimeClient::new(computer_home.to_owned())),
        builtin::BuiltinAdapter::new(BuiltinRuntimeClient::new(computer_home.to_owned(), config)?),
    ))
}

pub(in crate::computer) struct DriverAdapter<C, B> {
    codex: C,
    builtin: B,
    turns: BTreeMap<RunId, ActiveTurn>,
}

struct ActiveTurn {
    driver: DriverKind,
    locator: String,
}

impl<C, B> DriverAdapter<C, B> {
    pub(in crate::computer) fn new(codex: C, builtin: B) -> Self {
        Self {
            codex,
            builtin,
            turns: BTreeMap::new(),
        }
    }

    fn backend_mut(&mut self, kind: DriverKind) -> &mut dyn ProviderBackend
    where
        C: ProviderBackend,
        B: ProviderBackend,
    {
        match kind {
            DriverKind::Codex => &mut self.codex,
            DriverKind::Builtin => &mut self.builtin,
        }
    }
}

#[async_trait(?Send)]
impl<C: ProviderBackend, B: ProviderBackend> DriverPort for DriverAdapter<C, B> {
    async fn validate(&mut self, agent: &LocalAgent) -> Result<(), ApplicationError> {
        self.backend_mut(agent.driver).validate(agent).await
    }

    async fn open_or_resume(
        &mut self,
        request: OpenSessionRequest,
    ) -> Result<OpenedSession, ApplicationError> {
        let backend = self.backend_mut(request.fingerprint.driver);
        let opened = if let Some(locator) = request.resume_locator.as_deref() {
            backend.resume(request.agent_id, locator).await?
        } else {
            backend.open(request.agent_id).await?
        };
        match opened {
            ProviderOpen::Opened(locator) => Ok(OpenedSession {
                locator,
                resumed: false,
            }),
            ProviderOpen::Resumed(locator) => Ok(OpenedSession {
                locator,
                resumed: true,
            }),
            ProviderOpen::Lost => Err(ApplicationError::SessionLost),
        }
    }

    async fn start_turn(&mut self, run: &LocalRun, locator: &str) -> Result<(), ApplicationError> {
        if self.turns.contains_key(&run.id) {
            return Err(ApplicationError::Conflict);
        }
        let kind = run
            .session_fingerprint
            .as_ref()
            .ok_or(ApplicationError::Conflict)?
            .driver;
        self.backend_mut(kind)
            .start_turn(run.id, locator, &run.input, run.fencing_token.expose())
            .await?;
        self.turns.insert(
            run.id,
            ActiveTurn {
                driver: kind,
                locator: locator.to_owned(),
            },
        );
        Ok(())
    }

    async fn steer(
        &mut self,
        run: &LocalRun,
        sequence: u64,
    ) -> Result<SteerOutcome, ApplicationError> {
        let turn = self.turns.get(&run.id).ok_or(ApplicationError::NotFound)?;
        let driver = turn.driver;
        let locator = turn.locator.clone();
        let item = &run
            .deliveries
            .get(&sequence)
            .ok_or(ApplicationError::NotFound)?
            .item;
        self.backend_mut(driver).steer(&locator, item).await
    }

    async fn notice(&mut self, run: &LocalRun) -> Result<(), ApplicationError> {
        let turn = self.turns.get(&run.id).ok_or(ApplicationError::NotFound)?;
        let driver = turn.driver;
        let locator = turn.locator.clone();
        self.backend_mut(driver).notice(&locator).await
    }

    async fn interrupt(&mut self, run: &LocalRun) -> Result<(), ApplicationError> {
        let Some(turn) = self.turns.remove(&run.id) else {
            return Ok(());
        };
        self.backend_mut(turn.driver).interrupt(&turn.locator).await
    }

    async fn close_session(&mut self, session: &ProviderSession) -> Result<(), ApplicationError> {
        self.backend_mut(session.fingerprint.driver)
            .close(&session.locator)
            .await
    }

    async fn process_evidence(
        &mut self,
        run: &LocalRun,
    ) -> Result<ProcessEvidence, ApplicationError> {
        let driver = self.turns.get(&run.id).map_or_else(
            || {
                run.session_fingerprint
                    .as_ref()
                    .map(|fingerprint| fingerprint.driver)
                    .ok_or(ApplicationError::Conflict)
            },
            |turn| Ok(turn.driver),
        )?;
        self.backend_mut(driver).process_evidence(run.id).await
    }

    async fn poll_completions(&mut self) -> Result<Vec<DriverCompletion>, ApplicationError> {
        let mut completed = self.codex.poll_completions().await?;
        completed.extend(self.builtin.poll_completions().await?);
        for completion in &completed {
            self.turns.remove(&completion.run_id);
        }
        Ok(completed)
    }
}
