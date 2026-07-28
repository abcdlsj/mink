use super::local_ipc::{
    authenticate_run, handle_local_connection, local_response_error_code, prepare_download_target,
    prepare_upload_source,
};
use super::*;
use crate::local_protocol::{AgentIdentity, LocalRequest, LocalResponse};

fn test_agent_configuration(
    agent_id: Uuid,
    space_id: Uuid,
    driver_kind: &str,
) -> crate::computer_protocol::AgentConfiguration {
    crate::computer_protocol::AgentConfiguration {
        agent_id,
        space_id,
        name: "Test Agent".to_owned(),
        handle: "test-agent".to_owned(),
        role_text: "Test role".to_owned(),
        role_revision: 1,
        driver_kind: driver_kind.to_owned(),
        driver_config: crate::computer_protocol::DriverConfig { schema_version: 1 },
        attention_config: crate::computer_protocol::AttentionConfig {
            dm_immediate: true,
            mention_immediate: true,
            ambient_enabled: true,
            ambient_debounce_seconds: 5,
            ambient_max_wait_seconds: 30,
            max_retry_count: 3,
        },
        mode: None,
    }
}

#[test]
fn pairing_start_response_accepts_server_rfc3339_timestamp() {
    let response: PairingStartResponse = serde_json::from_value(serde_json::json!({
        "pairing_id": Uuid::now_v7(),
        "browser_path": "/pair-computer/example?code=secret",
        "expires_at": "2026-07-26T16:35:38.729535Z"
    }))
    .unwrap();

    assert_eq!(
        response.expires_at,
        OffsetDateTime::parse(
            "2026-07-26T16:35:38.729535Z",
            &time::format_description::well_known::Rfc3339,
        )
        .unwrap()
    );
}

#[tokio::test]
async fn secrets_are_created_with_restricted_permissions_and_reused() {
    let root = tempfile::tempdir().unwrap();
    let state = root.path().join("computer");
    prepare_state_dir(&state).await.unwrap();
    let path = state.join("secrets.json");
    let first = load_or_create_secrets(&path).await.unwrap();
    let second = load_or_create_secrets(&path).await.unwrap();
    assert_eq!(first.token.expose(), second.token.expose());
    let stored: serde_json::Value =
        serde_json::from_slice(&tokio::fs::read(&path).await.unwrap()).unwrap();
    let mut fields = stored
        .as_object()
        .unwrap()
        .keys()
        .map(String::as_str)
        .collect::<Vec<_>>();
    fields.sort_unstable();
    assert_eq!(
        fields,
        [
            "computer_id",
            "pairing_id",
            "schema_version",
            "space_id",
            "token"
        ]
    );
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        assert_eq!(
            std::fs::metadata(&state).unwrap().permissions().mode() & 0o777,
            0o700
        );
        assert_eq!(
            std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
    }
}

#[tokio::test]
async fn builtin_auth_is_cached_only_in_restricted_computer_secrets() {
    let root = tempfile::tempdir().unwrap();
    let state = root.path().join("computer");
    prepare_state_dir(&state).await.unwrap();
    let path = state.join("secrets.json");
    let mut secrets = load_or_create_secrets(&path).await.unwrap();
    let authentication = serde_json::from_value(serde_json::json!({
        "provider": "local",
        "api_key": "provider-secret"
    }))
    .unwrap();

    sync_builtin_auth(&path, &mut secrets, Some(authentication))
        .await
        .unwrap();

    let stored: serde_json::Value =
        serde_json::from_slice(&tokio::fs::read(&path).await.unwrap()).unwrap();
    assert_eq!(stored["builtin_auth"]["provider"], "local");
    assert_eq!(stored["builtin_auth"]["api_key"], "provider-secret");
    assert!(!stored.to_string().contains("builtin_settings_source"));
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        assert_eq!(
            std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
    }
}

#[tokio::test]
async fn deleted_computer_identity_is_cleared_for_fresh_pairing() {
    let root = tempfile::tempdir().unwrap();
    let state = root.path().join("computer");
    prepare_state_dir(&state).await.unwrap();
    let secrets_path = state.join("secrets.json");
    load_or_create_secrets(&secrets_path).await.unwrap();
    let database = database::connect_sqlite(&state.join("daemon.db"))
        .await
        .unwrap();
    sqlx::query(
        "INSERT INTO daemon_metadata (key, value_json, updated_at) VALUES ('cursor', '1', 'now')",
    )
    .execute(&database)
    .await
    .unwrap();
    sqlx::query("INSERT INTO server_commands (command_id, computer_seq, request_json, status, received_at) VALUES ('command', 1, '{}', 'received', 'now')")
        .execute(&database)
        .await
        .unwrap();
    sqlx::query("INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, token_expires_at, status) VALUES ('run', 'agent', 'space', ?, 'later', 'queued')")
        .bind(vec![0_u8; 32])
        .execute(&database)
        .await
        .unwrap();
    let retained_home = state.join("agents/retired-agent");
    tokio::fs::create_dir_all(&retained_home).await.unwrap();

    reset_deleted_identity(&database, &secrets_path)
        .await
        .unwrap();

    assert!(!secrets_path.exists());
    assert!(retained_home.exists());
    for table in ["daemon_metadata", "server_commands", "local_agent_runs"] {
        let count: i64 = sqlx::query_scalar(&format!("SELECT count(*) FROM {table}"))
            .fetch_one(&database)
            .await
            .unwrap();
        assert_eq!(count, 0, "{table} was not cleared");
    }
}

