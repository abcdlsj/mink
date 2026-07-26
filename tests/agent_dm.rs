mod support;

use std::{convert::Infallible, net::SocketAddr, time::Duration};

use anyhow::{Context, Result, ensure};
use axum::{
    Json, Router,
    body::{Body, Bytes},
    extract::State,
    http::{HeaderMap, StatusCode, header},
    response::Response,
    routing::post,
};
use futures_util::stream;
use reqwest::Client;
use sqlx::postgres::PgPoolOptions;
use support::{
    TestDatabase, confirm_pairing, create_space, pairing_url_from_daemon, register_human,
    reserve_local_port, spawn_computer, spawn_server, wait_for_computer_status, wait_for_health,
    write_builtin_computer_config, write_server_config,
};
use tokio::{net::TcpListener, sync::mpsc, task::JoinHandle};
use url::Url;
use uuid::Uuid;

struct FakeProvider {
    url: Url,
    requests: mpsc::Receiver<()>,
    task: JoinHandle<()>,
}

impl FakeProvider {
    async fn start() -> Result<Self> {
        let listener = TcpListener::bind(("127.0.0.1", 0)).await?;
        let address = listener.local_addr()?;
        let (sender, requests) = mpsc::channel(1);
        let app = Router::new()
            .route("/chat/completions", post(hold_chat_stream))
            .with_state(sender);
        let task = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });
        Ok(Self {
            url: Url::parse(&format!("http://{address}"))?,
            requests,
            task,
        })
    }

    async fn wait_for_request(&mut self) -> Result<()> {
        tokio::time::timeout(Duration::from_secs(20), self.requests.recv())
            .await
            .context("Builtin did not call the fake provider")?
            .context("fake provider stopped before receiving a request")
    }
}

impl Drop for FakeProvider {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn hold_chat_stream(
    State(requests): State<mpsc::Sender<()>>,
    headers: HeaderMap,
    Json(request): Json<serde_json::Value>,
) -> Response {
    let authorized = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value == "Bearer test-only-provider-key");
    let structurally_valid = request["stream"] == true
        && request["model"] == "sumi-test-model"
        && request["messages"].is_array();
    if !authorized || !structurally_valid {
        return Response::builder()
            .status(StatusCode::BAD_REQUEST)
            .body(Body::empty())
            .expect("static fake response");
    }
    let _ = requests.send(()).await;
    let pending = stream::pending::<std::result::Result<Bytes, Infallible>>();
    Response::builder()
        .header(header::CONTENT_TYPE, "text/event-stream")
        .body(Body::from_stream(pending))
        .expect("static fake response")
}

#[tokio::test]
async fn human_dm_launches_one_real_builtin_run() -> Result<()> {
    let root = tempfile::tempdir()?;
    let database = TestDatabase::create("sumi_agent_dm_process_test").await?;
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

    let mut provider = FakeProvider::start().await?;
    let computer_state = root.path().join("computer");
    let computer_config = root.path().join("computer.toml");
    write_builtin_computer_config(
        &computer_config,
        &server_url,
        &computer_state,
        &provider.url,
    )?;

    let client = Client::new();
    let cookie = register_human(&client, &server_url).await?;
    let space = create_space(&client, &server_url, &cookie).await?;
    let mut daemon = spawn_computer(&computer_config)?;
    let pairing_url = pairing_url_from_daemon(&mut daemon).await?;
    let paired = confirm_pairing(&client, &server_url, &cookie, space.id, &pairing_url).await?;
    let online =
        wait_for_computer_status(&client, &server_url, &cookie, space.id, "online").await?;
    ensure!(online.id == paired.id);

    let agent_response = client
        .post(server_url.join(&format!("/api/v1/spaces/{}/agents", space.id))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &cookie)
        .json(&serde_json::json!({
            "computer_id": paired.id,
            "name": "Lin",
            "handle": "lin",
            "role_text": "Read the current Inbox and respond through the Sumi Agent CLI.",
            "access_level": "member",
            "driver_kind": "builtin"
        }))
        .send()
        .await?;
    ensure!(
        agent_response.status() == StatusCode::CREATED,
        "create Agent: {}",
        agent_response.status()
    );
    let agent: serde_json::Value = agent_response.json().await?;
    let agent_id = Uuid::parse_str(agent["member_id"].as_str().context("Agent id missing")?)?;

    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database.url)
        .await?;
    wait_for_agent_active(&pool, agent_id).await?;

    let dm_response = client
        .post(server_url.join(&format!("/api/v1/spaces/{}/dms", space.id))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &cookie)
        .json(&serde_json::json!({ "member_id": agent_id }))
        .send()
        .await?;
    ensure!(
        dm_response.status() == StatusCode::CREATED,
        "create DM: {}",
        dm_response.status()
    );
    let dm: serde_json::Value = dm_response.json().await?;
    let channel_id = Uuid::parse_str(dm["channel_id"].as_str().context("DM channel id missing")?)?;

    let message_response = client
        .post(server_url.join(&format!("/api/v1/channels/{channel_id}/messages"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &cookie)
        .json(&serde_json::json!({
            "body_markdown": "Please inspect this DM.",
            "mentions": [],
            "attachment_ids": []
        }))
        .send()
        .await?;
    ensure!(
        message_response.status() == StatusCode::CREATED,
        "send DM: {}",
        message_response.status()
    );
    let message: serde_json::Value = message_response.json().await?;
    let message_id = Uuid::parse_str(message["id"].as_str().context("Message id missing")?)?;

    provider.wait_for_request().await?;
    assert_single_running_dm_run(&pool, agent_id, message_id).await?;
    daemon.ensure_running()?;

    daemon.interrupt().await?;
    server.interrupt().await?;
    pool.close().await;
    database.drop().await?;
    Ok(())
}

async fn wait_for_agent_active(pool: &sqlx::PgPool, agent_id: Uuid) -> Result<()> {
    tokio::time::timeout(Duration::from_secs(20), async {
        loop {
            let status: String =
                sqlx::query_scalar("SELECT status FROM agents WHERE member_id = $1")
                    .bind(agent_id)
                    .fetch_one(pool)
                    .await?;
            if status == "active" {
                return Ok(());
            }
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
    })
    .await
    .context("real daemon did not provision the Builtin Agent")?
}

async fn assert_single_running_dm_run(
    pool: &sqlx::PgPool,
    agent_id: Uuid,
    message_id: Uuid,
) -> Result<()> {
    let state: (i64, String, String, String, String, String, i64) = sqlx::query_as(
        "SELECT (SELECT count(*) FROM agent_runs WHERE agent_member_id = $1), \
                runs.driver_kind, runs.status, items.kind, items.priority, items.status, \
                (SELECT count(*) FROM agent_run_inbox_items WHERE run_id = runs.id) \
         FROM agent_runs runs \
         JOIN agent_run_inbox_items links ON links.run_id = runs.id \
         JOIN inbox_items items ON items.id = links.inbox_item_id \
         WHERE runs.agent_member_id = $1 AND items.message_id = $2",
    )
    .bind(agent_id)
    .bind(message_id)
    .fetch_one(pool)
    .await?;
    ensure!(
        state
            == (
                1,
                "builtin".to_owned(),
                "running".to_owned(),
                "direct".to_owned(),
                "hard".to_owned(),
                "leased".to_owned(),
                1
            ),
        "unexpected real DM run state: {state:?}"
    );
    Ok(())
}
