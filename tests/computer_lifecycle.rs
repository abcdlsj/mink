mod support;

use std::{ffi::OsStr, net::SocketAddr, path::Path, time::Duration};

use anyhow::{Context, Result, ensure};
use reqwest::{Client, StatusCode, header};
use serde::Deserialize;
use sqlx::{
    PgPool,
    postgres::PgPoolOptions,
    sqlite::{SqliteConnectOptions, SqlitePoolOptions},
};
use support::{
    SumiProcess, TcpCutProxy, TestDatabase, empty_path, reserve_local_port, wait_for_health,
    write_computer_config, write_server_config,
};
use tokio_tungstenite::tungstenite::{self, client::IntoClientRequest};
use url::Url;
use uuid::Uuid;

#[derive(Clone, Deserialize, Eq, PartialEq)]
struct StoredComputerIdentity {
    token: String,
    pairing_id: Option<Uuid>,
    computer_id: Option<Uuid>,
    space_id: Option<Uuid>,
}

#[derive(Deserialize)]
struct SpaceResponse {
    id: Uuid,
}

#[derive(Deserialize)]
struct ComputerResponse {
    id: Uuid,
    status: String,
}

#[tokio::test]
async fn computer_reuses_identity_across_process_and_network_lifecycle() -> Result<()> {
    let root = tempfile::tempdir()?;
    let database = TestDatabase::create("sumi_lifecycle_test").await?;
    let server_port = reserve_local_port()?;
    let server_address = SocketAddr::from(([127, 0, 0, 1], server_port));
    let server_url = Url::parse(&format!("http://{server_address}"))?;
    let server_config = root.path().join("server.toml");
    write_server_config(
        &server_config,
        server_address,
        &database.url,
        &root.path().join("attachments"),
        &root.path().join("web-dist"),
    )?;
    let mut server = spawn_server(&server_config)?;
    wait_for_health(&server_url).await?;

    let proxy = TcpCutProxy::start(server_address).await?;
    let client = Client::new();
    let cookie = register_human(&client, &server_url).await?;
    let space = create_space(&client, &server_url, &cookie).await?;

    let computer_state = root.path().join("computer");
    let computer_config = root.path().join("computer.toml");
    write_computer_config(&computer_config, &proxy.url()?, &computer_state)?;
    let process_path = empty_path(root.path())?;
    let mut daemon = spawn_computer(&computer_config, &process_path)?;
    let pairing_url = pairing_url_from_daemon(&mut daemon).await?;
    let paired = confirm_pairing(&client, &server_url, &cookie, space.id, &pairing_url).await?;
    wait_for_computer_status(
        &client,
        &server_url,
        &cookie,
        space.id,
        "online",
        Duration::from_secs(20),
    )
    .await?;

    let secrets_path = computer_state.join("secrets.json");
    let original = wait_for_identity(&secrets_path).await?;
    ensure!(original.pairing_id.is_none());
    ensure!(original.computer_id == Some(paired.id));
    ensure!(original.space_id == Some(space.id));
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database.url)
        .await?;
    assert_single_pairing(&pool, paired.id).await?;

    daemon.interrupt().await?;
    wait_for_computer_status(
        &client,
        &server_url,
        &cookie,
        space.id,
        "offline",
        Duration::from_secs(40),
    )
    .await?;

    let mut daemon = spawn_computer(&computer_config, &process_path)?;
    wait_for_computer_status(
        &client,
        &server_url,
        &cookie,
        space.id,
        "online",
        Duration::from_secs(20),
    )
    .await?;
    assert_identity_unchanged(&secrets_path, &original).await?;
    assert_single_pairing(&pool, paired.id).await?;

    proxy.set_enabled(false);
    wait_for_computer_status(
        &client,
        &server_url,
        &cookie,
        space.id,
        "offline",
        Duration::from_secs(40),
    )
    .await?;
    daemon.ensure_running()?;
    proxy.set_enabled(true);
    wait_for_computer_status(
        &client,
        &server_url,
        &cookie,
        space.id,
        "online",
        Duration::from_secs(40),
    )
    .await?;
    assert_identity_unchanged(&secrets_path, &original).await?;

    server.interrupt().await?;
    daemon.ensure_running()?;
    server = spawn_server(&server_config)?;
    wait_for_health(&server_url).await?;
    wait_for_computer_status(
        &client,
        &server_url,
        &cookie,
        space.id,
        "online",
        Duration::from_secs(20),
    )
    .await?;
    assert_identity_unchanged(&secrets_path, &original).await?;
    assert_single_pairing(&pool, paired.id).await?;

    daemon.interrupt().await?;
    server.interrupt().await?;
    pool.close().await;
    database.drop().await?;
    Ok(())
}

