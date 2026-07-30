use std::{collections::BTreeSet, sync::Arc};

use anyhow::Context;
use argon2::{Argon2, PasswordHash, PasswordHasher, PasswordVerifier, password_hash::SaltString};
use axum::{
    Json, Router,
    body::Bytes,
    extract::{
        Path, Query, State, WebSocketUpgrade,
        ws::{Message as WebSocketMessage, WebSocket},
    },
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use axum_extra::extract::cookie::{Cookie, CookieJar, SameSite};
use futures_util::StreamExt;
use serde::Deserialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row, postgres::PgPoolOptions};
use time::{Duration, OffsetDateTime};
use tower_http::{services::ServeDir, trace::TraceLayer};
use uuid::Uuid;

use crate::config::ServerConfig;
use crate::{
    ids::{
        AgentId, CommandId, ComputerId, InboxItemId, MemberId, MessageId, RunId, SpaceId, TaskId,
        ThreadId,
    },
    protocol::{
        capability,
        computer::{
            AgentConfiguration, Command as ComputerCommand, ComputerFrame, ComputerHello,
            DriverKind as ComputerDriverKind, RoleSnapshot, ServerFrame,
        },
    },
    server::{
        application::conversation::{
            CreateAgentAction, CreateAgentActionInput, CreateChannelAction,
            CreateChannelActionInput,
        },
        application::task::{
            CompleteTask, CompleteTaskInput, CreateTaskFromRootMessage, CreateTaskInput,
            FinishAgentTaskAction, FinishAgentTaskInput, FinishAgentTaskRun, LinkThreadInput,
            LinkThreadToTask, TaskAction, TaskPostTarget, TaskSource, UnlinkThreadFromTask,
            UpdateTask, UpdateTaskInput,
        },
        application::{
            execution::{
                ClaimRun, ClaimRunInput, RecordRunItemDisposition, RecordRunItemDispositionInput,
                RenewRun, RenewRunInput, StartRun, StartRunInput,
            },
            ports::{AttachmentObjectPort, RawFencingToken},
        },
        domain::{conversation::ChannelKind, identity::DriverKind, task::CloseReason},
    },
};

use super::{object_storage::AttachmentObjectStore, postgres::PostgresAdapter};

const SESSION_COOKIE: &str = "sumi_session";

#[derive(Clone)]
struct RuntimeState {
    pool: PgPool,
    storage: PostgresAdapter,
    objects: Arc<AttachmentObjectStore>,
    session_ttl_hours: i64,
}

#[derive(Debug)]
struct ApiError {
    status: StatusCode,
    code: &'static str,
    message: &'static str,
}

impl ApiError {
    fn unauthenticated() -> Self {
        Self {
            status: StatusCode::UNAUTHORIZED,
            code: "unauthenticated",
            message: "Browser Session is missing or expired",
        }
    }

    fn invalid(message: &'static str) -> Self {
        Self {
            status: StatusCode::BAD_REQUEST,
            code: "invalid_argument",
            message,
        }
    }

    fn not_found() -> Self {
        Self {
            status: StatusCode::NOT_FOUND,
            code: "not_found",
            message: "resource was not found",
        }
    }

    fn internal() -> Self {
        Self {
            status: StatusCode::INTERNAL_SERVER_ERROR,
            code: "internal",
            message: "Server could not complete the request",
        }
    }

    fn context_changed() -> Self {
        Self {
            status: StatusCode::CONFLICT,
            code: "context_changed",
            message: "Message context changed before the write",
        }
    }
}

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        (
            self.status,
            Json(json!({
                "error": {
                    "code": self.code,
                    "message": self.message,
                    "retryable": false
                }
            })),
        )
            .into_response()
    }
}

#[derive(Deserialize)]
struct RegisterBody {
    display_name: String,
    email: String,
    password: String,
}

#[derive(Deserialize)]
struct LoginBody {
    email: String,
    password: String,
}

#[derive(Deserialize)]
struct CreateSpaceBody {
    name: String,
    slug: String,
    #[serde(rename = "accent")]
    _accent: String,
}

#[derive(Deserialize)]
struct CreateChannelBody {
    name: String,
    slug: String,
    kind: String,
    topic: Option<String>,
    #[serde(default)]
    agent_member_ids: Vec<Uuid>,
}

#[derive(Deserialize)]
struct CreateAgentBody {
    computer_id: Uuid,
    name: String,
    handle: Option<String>,
    role_text: String,
    driver_kind: String,
    access_level: String,
}

#[derive(Deserialize)]
struct CreateMessageBody {
    body_markdown: String,
    #[serde(default)]
    mentions: Vec<Uuid>,
    #[serde(default)]
    attachment_ids: Vec<Uuid>,
    reply_to_message_id: Option<Uuid>,
}

#[derive(Deserialize)]
struct CreateTaskBody {
    title: Option<String>,
    assignee_agent_member_id: Option<Uuid>,
}

#[derive(Deserialize)]
struct StartTaskBody {
    assignee_agent_member_id: Uuid,
}

#[derive(Deserialize)]
struct LinkThreadBody {
    thread_id: Uuid,
}

#[derive(Deserialize)]
struct CompleteTaskBody {
    result_markdown: String,
    result_thread_id: Uuid,
}

#[derive(Deserialize)]
struct CloseTaskBody {
    reason: String,
    note: Option<String>,
}

#[derive(Deserialize)]
struct CreateUploadBody {
    space_id: Uuid,
    original_name: String,
    media_type: String,
}

#[derive(Deserialize)]
struct AgentCreateUploadBody {
    original_name: String,
    media_type: String,
}

#[derive(Deserialize)]
struct CompleteUploadBody {
    size: u64,
    sha256: String,
}

#[derive(Deserialize)]
struct BeginPairingBody {
    token_hash: String,
    hostname: String,
    os: String,
    daemon_version: String,
}

#[derive(Deserialize)]
struct PairingCodeQuery {
    code: String,
}

#[derive(Deserialize)]
struct ConfirmPairingBody {
    code: String,
    name: String,
    space_id: Uuid,
}

#[derive(Deserialize)]
struct AgentActionRequest {
    context: capability::RunContext,
    action: capability::Action,
    idempotency_key: Option<crate::ids::IdempotencyKey>,
}

pub(in crate::server) async fn run(config: ServerConfig) -> anyhow::Result<()> {
    let pool = PgPoolOptions::new()
        .max_connections(10)
        .connect(&config.database_url)
        .await
        .context("failed to connect to PostgreSQL")?;
    let storage = PostgresAdapter::new(pool.clone());
    storage
        .migrate()
        .await
        .map_err(|error| anyhow::anyhow!(error))?;

    tokio::fs::create_dir_all(&config.attachment_dir)
        .await
        .context("failed to create Attachment directory")?;
    let object_store =
        object_store::local::LocalFileSystem::new_with_prefix(&config.attachment_dir)
            .context("failed to open Attachment directory")?;
    let state = RuntimeState {
        pool,
        storage,
        objects: Arc::new(AttachmentObjectStore::new(Arc::new(object_store))),
        session_ttl_hours: config.session_ttl_hours,
    };
    let api = Router::new()
        .route("/health", get(|| async { "ok" }))
        .route("/auth/register", post(register))
        .route("/auth/login", post(login))
        .route("/auth/logout", post(logout))
        .route("/auth/me", get(current_user))
        .route("/computer-pairings", post(begin_pairing))
        .route("/computer-pairings/{pairing_id}", get(pairing_details))
        .route(
            "/computer-pairings/{pairing_id}/confirm",
            post(confirm_pairing),
        )
        .route(
            "/computer-pairings/{pairing_id}/status",
            get(pairing_status),
        )
        .route("/computers/{computer_id}/connect", get(connect_computer))
        .route("/computers/{computer_id}/runs/claim", post(claim_run))
        .route(
            "/computers/{computer_id}/runs/{run_id}/renew",
            post(renew_run),
        )
        .route("/computers/{computer_id}/agent-actions", post(agent_action))
        .route(
            "/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/uploads",
            post(agent_create_upload),
        )
        .route(
            "/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/{attachment_id}/content",
            axum::routing::put(agent_upload_content),
        )
        .route(
            "/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/{attachment_id}/complete",
            post(agent_complete_upload),
        )
        .route(
            "/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/{attachment_id}/download",
            get(agent_download_attachment),
        )
        .route("/spaces", get(list_spaces).post(create_space))
        .route("/spaces/by-slug/{slug}", get(space_by_slug))
        .route(
            "/spaces/{space_id}/channels",
            get(list_channels).post(create_channel),
        )
        .route("/spaces/{space_id}/members", get(list_members))
        .route("/spaces/{space_id}/computers", get(list_computers))
        .route("/spaces/{space_id}/dms", get(empty_list))
        .route(
            "/spaces/{space_id}/agents",
            get(list_agents).post(create_agent),
        )
        .route("/agents/{agent_id}", get(get_agent))
        .route("/spaces/{space_id}/approvals", get(empty_list))
        .route("/spaces/{space_id}/tasks", get(list_tasks))
        .route("/tasks/{task_id}", get(get_task))
        .route("/root-messages/{message_id}/task", post(create_task))
        .route("/tasks/{task_id}/threads", post(link_task_thread))
        .route("/tasks/{task_id}/start", post(start_task))
        .route("/tasks/{task_id}/submit-review", post(submit_task_review))
        .route(
            "/tasks/{task_id}/request-changes",
            post(request_task_changes),
        )
        .route("/tasks/{task_id}/done", post(complete_task))
        .route("/tasks/{task_id}/close", post(close_task))
        .route(
            "/channels/{channel_id}/messages",
            get(list_messages).post(create_root_message),
        )
        .route("/threads/{thread_id}", get(read_thread))
        .route("/threads/{thread_id}/messages", post(create_thread_reply))
        .route("/members/{member_id}/inbox", get(empty_list))
        .route("/attachments/uploads", post(create_upload))
        .route(
            "/attachments/{attachment_id}/content",
            axum::routing::put(upload_content),
        )
        .route(
            "/attachments/{attachment_id}/complete",
            post(complete_upload),
        )
        .route(
            "/attachments/{attachment_id}/download",
            get(download_attachment),
        )
        .with_state(state);
    let app = Router::new()
        .nest("/api/v1", api)
        .fallback_service(
            ServeDir::new(config.web_dist)
                .append_index_html_on_directories(true)
                .fallback(ServeDir::new("web/dist")),
        )
        .layer(TraceLayer::new_for_http());
    let listener = tokio::net::TcpListener::bind(config.bind)
        .await
        .with_context(|| format!("failed to bind Server at {}", config.bind))?;
    axum::serve(listener, app)
        .with_graceful_shutdown(shutdown_signal())
        .await
        .context("Server stopped unexpectedly")
}

async fn register(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Json(body): Json<RegisterBody>,
) -> Result<(CookieJar, (StatusCode, Json<Value>)), ApiError> {
    let display_name = body.display_name.trim();
    let email = body.email.trim().to_lowercase();
    if display_name.is_empty() || email.is_empty() || body.password.chars().count() < 12 {
        return Err(ApiError::invalid(
            "display name, email, and a password of at least 12 characters are required",
        ));
    }
    let user_id = Uuid::now_v7();
    let salt =
        SaltString::encode_b64(Uuid::now_v7().as_bytes()).map_err(|_| ApiError::internal())?;
    let password_hash = Argon2::default()
        .hash_password(body.password.as_bytes(), &salt)
        .map_err(|_| ApiError::internal())?
        .to_string();
    let now = OffsetDateTime::now_utc();
    sqlx::query(
        "INSERT INTO users(id,email_normalized,password_hash,display_name,created_at) \
         VALUES($1,$2,$3,$4,$5)",
    )
    .bind(user_id)
    .bind(&email)
    .bind(password_hash)
    .bind(display_name)
    .bind(now)
    .execute(&state.pool)
    .await
    .map_err(map_sqlx)?;
    let (jar, _) = create_session(&state, jar, user_id, now).await?;
    Ok((
        jar,
        (
            StatusCode::CREATED,
            Json(json!({
                "user": user_json(user_id, display_name, &email),
                "next": "create_space"
            })),
        ),
    ))
}

async fn login(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Json(body): Json<LoginBody>,
) -> Result<(CookieJar, Json<Value>), ApiError> {
    let email = body.email.trim().to_lowercase();
    let row = sqlx::query(
        "SELECT id,display_name,email_normalized,password_hash FROM users \
         WHERE email_normalized=$1 AND disabled_at IS NULL",
    )
    .bind(&email)
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::unauthenticated)?;
    let stored: String = row.get("password_hash");
    let parsed = PasswordHash::new(&stored).map_err(|_| ApiError::internal())?;
    Argon2::default()
        .verify_password(body.password.as_bytes(), &parsed)
        .map_err(|_| ApiError::unauthenticated())?;
    let user_id: Uuid = row.get("id");
    let (jar, _) = create_session(&state, jar, user_id, OffsetDateTime::now_utc()).await?;
    Ok((
        jar,
        Json(json!({
            "user": user_json(user_id, row.get("display_name"), row.get("email_normalized"))
        })),
    ))
}

async fn logout(State(state): State<RuntimeState>, jar: CookieJar) -> (CookieJar, StatusCode) {
    if let Some(cookie) = jar.get(SESSION_COOKIE) {
        let hash = token_hash(cookie.value());
        let _ = sqlx::query("DELETE FROM browser_sessions WHERE token_hash=$1")
            .bind(hash)
            .execute(&state.pool)
            .await;
    }
    (
        jar.remove(Cookie::from(SESSION_COOKIE)),
        StatusCode::NO_CONTENT,
    )
}

async fn current_user(
    State(state): State<RuntimeState>,
    jar: CookieJar,
) -> Result<Json<Value>, ApiError> {
    let user = authenticate(&state, &jar).await?;
    Ok(Json(user_json(user.id, &user.display_name, &user.email)))
}

async fn begin_pairing(
    State(state): State<RuntimeState>,
    Json(body): Json<BeginPairingBody>,
) -> Result<(StatusCode, Json<Value>), ApiError> {
    if body.token_hash.len() != 64
        || !body.token_hash.bytes().all(|byte| byte.is_ascii_hexdigit())
        || !matches!(body.os.as_str(), "macos" | "linux")
        || body.hostname.trim().is_empty()
    {
        return Err(ApiError::invalid("Computer pairing request is invalid"));
    }
    let pairing_id = Uuid::now_v7();
    let code = format!(
        "{:06}",
        u32::from_be_bytes(
            Uuid::now_v7().as_bytes()[..4]
                .try_into()
                .expect("four bytes")
        ) % 1_000_000
    );
    let now = OffsetDateTime::now_utc();
    let expires_at = now + Duration::minutes(10);
    sqlx::query("INSERT INTO computer_pairings(id,code_hash,token_hash,hostname,os,daemon_version,status,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6,'pending',$7,$8)")
        .bind(pairing_id).bind(token_hash(&code)).bind(body.token_hash.to_lowercase()).bind(body.hostname).bind(body.os).bind(body.daemon_version).bind(expires_at).bind(now)
        .execute(&state.pool).await.map_err(map_sqlx)?;
    Ok((
        StatusCode::CREATED,
        Json(json!({"pairing_id":pairing_id,"code":code,"expires_at":timestamp(expires_at)})),
    ))
}

async fn pairing_details(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(pairing_id): Path<Uuid>,
    Query(query): Query<PairingCodeQuery>,
) -> Result<Json<Value>, ApiError> {
    authenticate(&state, &jar).await?;
    expire_pairing(&state.pool, pairing_id).await?;
    let row = sqlx::query("SELECT * FROM computer_pairings WHERE id=$1 AND code_hash=$2")
        .bind(pairing_id)
        .bind(token_hash(&query.code))
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    Ok(Json(
        json!({"pairing_id":pairing_id,"hostname":row.get::<String,_>("hostname"),"os":row.get::<String,_>("os"),"daemon_version":row.get::<String,_>("daemon_version"),"token_fingerprint":&row.get::<String,_>("token_hash")[..12],"status":row.get::<String,_>("status"),"expires_at":timestamp(row.get("expires_at"))}),
    ))
}

