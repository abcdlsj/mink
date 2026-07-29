use time::OffsetDateTime;
use uuid::Uuid;

use crate::ids::{EventId, InboxItemId, RunId, TaskId};

use crate::computer::core::{
    input::{AttentionNoticeInput, ClaimedItemInput},
    session::{self, ProviderSession, SessionFingerprint, SessionScope, SessionState},
    supervisor::{DeliveryState, ItemDisposition, LocalRunState, TerminalStatus},
};

use super::{
    ApplicationError,
    ports::{
        ComputerTransaction, DriverPort, LocalErrorCode, LocalEvent, OpenSessionRequest,
        SteerOutcome, TransactionPort,
    },
};

pub(in crate::computer) struct RunService;

impl RunService {
    pub(in crate::computer) async fn start<P: TransactionPort, D: DriverPort>(
        store: &mut P,
        driver: &mut D,
        run_id: RunId,
        fingerprint: SessionFingerprint,
    ) -> Result<(), ApplicationError> {
        let (mut run, decision, scope) = store
            .transact(async |transaction| {
                let mut run = transaction.run(run_id)?.ok_or(ApplicationError::NotFound)?;
                if run.state == LocalRunState::Running {
                    return Err(ApplicationError::AlreadyApplied);
                }
                run.begin_start()?;
                let scope = run.task_id.map_or(
                    SessionScope::Thread(run.focus_thread_id),
                    SessionScope::Task,
                );
                let sessions = transaction.sessions(run.agent_id, scope)?;
                let decision = session::resolve(&sessions, run.agent_id, scope, &fingerprint);
                transaction.save_run(run.clone())?;
                Ok((run, decision, scope))
            })
            .await?;

        let (mut generation, resume_candidate) = match decision {
            session::ResolveDecision::Resume(existing) => (existing.generation, Some(existing)),
            session::ResolveDecision::Create { generation, close } => {
                if let Some(mut closing) = close {
                    closing.mark_closing();
                    let closed = driver.close_session(&closing).await.is_ok();
                    closing.close(closed, OffsetDateTime::now_utc());
                    store
                        .transact(async |transaction| transaction.save_session(closing))
                        .await?;
                }
                (generation, None)
            }
        };
        let request = OpenSessionRequest {
            agent_id: run.agent_id,
            scope,
            generation,
            fingerprint: fingerprint.clone(),
            resume_locator: resume_candidate
                .as_ref()
                .map(|session| session.locator.clone()),
        };
        let attempted_resume = resume_candidate.is_some();
        let session_created_at = resume_candidate
            .as_ref()
            .map_or_else(OffsetDateTime::now_utc, |session| session.created_at);
        let opened = match driver.open_or_resume(request).await {
            Ok(opened) if !attempted_resume || opened.resumed => opened,
            Ok(_) | Err(ApplicationError::SessionLost) if attempted_resume => {
                let mut lost = resume_candidate
                    .clone()
                    .expect("resume candidate was checked");
                lost.close(false, OffsetDateTime::now_utc());
                store
                    .transact(async |transaction| transaction.save_session(lost))
                    .await?;
                generation += 1;
                driver
                    .open_or_resume(OpenSessionRequest {
                        agent_id: run.agent_id,
                        scope,
                        generation,
                        fingerprint: fingerprint.clone(),
                        resume_locator: None,
                    })
                    .await?
            }
            Err(error) => return Err(error),
            Ok(_) => return Err(ApplicationError::SessionLost),
        };
        let mut provider_session = ProviderSession {
            agent_id: run.agent_id,
            scope,
            generation,
            locator: opened.locator,
            fingerprint,
            state: SessionState::Ready,
            created_at: session_created_at,
            last_resumed_at: None,
            closed_at: None,
        };
        provider_session.begin_use(OffsetDateTime::now_utc())?;
        store
            .transact(async |transaction| transaction.save_session(provider_session.clone()))
            .await?;
        driver.start_turn(&run, &provider_session.locator).await?;
        run.started(scope, generation)?;
        store
            .transact(async |transaction| {
                transaction.save_session(provider_session)?;
                transaction.save_run(run.clone())?;
                transaction.append_event(LocalEvent::RunStarted {
                    event_id: next_event_id(),
                    run_id: run.id,
                    fencing_token: run.fencing_token.clone(),
                })
            })
            .await
    }