#[tokio::test]
async fn delete_computer_revokes_identity_and_preserves_agent_history() -> Result<()> {
    let root = tempfile::tempdir()?;
    let database = TestDatabase::create("sumi_delete_lifecycle_test").await?;
    let server_port = reserve_local_port()?;
    let server_address = SocketAddr::from(([127, 0, 0, 1], server_port));
    let server_url = Url::parse(&format!("http://{server_address}"))?;
    let server_config = root.path().join("server.toml");
    write_server_config(
        &server_config,
        server_address,
        &database.url,
        &root.path().join("attachments"),
        &root.path().join("web-dist"),
    )?;
    let mut server = spawn_server(&server_config)?;
    wait_for_health(&server_url).await?;

    let client = Client::new();
    let cookie = register_human(&client, &server_url).await?;
    let space = create_space(&client, &server_url, &cookie).await?;
    let computer_state = root.path().join("computer");
    let computer_config = root.path().join("computer.toml");
    write_computer_config(&computer_config, &server_url, &computer_state)?;
    let process_path = empty_path(root.path())?;
    let secrets_path = computer_state.join("secrets.json");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database.url)
        .await?;

    let mut daemon = spawn_computer(&computer_config, &process_path)?;
    let pairing_url = pairing_url_from_daemon(&mut daemon).await?;
    let first_computer =
        confirm_pairing(&client, &server_url, &cookie, space.id, &pairing_url).await?;
    wait_for_computer_status(
        &client,
        &server_url,
        &cookie,
        space.id,
        "online",
        Duration::from_secs(20),
    )
    .await?;
    let first_identity = wait_for_identity(&secrets_path).await?;
    let (agent_id, run_id, home_marker) =
        seed_agent_history(&pool, &computer_state, space.id, first_computer.id).await?;
    seed_local_daemon_state(&computer_state).await?;

    delete_computer(&client, &server_url, &cookie, first_computer.id).await?;
    daemon.wait_for_success(Duration::from_secs(15)).await?;
    ensure!(
        !secrets_path.exists(),
        "online deletion retained old identity"
    );
    assert_deleted_server_state(&pool, first_computer.id, agent_id, run_id).await?;
    assert_local_daemon_state_cleared(&computer_state).await?;
    ensure!(home_marker.exists(), "online deletion removed Agent Home");
    assert_token_rejected(&server_url, first_computer.id, &first_identity.token).await?;

    let mut daemon = spawn_computer(&computer_config, &process_path)?;
    let pairing_url = pairing_url_from_daemon(&mut daemon).await?;
    let unpaired_second = read_identity(&secrets_path).await?;
    ensure!(unpaired_second.token != first_identity.token);
    ensure!(unpaired_second.computer_id.is_none() && unpaired_second.space_id.is_none());
    let second_computer =
        confirm_pairing(&client, &server_url, &cookie, space.id, &pairing_url).await?;
    wait_for_computer_status(
        &client,
        &server_url,
        &cookie,
        space.id,
        "online",
        Duration::from_secs(20),
    )
    .await?;
    let second_identity = wait_for_identity(&secrets_path).await?;
    ensure!(second_identity.computer_id == Some(second_computer.id));
    ensure!(home_marker.exists(), "re-pair removed prior Agent Home");

    daemon.interrupt().await?;
    wait_for_computer_status(
        &client,
        &server_url,
        &cookie,
        space.id,
        "offline",
        Duration::from_secs(40),
    )
    .await?;
    delete_computer(&client, &server_url, &cookie, second_computer.id).await?;
    assert_identity_unchanged(&secrets_path, &second_identity).await?;

    let mut rejected_daemon = spawn_computer(&computer_config, &process_path)?;
    rejected_daemon
        .wait_for_success(Duration::from_secs(15))
        .await?;
    ensure!(
        !secrets_path.exists(),
        "offline deletion did not clear rejected identity"
    );
    assert_token_rejected(&server_url, second_computer.id, &second_identity.token).await?;
    ensure!(home_marker.exists(), "offline deletion removed Agent Home");

    let mut daemon = spawn_computer(&computer_config, &process_path)?;
    let pairing_url = pairing_url_from_daemon(&mut daemon).await?;
    let unpaired_third = read_identity(&secrets_path).await?;
    ensure!(unpaired_third.token != first_identity.token);
    ensure!(unpaired_third.token != second_identity.token);
    let third_computer =
        confirm_pairing(&client, &server_url, &cookie, space.id, &pairing_url).await?;
    wait_for_computer_status(
        &client,
        &server_url,
        &cookie,
        space.id,
        "online",
        Duration::from_secs(20),
    )
    .await?;
    ensure!(third_computer.id != first_computer.id && third_computer.id != second_computer.id);
    assert_agent_history_retained(&pool, agent_id, first_computer.id).await?;
    ensure!(
        home_marker.exists(),
        "new pairing removed historical Agent Home"
    );

    daemon.interrupt().await?;
    server.interrupt().await?;
    pool.close().await;
    database.drop().await?;
    Ok(())
}

