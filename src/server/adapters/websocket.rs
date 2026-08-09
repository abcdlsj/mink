use crate::protocol::{
    computer::{
        ChannelActivityQueryErrorCode, ChannelActivityQueryResult, ChannelActivityResultEnvelope,
        CommandAck, CommandEnvelope, CommandOutcome, CommandResult, CommandSequence, ComputerFrame,
        ComputerHello, DeliveryOutcome, HandshakeErrorCode, Receipt, ReceiptKind,
        RunTerminalStatus, ServerFrame, ServerHandshake,
    },
    version::SUPPORTED,
};
use crate::{
    ids::{ComputerId, EventId, MemberId, SpaceId, ThreadId},
    server::application::ports::ApplicationError,
};

use super::postgres::PostgresAdapter;

pub(super) async fn replay_commands(
    storage: &PostgresAdapter,
    computer_id: ComputerId,
    watermark: CommandSequence,
) -> Result<Vec<CommandEnvelope>, ApplicationError> {
    storage
        .settle_stale_run_commands(computer_id, watermark)
        .await?;
    storage.pending_commands(computer_id, watermark).await
}

pub(super) async fn acknowledge_command(
    storage: &PostgresAdapter,
    computer_id: ComputerId,
    ack: &CommandAck,
) -> Result<(), ApplicationError> {
    storage.acknowledge_command(computer_id, ack).await
}

fn channel_activity_result(
    query_id: crate::ids::QueryId,
    result: Result<crate::protocol::computer::ChannelActivitySnapshot, ApplicationError>,
) -> ChannelActivityResultEnvelope {
    let result = match result {
        Ok(snapshot) => ChannelActivityQueryResult::Snapshot(snapshot),
        Err(error) => ChannelActivityQueryResult::Unavailable {
            code: channel_activity_error_code(&error),
        },
    };
    ChannelActivityResultEnvelope { query_id, result }
}

fn channel_activity_error_code(error: &ApplicationError) -> ChannelActivityQueryErrorCode {
    match error {
        ApplicationError::PermissionDenied => ChannelActivityQueryErrorCode::PermissionDenied,
        ApplicationError::Unavailable => ChannelActivityQueryErrorCode::Unavailable,
        ApplicationError::Conflict | ApplicationError::NotFound => {
            ChannelActivityQueryErrorCode::InvalidRequest
        }
        ApplicationError::Domain(_)
        | ApplicationError::Unauthenticated
        | ApplicationError::PayloadTooLarge
        | ApplicationError::ContextChanged
        | ApplicationError::StaleMessageContext { .. }
        | ApplicationError::Internal => ChannelActivityQueryErrorCode::Internal,
    }
}

pub(super) fn negotiate(
    hello: &ComputerHello,
    computer_deleted: bool,
    authenticated: bool,
) -> ServerHandshake {
    let rejection = if !authenticated {
        Some(HandshakeErrorCode::Unauthenticated)
    } else if computer_deleted {
        Some(HandshakeErrorCode::ComputerDeleted)
    } else if SUPPORTED.negotiate(hello.supported_versions).is_none() {
        Some(HandshakeErrorCode::NoCommonVersion)
    } else {
        None
    };
    if let Some(code) = rejection {
        return ServerHandshake::Rejected {
            code,
            supported_versions: SUPPORTED,
        };
    }
    ServerHandshake::Welcome {
        selected_version: SUPPORTED
            .negotiate(hello.supported_versions)
            .expect("common version checked above"),
        supported_versions: SUPPORTED,
        heartbeat_interval_seconds: 15,
    }
}