async fn confirm_pairing(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(pairing_id): Path<Uuid>,
    Json(body): Json<ConfirmPairingBody>,
) -> Result<Json<Value>, ApiError> {
    current_member(&state, &jar, body.space_id).await?;
    expire_pairing(&state.pool, pairing_id).await?;
    let mut transaction = state.pool.begin().await.map_err(map_sqlx)?;
    let pairing=sqlx::query("SELECT * FROM computer_pairings WHERE id=$1 AND code_hash=$2 AND status='pending' FOR UPDATE").bind(pairing_id).bind(token_hash(&body.code)).fetch_optional(&mut *transaction).await.map_err(map_sqlx)?.ok_or_else(ApiError::not_found)?;
    let computer_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    sqlx::query("INSERT INTO computers(id,space_id,name,hostname,os,token_hash,connection_status,daemon_version,created_at) VALUES($1,$2,$3,$4,$5,$6,'offline',$7,$8)")
        .bind(computer_id).bind(body.space_id).bind(&body.name).bind(pairing.get::<String,_>("hostname")).bind(pairing.get::<String,_>("os")).bind(pairing.get::<String,_>("token_hash")).bind(pairing.get::<String,_>("daemon_version")).bind(now)
        .execute(&mut *transaction).await.map_err(map_sqlx)?;
    sqlx::query("UPDATE computer_pairings SET status='confirmed',computer_id=$2,space_id=$3,confirmed_at=$4 WHERE id=$1").bind(pairing_id).bind(computer_id).bind(body.space_id).bind(now).execute(&mut *transaction).await.map_err(map_sqlx)?;
    transaction.commit().await.map_err(map_sqlx)?;
    Ok(Json(
        json!({"id":computer_id,"space_id":body.space_id,"name":body.name,"hostname":pairing.get::<String,_>("hostname"),"os":pairing.get::<String,_>("os"),"daemon_version":pairing.get::<String,_>("daemon_version"),"status":"offline","last_seen_at":Value::Null,"created_at":timestamp(now)}),
    ))
}

async fn pairing_status(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path(pairing_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    expire_pairing(&state.pool, pairing_id).await?;
    let raw = bearer_token(&headers)?;
    let row = sqlx::query(
        "SELECT status,computer_id,space_id FROM computer_pairings WHERE id=$1 AND token_hash=$2",
    )
    .bind(pairing_id)
    .bind(token_hash(raw))
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::unauthenticated)?;
    Ok(Json(
        json!({"status":row.get::<String,_>("status"),"computer_id":row.get::<Option<Uuid>,_>("computer_id"),"space_id":row.get::<Option<Uuid>,_>("space_id")}),
    ))
}

async fn connect_computer(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
    upgrade: WebSocketUpgrade,
) -> Result<Response, ApiError> {
    let raw = bearer_token(&headers)?;
    let row = sqlx::query("SELECT deleted_at FROM computers WHERE id=$1 AND token_hash=$2")
        .bind(computer_id)
        .bind(token_hash(raw))
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::unauthenticated)?;
    let deleted = row.get::<Option<OffsetDateTime>, _>("deleted_at").is_some();
    let storage = state.storage.clone();
    let pool = state.pool.clone();
    Ok(upgrade
        .on_upgrade(move |socket| computer_socket(socket, storage, pool, computer_id, deleted)))
}

async fn claim_run(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    let raw = bearer_token(&headers)?;
    let authorized: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM computers WHERE id=$1 AND token_hash=$2 AND deleted_at IS NULL)",
    )
    .bind(computer_id)
    .bind(token_hash(raw))
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    if !authorized {
        return Err(ApiError::unauthenticated());
    }
    let candidate = sqlx::query(
        "SELECT i.id,i.agent_id,i.task_id,i.thread_id FROM inbox_items i \
         JOIN agents a ON a.member_id=i.agent_id \
         WHERE a.computer_id=$1 AND a.lifecycle='active' AND i.status='pending' \
           AND i.available_at<=now() \
           AND NOT EXISTS(SELECT 1 FROM agent_runs r WHERE r.agent_id=i.agent_id AND r.status NOT IN ('completed','yielded','failed','canceled')) \
         ORDER BY (i.strength='hard') DESC,i.available_at,(i.task_id IS NOT NULL) DESC,i.id LIMIT 1",
    )
    .bind(computer_id)
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?;
    let Some(candidate) = candidate else {
        return Ok(Json(json!({"claimed":false})));
    };
    let run_id = RunId::from_uuid(Uuid::now_v7());
    let fencing_token = format!("{}{}", Uuid::now_v7().simple(), Uuid::now_v7().simple());
    let mut storage = state.storage.clone();
    ClaimRun::execute(
        &mut storage,
        ClaimRunInput {
            run_id,
            agent_id: MemberId::from_uuid(candidate.get("agent_id")),
            computer_id: ComputerId::from_uuid(computer_id),
            task_id: candidate
                .get::<Option<Uuid>, _>("task_id")
                .map(TaskId::from_uuid),
            focus_thread_id: ThreadId::from_uuid(candidate.get("thread_id")),
            item_ids: vec![InboxItemId::from_uuid(candidate.get("id"))],
            fencing_token: RawFencingToken::new(fencing_token),
            lease_expires_at: OffsetDateTime::now_utc() + Duration::minutes(2),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(json!({"claimed":true,"run_id":run_id})))
}

#[derive(Deserialize)]
struct RenewRunBody {
    fencing_token: String,
}

async fn renew_run(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, run_id)): Path<(Uuid, Uuid)>,
    Json(body): Json<RenewRunBody>,
) -> Result<Json<Value>, ApiError> {
    let raw = bearer_token(&headers)?;
    let authorized: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM computers WHERE id=$1 AND token_hash=$2 AND deleted_at IS NULL)",
    )
    .bind(computer_id)
    .bind(token_hash(raw))
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    if !authorized {
        return Err(ApiError::unauthenticated());
    }
    let lease_expires_at = OffsetDateTime::now_utc() + Duration::minutes(2);
    let mut storage = state.storage.clone();
    let run = RenewRun::execute(
        &mut storage,
        RenewRunInput {
            run_id: RunId::from_uuid(run_id),
            computer_id: ComputerId::from_uuid(computer_id),
            fencing_token_hash: token_hash(&body.fencing_token),
            lease_expires_at,
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(json!({
        "run_id": run.id,
        "lease_expires_at": timestamp(run.lease_expires_at)
    })))
}

async fn computer_socket(
    mut socket: WebSocket,
    storage: PostgresAdapter,
    pool: PgPool,
    computer_id: Uuid,
    deleted: bool,
) {
    let Some(Ok(WebSocketMessage::Text(encoded))) = socket.next().await else {
        return;
    };
    let Ok(hello) = serde_json::from_str::<ComputerHello>(&encoded) else {
        return;
    };
    let handshake = super::websocket::negotiate(&hello, deleted, true);
    if send_json(&mut socket, &handshake).await.is_err() {
        return;
    }
    if !matches!(
        handshake,
        crate::protocol::computer::ServerHandshake::Welcome { .. }
    ) {
        return;
    }
    let _=sqlx::query("UPDATE computers SET connection_status='online',daemon_version=$2,last_seen_at=$3 WHERE id=$1")
        .bind(computer_id).bind(&hello.daemon_version).bind(OffsetDateTime::now_utc()).execute(&pool).await;
    if let Ok(commands) = super::websocket::replay_commands(
        &storage,
        crate::ids::ComputerId::from_uuid(computer_id),
        hello.command_watermark,
    )
    .await
    {
        for envelope in commands {
            if send_json(
                &mut socket,
                &ServerFrame::Command {
                    envelope: Box::new(envelope),
                },
            )
            .await
            .is_err()
            {
                break;
            }
        }
    }
    while let Some(frame) = socket.next().await {
        let Ok(WebSocketMessage::Text(encoded)) = frame else {
            continue;
        };
        let Ok(frame) = serde_json::from_str::<ComputerFrame>(&encoded) else {
            continue;
        };
        match frame {
            ComputerFrame::Heartbeat { heartbeat } => {
                let _ = sqlx::query("UPDATE computers SET last_seen_at=$2 WHERE id=$1")
                    .bind(computer_id)
                    .bind(heartbeat.observed_at)
                    .execute(&pool)
                    .await;
                if let Ok(commands) = super::websocket::replay_commands(
                    &storage,
                    ComputerId::from_uuid(computer_id),
                    crate::protocol::computer::CommandSequence(0),
                )
                .await
                {
                    for envelope in commands {
                        if send_json(
                            &mut socket,
                            &ServerFrame::Command {
                                envelope: Box::new(envelope),
                            },
                        )
                        .await
                        .is_err()
                        {
                            break;
                        }
                    }
                }
            }
            ComputerFrame::CommandAck { .. } => {}
            ComputerFrame::RunResult { result } => {
                let event_id = result.event_id;
                let response = super::http::submit_run_result(
                    &storage,
                    super::http::ComputerPrincipal {
                        computer_id: crate::ids::ComputerId::from_uuid(computer_id),
                    },
                    result,
                )
                .await;
                if response.is_ok() {
                    let _ = send_json(
                        &mut socket,
                        &ServerFrame::Receipt {
                            receipt: crate::protocol::computer::Receipt {
                                event_id,
                                kind: crate::protocol::computer::ReceiptKind::RunResult,
                            },
                        },
                    )
                    .await;
                }
            }
            ComputerFrame::RunStarted { started } => {
                let mut application = storage.clone();
                let applied = StartRun::execute(
                    &mut application,
                    StartRunInput {
                        run_id: started.run_id,
                        computer_id: ComputerId::from_uuid(computer_id),
                        fencing_token_hash: token_hash(started.fencing_token.expose()),
                        now: started.observed_at,
                    },
                )
                .await;
                if applied.is_ok() {
                    let _ = send_json(
                        &mut socket,
                        &ServerFrame::Receipt {
                            receipt: crate::protocol::computer::Receipt {
                                event_id: started.event_id,
                                kind: crate::protocol::computer::ReceiptKind::RunStarted,
                            },
                        },
                    )
                    .await;
                }
            }
            ComputerFrame::DeliveryReceipt { receipt } => {
                let _ = send_json(
                    &mut socket,
                    &ServerFrame::Receipt {
                        receipt: crate::protocol::computer::Receipt {
                            event_id: receipt.event_id,
                            kind: crate::protocol::computer::ReceiptKind::Delivery,
                        },
                    },
                )
                .await;
            }
            ComputerFrame::CommandResult { result } => {
                if apply_command_result(&pool, computer_id, &result)
                    .await
                    .is_ok()
                {
                    let ack = crate::protocol::computer::CommandAck {
                        command_id: result.command_id,
                        sequence: result.sequence,
                    };
                    let _ = super::websocket::acknowledge_command(
                        &storage,
                        ComputerId::from_uuid(computer_id),
                        &ack,
                    )
                    .await;
                }
            }
        }
    }
    let _ = sqlx::query("UPDATE computers SET connection_status='offline' WHERE id=$1")
        .bind(computer_id)
        .execute(&pool)
        .await;
}

async fn apply_command_result(
    pool: &PgPool,
    computer_id: Uuid,
    result: &crate::protocol::computer::CommandResult,
) -> Result<(), ApiError> {
    let sequence = i64::try_from(result.sequence.0).map_err(|_| ApiError::internal())?;
    let row = sqlx::query(
        "SELECT kind,payload_json #>> '{payload,agent_id}' AS agent_id FROM computer_commands \
         WHERE id=$1 AND computer_id=$2 AND computer_seq=$3",
    )
    .bind(result.command_id.into_uuid())
    .bind(computer_id)
    .bind(sequence)
    .fetch_optional(pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::not_found)?;
    if row.get::<&str, _>("kind") == "agent.provision" {
        let agent_id =
            Uuid::parse_str(row.get::<&str, _>("agent_id")).map_err(|_| ApiError::internal())?;
        let lifecycle = match result.outcome {
            crate::protocol::computer::CommandOutcome::Applied => "active",
            crate::protocol::computer::CommandOutcome::Rejected { .. } => "error",
        };
        sqlx::query("UPDATE agents SET lifecycle=$2 WHERE member_id=$1 AND computer_id=$3 AND lifecycle IN ('provisioning','active','error')")
            .bind(agent_id).bind(lifecycle).bind(computer_id)
            .execute(pool).await.map_err(map_sqlx)?;
    }
    Ok(())
}

async fn agent_action(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
    Json(request): Json<AgentActionRequest>,
) -> Json<capability::Response<Value>> {
    match execute_agent_action(&state, &headers, computer_id, request).await {
        Ok(value) => Json(capability::Response::success(value)),
        Err(error) => Json(capability::Response::failure(error)),
    }
}

