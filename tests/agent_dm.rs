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
use sha2::{Digest, Sha256};
use sqlx::postgres::PgPoolOptions;
use support::{
    SpaceResponse, SumiProcess, TestDatabase, confirm_pairing, create_space,
    pairing_url_from_daemon, register_human, reserve_local_port, spawn_computer, spawn_server,
    wait_for_computer_status, wait_for_computer_status_for, wait_for_health,
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
const CHANNEL_REPLY_MENTION: &str = "@lin please reply through the CLI.";
const CHANNEL_ACK_MENTION: &str = "@lin acknowledge this without replying.";
const CHANNEL_DEFER_MENTION: &str = "@lin defer this mention for later.";
const CHANNEL_CLI_REPLY: &str = "Channel mention replied through the real Sumi CLI.";
const CHANNEL_DEFER_UNTIL: &str = "2099-01-01T00:00:00Z";
const CHANNEL_AMBIENT_MESSAGES: [&str; 5] = [
    "Ambient note one.",
    "Ambient note two.",
    "Ambient note three.",
    "Ambient note four.",
    "Ambient note five.",
];
const THREAD_BACKGROUND: &str = "Channel background before the Thread root.";
const THREAD_ROOT: &str = "Thread root on the Channel timeline.";
const THREAD_FIRST_REPLY: &str = "First Human reply in the Thread.";
const THREAD_MENTION: &str = "@lin inspect this Thread and reply through the CLI.";
const THREAD_FRESHNESS_REPLY: &str = "Human reply added after the Agent read the Thread.";
const THREAD_STALE_REPLY: &str = "This stale Thread reply must not be stored.";
const THREAD_CLI_REPLY: &str = "Thread reply published through the real Sumi CLI.";
const PRIVATE_CHANNEL_BODY: &str = "Private roadmap content must remain inaccessible.";
const PERMISSION_BOUNDARY_MENTION: &str =
    "@lin verify the current authorization boundary and acknowledge it.";

struct AgentProcessHarness {
    _root: tempfile::TempDir,
    database: TestDatabase,
    server_url: Url,
    server: SumiProcess,
    daemon: SumiProcess,
    client: Client,
    cookie: String,
    space: SpaceResponse,
    computer_id: Uuid,
    computer_config: PathBuf,
    computer_state: PathBuf,
    agent_id: Uuid,
    pool: sqlx::PgPool,
}

impl AgentProcessHarness {
    async fn start(provider_url: &Url, database_prefix: &str, role_text: &str) -> Result<Self> {
        let root = tempfile::tempdir()?;
        let database = TestDatabase::create(database_prefix).await?;
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
        write_builtin_computer_config(
            &computer_config,
            &server_url,
            &computer_state,
            provider_url,
        )?;
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
                "name": "Lin",
                "handle": "lin",
                "role_text": role_text,
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
        wait_for_agent_active(&pool, agent_id).await?;

        let add_agent = client
            .post(server_url.join(&format!(
                "/api/v1/channels/{}/members",
                space.general_channel_id
            ))?)
            .header("idempotency-key", Uuid::now_v7().to_string())
            .header(header::COOKIE, &cookie)
            .json(&serde_json::json!({ "agent_member_ids": [agent_id] }))
            .send()
            .await?;
        ensure!(add_agent.status() == StatusCode::OK);

        Ok(Self {
            _root: root,
            database,
            server_url,
            server,
            daemon,
            client,
            cookie,
            space,
            computer_id: paired.id,
            computer_config,
            computer_state,
            agent_id,
            pool,
        })
    }

    async fn shutdown(mut self) -> Result<()> {
        self.daemon.interrupt().await?;
        self.server.interrupt().await?;
        self.pool.close().await;
        self.database.drop().await
    }
}

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

#[derive(Clone, Debug)]
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

fn latest_tool_error_result(request: &serde_json::Value) -> Option<serde_json::Value> {
    let content = latest_tool_content(request)?;
    serde_json::Deserializer::from_str(content.get(content.find('{')?..)?)
        .into_iter()
        .next()?
        .ok()
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

#[derive(Clone, Debug)]
enum ChannelMentionEvent {
    Replied {
        inbox_item_id: Uuid,
        reply: ObservedAgentReply,
    },
    Acked(Uuid),
    Deferred(Uuid),
    Failed(String),
}

#[derive(Clone, Debug)]
enum ChannelCreateEvent {
    Denied(Uuid),
    Created {
        inbox_item_id: Uuid,
        channel_id: Uuid,
        kind: &'static str,
    },
    Failed(String),
}

struct ChannelCreateProvider {
    url: Url,
    events: mpsc::UnboundedReceiver<ChannelCreateEvent>,
    state: Arc<ChannelCreateProviderState>,
    task: JoinHandle<()>,
}

struct ChannelCreateProviderState {
    step: Mutex<usize>,
    agent_id: Mutex<Option<Uuid>>,
    events: mpsc::UnboundedSender<ChannelCreateEvent>,
}

impl ChannelCreateProvider {
    async fn start() -> Result<Self> {
        let listener = TcpListener::bind(("127.0.0.1", 0)).await?;
        let address = listener.local_addr()?;
        let (events_tx, events) = mpsc::unbounded_channel();
        let state = Arc::new(ChannelCreateProviderState {
            step: Mutex::new(0),
            agent_id: Mutex::new(None),
            events: events_tx,
        });
        let app = Router::new()
            .route("/chat/completions", post(channel_create_chat_stream))
            .with_state(state.clone());
        let task = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });
        Ok(Self {
            url: Url::parse(&format!("http://{address}"))?,
            events,
            state,
            task,
        })
    }

    async fn set_agent_id(&self, agent_id: Uuid) {
        *self.state.agent_id.lock().await = Some(agent_id);
    }

    async fn wait_for_event(&mut self) -> Result<ChannelCreateEvent> {
        match tokio::time::timeout(Duration::from_secs(30), self.events.recv()).await {
            Ok(event) => event.context("Channel create provider stopped before the CLI action"),
            Err(_) => anyhow::bail!(
                "timed out waiting for Channel create action at provider step {}",
                *self.state.step.lock().await
            ),
        }
    }
}

impl Drop for ChannelCreateProvider {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn channel_create_chat_stream(
    State(state): State<Arc<ChannelCreateProviderState>>,
    headers: HeaderMap,
    Json(request): Json<serde_json::Value>,
) -> Response {
    let authorized = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value == format!("Bearer {PROVIDER_AUTH}"));
    let Some(agent_id) = *state.agent_id.lock().await else {
        return bad_provider_request();
    };
    if !authorized || request["stream"] != true || request["messages"].as_array().is_none() {
        return bad_provider_request();
    }

    let mut step = state.step.lock().await;
    let response = match *step {
        0 | 4 | 8 => tool_call_stream(
            &format!("channel-create-inbox-{step}"),
            "sumi agent inbox current --json",
        ),
        1 => tool_call_stream(
            "channel-create-denied",
            "sumi agent channel create agent-denied --name 'Agent Denied' --private --json",
        ),
        2 => {
            let Some((inbox_item_id, _, _)) = claimed_channel_mention(&request) else {
                return bad_provider_request();
            };
            if !latest_tool_error_result(&request).is_some_and(|result| {
                result["ok"] == false && result["error"]["code"] == "permission_denied"
            }) {
                let _ = state.events.send(ChannelCreateEvent::Failed(
                    "unprivileged channel create did not return permission_denied".to_owned(),
                ));
                return bad_provider_request();
            }
            tool_call_stream(
                "channel-create-denied-ack",
                &format!(
                    "sumi agent inbox ack {inbox_item_id} --reason 'channel create permission checked' --json"
                ),
            )
        }
        3 => {
            let Some((inbox_item_id, _, _)) = claimed_channel_mention(&request) else {
                return bad_provider_request();
            };
            if !valid_ack_result(&request, inbox_item_id) {
                return bad_provider_request();
            }
            let _ = state.events.send(ChannelCreateEvent::Denied(inbox_item_id));
            text_stream("Denied Channel create was handled explicitly.")
        }
        5 => tool_call_stream(
            "channel-create-private",
            "sumi agent channel create agent-private-real --name 'Agent Private Real' --private --json",
        ),
        6 => {
            channel_create_then_ack(
                &state,
                &request,
                agent_id,
                "private",
                "agent-private-real",
                "channel-create-private-ack",
            )
            .await
        }
        7 => finish_channel_create(&state, &request, "private"),
        9 => tool_call_stream(
            "channel-create-admin-public",
            "sumi agent channel create agent-admin-real --name 'Agent Admin Real' --json",
        ),
        10 => {
            channel_create_then_ack(
                &state,
                &request,
                agent_id,
                "public",
                "agent-admin-real",
                "channel-create-admin-ack",
            )
            .await
        }
        11 => finish_channel_create(&state, &request, "public"),
        _ => return bad_provider_request(),
    };
    *step += 1;
    response
}

async fn channel_create_then_ack(
    state: &ChannelCreateProviderState,
    request: &serde_json::Value,
    agent_id: Uuid,
    expected_kind: &'static str,
    expected_slug: &str,
    call_id: &str,
) -> Response {
    let Some((inbox_item_id, _, _)) = claimed_channel_mention(request) else {
        return bad_provider_request();
    };
    let Some(result) = latest_tool_result(request) else {
        return bad_provider_request();
    };
    let valid = result["ok"] == true
        && result["data"]["kind"] == expected_kind
        && result["data"]["slug"] == expected_slug
        && result["data"]["joined"] == true
        && result["data"]["created_by_member_id"] == agent_id.to_string()
        && result["data"]["id"].as_str().is_some();
    if !valid {
        let _ = state.events.send(ChannelCreateEvent::Failed(format!(
            "invalid {expected_kind} Channel create response: {result}"
        )));
        return bad_provider_request();
    }
    let Some(channel_id) = result["data"]["id"]
        .as_str()
        .and_then(|value| Uuid::parse_str(value).ok())
    else {
        return bad_provider_request();
    };
    tool_call_stream(
        call_id,
        &format!(
            "sumi agent inbox ack {inbox_item_id} --reason 'created {expected_kind} channel {channel_id}' --json"
        ),
    )
}

fn finish_channel_create(
    state: &ChannelCreateProviderState,
    request: &serde_json::Value,
    kind: &'static str,
) -> Response {
    let Some((inbox_item_id, _, _)) = claimed_channel_mention(request) else {
        return bad_provider_request();
    };
    if !valid_ack_result(request, inbox_item_id) {
        return bad_provider_request();
    }
    let channel_id = request["messages"]
        .as_array()
        .into_iter()
        .flatten()
        .filter_map(tool_result)
        .find_map(|result| {
            (result["ok"] == true && result["data"]["kind"] == kind)
                .then(|| {
                    result["data"]["id"]
                        .as_str()
                        .and_then(|id| Uuid::parse_str(id).ok())
                })
                .flatten()
        });
    let Some(channel_id) = channel_id else {
        return bad_provider_request();
    };
    let _ = state.events.send(ChannelCreateEvent::Created {
        inbox_item_id,
        channel_id,
        kind,
    });
    text_stream("Channel create completed through the real Sumi CLI.")
}

struct GovernanceProvider {
    url: Url,
    completed: mpsc::Receiver<Uuid>,
    state: Arc<GovernanceProviderState>,
    task: JoinHandle<()>,
}

struct GovernanceProviderState {
    step: Mutex<usize>,
    target_agent_id: Mutex<Option<Uuid>>,
    hidden_channel_id: Mutex<Option<Uuid>>,
    completed: mpsc::Sender<Uuid>,
}

impl GovernanceProvider {
    async fn start() -> Result<Self> {
        let listener = TcpListener::bind(("127.0.0.1", 0)).await?;
        let address = listener.local_addr()?;
        let (completed_tx, completed) = mpsc::channel(1);
        let state = Arc::new(GovernanceProviderState {
            step: Mutex::new(0),
            target_agent_id: Mutex::new(None),
            hidden_channel_id: Mutex::new(None),
            completed: completed_tx,
        });
        let app = Router::new()
            .route("/chat/completions", post(governance_chat_stream))
            .with_state(state.clone());
        let task = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });
        Ok(Self {
            url: Url::parse(&format!("http://{address}"))?,
            completed,
            state,
            task,
        })
    }

    async fn configure(&self, target_agent_id: Uuid, hidden_channel_id: Uuid) {
        *self.state.target_agent_id.lock().await = Some(target_agent_id);
        *self.state.hidden_channel_id.lock().await = Some(hidden_channel_id);
    }

    async fn wait_for_completion(&mut self) -> Result<Uuid> {
        match tokio::time::timeout(Duration::from_secs(45), self.completed.recv()).await {
            Ok(Some(item_id)) => Ok(item_id),
            Ok(None) => anyhow::bail!("governance provider stopped before completion"),
            Err(_) => anyhow::bail!(
                "governance Agent stopped after provider step {}",
                *self.state.step.lock().await
            ),
        }
    }
}

