use std::time::Duration;

use anyhow::{Context, Result, bail, ensure};
use futures_util::{SinkExt, StreamExt};
use sqlx::SqlitePool;
use time::OffsetDateTime;
use tokio::sync::mpsc;
use tokio_tungstenite::tungstenite;
use tokio_util::sync::CancellationToken;
use uuid::Uuid;

use crate::{
    computer_protocol::{ComputerCommand, ComputerFrame, ServerFrame},
    supervisor::Supervisor,
};

use super::{ConnectionTaskExit, credentials::platform_os};

#[derive(Debug)]
pub(super) struct ReceivedCommand {
    pub(super) command_id: Uuid,
    pub(super) computer_seq: i64,
    pub(super) command: ComputerCommand,
}

#[derive(Debug, PartialEq, Eq)]
pub(super) struct CommandLogContext {
    pub(super) agent_member_id: Option<Uuid>,
    pub(super) run_id: Option<Uuid>,
}

pub(super) fn command_log_context(command: &ComputerCommand) -> CommandLogContext {
    CommandLogContext {
        agent_member_id: command.agent_id(),
        run_id: command.run_id(),
    }
}

pub(super) async fn mark_run_result_reported(
    database: &SqlitePool,
    computer_id: Uuid,
    event_id: &str,
) -> Result<()> {
    let updated = sqlx::query(
        "UPDATE run_result_outbox SET reported_at = COALESCE(reported_at, ?2), last_error = NULL \
         WHERE event_id = ?1",
    )
    .bind(event_id)
    .bind(OffsetDateTime::now_utc().to_string())
    .execute(database)
    .await?;
    ensure!(
        updated.rows_affected() == 1,
        "Server receipted an unknown Run result event"
    );
    tracing::info!(computer_id = %computer_id, event_id, "Agent run result receipted");
    Ok(())
}

pub(super) async fn mark_run_started_reported(
    database: &SqlitePool,
    computer_id: Uuid,
    event_id: &str,
) -> Result<()> {
    let updated = sqlx::query(
        "UPDATE run_started_outbox SET reported_at = COALESCE(reported_at, ?2) WHERE event_id = ?1",
    )
    .bind(event_id)
    .bind(OffsetDateTime::now_utc().to_string())
    .execute(database)
    .await?;
    ensure!(
        updated.rows_affected() == 1,
        "Server receipted an unknown Run started event"
    );
    tracing::info!(computer_id = %computer_id, event_id, "Agent run started event receipted");
    Ok(())
}

pub(super) fn daemon_http_client() -> Result<reqwest::Client> {
    reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(5))
        .timeout(Duration::from_secs(15))
        .build()
        .context("failed to build Computer HTTP client")
}

pub(super) fn encode_computer_frame(frame: &ComputerFrame) -> Result<tungstenite::Message> {
    Ok(tungstenite::Message::Text(
        serde_json::to_string(frame)?.into(),
    ))
}

pub(super) async fn queue_computer_frame(
    outgoing: &mpsc::Sender<tungstenite::Message>,
    frame: &ComputerFrame,
) -> Result<()> {
    outgoing
        .send(encode_computer_frame(frame)?)
        .await
        .context("Computer WebSocket writer stopped")
}

pub(super) async fn websocket_writer_task<W>(
    mut writer: W,
    mut outgoing: mpsc::Receiver<tungstenite::Message>,
    cancellation: CancellationToken,
) -> Result<ConnectionTaskExit>
where
    W: futures_util::Sink<tungstenite::Message, Error = tungstenite::Error> + Unpin,
{
    loop {
        tokio::select! {
            _ = cancellation.cancelled() => {
                let _ = tokio::time::timeout(
                    Duration::from_secs(1),
                    writer.send(tungstenite::Message::Close(None)),
                ).await;
                return Ok(ConnectionTaskExit::Cancelled);
            }
            message = outgoing.recv() => {
                let Some(message) = message else {
                    return Ok(ConnectionTaskExit::Disconnected);
                };
                writer.send(message).await.context("failed to write Computer WebSocket frame")?;
            }
        }
    }
}

