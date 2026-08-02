use std::{collections::BTreeMap, path::Path};

use sqlx::{Connection, Row, SqliteConnection, sqlite::SqliteConnectOptions};
use time::OffsetDateTime;

use crate::{
    computer::application::{
        ApplicationError, ClaimedItemInput, Delivery, DeliveryState, DriverKind, FencingToken,
        ItemDisposition, LocalRun, LocalRunSnapshot, LocalRunState, NoticeDelivery,
        ProviderSession, ProviderSessionSnapshot, RunInput, RunPriority, SessionFingerprint,
        SessionScope, SessionState, TerminalStatus,
        command::Command,
        ports::{
            CommandStatus, ComputerTransaction, LocalErrorCode, LocalEvent, StoredCommand,
            TransactionPort,
        },
    },
    ids::{AgentId, CommandId, EventId, InboxItemId, NoticeId, RunId, TaskId, ThreadId},
};

const BASELINE: &str = include_str!("../../../schema/computer.sql");

pub(in crate::computer) struct SqliteAdapter {
    connection: SqliteConnection,
}

#[derive(Clone, Default)]
struct Snapshot {
    commands: BTreeMap<CommandId, StoredCommand>,
    runs: BTreeMap<RunId, LocalRun>,
    sessions: Vec<ProviderSession>,
    events: BTreeMap<EventId, LocalEvent>,
}

#[derive(serde::Deserialize, serde::Serialize)]
struct RunPayload {
    priority: RunPriority,
    input: RunInput,
    session: Option<(SessionScope, u64)>,
    session_fingerprint: Option<SessionFingerprint>,
    notices: BTreeMap<NoticeId, NoticeDelivery>,
    terminal_status: Option<TerminalStatus>,
}

#[derive(serde::Deserialize, serde::Serialize)]
struct RunStartedPayload {
    fencing_token: FencingToken,
}

#[derive(serde::Deserialize, serde::Serialize)]
struct DeliveryPayload {
    sequence: u64,
    outcome: DeliveryState,
    fencing_token: FencingToken,
}

#[derive(serde::Deserialize, serde::Serialize)]
struct RunResultPayload {
    status: TerminalStatus,
    item_outcomes: Vec<(InboxItemId, ItemDisposition)>,
    continuation_note: Option<String>,
    error_code: Option<LocalErrorCode>,
    fencing_token: FencingToken,
}

pub(in crate::computer) struct SqliteTransaction {
    snapshot: Snapshot,
}

