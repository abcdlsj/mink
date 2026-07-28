use std::{path::Path, time::Duration};

use anyhow::{Context, Result, bail, ensure};
use sha2::{Digest, Sha256};
use sqlx::SqlitePool;
use time::OffsetDateTime;
use tokio::sync::mpsc;
use tokio_tungstenite::tungstenite;
use tokio_util::sync::CancellationToken;
use uuid::Uuid;

use crate::{
    agent_config::SuspendMode,
    computer_protocol::{
        AgentMemoryReadCommand, CommandResult, ComputerCommand, ComputerFrame, MemoryFileMetadata,
        RunResultStatus,
    },
    supervisor::{RunCommand, RunResult, Supervisor},
};

use super::{
    ConnectionTaskExit,
    connection::{ReceivedCommand, queue_computer_frame, send_ws_frame},
    credentials::{ensure_secure_permissions, set_permissions},
};

pub(super) async fn prepare_agent_root(state_dir: &Path) -> Result<()> {
    let agents = state_dir.join("agents");
    if !agents.exists() {
        tokio::fs::create_dir(&agents).await?;
        set_permissions(&agents, 0o700).await?;
    }
    let metadata = tokio::fs::metadata(&agents).await?;
    ensure_secure_permissions(&agents, &metadata, 0o700, "Computer Agents directory")
}

pub(super) async fn prepare_agent_home(
    state_dir: &Path,
    agent_id: Uuid,
) -> Result<std::path::PathBuf> {
    let home = state_dir.join("agents").join(agent_id.to_string());
    tokio::fs::create_dir_all(&home).await?;
    set_permissions(&home, 0o700).await?;
    for relative in [
        "memory",
        "workspace",
        "drivers/codex",
        "drivers/builtin",
        "runs",
        "logs",
    ] {
        let path = home.join(relative);
        tokio::fs::create_dir_all(&path).await?;
        set_permissions(&path, 0o700).await?;
    }
    Ok(home)
}

pub(super) async fn command_processor_task(
    processor: LocalCommandProcessor,
    mut commands: mpsc::Receiver<ReceivedCommand>,
    outgoing: mpsc::Sender<tungstenite::Message>,
    computer_id: Uuid,
    cancellation: CancellationToken,
) -> Result<ConnectionTaskExit> {
    loop {
        let command = tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            command = commands.recv() => command,
        };
        let Some(command) = command else {
            return Ok(ConnectionTaskExit::Disconnected);
        };
        let kind = command.command.kind();
        let outcome = tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            outcome = processor.process(
                command.command_id,
                command.computer_seq,
                &command.command,
            ) => outcome?,
        };
        if let Some(outcome) = outcome {
            let error_code = command_error_code(&outcome).to_owned();
            let ok = outcome.ok;
            queue_computer_frame(
                &outgoing,
                &ComputerFrame::CommandResult {
                    command_id: command.command_id,
                    computer_seq: command.computer_seq,
                    ok,
                    result: outcome.result,
                },
            )
            .await?;
            tracing::info!(
                computer_id = %computer_id,
                command_id = %command.command_id,
                computer_seq = command.computer_seq,
                kind,
                ok,
                error_code,
                "Computer command result sent"
            );
        }
    }
}

pub(super) async fn result_sender_task(
    database: SqlitePool,
    computer_id: Uuid,
    outgoing: mpsc::Sender<tungstenite::Message>,
    mut completions: mpsc::Receiver<()>,
    cancellation: CancellationToken,
) -> Result<ConnectionTaskExit> {
    let mut retry = tokio::time::interval(Duration::from_secs(1));
    retry.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    retry.tick().await;
    let mut sink = Box::pin(futures_util::sink::unfold(
        outgoing,
        |outgoing, message| async move {
            outgoing
                .send(message)
                .await
                .map_err(|_| tungstenite::Error::ConnectionClosed)?;
            Ok::<_, tungstenite::Error>(outgoing)
        },
    ));
    loop {
        tokio::select! {
            _ = cancellation.cancelled() => return Ok(ConnectionTaskExit::Cancelled),
            _ = retry.tick() => {
                send_pending_run_event(&mut sink, &database, computer_id).await?;
            }
            completion = completions.recv() => {
                if completion.is_none() {
                    return Ok(ConnectionTaskExit::Disconnected);
                }
                send_pending_run_event(&mut sink, &database, computer_id).await?;
            }
        }
    }
}