impl Drop for GovernanceProvider {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn governance_chat_stream(
    State(state): State<Arc<GovernanceProviderState>>,
    headers: HeaderMap,
    Json(request): Json<serde_json::Value>,
) -> Response {
    let authorized = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value == format!("Bearer {PROVIDER_AUTH}"));
    let Some(target_agent_id) = *state.target_agent_id.lock().await else {
        return bad_provider_request();
    };
    let Some(hidden_channel_id) = *state.hidden_channel_id.lock().await else {
        return bad_provider_request();
    };
    if !authorized || request["stream"] != true || request["messages"].as_array().is_none() {
        return bad_provider_request();
    }
    let mut step = state.step.lock().await;
    let response = match *step {
        0 => tool_call_stream("governance-inbox", "sumi agent inbox current --json"),
        1 => tool_call_stream(
            "governance-space",
            "sumi agent space update --name 'Governed Space' --accent '#A9D877' --json",
        ),
        2 if latest_tool_result(&request).is_some_and(|result| {
            result["data"]["name"] == "Governed Space" && result["data"]["accent"] == "#A9D877"
        }) =>
        {
            tool_call_stream(
                "governance-member-add",
                &format!("sumi agent channel member add '#governance' {target_agent_id} --json"),
            )
        }
        3 if latest_tool_result(&request)
            .is_some_and(|result| result["data"]["membership"] == "present") =>
        {
            tool_call_stream(
                "governance-member-remove",
                &format!("sumi agent channel member remove '#governance' {target_agent_id} --json"),
            )
        }
        4 if latest_tool_result(&request)
            .is_some_and(|result| result["data"]["membership"] == "absent") =>
        {
            tool_call_stream(
                "governance-suspend",
                &format!("sumi agent lifecycle suspend {target_agent_id} --json"),
            )
        }
        5 if latest_tool_result(&request)
            .is_some_and(|result| result["data"]["status"] == "suspended") =>
        {
            tool_call_stream(
                "governance-resume",
                &format!("sumi agent lifecycle resume {target_agent_id} --json"),
            )
        }
        6 if latest_tool_result(&request)
            .is_some_and(|result| result["data"]["status"] == "active") =>
        {
            tool_call_stream(
                "governance-audit",
                "sumi agent audit list --limit 100 --json",
            )
        }
        7 => {
            let Some(result) = latest_tool_result(&request) else {
                return bad_provider_request();
            };
            let visible = result["data"]["events"].as_array().is_some_and(|events| {
                !events.is_empty()
                    && events.iter().all(|event| {
                        event.get("metadata_json").is_none()
                            && event["subject_id"] != hidden_channel_id.to_string()
                    })
            });
            if !visible {
                return bad_provider_request();
            }
            tool_call_stream(
                "governance-hidden-private",
                "sumi agent channel archive '#owners-private' --json",
            )
        }
        8 if latest_tool_error_result(&request)
            .is_some_and(|result| result["error"]["code"] == "permission_denied") =>
        {
            tool_call_stream(
                "governance-human-only",
                "sumi agent human invite someone@example.com --json",
            )
        }
        9 if latest_tool_content(&request).is_some_and(|content| {
            content.contains("unrecognized subcommand") && content.contains("human")
        }) =>
        {
            tool_call_stream(
                "governance-archive",
                "sumi agent channel archive '#governance' --json",
            )
        }
        10 if latest_tool_result(&request)
            .is_some_and(|result| result["data"]["archived"] == true) =>
        {
            let Some((inbox_item_id, _, _)) = claimed_channel_mention(&request) else {
                return bad_provider_request();
            };
            tool_call_stream(
                "governance-ack",
                &format!(
                    "sumi agent inbox ack {inbox_item_id} --reason 'governance verified' --json"
                ),
            )
        }
        11 => {
            let Some((inbox_item_id, _, _)) = claimed_channel_mention(&request) else {
                return bad_provider_request();
            };
            if !valid_ack_result(&request, inbox_item_id) {
                return bad_provider_request();
            }
            let _ = state.completed.send(inbox_item_id).await;
            text_stream("Agent Admin governance completed through the real Sumi CLI.")
        }
        _ => return bad_provider_request(),
    };
    *step += 1;
    response
}

#[derive(Clone, Debug)]
enum AgentApprovalEvent {
    Requested {
        inbox_item_id: Uuid,
        approval_id: Uuid,
        name: &'static str,
    },
    Failed(String),
}

struct AgentApprovalProvider {
    url: Url,
    events: mpsc::UnboundedReceiver<AgentApprovalEvent>,
    state: Arc<AgentApprovalProviderState>,
    task: JoinHandle<()>,
}

struct AgentApprovalProviderState {
    step: Mutex<usize>,
    computer_id: Mutex<Option<Uuid>>,
    events: mpsc::UnboundedSender<AgentApprovalEvent>,
}

impl AgentApprovalProvider {
    async fn start() -> Result<Self> {
        let listener = TcpListener::bind(("127.0.0.1", 0)).await?;
        let address = listener.local_addr()?;
        let (events_tx, events) = mpsc::unbounded_channel();
        let state = Arc::new(AgentApprovalProviderState {
            step: Mutex::new(0),
            computer_id: Mutex::new(None),
            events: events_tx,
        });
        let app = Router::new()
            .route("/chat/completions", post(agent_approval_chat_stream))
            .with_state(state.clone());
        let task = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });
        Ok(Self {
            url: Url::parse(&format!("http://{address}"))?,
            events,
            state,
            task,
        })
    }

    async fn set_computer_id(&self, computer_id: Uuid) {
        *self.state.computer_id.lock().await = Some(computer_id);
    }

    async fn wait_for_event(&mut self) -> Result<AgentApprovalEvent> {
        match tokio::time::timeout(Duration::from_secs(30), self.events.recv()).await {
            Ok(event) => event.context("Agent Approval provider stopped before the CLI action"),
            Err(_) => anyhow::bail!(
                "timed out waiting for Agent Approval action at provider step {}",
                *self.state.step.lock().await
            ),
        }
    }
}

impl Drop for AgentApprovalProvider {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn agent_approval_chat_stream(
    State(state): State<Arc<AgentApprovalProviderState>>,
    headers: HeaderMap,
    Json(request): Json<serde_json::Value>,
) -> Response {
    let authorized = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value == format!("Bearer {PROVIDER_AUTH}"));
    let Some(computer_id) = *state.computer_id.lock().await else {
        return bad_provider_request();
    };
    if !authorized || request["stream"] != true || request["messages"].as_array().is_none() {
        return bad_provider_request();
    }

    let mut step = state.step.lock().await;
    let response = match *step {
        0 | 4 => tool_call_stream(
            &format!("agent-approval-inbox-{step}"),
            "sumi agent inbox current --json",
        ),
        1 => tool_call_stream(
            "agent-approval-create-approved",
            &format!(
                "sumi agent create --name 'Approved Child' --role-file ../memory/MEMORY.md --computer {computer_id} --driver builtin --json"
            ),
        ),
        2 => agent_approval_then_ack(
            &state,
            &request,
            "Approved Child",
            "agent-approval-approved-ack",
        ),
        3 => finish_agent_approval(&state, &request, "Approved Child"),
        5 => tool_call_stream(
            "agent-approval-create-rejected",
            &format!(
                "sumi agent create --name 'Rejected Child' --role-file ../memory/MEMORY.md --computer {computer_id} --driver builtin --json"
            ),
        ),
        6 => agent_approval_then_ack(
            &state,
            &request,
            "Rejected Child",
            "agent-approval-rejected-ack",
        ),
        7 => finish_agent_approval(&state, &request, "Rejected Child"),
        _ => return bad_provider_request(),
    };
    *step += 1;
    response
}

fn agent_approval_then_ack(
    state: &AgentApprovalProviderState,
    request: &serde_json::Value,
    name: &'static str,
    call_id: &str,
) -> Response {
    let Some((inbox_item_id, _, _)) = claimed_channel_mention(request) else {
        return bad_provider_request();
    };
    let Some(result) = latest_tool_result(request) else {
        return bad_provider_request();
    };
    if result["ok"] != true
        || result["data"]["status"] != "pending"
        || result["data"]["approval_id"].as_str().is_none()
    {
        let _ = state.events.send(AgentApprovalEvent::Failed(format!(
            "invalid pending Approval response for {name}: {result}"
        )));
        return bad_provider_request();
    }
    tool_call_stream(
        call_id,
        &format!(
            "sumi agent inbox ack {inbox_item_id} --reason 'requested Human approval for {name}' --json"
        ),
    )
}

fn finish_agent_approval(
    state: &AgentApprovalProviderState,
    request: &serde_json::Value,
    name: &'static str,
) -> Response {
    let Some((inbox_item_id, _, _)) = claimed_channel_mention(request) else {
        return bad_provider_request();
    };
    if !valid_ack_result(request, inbox_item_id) {
        return bad_provider_request();
    }
    let approval_id = request["messages"]
        .as_array()
        .into_iter()
        .flatten()
        .filter_map(tool_result)
        .find_map(|result| {
            (result["ok"] == true && result["data"]["status"] == "pending")
                .then(|| {
                    result["data"]["approval_id"]
                        .as_str()
                        .and_then(|id| Uuid::parse_str(id).ok())
                })
                .flatten()
        });
    let Some(approval_id) = approval_id else {
        return bad_provider_request();
    };
    let _ = state.events.send(AgentApprovalEvent::Requested {
        inbox_item_id,
        approval_id,
        name,
    });
    text_stream("Agent creation request was submitted for Human approval.")
}

fn valid_ack_result(request: &serde_json::Value, inbox_item_id: Uuid) -> bool {
    latest_tool_result(request).is_some_and(|result| {
        result["ok"] == true
            && result["data"]["handled_inbox_item_ids"]
                .as_array()
                .is_some_and(|ids| ids == &[serde_json::json!(inbox_item_id)])
    })
}

#[derive(sqlx::FromRow)]
struct ChannelMentionInboxState {
    kind: String,
    priority: String,
    retry_count: i32,
    lease_id: Option<Uuid>,
    handled_by_run_id: Option<Uuid>,
    available_at: Option<time::OffsetDateTime>,
    run_link_count: i64,
    outbox_count: i64,
}

struct ChannelMentionProvider {
    url: Url,
    events: mpsc::UnboundedReceiver<ChannelMentionEvent>,
    state: Arc<ChannelMentionProviderState>,
    task: JoinHandle<()>,
}

struct ChannelMentionProviderState {
    step: Mutex<usize>,
    agent_id: Mutex<Option<Uuid>>,
    events: mpsc::UnboundedSender<ChannelMentionEvent>,
}

impl ChannelMentionProvider {
    async fn start() -> Result<Self> {
        let listener = TcpListener::bind(("127.0.0.1", 0)).await?;
        let address = listener.local_addr()?;
        let (events_tx, events) = mpsc::unbounded_channel();
        let state = Arc::new(ChannelMentionProviderState {
            step: Mutex::new(0),
            agent_id: Mutex::new(None),
            events: events_tx,
        });
        let app = Router::new()
            .route("/chat/completions", post(channel_mention_chat_stream))
            .with_state(state.clone());
        let task = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });
        Ok(Self {
            url: Url::parse(&format!("http://{address}"))?,
            events,
            state,
            task,
        })
    }

    async fn set_agent_id(&self, agent_id: Uuid) {
        *self.state.agent_id.lock().await = Some(agent_id);
    }

    async fn wait_for_event(&mut self) -> Result<ChannelMentionEvent> {
        match tokio::time::timeout(Duration::from_secs(30), self.events.recv()).await {
            Ok(event) => {
                event.context("Channel mention provider stopped before the Agent action completed")
            }
            Err(_) => anyhow::bail!(
                "timed out waiting for Channel mention Agent action at provider step {}",
                *self.state.step.lock().await
            ),
        }
    }
}