pub(super) async fn websocket_reader_task<R>(
    mut reader: R,
    outgoing: mpsc::Sender<tungstenite::Message>,
    commands: mpsc::Sender<ReceivedCommand>,
    database: SqlitePool,
    computer_id: Uuid,
    cancellation: CancellationToken,
) -> Result<ConnectionTaskExit>
where
    R: futures_util::Stream<Item = std::result::Result<tungstenite::Message, tungstenite::Error>>
        + Unpin,
{
    loop {
        let message = tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            message = reader.next() => message,
        };
        let Some(message) = message else {
            tracing::info!(computer_id = %computer_id, status = "disconnected", reason = "stream_ended", "Computer disconnected");
            return Ok(ConnectionTaskExit::Disconnected);
        };
        match message? {
            tungstenite::Message::Text(text) => {
                match serde_json::from_str(&text)
                    .context("Server sent an invalid Computer frame")?
                {
                    ServerFrame::Command {
                        command_id,
                        computer_seq,
                        command,
                    } => {
                        let kind = command.kind();
                        let context = command_log_context(&command);
                        tracing::info!(
                            computer_id = %computer_id,
                            command_id = %command_id,
                            computer_seq,
                            kind,
                            agent_member_id = context.agent_member_id.map(|id| id.to_string()).as_deref().unwrap_or("none"),
                            run_id = context.run_id.map(|id| id.to_string()).as_deref().unwrap_or("none"),
                            "Computer command received"
                        );
                        persist_command(&database, command_id, computer_seq, &command).await?;
                        queue_computer_frame(
                            &outgoing,
                            &ComputerFrame::CommandAck {
                                command_id,
                                computer_seq,
                            },
                        )
                        .await?;
                        tracing::info!(computer_id = %computer_id, command_id = %command_id, computer_seq, kind, "Computer command acknowledged");
                        commands
                            .send(ReceivedCommand {
                                command_id,
                                computer_seq,
                                command: *command,
                            })
                            .await
                            .context("Computer command processor stopped")?;
                    }
                    ServerFrame::Shutdown { reason } => {
                        tracing::info!(computer_id = %computer_id, reason = %reason, "Server requested Computer shutdown");
                        return Ok(if reason == "computer_deleted" {
                            ConnectionTaskExit::Deleted
                        } else {
                            ConnectionTaskExit::Shutdown
                        });
                    }
                    ServerFrame::ResultReceipt { event_id } => {
                        mark_run_result_reported(&database, computer_id, &event_id).await?;
                    }
                    ServerFrame::StartedReceipt { event_id } => {
                        mark_run_started_reported(&database, computer_id, &event_id).await?;
                    }
                    ServerFrame::Welcome { .. } => {}
                }
            }
            tungstenite::Message::Ping(bytes) => {
                outgoing
                    .send(tungstenite::Message::Pong(bytes))
                    .await
                    .context("Computer WebSocket writer stopped")?;
            }
            tungstenite::Message::Close(_) => {
                tracing::info!(computer_id = %computer_id, status = "disconnected", reason = "server_closed", "Computer disconnected");
                return Ok(ConnectionTaskExit::Disconnected);
            }
            tungstenite::Message::Binary(_)
            | tungstenite::Message::Pong(_)
            | tungstenite::Message::Frame(_) => {}
        }
    }
}

pub(super) async fn heartbeat_reporter_task(
    supervisor: Supervisor,
    computer_id: Uuid,
    heartbeat_seconds: u64,
    outgoing: mpsc::Sender<tungstenite::Message>,
    cancellation: CancellationToken,
) -> Result<ConnectionTaskExit> {
    let mut interval = tokio::time::interval(Duration::from_secs(heartbeat_seconds));
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    interval.tick().await;
    loop {
        tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            _ = interval.tick() => {}
        }
        let (agents_count, active_runs) = supervisor.counts().await?;
        queue_computer_frame(
            &outgoing,
            &ComputerFrame::Heartbeat {
                daemon_version: env!("CARGO_PKG_VERSION").to_owned(),
                os: platform_os()?.to_owned(),
                cpu_count: std::thread::available_parallelism()
                    .map(usize::from)
                    .unwrap_or(1),
                memory_total_bytes: None,
                agents_count,
                active_runs,
            },
        )
        .await?;
        tracing::debug!(computer_id = %computer_id, agents_count, active_runs, "Computer heartbeat sent");
    }
}

pub(super) fn decode_server_frame(message: tungstenite::Message) -> Result<ServerFrame> {
    match message {
        tungstenite::Message::Text(text) => {
            serde_json::from_str(&text).context("Server sent an invalid Computer frame")
        }
        _ => bail!("Server did not send a text Computer frame"),
    }
}

pub(super) async fn send_ws_frame<S>(writer: &mut S, frame: &ComputerFrame) -> Result<()>
where
    S: futures_util::Sink<tungstenite::Message, Error = tungstenite::Error> + Unpin,
{
    writer
        .send(tungstenite::Message::Text(
            serde_json::to_string(frame)?.into(),
        ))
        .await?;
    Ok(())
}

pub(super) async fn last_acked_sequence(database: &SqlitePool) -> Result<i64> {
    Ok(sqlx::query_scalar::<_, Option<i64>>(
        "SELECT max(computer_seq) FROM server_commands WHERE status IN ('received', 'running', 'completed', 'failed')",
    )
    .fetch_one(database)
    .await?
    .unwrap_or(0))
}

pub(super) async fn persist_command(
    database: &SqlitePool,
    command_id: Uuid,
    computer_seq: i64,
    command: &ComputerCommand,
) -> Result<()> {
    ensure!(computer_seq > 0, "Server command sequence must be positive");
    let request = serde_json::to_value(command)?;
    let inserted = sqlx::query(
        "INSERT INTO server_commands \
         (command_id, computer_seq, request_json, status, received_at) \
         VALUES (?1, ?2, ?3, 'received', ?4) ON CONFLICT(command_id) DO NOTHING",
    )
    .bind(command_id.to_string())
    .bind(computer_seq)
    .bind(serde_json::to_string(&request)?)
    .bind(OffsetDateTime::now_utc().to_string())
    .execute(database)
    .await?;
    if inserted.rows_affected() == 0 {
        let existing: (i64, String) = sqlx::query_as(
            "SELECT computer_seq, request_json FROM server_commands WHERE command_id = ?1",
        )
        .bind(command_id.to_string())
        .fetch_one(database)
        .await?;
        ensure!(
            existing.0 == computer_seq && existing.1 == serde_json::to_string(&request)?,
            "Server reused a command ID with different content"
        );
    }
    Ok(())
}

pub(super) fn reconnect_delay(attempt: u32) -> std::time::Duration {
    let base_ms = 1_000_u64
        .saturating_mul(1_u64 << attempt.min(5))
        .min(30_000);
    let mut random = [0_u8; 2];
    let _ = getrandom::fill(&mut random);
    let jitter = u16::from_le_bytes(random) as u64 % (base_ms / 4 + 1);
    std::time::Duration::from_millis(base_ms + jitter)
}