#[tokio::test]
async fn insecure_existing_state_directory_is_rejected() {
    let root = tempfile::tempdir().unwrap();
    let state = root.path().join("computer");
    tokio::fs::create_dir(&state).await.unwrap();
    set_permissions(&state, 0o755).await.unwrap();
    let error = prepare_state_dir(&state).await.unwrap_err();
    assert!(error.to_string().contains("chmod 0700"));
}

#[tokio::test]
async fn duplicate_commands_reuse_sqlite_state_and_conflicting_payloads_fail() {
    let root = tempfile::tempdir().unwrap();
    let database = database::connect_sqlite(&root.path().join("daemon.db"))
        .await
        .unwrap();
    let command_id = Uuid::now_v7();
    let command = ComputerCommand::Provision(test_agent_configuration(
        Uuid::now_v7(),
        Uuid::now_v7(),
        "builtin",
    ));
    persist_command(&database, command_id, 1, &command)
        .await
        .unwrap();
    persist_command(&database, command_id, 1, &command)
        .await
        .unwrap();
    assert_eq!(last_acked_sequence(&database).await.unwrap(), 1);
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM server_commands")
        .fetch_one(&database)
        .await
        .unwrap();
    assert_eq!(count, 1);

    let conflicting = ComputerCommand::Provision(test_agent_configuration(
        Uuid::now_v7(),
        Uuid::now_v7(),
        "builtin",
    ));
    let error = persist_command(&database, command_id, 1, &conflicting)
        .await
        .unwrap_err();
    assert!(error.to_string().contains("different content"));
}

#[tokio::test]
async fn restart_recovers_runs_and_received_provision_commands() {
    let root = tempfile::tempdir().unwrap();
    let state = root.path().join("computer");
    prepare_state_dir(&state).await.unwrap();
    prepare_agent_root(&state).await.unwrap();
    let database = database::connect_sqlite(&state.join("daemon.db"))
        .await
        .unwrap();
    let run_id = Uuid::now_v7();
    let agent_id = Uuid::now_v7();
    let space_id = Uuid::now_v7();
    sqlx::query(
        "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
         token_expires_at, status, started_at) VALUES (?1, ?2, ?3, ?4, ?5, 'running', ?6)",
    )
    .bind(run_id.to_string())
    .bind(agent_id.to_string())
    .bind(space_id.to_string())
    .bind(Sha256::digest(b"token").to_vec())
    .bind((OffsetDateTime::now_utc() + time::Duration::hours(1)).to_string())
    .bind(OffsetDateTime::now_utc().to_string())
    .execute(&database)
    .await
    .unwrap();
    let run_command_id = Uuid::now_v7();
    persist_command(
        &database,
        run_command_id,
        1,
        &ComputerCommand::Run(crate::computer_protocol::AgentRunCommand {
            run_id,
            agent_id,
            space_id,
            driver_kind: "codex".to_owned(),
            fencing_token: "fencing-token".to_owned(),
            ownership_lease_expires_at: OffsetDateTime::now_utc() + time::Duration::hours(1),
            prompt: crate::prompt::AgentRunPrompt::plain("test"),
        }),
    )
    .await
    .unwrap();
    sqlx::query("UPDATE server_commands SET status = 'running' WHERE command_id = ?1")
        .bind(run_command_id.to_string())
        .execute(&database)
        .await
        .unwrap();
    let command_id = Uuid::now_v7();
    persist_command(
        &database,
        command_id,
        2,
        &ComputerCommand::Provision(test_agent_configuration(agent_id, space_id, "codex")),
    )
    .await
    .unwrap();

    crate::supervisor::recover_interrupted_runs(&database)
        .await
        .unwrap();
    let supervisor = Supervisor::new(
        database.clone(),
        state.clone(),
        state.join("daemon.sock"),
        &crate::config::ComputerConfig::default(),
        Arc::new(CodexDriver::new()),
        None,
    );
    resume_received_commands(&database, &state, &supervisor)
        .await
        .unwrap();

    let recovered: (String, Option<String>, Option<String>) = sqlx::query_as(
        "SELECT status, last_error_code, server_recovery_reported_at \
         FROM local_agent_runs WHERE run_id = ?1",
    )
    .bind(run_id.to_string())
    .fetch_one(&database)
    .await
    .unwrap();
    assert_eq!(
        recovered,
        ("failed".to_owned(), Some("process_lost".to_owned()), None)
    );
    let command_status: String =
        sqlx::query_scalar("SELECT status FROM server_commands WHERE command_id = ?1")
            .bind(command_id.to_string())
            .fetch_one(&database)
            .await
            .unwrap();
    assert_eq!(command_status, "completed");
    let recovered_outbox: (String, String, i64) = sqlx::query_as(
        "SELECT command_id, payload_json, attempt_count FROM run_result_outbox WHERE run_id = ?1",
    )
    .bind(run_id.to_string())
    .fetch_one(&database)
    .await
    .unwrap();
    assert_eq!(recovered_outbox.0, run_command_id.to_string());
    assert_eq!(recovered_outbox.2, 0);
    let recovered_payload: serde_json::Value = serde_json::from_str(&recovered_outbox.1).unwrap();
    assert_eq!(recovered_payload["status"], "failed");
    assert_eq!(recovered_payload["error_code"], "process_lost");
    let home = state.join("agents").join(agent_id.to_string());
    let profile: serde_json::Value =
        serde_json::from_slice(&tokio::fs::read(home.join("profile.json")).await.unwrap()).unwrap();
    assert_eq!(profile["agent_id"], agent_id.to_string());
    assert_eq!(profile["desired_lifecycle"], "active");
    assert_eq!(profile["provision_status"], "ready");
    assert!(profile.get("token").is_none());
    assert_eq!(
        tokio::fs::read_to_string(home.join("memory/MEMORY.md"))
            .await
            .unwrap(),
        "# Memory\n"
    );
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        for relative in [
            "",
            "memory",
            "workspace",
            "drivers/codex",
            "drivers/builtin",
            "runs",
            "logs",
        ] {
            let path = state
                .join("agents")
                .join(agent_id.to_string())
                .join(relative);
            assert_eq!(
                std::fs::metadata(path).unwrap().permissions().mode() & 0o777,
                0o700
            );
        }
    }
}