impl Drop for ChannelMentionProvider {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn channel_mention_chat_stream(
    State(state): State<Arc<ChannelMentionProviderState>>,
    headers: HeaderMap,
    Json(request): Json<serde_json::Value>,
) -> Response {
    let authorized = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value == format!("Bearer {PROVIDER_AUTH}"));
    let Some(agent_id) = *state.agent_id.lock().await else {
        return bad_provider_request();
    };
    if !authorized || request["stream"] != true || request["messages"].as_array().is_none() {
        return bad_provider_request();
    }

    let mut step = state.step.lock().await;
    let response = match *step {
        0 | 4 | 8 => {
            if !prompt_has_channel_mention_summary(&request) {
                return bad_provider_request();
            }
            tool_call_stream(
                &format!("channel-mention-inbox-{step}"),
                "sumi agent inbox current --json",
            )
        }
        1 | 5 | 9 => {
            let Some((_, address, _)) = claimed_channel_mention(&request) else {
                return bad_provider_request();
            };
            tool_call_stream(
                &format!("channel-mention-read-{step}"),
                &format!("sumi agent channel read '{address}' --json"),
            )
        }
        2 | 6 | 10 => {
            let Some((inbox_item_id, address, message_id)) = claimed_channel_mention(&request)
            else {
                return bad_provider_request();
            };
            let expected_snapshot = match *step {
                2 => 1,
                6 => 3,
                _ => 4,
            };
            let Some(result) = latest_tool_result(&request) else {
                let _ = state.events.send(ChannelMentionEvent::Failed(
                    "channel read did not return JSON".to_owned(),
                ));
                return bad_provider_request();
            };
            if !valid_channel_mention_read(
                &result,
                &address,
                expected_snapshot,
                agent_id,
                message_id,
            ) {
                let source = result["data"]["messages"].as_array().and_then(|messages| {
                    messages
                        .iter()
                        .find(|message| message["id"] == message_id.to_string())
                });
                let _ = state.events.send(ChannelMentionEvent::Failed(format!(
                    "invalid channel read: ok={}, address_match={}, snapshot={:?}, message_count={:?}, source_found={}, mentions_match={}",
                    result["ok"] == true,
                    result["data"]["address"] == address,
                    result["data"]["snapshot_channel_seq"].as_i64(),
                    result["data"]["messages"].as_array().map(Vec::len),
                    source.is_some(),
                    source.is_some_and(|message| message["mentions"] == serde_json::json!([agent_id])),
                )));
                return bad_provider_request();
            }
            match *step {
                2 => tool_call_stream(
                    "channel-mention-reply",
                    &format!(
                        "sumi agent message send '{address}' --body '{CHANNEL_CLI_REPLY}' --based-on {expected_snapshot} --handle {inbox_item_id} --json"
                    ),
                ),
                6 => tool_call_stream(
                    "channel-mention-ack",
                    &format!(
                        "sumi agent inbox ack {inbox_item_id} --reason 'explicitly acknowledged in process test' --json"
                    ),
                ),
                _ => tool_call_stream(
                    "channel-mention-defer",
                    &format!(
                        "sumi agent inbox defer {inbox_item_id} --until '{CHANNEL_DEFER_UNTIL}' --json"
                    ),
                ),
            }
        }
        3 => {
            let Some((inbox_item_id, address, _)) = claimed_channel_mention(&request) else {
                return bad_provider_request();
            };
            let Some(reply) = latest_tool_result(&request)
                .and_then(|result| valid_message_send(&result, &address))
            else {
                return bad_provider_request();
            };
            let _ = state.events.send(ChannelMentionEvent::Replied {
                inbox_item_id,
                reply,
            });
            text_stream("Channel mention reply action completed.")
        }
        7 => {
            let Some((inbox_item_id, _, _)) = claimed_channel_mention(&request) else {
                return bad_provider_request();
            };
            let Some(result) = latest_tool_result(&request) else {
                return bad_provider_request();
            };
            if result["data"]["handled_inbox_item_ids"]
                .as_array()
                .is_none_or(|ids| ids != &[serde_json::json!(inbox_item_id)])
            {
                return bad_provider_request();
            }
            let _ = state.events.send(ChannelMentionEvent::Acked(inbox_item_id));
            text_stream("Channel mention ack action completed.")
        }
        11 => {
            let Some((inbox_item_id, _, _)) = claimed_channel_mention(&request) else {
                return bad_provider_request();
            };
            let Some(result) = latest_tool_result(&request) else {
                return bad_provider_request();
            };
            if result["data"]["deferred_inbox_item_ids"]
                .as_array()
                .is_none_or(|ids| ids != &[serde_json::json!(inbox_item_id)])
                || result["data"]["available_at"] != CHANNEL_DEFER_UNTIL
            {
                let _ = state.events.send(ChannelMentionEvent::Failed(format!(
                    "invalid defer result: ids_match={}, available_at={}",
                    result["data"]["deferred_inbox_item_ids"]
                        .as_array()
                        .is_some_and(|ids| ids == &[serde_json::json!(inbox_item_id)]),
                    result["data"]["available_at"],
                )));
                return bad_provider_request();
            }
            let _ = state
                .events
                .send(ChannelMentionEvent::Deferred(inbox_item_id));
            text_stream("Channel mention defer action completed.")
        }
        _ => return bad_provider_request(),
    };
    *step += 1;
    response
}

fn prompt_has_channel_mention_summary(request: &serde_json::Value) -> bool {
    request["messages"].as_array().is_some_and(|messages| {
        messages.iter().any(|message| {
            message["content"].as_str().is_some_and(|content| {
                content.contains("\"kind\": \"mention\"")
                    && content.contains("\"priority\": \"hard\"")
                    && content.contains("\"address\": \"#general\"")
            })
        })
    })
}

fn claimed_channel_mention(request: &serde_json::Value) -> Option<(Uuid, String, Uuid)> {
    request["messages"]
        .as_array()?
        .iter()
        .filter_map(tool_result)
        .find_map(|result| {
            let item = result["data"]["items"].as_array()?.first()?;
            (item["kind"] == "mention"
                && item["priority"] == "hard"
                && item["status"] == "leased"
                && item["address"] == "#general")
                .then(|| {
                    Some((
                        Uuid::parse_str(item["id"].as_str()?).ok()?,
                        item["address"].as_str()?.to_owned(),
                        Uuid::parse_str(item["message_id"].as_str()?).ok()?,
                    ))
                })
                .flatten()
        })
}

fn valid_channel_mention_read(
    result: &serde_json::Value,
    expected_address: &str,
    expected_snapshot: i64,
    agent_id: Uuid,
    source_message_id: Uuid,
) -> bool {
    result["ok"] == true
        && result["data"]["address"] == expected_address
        && result["data"]["snapshot_channel_seq"].as_i64() == Some(expected_snapshot)
        && result["data"]["messages"]
            .as_array()
            .is_some_and(|messages| {
                messages.iter().any(|message| {
                    message["id"] == source_message_id.to_string()
                        && message["mentions"] == serde_json::json!([agent_id])
                })
            })
}

struct PermissionBoundaryProvider {
    url: Url,
    completed: mpsc::Receiver<Uuid>,
    state: Arc<PermissionBoundaryProviderState>,
    task: JoinHandle<()>,
}

struct PermissionBoundaryProviderState {
    step: Mutex<usize>,
    other_agent_marker: Mutex<Option<PathBuf>>,
    completed: mpsc::Sender<Uuid>,
}

impl PermissionBoundaryProvider {
    async fn start() -> Result<Self> {
        let listener = TcpListener::bind(("127.0.0.1", 0)).await?;
        let address = listener.local_addr()?;
        let (completed_tx, completed) = mpsc::channel(1);
        let state = Arc::new(PermissionBoundaryProviderState {
            step: Mutex::new(0),
            other_agent_marker: Mutex::new(None),
            completed: completed_tx,
        });
        let app = Router::new()
            .route("/chat/completions", post(permission_boundary_chat_stream))
            .with_state(state.clone());
        let task = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });
        Ok(Self {
            url: Url::parse(&format!("http://{address}"))?,
            completed,
            state,
            task,
        })
    }

    async fn set_other_agent_marker(&self, marker: PathBuf) {
        *self.state.other_agent_marker.lock().await = Some(marker);
    }

    async fn wait_for_completion(&mut self) -> Result<Uuid> {
        match tokio::time::timeout(Duration::from_secs(30), self.completed.recv()).await {
            Ok(Some(item_id)) => Ok(item_id),
            Ok(None) => anyhow::bail!("permission boundary provider stopped before completion"),
            Err(_) => anyhow::bail!(
                "permission boundary Agent stopped after provider step {}",
                *self.state.step.lock().await
            ),
        }
    }
}

impl Drop for PermissionBoundaryProvider {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn permission_boundary_chat_stream(
    State(state): State<Arc<PermissionBoundaryProviderState>>,
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
    let response = match *step {
        0 => tool_call_stream(
            "permission-boundary-inbox",
            "sumi agent inbox current --json",
        ),
        1 => {
            if claimed_channel_mention(&request).is_none() {
                return bad_provider_request();
            }
            tool_call_stream(
                "permission-boundary-private-read",
                "sumi agent channel read '#owners-private' --json",
            )
        }
        2 => {
            let Some(result) = latest_tool_error_result(&request) else {
                return bad_provider_request();
            };
            if result["ok"] != false
                || result["error"]["code"] != "permission_denied"
                || latest_tool_content(&request)
                    .is_some_and(|value| value.contains(PRIVATE_CHANNEL_BODY))
            {
                return bad_provider_request();
            }
            tool_call_stream(
                "permission-boundary-forged-token",
                "if SUMI_RUN_TOKEN=forged-run-token sumi agent whoami --json >/dev/null 2>&1; then exit 71; fi; printf forged-run-token-denied",
            )
        }
        3 => {
            if !latest_tool_content(&request)
                .is_some_and(|content| content.contains("forged-run-token-denied"))
            {
                return bad_provider_request();
            }
            let Some(marker) = state.other_agent_marker.lock().await.clone() else {
                return bad_provider_request();
            };
            tool_call_stream(
                "permission-boundary-other-home",
                &format!(
                    "if test -r '{}'; then exit 72; fi; printf other-agent-home-hidden",
                    marker.display()
                ),
            )
        }
        4 => {
            if !latest_tool_content(&request)
                .is_some_and(|content| content.contains("other-agent-home-hidden"))
            {
                return bad_provider_request();
            }
            let Some((inbox_item_id, _, _)) = claimed_channel_mention(&request) else {
                return bad_provider_request();
            };
            tool_call_stream(
                "permission-boundary-ack",
                &format!(
                    "sumi agent inbox ack {inbox_item_id} --reason 'authorization boundary verified' --json"
                ),
            )
        }
        5 => {
            let Some((inbox_item_id, _, _)) = claimed_channel_mention(&request) else {
                return bad_provider_request();
            };
            let Some(result) = latest_tool_result(&request) else {
                return bad_provider_request();
            };
            if result["data"]["handled_inbox_item_ids"]
                .as_array()
                .is_none_or(|ids| ids != &[serde_json::json!(inbox_item_id)])
            {
                return bad_provider_request();
            }
            let _ = state.completed.send(inbox_item_id).await;
            text_stream("Permission boundary verification completed.")
        }
        _ => return bad_provider_request(),
    };
    *step += 1;
    response
}

#[derive(Debug)]
enum ThreadEvent {
    ContextRead,
    Replied {
        inbox_item_id: Uuid,
        reply: ObservedAgentReply,
    },
    Failed(String),
}

struct ThreadProvider {
    url: Url,
    events: mpsc::UnboundedReceiver<ThreadEvent>,
    state: Arc<ThreadProviderState>,
    task: JoinHandle<()>,
}

struct ThreadProviderState {
    step: Mutex<usize>,
    agent_id: Mutex<Option<Uuid>>,
    context_changed: Notify,
    events: mpsc::UnboundedSender<ThreadEvent>,
}

impl ThreadProvider {
    async fn start() -> Result<Self> {
        let listener = TcpListener::bind(("127.0.0.1", 0)).await?;
        let address = listener.local_addr()?;
        let (events_tx, events) = mpsc::unbounded_channel();
        let state = Arc::new(ThreadProviderState {
            step: Mutex::new(0),
            agent_id: Mutex::new(None),
            context_changed: Notify::new(),
            events: events_tx,
        });
        let app = Router::new()
            .route("/chat/completions", post(thread_chat_stream))
            .with_state(state.clone());
        let task = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });
        Ok(Self {
            url: Url::parse(&format!("http://{address}"))?,
            events,
            state,
            task,
        })
    }

    async fn set_agent_id(&self, agent_id: Uuid) {
        *self.state.agent_id.lock().await = Some(agent_id);
    }

    async fn wait_for_event(&mut self) -> Result<ThreadEvent> {
        match tokio::time::timeout(Duration::from_secs(30), self.events.recv()).await {
            Ok(event) => event.context("Thread provider stopped before the Agent reply completed"),
            Err(_) => anyhow::bail!(
                "timed out waiting for Thread Agent reply at provider step {}",
                *self.state.step.lock().await
            ),
        }
    }

    fn context_changed(&self) {
        self.state.context_changed.notify_one();
    }
}

