#[path = "../support/mod.rs"]
mod support;

use std::net::SocketAddr;

use anyhow::{Context, Result, ensure};
use reqwest::{Client, StatusCode, header};
use sqlx::PgPool;
use tempfile::tempdir;
use time::OffsetDateTime;
use url::Url;
use uuid::Uuid;

use support::{
    TestDatabase, create_space, register_human, reserve_local_port, spawn_server, wait_for_health,
    write_server_config,
};

#[tokio::test]
async fn llm_usage_requires_governance_and_reports_offline_computers() -> Result<()> {
    let database = TestDatabase::create("sumi_computer_llm_usage").await?;
    let result = run_llm_usage_test(&database).await;
    database.drop().await?;
    result
}

async fn run_llm_usage_test(database: &TestDatabase) -> Result<()> {
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

    let pool = PgPool::connect(&database.url)
        .await
        .context("connect test database")?;
    let computer_id = Uuid::now_v7();
    sqlx::query(
        "INSERT INTO computers (id, space_id, name, hostname, os, connection_status, created_at)
         VALUES ($1, $2, 'offline-lab', 'offline-host', 'macos', 'offline', $3)",
    )
    .bind(computer_id)
    .bind(space.id)
    .bind(OffsetDateTime::now_utc())
    .execute(&pool)
    .await?;
    let _ = pool.close().await;

    let response = client
        .get(server_url.join(&format!(
            "/api/v1/computers/{computer_id}/llm-usage?range=7d"
        ))?)
        .header(header::COOKIE, &cookie)
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::SERVICE_UNAVAILABLE,
        "offline Computer must return 503, got {}",
        response.status()
    );

    let _ = server.interrupt().await;
    Ok(())
}
