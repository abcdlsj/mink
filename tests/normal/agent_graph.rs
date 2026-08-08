#[path = "../support/mod.rs"]
mod support;

use std::net::SocketAddr;

use anyhow::{Context, Result, ensure};
use reqwest::{Client, StatusCode, header};
use sqlx::{PgPool, Row};
use tempfile::tempdir;
use time::OffsetDateTime;
use url::Url;
use uuid::Uuid;

use support::{
    TestDatabase, create_space, register_human, reserve_local_port, spawn_server, wait_for_health,
    write_server_config,
};

#[tokio::test]
async fn agent_graph_aggregates_visible_interactions_and_hides_private_dm_bodies() -> Result<()> {
    let database = TestDatabase::create("sumi_agent_graph").await?;
    let result = run_agent_graph_test(&database).await;
    database.drop().await?;
    result
}

async fn run_agent_graph_test(database: &TestDatabase) -> Result<()> {
    let root = tempdir()?;
    let web_dist = root.path().join("web");
    let attachments = root.path().join("attachments");
    std::fs::create_dir(&web_dist)?;
    std::fs::write(
        web_dist.join("index.html"),
        "<!doctype html><title>Sumi</title>",
    )?;

    let bind = SocketAddr::from(([127, 0, 0, 1], reserve_local_port()?));
    let server_url = Url::parse(&format!("http://{bind}"))?;
    let config = root.path().join("server.toml");
    write_server_config(&config, bind, &database.url, &attachments, &web_dist)?;
    let mut server = spawn_server(&config)?;
    wait_for_health(&server_url).await?;

    let client = Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .build()?;
    let cookie = register_human(&client, &server_url).await?;
    let space = create_space(&client, &server_url, &cookie).await?;
    let space_id = space.id;
    let human_id = space.owner_member_id;

    let pool = PgPool::connect(&database.url)
        .await
        .context("connect test database")?;
    seed_graph(&pool, space_id, human_id).await?;

    let response = client
        .get(server_url.join(&format!("/api/v1/spaces/{space_id}/agent-graph"))?)
        .header(header::COOKIE, &cookie)
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::OK,
        "agent graph status: {}",
        response.status()
    );
    let graph: serde_json::Value = response.json().await?;
    let nodes = graph
        .get("nodes")
        .context("missing nodes")?
        .as_array()
        .context("nodes is not an array")?;
    ensure!(
        nodes.len() == 3,
        "expected 3 Agent nodes, got {}",
        nodes.len()
    );
    ensure!(
        nodes.iter().all(|node| node.get("display_name").is_some()),
        "every node needs a display name"
    );

    let edges = graph
        .get("edges")
        .context("missing edges")?
        .as_array()
        .context("edges is not an array")?;
    ensure!(edges.len() == 1, "expected one edge, got {}", edges.len());
    let edge = &edges[0];
    ensure!(
        edge["dm_message_count"] == 2,
        "DM count includes only non-deleted messages"
    );
    ensure!(edge["mention_a_to_b"] == 1, "mention direction a to b");
    ensure!(edge["reply_b_to_a"] == 1, "reply direction b to a");
    ensure!(edge["total_interactions"] == 4, "total interaction count");
    ensure!(
        edge["last_message_at"].as_str().is_some(),
        "edge carries last_message_at"
    );

    // The human viewer is not a member of the Agent DM, so the governor sees counts but no bodies.
    let recent = edge["recent_messages"]
        .as_array()
        .context("recent messages")?;
    ensure!(
        recent.iter().all(|message| message["kind"] != "dm"),
        "private DM bodies must not be exposed without membership"
    );
    ensure!(
        recent.len() >= 2,
        "mention and reply previews from the readable channel are included"
    );

    let _ = server.interrupt().await;
    let _ = pool.close().await;
    Ok(())
}