async fn execute_agent_action(
    state: &RuntimeState,
    headers: &HeaderMap,
    computer_id: Uuid,
    request: AgentActionRequest,
) -> Result<Value, capability::Error> {
    let raw = bearer_token(headers).map_err(|_| {
        capability_error(
            capability::ErrorCode::Unauthenticated,
            "Computer authentication failed",
            false,
        )
    })?;
    let authenticated:bool=sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM computers WHERE id=$1 AND token_hash=$2 AND deleted_at IS NULL)").bind(computer_id).bind(token_hash(raw)).fetch_one(&state.pool).await.map_err(|_|capability_error(capability::ErrorCode::Internal,"Computer authentication failed",false))?;
    if !authenticated {
        return Err(capability_error(
            capability::ErrorCode::Unauthenticated,
            "Computer authentication failed",
            false,
        ));
    }
    let context = &request.context;
    if let Some(key) = request.idempotency_key
        && matches!(
            &request.action,
            capability::Action::TaskSubmitReview { .. }
                | capability::Action::TaskDone { .. }
                | capability::Action::TaskClose { .. }
        )
    {
        let replayed = sqlx::query_scalar::<_, Uuid>(
            "SELECT records.resource_id FROM idempotency_records records \
             JOIN tasks ON tasks.id=records.resource_id \
             JOIN agents ON agents.member_id=records.actor_member_id \
             WHERE records.actor_member_id=$1 AND records.action=$2 \
             AND records.idempotency_key=$3 AND tasks.id=$4 AND tasks.space_id=$5 \
             AND agents.computer_id=$6",
        )
        .bind(context.agent_id.into_uuid())
        .bind(request.action.name())
        .bind(key.into_uuid())
        .bind(context.task_id.map(TaskId::into_uuid))
        .bind(context.space_id.into_uuid())
        .bind(computer_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(|_| {
            capability_error(
                capability::ErrorCode::Internal,
                "Task action replay could not be checked",
                false,
            )
        })?;
        if let Some(task_id) = replayed {
            return task_projection(&state.pool, task_id)
                .await
                .map_err(api_to_capability);
        }
    }
    let valid:bool=sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM agent_runs r JOIN agents a ON a.member_id=r.agent_id WHERE r.id=$1 AND r.agent_id=$2 AND r.space_id=$3 AND r.task_id IS NOT DISTINCT FROM $4 AND r.focus_thread_id=$5 AND r.status='running' AND r.fencing_token_hash=$6 AND r.lease_expires_at>now() AND a.computer_id=$7)")
        .bind(context.run_id.into_uuid()).bind(context.agent_id.into_uuid()).bind(context.space_id.into_uuid()).bind(context.task_id.map(|id|id.into_uuid())).bind(context.focus_thread_id.into_uuid()).bind(token_hash(&context.fencing_token)).bind(computer_id)
        .fetch_one(&state.pool).await.map_err(|_|capability_error(capability::ErrorCode::Internal,"Run context validation failed",false))?;
    if !valid {
        return Err(capability_error(
            capability::ErrorCode::ContextChanged,
            "Run context is no longer active",
            false,
        ));
    }
    match request.action {
        capability::Action::ContextCurrent => {
            let task = match context.task_id {
                Some(id) => Some(
                    task_projection(&state.pool, id.into_uuid())
                        .await
                        .map_err(api_to_capability)?,
                ),
                None => None,
            };
            let items=sqlx::query("SELECT i.id,i.kind,i.strength,i.status,i.available_at FROM run_items ri JOIN inbox_items i ON i.id=ri.inbox_item_id WHERE ri.run_id=$1 ORDER BY ri.delivery_seq").bind(context.run_id.into_uuid()).fetch_all(&state.pool).await.map_err(|_|capability_error(capability::ErrorCode::Internal,"Run Items could not be read",false))?;
            Ok(
                json!({"agent":{"id":context.agent_id,"space_id":context.space_id},"task":task,"focus_thread_id":context.focus_thread_id,"run":{"id":context.run_id,"message_snapshot_sequence":context.message_snapshot_sequence},"claimed_items":items.iter().map(|row|json!({"id":row.get::<Uuid,_>("id"),"kind":row.get::<String,_>("kind"),"strength":row.get::<String,_>("strength"),"status":row.get::<String,_>("status"),"available_at":timestamp(row.get("available_at"))})).collect::<Vec<_>>(),"session_continuity":{"state":"unavailable"}}),
            )
        }
        capability::Action::MessageRead(page) => {
            agent_read_thread(state, context.focus_thread_id.into_uuid(), page).await
        }
        capability::Action::ThreadRead { thread_id, page } => {
            agent_read_thread(state, thread_id.into_uuid(), page).await
        }
        capability::Action::ChannelRead {
            channel_id,
            around_message_id,
            limit,
        } => {
            if limit == 0 || limit > 100 {
                return Err(capability_error(
                    capability::ErrorCode::InvalidArgument,
                    "limit must be between 1 and 100",
                    false,
                ));
            }
            agent_read_channel(
                state,
                context.agent_id.into_uuid(),
                channel_id.into_uuid(),
                around_message_id.map(MessageId::into_uuid),
                limit,
            )
            .await
        }
        capability::Action::MessageSend(send) => {
            let expected_snapshot = send.snapshot_sequence.or_else(|| {
                matches!(&send.target, capability::MessageTarget::Focus)
                    .then_some(context.message_snapshot_sequence)
            });
            let (channel_id, thread_id) = match send.target {
                capability::MessageTarget::Focus => {
                    thread_channel(&state.pool, context.focus_thread_id.into_uuid())
                        .await
                        .map_err(api_to_capability)?
                        .map(|channel| (channel, Some(context.focus_thread_id.into_uuid())))
                        .ok_or_else(|| {
                            capability_error(
                                capability::ErrorCode::NotFound,
                                "Focus Thread was not found",
                                false,
                            )
                        })?
                }
                capability::MessageTarget::Thread(thread_id) => {
                    thread_channel(&state.pool, thread_id.into_uuid())
                        .await
                        .map_err(api_to_capability)?
                        .map(|channel| (channel, Some(thread_id.into_uuid())))
                        .ok_or_else(|| {
                            capability_error(
                                capability::ErrorCode::NotFound,
                                "Thread was not found",
                                false,
                            )
                        })?
                }
                capability::MessageTarget::Channel(channel_id) => (channel_id.into_uuid(), None),
            };
            let message_id = insert_message(
                state,
                channel_id,
                context.agent_id.into_uuid(),
                thread_id,
                send.handle_item_id
                    .map(|item_id| (context.run_id.into_uuid(), item_id.into_uuid())),
                expected_snapshot,
                CreateMessageBody {
                    body_markdown: send.body,
                    mentions: Vec::new(),
                    attachment_ids: Vec::new(),
                    reply_to_message_id: None,
                },
            )
            .await
            .map_err(api_to_capability)?;
            Ok(
                json!({"message_id":message_id,"thread_id":thread_id.unwrap_or(message_id),"channel_id":channel_id}),
            )
        }
        capability::Action::TaskCreate { title, assignee } => {
            let key = request.idempotency_key.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::InvalidArgument,
                    "idempotency key is required",
                    false,
                )
            })?;
            let default_title: String =
                sqlx::query_scalar("SELECT body_markdown FROM messages WHERE id=$1")
                    .bind(context.focus_thread_id.into_uuid())
                    .fetch_one(&state.pool)
                    .await
                    .map_err(|_| {
                        capability_error(
                            capability::ErrorCode::NotFound,
                            "Focus Root Message was not found",
                            false,
                        )
                    })?;
            let mut storage = state.storage.clone();
            let task = CreateTaskFromRootMessage::execute(
                &mut storage,
                CreateTaskInput {
                    task_id: TaskId::from_uuid(Uuid::now_v7()),
                    actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
                    source: TaskSource::AgentRun(context.run_id),
                    title: title.unwrap_or_else(|| default_title.chars().take(120).collect()),
                    assignee_agent_member_id: assignee,
                    idempotency_key: key,
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            task_projection(&state.pool, task.id.into_uuid())
                .await
                .map_err(api_to_capability)
        }
        capability::Action::TaskLinkThread { thread_id } => {
            let task_id = context.task_id.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::Conflict,
                    "Run is not bound to a Task",
                    false,
                )
            })?;
            let mut storage = state.storage.clone();
            LinkThreadToTask::execute(
                &mut storage,
                LinkThreadInput {
                    task_id,
                    target_thread_id: thread_id,
                    actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            task_projection(&state.pool, task_id.into_uuid())
                .await
                .map_err(api_to_capability)
        }
        capability::Action::TaskUnlinkThread { thread_id } => {
            let task_id = context.task_id.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::Conflict,
                    "Run is not bound to a Task",
                    false,
                )
            })?;
            let mut storage = state.storage.clone();
            UnlinkThreadFromTask::execute(
                &mut storage,
                LinkThreadInput {
                    task_id,
                    target_thread_id: thread_id,
                    actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            task_projection(&state.pool, task_id.into_uuid())
                .await
                .map_err(api_to_capability)
        }
        capability::Action::TaskUpdate { title } => {
            let task_id = context.task_id.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::Conflict,
                    "Run is not bound to a Task",
                    false,
                )
            })?;
            let mut storage = state.storage.clone();
            UpdateTask::execute(
                &mut storage,
                UpdateTaskInput {
                    task_id,
                    actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
                    action: TaskAction::Rename { title },
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            task_projection(&state.pool, task_id.into_uuid())
                .await
                .map_err(api_to_capability)
        }
        capability::Action::TaskSubmitReview { body, post_to } => {
            finish_agent_task(
                state,
                computer_id,
                context,
                request.idempotency_key,
                FinishAgentTaskAction::SubmitReview {
                    message_id: MessageId::from_uuid(Uuid::now_v7()),
                    body,
                    post_to: task_post_target(post_to),
                },
            )
            .await
        }
        capability::Action::TaskDone { result, post_to } => {
            finish_agent_task(
                state,
                computer_id,
                context,
                request.idempotency_key,
                FinishAgentTaskAction::Done {
                    message_id: MessageId::from_uuid(Uuid::now_v7()),
                    result,
                    post_to: task_post_target(post_to),
                },
            )
            .await
        }
        capability::Action::TaskClose { reason, note } => {
            let reason = match reason {
                capability::CloseReason::Invalid => CloseReason::Invalid,
                capability::CloseReason::Duplicate => CloseReason::Duplicate,
                capability::CloseReason::NotNeeded => CloseReason::NotNeeded,
                capability::CloseReason::Obsolete => CloseReason::Obsolete,
                capability::CloseReason::Other => CloseReason::Other,
            };
            finish_agent_task(
                state,
                computer_id,
                context,
                request.idempotency_key,
                FinishAgentTaskAction::Close { reason, note },
            )
            .await
        }
        capability::Action::ChannelCreate { name, private } => {
            let audience = if private {
                sqlx::query_scalar::<_, Uuid>(
                    "SELECT member_id FROM channel_members WHERE channel_id=(SELECT channel_id FROM threads WHERE id=$1)",
                )
                .bind(context.focus_thread_id.into_uuid())
                .fetch_all(&state.pool)
                .await
            } else {
                sqlx::query_scalar::<_, Uuid>("SELECT id FROM members WHERE space_id=$1 AND retired_at IS NULL")
                    .bind(context.space_id.into_uuid())
                    .fetch_all(&state.pool)
                    .await
            }
            .map_err(|_| capability_error(capability::ErrorCode::Internal, "Channel audience could not be resolved", false))?
            .into_iter()
            .map(MemberId::from_uuid)
            .collect();
            let channel_id = crate::ids::ChannelId::from_uuid(Uuid::now_v7());
            let mut storage = state.storage.clone();
            let channel = CreateChannelAction::execute(
                &mut storage,
                CreateChannelActionInput {
                    channel_id,
                    audience,
                    kind: if private {
                        ChannelKind::Private
                    } else {
                        ChannelKind::Public
                    },
                    slug: Some(unique_handle(&name, channel_id.into_uuid())),
                    topic: Some(name),
                    action_message_id: MessageId::from_uuid(Uuid::now_v7()),
                    actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
                    current_run_id: context.run_id,
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            Ok(json!({"channel_id":channel.id,"kind":if private{"private"}else{"public"}}))
        }
        capability::Action::AgentCreate { name, role, driver } => {
            let agent_id = MemberId::from_uuid(Uuid::now_v7());
            let mut storage = state.storage.clone();
            let agent = CreateAgentAction::execute(
                &mut storage,
                CreateAgentActionInput {
                    agent_member_id: agent_id,
                    display_name: name.clone(),
                    handle: unique_handle(&name, agent_id.into_uuid()),
                    role_text: role,
                    computer_id: ComputerId::from_uuid(computer_id),
                    driver_kind: match driver {
                        capability::DriverKind::Codex => DriverKind::Codex,
                        capability::DriverKind::Builtin => DriverKind::Builtin,
                    },
                    action_message_id: MessageId::from_uuid(Uuid::now_v7()),
                    actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
                    current_run_id: context.run_id,
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            Ok(json!({"agent_id":agent.member_id,"lifecycle":"provisioning"}))
        }
        capability::Action::InboxCurrent => {
            let rows=sqlx::query("SELECT i.id,i.kind,i.strength,i.status,i.available_at,ri.disposition FROM run_items ri JOIN inbox_items i ON i.id=ri.inbox_item_id WHERE ri.run_id=$1 ORDER BY ri.delivery_seq").bind(context.run_id.into_uuid()).fetch_all(&state.pool).await.map_err(|_|capability_error(capability::ErrorCode::Internal,"Run Items could not be read",false))?;
            Ok(
                json!({"run_id":context.run_id,"items":rows.iter().map(|row|json!({"id":row.get::<Uuid,_>("id"),"kind":row.get::<String,_>("kind"),"strength":row.get::<String,_>("strength"),"status":row.get::<String,_>("status"),"available_at":timestamp(row.get("available_at")),"disposition":row.get::<Option<String>,_>("disposition")})).collect::<Vec<_>>(),"notices":[]}),
            )
        }
        capability::Action::InboxAck { item_id, .. } => {
            record_agent_item_disposition(
                state,
                computer_id,
                context,
                item_id,
                crate::server::domain::attention::InboxItemDisposition::Handled,
                None,
            )
            .await?;
            Ok(json!({"item_id":item_id,"disposition":"handled"}))
        }
        capability::Action::InboxDefer { item_id, until } => {
            record_agent_item_disposition(
                state,
                computer_id,
                context,
                item_id,
                crate::server::domain::attention::InboxItemDisposition::Deferred,
                Some(until),
            )
            .await?;
            Ok(json!({"item_id":item_id,"disposition":"deferred","available_at":timestamp(until)}))
        }
        _ => Err(capability_error(
            capability::ErrorCode::Unavailable,
            "Agent action is not connected in this runtime",
            false,
        )),
    }
}

fn task_post_target(target: capability::PostTarget) -> TaskPostTarget {
    match target {
        capability::PostTarget::Focus => TaskPostTarget::Focus,
        capability::PostTarget::Source => TaskPostTarget::Source,
    }
}

async fn finish_agent_task(
    state: &RuntimeState,
    computer_id: Uuid,
    context: &capability::RunContext,
    idempotency_key: Option<crate::ids::IdempotencyKey>,
    action: FinishAgentTaskAction,
) -> Result<Value, capability::Error> {
    let task_id = context.task_id.ok_or_else(|| {
        capability_error(
            capability::ErrorCode::Conflict,
            "Run is not bound to a Task",
            false,
        )
    })?;
    let idempotency_key = idempotency_key.ok_or_else(|| {
        capability_error(
            capability::ErrorCode::InvalidArgument,
            "idempotency key is required",
            false,
        )
    })?;
    let mut storage = state.storage.clone();
    FinishAgentTaskRun::execute(
        &mut storage,
        FinishAgentTaskInput {
            run_id: context.run_id,
            computer_id: ComputerId::from_uuid(computer_id),
            actor_member_id: MemberId::from_uuid(context.agent_id.into_uuid()),
            fencing_token_hash: token_hash(&context.fencing_token),
            idempotency_key,
            message_snapshot_sequence: context.message_snapshot_sequence,
            action,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(app_to_capability)?;
    task_projection(&state.pool, task_id.into_uuid())
        .await
        .map_err(api_to_capability)
}

async fn record_agent_item_disposition(
    state: &RuntimeState,
    computer_id: Uuid,
    context: &capability::RunContext,
    item_id: InboxItemId,
    disposition: crate::server::domain::attention::InboxItemDisposition,
    defer_until: Option<OffsetDateTime>,
) -> Result<(), capability::Error> {
    let mut storage = state.storage.clone();
    RecordRunItemDisposition::execute(
        &mut storage,
        RecordRunItemDispositionInput {
            run_id: context.run_id,
            computer_id: ComputerId::from_uuid(computer_id),
            fencing_token_hash: token_hash(&context.fencing_token),
            item_id,
            disposition,
            defer_until,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(app_to_capability)?;
    Ok(())
}

async fn agent_read_thread(
    state: &RuntimeState,
    thread_id: Uuid,
    page: capability::Page,
) -> Result<Value, capability::Error> {
    let limit = i64::from(page.limit);
    let rows=sqlx::query("SELECT id,channel_seq,author_member_id,content_kind,body_markdown,created_at FROM messages WHERE thread_id=$1 AND ($2::bigint IS NULL OR channel_seq<$2) AND ($3::bigint IS NULL OR channel_seq>$3) ORDER BY channel_seq LIMIT $4").bind(thread_id).bind(page.before.map(|v|v as i64)).bind(page.after.map(|v|v as i64)).bind(limit).fetch_all(&state.pool).await.map_err(|_|capability_error(capability::ErrorCode::Internal,"Messages could not be read",false))?;
    Ok(
        json!({"thread_id":thread_id,"messages":rows.iter().map(|row|json!({"id":row.get::<Uuid,_>("id"),"seq":row.get::<i64,_>("channel_seq"),"author_member_id":row.get::<Uuid,_>("author_member_id"),"content":{"type":"text","body_markdown":row.get::<Option<String>,_>("body_markdown").unwrap_or_default()},"created_at":timestamp(row.get("created_at"))})).collect::<Vec<_>>()}),
    )
}

async fn agent_read_channel(
    state: &RuntimeState,
    agent_id: Uuid,
    channel_id: Uuid,
    around_message_id: Option<Uuid>,
    limit: u16,
) -> Result<Value, capability::Error> {
    let member: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM channel_members WHERE channel_id=$1 AND member_id=$2)",
    )
    .bind(channel_id)
    .bind(agent_id)
    .fetch_one(&state.pool)
    .await
    .map_err(|_| {
        capability_error(
            capability::ErrorCode::Internal,
            "Channel membership could not be checked",
            false,
        )
    })?;
    if !member {
        return Err(capability_error(
            capability::ErrorCode::PermissionDenied,
            "Channel is not visible to the Agent",
            false,
        ));
    }
    let around_sequence = match around_message_id {
        Some(message_id) => Some(
            sqlx::query_scalar::<_, i64>(
                "SELECT channel_seq FROM messages WHERE id=$1 AND channel_id=$2",
            )
            .bind(message_id)
            .bind(channel_id)
            .fetch_optional(&state.pool)
            .await
            .map_err(|_| {
                capability_error(
                    capability::ErrorCode::Internal,
                    "Channel cursor could not be read",
                    false,
                )
            })?
            .ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::NotFound,
                    "Around Message was not found in the Channel",
                    false,
                )
            })?,
        ),
        None => None,
    };
    let rows = if let Some(sequence) = around_sequence {
        sqlx::query(
            "SELECT * FROM messages WHERE channel_id=$1 AND deleted_at IS NULL \
             ORDER BY abs(channel_seq-$2),channel_seq LIMIT $3",
        )
        .bind(channel_id)
        .bind(sequence)
        .bind(i64::from(limit))
        .fetch_all(&state.pool)
        .await
    } else {
        sqlx::query(
            "SELECT * FROM messages WHERE channel_id=$1 AND deleted_at IS NULL \
             ORDER BY channel_seq DESC LIMIT $2",
        )
        .bind(channel_id)
        .bind(i64::from(limit))
        .fetch_all(&state.pool)
        .await
    }
    .map_err(|_| {
        capability_error(
            capability::ErrorCode::Internal,
            "Channel Messages could not be read",
            false,
        )
    })?;
    let mut rows = rows;
    rows.sort_by_key(|row| row.get::<i64, _>("channel_seq"));
    let mut messages = Vec::with_capacity(rows.len());
    for row in &rows {
        messages.push(
            message_row(&state.pool, row, agent_id)
                .await
                .map_err(api_to_capability)?,
        );
    }
    let snapshot: i64 = sqlx::query_scalar("SELECT next_seq-1 FROM channels WHERE id=$1")
        .bind(channel_id)
        .fetch_one(&state.pool)
        .await
        .map_err(|_| {
            capability_error(
                capability::ErrorCode::Internal,
                "Channel snapshot could not be read",
                false,
            )
        })?;
    Ok(json!({
        "channel_id": channel_id,
        "messages": messages,
        "snapshot_channel_seq": snapshot
    }))
}

async fn thread_channel(pool: &PgPool, thread_id: Uuid) -> Result<Option<Uuid>, ApiError> {
    sqlx::query_scalar("SELECT channel_id FROM threads WHERE id=$1")
        .bind(thread_id)
        .fetch_optional(pool)
        .await
        .map_err(map_sqlx)
}
fn capability_error(
    code: capability::ErrorCode,
    message: &str,
    retryable: bool,
) -> capability::Error {
    capability::Error {
        code,
        message: message.into(),
        retryable,
        details: Default::default(),
    }
}
fn api_to_capability(error: ApiError) -> capability::Error {
    let code = if error.code == "context_changed" {
        capability::ErrorCode::ContextChanged
    } else {
        match error.status {
            StatusCode::NOT_FOUND => capability::ErrorCode::NotFound,
            StatusCode::FORBIDDEN => capability::ErrorCode::PermissionDenied,
            StatusCode::CONFLICT => capability::ErrorCode::Conflict,
            _ => capability::ErrorCode::Internal,
        }
    };
    capability_error(code, error.message, false)
}
fn app_to_capability(
    error: crate::server::application::ports::ApplicationError,
) -> capability::Error {
    let api = application_error(error);
    api_to_capability(api)
}

async fn send_json(
    socket: &mut WebSocket,
    value: &impl serde::Serialize,
) -> Result<(), axum::Error> {
    let encoded = serde_json::to_string(value).map_err(axum::Error::new)?;
    socket.send(WebSocketMessage::Text(encoded.into())).await
}

async fn expire_pairing(pool: &PgPool, pairing_id: Uuid) -> Result<(), ApiError> {
    sqlx::query("UPDATE computer_pairings SET status='expired' WHERE id=$1 AND status='pending' AND expires_at<=now()").bind(pairing_id).execute(pool).await.map_err(map_sqlx)?;
    Ok(())
}

async fn create_space(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Json(body): Json<CreateSpaceBody>,
) -> Result<(StatusCode, Json<Value>), ApiError> {
    let user = authenticate(&state, &jar).await?;
    let name = body.name.trim();
    let slug = body.slug.trim().to_lowercase();
    if name.is_empty() || slug.is_empty() {
        return Err(ApiError::invalid("Space name and slug are required"));
    }
    let space_id = Uuid::now_v7();
    let owner_id = Uuid::now_v7();
    let general_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let handle = unique_handle(&user.display_name, owner_id);
    let mut transaction = state.pool.begin().await.map_err(map_sqlx)?;
    sqlx::query("SET CONSTRAINTS ALL DEFERRED")
        .execute(&mut *transaction)
        .await
        .map_err(map_sqlx)?;
    sqlx::query(
        "INSERT INTO spaces(id,slug,name,owner_member_id,created_at) VALUES($1,$2,$3,$4,$5)",
    )
    .bind(space_id)
    .bind(&slug)
    .bind(name)
    .bind(owner_id)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(map_sqlx)?;
    sqlx::query("INSERT INTO members(id,space_id,kind,display_name,handle,access_level,created_at) VALUES($1,$2,'human',$3,$4,'owner',$5)")
        .bind(owner_id).bind(space_id).bind(&user.display_name).bind(handle).bind(now)
        .execute(&mut *transaction).await.map_err(map_sqlx)?;
    sqlx::query("INSERT INTO human_members(member_id,space_id,user_id) VALUES($1,$2,$3)")
        .bind(owner_id)
        .bind(space_id)
        .bind(user.id)
        .execute(&mut *transaction)
        .await
        .map_err(map_sqlx)?;
    sqlx::query("INSERT INTO channels(id,space_id,kind,slug,topic,created_at) VALUES($1,$2,'public','general',NULL,$3)")
        .bind(general_id).bind(space_id).bind(now)
        .execute(&mut *transaction).await.map_err(map_sqlx)?;
    sqlx::query(
        "INSERT INTO channel_members(channel_id,space_id,member_id,joined_at) VALUES($1,$2,$3,$4)",
    )
    .bind(general_id)
    .bind(space_id)
    .bind(owner_id)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(map_sqlx)?;
    transaction.commit().await.map_err(map_sqlx)?;
    Ok((
        StatusCode::CREATED,
        Json(space_json(
            space_id, name, &slug, owner_id, owner_id, general_id,
        )),
    ))
}

async fn list_spaces(
    State(state): State<RuntimeState>,
    jar: CookieJar,
) -> Result<Json<Value>, ApiError> {
    let user = authenticate(&state, &jar).await?;
    let rows = sqlx::query(
        "SELECT s.id,s.name,s.slug,s.owner_member_id,hm.member_id AS current_member_id, \
         (SELECT id FROM channels WHERE space_id=s.id AND slug='general' LIMIT 1) AS general_channel_id \
         FROM spaces s JOIN human_members hm ON hm.space_id=s.id \
         WHERE hm.user_id=$1 AND s.deleted_at IS NULL ORDER BY s.created_at",
    )
    .bind(user.id)
    .fetch_all(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok(Json(Value::Array(rows.iter().map(space_row).collect())))
}

async fn space_by_slug(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(slug): Path<String>,
) -> Result<Json<Value>, ApiError> {
    let user = authenticate(&state, &jar).await?;
    let row = sqlx::query(
        "SELECT s.id,s.name,s.slug,s.owner_member_id,hm.member_id AS current_member_id, \
         (SELECT id FROM channels WHERE space_id=s.id AND slug='general' LIMIT 1) AS general_channel_id \
         FROM spaces s JOIN human_members hm ON hm.space_id=s.id \
         WHERE hm.user_id=$1 AND lower(s.slug)=lower($2) AND s.deleted_at IS NULL",
    )
    .bind(user.id)
    .bind(slug)
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::not_found)?;
    Ok(Json(space_row(&row)))
}

async fn list_channels(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    let member_id = current_member(&state, &jar, space_id).await?;
    let rows = sqlx::query(
        "SELECT c.id,c.space_id,c.kind,c.slug,c.topic,c.archived_at, \
         EXISTS(SELECT 1 FROM channel_members cm WHERE cm.channel_id=c.id AND cm.member_id=$2) AS joined \
         FROM channels c WHERE c.space_id=$1 AND c.kind<>'direct' ORDER BY c.created_at",
    )
    .bind(space_id)
    .bind(member_id)
    .fetch_all(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok(Json(json!({
        "channels": rows.iter().map(|row| channel_row(row, member_id)).collect::<Vec<_>>(),
        "can_create": true
    })))
}

async fn create_channel(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
    Json(body): Json<CreateChannelBody>,
) -> Result<(StatusCode, Json<Value>), ApiError> {
    let member_id = current_member(&state, &jar, space_id).await?;
    if !matches!(body.kind.as_str(), "public" | "private") || body.slug.trim().is_empty() {
        return Err(ApiError::invalid("Channel kind and slug are invalid"));
    }
    let channel_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let mut audience = BTreeSet::from([member_id]);
    audience.extend(body.agent_member_ids);
    let mut transaction = state.pool.begin().await.map_err(map_sqlx)?;
    sqlx::query(
        "INSERT INTO channels(id,space_id,kind,slug,topic,created_at) VALUES($1,$2,$3,$4,$5,$6)",
    )
    .bind(channel_id)
    .bind(space_id)
    .bind(&body.kind)
    .bind(body.slug.trim())
    .bind(body.topic)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(map_sqlx)?;
    for audience_member in audience {
        sqlx::query("INSERT INTO channel_members(channel_id,space_id,member_id,joined_at) VALUES($1,$2,$3,$4)")
            .bind(channel_id).bind(space_id).bind(audience_member).bind(now)
            .execute(&mut *transaction).await.map_err(map_sqlx)?;
    }
    transaction.commit().await.map_err(map_sqlx)?;
    Ok((
        StatusCode::CREATED,
        Json(json!({
            "id": channel_id, "space_id": space_id, "name": body.name, "slug": body.slug,
            "topic": Value::Null, "kind": body.kind, "created_by_member_id": member_id,
            "joined": true, "archived_at": Value::Null
        })),
    ))
}

async fn list_members(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    current_member(&state, &jar, space_id).await?;
    let rows = sqlx::query("SELECT id,kind,display_name,handle,access_level FROM members WHERE space_id=$1 AND retired_at IS NULL ORDER BY created_at")
        .bind(space_id).fetch_all(&state.pool).await.map_err(map_sqlx)?;
    let mut values = Vec::with_capacity(rows.len());
    for row in rows {
        values.push(member_row(&state.pool, &row).await?);
    }
    Ok(Json(Value::Array(values)))
}

async fn list_computers(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    current_member(&state, &jar, space_id).await?;
    let rows = sqlx::query("SELECT * FROM computers WHERE space_id=$1 ORDER BY created_at")
        .bind(space_id)
        .fetch_all(&state.pool)
        .await
        .map_err(map_sqlx)?;
    Ok(Json(Value::Array(rows.iter().map(|row| json!({
        "id": row.get::<Uuid,_>("id"), "space_id": space_id, "name": row.get::<String,_>("name"),
        "hostname": row.get::<String,_>("hostname"), "os": row.get::<String,_>("os"),
        "daemon_version": row.get::<Option<String>,_>("daemon_version").unwrap_or_default(),
        "status": if row.get::<Option<OffsetDateTime>,_>("deleted_at").is_some() {"revoked"} else {row.get::<&str,_>("connection_status")},
        "last_seen_at": optional_timestamp(row.get("last_seen_at")), "created_at": timestamp(row.get("created_at"))
    })).collect())))
}

async fn list_agents(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    current_member(&state, &jar, space_id).await?;
    let rows = sqlx::query(
        "SELECT a.*,m.display_name,m.handle,m.access_level,c.connection_status,c.deleted_at AS computer_deleted_at, \
         (SELECT status FROM agent_runs r WHERE r.agent_id=a.member_id AND r.status NOT IN ('completed','yielded','failed','canceled') ORDER BY r.created_at DESC LIMIT 1) AS run_status \
         FROM agents a JOIN members m ON m.id=a.member_id JOIN computers c ON c.id=a.computer_id \
         WHERE a.space_id=$1 ORDER BY a.created_at",
    )
    .bind(space_id)
    .fetch_all(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok(Json(Value::Array(rows.iter().map(agent_row).collect())))
}

async fn get_agent(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(agent_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    let row = sqlx::query(
        "SELECT a.*,m.display_name,m.handle,m.access_level,c.connection_status,c.deleted_at AS computer_deleted_at, \
         (SELECT status FROM agent_runs r WHERE r.agent_id=a.member_id AND r.status NOT IN ('completed','yielded','failed','canceled') ORDER BY r.created_at DESC LIMIT 1) AS run_status \
         FROM agents a JOIN members m ON m.id=a.member_id JOIN computers c ON c.id=a.computer_id \
         WHERE a.member_id=$1",
    )
    .bind(agent_id)
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::not_found)?;
    current_member(&state, &jar, row.get("space_id")).await?;
    Ok(Json(agent_row(&row)))
}

async fn create_agent(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
    Json(body): Json<CreateAgentBody>,
) -> Result<(StatusCode, Json<Value>), ApiError> {
    let actor_id = current_member(&state, &jar, space_id).await?;
    let actor_level: String =
        sqlx::query_scalar("SELECT access_level FROM members WHERE id=$1 AND space_id=$2")
            .bind(actor_id)
            .bind(space_id)
            .fetch_one(&state.pool)
            .await
            .map_err(map_sqlx)?;
    if !matches!(actor_level.as_str(), "owner" | "admin") {
        return Err(ApiError {
            status: StatusCode::FORBIDDEN,
            code: "permission_denied",
            message: "Space Owner or Admin access is required",
        });
    }
    let name = body.name.trim();
    let role = body.role_text.trim();
    if name.is_empty()
        || name.chars().count() > 40
        || role.is_empty()
        || role.chars().count() > 12_000
        || !matches!(body.driver_kind.as_str(), "codex" | "builtin")
        || !matches!(body.access_level.as_str(), "member" | "admin")
        || (body.access_level == "admin" && actor_level != "owner")
    {
        return Err(ApiError::invalid("Agent configuration is invalid"));
    }
    let computer_exists: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM computers WHERE id=$1 AND space_id=$2 AND deleted_at IS NULL AND connection_status='online')",
    )
    .bind(body.computer_id)
    .bind(space_id)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    if !computer_exists {
        return Err(ApiError {
            status: StatusCode::CONFLICT,
            code: "conflict",
            message: "Computer must be online in this Space",
        });
    }
    let agent_id = Uuid::now_v7();
    let handle = body.handle.map_or_else(
        || unique_handle(name, agent_id),
        |value| value.trim().to_owned(),
    );
    if handle.is_empty()
        || !handle.chars().all(|character| {
            character.is_ascii_lowercase() || character.is_ascii_digit() || character == '-'
        })
        || handle.starts_with('-')
        || handle.ends_with('-')
        || handle.contains("--")
    {
        return Err(ApiError::invalid("Agent handle is invalid"));
    }
    let now = OffsetDateTime::now_utc();
    let configuration = AgentConfiguration {
        agent_id: AgentId::from_uuid(agent_id),
        space_id: SpaceId::from_uuid(space_id),
        name: name.to_owned(),
        handle: handle.clone(),
        role: RoleSnapshot {
            revision: 1,
            text: role.to_owned(),
        },
        driver: if body.driver_kind == "codex" {
            ComputerDriverKind::Codex
        } else {
            ComputerDriverKind::Builtin
        },
    };
    let command = ComputerCommand::AgentProvision(configuration);
    let payload = serde_json::to_value(&command).map_err(|_| ApiError::internal())?;
    let mut transaction = state.pool.begin().await.map_err(map_sqlx)?;
    sqlx::query("INSERT INTO members(id,space_id,kind,display_name,handle,access_level,created_at) VALUES($1,$2,'agent',$3,$4,$5,$6)")
        .bind(agent_id).bind(space_id).bind(name).bind(&handle).bind(&body.access_level).bind(now)
        .execute(&mut *transaction).await.map_err(map_sqlx)?;
    sqlx::query("INSERT INTO agents(member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES($1,$2,$3,$4,1,'provisioning',$5,$6)")
        .bind(agent_id).bind(space_id).bind(body.computer_id).bind(role).bind(&body.driver_kind).bind(now)
        .execute(&mut *transaction).await.map_err(map_sqlx)?;
    let command_sequence: i64 = sqlx::query_scalar(
        "UPDATE computers SET next_command_seq=next_command_seq+1 WHERE id=$1 AND space_id=$2 AND deleted_at IS NULL AND connection_status='online' RETURNING next_command_seq-1",
    )
    .bind(body.computer_id)
    .bind(space_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(map_sqlx)?
    .ok_or(ApiError {
        status: StatusCode::CONFLICT,
        code: "conflict",
        message: "Computer is no longer available",
    })?;
    sqlx::query("INSERT INTO computer_commands(id,computer_id,computer_seq,kind,payload_json,created_at) VALUES($1,$2,$3,'agent.provision',$4,$5)")
        .bind(CommandId::from_uuid(Uuid::now_v7()).into_uuid())
        .bind(ComputerId::from_uuid(body.computer_id).into_uuid())
        .bind(command_sequence).bind(payload).bind(now)
        .execute(&mut *transaction).await.map_err(map_sqlx)?;
    transaction.commit().await.map_err(map_sqlx)?;

    let row = sqlx::query(
        "SELECT a.*,m.display_name,m.handle,m.access_level,c.connection_status,c.deleted_at AS computer_deleted_at,NULL::TEXT AS run_status \
         FROM agents a JOIN members m ON m.id=a.member_id JOIN computers c ON c.id=a.computer_id WHERE a.member_id=$1",
    )
    .bind(agent_id)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok((StatusCode::CREATED, Json(agent_row(&row))))
}

fn agent_row(row: &sqlx::postgres::PgRow) -> Value {
    let lifecycle: &str = row.get("lifecycle");
    let connection: &str = row.get("connection_status");
    let run_status: Option<String> = row.get("run_status");
    let retired = lifecycle == "retired";
    let desired_lifecycle = match lifecycle {
        "suspended" => "suspended",
        "retired" => "retired",
        _ => "active",
    };
    let provision_status = match lifecycle {
        "provisioning" => "provisioning",
        "error" => "error",
        _ => "ready",
    };
    let activity_status = if matches!(lifecycle, "provisioning" | "error") {
        if lifecycle == "error" {
            "error"
        } else {
            "unreachable"
        }
    } else if lifecycle == "suspended" {
        "suspended"
    } else if connection != "online" {
        "unreachable"
    } else {
        run_status.as_deref().unwrap_or("idle")
    };
    json!({
        "member_id": row.get::<Uuid,_>("member_id"),
        "space_id": row.get::<Uuid,_>("space_id"),
        "computer_id": row.get::<Uuid,_>("computer_id"),
        "name": row.get::<String,_>("display_name"),
        "handle": row.get::<String,_>("handle"),
        "access_level": row.get::<String,_>("access_level"),
        "role_text": row.get::<String,_>("role_text"),
        "role_revision": row.get::<i64,_>("role_revision"),
        "desired_lifecycle": desired_lifecycle,
        "provision_status": provision_status,
        "activity_status": activity_status,
        "driver_kind": row.get::<String,_>("driver_kind"),
        "attention_config": {"dm_immediate":true,"mention_immediate":true,"ambient_enabled":true,"ambient_debounce_seconds":30,"ambient_max_wait_seconds":300,"max_retry_count":5},
        "activity": Value::Null,
        "last_error_code": if lifecycle == "error" {Some("driver_unavailable")} else {None},
        "memory_files": [],
        "created_at": timestamp(row.get("created_at")),
        "updated_at": timestamp(row.get("created_at")),
        "retired_at": if retired {optional_timestamp(row.get("retired_at"))} else {None}
    })
}

async fn list_messages(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(channel_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    let member_id = channel_member(&state, &jar, channel_id).await?;
    let rows = sqlx::query(
        "SELECT * FROM messages WHERE channel_id=$1 AND placement='root' ORDER BY channel_seq",
    )
    .bind(channel_id)
    .fetch_all(&state.pool)
    .await
    .map_err(map_sqlx)?;
    let mut messages = Vec::with_capacity(rows.len());
    for row in rows {
        messages.push(message_row(&state.pool, &row, member_id).await?);
    }
    let snapshot: i64 = sqlx::query_scalar("SELECT next_seq-1 FROM channels WHERE id=$1")
        .bind(channel_id)
        .fetch_one(&state.pool)
        .await
        .map_err(map_sqlx)?;
    Ok(Json(
        json!({"channel_id": channel_id, "messages": messages, "snapshot_channel_seq": snapshot, "has_more_before": false, "has_more_after": false}),
    ))
}

async fn create_root_message(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(channel_id): Path<Uuid>,
    Json(body): Json<CreateMessageBody>,
) -> Result<(StatusCode, Json<Value>), ApiError> {
    let member_id = channel_member(&state, &jar, channel_id).await?;
    let message_id = insert_message(&state, channel_id, member_id, None, None, None, body).await?;
    let row = sqlx::query("SELECT * FROM messages WHERE id=$1")
        .bind(message_id)
        .fetch_one(&state.pool)
        .await
        .map_err(map_sqlx)?;
    Ok((
        StatusCode::CREATED,
        Json(message_row(&state.pool, &row, member_id).await?),
    ))
}

async fn read_thread(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(thread_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    let thread = sqlx::query("SELECT channel_id FROM threads WHERE id=$1")
        .bind(thread_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    let channel_id: Uuid = thread.get("channel_id");
    let member_id = channel_member(&state, &jar, channel_id).await?;
    let rows = sqlx::query("SELECT * FROM messages WHERE thread_id=$1 ORDER BY channel_seq")
        .bind(thread_id)
        .fetch_all(&state.pool)
        .await
        .map_err(map_sqlx)?;
    let mut projected = Vec::with_capacity(rows.len());
    for row in &rows {
        projected.push(message_row(&state.pool, row, member_id).await?);
    }
    let root = projected.first().cloned().ok_or_else(ApiError::not_found)?;
    let snapshot: i64 = sqlx::query_scalar("SELECT next_seq-1 FROM channels WHERE id=$1")
        .bind(channel_id)
        .fetch_one(&state.pool)
        .await
        .map_err(map_sqlx)?;
    Ok(Json(
        json!({"thread_id":thread_id,"channel_id":channel_id,"root":root,"replies":projected.into_iter().skip(1).collect::<Vec<_>>(),"snapshot_channel_seq":snapshot,"is_following":false,"task":Value::Null,"task_relation":Value::Null}),
    ))
}

async fn create_thread_reply(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(thread_id): Path<Uuid>,
    Json(body): Json<CreateMessageBody>,
) -> Result<(StatusCode, Json<Value>), ApiError> {
    let row = sqlx::query("SELECT channel_id FROM threads WHERE id=$1")
        .bind(thread_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    let channel_id: Uuid = row.get("channel_id");
    let member_id = channel_member(&state, &jar, channel_id).await?;
    let message_id = insert_message(
        &state,
        channel_id,
        member_id,
        Some(thread_id),
        None,
        None,
        body,
    )
    .await?;
    let row = sqlx::query("SELECT * FROM messages WHERE id=$1")
        .bind(message_id)
        .fetch_one(&state.pool)
        .await
        .map_err(map_sqlx)?;
    Ok((
        StatusCode::CREATED,
        Json(message_row(&state.pool, &row, member_id).await?),
    ))
}

async fn list_tasks(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    current_member(&state, &jar, space_id).await?;
    let ids = sqlx::query_scalar::<_, Uuid>(
        "SELECT id FROM tasks WHERE space_id=$1 ORDER BY updated_at DESC,id DESC",
    )
    .bind(space_id)
    .fetch_all(&state.pool)
    .await
    .map_err(map_sqlx)?;
    let mut tasks = Vec::with_capacity(ids.len());
    for id in ids {
        tasks.push(task_projection(&state.pool, id).await?);
    }
    Ok(Json(Value::Array(tasks)))
}

async fn get_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(task_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    let space_id = sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM tasks WHERE id=$1")
        .bind(task_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    current_member(&state, &jar, space_id).await?;
    Ok(Json(task_projection(&state.pool, task_id).await?))
}

async fn create_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(message_id): Path<Uuid>,
    Json(body): Json<CreateTaskBody>,
) -> Result<(StatusCode, Json<Value>), ApiError> {
    let source = sqlx::query(
        "SELECT m.space_id,m.thread_id,m.body_markdown FROM messages m WHERE m.id=$1 AND m.placement='root'",
    )
    .bind(message_id)
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::not_found)?;
    let actor = current_member(&state, &jar, source.get("space_id")).await?;
    let title = body
        .title
        .filter(|title| !title.trim().is_empty())
        .unwrap_or_else(|| {
            source
                .get::<String, _>("body_markdown")
                .chars()
                .take(120)
                .collect()
        });
    let context = super::http::write_context(
        super::http::AuthenticationSurface::Browser,
        super::http::AuthenticationSurface::Browser,
        headers
            .get("Idempotency-Key")
            .and_then(|value| value.to_str().ok()),
    )
    .map_err(|_| ApiError::invalid("Idempotency-Key must be a UUID"))?;
    let task = super::http::create_task(
        &state.storage,
        super::http::BrowserPrincipal {
            member_id: MemberId::from_uuid(actor),
        },
        MessageId::from_uuid(message_id),
        context,
        super::http::CreateTaskBody {
            title,
            assignee_agent_member_id: body.assignee_agent_member_id.map(MemberId::from_uuid),
        },
    )
    .await
    .map_err(|_| ApiError {
        status: StatusCode::CONFLICT,
        code: "conflict",
        message: "Task could not be created from this Root Message",
    })?;
    Ok((
        StatusCode::CREATED,
        Json(task_projection(&state.pool, task.0.id.into_uuid()).await?),
    ))
}

async fn link_task_thread(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(task_id): Path<Uuid>,
    Json(body): Json<LinkThreadBody>,
) -> Result<Json<Value>, ApiError> {
    let actor = task_actor(&state, &jar, task_id).await?;
    let mut storage = state.storage.clone();
    LinkThreadToTask::execute(
        &mut storage,
        LinkThreadInput {
            task_id: TaskId::from_uuid(task_id),
            target_thread_id: ThreadId::from_uuid(body.thread_id),
            actor_member_id: MemberId::from_uuid(actor),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(task_projection(&state.pool, task_id).await?))
}

async fn start_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(task_id): Path<Uuid>,
    Json(body): Json<StartTaskBody>,
) -> Result<Json<Value>, ApiError> {
    update_task_action(
        &state,
        &jar,
        task_id,
        TaskAction::Start {
            assignee: MemberId::from_uuid(body.assignee_agent_member_id),
        },
    )
    .await
}

async fn submit_task_review(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(task_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    update_task_action(&state, &jar, task_id, TaskAction::SubmitReview).await
}

async fn request_task_changes(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(task_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    update_task_action(&state, &jar, task_id, TaskAction::RequestChanges).await
}

async fn close_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(task_id): Path<Uuid>,
    Json(body): Json<CloseTaskBody>,
) -> Result<Json<Value>, ApiError> {
    let reason = match body.reason.as_str() {
        "invalid" => CloseReason::Invalid,
        "duplicate" => CloseReason::Duplicate,
        "not_needed" => CloseReason::NotNeeded,
        "obsolete" => CloseReason::Obsolete,
        "other" => CloseReason::Other,
        _ => return Err(ApiError::invalid("close reason is invalid")),
    };
    update_task_action(
        &state,
        &jar,
        task_id,
        TaskAction::Close {
            reason,
            note: body.note,
        },
    )
    .await
}

async fn complete_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(task_id): Path<Uuid>,
    Json(body): Json<CompleteTaskBody>,
) -> Result<Json<Value>, ApiError> {
    let actor = task_actor(&state, &jar, task_id).await?;
    let mut storage = state.storage.clone();
    CompleteTask::execute(
        &mut storage,
        CompleteTaskInput {
            task_id: TaskId::from_uuid(task_id),
            actor_member_id: MemberId::from_uuid(actor),
            result_message_id: MessageId::from_uuid(Uuid::now_v7()),
            result_thread_id: ThreadId::from_uuid(body.result_thread_id),
            result_markdown: body.result_markdown,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(task_projection(&state.pool, task_id).await?))
}

async fn update_task_action(
    state: &RuntimeState,
    jar: &CookieJar,
    task_id: Uuid,
    action: TaskAction,
) -> Result<Json<Value>, ApiError> {
    let actor = task_actor(state, jar, task_id).await?;
    let mut storage = state.storage.clone();
    UpdateTask::execute(
        &mut storage,
        UpdateTaskInput {
            task_id: TaskId::from_uuid(task_id),
            actor_member_id: MemberId::from_uuid(actor),
            action,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(task_projection(&state.pool, task_id).await?))
}

async fn task_actor(
    state: &RuntimeState,
    jar: &CookieJar,
    task_id: Uuid,
) -> Result<Uuid, ApiError> {
    let space_id = sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM tasks WHERE id=$1")
        .bind(task_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    current_member(state, jar, space_id).await
}

async fn insert_message(
    state: &RuntimeState,
    channel_id: Uuid,
    author: Uuid,
    thread_id: Option<Uuid>,
    handled_item: Option<(Uuid, Uuid)>,
    expected_snapshot: Option<u64>,
    body: CreateMessageBody,
) -> Result<Uuid, ApiError> {
    if body.body_markdown.trim().is_empty() {
        return Err(ApiError::invalid("Message body is required"));
    }
    let message_id = Uuid::now_v7();
    let effective_thread = thread_id.unwrap_or(message_id);
    let now = OffsetDateTime::now_utc();
    let mut transaction = state.pool.begin().await.map_err(map_sqlx)?;
    sqlx::query("SET CONSTRAINTS ALL DEFERRED")
        .execute(&mut *transaction)
        .await
        .map_err(map_sqlx)?;
    let channel =
        sqlx::query("SELECT space_id,next_seq-1 AS snapshot FROM channels WHERE id=$1 FOR UPDATE")
            .bind(channel_id)
            .fetch_one(&mut *transaction)
            .await
            .map_err(map_sqlx)?;
    let space_id: Uuid = channel.get("space_id");
    let snapshot: i64 = channel.get("snapshot");
    if expected_snapshot.is_some_and(|expected| u64::try_from(snapshot).ok() != Some(expected)) {
        return Err(ApiError::context_changed());
    }
    let seq: i64 = sqlx::query_scalar(
        "UPDATE channels SET next_seq=next_seq+1 WHERE id=$1 RETURNING next_seq-1",
    )
    .bind(channel_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(map_sqlx)?;
    sqlx::query("INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,reply_to_message_id,author_member_id,body_markdown,created_at) VALUES($1,$2,$3,$4,$5,$6,'text',$7,$8,$9,$10)")
        .bind(message_id).bind(space_id).bind(channel_id).bind(effective_thread).bind(seq)
        .bind(if thread_id.is_some(){"reply"}else{"root"}).bind(body.reply_to_message_id)
        .bind(author).bind(body.body_markdown).bind(now)
        .execute(&mut *transaction).await.map_err(map_sqlx)?;
    if thread_id.is_none() {
        sqlx::query("INSERT INTO threads(id,space_id,channel_id,root_message_id,created_at) VALUES($1,$2,$3,$1,$4)")
            .bind(message_id).bind(space_id).bind(channel_id).bind(now)
            .execute(&mut *transaction).await.map_err(map_sqlx)?;
    }
    for (position, attachment_id) in body.attachment_ids.into_iter().enumerate() {
        sqlx::query("INSERT INTO message_attachments(message_id,attachment_id,space_id,position) VALUES($1,$2,$3,$4)")
            .bind(message_id).bind(attachment_id).bind(space_id).bind(position as i32)
            .execute(&mut *transaction).await.map_err(map_sqlx)?;
    }
    if let Some((run_id, item_id)) = handled_item {
        let updated = sqlx::query(
            "UPDATE run_items SET disposition='handled' \
             WHERE run_id=$1 AND inbox_item_id=$2 \
               AND (disposition IS NULL OR disposition='handled') RETURNING inbox_item_id",
        )
        .bind(run_id)
        .bind(item_id)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(map_sqlx)?;
        if updated.is_none() {
            return Err(ApiError {
                status: StatusCode::CONFLICT,
                code: "conflict",
                message: "Inbox Item is not leased by the current Run",
            });
        }
    }
    let recipients = sqlx::query("SELECT m.id FROM channel_members cm JOIN members m ON m.id=cm.member_id WHERE cm.channel_id=$1 AND m.kind='agent' AND m.id<>$2")
        .bind(channel_id).bind(author).fetch_all(&mut *transaction).await.map_err(map_sqlx)?;
    let mentioned = body.mentions.into_iter().collect::<BTreeSet<_>>();
    for recipient in recipients {
        let agent_id: Uuid = recipient.get("id");
        let hard = mentioned.contains(&agent_id);
        sqlx::query("INSERT INTO inbox_items(id,space_id,agent_id,message_id,thread_id,kind,strength,status,available_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,'pending',$8,$8)")
            .bind(Uuid::now_v7()).bind(space_id).bind(agent_id).bind(message_id).bind(effective_thread)
            .bind(if hard{"mention"}else{"channel_activity"}).bind(if hard{"hard"}else{"ambient"}).bind(now)
            .execute(&mut *transaction).await.map_err(map_sqlx)?;
    }
    transaction.commit().await.map_err(map_sqlx)?;
    Ok(message_id)
}

async fn require_active_agent_run(
    state: &RuntimeState,
    headers: &HeaderMap,
    computer_id: Uuid,
    agent_id: Uuid,
    run_id: Uuid,
) -> Result<Uuid, ApiError> {
    let computer_token = bearer_token(headers)?;
    let fencing_token = headers
        .get("x-sumi-fencing-token")
        .and_then(|value| value.to_str().ok())
        .ok_or_else(ApiError::unauthenticated)?;
    sqlx::query_scalar(
        "SELECT runs.space_id FROM agent_runs runs \
         JOIN agents ON agents.member_id=runs.agent_id \
         JOIN computers ON computers.id=agents.computer_id \
         WHERE computers.id=$1 AND computers.token_hash=$2 AND computers.deleted_at IS NULL \
         AND agents.member_id=$3 AND runs.id=$4 AND runs.status='running' \
         AND runs.fencing_token_hash=$5 AND runs.lease_expires_at>now()",
    )
    .bind(computer_id)
    .bind(token_hash(computer_token))
    .bind(agent_id)
    .bind(run_id)
    .bind(token_hash(fencing_token))
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::unauthenticated)
}

async fn agent_create_upload(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, agent_id, run_id)): Path<(Uuid, Uuid, Uuid)>,
    Json(body): Json<AgentCreateUploadBody>,
) -> Result<(StatusCode, Json<Value>), ApiError> {
    let space_id =
        require_active_agent_run(&state, &headers, computer_id, agent_id, run_id).await?;
    if body.original_name.trim().is_empty() || body.media_type.trim().is_empty() {
        return Err(ApiError::invalid(
            "Attachment name and media type are required",
        ));
    }
    let key = idempotency_header(&headers)?;
    let mut transaction = state.pool.begin().await.map_err(map_sqlx)?;
    if let Some(existing_id) = sqlx::query_scalar::<_, Uuid>(
        "SELECT resource_id FROM idempotency_records \
         WHERE actor_member_id=$1 AND action='attachment.upload.create' AND idempotency_key=$2",
    )
    .bind(agent_id)
    .bind(key)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(map_sqlx)?
    {
        let row = sqlx::query("SELECT * FROM attachments WHERE id=$1")
            .bind(existing_id)
            .fetch_one(&mut *transaction)
            .await
            .map_err(map_sqlx)?;
        transaction.commit().await.map_err(map_sqlx)?;
        return Ok((StatusCode::OK, Json(attachment_row(&row))));
    }
    let id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let object_key = format!("spaces/{space_id}/attachments/{id}");
    sqlx::query("INSERT INTO attachments(id,space_id,uploader_member_id,name,media_type,object_key,status,created_at) VALUES($1,$2,$3,$4,$5,$6,'uploading',$7)")
        .bind(id).bind(space_id).bind(agent_id).bind(body.original_name.trim()).bind(body.media_type.trim()).bind(object_key).bind(now)
        .execute(&mut *transaction).await.map_err(map_sqlx)?;
    insert_attachment_write_records(
        &mut transaction,
        space_id,
        agent_id,
        "attachment.upload.create",
        key,
        id,
        "attachment.created",
        now,
    )
    .await?;
    transaction.commit().await.map_err(map_sqlx)?;
    let row = sqlx::query("SELECT * FROM attachments WHERE id=$1")
        .bind(id)
        .fetch_one(&state.pool)
        .await
        .map_err(map_sqlx)?;
    Ok((StatusCode::CREATED, Json(attachment_row(&row))))
}

async fn agent_upload_content(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, agent_id, run_id, attachment_id)): Path<(Uuid, Uuid, Uuid, Uuid)>,
    body: Bytes,
) -> Result<StatusCode, ApiError> {
    require_active_agent_run(&state, &headers, computer_id, agent_id, run_id).await?;
    let object_key = sqlx::query_scalar::<_, String>(
        "SELECT object_key FROM attachments \
         WHERE id=$1 AND uploader_member_id=$2 AND status='uploading'",
    )
    .bind(attachment_id)
    .bind(agent_id)
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::not_found)?;
    state
        .objects
        .put(&object_key, body.to_vec())
        .await
        .map_err(application_error)?;
    Ok(StatusCode::NO_CONTENT)
}

async fn agent_complete_upload(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, agent_id, run_id, attachment_id)): Path<(Uuid, Uuid, Uuid, Uuid)>,
    Json(body): Json<CompleteUploadBody>,
) -> Result<Json<Value>, ApiError> {
    let space_id =
        require_active_agent_run(&state, &headers, computer_id, agent_id, run_id).await?;
    let key = idempotency_header(&headers)?;
    let row = sqlx::query(
        "SELECT object_key,status FROM attachments WHERE id=$1 AND uploader_member_id=$2",
    )
    .bind(attachment_id)
    .bind(agent_id)
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::not_found)?;
    let content = state
        .objects
        .get(row.get("object_key"))
        .await
        .map_err(application_error)?;
    let digest = Sha256::digest(&content);
    if content.len() as u64 != body.size || hex::encode(digest) != body.sha256.to_lowercase() {
        return Err(ApiError::invalid(
            "Attachment size or SHA-256 does not match uploaded content",
        ));
    }
    let now = OffsetDateTime::now_utc();
    let mut transaction = state.pool.begin().await.map_err(map_sqlx)?;
    let already_applied: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM idempotency_records \
         WHERE actor_member_id=$1 AND action='attachment.upload.complete' AND idempotency_key=$2)",
    )
    .bind(agent_id)
    .bind(key)
    .fetch_one(&mut *transaction)
    .await
    .map_err(map_sqlx)?;
    if !already_applied {
        let changed = sqlx::query("UPDATE attachments SET length=$2,sha256=$3,status='ready',ready_at=$4 WHERE id=$1 AND uploader_member_id=$5 AND status='uploading'")
            .bind(attachment_id).bind(i64::try_from(body.size).map_err(|_|ApiError::invalid("Attachment is too large"))?).bind(digest.as_slice()).bind(now).bind(agent_id)
            .execute(&mut *transaction).await.map_err(map_sqlx)?;
        if changed.rows_affected() != 1 {
            return Err(ApiError {
                status: StatusCode::CONFLICT,
                code: "conflict",
                message: "Attachment upload is not open",
            });
        }
        insert_attachment_write_records(
            &mut transaction,
            space_id,
            agent_id,
            "attachment.upload.complete",
            key,
            attachment_id,
            "attachment.ready",
            now,
        )
        .await?;
    }
    let row = sqlx::query("SELECT * FROM attachments WHERE id=$1")
        .bind(attachment_id)
        .fetch_one(&mut *transaction)
        .await
        .map_err(map_sqlx)?;
    transaction.commit().await.map_err(map_sqlx)?;
    Ok(Json(attachment_row(&row)))
}

