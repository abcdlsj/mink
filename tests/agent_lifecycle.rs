mod support;

use std::{net::SocketAddr, path::Path, sync::Arc, time::Duration};

use anyhow::{Context, Result, ensure};
use axum::{
    Json, Router,
    body::Body,
    extract::State,
    http::{HeaderMap, StatusCode, header},
    response::Response,
    routing::post,
};
use reqwest::Client;
use sqlx::postgres::PgPoolOptions;
use support::{
    TestDatabase, confirm_pairing, create_space, pairing_url_from_daemon, register_human,
    reserve_local_port, spawn_computer, spawn_server, wait_for_computer_status, wait_for_health,
    write_builtin_computer_config, write_server_config,
};
use tokio::{
    net::TcpListener,
    sync::{Mutex, Notify, mpsc},
    task::JoinHandle,
};
use url::Url;
use uuid::Uuid;

const PROVIDER_AUTH: &str = "test-only-provider-key";

struct LifecycleProvider {
    url: Url,
    events: mpsc::UnboundedReceiver<&'static str>,
    continue_current: Arc<Notify>,
    task: JoinHandle<()>,
}

struct LifecycleProviderState {
    step: Mutex<usize>,
    events: mpsc::UnboundedSender<&'static str>,
    continue_current: Arc<Notify>,
}

impl LifecycleProvider {
    async fn start() -> Result<Self> {
        let listener = TcpListener::bind(("127.0.0.1", 0)).await?;
        let address = listener.local_addr()?;
        let (events_tx, events) = mpsc::unbounded_channel();
        let continue_current = Arc::new(Notify::new());
        let state = Arc::new(LifecycleProviderState {
            step: Mutex::new(0),
            events: events_tx,
            continue_current: continue_current.clone(),
        });
        let app = Router::new()
            .route("/chat/completions", post(lifecycle_chat_stream))
            .with_state(state);
        let task = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });
        Ok(Self {
            url: Url::parse(&format!("http://{address}"))?,
            events,
            continue_current,
            task,
        })
    }

    async fn wait_for(&mut self, expected: &'static str) -> Result<()> {
        let event = tokio::time::timeout(Duration::from_secs(30), self.events.recv())
            .await
            .with_context(|| format!("timed out waiting for lifecycle provider event {expected}"))?
            .context("lifecycle provider stopped")?;
        ensure!(
            event == expected,
            "expected provider event {expected}, got {event}"
        );
        Ok(())
    }
}

impl Drop for LifecycleProvider {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn lifecycle_chat_stream(
    State(state): State<Arc<LifecycleProviderState>>,
    headers: HeaderMap,
    Json(request): Json<serde_json::Value>,
) -> Response {
    let authorized = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value == format!("Bearer {PROVIDER_AUTH}"));
    if !authorized || request["stream"] != true || !request["messages"].is_array() {
        return bad_provider_request();
    }
    let mut step = state.step.lock().await;
    let response = match *step {
        0 => {
            let _ = state.events.send("stop_after_current_started");
            state.continue_current.notified().await;
            tool_call_stream("current-inbox", "sumi agent inbox current --json")
        }
        1 => match claimed_inbox_id(&request) {
            Some(id) => tool_call_stream(
                "current-ack",
                &format!("sumi agent inbox ack {id} --reason 'completed before suspension' --json"),
            ),
            None => return bad_provider_request(),
        },
        2 => {
            if !ack_succeeded(&request) {
                return bad_provider_request();
            }
            let _ = state.events.send("stop_after_current_completed");
            text_stream("Current run completed before suspension.")
        }
        3 => tool_call_stream(
            "cancel-shell",
            "printf started > lifecycle-cancel-started; sleep 60",
        ),
        4 => tool_call_stream("retry-inbox", "sumi agent inbox current --json"),
        5 => match claimed_inbox_id(&request) {
            Some(id) => tool_call_stream(
                "retry-ack",
                &format!("sumi agent inbox ack {id} --reason 'handled after resume' --json"),
            ),
            None => return bad_provider_request(),
        },
        6 => {
            if !ack_succeeded(&request) {
                return bad_provider_request();
            }
            let _ = state.events.send("retry_completed");
            text_stream("Canceled work was reclaimed after resume.")
        }
        _ => return bad_provider_request(),
    };
    *step += 1;
    response
}