impl SqliteAdapter {
    pub(in crate::computer) async fn open(path: &Path) -> Result<Self, ApplicationError> {
        if let Some(parent) = path.parent() {
            tokio::fs::create_dir_all(parent)
                .await
                .map_err(|_| ApplicationError::Internal)?;
        }
        let options = SqliteConnectOptions::new()
            .filename(path)
            .create_if_missing(true)
            .foreign_keys(true)
            .journal_mode(sqlx::sqlite::SqliteJournalMode::Wal);
        let mut connection = SqliteConnection::connect_with(&options)
            .await
            .map_err(map_sqlx)?;
        let initialized: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_meta'",
        )
        .fetch_one(&mut connection)
        .await
        .map_err(map_sqlx)?;
        if initialized == 0 {
            sqlx::raw_sql(BASELINE)
                .execute(&mut connection)
                .await
                .map_err(map_sqlx)?;
        }
        let version: i64 = sqlx::query_scalar("SELECT max(version) FROM schema_meta")
            .fetch_one(&mut connection)
            .await
            .map_err(map_sqlx)?;
        if version != 1 {
            return Err(ApplicationError::Internal);
        }
        Ok(Self { connection })
    }

    async fn load(&mut self) -> Result<Snapshot, ApplicationError> {
        let mut snapshot = Snapshot::default();
        for row in sqlx::query(
            "SELECT command_id,computer_seq,fingerprint,payload_json,status,error_code \
             FROM local_commands ORDER BY computer_seq",
        )
        .fetch_all(&mut self.connection)
        .await
        .map_err(map_sqlx)?
        {
            let command = StoredCommand {
                id: parse_id(row.get("command_id"))?,
                sequence: u64::try_from(row.get::<i64, _>("computer_seq"))
                    .map_err(|_| ApplicationError::Internal)?,
                fingerprint: row.get("fingerprint"),
                command: decode::<Command>(row.get("payload_json"))?,
                status: command_status(row.get("status"))?,
                error: row
                    .get::<Option<&str>, _>("error_code")
                    .map(application_error)
                    .transpose()?,
            };
            snapshot.commands.insert(command.id, command);
        }

        let mut run_snapshots = BTreeMap::new();
        for row in sqlx::query(
            "SELECT run_id,agent_id,task_id,focus_thread_id,fencing_token, \
             ownership_lease_expires_at,state,run_json FROM local_runs ORDER BY run_id",
        )
        .fetch_all(&mut self.connection)
        .await
        .map_err(map_sqlx)?
        {
            let run_id = parse_id(row.get("run_id"))?;
            let payload: RunPayload = decode(row.get("run_json"))?;
            let previous = run_snapshots.insert(
                run_id,
                LocalRunSnapshot {
                    id: run_id,
                    agent_id: parse_id(row.get("agent_id"))?,
                    task_id: row
                        .get::<Option<&str>, _>("task_id")
                        .map(parse_id)
                        .transpose()?,
                    focus_thread_id: parse_id(row.get("focus_thread_id"))?,
                    fencing_token: FencingToken::new(row.get("fencing_token")),
                    priority: payload.priority,
                    ownership_lease_expires_at: parse_time(row.get("ownership_lease_expires_at"))?,
                    input: payload.input,
                    state: run_state(row.get("state"))?,
                    session: payload.session,
                    session_fingerprint: payload.session_fingerprint,
                    deliveries: BTreeMap::new(),
                    notices: payload.notices,
                    terminal_status: payload.terminal_status,
                },
            );
            if previous.is_some() {
                tracing::error!(%run_id, "duplicate local Run row");
                return Err(ApplicationError::Internal);
            }
        }
        for row in sqlx::query(
            "SELECT run_id,delivery_seq,inbox_item_id,state,disposition,item_json FROM run_deliveries \
             ORDER BY run_id,delivery_seq",
        )
        .fetch_all(&mut self.connection)
        .await
        .map_err(map_sqlx)?
        {
            let run_id: RunId = parse_id(row.get("run_id"))?;
            let sequence = u64::try_from(row.get::<i64, _>("delivery_seq"))
                .map_err(|_| ApplicationError::Internal)?;
            let run = run_snapshots
                .get_mut(&run_id)
                .ok_or(ApplicationError::Internal)?;
            let item: ClaimedItemInput = decode(row.get("item_json"))?;
            let stored_item_id: InboxItemId = parse_id(row.get("inbox_item_id"))?;
            if stored_item_id != item.item_id {
                return Err(ApplicationError::Internal);
            }
            let previous = run.deliveries.insert(sequence, Delivery {
                sequence,
                item,
                state: delivery_state(row.get("state"))?,
                disposition: row
                    .get::<Option<&str>, _>("disposition")
                    .map(item_disposition)
                    .transpose()?,
            });
            if previous.is_some() {
                tracing::error!(%run_id, sequence, "duplicate local Run delivery row");
                return Err(ApplicationError::Internal);
            }
        }
        for run_snapshot in run_snapshots.into_values() {
            let run_id = run_snapshot.id;
            let run = LocalRun::rehydrate(run_snapshot).map_err(|error| {
                tracing::error!(%run_id, ?error, "failed to rehydrate local Run");
                ApplicationError::Internal
            })?;
            snapshot.runs.insert(run_id, run);
        }

        for row in sqlx::query(
            "SELECT agent_id,scope_kind,scope_id,generation,driver_kind,provider_locator, \
             workspace_fingerprint,role_revision,audience_fingerprint,state,created_at, \
             last_resumed_at,closed_at FROM provider_sessions \
             ORDER BY agent_id,scope_kind,scope_id,generation",
        )
        .fetch_all(&mut self.connection)
        .await
        .map_err(map_sqlx)?
        {
            let session = ProviderSession::rehydrate(ProviderSessionSnapshot {
                agent_id: parse_id(row.get("agent_id"))?,
                scope: session_scope(row.get("scope_kind"), row.get("scope_id"))?,
                generation: u64::try_from(row.get::<i64, _>("generation"))
                    .map_err(|_| ApplicationError::Internal)?,
                locator: row.get("provider_locator"),
                fingerprint: SessionFingerprint {
                    driver: driver_kind(row.get("driver_kind"))?,
                    workspace: row.get("workspace_fingerprint"),
                    role_revision: u64::try_from(row.get::<i64, _>("role_revision"))
                        .map_err(|_| ApplicationError::Internal)?,
                    audience: row.get("audience_fingerprint"),
                },
                state: session_state(row.get("state"))?,
                created_at: parse_time(row.get("created_at"))?,
                last_resumed_at: parse_optional_time(row.get("last_resumed_at"))?,
                closed_at: parse_optional_time(row.get("closed_at"))?,
            })
            .map_err(|_| ApplicationError::Internal)?;
            snapshot.sessions.push(session);
        }

        for row in sqlx::query(
            "SELECT event_id,run_id,kind,payload_json FROM result_outbox ORDER BY event_id",
        )
        .fetch_all(&mut self.connection)
        .await
        .map_err(map_sqlx)?
        {
            let event_id = parse_id(row.get("event_id"))?;
            let run_id = parse_id(row.get("run_id"))?;
            let payload = row.get("payload_json");
            let event = match row.get::<&str, _>("kind") {
                "run_started" => {
                    let payload: RunStartedPayload = decode(payload)?;
                    LocalEvent::RunStarted {
                        event_id,
                        run_id,
                        fencing_token: payload.fencing_token,
                    }
                }
                "delivery" => {
                    let payload: DeliveryPayload = decode(payload)?;
                    LocalEvent::Delivery {
                        event_id,
                        run_id,
                        sequence: payload.sequence,
                        outcome: payload.outcome,
                        fencing_token: payload.fencing_token,
                    }
                }
                "run_result" => {
                    let payload: RunResultPayload = decode(payload)?;
                    LocalEvent::RunResult {
                        event_id,
                        run_id,
                        status: payload.status,
                        item_outcomes: payload.item_outcomes,
                        continuation_note: payload.continuation_note,
                        error_code: payload.error_code,
                        fencing_token: payload.fencing_token,
                    }
                }
                _ => return Err(ApplicationError::Internal),
            };
            snapshot.events.insert(event_id, event);
        }
        Ok(snapshot)
    }

    async fn save(&mut self, snapshot: &Snapshot) -> Result<(), ApplicationError> {
        sqlx::query("DELETE FROM result_outbox")
            .execute(&mut self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("DELETE FROM run_deliveries")
            .execute(&mut self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("DELETE FROM provider_sessions")
            .execute(&mut self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("DELETE FROM local_runs")
            .execute(&mut self.connection)
            .await
            .map_err(map_sqlx)?;
        sqlx::query("DELETE FROM local_commands")
            .execute(&mut self.connection)
            .await
            .map_err(map_sqlx)?;

        for command in snapshot.commands.values() {
            sqlx::query(
                "INSERT INTO local_commands \
                 (command_id,computer_seq,fingerprint,payload_json,status,error_code) \
                 VALUES (?,?,?,?,?,?)",
            )
            .bind(command.id.to_string())
            .bind(i64::try_from(command.sequence).map_err(|_| ApplicationError::Internal)?)
            .bind(&command.fingerprint)
            .bind(encode(&command.command)?)
            .bind(command_status_name(command.status))
            .bind(command.error.as_ref().map(application_error_name))
            .execute(&mut self.connection)
            .await
            .map_err(map_sqlx)?;
        }
        for run in snapshot.runs.values() {
            let payload = RunPayload {
                priority: *run.view().priority,
                input: run.view().input.clone(),
                session: run.view().session,
                session_fingerprint: run.view().session_fingerprint.cloned(),
                notices: run.view().notices.clone(),
                terminal_status: run.view().terminal_status,
            };
            sqlx::query(
                "INSERT INTO local_runs \
                 (run_id,agent_id,task_id,focus_thread_id,fencing_token, \
                  ownership_lease_expires_at,state,run_json) VALUES (?,?,?,?,?,?,?,?)",
            )
            .bind(run.view().id.to_string())
            .bind(run.view().agent_id.to_string())
            .bind(run.view().task_id.map(|id| id.to_string()))
            .bind(run.view().focus_thread_id.to_string())
            .bind(run.view().fencing_token.expose())
            .bind(format_time(run.view().ownership_lease_expires_at)?)
            .bind(run_state_name(run.view().state))
            .bind(encode(&payload)?)
            .execute(&mut self.connection)
            .await
            .map_err(map_sqlx)?;
            for delivery in run.view().deliveries.values() {
                sqlx::query(
                    "INSERT INTO run_deliveries \
                     (run_id,delivery_seq,inbox_item_id,state,disposition,item_json) VALUES (?,?,?,?,?,?)",
                )
                .bind(run.view().id.to_string())
                .bind(i64::try_from(delivery.sequence).map_err(|_| ApplicationError::Internal)?)
                .bind(delivery.item.item_id.to_string())
                .bind(delivery_state_name(delivery.state))
                .bind(delivery.disposition.map(item_disposition_name))
                .bind(encode(&delivery.item)?)
                .execute(&mut self.connection)
                .await
                .map_err(map_sqlx)?;
            }
        }
        for session in &snapshot.sessions {
            let (scope_kind, scope_id) = session_scope_parts(session.view().scope);
            sqlx::query(
                "INSERT INTO provider_sessions \
                 (agent_id,scope_kind,scope_id,generation,driver_kind,provider_locator, \
                  workspace_fingerprint,role_revision,audience_fingerprint,state,created_at, \
                  last_resumed_at,closed_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)",
            )
            .bind(session.view().agent_id.to_string())
            .bind(scope_kind)
            .bind(scope_id)
            .bind(i64::try_from(session.view().generation).map_err(|_| ApplicationError::Internal)?)
            .bind(driver_kind_name(session.view().fingerprint.driver))
            .bind(session.view().locator)
            .bind(&session.view().fingerprint.workspace)
            .bind(
                i64::try_from(session.view().fingerprint.role_revision)
                    .map_err(|_| ApplicationError::Internal)?,
            )
            .bind(&session.view().fingerprint.audience)
            .bind(session_state_name(session.view().state))
            .bind(format_time(session.view().created_at)?)
            .bind(format_optional_time(session.view().last_resumed_at)?)
            .bind(format_optional_time(session.view().closed_at)?)
            .execute(&mut self.connection)
            .await
            .map_err(map_sqlx)?;
        }
        for event in snapshot.events.values() {
            let (run_id, kind, payload) = match event {
                LocalEvent::RunStarted {
                    run_id,
                    fencing_token,
                    ..
                } => (
                    *run_id,
                    "run_started",
                    encode(&RunStartedPayload {
                        fencing_token: fencing_token.clone(),
                    })?,
                ),
                LocalEvent::Delivery {
                    run_id,
                    sequence,
                    outcome,
                    fencing_token,
                    ..
                } => (
                    *run_id,
                    "delivery",
                    encode(&DeliveryPayload {
                        sequence: *sequence,
                        outcome: *outcome,
                        fencing_token: fencing_token.clone(),
                    })?,
                ),
                LocalEvent::RunResult {
                    run_id,
                    status,
                    item_outcomes,
                    continuation_note,
                    error_code,
                    fencing_token,
                    ..
                } => (
                    *run_id,
                    "run_result",
                    encode(&RunResultPayload {
                        status: *status,
                        item_outcomes: item_outcomes.clone(),
                        continuation_note: continuation_note.clone(),
                        error_code: *error_code,
                        fencing_token: fencing_token.clone(),
                    })?,
                ),
            };
            sqlx::query(
                "INSERT INTO result_outbox (event_id,run_id,kind,payload_json) VALUES (?,?,?,?)",
            )
            .bind(event.id().to_string())
            .bind(run_id.to_string())
            .bind(kind)
            .bind(payload)
            .execute(&mut self.connection)
            .await
            .map_err(map_sqlx)?;
        }
        Ok(())
    }
}

impl TransactionPort for SqliteAdapter {
    type Transaction = SqliteTransaction;

    async fn transact<T>(
        &mut self,
        operation: impl for<'a> AsyncFnOnce(&'a mut Self::Transaction) -> Result<T, ApplicationError>,
    ) -> Result<T, ApplicationError> {
        sqlx::query("BEGIN IMMEDIATE")
            .execute(&mut self.connection)
            .await
            .map_err(map_sqlx)?;
        let result = async {
            let mut transaction = SqliteTransaction {
                snapshot: self.load().await?,
            };
            let value = operation(&mut transaction).await?;
            self.save(&transaction.snapshot).await?;
            Ok(value)
        }
        .await;
        match result {
            Ok(value) => {
                sqlx::query("COMMIT")
                    .execute(&mut self.connection)
                    .await
                    .map_err(map_sqlx)?;
                Ok(value)
            }
            Err(error) => {
                let _ = sqlx::query("ROLLBACK").execute(&mut self.connection).await;
                Err(error)
            }
        }
    }
}

impl ComputerTransaction for SqliteTransaction {
    fn command(&mut self, id: CommandId) -> Result<Option<StoredCommand>, ApplicationError> {
        Ok(self.snapshot.commands.get(&id).cloned())
    }

    fn insert_command(&mut self, command: StoredCommand) -> Result<(), ApplicationError> {
        if self.snapshot.commands.contains_key(&command.id)
            || self
                .snapshot
                .commands
                .values()
                .any(|stored| stored.sequence == command.sequence)
        {
            return Err(ApplicationError::Conflict);
        }
        self.snapshot.commands.insert(command.id, command);
        Ok(())
    }

    fn save_command(&mut self, command: StoredCommand) -> Result<(), ApplicationError> {
        self.snapshot.commands.insert(command.id, command);
        Ok(())
    }

    fn pending_commands(&mut self) -> Result<Vec<StoredCommand>, ApplicationError> {
        Ok(self
            .snapshot
            .commands
            .values()
            .filter(|command| command.status == CommandStatus::Pending)
            .cloned()
            .collect())
    }

    fn run(&mut self, id: RunId) -> Result<Option<LocalRun>, ApplicationError> {
        Ok(self.snapshot.runs.get(&id).cloned())
    }

    fn save_run(&mut self, run: LocalRun) -> Result<(), ApplicationError> {
        self.snapshot.runs.insert(run.view().id, run);
        Ok(())
    }

    fn nonterminal_runs(&mut self) -> Result<Vec<LocalRun>, ApplicationError> {
        Ok(self
            .snapshot
            .runs
            .values()
            .filter(|run| !run.view().state.is_terminal())
            .cloned()
            .collect())
    }

    fn sessions(
        &mut self,
        agent_id: AgentId,
        scope: SessionScope,
    ) -> Result<Vec<ProviderSession>, ApplicationError> {
        Ok(self
            .snapshot
            .sessions
            .iter()
            .filter(|session| session.view().agent_id == agent_id && session.view().scope == scope)
            .cloned()
            .collect())
    }

    fn agent_sessions(
        &mut self,
        agent_id: AgentId,
    ) -> Result<Vec<ProviderSession>, ApplicationError> {
        Ok(self
            .snapshot
            .sessions
            .iter()
            .filter(|session| session.view().agent_id == agent_id)
            .cloned()
            .collect())
    }

    fn save_session(&mut self, session: ProviderSession) -> Result<(), ApplicationError> {
        if let Some(existing) = self.snapshot.sessions.iter_mut().find(|existing| {
            existing.view().agent_id == session.view().agent_id
                && existing.view().scope == session.view().scope
                && existing.view().generation == session.view().generation
        }) {
            *existing = session;
        } else {
            self.snapshot.sessions.push(session);
        }
        Ok(())
    }

    fn delete_session(
        &mut self,
        agent_id: AgentId,
        scope: SessionScope,
        generation: u64,
    ) -> Result<(), ApplicationError> {
        self.snapshot.sessions.retain(|session| {
            !(session.view().agent_id == agent_id
                && session.view().scope == scope
                && session.view().generation == generation)
        });
        Ok(())
    }

    fn append_event(&mut self, event: LocalEvent) -> Result<(), ApplicationError> {
        if self.snapshot.events.insert(event.id(), event).is_some() {
            return Err(ApplicationError::Conflict);
        }
        Ok(())
    }

    fn pending_events(&mut self) -> Result<Vec<LocalEvent>, ApplicationError> {
        Ok(self.snapshot.events.values().cloned().collect())
    }

    fn acknowledge_event(&mut self, event_id: EventId) -> Result<(), ApplicationError> {
        self.snapshot.events.remove(&event_id);
        Ok(())
    }
}

fn encode<T: serde::Serialize>(value: &T) -> Result<String, ApplicationError> {
    serde_json::to_string(value).map_err(|_| ApplicationError::Internal)
}

fn decode<T: serde::de::DeserializeOwned>(value: &str) -> Result<T, ApplicationError> {
    serde_json::from_str(value).map_err(|_| ApplicationError::Internal)
}

fn parse_id<T: std::str::FromStr>(value: &str) -> Result<T, ApplicationError> {
    value.parse().map_err(|_| ApplicationError::Internal)
}

fn format_time(value: OffsetDateTime) -> Result<String, ApplicationError> {
    value
        .format(&time::format_description::well_known::Rfc3339)
        .map_err(|_| ApplicationError::Internal)
}

fn format_optional_time(value: Option<OffsetDateTime>) -> Result<Option<String>, ApplicationError> {
    value.map(format_time).transpose()
}

fn parse_time(value: &str) -> Result<OffsetDateTime, ApplicationError> {
    OffsetDateTime::parse(value, &time::format_description::well_known::Rfc3339)
        .map_err(|_| ApplicationError::Internal)
}

fn parse_optional_time(value: Option<&str>) -> Result<Option<OffsetDateTime>, ApplicationError> {
    value.map(parse_time).transpose()
}

fn command_status(value: &str) -> Result<CommandStatus, ApplicationError> {
    match value {
        "pending" => Ok(CommandStatus::Pending),
        "applied" => Ok(CommandStatus::Applied),
        "rejected" => Ok(CommandStatus::Rejected),
        _ => Err(ApplicationError::Internal),
    }
}

fn command_status_name(value: CommandStatus) -> &'static str {
    match value {
        CommandStatus::Pending => "pending",
        CommandStatus::Applied => "applied",
        CommandStatus::Rejected => "rejected",
    }
}