async fn seed_agent_history(
    pool: &PgPool,
    computer_state: &Path,
    space_id: Uuid,
    computer_id: Uuid,
) -> Result<(Uuid, Uuid, std::path::PathBuf)> {
    let agent_id = Uuid::now_v7();
    let run_id = Uuid::now_v7();
    let owner_id: Uuid = sqlx::query_scalar("SELECT owner_member_id FROM spaces WHERE id = $1")
        .bind(space_id)
        .fetch_one(pool)
        .await?;
    let mut transaction = pool.begin().await?;
    sqlx::query(
        "INSERT INTO members (id, space_id, kind, display_name, handle, avatar_seed, \
         access_level, created_at) VALUES ($1, $2, 'agent', 'Retained Agent', $3, $4, \
         'member', now())",
    )
    .bind(agent_id)
    .bind(space_id)
    .bind(format!("retained-{}", &agent_id.simple().to_string()[..8]))
    .bind(agent_id.to_string())
    .execute(&mut *transaction)
    .await?;
    sqlx::query(
        "INSERT INTO agents (member_id, space_id, computer_id, role_text, status, driver_kind, \
         driver_config_json, attention_config_json, created_by_member_id, created_at, updated_at) \
         VALUES ($1, $2, $3, 'Preserve history during Computer deletion', 'active', 'codex', \
         '{}'::jsonb, '{}'::jsonb, $4, now(), now())",
    )
    .bind(agent_id)
    .bind(space_id)
    .bind(computer_id)
    .bind(owner_id)
    .execute(&mut *transaction)
    .await?;
    sqlx::query(
        "INSERT INTO agent_runs (id, agent_member_id, computer_id, driver_kind, role_revision, \
         status, created_at, started_at, fencing_token, ownership_lease_expires_at, \
         last_renewed_at) VALUES ($1, $2, $3, 'codex', 1, 'running', now(), now(), $4, \
         now() + interval '35 minutes', now())",
    )
    .bind(run_id)
    .bind(agent_id)
    .bind(computer_id)
    .bind(Uuid::now_v7().to_string())
    .execute(&mut *transaction)
    .await?;
    transaction.commit().await?;

    let memory_dir = computer_state
        .join("agents")
        .join(agent_id.to_string())
        .join("memory");
    tokio::fs::create_dir_all(&memory_dir).await?;
    let marker = memory_dir.join("MEMORY.md");
    tokio::fs::write(&marker, "retained Agent Memory\n").await?;
    Ok((agent_id, run_id, marker))
}

async fn seed_local_daemon_state(computer_state: &Path) -> Result<()> {
    let database = SqlitePoolOptions::new()
        .max_connections(1)
        .connect_with(
            SqliteConnectOptions::new()
                .filename(computer_state.join("daemon.db"))
                .foreign_keys(true),
        )
        .await?;
    sqlx::query(
        "INSERT INTO daemon_metadata (key, value_json, updated_at) \
         VALUES ('delete-test', '{}', '2026-07-27T00:00:00Z')",
    )
    .execute(&database)
    .await?;
    sqlx::query(
        "INSERT INTO server_commands (command_id, computer_seq, request_json, status, result_json, \
         received_at, completed_at) VALUES ($1, 999999, '{}', 'completed', '{}', $2, $2)",
    )
    .bind(Uuid::now_v7().to_string())
    .bind("2026-07-27T00:00:00Z")
    .execute(&database)
    .await?;
    database.close().await;
    Ok(())
}

async fn assert_local_daemon_state_cleared(computer_state: &Path) -> Result<()> {
    let database = SqlitePoolOptions::new()
        .max_connections(1)
        .connect_with(SqliteConnectOptions::new().filename(computer_state.join("daemon.db")))
        .await?;
    for table in ["daemon_metadata", "server_commands", "local_agent_runs"] {
        let count: i64 = sqlx::query_scalar(&format!("SELECT count(*) FROM {table}"))
            .fetch_one(&database)
            .await?;
        ensure!(count == 0, "deleted identity retained rows in {table}");
    }
    database.close().await;
    Ok(())
}