#[tokio::test]
async fn daemon_renews_active_leases_and_reports_process_lost_once() {
    let root = tempfile::tempdir().unwrap();
    let database = database::connect_sqlite(&root.path().join("daemon.db"))
        .await
        .unwrap();
    let computer_id = Uuid::now_v7();
    let agent_id = Uuid::now_v7();
    let active_run_id = Uuid::now_v7();
    let lost_run_id = Uuid::now_v7();
    let space_id = Uuid::now_v7();
    for (run_id, status, error_code) in [
        (active_run_id, "running", None),
        (lost_run_id, "failed", Some("process_lost")),
    ] {
        sqlx::query(
            "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
             token_expires_at, status, started_at, finished_at, last_error_code) \
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)",
        )
        .bind(run_id.to_string())
        .bind(agent_id.to_string())
        .bind(space_id.to_string())
        .bind(Sha256::digest(b"token").to_vec())
        .bind((OffsetDateTime::now_utc() + time::Duration::hours(1)).to_string())
        .bind(status)
        .bind(OffsetDateTime::now_utc().to_string())
        .bind((status == "failed").then(|| OffsetDateTime::now_utc().to_string()))
        .bind(error_code)
        .execute(&database)
        .await
        .unwrap();
    }
    let app = axum::Router::new()
        .route(
            "/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/renew",
            axum::routing::post(|| async {
                axum::Json(serde_json::json!({
                    "ownership_lease_expires_at": (OffsetDateTime::now_utc()
                        + time::Duration::minutes(35))
                        .format(&time::format_description::well_known::Rfc3339)
                        .unwrap()
                }))
            }),
        )
        .route(
            "/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/release",
            axum::routing::post(|| async { axum::Json(serde_json::json!({ "released": true })) }),
        );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let server = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    let server_url = Url::parse(&format!("http://{address}")).unwrap();

    let client = daemon_http_client().unwrap();
    renew_active_run_leases(&client, &server_url, computer_id, "token", &database)
        .await
        .unwrap();
    release_interrupted_runs(&client, &server_url, computer_id, "token", &database)
        .await
        .unwrap();
    let reported_at: Option<String> = sqlx::query_scalar(
        "SELECT server_recovery_reported_at FROM local_agent_runs WHERE run_id = ?1",
    )
    .bind(lost_run_id.to_string())
    .fetch_one(&database)
    .await
    .unwrap();
    assert!(reported_at.is_some());
    server.abort();
    let _ = server.await;
}

#[tokio::test]
async fn run_result_outbox_retries_until_server_receipt() {
    let root = tempfile::tempdir().unwrap();
    let database = database::connect_sqlite(&root.path().join("daemon.db"))
        .await
        .unwrap();
    let event_id = Uuid::now_v7().to_string();
    let command_id = Uuid::now_v7();
    let run_id = Uuid::now_v7();
    let agent_id = Uuid::now_v7();
    let space_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc()
        .format(&time::format_description::well_known::Rfc3339)
        .unwrap();
    sqlx::query(
        "INSERT INTO server_commands (command_id, computer_seq, request_json, status, \
         result_json, received_at, completed_at) VALUES (?1, 1, ?2, 'completed', ?3, ?4, ?4)",
    )
    .bind(command_id.to_string())
    .bind(serde_json::json!({ "kind": "agent.run", "payload": { "run_id": run_id } }).to_string())
    .bind(serde_json::json!({ "ok": true, "run_id": run_id, "status": "completed" }).to_string())
    .bind(&now)
    .execute(&database)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
         token_expires_at, status, finished_at, command_id, computer_seq, result_event_id) \
         VALUES (?1, ?2, ?3, zeroblob(32), ?4, 'completed', ?4, ?5, 1, ?6)",
    )
    .bind(run_id.to_string())
    .bind(agent_id.to_string())
    .bind(space_id.to_string())
    .bind(&now)
    .bind(command_id.to_string())
    .bind(&event_id)
    .execute(&database)
    .await
    .unwrap();
    let payload = serde_json::json!({
        "ok": true,
        "run_id": run_id,
        "status": "completed",
        "memory_files": []
    });
    sqlx::query(
        "INSERT INTO run_result_outbox (event_id, command_id, computer_seq, run_id, \
         payload_json, next_attempt_at, created_at) VALUES (?1, ?2, 1, ?3, ?4, ?5, ?5)",
    )
    .bind(&event_id)
    .bind(command_id.to_string())
    .bind(run_id.to_string())
    .bind(payload.to_string())
    .bind(&now)
    .execute(&database)
    .await
    .unwrap();

    let computer_id = Uuid::now_v7();

    for attempt in 1..=2 {
        let (daemon_io, server_io) = tokio::io::duplex(4096);
        let mut daemon_socket = tokio_tungstenite::WebSocketStream::from_raw_socket(
            daemon_io,
            tungstenite::protocol::Role::Client,
            None,
        )
        .await;
        let mut server_socket = tokio_tungstenite::WebSocketStream::from_raw_socket(
            server_io,
            tungstenite::protocol::Role::Server,
            None,
        )
        .await;
        sqlx::query("UPDATE run_result_outbox SET next_attempt_at = ?2 WHERE event_id = ?1")
            .bind(&event_id)
            .bind(OffsetDateTime::now_utc().to_string())
            .execute(&database)
            .await
            .unwrap();
        send_pending_run_result(&mut daemon_socket, &database, computer_id)
            .await
            .unwrap();
        let frame = server_socket.next().await.unwrap().unwrap();
        let frame: serde_json::Value = serde_json::from_str(frame.to_text().unwrap()).unwrap();
        assert_eq!(frame["type"], "run_result");
        assert_eq!(frame["event_id"], event_id);
        assert_eq!(frame["command_id"], command_id.to_string());
        let stored_attempt: i64 =
            sqlx::query_scalar("SELECT attempt_count FROM run_result_outbox WHERE event_id = ?1")
                .bind(&event_id)
                .fetch_one(&database)
                .await
                .unwrap();
        assert_eq!(stored_attempt, attempt);
    }

    mark_run_result_reported(&database, computer_id, &event_id)
        .await
        .unwrap();
    let (daemon_io, server_io) = tokio::io::duplex(4096);
    let mut daemon_socket = tokio_tungstenite::WebSocketStream::from_raw_socket(
        daemon_io,
        tungstenite::protocol::Role::Client,
        None,
    )
    .await;
    let mut server_socket = tokio_tungstenite::WebSocketStream::from_raw_socket(
        server_io,
        tungstenite::protocol::Role::Server,
        None,
    )
    .await;
    send_pending_run_result(&mut daemon_socket, &database, computer_id)
        .await
        .unwrap();
    assert!(
        tokio::time::timeout(std::time::Duration::from_millis(25), server_socket.next())
            .await
            .is_err()
    );
    let reported_at: Option<String> =
        sqlx::query_scalar("SELECT reported_at FROM run_result_outbox WHERE event_id = ?1")
            .bind(&event_id)
            .fetch_one(&database)
            .await
            .unwrap();
    assert!(reported_at.is_some());
}