async fn agent_download_attachment(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, agent_id, run_id, attachment_id)): Path<(Uuid, Uuid, Uuid, Uuid)>,
) -> Result<Bytes, ApiError> {
    require_active_agent_run(&state, &headers, computer_id, agent_id, run_id).await?;
    let row = sqlx::query(
        "SELECT object_key FROM attachments WHERE id=$1 AND status='ready' AND ( \
         uploader_member_id=$2 OR EXISTS(SELECT 1 FROM message_attachments links \
         JOIN messages ON messages.id=links.message_id \
         JOIN channel_members members ON members.channel_id=messages.channel_id \
         WHERE links.attachment_id=attachments.id AND members.member_id=$2))",
    )
    .bind(attachment_id)
    .bind(agent_id)
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::not_found)?;
    state
        .objects
        .get(row.get("object_key"))
        .await
        .map(Bytes::from)
        .map_err(application_error)
}

#[allow(clippy::too_many_arguments)]
async fn insert_attachment_write_records(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    space_id: Uuid,
    actor_id: Uuid,
    action: &str,
    key: Uuid,
    attachment_id: Uuid,
    event_kind: &str,
    now: OffsetDateTime,
) -> Result<(), ApiError> {
    let result_hash = Sha256::digest(attachment_id.as_bytes());
    sqlx::query("INSERT INTO idempotency_records(actor_member_id,action,idempotency_key,response_code,resource_id,result_hash,created_at) VALUES($1,$2,$3,'ok',$4,$5,$6)")
        .bind(actor_id).bind(action).bind(key).bind(attachment_id).bind(result_hash.as_slice()).bind(now)
        .execute(&mut **transaction).await.map_err(map_sqlx)?;
    sqlx::query("INSERT INTO audit_events(id,space_id,actor_member_id,action,subject_type,subject_id,created_at) VALUES($1,$2,$3,$4,'attachment',$5,$6)")
        .bind(Uuid::now_v7()).bind(space_id).bind(actor_id).bind(action).bind(attachment_id).bind(now)
        .execute(&mut **transaction).await.map_err(map_sqlx)?;
    sqlx::query("INSERT INTO outbox_events(id,space_id,kind,payload_json,created_at) VALUES($1,$2,$3,$4,$5)")
        .bind(Uuid::now_v7()).bind(space_id).bind(event_kind).bind(json!({"attachment_id":attachment_id})).bind(now)
        .execute(&mut **transaction).await.map_err(map_sqlx)?;
    Ok(())
}