    pub(in crate::computer) async fn attach<P: TransactionPort, D: DriverPort>(
        store: &mut P,
        driver: &mut D,
        run_id: RunId,
        sequence: u64,
        item: ClaimedItemInput,
    ) -> Result<DeliveryState, ApplicationError> {
        let (mut run, inserted) = store
            .transact(async |transaction| {
                let mut run = transaction.run(run_id)?.ok_or(ApplicationError::NotFound)?;
                let inserted = run.attach(sequence, item)?;
                transaction.save_run(run.clone())?;
                Ok((run, inserted))
            })
            .await?;
        if !inserted && run.deliveries[&sequence].state != DeliveryState::Pending {
            return Ok(run.deliveries[&sequence].state);
        }
        let outcome = match driver.steer(&run, sequence).await? {
            SteerOutcome::Accepted => DeliveryState::Accepted,
            SteerOutcome::TooLate => DeliveryState::TooLate,
            SteerOutcome::Unsupported => DeliveryState::Unsupported,
        };
        run.record_delivery(sequence, outcome)?;
        store
            .transact(async |transaction| {
                transaction.save_run(run.clone())?;
                transaction.append_event(LocalEvent::Delivery {
                    event_id: next_event_id(),
                    run_id,
                    sequence,
                    outcome,
                    fencing_token: run.fencing_token.clone(),
                })
            })
            .await?;
        Ok(outcome)
    }

    pub(in crate::computer) async fn notice<P: TransactionPort, D: DriverPort>(
        store: &mut P,
        driver: &mut D,
        run_id: RunId,
        notice: AttentionNoticeInput,
    ) -> Result<(), ApplicationError> {
        let notice_id = notice.notice_id;
        let (mut run, inserted) = store
            .transact(async |transaction| {
                let mut run = transaction.run(run_id)?.ok_or(ApplicationError::NotFound)?;
                let inserted = run.add_notice(notice)?;
                transaction.save_run(run.clone())?;
                Ok((run, inserted))
            })
            .await?;
        if inserted || run.notice_is_pending(notice_id) {
            driver.notice(&run).await?;
            run.record_notice(notice_id)?;
            store
                .transact(async |transaction| transaction.save_run(run))
                .await?;
        }
        Ok(())
    }

    pub(in crate::computer) async fn bind_task<P: TransactionPort>(
        store: &mut P,
        run_id: RunId,
        task_id: TaskId,
        fingerprint: SessionFingerprint,
    ) -> Result<(), ApplicationError> {
        store
            .transact(async |transaction| {
                let mut run = transaction.run(run_id)?.ok_or(ApplicationError::NotFound)?;
                if run.task_id == Some(task_id)
                    && run
                        .session
                        .is_some_and(|(scope, _)| scope == SessionScope::Task(task_id))
                {
                    return Ok(());
                }
                let old_scope = SessionScope::Thread(run.focus_thread_id);
                let mut sessions = transaction.sessions(run.agent_id, old_scope)?;
                let session = sessions
                    .iter_mut()
                    .max_by_key(|session| session.generation)
                    .ok_or(ApplicationError::NotFound)?;
                session.promote(run.focus_thread_id, task_id, &fingerprint)?;
                run.bind_task(task_id)?;
                run.session = Some((SessionScope::Task(task_id), session.generation));
                transaction.delete_session(run.agent_id, old_scope, session.generation)?;
                transaction.save_session(session.clone())?;
                transaction.save_run(run)
            })
            .await
    }