#[tokio::test]
async fn run_started_outbox_retries_until_server_receipt() {
    let root = tempfile::tempdir().unwrap();
    let database = database::connect_sqlite(&root.path().join("daemon.db"))
        .await
        .unwrap();
    let event_id = Uuid::now_v7().to_string();
    let run_id = Uuid::now_v7();
    let process_instance_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc()
        .format(&time::format_description::well_known::Rfc3339)
        .unwrap();
    sqlx::query(
        "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
         token_expires_at, status, process_instance_id) \
         VALUES (?1, ?2, ?3, zeroblob(32), ?4, 'queued', ?5)",
    )
    .bind(run_id.to_string())
    .bind(Uuid::now_v7().to_string())
    .bind(Uuid::now_v7().to_string())
    .bind(&now)
    .bind(process_instance_id.to_string())
    .execute(&database)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO run_started_outbox (event_id, run_id, run_attempt, process_instance_id, \
         daemon_observed_at, next_attempt_at, created_at) VALUES (?1, ?2, 1, ?3, ?4, ?4, ?4)",
    )
    .bind(&event_id)
    .bind(run_id.to_string())
    .bind(process_instance_id.to_string())
    .bind(&now)
    .execute(&database)
    .await
    .unwrap();

    let (daemon_io, server_io) = tokio::io::duplex(4096);
    let mut daemon_socket = tokio_tungstenite::WebSocketStream::from_raw_socket(
        daemon_io,
        tungstenite::protocol::Role::Client,
        None,
    )
    .await;
    let mut server_socket = tokio_tungstenite::WebSocketStream::from_raw_socket(
        server_io,
        tungstenite::protocol::Role::Server,
        None,
    )
    .await;
    let computer_id = Uuid::now_v7();

    for attempt in 1..=2 {
        sqlx::query("UPDATE run_started_outbox SET next_attempt_at = ?2 WHERE event_id = ?1")
            .bind(&event_id)
            .bind(OffsetDateTime::now_utc().to_string())
            .execute(&database)
            .await
            .unwrap();
        assert!(
            send_pending_run_started(&mut daemon_socket, &database, computer_id)
                .await
                .unwrap()
        );
        let frame = server_socket.next().await.unwrap().unwrap();
        let frame: serde_json::Value = serde_json::from_str(frame.to_text().unwrap()).unwrap();
        assert_eq!(frame["type"], "run_started");
        assert_eq!(frame["event_id"], event_id);
        assert_eq!(frame["run_id"], run_id.to_string());
        assert_eq!(
            frame["process_instance_id"],
            process_instance_id.to_string()
        );
        let stored_attempt: i64 =
            sqlx::query_scalar("SELECT attempt_count FROM run_started_outbox WHERE event_id = ?1")
                .bind(&event_id)
                .fetch_one(&database)
                .await
                .unwrap();
        assert_eq!(stored_attempt, attempt);
    }

    mark_run_started_reported(&database, computer_id, &event_id)
        .await
        .unwrap();
    assert!(
        !send_pending_run_started(&mut daemon_socket, &database, computer_id)
            .await
            .unwrap()
    );
}