pub(super) async fn send_pending_run_event<S>(
    writer: &mut S,
    database: &SqlitePool,
    computer_id: Uuid,
) -> Result<()>
where
    S: futures_util::Sink<tungstenite::Message, Error = tungstenite::Error> + Unpin,
{
    if send_pending_run_started(writer, database, computer_id).await? {
        return Ok(());
    }
    send_pending_run_result(writer, database, computer_id).await
}

pub(super) async fn send_pending_run_started<S>(
    writer: &mut S,
    database: &SqlitePool,
    computer_id: Uuid,
) -> Result<bool>
where
    S: futures_util::Sink<tungstenite::Message, Error = tungstenite::Error> + Unpin,
{
    let now = OffsetDateTime::now_utc();
    let pending: Option<(String, String, String, i64, String, String, i64)> = sqlx::query_as(
        "SELECT outbox.event_id, outbox.run_id, runs.fencing_token, outbox.run_attempt, \
                outbox.process_instance_id, outbox.daemon_observed_at, outbox.attempt_count \
         FROM run_started_outbox outbox JOIN local_agent_runs runs ON runs.run_id = outbox.run_id \
         WHERE outbox.reported_at IS NULL AND outbox.next_attempt_at <= ?1 \
         ORDER BY next_attempt_at, event_id LIMIT 1",
    )
    .bind(now.to_string())
    .fetch_optional(database)
    .await?;
    let Some((
        event_id,
        run_id,
        fencing_token,
        run_attempt,
        process_instance_id,
        observed_at,
        attempt_count,
    )) = pending
    else {
        return Ok(false);
    };
    let run_id = Uuid::parse_str(&run_id)?;
    let process_instance_id = Uuid::parse_str(&process_instance_id)?;
    let daemon_observed_at =
        OffsetDateTime::parse(&observed_at, &time::format_description::well_known::Rfc3339)?;
    send_ws_frame(
        writer,
        &ComputerFrame::RunStarted {
            event_id: event_id.clone(),
            run_id,
            fencing_token,
            run_attempt,
            process_instance_id,
            daemon_observed_at,
        },
    )
    .await?;
    let retry_seconds = 1_i64 << attempt_count.min(5);
    sqlx::query(
        "UPDATE run_started_outbox SET attempt_count = attempt_count + 1, next_attempt_at = ?2 \
         WHERE event_id = ?1 AND reported_at IS NULL",
    )
    .bind(&event_id)
    .bind((now + time::Duration::seconds(retry_seconds)).to_string())
    .execute(database)
    .await?;
    tracing::info!(computer_id = %computer_id, run_id = %run_id, event_id, attempt = attempt_count + 1, "Agent run started event sent");
    Ok(true)
}

