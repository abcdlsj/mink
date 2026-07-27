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

    let owner = register_human(
        &app,
        "Invariant Owner",
        "invariants@example.test",
        "correct-horse-invariants",
    )
    .await?;
    let space = create_space(&app, &owner.cookie, "Invariant Lab", "invariant-lab").await?;

    verify_message_thread_and_idempotency_concurrency(&app, &pool, &owner.cookie, &space).await?;
    let uploading_attachment =
        verify_attachment_deduplication(&app, &pool, &owner.cookie, &space).await?;
    verify_transactional_rollback(&app, &pool, &owner.cookie, &space, uploading_attachment).await?;
    verify_claim_competition_and_active_run_constraint(&app, &pool, &space).await?;
    verify_composite_foreign_keys(&app, &pool, &owner.cookie, &space).await?;

    pool.close().await;
    Ok(())
}

async fn create_space(app: &Router, cookie: &str, name: &str, slug: &str) -> Result<SpaceResponse> {
    let response = app
        .clone()
        .oneshot(json_request(
            "/api/v1/spaces",
            Uuid::now_v7(),
            &serde_json::json!({
                "name": name,
                "slug": slug,
                "accent": "#FE7DA8"
            }),
            Some(cookie),
        )?)
        .await?;
    ensure!(response.status() == StatusCode::CREATED);
    decode_json(response).await
}

async fn verify_message_thread_and_idempotency_concurrency(
    app: &Router,
    pool: &sqlx::PgPool,
    cookie: &str,
    space: &SpaceResponse,
) -> Result<()> {
    let message_futures = (0..8).map(|index| {
        app.clone().oneshot(
            json_request(
                &format!("/api/v1/channels/{}/messages", space.general_channel_id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "body_markdown": format!("concurrent root {index}"),
                    "mentions": []
                }),
                Some(cookie),
            )
            .expect("concurrent Message request must be valid"),
        )
    });
    let mut messages = Vec::new();
    for response in futures_util::future::join_all(message_futures).await {
        let response = response?;
        ensure!(response.status() == StatusCode::CREATED);
        messages.push(decode_json::<MessageResponse>(response).await?);
    }
    messages.sort_by_key(|message| message.seq);
    ensure!(
        messages.iter().map(|message| message.seq).eq(1_i64..=8_i64),
        "concurrent Message sequence was not gap-free and unique"
    );

    let duplicate_key = Uuid::now_v7();
    let duplicate_body = serde_json::json!({
        "body_markdown": "same concurrent retry",
        "mentions": []
    });
    let duplicate_futures = (0..2).map(|_| {
        app.clone().oneshot(
            json_request(
                &format!("/api/v1/channels/{}/messages", space.general_channel_id),
                duplicate_key,
                &duplicate_body,
                Some(cookie),
            )
            .expect("duplicate Message request must be valid"),
        )
    });
    let mut duplicate_ids = Vec::new();
    for response in futures_util::future::join_all(duplicate_futures).await {
        let response = response?;
        ensure!(response.status() == StatusCode::CREATED);
        duplicate_ids.push(decode_json::<MessageResponse>(response).await?.id);
    }
    ensure!(duplicate_ids[0] == duplicate_ids[1]);

    let conflict = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/messages", space.general_channel_id),
            duplicate_key,
            &serde_json::json!({
                "body_markdown": "different payload",
                "mentions": []
            }),
            Some(cookie),
        )?)
        .await?;
    ensure!(conflict.status() == StatusCode::CONFLICT);

    let thread_futures = messages.iter().map(|message| {
        app.clone().oneshot(
            json_request(
                &format!("/api/v1/channels/{}/threads", space.general_channel_id),
                Uuid::now_v7(),
                &serde_json::json!({ "root_message_id": message.id }),
                Some(cookie),
            )
            .expect("concurrent Thread request must be valid"),
        )
    });
    let mut thread_ids = Vec::new();
    for response in futures_util::future::join_all(thread_futures).await {
        let response = response?;
        ensure!(response.status() == StatusCode::CREATED);
        thread_ids.push(decode_json::<ThreadResponse>(response).await?.thread_id);
    }
    thread_ids.sort_unstable();
    ensure!(thread_ids.into_iter().eq(1_i64..=8_i64));

    let invariants: (i64, i64, i64, i64, i64) = sqlx::query_as(
        "SELECT channels.next_seq, channels.next_thread_id, \
           (SELECT count(*) FROM messages WHERE channel_id = channels.id), \
           (SELECT count(*) FROM outbox_events WHERE topic = 'message.created' \
              AND payload_json->>'channel_id' = channels.id::text), \
           (SELECT count(*) FROM idempotency_records WHERE scope LIKE \
              'channel:' || channels.id::text || ':%:message:create') \
         FROM channels WHERE id = $1",
    )
    .bind(space.general_channel_id)
    .fetch_one(pool)
    .await?;
    ensure!(invariants == (10, 9, 9, 9, 9));
    Ok(())
}

