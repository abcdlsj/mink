mod support;

use std::{net::SocketAddr, path::PathBuf, sync::Arc, time::Duration};

use anyhow::{Context, Result, ensure};
use axum::{
    Json, Router,
    body::Body,
    extract::State,
    http::{HeaderMap, StatusCode, header},
    response::Response,
    routing::post,
};
use futures_util::StreamExt;
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

const HUMAN_MESSAGE: &str = "Please inspect this DM.";
const CLI_REPLY: &str = "Acknowledged through the real Sumi CLI.";
const DRIVER_STDOUT: &str = "Controlled Driver stdout must not become a Message.";
const MODEL_FINAL_TEXT: &str = "Controlled model final text must not become a Message.";
const PROVIDER_AUTH: &str = "test-only-provider-key";
const RECOVERY_HUMAN_MESSAGE: &str = "Recovery boundary Human Message.";
const RECOVERY_CLI_REPLY: &str = "Recovery boundary CLI reply.";

struct FakeProvider {
    url: Url,
    rollback_observed: mpsc::Receiver<()>,
    completed: mpsc::Receiver<()>,
    state: Arc<FakeProviderState>,
    task: JoinHandle<()>,
}

struct FakeProviderState {
    step: Mutex<usize>,
    rollback_observed: mpsc::Sender<()>,
    retry_allowed: Notify,
    observed_reply: Mutex<Option<ObservedAgentReply>>,
    completed: mpsc::Sender<()>,
}

#[derive(Clone)]
struct ObservedAgentReply {
    id: Uuid,
    channel_id: Uuid,
    address: String,
    author_id: Uuid,
    seq: i64,
}

impl FakeProvider {
    async fn start() -> Result<Self> {
        let listener = TcpListener::bind(("127.0.0.1", 0)).await?;
        let address = listener.local_addr()?;
        let (rollback_observed_tx, rollback_observed) = mpsc::channel(1);
        let (completed_tx, completed) = mpsc::channel(1);
        let state = Arc::new(FakeProviderState {
            step: Mutex::new(0),
            rollback_observed: rollback_observed_tx,
            retry_allowed: Notify::new(),
            observed_reply: Mutex::new(None),
            completed: completed_tx,
        });
        let app = Router::new()
            .route("/chat/completions", post(chat_stream))
            .with_state(state.clone());
        let task = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });
        Ok(Self {
            url: Url::parse(&format!("http://{address}"))?,
            rollback_observed,
            completed,
            state,
            task,
        })
    }

    async fn wait_for_rollback(&mut self) -> Result<()> {
        match tokio::time::timeout(Duration::from_secs(30), self.rollback_observed.recv()).await {
            Ok(Some(())) => Ok(()),
            Ok(None) => anyhow::bail!("fake provider stopped before observing the failed send"),
            Err(_) => anyhow::bail!("Builtin Agent did not observe the forced transaction failure"),
        }
    }

    fn allow_retry(&self) {
        self.state.retry_allowed.notify_one();
    }

    async fn wait_for_completion(&mut self) -> Result<()> {
        match tokio::time::timeout(Duration::from_secs(30), self.completed.recv()).await {
            Ok(Some(())) => Ok(()),
            Ok(None) => {
                anyhow::bail!("fake provider stopped before the Agent CLI sequence completed")
            }
            Err(_) => {
                let step = *self.state.step.lock().await;
                anyhow::bail!("Builtin Agent CLI sequence stopped after provider step {step}")
            }
        }
    }

    async fn observed_reply(&self) -> Result<ObservedAgentReply> {
        self.state
            .observed_reply
            .lock()
            .await
            .clone()
            .context("fake provider did not observe successful Agent reply metadata")
    }
}

impl Drop for FakeProvider {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn chat_stream(
    State(state): State<Arc<FakeProviderState>>,
    headers: HeaderMap,
    Json(request): Json<serde_json::Value>,
) -> Response {
    let authorized = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value == format!("Bearer {PROVIDER_AUTH}"));
    let structurally_valid = request["stream"] == true
        && request["model"] == "sumi-test-model"
        && request["messages"].is_array();
    if !authorized || !structurally_valid {
        return bad_provider_request();
    }