pub(super) async fn send_pending_run_result<S>(
    writer: &mut S,
    database: &SqlitePool,
    computer_id: Uuid,
) -> Result<()>
where
    S: futures_util::Sink<tungstenite::Message, Error = tungstenite::Error> + Unpin,
{
    let now = OffsetDateTime::now_utc();
    let pending: Option<(String, String, String, i64, String, i64)> = sqlx::query_as(
        "SELECT outbox.event_id, runs.fencing_token, outbox.command_id, outbox.computer_seq, \
                outbox.payload_json, outbox.attempt_count FROM run_result_outbox outbox \
         JOIN local_agent_runs runs ON runs.run_id = outbox.run_id \
         WHERE outbox.reported_at IS NULL AND outbox.next_attempt_at <= ?1 \
         ORDER BY next_attempt_at, event_id LIMIT 1",
    )
    .bind(now.to_string())
    .fetch_optional(database)
    .await?;
    let Some((event_id, fencing_token, command_id, computer_seq, payload, attempt_count)) = pending
    else {
        return Ok(());
    };
    let command_id = Uuid::parse_str(&command_id)?;
    let result: CommandResult = serde_json::from_str(&payload)?;
    let ok = matches!(result.status, Some(RunResultStatus::Completed));
    send_ws_frame(
        writer,
        &ComputerFrame::RunResult {
            event_id: event_id.clone(),
            fencing_token,
            command_id,
            computer_seq,
            ok,
            result,
        },
    )
    .await?;
    let retry_seconds = 1_i64 << attempt_count.min(5);
    sqlx::query(
        "UPDATE run_result_outbox SET attempt_count = attempt_count + 1, \
         next_attempt_at = ?2, last_error = NULL \
         WHERE event_id = ?1 AND reported_at IS NULL",
    )
    .bind(&event_id)
    .bind((now + time::Duration::seconds(retry_seconds)).to_string())
    .execute(database)
    .await?;
    tracing::info!(computer_id = %computer_id, command_id = %command_id, event_id, attempt = attempt_count + 1, "Agent run result sent");
    Ok(())
}

pub(super) struct LocalCommandOutcome {
    pub(super) ok: bool,
    pub(super) result: CommandResult,
}

pub(super) fn command_error_code(outcome: &LocalCommandOutcome) -> &str {
    outcome.result.error_code.as_deref().unwrap_or("none")
}

pub(super) struct LocalCommandProcessor {
    pub(super) database: SqlitePool,
    pub(super) state_dir: std::path::PathBuf,
    pub(super) supervisor: Supervisor,
    pub(super) completion_tx: tokio::sync::mpsc::Sender<()>,
}

impl LocalCommandProcessor {
    pub(super) async fn process(
        &self,
        command_id: Uuid,
        computer_seq: i64,
        command: &ComputerCommand,
    ) -> Result<Option<LocalCommandOutcome>> {
        let kind = command.kind();
        let existing: (String, Option<String>) =
            sqlx::query_as("SELECT status, result_json FROM server_commands WHERE command_id = ?1")
                .bind(command_id.to_string())
                .fetch_one(&self.database)
                .await?;
        if matches!(existing.0.as_str(), "completed" | "failed") {
            tracing::info!(command_id = %command_id, computer_seq, kind, replayed = true, status = %existing.0, "Computer command replayed from local result");
            if matches!(command, ComputerCommand::Run(_)) {
                return Ok(None);
            }
            return Ok(Some(LocalCommandOutcome {
                ok: existing.0 == "completed",
                result: serde_json::from_str(existing.1.as_deref().unwrap_or("{}"))?,
            }));
        }
        if existing.0 == "running" {
            tracing::info!(command_id = %command_id, computer_seq, kind, replayed = true, status = "running", "Computer command already running");
            return Ok(None);
        }
        if let ComputerCommand::MemoryRead(payload) = command {
            let outcome = match read_memory_file(&self.state_dir, payload).await {
                Ok(file) => LocalCommandOutcome {
                    ok: true,
                    result: file,
                },
                Err(_) => LocalCommandOutcome {
                    ok: false,
                    result: failed_command_result(),
                },
            };
            finish_local_command_with_result(
                &self.database,
                command_id,
                &outcome,
                &CommandResult {
                    ok: Some(outcome.ok),
                    ..CommandResult::default()
                },
            )
            .await?;
            return Ok(Some(outcome));
        }
        if let ComputerCommand::Run(run) = command {
            sqlx::query("UPDATE server_commands SET status = 'running' WHERE command_id = ?1")
                .bind(command_id.to_string())
                .execute(&self.database)
                .await?;
            let result = self
                .supervisor
                .start(
                    run.clone(),
                    RunCommand {
                        command_id,
                        computer_seq,
                    },
                )
                .await;
            match result {
                Ok(receiver) => {
                    let completion_tx = self.completion_tx.clone();
                    tokio::spawn(async move {
                        let _result = receiver.await.unwrap_or(RunResult {
                            run_id: Uuid::nil(),
                            status: "failed".to_owned(),
                            error_code: Some("supervisor_stopped".to_owned()),
                            memory_files: Vec::new(),
                        });
                        completion_tx.send(()).await.ok();
                    });
                    return Ok(None);
                }
                Err(_) => {
                    let outcome = LocalCommandOutcome {
                        ok: false,
                        result: failed_command_result(),
                    };
                    finish_local_command(&self.database, command_id, &outcome).await?;
                    return Ok(Some(outcome));
                }
            }
        }
        if let ComputerCommand::Cancel(payload) = command {
            let result = self.supervisor.cancel(payload.run_id).await;
            let outcome = match result {
                Ok(()) => LocalCommandOutcome {
                    ok: true,
                    result: successful_command_result(None),
                },
                Err(_) => LocalCommandOutcome {
                    ok: false,
                    result: failed_command_result(),
                },
            };
            finish_local_command(&self.database, command_id, &outcome).await?;
            return Ok(Some(outcome));
        }
        let result = execute_local_command(&self.state_dir, command, &self.supervisor).await;
        let result = validate_provision_result(result, command, &self.supervisor).await;
        let outcome = match result {
            Ok(memory_files) => LocalCommandOutcome {
                ok: true,
                result: successful_command_result(Some(memory_files)),
            },
            Err(_) => LocalCommandOutcome {
                ok: false,
                result: failed_command_result(),
            },
        };
        finish_local_command(&self.database, command_id, &outcome).await?;
        Ok(Some(outcome))
    }
}

