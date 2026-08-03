use time::OffsetDateTime;
use uuid::Uuid;

use crate::ids::{EventId, InboxItemId, RunId, TaskId};

use crate::computer::core::{
    input::{AttentionNoticeInput, DispatchedItemInput},
    session::{self, ProviderSession, SessionFingerprint, SessionScope, SessionState},
    supervisor::{DeliveryState, ItemDisposition, LocalRunState, TerminalStatus},
};

use super::{
    ApplicationError,
    ports::{
        ComputerTransaction, DriverPort, DriverTurnOutcome, LocalErrorCode, LocalEvent,
        OpenSessionRequest, SteerOutcome, TransactionPort,
    },
};

pub(in crate::computer) struct RunService;

impl RunService {
    pub(in crate::computer) async fn finish_driver_turn<P: TransactionPort>(
        store: &mut P,
        run_id: RunId,
        outcome: DriverTurnOutcome,
    ) -> Result<Option<EventId>, ApplicationError> {
        let run = store
            .transact(async |transaction| {
                transaction.run(run_id)?.ok_or(ApplicationError::NotFound)
            })
            .await?;
        if run.view().state.is_terminal() {
            return Ok(None);
        }
        let item_outcomes = run
            .view()
            .deliveries
            .values()
            .map(|delivery| {
                (
                    delivery.item.item_id,
                    delivery.disposition.unwrap_or(ItemDisposition::Released),
                )
            })
            .collect::<Vec<_>>();
        let has_unhandled_items = run
            .view()
            .deliveries
            .values()
            .any(|delivery| delivery.disposition.is_none());
        let (status, error_code) = match outcome {
            DriverTurnOutcome::Completed if !has_unhandled_items => {
                (TerminalStatus::Completed, None)
            }
            DriverTurnOutcome::Completed => (TerminalStatus::Failed, None),
            DriverTurnOutcome::Failed => {
                (TerminalStatus::Failed, Some(LocalErrorCode::DriverError))
            }
            DriverTurnOutcome::Interrupted => (TerminalStatus::Canceled, None),
        };
        Self::finish(store, run_id, status, item_outcomes, None, error_code)
            .await
            .map(Some)
    }

    pub(in crate::computer) async fn record_item_disposition<P: TransactionPort>(
        store: &mut P,
        run_id: RunId,
        item_id: InboxItemId,
        disposition: ItemDisposition,
    ) -> Result<(), ApplicationError> {
        store
            .transact(async |transaction| {
                let mut run = transaction.run(run_id)?.ok_or(ApplicationError::NotFound)?;
                if run.view().state != LocalRunState::Running {
                    return Err(ApplicationError::Conflict);
                }
                run.record_item_disposition(item_id, disposition)?;
                transaction.save_run(run)
            })
            .await
    }

    pub(in crate::computer) async fn yield_run<P: TransactionPort>(
        store: &mut P,
        run_id: RunId,
        continuation_note: Option<String>,
    ) -> Result<EventId, ApplicationError> {
        let run = store
            .transact(async |transaction| {
                transaction.run(run_id)?.ok_or(ApplicationError::NotFound)
            })
            .await?;
        if run.view().state != LocalRunState::Running {
            return Err(ApplicationError::Conflict);
        }
        let item_outcomes = run
            .view()
            .deliveries
            .values()
            .map(|delivery| {
                (
                    delivery.item.item_id,
                    delivery.disposition.unwrap_or(ItemDisposition::Released),
                )
            })
            .collect();
        Self::finish(
            store,
            run_id,
            TerminalStatus::Yielded,
            item_outcomes,
            continuation_note,
            None,
        )
        .await
    }

    pub(in crate::computer) async fn interrupt_terminal<P: TransactionPort, D: DriverPort>(
        store: &mut P,
        driver: &mut D,
        run_id: RunId,
    ) -> Result<(), ApplicationError> {
        let run = store
            .transact(async |transaction| {
                transaction.run(run_id)?.ok_or(ApplicationError::NotFound)
            })
            .await?;
        if !run.view().state.is_terminal() {
            return Err(ApplicationError::Conflict);
        }
        match driver.interrupt(&run).await {
            Ok(()) | Err(ApplicationError::NotFound) => Ok(()),
            Err(error) => Err(error),
        }
    }

