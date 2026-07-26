mod support;

use std::{ffi::OsStr, net::SocketAddr, path::Path, time::Duration};

use anyhow::{Context, Result, ensure};
use reqwest::{Client, StatusCode, header};
use serde::Deserialize;
use sqlx::postgres::PgPoolOptions;
use support::{
    SumiProcess, TcpCutProxy, TestDatabase, empty_path, reserve_local_port, wait_for_health,
    write_computer_config, write_server_config,
};
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
        Duration::from_secs(20),
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
            "accent": "#5065D8"
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