impl Drop for ThreadProvider {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn thread_chat_stream(
    State(state): State<Arc<ThreadProviderState>>,
    headers: HeaderMap,
    Json(request): Json<serde_json::Value>,
) -> Response {
    let authorized = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value == format!("Bearer {PROVIDER_AUTH}"));
    let Some(agent_id) = *state.agent_id.lock().await else {
        return bad_provider_request();
    };
    if !authorized || request["stream"] != true || request["messages"].as_array().is_none() {
        return bad_provider_request();
    }

    let mut step = state.step.lock().await;
    let response = match *step {
        0 => {
            if !prompt_has_thread_summary(&request) {
                return bad_provider_request();
            }
            tool_call_stream("thread-inbox", "sumi agent inbox current --json")
        }
        1 => {
            let Some((_, address, _)) = claimed_thread_mention(&request) else {
                return bad_provider_request();
            };
            tool_call_stream(
                "thread-read",
                &format!("sumi agent thread read '{address}' --include-channel 20 --json"),
            )
        }
        2 => {
            let Some((inbox_item_id, address, source_message_id)) =
                claimed_thread_mention(&request)
            else {
                return bad_provider_request();
            };
            let Some(result) = latest_tool_result(&request) else {
                let _ = state.events.send(ThreadEvent::Failed(
                    "thread read did not return JSON".to_owned(),
                ));
                return bad_provider_request();
            };
            if !valid_thread_read(&result, &address, agent_id, source_message_id) {
                let _ = state.events.send(ThreadEvent::Failed(format!(
                    "invalid thread read: ok={}, address={}, thread_id={:?}, snapshot={:?}, replies={:?}, background={:?}",
                    result["ok"] == true,
                    result["data"]["address"],
                    result["data"]["thread_id"].as_i64(),
                    result["data"]["snapshot_channel_seq"].as_i64(),
                    result["data"]["replies"].as_array().map(Vec::len),
                    result["data"]["channel_background"].as_array().map(Vec::len),
                )));
                return bad_provider_request();
            }
            let _ = state.events.send(ThreadEvent::ContextRead);
            state.context_changed.notified().await;
            tool_call_stream(
                "thread-stale-reply",
                &format!(
                    "sumi agent message send '{address}' --body '{THREAD_STALE_REPLY}' --based-on 4 --handle {inbox_item_id} --json"
                ),
            )
        }
        3 => {
            let Some((_, address, _)) = claimed_thread_mention(&request) else {
                return bad_provider_request();
            };
            let Some(content) = latest_tool_content(&request) else {
                let _ = state.events.send(ThreadEvent::Failed(
                    "stale send returned no tool content".to_owned(),
                ));
                return bad_provider_request();
            };
            let Some(result) = latest_tool_error_result(&request) else {
                let _ = state.events.send(ThreadEvent::Failed(format!(
                    "stale tool result was not parseable JSON: length={}, starts_with_error={}, contains_context_changed={}",
                    content.len(),
                    content.starts_with("error:"),
                    content.contains("context_changed"),
                )));
                return bad_provider_request();
            };
            if !valid_thread_context_changed(&result, &address) {
                let _ = state.events.send(ThreadEvent::Failed(format!(
                    "invalid context_changed response: {result}"
                )));
                return bad_provider_request();
            }
            tool_call_stream(
                "thread-refresh",
                &format!("sumi agent thread read '{address}' --include-channel 20 --json"),
            )
        }
        4 => {
            let Some((inbox_item_id, address, _)) = claimed_thread_mention(&request) else {
                return bad_provider_request();
            };
            let Some(result) = latest_tool_result(&request) else {
                return bad_provider_request();
            };
            if !valid_refreshed_thread_read(&result, &address) {
                let _ = state.events.send(ThreadEvent::Failed(format!(
                    "invalid refreshed Thread read: {result}"
                )));
                return bad_provider_request();
            }
            tool_call_stream(
                "thread-fresh-reply",
                &format!(
                    "sumi agent message send '{address}' --body '{THREAD_CLI_REPLY}' --based-on 5 --handle {inbox_item_id} --json"
                ),
            )
        }
        5 => {
            let Some((inbox_item_id, address, _)) = claimed_thread_mention(&request) else {
                return bad_provider_request();
            };
            let Some(reply) = latest_tool_result(&request)
                .and_then(|result| observed_message_send(&result, &address, 6))
            else {
                return bad_provider_request();
            };
            let _ = state.events.send(ThreadEvent::Replied {
                inbox_item_id,
                reply,
            });
            text_stream("Thread reply action completed.")
        }
        _ => return bad_provider_request(),
    };
    *step += 1;
    response
}

fn valid_thread_context_changed(result: &serde_json::Value, expected_address: &str) -> bool {
    let details = &result["error"]["details"];
    let changes = details["changes"].as_array();
    result["ok"] == false
        && result["error"]["code"] == "context_changed"
        && result["error"]["retryable"] == false
        && details["snapshot_channel_seq"].as_i64() == Some(4)
        && details["latest_channel_seq"].as_i64() == Some(5)
        && details["has_more"] == false
        && changes.is_some_and(|changes| {
            changes.len() == 1
                && changes[0]["seq"].as_i64() == Some(5)
                && changes[0]["address"] == expected_address
                && changes[0]["thread_id"].as_i64() == Some(1)
                && changes[0]["author"]["kind"] == "human"
                && changes[0].get("body_markdown").is_none()
        })
}

fn valid_refreshed_thread_read(result: &serde_json::Value, expected_address: &str) -> bool {
    result["ok"] == true
        && result["data"]["address"] == expected_address
        && result["data"]["snapshot_channel_seq"].as_i64() == Some(5)
        && result["data"]["replies"].as_array().is_some_and(|replies| {
            replies.len() == 3
                && replies[2]["seq"].as_i64() == Some(5)
                && replies[2]["body_markdown"] == THREAD_FRESHNESS_REPLY
        })
}

fn prompt_has_thread_summary(request: &serde_json::Value) -> bool {
    request["messages"].as_array().is_some_and(|messages| {
        messages.iter().any(|message| {
            message["content"].as_str().is_some_and(|content| {
                content.contains("\"kind\": \"mention\"")
                    && content.contains("\"priority\": \"hard\"")
                    && content.contains("\"address\": \"#general:1\"")
            })
        })
    })
}

fn claimed_thread_mention(request: &serde_json::Value) -> Option<(Uuid, String, Uuid)> {
    request["messages"]
        .as_array()?
        .iter()
        .filter_map(tool_result)
        .find_map(|result| {
            result["data"]["items"].as_array()?.iter().find_map(|item| {
                (item["kind"] == "mention"
                    && item["priority"] == "hard"
                    && item["status"] == "leased"
                    && item["thread_id"].as_i64() == Some(1)
                    && item["address"] == "#general:1")
                    .then(|| {
                        Some((
                            Uuid::parse_str(item["id"].as_str()?).ok()?,
                            item["address"].as_str()?.to_owned(),
                            Uuid::parse_str(item["message_id"].as_str()?).ok()?,
                        ))
                    })
                    .flatten()
            })
        })
}

fn valid_thread_read(
    result: &serde_json::Value,
    expected_address: &str,
    agent_id: Uuid,
    source_message_id: Uuid,
) -> bool {
    let replies = result["data"]["replies"].as_array();
    result["ok"] == true
        && result["data"]["address"] == expected_address
        && result["data"]["thread_id"].as_i64() == Some(1)
        && result["data"]["snapshot_channel_seq"].as_i64() == Some(4)
        && result["data"]["root"]["seq"].as_i64() == Some(2)
        && result["data"]["root"]["address"] == expected_address
        && result["data"]["root"]["body_markdown"] == THREAD_ROOT
        && replies.is_some_and(|replies| {
            replies.len() == 2
                && replies[0]["seq"].as_i64() == Some(3)
                && replies[0]["body_markdown"] == THREAD_FIRST_REPLY
                && replies[1]["id"] == source_message_id.to_string()
                && replies[1]["seq"].as_i64() == Some(4)
                && replies[1]["mentions"] == serde_json::json!([agent_id])
                && replies
                    .iter()
                    .all(|message| message["address"] == expected_address)
        })
        && result["data"]["channel_background"]
            .as_array()
            .is_some_and(|messages| {
                messages.len() == 1
                    && messages[0]["seq"].as_i64() == Some(1)
                    && messages[0]["address"] == "#general"
                    && messages[0]["body_markdown"] == THREAD_BACKGROUND
            })
        && result["data"]["has_more_before"] == false
        && result["data"]["has_more_after"] == false
}

fn observed_message_send(
    result: &serde_json::Value,
    expected_address: &str,
    expected_seq: i64,
) -> Option<ObservedAgentReply> {
    (result["ok"] == true
        && result["data"]["address"] == expected_address
        && result["data"]["author"]["kind"] == "agent"
        && result["data"]["seq"].as_i64() == Some(expected_seq))
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

#[derive(Debug)]
enum ChannelAmbientEvent {
    Acked(Uuid),
    Failed(String),
}

struct ChannelAmbientProvider {
    url: Url,
    events: mpsc::UnboundedReceiver<ChannelAmbientEvent>,
    state: Arc<ChannelAmbientProviderState>,
    task: JoinHandle<()>,
}

struct ChannelAmbientProviderState {
    step: Mutex<usize>,
    events: mpsc::UnboundedSender<ChannelAmbientEvent>,
}

impl ChannelAmbientProvider {
    async fn start() -> Result<Self> {
        let listener = TcpListener::bind(("127.0.0.1", 0)).await?;
        let address = listener.local_addr()?;
        let (events_tx, events) = mpsc::unbounded_channel();
        let state = Arc::new(ChannelAmbientProviderState {
            step: Mutex::new(0),
            events: events_tx,
        });
        let app = Router::new()
            .route("/chat/completions", post(channel_ambient_chat_stream))
            .with_state(state.clone());
        let task = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });
        Ok(Self {
            url: Url::parse(&format!("http://{address}"))?,
            events,
            state,
            task,
        })
    }

    async fn wait_for_event(&mut self) -> Result<ChannelAmbientEvent> {
        match tokio::time::timeout(Duration::from_secs(30), self.events.recv()).await {
            Ok(event) => event.context("Channel ambient provider stopped before ack completed"),
            Err(_) => anyhow::bail!(
                "timed out waiting for Channel ambient ack at provider step {}",
                *self.state.step.lock().await
            ),
        }
    }
}