#[tokio::test]
async fn attention_claims_respect_capacity_and_rotate_agents() {
    use axum::{extract::State, routing::get};

    #[derive(Clone)]
    struct SchedulerState {
        agents: Arc<Vec<Uuid>>,
        claimed: Arc<tokio::sync::Mutex<Vec<Uuid>>>,
    }

    async fn list_agents(State(state): State<SchedulerState>) -> axum::Json<serde_json::Value> {
        axum::Json(serde_json::Value::Array(
            state
                .agents
                .iter()
                .map(|agent_id| {
                    serde_json::json!({
                        "member_id": agent_id,
                        "desired_lifecycle": "active",
                        "provision_status": "ready"
                    })
                })
                .collect(),
        ))
    }

    async fn claim_agent(
        axum::extract::Path((_computer_id, agent_id)): axum::extract::Path<(Uuid, Uuid)>,
        State(state): State<SchedulerState>,
    ) -> axum::Json<serde_json::Value> {
        state.claimed.lock().await.push(agent_id);
        axum::Json(serde_json::json!({
            "claimed": true,
            "run_id": agent_id,
            "inbox_item_ids": [Uuid::now_v7()]
        }))
    }

    let root = tempfile::tempdir().unwrap();
    let database = database::connect_sqlite(&root.path().join("daemon.db"))
        .await
        .unwrap();
    let existing_run_id = Uuid::now_v7();
    sqlx::query(
        "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
         token_expires_at, status) VALUES (?1, ?2, ?3, zeroblob(32), ?4, 'queued')",
    )
    .bind(existing_run_id.to_string())
    .bind(Uuid::now_v7().to_string())
    .bind(Uuid::now_v7().to_string())
    .bind(OffsetDateTime::now_utc().to_string())
    .execute(&database)
    .await
    .unwrap();
    let agents = Arc::new((0..4).map(|_| Uuid::now_v7()).collect::<Vec<_>>());
    let claimed = Arc::new(tokio::sync::Mutex::new(Vec::new()));
    let state = SchedulerState {
        agents: agents.clone(),
        claimed: claimed.clone(),
    };
    let app = axum::Router::new()
        .route("/api/v1/computers/{computer_id}/agents", get(list_agents))
        .route(
            "/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/claim",
            axum::routing::post(claim_agent),
        )
        .with_state(state);
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let server = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    let server_url = Url::parse(&format!("http://{address}")).unwrap();
    let client = daemon_http_client().unwrap();
    let computer_id = Uuid::now_v7();
    let mut scheduler = AttentionSchedulerState {
        database: database.clone(),
        max_claimed_runs: 2,
        next_agent_index: 0,
        pending_claims: std::collections::HashSet::new(),
    };

    poll_agent_inbox(&client, &server_url, computer_id, "token", &mut scheduler)
        .await
        .unwrap();
    assert_eq!(claimed.lock().await.as_slice(), &[agents[0]]);

    sqlx::query("UPDATE local_agent_runs SET status = 'completed' WHERE run_id = ?1")
        .bind(existing_run_id.to_string())
        .execute(&database)
        .await
        .unwrap();
    sqlx::query(
        "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
         token_expires_at, status) VALUES (?1, ?2, ?3, zeroblob(32), ?4, 'completed')",
    )
    .bind(agents[0].to_string())
    .bind(agents[0].to_string())
    .bind(Uuid::now_v7().to_string())
    .bind(OffsetDateTime::now_utc().to_string())
    .execute(&database)
    .await
    .unwrap();
    poll_agent_inbox(&client, &server_url, computer_id, "token", &mut scheduler)
        .await
        .unwrap();
    assert_eq!(claimed.lock().await.as_slice(), &agents[..3]);
    for agent_id in &agents[1..3] {
        sqlx::query(
            "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
             token_expires_at, status) VALUES (?1, ?2, ?3, zeroblob(32), ?4, 'completed')",
        )
        .bind(agent_id.to_string())
        .bind(agent_id.to_string())
        .bind(Uuid::now_v7().to_string())
        .bind(OffsetDateTime::now_utc().to_string())
        .execute(&database)
        .await
        .unwrap();
    }
    poll_agent_inbox(&client, &server_url, computer_id, "token", &mut scheduler)
        .await
        .unwrap();
    assert_eq!(
        claimed.lock().await.as_slice(),
        &[agents[0], agents[1], agents[2], agents[3], agents[0]]
    );

    server.abort();
    let _ = server.await;
}

