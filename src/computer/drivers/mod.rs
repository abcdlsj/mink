mod builtin;
mod builtin_agent;
mod codex;
mod contract;
pub(in crate::computer) mod prompt;
mod runtime;

#[cfg(test)]
mod tests;

use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
};

use async_trait::async_trait;

use crate::{
    computer::{
        application::{
            ApplicationError,
            ports::{
                DriverCompletion, DriverPort, DriverTurnOutcome, OpenSessionRequest, OpenedSession,
                ProcessEvidence, SteerOutcome,
            },
        },
        core::{
            home::LocalAgent,
            session::{DriverKind, ProviderSession},
            supervisor::LocalRun,
        },
    },
    ids::{AgentId, RunId},
};

use self::{
    builtin_agent::BuiltinRuntimeClient,
    contract::{ProviderBackend, ProviderOpen},
    runtime::CodexRuntimeClient,
};

pub(in crate::computer) fn agent_home(computer_home: &Path, agent_id: AgentId) -> PathBuf {
    computer_home.join("agents").join(agent_id.to_string())
}

pub(in crate::computer) fn runtime(
    computer_home: &std::path::Path,
    config: &crate::config::ComputerConfig,
    driver_secret: [u8; 32],
) -> Result<impl DriverPort, ApplicationError> {
    Ok(DriverAdapter::new(
        codex::CodexAdapter::new(CodexRuntimeClient::new(
            computer_home.to_owned(),
            driver_secret,
        )),
        builtin::BuiltinAdapter::new(BuiltinRuntimeClient::new(
            computer_home.to_owned(),
            config,
            driver_secret,
        )?),
    ))
}

pub(in crate::computer) struct DriverAdapter<C, B> {
    codex: C,
    builtin: B,
    turns: BTreeMap<RunId, ActiveTurn>,
    completions: std::collections::VecDeque<DriverCompletion>,
}

struct ActiveTurn {
    agent_id: crate::ids::AgentId,
    driver: DriverKind,
    locator: String,
}

impl<C, B> DriverAdapter<C, B> {
    pub(in crate::computer) fn new(codex: C, builtin: B) -> Self {
        Self {
            codex,
            builtin,
            turns: BTreeMap::new(),
            completions: std::collections::VecDeque::new(),
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
        if self.turns.contains_key(&run.view().id) {
            return Err(ApplicationError::Conflict);
        }
        let kind = run
            .view()
            .session_fingerprint
            .ok_or(ApplicationError::Conflict)?
            .driver;
        self.backend_mut(kind)
            .start_turn(run.view().id, locator, run.view().input)
            .await?;
        self.turns.insert(
            run.view().id,
            ActiveTurn {
                agent_id: run.view().agent_id,
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
        let turn = self
            .turns
            .get(&run.view().id)
            .ok_or(ApplicationError::NotFound)?;
        let driver = turn.driver;
        let locator = turn.locator.clone();
        let item = &run
            .view()
            .deliveries
            .get(&sequence)
            .ok_or(ApplicationError::NotFound)?
            .item;
        self.backend_mut(driver).steer(&locator, item).await
    }

    async fn notice(&mut self, run: &LocalRun) -> Result<(), ApplicationError> {
        let turn = self
            .turns
            .get(&run.view().id)
            .ok_or(ApplicationError::NotFound)?;
        let driver = turn.driver;
        let locator = turn.locator.clone();
        self.backend_mut(driver).notice(&locator).await
    }

    async fn interrupt(&mut self, run: &LocalRun) -> Result<(), ApplicationError> {
        let Some(turn) = self.turns.remove(&run.view().id) else {
            return Ok(());
        };
        self.backend_mut(turn.driver).interrupt(&turn.locator).await
    }

    async fn restart_agent(
        &mut self,
        agent_id: crate::ids::AgentId,
    ) -> Result<(), ApplicationError> {
        self.turns.retain(|_, turn| turn.agent_id != agent_id);
        self.codex.restart_agent(agent_id).await?;
        self.builtin.restart_agent(agent_id).await
    }

    async fn close_session(&mut self, session: &ProviderSession) -> Result<(), ApplicationError> {
        self.backend_mut(session.view().fingerprint.driver)
            .close(session.view().locator)
            .await
    }

    async fn process_evidence(
        &mut self,
        run: &LocalRun,
    ) -> Result<ProcessEvidence, ApplicationError> {
        let driver = self.turns.get(&run.view().id).map_or_else(
            || {
                run.view()
                    .session_fingerprint
                    .map(|fingerprint| fingerprint.driver)
                    .ok_or(ApplicationError::Conflict)
            },
            |turn| Ok(turn.driver),
        )?;
        self.backend_mut(driver)
            .process_evidence(run.view().id)
            .await
    }

    async fn poll_completions(&mut self) -> Result<Vec<DriverCompletion>, ApplicationError> {
        let mut completed = self.codex.poll_completions().await?;
        completed.extend(self.builtin.poll_completions().await?);
        for completion in &completed {
            self.turns.remove(&completion.run_id);
        }
        self.completions.extend(completed);
        Ok(self.completions.drain(..).collect())
    }

    async fn wait_for_completion(
        &mut self,
        run_id: RunId,
        timeout: std::time::Duration,
    ) -> Result<Option<DriverTurnOutcome>, ApplicationError> {
        let deadline = tokio::time::Instant::now() + timeout;
        loop {
            if let Some(index) = self
                .completions
                .iter()
                .position(|completion| completion.run_id == run_id)
            {
                return Ok(self
                    .completions
                    .remove(index)
                    .map(|completion| completion.outcome));
            }
            let mut completed = self.codex.poll_completions().await?;
            completed.extend(self.builtin.poll_completions().await?);
            for completion in completed {
                self.turns.remove(&completion.run_id);
                if completion.run_id == run_id {
                    return Ok(Some(completion.outcome));
                }
                self.completions.push_back(completion);
            }
            let now = tokio::time::Instant::now();
            if now >= deadline {
                return Ok(None);
            }
            tokio::time::sleep_until((now + std::time::Duration::from_millis(25)).min(deadline))
                .await;
        }
    }
}