fn application_error(value: &str) -> Result<ApplicationError, ApplicationError> {
    match value {
        "not_found" => Ok(ApplicationError::NotFound),
        "conflict" => Ok(ApplicationError::Conflict),
        "already_applied" => Ok(ApplicationError::AlreadyApplied),
        "driver_unavailable" => Ok(ApplicationError::DriverUnavailable),
        "session_lost" => Ok(ApplicationError::SessionLost),
        "internal" => Ok(ApplicationError::Internal),
        _ => Err(ApplicationError::Internal),
    }
}

fn application_error_name(value: &ApplicationError) -> &'static str {
    match value {
        ApplicationError::Core(_) => "internal",
        ApplicationError::NotFound => "not_found",
        ApplicationError::Unauthenticated => "unauthenticated",
        ApplicationError::Conflict => "conflict",
        ApplicationError::AlreadyApplied => "already_applied",
        ApplicationError::DriverUnavailable => "driver_unavailable",
        ApplicationError::SessionLost => "session_lost",
        ApplicationError::Internal => "internal",
    }
}

fn run_state_name(value: LocalRunState) -> &'static str {
    match value {
        LocalRunState::Queued => "queued",
        LocalRunState::Starting => "starting",
        LocalRunState::Running => "running",
        LocalRunState::Finalizing => "finalizing",
        LocalRunState::Stopping => "stopping",
        LocalRunState::Completed => "completed",
        LocalRunState::Yielded => "yielded",
        LocalRunState::Failed => "failed",
        LocalRunState::Canceled => "canceled",
    }
}