async fn verify_attachment_deduplication(
    app: &Router,
    pool: &sqlx::PgPool,
    cookie: &str,
    space: &SpaceResponse,
) -> Result<Uuid> {
    let key = Uuid::now_v7();
    let body = serde_json::json!({
        "space_id": space.id,
        "original_name": "evidence.txt",
        "media_type": "text/plain"
    });
    let futures = (0..2).map(|_| {
        app.clone().oneshot(
            json_request("/api/v1/attachments/uploads", key, &body, Some(cookie))
                .expect("duplicate Attachment request must be valid"),
        )
    });
    let mut ids = Vec::new();
    for response in futures_util::future::join_all(futures).await {
        let response = response?;
        ensure!(response.status() == StatusCode::CREATED);
        ids.push(decode_json::<AttachmentResponse>(response).await?.id);
    }
    ensure!(ids[0] == ids[1]);

    let conflict = app
        .clone()
        .oneshot(json_request(
            "/api/v1/attachments/uploads",
            key,
            &serde_json::json!({
                "space_id": space.id,
                "original_name": "different.txt",
                "media_type": "text/plain"
            }),
            Some(cookie),
        )?)
        .await?;
    ensure!(conflict.status() == StatusCode::CONFLICT);
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM attachments WHERE id = $1")
        .bind(ids[0])
        .fetch_one(pool)
        .await?;
    ensure!(count == 1);
    Ok(ids[0])
}

async fn verify_transactional_rollback(
    app: &Router,
    pool: &sqlx::PgPool,
    cookie: &str,
    space: &SpaceResponse,
    uploading_attachment: Uuid,
) -> Result<()> {
    let failed_key = Uuid::now_v7();
    let before: (i64, i64, i64) = sqlx::query_as(
        "SELECT next_seq, \
           (SELECT count(*) FROM messages WHERE channel_id = channels.id), \
           (SELECT count(*) FROM outbox_events WHERE topic = 'message.created' \
              AND payload_json->>'channel_id' = channels.id::text) \
         FROM channels WHERE id = $1",
    )
    .bind(space.general_channel_id)
    .fetch_one(pool)
    .await?;
    let failed = app
        .clone()
        .oneshot(json_request(
            &format!("/api/v1/channels/{}/messages", space.general_channel_id),
            failed_key,
            &serde_json::json!({
                "body_markdown": "must roll back",
                "mentions": [],
                "attachment_ids": [uploading_attachment]
            }),
            Some(cookie),
        )?)
        .await?;
    ensure!(failed.status() == StatusCode::BAD_REQUEST);
    let after: (i64, i64, i64, i64) = sqlx::query_as(
        "SELECT next_seq, \
           (SELECT count(*) FROM messages WHERE channel_id = channels.id), \
           (SELECT count(*) FROM outbox_events WHERE topic = 'message.created' \
              AND payload_json->>'channel_id' = channels.id::text), \
           (SELECT count(*) FROM idempotency_records WHERE idempotency_key = $2) \
         FROM channels WHERE id = $1",
    )
    .bind(space.general_channel_id)
    .bind(failed_key)
    .fetch_one(pool)
    .await?;
    ensure!((after.0, after.1, after.2) == before && after.3 == 0);
    Ok(())
}

