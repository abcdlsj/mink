mod support;

use std::net::SocketAddr;

use anyhow::{Context, Result, ensure};
use reqwest::{Client, StatusCode, header};
use sqlx::postgres::PgPoolOptions;
use tempfile::tempdir;
use url::Url;
use uuid::Uuid;

use support::{
    TestDatabase, create_space, register_human, reserve_local_port, spawn_server, wait_for_health,
    write_server_config,
};

#[tokio::test]
async fn current_run_with_a_dm_focus_returns_a_null_channel_slug() -> Result<()> {
    let database = TestDatabase::create("sumi_dm_run_focus").await?;
    let result = run_dm_focus_flow(&database).await;
    database.drop().await?;
    result
}

async fn run_dm_focus_flow(database: &TestDatabase) -> Result<()> {
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
    let owner = register_human(&client, &server_url).await?;
    let space = create_space(&client, &server_url, &owner).await?;
    let space_id = space.id;

    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database.url)
        .await
        .context("connect to test database")?;
    let computer_id = Uuid::now_v7();
    let agent_id = Uuid::now_v7();
    let dm_channel_id = Uuid::now_v7();
    let root_message_id = Uuid::now_v7();
    let run_id = Uuid::now_v7();

    sqlx::query(
        "INSERT INTO computers \
         (id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) \
         VALUES ($1,$2,$3,'localhost','linux','test-token','online',1,now())",
    )
    .bind(computer_id)
    .bind(space_id)
    .bind("Test Computer")
    .execute(&pool)
    .await?;
    sqlx::query(
        "INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) \
         VALUES ($1,$2,'agent','Lin','member',now())",
    )
    .bind(agent_id)
    .bind(space_id)
    .execute(&pool)
    .await?;
    sqlx::query(
        "INSERT INTO agents \
         (member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) \
         VALUES ($1,$2,$3,'Assist',1,'active','codex',now())",
    )
    .bind(agent_id)
    .bind(space_id)
    .bind(computer_id)
    .execute(&pool)
    .await?;
    sqlx::query(
        "INSERT INTO channels (id,space_id,kind,slug,topic,next_seq,created_at) \
         VALUES ($1,$2,'direct',NULL,NULL,1,now())",
    )
    .bind(dm_channel_id)
    .bind(space_id)
    .execute(&pool)
    .await?;
    for member_id in [space.owner_member_id, agent_id] {
        sqlx::query(
            "INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) \
             VALUES ($1,$2,$3,now(),0)",
        )
        .bind(dm_channel_id)
        .bind(space_id)
        .bind(member_id)
        .execute(&pool)
        .await?;
    }
    sqlx::query(
        "INSERT INTO messages \
         (id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,mention_all,created_at) \
         VALUES ($1,$2,$3,$1,1,'root','text',$4,'hello',false,now())",
    )
    .bind(root_message_id)
    .bind(space_id)
    .bind(dm_channel_id)
    .bind(space.owner_member_id)
    .execute(&pool)
    .await?;
    sqlx::query(
        "INSERT INTO agent_runs \
         (id,space_id,agent_id,task_id,focus_thread_id,status,fencing_token_hash,lease_expires_at,created_at,started_at) \
         VALUES ($1,$2,$3,NULL,$4,'running','test',now()+interval '1 hour',now(),now())",
    )
    .bind(run_id)
    .bind(space_id)
    .bind(agent_id)
    .bind(root_message_id)
    .execute(&pool)
    .await?;

    let response = client
        .get(server_url.join(&format!("/api/v1/agents/{agent_id}/runs/current"))?)
        .header(header::COOKIE, &owner)
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::OK,
        "current run endpoint: {}",
        response.status()
    );
    let body: serde_json::Value = response.json().await?;
    let focus = &body["current_run"]["focus"];
    ensure!(
        focus["channel_slug"].is_null(),
        "DM Focus must expose a null channel_slug"
    );
    ensure!(
        focus["root_message_seq"] == 1,
        "DM Focus must expose the root message sequence"
    );

    server.interrupt().await?;
    Ok(())
}
