mod support;

use std::{net::SocketAddr, os::unix::fs::symlink, path::Path, time::Duration};

use anyhow::{Context, Result, ensure};
use reqwest::{Client, StatusCode, header};
use sha2::{Digest, Sha256};
use sqlx::{Row, postgres::PgPoolOptions, sqlite::SqlitePoolOptions};
use support::{
    TestDatabase, confirm_pairing, create_space, pairing_url_from_daemon, register_human,
    reserve_local_port, spawn_computer, spawn_server, wait_for_computer_status, wait_for_health,
    write_builtin_computer_config, write_server_config,
};
use url::Url;
use uuid::Uuid;

const MEMORY_BODY: &str = "memory-body-must-stay-on-the-computer\n";

#[tokio::test]
async fn agent_memory_is_transient_scoped_and_unavailable_offline() -> Result<()> {
    let root = tempfile::tempdir()?;
    let database = TestDatabase::create("sumi_agent_memory").await?;
    let result = run_memory_flow(root.path(), &database).await;
    database.drop().await?;
    result
}

async fn run_memory_flow(root: &Path, database: &TestDatabase) -> Result<()> {
    let server_port = reserve_local_port()?;
    let server_address = SocketAddr::from(([127, 0, 0, 1], server_port));
    let server_url = Url::parse(&format!("http://{server_address}"))?;
    let server_config = root.join("server.toml");
    write_server_config(
        &server_config,
        server_address,
        &database.url,
        &root.join("attachments"),
        &root.join("web-dist"),
    )?;
    let mut server = spawn_server(&server_config)?;
    wait_for_health(&server_url).await?;

    let computer_state = root.join("computer");
    let computer_config = root.join("computer.toml");
    write_builtin_computer_config(&computer_config, &server_url, &computer_state, &server_url)?;
    let client = Client::new();
    let cookie = register_human(&client, &server_url).await?;
    let space = create_space(&client, &server_url, &cookie).await?;
    let mut daemon = spawn_computer(&computer_config)?;
    let pairing_url = pairing_url_from_daemon(&mut daemon).await?;
    let computer = confirm_pairing(&client, &server_url, &cookie, space.id, &pairing_url).await?;
    wait_for_computer_status(&client, &server_url, &cookie, space.id, "online").await?;

    let created = client
        .post(server_url.join(&format!("/api/v1/spaces/{}/agents", space.id))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &cookie)
        .json(&serde_json::json!({
            "computer_id": computer.id,
            "name": "Memory Agent",
            "handle": "memory-agent",
            "role_text": "Maintain durable local Memory.",
            "access_level": "member",
            "driver_kind": "builtin"
        }))
        .send()
        .await?;
    ensure!(created.status() == StatusCode::CREATED);
    let created: serde_json::Value = created.json().await?;
    let agent_id = Uuid::parse_str(created["member_id"].as_str().context("Agent id missing")?)?;
    let pool = PgPoolOptions::new()
        .max_connections(3)
        .connect(&database.url)
        .await?;
    wait_for_agent_status(&pool, agent_id, "active").await?;

    let memory_root = computer_state
        .join("agents")
        .join(agent_id.to_string())
        .join("memory");
    let note = memory_root.join("notes/current.md");
    tokio::fs::create_dir_all(note.parent().context("Memory note has no parent")?).await?;
    tokio::fs::write(&note, MEMORY_BODY).await?;

    let configured = client
        .patch(server_url.join(&format!("/api/v1/agents/{agent_id}"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &cookie)
        .json(&serde_json::json!({
            "role_text": "Maintain durable local Memory and refresh its index."
        }))
        .send()
        .await?;
    ensure!(configured.status() == StatusCode::OK);
    wait_for_memory_snapshot(&pool, agent_id, "notes/current.md", MEMORY_BODY.as_bytes()).await?;

    let detail = client
        .get(server_url.join(&format!("/api/v1/agents/{agent_id}"))?)
        .header(header::COOKIE, &cookie)
        .send()
        .await?;
    ensure!(detail.status() == StatusCode::OK);
    let detail: serde_json::Value = detail.json().await?;
    ensure!(detail["memory_files"].as_array().is_some_and(|files| {
        files.iter().any(|file| {
            file["path"] == "notes/current.md"
                && file["size"] == MEMORY_BODY.len() as u64
                && file["sha256"] == hex::encode(Sha256::digest(MEMORY_BODY.as_bytes()))
        })
    }));

    let read = read_memory(&client, &server_url, &cookie, agent_id, "notes/current.md").await?;
    ensure!(read.status() == StatusCode::OK);
    ensure!(
        read.headers()
            .get(header::CACHE_CONTROL)
            .is_some_and(|value| value == "no-store")
    );
    let content: serde_json::Value = read.json().await?;
    ensure!(content["path"] == "notes/current.md" && content["content"] == MEMORY_BODY);
    ensure!(content["size"] == MEMORY_BODY.len() as u64);

    assert_memory_not_persisted(&pool, &computer_state, MEMORY_BODY).await?;
    ensure!(!server.logs_contain(MEMORY_BODY.trim()));
    ensure!(!daemon.logs_contain(MEMORY_BODY.trim()));

    tokio::fs::write(&note, vec![b'x'; 1024 * 1024 + 1]).await?;
    assert_memory_read_failed(&client, &server_url, &cookie, agent_id).await?;

    tokio::fs::write(&note, [0xff, 0xfe, 0xfd]).await?;
    assert_memory_read_failed(&client, &server_url, &cookie, agent_id).await?;

    tokio::fs::remove_file(&note).await?;
    let outside = root.join("outside-memory.md");
    tokio::fs::write(&outside, "outside Agent Home").await?;
    symlink(&outside, &note)?;
    assert_memory_read_failed(&client, &server_url, &cookie, agent_id).await?;

    let invalid = read_memory(
        &client,
        &server_url,
        &cookie,
        agent_id,
        "../outside-memory.md",
    )
    .await?;
    ensure!(invalid.status() == StatusCode::NOT_FOUND);

    let commands_before_offline: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM computer_commands WHERE kind = 'agent.memory.read'",
    )
    .fetch_one(&pool)
    .await?;
    daemon.crash().await?;
    wait_for_computer_offline(&pool, computer.id).await?;
    let offline = read_memory(&client, &server_url, &cookie, agent_id, "notes/current.md").await?;
    ensure!(offline.status() == StatusCode::CONFLICT);
    let offline_error: serde_json::Value = offline.json().await?;
    ensure!(offline_error["error"]["code"] == "computer_offline");
    let commands_after_offline: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM computer_commands WHERE kind = 'agent.memory.read'",
    )
    .fetch_one(&pool)
    .await?;
    ensure!(commands_after_offline == commands_before_offline);

    server.interrupt().await?;
    pool.close().await;
    Ok(())
}