fn claimed_inbox_id(request: &serde_json::Value) -> Option<Uuid> {
    request["messages"]
        .as_array()?
        .iter()
        .filter(|message| message["role"] == "tool")
        .filter_map(|message| {
            serde_json::from_str::<serde_json::Value>(message["content"].as_str()?).ok()
        })
        .find_map(|result| {
            let item = result["data"]["items"].as_array()?.first()?;
            (item["status"] == "leased")
                .then(|| Uuid::parse_str(item["id"].as_str()?).ok())
                .flatten()
        })
}

fn ack_succeeded(request: &serde_json::Value) -> bool {
    request["messages"].as_array().is_some_and(|messages| {
        messages.iter().rev().any(|message| {
            message["role"] == "tool"
                && message["content"]
                    .as_str()
                    .and_then(|content| serde_json::from_str::<serde_json::Value>(content).ok())
                    .is_some_and(|result| result["ok"] == true)
        })
    })
}

fn tool_call_stream(id: &str, command: &str) -> Response {
    let arguments = serde_json::json!({ "command": command }).to_string();
    let event = serde_json::json!({
        "choices": [{
            "delta": { "tool_calls": [{
                "index": 0,
                "id": id,
                "function": { "name": "bash", "arguments": arguments }
            }]},
            "finish_reason": null
        }]
    });
    let finished = serde_json::json!({
        "choices": [{ "delta": {}, "finish_reason": "tool_calls" }]
    });
    sse_response(format!(
        "data: {event}\n\ndata: {finished}\n\ndata: [DONE]\n\n"
    ))
}

fn text_stream(text: &str) -> Response {
    let event = serde_json::json!({
        "choices": [{ "delta": { "content": text }, "finish_reason": null }]
    });
    let finished = serde_json::json!({
        "choices": [{ "delta": {}, "finish_reason": "stop" }]
    });
    sse_response(format!(
        "data: {event}\n\ndata: {finished}\n\ndata: [DONE]\n\n"
    ))
}

fn sse_response(body: String) -> Response {
    Response::builder()
        .header(header::CONTENT_TYPE, "text/event-stream")
        .body(Body::from(body))
        .expect("static fake provider response")
}

fn bad_provider_request() -> Response {
    Response::builder()
        .status(StatusCode::BAD_REQUEST)
        .body(Body::empty())
        .expect("static fake provider response")
}

#[tokio::test]
async fn agent_lifecycle_preserves_identity_and_distinguishes_suspend_modes() -> Result<()> {
    let mut provider = LifecycleProvider::start().await?;
    let root = tempfile::tempdir()?;
    let database = TestDatabase::create("sumi_agent_lifecycle").await?;
    let result = run_lifecycle_flow(&mut provider, root.path(), &database).await;
    database.drop().await?;
    result
}