fn idempotency_header(headers: &HeaderMap) -> Result<Uuid, ApiError> {
    headers
        .get("idempotency-key")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| Uuid::parse_str(value).ok())
        .ok_or_else(|| ApiError::invalid("Idempotency-Key must be a UUID"))
}

async fn create_upload(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Json(body): Json<CreateUploadBody>,
) -> Result<(StatusCode, Json<Value>), ApiError> {
    let member = current_member(&state, &jar, body.space_id).await?;
    if body.original_name.trim().is_empty() || body.media_type.trim().is_empty() {
        return Err(ApiError::invalid(
            "Attachment name and media type are required",
        ));
    }
    let id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let object_key = format!("spaces/{}/attachments/{}", body.space_id, id);
    sqlx::query("INSERT INTO attachments(id,space_id,uploader_member_id,name,media_type,object_key,status,created_at) VALUES($1,$2,$3,$4,$5,$6,'uploading',$7)")
        .bind(id).bind(body.space_id).bind(member).bind(body.original_name).bind(body.media_type).bind(object_key).bind(now)
        .execute(&state.pool).await.map_err(map_sqlx)?;
    let row = sqlx::query("SELECT * FROM attachments WHERE id=$1")
        .bind(id)
        .fetch_one(&state.pool)
        .await
        .map_err(map_sqlx)?;
    let mut value = attachment_row(&row);
    value["upload_path"] = json!(format!("/api/v1/attachments/{id}/content"));
    Ok((StatusCode::CREATED, Json(value)))
}