fn run_state(value: &str) -> Result<LocalRunState, ApplicationError> {
    match value {
        "queued" => Ok(LocalRunState::Queued),
        "starting" => Ok(LocalRunState::Starting),
        "running" => Ok(LocalRunState::Running),
        "finalizing" => Ok(LocalRunState::Finalizing),
        "stopping" => Ok(LocalRunState::Stopping),
        "completed" => Ok(LocalRunState::Completed),
        "yielded" => Ok(LocalRunState::Yielded),
        "failed" => Ok(LocalRunState::Failed),
        "canceled" => Ok(LocalRunState::Canceled),
        _ => Err(ApplicationError::Internal),
    }
}

fn delivery_state(value: &str) -> Result<DeliveryState, ApplicationError> {
    match value {
        "pending" => Ok(DeliveryState::Pending),
        "accepted" => Ok(DeliveryState::Accepted),
        "too_late" => Ok(DeliveryState::TooLate),
        "unsupported" => Ok(DeliveryState::Unsupported),
        _ => Err(ApplicationError::Internal),
    }
}

fn delivery_state_name(value: DeliveryState) -> &'static str {
    match value {
        DeliveryState::Pending => "pending",
        DeliveryState::Accepted => "accepted",
        DeliveryState::TooLate => "too_late",
        DeliveryState::Unsupported => "unsupported",
    }
}