    pub(in crate::computer) async fn start<P: TransactionPort, D: DriverPort>(
        store: &mut P,
        driver: &mut D,
        run_id: RunId,
        fingerprint: SessionFingerprint,
    ) -> Result<(), ApplicationError> {
        let (mut run, decision, scope) = store
            .transact(async |transaction| {
                let mut run = transaction.run(run_id)?.ok_or(ApplicationError::NotFound)?;
                if run.view().state == LocalRunState::Running {
                    return Err(ApplicationError::AlreadyApplied);
                }
                run.begin_start()?;
                let scope = run.view().task_id.map_or(
                    SessionScope::Thread(run.view().focus_thread_id),
                    SessionScope::Task,
                );
                let sessions = transaction.sessions(run.view().agent_id, scope)?;
                let decision =
                    session::resolve(&sessions, run.view().agent_id, scope, &fingerprint);
                transaction.save_run(run.clone())?;
                Ok((run, decision, scope))
            })
            .await?;

        let (mut generation, resume_candidate) = match decision {
            session::ResolveDecision::Resume(existing) => {
                (existing.view().generation, Some(existing))
            }
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
            agent_id: run.view().agent_id,
            scope,
            generation,
            fingerprint: fingerprint.clone(),
            resume_locator: resume_candidate
                .as_ref()
                .map(|session| session.view().locator.to_owned()),
            run_token: run.view().run_secret.clone(),
        };
        let attempted_resume = resume_candidate.is_some();
        let session_created_at = resume_candidate
            .as_ref()
            .map_or_else(OffsetDateTime::now_utc, |session| session.view().created_at);
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
                        agent_id: run.view().agent_id,
                        scope,
                        generation,
                        fingerprint: fingerprint.clone(),
                        resume_locator: None,
                        run_token: run.view().run_secret.clone(),
                    })
                    .await?
            }
            Err(error) => return Err(error),
            Ok(_) => return Err(ApplicationError::SessionLost),
        };
        let mut provider_session = ProviderSession::create(
            run.view().agent_id,
            scope,
            generation,
            opened.locator,
            fingerprint,
            session_created_at,
        )
        .map_err(ApplicationError::from)?;
        provider_session.begin_use(OffsetDateTime::now_utc())?;
        store
            .transact(async |transaction| transaction.save_session(provider_session.clone()))
            .await?;
        driver
            .start_turn(&run, provider_session.view().locator)
            .await?;
        run.started(scope, generation)?;
        store
            .transact(async |transaction| {
                transaction.save_session(provider_session)?;
                transaction.save_run(run.clone())?;
                transaction.append_event(LocalEvent::RunStarted {
                    event_id: next_event_id(),
                    run_id: run.view().id,
                })
            })
            .await
    }

    pub(in crate::computer) async fn attach<P: TransactionPort, D: DriverPort>(
        store: &mut P,
        driver: &mut D,
        run_id: RunId,
        sequence: u64,
        item: DispatchedItemInput,
    ) -> Result<DeliveryState, ApplicationError> {
        let (mut run, inserted, late_outcome) = store
            .transact(async |transaction| {
                let mut run = transaction.run(run_id)?.ok_or(ApplicationError::NotFound)?;
                if run.view().state.is_terminal() {
                    let outcome = run.late_delivery(sequence, &item)?;
                    if outcome == DeliveryState::TooLate
                        && !transaction.pending_events()?.into_iter().any(|event| {
                            matches!(
                                event,
                                LocalEvent::Delivery {
                                    run_id: event_run_id,
                                    sequence: event_sequence,
                                    outcome: DeliveryState::TooLate,
                                    ..
                                } if event_run_id == run_id && event_sequence == sequence
                            )
                        })
                    {
                        transaction.append_event(LocalEvent::Delivery {
                            event_id: next_event_id(),
                            run_id,
                            sequence,
                            outcome,
                        })?;
                    }
                    transaction.save_run(run.clone())?;
                    return Ok((run, false, Some(outcome)));
                }
                let inserted = run.attach(sequence, item)?;
                transaction.save_run(run.clone())?;
                Ok((run, inserted, None))
            })
            .await?;
        if let Some(outcome) = late_outcome {
            return Ok(outcome);
        }
        if !inserted && run.view().deliveries[&sequence].state != DeliveryState::Pending {
            return Ok(run.view().deliveries[&sequence].state);
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
                if run.view().task_id == Some(task_id)
                    && run
                        .view()
                        .session
                        .is_some_and(|(scope, _)| scope == SessionScope::Task(task_id))
                {
                    return Ok(());
                }
                let old_scope = SessionScope::Thread(run.view().focus_thread_id);
                let mut sessions = transaction.sessions(run.view().agent_id, old_scope)?;
                let session = sessions
                    .iter_mut()
                    .max_by_key(|session| session.view().generation)
                    .ok_or(ApplicationError::NotFound)?;
                session.promote(run.view().focus_thread_id, task_id, &fingerprint)?;
                run.bind_task(task_id)?;
                run.set_session(Some((
                    SessionScope::Task(task_id),
                    session.view().generation,
                )));
                transaction.delete_session(
                    run.view().agent_id,
                    old_scope,
                    session.view().generation,
                )?;
                transaction.save_session(session.clone())?;
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
                    if run.view().state != LocalRunState::Stopping {
                        run.request_stop()?;
                    }
                } else if matches!(
                    run.view().state,
                    LocalRunState::Starting | LocalRunState::Running
                ) {
                    run.begin_finalizing()?;
                }
                run.validate_item_outcomes(&item_outcomes)?;
                run.finish(status)?;
                if let Some((scope, generation)) = run.view().session {
                    let mut sessions = transaction.sessions(run.view().agent_id, scope)?;
                    if let Some(session) = sessions
                        .iter_mut()
                        .find(|session| session.view().generation == generation)
                        && session.view().state == SessionState::InUse
                    {
                        session.release()?;
                        transaction.save_session(session.clone())?;
                    }
                }
                let event_id = next_event_id();
                transaction.save_run(run)?;
                transaction.append_event(LocalEvent::RunResult {
                    event_id,
                    run_id,
                    status,
                    item_outcomes,
                    continuation_note,
                    error_code,
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
        if run.view().state == LocalRunState::Queued {
            run.cancel_queued()?;
            let event_id = next_event_id();
            store
                .transact(async |transaction| {
                    transaction.save_run(run.clone())?;
                    transaction.append_event(LocalEvent::RunResult {
                        event_id,
                        run_id,
                        status: TerminalStatus::Canceled,
                        item_outcomes: run
                            .view()
                            .deliveries
                            .values()
                            .map(|delivery| (delivery.item.item_id, ItemDisposition::Released))
                            .collect(),
                        continuation_note: None,
                        error_code: None,
                    })
                })
                .await?;
            return Ok(event_id);
        }
        if !run.view().state.is_terminal() && run.view().state != LocalRunState::Stopping {
            run.request_stop()?;
            store
                .transact(async |transaction| transaction.save_run(run.clone()))
                .await?;
            driver.interrupt(&run).await?;
        }
        let outcomes = run
            .view()
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