async fn read_memory(
    client: &Client,
    server: &Url,
    cookie: &str,
    agent_id: Uuid,
    path: &str,
) -> Result<reqwest::Response> {
    Ok(client
        .post(server.join(&format!("/api/v1/agents/{agent_id}/memory/read"))?)
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({ "path": path }))
        .send()
        .await?)
}

async fn assert_memory_read_failed(
    client: &Client,
    server: &Url,
    cookie: &str,
    agent_id: Uuid,
) -> Result<()> {
    let response = read_memory(client, server, cookie, agent_id, "notes/current.md").await?;
    ensure!(response.status() == StatusCode::CONFLICT);
    let error: serde_json::Value = response.json().await?;
    ensure!(error["error"]["code"] == "memory_read_failed");
    Ok(())
}

async fn wait_for_agent_status(pool: &sqlx::PgPool, agent_id: Uuid, expected: &str) -> Result<()> {
    tokio::time::timeout(Duration::from_secs(20), async {
        loop {
            let status: Option<String> =
                sqlx::query_scalar("SELECT provision_status FROM agents WHERE member_id = $1")
                    .bind(agent_id)
                    .fetch_optional(pool)
                    .await?;
            if status.as_deref()
                == Some(if expected == "active" {
                    "ready"
                } else {
                    expected
                })
            {
                return Ok::<_, sqlx::Error>(());
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await
    .with_context(|| format!("Agent did not become {expected}"))??;
    Ok(())
}

async fn wait_for_computer_offline(pool: &sqlx::PgPool, computer_id: Uuid) -> Result<()> {
    tokio::time::timeout(Duration::from_secs(45), async {
        loop {
            let status: String = sqlx::query_scalar("SELECT status FROM computers WHERE id = $1")
                .bind(computer_id)
                .fetch_one(pool)
                .await?;
            if status == "offline" {
                return Ok::<_, sqlx::Error>(());
            }
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
    })
    .await
    .context("Computer did not become offline after its heartbeat expired")??;
    Ok(())
}

async fn wait_for_memory_snapshot(
    pool: &sqlx::PgPool,
    agent_id: Uuid,
    path: &str,
    bytes: &[u8],
) -> Result<()> {
    let expected_sha = Sha256::digest(bytes).to_vec();
    tokio::time::timeout(Duration::from_secs(20), async {
        loop {
            let snapshot: Option<(i64, Vec<u8>)> = sqlx::query_as(
                "SELECT size, sha256 FROM agent_memory_files \
                 WHERE agent_member_id = $1 AND path = $2",
            )
            .bind(agent_id)
            .bind(path)
            .fetch_optional(pool)
            .await?;
            if snapshot
                .as_ref()
                .is_some_and(|(size, sha)| *size == bytes.len() as i64 && *sha == expected_sha)
            {
                return Ok::<_, sqlx::Error>(());
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await
    .context("daemon did not refresh the Memory metadata snapshot")??;
    Ok(())
}

async fn assert_memory_not_persisted(
    pool: &sqlx::PgPool,
    computer_state: &Path,
    secret: &str,
) -> Result<()> {
    let persisted: (i64, i64, i64, i64) = sqlx::query_as(
        "SELECT \
           (SELECT count(*) FROM computer_commands \
             WHERE payload_json::text LIKE $1 OR result_json::text LIKE $1), \
           (SELECT count(*) FROM idempotency_records WHERE response_json::text LIKE $1), \
           (SELECT count(*) FROM audit_events WHERE metadata_json::text LIKE $1), \
           (SELECT count(*) FROM outbox_events WHERE payload_json::text LIKE $1)",
    )
    .bind(format!("%{}%", secret.trim()))
    .fetch_one(pool)
    .await?;
    ensure!(persisted == (0, 0, 0, 0));

    let command: serde_json::Value = sqlx::query_scalar(
        "SELECT result_json FROM computer_commands WHERE kind = 'agent.memory.read' \
         ORDER BY completed_at DESC LIMIT 1",
    )
    .fetch_one(pool)
    .await?;
    ensure!(command.get("content").is_none());

    let sqlite = SqlitePoolOptions::new()
        .max_connections(1)
        .connect(&format!(
            "sqlite://{}?mode=ro",
            computer_state.join("daemon.db").display()
        ))
        .await?;
    let rows = sqlx::query("SELECT request_json, result_json FROM server_commands")
        .fetch_all(&sqlite)
        .await?;
    ensure!(rows.iter().all(|row| {
        let request: String = row.get("request_json");
        let result: Option<String> = row.get("result_json");
        !request.contains(secret.trim())
            && !result
                .as_deref()
                .is_some_and(|value| value.contains(secret.trim()))
    }));
    sqlite.close().await;
    Ok(())
}