impl Drop for ChannelAmbientProvider {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn channel_ambient_chat_stream(
    State(state): State<Arc<ChannelAmbientProviderState>>,
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
    let response = match *step {
        0 => {
            if !prompt_has_channel_ambient_summary(&request) {
                return bad_provider_request();
            }
            tool_call_stream("channel-ambient-inbox", "sumi agent inbox current --json")
        }
        1 => {
            let Some((_, address, _)) = claimed_channel_ambient(&request) else {
                return bad_provider_request();
            };
            tool_call_stream(
                "channel-ambient-read",
                &format!("sumi agent channel read '{address}' --json"),
            )
        }
        2 => {
            let Some((inbox_item_id, address, latest_message_id)) =
                claimed_channel_ambient(&request)
            else {
                return bad_provider_request();
            };
            let Some(result) = latest_tool_result(&request) else {
                let _ = state.events.send(ChannelAmbientEvent::Failed(
                    "channel read did not return JSON".to_owned(),
                ));
                return bad_provider_request();
            };
            if !valid_channel_ambient_read(&result, &address, latest_message_id) {
                let _ = state.events.send(ChannelAmbientEvent::Failed(format!(
                    "invalid ambient channel read: ok={}, address={}, snapshot={:?}, count={:?}",
                    result["ok"] == true,
                    result["data"]["address"],
                    result["data"]["snapshot_channel_seq"].as_i64(),
                    result["data"]["messages"].as_array().map(Vec::len),
                )));
                return bad_provider_request();
            }
            tool_call_stream(
                "channel-ambient-ack",
                &format!(
                    "sumi agent inbox ack {inbox_item_id} --reason 'ordinary Channel activity needs no reply' --json"
                ),
            )
        }
        3 => {
            let Some((inbox_item_id, _, _)) = claimed_channel_ambient(&request) else {
                return bad_provider_request();
            };
            let Some(result) = latest_tool_result(&request) else {
                return bad_provider_request();
            };
            if result["data"]["handled_inbox_item_ids"]
                .as_array()
                .is_none_or(|ids| ids != &[serde_json::json!(inbox_item_id)])
            {
                return bad_provider_request();
            }
            let _ = state.events.send(ChannelAmbientEvent::Acked(inbox_item_id));
            text_stream("Channel ambient activity acknowledged without a reply.")
        }
        _ => return bad_provider_request(),
    };
    *step += 1;
    response
}

fn prompt_has_channel_ambient_summary(request: &serde_json::Value) -> bool {
    request["messages"].as_array().is_some_and(|messages| {
        messages.iter().any(|message| {
            message["content"].as_str().is_some_and(|content| {
                content.contains("\"kind\": \"channel_activity\"")
                    && content.contains("\"priority\": \"ambient\"")
                    && content.contains("\"address\": \"#general\"")
            })
        })
    })
}

fn claimed_channel_ambient(request: &serde_json::Value) -> Option<(Uuid, String, Uuid)> {
    request["messages"]
        .as_array()?
        .iter()
        .filter_map(tool_result)
        .find_map(|result| {
            let items = result["data"]["items"].as_array()?;
            if items.len() != 1 {
                return None;
            }
            let item = &items[0];
            (item["kind"] == "channel_activity"
                && item["priority"] == "ambient"
                && item["status"] == "leased"
                && item["address"] == "#general")
                .then(|| {
                    Some((
                        Uuid::parse_str(item["id"].as_str()?).ok()?,
                        item["address"].as_str()?.to_owned(),
                        Uuid::parse_str(item["message_id"].as_str()?).ok()?,
                    ))
                })
                .flatten()
        })
}

fn valid_channel_ambient_read(
    result: &serde_json::Value,
    expected_address: &str,
    latest_message_id: Uuid,
) -> bool {
    result["ok"] == true
        && result["data"]["address"] == expected_address
        && result["data"]["snapshot_channel_seq"].as_i64() == Some(5)
        && result["data"]["messages"]
            .as_array()
            .is_some_and(|messages| {
                messages.len() == 5
                    && messages.iter().enumerate().all(|(index, message)| {
                        message["seq"].as_i64() == Some(index as i64 + 1)
                            && message["mentions"] == serde_json::json!([])
                    })
                    && messages[4]["id"] == latest_message_id.to_string()
            })
}

#[tokio::test]
async fn builtin_agent_create_requires_human_approval_and_provisions_through_real_daemon()
-> Result<()> {
    let mut provider = AgentApprovalProvider::start().await?;
    let mut harness = AgentProcessHarness::start(
        &provider.url,
        "sumi_agent_approval_test",
        "Request new Agents only through the Sumi Agent CLI and Human Approval.",
    )
    .await?;
    provider.set_computer_id(harness.computer_id).await;
    disable_agent_ambient(&harness).await?;

    let promote = harness
        .client
        .patch(harness.server_url.join(&format!(
            "/api/v1/spaces/{}/members/{}",
            harness.space.id, harness.agent_id
        ))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({ "access_level": "admin" }))
        .send()
        .await?;
    ensure!(promote.status() == StatusCode::OK);

    send_channel_mention(
        &harness.client,
        &harness.server_url,
        &harness.cookie,
        harness.space.general_channel_id,
        harness.agent_id,
        "@lin request the approved child Agent through the CLI.",
    )
    .await?;
    let (approved_item_id, approved_id) = match provider.wait_for_event().await? {
        AgentApprovalEvent::Requested {
            inbox_item_id,
            approval_id,
            name: "Approved Child",
        } => (inbox_item_id, approval_id),
        AgentApprovalEvent::Failed(reason) => anyhow::bail!(reason),
        event => anyhow::bail!("expected approved child request, got {event:?}"),
    };
    wait_for_mention_run(&harness.pool, approved_item_id, "handled", 1).await?;

    let pending_state: (i64, i64, i64, i64, i64, i64, i64) = sqlx::query_as(
        "SELECT \
            (SELECT count(*) FROM approvals WHERE id = $1 AND status = 'pending' \
                AND requested_by_member_id = $2), \
            (SELECT count(*) FROM members WHERE space_id = $3 \
                AND display_name = 'Approved Child'), \
            (SELECT count(*) FROM computer_commands \
                WHERE payload_json->>'name' = 'Approved Child'), \
            (SELECT count(*) FROM inbox_items WHERE approval_id = $1 \
                AND kind = 'approval' AND status = 'pending'), \
            (SELECT count(*) FROM audit_events WHERE subject_id = $1 \
                AND action = 'approval.created'), \
            (SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 \
                AND topic = 'approval.created'), \
            (SELECT count(*) FROM idempotency_records WHERE scope = $4 \
                AND response_status = 201)",
    )
    .bind(approved_id)
    .bind(harness.agent_id)
    .bind(harness.space.id)
    .bind(format!("member:{}:agent:create:approval", harness.agent_id))
    .fetch_one(&harness.pool)
    .await?;
    ensure!(pending_state == (1, 0, 0, 1, 1, 1, 1));
    ensure!(
        std::fs::read_dir(harness.computer_state.join("agents"))?.count() == 1,
        "pending Approval created an Agent Home"
    );

    let secrets: serde_json::Value =
        serde_json::from_slice(&std::fs::read(harness.computer_state.join("secrets.json"))?)?;
    let computer_token = secrets["token"]
        .as_str()
        .context("Computer Token missing from local secrets")?;
    let computer_cannot_approve = harness
        .client
        .post(
            harness
                .server_url
                .join(&format!("/api/v1/approvals/{approved_id}/approve"))?,
        )
        .header("idempotency-key", Uuid::now_v7().to_string())
        .bearer_auth(computer_token)
        .json(&serde_json::json!({}))
        .send()
        .await?;
    ensure!(computer_cannot_approve.status() == StatusCode::UNAUTHORIZED);

    harness.daemon.interrupt().await?;
    wait_for_computer_status_for(
        &harness.client,
        &harness.server_url,
        &harness.cookie,
        harness.space.id,
        "offline",
        Duration::from_secs(40),
    )
    .await?;
    let offline_approve = harness
        .client
        .post(
            harness
                .server_url
                .join(&format!("/api/v1/approvals/{approved_id}/approve"))?,
        )
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({}))
        .send()
        .await?;
    ensure!(offline_approve.status() == StatusCode::CONFLICT);
    let offline_state: (String, i64, i64) = sqlx::query_as(
        "SELECT status, \
            (SELECT count(*) FROM members WHERE space_id = $2 \
                AND display_name = 'Approved Child'), \
            (SELECT count(*) FROM computer_commands \
                WHERE payload_json->>'name' = 'Approved Child') \
         FROM approvals WHERE id = $1",
    )
    .bind(approved_id)
    .bind(harness.space.id)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(offline_state == ("pending".to_owned(), 0, 0));

    harness.daemon = spawn_computer(&harness.computer_config)?;
    wait_for_computer_status(
        &harness.client,
        &harness.server_url,
        &harness.cookie,
        harness.space.id,
        "online",
    )
    .await?;
    let approve = harness
        .client
        .post(
            harness
                .server_url
                .join(&format!("/api/v1/approvals/{approved_id}/approve"))?,
        )
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({}))
        .send()
        .await?;
    ensure!(approve.status() == StatusCode::OK);
    let approved: serde_json::Value = approve.json().await?;
    ensure!(approved["status"] == "approved");
    let child_id: Uuid = sqlx::query_scalar(
        "SELECT members.id FROM members JOIN agents ON agents.member_id = members.id \
         WHERE members.space_id = $1 AND members.display_name = 'Approved Child'",
    )
    .bind(harness.space.id)
    .fetch_one(&harness.pool)
    .await?;
    wait_for_agent_active(&harness.pool, child_id).await?;
    ensure!(
        harness
            .computer_state
            .join("agents")
            .join(child_id.to_string())
            .join("profile.json")
            .is_file(),
        "approved Agent was not provisioned into a real Agent Home"
    );

    let owner_id: Uuid =
        sqlx::query_scalar("SELECT id FROM members WHERE space_id = $1 AND access_level = 'owner'")
            .bind(harness.space.id)
            .fetch_one(&harness.pool)
            .await?;
    let approved_state: (i64, i64, i64, i64, i64, i64) = sqlx::query_as(
        "SELECT \
            (SELECT count(*) FROM approvals WHERE id = $1 AND status = 'approved' \
                AND resolved_by_member_id = $2), \
            (SELECT count(*) FROM inbox_items WHERE approval_id = $1 \
                AND status = 'handled'), \
            (SELECT count(*) FROM agents WHERE member_id = $3 AND status = 'active'), \
            (SELECT count(*) FROM computer_commands WHERE computer_id = $4 \
                AND kind = 'agent.provision' AND payload_json->>'agent_id' = $3::text \
                AND status = 'completed'), \
            (SELECT count(*) FROM audit_events WHERE subject_id = $1 \
                AND action = 'approval.resolved'), \
            (SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 \
                AND topic = 'approval.resolved')",
    )
    .bind(approved_id)
    .bind(owner_id)
    .bind(child_id)
    .bind(harness.computer_id)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(approved_state == (1, 1, 1, 1, 1, 1));

    send_channel_mention(
        &harness.client,
        &harness.server_url,
        &harness.cookie,
        harness.space.general_channel_id,
        harness.agent_id,
        "@lin request the rejected child Agent through the CLI.",
    )
    .await?;
    let (rejected_item_id, rejected_id) = match provider.wait_for_event().await? {
        AgentApprovalEvent::Requested {
            inbox_item_id,
            approval_id,
            name: "Rejected Child",
        } => (inbox_item_id, approval_id),
        AgentApprovalEvent::Failed(reason) => anyhow::bail!(reason),
        event => anyhow::bail!("expected rejected child request, got {event:?}"),
    };
    wait_for_mention_run(&harness.pool, rejected_item_id, "handled", 2).await?;
    let reject = harness
        .client
        .post(
            harness
                .server_url
                .join(&format!("/api/v1/approvals/{rejected_id}/reject"))?,
        )
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({}))
        .send()
        .await?;
    ensure!(reject.status() == StatusCode::OK);
    let rejected_state: (i64, i64, i64, i64, i64, i64) = sqlx::query_as(
        "SELECT \
            (SELECT count(*) FROM approvals WHERE id = $1 AND status = 'rejected' \
                AND resolved_by_member_id = $2), \
            (SELECT count(*) FROM members WHERE space_id = $3 \
                AND display_name = 'Rejected Child'), \
            (SELECT count(*) FROM computer_commands \
                WHERE payload_json->>'name' = 'Rejected Child'), \
            (SELECT count(*) FROM inbox_items WHERE approval_id = $1 \
                AND status = 'handled'), \
            (SELECT count(*) FROM audit_events WHERE subject_id = $1 \
                AND action = 'approval.resolved'), \
            (SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 \
                AND topic = 'approval.resolved')",
    )
    .bind(rejected_id)
    .bind(owner_id)
    .bind(harness.space.id)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(rejected_state == (1, 0, 0, 1, 1, 1));
    ensure!(
        std::fs::read_dir(harness.computer_state.join("agents"))?.count() == 2,
        "rejected Approval created an Agent Home"
    );

    harness.daemon.ensure_running()?;
    for text in [
        "request the approved child Agent",
        "request the rejected child Agent",
        PROVIDER_AUTH,
    ] {
        ensure!(
            !harness.server.logs_contain(text),
            "Server logs leaked run input"
        );
        ensure!(
            !harness.daemon.logs_contain(text),
            "daemon logs leaked run input"
        );
    }

    harness.shutdown().await
}