use super::command::CommandRegistry;
use super::http::{ApiError, ComputerPrincipal};
use super::query::QueryRegistry;
use crate::server::application::execution::{
    AcknowledgeDelivery, AcknowledgeDeliveryInput, ApplyCommandResult, CompleteRun,
    CompleteRunInput, StartRun, StartRunInput, SyncComputerRuns, SyncComputerRunsInput,
};
use crate::server::domain::attention::AttentionPolicy;
use crate::server::domain::execution::{DeliveryOutcome as ServerDeliveryOutcome, RunOutcome};
use axum::extract::ws::{Message as WebSocketMessage, WebSocket};
use futures_util::StreamExt;
use sqlx::{PgPool, Row};
use time::OffsetDateTime;
use uuid::Uuid;
pub(super) async fn computer_socket(
    mut socket: WebSocket,
    storage: PostgresAdapter,
    pool: PgPool,
    queries: QueryRegistry,
    commands: CommandRegistry,
    computer_id: Uuid,
    deleted: bool,
) {
    let Some(Ok(WebSocketMessage::Text(encoded))) = socket.next().await else {
        return;
    };
    let Ok(hello) = serde_json::from_str::<ComputerHello>(&encoded) else {
        return;
    };
    let handshake = super::websocket::negotiate(&hello, deleted, true);
    if send_json(&mut socket, &handshake).await.is_err() {
        return;
    }
    if !matches!(handshake, ServerHandshake::Welcome { .. }) {
        return;
    }
    tracing::info!(
        %computer_id,
        daemon_version = %hello.daemon_version,
        live_run_count = hello.live_run_ids.len(),
        "Computer connected"
    );
    let _=sqlx::query("UPDATE computers SET connection_status='online',daemon_version=$2,last_seen_at=$3 WHERE id=$1")
        .bind(computer_id).bind(&hello.daemon_version).bind(OffsetDateTime::now_utc()).execute(&pool).await;
    // Reconcile before replaying commands. The Computer just told us which Runs it still holds; every
    // other non-terminal Run of ours died with its previous daemon process. This is the only path that
    // fails a Run the Server never heard about, and it runs because the Computer reported, not because
    // time passed.
    {
        let mut application = storage.clone();
        match SyncComputerRuns::execute(
            &mut application,
            SyncComputerRunsInput {
                computer_id: ComputerId::from_uuid(computer_id),
                live_run_ids: hello.live_run_ids.clone(),
                max_retry_count: AttentionPolicy::MAX_RETRY_COUNT,
                now: OffsetDateTime::now_utc(),
            },
        )
        .await
        {
            Ok(synced) if synced.runs_failed > 0 => tracing::warn!(
                %computer_id,
                runs_failed = synced.runs_failed,
                items_released = synced.items_released,
                items_dead = synced.items_dead,
                "reconnecting Computer no longer holds these Runs; their Items returned to the queue"
            ),
            Ok(_) => {}
            Err(error) => tracing::error!(
                %computer_id,
                %error,
                "Computer Run sync failed"
            ),
        }
    }
    let mut sent_watermark = replay_pending_commands(
        &storage,
        &mut socket,
        ComputerId::from_uuid(computer_id),
        hello.command_watermark,
    )
    .await;
    let (connection, mut outbound) = queries.connect(computer_id);
    let (command_connection, mut command_wakeups) = commands.connect(computer_id);
    loop {
        let frame = tokio::select! {
            outgoing = outbound.recv() => {
                let Some(outgoing) = outgoing else { break };
                if send_json(&mut socket, &outgoing).await.is_err() {
                    break;
                }
                continue;
            }
            wakeup = command_wakeups.recv() => {
                let Some(()) = wakeup else { break };
                sent_watermark = replay_pending_commands(
                    &storage,
                    &mut socket,
                    ComputerId::from_uuid(computer_id),
                    sent_watermark,
                )
                .await;
                continue;
            }
            frame = socket.next() => match frame {
                Some(frame) => frame,
                None => break,
            },
        };
        let Ok(WebSocketMessage::Text(encoded)) = frame else {
            continue;
        };
        let Ok(frame) = serde_json::from_str::<ComputerFrame>(&encoded) else {
            continue;
        };
        match frame {
            ComputerFrame::ChannelActivityQuery { query } => {
                let application = storage.clone();
                let result = application
                    .channel_activity_snapshot(
                        ComputerId::from_uuid(computer_id),
                        query.run_id,
                        MemberId::from_uuid(query.agent_id.into_uuid()),
                        query.channel_id,
                        query.after_sequence,
                        query.through_sequence,
                        query.limit,
                    )
                    .await;
                let result = channel_activity_result(query.query_id, result);
                let _ =
                    send_json(&mut socket, &ServerFrame::ChannelActivityResult { result }).await;
            }
            ComputerFrame::QueryResult { result } => queries.resolve(result),
            ComputerFrame::Heartbeat { heartbeat } => {
                let _ = sqlx::query("UPDATE computers SET last_seen_at=$2 WHERE id=$1")
                    .bind(computer_id)
                    .bind(heartbeat.observed_at)
                    .execute(&pool)
                    .await;
                let replay_watermark = replay_pending_commands(
                    &storage,
                    &mut socket,
                    ComputerId::from_uuid(computer_id),
                    CommandSequence(0),
                )
                .await;
                if replay_watermark.0 > sent_watermark.0 {
                    sent_watermark = replay_watermark;
                }
            }
            ComputerFrame::CommandAck { .. } => {}
            ComputerFrame::RunResult { result } => {
                let event_id = result.event_id;
                let run_id = result.run_id;
                let yielded = result.status == RunTerminalStatus::Yielded;
                let response = super::http::submit_run_result(
                    &storage,
                    ComputerPrincipal {
                        computer_id: ComputerId::from_uuid(computer_id),
                    },
                    result,
                )
                .await;
                if response.is_ok() {
                    if yielded {
                        let run = sqlx::query(
                            "SELECT agent_runs.agent_id,agent_runs.space_id,agent_runs.focus_thread_id,\
                                    agent_runs.continuation_note,\
                                    messages.channel_id,messages.channel_seq,channels.slug \
                             FROM agent_runs JOIN messages ON messages.id=agent_runs.focus_thread_id \
                                    JOIN channels ON channels.id=messages.channel_id \
                             WHERE agent_runs.id=$1",
                        )
                        .bind(run_id.into_uuid())
                        .fetch_optional(&pool)
                        .await;
                        if let Ok(Some(row)) = run {
                            let slug = row.get::<Option<String>, _>("slug");
                            let continuation_note =
                                row.get::<Option<String>, _>("continuation_note");
                            let focus_label = super::http::activity_thread_label(
                                slug.as_deref(),
                                row.get("channel_seq"),
                            );
                            storage
                                .record_agent_activity(
                                    SpaceId::from_uuid(row.get("space_id")),
                                    MemberId::from_uuid(row.get("agent_id")),
                                    "run.yield",
                                    super::http::agent_activity_details(
                                        serde_json::json!({
                                            "run_id": run_id,
                                            "thread_id": ThreadId::from_uuid(row.get("focus_thread_id")),
                                            "channel_id": row.get::<Uuid, _>("channel_id"),
                                            "scope_channel_id": row.get::<Uuid, _>("channel_id"),
                                        }),
                                        vec![("focus", focus_label)],
                                        continuation_note
                                            .as_deref()
                                            .map(super::http::agent_activity_preview),
                                    ),
                                )
                                .await;
                        }
                    }
                    let _ = send_json(
                        &mut socket,
                        &ServerFrame::Receipt {
                            receipt: Receipt {
                                event_id,
                                kind: ReceiptKind::RunResult,
                            },
                        },
                    )
                    .await;
                }
            }
            ComputerFrame::RunStarted { started } => {
                let mut application = storage.clone();
                let applied = StartRun::execute(
                    &mut application,
                    StartRunInput {
                        run_id: started.run_id,
                        computer_id: ComputerId::from_uuid(computer_id),
                        now: started.observed_at,
                    },
                )
                .await;
                if applied.is_ok() {
                    let _ = send_json(
                        &mut socket,
                        &ServerFrame::Receipt {
                            receipt: Receipt {
                                event_id: started.event_id,
                                kind: ReceiptKind::RunStarted,
                            },
                        },
                    )
                    .await;
                }
            }
            ComputerFrame::DeliveryReceipt { receipt } => {
                let mut application = storage.clone();
                let applied = AcknowledgeDelivery::execute(
                    &mut application,
                    AcknowledgeDeliveryInput {
                        event_id: receipt.event_id,
                        run_id: receipt.run_id,
                        computer_id: ComputerId::from_uuid(computer_id),
                        delivery_sequence: receipt.delivery_sequence.0,
                        outcome: match receipt.outcome {
                            DeliveryOutcome::Accepted => ServerDeliveryOutcome::Accepted,
                            DeliveryOutcome::TooLate => ServerDeliveryOutcome::TooLate,
                            DeliveryOutcome::Unsupported => ServerDeliveryOutcome::Unsupported,
                        },
                        now: OffsetDateTime::now_utc(),
                    },
                )
                .await;
                if applied.is_ok() {
                    let _ = send_json(
                        &mut socket,
                        &ServerFrame::Receipt {
                            receipt: Receipt {
                                event_id: receipt.event_id,
                                kind: ReceiptKind::Delivery,
                            },
                        },
                    )
                    .await;
                }
            }
            ComputerFrame::CommandResult { result } => {
                if let CommandOutcome::Rejected { ref code } = result.outcome {
                    let diagnostic = storage
                        .command_diagnostic(
                            ComputerId::from_uuid(computer_id),
                            result.command_id,
                            result.sequence.0,
                        )
                        .await
                        .ok()
                        .flatten();
                    tracing::warn!(
                        %computer_id,
                        command_id = %result.command_id.into_uuid(),
                        sequence = result.sequence.0,
                        command_kind = diagnostic.as_ref().map_or("<unknown>", |item| item.kind),
                        agent_id = ?diagnostic.as_ref().and_then(|item| item.agent_id),
                        run_id = ?diagnostic.as_ref().and_then(|item| item.run_id),
                        task_id = ?diagnostic.as_ref().and_then(|item| item.task_id),
                        delivery_sequence =
                            diagnostic.as_ref().and_then(|item| item.delivery_sequence),
                        error_code = ?code,
                        "Computer command rejected"
                    );
                }
                if apply_command_result(&storage, computer_id, &result)
                    .await
                    .is_ok()
                {
                    if matches!(result.outcome, CommandOutcome::Rejected { .. })
                        && let Err(error) =
                            apply_rejected_command(&storage, computer_id, &pool, &result).await
                    {
                        tracing::error!(
                            %computer_id,
                            command_id = %result.command_id.into_uuid(),
                            sequence = result.sequence.0,
                            ?error,
                            "Rejected Computer command could not be reconciled"
                        );
                        continue;
                    }
                    let ack = CommandAck {
                        command_id: result.command_id,
                        sequence: result.sequence,
                    };
                    let _ = super::websocket::acknowledge_command(
                        &storage,
                        ComputerId::from_uuid(computer_id),
                        &ack,
                    )
                    .await;
                }
            }
        }
    }
    commands.disconnect(command_connection);
    queries.disconnect(connection);
    tracing::info!(%computer_id, "Computer disconnected");
    let _ = sqlx::query("UPDATE computers SET connection_status='offline' WHERE id=$1")
        .bind(computer_id)
        .execute(&pool)
        .await;
}