pub(super) async fn finish_local_command(
    database: &SqlitePool,
    command_id: Uuid,
    outcome: &LocalCommandOutcome,
) -> Result<()> {
    finish_local_command_with_result(database, command_id, outcome, &outcome.result).await
}

pub(super) async fn finish_local_command_with_result(
    database: &SqlitePool,
    command_id: Uuid,
    outcome: &LocalCommandOutcome,
    stored_result: &CommandResult,
) -> Result<()> {
    let status = if outcome.ok { "completed" } else { "failed" };
    sqlx::query(
        "UPDATE server_commands SET status = ?2, result_json = ?3, completed_at = ?4 \
         WHERE command_id = ?1",
    )
    .bind(command_id.to_string())
    .bind(status)
    .bind(serde_json::to_string(stored_result)?)
    .bind(OffsetDateTime::now_utc().to_string())
    .execute(database)
    .await?;
    Ok(())
}

pub(super) const COMMAND_FAILED_ERROR_CODE: &str = "command_failed";

pub(super) fn successful_command_result(
    memory_files: Option<Vec<MemoryFileMetadata>>,
) -> CommandResult {
    CommandResult {
        ok: Some(true),
        memory_files,
        ..CommandResult::default()
    }
}

pub(super) fn failed_command_result() -> CommandResult {
    CommandResult {
        ok: Some(false),
        error_code: Some(COMMAND_FAILED_ERROR_CODE.to_owned()),
        ..CommandResult::default()
    }
}