fn item_disposition(value: &str) -> Result<ItemDisposition, ApplicationError> {
    match value {
        "handled" => Ok(ItemDisposition::Handled),
        "deferred" => Ok(ItemDisposition::Deferred),
        "released" => Ok(ItemDisposition::Released),
        _ => Err(ApplicationError::Internal),
    }
}

fn item_disposition_name(value: ItemDisposition) -> &'static str {
    match value {
        ItemDisposition::Handled => "handled",
        ItemDisposition::Deferred => "deferred",
        ItemDisposition::Released => "released",
    }
}

fn session_scope(kind: &str, id: &str) -> Result<SessionScope, ApplicationError> {
    match kind {
        "thread" => Ok(SessionScope::Thread(parse_id::<ThreadId>(id)?)),
        "task" => Ok(SessionScope::Task(parse_id::<TaskId>(id)?)),
        _ => Err(ApplicationError::Internal),
    }
}

fn session_scope_parts(scope: SessionScope) -> (&'static str, String) {
    match scope {
        SessionScope::Thread(id) => ("thread", id.to_string()),
        SessionScope::Task(id) => ("task", id.to_string()),
    }
}

fn driver_kind(value: &str) -> Result<DriverKind, ApplicationError> {
    match value {
        "codex" => Ok(DriverKind::Codex),
        "builtin" => Ok(DriverKind::Builtin),
        _ => Err(ApplicationError::Internal),
    }
}