    let mut step = state.step.lock().await;
    let response = match *step {
        0 => tool_call_stream("call-inbox-current", "sumi agent inbox current --json"),
        1 => {
            let Some((_, address)) = claimed_inbox_identity(&request) else {
                return bad_provider_request();
            };
            tool_call_stream(
                "call-channel-read",
                &format!("sumi agent channel read {address} --json"),
            )
        }
        2 => {
            let Some((_, address)) = claimed_inbox_identity(&request) else {
                return bad_provider_request();
            };
            if !latest_tool_result(&request)
                .is_some_and(|result| valid_channel_read(&result, &address))
            {
                return bad_provider_request();
            }
            tool_call_stream(
                "call-driver-stdout",
                &format!("printf '%s' '{DRIVER_STDOUT}'"),
            )
        }
        3 => {
            if latest_tool_content(&request) != Some(DRIVER_STDOUT) {
                return bad_provider_request();
            }
            let Some((inbox_id, address)) = claimed_inbox_identity(&request) else {
                return bad_provider_request();
            };
            tool_call_stream(
                "call-message-send-forced-rollback",
                &format!(
                    "sumi agent message send {address} --body '{CLI_REPLY}' --handle {inbox_id} --json"
                ),
            )
        }
        4 => {
            if !latest_tool_content(&request).is_some_and(valid_failed_message_send) {
                return bad_provider_request();
            }
            let Some((inbox_id, address)) = claimed_inbox_identity(&request) else {
                return bad_provider_request();
            };
            let _ = state.rollback_observed.send(()).await;
            state.retry_allowed.notified().await;
            tool_call_stream(
                "call-message-send-after-rollback",
                &format!(
                    "sumi agent message send {address} --body '{CLI_REPLY}' --handle {inbox_id} --json"
                ),
            )
        }
        5 => {
            let Some((_, address)) = claimed_inbox_identity(&request) else {
                return bad_provider_request();
            };
            let Some(reply) = latest_tool_result(&request)
                .and_then(|result| valid_message_send(&result, &address))
            else {
                return bad_provider_request();
            };
            *state.observed_reply.lock().await = Some(reply);
            let _ = state.completed.send(()).await;
            text_stream(MODEL_FINAL_TEXT)
        }
        _ => return bad_provider_request(),
    };
    *step += 1;
    response
}

fn claimed_inbox_identity(request: &serde_json::Value) -> Option<(Uuid, String)> {
    request["messages"]
        .as_array()?
        .iter()
        .filter_map(tool_result)
        .find_map(|result| {
            let item = result["data"]["items"].as_array()?.first()?;
            let id = Uuid::parse_str(item["id"].as_str()?).ok()?;
            let address = item["address"].as_str()?;
            (item["kind"] == "direct"
                && item["priority"] == "hard"
                && item["status"] == "leased"
                && address.starts_with('@'))
            .then(|| (id, address.to_owned()))
        })
}

fn latest_tool_result(request: &serde_json::Value) -> Option<serde_json::Value> {
    serde_json::from_str(latest_tool_content(request)?).ok()
}

fn latest_tool_content(request: &serde_json::Value) -> Option<&str> {
    request["messages"]
        .as_array()?
        .iter()
        .rev()
        .find(|message| message["role"] == "tool")?["content"]
        .as_str()
}

fn tool_result(message: &serde_json::Value) -> Option<serde_json::Value> {
    if message["role"] != "tool" {
        return None;
    }
    serde_json::from_str(message["content"].as_str()?).ok()
}

fn valid_channel_read(result: &serde_json::Value, expected_address: &str) -> bool {
    result["ok"] == true
        && result["data"]["address"] == expected_address
        && result["data"]["snapshot_channel_seq"].as_i64() == Some(1)
        && result["data"]["messages"]
            .as_array()
            .is_some_and(|messages| messages.len() == 1)
}

fn valid_failed_message_send(content: &str) -> bool {
    content.starts_with("error:") && !content.contains("\"ok\":true")
}

fn valid_message_send(
    result: &serde_json::Value,
    expected_address: &str,
) -> Option<ObservedAgentReply> {
    (result["ok"] == true
        && result["data"]["address"] == expected_address
        && result["data"]["author"]["kind"] == "agent"
        && result["data"]["seq"].as_i64() == Some(2))
    .then(|| {
        Some(ObservedAgentReply {
            id: Uuid::parse_str(result["data"]["id"].as_str()?).ok()?,
            channel_id: Uuid::parse_str(result["data"]["channel_id"].as_str()?).ok()?,
            address: result["data"]["address"].as_str()?.to_owned(),
            author_id: Uuid::parse_str(result["data"]["author"]["id"].as_str()?).ok()?,
            seq: result["data"]["seq"].as_i64()?,
        })
    })
    .flatten()
}