async fn delete_computer(
    client: &Client,
    server: &Url,
    cookie: &str,
    computer_id: Uuid,
) -> Result<()> {
    let response = client
        .delete(server.join(&format!("/api/v1/computers/{computer_id}"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, cookie)
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::OK,
        "delete Computer: {}",
        response.status()
    );
    Ok(())
}

async fn assert_token_rejected(server: &Url, computer_id: Uuid, token: &str) -> Result<()> {
    let mut endpoint = server.join(&format!("/api/v1/computers/{computer_id}/connect"))?;
    endpoint
        .set_scheme("ws")
        .map_err(|_| anyhow::anyhow!("failed to build Computer WebSocket URL"))?;
    let mut request = endpoint.as_str().into_client_request()?;
    request.headers_mut().insert(
        tungstenite::http::header::AUTHORIZATION,
        format!("Bearer {token}").parse()?,
    );
    let connection = tokio_tungstenite::connect_async(request).await;
    ensure!(matches!(
        connection,
        Err(tungstenite::Error::Http(response))
            if response.status() == tungstenite::http::StatusCode::UNAUTHORIZED
    ));
    Ok(())
}

async fn assert_deleted_server_state(
    pool: &PgPool,
    computer_id: Uuid,
    agent_id: Uuid,
    run_id: Uuid,
) -> Result<()> {
    let computer: (String, Option<time::OffsetDateTime>) =
        sqlx::query_as("SELECT status, revoked_at FROM computers WHERE id = $1")
            .bind(computer_id)
            .fetch_one(pool)
            .await?;
    ensure!(computer.0 == "revoked" && computer.1.is_some());
    let agent: (String, Option<time::OffsetDateTime>) =
        sqlx::query_as("SELECT status, retired_at FROM agents WHERE member_id = $1")
            .bind(agent_id)
            .fetch_one(pool)
            .await?;
    ensure!(agent.0 == "retired" && agent.1.is_some());
    let member_retired_at: Option<time::OffsetDateTime> =
        sqlx::query_scalar("SELECT retired_at FROM members WHERE id = $1")
            .bind(agent_id)
            .fetch_one(pool)
            .await?;
    ensure!(member_retired_at.is_some());
    let run: (String, Option<String>, Option<time::OffsetDateTime>) =
        sqlx::query_as("SELECT status, error_code, finished_at FROM agent_runs WHERE id = $1")
            .bind(run_id)
            .fetch_one(pool)
            .await?;
    ensure!(run.0 == "canceled");
    ensure!(run.1.as_deref() == Some("computer_deleted") && run.2.is_some());
    assert_agent_history_retained(pool, agent_id, computer_id).await
}

async fn assert_agent_history_retained(
    pool: &PgPool,
    agent_id: Uuid,
    original_computer_id: Uuid,
) -> Result<()> {
    let agent: (Uuid, String, Option<time::OffsetDateTime>) =
        sqlx::query_as("SELECT computer_id, status, retired_at FROM agents WHERE member_id = $1")
            .bind(agent_id)
            .fetch_one(pool)
            .await?;
    ensure!(agent.0 == original_computer_id);
    ensure!(agent.1 == "retired" && agent.2.is_some());
    let pairing_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM computer_pairings WHERE computer_id = $1 AND status = 'confirmed'",
    )
    .bind(original_computer_id)
    .fetch_one(pool)
    .await?;
    ensure!(pairing_count == 1, "Computer pairing history was removed");
    Ok(())
}

async fn read_identity(path: &Path) -> Result<StoredComputerIdentity> {
    tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            if let Ok(bytes) = tokio::fs::read(path).await
                && let Ok(identity) = serde_json::from_slice::<StoredComputerIdentity>(&bytes)
            {
                return identity;
            }
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
    })
    .await
    .context("Computer identity was not readable")
}

fn spawn_server(config: &Path) -> Result<SumiProcess> {
    SumiProcess::spawn(
        [
            OsStr::new("server"),
            OsStr::new("--config"),
            config.as_os_str(),
        ],
        &[],
    )
}

fn spawn_computer(config: &Path, process_path: &Path) -> Result<SumiProcess> {
    SumiProcess::spawn(
        [
            OsStr::new("computer"),
            OsStr::new("--config"),
            config.as_os_str(),
        ],
        &[("PATH", process_path.as_os_str())],
    )
}