async fn seed_graph(pool: &PgPool, space_id: Uuid, human_id: Uuid) -> Result<()> {
    let now = OffsetDateTime::now_utc();
    let agent_a = Uuid::from_u128(1);
    let agent_b = Uuid::from_u128(2);
    let agent_c = Uuid::from_u128(3);
    let computer_id = Uuid::from_u128(4);

    sqlx::query(
        "INSERT INTO computers (id, space_id, name, hostname, os, connection_status, created_at)
         VALUES ($1, $2, 'test', 'test-host', 'macos', 'offline', $3)",
    )
    .bind(computer_id)
    .bind(space_id)
    .bind(now)
    .execute(pool)
    .await?;

    for (member_id, name, role) in [
        (agent_a, "Coder_One", "Coder"),
        (agent_b, "Reviewer_Two", "Reviewer"),
        (agent_c, "Lone_Three", "Lone"),
    ] {
        sqlx::query(
            "INSERT INTO members (id, space_id, kind, display_name, access_level, created_at)
             VALUES ($1, $2, 'agent', $3, 'member', $4)",
        )
        .bind(member_id)
        .bind(space_id)
        .bind(name)
        .bind(now)
        .execute(pool)
        .await?;
        sqlx::query(
            "INSERT INTO agents (member_id, space_id, computer_id, role_text, role_revision,
                                 lifecycle, driver_kind, created_at)
             VALUES ($1, $2, $3, $4, 1, 'active', 'builtin', $5)",
        )
        .bind(member_id)
        .bind(space_id)
        .bind(computer_id)
        .bind(role)
        .bind(now)
        .execute(pool)
        .await?;
    }

    let direct_channel = Uuid::from_u128(10);
    sqlx::query(
        "INSERT INTO channels (id, space_id, kind, created_at) VALUES ($1, $2, 'direct', $3)",
    )
    .bind(direct_channel)
    .bind(space_id)
    .bind(now)
    .execute(pool)
    .await?;
    for member in [agent_a, agent_b] {
        sqlx::query(
            "INSERT INTO channel_members (channel_id, space_id, member_id, joined_at)
             VALUES ($1, $2, $3, $4)",
        )
        .bind(direct_channel)
        .bind(space_id)
        .bind(member)
        .bind(now)
        .execute(pool)
        .await?;
    }

    let shared_channel = Uuid::from_u128(11);
    sqlx::query(
        "INSERT INTO channels (id, space_id, kind, slug, created_at)
         VALUES ($1, $2, 'private', 'graph-lab', $3)",
    )
    .bind(shared_channel)
    .bind(space_id)
    .bind(now)
    .execute(pool)
    .await?;
    for member in [agent_a, agent_b, human_id] {
        sqlx::query(
            "INSERT INTO channel_members (channel_id, space_id, member_id, joined_at)
             VALUES ($1, $2, $3, $4)",
        )
        .bind(shared_channel)
        .bind(space_id)
        .bind(member)
        .bind(now)
        .execute(pool)
        .await?;
    }

    let _dm_one = insert_root_message(
        pool,
        space_id,
        direct_channel,
        agent_a,
        "dm one",
        now,
        1,
        false,
    )
    .await?;
    let _dm_two = insert_root_message(
        pool,
        space_id,
        direct_channel,
        agent_b,
        "dm two",
        now.checked_add(time::Duration::seconds(1)).expect("time"),
        2,
        false,
    )
    .await?;
    let _dm_deleted = insert_root_message(
        pool,
        space_id,
        direct_channel,
        agent_a,
        "deleted dm",
        now.checked_add(time::Duration::seconds(2)).expect("time"),
        3,
        true,
    )
    .await?;

    let mention_root = insert_root_message(
        pool,
        space_id,
        shared_channel,
        agent_a,
        "please review this",
        now.checked_add(time::Duration::seconds(3)).expect("time"),
        1,
        false,
    )
    .await?;
    sqlx::query(
        "INSERT INTO message_mentions (message_id, space_id, member_id, created_at)
         VALUES ($1, $2, $3, $4)",
    )
    .bind(mention_root)
    .bind(space_id)
    .bind(agent_b)
    .bind(now)
    .execute(pool)
    .await?;
    insert_reply_message(
        pool,
        space_id,
        shared_channel,
        agent_b,
        mention_root,
        "looks good",
        now.checked_add(time::Duration::seconds(4)).expect("time"),
        2,
    )
    .await?;
    Ok(())
}

#[allow(clippy::too_many_arguments)]
async fn insert_root_message(
    pool: &PgPool,
    space_id: Uuid,
    channel_id: Uuid,
    author_member_id: Uuid,
    body: &str,
    created_at: OffsetDateTime,
    channel_seq: i64,
    deleted: bool,
) -> Result<Uuid> {
    let id = Uuid::now_v7();
    let deleted_at = if deleted { Some(created_at) } else { None };
    sqlx::query(
        "INSERT INTO messages (id, space_id, channel_id, thread_id, channel_seq, placement,
                               content_kind, author_member_id, body_markdown, created_at, deleted_at)
         VALUES ($1, $2, $3, $1, $4, 'root', 'text', $5, $6, $7, $8)",
    )
    .bind(id)
    .bind(space_id)
    .bind(channel_id)
    .bind(channel_seq)
    .bind(author_member_id)
    .bind(body)
    .bind(created_at)
    .bind(deleted_at)
    .execute(pool)
    .await?;
    Ok(id)
}

#[allow(clippy::too_many_arguments)]
async fn insert_reply_message(
    pool: &PgPool,
    space_id: Uuid,
    channel_id: Uuid,
    author_member_id: Uuid,
    reply_to_message_id: Uuid,
    body: &str,
    created_at: OffsetDateTime,
    channel_seq: i64,
) -> Result<Uuid> {
    let id = Uuid::now_v7();
    let thread_id: Uuid = sqlx::query("SELECT thread_id FROM messages WHERE id = $1")
        .bind(reply_to_message_id)
        .fetch_one(pool)
        .await?
        .get("thread_id");
    sqlx::query(
        "INSERT INTO messages (id, space_id, channel_id, thread_id, channel_seq, placement,
                               content_kind, reply_to_message_id, author_member_id, body_markdown,
                               created_at)
         VALUES ($1, $2, $3, $4, $5, 'reply', 'text', $6, $7, $8, $9)",
    )
    .bind(id)
    .bind(space_id)
    .bind(channel_id)
    .bind(thread_id)
    .bind(channel_seq)
    .bind(reply_to_message_id)
    .bind(author_member_id)
    .bind(body)
    .bind(created_at)
    .execute(pool)
    .await?;
    Ok(id)
}