    pub(in crate::computer) async fn renew_lease<P: TransactionPort>(
        store: &mut P,
        run_id: RunId,
        lease_expires_at: OffsetDateTime,
    ) -> Result<(), ApplicationError> {
        store
            .transact(async |transaction| {
                let mut run = transaction.run(run_id)?.ok_or(ApplicationError::NotFound)?;
                run.renew_lease(lease_expires_at)?;
                transaction.save_run(run)
            })
            .await
    }

    pub(in crate::computer) async fn finish<P: TransactionPort>(
        store: &mut P,
        run_id: RunId,
        status: TerminalStatus,
        item_outcomes: Vec<(InboxItemId, ItemDisposition)>,
        continuation_note: Option<String>,
        error_code: Option<LocalErrorCode>,
    ) -> Result<EventId, ApplicationError> {
        store
            .transact(async |transaction| {
                let mut run = transaction.run(run_id)?.ok_or(ApplicationError::NotFound)?;
                if let Some(existing) =
                    transaction
                        .pending_events()?
                        .into_iter()
                        .find_map(|event| match event {
                            LocalEvent::RunResult {
                                event_id,
                                run_id: existing,
                                ..
                            } if existing == run_id => Some(event_id),
                            _ => None,
                        })
                {
                    return Ok(existing);
                }
                if status == TerminalStatus::Canceled {
                    if run.state != LocalRunState::Stopping {
                        run.request_stop()?;
                    }
                } else if run.state == LocalRunState::Running {
                    run.begin_finalizing()?;
                }
                run.validate_item_outcomes(&item_outcomes)?;
                run.finish(status)?;
                if let Some((scope, generation)) = run.session {
                    let mut sessions = transaction.sessions(run.agent_id, scope)?;
                    if let Some(session) = sessions
                        .iter_mut()
                        .find(|session| session.generation == generation)
                        && session.state == SessionState::InUse
                    {
                        session.release()?;
                        transaction.save_session(session.clone())?;
                    }
                }
                let event_id = next_event_id();
                let fencing_token = run.fencing_token.clone();
                transaction.save_run(run)?;
                transaction.append_event(LocalEvent::RunResult {
                    event_id,
                    run_id,
                    status,
                    item_outcomes,
                    continuation_note,
                    error_code,
                    fencing_token,
                })?;
                Ok(event_id)
            })
            .await
    }

    pub(in crate::computer) async fn stop<P: TransactionPort, D: DriverPort>(
        store: &mut P,
        driver: &mut D,
        run_id: RunId,
    ) -> Result<EventId, ApplicationError> {
        let mut run = store
            .transact(async |transaction| {
                transaction.run(run_id)?.ok_or(ApplicationError::NotFound)
            })
            .await?;
        if run.state == LocalRunState::Queued {
            run.cancel_queued()?;
            let event_id = next_event_id();
            let fencing_token = run.fencing_token.clone();
            store
                .transact(async |transaction| {
                    transaction.save_run(run.clone())?;
                    transaction.append_event(LocalEvent::RunResult {
                        event_id,
                        run_id,
                        status: TerminalStatus::Canceled,
                        item_outcomes: run
                            .deliveries
                            .values()
                            .map(|delivery| (delivery.item.item_id, ItemDisposition::Released))
                            .collect(),
                        continuation_note: None,
                        error_code: None,
                        fencing_token,
                    })
                })
                .await?;
            return Ok(event_id);
        }
        if !run.state.is_terminal() && run.state != LocalRunState::Stopping {
            run.request_stop()?;
            store
                .transact(async |transaction| transaction.save_run(run.clone()))
                .await?;
            driver.interrupt(&run).await?;
        }
        let outcomes = run
            .deliveries
            .values()
            .map(|delivery| (delivery.item.item_id, ItemDisposition::Released))
            .collect();
        Self::finish(
            store,
            run_id,
            TerminalStatus::Canceled,
            outcomes,
            None,
            None,
        )
        .await
    }
}

fn next_event_id() -> EventId {
    EventId::from_uuid(Uuid::now_v7())
}