#[tokio::test]
async fn builtin_agent_creates_channels_with_permission_and_admin_through_real_cli() -> Result<()> {
    let mut provider = ChannelCreateProvider::start().await?;
    let mut harness = AgentProcessHarness::start(
        &provider.url,
        "sumi_agent_channel_create_test",
        "Create Channels only through the Sumi Agent CLI when authorized.",
    )
    .await?;
    provider.set_agent_id(harness.agent_id).await;
    disable_agent_ambient(&harness).await?;

    send_channel_mention(
        &harness.client,
        &harness.server_url,
        &harness.cookie,
        harness.space.general_channel_id,
        harness.agent_id,
        "@lin attempt the denied Channel create boundary.",
    )
    .await?;
    let denied_item_id = match provider.wait_for_event().await? {
        ChannelCreateEvent::Denied(item_id) => item_id,
        ChannelCreateEvent::Failed(reason) => anyhow::bail!(reason),
        event => anyhow::bail!("expected denied Channel create, got {event:?}"),
    };
    wait_for_mention_run(&harness.pool, denied_item_id, "handled", 1).await?;
    let denied_count: i64 =
        sqlx::query_scalar("SELECT count(*) FROM channels WHERE slug = 'agent-denied'")
            .fetch_one(&harness.pool)
            .await?;
    ensure!(denied_count == 0);

    let grant = harness
        .client
        .patch(harness.server_url.join(&format!(
            "/api/v1/spaces/{}/members/{}",
            harness.space.id, harness.agent_id
        ))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({ "permissions": ["channel:create"] }))
        .send()
        .await?;
    ensure!(grant.status() == StatusCode::OK);

    send_channel_mention(
        &harness.client,
        &harness.server_url,
        &harness.cookie,
        harness.space.general_channel_id,
        harness.agent_id,
        "@lin create the authorized private Channel.",
    )
    .await?;
    let (private_item_id, private_channel_id) = match provider.wait_for_event().await? {
        ChannelCreateEvent::Created {
            inbox_item_id,
            channel_id,
            kind: "private",
        } => (inbox_item_id, channel_id),
        ChannelCreateEvent::Failed(reason) => anyhow::bail!(reason),
        event => anyhow::bail!("expected private Channel create, got {event:?}"),
    };
    wait_for_mention_run(&harness.pool, private_item_id, "handled", 2).await?;

    let promote = harness
        .client
        .patch(harness.server_url.join(&format!(
            "/api/v1/spaces/{}/members/{}",
            harness.space.id, harness.agent_id
        ))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({ "access_level": "admin" }))
        .send()
        .await?;
    ensure!(promote.status() == StatusCode::OK);
    let explicit_permission_count: i64 =
        sqlx::query_scalar("SELECT count(*) FROM member_permissions WHERE member_id = $1")
            .bind(harness.agent_id)
            .fetch_one(&harness.pool)
            .await?;
    ensure!(explicit_permission_count == 0);

    send_channel_mention(
        &harness.client,
        &harness.server_url,
        &harness.cookie,
        harness.space.general_channel_id,
        harness.agent_id,
        "@lin create the Agent Admin public Channel.",
    )
    .await?;
    let (admin_item_id, public_channel_id) = match provider.wait_for_event().await? {
        ChannelCreateEvent::Created {
            inbox_item_id,
            channel_id,
            kind: "public",
        } => (inbox_item_id, channel_id),
        ChannelCreateEvent::Failed(reason) => anyhow::bail!(reason),
        event => anyhow::bail!("expected Agent Admin public Channel create, got {event:?}"),
    };
    wait_for_mention_run(&harness.pool, admin_item_id, "handled", 3).await?;

    let private_state: (Uuid, String, Uuid, i64, i64, i64) = sqlx::query_as(
        "SELECT space_id, kind, created_by_member_id, \
            (SELECT count(*) FROM channel_members WHERE channel_id = channels.id), \
            (SELECT count(*) FROM audit_events WHERE subject_id = channels.id \
                AND action = 'channel.created'), \
            (SELECT count(*) FROM outbox_events WHERE aggregate_id = channels.id \
                AND topic = 'channel.created') \
         FROM channels WHERE id = $1",
    )
    .bind(private_channel_id)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(
        private_state
            == (
                harness.space.id,
                "private".to_owned(),
                harness.agent_id,
                1,
                1,
                1,
            )
    );
    let public_state: (Uuid, String, Uuid, i64, i64, i64) = sqlx::query_as(
        "SELECT space_id, kind, created_by_member_id, \
            (SELECT count(*) FROM channel_members WHERE channel_id = channels.id), \
            (SELECT count(*) FROM audit_events WHERE subject_id = channels.id \
                AND action = 'channel.created'), \
            (SELECT count(*) FROM outbox_events WHERE aggregate_id = channels.id \
                AND topic = 'channel.created') \
         FROM channels WHERE id = $1",
    )
    .bind(public_channel_id)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(
        public_state
            == (
                harness.space.id,
                "public".to_owned(),
                harness.agent_id,
                1,
                1,
                1,
            )
    );
    let transaction_state: (i64, i64, i64) = sqlx::query_as(
        "SELECT \
            (SELECT count(*) FROM idempotency_records \
                WHERE scope = $1 AND response_status = 201), \
            (SELECT count(*) FROM inbox_items WHERE id = ANY($2) AND status = 'handled'), \
            (SELECT count(*) FROM agent_runs WHERE agent_member_id = $3 \
                AND status = 'completed')",
    )
    .bind(format!("space:{}:channel:create", harness.space.id))
    .bind(vec![denied_item_id, private_item_id, admin_item_id])
    .bind(harness.agent_id)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(transaction_state == (2, 3, 3));

    harness.daemon.ensure_running()?;
    for text in [
        "attempt the denied Channel create boundary",
        "create the authorized private Channel",
        "create the Agent Admin public Channel",
        PROVIDER_AUTH,
    ] {
        ensure!(
            !harness.server.logs_contain(text),
            "Server logs leaked run input"
        );
        ensure!(
            !harness.daemon.logs_contain(text),
            "daemon logs leaked run input"
        );
    }

    harness.shutdown().await
}

#[tokio::test]
async fn builtin_agent_admin_executes_governance_and_respects_human_private_boundaries()
-> Result<()> {
    let mut provider = GovernanceProvider::start().await?;
    let harness = AgentProcessHarness::start(
        &provider.url,
        "sumi_agent_admin_governance_test",
        "Exercise only the Agent Admin governance exposed by the Sumi Agent CLI.",
    )
    .await?;
    disable_agent_ambient(&harness).await?;

    let promote = harness
        .client
        .patch(harness.server_url.join(&format!(
            "/api/v1/spaces/{}/members/{}",
            harness.space.id, harness.agent_id
        ))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({ "access_level": "admin" }))
        .send()
        .await?;
    ensure!(promote.status() == StatusCode::OK);

    let target = harness
        .client
        .post(
            harness
                .server_url
                .join(&format!("/api/v1/spaces/{}/agents", harness.space.id))?,
        )
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({
            "computer_id": harness.computer_id,
            "name": "Governed Agent",
            "handle": "governed-agent",
            "role_text": "Remain idle while another Agent Admin manages lifecycle.",
            "access_level": "member",
            "driver_kind": "builtin"
        }))
        .send()
        .await?;
    ensure!(target.status() == StatusCode::CREATED);
    let target: serde_json::Value = target.json().await?;
    let target_agent_id = Uuid::parse_str(
        target["member_id"]
            .as_str()
            .context("target Agent id missing")?,
    )?;
    wait_for_agent_active(&harness.pool, target_agent_id).await?;
    sqlx::query(
        "UPDATE agents SET attention_config_json = jsonb_set( \
         attention_config_json, '{ambient_enabled}', 'false') WHERE member_id = $1",
    )
    .bind(target_agent_id)
    .execute(&harness.pool)
    .await?;

    let governance = harness
        .client
        .post(
            harness
                .server_url
                .join(&format!("/api/v1/spaces/{}/channels", harness.space.id))?,
        )
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({
            "slug": "governance",
            "name": "Governance",
            "kind": "private",
            "topic": null,
            "agent_member_ids": [harness.agent_id]
        }))
        .send()
        .await?;
    ensure!(governance.status() == StatusCode::CREATED);
    let governance: serde_json::Value = governance.json().await?;
    let governance_channel_id = Uuid::parse_str(
        governance["id"]
            .as_str()
            .context("governance Channel id missing")?,
    )?;

    let hidden = harness
        .client
        .post(
            harness
                .server_url
                .join(&format!("/api/v1/spaces/{}/channels", harness.space.id))?,
        )
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({
            "slug": "owners-private",
            "name": "Owners Private",
            "kind": "private",
            "topic": null,
            "agent_member_ids": []
        }))
        .send()
        .await?;
    ensure!(hidden.status() == StatusCode::CREATED);
    let hidden: serde_json::Value = hidden.json().await?;
    let hidden_channel_id =
        Uuid::parse_str(hidden["id"].as_str().context("hidden Channel id missing")?)?;
    provider.configure(target_agent_id, hidden_channel_id).await;

    send_channel_mention(
        &harness.client,
        &harness.server_url,
        &harness.cookie,
        harness.space.general_channel_id,
        harness.agent_id,
        "@lin execute the permitted Agent Admin governance through the CLI.",
    )
    .await?;
    let inbox_item_id = provider.wait_for_completion().await?;
    wait_for_mention_run(&harness.pool, inbox_item_id, "handled", 1).await?;

    let state: (String, String, String, i64, bool, bool, i64, i64, i64) = sqlx::query_as(
        "SELECT spaces.name, spaces.accent, agents.status, \
            (SELECT count(*) FROM channel_members WHERE channel_id = $3 AND member_id = $4), \
            (SELECT archived_at IS NOT NULL FROM channels WHERE id = $3), \
            (SELECT archived_at IS NULL FROM channels WHERE id = $5), \
            (SELECT count(*) FROM audit_events WHERE actor_member_id = $2 \
                AND action IN ('space.updated', 'channel.updated', 'agent.suspended', 'agent.resumed')), \
            (SELECT count(*) FROM outbox_events WHERE topic IN \
                ('space.updated', 'channel.updated', 'agent.status_changed')), \
            (SELECT count(*) FROM human_invitations WHERE invited_by_member_id = $2) \
         FROM spaces JOIN agents ON agents.member_id = $4 WHERE spaces.id = $1",
    )
    .bind(harness.space.id)
    .bind(harness.agent_id)
    .bind(governance_channel_id)
    .bind(target_agent_id)
    .bind(hidden_channel_id)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(state.0 == "Governed Space" && state.1 == "#A9D877" && state.2 == "active");
    ensure!(state.3 == 0 && state.4 && state.5);
    ensure!(state.6 >= 6 && state.7 >= 6 && state.8 == 0);

    let mut lifecycle_commands_completed = false;
    for _ in 0..100 {
        let count: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM computer_commands WHERE computer_id = $1 \
             AND kind IN ('agent.suspend', 'agent.resume') \
             AND payload_json->>'agent_id' = $2::text AND status = 'completed'",
        )
        .bind(harness.computer_id)
        .bind(target_agent_id)
        .fetch_one(&harness.pool)
        .await?;
        if count == 2 {
            lifecycle_commands_completed = true;
            break;
        }
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    ensure!(
        lifecycle_commands_completed,
        "daemon did not complete Agent Admin lifecycle commands"
    );
    harness.shutdown().await
}

#[tokio::test]
async fn builtin_agent_replies_acks_and_defers_channel_mentions_through_real_cli() -> Result<()> {
    let mut provider = ChannelMentionProvider::start().await?;
    let mut harness = AgentProcessHarness::start(
        &provider.url,
        "sumi_agent_channel_mention_test",
        "Handle each Channel mention through the Sumi Agent CLI.",
    )
    .await?;
    provider.set_agent_id(harness.agent_id).await;

    let reply_source = send_channel_mention(
        &harness.client,
        &harness.server_url,
        &harness.cookie,
        harness.space.general_channel_id,
        harness.agent_id,
        CHANNEL_REPLY_MENTION,
    )
    .await?;
    let (reply_item_id, reply) = match provider.wait_for_event().await? {
        ChannelMentionEvent::Replied {
            inbox_item_id,
            reply,
        } => (inbox_item_id, reply),
        ChannelMentionEvent::Failed(reason) => anyhow::bail!(reason),
        event => anyhow::bail!("expected Channel mention reply, got {event:?}"),
    };
    ensure!(
        reply.channel_id == harness.space.general_channel_id
            && reply.author_id == harness.agent_id
            && reply.address == "#general"
            && reply.seq == 2
    );
    wait_for_mention_run(&harness.pool, reply_item_id, "handled", 1).await?;

    let ack_source = send_channel_mention(
        &harness.client,
        &harness.server_url,
        &harness.cookie,
        harness.space.general_channel_id,
        harness.agent_id,
        CHANNEL_ACK_MENTION,
    )
    .await?;
    let ack_item_id = match provider.wait_for_event().await? {
        ChannelMentionEvent::Acked(item_id) => item_id,
        event => anyhow::bail!("expected Channel mention ack, got {event:?}"),
    };
    wait_for_mention_run(&harness.pool, ack_item_id, "handled", 2).await?;

    let defer_source = send_channel_mention(
        &harness.client,
        &harness.server_url,
        &harness.cookie,
        harness.space.general_channel_id,
        harness.agent_id,
        CHANNEL_DEFER_MENTION,
    )
    .await?;
    let defer_item_id = match provider.wait_for_event().await? {
        ChannelMentionEvent::Deferred(item_id) => item_id,
        event => anyhow::bail!("expected Channel mention defer, got {event:?}"),
    };
    wait_for_mention_run(&harness.pool, defer_item_id, "deferred", 3).await?;

    assert_channel_mention_state(
        &harness.pool,
        harness.space.general_channel_id,
        harness.agent_id,
        [reply_source, ack_source, defer_source],
        [reply_item_id, ack_item_id, defer_item_id],
        reply.id,
    )
    .await?;
    harness.daemon.ensure_running()?;
    for text in [
        CHANNEL_REPLY_MENTION,
        CHANNEL_ACK_MENTION,
        CHANNEL_DEFER_MENTION,
        CHANNEL_CLI_REPLY,
        PROVIDER_AUTH,
    ] {
        ensure!(
            !harness.server.logs_contain(text),
            "Server logs leaked Channel mention data"
        );
        ensure!(
            !harness.daemon.logs_contain(text),
            "daemon logs leaked Channel mention data"
        );
    }

    harness.shutdown().await
}