pub(super) async fn execute_local_command(
    state_dir: &Path,
    command: &ComputerCommand,
    supervisor: &Supervisor,
) -> Result<Vec<MemoryFileMetadata>> {
    let (configuration, lifecycle, provision) = match command {
        ComputerCommand::Provision(configuration) => (configuration, "active", true),
        ComputerCommand::Configure(configuration) => (configuration, "unchanged", false),
        ComputerCommand::Suspend(configuration) => (configuration, "suspended", false),
        ComputerCommand::Resume(configuration) => (configuration, "active", false),
        ComputerCommand::Retire(configuration) => (configuration, "retired", false),
        _ => bail!("unsupported local command: {}", command.kind()),
    };
    ensure!(
        configuration.driver_config.schema_version == 1,
        "unsupported Agent driver configuration schema"
    );
    let agent_id = configuration.agent_id;
    let home = if provision {
        ensure!(
            matches!(configuration.driver_kind.as_str(), "codex" | "builtin"),
            "agent.provision command has unknown driver_kind"
        );
        let home = prepare_agent_home(state_dir, agent_id).await?;
        supervisor
            .prepare_agent_driver(agent_id, &configuration.driver_kind)
            .await?;
        home
    } else {
        let home = state_dir.join("agents").join(agent_id.to_string());
        ensure!(home.is_dir(), "Agent Home is unavailable");
        home
    };
    let profile_path = home.join("profile.json");
    let mut profile = if provision {
        serde_json::json!({
            "schema_version": 1,
            "agent_id": agent_id,
            "space_id": configuration.space_id,
            "name": configuration.name,
            "handle": configuration.handle,
            "driver_kind": configuration.driver_kind,
            "driver_config": configuration.driver_config,
        })
    } else {
        let profile: serde_json::Value =
            serde_json::from_slice(&tokio::fs::read(&profile_path).await?)?;
        ensure!(
            profile
                .get("schema_version")
                .and_then(serde_json::Value::as_u64)
                == Some(1),
            "unsupported Agent profile schema"
        );
        profile
    };
    let profile = profile
        .as_object_mut()
        .context("Agent profile must be a JSON object")?;
    profile.insert(
        "role_text".to_owned(),
        serde_json::to_value(&configuration.role_text)?,
    );
    profile.insert(
        "role_revision".to_owned(),
        serde_json::to_value(configuration.role_revision)?,
    );
    profile.insert(
        "attention_config".to_owned(),
        serde_json::to_value(&configuration.attention_config)?,
    );
    let desired_lifecycle = if lifecycle == "unchanged" {
        profile
            .get("desired_lifecycle")
            .and_then(serde_json::Value::as_str)
            .context("Agent profile has no desired lifecycle")?
    } else {
        lifecycle
    };
    profile.insert(
        "desired_lifecycle".to_owned(),
        serde_json::json!(desired_lifecycle),
    );
    profile.insert("provision_status".to_owned(), serde_json::json!("ready"));
    write_restricted_file_atomic(&profile_path, &serde_json::to_vec_pretty(&profile)?).await?;
    let memory = home.join("memory/MEMORY.md");
    if !memory.exists() {
        write_restricted_file(&memory, b"# Memory\n").await?;
    }
    if matches!(
        command,
        ComputerCommand::Suspend(_) | ComputerCommand::Retire(_)
    ) && (matches!(command, ComputerCommand::Retire(_))
        || matches!(configuration.mode, Some(SuspendMode::CancelNow)))
    {
        supervisor.cancel_agent(agent_id).await?;
    }
    scan_memory(&home.join("memory")).await
}

pub(super) async fn scan_memory(root: &Path) -> Result<Vec<MemoryFileMetadata>> {
    let mut pending = vec![root.to_owned()];
    let mut files = Vec::new();
    while let Some(directory) = pending.pop() {
        let mut entries = tokio::fs::read_dir(&directory).await?;
        while let Some(entry) = entries.next_entry().await? {
            let file_type = entry.file_type().await?;
            if file_type.is_symlink() {
                continue;
            }
            if file_type.is_dir() {
                pending.push(entry.path());
                continue;
            }
            if !file_type.is_file() {
                continue;
            }
            let bytes = tokio::fs::read(entry.path()).await?;
            let metadata = entry.metadata().await?;
            let path = entry.path();
            let relative = path
                .strip_prefix(root)?
                .to_str()
                .context("Memory path is not UTF-8")?;
            let modified = metadata.modified()?;
            files.push(MemoryFileMetadata {
                path: relative.to_owned(),
                size: metadata.len(),
                sha256: hex::encode(Sha256::digest(&bytes)),
                updated_at: OffsetDateTime::from(modified),
            });
        }
    }
    files.sort_by(|left, right| left.path.cmp(&right.path));
    Ok(files)
}