#[tokio::test]
async fn hanging_claim_does_not_block_websocket_results_or_heartbeat() {
    use axum::extract::State;

    async fn list_agents(agent_id: Uuid) -> axum::Json<serde_json::Value> {
        axum::Json(serde_json::json!([{
            "member_id": agent_id,
            "desired_lifecycle": "active",
            "provision_status": "ready"
        }]))
    }

    async fn hang_claim(
        State(started): State<Arc<tokio::sync::Notify>>,
    ) -> axum::Json<serde_json::Value> {
        started.notify_one();
        std::future::pending().await
    }

    let root = tempfile::tempdir().unwrap();
    let database = database::connect_sqlite(&root.path().join("daemon.db"))
        .await
        .unwrap();
    let computer_id = Uuid::now_v7();
    let agent_id = Uuid::now_v7();
    let run_id = Uuid::now_v7();
    let process_instance_id = Uuid::now_v7();
    let event_id = Uuid::now_v7().to_string();
    let now = OffsetDateTime::now_utc()
        .format(&time::format_description::well_known::Rfc3339)
        .unwrap();
    sqlx::query(
        "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
         token_expires_at, status, process_instance_id, fencing_token) \
         VALUES (?1, ?2, ?3, zeroblob(32), ?4, 'queued', ?5, 'fence')",
    )
    .bind(run_id.to_string())
    .bind(agent_id.to_string())
    .bind(Uuid::now_v7().to_string())
    .bind(&now)
    .bind(process_instance_id.to_string())
    .execute(&database)
    .await
    .unwrap();
    sqlx::query(
        "INSERT INTO run_started_outbox (event_id, run_id, run_attempt, process_instance_id, \
         daemon_observed_at, next_attempt_at, created_at) VALUES (?1, ?2, 1, ?3, ?4, ?4, ?4)",
    )
    .bind(&event_id)
    .bind(run_id.to_string())
    .bind(process_instance_id.to_string())
    .bind(&now)
    .execute(&database)
    .await
    .unwrap();

    let claim_started = Arc::new(tokio::sync::Notify::new());
    let app = axum::Router::new()
        .route(
            "/api/v1/computers/{computer_id}/agents",
            axum::routing::get(move || list_agents(agent_id)),
        )
        .route(
            "/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/claim",
            axum::routing::post(hang_claim),
        )
        .with_state(claim_started.clone());
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let server = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    let server_url = Url::parse(&format!("http://{address}")).unwrap();

    let state_dir = root.path().to_owned();
    prepare_agent_root(&state_dir).await.unwrap();
    let supervisor = Supervisor::new(
        database.clone(),
        state_dir.clone(),
        state_dir.join("daemon.sock"),
        &crate::config::ComputerConfig::default(),
        Arc::new(CodexDriver::new()),
        None,
    );
    let cancellation = CancellationToken::new();
    let (outgoing_tx, mut outgoing_rx) = mpsc::channel(16);
    let (completion_tx, completion_rx) = mpsc::channel(1);
    let (command_tx, _command_rx) = mpsc::channel(1);
    let (daemon_io, server_io) = tokio::io::duplex(4096);
    let daemon_socket = tokio_tungstenite::WebSocketStream::from_raw_socket(
        daemon_io,
        tungstenite::protocol::Role::Client,
        None,
    )
    .await;
    let (_, daemon_reader) = daemon_socket.split();
    let mut server_socket = tokio_tungstenite::WebSocketStream::from_raw_socket(
        server_io,
        tungstenite::protocol::Role::Server,
        None,
    )
    .await;

    let attention = tokio::spawn(attention_scheduler_task(
        daemon_http_client().unwrap(),
        server_url,
        computer_id,
        "token".to_owned(),
        database.clone(),
        2,
        cancellation.child_token(),
    ));
    let heartbeat = tokio::spawn(heartbeat_reporter_task(
        supervisor,
        computer_id,
        1,
        outgoing_tx.clone(),
        cancellation.child_token(),
    ));
    let results = tokio::spawn(result_sender_task(
        database.clone(),
        computer_id,
        outgoing_tx.clone(),
        completion_rx,
        cancellation.child_token(),
    ));
    let reader = tokio::spawn(websocket_reader_task(
        daemon_reader,
        outgoing_tx.clone(),
        command_tx,
        database.clone(),
        computer_id,
        cancellation.child_token(),
    ));

    tokio::time::timeout(Duration::from_secs(2), claim_started.notified())
        .await
        .expect("attention scheduler did not enter the hanging claim");
    assert!(
        !results.is_finished(),
        "result sender stopped before claim hung"
    );
    sqlx::query("UPDATE run_started_outbox SET next_attempt_at = ?2 WHERE event_id = ?1")
        .bind(&event_id)
        .bind(OffsetDateTime::now_utc().to_string())
        .execute(&database)
        .await
        .unwrap();
    completion_tx.send(()).await.unwrap();
    server_socket
        .send(tungstenite::Message::Ping(vec![1, 2, 3].into()))
        .await
        .unwrap();

    let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
    let mut observed = std::collections::BTreeSet::new();
    while observed.len() < 3 {
        let Ok(Some(message)) = tokio::time::timeout_at(deadline, outgoing_rx.recv()).await else {
            break;
        };
        match message {
            tungstenite::Message::Pong(_) => {
                observed.insert("pong".to_owned());
            }
            tungstenite::Message::Text(text) => {
                let frame: serde_json::Value = serde_json::from_str(&text).unwrap();
                if let Some(kind) = frame["type"].as_str() {
                    observed.insert(kind.to_owned());
                }
            }
            _ => {}
        }
    }
    assert_eq!(
        observed,
        ["heartbeat", "pong", "run_started"]
            .map(str::to_owned)
            .into()
    );

    cancellation.cancel();
    for task in [attention, heartbeat, results, reader] {
        assert!(matches!(
            tokio::time::timeout(Duration::from_secs(1), task)
                .await
                .unwrap()
                .unwrap()
                .unwrap(),
            ConnectionTaskExit::Cancelled
        ));
    }
    server.abort();
    let _ = server.await;
}