async fn run_lifecycle_flow(
    provider: &mut LifecycleProvider,
    root: &Path,
    database: &TestDatabase,
) -> Result<()> {
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
    let computer = confirm_pairing(&client, &server_url, &cookie, space.id, &pairing_url).await?;
    wait_for_computer_status(&client, &server_url, &cookie, space.id, "online").await?;

    let created = client
        .post(server_url.join(&format!("/api/v1/spaces/{}/agents", space.id))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &cookie)
        .json(&serde_json::json!({
            "computer_id": computer.id,
            "name": "Lifecycle Agent",
            "handle": "lifecycle-agent",
            "role_text": "Exercise the Agent lifecycle through the Sumi CLI.",
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

    let dm = client
        .post(server_url.join(&format!("/api/v1/spaces/{}/dms", space.id))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &cookie)
        .json(&serde_json::json!({ "member_id": agent_id }))
        .send()
        .await?;
    ensure!(dm.status() == StatusCode::CREATED);
    let dm: serde_json::Value = dm.json().await?;
    let channel_id = Uuid::parse_str(dm["channel_id"].as_str().context("DM id missing")?)?;

    send_dm(
        &client,
        &server_url,
        &cookie,
        channel_id,
        "Finish this run before suspending.",
    )
    .await?;
    provider.wait_for("stop_after_current_started").await?;
    let first_run = wait_for_running_run(&pool, agent_id).await?;
    let suspend_key = Uuid::now_v7();
    let suspended = update_agent(
        &client,
        &server_url,
        &cookie,
        agent_id,
        suspend_key,
        serde_json::json!({ "lifecycle": { "action": "suspend", "mode": "stop_after_current" } }),
    )
    .await?;
    ensure!(
        suspended["desired_lifecycle"] == "suspended"
            && suspended["provision_status"] == "ready"
            && suspended["activity_status"] == "running"
    );
    let duplicate = update_agent(
        &client,
        &server_url,
        &cookie,
        agent_id,
        suspend_key,
        serde_json::json!({ "lifecycle": { "action": "suspend", "mode": "stop_after_current" } }),
    )
    .await?;
    ensure!(duplicate["member_id"] == agent_id.to_string());
    tokio::time::sleep(Duration::from_millis(250)).await;
    let still_running: String = sqlx::query_scalar("SELECT status FROM agent_runs WHERE id = $1")
        .bind(first_run)
        .fetch_one(&pool)
        .await?;
    ensure!(still_running == "running");
    provider.continue_current.notify_one();
    provider.wait_for("stop_after_current_completed").await?;
    wait_for_run_status(&pool, first_run, "completed").await?;
    assert_profile_status(&computer_state, agent_id, "suspended").await?;

    let resumed = update_agent(
        &client,
        &server_url,
        &cookie,
        agent_id,
        Uuid::now_v7(),
        serde_json::json!({ "lifecycle": { "action": "resume" } }),
    )
    .await?;
    ensure!(
        resumed["member_id"] == agent_id.to_string() && resumed["desired_lifecycle"] == "active"
    );
    wait_for_agent_status(&pool, agent_id, "active").await?;

    send_dm(
        &client,
        &server_url,
        &cookie,
        channel_id,
        "Cancel this run immediately.",
    )
    .await?;
    let second_run = wait_for_new_running_run(&pool, agent_id, first_run).await?;
    wait_for_file(
        &computer_state
            .join("agents")
            .join(agent_id.to_string())
            .join("workspace/lifecycle-cancel-started"),
    )
    .await?;
    let canceled = update_agent(
        &client,
        &server_url,
        &cookie,
        agent_id,
        Uuid::now_v7(),
        serde_json::json!({ "lifecycle": { "action": "suspend", "mode": "cancel_now" } }),
    )
    .await?;
    ensure!(canceled["desired_lifecycle"] == "suspended");
    wait_for_run_status(&pool, second_run, "canceled").await?;
    let retried_item: (String, i32) = sqlx::query_as(
        "SELECT status, retry_count FROM inbox_items WHERE member_id = $1 AND message_id = \
         (SELECT id FROM messages WHERE channel_id = $2 AND channel_seq = 2)",
    )
    .bind(agent_id)
    .bind(channel_id)
    .fetch_one(&pool)
    .await?;
    ensure!(retried_item == ("pending".to_owned(), 1));

    update_agent(
        &client,
        &server_url,
        &cookie,
        agent_id,
        Uuid::now_v7(),
        serde_json::json!({ "lifecycle": { "action": "resume" } }),
    )
    .await?;
    provider.wait_for("retry_completed").await?;
    wait_for_agent_status(&pool, agent_id, "active").await?;

    let retired = update_agent(
        &client,
        &server_url,
        &cookie,
        agent_id,
        Uuid::now_v7(),
        serde_json::json!({ "lifecycle": { "action": "retire" } }),
    )
    .await?;
    ensure!(
        retired["member_id"] == agent_id.to_string() && retired["desired_lifecycle"] == "retired"
    );
    wait_for_agent_status(&pool, agent_id, "retired").await?;
    assert_profile_status(&computer_state, agent_id, "retired").await?;

    let facts: (i64, i64, i64, i64, i64) = sqlx::query_as(
        "SELECT \
           (SELECT count(*) FROM members WHERE id = $1 AND retired_at IS NOT NULL), \
           (SELECT count(*) FROM messages WHERE channel_id = $2), \
           (SELECT count(*) FROM computer_commands WHERE payload_json->>'agent_id' = $3 \
             AND kind IN ('agent.provision', 'agent.suspend', 'agent.resume', 'agent.retire')), \
           (SELECT count(*) FROM audit_events WHERE subject_id = $1 AND action IN \
             ('agent.suspended', 'agent.resumed', 'agent.retired')), \
           (SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND topic = 'agent.status_changed')",
    )
    .bind(agent_id)
    .bind(channel_id)
    .bind(agent_id.to_string())
    .fetch_one(&pool)
    .await?;
    ensure!(facts.0 == 1 && facts.1 == 2);
    ensure!(
        facts.2 == 6,
        "expected provision plus five lifecycle commands"
    );
    ensure!(
        facts.3 == 5,
        "expected two suspend, two resume, and one retire audit event"
    );
    ensure!(
        facts.4 >= 6,
        "expected persisted Agent status outbox events"
    );
    ensure!(
        computer_state
            .join("agents")
            .join(agent_id.to_string())
            .is_dir()
    );
    for secret in [
        "Finish this run before suspending.",
        "Cancel this run immediately.",
        PROVIDER_AUTH,
    ] {
        ensure!(!server.logs_contain(secret));
        ensure!(!daemon.logs_contain(secret));
    }

    daemon.interrupt().await?;
    server.interrupt().await?;
    pool.close().await;
    Ok(())
}

async fn update_agent(
    client: &Client,
    server: &Url,
    cookie: &str,
    agent_id: Uuid,
    key: Uuid,
    body: serde_json::Value,
) -> Result<serde_json::Value> {
    let response = client
        .patch(server.join(&format!("/api/v1/agents/{agent_id}"))?)
        .header("idempotency-key", key.to_string())
        .header(header::COOKIE, cookie)
        .json(&body)
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::OK,
        "update Agent: {}",
        response.status()
    );
    Ok(response.json().await?)
}

async fn send_dm(
    client: &Client,
    server: &Url,
    cookie: &str,
    channel_id: Uuid,
    body: &str,
) -> Result<()> {
    let response = client
        .post(server.join(&format!("/api/v1/channels/{channel_id}/messages"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({
            "body_markdown": body,
            "mentions": [],
            "attachment_ids": []
        }))
        .send()
        .await?;
    ensure!(response.status() == StatusCode::CREATED);
    Ok(())
}

async fn wait_for_agent_status(pool: &sqlx::PgPool, agent_id: Uuid, expected: &str) -> Result<()> {
    tokio::time::timeout(Duration::from_secs(20), async {
        loop {
            let status: Option<String> =
                sqlx::query_scalar("SELECT desired_lifecycle FROM agents WHERE member_id = $1")
                    .bind(agent_id)
                    .fetch_optional(pool)
                    .await?;
            if status.as_deref() == Some(expected) {
                return Ok::<_, sqlx::Error>(());
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await
    .with_context(|| format!("Agent did not become {expected}"))??;
    Ok(())
}

async fn wait_for_running_run(pool: &sqlx::PgPool, agent_id: Uuid) -> Result<Uuid> {
    wait_for_new_running_run(pool, agent_id, Uuid::nil()).await
}

async fn wait_for_new_running_run(
    pool: &sqlx::PgPool,
    agent_id: Uuid,
    previous: Uuid,
) -> Result<Uuid> {
    Ok(tokio::time::timeout(Duration::from_secs(20), async {
        loop {
            let run: Option<Uuid> = sqlx::query_scalar(
                "SELECT id FROM agent_runs WHERE agent_member_id = $1 AND id != $2 \
                 AND status = 'running' ORDER BY created_at DESC LIMIT 1",
            )
            .bind(agent_id)
            .bind(previous)
            .fetch_optional(pool)
            .await?;
            if let Some(run) = run {
                return Ok::<_, sqlx::Error>(run);
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await
    .context("Agent run did not become running")??)
}

async fn wait_for_run_status(pool: &sqlx::PgPool, run_id: Uuid, expected: &str) -> Result<()> {
    tokio::time::timeout(Duration::from_secs(20), async {
        loop {
            let status: String = sqlx::query_scalar("SELECT status FROM agent_runs WHERE id = $1")
                .bind(run_id)
                .fetch_one(pool)
                .await?;
            if status == expected {
                return Ok::<_, sqlx::Error>(());
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await
    .with_context(|| format!("Agent run did not become {expected}"))??;
    Ok(())
}

async fn wait_for_file(path: &Path) -> Result<()> {
    tokio::time::timeout(Duration::from_secs(20), async {
        while !path.is_file() {
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await
    .context("Builtin shell tool did not start")?;
    Ok(())
}

async fn assert_profile_status(state: &Path, agent_id: Uuid, expected: &str) -> Result<()> {
    let path = state
        .join("agents")
        .join(agent_id.to_string())
        .join("profile.json");
    tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            if let Ok(bytes) = tokio::fs::read(&path).await
                && let Ok(profile) = serde_json::from_slice::<serde_json::Value>(&bytes)
                && profile["desired_lifecycle"] == expected
            {
                return;
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await
    .with_context(|| format!("local Agent profile did not become {expected}"))?;
    Ok(())
}
