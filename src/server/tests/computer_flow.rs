use super::*;

pub(super) async fn run(database_url: &str) -> Result<()> {
    let pool = database::connect_postgres(database_url).await?;
    let web_dist = tempdir()?;
    std::fs::write(web_dist.path().join("index.html"), "<!doctype html>")?;
    let app = router(
        pool.clone(),
        ServerConfig {
            database_url: database_url.to_owned(),
            web_dist: web_dist.path().to_owned(),
            attachment_dir: web_dist.path().join("attachments"),
            auth_ip_attempts_per_minute: 100,
            auth_email_attempts_per_minute: 100,
            ..ServerConfig::default()
        },
    )?;
    let owner = register_human(&app, "Owner", "owner@example.test", "correct-horse-owner").await?;
    let member = register_human(
        &app,
        "Member",
        "member@example.test",
        "correct-horse-member",
    )
    .await?;
    let space_response = app
        .clone()
        .oneshot(json_request(
            "/api/v1/spaces",
            Uuid::now_v7(),
            &serde_json::json!({
                "name": "Computer Lab",
                "slug": "computer-lab",
                "accent": "#6B8F71"
            }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(space_response.status() == StatusCode::CREATED);
    let space: SpaceResponse = decode_json(space_response).await?;
    let member_id = Uuid::now_v7();
    let now = time::OffsetDateTime::now_utc();
    sqlx::query(
        "INSERT INTO members (id, space_id, kind, display_name, handle, avatar_seed, \
         access_level, created_at) VALUES ($1, $2, 'human', 'Member', 'member', $3, 'member', $4)",
    )
    .bind(member_id)
    .bind(space.id)
    .bind(member_id.to_string())
    .bind(now)
    .execute(&pool)
    .await?;
    sqlx::query("INSERT INTO human_members (member_id, space_id, user_id) VALUES ($1, $2, $3)")
        .bind(member_id)
        .bind(space.id)
        .bind(member.user_id)
        .execute(&pool)
        .await?;

    let token = URL_SAFE_NO_PAD.encode([23_u8; 32]);
    let start = app
        .clone()
        .oneshot(json_request(
            "/api/v1/computer-pairings/start",
            Uuid::now_v7(),
            &serde_json::json!({
                "token_hash": URL_SAFE_NO_PAD.encode(sha2::Sha256::digest(token.as_bytes())),
                "hostname": "computer-test.local",
                "os": "macos",
                "daemon_version": "0.1.0"
            }),
            None,
        )?)
        .await?;
    ensure!(start.status() == StatusCode::CREATED);
    let pairing: PairingStartResponse = decode_json(start).await?;
    let details = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!(
                    "/api/v1/computer-pairings/{}?code={}",
                    pairing.pairing_id, pairing.code
                ))
                .header(header::COOKIE, &owner.cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(details.status() == StatusCode::OK);
    let details: PairingDetailsResponse = decode_json(details).await?;
    ensure!(
        details.token_fingerprint
            == sha2::Sha256::digest(token.as_bytes())[..6]
                .iter()
                .map(|byte| format!("{byte:02x}"))
                .collect::<Vec<_>>()
                .join(":")
    );
    let wrong_token_result = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!(
                    "/api/v1/computer-pairings/{}/result",
                    pairing.pairing_id
                ))
                .header(header::AUTHORIZATION, "Bearer wrong-token")
                .body(Body::empty())?,
        )
        .await?;
    ensure!(wrong_token_result.status() == StatusCode::UNAUTHORIZED);

    let member_denied = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/computer-pairings/{}/confirm", pairing.pairing_id),
            Uuid::now_v7(),
            &serde_json::json!({ "space_id": space.id, "name": "Desk", "code": pairing.code.clone() }),
            Some(&member.cookie),
        )?)
        .await?;
    ensure!(member_denied.status() == StatusCode::FORBIDDEN);

    let wrong_code = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/computer-pairings/{}/confirm", pairing.pairing_id),
            Uuid::now_v7(),
            &serde_json::json!({ "space_id": space.id, "name": "Desk", "code": "wrong" }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(wrong_code.status() == StatusCode::FORBIDDEN);

    let confirm_key = Uuid::now_v7();
    let confirmed = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/computer-pairings/{}/confirm", pairing.pairing_id),
            confirm_key,
            &serde_json::json!({ "space_id": space.id, "name": "Desk", "code": pairing.code.clone() }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(confirmed.status() == StatusCode::CREATED);
    let computer: ComputerResponse = decode_json(confirmed).await?;

    for _ in 0..2 {
        let result = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!(
                        "/api/v1/computer-pairings/{}/result",
                        pairing.pairing_id
                    ))
                    .header(header::AUTHORIZATION, format!("Bearer {token}"))
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(result.status() == StatusCode::OK);
        match decode_json::<PairingResultResponse>(result).await? {
            PairingResultResponse::Confirmed {
                computer_id,
                space_id,
            } => {
                ensure!(computer_id == computer.id && space_id == space.id);
            }
            PairingResultResponse::Pending => {
                anyhow::bail!("confirmed pairing returned pending")
            }
        }
    }
    let command_id = Uuid::now_v7();
    sqlx::query(
        "WITH allocated AS ( \
             UPDATE computers SET next_command_seq = next_command_seq + 1 WHERE id = $2 \
             RETURNING next_command_seq - 1 AS computer_seq \
         ) INSERT INTO computer_commands \
         (id, computer_id, computer_seq, kind, payload_json, created_at) \
         SELECT $1, $2, computer_seq, 'agent.provision', $3, now() FROM allocated",
    )
    .bind(command_id)
    .bind(computer.id)
    .bind(serde_json::json!({ "agent_id": Uuid::now_v7() }))
    .execute(&pool)
    .await?;
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await?;
    let address = listener.local_addr()?;
    let server_app = app.clone();
    let server_task = tokio::spawn(async move {
        axum::serve(
            listener,
            server_app.into_make_service_with_connect_info::<SocketAddr>(),
        )
        .await
    });
    let mut request =
        format!("ws://{address}/api/v1/computers/{}/connect", computer.id).into_client_request()?;
    request
        .headers_mut()
        .insert(header::AUTHORIZATION, format!("Bearer {token}").parse()?);
    let (mut socket, _) = tokio_tungstenite::connect_async(request).await?;
    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "hello",
                "last_acked_computer_seq": 0
            })
            .to_string()
            .into(),
        ))
        .await?;
    let welcome = socket.next().await.context("Computer welcome missing")??;
    ensure!(welcome.to_text()?.contains("\"type\":\"welcome\""));
    let command = socket.next().await.context("Computer command missing")??;
    ensure!(command.to_text()?.contains(&command_id.to_string()));
    for frame in [
        serde_json::json!({
            "type": "command_ack", "command_id": command_id, "computer_seq": 1
        }),
        serde_json::json!({
            "type": "command_result", "command_id": command_id, "computer_seq": 1,
            "ok": true, "result": { "ok": true }
        }),
    ] {
        socket
            .send(tokio_tungstenite::tungstenite::Message::Text(
                frame.to_string().into(),
            ))
            .await?;
    }
    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "heartbeat",
                "daemon_version": "0.1.0",
                "os": "macos",
                "cpu_count": 8,
                "memory_total_bytes": 16_000_000_000_u64,
                "agents_count": 0,
                "active_runs": 0
            })
            .to_string()
            .into(),
        ))
        .await?;
    tokio::time::sleep(std::time::Duration::from_millis(50)).await;
    let online_status: String = sqlx::query_scalar("SELECT status FROM computers WHERE id = $1")
        .bind(computer.id)
        .fetch_one(&pool)
        .await?;
    ensure!(online_status == "online");
    let command_status: String =
        sqlx::query_scalar("SELECT status FROM computer_commands WHERE id = $1")
            .bind(command_id)
            .fetch_one(&pool)
            .await?;
    ensure!(command_status == "completed");

    let agent_created = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/spaces/{}/agents", space.id),
            Uuid::now_v7(),
            &serde_json::json!({
                "computer_id": computer.id,
                "name": "Lin",
                "handle": "lin",
                "role_text": "Review implementation boundaries and report concrete risks.",
                "access_level": "member",
                "driver_kind": "builtin"
            }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(agent_created.status() == StatusCode::CREATED);
    let agent: AgentResponse = decode_json(agent_created).await?;
    ensure!(agent.status == "provisioning" && agent.name == "Lin");
    let provision = tokio::time::timeout(std::time::Duration::from_secs(2), socket.next())
        .await?
        .context("Agent provision command missing")??;
    let provision: serde_json::Value = serde_json::from_str(provision.to_text()?)?;
    let provision_command_id = Uuid::parse_str(
        provision["command_id"]
            .as_str()
            .context("provision command id missing")?,
    )?;
    ensure!(provision["payload"]["agent_id"] == agent.member_id.to_string());
    ensure!(provision["payload"]["driver_kind"] == "builtin");
    let provision_seq = provision["computer_seq"]
        .as_i64()
        .context("provision sequence missing")?;
    for frame in [
        serde_json::json!({
            "type": "command_ack", "command_id": provision_command_id,
            "computer_seq": provision_seq
        }),
        serde_json::json!({
            "type": "command_result", "command_id": provision_command_id,
            "computer_seq": provision_seq, "ok": false,
            "result": { "ok": false, "error_code": "driver_unavailable" }
        }),
    ] {
        socket
            .send(tokio_tungstenite::tungstenite::Message::Text(
                frame.to_string().into(),
            ))
            .await?;
    }
    tokio::time::timeout(std::time::Duration::from_secs(2), async {
        loop {
            let state: (String, Option<String>) =
                sqlx::query_as("SELECT status, last_error_code FROM agents WHERE member_id = $1")
                    .bind(agent.member_id)
                    .fetch_one(&pool)
                    .await
                    .expect("Agent error query must succeed");
            if state == ("error".to_owned(), Some("driver_unavailable".to_owned())) {
                break;
            }
            tokio::time::sleep(std::time::Duration::from_millis(10)).await;
        }
    })
    .await
    .context("Failed provision did not retain its retryable reason")?;
    let retried = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/agents/{}", agent.member_id),
            Uuid::now_v7(),
            &serde_json::json!({ "lifecycle": { "action": "retry" } }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(retried.status() == StatusCode::OK);
    let retried: AgentResponse = decode_json(retried).await?;
    ensure!(retried.status == "provisioning" && retried.last_error_code.is_none());
    let provision = socket
        .next()
        .await
        .context("Retried Agent provision command missing")??;
    let provision: serde_json::Value = serde_json::from_str(provision.to_text()?)?;
    ensure!(provision["kind"] == "agent.provision");
    ensure!(provision["payload"]["name"] == "Lin");
    let provision_command_id = Uuid::parse_str(
        provision["command_id"]
            .as_str()
            .context("retried provision command id missing")?,
    )?;
    let provision_seq = provision["computer_seq"]
        .as_i64()
        .context("retried provision sequence missing")?;
    for frame in [
        serde_json::json!({
            "type": "command_ack", "command_id": provision_command_id,
            "computer_seq": provision_seq
        }),
        serde_json::json!({
            "type": "command_result", "command_id": provision_command_id,
            "computer_seq": provision_seq, "ok": true, "result": {
                "ok": true,
                "memory_files": [{
                    "path": "MEMORY.md",
                    "size": 9,
                    "sha256": hex::encode(sha2::Sha256::digest(b"# Memory\n")),
                    "updated_at": "2026-07-25T00:00:00Z"
                }]
            }
        }),
    ] {
        socket
            .send(tokio_tungstenite::tungstenite::Message::Text(
                frame.to_string().into(),
            ))
            .await?;
    }
    let provision_applied = tokio::time::timeout(std::time::Duration::from_secs(2), async {
        loop {
            let status: String =
                sqlx::query_scalar("SELECT status FROM agents WHERE member_id = $1")
                    .bind(agent.member_id)
                    .fetch_one(&pool)
                    .await
                    .expect("Agent status query must succeed");
            if status == "active" {
                break;
            }
            tokio::time::sleep(std::time::Duration::from_millis(10)).await;
        }
    })
    .await;
    if provision_applied.is_err() {
        let command_state: (String, Option<serde_json::Value>) =
            sqlx::query_as("SELECT status, result_json FROM computer_commands WHERE id = $1")
                .bind(provision_command_id)
                .fetch_one(&pool)
                .await?;
        let agent_state: String =
            sqlx::query_scalar("SELECT status FROM agents WHERE member_id = $1")
                .bind(agent.member_id)
                .fetch_one(&pool)
                .await?;
        anyhow::bail!(
            "Agent provision result was not applied: command={command_state:?}, agent={agent_state}, server_finished={}",
            server_task.is_finished()
        );
    }
    let agent_invariants: (String, String, i64) = sqlx::query_as(
        "SELECT agents.status, members.kind, \
         (SELECT count(*) FROM channel_members cm JOIN channels c ON c.id = cm.channel_id \
          WHERE cm.member_id = agents.member_id AND c.slug = 'general') \
         FROM agents JOIN members ON members.id = agents.member_id WHERE agents.member_id = $1",
    )
    .bind(agent.member_id)
    .fetch_one(&pool)
    .await?;
    ensure!(agent_invariants == ("active".to_owned(), "agent".to_owned(), 1));

    let agent_detail = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/api/v1/agents/{}", agent.member_id))
                .header(header::COOKIE, &owner.cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(agent_detail.status() == StatusCode::OK);
    let agent_detail: AgentResponse = decode_json(agent_detail).await?;
    ensure!(agent_detail.memory_files.len() == 1);
    ensure!(agent_detail.memory_files[0].path == "MEMORY.md");
    ensure!(agent_detail.activity_status == "idle");

    let memory_app = app.clone();
    let memory_cookie = owner.cookie.clone();
    let memory_agent_id = agent.member_id;
    let memory_request = tokio::spawn(async move {
        memory_app
            .oneshot(json_request(
                &format!("/api/v1/agents/{memory_agent_id}/memory/read"),
                Uuid::now_v7(),
                &serde_json::json!({ "path": "MEMORY.md" }),
                Some(&memory_cookie),
            )?)
            .await
            .map_err(anyhow::Error::from)
    });
    let memory_command = tokio::time::timeout(std::time::Duration::from_secs(2), socket.next())
        .await?
        .context("Agent Memory read command missing")??;
    let memory_command: serde_json::Value = serde_json::from_str(memory_command.to_text()?)?;
    ensure!(memory_command["kind"] == "agent.memory.read");
    ensure!(memory_command["payload"]["path"] == "MEMORY.md");
    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "command_result",
                "command_id": memory_command["command_id"],
                "computer_seq": memory_command["computer_seq"],
                "ok": true,
                "result": {
                    "path": "MEMORY.md",
                    "content": "# Memory\n",
                    "size": 9,
                    "sha256": hex::encode(sha2::Sha256::digest(b"# Memory\n")),
                    "updated_at": "2026-07-25T00:00:00Z"
                }
            })
            .to_string()
            .into(),
        ))
        .await?;
    let memory_response = memory_request.await??;
    ensure!(memory_response.status() == StatusCode::OK);
    ensure!(
        memory_response
            .headers()
            .get(header::CACHE_CONTROL)
            .and_then(|value| value.to_str().ok())
            == Some("no-store")
    );
    let memory: MemoryContentResponse = decode_json(memory_response).await?;
    ensure!(memory.path == "MEMORY.md" && memory.content == "# Memory\n");
    let stored_memory_result: serde_json::Value =
        sqlx::query_scalar("SELECT result_json FROM computer_commands WHERE id = $1")
            .bind(Uuid::parse_str(
                memory_command["command_id"]
                    .as_str()
                    .context("Memory command id missing")?,
            )?)
            .fetch_one(&pool)
            .await?;
    ensure!(stored_memory_result.get("content").is_none());

    let configured = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/agents/{}", agent.member_id),
            Uuid::now_v7(),
            &serde_json::json!({
                "role_text": "Review boundaries and enforce the current specification.",
                "attention_config": {
                    "dm_immediate": true,
                    "mention_immediate": true,
                    "ambient_enabled": false,
                    "ambient_debounce_seconds": 8,
                    "ambient_max_wait_seconds": 40,
                    "max_retry_count": 4
                }
            }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(configured.status() == StatusCode::OK);
    let configured: AgentResponse = decode_json(configured).await?;
    ensure!(configured.role_revision == 2);
    ensure!(!configured.attention_config.ambient_enabled);
    let configure = socket
        .next()
        .await
        .context("Agent configure command missing")??;
    let configure: serde_json::Value = serde_json::from_str(configure.to_text()?)?;
    ensure!(configure["kind"] == "agent.configure");
    ensure!(configure["payload"]["role_revision"] == 2);
    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "command_result",
                "command_id": configure["command_id"],
                "computer_seq": configure["computer_seq"],
                "ok": true,
                "result": { "ok": true, "memory_files": [] }
            })
            .to_string()
            .into(),
        ))
        .await?;

    let suspended = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/agents/{}", agent.member_id),
            Uuid::now_v7(),
            &serde_json::json!({
                "lifecycle": { "action": "suspend", "mode": "cancel_now" }
            }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(suspended.status() == StatusCode::OK);
    let suspended: AgentResponse = decode_json(suspended).await?;
    ensure!(suspended.status == "suspended");
    let suspend = socket
        .next()
        .await
        .context("Agent suspend command missing")??;
    let suspend: serde_json::Value = serde_json::from_str(suspend.to_text()?)?;
    ensure!(suspend["kind"] == "agent.suspend");
    ensure!(suspend["payload"]["mode"] == "cancel_now");
    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "command_result",
                "command_id": suspend["command_id"],
                "computer_seq": suspend["computer_seq"],
                "ok": true,
                "result": { "ok": true, "memory_files": [] }
            })
            .to_string()
            .into(),
        ))
        .await?;

    let resumed = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/agents/{}", agent.member_id),
            Uuid::now_v7(),
            &serde_json::json!({ "lifecycle": { "action": "resume" } }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(resumed.status() == StatusCode::OK);
    let resumed: AgentResponse = decode_json(resumed).await?;
    ensure!(resumed.status == "active");
    let resume = socket
        .next()
        .await
        .context("Agent resume command missing")??;
    let resume: serde_json::Value = serde_json::from_str(resume.to_text()?)?;
    ensure!(resume["kind"] == "agent.resume");
    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "command_result",
                "command_id": resume["command_id"],
                "computer_seq": resume["computer_seq"],
                "ok": true,
                "result": { "ok": true, "memory_files": [] }
            })
            .to_string()
            .into(),
        ))
        .await?;
    tokio::time::sleep(std::time::Duration::from_millis(50)).await;
    let lifecycle_state: (String, i64, i64) = sqlx::query_as(
        "SELECT status, role_revision, \
         (SELECT count(*) FROM audit_events WHERE subject_id = agents.member_id \
          AND action IN ('agent.configured', 'agent.suspended', 'agent.resumed')) \
         FROM agents WHERE member_id = $1",
    )
    .bind(agent.member_id)
    .fetch_one(&pool)
    .await?;
    ensure!(lifecycle_state == ("active".to_owned(), 2, 3));

    let dm = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/spaces/{}/dms", space.id),
            Uuid::now_v7(),
            &serde_json::json!({ "member_id": agent.member_id }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(dm.status() == StatusCode::CREATED);
    let dm: DirectMessageResponse = decode_json(dm).await?;
    let human_message = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/messages", dm.channel_id),
            Uuid::now_v7(),
            &serde_json::json!({
                "body_markdown": "Please review the current boundary.",
                "mentions": [],
                "attachment_ids": []
            }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(human_message.status() == StatusCode::CREATED);
    let human_message: MessageResponse = decode_json(human_message).await?;
    let inbox_item_id: Uuid = sqlx::query_scalar(
        "SELECT id FROM inbox_items WHERE member_id = $1 AND message_id = $2 \
         AND kind = 'direct' AND status = 'pending'",
    )
    .bind(agent.member_id)
    .bind(human_message.id)
    .fetch_one(&pool)
    .await?;

    let claim = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri(format!(
                    "/api/v1/computers/{}/agents/{}/inbox/claim",
                    computer.id, agent.member_id
                ))
                .header(header::AUTHORIZATION, format!("Bearer {token}"))
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from("{}"))?,
        )
        .await?;
    ensure!(claim.status() == StatusCode::OK);
    let claim: serde_json::Value = decode_json(claim).await?;
    ensure!(claim["claimed"] == true);
    let run_id = Uuid::parse_str(claim["run_id"].as_str().context("claimed run id missing")?)?;
    let busy_agent = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/api/v1/agents/{}", agent.member_id))
                .header(header::COOKIE, &owner.cookie)
                .body(Body::empty())?,
        )
        .await?;
    let busy_agent: AgentResponse = decode_json(busy_agent).await?;
    ensure!(busy_agent.activity_status == "busy");
    let run_command = tokio::time::timeout(std::time::Duration::from_secs(2), socket.next())
        .await?
        .context("Agent run command missing")??;
    let run_command: serde_json::Value = serde_json::from_str(run_command.to_text()?)?;
    ensure!(run_command["kind"] == "agent.run");
    ensure!(run_command["payload"]["driver_kind"] == "builtin");
    let prompt = &run_command["payload"]["prompt"];
    let rendered_prompt = [
        "global_static",
        "agent_static",
        "dynamic_context",
        "user_input",
    ]
    .into_iter()
    .filter_map(|field| prompt[field].as_str())
    .collect::<Vec<_>>()
    .join("\n\n");
    ensure!(rendered_prompt.contains("sumi agent inbox current --json"));
    ensure!(rendered_prompt.contains("Review boundaries and enforce"));
    ensure!(rendered_prompt.contains("@owner"));
    ensure!(!rendered_prompt.contains("Task Board"));
    ensure!(!rendered_prompt.contains("proposal-only"));
    ensure!(
        prompt["cache_key"]
            .as_str()
            .is_some_and(|key| !key.is_empty())
    );
    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "command_ack",
                "command_id": run_command["command_id"],
                "computer_seq": run_command["computer_seq"]
            })
            .to_string()
            .into(),
        ))
        .await?;
    tokio::time::timeout(std::time::Duration::from_secs(2), async {
        loop {
            let status: String = sqlx::query_scalar("SELECT status FROM agent_runs WHERE id = $1")
                .bind(run_id)
                .fetch_one(&pool)
                .await
                .expect("Agent run query must succeed");
            if status == "running" {
                break;
            }
            tokio::time::sleep(std::time::Duration::from_millis(10)).await;
        }
    })
    .await
    .context("Agent run did not enter running")?;

    let denied_create = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "channel_create",
                "slug": "agent-private",
                "name": "Agent Private",
                "private": true,
                "idempotency_key": Uuid::now_v7()
            }),
        )?)
        .await?;
    ensure!(denied_create.status() == StatusCode::FORBIDDEN);
    let denied_agent_create = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "agent_create",
                "name": "Denied Child",
                "role_text": "This request must be denied.",
                "computer_id": computer.id,
                "driver_kind": "codex",
                "idempotency_key": Uuid::now_v7()
            }),
        )?)
        .await?;
    ensure!(denied_agent_create.status() == StatusCode::FORBIDDEN);

    let grant_human_agent_create = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/spaces/{}/members/{member_id}", space.id),
            Uuid::now_v7(),
            &serde_json::json!({ "permissions": ["agent:create"] }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(grant_human_agent_create.status() == StatusCode::OK);
    let human_approval = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/spaces/{}/agents", space.id),
            Uuid::now_v7(),
            &serde_json::json!({
                "computer_id": computer.id,
                "name": "Human Requested Child",
                "handle": null,
                "role_text": "Review changes requested by a Human Member.",
                "access_level": "member",
                "driver_kind": "codex"
            }),
            Some(&member.cookie),
        )?)
        .await?;
    ensure!(human_approval.status() == StatusCode::ACCEPTED);
    let human_approval: serde_json::Value = decode_json(human_approval).await?;
    ensure!(human_approval["status"] == "pending");
    let human_approval_id = Uuid::parse_str(
        human_approval["approval_id"]
            .as_str()
            .context("Human Approval id missing")?,
    )?;
    let human_pending_invariants: (i64, i64, i64) = sqlx::query_as(
        "SELECT \
         (SELECT count(*) FROM approvals WHERE id = $1 AND requested_by_member_id = $2 \
            AND status = 'pending'), \
         (SELECT count(*) FROM members WHERE space_id = $3 \
            AND display_name = 'Human Requested Child'), \
         (SELECT count(*) FROM computer_commands \
            WHERE payload_json->>'name' = 'Human Requested Child')",
    )
    .bind(human_approval_id)
    .bind(member_id)
    .bind(space.id)
    .fetch_one(&pool)
    .await?;
    ensure!(human_pending_invariants == (1, 0, 0));
    let promote_requester = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/spaces/{}/members/{member_id}", space.id),
            Uuid::now_v7(),
            &serde_json::json!({ "access_level": "admin" }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(promote_requester.status() == StatusCode::OK);
    let requester_cannot_self_approve = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/approvals/{human_approval_id}/approve"),
            Uuid::now_v7(),
            &serde_json::json!({}),
            Some(&member.cookie),
        )?)
        .await?;
    ensure!(requester_cannot_self_approve.status() == StatusCode::FORBIDDEN);
    let human_request_rejected = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/approvals/{human_approval_id}/reject"),
            Uuid::now_v7(),
            &serde_json::json!({}),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(human_request_rejected.status() == StatusCode::OK);

    let owners_private = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/spaces/{}/channels", space.id),
            Uuid::now_v7(),
            &serde_json::json!({
                "slug": "owners-private",
                "name": "Owners Private",
                "kind": "private",
                "topic": null,
                "agent_member_ids": []
            }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(owners_private.status() == StatusCode::CREATED);
    let owners_private: ChannelResponse = decode_json(owners_private).await?;
    let private_read = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "channel_read",
                "address": "#owners-private",
                "before": null,
                "after": null,
                "around": null,
                "limit": 50
            }),
        )?)
        .await?;
    ensure!(private_read.status() == StatusCode::FORBIDDEN);

    let add_agent = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/members", owners_private.id),
            Uuid::now_v7(),
            &serde_json::json!({ "agent_member_ids": [agent.member_id] }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(add_agent.status() == StatusCode::OK);
    let channel_members: ChannelMembersResponse = decode_json(add_agent).await?;
    ensure!(channel_members.can_manage);
    ensure!(
        channel_members
            .members
            .iter()
            .any(|member| member.id == agent.member_id)
    );

    let private_read_after_add = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "channel_read",
                "address": "#owners-private",
                "before": null,
                "after": null,
                "around": null,
                "limit": 50
            }),
        )?)
        .await?;
    ensure!(private_read_after_add.status() == StatusCode::OK);

    let grant_channel_create = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/spaces/{}/members/{}", space.id, agent.member_id),
            Uuid::now_v7(),
            &serde_json::json!({ "permissions": ["channel:create"] }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(grant_channel_create.status() == StatusCode::OK);
    let create_key = Uuid::now_v7();
    let create_action = serde_json::json!({
        "action": "channel_create",
        "slug": "agent-private",
        "name": "Agent Private",
        "private": true,
        "idempotency_key": create_key
    });
    let mut created_channel_id = None;
    for _ in 0..2 {
        let created = app
            .clone()
            .oneshot(computer_agent_action_request(
                computer.id,
                &token,
                agent.member_id,
                run_id,
                create_action.clone(),
            )?)
            .await?;
        ensure!(created.status() == StatusCode::OK);
        let created: ChannelResponse = decode_json(created).await?;
        ensure!(created.kind == "private" && created.joined);
        ensure!(created.created_by_member_id == agent.member_id);
        if let Some(expected) = created_channel_id {
            ensure!(created.id == expected);
        } else {
            created_channel_id = Some(created.id);
        }
    }
    let public_created = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "channel_create",
                "slug": "agent-public",
                "name": "Agent Public",
                "private": false,
                "idempotency_key": Uuid::now_v7()
            }),
        )?)
        .await?;
    ensure!(public_created.status() == StatusCode::OK);
    let public_created: ChannelResponse = decode_json(public_created).await?;
    ensure!(public_created.kind == "public" && public_created.joined);
    let created_invariants: (i64, i64, i64) = sqlx::query_as(
        "SELECT (SELECT count(*) FROM channels WHERE space_id = $1 AND slug = 'agent-private'), \
         (SELECT count(*) FROM channel_members WHERE channel_id = $2 AND member_id = $3), \
         (SELECT count(*) FROM audit_events WHERE subject_id = $2 AND action = 'channel.created')",
    )
    .bind(space.id)
    .bind(created_channel_id.context("created Channel id missing")?)
    .bind(agent.member_id)
    .fetch_one(&pool)
    .await?;
    ensure!(created_invariants == (1, 1, 1));

    let promote_agent = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/spaces/{}/members/{}", space.id, agent.member_id),
            Uuid::now_v7(),
            &serde_json::json!({ "access_level": "admin" }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(promote_agent.status() == StatusCode::OK);
    let explicit_permissions_after_promotion: i64 =
        sqlx::query_scalar("SELECT count(*) FROM member_permissions WHERE member_id = $1")
            .bind(agent.member_id)
            .fetch_one(&pool)
            .await?;
    ensure!(explicit_permissions_after_promotion == 0);
    let admin_channel = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "channel_create",
                "slug": "agent-admin-channel",
                "name": "Agent Admin Channel",
                "private": false,
                "idempotency_key": Uuid::now_v7()
            }),
        )?)
        .await?;
    ensure!(admin_channel.status() == StatusCode::OK);

    let other_space = app
        .clone()
        .oneshot(json_request(
            "/api/v1/spaces",
            Uuid::now_v7(),
            &serde_json::json!({
                "name": "Other Agent Lab",
                "slug": "other-agent-lab",
                "accent": "#B08A5A"
            }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(other_space.status() == StatusCode::CREATED);
    let other_space: SpaceResponse = decode_json(other_space).await?;
    let other_computer_id = Uuid::now_v7();
    sqlx::query(
        "INSERT INTO computers \
         (id, space_id, name, hostname, os, token_hash, status, \
          daemon_version, last_seen_at, created_at) \
         VALUES ($1, $2, 'Other Computer', 'other.local', 'macos', $3, 'online', \
                 '0.1.0', now(), now())",
    )
    .bind(other_computer_id)
    .bind(other_space.id)
    .bind(vec![8_u8; 32])
    .execute(&pool)
    .await?;
    let cross_space_computer = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "agent_create",
                "name": "Cross Space Child",
                "role_text": "This request must not cross Space boundaries.",
                "computer_id": other_computer_id,
                "driver_kind": "codex",
                "idempotency_key": Uuid::now_v7()
            }),
        )?)
        .await?;
    ensure!(cross_space_computer.status() == StatusCode::NOT_FOUND);

    let approval_key = Uuid::now_v7();
    let approval_action = serde_json::json!({
        "action": "agent_create",
        "name": "Reviewer Child",
        "role_text": "Review the requested implementation.",
        "computer_id": computer.id,
        "driver_kind": "codex",
        "idempotency_key": approval_key
    });
    let mut approval_id = None;
    for _ in 0..2 {
        let requested = app
            .clone()
            .oneshot(computer_agent_action_request(
                computer.id,
                &token,
                agent.member_id,
                run_id,
                approval_action.clone(),
            )?)
            .await?;
        ensure!(requested.status() == StatusCode::OK);
        let requested: serde_json::Value = decode_json(requested).await?;
        ensure!(requested["status"] == "pending");
        let current_id = Uuid::parse_str(
            requested["approval_id"]
                .as_str()
                .context("approval id missing")?,
        )?;
        if let Some(expected) = approval_id {
            ensure!(current_id == expected);
        } else {
            approval_id = Some(current_id);
        }
    }
    let approval_id = approval_id.context("approval was not created")?;
    let pending_invariants: (i64, i64, i64) = sqlx::query_as(
        "SELECT \
         (SELECT count(*) FROM approvals WHERE id = $1 AND status = 'pending'), \
         (SELECT count(*) FROM members WHERE space_id = $2 AND display_name = 'Reviewer Child'), \
         (SELECT count(*) FROM computer_commands WHERE payload_json->>'name' = 'Reviewer Child')",
    )
    .bind(approval_id)
    .bind(space.id)
    .fetch_one(&pool)
    .await?;
    ensure!(pending_invariants == (1, 0, 0));

    let idempotency_conflict = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "agent_create",
                "name": "Conflicting Child",
                "role_text": "A different payload.",
                "computer_id": computer.id,
                "driver_kind": "codex",
                "idempotency_key": approval_key
            }),
        )?)
        .await?;
    ensure!(idempotency_conflict.status() == StatusCode::CONFLICT);

    let computer_token_cannot_approve = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri(format!("/api/v1/approvals/{approval_id}/approve"))
                .header(header::AUTHORIZATION, format!("Bearer {token}"))
                .header("idempotency-key", Uuid::now_v7().to_string())
                .body(Body::from("{}"))?,
        )
        .await?;
    ensure!(computer_token_cannot_approve.status() == StatusCode::UNAUTHORIZED);

    let rejected = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/approvals/{approval_id}/reject"),
            Uuid::now_v7(),
            &serde_json::json!({}),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(rejected.status() == StatusCode::OK);
    let rejected: ApprovalResponse = decode_json(rejected).await?;
    ensure!(rejected.status == "rejected");
    let rejected_side_effects: (i64, i64) = sqlx::query_as(
        "SELECT \
         (SELECT count(*) FROM members WHERE space_id = $1 AND display_name = 'Reviewer Child'), \
         (SELECT count(*) FROM computer_commands WHERE payload_json->>'name' = 'Reviewer Child')",
    )
    .bind(space.id)
    .fetch_one(&pool)
    .await?;
    ensure!(rejected_side_effects == (0, 0));

    let approved_request = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "agent_create",
                "name": "Provisioned Child",
                "role_text": "Review the current implementation.",
                "computer_id": computer.id,
                "driver_kind": "builtin",
                "idempotency_key": Uuid::now_v7()
            }),
        )?)
        .await?;
    ensure!(approved_request.status() == StatusCode::OK);
    let approved_request: serde_json::Value = decode_json(approved_request).await?;
    let approved_id = Uuid::parse_str(
        approved_request["approval_id"]
            .as_str()
            .context("second approval id missing")?,
    )?;
    let approved = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/approvals/{approved_id}/approve"),
            Uuid::now_v7(),
            &serde_json::json!({}),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(approved.status() == StatusCode::OK);
    let approved: ApprovalResponse = decode_json(approved).await?;
    ensure!(approved.status == "approved");
    let approved_invariants: (i64, i64, i64, i64) = sqlx::query_as(
        "SELECT \
         (SELECT count(*) FROM members WHERE space_id = $1 AND kind = 'agent' \
          AND display_name = 'Provisioned Child'), \
         (SELECT count(*) FROM agents JOIN members ON members.id = agents.member_id \
          WHERE agents.space_id = $1 AND members.display_name = 'Provisioned Child' \
            AND agents.status = 'provisioning'), \
         (SELECT count(*) FROM computer_commands WHERE computer_id = $2 \
          AND kind = 'agent.provision' AND payload_json->>'name' = 'Provisioned Child'), \
         (SELECT count(*) FROM inbox_items WHERE approval_id = $3 AND status = 'handled')",
    )
    .bind(space.id)
    .bind(computer.id)
    .bind(approved_id)
    .fetch_one(&pool)
    .await?;
    ensure!(approved_invariants == (1, 1, 1, 2));
    let child_provision = tokio::time::timeout(std::time::Duration::from_secs(2), async {
        loop {
            let frame = socket
                .next()
                .await
                .context("Computer command stream ended")??;
            let command: serde_json::Value = serde_json::from_str(frame.to_text()?)?;
            if command["kind"] == "agent.provision"
                && command["payload"]["name"] == "Provisioned Child"
            {
                ensure!(command["payload"]["driver_kind"] == "builtin");
                break Ok::<_, anyhow::Error>(command);
            }
        }
    })
    .await?
    .context("approved Agent provision command missing")?;
    for frame_type in ["command_ack", "command_result"] {
        let mut frame = serde_json::json!({
            "type": frame_type,
            "command_id": child_provision["command_id"],
            "computer_seq": child_provision["computer_seq"]
        });
        if frame_type == "command_result" {
            frame["ok"] = serde_json::Value::Bool(true);
            frame["result"] = serde_json::json!({ "ok": true, "memory_files": [] });
        }
        socket
            .send(tokio_tungstenite::tungstenite::Message::Text(
                frame.to_string().into(),
            ))
            .await?;
    }

    let general_id: Uuid =
        sqlx::query_scalar("SELECT id FROM channels WHERE space_id = $1 AND slug = 'general'")
            .bind(space.id)
            .fetch_one(&pool)
            .await?;
    let mut general_messages = Vec::new();
    for index in 1..=5 {
        let message = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{general_id}/messages"),
                Uuid::now_v7(),
                &serde_json::json!({
                    "body_markdown": format!("Pagination message {index}"),
                    "mentions": [],
                    "attachment_ids": []
                }),
                Some(&owner.cookie),
            )?)
            .await?;
        ensure!(message.status() == StatusCode::CREATED);
        general_messages.push(decode_json::<MessageResponse>(message).await?);
    }
    for (cursor, expected, expected_before, expected_after) in [
        (
            serde_json::json!({ "after": general_messages[0].seq }),
            vec![general_messages[1].seq, general_messages[2].seq],
            true,
            true,
        ),
        (
            serde_json::json!({ "before": general_messages[4].seq }),
            vec![general_messages[2].seq, general_messages[3].seq],
            true,
            true,
        ),
        (
            serde_json::json!({ "around": general_messages[2].id }),
            vec![
                general_messages[1].seq,
                general_messages[2].seq,
                general_messages[3].seq,
            ],
            true,
            true,
        ),
    ] {
        let mut action = serde_json::json!({
            "action": "channel_read",
            "address": "#general",
            "before": null,
            "after": null,
            "around": null,
            "limit": expected.len()
        });
        for (key, value) in cursor.as_object().context("cursor object missing")? {
            action[key] = value.clone();
        }
        let page = app
            .clone()
            .oneshot(computer_agent_action_request(
                computer.id,
                &token,
                agent.member_id,
                run_id,
                action,
            )?)
            .await?;
        ensure!(page.status() == StatusCode::OK);
        let page: serde_json::Value = decode_json(page).await?;
        let actual = page["messages"]
            .as_array()
            .context("message page missing")?
            .iter()
            .map(|message| message["seq"].as_i64().context("message seq missing"))
            .collect::<Result<Vec<_>>>()?;
        ensure!(actual == expected);
        ensure!(page["has_more_before"] == expected_before);
        ensure!(page["has_more_after"] == expected_after);
    }
    let exhausted_after = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "channel_read",
                "address": "#general",
                "before": null,
                "after": general_messages[4].seq,
                "around": null,
                "limit": 2
            }),
        )?)
        .await?;
    ensure!(exhausted_after.status() == StatusCode::OK);
    let exhausted_after: serde_json::Value = decode_json(exhausted_after).await?;
    ensure!(
        exhausted_after["messages"]
            .as_array()
            .is_some_and(Vec::is_empty)
    );
    ensure!(exhausted_after["has_more_before"] == true);
    ensure!(exhausted_after["has_more_after"] == false);

    let inbox_current = computer_agent_action_request(
        computer.id,
        &token,
        agent.member_id,
        run_id,
        serde_json::json!({ "action": "inbox_current" }),
    )?;
    let inbox_current = app.clone().oneshot(inbox_current).await?;
    ensure!(inbox_current.status() == StatusCode::OK);
    let inbox_current: serde_json::Value = decode_json(inbox_current).await?;
    ensure!(inbox_current["items"][0]["id"] == inbox_item_id.to_string());

    let channel_read = computer_agent_action_request(
        computer.id,
        &token,
        agent.member_id,
        run_id,
        serde_json::json!({
            "action": "channel_read",
            "address": "@owner",
            "before": null,
            "limit": 50
        }),
    )?;
    let channel_read = app.clone().oneshot(channel_read).await?;
    ensure!(channel_read.status() == StatusCode::OK);
    let channel_read: serde_json::Value = decode_json(channel_read).await?;
    ensure!(channel_read["messages"][0]["body_markdown"] == "Please review the current boundary.");
    let snapshot = channel_read["snapshot_channel_seq"]
        .as_i64()
        .context("snapshot sequence missing")?;

    let attachment_bytes = b"agent attachment payload";
    let attachment_sha = hex::encode(Sha256::digest(attachment_bytes));
    let create_attachment = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri(format!(
                    "/api/v1/computers/{}/agents/{}/runs/{run_id}/attachments/uploads",
                    computer.id, agent.member_id
                ))
                .header(header::AUTHORIZATION, format!("Bearer {token}"))
                .header(header::CONTENT_TYPE, "application/json")
                .header("idempotency-key", Uuid::now_v7().to_string())
                .body(Body::from(serde_json::to_vec(&serde_json::json!({
                    "original_name": "report.txt",
                    "media_type": "text/plain"
                }))?))?,
        )
        .await?;
    ensure!(create_attachment.status() == StatusCode::CREATED);
    let created_attachment: AttachmentResponse = decode_json(create_attachment).await?;
    ensure!(created_attachment.uploader_member_id == agent.member_id);

    let upload_attachment = app
        .clone()
        .oneshot(
            Request::builder()
                .method("PUT")
                .uri(format!(
                    "/api/v1/computers/{}/agents/{}/runs/{run_id}/attachments/{}/content",
                    computer.id, agent.member_id, created_attachment.id
                ))
                .header(header::AUTHORIZATION, format!("Bearer {token}"))
                .body(Body::from(attachment_bytes.as_slice()))?,
        )
        .await?;
    ensure!(upload_attachment.status() == StatusCode::NO_CONTENT);

    let complete_attachment = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri(format!(
                    "/api/v1/computers/{}/agents/{}/runs/{run_id}/attachments/{}/complete",
                    computer.id, agent.member_id, created_attachment.id
                ))
                .header(header::AUTHORIZATION, format!("Bearer {token}"))
                .header(header::CONTENT_TYPE, "application/json")
                .header("idempotency-key", Uuid::now_v7().to_string())
                .body(Body::from(serde_json::to_vec(&serde_json::json!({
                    "size": attachment_bytes.len(),
                    "sha256": attachment_sha
                }))?))?,
        )
        .await?;
    ensure!(complete_attachment.status() == StatusCode::OK);
    let completed_attachment: AttachmentResponse = decode_json(complete_attachment).await?;
    ensure!(completed_attachment.status == "ready");

    let attachment_info = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!(
                    "/api/v1/computers/{}/agents/{}/runs/{run_id}/attachments/{}",
                    computer.id, agent.member_id, created_attachment.id
                ))
                .header(header::AUTHORIZATION, format!("Bearer {token}"))
                .body(Body::empty())?,
        )
        .await?;
    ensure!(attachment_info.status() == StatusCode::OK);

    let unlinked_download = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!(
                    "/api/v1/computers/{}/agents/{}/runs/{run_id}/attachments/{}/download",
                    computer.id, agent.member_id, created_attachment.id
                ))
                .header(header::AUTHORIZATION, format!("Bearer {token}"))
                .body(Body::empty())?,
        )
        .await?;
    ensure!(unlinked_download.status() == StatusCode::FORBIDDEN);

    let changed_context = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/messages", dm.channel_id),
            Uuid::now_v7(),
            &serde_json::json!({
                "body_markdown": "One more detail arrived after your read.",
                "mentions": [],
                "attachment_ids": []
            }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(changed_context.status() == StatusCode::CREATED);
    let changed_context: MessageResponse = decode_json(changed_context).await?;
    let stale_send = computer_agent_action_request(
        computer.id,
        &token,
        agent.member_id,
        run_id,
        serde_json::json!({
            "action": "message_send",
            "address": "@owner",
            "body_markdown": "This stale response must not be stored.",
            "based_on": snapshot,
            "handle_inbox_item_id": inbox_item_id,
            "idempotency_key": Uuid::now_v7()
        }),
    )?;
    let stale_send = app.clone().oneshot(stale_send).await?;
    ensure!(stale_send.status() == StatusCode::CONFLICT);
    let stale_error: serde_json::Value = decode_json(stale_send).await?;
    ensure!(stale_error["error"]["code"] == "context_changed");
    ensure!(stale_error["error"]["details"]["snapshot_channel_seq"] == snapshot);
    ensure!(stale_error["error"]["details"]["latest_channel_seq"] == changed_context.seq);
    ensure!(stale_error["error"]["details"]["changes"][0]["address"] == "@owner");
    ensure!(
        stale_error["error"]["details"]["changes"][0]
            .get("body_markdown")
            .is_none()
    );
    let unchanged: (String, i64) = sqlx::query_as(
        "SELECT status, (SELECT count(*) FROM messages WHERE channel_id = $2 \
         AND author_member_id = $3) FROM inbox_items WHERE id = $1",
    )
    .bind(inbox_item_id)
    .bind(dm.channel_id)
    .bind(agent.member_id)
    .fetch_one(&pool)
    .await?;
    ensure!(unchanged == ("leased".to_owned(), 0));
    let refreshed = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "channel_read",
                "address": "@owner",
                "before": null,
                "limit": 50
            }),
        )?)
        .await?;
    ensure!(refreshed.status() == StatusCode::OK);
    let refreshed: serde_json::Value = decode_json(refreshed).await?;
    let refreshed_snapshot = refreshed["snapshot_channel_seq"]
        .as_i64()
        .context("refreshed snapshot sequence missing")?;

    let send_key = Uuid::now_v7();
    let send_action = serde_json::json!({
        "action": "message_send",
        "address": "@owner",
        "body_markdown": "The boundary matches the current specification.",
        "based_on": refreshed_snapshot,
        "handle_inbox_item_id": inbox_item_id,
        "attachment_ids": [created_attachment.id],
        "idempotency_key": send_key
    });
    let send = computer_agent_action_request(
        computer.id,
        &token,
        agent.member_id,
        run_id,
        send_action.clone(),
    )?;
    let send = app.clone().oneshot(send).await?;
    ensure!(send.status() == StatusCode::OK);
    let send: serde_json::Value = decode_json(send).await?;
    ensure!(send["author"]["id"] == agent.member_id.to_string());
    ensure!(send["attachments"][0]["id"] == created_attachment.id.to_string());
    let replayed_send = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            send_action,
        )?)
        .await?;
    ensure!(replayed_send.status() == StatusCode::OK);
    let replayed_send: serde_json::Value = decode_json(replayed_send).await?;
    ensure!(replayed_send["id"] == send["id"]);
    let conflicting_send = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "message_send",
                "address": "@owner",
                "body_markdown": "Different payload.",
                "based_on": refreshed_snapshot,
                "handle_inbox_item_id": inbox_item_id,
                "attachment_ids": [created_attachment.id],
                "idempotency_key": send_key
            }),
        )?)
        .await?;
    ensure!(conflicting_send.status() == StatusCode::CONFLICT);

    let ack_item_id = Uuid::now_v7();
    let defer_item_id = Uuid::now_v7();
    let ack_lease_id = Uuid::now_v7();
    let defer_lease_id = Uuid::now_v7();
    for (item_id, lease_id) in [(ack_item_id, ack_lease_id), (defer_item_id, defer_lease_id)] {
        sqlx::query(
            "INSERT INTO inbox_items (id, member_id, space_id, kind, priority, channel_id, \
             message_id, first_seq, last_seq, status, available_at, lease_id, \
             lease_expires_at, created_at) VALUES ($1, $2, $3, 'direct', 'hard', $4, $5, \
             $6, $6, 'leased', now(), $7, now() + interval '35 minutes', now())",
        )
        .bind(item_id)
        .bind(agent.member_id)
        .bind(space.id)
        .bind(dm.channel_id)
        .bind(changed_context.id)
        .bind(changed_context.seq)
        .bind(lease_id)
        .execute(&pool)
        .await?;
        sqlx::query(
            "INSERT INTO agent_run_inbox_items (run_id, inbox_item_id, lease_id) \
             VALUES ($1, $2, $3)",
        )
        .bind(run_id)
        .bind(item_id)
        .bind(lease_id)
        .execute(&pool)
        .await?;
    }
    let ack_key = Uuid::now_v7();
    let ack_action = serde_json::json!({
        "action": "inbox_ack",
        "inbox_item_ids": [ack_item_id],
        "reason": "No response needed.",
        "idempotency_key": ack_key
    });
    for _ in 0..2 {
        let acked = app
            .clone()
            .oneshot(computer_agent_action_request(
                computer.id,
                &token,
                agent.member_id,
                run_id,
                ack_action.clone(),
            )?)
            .await?;
        ensure!(acked.status() == StatusCode::OK);
    }
    let conflicting_ack = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "inbox_ack",
                "inbox_item_ids": [ack_item_id],
                "reason": "Different reason.",
                "idempotency_key": ack_key
            }),
        )?)
        .await?;
    ensure!(conflicting_ack.status() == StatusCode::CONFLICT);

    let defer_key = Uuid::now_v7();
    let defer_until = time::OffsetDateTime::now_utc() + time::Duration::hours(1);
    let defer_action = serde_json::json!({
        "action": "inbox_defer",
        "inbox_item_ids": [defer_item_id],
        "until": defer_until,
        "idempotency_key": defer_key
    });
    for _ in 0..2 {
        let deferred = app
            .clone()
            .oneshot(computer_agent_action_request(
                computer.id,
                &token,
                agent.member_id,
                run_id,
                defer_action.clone(),
            )?)
            .await?;
        ensure!(deferred.status() == StatusCode::OK);
    }

    let attachment_download = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!(
                    "/api/v1/computers/{}/agents/{}/runs/{run_id}/attachments/{}/download",
                    computer.id, agent.member_id, created_attachment.id
                ))
                .header(header::AUTHORIZATION, format!("Bearer {token}"))
                .body(Body::empty())?,
        )
        .await?;
    ensure!(attachment_download.status() == StatusCode::OK);
    ensure!(
        to_bytes(attachment_download.into_body(), 1024)
            .await?
            .as_ref()
            == attachment_bytes
    );
    let atomic_result: (String, Uuid, i64) = sqlx::query_as(
        "SELECT status, handled_by_run_id, \
         (SELECT count(*) FROM messages WHERE channel_id = $2 AND author_member_id = $3) \
         FROM inbox_items WHERE id = $1",
    )
    .bind(inbox_item_id)
    .bind(dm.channel_id)
    .bind(agent.member_id)
    .fetch_one(&pool)
    .await?;
    ensure!(atomic_result == ("handled".to_owned(), run_id, 1));

    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "command_result",
                "command_id": run_command["command_id"],
                "computer_seq": run_command["computer_seq"],
                "ok": true,
                "result": { "ok": true, "run_id": run_id, "status": "completed" }
            })
            .to_string()
            .into(),
        ))
        .await?;
    tokio::time::timeout(std::time::Duration::from_secs(2), async {
        loop {
            let status: String = sqlx::query_scalar("SELECT status FROM agent_runs WHERE id = $1")
                .bind(run_id)
                .fetch_one(&pool)
                .await
                .expect("Agent run query must succeed");
            if status == "completed" {
                break;
            }
            tokio::time::sleep(std::time::Duration::from_millis(10)).await;
        }
    })
    .await
    .context("Agent run result was not applied")?;
    let inactive_run_create = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "agent_create",
                "name": "Late Child",
                "role_text": "This request is outside an active run.",
                "computer_id": computer.id,
                "driver_kind": "codex",
                "idempotency_key": Uuid::now_v7()
            }),
        )?)
        .await?;
    ensure!(inactive_run_create.status() == StatusCode::FORBIDDEN);
    let pending_after_completed: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM inbox_items WHERE member_id = $1 AND status = 'pending'",
    )
    .bind(agent.member_id)
    .fetch_one(&pool)
    .await?;
    ensure!(pending_after_completed == 1);

    let retry_claim = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri(format!(
                    "/api/v1/computers/{}/agents/{}/inbox/claim",
                    computer.id, agent.member_id
                ))
                .header(header::AUTHORIZATION, format!("Bearer {token}"))
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from("{}"))?,
        )
        .await?;
    ensure!(retry_claim.status() == StatusCode::OK);
    let retry_claim: serde_json::Value = decode_json(retry_claim).await?;
    ensure!(retry_claim["claimed"] == true);
    let retry_inbox_item_ids = retry_claim["inbox_item_ids"]
        .as_array()
        .context("retry Inbox item ids missing")?;
    ensure!(retry_inbox_item_ids.len() == 1);
    let retry_inbox_item_id = Uuid::parse_str(
        retry_inbox_item_ids[0]
            .as_str()
            .context("retry Inbox item id missing")?,
    )?;
    let retry_run_id = Uuid::parse_str(
        retry_claim["run_id"]
            .as_str()
            .context("retry run id missing")?,
    )?;
    let retry_command = socket.next().await.context("Retry run command missing")??;
    let retry_command: serde_json::Value = serde_json::from_str(retry_command.to_text()?)?;
    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "command_ack",
                "command_id": retry_command["command_id"],
                "computer_seq": retry_command["computer_seq"]
            })
            .to_string()
            .into(),
        ))
        .await?;
    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "command_result",
                "command_id": retry_command["command_id"],
                "computer_seq": retry_command["computer_seq"],
                "ok": false,
                "result": {
                    "ok": false,
                    "run_id": retry_run_id,
                    "status": "failed",
                    "error_code": "driver_failed"
                }
            })
            .to_string()
            .into(),
        ))
        .await?;
    let failed_run_released = tokio::time::timeout(std::time::Duration::from_secs(2), async {
        loop {
            let state: (String, i32) =
                sqlx::query_as("SELECT status, retry_count FROM inbox_items WHERE id = $1")
                    .bind(retry_inbox_item_id)
                    .fetch_one(&pool)
                    .await
                    .expect("Retry Inbox query must succeed");
            if state == ("pending".to_owned(), 1) {
                break;
            }
            tokio::time::sleep(std::time::Duration::from_millis(10)).await;
        }
    })
    .await;
    if failed_run_released.is_err() {
        let inbox_state: (String, i32, Option<Uuid>, Option<String>) = sqlx::query_as(
            "SELECT status, retry_count, lease_id, last_error FROM inbox_items \
             WHERE id = $1",
        )
        .bind(retry_inbox_item_id)
        .fetch_one(&pool)
        .await?;
        let run_state: (String, Option<String>) =
            sqlx::query_as("SELECT status, error_code FROM agent_runs WHERE id = $1")
                .bind(retry_run_id)
                .fetch_one(&pool)
                .await?;
        anyhow::bail!(
            "Failed run did not release its Inbox lease: inbox={inbox_state:?}, run={run_state:?}"
        );
    }

    let ambient_configured = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/agents/{}", agent.member_id),
            Uuid::now_v7(),
            &serde_json::json!({
                "attention_config": {
                    "dm_immediate": true,
                    "mention_immediate": true,
                    "ambient_enabled": true,
                    "ambient_debounce_seconds": 5,
                    "ambient_max_wait_seconds": 30,
                    "max_retry_count": 4
                }
            }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(ambient_configured.status() == StatusCode::OK);
    let configure = socket
        .next()
        .await
        .context("Ambient configure command missing")??;
    let configure: serde_json::Value = serde_json::from_str(configure.to_text()?)?;
    ensure!(configure["kind"] == "agent.configure");
    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "command_result",
                "command_id": configure["command_id"],
                "computer_seq": configure["computer_seq"],
                "ok": true,
                "result": { "ok": true, "memory_files": [] }
            })
            .to_string()
            .into(),
        ))
        .await?;

    let mut ambient_messages = Vec::new();
    for index in 1..=5 {
        let response = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/messages", space.general_channel_id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "body_markdown": format!("Ambient update {index}."),
                    "mentions": []
                }),
                Some(&owner.cookie),
            )?)
            .await?;
        ensure!(response.status() == StatusCode::CREATED);
        ambient_messages.push(decode_json::<MessageResponse>(response).await?);
    }
    let mention_response = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/messages", space.general_channel_id),
            Uuid::now_v7(),
            &serde_json::json!({
                "body_markdown": "@lin please review now.",
                "mentions": [agent.member_id]
            }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(mention_response.status() == StatusCode::CREATED);
    let mention_message: MessageResponse = decode_json(mention_response).await?;
    let ambient_state: (
        Uuid,
        i64,
        i64,
        i32,
        String,
        Uuid,
        time::OffsetDateTime,
        time::OffsetDateTime,
    ) = sqlx::query_as(
        "SELECT id, first_seq, last_seq, message_count, status, message_id, available_at, \
             created_at FROM inbox_items WHERE member_id = $1 AND channel_id = $2 \
             AND kind = 'channel_activity' AND status = 'pending'",
    )
    .bind(agent.member_id)
    .bind(space.general_channel_id)
    .fetch_one(&pool)
    .await?;
    ensure!(ambient_state.1 == ambient_messages[0].seq);
    ensure!(ambient_state.2 == ambient_messages[4].seq && ambient_state.3 == 5);
    ensure!(ambient_state.4 == "pending" && ambient_state.5 == ambient_messages[4].id);
    ensure!(ambient_state.6 == ambient_state.7 + time::Duration::seconds(5));
    let mention_item_id: Uuid = sqlx::query_scalar(
        "SELECT id FROM inbox_items WHERE member_id = $1 AND message_id = $2 \
         AND kind = 'mention' AND priority = 'hard'",
    )
    .bind(agent.member_id)
    .bind(mention_message.id)
    .fetch_one(&pool)
    .await?;
    let ambient_claim = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri(format!(
                    "/api/v1/computers/{}/agents/{}/inbox/claim",
                    computer.id, agent.member_id
                ))
                .header(header::AUTHORIZATION, format!("Bearer {token}"))
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from("{}"))?,
        )
        .await?;
    ensure!(ambient_claim.status() == StatusCode::OK);
    let ambient_claim: serde_json::Value = decode_json(ambient_claim).await?;
    ensure!(ambient_claim["claimed"] == true);
    let ambient_run_id = Uuid::parse_str(
        ambient_claim["run_id"]
            .as_str()
            .context("ambient run id missing")?,
    )?;
    let claimed_ids = ambient_claim["inbox_item_ids"]
        .as_array()
        .context("ambient claim item ids missing")?
        .iter()
        .map(|value| {
            Uuid::parse_str(value.as_str().context("ambient claim item id missing")?)
                .map_err(Into::into)
        })
        .collect::<anyhow::Result<Vec<_>>>()?;
    ensure!(claimed_ids.contains(&ambient_state.0));
    ensure!(claimed_ids.contains(&mention_item_id));
    let ambient_command = socket
        .next()
        .await
        .context("Ambient run command missing")??;
    let ambient_command: serde_json::Value = serde_json::from_str(ambient_command.to_text()?)?;
    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "command_ack",
                "command_id": ambient_command["command_id"],
                "computer_seq": ambient_command["computer_seq"]
            })
            .to_string()
            .into(),
        ))
        .await?;
    tokio::time::timeout(std::time::Duration::from_secs(2), async {
        loop {
            let status: String = sqlx::query_scalar("SELECT status FROM agent_runs WHERE id = $1")
                .bind(ambient_run_id)
                .fetch_one(&pool)
                .await
                .expect("Ambient run query must succeed");
            if status == "running" {
                break;
            }
            tokio::time::sleep(std::time::Duration::from_millis(10)).await;
        }
    })
    .await
    .context("Ambient run did not enter running")?;
    let ambient_ack = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            ambient_run_id,
            serde_json::json!({
                "action": "inbox_ack",
                "inbox_item_ids": claimed_ids,
                "reason": "No response needed for this batch.",
                "idempotency_key": Uuid::now_v7()
            }),
        )?)
        .await?;
    ensure!(ambient_ack.status() == StatusCode::OK);
    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "command_result",
                "command_id": ambient_command["command_id"],
                "computer_seq": ambient_command["computer_seq"],
                "ok": true,
                "result": { "ok": true, "run_id": ambient_run_id, "status": "completed" }
            })
            .to_string()
            .into(),
        ))
        .await?;
    tokio::time::timeout(std::time::Duration::from_secs(2), async {
        loop {
            let status: String = sqlx::query_scalar("SELECT status FROM agent_runs WHERE id = $1")
                .bind(ambient_run_id)
                .fetch_one(&pool)
                .await
                .expect("Ambient run completion query must succeed");
            if status == "completed" {
                break;
            }
            tokio::time::sleep(std::time::Duration::from_millis(10)).await;
        }
    })
    .await
    .context("Ambient run result was not applied")?;
    let empty_claim = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri(format!(
                    "/api/v1/computers/{}/agents/{}/inbox/claim",
                    computer.id, agent.member_id
                ))
                .header(header::AUTHORIZATION, format!("Bearer {token}"))
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from("{}"))?,
        )
        .await?;
    ensure!(empty_claim.status() == StatusCode::OK);
    let empty_claim: serde_json::Value = decode_json(empty_claim).await?;
    ensure!(empty_claim["claimed"] == false);

    let thread_created = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/threads", space.general_channel_id),
            Uuid::now_v7(),
            &serde_json::json!({ "root_message_id": mention_message.id }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(thread_created.status() == StatusCode::CREATED);
    let thread: ThreadResponse = decode_json(thread_created).await?;
    let mentioned_reply = app
        .clone()
        .oneshot(json_request(
            &format!(
                "/api/v1/channels/{}/threads/{}/messages",
                space.general_channel_id, thread.thread_id
            ),
            Uuid::now_v7(),
            &serde_json::json!({
                "body_markdown": "@lin follow this Thread.",
                "mentions": [agent.member_id],
                "reply_to_message_id": mention_message.id
            }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(mentioned_reply.status() == StatusCode::CREATED);
    let mentioned_reply: MessageResponse = decode_json(mentioned_reply).await?;
    let agent_following: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM thread_subscriptions WHERE channel_id = $1 \
         AND thread_id = $2 AND member_id = $3 AND muted_at IS NULL)",
    )
    .bind(space.general_channel_id)
    .bind(thread.thread_id)
    .bind(agent.member_id)
    .fetch_one(&pool)
    .await?;
    ensure!(agent_following);

    let read_thread = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!(
                    "/api/v1/channels/{}/threads/{}",
                    space.general_channel_id, thread.thread_id
                ))
                .header(header::COOKIE, &owner.cookie)
                .body(Body::empty())?,
        )
        .await?;
    let read_thread: ThreadReadResponse = decode_json(read_thread).await?;
    ensure!(read_thread.is_following);
    for (method, expected) in [("DELETE", false), ("PUT", true)] {
        let subscription = app
            .clone()
            .oneshot(json_request_with_method(
                method,
                &format!(
                    "/api/v1/channels/{}/threads/{}/subscription",
                    space.general_channel_id, thread.thread_id
                ),
                Uuid::now_v7(),
                &serde_json::json!({}),
                Some(&owner.cookie),
            )?)
            .await?;
        ensure!(subscription.status() == StatusCode::OK);
        let subscription: serde_json::Value = decode_json(subscription).await?;
        ensure!(subscription["is_following"] == expected);
    }

    let agent_message_seq: i64 = sqlx::query_scalar(
        "UPDATE channels SET next_seq = next_seq + 1 WHERE id = $1 RETURNING next_seq - 1",
    )
    .bind(space.general_channel_id)
    .fetch_one(&pool)
    .await?;
    let agent_message_id = Uuid::now_v7();
    sqlx::query(
        "INSERT INTO messages (id, channel_id, space_id, channel_seq, thread_id, \
         author_member_id, body_markdown, idempotency_key, created_at) \
         VALUES ($1, $2, $3, $4, $5, $6, 'Agent Thread note.', $7, now())",
    )
    .bind(agent_message_id)
    .bind(space.general_channel_id)
    .bind(space.id)
    .bind(agent_message_seq)
    .bind(thread.thread_id)
    .bind(agent.member_id)
    .bind(Uuid::now_v7())
    .execute(&pool)
    .await?;
    let deduplicated_reply = app
        .clone()
        .oneshot(json_request(
            &format!(
                "/api/v1/channels/{}/threads/{}/messages",
                space.general_channel_id, thread.thread_id
            ),
            Uuid::now_v7(),
            &serde_json::json!({
                "body_markdown": "@lin direct reply.",
                "mentions": [agent.member_id],
                "reply_to_message_id": agent_message_id
            }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(deduplicated_reply.status() == StatusCode::CREATED);
    let deduplicated_reply: MessageResponse = decode_json(deduplicated_reply).await?;
    let hard_kinds: Vec<String> = sqlx::query_scalar(
        "SELECT kind FROM inbox_items WHERE member_id = $1 AND message_id = $2 \
         AND priority = 'hard' ORDER BY kind",
    )
    .bind(agent.member_id)
    .bind(deduplicated_reply.id)
    .fetch_all(&pool)
    .await?;
    ensure!(hard_kinds == vec!["mention"]);

    let ambient_reply = app
        .clone()
        .oneshot(json_request(
            &format!(
                "/api/v1/channels/{}/threads/{}/messages",
                space.general_channel_id, thread.thread_id
            ),
            Uuid::now_v7(),
            &serde_json::json!({
                "body_markdown": "A subscribed Thread update.",
                "mentions": [],
                "reply_to_message_id": mentioned_reply.id
            }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(ambient_reply.status() == StatusCode::CREATED);
    let ambient_reply: MessageResponse = decode_json(ambient_reply).await?;
    let thread_ambient: (Uuid, i64, i32) = sqlx::query_as(
        "SELECT id, last_seq, message_count FROM inbox_items WHERE member_id = $1 \
         AND channel_id = $2 AND thread_id = $3 AND kind = 'thread_activity' \
         AND status = 'pending'",
    )
    .bind(agent.member_id)
    .bind(space.general_channel_id)
    .bind(thread.thread_id)
    .fetch_one(&pool)
    .await?;
    ensure!(thread_ambient.1 == ambient_reply.seq && thread_ambient.2 == 1);

    sqlx::query(
        "UPDATE agents SET attention_config_json = \
         jsonb_set(attention_config_json, '{max_retry_count}', '1') WHERE member_id = $1",
    )
    .bind(agent.member_id)
    .execute(&pool)
    .await?;
    let thread_claim = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri(format!(
                    "/api/v1/computers/{}/agents/{}/inbox/claim",
                    computer.id, agent.member_id
                ))
                .header(header::AUTHORIZATION, format!("Bearer {token}"))
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from("{}"))?,
        )
        .await?;
    let thread_claim: serde_json::Value = decode_json(thread_claim).await?;
    ensure!(thread_claim["claimed"] == true);
    let thread_run_id = Uuid::parse_str(
        thread_claim["run_id"]
            .as_str()
            .context("Thread run id missing")?,
    )?;
    let thread_run_command = socket
        .next()
        .await
        .context("Thread run command missing")??;
    let thread_run_command: serde_json::Value =
        serde_json::from_str(thread_run_command.to_text()?)?;
    ensure!(thread_run_command["kind"] == "agent.run");
    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "command_ack",
                "command_id": thread_run_command["command_id"],
                "computer_seq": thread_run_command["computer_seq"]
            })
            .to_string()
            .into(),
        ))
        .await?;
    tokio::time::timeout(std::time::Duration::from_secs(2), async {
        loop {
            let status: String = sqlx::query_scalar("SELECT status FROM agent_runs WHERE id = $1")
                .bind(thread_run_id)
                .fetch_one(&pool)
                .await
                .expect("Thread run status query must succeed");
            if status == "running" {
                break;
            }
            tokio::time::sleep(std::time::Duration::from_millis(10)).await;
        }
    })
    .await
    .context("Thread run did not enter running")?;
    let hard_thread_item_id: Uuid = sqlx::query_scalar(
        "SELECT inbox_items.id FROM agent_run_inbox_items \
         JOIN inbox_items ON inbox_items.id = agent_run_inbox_items.inbox_item_id \
         WHERE agent_run_inbox_items.run_id = $1 AND inbox_items.priority = 'hard' \
         ORDER BY inbox_items.created_at LIMIT 1",
    )
    .bind(thread_run_id)
    .fetch_one(&pool)
    .await?;
    let thread_address = format!("#general:{}", thread.thread_id);
    let initial_thread_read = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            thread_run_id,
            serde_json::json!({
                "action": "thread_read",
                "address": thread_address,
                "after": null,
                "limit": 50,
                "include_channel": 20
            }),
        )?)
        .await?;
    ensure!(initial_thread_read.status() == StatusCode::OK);
    let initial_thread_read: serde_json::Value = decode_json(initial_thread_read).await?;
    let thread_snapshot = initial_thread_read["snapshot_channel_seq"]
        .as_i64()
        .context("Thread snapshot sequence missing")?;
    let later_thread_reply = app
        .clone()
        .oneshot(json_request(
            &format!(
                "/api/v1/channels/{}/threads/{}/messages",
                space.general_channel_id, thread.thread_id
            ),
            Uuid::now_v7(),
            &serde_json::json!({
                "body_markdown": "This arrived after the Agent read.",
                "mentions": [],
                "reply_to_message_id": ambient_reply.id
            }),
            Some(&owner.cookie),
        )?)
        .await?;
    let later_thread_reply: MessageResponse = decode_json(later_thread_reply).await?;
    let stale_thread_send = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            thread_run_id,
            serde_json::json!({
                "action": "message_send",
                "address": thread_address,
                "body_markdown": "This stale Thread reply must not be stored.",
                "based_on": thread_snapshot,
                "handle_inbox_item_id": hard_thread_item_id,
                "idempotency_key": Uuid::now_v7()
            }),
        )?)
        .await?;
    ensure!(stale_thread_send.status() == StatusCode::CONFLICT);
    let stale_thread_error: serde_json::Value = decode_json(stale_thread_send).await?;
    ensure!(stale_thread_error["error"]["code"] == "context_changed");
    ensure!(stale_thread_error["error"]["details"]["latest_channel_seq"] == later_thread_reply.seq);
    ensure!(stale_thread_error["error"]["details"]["changes"][0]["address"] == thread_address);
    let stale_message_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM messages WHERE author_member_id = $1 \
         AND body_markdown = 'This stale Thread reply must not be stored.'",
    )
    .bind(agent.member_id)
    .fetch_one(&pool)
    .await?;
    ensure!(stale_message_count == 0);
    let refreshed_thread_read = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            thread_run_id,
            serde_json::json!({
                "action": "thread_read",
                "address": thread_address,
                "after": thread_snapshot,
                "limit": 50,
                "include_channel": 20
            }),
        )?)
        .await?;
    let refreshed_thread_read: serde_json::Value = decode_json(refreshed_thread_read).await?;
    let refreshed_thread_snapshot = refreshed_thread_read["snapshot_channel_seq"]
        .as_i64()
        .context("Refreshed Thread snapshot sequence missing")?;
    let fresh_thread_send = app
        .clone()
        .oneshot(computer_agent_action_request(
            computer.id,
            &token,
            agent.member_id,
            thread_run_id,
            serde_json::json!({
                "action": "message_send",
                "address": thread_address,
                "body_markdown": "This reply uses the refreshed Thread context.",
                "based_on": refreshed_thread_snapshot,
                "handle_inbox_item_id": hard_thread_item_id,
                "idempotency_key": Uuid::now_v7()
            }),
        )?)
        .await?;
    ensure!(fresh_thread_send.status() == StatusCode::OK);
    let lease_before: time::OffsetDateTime =
        sqlx::query_scalar("SELECT lease_expires_at FROM inbox_items WHERE id = $1")
            .bind(thread_ambient.0)
            .fetch_one(&pool)
            .await?;
    let renewed = app
        .clone()
        .oneshot(computer_json_request(
            "POST",
            &format!(
                "/api/v1/computers/{}/agents/{}/inbox/renew",
                computer.id, agent.member_id
            ),
            &token,
            &serde_json::json!({ "run_id": thread_run_id }),
        )?)
        .await?;
    ensure!(renewed.status() == StatusCode::OK);
    let renewed: serde_json::Value = decode_json(renewed).await?;
    ensure!(renewed["renewed_items"].as_u64().unwrap_or_default() >= 1);
    let lease_after: time::OffsetDateTime =
        sqlx::query_scalar("SELECT lease_expires_at FROM inbox_items WHERE id = $1")
            .bind(thread_ambient.0)
            .fetch_one(&pool)
            .await?;
    ensure!(lease_after >= lease_before);

    let released = app
        .clone()
        .oneshot(computer_json_request(
            "POST",
            &format!(
                "/api/v1/computers/{}/agents/{}/inbox/release",
                computer.id, agent.member_id
            ),
            &token,
            &serde_json::json!({
                "run_id": thread_run_id,
                "error_code": "process_lost"
            }),
        )?)
        .await?;
    ensure!(released.status() == StatusCode::OK);
    let released: serde_json::Value = decode_json(released).await?;
    ensure!(released["released"] == true);
    ensure!(released["dead_items"].as_u64().unwrap_or_default() >= 1);
    let duplicate_release = app
        .clone()
        .oneshot(computer_json_request(
            "POST",
            &format!(
                "/api/v1/computers/{}/agents/{}/inbox/release",
                computer.id, agent.member_id
            ),
            &token,
            &serde_json::json!({
                "run_id": thread_run_id,
                "error_code": "process_lost"
            }),
        )?)
        .await?;
    let duplicate_release: serde_json::Value = decode_json(duplicate_release).await?;
    ensure!(duplicate_release["released"] == false);
    let system_items: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM inbox_items WHERE member_id = $1 AND kind = 'system' \
         AND priority = 'hard' AND status = 'pending' AND last_error = 'process_lost'",
    )
    .bind(space.owner_member_id)
    .fetch_one(&pool)
    .await?;
    ensure!(system_items == 1);

    let retired = app
        .clone()
        .oneshot(json_request_with_method(
            "PATCH",
            &format!("/api/v1/agents/{}", agent.member_id),
            Uuid::now_v7(),
            &serde_json::json!({ "lifecycle": { "action": "retire" } }),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(retired.status() == StatusCode::OK);
    let retired: AgentResponse = decode_json(retired).await?;
    ensure!(retired.status == "retired" && retired.retired_at.is_some());
    let retire_command = socket
        .next()
        .await
        .context("Agent retire command missing")??;
    let retire_command: serde_json::Value = serde_json::from_str(retire_command.to_text()?)?;
    ensure!(retire_command["kind"] == "agent.retire");
    socket
        .send(tokio_tungstenite::tungstenite::Message::Text(
            serde_json::json!({
                "type": "command_result",
                "command_id": retire_command["command_id"],
                "computer_seq": retire_command["computer_seq"],
                "ok": true,
                "result": { "ok": true, "memory_files": [] }
            })
            .to_string()
            .into(),
        ))
        .await?;
    tokio::time::sleep(std::time::Duration::from_millis(50)).await;
    let retired_member_at: Option<time::OffsetDateTime> =
        sqlx::query_scalar("SELECT retired_at FROM members WHERE id = $1")
            .bind(agent.member_id)
            .fetch_one(&pool)
            .await?;
    ensure!(retired_member_at.is_some());

    let revoked = app
        .clone()
        .oneshot(json_request_with_method(
            "DELETE",
            &format!("/api/v1/computers/{}", computer.id),
            Uuid::now_v7(),
            &serde_json::json!({}),
            Some(&owner.cookie),
        )?)
        .await?;
    ensure!(revoked.status() == StatusCode::OK);
    let listed_after_delete = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/api/v1/spaces/{}/computers", space.id))
                .header(header::COOKIE, &owner.cookie)
                .body(Body::empty())?,
        )
        .await?;
    ensure!(listed_after_delete.status() == StatusCode::OK);
    let listed_after_delete: Vec<ComputerResponse> = decode_json(listed_after_delete).await?;
    ensure!(listed_after_delete.is_empty());
    let shutdown = tokio::time::timeout(std::time::Duration::from_secs(2), socket.next())
        .await
        .context("Computer shutdown frame timed out")?
        .context("Computer socket closed before shutdown frame")??;
    let shutdown: serde_json::Value = serde_json::from_str(shutdown.to_text()?)?;
    ensure!(shutdown["type"] == "shutdown" && shutdown["reason"] == "computer_deleted");
    let mut revoked_request =
        format!("ws://{address}/api/v1/computers/{}/connect", computer.id).into_client_request()?;
    revoked_request
        .headers_mut()
        .insert(header::AUTHORIZATION, format!("Bearer {token}").parse()?);
    let reconnect = tokio_tungstenite::connect_async(revoked_request).await;
    ensure!(matches!(
        reconnect,
        Err(tokio_tungstenite::tungstenite::Error::Http(response))
            if response.status() == StatusCode::UNAUTHORIZED
    ));
    let status_event_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM outbox_events WHERE topic = 'computer.status_changed' \
         AND aggregate_id = $1",
    )
    .bind(computer.id)
    .fetch_one(&pool)
    .await?;
    ensure!(status_event_count == 2);
    server_task.abort();
    let _ = server_task.await;
    let stored_hash: Vec<u8> = sqlx::query_scalar("SELECT token_hash FROM computers WHERE id = $1")
        .bind(computer.id)
        .fetch_one(&pool)
        .await?;
    ensure!(stored_hash == sha2::Sha256::digest(token.as_bytes()).as_slice());
    ensure!(stored_hash.as_slice() != token.as_bytes());

    let expired_token = URL_SAFE_NO_PAD.encode([31_u8; 32]);
    let expired_start = app
        .clone()
        .oneshot(json_request(
            "/api/v1/computer-pairings/start",
            Uuid::now_v7(),
            &serde_json::json!({
                "token_hash": URL_SAFE_NO_PAD.encode(sha2::Sha256::digest(expired_token.as_bytes())),
                "hostname": "expired.local", "os": "linux", "daemon_version": "0.1.0"
            }),
            None,
        )?)
        .await?;
    let expired: PairingStartResponse = decode_json(expired_start).await?;
    sqlx::query(
        "UPDATE computer_pairings SET expires_at = now() - interval '1 second' WHERE id = $1",
    )
    .bind(expired.pairing_id)
    .execute(&pool)
    .await?;
    let expired_result = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!(
                    "/api/v1/computer-pairings/{}/result",
                    expired.pairing_id
                ))
                .header(header::AUTHORIZATION, format!("Bearer {expired_token}"))
                .body(Body::empty())?,
        )
        .await?;
    ensure!(expired_result.status() == StatusCode::GONE);
    let expired_status: String =
        sqlx::query_scalar("SELECT status FROM computer_pairings WHERE id = $1")
            .bind(expired.pairing_id)
            .fetch_one(&pool)
            .await?;
    ensure!(expired_status == "expired");
    pool.close().await;
    Ok(())
}