pub(super) async fn read_memory_file(
    state_dir: &Path,
    command: &AgentMemoryReadCommand,
) -> Result<CommandResult> {
    const MAX_MEMORY_READ_BYTES: u64 = 1024 * 1024;
    let agent_id = command.agent_id;
    let relative = command.path.as_str();
    let relative_path = Path::new(relative);
    ensure!(
        !relative.is_empty()
            && relative_path
                .components()
                .all(|component| matches!(component, std::path::Component::Normal(_))),
        "Memory path is invalid"
    );
    let root = state_dir
        .join("agents")
        .join(agent_id.to_string())
        .join("memory");
    let canonical_root = tokio::fs::canonicalize(&root).await?;
    let mut candidate = root;
    for component in relative_path.components() {
        let std::path::Component::Normal(component) = component else {
            bail!("Memory path is invalid");
        };
        candidate.push(component);
        ensure!(
            !tokio::fs::symlink_metadata(&candidate)
                .await?
                .file_type()
                .is_symlink(),
            "Memory path cannot contain a symlink"
        );
    }
    let canonical = tokio::fs::canonicalize(&candidate).await?;
    ensure!(
        canonical.starts_with(&canonical_root),
        "Memory path escapes Agent Home"
    );
    let metadata = tokio::fs::metadata(&canonical).await?;
    ensure!(metadata.is_file(), "Memory path is not a regular file");
    ensure!(
        metadata.len() <= MAX_MEMORY_READ_BYTES,
        "Memory file is too large"
    );
    let bytes = tokio::fs::read(&canonical).await?;
    let content = String::from_utf8(bytes.clone()).context("Memory file is not UTF-8")?;
    Ok(CommandResult {
        path: Some(relative.to_owned()),
        content: Some(content),
        size: Some(metadata.len()),
        sha256: Some(hex::encode(Sha256::digest(&bytes))),
        updated_at: Some(OffsetDateTime::from(metadata.modified()?)),
        ..CommandResult::default()
    })
}

pub(super) async fn write_restricted_file(path: &Path, bytes: &[u8]) -> Result<()> {
    let mut options = tokio::fs::OpenOptions::new();
    options.create(true).truncate(true).write(true).mode(0o600);
    let mut file = options.open(path).await?;
    use tokio::io::AsyncWriteExt;
    file.write_all(bytes).await?;
    file.sync_all().await?;
    set_permissions(path, 0o600).await
}

pub(super) async fn write_restricted_file_atomic(path: &Path, bytes: &[u8]) -> Result<()> {
    let parent = path.parent().context("restricted file has no parent")?;
    let temporary = parent.join(format!(".profile-{}.tmp", Uuid::now_v7()));
    write_restricted_file(&temporary, bytes).await?;
    tokio::fs::rename(&temporary, path).await?;
    Ok(())
}

pub(super) async fn validate_provision_result(
    result: Result<Vec<MemoryFileMetadata>>,
    command: &ComputerCommand,
    supervisor: &Supervisor,
) -> Result<Vec<MemoryFileMetadata>> {
    let memory_files = result?;
    if let ComputerCommand::Provision(configuration) = command {
        supervisor
            .validate_agent(configuration.agent_id, &configuration.driver_kind)
            .await?;
    }
    Ok(memory_files)
}

pub(super) async fn resume_received_commands(
    database: &SqlitePool,
    state_dir: &Path,
    supervisor: &Supervisor,
) -> Result<()> {
    let commands: Vec<(String, String)> = sqlx::query_as(
        "SELECT command_id, request_json FROM server_commands \
         WHERE status = 'received' ORDER BY computer_seq",
    )
    .fetch_all(database)
    .await?;
    for (command_id, request_json) in commands {
        let command_id = Uuid::parse_str(&command_id)?;
        let command: ComputerCommand = serde_json::from_str(&request_json)
            .context("persisted command has invalid protocol payload")?;
        let result = execute_local_command(state_dir, &command, supervisor).await;
        let result = validate_provision_result(result, &command, supervisor).await;
        let outcome = match result {
            Ok(memory_files) => LocalCommandOutcome {
                ok: true,
                result: successful_command_result(Some(memory_files)),
            },
            Err(_) => LocalCommandOutcome {
                ok: false,
                result: failed_command_result(),
            },
        };
        finish_local_command(database, command_id, &outcome).await?;
    }
    Ok(())
}