async fn upload_content(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(attachment_id): Path<Uuid>,
    body: Bytes,
) -> Result<StatusCode, ApiError> {
    let row = sqlx::query("SELECT space_id,object_key,status FROM attachments WHERE id=$1")
        .bind(attachment_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    current_member(&state, &jar, row.get("space_id")).await?;
    if row.get::<&str, _>("status") != "uploading" {
        return Err(ApiError {
            status: StatusCode::CONFLICT,
            code: "conflict",
            message: "Attachment upload is not open",
        });
    }
    state
        .objects
        .put(row.get("object_key"), body.to_vec())
        .await
        .map_err(application_error)?;
    Ok(StatusCode::NO_CONTENT)
}

async fn complete_upload(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(attachment_id): Path<Uuid>,
    Json(body): Json<CompleteUploadBody>,
) -> Result<Json<Value>, ApiError> {
    let row = sqlx::query("SELECT space_id,object_key,status FROM attachments WHERE id=$1")
        .bind(attachment_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    current_member(&state, &jar, row.get("space_id")).await?;
    let content = state
        .objects
        .get(row.get("object_key"))
        .await
        .map_err(application_error)?;
    let digest = Sha256::digest(&content);
    if content.len() as u64 != body.size || hex::encode(digest) != body.sha256.to_lowercase() {
        return Err(ApiError::invalid(
            "Attachment size or SHA-256 does not match uploaded content",
        ));
    }
    sqlx::query("UPDATE attachments SET length=$2,sha256=$3,status='ready',ready_at=$4 WHERE id=$1 AND status='uploading'")
        .bind(attachment_id).bind(i64::try_from(body.size).map_err(|_|ApiError::invalid("Attachment is too large"))?).bind(digest.as_slice()).bind(OffsetDateTime::now_utc())
        .execute(&state.pool).await.map_err(map_sqlx)?;
    let row = sqlx::query("SELECT * FROM attachments WHERE id=$1")
        .bind(attachment_id)
        .fetch_one(&state.pool)
        .await
        .map_err(map_sqlx)?;
    let mut value = attachment_row(&row);
    value["download_path"] = json!(format!("/api/v1/attachments/{attachment_id}/download"));
    Ok(Json(value))
}

async fn download_attachment(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(attachment_id): Path<Uuid>,
) -> Result<Bytes, ApiError> {
    let row = sqlx::query("SELECT space_id,object_key,status FROM attachments WHERE id=$1")
        .bind(attachment_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    current_member(&state, &jar, row.get("space_id")).await?;
    if row.get::<&str, _>("status") != "ready" {
        return Err(ApiError::not_found());
    }
    state
        .objects
        .get(row.get("object_key"))
        .await
        .map(Bytes::from)
        .map_err(application_error)
}

struct BrowserUser {
    id: Uuid,
    display_name: String,
    email: String,
}

async fn authenticate(state: &RuntimeState, jar: &CookieJar) -> Result<BrowserUser, ApiError> {
    let cookie = jar
        .get(SESSION_COOKIE)
        .ok_or_else(ApiError::unauthenticated)?;
    let row = sqlx::query("SELECT u.id,u.display_name,u.email_normalized FROM browser_sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>now() AND u.disabled_at IS NULL")
        .bind(token_hash(cookie.value())).fetch_optional(&state.pool).await.map_err(map_sqlx)?.ok_or_else(ApiError::unauthenticated)?;
    Ok(BrowserUser {
        id: row.get("id"),
        display_name: row.get("display_name"),
        email: row.get("email_normalized"),
    })
}

async fn current_member(
    state: &RuntimeState,
    jar: &CookieJar,
    space_id: Uuid,
) -> Result<Uuid, ApiError> {
    let user = authenticate(state, jar).await?;
    sqlx::query_scalar("SELECT member_id FROM human_members WHERE space_id=$1 AND user_id=$2")
        .bind(space_id)
        .bind(user.id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)
}

async fn channel_member(
    state: &RuntimeState,
    jar: &CookieJar,
    channel_id: Uuid,
) -> Result<Uuid, ApiError> {
    let user = authenticate(state, jar).await?;
    sqlx::query_scalar("SELECT hm.member_id FROM channels c JOIN human_members hm ON hm.space_id=c.space_id JOIN channel_members cm ON cm.channel_id=c.id AND cm.member_id=hm.member_id WHERE c.id=$1 AND hm.user_id=$2")
        .bind(channel_id).bind(user.id).fetch_optional(&state.pool).await.map_err(map_sqlx)?.ok_or_else(ApiError::not_found)
}

async fn create_session(
    state: &RuntimeState,
    jar: CookieJar,
    user_id: Uuid,
    now: OffsetDateTime,
) -> Result<(CookieJar, Uuid), ApiError> {
    let session_id = Uuid::now_v7();
    let token = format!("{}{}", Uuid::now_v7().simple(), Uuid::now_v7().simple());
    sqlx::query("INSERT INTO browser_sessions(id,user_id,token_hash,expires_at,last_seen_at,created_at) VALUES($1,$2,$3,$4,$5,$5)")
        .bind(session_id).bind(user_id).bind(token_hash(&token)).bind(now + Duration::hours(state.session_ttl_hours)).bind(now)
        .execute(&state.pool).await.map_err(map_sqlx)?;
    let cookie = Cookie::build((SESSION_COOKIE, token))
        .path("/")
        .http_only(true)
        .same_site(SameSite::Lax)
        .build();
    Ok((jar.add(cookie), session_id))
}

fn user_json(id: Uuid, display_name: &str, email: &str) -> Value {
    json!({"id":id,"display_name":display_name,"email":email})
}
fn space_json(
    id: Uuid,
    name: &str,
    slug: &str,
    owner: Uuid,
    current: Uuid,
    general: Uuid,
) -> Value {
    json!({"id":id,"name":name,"slug":slug,"accent":"#FFD440","owner_member_id":owner,"current_member_id":current,"general_channel_id":general})
}
fn space_row(row: &sqlx::postgres::PgRow) -> Value {
    space_json(
        row.get("id"),
        row.get("name"),
        row.get("slug"),
        row.get("owner_member_id"),
        row.get("current_member_id"),
        row.get("general_channel_id"),
    )
}
fn channel_row(row: &sqlx::postgres::PgRow, creator: Uuid) -> Value {
    let slug: String = row.get("slug");
    json!({"id":row.get::<Uuid,_>("id"),"space_id":row.get::<Uuid,_>("space_id"),"name":slug,"slug":row.get::<String,_>("slug"),"topic":row.get::<Option<String>,_>("topic"),"kind":row.get::<String,_>("kind"),"created_by_member_id":creator,"joined":row.get::<bool,_>("joined"),"archived_at":optional_timestamp(row.get("archived_at"))})
}
async fn member_row(pool: &PgPool, row: &sqlx::postgres::PgRow) -> Result<Value, ApiError> {
    let id: Uuid = row.get("id");
    let permissions = sqlx::query_scalar::<_, String>(
        "SELECT action_code FROM member_permissions WHERE member_id=$1 ORDER BY action_code",
    )
    .bind(id)
    .fetch_all(pool)
    .await
    .map_err(map_sqlx)?;
    Ok(
        json!({"id":id,"kind":row.get::<String,_>("kind"),"display_name":row.get::<String,_>("display_name"),"handle":row.get::<String,_>("handle"),"access_level":row.get::<String,_>("access_level"),"permissions":permissions}),
    )
}
async fn message_row(
    pool: &PgPool,
    row: &sqlx::postgres::PgRow,
    _viewer: Uuid,
) -> Result<Value, ApiError> {
    let id: Uuid = row.get("id");
    let author = sqlx::query("SELECT id,kind,display_name,handle FROM members WHERE id=$1")
        .bind(row.get::<Uuid, _>("author_member_id"))
        .fetch_one(pool)
        .await
        .map_err(map_sqlx)?;
    let replies: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM messages WHERE thread_id=$1 AND placement='reply'",
    )
    .bind(row.get::<Uuid, _>("thread_id"))
    .fetch_one(pool)
    .await
    .map_err(map_sqlx)?;
    let attachments=sqlx::query("SELECT a.* FROM attachments a JOIN message_attachments ma ON ma.attachment_id=a.id WHERE ma.message_id=$1 ORDER BY ma.position").bind(id).fetch_all(pool).await.map_err(map_sqlx)?;
    Ok(
        json!({"id":id,"channel_id":row.get::<Uuid,_>("channel_id"),"thread_id":row.get::<Uuid,_>("thread_id"),"seq":row.get::<i64,_>("channel_seq"),"placement":row.get::<String,_>("placement"),"content":{"type":"text","body_markdown":row.get::<Option<String>,_>("body_markdown").unwrap_or_default()},"author":{"id":author.get::<Uuid,_>("id"),"kind":author.get::<String,_>("kind"),"display_name":author.get::<String,_>("display_name"),"handle":author.get::<String,_>("handle")},"mentions":[],"attachments":attachments.iter().map(attachment_row).collect::<Vec<_>>(),"reply_count":replies,"task":Value::Null,"created_at":timestamp(row.get("created_at")),"edited_at":optional_timestamp(row.get("edited_at")),"deleted_at":optional_timestamp(row.get("deleted_at"))}),
    )
}
fn attachment_row(row: &sqlx::postgres::PgRow) -> Value {
    json!({"id":row.get::<Uuid,_>("id"),"space_id":row.get::<Uuid,_>("space_id"),"uploader_member_id":row.get::<Uuid,_>("uploader_member_id"),"original_name":row.get::<String,_>("name"),"media_type":row.get::<String,_>("media_type"),"size":row.get::<Option<i64>,_>("length"),"sha256":row.get::<Option<Vec<u8>>,_>("sha256").map(hex::encode),"status":row.get::<String,_>("status"),"upload_path":Value::Null,"download_path":Value::Null,"created_at":timestamp(row.get("created_at"))})
}
async fn task_projection(pool: &PgPool, task_id: Uuid) -> Result<Value, ApiError> {
    let row=sqlx::query("SELECT t.*,creator.display_name AS creator_name,assignee.display_name AS assignee_name FROM tasks t JOIN members creator ON creator.id=t.creator_member_id LEFT JOIN members assignee ON assignee.id=t.assignee_agent_member_id WHERE t.id=$1").bind(task_id).fetch_optional(pool).await.map_err(map_sqlx)?.ok_or_else(ApiError::not_found)?;
    let source = thread_reference(pool, row.get("source_thread_id"), "source").await?;
    let related_ids = sqlx::query_scalar::<_, Uuid>(
        "SELECT thread_id FROM task_threads WHERE task_id=$1 ORDER BY linked_at",
    )
    .bind(task_id)
    .fetch_all(pool)
    .await
    .map_err(map_sqlx)?;
    let mut related = Vec::with_capacity(related_ids.len());
    for id in related_ids {
        related.push(thread_reference(pool, id, "related").await?);
    }
    let result_message = if let Some(message_id) = row.get::<Option<Uuid>, _>("result_message_id") {
        let message = sqlx::query("SELECT * FROM messages WHERE id=$1")
            .bind(message_id)
            .fetch_one(pool)
            .await
            .map_err(map_sqlx)?;
        Some(message_row(pool, &message, row.get("creator_member_id")).await?)
    } else {
        None
    };
    Ok(json!({
        "id":task_id,"space_id":row.get::<Uuid,_>("space_id"),"title":row.get::<String,_>("title"),"status":row.get::<String,_>("status"),
        "source_thread":source,"related_threads":related,"creator_member_id":row.get::<Uuid,_>("creator_member_id"),"creator_name":row.get::<String,_>("creator_name"),
        "assignee_agent_member_id":row.get::<Option<Uuid>,_>("assignee_agent_member_id"),"assignee_name":row.get::<Option<String>,_>("assignee_name"),
        "result_message":result_message,"close_reason_code":row.get::<Option<String>,_>("close_reason_code"),"close_reason_note":row.get::<Option<String>,_>("close_reason_note"),
        "current_run":Value::Null,"recent_runs":[],"session_continuity":{"state":"unavailable","generation":Value::Null,"reason_code":Value::Null},"runtime_issue_code":Value::Null,
        "created_at":timestamp(row.get("created_at")),"updated_at":timestamp(row.get("updated_at")),"finished_at":optional_timestamp(row.get("finished_at"))
    }))
}
async fn thread_reference(
    pool: &PgPool,
    thread_id: Uuid,
    relation: &str,
) -> Result<Value, ApiError> {
    let row=sqlx::query("SELECT t.id,t.root_message_id,t.channel_id,c.slug,m.channel_seq FROM threads t JOIN channels c ON c.id=t.channel_id JOIN messages m ON m.id=t.root_message_id WHERE t.id=$1").bind(thread_id).fetch_one(pool).await.map_err(map_sqlx)?;
    Ok(
        json!({"id":thread_id,"root_message_id":row.get::<Uuid,_>("root_message_id"),"channel_id":row.get::<Uuid,_>("channel_id"),"channel_slug":row.get::<String,_>("slug"),"root_message_seq":row.get::<i64,_>("channel_seq"),"relation":relation}),
    )
}
fn bearer_token(headers: &HeaderMap) -> Result<&str, ApiError> {
    headers
        .get("Authorization")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.strip_prefix("Bearer "))
        .filter(|value| !value.is_empty())
        .ok_or_else(ApiError::unauthenticated)
}
fn application_error(error: crate::server::application::ports::ApplicationError) -> ApiError {
    use crate::server::application::ports::ApplicationError;
    match error {
        ApplicationError::NotFound => ApiError::not_found(),
        ApplicationError::PermissionDenied => ApiError {
            status: StatusCode::FORBIDDEN,
            code: "permission_denied",
            message: "actor is not allowed to perform this action",
        },
        ApplicationError::ContextChanged => ApiError::context_changed(),
        ApplicationError::Domain(_) | ApplicationError::Conflict => ApiError {
            status: StatusCode::CONFLICT,
            code: "conflict",
            message: "request conflicts with current state",
        },
        ApplicationError::Unavailable => ApiError {
            status: StatusCode::SERVICE_UNAVAILABLE,
            code: "unavailable",
            message: "dependency is unavailable",
        },
        ApplicationError::Internal => ApiError::internal(),
    }
}
fn token_hash(token: &str) -> String {
    hex::encode(Sha256::digest(token.as_bytes()))
}
fn timestamp(value: OffsetDateTime) -> String {
    value
        .format(&time::format_description::well_known::Rfc3339)
        .expect("OffsetDateTime formats as RFC3339")
}
fn optional_timestamp(value: Option<OffsetDateTime>) -> Option<String> {
    value.map(timestamp)
}
fn unique_handle(name: &str, id: Uuid) -> String {
    let base = name
        .chars()
        .filter(|c| c.is_ascii_alphanumeric())
        .collect::<String>()
        .to_lowercase();
    format!(
        "{}-{}",
        if base.is_empty() { "member" } else { &base },
        &id.simple().to_string()[..8]
    )
}
fn map_sqlx(error: sqlx::Error) -> ApiError {
    tracing::warn!(code=?error.as_database_error().and_then(|e|e.code()),"database operation failed");
    if error
        .as_database_error()
        .is_some_and(|e| e.is_unique_violation())
    {
        ApiError {
            status: StatusCode::CONFLICT,
            code: "conflict",
            message: "resource already exists",
        }
    } else {
        ApiError::internal()
    }
}
async fn empty_list(
    State(state): State<RuntimeState>,
    jar: CookieJar,
) -> Result<Json<Value>, ApiError> {
    authenticate(&state, &jar).await?;
    Ok(Json(json!([])))
}
async fn shutdown_signal() {
    let _ = tokio::signal::ctrl_c().await;
}

#[cfg(test)]
mod tests {
    use std::{str::FromStr, sync::Arc};

    use axum::http::{HeaderMap, HeaderValue};
    use object_store::local::LocalFileSystem;
    use sqlx::{Connection, PgConnection, postgres::PgConnectOptions};
    use tempfile::TempDir;
    use url::Url;

    use super::*;

    struct CapabilityFixture {
        state: RuntimeState,
        admin: PgConnection,
        database_name: String,
        _objects: TempDir,
        headers: HeaderMap,
        computer_id: Uuid,
        context: capability::RunContext,
        handled_item_id: InboxItemId,
        deferred_item_id: InboxItemId,
    }

    impl CapabilityFixture {
        async fn create() -> Self {
            let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
                .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
            let database_name = format!("sumi_capability_{}", Uuid::now_v7().simple());
            let mut admin =
                PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url).unwrap())
                    .await
                    .unwrap();
            sqlx::query(&format!("CREATE DATABASE \"{database_name}\""))
                .execute(&mut admin)
                .await
                .unwrap();
            let mut database_url = Url::parse(&admin_url).unwrap();
            database_url.set_path(&format!("/{database_name}"));
            let pool = PgPool::connect(database_url.as_str()).await.unwrap();
            let storage = PostgresAdapter::new(pool.clone());
            storage.migrate().await.unwrap();

            let space_id = Uuid::now_v7();
            let owner_id = Uuid::now_v7();
            let agent_id = Uuid::now_v7();
            let computer_id = Uuid::now_v7();
            let channel_id = Uuid::now_v7();
            let focus_id = Uuid::now_v7();
            let run_id = Uuid::now_v7();
            let handled_item_id = Uuid::now_v7();
            let deferred_item_id = Uuid::now_v7();
            let computer_token = "capability-computer-token";
            let fencing_token = "capability-fencing-token";
            sqlx::raw_sql(&format!(
                "BEGIN;
                 INSERT INTO spaces(id,slug,name,owner_member_id,created_at) VALUES ('{space_id}','capability','Capability','{owner_id}',now());
                 INSERT INTO members(id,space_id,kind,display_name,handle,access_level,created_at) VALUES ('{owner_id}','{space_id}','human','Owner','owner','owner',now());
                 INSERT INTO members(id,space_id,kind,display_name,handle,access_level,created_at) VALUES ('{agent_id}','{space_id}','agent','Agent','agent','member',now());
                 INSERT INTO computers(id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES ('{computer_id}','{space_id}','Computer','localhost','linux','{}','online',1,now());
                 INSERT INTO agents(member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES ('{agent_id}','{space_id}','{computer_id}','Test',1,'active','codex',now());
                 INSERT INTO channels(id,space_id,kind,slug,next_seq,created_at) VALUES ('{channel_id}','{space_id}','private','general',2,now());
                 INSERT INTO channel_members(channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel_id}','{space_id}','{owner_id}',now(),0);
                 INSERT INTO channel_members(channel_id,space_id,member_id,joined_at,last_read_seq) VALUES ('{channel_id}','{space_id}','{agent_id}',now(),0);
                 INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES ('{focus_id}','{space_id}','{channel_id}','{focus_id}',1,'root','text','{owner_id}','source',now());
                 INSERT INTO threads(id,space_id,channel_id,root_message_id,created_at) VALUES ('{focus_id}','{space_id}','{channel_id}','{focus_id}',now());
                 INSERT INTO agent_runs(id,space_id,agent_id,focus_thread_id,status,fencing_token_hash,lease_expires_at,created_at,started_at) VALUES ('{run_id}','{space_id}','{agent_id}','{focus_id}','running','{}',now()+interval '1 hour',now(),now());
                 INSERT INTO inbox_items(id,space_id,agent_id,thread_id,kind,strength,status,available_at,lease_run_id,lease_expires_at,created_at) VALUES
                   ('{handled_item_id}','{space_id}','{agent_id}','{focus_id}','mention','hard','leased',now(),'{run_id}',now()+interval '1 hour',now()),
                   ('{deferred_item_id}','{space_id}','{agent_id}','{focus_id}','mention','hard','leased',now(),'{run_id}',now()+interval '1 hour',now());
                 INSERT INTO run_items(run_id,inbox_item_id,delivery_seq,attached_at) VALUES
                   ('{run_id}','{handled_item_id}',1,now()),
                   ('{run_id}','{deferred_item_id}',2,now());
                 COMMIT;",
                token_hash(computer_token),
                token_hash(fencing_token),
            ))
            .execute(&pool)
            .await
            .unwrap();

            let objects = tempfile::tempdir().unwrap();
            let object_store = LocalFileSystem::new_with_prefix(objects.path()).unwrap();
            let state = RuntimeState {
                pool,
                storage,
                objects: Arc::new(AttachmentObjectStore::new(Arc::new(object_store))),
                session_ttl_hours: 1,
            };
            let mut headers = HeaderMap::new();
            headers.insert(
                "Authorization",
                HeaderValue::from_str(&format!("Bearer {computer_token}")).unwrap(),
            );
            Self {
                state,
                admin,
                database_name,
                _objects: objects,
                headers,
                computer_id,
                context: capability::RunContext {
                    agent_id: AgentId::from_uuid(agent_id),
                    space_id: crate::ids::SpaceId::from_uuid(space_id),
                    task_id: None,
                    focus_thread_id: ThreadId::from_uuid(focus_id),
                    run_id: RunId::from_uuid(run_id),
                    fencing_token: fencing_token.to_owned(),
                    message_snapshot_sequence: 1,
                },
                handled_item_id: InboxItemId::from_uuid(handled_item_id),
                deferred_item_id: InboxItemId::from_uuid(deferred_item_id),
            }
        }

        async fn execute(&self, action: capability::Action) -> Result<Value, capability::Error> {
            self.execute_with_key(
                action,
                crate::ids::IdempotencyKey::from_uuid(Uuid::now_v7()),
            )
            .await
        }

        async fn execute_with_key(
            &self,
            action: capability::Action,
            idempotency_key: crate::ids::IdempotencyKey,
        ) -> Result<Value, capability::Error> {
            execute_agent_action(
                &self.state,
                &self.headers,
                self.computer_id,
                AgentActionRequest {
                    context: self.context.clone(),
                    action,
                    idempotency_key: Some(idempotency_key),
                },
            )
            .await
        }

        async fn bind_task(&mut self) -> TaskId {
            let created = self
                .execute(capability::Action::TaskCreate {
                    title: Some("Capability Task".into()),
                    assignee: None,
                })
                .await
                .unwrap();
            let task_id =
                TaskId::from_uuid(Uuid::parse_str(created["id"].as_str().unwrap()).unwrap());
            self.context.task_id = Some(task_id);
            task_id
        }

        async fn destroy(mut self) {
            self.state.pool.close().await;
            sqlx::query(&format!(
                "DROP DATABASE \"{}\" WITH (FORCE)",
                self.database_name
            ))
            .execute(&mut self.admin)
            .await
            .unwrap();
        }
    }

    #[tokio::test]
    async fn capability_dispositions_are_atomic_idempotent_and_conflict_safe() {
        let fixture = CapabilityFixture::create().await;
        sqlx::raw_sql(
            "CREATE FUNCTION test_reject_run_item_disposition() RETURNS trigger LANGUAGE plpgsql AS $$
             BEGIN RAISE EXCEPTION 'forced capability rollback'; END $$;
             CREATE CONSTRAINT TRIGGER test_reject_run_item_disposition
             AFTER UPDATE OF disposition ON run_items DEFERRABLE INITIALLY DEFERRED
             FOR EACH ROW EXECUTE FUNCTION test_reject_run_item_disposition();",
        )
        .execute(&fixture.state.pool)
        .await
        .unwrap();
        let failed = fixture
            .execute(capability::Action::MessageSend(capability::MessageSend {
                target: capability::MessageTarget::Focus,
                body: "must roll back".to_owned(),
                handle_item_id: Some(fixture.handled_item_id),
                snapshot_sequence: None,
            }))
            .await
            .unwrap_err();
        assert_eq!(failed.code, capability::ErrorCode::Internal);
        let rolled_back: (i64, Option<String>) = (
            sqlx::query_scalar(
                "SELECT count(*) FROM messages WHERE body_markdown='must roll back'",
            )
            .fetch_one(&fixture.state.pool)
            .await
            .unwrap(),
            sqlx::query_scalar(
                "SELECT disposition FROM run_items WHERE run_id=$1 AND inbox_item_id=$2",
            )
            .bind(fixture.context.run_id.into_uuid())
            .bind(fixture.handled_item_id.into_uuid())
            .fetch_one(&fixture.state.pool)
            .await
            .unwrap(),
        );
        assert_eq!(rolled_back, (0, None));
        sqlx::raw_sql(
            "DROP TRIGGER test_reject_run_item_disposition ON run_items;
             DROP FUNCTION test_reject_run_item_disposition();",
        )
        .execute(&fixture.state.pool)
        .await
        .unwrap();

        let ack = capability::Action::InboxAck {
            item_id: fixture.handled_item_id,
            reason: Some("handled".to_owned()),
        };
        fixture.execute(ack.clone()).await.unwrap();
        fixture.execute(ack).await.unwrap();
        let defer_until = OffsetDateTime::now_utc() + Duration::hours(2);
        let defer = capability::Action::InboxDefer {
            item_id: fixture.deferred_item_id,
            until: defer_until,
        };
        fixture.execute(defer.clone()).await.unwrap();
        fixture.execute(defer).await.unwrap();

        let facts: (String, String, String, String) = sqlx::query_as(
            "SELECT handled.disposition,deferred.disposition,handled_item.status,deferred_item.status
             FROM run_items handled
             JOIN run_items deferred ON deferred.run_id=handled.run_id
             JOIN inbox_items handled_item ON handled_item.id=handled.inbox_item_id
             JOIN inbox_items deferred_item ON deferred_item.id=deferred.inbox_item_id
             WHERE handled.inbox_item_id=$1 AND deferred.inbox_item_id=$2",
        )
        .bind(fixture.handled_item_id.into_uuid())
        .bind(fixture.deferred_item_id.into_uuid())
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
        assert_eq!(
            facts,
            (
                "handled".into(),
                "deferred".into(),
                "leased".into(),
                "leased".into()
            )
        );

        let conflict = fixture
            .execute(capability::Action::InboxDefer {
                item_id: fixture.handled_item_id,
                until: defer_until,
            })
            .await
            .unwrap_err();
        assert_eq!(conflict.code, capability::ErrorCode::Conflict);

        let submitted = super::super::http::submit_run_result(
            &fixture.state.storage,
            super::super::http::ComputerPrincipal {
                computer_id: ComputerId::from_uuid(fixture.computer_id),
            },
            crate::protocol::computer::RunResult {
                event_id: crate::ids::EventId::from_uuid(Uuid::now_v7()),
                run_id: fixture.context.run_id,
                fencing_token: crate::protocol::computer::FencingToken::new(
                    fixture.context.fencing_token.clone(),
                ),
                status: crate::protocol::computer::RunTerminalStatus::Yielded,
                item_outcomes: vec![
                    crate::protocol::computer::ItemOutcome {
                        item_id: fixture.handled_item_id,
                        disposition: crate::protocol::computer::ItemDisposition::Handled,
                    },
                    crate::protocol::computer::ItemOutcome {
                        item_id: fixture.deferred_item_id,
                        disposition: crate::protocol::computer::ItemDisposition::Deferred,
                    },
                ],
                continuation_note: Some("continue later".to_owned()),
                error_code: None,
            },
        )
        .await;
        assert!(submitted.is_ok());
        let yielded: (String, Option<String>, String, String) = sqlx::query_as(
            "SELECT runs.status,runs.continuation_note,handled.status,deferred.status
             FROM agent_runs runs
             JOIN inbox_items handled ON handled.lease_run_id IS NULL AND handled.id=$2
             JOIN inbox_items deferred ON deferred.lease_run_id IS NULL AND deferred.id=$3
             WHERE runs.id=$1",
        )
        .bind(fixture.context.run_id.into_uuid())
        .bind(fixture.handled_item_id.into_uuid())
        .bind(fixture.deferred_item_id.into_uuid())
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
        assert_eq!(
            yielded,
            (
                "yielded".into(),
                Some("continue later".into()),
                "handled".into(),
                "deferred".into()
            )
        );
        fixture.destroy().await;
    }

    #[tokio::test]
    async fn capability_task_done_commits_collaboration_facts_and_replays() {
        let mut fixture = CapabilityFixture::create().await;
        let task_id = fixture.bind_task().await;
        let key = crate::ids::IdempotencyKey::from_uuid(Uuid::now_v7());
        let action = capability::Action::TaskDone {
            result: "Task Result".into(),
            post_to: capability::PostTarget::Focus,
        };

        sqlx::raw_sql(
            "CREATE FUNCTION test_reject_task_audit() RETURNS trigger LANGUAGE plpgsql AS $$
             BEGIN RAISE EXCEPTION 'forced Task transaction rollback'; END $$;
             CREATE CONSTRAINT TRIGGER test_reject_task_audit
             AFTER INSERT ON audit_events DEFERRABLE INITIALLY DEFERRED
             FOR EACH ROW EXECUTE FUNCTION test_reject_task_audit();",
        )
        .execute(&fixture.state.pool)
        .await
        .unwrap();
        let failed = fixture
            .execute_with_key(action.clone(), key)
            .await
            .unwrap_err();
        assert_eq!(failed.code, capability::ErrorCode::Internal);
        let rolled_back: (String, String, i64, i64) = sqlx::query_as(
            "SELECT tasks.status,runs.status, \
             (SELECT count(*) FROM messages WHERE body_markdown='Task Result'), \
             (SELECT count(*) FROM inbox_items WHERE status='leased' AND lease_run_id=$2) \
             FROM tasks JOIN agent_runs runs ON runs.task_id=tasks.id WHERE tasks.id=$1",
        )
        .bind(task_id.into_uuid())
        .bind(fixture.context.run_id.into_uuid())
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
        assert_eq!(rolled_back, ("in_progress".into(), "running".into(), 0, 2));
        sqlx::raw_sql(
            "DROP TRIGGER test_reject_task_audit ON audit_events;
             DROP FUNCTION test_reject_task_audit();",
        )
        .execute(&fixture.state.pool)
        .await
        .unwrap();

        fixture.execute_with_key(action.clone(), key).await.unwrap();
        fixture.execute_with_key(action, key).await.unwrap();

        let facts: (String, String, i64, i64, i64, i64, i64) = sqlx::query_as(
            "SELECT tasks.status,runs.status, \
             (SELECT count(*) FROM inbox_items WHERE lease_run_id IS NULL AND status='handled' AND id IN ($2,$3)), \
             (SELECT count(*) FROM messages WHERE body_markdown='Task Result'), \
             (SELECT count(*) FROM audit_events WHERE action='task.done' AND subject_id=$1), \
             (SELECT count(*) FROM idempotency_records WHERE action='task.done' AND resource_id=$1), \
             (SELECT count(*) FROM computer_commands WHERE kind='session.close') \
             FROM tasks JOIN agent_runs runs ON runs.task_id=tasks.id WHERE tasks.id=$1",
        )
        .bind(task_id.into_uuid())
        .bind(fixture.handled_item_id.into_uuid())
        .bind(fixture.deferred_item_id.into_uuid())
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
        assert_eq!(facts, ("done".into(), "completed".into(), 2, 1, 1, 1, 1));
        fixture.destroy().await;
    }

    #[tokio::test]
    async fn capability_task_submit_review_and_close_use_terminal_transaction() {
        let mut review = CapabilityFixture::create().await;
        let review_task = review.bind_task().await;
        review
            .execute(capability::Action::TaskSubmitReview {
                body: "Ready for review".into(),
                post_to: capability::PostTarget::Source,
            })
            .await
            .unwrap();
        let review_facts: (String, String, i64) = sqlx::query_as(
            "SELECT tasks.status,runs.status, \
             (SELECT count(*) FROM messages WHERE body_markdown='Ready for review') \
             FROM tasks JOIN agent_runs runs ON runs.task_id=tasks.id WHERE tasks.id=$1",
        )
        .bind(review_task.into_uuid())
        .fetch_one(&review.state.pool)
        .await
        .unwrap();
        assert_eq!(review_facts, ("in_review".into(), "completed".into(), 1));
        review.destroy().await;

        let mut close = CapabilityFixture::create().await;
        let close_task = close.bind_task().await;
        close
            .execute(capability::Action::TaskClose {
                reason: capability::CloseReason::Obsolete,
                note: Some("No longer needed".into()),
            })
            .await
            .unwrap();
        let close_facts: (String, String, String, Option<String>, i64) = sqlx::query_as(
            "SELECT tasks.status,runs.status,tasks.close_reason_code,tasks.close_reason_note, \
             (SELECT count(*) FROM audit_events WHERE action='task.close' AND subject_id=$1) \
             FROM tasks JOIN agent_runs runs ON runs.task_id=tasks.id WHERE tasks.id=$1",
        )
        .bind(close_task.into_uuid())
        .fetch_one(&close.state.pool)
        .await
        .unwrap();
        assert_eq!(
            close_facts,
            (
                "closed".into(),
                "completed".into(),
                "obsolete".into(),
                Some("No longer needed".into()),
                1
            )
        );
        close.destroy().await;
    }

    #[tokio::test]
    async fn agent_attachment_stream_uses_active_run_and_commits_metadata() {
        let fixture = CapabilityFixture::create().await;
        let mut headers = fixture.headers.clone();
        headers.insert(
            "x-sumi-fencing-token",
            HeaderValue::from_str(&fixture.context.fencing_token).unwrap(),
        );
        let key = Uuid::now_v7();
        headers.insert(
            "idempotency-key",
            HeaderValue::from_str(&key.to_string()).unwrap(),
        );
        let path = (
            fixture.computer_id,
            fixture.context.agent_id.into_uuid(),
            fixture.context.run_id.into_uuid(),
        );
        let created = agent_create_upload(
            State(fixture.state.clone()),
            headers.clone(),
            Path(path),
            Json(AgentCreateUploadBody {
                original_name: "result.txt".into(),
                media_type: "text/plain".into(),
            }),
        )
        .await
        .unwrap();
        let attachment_id = Uuid::parse_str(created.1.0["id"].as_str().unwrap()).unwrap();
        let replayed = agent_create_upload(
            State(fixture.state.clone()),
            headers.clone(),
            Path(path),
            Json(AgentCreateUploadBody {
                original_name: "result.txt".into(),
                media_type: "text/plain".into(),
            }),
        )
        .await
        .unwrap();
        assert_eq!(replayed.1.0["id"], attachment_id.to_string());

        let content = Bytes::from_static(b"agent attachment payload");
        let attachment_path = (path.0, path.1, path.2, attachment_id);
        agent_upload_content(
            State(fixture.state.clone()),
            headers.clone(),
            Path(attachment_path),
            content.clone(),
        )
        .await
        .unwrap();
        let digest = hex::encode(Sha256::digest(&content));
        let _ = agent_complete_upload(
            State(fixture.state.clone()),
            headers.clone(),
            Path(attachment_path),
            Json(CompleteUploadBody {
                size: content.len() as u64,
                sha256: digest.clone(),
            }),
        )
        .await
        .unwrap();
        let completed_again = agent_complete_upload(
            State(fixture.state.clone()),
            headers.clone(),
            Path(attachment_path),
            Json(CompleteUploadBody {
                size: content.len() as u64,
                sha256: digest,
            }),
        )
        .await
        .unwrap();
        assert_eq!(completed_again.0["status"], "ready");
        let downloaded =
            agent_download_attachment(State(fixture.state.clone()), headers, Path(attachment_path))
                .await
                .unwrap();
        assert_eq!(downloaded, content);

        let records: (i64, i64, i64) = sqlx::query_as(
            "SELECT \
             (SELECT count(*) FROM idempotency_records WHERE resource_id=$1), \
             (SELECT count(*) FROM audit_events WHERE subject_id=$1), \
             (SELECT count(*) FROM outbox_events WHERE payload_json->>'attachment_id'=$1::text)",
        )
        .bind(attachment_id)
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
        assert_eq!(records, (2, 2, 2));
        fixture.destroy().await;
    }

    #[tokio::test]
    async fn channel_read_is_authorized_and_stale_writes_are_rejected() {
        let mut fixture = CapabilityFixture::create().await;
        let channel_id: Uuid = sqlx::query_scalar("SELECT channel_id FROM threads WHERE id=$1")
            .bind(fixture.context.focus_thread_id.into_uuid())
            .fetch_one(&fixture.state.pool)
            .await
            .unwrap();
        let read = fixture
            .execute(capability::Action::ChannelRead {
                channel_id: crate::ids::ChannelId::from_uuid(channel_id),
                around_message_id: None,
                limit: 20,
            })
            .await
            .unwrap();
        assert_eq!(read["snapshot_channel_seq"], 1);
        assert_eq!(read["messages"].as_array().unwrap().len(), 1);

        let task_id = fixture.bind_task().await;
        let new_message_id = Uuid::now_v7();
        sqlx::query("UPDATE channels SET next_seq=next_seq+1 WHERE id=$1")
            .bind(channel_id)
            .execute(&fixture.state.pool)
            .await
            .unwrap();
        sqlx::query("INSERT INTO messages(id,space_id,channel_id,thread_id,channel_seq,placement,content_kind,author_member_id,body_markdown,created_at) VALUES($1,$2,$3,$4,2,'reply','text',$5,'new context',now())")
            .bind(new_message_id)
            .bind(fixture.context.space_id.into_uuid())
            .bind(channel_id)
            .bind(fixture.context.focus_thread_id.into_uuid())
            .bind(fixture.context.agent_id.into_uuid())
            .execute(&fixture.state.pool)
            .await
            .unwrap();

        let stale_message = fixture
            .execute(capability::Action::MessageSend(capability::MessageSend {
                target: capability::MessageTarget::Focus,
                body: "must not commit".into(),
                handle_item_id: None,
                snapshot_sequence: None,
            }))
            .await
            .unwrap_err();
        assert_eq!(stale_message.code, capability::ErrorCode::ContextChanged);
        let stale_done = fixture
            .execute(capability::Action::TaskDone {
                result: "must not finish".into(),
                post_to: capability::PostTarget::Focus,
            })
            .await
            .unwrap_err();
        assert_eq!(stale_done.code, capability::ErrorCode::ContextChanged);
        let facts: (String, String, i64) = sqlx::query_as(
            "SELECT tasks.status,runs.status, \
             (SELECT count(*) FROM messages WHERE body_markdown IN ('must not commit','must not finish')) \
             FROM tasks JOIN agent_runs runs ON runs.task_id=tasks.id WHERE tasks.id=$1",
        )
        .bind(task_id.into_uuid())
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
        assert_eq!(facts, ("in_progress".into(), "running".into(), 0));
        fixture.destroy().await;
    }
}