async fn register_human(client: &Client, server: &Url) -> Result<String> {
    let response = client
        .post(server.join("/api/v1/auth/register")?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .json(&serde_json::json!({
            "display_name": "Lifecycle Owner",
            "email": format!("lifecycle-{}@example.com", Uuid::now_v7()),
            "password": "correct horse battery staple"
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "register Human: {}",
        response.status()
    );
    Ok(response
        .headers()
        .get(header::SET_COOKIE)
        .context("registration did not set a Session cookie")?
        .to_str()?
        .split(';')
        .next()
        .context("Session cookie is empty")?
        .to_owned())
}

async fn create_space(client: &Client, server: &Url, cookie: &str) -> Result<SpaceResponse> {
    let response = client
        .post(server.join("/api/v1/spaces")?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({
            "name": "Lifecycle Lab",
            "slug": format!("life-{}", &Uuid::now_v7().simple().to_string()[..8]),
            "accent": "#FE7DA8"
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "create Space: {}",
        response.status()
    );
    Ok(response.json().await?)
}

async fn pairing_url_from_daemon(daemon: &mut SumiProcess) -> Result<Url> {
    let line = daemon
        .wait_for_stderr(
            "Open this URL to pair the Computer",
            Duration::from_secs(15),
        )
        .await?;
    let raw = line
        .split_once("url=")
        .context("pairing log did not include a URL")?
        .1
        .split_whitespace()
        .next()
        .context("pairing URL is empty")?;
    Ok(Url::parse(raw.trim_matches('"'))?)
}

async fn confirm_pairing(
    client: &Client,
    server: &Url,
    cookie: &str,
    space_id: Uuid,
    pairing_url: &Url,
) -> Result<ComputerResponse> {
    let pairing_id = pairing_url
        .path_segments()
        .and_then(|mut segments| segments.nth(1))
        .context("pairing URL has no pairing id")?
        .parse::<Uuid>()?;
    let code = pairing_url
        .query_pairs()
        .find_map(|(key, value)| (key == "code").then(|| value.into_owned()))
        .context("pairing URL has no code")?;
    let response = client
        .post(server.join(&format!("/api/v1/computer-pairings/{pairing_id}/confirm"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({
            "space_id": space_id,
            "name": "Lifecycle Computer",
            "code": code
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "confirm pairing: {}",
        response.status()
    );
    Ok(response.json().await?)
}

async fn wait_for_computer_status(
    client: &Client,
    server: &Url,
    cookie: &str,
    space_id: Uuid,
    expected: &str,
    duration: Duration,
) -> Result<()> {
    tokio::time::timeout(duration, async {
        loop {
            let response = client
                .get(server.join(&format!("/api/v1/spaces/{space_id}/computers"))?)
                .header(header::COOKIE, cookie)
                .send()
                .await?;
            if response.status().is_success() {
                let computers: Vec<ComputerResponse> = response.json().await?;
                ensure!(computers.len() == 1, "expected exactly one Computer");
                ensure!(
                    matches!(computers[0].status.as_str(), "online" | "offline"),
                    "unexpected Computer lifecycle status {}",
                    computers[0].status
                );
                if computers[0].status == expected {
                    return Ok(());
                }
            }
            tokio::time::sleep(Duration::from_millis(200)).await;
        }
    })
    .await
    .with_context(|| format!("Computer did not become {expected}"))?
}

async fn wait_for_identity(path: &Path) -> Result<StoredComputerIdentity> {
    tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            if let Ok(bytes) = tokio::fs::read(path).await
                && let Ok(identity) = serde_json::from_slice::<StoredComputerIdentity>(&bytes)
                && identity.computer_id.is_some()
            {
                return identity;
            }
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
    })
    .await
    .context("Computer identity was not persisted")
}

async fn assert_identity_unchanged(path: &Path, expected: &StoredComputerIdentity) -> Result<()> {
    let actual: StoredComputerIdentity = serde_json::from_slice(&tokio::fs::read(path).await?)?;
    ensure!(
        actual == *expected,
        "Computer identity changed across reconnect"
    );
    Ok(())
}

async fn assert_single_pairing(pool: &sqlx::PgPool, computer_id: Uuid) -> Result<()> {
    let pairing_count: i64 = sqlx::query_scalar("SELECT count(*) FROM computer_pairings")
        .fetch_one(pool)
        .await?;
    ensure!(
        pairing_count == 1,
        "daemon unexpectedly started another pairing"
    );
    let paired_computer: Uuid =
        sqlx::query_scalar("SELECT computer_id FROM computer_pairings WHERE status = 'confirmed'")
            .fetch_one(pool)
            .await?;
    ensure!(paired_computer == computer_id);
    Ok(())
}