fn driver_kind_name(value: DriverKind) -> &'static str {
    match value {
        DriverKind::Codex => "codex",
        DriverKind::Builtin => "builtin",
    }
}

fn session_state(value: &str) -> Result<SessionState, ApplicationError> {
    match value {
        "ready" => Ok(SessionState::Ready),
        "in_use" => Ok(SessionState::InUse),
        "closing" => Ok(SessionState::Closing),
        "closed" => Ok(SessionState::Closed),
        "lost" => Ok(SessionState::Lost),
        _ => Err(ApplicationError::Internal),
    }
}

fn session_state_name(value: SessionState) -> &'static str {
    match value {
        SessionState::Ready => "ready",
        SessionState::InUse => "in_use",
        SessionState::Closing => "closing",
        SessionState::Closed => "closed",
        SessionState::Lost => "lost",
    }
}

fn map_sqlx(error: sqlx::Error) -> ApplicationError {
    let error_kind = match &error {
        sqlx::Error::Database(_) => "database",
        sqlx::Error::Io(_) => "io",
        sqlx::Error::Tls(_) => "tls",
        sqlx::Error::Protocol(_) => "protocol",
        sqlx::Error::RowNotFound => "row_not_found",
        sqlx::Error::TypeNotFound { .. } => "type_not_found",
        sqlx::Error::ColumnIndexOutOfBounds { .. } => "column_index",
        sqlx::Error::ColumnNotFound(_) => "column_not_found",
        sqlx::Error::ColumnDecode { .. } => "column_decode",
        sqlx::Error::Decode(_) => "decode",
        sqlx::Error::AnyDriverError(_) => "driver",
        sqlx::Error::PoolTimedOut => "pool_timeout",
        sqlx::Error::PoolClosed => "pool_closed",
        sqlx::Error::WorkerCrashed => "worker_crashed",
        sqlx::Error::Migrate(_) => "migration",
        _ => "other",
    };
    let database_code = error
        .as_database_error()
        .and_then(|database| database.code())
        .map(|code| code.into_owned())
        .unwrap_or_else(|| "none".to_owned());
    tracing::error!(error_kind, database_code, "local SQLite operation failed");
    ApplicationError::Internal
}

#[cfg(test)]
mod tests {
    use time::{Duration, OffsetDateTime};
    use uuid::Uuid;

    use super::*;
    use crate::computer::application::{
        AgentInput, ClaimedItemInput, ContextMessageInput, DriverKind, FencingToken, LocalRun,
        NewRun, ProviderSession, ProviderSessionSnapshot, RunContextInput, RunInput, RunPriority,
        SessionFingerprint, SessionScope, SessionState, WorkInput, WorkStrength,
        command::Command,
        ports::{CommandStatus, LocalEvent, StoredCommand},
    };
    use crate::ids::{CommandId, EventId, InboxItemId};

    #[tokio::test]
    async fn empty_directory_creates_wal_schema_and_survives_reopen() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("computer/daemon.db");
        let run = test_run();
        let run_id = run.view().id;
        let mut adapter = SqliteAdapter::open(&path).await.unwrap();
        let journal: String = sqlx::query_scalar("PRAGMA journal_mode")
            .fetch_one(&mut adapter.connection)
            .await
            .unwrap();
        let foreign_keys: i64 = sqlx::query_scalar("PRAGMA foreign_keys")
            .fetch_one(&mut adapter.connection)
            .await
            .unwrap();
        assert_eq!(journal, "wal");
        assert_eq!(foreign_keys, 1);
        adapter
            .transact(async |transaction| transaction.save_run(run))
            .await
            .unwrap();
        drop(adapter);