#[tokio::test]
async fn agent_admin_cannot_cross_private_computer_home_or_run_boundaries() -> Result<()> {
    let mut provider = PermissionBoundaryProvider::start().await?;
    let harness = AgentProcessHarness::start(
        &provider.url,
        "sumi_agent_permission_boundary_test",
        "Verify authorization boundaries through the Sumi Agent CLI.",
    )
    .await?;

    let private_channel = harness
        .client
        .post(
            harness
                .server_url
                .join(&format!("/api/v1/spaces/{}/channels", harness.space.id))?,
        )
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({
            "slug": "owners-private",
            "name": "Owners Private",
            "kind": "private",
            "topic": null,
            "agent_member_ids": []
        }))
        .send()
        .await?;
    ensure!(private_channel.status() == StatusCode::CREATED);
    let private_channel: serde_json::Value = private_channel.json().await?;
    let private_channel_id = Uuid::parse_str(
        private_channel["id"]
            .as_str()
            .context("private Channel id missing")?,
    )?;
    let private_message = harness
        .client
        .post(
            harness
                .server_url
                .join(&format!("/api/v1/channels/{private_channel_id}/messages"))?,
        )
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({
            "body_markdown": PRIVATE_CHANNEL_BODY,
            "mentions": [],
            "attachment_ids": []
        }))
        .send()
        .await?;
    ensure!(private_message.status() == StatusCode::CREATED);

    let promote = harness
        .client
        .patch(harness.server_url.join(&format!(
            "/api/v1/spaces/{}/members/{}",
            harness.space.id, harness.agent_id
        ))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({ "access_level": "admin" }))
        .send()
        .await?;
    ensure!(promote.status() == StatusCode::OK);

    let other_agent = harness
        .client
        .post(
            harness
                .server_url
                .join(&format!("/api/v1/spaces/{}/agents", harness.space.id))?,
        )
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({
            "computer_id": harness.computer_id,
            "name": "Mira",
            "handle": "mira",
            "role_text": "Own a distinct Agent Home for the isolation test.",
            "access_level": "member",
            "driver_kind": "builtin"
        }))
        .send()
        .await?;
    ensure!(other_agent.status() == StatusCode::CREATED);
    let other_agent: serde_json::Value = other_agent.json().await?;
    let other_agent_id = Uuid::parse_str(
        other_agent["member_id"]
            .as_str()
            .context("other Agent id missing")?,
    )?;
    wait_for_agent_active(&harness.pool, other_agent_id).await?;
    let other_agent_marker = harness
        .computer_state
        .join("agents")
        .join(other_agent_id.to_string())
        .join("memory")
        .join("isolation-marker");
    tokio::fs::write(&other_agent_marker, "other-agent-private-memory").await?;
    provider
        .set_other_agent_marker(other_agent_marker.clone())
        .await;

    let other_computer_id = Uuid::now_v7();
    let other_computer_token = "test-only-other-computer-token";
    sqlx::query(
        "INSERT INTO computers \
         (id, space_id, name, hostname, os, token_hash, status, daemon_version, \
          last_seen_at, created_at) \
         VALUES ($1, $2, 'Other Computer', 'other.local', 'macos', $3, 'online', \
                 '0.1.0', now(), now())",
    )
    .bind(other_computer_id)
    .bind(harness.space.id)
    .bind(Sha256::digest(other_computer_token.as_bytes()).to_vec())
    .execute(&harness.pool)
    .await?;
    let cross_computer = harness
        .client
        .post(harness.server_url.join(&format!(
            "/api/v1/computers/{}/agent-actions",
            harness.computer_id
        ))?)
        .bearer_auth(other_computer_token)
        .json(&serde_json::json!({
            "agent_member_id": harness.agent_id,
            "run_id": Uuid::now_v7(),
            "action": { "action": "channel_list" }
        }))
        .send()
        .await?;
    ensure!(cross_computer.status() == StatusCode::UNAUTHORIZED);

    let source_message_id = send_channel_mention(
        &harness.client,
        &harness.server_url,
        &harness.cookie,
        harness.space.general_channel_id,
        harness.agent_id,
        PERMISSION_BOUNDARY_MENTION,
    )
    .await?;
    let inbox_item_id = provider.wait_for_completion().await?;
    wait_for_mention_run(&harness.pool, inbox_item_id, "handled", 1).await?;

    let invariants: (String, i64, i64, i64, i64) = sqlx::query_as(
        "SELECT members.access_level, \
         (SELECT count(*) FROM channel_members WHERE channel_id = $2 AND member_id = $1), \
         (SELECT count(*) FROM messages WHERE channel_id = $2 AND body_markdown = $3), \
         (SELECT count(*) FROM inbox_items WHERE id = $4 AND message_id = $5 \
            AND status = 'handled'), \
         (SELECT count(*) FROM agent_runs WHERE agent_member_id = $1) \
         FROM members WHERE members.id = $1",
    )
    .bind(harness.agent_id)
    .bind(private_channel_id)
    .bind(PRIVATE_CHANNEL_BODY)
    .bind(inbox_item_id)
    .bind(source_message_id)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(invariants == ("admin".to_owned(), 0, 1, 1, 1));
    ensure!(other_agent_marker.exists());
    for text in [
        PRIVATE_CHANNEL_BODY,
        "other-agent-private-memory",
        PROVIDER_AUTH,
    ] {
        ensure!(!harness.server.logs_contain(text));
        ensure!(!harness.daemon.logs_contain(text));
    }

    harness.shutdown().await
}

#[tokio::test]
async fn builtin_agent_refreshes_changed_thread_and_replies_through_real_cli() -> Result<()> {
    let mut provider = ThreadProvider::start().await?;
    let mut harness = AgentProcessHarness::start(
        &provider.url,
        "sumi_agent_thread_test",
        "Read Thread context and reply in the same Thread through the Sumi Agent CLI.",
    )
    .await?;
    provider.set_agent_id(harness.agent_id).await;

    disable_agent_ambient(&harness).await?;
    let background = send_channel_message(&harness, THREAD_BACKGROUND, &[]).await?;
    let root = send_channel_message(&harness, THREAD_ROOT, &[]).await?;
    ensure!(background.1 == 1 && root.1 == 2);

    let thread_response = harness
        .client
        .post(harness.server_url.join(&format!(
            "/api/v1/channels/{}/threads",
            harness.space.general_channel_id
        ))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({ "root_message_id": root.0 }))
        .send()
        .await?;
    ensure!(thread_response.status() == StatusCode::CREATED);
    let thread: serde_json::Value = thread_response.json().await?;
    let thread_id = thread["thread_id"]
        .as_i64()
        .context("created Thread id missing")?;
    ensure!(thread_id == 1 && thread["root_message_id"] == root.0.to_string());

    let first_reply = send_thread_message(&harness, thread_id, THREAD_FIRST_REPLY, &[]).await?;
    let mention_reply =
        send_thread_message(&harness, thread_id, THREAD_MENTION, &[harness.agent_id]).await?;
    ensure!(first_reply.1 == 3 && mention_reply.1 == 4);

    match provider.wait_for_event().await? {
        ThreadEvent::ContextRead => {}
        ThreadEvent::Failed(reason) => anyhow::bail!(reason),
        event => anyhow::bail!("expected initial Thread context read, got {event:?}"),
    }
    let freshness_reply =
        send_thread_message(&harness, thread_id, THREAD_FRESHNESS_REPLY, &[]).await?;
    ensure!(freshness_reply.1 == 5);
    provider.context_changed();

    let (inbox_item_id, reply) = match provider.wait_for_event().await? {
        ThreadEvent::Replied {
            inbox_item_id,
            reply,
        } => (inbox_item_id, reply),
        ThreadEvent::ContextRead => anyhow::bail!("Thread context was read more than once"),
        ThreadEvent::Failed(reason) => anyhow::bail!(reason),
    };
    ensure!(
        reply.channel_id == harness.space.general_channel_id
            && reply.author_id == harness.agent_id
            && reply.address == "#general:1"
            && reply.seq == 6
    );
    wait_for_mention_run(&harness.pool, inbox_item_id, "handled", 1).await?;

    let browser_thread = harness
        .client
        .get(harness.server_url.join(&format!(
            "/api/v1/channels/{}/threads/{thread_id}",
            harness.space.general_channel_id
        ))?)
        .header(header::COOKIE, &harness.cookie)
        .send()
        .await?;
    ensure!(browser_thread.status() == StatusCode::OK);
    let browser_thread: serde_json::Value = browser_thread.json().await?;
    let replies = browser_thread["replies"]
        .as_array()
        .context("Browser Thread replies missing")?;
    ensure!(browser_thread["snapshot_channel_seq"].as_i64() == Some(6));
    ensure!(browser_thread["root"]["id"] == root.0.to_string());
    ensure!(replies.len() == 4);
    let browser_reply = replies
        .iter()
        .find(|message| message["id"] == reply.id.to_string())
        .context("Browser Thread did not return the Agent reply")?;
    ensure!(browser_reply["thread_id"].as_i64() == Some(thread_id));
    ensure!(browser_reply["author"]["id"] == harness.agent_id.to_string());

    let state: (i64, i64, i64, i64, i64, bool) = sqlx::query_as(
        "SELECT channels.next_seq, \
            (SELECT count(*) FROM messages WHERE channel_id = $1 AND thread_id IS NULL), \
            (SELECT count(*) FROM messages WHERE channel_id = $1 AND thread_id = $2), \
            (SELECT count(*) FROM messages WHERE id = $3 AND channel_id = $1 \
                AND thread_id = $2 AND author_member_id = $4), \
            (SELECT count(*) FROM inbox_items WHERE id = $5 AND member_id = $4 \
                AND channel_id = $1 AND thread_id = $2 AND kind = 'mention' \
                AND priority = 'hard' AND status = 'handled'), \
            EXISTS(SELECT 1 FROM thread_subscriptions WHERE channel_id = $1 AND thread_id = $2 \
                AND member_id = $4 AND muted_at IS NULL) \
         FROM channels WHERE channels.id = $1",
    )
    .bind(harness.space.general_channel_id)
    .bind(thread_id)
    .bind(reply.id)
    .bind(harness.agent_id)
    .bind(inbox_item_id)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(state == (7, 2, 4, 1, 1, true));
    let stale_message_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM messages WHERE channel_id = $1 AND author_member_id = $2 \
         AND body_markdown = $3",
    )
    .bind(harness.space.general_channel_id)
    .bind(harness.agent_id)
    .bind(THREAD_STALE_REPLY)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(stale_message_count == 0);

    harness.daemon.ensure_running()?;
    for text in [
        THREAD_BACKGROUND,
        THREAD_ROOT,
        THREAD_FIRST_REPLY,
        THREAD_MENTION,
        THREAD_FRESHNESS_REPLY,
        THREAD_STALE_REPLY,
        THREAD_CLI_REPLY,
        PROVIDER_AUTH,
    ] {
        ensure!(
            !harness.server.logs_contain(text),
            "Server logs leaked Thread data"
        );
        ensure!(
            !harness.daemon.logs_contain(text),
            "daemon logs leaked Thread data"
        );
    }

    harness.shutdown().await
}

