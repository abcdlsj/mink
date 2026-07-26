use super::local_ipc::{
    authenticate_run, handle_local_connection, prepare_download_target, prepare_upload_source,
};
use super::*;
use crate::local_protocol::{AgentIdentity, LocalRequest, LocalResponse};

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
    let payload = serde_json::json!({ "agent_id": Uuid::now_v7() });
    persist_command(&database, command_id, 1, "agent.provision", &payload)
        .await
        .unwrap();
    persist_command(&database, command_id, 1, "agent.provision", &payload)
        .await
        .unwrap();
    assert_eq!(last_acked_sequence(&database).await.unwrap(), 1);
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM server_commands")
        .fetch_one(&database)
        .await
        .unwrap();
    assert_eq!(count, 1);

    let error = persist_command(
        &database,
        command_id,
        1,
        "agent.provision",
        &serde_json::json!({ "agent_id": Uuid::now_v7() }),
    )
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
    let command_id = Uuid::now_v7();
    persist_command(
        &database,
        command_id,
        1,
        "agent.provision",
        &serde_json::json!({
            "agent_id": agent_id,
            "driver_kind": "codex"
        }),
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
    let home = state.join("agents").join(agent_id.to_string());
    let profile: serde_json::Value =
        serde_json::from_slice(&tokio::fs::read(home.join("profile.json")).await.unwrap()).unwrap();
    assert_eq!(profile["agent_id"], agent_id.to_string());
    assert_eq!(profile["status"], "active");
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
            axum::routing::post(|| async { axum::Json(serde_json::json!({ "ok": true })) }),
        )
        .route(
            "/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/release",
            axum::routing::post(|| async { axum::Json(serde_json::json!({ "released": true })) }),
        );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let server = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    let server_url = Url::parse(&format!("http://{address}")).unwrap();

    renew_active_run_leases(&server_url, computer_id, "token", &database)
        .await
        .unwrap();
    release_interrupted_runs(&server_url, computer_id, "token", &database)
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
    );
    let provision = serde_json::json!({
        "agent_id": agent_id,
        "space_id": Uuid::now_v7(),
        "name": "Lin",
        "handle": "lin",
        "role_text": "Review boundaries.",
        "role_revision": 1,
        "driver_kind": "codex",
        "driver_config": { "schema_version": 1 },
        "attention_config": {
            "dm_immediate": true,
            "mention_immediate": true,
            "ambient_enabled": true,
            "ambient_debounce_seconds": 5,
            "ambient_max_wait_seconds": 30,
            "max_retry_count": 3
        }
    });
    let files = execute_local_command(&state, "agent.provision", &provision, &supervisor)
        .await
        .unwrap();
    assert_eq!(files.len(), 1);
    assert_eq!(files[0].path, "MEMORY.md");
    assert_eq!(files[0].sha256, hex::encode(Sha256::digest(b"# Memory\n")));

    execute_local_command(
        &state,
        "agent.configure",
        &serde_json::json!({
            "agent_id": agent_id,
            "role_text": "Enforce the current specification.",
            "role_revision": 2,
            "attention_config": provision["attention_config"]
        }),
        &supervisor,
    )
    .await
    .unwrap();
    execute_local_command(
        &state,
        "agent.suspend",
        &serde_json::json!({ "agent_id": agent_id, "mode": "cancel_now" }),
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
    assert_eq!(suspended["status"], "suspended");
    assert_eq!(suspended["role_revision"], 2);

    execute_local_command(
        &state,
        "agent.resume",
        &serde_json::json!({ "agent_id": agent_id }),
        &supervisor,
    )
    .await
    .unwrap();
    let resumed: serde_json::Value =
        serde_json::from_slice(&tokio::fs::read(&profile_path).await.unwrap()).unwrap();
    assert_eq!(resumed["status"], "active");

    execute_local_command(
        &state,
        "agent.retire",
        &serde_json::json!({ "agent_id": agent_id }),
        &supervisor,
    )
    .await
    .unwrap();
    let retired: serde_json::Value =
        serde_json::from_slice(&tokio::fs::read(profile_path).await.unwrap()).unwrap();
    assert_eq!(retired["status"], "retired");
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
        &serde_json::json!({ "agent_id": agent_id, "path": "notes/current.md" }),
    )
    .await
    .unwrap();
    assert_eq!(content.path, "notes/current.md");
    assert_eq!(content.content, "Current facts.\n");

    let escaped = read_memory_file(
        &state,
        &serde_json::json!({ "agent_id": agent_id, "path": "../profile.json" }),
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
            &serde_json::json!({ "agent_id": agent_id, "path": "linked.md" }),
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