async fn verify_claim_competition_and_active_run_constraint(
    app: &Router,
    pool: &sqlx::PgPool,
    space: &SpaceResponse,
) -> Result<()> {
    let now = time::OffsetDateTime::now_utc();
    let token = "postgres-invariant-computer-token";
    let computer_id = Uuid::now_v7();
    sqlx::query(
        "INSERT INTO computers (id, space_id, name, hostname, os, token_hash, status, \
         daemon_version, last_seen_at, created_at) \
         VALUES ($1, $2, 'Invariant Computer', 'invariant.local', 'macos', $3, 'online', \
         'test', $4, $4)",
    )
    .bind(computer_id)
    .bind(space.id)
    .bind(Sha256::digest(token.as_bytes()).to_vec())
    .bind(now)
    .execute(pool)
    .await?;
    let agent_id = seed_agent(pool, space, computer_id, "claim-agent", now).await?;
    let inbox_id = Uuid::now_v7();
    sqlx::query(
        "INSERT INTO inbox_items (id, member_id, space_id, kind, priority, status, \
         available_at, created_at) VALUES ($1, $2, $3, 'system', 'hard', 'pending', $4, $4)",
    )
    .bind(inbox_id)
    .bind(agent_id)
    .bind(space.id)
    .bind(now)
    .execute(pool)
    .await?;

    let uri = format!("/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/claim");
    let claims = (0..2).map(|_| {
        app.clone().oneshot(
            computer_json_request("POST", &uri, token, &serde_json::json!({}))
                .expect("claim request must be valid"),
        )
    });
    let mut claimed = Vec::new();
    for response in futures_util::future::join_all(claims).await {
        let response = response?;
        ensure!(response.status() == StatusCode::OK);
        claimed.push(decode_json::<serde_json::Value>(response).await?["claimed"] == true);
    }
    claimed.sort_unstable();
    ensure!(claimed == [false, true]);
    let claim_state: (i64, String, i64, i64) = sqlx::query_as(
        "SELECT \
           (SELECT count(*) FROM agent_runs WHERE agent_member_id = $1 \
              AND status IN ('queued', 'running')), \
           (SELECT status FROM inbox_items WHERE id = $2), \
           (SELECT count(*) FROM computer_commands WHERE computer_id = $3 AND kind = 'agent.run'), \
           (SELECT count(*) FROM outbox_events WHERE topic = 'agent.run_changed' \
              AND payload_json->>'agent_member_id' = $1::text)",
    )
    .bind(agent_id)
    .bind(inbox_id)
    .bind(computer_id)
    .fetch_one(pool)
    .await?;
    ensure!(claim_state == (1, "leased".to_owned(), 1, 1));

    sqlx::query(
        "UPDATE agent_runs SET status = 'completed', started_at = $2, finished_at = $2 \
         WHERE agent_member_id = $1",
    )
    .bind(agent_id)
    .bind(now)
    .execute(pool)
    .await?;
    let constrained_agent = seed_agent(pool, space, computer_id, "constraint-agent", now).await?;
    let insert_run = |run_id| {
        let pool = pool.clone();
        async move {
            sqlx::query(
                "INSERT INTO agent_runs (id, agent_member_id, computer_id, driver_kind, \
                 role_revision, status, created_at) VALUES ($1, $2, $3, 'builtin', 1, 'queued', $4)",
            )
            .bind(run_id)
            .bind(constrained_agent)
            .bind(computer_id)
            .bind(now)
            .execute(&pool)
            .await
        }
    };
    let (first, second) = tokio::join!(insert_run(Uuid::now_v7()), insert_run(Uuid::now_v7()));
    ensure!(first.is_ok() ^ second.is_ok());
    let constraint_error = first
        .err()
        .or_else(|| second.err())
        .context("one concurrent active run insert must fail")?;
    ensure!(
        constraint_error
            .as_database_error()
            .and_then(|error| error.code())
            .as_deref()
            == Some("23505")
    );
    Ok(())
}

async fn seed_agent(
    pool: &sqlx::PgPool,
    space: &SpaceResponse,
    computer_id: Uuid,
    handle: &str,
    now: time::OffsetDateTime,
) -> Result<Uuid> {
    let agent_id = Uuid::now_v7();
    sqlx::query(
        "INSERT INTO members (id, space_id, kind, display_name, handle, avatar_seed, \
         access_level, created_at) VALUES ($1, $2, 'agent', $3, $3, $4, 'member', $5)",
    )
    .bind(agent_id)
    .bind(space.id)
    .bind(handle)
    .bind(agent_id.to_string())
    .bind(now)
    .execute(pool)
    .await?;
    sqlx::query(
        "INSERT INTO agents (member_id, space_id, computer_id, role_text, status, driver_kind, \
         driver_config_json, attention_config_json, created_by_member_id, created_at, updated_at) \
         VALUES ($1, $2, $3, 'Verify invariants.', 'active', 'builtin', '{}', \
         '{\"max_retry_count\":3}', $4, $5, $5)",
    )
    .bind(agent_id)
    .bind(space.id)
    .bind(computer_id)
    .bind(space.owner_member_id)
    .bind(now)
    .execute(pool)
    .await?;
    Ok(agent_id)
}

async fn verify_composite_foreign_keys(
    app: &Router,
    pool: &sqlx::PgPool,
    cookie: &str,
    first_space: &SpaceResponse,
) -> Result<()> {
    let second_space = create_space(app, cookie, "Other Lab", "other-lab").await?;
    let result = sqlx::query(
        "INSERT INTO messages (id, channel_id, space_id, channel_seq, author_member_id, \
         body_markdown, idempotency_key, created_at) VALUES ($1, $2, $3, 999, $4, \
         'cross-space', $5, $6)",
    )
    .bind(Uuid::now_v7())
    .bind(first_space.general_channel_id)
    .bind(first_space.id)
    .bind(second_space.owner_member_id)
    .bind(Uuid::now_v7())
    .bind(time::OffsetDateTime::now_utc())
    .execute(pool)
    .await;
    let error = result.expect_err("cross-Space Message author must violate composite FK");
    ensure!(
        error
            .as_database_error()
            .and_then(|error| error.code())
            .as_deref()
            == Some("23503")
    );
    Ok(())
}