#[tokio::test]
async fn lifecycle_commands_update_profile_and_report_memory_metadata() {
    let root = tempfile::tempdir().unwrap();
    let state = root.path().join("computer");
    prepare_state_dir(&state).await.unwrap();
    prepare_agent_root(&state).await.unwrap();
    let database = database::connect_sqlite(&state.join("daemon.db"))
        .await
        .unwrap();
    let agent_id = Uuid::now_v7();
    let supervisor = Supervisor::new(
        database,
        state.clone(),
        state.join("daemon.sock"),
        &crate::config::ComputerConfig::default(),
        Arc::new(CodexDriver::new()),
        None,
    );
    let mut configuration = test_agent_configuration(agent_id, Uuid::now_v7(), "codex");
    configuration.name = "Lin".to_owned();
    configuration.handle = "lin".to_owned();
    configuration.role_text = "Review boundaries.".to_owned();
    let files = execute_local_command(
        &state,
        &ComputerCommand::Provision(configuration.clone()),
        &supervisor,
    )
    .await
    .unwrap();
    assert_eq!(files.len(), 1);
    assert_eq!(files[0].path, "MEMORY.md");
    assert_eq!(files[0].sha256, hex::encode(Sha256::digest(b"# Memory\n")));

    configuration.role_text = "Enforce the current specification.".to_owned();
    configuration.role_revision = 2;
    execute_local_command(
        &state,
        &ComputerCommand::Configure(configuration.clone()),
        &supervisor,
    )
    .await
    .unwrap();
    configuration.mode = Some(crate::computer_protocol::SuspendMode::CancelNow);
    execute_local_command(
        &state,
        &ComputerCommand::Suspend(configuration.clone()),
        &supervisor,
    )
    .await
    .unwrap();
    let profile_path = state
        .join("agents")
        .join(agent_id.to_string())
        .join("profile.json");
    let suspended: serde_json::Value =
        serde_json::from_slice(&tokio::fs::read(&profile_path).await.unwrap()).unwrap();
    assert_eq!(suspended["desired_lifecycle"], "suspended");
    assert_eq!(suspended["role_revision"], 2);

    execute_local_command(
        &state,
        &ComputerCommand::Resume(configuration.clone()),
        &supervisor,
    )
    .await
    .unwrap();
    let resumed: serde_json::Value =
        serde_json::from_slice(&tokio::fs::read(&profile_path).await.unwrap()).unwrap();
    assert_eq!(resumed["desired_lifecycle"], "active");

    execute_local_command(&state, &ComputerCommand::Retire(configuration), &supervisor)
        .await
        .unwrap();
    let retired: serde_json::Value =
        serde_json::from_slice(&tokio::fs::read(profile_path).await.unwrap()).unwrap();
    assert_eq!(retired["desired_lifecycle"], "retired");
}

#[tokio::test]
async fn memory_read_is_scoped_to_agent_memory_and_rejects_symlinks() {
    let root = tempfile::tempdir().unwrap();
    let state = root.path().join("computer");
    let agent_id = Uuid::now_v7();
    let memory = state
        .join("agents")
        .join(agent_id.to_string())
        .join("memory");
    tokio::fs::create_dir_all(memory.join("notes"))
        .await
        .unwrap();
    tokio::fs::write(memory.join("notes/current.md"), b"Current facts.\n")
        .await
        .unwrap();
    let content = read_memory_file(
        &state,
        &crate::computer_protocol::AgentMemoryReadCommand {
            agent_id,
            path: "notes/current.md".to_owned(),
        },
    )
    .await
    .unwrap();
    assert_eq!(content.path.as_deref(), Some("notes/current.md"));
    assert_eq!(content.content.as_deref(), Some("Current facts.\n"));

    let escaped = read_memory_file(
        &state,
        &crate::computer_protocol::AgentMemoryReadCommand {
            agent_id,
            path: "../profile.json".to_owned(),
        },
    )
    .await
    .unwrap_err();
    assert!(escaped.to_string().contains("invalid"));

    #[cfg(unix)]
    {
        std::os::unix::fs::symlink(memory.join("notes/current.md"), memory.join("linked.md"))
            .unwrap();
        let linked = read_memory_file(
            &state,
            &crate::computer_protocol::AgentMemoryReadCommand {
                agent_id,
                path: "linked.md".to_owned(),
            },
        )
        .await
        .unwrap_err();
        assert!(linked.to_string().contains("symlink"));
    }
}

#[tokio::test]
async fn local_run_capability_is_scoped_and_expires() {
    let root = tempfile::tempdir().unwrap();
    let database = database::connect_sqlite(&root.path().join("daemon.db"))
        .await
        .unwrap();
    let run_id = Uuid::now_v7();
    let agent_id = Uuid::now_v7();
    let space_id = Uuid::now_v7();
    sqlx::query(
        "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
         token_expires_at, status) VALUES (?1, ?2, ?3, ?4, ?5, 'running')",
    )
    .bind(run_id.to_string())
    .bind(agent_id.to_string())
    .bind(space_id.to_string())
    .bind(Sha256::digest(b"correct").to_vec())
    .bind((OffsetDateTime::now_utc() + time::Duration::minutes(5)).to_string())
    .execute(&database)
    .await
    .unwrap();
    let identity = authenticate_run(&database, "correct")
        .await
        .unwrap()
        .unwrap();
    assert_eq!(identity.agent_member_id, agent_id);
    assert!(
        authenticate_run(&database, "wrong")
            .await
            .unwrap()
            .is_none()
    );
    sqlx::query("UPDATE local_agent_runs SET token_expires_at = ?2 WHERE run_id = ?1")
        .bind(run_id.to_string())
        .bind((OffsetDateTime::now_utc() - time::Duration::seconds(1)).to_string())
        .execute(&database)
        .await
        .unwrap();
    assert!(
        authenticate_run(&database, "correct")
            .await
            .unwrap()
            .is_none()
    );
}