/// Sends every pending command after `watermark` and returns the last sequence actually sent.
async fn replay_pending_commands(
    storage: &PostgresAdapter,
    socket: &mut WebSocket,
    computer_id: ComputerId,
    watermark: CommandSequence,
) -> CommandSequence {
    let mut sent = watermark;
    if let Ok(commands) = super::websocket::replay_commands(storage, computer_id, watermark).await {
        for envelope in commands {
            let sequence = envelope.sequence;
            if send_json(
                socket,
                &ServerFrame::Command {
                    envelope: Box::new(envelope),
                },
            )
            .await
            .is_err()
            {
                break;
            }
            sent = sequence;
        }
    }
    sent
}

async fn apply_command_result(
    storage: &PostgresAdapter,
    computer_id: Uuid,
    result: &CommandResult,
) -> Result<(), ApiError> {
    let mut storage = storage.clone();
    ApplyCommandResult::execute(
        &mut storage,
        ComputerId::from_uuid(computer_id),
        result.command_id,
        result.sequence.0,
        matches!(result.outcome, CommandOutcome::Applied),
    )
    .await
    .map_err(|_| ApiError::internal())
}

async fn apply_rejected_command(
    storage: &PostgresAdapter,
    computer_id: Uuid,
    pool: &PgPool,
    result: &CommandResult,
) -> Result<(), ApiError> {
    // A command is acknowledged only after its rejected delivery compensation succeeds. Once the
    // acknowledgement is durable, replaying the same result must not emit another activity event.
    if storage
        .command_is_acknowledged(
            ComputerId::from_uuid(computer_id),
            result.command_id,
            result.sequence.0,
        )
        .await
        .map_err(|_| ApiError::internal())?
    {
        return Ok(());
    }
    if let Some((run_id, delivery_sequence)) = storage
        .run_attach_command_target(
            ComputerId::from_uuid(computer_id),
            result.command_id,
            result.sequence.0,
        )
        .await
        .map_err(|_| ApiError::internal())?
    {
        let mut application = storage.clone();
        AcknowledgeDelivery::execute(
            &mut application,
            crate::server::application::execution::AcknowledgeDeliveryInput {
                event_id: EventId::from_uuid(result.command_id.into_uuid()),
                run_id,
                computer_id: ComputerId::from_uuid(computer_id),
                delivery_sequence,
                outcome: ServerDeliveryOutcome::Unsupported,
                now: OffsetDateTime::now_utc(),
            },
        )
        .await
        .map_err(|_| ApiError::internal())?;

        let row = sqlx::query(
            "SELECT r.agent_id,r.space_id,r.focus_thread_id,m.channel_id \
             FROM agent_runs r JOIN messages m ON m.id=r.focus_thread_id WHERE r.id=$1",
        )
        .bind(run_id.into_uuid())
        .fetch_optional(pool)
        .await
        .map_err(|_| ApiError::internal())?;
        if let Some(row) = row {
            storage
                .record_agent_activity_with_id(
                    EventId::from_uuid(result.command_id.into_uuid()),
                    SpaceId::from_uuid(row.get("space_id")),
                    MemberId::from_uuid(row.get("agent_id")),
                    "run.delivery_rejected",
                    super::http::agent_activity_details(
                        serde_json::json!({
                            "run_id": run_id,
                            "thread_id": ThreadId::from_uuid(row.get("focus_thread_id")),
                            "channel_id": row.get::<Uuid, _>("channel_id"),
                            "scope_channel_id": row.get::<Uuid, _>("channel_id"),
                        }),
                        vec![("delivery_sequence", delivery_sequence.to_string())],
                        None,
                    ),
                )
                .await
                .map_err(|_| ApiError::internal())?;
        }
        return Ok(());
    }

    let Some(run_id) = storage
        .run_start_command_target(
            ComputerId::from_uuid(computer_id),
            result.command_id,
            result.sequence.0,
        )
        .await
        .map_err(|_| ApiError::internal())?
    else {
        return Ok(());
    };
    let CommandOutcome::Rejected { code } = &result.outcome else {
        return Ok(());
    };
    let mut application = storage.clone();
    CompleteRun::execute(
        &mut application,
        CompleteRunInput {
            event_id: EventId::from_uuid(result.command_id.into_uuid()),
            run_id,
            computer_id: ComputerId::from_uuid(computer_id),
            outcome: RunOutcome::Failed,
            error_code: Some(super::http::run_error_code(*code)),
            item_dispositions: Vec::new(),
            continuation_note: None,
            max_retry_count: AttentionPolicy::MAX_RETRY_COUNT,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(|_| ApiError::internal())?;
    Ok(())
}

async fn send_json(
    socket: &mut WebSocket,
    value: &impl serde::Serialize,
) -> Result<(), axum::Error> {
    let encoded = serde_json::to_string(value).map_err(axum::Error::new)?;
    socket.send(WebSocketMessage::Text(encoded.into())).await
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeSet;

    use uuid::Uuid;

    use super::*;
    use crate::{
        ids::DaemonSessionId,
        protocol::{
            computer::{CommandSequence, DaemonCapability},
            version::{ProtocolVersion, ProtocolVersionRange},
        },
    };

    fn hello(range: ProtocolVersionRange) -> ComputerHello {
        ComputerHello {
            supported_versions: range,
            daemon_version: "1.0.0".into(),
            capabilities: BTreeSet::from([DaemonCapability::Sandbox]),
            daemon_session_id: DaemonSessionId::from_uuid(Uuid::now_v7()),
            command_watermark: CommandSequence(0),
            live_run_ids: Vec::new(),
        }
    }

    #[test]
    fn handshake_rejects_deleted_computer_and_disjoint_protocol() {
        assert!(matches!(
            negotiate(&hello(SUPPORTED), true, true),
            ServerHandshake::Rejected {
                code: HandshakeErrorCode::ComputerDeleted,
                ..
            }
        ));
        let future = ProtocolVersionRange::new(ProtocolVersion::new(5), ProtocolVersion::new(5));
        assert!(matches!(
            negotiate(&hello(future), false, true),
            ServerHandshake::Rejected {
                code: HandshakeErrorCode::NoCommonVersion,
                ..
            }
        ));
    }

    #[test]
    fn channel_activity_query_failure_is_explicit_and_content_free() {
        let query_id = crate::ids::QueryId::from_uuid(Uuid::now_v7());
        let frame = channel_activity_result(query_id, Err(ApplicationError::Unavailable));
        assert_eq!(frame.query_id, query_id);
        assert!(matches!(
            frame.result,
            ChannelActivityQueryResult::Unavailable {
                code: ChannelActivityQueryErrorCode::Unavailable,
            }
        ));
        let encoded = serde_json::to_string(&frame).unwrap();
        assert!(!encoded.contains("Message body"));
    }
}