async fn disable_agent_ambient(harness: &AgentProcessHarness) -> Result<()> {
    let response = harness
        .client
        .patch(
            harness
                .server_url
                .join(&format!("/api/v1/agents/{}", harness.agent_id))?,
        )
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({
            "attention_config": {
                "dm_immediate": true,
                "mention_immediate": true,
                "ambient_enabled": false,
                "ambient_debounce_seconds": 5,
                "ambient_max_wait_seconds": 30,
                "max_retry_count": 3
            }
        }))
        .send()
        .await?;
    ensure!(response.status() == StatusCode::OK);
    Ok(())
}

async fn send_channel_message(
    harness: &AgentProcessHarness,
    body: &str,
    mentions: &[Uuid],
) -> Result<(Uuid, i64)> {
    let response = harness
        .client
        .post(harness.server_url.join(&format!(
            "/api/v1/channels/{}/messages",
            harness.space.general_channel_id
        ))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({
            "body_markdown": body,
            "mentions": mentions,
            "attachment_ids": []
        }))
        .send()
        .await?;
    ensure!(response.status() == StatusCode::CREATED);
    let message: serde_json::Value = response.json().await?;
    Ok((
        Uuid::parse_str(
            message["id"]
                .as_str()
                .context("Channel Message id missing")?,
        )?,
        message["seq"]
            .as_i64()
            .context("Channel Message sequence missing")?,
    ))
}

async fn send_thread_message(
    harness: &AgentProcessHarness,
    thread_id: i64,
    body: &str,
    mentions: &[Uuid],
) -> Result<(Uuid, i64)> {
    let response = harness
        .client
        .post(harness.server_url.join(&format!(
            "/api/v1/channels/{}/threads/{thread_id}/messages",
            harness.space.general_channel_id
        ))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &harness.cookie)
        .json(&serde_json::json!({
            "body_markdown": body,
            "mentions": mentions,
            "attachment_ids": [],
            "reply_to_message_id": null
        }))
        .send()
        .await?;
    ensure!(response.status() == StatusCode::CREATED);
    let message: serde_json::Value = response.json().await?;
    Ok((
        Uuid::parse_str(
            message["id"]
                .as_str()
                .context("Thread Message id missing")?,
        )?,
        message["seq"]
            .as_i64()
            .context("Thread Message sequence missing")?,
    ))
}

#[derive(sqlx::FromRow)]
struct ChannelAmbientInboxState {
    id: Uuid,
    first_seq: i64,
    last_seq: i64,
    message_count: i32,
    status: String,
    message_id: Option<Uuid>,
    available_at: time::OffsetDateTime,
    created_at: time::OffsetDateTime,
}

#[tokio::test]
async fn builtin_agent_acks_aggregated_channel_ambient_once_through_real_cli() -> Result<()> {
    let mut provider = ChannelAmbientProvider::start().await?;
    let mut harness = AgentProcessHarness::start(
        &provider.url,
        "sumi_agent_channel_ambient_test",
        "Review ordinary Channel activity and stay silent when no response is useful.",
    )
    .await?;

    let mut message_ids = Vec::new();
    for body in CHANNEL_AMBIENT_MESSAGES {
        let response = harness
            .client
            .post(harness.server_url.join(&format!(
                "/api/v1/channels/{}/messages",
                harness.space.general_channel_id
            ))?)
            .header("idempotency-key", Uuid::now_v7().to_string())
            .header(header::COOKIE, &harness.cookie)
            .json(&serde_json::json!({
                "body_markdown": body,
                "mentions": [],
                "attachment_ids": []
            }))
            .send()
            .await?;
        ensure!(response.status() == StatusCode::CREATED);
        let message: serde_json::Value = response.json().await?;
        message_ids.push(Uuid::parse_str(
            message["id"]
                .as_str()
                .context("ambient Message id missing")?,
        )?);
    }

    let pending: ChannelAmbientInboxState = sqlx::query_as(
        "SELECT id, first_seq, last_seq, message_count, status, message_id, available_at, \
             created_at FROM inbox_items WHERE member_id = $1 AND channel_id = $2 \
             AND kind = 'channel_activity' AND priority = 'ambient'",
    )
    .bind(harness.agent_id)
    .bind(harness.space.general_channel_id)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(
        pending.first_seq == 1
            && pending.last_seq == 5
            && pending.message_count == 5
            && pending.status == "pending"
            && pending.message_id == message_ids.last().copied()
            && pending.available_at == pending.created_at + time::Duration::seconds(5)
    );

    let acked_item_id = match provider.wait_for_event().await? {
        ChannelAmbientEvent::Acked(item_id) => item_id,
        ChannelAmbientEvent::Failed(reason) => anyhow::bail!(reason),
    };
    ensure!(acked_item_id == pending.id);
    wait_for_ambient_run(&harness.pool, harness.agent_id, pending.id).await?;

    tokio::time::sleep(Duration::from_secs(2)).await;
    let state: (i64, i64, i64, i64, i64) = sqlx::query_as(
        "SELECT channels.next_seq, \
            (SELECT count(*) FROM messages WHERE channel_id = $1), \
            (SELECT count(*) FROM messages WHERE channel_id = $1 AND author_member_id = $2), \
            (SELECT count(*) FROM agent_runs WHERE agent_member_id = $2), \
            (SELECT count(*) FROM agent_run_inbox_items links \
                JOIN inbox_items items ON items.id = links.inbox_item_id \
                WHERE items.member_id = $2 AND items.channel_id = $1) \
         FROM channels WHERE channels.id = $1",
    )
    .bind(harness.space.general_channel_id)
    .bind(harness.agent_id)
    .fetch_one(&harness.pool)
    .await?;
    ensure!(state == (6, 5, 0, 1, 1));
    harness.daemon.ensure_running()?;
    for text in CHANNEL_AMBIENT_MESSAGES
        .into_iter()
        .chain(std::iter::once(PROVIDER_AUTH))
    {
        ensure!(
            !harness.server.logs_contain(text),
            "Server logs leaked Channel ambient data"
        );
        ensure!(
            !harness.daemon.logs_contain(text),
            "daemon logs leaked Channel ambient data"
        );
    }

    harness.shutdown().await
}

async fn wait_for_ambient_run(pool: &sqlx::PgPool, agent_id: Uuid, item_id: Uuid) -> Result<()> {
    tokio::time::timeout(Duration::from_secs(20), async {
        loop {
            let state: (String, Option<Uuid>, i32, String, i64) = sqlx::query_as(
                "SELECT items.status, items.handled_by_run_id, items.retry_count, runs.status, \
                    (SELECT count(*) FROM agent_runs WHERE agent_member_id = $2) \
                 FROM inbox_items items \
                 JOIN agent_run_inbox_items links ON links.inbox_item_id = items.id \
                 JOIN agent_runs runs ON runs.id = links.run_id WHERE items.id = $1",
            )
            .bind(item_id)
            .bind(agent_id)
            .fetch_one(pool)
            .await?;
            if state.0 == "handled"
                && state.1.is_some()
                && state.2 == 0
                && state.3 == "completed"
                && state.4 == 1
            {
                return Ok::<_, anyhow::Error>(());
            }
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
    })
    .await
    .context("Channel ambient Item did not finish once through the real Agent CLI")?
}

async fn send_channel_mention(
    client: &Client,
    server_url: &Url,
    cookie: &str,
    channel_id: Uuid,
    agent_id: Uuid,
    body: &str,
) -> Result<Uuid> {
    let response = client
        .post(server_url.join(&format!("/api/v1/channels/{channel_id}/messages"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({
            "body_markdown": body,
            "mentions": [agent_id],
            "attachment_ids": []
        }))
        .send()
        .await?;
    ensure!(response.status() == StatusCode::CREATED);
    let message: serde_json::Value = response.json().await?;
    Ok(Uuid::parse_str(
        message["id"]
            .as_str()
            .context("mention Message id missing")?,
    )?)
}

async fn wait_for_mention_run(
    pool: &sqlx::PgPool,
    item_id: Uuid,
    expected_item_status: &str,
    expected_run_count: i64,
) -> Result<()> {
    tokio::time::timeout(Duration::from_secs(20), async {
        loop {
            let state: (String, String, i64) = sqlx::query_as(
                "SELECT items.status, runs.status, \
                    (SELECT count(*) FROM agent_runs WHERE agent_member_id = items.member_id) \
                 FROM inbox_items items \
                 JOIN agent_run_inbox_items links ON links.inbox_item_id = items.id \
                 JOIN agent_runs runs ON runs.id = links.run_id WHERE items.id = $1",
            )
            .bind(item_id)
            .fetch_one(pool)
            .await?;
            if state
                == (
                    expected_item_status.to_owned(),
                    "completed".to_owned(),
                    expected_run_count,
                )
            {
                return Ok::<_, anyhow::Error>(());
            }
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
    })
    .await
    .with_context(|| format!("Channel mention Item {item_id} did not finish as expected"))?
}

async fn assert_channel_mention_state(
    pool: &sqlx::PgPool,
    channel_id: Uuid,
    agent_id: Uuid,
    source_message_ids: [Uuid; 3],
    item_ids: [Uuid; 3],
    reply_id: Uuid,
) -> Result<()> {
    let channel_state: (i64, i64, i64, i64, i64) = sqlx::query_as(
        "SELECT channels.next_seq, \
            (SELECT count(*) FROM messages WHERE channel_id = $1), \
            (SELECT count(*) FROM message_mentions mentions JOIN messages \
                ON messages.id = mentions.message_id WHERE messages.channel_id = $1 \
                AND mentions.member_id = $2), \
            (SELECT count(*) FROM agent_runs WHERE agent_member_id = $2 AND status = 'completed'), \
            (SELECT count(*) FROM outbox_events WHERE topic = 'message.created' \
                AND payload_json->>'channel_id' = $1::text) \
         FROM channels WHERE channels.id = $1",
    )
    .bind(channel_id)
    .bind(agent_id)
    .fetch_one(pool)
    .await?;
    ensure!(channel_state == (5, 4, 3, 3, 4));

    let message_rows: Vec<(Uuid, i64, Uuid, Vec<Uuid>)> = sqlx::query_as(
        "SELECT messages.id, messages.channel_seq, messages.author_member_id, \
            COALESCE(array_agg(message_mentions.member_id ORDER BY message_mentions.member_id) \
                FILTER (WHERE message_mentions.member_id IS NOT NULL), ARRAY[]::uuid[]) \
         FROM messages LEFT JOIN message_mentions ON message_mentions.message_id = messages.id \
         WHERE messages.channel_id = $1 GROUP BY messages.id ORDER BY messages.channel_seq",
    )
    .bind(channel_id)
    .fetch_all(pool)
    .await?;
    ensure!(
        message_rows.len() == 4
            && message_rows[0] == (source_message_ids[0], 1, message_rows[0].2, vec![agent_id])
            && message_rows[1] == (reply_id, 2, agent_id, Vec::new())
            && message_rows[2] == (source_message_ids[1], 3, message_rows[2].2, vec![agent_id])
            && message_rows[3] == (source_message_ids[2], 4, message_rows[3].2, vec![agent_id])
            && message_rows[0].2 == message_rows[2].2
            && message_rows[2].2 == message_rows[3].2
            && message_rows[0].2 != agent_id
    );

    for (index, item_id) in item_ids.into_iter().enumerate() {
        let state: ChannelMentionInboxState = sqlx::query_as(
            "SELECT items.kind, items.priority, items.retry_count, items.lease_id, \
                items.handled_by_run_id, items.available_at, \
                (SELECT count(*) FROM agent_run_inbox_items \
                    WHERE inbox_item_id = items.id) AS run_link_count, \
                (SELECT count(*) FROM outbox_events WHERE topic = 'inbox.changed' \
                    AND aggregate_id = items.id) AS outbox_count \
             FROM inbox_items items WHERE items.id = $1",
        )
        .bind(item_id)
        .fetch_one(pool)
        .await?;
        ensure!(state.kind == "mention" && state.priority == "hard" && state.retry_count == 0);
        ensure!(state.lease_id.is_none() && state.run_link_count == 1 && state.outbox_count == 1);
        if index < 2 {
            ensure!(state.handled_by_run_id.is_some());
        } else {
            ensure!(state.handled_by_run_id.is_none());
            ensure!(
                state.available_at
                    == Some(time::OffsetDateTime::parse(
                        CHANNEL_DEFER_UNTIL,
                        &time::format_description::well_known::Rfc3339,
                    )?)
            );
        }
    }
    Ok(())
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