#[tokio::test]
async fn local_socket_protocol_returns_scoped_agent_identity() {
    use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};

    let root = tempfile::tempdir().unwrap();
    let database = database::connect_sqlite(&root.path().join("daemon.db"))
        .await
        .unwrap();
    let run_id = Uuid::now_v7();
    let agent_id = Uuid::now_v7();
    let space_id = Uuid::now_v7();
    sqlx::query(
        "INSERT INTO local_agent_runs (run_id, agent_member_id, space_id, run_token_hash, \
         token_expires_at, status) VALUES (?1, ?2, ?3, ?4, ?5, 'running')",
    )
    .bind(run_id.to_string())
    .bind(agent_id.to_string())
    .bind(space_id.to_string())
    .bind(Sha256::digest(b"socket-token").to_vec())
    .bind((OffsetDateTime::now_utc() + time::Duration::minutes(5)).to_string())
    .execute(&database)
    .await
    .unwrap();
    let (client, server) = tokio::net::UnixStream::pair().unwrap();
    let task = tokio::spawn({
        let database = database.clone();
        let state_dir = root.path().to_owned();
        async move {
            handle_local_connection(
                server,
                &state_dir,
                &database,
                &Url::parse("http://127.0.0.1:1").unwrap(),
                Uuid::now_v7(),
                "token",
            )
            .await
        }
    });
    let (reader, mut writer) = client.into_split();
    let request = LocalRequest::Whoami {
        run_token: "socket-token".to_owned(),
    };
    writer
        .write_all(format!("{}\n", serde_json::to_string(&request).unwrap()).as_bytes())
        .await
        .unwrap();
    let mut response = String::new();
    BufReader::new(reader)
        .read_line(&mut response)
        .await
        .unwrap();
    let response: LocalResponse = serde_json::from_str(&response).unwrap();
    assert!(response.ok);
    let identity: AgentIdentity = serde_json::from_value(response.data.unwrap()).unwrap();
    assert_eq!(identity.run_id, run_id);
    assert_eq!(identity.agent_member_id, agent_id);
    assert_eq!(identity.space_id, space_id);
    task.await.unwrap().unwrap();
}

#[tokio::test]
async fn attachment_paths_cannot_escape_agent_home_or_overwrite_files() {
    let root = tempfile::tempdir().unwrap();
    prepare_agent_root(root.path()).await.unwrap();
    let agent_id = Uuid::now_v7();
    let home = prepare_agent_home(root.path(), agent_id).await.unwrap();
    let source = home.join("workspace/report.txt");
    tokio::fs::write(&source, b"report").await.unwrap();
    let prepared = prepare_upload_source(root.path(), agent_id, source.to_str().unwrap())
        .await
        .unwrap();
    assert_eq!(prepared.1, "report.txt");
    assert_eq!(prepared.2, 6);
    assert_eq!(prepared.3, hex::encode(Sha256::digest(b"report")));

    let outside = root.path().join("outside.txt");
    tokio::fs::write(&outside, b"private").await.unwrap();
    let denied = prepare_upload_source(root.path(), agent_id, outside.to_str().unwrap())
        .await
        .err()
        .unwrap();
    assert_eq!(denied.error.unwrap().code, "permission_denied");

    let output = home.join("workspace/download.txt");
    let expected_output = tokio::fs::canonicalize(home.join("workspace"))
        .await
        .unwrap()
        .join("download.txt");
    assert_eq!(
        prepare_download_target(root.path(), agent_id, output.to_str().unwrap())
            .await
            .unwrap(),
        expected_output
    );
    tokio::fs::write(&output, b"existing").await.unwrap();
    let existing = prepare_download_target(root.path(), agent_id, output.to_str().unwrap())
        .await
        .err()
        .unwrap();
    assert_eq!(existing.error.unwrap().code, "attachment_output_exists");

    let denied = prepare_download_target(
        root.path(),
        agent_id,
        root.path().join("escape.txt").to_str().unwrap(),
    )
    .await
    .err()
    .unwrap();
    assert_eq!(denied.error.unwrap().code, "permission_denied");
}

#[test]
fn reconnect_backoff_is_bounded_and_jittered_above_base() {
    for attempt in 0..10 {
        let delay = reconnect_delay(attempt).as_millis() as u64;
        let base = 1_000_u64
            .saturating_mul(1_u64 << attempt.min(5))
            .min(30_000);
        assert!(delay >= base && delay <= base + base / 4);
    }
}

#[test]
fn shutdown_frame_is_a_terminal_server_instruction() {
    let frame = super::decode_server_frame(tungstenite::Message::Text(
        serde_json::json!({ "type": "shutdown", "reason": "computer_deleted" })
            .to_string()
            .into(),
    ))
    .unwrap();
    assert!(matches!(
        frame,
        super::ServerFrame::Shutdown { reason } if reason == "computer_deleted"
    ));
}

#[test]
fn command_log_context_only_extracts_safe_identifiers() {
    let agent_id = Uuid::now_v7();
    let run_id = Uuid::now_v7();
    let command =
        crate::computer_protocol::ComputerCommand::Run(crate::computer_protocol::AgentRunCommand {
            agent_id,
            run_id,
            space_id: Uuid::now_v7(),
            driver_kind: "builtin".to_owned(),
            fencing_token: "private token".to_owned(),
            ownership_lease_expires_at: OffsetDateTime::now_utc(),
            prompt: crate::prompt::AgentRunPrompt::plain("private prompt"),
        });

    assert_eq!(
        command_log_context(&command),
        CommandLogContext {
            agent_member_id: Some(agent_id),
            run_id: Some(run_id),
        }
    );
}

#[test]
fn command_result_log_summary_only_exposes_error_code() {
    let outcome = LocalCommandOutcome {
        ok: false,
        result: CommandResult {
            error_code: Some("driver_failed".to_owned()),
            ..CommandResult::default()
        },
    };

    assert_eq!(command_error_code(&outcome), "driver_failed");
}

#[test]
fn local_action_log_summary_only_exposes_error_code() {
    let response = LocalResponse::failure("context_changed", "private upstream explanation", true);

    assert_eq!(local_response_error_code(&response), "context_changed");
}