fn tool_call_stream(id: &str, command: &str) -> Response {
    let arguments = serde_json::json!({ "command": command }).to_string();
    let event = serde_json::json!({
        "choices": [{
            "delta": {
                "tool_calls": [{
                    "index": 0,
                    "id": id,
                    "function": { "name": "bash", "arguments": arguments }
                }]
            },
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
        .expect("static fake response")
}

fn bad_provider_request() -> Response {
    Response::builder()
        .status(StatusCode::BAD_REQUEST)
        .body(Body::empty())
        .expect("static fake response")
}

#[tokio::test]
async fn builtin_agent_reads_and_replies_to_dm_through_real_cli() -> Result<()> {
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

    let events_response = client
        .get(server_url.join(&format!("/api/v1/spaces/{}/events", space.id))?)
        .header(header::COOKIE, &cookie)
        .send()
        .await?;
    ensure!(
        events_response.status() == StatusCode::OK,
        "subscribe to Space events: {}",
        events_response.status()
    );
    install_handled_rollback_trigger(&pool).await?;

    let message_response = client
        .post(server_url.join(&format!("/api/v1/channels/{channel_id}/messages"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &cookie)
        .json(&serde_json::json!({
            "body_markdown": HUMAN_MESSAGE,
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

    provider.wait_for_rollback().await?;
    let inbox_item_id =
        assert_send_and_handle_rolled_back(&pool, channel_id, agent_id, message_id).await?;
    remove_handled_rollback_trigger(&pool).await?;
    provider.allow_retry();
    provider.wait_for_completion().await?;
    let reply = provider.observed_reply().await?;
    ensure!(
        reply.channel_id == channel_id
            && reply.author_id == agent_id
            && reply.seq == 2
            && reply.address.starts_with('@')
    );
    assert_completed_dm_reply(&pool, agent_id, message_id, reply.id).await?;
    assert_browser_message_read(&client, &server_url, &cookie, channel_id, &reply).await?;
    assert_reply_sse(events_response, space.id, inbox_item_id, &reply).await?;
    daemon.ensure_running()?;
    assert_sensitive_text_absent_from_logs(&server, &daemon)?;

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

#[derive(sqlx::FromRow)]
struct CompletedDmRun {
    run_count: i64,
    run_id: Uuid,
    driver_kind: String,
    run_status: String,
    inbox_kind: String,
    inbox_priority: String,
    inbox_status: String,
    handled_by_run_id: Option<Uuid>,
    linked_item_count: i64,
    cli_reply_count: i64,
    channel_message_count: i64,
    model_final_message_count: i64,
    driver_stdout_message_count: i64,
}

async fn assert_completed_dm_reply(
    pool: &sqlx::PgPool,
    agent_id: Uuid,
    message_id: Uuid,
    reply_id: Uuid,
) -> Result<()> {
    tokio::time::timeout(Duration::from_secs(20), async {
        loop {
            let state: Option<CompletedDmRun> = sqlx::query_as(
                "SELECT (SELECT count(*) FROM agent_runs WHERE agent_member_id = $1) AS run_count, \
                            runs.id AS run_id, runs.driver_kind, runs.status AS run_status, \
                            items.kind AS inbox_kind, items.priority AS inbox_priority, \
                            items.status AS inbox_status, items.handled_by_run_id, \
                            (SELECT count(*) FROM agent_run_inbox_items WHERE run_id = runs.id) \
                                AS linked_item_count, \
                            (SELECT count(*) FROM messages WHERE id = $3 \
                                AND channel_id = items.channel_id \
                                AND author_member_id = $1 AND channel_seq = 2 \
                                AND body_markdown = $4) \
                                AS cli_reply_count, \
                            (SELECT count(*) FROM messages WHERE channel_id = items.channel_id) \
                                AS channel_message_count, \
                            (SELECT count(*) FROM messages WHERE channel_id = items.channel_id \
                                AND body_markdown = $5) AS model_final_message_count, \
                            (SELECT count(*) FROM messages WHERE channel_id = items.channel_id \
                                AND body_markdown = $6) AS driver_stdout_message_count \
                     FROM agent_runs runs \
                     JOIN agent_run_inbox_items links ON links.run_id = runs.id \
                     JOIN inbox_items items ON items.id = links.inbox_item_id \
                     WHERE runs.agent_member_id = $1 AND items.message_id = $2",
            )
            .bind(agent_id)
            .bind(message_id)
            .bind(reply_id)
            .bind(CLI_REPLY)
            .bind(MODEL_FINAL_TEXT)
            .bind(DRIVER_STDOUT)
            .fetch_optional(pool)
            .await?;
            if let Some(state) = state
                && state.run_count == 1
                && state.driver_kind == "builtin"
                && state.run_status == "completed"
                && state.inbox_kind == "direct"
                && state.inbox_priority == "hard"
                && state.inbox_status == "handled"
                && state.handled_by_run_id == Some(state.run_id)
                && state.linked_item_count == 1
                && state.cli_reply_count == 1
            {
                ensure!(
                    state.model_final_message_count == 0,
                    "model final text was automatically published in PostgreSQL"
                );
                ensure!(
                    state.driver_stdout_message_count == 0,
                    "Driver stdout was automatically published in PostgreSQL"
                );
                ensure!(
                    state.channel_message_count == 2,
                    "the CLI reply was not the only Agent-authored Message in PostgreSQL"
                );
                return Ok(());
            }
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
    })
    .await
    .context("real Builtin run did not publish and handle the DM through the Agent CLI")?
}

async fn install_handled_rollback_trigger(pool: &sqlx::PgPool) -> Result<()> {
    sqlx::query(
        "CREATE FUNCTION test_reject_handled_inbox() RETURNS trigger LANGUAGE plpgsql AS $$ \
         BEGIN IF NEW.status = 'handled' AND OLD.status = 'leased' THEN \
             RAISE EXCEPTION 'test-only forced send-and-handle rollback'; \
         END IF; RETURN NEW; END $$",
    )
    .execute(pool)
    .await?;
    sqlx::query(
        "CREATE CONSTRAINT TRIGGER test_reject_handled_inbox \
         AFTER UPDATE ON inbox_items DEFERRABLE INITIALLY DEFERRED \
         FOR EACH ROW EXECUTE FUNCTION test_reject_handled_inbox()",
    )
    .execute(pool)
    .await?;
    Ok(())
}

async fn remove_handled_rollback_trigger(pool: &sqlx::PgPool) -> Result<()> {
    sqlx::query("DROP TRIGGER test_reject_handled_inbox ON inbox_items")
        .execute(pool)
        .await?;
    sqlx::query("DROP FUNCTION test_reject_handled_inbox()")
        .execute(pool)
        .await?;
    Ok(())
}

#[derive(sqlx::FromRow)]
struct RolledBackSendAndHandle {
    inbox_item_id: Uuid,
    inbox_status: String,
    handled_by_run_id: Option<Uuid>,
    next_seq: i64,
    message_count: i64,
    agent_message_count: i64,
    reply_event_count: i64,
    handled_event_count: i64,
}

async fn assert_send_and_handle_rolled_back(
    pool: &sqlx::PgPool,
    channel_id: Uuid,
    agent_id: Uuid,
    source_message_id: Uuid,
) -> Result<Uuid> {
    let state: RolledBackSendAndHandle = sqlx::query_as(
        "SELECT items.id AS inbox_item_id, items.status AS inbox_status, \
                items.handled_by_run_id, channels.next_seq, \
                (SELECT count(*) FROM messages WHERE channel_id = $1) AS message_count, \
                (SELECT count(*) FROM messages WHERE channel_id = $1 \
                    AND author_member_id = $2) AS agent_message_count, \
                (SELECT count(*) FROM outbox_events WHERE topic = 'message.created' \
                    AND payload_json->>'channel_id' = $1::text \
                    AND payload_json->>'channel_seq' = '2') AS reply_event_count, \
                (SELECT count(*) FROM outbox_events WHERE topic = 'inbox.changed' \
                    AND aggregate_id = items.id) AS handled_event_count \
         FROM inbox_items items JOIN channels ON channels.id = items.channel_id \
         WHERE items.member_id = $2 AND items.message_id = $3",
    )
    .bind(channel_id)
    .bind(agent_id)
    .bind(source_message_id)
    .fetch_one(pool)
    .await?;
    ensure!(
        state.inbox_status == "leased"
            && state.handled_by_run_id.is_none()
            && state.next_seq == 2
            && state.message_count == 1
            && state.agent_message_count == 0
            && state.reply_event_count == 0
            && state.handled_event_count == 0,
        "forced transaction failure did not roll back send-and-handle state"
    );
    Ok(state.inbox_item_id)
}

async fn assert_browser_message_read(
    client: &Client,
    server_url: &Url,
    cookie: &str,
    channel_id: Uuid,
    reply: &ObservedAgentReply,
) -> Result<()> {
    let response = client
        .get(server_url.join(&format!("/api/v1/channels/{channel_id}/messages"))?)
        .header(header::COOKIE, cookie)
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::OK,
        "read DM messages: {}",
        response.status()
    );
    let page: serde_json::Value = response.json().await?;
    let messages = page["messages"]
        .as_array()
        .context("Browser Message page missing messages")?;
    let api_reply = messages
        .iter()
        .find(|message| message["id"].as_str() == Some(reply.id.to_string().as_str()))
        .context("Browser Message page missing Agent reply")?;
    ensure!(
        !messages
            .iter()
            .any(|message| message["body_markdown"] == MODEL_FINAL_TEXT),
        "Browser Message API exposed model final text as a Message"
    );
    ensure!(
        !messages
            .iter()
            .any(|message| message["body_markdown"] == DRIVER_STDOUT),
        "Browser Message API exposed Driver stdout as a Message"
    );
    ensure!(
        messages.len() == 2
            && page["channel_id"] == channel_id.to_string()
            && page["snapshot_channel_seq"].as_i64() == Some(2)
            && api_reply["seq"].as_i64() == Some(reply.seq)
            && api_reply["author"]["id"] == reply.author_id.to_string()
            && api_reply["author"]["kind"] == "agent"
    );
    Ok(())
}

fn assert_sensitive_text_absent_from_logs(
    server: &support::SumiProcess,
    daemon: &support::SumiProcess,
) -> Result<()> {
    for (label, text) in [
        ("Human Message", HUMAN_MESSAGE),
        ("CLI Message", CLI_REPLY),
        ("model final text", MODEL_FINAL_TEXT),
        ("Driver stdout", DRIVER_STDOUT),
        ("provider authentication", PROVIDER_AUTH),
    ] {
        ensure!(!server.logs_contain(text), "Server logs leaked {label}");
        ensure!(!daemon.logs_contain(text), "daemon logs leaked {label}");
    }
    Ok(())
}

async fn assert_reply_sse(
    response: reqwest::Response,
    space_id: Uuid,
    handled_item_id: Uuid,
    reply: &ObservedAgentReply,
) -> Result<()> {
    tokio::time::timeout(Duration::from_secs(10), async move {
        let mut stream = response.bytes_stream();
        let mut buffer = String::new();
        let mut saw_message = false;
        let mut saw_handled = false;
        while let Some(chunk) = stream.next().await {
            buffer.push_str(std::str::from_utf8(&chunk?)?);
            while let Some(end) = buffer.find("\n\n") {
                let frame = buffer.drain(..end + 2).collect::<String>();
                let Some(data) = frame.lines().find_map(|line| line.strip_prefix("data: ")) else {
                    continue;
                };
                let event: serde_json::Value = serde_json::from_str(data)?;
                ensure!(event["space_id"] == space_id.to_string());
                let payload = &event["data"];
                ensure!(payload.get("body_markdown").is_none());
                if event["type"] == "message.created"
                    && payload["message_id"] == reply.id.to_string()
                {
                    ensure!(
                        payload["channel_id"] == reply.channel_id.to_string()
                            && payload["channel_seq"].as_i64() == Some(reply.seq)
                    );
                    saw_message = true;
                }
                if event["type"] == "inbox.changed"
                    && payload["item_id"] == handled_item_id.to_string()
                {
                    ensure!(payload["member_id"] == reply.author_id.to_string());
                    saw_handled = true;
                }
                if saw_message && saw_handled {
                    return Ok::<_, anyhow::Error>(());
                }
            }
        }
        anyhow::bail!("Space SSE ended before Agent reply events arrived")
    })
    .await
    .context("Space SSE did not deliver Agent reply events")??;
    Ok(())
}

#[derive(Clone, Copy)]
enum RecoveryProviderMode {
    FailThenBlock,
    CrashAfterHandle,
    AlwaysFail,
}

#[derive(Debug, Eq, PartialEq)]
enum RecoveryProviderEvent {
    Request(usize),
    ReplyHandled,
}

struct RecoveryProvider {
    url: Url,
    events: mpsc::UnboundedReceiver<RecoveryProviderEvent>,
    task: JoinHandle<()>,
}

struct RecoveryProviderState {
    mode: RecoveryProviderMode,
    step: Mutex<usize>,
    events: mpsc::UnboundedSender<RecoveryProviderEvent>,
    blocked: Notify,
}

impl RecoveryProvider {
    async fn start(mode: RecoveryProviderMode) -> Result<Self> {
        let listener = TcpListener::bind(("127.0.0.1", 0)).await?;
        let address = listener.local_addr()?;
        let (events_tx, events) = mpsc::unbounded_channel();
        let state = Arc::new(RecoveryProviderState {
            mode,
            step: Mutex::new(0),
            events: events_tx,
            blocked: Notify::new(),
        });
        let app = Router::new()
            .route("/chat/completions", post(recovery_chat_stream))
            .with_state(state);
        let task = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });
        Ok(Self {
            url: Url::parse(&format!("http://{address}"))?,
            events,
            task,
        })
    }

    async fn wait_for(&mut self, expected: RecoveryProviderEvent) -> Result<()> {
        let event = tokio::time::timeout(Duration::from_secs(30), self.events.recv())
            .await
            .context("timed out waiting for recovery provider event")?
            .context("recovery provider stopped before expected event")?;
        ensure!(
            event == expected,
            "unexpected recovery provider event: {event:?}"
        );
        Ok(())
    }

    async fn assert_quiet(&mut self, duration: Duration) -> Result<()> {
        ensure!(
            tokio::time::timeout(duration, self.events.recv())
                .await
                .is_err(),
            "recovery provider received an unexpected extra request"
        );
        Ok(())
    }
}

impl Drop for RecoveryProvider {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn recovery_chat_stream(
    State(state): State<Arc<RecoveryProviderState>>,
    headers: HeaderMap,
    Json(request): Json<serde_json::Value>,
) -> Response {
    let authorized = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value == format!("Bearer {PROVIDER_AUTH}"));
    if !authorized || request["stream"] != true || request["messages"].as_array().is_none() {
        return bad_provider_request();
    }
    let mut step = state.step.lock().await;
    let request_number = *step + 1;
    let _ = state
        .events
        .send(RecoveryProviderEvent::Request(request_number));
    let response = match state.mode {
        RecoveryProviderMode::AlwaysFail => provider_failure(),
        RecoveryProviderMode::FailThenBlock if *step == 0 => provider_failure(),
        RecoveryProviderMode::FailThenBlock => {
            state.blocked.notified().await;
            provider_failure()
        }
        RecoveryProviderMode::CrashAfterHandle => match *step {
            0 => tool_call_stream("recovery-inbox-current", "sumi agent inbox current --json"),
            1 => {
                let Some((inbox_id, address)) = claimed_inbox_identity(&request) else {
                    return bad_provider_request();
                };
                tool_call_stream(
                    "recovery-message-send",
                    &format!(
                        "sumi agent message send {address} --body '{RECOVERY_CLI_REPLY}' --handle {inbox_id} --json"
                    ),
                )
            }
            2 => {
                let Some((_, address)) = claimed_inbox_identity(&request) else {
                    return bad_provider_request();
                };
                if latest_tool_result(&request)
                    .and_then(|result| valid_message_send(&result, &address))
                    .is_none()
                {
                    return bad_provider_request();
                }
                let _ = state.events.send(RecoveryProviderEvent::ReplyHandled);
                state.blocked.notified().await;
                text_stream("must not complete before daemon crash")
            }
            _ => return bad_provider_request(),
        },
    };
    *step += 1;
    response
}

fn provider_failure() -> Response {
    Response::builder()
        .status(StatusCode::INTERNAL_SERVER_ERROR)
        .body(Body::empty())
        .expect("static fake failure response")
}

struct RecoveryHarness {
    _root: tempfile::TempDir,
    database: TestDatabase,
    computer_config: PathBuf,
    server: support::SumiProcess,
    daemon: support::SumiProcess,
    pool: sqlx::PgPool,
    owner_id: Uuid,
    admin_id: Option<Uuid>,
    agent_id: Uuid,
    channel_id: Uuid,
    message_id: Uuid,
}

#[derive(Debug, sqlx::FromRow)]
struct SystemNotification {
    member_id: Uuid,
    channel_id: Option<Uuid>,
    thread_id: Option<i64>,
    message_id: Option<Uuid>,
    last_error: String,
    message_count: i32,
}

async fn start_recovery_harness(
    provider_url: &Url,
    max_retry_count: u8,
    create_admin: bool,
) -> Result<RecoveryHarness> {
    let root = tempfile::tempdir()?;
    let database = TestDatabase::create("sumi_agent_dm_recovery_test").await?;
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
    let server = spawn_server(&server_config)?;
    wait_for_health(&server_url).await?;

    let computer_state = root.path().join("computer");
    let computer_config = root.path().join("computer.toml");
    write_builtin_computer_config(&computer_config, &server_url, &computer_state, provider_url)?;
    let client = Client::new();
    let cookie = register_human(&client, &server_url).await?;
    let space = create_space(&client, &server_url, &cookie).await?;
    let mut daemon = spawn_computer(&computer_config)?;
    let pairing_url = pairing_url_from_daemon(&mut daemon).await?;
    let paired = confirm_pairing(&client, &server_url, &cookie, space.id, &pairing_url).await?;
    wait_for_computer_status(&client, &server_url, &cookie, space.id, "online").await?;

    let agent_response = client
        .post(server_url.join(&format!("/api/v1/spaces/{}/agents", space.id))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &cookie)
        .json(&serde_json::json!({
            "computer_id": paired.id,
            "name": "Recovery Lin",
            "handle": "recovery-lin",
            "role_text": "Exercise the current DM recovery boundary through the Sumi Agent CLI.",
            "access_level": "member",
            "driver_kind": "builtin"
        }))
        .send()
        .await?;
    ensure!(agent_response.status() == StatusCode::CREATED);
    let agent: serde_json::Value = agent_response.json().await?;
    let agent_id = Uuid::parse_str(agent["member_id"].as_str().context("Agent id missing")?)?;
    let pool = PgPoolOptions::new()
        .max_connections(3)
        .connect(&database.url)
        .await?;
    let owner_id: Uuid = sqlx::query_scalar("SELECT owner_member_id FROM spaces WHERE id = $1")
        .bind(space.id)
        .fetch_one(&pool)
        .await?;
    wait_for_agent_active(&pool, agent_id).await?;
    sqlx::query(
        "UPDATE agents SET attention_config_json = jsonb_set(\
         attention_config_json, '{max_retry_count}', to_jsonb($2::int)) WHERE member_id = $1",
    )
    .bind(agent_id)
    .bind(i32::from(max_retry_count))
    .execute(&pool)
    .await?;

    let admin_id = if create_admin {
        let user_id = Uuid::now_v7();
        let member_id = Uuid::now_v7();
        let now = time::OffsetDateTime::now_utc();
        let mut transaction = pool.begin().await?;
        sqlx::query(
            "INSERT INTO users (id, email_normalized, password_hash, display_name, created_at) \
             VALUES ($1, $2, 'test-only-password-hash', 'Recovery Admin', $3)",
        )
        .bind(user_id)
        .bind(format!("recovery-admin-{user_id}@example.test"))
        .bind(now)
        .execute(&mut *transaction)
        .await?;
        sqlx::query(
            "INSERT INTO members (id, space_id, kind, display_name, handle, avatar_seed, \
             access_level, created_at) VALUES ($1, $2, 'human', 'Recovery Admin', \
             'recovery-admin', $3, 'admin', $4)",
        )
        .bind(member_id)
        .bind(space.id)
        .bind(member_id.to_string())
        .bind(now)
        .execute(&mut *transaction)
        .await?;
        sqlx::query("INSERT INTO human_members (member_id, space_id, user_id) VALUES ($1, $2, $3)")
            .bind(member_id)
            .bind(space.id)
            .bind(user_id)
            .execute(&mut *transaction)
            .await?;
        transaction.commit().await?;
        Some(member_id)
    } else {
        None
    };

    let dm_response = client
        .post(server_url.join(&format!("/api/v1/spaces/{}/dms", space.id))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &cookie)
        .json(&serde_json::json!({ "member_id": agent_id }))
        .send()
        .await?;
    ensure!(dm_response.status() == StatusCode::CREATED);
    let dm: serde_json::Value = dm_response.json().await?;
    let channel_id = Uuid::parse_str(dm["channel_id"].as_str().context("DM id missing")?)?;
    let message_response = client
        .post(server_url.join(&format!("/api/v1/channels/{channel_id}/messages"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &cookie)
        .json(&serde_json::json!({
            "body_markdown": RECOVERY_HUMAN_MESSAGE,
            "mentions": [],
            "attachment_ids": []
        }))
        .send()
        .await?;
    ensure!(message_response.status() == StatusCode::CREATED);
    let message: serde_json::Value = message_response.json().await?;
    let message_id = Uuid::parse_str(message["id"].as_str().context("Message id missing")?)?;

    Ok(RecoveryHarness {
        _root: root,
        database,
        computer_config,
        server,
        daemon,
        pool,
        owner_id,
        admin_id,
        agent_id,
        channel_id,
        message_id,
    })
}

async fn stop_recovery_harness(mut harness: RecoveryHarness) -> Result<()> {
    harness.daemon.interrupt().await?;
    harness.server.interrupt().await?;
    harness.pool.close().await;
    harness.database.drop().await
}

fn assert_recovery_logs_redacted(
    server: &support::SumiProcess,
    daemon: &support::SumiProcess,
) -> Result<()> {
    for (label, text) in [
        ("recovery Human Message", RECOVERY_HUMAN_MESSAGE),
        ("recovery CLI Message", RECOVERY_CLI_REPLY),
        ("provider authentication", PROVIDER_AUTH),
    ] {
        ensure!(!server.logs_contain(text), "Server logs leaked {label}");
        ensure!(!daemon.logs_contain(text), "daemon logs leaked {label}");
    }
    Ok(())
}

#[tokio::test]
async fn driver_failure_releases_and_reclaims_dm_inbox_once() -> Result<()> {
    let mut provider = RecoveryProvider::start(RecoveryProviderMode::FailThenBlock).await?;
    let mut harness = start_recovery_harness(&provider.url, 3, false).await?;
    provider.wait_for(RecoveryProviderEvent::Request(1)).await?;
    tokio::time::timeout(Duration::from_secs(20), async {
        loop {
            let state: (String, i32, Option<Uuid>, i64) = sqlx::query_as(
                "SELECT items.status, items.retry_count, items.lease_id, \
                 (SELECT count(*) FROM agent_runs WHERE agent_member_id = $1) \
                 FROM inbox_items items WHERE items.member_id = $1 AND items.message_id = $2",
            )
            .bind(harness.agent_id)
            .bind(harness.message_id)
            .fetch_one(&harness.pool)
            .await?;
            if state == ("pending".to_owned(), 1, None, 1) {
                return Ok::<_, anyhow::Error>(());
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await
    .context("failed Driver run did not release its Inbox Item exactly once")??;
    provider.wait_for(RecoveryProviderEvent::Request(2)).await?;
    let reclaimed: (String, i32, i64, i64) = sqlx::query_as(
        "SELECT items.status, items.retry_count, \
         (SELECT count(DISTINCT links.run_id) FROM agent_run_inbox_items links \
            WHERE links.inbox_item_id = items.id), \
         (SELECT count(*) FROM agent_runs WHERE agent_member_id = $1 \
            AND status IN ('queued', 'running')) \
         FROM inbox_items items WHERE items.member_id = $1 AND items.message_id = $2",
    )
    .bind(harness.agent_id)
    .bind(harness.message_id)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(
        reclaimed == ("leased".to_owned(), 1, 2, 1),
        "released Inbox Item was not reclaimed by one later real run"
    );
    assert_recovery_logs_redacted(&harness.server, &harness.daemon)?;
    harness.daemon.crash().await?;
    harness.server.interrupt().await?;
    harness.pool.close().await;
    harness.database.drop().await
}

#[tokio::test]
async fn daemon_crash_after_send_and_handle_does_not_repeat_dm_reply() -> Result<()> {
    let mut provider = RecoveryProvider::start(RecoveryProviderMode::CrashAfterHandle).await?;
    let mut harness = start_recovery_harness(&provider.url, 3, false).await?;
    provider.wait_for(RecoveryProviderEvent::Request(1)).await?;
    provider.wait_for(RecoveryProviderEvent::Request(2)).await?;
    provider.wait_for(RecoveryProviderEvent::Request(3)).await?;
    provider
        .wait_for(RecoveryProviderEvent::ReplyHandled)
        .await?;
    let committed: (String, i32, i64) = sqlx::query_as(
        "SELECT status, retry_count, \
         (SELECT count(*) FROM messages WHERE channel_id = $2 AND author_member_id = $1 \
            AND body_markdown = $3) FROM inbox_items WHERE member_id = $1 AND message_id = $4",
    )
    .bind(harness.agent_id)
    .bind(harness.channel_id)
    .bind(RECOVERY_CLI_REPLY)
    .bind(harness.message_id)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(committed == ("handled".to_owned(), 0, 1));
    assert_recovery_logs_redacted(&harness.server, &harness.daemon)?;
    harness.daemon.crash().await?;
    harness.daemon = spawn_computer(&harness.computer_config)?;
    tokio::time::timeout(Duration::from_secs(20), async {
        loop {
            let state: (String, Option<String>, String, i32, i64, i64) = sqlx::query_as(
                "SELECT runs.status, runs.error_code, items.status, items.retry_count, \
                 (SELECT count(*) FROM agent_runs WHERE agent_member_id = $1), \
                 (SELECT count(*) FROM messages WHERE channel_id = $2 AND author_member_id = $1) \
                 FROM agent_runs runs JOIN agent_run_inbox_items links ON links.run_id = runs.id \
                 JOIN inbox_items items ON items.id = links.inbox_item_id \
                 WHERE runs.agent_member_id = $1 AND items.message_id = $3",
            )
            .bind(harness.agent_id)
            .bind(harness.channel_id)
            .bind(harness.message_id)
            .fetch_one(&harness.pool)
            .await?;
            if state
                == (
                    "failed".to_owned(),
                    Some("process_lost".to_owned()),
                    "handled".to_owned(),
                    0,
                    1,
                    1,
                )
            {
                return Ok::<_, anyhow::Error>(());
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await
    .context("restarted daemon did not recover handled run without duplicating the reply")??;
    provider.assert_quiet(Duration::from_secs(2)).await?;
    harness.daemon.ensure_running()?;
    assert_recovery_logs_redacted(&harness.server, &harness.daemon)?;
    stop_recovery_harness(harness).await
}

#[tokio::test]
async fn repeated_driver_failure_marks_dm_dead_and_notifies_human_governors() -> Result<()> {
    let mut provider = RecoveryProvider::start(RecoveryProviderMode::AlwaysFail).await?;
    let harness = start_recovery_harness(&provider.url, 2, true).await?;
    provider.wait_for(RecoveryProviderEvent::Request(1)).await?;
    provider.wait_for(RecoveryProviderEvent::Request(2)).await?;
    let admin_id = harness.admin_id.context("recovery Admin missing")?;
    tokio::time::timeout(Duration::from_secs(20), async {
        loop {
            let state: (String, i32, i64, i64) = sqlx::query_as(
                "SELECT status, retry_count, \
                 (SELECT count(*) FROM agent_runs WHERE agent_member_id = $1), \
                 (SELECT count(*) FROM inbox_items WHERE member_id IN ($3, $4) \
                    AND kind = 'system' AND priority = 'hard') \
                 FROM inbox_items WHERE member_id = $1 AND message_id = $2",
            )
            .bind(harness.agent_id)
            .bind(harness.message_id)
            .bind(harness.owner_id)
            .bind(admin_id)
            .fetch_one(&harness.pool)
            .await?;
            if state == ("dead".to_owned(), 2, 2, 2) {
                return Ok::<_, anyhow::Error>(());
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await
    .context("retry limit did not atomically dead-letter and notify Human governors")??;
    let notifications: Vec<SystemNotification> = sqlx::query_as(
        "SELECT member_id, channel_id, thread_id, message_id, last_error, message_count \
             FROM inbox_items WHERE member_id IN ($1, $2) AND kind = 'system' \
             AND priority = 'hard' ORDER BY member_id",
    )
    .bind(harness.owner_id)
    .bind(admin_id)
    .fetch_all(&harness.pool)
    .await?;
    ensure!(notifications.len() == 2);
    ensure!(
        notifications.iter().all(|notification| {
            [harness.owner_id, admin_id].contains(&notification.member_id)
                && notification.channel_id.is_none()
                && notification.thread_id.is_none()
                && notification.message_id.is_none()
                && notification.last_error == "driver_failed"
                && notification.message_count == 1
        }),
        "system Inbox notification exposed private Message coordinates or incorrect retry data: {notifications:?}"
    );
    let notification_outbox_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM outbox_events WHERE topic = 'inbox.changed' \
         AND aggregate_id IN (SELECT id FROM inbox_items WHERE member_id IN ($1, $2) \
           AND kind = 'system' AND priority = 'hard')",
    )
    .bind(harness.owner_id)
    .bind(admin_id)
    .fetch_one(&harness.pool)
    .await?;
    let agent_message_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM messages WHERE channel_id = $1 AND author_member_id = $2",
    )
    .bind(harness.channel_id)
    .bind(harness.agent_id)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(notification_outbox_count == 2 && agent_message_count == 0);
    provider.assert_quiet(Duration::from_secs(2)).await?;
    assert_recovery_logs_redacted(&harness.server, &harness.daemon)?;
    stop_recovery_harness(harness).await
}
