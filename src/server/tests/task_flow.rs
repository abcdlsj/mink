use super::*;

pub(super) async fn run(database_url: &str) -> Result<()> {
    let pool = database::connect_postgres(database_url).await?;
    let now = time::OffsetDateTime::now_utc();
    let space_id = Uuid::now_v7();
    let owner_id = Uuid::now_v7();
    let agent_a = Uuid::now_v7();
    let agent_b = Uuid::now_v7();
    let hidden_agent = Uuid::now_v7();
    let computer_id = Uuid::now_v7();
    let channel_id = Uuid::now_v7();
    let source_message_id = Uuid::now_v7();
    let mut tx = pool.begin().await?;
    sqlx::query("SET CONSTRAINTS ALL DEFERRED")
        .execute(&mut *tx)
        .await?;
    sqlx::query(
        "INSERT INTO spaces (id, slug, name, accent, owner_member_id, created_at, updated_at) \
         VALUES ($1, 'task-lab', 'Task Lab', '#FE7DA8', $2, $3, $3)",
    )
    .bind(space_id)
    .bind(owner_id)
    .bind(now)
    .execute(&mut *tx)
    .await
    .map_err(|error| anyhow::anyhow!("{error:?}"))?;
    for (id, kind, name, handle, access) in [
        (owner_id, "human", "Owner", "owner", "owner"),
        (agent_a, "agent", "Agent A", "agent-a", "member"),
        (agent_b, "agent", "Agent B", "agent-b", "member"),
        (hidden_agent, "agent", "Hidden", "hidden", "member"),
    ] {
        sqlx::query(
            "INSERT INTO members (id, space_id, kind, display_name, handle, avatar_seed, access_level, created_at) \
             VALUES ($1, $2, $3, $4, $5, $1::text, $6, $7)",
        ).bind(id).bind(space_id).bind(kind).bind(name).bind(handle).bind(access).bind(now)
          .execute(&mut *tx).await?;
    }
    sqlx::query(
        "INSERT INTO computers (id, space_id, name, hostname, os, token_hash, status, daemon_version, created_at) \
         VALUES ($1, $2, 'Task Computer', 'localhost', 'macos', $3, 'online', 'test', $4)",
    ).bind(computer_id).bind(space_id).bind(vec![7_u8; 32]).bind(now).execute(&mut *tx).await?;
    for id in [agent_a, agent_b, hidden_agent] {
        sqlx::query(
            "INSERT INTO agents (member_id, space_id, computer_id, role_text, status, driver_kind, \
             driver_config_json, attention_config_json, created_by_member_id, created_at, updated_at) \
             VALUES ($1, $2, $3, 'Handle Tasks', 'active', 'builtin', '{}', \
             '{\"ambient_enabled\":false}', $4, $5, $5)",
        ).bind(id).bind(space_id).bind(computer_id).bind(owner_id).bind(now).execute(&mut *tx).await?;
    }
    sqlx::query(
        "INSERT INTO channels (id, space_id, kind, name, slug, created_by_member_id, next_seq, created_at) \
         VALUES ($1, $2, 'private', 'Build', 'build', $3, 2, $4)",
    ).bind(channel_id).bind(space_id).bind(owner_id).bind(now).execute(&mut *tx).await?;
    for id in [owner_id, agent_a, agent_b] {
        sqlx::query(
            "INSERT INTO channel_members (channel_id, member_id, space_id, joined_at) VALUES ($1, $2, $3, $4)",
        ).bind(channel_id).bind(id).bind(space_id).bind(now).execute(&mut *tx).await?;
    }
    sqlx::query(
        "INSERT INTO messages (id, channel_id, space_id, channel_seq, author_member_id, body_markdown, idempotency_key, created_at) \
         VALUES ($1, $2, $3, 1, $4, 'Implement the Task prototype', $5, $6)",
    ).bind(source_message_id).bind(channel_id).bind(space_id).bind(owner_id).bind(Uuid::now_v7()).bind(now)
      .execute(&mut *tx).await?;
    tx.commit().await?;

    let converted = super::super::task::convert_for_agent(
        &pool,
        agent_a,
        source_message_id,
        None,
        Some(agent_b),
        Uuid::now_v7(),
    )
    .await
    .map_err(|error| anyhow::anyhow!("{error:?}"))?;
    ensure!(converted.status == "open");
    ensure!(converted.assigned_agent_member_id == Some(agent_b));

    let claimed = super::super::task::claim_for_agent(&pool, agent_a, converted.id, Uuid::now_v7())
        .await
        .map_err(|error| anyhow::anyhow!("{error:?}"))?;
    ensure!(claimed.status == "in_progress");
    ensure!(claimed.assigned_agent_member_id == Some(agent_a));

    let assigned =
        super::super::task::assign_for_agent(&pool, agent_a, converted.id, agent_b, Uuid::now_v7())
            .await
            .map_err(|error| anyhow::anyhow!("{error:?}"))?;
    ensure!(assigned.assigned_agent_member_id == Some(agent_b));
    let done = super::super::task::status_for_agent(
        &pool,
        agent_b,
        converted.id,
        "done".to_owned(),
        Uuid::now_v7(),
    )
    .await
    .map_err(|error| anyhow::anyhow!("{error:?}"))?;
    ensure!(done.status == "done");
    let mut message_tx = pool.begin().await?;
    let projected_message =
        super::super::message::message_by_id(&mut message_tx, source_message_id)
            .await
            .map_err(|error| anyhow::anyhow!("{error:?}"))?;
    message_tx.rollback().await?;
    let task_summary = projected_message
        .task
        .context("Task summary is projected onto its root Message")?;
    ensure!(task_summary.id == converted.id);
    ensure!(task_summary.status == "done");
    ensure!(task_summary.assigned_agent_member_id == Some(agent_b));
    ensure!(task_summary.assignee_name.as_deref() == Some("Agent B"));

    let created = super::super::task::create_for_agent(
        &pool,
        agent_a,
        "#build",
        "Verify Task flow".to_owned(),
        "Run the focused checks.".to_owned(),
        None,
        Uuid::now_v7(),
    )
    .await
    .map_err(|error| anyhow::anyhow!("{error:?}"))?;
    ensure!(created.source_seq == 2);
    let message_count: i64 =
        sqlx::query_scalar("SELECT count(*) FROM messages WHERE channel_id = $1")
            .bind(channel_id)
            .fetch_one(&pool)
            .await?;
    ensure!(message_count == 2);

    let hidden = super::super::task::status_for_agent(
        &pool,
        hidden_agent,
        converted.id,
        "canceled".to_owned(),
        Uuid::now_v7(),
    )
    .await;
    ensure!(hidden.is_err());
    let visible = super::super::task::list_for_agent(&pool, agent_a, None)
        .await
        .map_err(|error| anyhow::anyhow!("{error:?}"))?;
    ensure!(
        visible["tasks"]
            .as_array()
            .is_some_and(|tasks| tasks.len() == 2)
    );
    Ok(())
}