        let mut reopened = SqliteAdapter::open(&path).await.unwrap();
        let stored = reopened
            .transact(async |transaction| transaction.run(run_id))
            .await
            .unwrap();
        assert_eq!(stored.unwrap().view().id, run_id);
    }

    #[tokio::test]
    async fn failed_operation_rolls_back_local_state_and_outbox_together() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("daemon.db");
        let mut adapter = SqliteAdapter::open(&path).await.unwrap();
        let run = test_run();
        let run_id = run.view().id;
        let event_id = EventId::from_uuid(Uuid::now_v7());

        let result = adapter
            .transact(async |transaction| {
                transaction.save_run(run)?;
                transaction.append_event(LocalEvent::RunStarted {
                    event_id,
                    run_id,
                    fencing_token: FencingToken::new("secret".to_owned()),
                })?;
                Err::<(), _>(ApplicationError::Conflict)
            })
            .await;
        assert_eq!(result, Err(ApplicationError::Conflict));
        let (stored_run, events) = adapter
            .transact(async |transaction| {
                Ok((transaction.run(run_id)?, transaction.pending_events()?))
            })
            .await
            .unwrap();
        assert!(stored_run.is_none());
        assert!(events.is_empty());
    }

    #[tokio::test]
    async fn run_delivery_and_fencing_token_are_restored_without_debug_exposure() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("daemon.db");
        let mut adapter = SqliteAdapter::open(&path).await.unwrap();
        let run = test_run();
        let run_id = run.view().id;
        adapter
            .transact(async |transaction| transaction.save_run(run))
            .await
            .unwrap();
        drop(adapter);

        let mut reopened = SqliteAdapter::open(&path).await.unwrap();
        let restored = reopened
            .transact(async |transaction| transaction.run(run_id))
            .await
            .unwrap()
            .unwrap();
        assert_eq!(restored.view().fencing_token.expose(), "secret");
        assert!(!format!("{restored:?}").contains("secret"));
    }

    #[tokio::test]
    async fn run_with_initial_claimed_item_survives_reopen() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("daemon.db");
        let (run, item_id) = test_run_with_claimed_item();
        let run_id = run.view().id;
        let mut adapter = SqliteAdapter::open(&path).await.unwrap();
        adapter
            .transact(async |transaction| transaction.save_run(run))
            .await
            .unwrap();
        drop(adapter);

        let mut reopened = SqliteAdapter::open(&path).await.unwrap();
        let restored = reopened
            .transact(async |transaction| transaction.run(run_id))
            .await
            .unwrap()
            .unwrap();
        let delivery = restored.view().deliveries.get(&1).unwrap();
        assert_eq!(delivery.item.item_id, item_id);
        assert_eq!(delivery.sequence, 1);
    }

    #[tokio::test]
    async fn start_command_with_claimed_item_can_be_marked_applied_after_run_is_saved() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("daemon.db");
        let (run, item_id) = test_run_with_claimed_item();
        let run_id = run.view().id;
        let command_id = CommandId::from_uuid(Uuid::now_v7());
        let command = Command::Start {
            run: Box::new(run.clone()),
            fingerprint: test_fingerprint(),
        };
        let mut adapter = SqliteAdapter::open(&path).await.unwrap();
        adapter
            .transact(async |transaction| {
                transaction.insert_command(StoredCommand {
                    id: command_id,
                    sequence: 1,
                    fingerprint: "fingerprint".to_owned(),
                    command,
                    status: CommandStatus::Pending,
                    error: None,
                })
            })
            .await
            .unwrap();
        adapter
            .transact(async |transaction| transaction.save_run(run))
            .await
            .unwrap();
        adapter
            .transact(async |transaction| {
                let mut stored = transaction.command(command_id)?.unwrap();
                stored.status = CommandStatus::Applied;
                transaction.save_command(stored)
            })
            .await
            .unwrap();
        drop(adapter);

        let mut reopened = SqliteAdapter::open(&path).await.unwrap();
        let (stored, restored) = reopened
            .transact(async |transaction| {
                Ok((transaction.command(command_id)?, transaction.run(run_id)?))
            })
            .await
            .unwrap();
        assert_eq!(stored.unwrap().status, CommandStatus::Applied);
        assert_eq!(
            restored.unwrap().view().deliveries[&1].item.item_id,
            item_id
        );
    }

    #[tokio::test]
    async fn rejects_run_when_an_initial_claimed_item_delivery_is_missing() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("daemon.db");
        let (run, _) = test_run_with_claimed_item();
        let run_id = run.view().id;
        let mut adapter = SqliteAdapter::open(&path).await.unwrap();
        adapter
            .transact(async |transaction| transaction.save_run(run))
            .await
            .unwrap();
        sqlx::query("DELETE FROM run_deliveries WHERE run_id=?")
            .bind(run_id.to_string())
            .execute(&mut adapter.connection)
            .await
            .unwrap();

        let result = adapter
            .transact(async |transaction| transaction.run(run_id))
            .await;
        assert_eq!(result, Err(ApplicationError::Internal));
    }

    #[tokio::test]
    async fn rejects_run_delivery_when_column_id_differs_from_json() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("daemon.db");
        let mut adapter = SqliteAdapter::open(&path).await.unwrap();
        let run = test_run();
        let run_id = run.view().id;
        let thread_id = run.view().focus_thread_id;
        let json_item_id = InboxItemId::from_uuid(Uuid::now_v7());
        let column_item_id = InboxItemId::from_uuid(Uuid::now_v7());
        let item = ClaimedItemInput {
            item_id: json_item_id,
            task_id: None,
            thread_id,
            message_id: None,
            content: Some("item".to_owned()),
        };
        adapter
            .transact(async |transaction| transaction.save_run(run))
            .await
            .unwrap();
        sqlx::query(
            "INSERT INTO run_deliveries \
             (run_id,delivery_seq,inbox_item_id,state,disposition,item_json) VALUES (?,?,?,?,?,?)",
        )
        .bind(run_id.to_string())
        .bind(1_i64)
        .bind(column_item_id.to_string())
        .bind("pending")
        .bind(None::<&str>)
        .bind(serde_json::to_string(&item).unwrap())
        .execute(&mut adapter.connection)
        .await
        .unwrap();

        let result = adapter
            .transact(async |transaction| transaction.run(run_id))
            .await;
        assert_eq!(result, Err(ApplicationError::Internal));
    }

    #[test]
    fn command_deserialization_rejects_invalid_embedded_run() {
        let command = Command::Start {
            run: Box::new(test_run()),
            fingerprint: SessionFingerprint {
                driver: DriverKind::Builtin,
                workspace: "workspace".to_owned(),
                role_revision: 1,
                audience: "audience".to_owned(),
            },
        };
        let mut value = serde_json::to_value(command).unwrap();
        value["run"]["state"] = serde_json::json!("Completed");
        value["run"]["terminal_status"] = serde_json::Value::Null;
        let result = serde_json::from_value::<Command>(value);
        assert!(result.is_err());
    }

    #[test]
    fn local_run_deserialization_rejects_empty_token_and_missing_initial_delivery() {
        let run = test_run();
        let focus_thread_id = run.view().focus_thread_id;
        let mut empty_token = serde_json::to_value(&run).unwrap();
        empty_token["fencing_token"] = serde_json::json!("");
        assert!(serde_json::from_value::<LocalRun>(empty_token).is_err());

        let mut missing_delivery = serde_json::to_value(run).unwrap();
        missing_delivery["input"]["context"]["claimed_items"] = serde_json::json!([{
            "item_id": InboxItemId::from_uuid(Uuid::now_v7()).to_string(),
            "task_id": null,
            "thread_id": focus_thread_id.to_string(),
            "content": "item"
        }]);
        assert!(serde_json::from_value::<LocalRun>(missing_delivery).is_err());
    }

    #[test]
    fn provider_session_snapshot_debug_redacts_locator_and_rehydrate_checks_time_order() {
        let created_at = OffsetDateTime::now_utc();
        let snapshot = ProviderSessionSnapshot {
            agent_id: AgentId::from_uuid(Uuid::now_v7()),
            scope: SessionScope::Thread(ThreadId::from_uuid(Uuid::now_v7())),
            generation: 1,
            locator: "provider-secret-locator".to_owned(),
            fingerprint: SessionFingerprint {
                driver: DriverKind::Builtin,
                workspace: "workspace".to_owned(),
                role_revision: 1,
                audience: "audience".to_owned(),
            },
            state: SessionState::Closed,
            created_at,
            last_resumed_at: Some(created_at),
            closed_at: Some(created_at - time::Duration::seconds(1)),
        };
        assert!(!format!("{snapshot:?}").contains("provider-secret-locator"));
        assert!(ProviderSession::rehydrate(snapshot).is_err());
    }

    fn test_run() -> LocalRun {
        test_run_with_optional_item(None)
    }

    fn test_run_with_claimed_item() -> (LocalRun, InboxItemId) {
        let item_id = InboxItemId::from_uuid(Uuid::now_v7());
        (test_run_with_optional_item(Some(item_id)), item_id)
    }

    fn test_run_with_optional_item(item_id: Option<InboxItemId>) -> LocalRun {
        let agent_id = AgentId::from_uuid(Uuid::now_v7());
        let thread_id = ThreadId::from_uuid(Uuid::now_v7());
        let claimed_items = item_id
            .map(|item_id| ClaimedItemInput {
                item_id,
                task_id: None,
                thread_id,
                message_id: None,
                content: Some("item".to_owned()),
            })
            .into_iter()
            .collect();
        LocalRun::new(NewRun {
            id: RunId::from_uuid(Uuid::now_v7()),
            agent_id,
            task_id: None,
            focus_thread_id: thread_id,
            fencing_token: FencingToken::new("secret".to_owned()),
            priority: RunPriority {
                explicit_human_redirect: false,
                strength: WorkStrength::Hard,
                available_at: OffsetDateTime::now_utc(),
                has_task_continuity: false,
            },
            ownership_lease_expires_at: OffsetDateTime::now_utc() + Duration::minutes(5),
            input: RunInput {
                global_contract: "contract".to_owned(),
                agent: AgentInput {
                    agent_id,
                    space_id: crate::ids::SpaceId::from_uuid(Uuid::nil()),
                    identity: "agent".to_owned(),
                    role_revision: 1,
                    role: "role".to_owned(),
                    memory: Vec::new(),
                },
                work: WorkInput {
                    task: None,
                    linked_thread_ids: vec![thread_id],
                    public_result_message_id: None,
                },
                context: RunContextInput {
                    focus_thread_id: thread_id,
                    message_snapshot_sequence: 1,
                    focus_messages: vec![ContextMessageInput {
                        message_id: crate::ids::MessageId::from_uuid(Uuid::now_v7()),
                        author_member_id: crate::ids::MemberId::from_uuid(Uuid::now_v7()),
                        body: "body".to_owned(),
                    }],
                    claimed_items,
                },
            },
        })
        .unwrap()
    }

    fn test_fingerprint() -> SessionFingerprint {
        SessionFingerprint {
            driver: DriverKind::Builtin,
            workspace: "workspace".to_owned(),
            role_revision: 1,
            audience: "audience".to_owned(),
        }
    }
}
