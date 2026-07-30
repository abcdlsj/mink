use std::{
    collections::{BTreeSet, VecDeque},
    convert::Infallible,
    sync::Arc,
};

use anyhow::Context;
use axum::{
    Json, Router,
    body::Bytes,
    extract::{
        DefaultBodyLimit, Path, Query, State, WebSocketUpgrade,
        ws::{Message as WebSocketMessage, WebSocket},
    },
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{
        IntoResponse, Response, Sse,
        sse::{Event as SseEvent, KeepAlive},
    },
    routing::{get, post},
};
use axum_extra::extract::cookie::{Cookie, CookieJar, SameSite};
use futures_util::{StreamExt, stream};
use serde::Deserialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row, postgres::PgPoolOptions};
use time::{Duration, OffsetDateTime};
use tower_http::{
    services::{ServeDir, ServeFile},
    trace::TraceLayer,
};
use uuid::Uuid;

use crate::config::ServerConfig;
use crate::{
    ids::{
        AgentId, AttachmentId, ChannelId, ComputerId, InboxItemId, MemberId, MessageId, RunId,
        SpaceId, TaskId, ThreadId,
    },
    protocol::{
        capability,
        computer::{
            ComputerFrame, ComputerHello, MemoryQuery, MemoryReadQuery, Query as ComputerQuery,
            QueryErrorCode, QueryResult, ServerFrame, SessionContinuityQuery,
            SessionContinuityState, SessionScope,
        },
    },
    server::{
        application::attachment::{
            AttachmentContent, CompleteUpload, CompleteUploadInput, OpenUpload, OpenUploadInput,
            ReadAttachment, WriteUploadContent, WriteUploadContentInput,
        },
        application::attention::{ReadMemberInbox, RouteHardItem, RouteHardItemInput},
        application::computer::{
            AuthenticateComputer, BeginPairing, BeginPairingInput, ConfirmPairing,
            ConfirmPairingInput, ListSpaceComputers, ReadPairedComputer, ReadPairing,
            ReadPairingStatus,
        },
        application::conversation::{
            ArchiveChannel, CreateAgent, CreateAgentAction, CreateAgentActionInput,
            CreateAgentInput, CreateChannel, CreateChannelAction, CreateChannelActionInput,
            CreateChannelInput, DeleteMessage, EditMessage, EditMessageInput, JoinChannel,
            ListDirectMessages, OpenDirectMessage, OpenDirectMessageInput, PublishMessage,
            SetThreadSubscription,
        },
        application::task::{
            CompleteTask, CompleteTaskInput, CreateTaskFromRootMessage, CreateTaskInput,
            FinishAgentTaskAction, FinishAgentTaskInput, FinishAgentTaskRun, LinkThreadInput,
            LinkThreadToTask, TaskAction, TaskPostTarget, TaskSource, UnlinkThreadFromTask,
            UpdateTask, UpdateTaskInput,
        },
        application::{
            execution::{
                AcknowledgeDelivery, AcknowledgeDeliveryInput, ClaimRun, ClaimRunInput,
                CompleteRun, CompleteRunInput, ItemDispositionInput, RecordRunItemDisposition,
                RecordRunItemDispositionInput, RenewRun, RenewRunInput, StartRun, StartRunInput,
            },
            identity::{
                AgentLifecycleAction, AuthenticateHuman, AuthenticateHumanInput,
                AuthenticateSession, AuthorizeAgentAccess, AuthorizeAgentGovernance,
                AuthorizeAttachmentAccess, AuthorizeChannelAccess, AuthorizeComputerGovernance,
                AuthorizeSpaceAccess, AuthorizeSpaceGovernance, CloseSession, CreateSpace,
                CreateSpaceInput, DeleteComputer, RegisterHuman, RegisterHumanInput, RetireAgent,
                SetPermission, UpdateAgent, UpdateAgentInput, UpdateMemberAccess,
            },
            invitation::{
                AcceptInvitation, AcceptInvitationInput, InviteHuman, InviteHumanInput,
                ReadInvitation,
            },
            ports::{
                ApplicationError, AuthenticatedHuman, DirectMessageView, InboxItemView,
                InvitationView, MemberKind, MessageDraft, PairedComputer, RawFencingToken,
                RawInvitationToken, RawPairingCode, RawSessionToken, ServerTransaction,
                SpaceMemberView, TransactionPort,
            },
        },
        domain::{
            access::SessionLifetime,
            attachment::{Attachment, AttachmentStatus as AttachmentStatusKind, DeclaredContent},
            attention::{AttentionStrength, InboxItemKind, InboxItemStatus},
            conversation::ChannelKind,
            identity::{AccessLevel, DriverKind, PermissionAction},
            pairing::ComputerOs as ComputerOsKind,
            task::CloseReason,
        },
    },
};

use super::{
    credential::{Argon2Passwords, NumericPairingCodes, UuidInvitationTokens, UuidSessionTokens},
    object_storage::AttachmentObjectStore,
    openapi::{
        AccessLevel as AccessLevelCode, ActionAgentResponse, ActionChannelResponse,
        AgentAccessLevel, AgentActivityResponse, AgentActivityStatus, AgentLifecycle,
        AgentResponse, AgentRuntimeResponse, AttachmentResponse, AttachmentStatus, AttentionConfig,
        AttentionFailureResponse, ChannelKind as ChannelKindCode, ChannelListResponse,
        ChannelMembersResponse, ChannelResponse, CloseReason as CloseReasonCode, ComputerOs,
        ComputerResponse, ComputerStatus, CreatedInvitationResponse, DirectMessageResponse,
        DriverKind as DriverKindCode, InboxItemResponse, InboxKind, InboxPriority, InboxStatus,
        InvitationResponse, LoginResponse, MemberKind as MemberKindCode, MemberResponse,
        MemoryContentResponse, MemoryFileResponse, MessageAuthor, MessageContentResponse,
        MessagePageResponse, MessagePlacement, MessageResponse, ProvisionStatus, RegisterResponse,
        RunOutcome, RunResponse, RunStatus, SessionContinuityResponse,
        SessionContinuityState as ContinuityStateCode, SpaceResponse, TaskResponse, TaskStatus,
        ThreadReadResponse, ThreadReferenceResponse, ThreadRelation, ThreadSubscriptionResponse,
        UserResponse,
    },
    postgres::PostgresAdapter,
    query::QueryRegistry,
};

const SESSION_COOKIE: &str = "sumi_session";

#[derive(Clone)]
struct RuntimeState {
    pool: PgPool,
    storage: PostgresAdapter,
    objects: Arc<AttachmentObjectStore>,
    session_lifetime: SessionLifetime,
    attachment_max_bytes: u64,
    queries: QueryRegistry,
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

    fn permission_denied() -> Self {
        Self {
            status: StatusCode::FORBIDDEN,
            code: "permission_denied",
            message: "Member cannot access this resource",
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

    /// Computer 未连接,或已连接但未在超时内回应。两种情况可用的事实相同。
    fn computer_unreachable() -> Self {
        Self {
            status: StatusCode::SERVICE_UNAVAILABLE,
            code: "computer_unreachable",
            message: "Computer did not answer the query",
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
#[serde(deny_unknown_fields)]
struct CreateChannelBody {
    name: String,
    slug: String,
    kind: String,
    topic: Option<String>,
    #[serde(default)]
    agent_member_ids: Vec<Uuid>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AddChannelAgentsBody {
    agent_member_ids: Vec<Uuid>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CreateAgentBody {
    computer_id: Uuid,
    name: String,
    handle: Option<String>,
    role_text: String,
    driver_kind: String,
    access_level: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CreateMessageBody {
    body_markdown: String,
    #[serde(default)]
    mentions: Vec<Uuid>,
    #[serde(default)]
    attachment_ids: Vec<Uuid>,
    reply_to_message_id: Option<Uuid>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct UpdateMessageBody {
    body_markdown: String,
}

struct MessageWriteContext {
    idempotency_key: crate::ids::IdempotencyKey,
    thread_id: Option<Uuid>,
    handled_item: Option<(Uuid, Uuid)>,
    expected_snapshot: Option<u64>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CreateTaskBody {
    title: Option<String>,
    assignee_agent_member_id: Option<Uuid>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct UpdateTaskBody {
    title: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct StartTaskBody {
    assignee_agent_member_id: Uuid,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct LinkThreadBody {
    thread_id: Uuid,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CompleteTaskBody {
    result_markdown: String,
    result_thread_id: Uuid,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
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
struct InviteHumanBody {
    email: String,
}

#[derive(Deserialize)]
struct OpenDirectMessageBody {
    member_id: Uuid,
}

#[derive(Deserialize)]
struct UpdateAgentBody {
    role_text: Option<String>,
    lifecycle: Option<LifecycleActionBody>,
}

#[derive(Deserialize)]
struct LifecycleActionBody {
    action: String,
    mode: Option<String>,
}

#[derive(Deserialize)]
struct UpdateMemberBody {
    access_level: Option<String>,
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
        .context("failed to migrate PostgreSQL")?;

    tokio::fs::create_dir_all(&config.attachment_dir)
        .await
        .context("failed to create Attachment directory")?;
    let object_store =
        object_store::local::LocalFileSystem::new_with_prefix(&config.attachment_dir)
            .context("failed to open Attachment directory")?;
    let attachment_body_limit = 100 * 1024 * 1024;
    let state = RuntimeState {
        pool,
        storage,
        objects: Arc::new(AttachmentObjectStore::new(Arc::new(object_store))),
        session_lifetime: SessionLifetime::from_hours(config.session_ttl_hours)
            .context("Server session TTL must be a positive number of hours")?,
        attachment_max_bytes: config.attachment_max_bytes,
        queries: QueryRegistry::default(),
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
        .route("/computers/{computer_id}/agents", get(computer_agents))
        .route("/computers/{computer_id}/runs/claim", post(claim_run))
        .route(
            "/computers/{computer_id}/runs/{run_id}/started",
            post(run_started),
        )
        .route(
            "/computers/{computer_id}/runs/{run_id}/renew",
            post(renew_run),
        )
        .route(
            "/computers/{computer_id}/runs/{run_id}/delivery-receipts",
            post(delivery_receipt),
        )
        .route(
            "/computers/{computer_id}/runs/{run_id}/result",
            post(run_result),
        )
        .route("/computers/{computer_id}/agent-actions", post(agent_action))
        .route(
            "/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/uploads",
            post(agent_create_upload),
        )
        .route(
            "/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/{attachment_id}/content",
            axum::routing::put(agent_upload_content)
                .layer(DefaultBodyLimit::max(attachment_body_limit)),
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
        .route("/spaces/{space_id}/events", get(space_events))
        .route(
            "/spaces/{space_id}/channels",
            get(list_channels).post(create_channel),
        )
        .route("/spaces/{space_id}/members", get(list_members))
        .route(
            "/spaces/{space_id}/members/{member_id}",
            axum::routing::patch(update_space_member),
        )
        .route("/channels/{channel_id}/members/me", post(join_channel))
        .route("/channels/{channel_id}/archive", post(archive_channel))
        .route(
            "/threads/{thread_id}/subscription",
            axum::routing::put(follow_thread).delete(unfollow_thread),
        )
        .route("/spaces/{space_id}/invites", post(invite_human))
        .route("/invites/{invite_token}", get(invitation_details))
        .route("/invites/{invite_token}/accept", post(accept_invitation))
        .route("/spaces/{space_id}/computers", get(list_computers))
        .route(
            "/spaces/{space_id}/dms",
            get(list_direct_messages).post(open_direct_message),
        )
        .route(
            "/spaces/{space_id}/agents",
            get(list_agents).post(create_agent),
        )
        .route(
            "/agents/{agent_id}",
            get(get_agent).patch(update_agent).delete(retire_agent),
        )
        .route("/agents/{agent_id}/runs/current", get(current_agent_run))
        .route("/agents/{agent_id}/memory/read", post(read_agent_memory))
        .route("/spaces/{space_id}/tasks", get(list_tasks))
        .route("/tasks/{task_id}", get(get_task).patch(update_task))
        .route("/tasks/{task_id}/runs", get(task_runs))
        .route("/root-messages/{message_id}/task", post(create_task))
        .route("/tasks/{task_id}/threads", post(link_task_thread))
        .route(
            "/tasks/{task_id}/threads/{thread_id}",
            axum::routing::delete(unlink_task_thread),
        )
        .route("/tasks/{task_id}/start", post(start_task))
        .route("/tasks/{task_id}/submit-review", post(submit_task_review))
        .route(
            "/tasks/{task_id}/request-changes",
            post(request_task_changes),
        )
        .route("/tasks/{task_id}/done", post(complete_task))
        .route("/tasks/{task_id}/close", post(close_task))
        .route("/tasks/{task_id}/reset-session", post(reset_task_session))
        .route(
            "/channels/{channel_id}/messages",
            get(list_messages).post(create_root_message),
        )
        .route(
            "/channels/{channel_id}/members",
            get(list_channel_members).post(add_channel_agents),
        )
        .route("/threads/{thread_id}", get(read_thread))
        .route("/threads/{thread_id}/messages", post(create_thread_reply))
        .route(
            "/messages/{message_id}",
            axum::routing::patch(update_message).delete(delete_message),
        )
        .route("/members/{member_id}/inbox", get(member_inbox))
        .route(
            "/members/{member_id}/permissions/{action_code}",
            axum::routing::put(grant_permission).delete(revoke_permission),
        )
        .route("/attachments/uploads", post(create_upload))
        .route(
            "/attachments/{attachment_id}/content",
            axum::routing::put(upload_content)
                .layer(DefaultBodyLimit::max(attachment_body_limit)),
        )
        .route(
            "/attachments/{attachment_id}/complete",
            post(complete_upload),
        )
        .route(
            "/attachments/{attachment_id}/download",
            get(download_attachment),
        )
        .route("/computers/{computer_id}", axum::routing::delete(delete_computer))
        .with_state(state);
    let app = Router::new()
        .nest("/api/v1", api)
        .fallback_service(
            ServeDir::new(&config.web_dist)
                .append_index_html_on_directories(true)
                .not_found_service(ServeFile::new(config.web_dist.join("index.html"))),
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
) -> Result<(CookieJar, (StatusCode, Json<RegisterResponse>)), ApiError> {
    let mut storage = state.storage.clone();
    let session = RegisterHuman::execute(
        &mut storage,
        &Argon2Passwords,
        &UuidSessionTokens,
        RegisterHumanInput {
            user_id: Uuid::now_v7(),
            session_id: Uuid::now_v7(),
            display_name: &body.display_name,
            email: &body.email,
            password: &body.password,
            lifetime: state.session_lifetime,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok((
        session_cookie(jar, &session.token),
        (
            StatusCode::CREATED,
            Json(RegisterResponse {
                user: user_response(&session.human),
                next: "create_space".to_owned(),
            }),
        ),
    ))
}

async fn login(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Json(body): Json<LoginBody>,
) -> Result<(CookieJar, Json<LoginResponse>), ApiError> {
    let mut storage = state.storage.clone();
    let session = AuthenticateHuman::execute(
        &mut storage,
        &Argon2Passwords,
        &UuidSessionTokens,
        AuthenticateHumanInput {
            session_id: Uuid::now_v7(),
            email: &body.email,
            password: &body.password,
            lifetime: state.session_lifetime,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok((
        session_cookie(jar, &session.token),
        Json(LoginResponse {
            user: user_response(&session.human),
        }),
    ))
}

async fn logout(State(state): State<RuntimeState>, jar: CookieJar) -> (CookieJar, StatusCode) {
    if let Some(token) = session_token(&jar) {
        let mut storage = state.storage.clone();
        let _ = CloseSession::execute(&mut storage, &token).await;
    }
    (
        jar.remove(Cookie::from(SESSION_COOKIE)),
        StatusCode::NO_CONTENT,
    )
}

async fn current_user(
    State(state): State<RuntimeState>,
    jar: CookieJar,
) -> Result<Json<UserResponse>, ApiError> {
    let human = authenticate(&state, &jar).await?;
    Ok(Json(user_response(&human)))
}

async fn begin_pairing(
    State(state): State<RuntimeState>,
    Json(body): Json<BeginPairingBody>,
) -> Result<(StatusCode, Json<Value>), ApiError> {
    let mut storage = state.storage.clone();
    let started = BeginPairing::execute(
        &mut storage,
        &NumericPairingCodes,
        BeginPairingInput {
            pairing_id: Uuid::now_v7(),
            token_hash: &body.token_hash,
            hostname: &body.hostname,
            os: &body.os,
            daemon_version: &body.daemon_version,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok((
        StatusCode::CREATED,
        Json(json!({
            "pairing_id": started.pairing_id,
            "code": started.code,
            "expires_at": timestamp(started.expires_at)
        })),
    ))
}

async fn pairing_details(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(pairing_id): Path<Uuid>,
    Query(query): Query<PairingCodeQuery>,
) -> Result<Json<Value>, ApiError> {
    authenticate(&state, &jar).await?;
    let mut storage = state.storage.clone();
    let pairing = ReadPairing::execute(
        &mut storage,
        pairing_id,
        &RawPairingCode::new(query.code).sha256_hash(),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(Json(json!({
        "pairing_id": pairing.pairing_id,
        "hostname": pairing.hostname,
        "os": pairing.os.code(),
        "daemon_version": pairing.daemon_version,
        "token_fingerprint": pairing.token_fingerprint,
        "status": pairing.status.code(),
        "expires_at": timestamp(pairing.expires_at)
    })))
}

async fn confirm_pairing(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(pairing_id): Path<Uuid>,
    Json(body): Json<ConfirmPairingBody>,
) -> Result<(StatusCode, Json<ComputerResponse>), ApiError> {
    let actor_id = current_member(&state, &jar, body.space_id).await?;
    let key = idempotency_header(&headers)?;
    let mut storage = state.storage.clone();
    let computer = ConfirmPairing::execute(
        &mut storage,
        ConfirmPairingInput {
            actor_id: MemberId::from_uuid(actor_id),
            pairing_id,
            computer_id: ComputerId::from_uuid(Uuid::now_v7()),
            space_id: SpaceId::from_uuid(body.space_id),
            code_hash: &RawPairingCode::new(body.code).sha256_hash(),
            name: &body.name,
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(key),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok((StatusCode::CREATED, Json(computer_response(&computer))))
}

async fn pairing_status(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path(pairing_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    let raw = bearer_token(&headers)?;
    let mut storage = state.storage.clone();
    let progress = ReadPairingStatus::execute(
        &mut storage,
        pairing_id,
        &token_hash(raw),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(Json(json!({
        "status": progress.status.code(),
        "computer_id": progress.computer_id.map(ComputerId::into_uuid),
        "space_id": progress.space_id.map(SpaceId::into_uuid)
    })))
}

async fn invite_human(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(space_id): Path<Uuid>,
    Json(body): Json<InviteHumanBody>,
) -> Result<(StatusCode, Json<CreatedInvitationResponse>), ApiError> {
    let token = require_session_token(&jar)?;
    let key = idempotency_header(&headers)?;
    let mut storage = state.storage.clone();
    let access = AuthorizeSpaceGovernance::execute(
        &mut storage,
        &token,
        SpaceId::from_uuid(space_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    let issued = InviteHuman::execute(
        &mut storage,
        &UuidInvitationTokens,
        InviteHumanInput {
            invitation_id: Uuid::now_v7(),
            space_id: SpaceId::from_uuid(space_id),
            actor_id: access.member_id,
            email: &body.email,
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(key),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(|error| match error {
        // 创建路径上调用方唯一能触发的唯一性冲突是「同一 email 已有可用链接」。
        crate::server::application::ports::ApplicationError::Conflict => ApiError {
            status: StatusCode::CONFLICT,
            code: "invitation_already_pending",
            message: "this email already has a pending invitation to the Space",
        },
        other => application_error(other),
    })?;
    let view = invitation_response(&issued.view);
    Ok((
        if issued.token.is_some() {
            StatusCode::CREATED
        } else {
            StatusCode::OK
        },
        Json(CreatedInvitationResponse {
            id: view.id,
            space_id: view.space_id,
            space_name: view.space_name,
            space_slug: view.space_slug,
            email: view.email,
            expires_at: view.expires_at,
            accepted_at: view.accepted_at,
            accepted_by_member_id: view.accepted_by_member_id,
            // 明文 token 只在首次创建时存在，重放不再返回。
            token: issued.token.as_ref().map(|token| token.expose().to_owned()),
        }),
    ))
}

async fn invitation_details(
    State(state): State<RuntimeState>,
    Path(invite_token): Path<String>,
) -> Result<Json<InvitationResponse>, ApiError> {
    // 受邀 Human 点击链接时可能尚无账号，因此该端点不要求认证。
    let mut storage = state.storage.clone();
    let invitation = ReadInvitation::execute(
        &mut storage,
        &RawInvitationToken::new(invite_token),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(invitation_error)?;
    Ok(Json(invitation_response(&invitation)))
}

async fn accept_invitation(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(invite_token): Path<String>,
) -> Result<(StatusCode, Json<MemberResponse>), ApiError> {
    let human = authenticate(&state, &jar).await?;
    let member_id = Uuid::now_v7();
    let mut storage = state.storage.clone();
    let member = AcceptInvitation::execute(
        &mut storage,
        AcceptInvitationInput {
            token: &RawInvitationToken::new(invite_token),
            member_id: MemberId::from_uuid(member_id),
            user_id: human.user_id,
            user_email: &human.email_normalized,
            display_name: &human.display_name,
            handle: &unique_handle(&human.display_name, member_id),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(accept_invitation_error)?;
    Ok((
        StatusCode::CREATED,
        Json(MemberResponse {
            id: member.member_id.into_uuid(),
            kind: MemberKindCode::Human,
            display_name: member.display_name,
            handle: member.handle,
            access_level: AccessLevelCode::Member,
            // 接受 Invitation 只建立 Member 身份，不授予任何 Permission。
            permissions: Vec::new(),
        }),
    ))
}

fn invitation_response(invitation: &InvitationView) -> InvitationResponse {
    InvitationResponse {
        id: invitation.id,
        space_id: invitation.space_id.into_uuid(),
        space_name: invitation.space_name.clone(),
        space_slug: invitation.space_slug.clone(),
        email: invitation.email.clone(),
        expires_at: timestamp(invitation.expires_at),
        accepted_at: invitation.accepted_at.map(timestamp),
        accepted_by_member_id: invitation.accepted_by_member_id.map(MemberId::into_uuid),
    }
}

/// 未命中、已过期和已接受返回同一个错误码，使读取端点不能用于探测 token
/// 是否存在。该端点不要求认证，因此不能区分原因。
fn invitation_error(error: crate::server::application::ports::ApplicationError) -> ApiError {
    use crate::server::application::ports::ApplicationError;
    use crate::server::domain::DomainError;
    match error {
        ApplicationError::NotFound | ApplicationError::Domain(DomainError::InvitationLapsed) => {
            ApiError {
                status: StatusCode::NOT_FOUND,
                code: "invitation_unavailable",
                message: "invitation link is not usable",
            }
        }
        other => application_error(other),
    }
}

/// 接受端点要求 Session，因此可以告知收件人不匹配：调用方已经证明了自己的身份，
/// 该信息不构成新的探测面，且是 Human 纠正登录账号所必需的。
fn accept_invitation_error(error: crate::server::application::ports::ApplicationError) -> ApiError {
    use crate::server::application::ports::ApplicationError;
    use crate::server::domain::DomainError;
    match error {
        ApplicationError::Domain(DomainError::InvitationEmailMismatch) => ApiError {
            status: StatusCode::FORBIDDEN,
            code: "invitation_email_mismatch",
            message: "invitation was issued to another email",
        },
        ApplicationError::Conflict => ApiError {
            status: StatusCode::CONFLICT,
            code: "already_member",
            message: "signed in Human is already a Member of this Space",
        },
        other => invitation_error(other),
    }
}

async fn connect_computer(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
    upgrade: WebSocketUpgrade,
) -> Result<Response, ApiError> {
    let raw = bearer_token(&headers)?;
    let mut storage = state.storage.clone();
    // 已删除 Computer 仍可完成握手，由 negotiate 返回稳定拒绝原因。
    let identity = AuthenticateComputer::execute(
        &mut storage,
        ComputerId::from_uuid(computer_id),
        &token_hash(raw),
    )
    .await
    .map_err(application_error)?;
    let storage = state.storage.clone();
    let pool = state.pool.clone();
    let queries = state.queries.clone();
    Ok(upgrade.on_upgrade(move |socket| {
        computer_socket(
            socket,
            storage,
            pool,
            queries,
            computer_id,
            identity.deleted,
        )
    }))
}

async fn authenticate_computer(
    state: &RuntimeState,
    headers: &HeaderMap,
    computer_id: Uuid,
) -> Result<(), ApiError> {
    let raw = bearer_token(headers)?;
    let mut storage = state.storage.clone();
    AuthenticateComputer::require_active(
        &mut storage,
        ComputerId::from_uuid(computer_id),
        &token_hash(raw),
    )
    .await
    .map_err(application_error)?;
    Ok(())
}

fn computer_response(computer: &PairedComputer) -> ComputerResponse {
    ComputerResponse {
        id: computer.id.into_uuid(),
        space_id: computer.space_id.into_uuid(),
        name: computer.name.clone(),
        hostname: computer.hostname.clone(),
        os: match computer.os {
            ComputerOsKind::MacOs => ComputerOs::Macos,
            ComputerOsKind::Linux => ComputerOs::Linux,
        },
        daemon_version: computer.daemon_version.clone().unwrap_or_default(),
        status: computer_status(computer),
        last_seen_at: optional_timestamp(computer.last_seen_at),
        created_at: timestamp(computer.created_at),
    }
}

fn computer_status(computer: &PairedComputer) -> ComputerStatus {
    if computer.deleted {
        ComputerStatus::Revoked
    } else if computer.connected {
        ComputerStatus::Online
    } else {
        ComputerStatus::Offline
    }
}

async fn computer_agents(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    let rows = sqlx::query(
        "SELECT a.member_id,a.space_id,a.role_text,a.role_revision,a.lifecycle,a.driver_kind,\
                a.driver_config_json,m.display_name,m.handle,m.access_level \
         FROM agents a JOIN members m ON m.id=a.member_id \
         WHERE a.computer_id=$1 AND a.lifecycle<>'retired' ORDER BY a.created_at,a.member_id",
    )
    .bind(computer_id)
    .fetch_all(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok(Json(Value::Array(
        rows.iter()
            .map(|row| {
                json!({
                    "member_id": row.get::<Uuid,_>("member_id"),
                    "space_id": row.get::<Uuid,_>("space_id"),
                    "display_name": row.get::<String,_>("display_name"),
                    "handle": row.get::<String,_>("handle"),
                    "access_level": row.get::<String,_>("access_level"),
                    "role_text": row.get::<String,_>("role_text"),
                    "role_revision": row.get::<i64,_>("role_revision"),
                    "lifecycle": row.get::<String,_>("lifecycle"),
                    "driver_kind": row.get::<String,_>("driver_kind"),
                    "driver_config": row.get::<Value,_>("driver_config_json"),
                })
            })
            .collect(),
    )))
}

async fn run_started(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, run_id)): Path<(Uuid, Uuid)>,
    Json(started): Json<crate::protocol::computer::RunStarted>,
) -> Result<StatusCode, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    if started.run_id.into_uuid() != run_id {
        return Err(ApiError::invalid("Run ID does not match the request path"));
    }
    let mut storage = state.storage.clone();
    StartRun::execute(
        &mut storage,
        StartRunInput {
            run_id: started.run_id,
            computer_id: ComputerId::from_uuid(computer_id),
            fencing_token_hash: token_hash(started.fencing_token.expose()),
            now: started.observed_at,
        },
    )
    .await
    .map_err(application_error)?;
    Ok(StatusCode::OK)
}

async fn delivery_receipt(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, run_id)): Path<(Uuid, Uuid)>,
    Json(receipt): Json<crate::protocol::computer::DeliveryReceipt>,
) -> Result<StatusCode, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    if receipt.run_id.into_uuid() != run_id {
        return Err(ApiError::invalid("Run ID does not match the request path"));
    }
    let mut storage = state.storage.clone();
    AcknowledgeDelivery::execute(
        &mut storage,
        AcknowledgeDeliveryInput {
            run_id: receipt.run_id,
            computer_id: ComputerId::from_uuid(computer_id),
            fencing_token_hash: token_hash(receipt.fencing_token.expose()),
            delivery_sequence: receipt.delivery_sequence.0,
            accepted: matches!(
                receipt.outcome,
                crate::protocol::computer::DeliveryOutcome::Accepted
            ),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(StatusCode::OK)
}

async fn apply_run_result(
    state: &RuntimeState,
    computer_id: Uuid,
    result: crate::protocol::computer::RunResult,
) -> Result<(), ApiError> {
    use crate::protocol::computer::{ItemDisposition, RunTerminalStatus};
    let error_code = result.error_code.map(super::http::run_error_code);
    let outcome = match result.status {
        RunTerminalStatus::Completed => crate::server::domain::execution::RunOutcome::Completed,
        RunTerminalStatus::Yielded => crate::server::domain::execution::RunOutcome::Yielded,
        RunTerminalStatus::Failed => crate::server::domain::execution::RunOutcome::Failed,
        RunTerminalStatus::Canceled => crate::server::domain::execution::RunOutcome::Canceled,
    };
    let item_dispositions = result
        .item_outcomes
        .into_iter()
        .map(|item| ItemDispositionInput {
            item_id: item.item_id,
            disposition: match item.disposition {
                ItemDisposition::Handled => {
                    crate::server::domain::attention::InboxItemDisposition::Handled
                }
                ItemDisposition::Deferred => {
                    crate::server::domain::attention::InboxItemDisposition::Deferred
                }
                ItemDisposition::Released => {
                    crate::server::domain::attention::InboxItemDisposition::Released
                }
            },
        })
        .collect();
    let mut storage = state.storage.clone();
    CompleteRun::execute(
        &mut storage,
        CompleteRunInput {
            event_id: result.event_id,
            run_id: result.run_id,
            computer_id: ComputerId::from_uuid(computer_id),
            fencing_token_hash: token_hash(result.fencing_token.expose()),
            outcome,
            error_code,
            item_dispositions,
            continuation_note: result.continuation_note,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(())
}

async fn run_result(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, run_id)): Path<(Uuid, Uuid)>,
    Json(result): Json<crate::protocol::computer::RunResult>,
) -> Result<StatusCode, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    if result.run_id.into_uuid() != run_id {
        return Err(ApiError::invalid("Run ID does not match the request path"));
    }
    apply_run_result(&state, computer_id, result).await?;
    Ok(StatusCode::OK)
}

async fn claim_run(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
) -> Result<Json<Value>, ApiError> {
    authenticate_computer(&state, &headers, computer_id).await?;
    let candidate = sqlx::query(
        "SELECT i.id,i.agent_id,i.task_id,i.thread_id,i.message_id,t.channel_id FROM inbox_items i \
         JOIN agents a ON a.member_id=i.agent_id \
         JOIN threads t ON t.id=i.thread_id \
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
    let claim_result = ClaimRun::execute(
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
    .await;
    if let Err(error) = claim_result {
        let error_code = run_claim_error_code(&error);
        let item_id: Uuid = candidate.get("id");
        let changed = record_run_claim_failure(
            &state.pool,
            item_id,
            candidate.get("message_id"),
            candidate.get("channel_id"),
            error_code,
        )
        .await?;
        if changed {
            tracing::warn!(
                %computer_id,
                %item_id,
                agent_id = %candidate.get::<Uuid, _>("agent_id"),
                error_code,
                "Computer Run claim failed; the Inbox Item remains pending"
            );
        }
        return Err(application_error(error));
    }
    Ok(Json(json!({"claimed":true,"run_id":run_id})))
}

fn run_claim_error_code(
    error: &crate::server::application::ports::ApplicationError,
) -> &'static str {
    use crate::server::application::ports::ApplicationError;
    match error {
        ApplicationError::NotFound => "run_claim_not_found",
        ApplicationError::Unauthenticated => "run_claim_unauthenticated",
        ApplicationError::PayloadTooLarge => "run_claim_payload_too_large",
        ApplicationError::PermissionDenied => "run_claim_permission_denied",
        ApplicationError::Conflict | ApplicationError::Domain(_) => "run_claim_conflict",
        ApplicationError::ContextChanged => "run_claim_context_changed",
        ApplicationError::Unavailable => "run_claim_unavailable",
        ApplicationError::Internal => "run_claim_internal",
    }
}

async fn record_run_claim_failure(
    pool: &PgPool,
    item_id: Uuid,
    message_id: Option<Uuid>,
    channel_id: Uuid,
    error_code: &str,
) -> Result<bool, ApiError> {
    let mut transaction = pool.begin().await.map_err(map_sqlx)?;
    let changed = sqlx::query(
        "UPDATE inbox_items SET last_error_code=$2 WHERE id=$1 AND status='pending' \
         AND last_error_code IS DISTINCT FROM $2",
    )
    .bind(item_id)
    .bind(error_code)
    .execute(&mut *transaction)
    .await
    .map_err(map_sqlx)?
    .rows_affected()
        == 1;
    if changed && let Some(message_id) = message_id {
        let space_id: Uuid = sqlx::query_scalar("SELECT space_id FROM messages WHERE id=$1")
            .bind(message_id)
            .fetch_one(&mut *transaction)
            .await
            .map_err(map_sqlx)?;
        sqlx::query(
            "INSERT INTO outbox_events(id,space_id,kind,payload_json,created_at) \
             VALUES($1,$2,'message.updated',$3,now())",
        )
        .bind(Uuid::now_v7())
        .bind(space_id)
        .bind(json!({"resource_id":message_id,"channel_id":channel_id}))
        .execute(&mut *transaction)
        .await
        .map_err(map_sqlx)?;
    }
    transaction.commit().await.map_err(map_sqlx)?;
    Ok(changed)
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
    authenticate_computer(&state, &headers, computer_id).await?;
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
    queries: QueryRegistry,
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
    let (connection, mut outbound) = queries.connect(computer_id);
    loop {
        let frame = tokio::select! {
            outgoing = outbound.recv() => {
                let Some(outgoing) = outgoing else { break };
                if send_json(&mut socket, &outgoing).await.is_err() {
                    break;
                }
                continue;
            }
            frame = socket.next() => match frame {
                Some(frame) => frame,
                None => break,
            },
        };
        let Ok(WebSocketMessage::Text(encoded)) = frame else {
            continue;
        };
        let Ok(frame) = serde_json::from_str::<ComputerFrame>(&encoded) else {
            continue;
        };
        match frame {
            ComputerFrame::QueryResult { result } => queries.resolve(result),
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
                let mut application = storage.clone();
                let applied = AcknowledgeDelivery::execute(
                    &mut application,
                    AcknowledgeDeliveryInput {
                        run_id: receipt.run_id,
                        computer_id: ComputerId::from_uuid(computer_id),
                        fencing_token_hash: token_hash(receipt.fencing_token.expose()),
                        delivery_sequence: receipt.delivery_sequence.0,
                        accepted: matches!(
                            receipt.outcome,
                            crate::protocol::computer::DeliveryOutcome::Accepted
                        ),
                        now: OffsetDateTime::now_utc(),
                    },
                )
                .await;
                if applied.is_ok() {
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
    queries.disconnect(connection);
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
            return capability_value(
                &task_projection(&state.pool, task_id)
                    .await
                    .map_err(api_to_capability)?,
            );
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
            let scope = match context.task_id {
                Some(task_id) => SessionScope::Task(task_id),
                None => SessionScope::Thread(context.focus_thread_id),
            };
            let continuity = agent_continuity(state, context.agent_id.into_uuid(), scope).await;
            Ok(
                json!({"agent":{"id":context.agent_id,"space_id":context.space_id},"task":task,"focus_thread_id":context.focus_thread_id,"run":{"id":context.run_id,"message_snapshot_sequence":context.message_snapshot_sequence},"claimed_items":items.iter().map(|row|json!({"id":row.get::<Uuid,_>("id"),"kind":row.get::<String,_>("kind"),"strength":row.get::<String,_>("strength"),"status":row.get::<String,_>("status"),"available_at":timestamp(row.get("available_at"))})).collect::<Vec<_>>(),"session_continuity":continuity}),
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
                MessageWriteContext {
                    idempotency_key: request.idempotency_key.ok_or_else(|| {
                        capability_error(
                            capability::ErrorCode::InvalidArgument,
                            "Idempotency key is required",
                            false,
                        )
                    })?,
                    thread_id,
                    handled_item: send
                        .handle_item_id
                        .map(|item_id| (context.run_id.into_uuid(), item_id.into_uuid())),
                    expected_snapshot,
                },
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
            capability_value(
                &task_projection(&state.pool, task.id.into_uuid())
                    .await
                    .map_err(api_to_capability)?,
            )
        }
        capability::Action::TaskLinkThread { thread_id } => {
            let key = request.idempotency_key.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::InvalidArgument,
                    "Idempotency key is required",
                    false,
                )
            })?;
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
                    idempotency_key: key,
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            capability_value(
                &task_projection(&state.pool, task_id.into_uuid())
                    .await
                    .map_err(api_to_capability)?,
            )
        }
        capability::Action::TaskUnlinkThread { thread_id } => {
            let key = request.idempotency_key.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::InvalidArgument,
                    "Idempotency key is required",
                    false,
                )
            })?;
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
                    idempotency_key: key,
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            capability_value(
                &task_projection(&state.pool, task_id.into_uuid())
                    .await
                    .map_err(api_to_capability)?,
            )
        }
        capability::Action::TaskUpdate { title } => {
            let key = request.idempotency_key.ok_or_else(|| {
                capability_error(
                    capability::ErrorCode::InvalidArgument,
                    "Idempotency key is required",
                    false,
                )
            })?;
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
                    idempotency_key: key,
                    action: TaskAction::Rename { title },
                    now: OffsetDateTime::now_utc(),
                },
            )
            .await
            .map_err(app_to_capability)?;
            capability_value(
                &task_projection(&state.pool, task_id.into_uuid())
                    .await
                    .map_err(api_to_capability)?,
            )
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
                    idempotency_key: request.idempotency_key.ok_or_else(|| {
                        capability_error(
                            capability::ErrorCode::InvalidArgument,
                            "Idempotency key is required",
                            false,
                        )
                    })?,
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
                    idempotency_key: request.idempotency_key.ok_or_else(|| {
                        capability_error(
                            capability::ErrorCode::InvalidArgument,
                            "Idempotency key is required",
                            false,
                        )
                    })?,
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
    capability_value(
        &task_projection(&state.pool, task_id.into_uuid())
            .await
            .map_err(api_to_capability)?,
    )
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

async fn create_space(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Json(body): Json<CreateSpaceBody>,
) -> Result<(StatusCode, Json<SpaceResponse>), ApiError> {
    let user = authenticate(&state, &jar).await?;
    let key = idempotency_header(&headers)?;
    let name = body.name.trim();
    let slug = body.slug.trim().to_lowercase();
    if name.is_empty() || slug.is_empty() {
        return Err(ApiError::invalid("Space name and slug are required"));
    }
    let space_id = Uuid::now_v7();
    let owner_id = Uuid::now_v7();
    let general_id = Uuid::now_v7();
    let now = OffsetDateTime::now_utc();
    let owner_handle = unique_handle(&user.display_name, owner_id);
    let mut storage = state.storage.clone();
    let created = CreateSpace::execute(
        &mut storage,
        CreateSpaceInput {
            actor_user_id: user.user_id,
            space_id: SpaceId::from_uuid(space_id),
            owner_id: MemberId::from_uuid(owner_id),
            general_channel_id: ChannelId::from_uuid(general_id),
            name,
            slug: &slug,
            owner_handle: &owner_handle,
            owner_display_name: &user.display_name,
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(key),
            now,
        },
    )
    .await
    .map_err(application_error)?;
    Ok((
        if created.space_id.into_uuid() == space_id {
            StatusCode::CREATED
        } else {
            StatusCode::OK
        },
        Json(space_response(
            created.space_id.into_uuid(),
            name,
            &slug,
            created.owner_id.into_uuid(),
            created.owner_id.into_uuid(),
            created.general_channel_id.into_uuid(),
        )),
    ))
}

async fn list_spaces(
    State(state): State<RuntimeState>,
    jar: CookieJar,
) -> Result<Json<Vec<SpaceResponse>>, ApiError> {
    let user = authenticate(&state, &jar).await?;
    let rows = sqlx::query(
        "SELECT s.id,s.name,s.slug,s.owner_member_id,hm.member_id AS current_member_id, \
         (SELECT id FROM channels WHERE space_id=s.id AND slug='general' LIMIT 1) AS general_channel_id \
         FROM spaces s JOIN human_members hm ON hm.space_id=s.id \
         WHERE hm.user_id=$1 AND s.deleted_at IS NULL ORDER BY s.created_at",
    )
    .bind(user.user_id)
    .fetch_all(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok(Json(rows.iter().map(space_row).collect()))
}

async fn space_by_slug(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(slug): Path<String>,
) -> Result<Json<SpaceResponse>, ApiError> {
    let user = authenticate(&state, &jar).await?;
    let row = sqlx::query(
        "SELECT s.id,s.name,s.slug,s.owner_member_id,hm.member_id AS current_member_id, \
         (SELECT id FROM channels WHERE space_id=s.id AND slug='general' LIMIT 1) AS general_channel_id \
         FROM spaces s JOIN human_members hm ON hm.space_id=s.id \
         WHERE hm.user_id=$1 AND lower(s.slug)=lower($2) AND s.deleted_at IS NULL",
    )
    .bind(user.user_id)
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
) -> Result<Json<ChannelListResponse>, ApiError> {
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
    let mut channels = Vec::with_capacity(rows.len());
    for row in &rows {
        channels.push(channel_row(row, member_id)?);
    }
    Ok(Json(ChannelListResponse {
        channels,
        can_create: true,
    }))
}

async fn create_channel(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(space_id): Path<Uuid>,
    Json(body): Json<CreateChannelBody>,
) -> Result<(StatusCode, Json<ChannelResponse>), ApiError> {
    let member_id = current_member(&state, &jar, space_id).await?;
    let kind = match body.kind.as_str() {
        "public" => ChannelKind::Public,
        "private" => ChannelKind::Private,
        _ => return Err(ApiError::invalid("Channel kind and slug are invalid")),
    };
    if body.slug.trim().is_empty() {
        return Err(ApiError::invalid("Channel kind and slug are invalid"));
    }
    let channel_id = ChannelId::from_uuid(Uuid::now_v7());
    let now = OffsetDateTime::now_utc();
    let mut audience = BTreeSet::from([MemberId::from_uuid(member_id)]);
    audience.extend(body.agent_member_ids.into_iter().map(MemberId::from_uuid));
    let mut storage = state.storage.clone();
    let channel = CreateChannel::execute(
        &mut storage,
        CreateChannelInput {
            channel_id,
            space_id: SpaceId::from_uuid(space_id),
            audience,
            kind,
            slug: Some(body.slug.trim().to_owned()),
            topic: body.topic.clone(),
            actor_member_id: MemberId::from_uuid(member_id),
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            now,
        },
    )
    .await
    .map_err(application_error)?;
    Ok((
        StatusCode::CREATED,
        Json(ChannelResponse {
            id: channel.id.into_uuid(),
            space_id,
            name: body.name,
            slug: body.slug,
            topic: body.topic,
            kind: match kind {
                ChannelKind::Public => ChannelKindCode::Public,
                // 创建端点只接受 public 和 private，direct 走 DM 端点。
                _ => ChannelKindCode::Private,
            },
            created_by_member_id: member_id,
            joined: true,
            archived_at: None,
        }),
    ))
}

async fn list_members(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<MemberResponse>>, ApiError> {
    current_member(&state, &jar, space_id).await?;
    let rows = sqlx::query("SELECT id,kind,display_name,handle,access_level FROM members WHERE space_id=$1 AND retired_at IS NULL ORDER BY created_at")
        .bind(space_id).fetch_all(&state.pool).await.map_err(map_sqlx)?;
    let mut values = Vec::with_capacity(rows.len());
    for row in rows {
        values.push(member_row(&state.pool, &row).await?);
    }
    Ok(Json(values))
}

async fn list_computers(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<ComputerResponse>>, ApiError> {
    current_member(&state, &jar, space_id).await?;
    let mut storage = state.storage.clone();
    let computers = ListSpaceComputers::execute(&mut storage, SpaceId::from_uuid(space_id))
        .await
        .map_err(application_error)?;
    Ok(Json(computers.iter().map(computer_response).collect()))
}

async fn list_agents(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<AgentResponse>>, ApiError> {
    current_member(&state, &jar, space_id).await?;
    let rows = sqlx::query(
        &format!(
            "SELECT a.*,m.display_name,m.handle,m.access_level,c.connection_status,\
             c.deleted_at AS computer_deleted_at,{ACTIVITY_COLUMNS} \
             FROM agents a JOIN members m ON m.id=a.member_id LEFT JOIN computers c ON c.id=a.computer_id \
             {ACTIVITY_JOINS} WHERE a.space_id=$1 ORDER BY a.created_at"
        ),
    )
    .bind(space_id)
    .fetch_all(&state.pool)
    .await
    .map_err(map_sqlx)?;
    let mut agents = Vec::with_capacity(rows.len());
    for row in &rows {
        agents.push(agent_row(row)?);
    }
    Ok(Json(agents))
}

async fn get_agent(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(agent_id): Path<Uuid>,
) -> Result<Json<AgentResponse>, ApiError> {
    let row = sqlx::query(
        &format!(
            "SELECT a.*,m.display_name,m.handle,m.access_level,c.connection_status,\
             c.deleted_at AS computer_deleted_at,{ACTIVITY_COLUMNS} \
             FROM agents a JOIN members m ON m.id=a.member_id LEFT JOIN computers c ON c.id=a.computer_id \
             {ACTIVITY_JOINS} WHERE a.member_id=$1"
        ),
    )
    .bind(agent_id)
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::not_found)?;
    current_member(&state, &jar, row.get("space_id")).await?;
    let mut agent = agent_row(&row)?;
    agent.memory_files = memory_files(&state, row.get("computer_id"), agent_id).await;
    Ok(Json(agent))
}

async fn current_agent_run(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(agent_id): Path<Uuid>,
) -> Result<Json<AgentRuntimeResponse>, ApiError> {
    let viewer_id = agent_space_member(&state, &jar, agent_id).await?;
    let run_id = sqlx::query_scalar::<_, Uuid>(
        "SELECT r.id FROM agent_runs r \
         JOIN threads t ON t.id=r.focus_thread_id \
         JOIN channel_members cm ON cm.channel_id=t.channel_id AND cm.member_id=$2 \
         WHERE r.agent_id=$1 AND r.status NOT IN ('completed','yielded','failed','canceled') \
         ORDER BY r.created_at DESC LIMIT 1",
    )
    .bind(agent_id)
    .bind(viewer_id)
    .fetch_optional(&state.pool)
    .await
    .map_err(map_sqlx)?;
    let Some(run_id) = run_id else {
        return Ok(Json(AgentRuntimeResponse {
            current_run: None,
            current_task: None,
            focus: None,
            another_item_waiting: false,
            session_continuity: unavailable_continuity(),
        }));
    };
    let run = run_projection(&state.pool, run_id).await?;
    let current_task = match run.task_id {
        Some(task_id) => Some(task_projection(&state.pool, task_id).await?),
        None => None,
    };
    // Run 绑定 Task 时 Session 属于该 Task，否则属于 Focus Thread。
    let scope = match run.task_id {
        Some(task_id) => SessionScope::Task(TaskId::from_uuid(task_id)),
        None => SessionScope::Thread(ThreadId::from_uuid(run.focus.id)),
    };
    let session_continuity = agent_continuity(&state, agent_id, scope).await;
    let another_item_waiting: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM inbox_items WHERE agent_id=$1 AND status='pending')",
    )
    .bind(agent_id)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok(Json(AgentRuntimeResponse {
        focus: Some(run.focus.clone()),
        current_run: Some(run),
        current_task,
        another_item_waiting,
        session_continuity,
    }))
}

async fn retire_agent(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(agent_id): Path<Uuid>,
) -> Result<Json<AgentResponse>, ApiError> {
    let key = crate::ids::IdempotencyKey::from_uuid(idempotency_header(&headers)?);
    let actor_id = require_agent_governor(&state, &jar, agent_id).await?;
    let mut storage = state.storage.clone();
    RetireAgent::execute(
        &mut storage,
        MemberId::from_uuid(actor_id),
        MemberId::from_uuid(agent_id),
        key,
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    let row = sqlx::query(&format!(
        "SELECT a.*,m.display_name,m.handle,m.access_level,c.connection_status,\
             c.deleted_at AS computer_deleted_at,{ACTIVITY_COLUMNS} \
             FROM agents a JOIN members m ON m.id=a.member_id \
             LEFT JOIN computers c ON c.id=a.computer_id {ACTIVITY_JOINS} WHERE a.member_id=$1"
    ))
    .bind(agent_id)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok(Json(agent_row(&row)?))
}

async fn delete_computer(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(computer_id): Path<Uuid>,
) -> Result<Json<ComputerResponse>, ApiError> {
    let key = crate::ids::IdempotencyKey::from_uuid(idempotency_header(&headers)?);
    let actor_id = require_computer_governor(&state, &jar, computer_id).await?;
    let mut storage = state.storage.clone();
    DeleteComputer::execute(
        &mut storage,
        MemberId::from_uuid(actor_id),
        ComputerId::from_uuid(computer_id),
        key,
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    let computer = ReadPairedComputer::execute(&mut storage, ComputerId::from_uuid(computer_id))
        .await
        .map_err(application_error)?;
    Ok(Json(computer_response(&computer)))
}

async fn create_agent(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(space_id): Path<Uuid>,
    Json(body): Json<CreateAgentBody>,
) -> Result<(StatusCode, Json<AgentResponse>), ApiError> {
    let actor_id = current_member(&state, &jar, space_id).await?;
    let requested_access = match body.access_level.as_str() {
        "member" => AccessLevel::Member,
        "admin" => AccessLevel::Admin,
        _ => return Err(ApiError::invalid("Agent configuration is invalid")),
    };
    let name = body.name.trim();
    let role = body.role_text.trim();
    if name.is_empty()
        || name.chars().count() > 40
        || role.is_empty()
        || role.chars().count() > 12_000
        || !matches!(body.driver_kind.as_str(), "codex" | "builtin")
        || !matches!(body.access_level.as_str(), "member" | "admin")
    {
        return Err(ApiError::invalid("Agent configuration is invalid"));
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
    let mut storage = state.storage.clone();
    CreateAgent::execute(
        &mut storage,
        CreateAgentInput {
            agent_member_id: MemberId::from_uuid(agent_id),
            space_id: SpaceId::from_uuid(space_id),
            display_name: name.to_owned(),
            handle: handle.clone(),
            access_level: requested_access,
            role_text: role.to_owned(),
            computer_id: ComputerId::from_uuid(body.computer_id),
            driver_kind: if body.driver_kind == "codex" {
                DriverKind::Codex
            } else {
                DriverKind::Builtin
            },
            actor_member_id: MemberId::from_uuid(actor_id),
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            now,
        },
    )
    .await
    .map_err(application_error)?;

    let row = sqlx::query(
        &format!(
            "SELECT a.*,m.display_name,m.handle,m.access_level,c.connection_status,\
             c.deleted_at AS computer_deleted_at,{ACTIVITY_COLUMNS} \
             FROM agents a JOIN members m ON m.id=a.member_id JOIN computers c ON c.id=a.computer_id \
             {ACTIVITY_JOINS} WHERE a.member_id=$1"
        ),
    )
    .bind(agent_id)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok((StatusCode::CREATED, Json(agent_row(&row)?)))
}

/// Agent activity 与 last_error_code 的事实来源。
///
/// activity 取当前非终态 Run 的 status、Focus 地址和绑定 Task；Focus 地址是
/// `#slug:seq`，不含 Message 正文。last_error_code 先取最近一次失败 Run 上报的
/// 错误码，没有失败 Run 时退回该 Agent pending Item 上记录的领取错误。两者都是
/// 已落库的可验证事实，不由 lifecycle 猜测。
const ACTIVITY_COLUMNS: &str = "\
    active_run.status AS run_status,\
    active_run.task_id AS run_task_id,\
    active_task.title AS run_task_title,\
    focus_channel.slug AS run_focus_slug,\
    focus_message.channel_seq AS run_focus_seq,\
    COALESCE(\
        (SELECT r.error_code FROM agent_runs r \
         WHERE r.agent_id=a.member_id AND r.outcome_code='failed' AND r.error_code IS NOT NULL \
         ORDER BY r.finished_at DESC NULLS LAST,r.id DESC LIMIT 1),\
        (SELECT i.last_error_code FROM inbox_items i \
         WHERE i.agent_id=a.member_id AND i.status='pending' AND i.last_error_code IS NOT NULL \
         ORDER BY i.created_at DESC,i.id DESC LIMIT 1)\
    ) AS last_error_code";

/// activity 需要的 join。与 [`ACTIVITY_COLUMNS`] 成对使用。
const ACTIVITY_JOINS: &str = "\
    LEFT JOIN LATERAL (\
        SELECT r.status,r.task_id,r.focus_thread_id FROM agent_runs r \
        WHERE r.agent_id=a.member_id \
          AND r.status NOT IN ('completed','yielded','failed','canceled') \
        ORDER BY r.created_at DESC LIMIT 1\
    ) active_run ON true \
    LEFT JOIN tasks active_task ON active_task.id=active_run.task_id \
    LEFT JOIN threads focus_thread ON focus_thread.id=active_run.focus_thread_id \
    LEFT JOIN channels focus_channel ON focus_channel.id=focus_thread.channel_id \
    LEFT JOIN messages focus_message ON focus_message.id=focus_thread.root_message_id";

/// Attention 策略。当前是 Server 的固定策略,不是每个 Agent 的可配置字段:
/// 没有任何表保存它,写入路径也不存在。投影为只读值,使 Browser 知道生效参数。
fn attention_policy() -> AttentionConfig {
    AttentionConfig {
        dm_immediate: true,
        mention_immediate: true,
        ambient_enabled: true,
        ambient_debounce_seconds: 30,
        ambient_max_wait_seconds: 300,
        max_retry_count: 5,
    }
}

/// Focus 的可读地址。`#design:42`定位到 Channel 与 Root Message 序号，
/// 不暴露 Message 正文。
fn focus_address(row: &sqlx::postgres::PgRow) -> Option<String> {
    let slug: Option<String> = row.get("run_focus_slug");
    let seq: Option<i64> = row.get("run_focus_seq");
    Some(format!("#{}:{}", slug?, seq?))
}

/// activity 只描述可验证动作，见 [09-security-operations](../../../docs/design/09-security-operations.md)。
fn agent_activity(
    row: &sqlx::postgres::PgRow,
    activity_status: AgentActivityStatus,
) -> Option<AgentActivityResponse> {
    let run_status = row.get::<Option<String>, _>("run_status")?;
    let address = focus_address(row);
    let task_title: Option<String> = row.get("run_task_title");
    let label = match (run_status.as_str(), address.as_deref()) {
        ("stopping", Some(address)) => format!("Stopping work on {address}"),
        ("stopping", None) => "Stopping the current Run".to_owned(),
        ("finalizing", Some(address)) => format!("Finishing work on {address}"),
        ("finalizing", None) => "Finishing the current Run".to_owned(),
        ("queued", Some(address)) => format!("Queued for {address}"),
        ("queued", None) => "Queued for a Run".to_owned(),
        (_, Some(address)) => match task_title.as_deref() {
            Some(title) => format!("Working on {address} for {title}"),
            None => format!("Working on {address}"),
        },
        (_, None) => "Working on a Run".to_owned(),
    };
    Some(AgentActivityResponse {
        kind: run_status,
        label,
        status: activity_status,
    })
}

/// Run status 到 Agent activity 取值域的映射。未知 status 视为 idle:
/// 该字段只描述当前可见动作，不能因为新增 Run status 就让整个投影失败。
fn run_activity_status(status: Option<&str>) -> AgentActivityStatus {
    match status {
        Some("queued") => AgentActivityStatus::Queued,
        Some("starting") => AgentActivityStatus::Starting,
        Some("running") => AgentActivityStatus::Running,
        Some("finalizing") => AgentActivityStatus::Finalizing,
        Some("stopping") => AgentActivityStatus::Stopping,
        _ => AgentActivityStatus::Idle,
    }
}

fn agent_row(row: &sqlx::postgres::PgRow) -> Result<AgentResponse, ApiError> {
    let lifecycle: &str = row.get("lifecycle");
    let connection: Option<String> = row.get("connection_status");
    let run_status: Option<String> = row.get("run_status");
    let retired = lifecycle == "retired";
    let desired_lifecycle = match lifecycle {
        "suspended" => AgentLifecycle::Suspended,
        "retired" => AgentLifecycle::Retired,
        _ => AgentLifecycle::Active,
    };
    let provision_status = match lifecycle {
        "provisioning" => ProvisionStatus::Provisioning,
        "error" => ProvisionStatus::Error,
        _ => ProvisionStatus::Ready,
    };
    let activity_status = match lifecycle {
        "error" => AgentActivityStatus::Error,
        "provisioning" => AgentActivityStatus::Unreachable,
        "suspended" => AgentActivityStatus::Suspended,
        _ if connection.as_deref() != Some("online") => AgentActivityStatus::Unreachable,
        _ => run_activity_status(run_status.as_deref()),
    };
    Ok(AgentResponse {
        member_id: row.get("member_id"),
        space_id: row.get("space_id"),
        computer_id: row.get("computer_id"),
        name: row.get("display_name"),
        handle: row.get("handle"),
        // Agent 不能是 Owner:Space 治理者只能是 Human。
        access_level: match row.get::<&str, _>("access_level") {
            "admin" => AgentAccessLevel::Admin,
            "member" => AgentAccessLevel::Member,
            _ => return Err(ApiError::internal()),
        },
        role_text: row.get("role_text"),
        role_revision: u64::try_from(row.get::<i64, _>("role_revision"))
            .map_err(|_| ApiError::internal())?,
        desired_lifecycle,
        provision_status,
        activity_status,
        driver_kind: match row.get::<&str, _>("driver_kind") {
            "codex" => DriverKindCode::Codex,
            "builtin" => DriverKindCode::Builtin,
            _ => return Err(ApiError::internal()),
        },
        attention_config: attention_policy(),
        activity: agent_activity(row, activity_status),
        last_error_code: row.get("last_error_code"),
        // Memory 投影来自在线 Computer，由单资源读取补齐。
        memory_files: Vec::new(),
        created_at: timestamp(row.get("created_at")),
        updated_at: timestamp(row.get("created_at")),
        retired_at: if retired {
            optional_timestamp(row.get("retired_at"))
        } else {
            None
        },
    })
}

async fn list_messages(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(channel_id): Path<Uuid>,
) -> Result<Json<MessagePageResponse>, ApiError> {
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
    Ok(Json(MessagePageResponse {
        channel_id,
        messages,
        snapshot_channel_seq: u64::try_from(snapshot).map_err(|_| ApiError::internal())?,
        // 分页游标尚未实现，当前投影一次返回全部 Root Message。
        has_more_before: false,
        has_more_after: false,
    }))
}

async fn list_channel_members(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(channel_id): Path<Uuid>,
) -> Result<Json<ChannelMembersResponse>, ApiError> {
    let viewer_id = channel_member(&state, &jar, channel_id).await?;
    Ok(Json(
        channel_members_response(&state.pool, channel_id, viewer_id).await?,
    ))
}

async fn channel_members_response(
    pool: &PgPool,
    channel_id: Uuid,
    viewer_id: Uuid,
) -> Result<ChannelMembersResponse, ApiError> {
    let can_manage: bool =
        sqlx::query_scalar("SELECT access_level IN ('owner','admin') FROM members WHERE id=$1")
            .bind(viewer_id)
            .fetch_one(pool)
            .await
            .map_err(map_sqlx)?;
    let rows = sqlx::query(
        "SELECT members.id,members.kind,members.display_name,members.handle,members.access_level \
         FROM channel_members JOIN members ON members.id=channel_members.member_id \
         WHERE channel_members.channel_id=$1 ORDER BY channel_members.joined_at,members.id",
    )
    .bind(channel_id)
    .fetch_all(pool)
    .await
    .map_err(map_sqlx)?;
    let mut members = Vec::with_capacity(rows.len());
    for row in &rows {
        members.push(member_row(pool, row).await?);
    }
    Ok(ChannelMembersResponse {
        members,
        can_manage,
    })
}

async fn add_channel_agents(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(channel_id): Path<Uuid>,
    Json(body): Json<AddChannelAgentsBody>,
) -> Result<Json<ChannelMembersResponse>, ApiError> {
    let actor_id = channel_member(&state, &jar, channel_id).await?;
    let key = idempotency_header(&headers)?;
    let agent_ids = body.agent_member_ids.into_iter().collect::<BTreeSet<_>>();
    if agent_ids.is_empty() {
        return Err(ApiError::invalid("At least one Agent is required"));
    }
    let agent_ids = agent_ids.into_iter().collect::<Vec<_>>();
    let mut transaction = state.pool.begin().await.map_err(map_sqlx)?;
    lock_idempotency(&mut transaction, actor_id, "channel.members.add", key).await?;
    let channel = sqlx::query(
        "SELECT c.space_id,c.kind,m.access_level FROM channels c \
         JOIN members m ON m.id=$2 AND m.space_id=c.space_id \
         JOIN channel_members cm ON cm.channel_id=c.id AND cm.member_id=m.id \
         WHERE c.id=$1 FOR UPDATE OF c",
    )
    .bind(channel_id)
    .bind(actor_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::not_found)?;
    if !matches!(channel.get::<&str, _>("access_level"), "owner" | "admin") {
        return Err(ApiError::permission_denied());
    }
    if channel.get::<&str, _>("kind") == "direct" {
        return Err(ApiError::invalid("DM membership cannot be changed"));
    }
    let space_id: Uuid = channel.get("space_id");
    let valid_agent_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM members m JOIN agents a ON a.member_id=m.id \
         WHERE m.space_id=$1 AND m.kind='agent' AND a.lifecycle<>'retired' AND m.id=ANY($2)",
    )
    .bind(space_id)
    .bind(&agent_ids)
    .fetch_one(&mut *transaction)
    .await
    .map_err(map_sqlx)?;
    if valid_agent_count != agent_ids.len() as i64 {
        return Err(ApiError::invalid(
            "Channel members must be active Agents in the same Space",
        ));
    }
    let replayed: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM idempotency_records WHERE actor_member_id=$1 \
         AND action='channel.members.add' AND idempotency_key=$2 AND resource_id=$3)",
    )
    .bind(actor_id)
    .bind(key)
    .bind(channel_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(map_sqlx)?;
    if !replayed {
        let now = OffsetDateTime::now_utc();
        let inserted = sqlx::query_scalar::<_, Uuid>(
            "INSERT INTO channel_members(channel_id,space_id,member_id,joined_at,last_read_seq) \
             SELECT $1,$2,requested.member_id,$4,0 FROM UNNEST($3::uuid[]) AS requested(member_id) \
             ON CONFLICT DO NOTHING RETURNING member_id",
        )
        .bind(channel_id)
        .bind(space_id)
        .bind(&agent_ids)
        .bind(now)
        .fetch_all(&mut *transaction)
        .await
        .map_err(map_sqlx)?;
        let mut result_hasher = Sha256::new();
        result_hasher.update(channel_id.as_bytes());
        for agent_id in &agent_ids {
            result_hasher.update(agent_id.as_bytes());
        }
        let result_hash = result_hasher.finalize();
        sqlx::query("INSERT INTO idempotency_records(actor_member_id,action,idempotency_key,response_code,resource_id,result_hash,created_at) VALUES($1,'channel.members.add',$2,'ok',$3,$4,$5)")
            .bind(actor_id).bind(key).bind(channel_id).bind(result_hash.as_slice()).bind(now)
            .execute(&mut *transaction).await.map_err(map_sqlx)?;
        if !inserted.is_empty() {
            sqlx::query("INSERT INTO audit_events(id,space_id,actor_member_id,action,subject_type,subject_id,metadata_json,created_at) VALUES($1,$2,$3,'channel.members_added','channel',$4,$5,$6)")
                .bind(Uuid::now_v7()).bind(space_id).bind(actor_id).bind(channel_id)
                .bind(json!({"added_count": inserted.len()})).bind(now)
                .execute(&mut *transaction).await.map_err(map_sqlx)?;
            for member_id in inserted {
                sqlx::query("INSERT INTO outbox_events(id,space_id,kind,payload_json,created_at) VALUES($1,$2,'member.changed',$3,$4)")
                    .bind(Uuid::now_v7()).bind(space_id).bind(json!({"resource_id":member_id})).bind(now)
                    .execute(&mut *transaction).await.map_err(map_sqlx)?;
            }
        }
    }
    transaction.commit().await.map_err(map_sqlx)?;
    Ok(Json(
        channel_members_response(&state.pool, channel_id, actor_id).await?,
    ))
}

async fn create_root_message(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(channel_id): Path<Uuid>,
    Json(body): Json<CreateMessageBody>,
) -> Result<(StatusCode, Json<MessageResponse>), ApiError> {
    let member_id = channel_member(&state, &jar, channel_id).await?;
    let message_id = insert_message(
        &state,
        channel_id,
        member_id,
        MessageWriteContext {
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            thread_id: None,
            handled_item: None,
            expected_snapshot: None,
        },
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

async fn read_thread(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(thread_id): Path<Uuid>,
) -> Result<Json<ThreadReadResponse>, ApiError> {
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
    let is_following: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM thread_subscriptions WHERE thread_id=$1 AND member_id=$2)",
    )
    .bind(thread_id)
    .bind(member_id)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok(Json(ThreadReadResponse {
        thread_id,
        channel_id,
        root,
        replies: projected.into_iter().skip(1).collect(),
        snapshot_channel_seq: u64::try_from(snapshot).map_err(|_| ApiError::internal())?,
        is_following,
        task: None,
        task_relation: None,
    }))
}

async fn create_thread_reply(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(thread_id): Path<Uuid>,
    Json(body): Json<CreateMessageBody>,
) -> Result<(StatusCode, Json<MessageResponse>), ApiError> {
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
        MessageWriteContext {
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            thread_id: Some(thread_id),
            handled_item: None,
            expected_snapshot: None,
        },
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

async fn update_message(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(message_id): Path<Uuid>,
    Json(body): Json<UpdateMessageBody>,
) -> Result<Json<MessageResponse>, ApiError> {
    let channel_id = sqlx::query_scalar::<_, Uuid>("SELECT channel_id FROM messages WHERE id=$1")
        .bind(message_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    let actor = channel_member(&state, &jar, channel_id).await?;
    let mut storage = state.storage.clone();
    EditMessage::execute(
        &mut storage,
        EditMessageInput {
            message_id: MessageId::from_uuid(message_id),
            actor_member_id: MemberId::from_uuid(actor),
            body_markdown: body.body_markdown,
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    let row = sqlx::query("SELECT * FROM messages WHERE id=$1")
        .bind(message_id)
        .fetch_one(&state.pool)
        .await
        .map_err(map_sqlx)?;
    Ok(Json(message_row(&state.pool, &row, actor).await?))
}

async fn delete_message(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(message_id): Path<Uuid>,
) -> Result<Json<MessageResponse>, ApiError> {
    let channel_id = sqlx::query_scalar::<_, Uuid>("SELECT channel_id FROM messages WHERE id=$1")
        .bind(message_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    let actor = channel_member(&state, &jar, channel_id).await?;
    let mut storage = state.storage.clone();
    DeleteMessage::execute(
        &mut storage,
        MessageId::from_uuid(message_id),
        MemberId::from_uuid(actor),
        crate::ids::IdempotencyKey::from_uuid(idempotency_header(&headers)?),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    let row = sqlx::query("SELECT * FROM messages WHERE id=$1")
        .bind(message_id)
        .fetch_one(&state.pool)
        .await
        .map_err(map_sqlx)?;
    Ok(Json(message_row(&state.pool, &row, actor).await?))
}

fn permission_action(action_code: &str) -> Result<PermissionAction, ApiError> {
    match action_code {
        "channel.create" => Ok(PermissionAction::ChannelCreate),
        "agent.create" => Ok(PermissionAction::AgentCreate),
        _ => Err(ApiError::invalid("Permission action code is not supported")),
    }
}

async fn set_permission(
    state: &RuntimeState,
    jar: &CookieJar,
    headers: &HeaderMap,
    member_id: Uuid,
    action_code: &str,
    enabled: bool,
) -> Result<Json<MemberResponse>, ApiError> {
    let space_id = sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM members WHERE id=$1")
        .bind(member_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    let actor = current_member(state, jar, space_id).await?;
    let mut storage = state.storage.clone();
    SetPermission::execute(
        &mut storage,
        MemberId::from_uuid(actor),
        MemberId::from_uuid(member_id),
        permission_action(action_code)?,
        enabled,
        crate::ids::IdempotencyKey::from_uuid(idempotency_header(headers)?),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    let row =
        sqlx::query("SELECT id,kind,display_name,handle,access_level FROM members WHERE id=$1")
            .bind(member_id)
            .fetch_one(&state.pool)
            .await
            .map_err(map_sqlx)?;
    Ok(Json(member_row(&state.pool, &row).await?))
}

async fn grant_permission(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path((member_id, action_code)): Path<(Uuid, String)>,
) -> Result<Json<MemberResponse>, ApiError> {
    set_permission(&state, &jar, &headers, member_id, &action_code, true).await
}

async fn revoke_permission(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path((member_id, action_code)): Path<(Uuid, String)>,
) -> Result<Json<MemberResponse>, ApiError> {
    set_permission(&state, &jar, &headers, member_id, &action_code, false).await
}

async fn list_tasks(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<TaskResponse>>, ApiError> {
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
    Ok(Json(tasks))
}

async fn get_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(task_id): Path<Uuid>,
) -> Result<Json<TaskResponse>, ApiError> {
    let space_id = sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM tasks WHERE id=$1")
        .bind(task_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    current_member(&state, &jar, space_id).await?;
    Ok(Json(task_detail(&state, task_id).await?))
}

async fn task_runs(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(task_id): Path<Uuid>,
) -> Result<Json<Vec<RunResponse>>, ApiError> {
    task_actor(&state, &jar, task_id).await?;
    let ids = sqlx::query_scalar::<_, Uuid>(
        "SELECT id FROM agent_runs WHERE task_id=$1 ORDER BY created_at DESC,id DESC",
    )
    .bind(task_id)
    .fetch_all(&state.pool)
    .await
    .map_err(map_sqlx)?;
    let mut runs = Vec::with_capacity(ids.len());
    for id in ids {
        runs.push(run_projection(&state.pool, id).await?);
    }
    Ok(Json(runs))
}

async fn create_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(message_id): Path<Uuid>,
    Json(body): Json<CreateTaskBody>,
) -> Result<(StatusCode, Json<TaskResponse>), ApiError> {
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
        Json(task_detail(&state, task.0.id.into_uuid()).await?),
    ))
}

async fn link_task_thread(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
    Json(body): Json<LinkThreadBody>,
) -> Result<Json<TaskResponse>, ApiError> {
    let actor = task_actor(&state, &jar, task_id).await?;
    let mut storage = state.storage.clone();
    LinkThreadToTask::execute(
        &mut storage,
        LinkThreadInput {
            task_id: TaskId::from_uuid(task_id),
            target_thread_id: ThreadId::from_uuid(body.thread_id),
            actor_member_id: MemberId::from_uuid(actor),
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(task_detail(&state, task_id).await?))
}

async fn unlink_task_thread(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path((task_id, thread_id)): Path<(Uuid, Uuid)>,
) -> Result<Json<TaskResponse>, ApiError> {
    let actor = task_actor(&state, &jar, task_id).await?;
    let mut storage = state.storage.clone();
    UnlinkThreadFromTask::execute(
        &mut storage,
        LinkThreadInput {
            task_id: TaskId::from_uuid(task_id),
            target_thread_id: ThreadId::from_uuid(thread_id),
            actor_member_id: MemberId::from_uuid(actor),
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(task_detail(&state, task_id).await?))
}

async fn update_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
    Json(body): Json<UpdateTaskBody>,
) -> Result<Json<TaskResponse>, ApiError> {
    if body.title.trim().is_empty() {
        return Err(ApiError::invalid("Task title is required"));
    }
    update_task_action(
        &state,
        &jar,
        &headers,
        task_id,
        TaskAction::Rename { title: body.title },
    )
    .await
}

async fn start_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
    Json(body): Json<StartTaskBody>,
) -> Result<Json<TaskResponse>, ApiError> {
    update_task_action(
        &state,
        &jar,
        &headers,
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
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
) -> Result<Json<TaskResponse>, ApiError> {
    update_task_action(&state, &jar, &headers, task_id, TaskAction::SubmitReview).await
}

async fn request_task_changes(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
) -> Result<Json<TaskResponse>, ApiError> {
    update_task_action(&state, &jar, &headers, task_id, TaskAction::RequestChanges).await
}

async fn close_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
    Json(body): Json<CloseTaskBody>,
) -> Result<Json<TaskResponse>, ApiError> {
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
        &headers,
        task_id,
        TaskAction::Close {
            reason,
            note: body.note,
        },
    )
    .await
}

async fn reset_task_session(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
) -> Result<Json<TaskResponse>, ApiError> {
    update_task_action(&state, &jar, &headers, task_id, TaskAction::ResetSession).await
}

async fn complete_task(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(task_id): Path<Uuid>,
    Json(body): Json<CompleteTaskBody>,
) -> Result<Json<TaskResponse>, ApiError> {
    let actor = task_actor(&state, &jar, task_id).await?;
    let mut storage = state.storage.clone();
    CompleteTask::execute(
        &mut storage,
        CompleteTaskInput {
            task_id: TaskId::from_uuid(task_id),
            actor_member_id: MemberId::from_uuid(actor),
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(idempotency_header(&headers)?),
            result_message_id: MessageId::from_uuid(Uuid::now_v7()),
            result_thread_id: ThreadId::from_uuid(body.result_thread_id),
            result_markdown: body.result_markdown,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(task_detail(&state, task_id).await?))
}

async fn update_task_action(
    state: &RuntimeState,
    jar: &CookieJar,
    headers: &HeaderMap,
    task_id: Uuid,
    action: TaskAction,
) -> Result<Json<TaskResponse>, ApiError> {
    let actor = task_actor(state, jar, task_id).await?;
    let mut storage = state.storage.clone();
    UpdateTask::execute(
        &mut storage,
        UpdateTaskInput {
            task_id: TaskId::from_uuid(task_id),
            actor_member_id: MemberId::from_uuid(actor),
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(idempotency_header(headers)?),
            action,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(task_detail(state, task_id).await?))
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
    let actor = current_member(state, jar, space_id).await?;
    let can_read: bool = sqlx::query_scalar(
        "SELECT EXISTS(\
           SELECT 1 FROM tasks task \
           JOIN threads source ON source.id=task.source_thread_id \
           JOIN channel_members membership ON membership.channel_id=source.channel_id \
           WHERE task.id=$1 AND membership.member_id=$2\
         )",
    )
    .bind(task_id)
    .bind(actor)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    if !can_read {
        return Err(ApiError::not_found());
    }
    Ok(actor)
}

async fn insert_message(
    state: &RuntimeState,
    channel_id: Uuid,
    author: Uuid,
    context: MessageWriteContext,
    body: CreateMessageBody,
) -> Result<Uuid, ApiError> {
    if body.body_markdown.trim().is_empty() {
        return Err(ApiError::invalid("Message body is required"));
    }
    let message_id = MessageId::from_uuid(Uuid::now_v7());
    let mut storage = state.storage.clone();
    let published = PublishMessage::execute(
        &mut storage,
        MessageDraft {
            message_id,
            channel_id: ChannelId::from_uuid(channel_id),
            author_member_id: MemberId::from_uuid(author),
            idempotency_key: context.idempotency_key,
            thread_id: context.thread_id.map(ThreadId::from_uuid),
            reply_to_message_id: body.reply_to_message_id.map(MessageId::from_uuid),
            body_markdown: body.body_markdown,
            mentions: body.mentions.into_iter().map(MemberId::from_uuid).collect(),
            attachment_ids: body
                .attachment_ids
                .into_iter()
                .map(AttachmentId::from_uuid)
                .collect(),
            handled_item: context.handled_item.map(|(run_id, item_id)| {
                (RunId::from_uuid(run_id), InboxItemId::from_uuid(item_id))
            }),
            expected_snapshot: context.expected_snapshot,
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    for item_id in published.hard_item_ids {
        let mut storage = state.storage.clone();
        if let Err(error) =
            RouteHardItem::execute(&mut storage, RouteHardItemInput { item_id }).await
        {
            tracing::warn!(%item_id, error = %error, "hard Inbox Item remains pending after immediate routing failed");
        }
    }
    Ok(published.message_id.into_uuid())
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
) -> Result<(StatusCode, Json<AttachmentResponse>), ApiError> {
    let space_id =
        require_active_agent_run(&state, &headers, computer_id, agent_id, run_id).await?;
    let key = idempotency_header(&headers)?;
    let mut storage = state.storage.clone();
    let opened = OpenUpload::execute(
        &mut storage,
        OpenUploadInput {
            attachment_id: AttachmentId::from_uuid(Uuid::now_v7()),
            space_id: SpaceId::from_uuid(space_id),
            uploader_member_id: MemberId::from_uuid(agent_id),
            name: &body.original_name,
            media_type: &body.media_type,
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(key),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    let status = if opened.created {
        StatusCode::CREATED
    } else {
        StatusCode::OK
    };
    Ok((
        status,
        Json(attachment_response(
            &opened.attachment,
            AttachmentPath::Upload,
        )),
    ))
}

async fn agent_upload_content(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, agent_id, run_id, attachment_id)): Path<(Uuid, Uuid, Uuid, Uuid)>,
    body: Bytes,
) -> Result<StatusCode, ApiError> {
    require_active_agent_run(&state, &headers, computer_id, agent_id, run_id).await?;
    let mut storage = state.storage.clone();
    WriteUploadContent::execute(
        &mut storage,
        state.objects.as_ref(),
        WriteUploadContentInput {
            attachment_id: AttachmentId::from_uuid(attachment_id),
            uploader_member_id: MemberId::from_uuid(agent_id),
            content: body.to_vec(),
            max_bytes: state.attachment_max_bytes,
        },
    )
    .await
    .map_err(application_error)?;
    Ok(StatusCode::NO_CONTENT)
}

async fn agent_complete_upload(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, agent_id, run_id, attachment_id)): Path<(Uuid, Uuid, Uuid, Uuid)>,
    Json(body): Json<CompleteUploadBody>,
) -> Result<Json<AttachmentResponse>, ApiError> {
    require_active_agent_run(&state, &headers, computer_id, agent_id, run_id).await?;
    let key = idempotency_header(&headers)?;
    let mut storage = state.storage.clone();
    let attachment = CompleteUpload::execute(
        &mut storage,
        state.objects.as_ref(),
        CompleteUploadInput {
            attachment_id: AttachmentId::from_uuid(attachment_id),
            uploader_member_id: MemberId::from_uuid(agent_id),
            declared: DeclaredContent {
                size: body.size,
                sha256_hex: body.sha256,
            },
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(key),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(attachment_response(
        &attachment,
        AttachmentPath::Download,
    )))
}

async fn agent_download_attachment(
    State(state): State<RuntimeState>,
    headers: HeaderMap,
    Path((computer_id, agent_id, run_id, attachment_id)): Path<(Uuid, Uuid, Uuid, Uuid)>,
) -> Result<Bytes, ApiError> {
    require_active_agent_run(&state, &headers, computer_id, agent_id, run_id).await?;
    let mut storage = state.storage.clone();
    // Agent 可以取回自己刚上传、尚未链接到 Message 的 Attachment。
    let downloaded = ReadAttachment::for_uploader_or_member(
        &mut storage,
        state.objects.as_ref(),
        AttachmentId::from_uuid(attachment_id),
        MemberId::from_uuid(agent_id),
    )
    .await
    .map_err(application_error)?;
    Ok(Bytes::from(downloaded.content))
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
    headers: HeaderMap,
    Json(body): Json<CreateUploadBody>,
) -> Result<(StatusCode, Json<AttachmentResponse>), ApiError> {
    let member = current_member(&state, &jar, body.space_id).await?;
    let key = idempotency_header(&headers)?;
    let mut storage = state.storage.clone();
    let opened = OpenUpload::execute(
        &mut storage,
        OpenUploadInput {
            attachment_id: AttachmentId::from_uuid(Uuid::now_v7()),
            space_id: SpaceId::from_uuid(body.space_id),
            uploader_member_id: MemberId::from_uuid(member),
            name: &body.original_name,
            media_type: &body.media_type,
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(key),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    let status = if opened.created {
        StatusCode::CREATED
    } else {
        StatusCode::OK
    };
    Ok((
        status,
        Json(attachment_response(
            &opened.attachment,
            AttachmentPath::Upload,
        )),
    ))
}

async fn upload_content(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(attachment_id): Path<Uuid>,
    body: Bytes,
) -> Result<StatusCode, ApiError> {
    let member = attachment_space_member(&state, &jar, attachment_id).await?;
    let mut storage = state.storage.clone();
    WriteUploadContent::execute(
        &mut storage,
        state.objects.as_ref(),
        WriteUploadContentInput {
            attachment_id: AttachmentId::from_uuid(attachment_id),
            uploader_member_id: MemberId::from_uuid(member),
            content: body.to_vec(),
            max_bytes: state.attachment_max_bytes,
        },
    )
    .await
    .map_err(application_error)?;
    Ok(StatusCode::NO_CONTENT)
}

async fn complete_upload(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(attachment_id): Path<Uuid>,
    Json(body): Json<CompleteUploadBody>,
) -> Result<Json<AttachmentResponse>, ApiError> {
    let member = attachment_space_member(&state, &jar, attachment_id).await?;
    let key = idempotency_header(&headers)?;
    let mut storage = state.storage.clone();
    let attachment = CompleteUpload::execute(
        &mut storage,
        state.objects.as_ref(),
        CompleteUploadInput {
            attachment_id: AttachmentId::from_uuid(attachment_id),
            uploader_member_id: MemberId::from_uuid(member),
            declared: DeclaredContent {
                size: body.size,
                sha256_hex: body.sha256,
            },
            idempotency_key: crate::ids::IdempotencyKey::from_uuid(key),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok(Json(attachment_response(
        &attachment,
        AttachmentPath::Download,
    )))
}

async fn download_attachment(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(attachment_id): Path<Uuid>,
) -> Result<Response, ApiError> {
    let member = attachment_space_member(&state, &jar, attachment_id).await?;
    let mut storage = state.storage.clone();
    let downloaded = ReadAttachment::for_member(
        &mut storage,
        state.objects.as_ref(),
        AttachmentId::from_uuid(attachment_id),
        MemberId::from_uuid(member),
    )
    .await
    .map_err(application_error)?;
    attachment_download_response(&downloaded)
}

fn attachment_download_response(downloaded: &AttachmentContent) -> Result<Response, ApiError> {
    let disposition = HeaderValue::from_str(&format!(
        "attachment; filename=\"{}\"",
        downloaded.attachment.header_safe_name()
    ))
    .map_err(|_| ApiError::invalid("Attachment name cannot be represented in a header"))?;
    let media_type = HeaderValue::from_str(&downloaded.attachment.media_type)
        .map_err(|_| ApiError::invalid("Attachment media type is invalid"))?;
    Ok((
        [
            (header::CONTENT_DISPOSITION, disposition),
            (header::CONTENT_TYPE, media_type),
        ],
        Bytes::from(downloaded.content.clone()),
    )
        .into_response())
}

/// Attachment 响应中返回哪个操作路径。上传态返回写入路径，就绪态返回下载路径。
enum AttachmentPath {
    Upload,
    Download,
}

fn attachment_response(attachment: &Attachment, path: AttachmentPath) -> AttachmentResponse {
    let id = attachment.id.into_uuid();
    let (upload_path, download_path) = match path {
        AttachmentPath::Upload => (Some(format!("/api/v1/attachments/{id}/content")), None),
        AttachmentPath::Download => (None, Some(format!("/api/v1/attachments/{id}/download"))),
    };
    AttachmentResponse {
        id,
        space_id: attachment.space_id.into_uuid(),
        uploader_member_id: attachment.uploader_member_id.into_uuid(),
        original_name: attachment.name.clone(),
        media_type: attachment.media_type.clone(),
        size: attachment.length,
        sha256: attachment.sha256.map(hex::encode),
        status: match attachment.status {
            AttachmentStatusKind::Uploading => AttachmentStatus::Uploading,
            AttachmentStatusKind::Ready => AttachmentStatus::Ready,
            AttachmentStatusKind::Deleted => AttachmentStatus::Deleted,
        },
        upload_path,
        download_path,
        created_at: timestamp(attachment.created_at),
    }
}

/// 在 actor 与 idempotency key 上取事务级锁。
/// 尚未迁移到 application 的 Channel 成员写入路径继续使用该 helper。
async fn lock_idempotency(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    actor_id: Uuid,
    action: &str,
    key: Uuid,
) -> Result<(), ApiError> {
    let lock_key = format!("{actor_id}:{action}:{key}");
    sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))")
        .bind(lock_key)
        .execute(&mut **transaction)
        .await
        .map_err(map_sqlx)?;
    Ok(())
}

async fn attachment_space_member(
    state: &RuntimeState,
    jar: &CookieJar,
    attachment_id: Uuid,
) -> Result<Uuid, ApiError> {
    let token = require_session_token(jar)?;
    let mut storage = state.storage.clone();
    let access = AuthorizeAttachmentAccess::execute(
        &mut storage,
        &token,
        AttachmentId::from_uuid(attachment_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(access.member_id.into_uuid())
}

fn session_token(jar: &CookieJar) -> Option<RawSessionToken> {
    jar.get(SESSION_COOKIE)
        .map(|cookie| RawSessionToken::new(cookie.value().to_owned()))
}

fn require_session_token(jar: &CookieJar) -> Result<RawSessionToken, ApiError> {
    session_token(jar).ok_or_else(ApiError::unauthenticated)
}

fn session_cookie(jar: CookieJar, token: &RawSessionToken) -> CookieJar {
    let cookie = Cookie::build((SESSION_COOKIE, token.expose().to_owned()))
        .path("/")
        .http_only(true)
        .same_site(SameSite::Lax)
        .build();
    jar.add(cookie)
}

async fn authenticate(
    state: &RuntimeState,
    jar: &CookieJar,
) -> Result<AuthenticatedHuman, ApiError> {
    let token = require_session_token(jar)?;
    let mut storage = state.storage.clone();
    AuthenticateSession::execute(&mut storage, &token, OffsetDateTime::now_utc())
        .await
        .map_err(application_error)
}

async fn current_member(
    state: &RuntimeState,
    jar: &CookieJar,
    space_id: Uuid,
) -> Result<Uuid, ApiError> {
    let token = require_session_token(jar)?;
    let mut storage = state.storage.clone();
    let access = AuthorizeSpaceAccess::execute(
        &mut storage,
        &token,
        SpaceId::from_uuid(space_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(access.member_id.into_uuid())
}

async fn agent_space_member(
    state: &RuntimeState,
    jar: &CookieJar,
    agent_id: Uuid,
) -> Result<Uuid, ApiError> {
    let token = require_session_token(jar)?;
    let mut storage = state.storage.clone();
    let access = AuthorizeAgentAccess::execute(
        &mut storage,
        &token,
        MemberId::from_uuid(agent_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(access.member_id.into_uuid())
}

async fn require_agent_governor(
    state: &RuntimeState,
    jar: &CookieJar,
    agent_id: Uuid,
) -> Result<Uuid, ApiError> {
    let token = require_session_token(jar)?;
    let mut storage = state.storage.clone();
    let access = AuthorizeAgentGovernance::execute(
        &mut storage,
        &token,
        MemberId::from_uuid(agent_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(access.member_id.into_uuid())
}

async fn require_computer_governor(
    state: &RuntimeState,
    jar: &CookieJar,
    computer_id: Uuid,
) -> Result<Uuid, ApiError> {
    let token = require_session_token(jar)?;
    let mut storage = state.storage.clone();
    let access = AuthorizeComputerGovernance::execute(
        &mut storage,
        &token,
        ComputerId::from_uuid(computer_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(access.member_id.into_uuid())
}

async fn space_events(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    headers: HeaderMap,
    Path(space_id): Path<Uuid>,
) -> Result<Sse<impl futures_util::Stream<Item = Result<SseEvent, Infallible>>>, ApiError> {
    current_member(&state, &jar, space_id).await?;
    let space_id = SpaceId::from_uuid(space_id);
    let last_event_id = headers
        .get("last-event-id")
        .map(|value| {
            value
                .to_str()
                .ok()
                .and_then(|value| value.parse().ok())
                .ok_or_else(|| ApiError::invalid("Last-Event-ID is invalid"))
        })
        .transpose()?;
    let initial = state
        .storage
        .browser_events(space_id, last_event_id)
        .await
        .map_err(application_error)?
        .ok_or_else(ApiError::context_changed)?;
    let events = stream::unfold(
        (
            state.storage,
            space_id,
            last_event_id,
            VecDeque::from(initial),
        ),
        |(storage, space_id, mut cursor, mut buffered)| async move {
            loop {
                if let Some(event) = buffered.pop_front() {
                    cursor = Some(event.event_id);
                    let event = match event.into_sse() {
                        Ok(event) => event,
                        Err(_) => return None,
                    };
                    return Some((Ok(event), (storage, space_id, cursor, buffered)));
                }
                tokio::time::sleep(std::time::Duration::from_secs(1)).await;
                buffered = match storage.browser_events(space_id, cursor).await {
                    Ok(Some(events)) => VecDeque::from(events),
                    Ok(None) | Err(_) => return None,
                };
            }
        },
    );
    Ok(Sse::new(events).keep_alive(KeepAlive::default()))
}

async fn channel_member(
    state: &RuntimeState,
    jar: &CookieJar,
    channel_id: Uuid,
) -> Result<Uuid, ApiError> {
    let token = require_session_token(jar)?;
    let mut storage = state.storage.clone();
    let member_id = AuthorizeChannelAccess::execute(
        &mut storage,
        &token,
        ChannelId::from_uuid(channel_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(member_id.into_uuid())
}

fn user_response(human: &AuthenticatedHuman) -> UserResponse {
    UserResponse {
        id: human.user_id,
        display_name: human.display_name.clone(),
        email: human.email_normalized.clone(),
    }
}
/// Space 的 accent 目前是固定值:没有对应存储也没有写入路径。
const SPACE_ACCENT: &str = "#FFD440";

fn space_response(
    id: Uuid,
    name: &str,
    slug: &str,
    owner: Uuid,
    current: Uuid,
    general: Uuid,
) -> SpaceResponse {
    SpaceResponse {
        id,
        name: name.to_owned(),
        slug: slug.to_owned(),
        accent: SPACE_ACCENT.to_owned(),
        owner_member_id: owner,
        current_member_id: current,
        general_channel_id: general,
    }
}

fn space_row(row: &sqlx::postgres::PgRow) -> SpaceResponse {
    space_response(
        row.get("id"),
        row.get("name"),
        row.get("slug"),
        row.get("owner_member_id"),
        row.get("current_member_id"),
        row.get("general_channel_id"),
    )
}

/// Channel 投影只描述非 DM Channel:它要求 slug，且 kind 取值域不含 direct。
/// DM 用 [`direct_message_response`]，两者的可见字段不同。
fn channel_row(row: &sqlx::postgres::PgRow, creator: Uuid) -> Result<ChannelResponse, ApiError> {
    let kind = match row.get::<&str, _>("kind") {
        "public" => ChannelKindCode::Public,
        "private" => ChannelKindCode::Private,
        _ => return Err(ApiError::internal()),
    };
    let slug: String = row.try_get("slug").map_err(|_| ApiError::internal())?;
    let topic: Option<String> = row.get("topic");
    Ok(ChannelResponse {
        id: row.get("id"),
        space_id: row.get("space_id"),
        name: topic.clone().unwrap_or_else(|| slug.clone()),
        slug,
        topic,
        kind,
        created_by_member_id: creator,
        joined: row.get("joined"),
        archived_at: optional_timestamp(row.get("archived_at")),
    })
}
async fn member_row(
    pool: &PgPool,
    row: &sqlx::postgres::PgRow,
) -> Result<MemberResponse, ApiError> {
    let id: Uuid = row.get("id");
    let permissions = sqlx::query_scalar::<_, String>(
        "SELECT action_code FROM member_permissions WHERE member_id=$1 ORDER BY action_code",
    )
    .bind(id)
    .fetch_all(pool)
    .await
    .map_err(map_sqlx)?;
    Ok(MemberResponse {
        id,
        kind: match row.get::<&str, _>("kind") {
            "human" => MemberKindCode::Human,
            "agent" => MemberKindCode::Agent,
            _ => return Err(ApiError::internal()),
        },
        display_name: row.get("display_name"),
        handle: row.get("handle"),
        access_level: match row.get::<&str, _>("access_level") {
            "owner" => AccessLevelCode::Owner,
            "admin" => AccessLevelCode::Admin,
            "member" => AccessLevelCode::Member,
            _ => return Err(ApiError::internal()),
        },
        permissions,
    })
}
async fn message_row(
    pool: &PgPool,
    row: &sqlx::postgres::PgRow,
    _viewer: Uuid,
) -> Result<MessageResponse, ApiError> {
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
    let mut attachment_views = Vec::with_capacity(attachments.len());
    for attachment in &attachments {
        attachment_views.push(attachment_row(attachment)?);
    }
    let attention_failures = sqlx::query(
        "SELECT i.agent_id,m.handle,i.last_error_code FROM inbox_items i \
         JOIN members m ON m.id=i.agent_id WHERE i.message_id=$1 \
         AND i.last_error_code IS NOT NULL ORDER BY m.handle,i.agent_id",
    )
    .bind(id)
    .fetch_all(pool)
    .await
    .map_err(map_sqlx)?
    .iter()
    .map(|failure| AttentionFailureResponse {
        agent_member_id: failure.get("agent_id"),
        agent_handle: failure.get("handle"),
        error_code: failure.get("last_error_code"),
        // 领取失败由 Server 自动重试，Human 无需操作。
        retrying: true,
    })
    .collect::<Vec<_>>();
    let content = match row.get::<&str, _>("content_kind") {
        "text" => MessageContentResponse::Text {
            body_markdown: row
                .get::<Option<String>, _>("body_markdown")
                .unwrap_or_default(),
        },
        "channel_created" => {
            let target = sqlx::query("SELECT id,slug,topic,archived_at FROM channels WHERE id=$1")
                .bind(row.get::<Uuid, _>("action_channel_id"))
                .fetch_one(pool)
                .await
                .map_err(map_sqlx)?;
            let slug: String = target.get("slug");
            MessageContentResponse::ChannelCreated {
                channel: ActionChannelResponse {
                    id: target.get("id"),
                    name: target
                        .get::<Option<String>, _>("topic")
                        .unwrap_or_else(|| slug.clone()),
                    slug,
                    available: target
                        .get::<Option<OffsetDateTime>, _>("archived_at")
                        .is_none(),
                },
            }
        }
        "agent_created" => {
            let target = sqlx::query("SELECT a.member_id,a.lifecycle,m.display_name,m.retired_at FROM agents a JOIN members m ON m.id=a.member_id WHERE a.member_id=$1")
                .bind(row.get::<Uuid,_>("action_agent_member_id"))
                .fetch_one(pool)
                .await
                .map_err(map_sqlx)?;
            MessageContentResponse::AgentCreated {
                agent: ActionAgentResponse {
                    member_id: target.get("member_id"),
                    name: target.get("display_name"),
                    lifecycle: match target.get::<&str, _>("lifecycle") {
                        "suspended" => AgentLifecycle::Suspended,
                        "retired" => AgentLifecycle::Retired,
                        _ => AgentLifecycle::Active,
                    },
                    available: target
                        .get::<Option<OffsetDateTime>, _>("retired_at")
                        .is_none(),
                },
            }
        }
        _ => return Err(ApiError::internal()),
    };
    Ok(MessageResponse {
        id,
        channel_id: row.get("channel_id"),
        thread_id: row.get("thread_id"),
        seq: u64::try_from(row.get::<i64, _>("channel_seq")).map_err(|_| ApiError::internal())?,
        placement: match row.get::<&str, _>("placement") {
            "root" => MessagePlacement::Root,
            "reply" => MessagePlacement::Reply,
            _ => return Err(ApiError::internal()),
        },
        author: MessageAuthor {
            id: author.get("id"),
            kind: match author.get::<&str, _>("kind") {
                "human" => MemberKindCode::Human,
                "agent" => MemberKindCode::Agent,
                _ => return Err(ApiError::internal()),
            },
            display_name: author.get("display_name"),
            handle: author.get("handle"),
        },
        content,
        mentions: Vec::new(),
        attachments: attachment_views,
        reply_count: u64::try_from(replies).map_err(|_| ApiError::internal())?,
        task: None,
        attention_failures,
        created_at: timestamp(row.get("created_at")),
        edited_at: optional_timestamp(row.get("edited_at")),
        deleted_at: optional_timestamp(row.get("deleted_at")),
    })
}

fn attachment_row(row: &sqlx::postgres::PgRow) -> Result<AttachmentResponse, ApiError> {
    Ok(AttachmentResponse {
        id: row.get("id"),
        space_id: row.get("space_id"),
        uploader_member_id: row.get("uploader_member_id"),
        original_name: row.get("name"),
        media_type: row.get("media_type"),
        size: row
            .get::<Option<i64>, _>("length")
            .map(u64::try_from)
            .transpose()
            .map_err(|_| ApiError::internal())?,
        sha256: row.get::<Option<Vec<u8>>, _>("sha256").map(hex::encode),
        status: match row.get::<&str, _>("status") {
            "uploading" => AttachmentStatus::Uploading,
            "ready" => AttachmentStatus::Ready,
            "deleted" => AttachmentStatus::Deleted,
            _ => return Err(ApiError::internal()),
        },
        // 读写路径只在开启上传或下载的响应里出现，见 attachment_response。
        upload_path: None,
        download_path: None,
        created_at: timestamp(row.get("created_at")),
    })
}

/// Task 单资源读取。continuity 需要向在线 Computer 取值，因此只在单资源读取里查询；
/// 列表投影沿用 [`task_projection`]，把 continuity 留在 `unavailable`。
async fn task_detail(state: &RuntimeState, task_id: Uuid) -> Result<TaskResponse, ApiError> {
    let mut task = task_projection(&state.pool, task_id).await?;
    if let Some(agent_id) = task.assignee_agent_member_id {
        task.session_continuity = agent_continuity(
            state,
            agent_id,
            SessionScope::Task(TaskId::from_uuid(task_id)),
        )
        .await;
    }
    Ok(task)
}

/// 向 Agent 所在 Computer 取 continuity 投影。Agent 未分配 Computer、Computer 离线或
/// 超时未回应时返回 `unavailable`。
async fn agent_continuity(
    state: &RuntimeState,
    agent_id: Uuid,
    scope: SessionScope,
) -> SessionContinuityResponse {
    let computer_id =
        sqlx::query_scalar::<_, Option<Uuid>>("SELECT computer_id FROM agents WHERE member_id=$1")
            .bind(agent_id)
            .fetch_optional(&state.pool)
            .await
            .ok()
            .flatten()
            .flatten();
    let Some(computer_id) = computer_id else {
        return continuity_response(QueryResult::Unavailable {
            code: QueryErrorCode::Unreachable,
        });
    };
    let result = state
        .queries
        .ask(
            computer_id,
            ComputerQuery::SessionContinuity(SessionContinuityQuery {
                agent_id: AgentId::from_uuid(agent_id),
                scope,
            }),
        )
        .await;
    continuity_response(result)
}

fn continuity_response(result: QueryResult) -> SessionContinuityResponse {
    match result {
        QueryResult::SessionContinuity(continuity) => SessionContinuityResponse {
            state: match continuity.state {
                SessionContinuityState::Warm => ContinuityStateCode::Warm,
                SessionContinuityState::Cold => ContinuityStateCode::Cold,
                // lost 的 Session 无法 resume，下次执行必须新建 generation。
                SessionContinuityState::Lost => ContinuityStateCode::ResetRequired,
            },
            generation: continuity.generation,
            reason_code: continuity.reason_code,
        },
        QueryResult::Unavailable { code } => SessionContinuityResponse {
            state: ContinuityStateCode::Unavailable,
            generation: None,
            reason_code: Some(query_error_code(code).to_owned()),
        },
        // Computer 回了其他 query 的结果类型，该值不能回答 continuity。
        _ => unavailable_continuity(),
    }
}

fn unavailable_continuity() -> SessionContinuityResponse {
    SessionContinuityResponse {
        state: ContinuityStateCode::Unavailable,
        generation: None,
        reason_code: None,
    }
}

fn query_error_code(code: QueryErrorCode) -> &'static str {
    match code {
        QueryErrorCode::UnknownAgent => "unknown_agent",
        QueryErrorCode::UnknownPath => "unknown_path",
        QueryErrorCode::SessionLost => "session_lost",
        QueryErrorCode::DriverUnavailable => "driver_unavailable",
        QueryErrorCode::Unreachable => "unreachable",
        QueryErrorCode::Internal => "internal",
    }
}

/// Agent capability 响应按协议是任意 JSON，因此 Task 投影在这一层序列化一次。
/// Browser 端点直接返回 [`TaskResponse`]，不经过这里。
fn capability_value(value: &impl serde::Serialize) -> Result<Value, capability::Error> {
    serde_json::to_value(value).map_err(|_| {
        capability_error(
            capability::ErrorCode::Internal,
            "projection could not be encoded",
            false,
        )
    })
}

async fn task_projection(pool: &PgPool, task_id: Uuid) -> Result<TaskResponse, ApiError> {
    let row=sqlx::query("SELECT t.*,creator.display_name AS creator_name,assignee.display_name AS assignee_name FROM tasks t JOIN members creator ON creator.id=t.creator_member_id LEFT JOIN members assignee ON assignee.id=t.assignee_agent_member_id WHERE t.id=$1").bind(task_id).fetch_optional(pool).await.map_err(map_sqlx)?.ok_or_else(ApiError::not_found)?;
    let source =
        thread_reference(pool, row.get("source_thread_id"), ThreadRelation::Source).await?;
    let related_ids = sqlx::query_scalar::<_, Uuid>(
        "SELECT thread_id FROM task_threads WHERE task_id=$1 ORDER BY linked_at",
    )
    .bind(task_id)
    .fetch_all(pool)
    .await
    .map_err(map_sqlx)?;
    let mut related = Vec::with_capacity(related_ids.len());
    for id in related_ids {
        related.push(thread_reference(pool, id, ThreadRelation::Related).await?);
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
    let run_ids = sqlx::query_scalar::<_, Uuid>(
        "SELECT id FROM agent_runs WHERE task_id=$1 ORDER BY created_at DESC,id DESC LIMIT 20",
    )
    .bind(task_id)
    .fetch_all(pool)
    .await
    .map_err(map_sqlx)?;
    let mut runs = Vec::with_capacity(run_ids.len());
    for run_id in run_ids {
        runs.push(run_projection(pool, run_id).await?);
    }
    // 非终态 Run 至多一个，它就是 current_run。它同时留在 recent_runs 里。
    let current_run = runs.iter().find(|run| run.outcome.is_none()).cloned();
    Ok(TaskResponse {
        id: task_id,
        space_id: row.get("space_id"),
        title: row.get("title"),
        status: match row.get::<&str, _>("status") {
            "todo" => TaskStatus::Todo,
            "in_progress" => TaskStatus::InProgress,
            "in_review" => TaskStatus::InReview,
            "done" => TaskStatus::Done,
            "closed" => TaskStatus::Closed,
            _ => return Err(ApiError::internal()),
        },
        creator_member_id: row.get("creator_member_id"),
        creator_name: row.get("creator_name"),
        assignee_agent_member_id: row.get("assignee_agent_member_id"),
        assignee_name: row.get("assignee_name"),
        source_thread: source,
        related_threads: related,
        result_message,
        close_reason_code: match row.get::<Option<&str>, _>("close_reason_code") {
            None => None,
            Some("invalid") => Some(CloseReasonCode::Invalid),
            Some("duplicate") => Some(CloseReasonCode::Duplicate),
            Some("not_needed") => Some(CloseReasonCode::NotNeeded),
            Some("obsolete") => Some(CloseReasonCode::Obsolete),
            Some("other") => Some(CloseReasonCode::Other),
            Some(_) => return Err(ApiError::internal()),
        },
        close_reason_note: row.get("close_reason_note"),
        current_run,
        recent_runs: runs,
        // continuity 需要向在线 Computer 取值，由 task_detail 在单资源读取时补齐。
        session_continuity: unavailable_continuity(),
        runtime_issue_code: None,
        created_at: timestamp(row.get("created_at")),
        updated_at: timestamp(row.get("updated_at")),
        finished_at: optional_timestamp(row.get("finished_at")),
    })
}

async fn run_projection(pool: &PgPool, run_id: Uuid) -> Result<RunResponse, ApiError> {
    let row = sqlx::query(
        "SELECT r.*,m.display_name AS agent_name,\
                CASE WHEN task.id IS NULL OR task.source_thread_id=r.focus_thread_id THEN 'source' ELSE 'related' END AS relation \
         FROM agent_runs r JOIN members m ON m.id=r.agent_id \
         LEFT JOIN tasks task ON task.id=r.task_id WHERE r.id=$1",
    )
    .bind(run_id)
    .fetch_optional(pool)
    .await
    .map_err(map_sqlx)?
    .ok_or_else(ApiError::not_found)?;
    let focus = thread_reference(
        pool,
        row.get("focus_thread_id"),
        thread_relation(row.get("relation"))?,
    )
    .await?;
    Ok(RunResponse {
        id: run_id,
        task_id: row.get("task_id"),
        agent_member_id: row.get("agent_id"),
        agent_name: row.get("agent_name"),
        focus,
        status: match row.get::<&str, _>("status") {
            "queued" => RunStatus::Queued,
            "starting" => RunStatus::Starting,
            "running" => RunStatus::Running,
            "finalizing" => RunStatus::Finalizing,
            "stopping" => RunStatus::Stopping,
            "completed" => RunStatus::Completed,
            "yielded" => RunStatus::Yielded,
            "failed" => RunStatus::Failed,
            "canceled" => RunStatus::Canceled,
            _ => return Err(ApiError::internal()),
        },
        outcome: match row.get::<Option<&str>, _>("outcome_code") {
            None => None,
            Some("completed") => Some(RunOutcome::Completed),
            Some("yielded") => Some(RunOutcome::Yielded),
            Some("failed") => Some(RunOutcome::Failed),
            Some("canceled") => Some(RunOutcome::Canceled),
            Some(_) => return Err(ApiError::internal()),
        },
        continuation_note: row.get("continuation_note"),
        error_code: row.get("error_code"),
        started_at: optional_timestamp(row.get("started_at")),
        finished_at: optional_timestamp(row.get("finished_at")),
    })
}

fn thread_relation(code: &str) -> Result<ThreadRelation, ApiError> {
    match code {
        "source" => Ok(ThreadRelation::Source),
        "related" => Ok(ThreadRelation::Related),
        _ => Err(ApiError::internal()),
    }
}

async fn thread_reference(
    pool: &PgPool,
    thread_id: Uuid,
    relation: ThreadRelation,
) -> Result<ThreadReferenceResponse, ApiError> {
    let row=sqlx::query("SELECT t.id,t.root_message_id,t.channel_id,c.slug,m.channel_seq FROM threads t JOIN channels c ON c.id=t.channel_id JOIN messages m ON m.id=t.root_message_id WHERE t.id=$1").bind(thread_id).fetch_one(pool).await.map_err(map_sqlx)?;
    Ok(ThreadReferenceResponse {
        id: thread_id,
        root_message_id: row.get("root_message_id"),
        channel_id: row.get("channel_id"),
        channel_slug: row.get("slug"),
        root_message_seq: u64::try_from(row.get::<i64, _>("channel_seq"))
            .map_err(|_| ApiError::internal())?,
        relation,
    })
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
        ApplicationError::Unauthenticated => ApiError::unauthenticated(),
        ApplicationError::Domain(crate::server::domain::DomainError::GovernorRequired) => {
            ApiError {
                status: StatusCode::FORBIDDEN,
                code: "permission_denied",
                message: "Space Owner or Admin access is required",
            }
        }
        ApplicationError::Domain(crate::server::domain::DomainError::InvalidCredential) => {
            ApiError::invalid(
                "display name, email, and a password of at least 12 characters are required",
            )
        }
        ApplicationError::Domain(crate::server::domain::DomainError::InvalidPairing) => {
            ApiError::invalid("Computer pairing request is invalid")
        }
        ApplicationError::Domain(crate::server::domain::DomainError::PairingLapsed) => ApiError {
            status: StatusCode::CONFLICT,
            code: "pairing_lapsed",
            message: "Computer pairing expired or was already confirmed",
        },
        // 保持既有 Browser 契约：超限视为无效请求参数，不改用 413。
        ApplicationError::PayloadTooLarge => ApiError::invalid("Attachment is too large"),
        ApplicationError::Domain(crate::server::domain::DomainError::InvalidAttachment) => {
            ApiError::invalid("Attachment name and media type are required")
        }
        ApplicationError::Domain(crate::server::domain::DomainError::AttachmentContentMismatch) => {
            ApiError::invalid("Attachment size or SHA-256 does not match uploaded content")
        }
        ApplicationError::Domain(crate::server::domain::DomainError::AttachmentNotOpen) => {
            ApiError {
                status: StatusCode::CONFLICT,
                code: "conflict",
                message: "Attachment upload is not open",
            }
        }
        // 上传者不符与内容未就绪都不向调用方证明 Attachment 存在。
        ApplicationError::Domain(crate::server::domain::DomainError::AttachmentNotOwned) => {
            ApiError::permission_denied()
        }
        ApplicationError::Domain(crate::server::domain::DomainError::AttachmentNotReady) => {
            ApiError::not_found()
        }
        ApplicationError::PermissionDenied => ApiError {
            status: StatusCode::FORBIDDEN,
            code: "permission_denied",
            message: "actor is not allowed to perform this action",
        },
        ApplicationError::ContextChanged => ApiError::context_changed(),
        ApplicationError::Domain(crate::server::domain::DomainError::ComputerHasAgents) => {
            ApiError {
                status: StatusCode::CONFLICT,
                code: "computer_has_agents",
                message: "Computer still has assigned Agents",
            }
        }
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
async fn list_direct_messages(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
) -> Result<Json<Vec<DirectMessageResponse>>, ApiError> {
    let member = current_member(&state, &jar, space_id).await?;
    let mut storage = state.storage.clone();
    let conversations = ListDirectMessages::execute(
        &mut storage,
        MemberId::from_uuid(member),
        SpaceId::from_uuid(space_id),
    )
    .await
    .map_err(application_error)?;
    Ok(Json(
        conversations.iter().map(direct_message_response).collect(),
    ))
}

async fn open_direct_message(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(space_id): Path<Uuid>,
    Json(body): Json<OpenDirectMessageBody>,
) -> Result<(StatusCode, Json<DirectMessageResponse>), ApiError> {
    let member = current_member(&state, &jar, space_id).await?;
    let mut storage = state.storage.clone();
    let opened = OpenDirectMessage::execute(
        &mut storage,
        OpenDirectMessageInput {
            channel_id: ChannelId::from_uuid(Uuid::now_v7()),
            space_id: SpaceId::from_uuid(space_id),
            actor_member_id: MemberId::from_uuid(member),
            other_member_id: MemberId::from_uuid(body.member_id),
            now: OffsetDateTime::now_utc(),
        },
    )
    .await
    .map_err(application_error)?;
    Ok((
        if opened.created {
            StatusCode::CREATED
        } else {
            StatusCode::OK
        },
        Json(direct_message_response(&opened.view)),
    ))
}

async fn member_inbox(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(member_id): Path<Uuid>,
) -> Result<Json<Vec<InboxItemResponse>>, ApiError> {
    let target = MemberId::from_uuid(member_id);
    let mut storage = state.storage.clone();
    // Member 路径参数先解析回它所属的 Space，再据此判定调用方的授权范围。
    let space_id = storage
        .transact(async |transaction| {
            transaction
                .space_of_member(target)
                .await?
                .ok_or(ApplicationError::NotFound)
        })
        .await
        .map_err(application_error)?;
    let actor = current_member(&state, &jar, space_id.into_uuid()).await?;
    let items =
        ReadMemberInbox::execute(&mut storage, MemberId::from_uuid(actor), target, space_id)
            .await
            .map_err(application_error)?;
    Ok(Json(items.iter().map(inbox_item_response).collect()))
}

fn direct_message_response(conversation: &DirectMessageView) -> DirectMessageResponse {
    DirectMessageResponse {
        channel_id: conversation.channel_id.into_uuid(),
        space_id: conversation.space_id.into_uuid(),
        other_member: space_member_response(&conversation.other_member),
        created_at: timestamp(conversation.created_at),
    }
}

fn space_member_response(member: &SpaceMemberView) -> MemberResponse {
    MemberResponse {
        id: member.id.into_uuid(),
        kind: match member.kind {
            MemberKind::Human => MemberKindCode::Human,
            MemberKind::Agent => MemberKindCode::Agent,
        },
        display_name: member.display_name.clone(),
        handle: member.handle.clone(),
        access_level: access_level_code(member.access_level),
        permissions: member
            .permissions
            .iter()
            .map(|action| action.code().to_owned())
            .collect(),
    }
}

/// Inbox 投影不含 Message 正文，只给出定位来源所需的标识与时间。
fn inbox_item_response(item: &InboxItemView) -> InboxItemResponse {
    InboxItemResponse {
        id: item.id.into_uuid(),
        member_id: item.member_id.into_uuid(),
        kind: inbox_kind_code(item.kind),
        priority: match item.strength {
            AttentionStrength::Hard => InboxPriority::Hard,
            AttentionStrength::Ambient => InboxPriority::Ambient,
        },
        channel_id: item.channel_id.map(ChannelId::into_uuid),
        channel_slug: item.channel_slug.clone(),
        thread_id: item.thread_id.map(ThreadId::into_uuid),
        message_id: item.message_id.map(MessageId::into_uuid),
        sender_member_id: item.sender_member_id.map(MemberId::into_uuid),
        sender_display_name: item.sender_display_name.clone(),
        summary: inbox_summary(item).to_owned(),
        status: inbox_status_code(item.status),
        available_at: timestamp(item.available_at),
        created_at: timestamp(item.created_at),
    }
}

/// 摘要只描述注意力来源的类型，不含 Message 正文。
fn inbox_summary(item: &InboxItemView) -> &'static str {
    match item.kind {
        InboxItemKind::Direct => "Direct message",
        InboxItemKind::Mention => "You were mentioned",
        InboxItemKind::Reply => "Reply to your Message",
        InboxItemKind::TaskActivity => "Linked Thread activity",
        InboxItemKind::ThreadActivity => "Thread activity",
        InboxItemKind::ChannelActivity => "Channel activity",
        InboxItemKind::System => "System notice",
    }
}

fn inbox_kind_code(kind: InboxItemKind) -> InboxKind {
    match kind {
        InboxItemKind::Direct => InboxKind::Direct,
        InboxItemKind::Mention => InboxKind::Mention,
        InboxItemKind::Reply => InboxKind::Reply,
        InboxItemKind::TaskActivity => InboxKind::TaskActivity,
        InboxItemKind::ThreadActivity => InboxKind::ThreadActivity,
        InboxItemKind::ChannelActivity => InboxKind::ChannelActivity,
        InboxItemKind::System => InboxKind::System,
    }
}

fn inbox_status_code(status: InboxItemStatus) -> InboxStatus {
    match status {
        InboxItemStatus::Pending => InboxStatus::Pending,
        InboxItemStatus::Leased => InboxStatus::Leased,
        InboxItemStatus::Deferred => InboxStatus::Deferred,
        InboxItemStatus::Handled => InboxStatus::Handled,
        InboxItemStatus::Dead => InboxStatus::Dead,
    }
}

fn access_level_code(level: AccessLevel) -> AccessLevelCode {
    match level {
        AccessLevel::Owner => AccessLevelCode::Owner,
        AccessLevel::Admin => AccessLevelCode::Admin,
        AccessLevel::Member => AccessLevelCode::Member,
    }
}

async fn update_agent(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(agent_id): Path<Uuid>,
    Json(body): Json<UpdateAgentBody>,
) -> Result<Json<AgentResponse>, ApiError> {
    let actor_id = require_agent_governor(&state, &jar, agent_id).await?;
    let lifecycle = body.lifecycle.as_ref().map(lifecycle_action).transpose()?;
    let mut storage = state.storage.clone();
    UpdateAgent::execute(
        &mut storage,
        UpdateAgentInput {
            actor_id: MemberId::from_uuid(actor_id),
            agent_id: MemberId::from_uuid(agent_id),
            role_text: body.role_text.as_deref(),
            lifecycle,
        },
    )
    .await
    .map_err(application_error)?;
    read_agent_projection(&state, agent_id).await
}

fn lifecycle_action(body: &LifecycleActionBody) -> Result<AgentLifecycleAction, ApiError> {
    match body.action.as_str() {
        "suspend" => Ok(AgentLifecycleAction::Suspend {
            // 未指定 mode 时等待当前 Run 结束，不打断正在进行的工作。
            cancel_current_run: body.mode.as_deref() == Some("cancel_now"),
        }),
        "resume" => Ok(AgentLifecycleAction::Resume),
        "retry" => Ok(AgentLifecycleAction::RetryProvisioning),
        // retire 有独立端点：它不可恢复，不与可逆动作共用入口。
        _ => Err(ApiError::invalid(
            "lifecycle action must be suspend, resume, or retry",
        )),
    }
}

async fn read_agent_projection(
    state: &RuntimeState,
    agent_id: Uuid,
) -> Result<Json<AgentResponse>, ApiError> {
    let row = sqlx::query(&format!(
        "SELECT a.*,m.display_name,m.handle,m.access_level,c.connection_status,\
         c.deleted_at AS computer_deleted_at,{ACTIVITY_COLUMNS} \
         FROM agents a JOIN members m ON m.id=a.member_id \
         LEFT JOIN computers c ON c.id=a.computer_id {ACTIVITY_JOINS} WHERE a.member_id=$1"
    ))
    .bind(agent_id)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    let mut agent = agent_row(&row)?;
    agent.memory_files = memory_files(state, row.get("computer_id"), agent_id).await;
    Ok(Json(agent))
}

/// Memory 文件投影来自在线 Computer。Server 不保存投影,Computer 不可达时返回空列表:
/// 该端点的其他事实仍然可用。
async fn memory_files(
    state: &RuntimeState,
    computer_id: Option<Uuid>,
    agent_id: Uuid,
) -> Vec<MemoryFileResponse> {
    let Some(computer_id) = computer_id else {
        return Vec::new();
    };
    let result = state
        .queries
        .ask(
            computer_id,
            ComputerQuery::MemoryList(MemoryQuery {
                agent_id: AgentId::from_uuid(agent_id),
            }),
        )
        .await;
    match result {
        QueryResult::MemoryList(list) => list
            .files
            .iter()
            .map(|file| MemoryFileResponse {
                path: file.path.clone(),
                size: file.size,
                sha256: file.sha256.clone(),
                updated_at: timestamp(file.updated_at),
            })
            .collect(),
        _ => Vec::new(),
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ReadMemoryBody {
    path: String,
}

/// 读取单个 Memory 文件正文。正文只在响应中经过 Server:不落库、不进日志,
/// 并以 `no-store` 阻止 Browser 缓存。
async fn read_agent_memory(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(agent_id): Path<Uuid>,
    Json(body): Json<ReadMemoryBody>,
) -> Result<Response, ApiError> {
    require_agent_governor(&state, &jar, agent_id).await?;
    let computer_id =
        sqlx::query_scalar::<_, Option<Uuid>>("SELECT computer_id FROM agents WHERE member_id=$1")
            .bind(agent_id)
            .fetch_optional(&state.pool)
            .await
            .map_err(map_sqlx)?
            .flatten()
            .ok_or_else(ApiError::computer_unreachable)?;
    let result = state
        .queries
        .ask(
            computer_id,
            ComputerQuery::MemoryRead(MemoryReadQuery {
                agent_id: AgentId::from_uuid(agent_id),
                path: body.path,
            }),
        )
        .await;
    let QueryResult::MemoryRead(read) = result else {
        return Err(match result {
            QueryResult::Unavailable {
                code: QueryErrorCode::UnknownPath,
            } => ApiError::not_found(),
            _ => ApiError::computer_unreachable(),
        });
    };
    let mut response = Json(MemoryContentResponse {
        path: read.file.path,
        size: read.file.size,
        sha256: read.file.sha256,
        updated_at: timestamp(read.file.updated_at),
        content: read.content,
    })
    .into_response();
    response
        .headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    Ok(response)
}

async fn update_space_member(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path((space_id, member_id)): Path<(Uuid, Uuid)>,
    Json(body): Json<UpdateMemberBody>,
) -> Result<Json<MemberResponse>, ApiError> {
    let actor_id = current_member(&state, &jar, space_id).await?;
    let Some(requested) = body.access_level.as_deref() else {
        return Err(ApiError::invalid("access_level is required"));
    };
    let requested = match requested {
        "admin" => AccessLevel::Admin,
        "member" => AccessLevel::Member,
        // Owner 由创建 Space 确定，不能通过该端点授予。
        _ => return Err(ApiError::invalid("access_level must be admin or member")),
    };
    let mut storage = state.storage.clone();
    UpdateMemberAccess::execute(
        &mut storage,
        MemberId::from_uuid(actor_id),
        MemberId::from_uuid(member_id),
        SpaceId::from_uuid(space_id),
        requested,
    )
    .await
    .map_err(application_error)?;
    let row = sqlx::query(
        "SELECT id,kind,display_name,handle,access_level FROM members WHERE id=$1 AND space_id=$2",
    )
    .bind(member_id)
    .bind(space_id)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok(Json(member_row(&state.pool, &row).await?))
}

async fn join_channel(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(channel_id): Path<Uuid>,
) -> Result<Json<ChannelResponse>, ApiError> {
    let space_id = space_of_channel(&state, channel_id).await?;
    let actor_id = current_member(&state, &jar, space_id).await?;
    let mut storage = state.storage.clone();
    JoinChannel::execute(
        &mut storage,
        MemberId::from_uuid(actor_id),
        ChannelId::from_uuid(channel_id),
    )
    .await
    .map_err(application_error)?;
    read_channel_projection(&state, channel_id, actor_id).await
}

async fn archive_channel(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(channel_id): Path<Uuid>,
) -> Result<Json<ChannelResponse>, ApiError> {
    let space_id = space_of_channel(&state, channel_id).await?;
    let actor_id = current_member(&state, &jar, space_id).await?;
    let mut storage = state.storage.clone();
    ArchiveChannel::execute(
        &mut storage,
        MemberId::from_uuid(actor_id),
        ChannelId::from_uuid(channel_id),
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    read_channel_projection(&state, channel_id, actor_id).await
}

async fn space_of_channel(state: &RuntimeState, channel_id: Uuid) -> Result<Uuid, ApiError> {
    sqlx::query_scalar::<_, Uuid>("SELECT space_id FROM channels WHERE id=$1")
        .bind(channel_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)
}

async fn read_channel_projection(
    state: &RuntimeState,
    channel_id: Uuid,
    viewer: Uuid,
) -> Result<Json<ChannelResponse>, ApiError> {
    let row = sqlx::query(
        "SELECT c.id,c.space_id,c.kind,c.slug,c.topic,c.archived_at,\
         EXISTS(SELECT 1 FROM channel_members cm WHERE cm.channel_id=c.id AND cm.member_id=$2) \
         AS joined FROM channels c WHERE c.id=$1",
    )
    .bind(channel_id)
    .bind(viewer)
    .fetch_one(&state.pool)
    .await
    .map_err(map_sqlx)?;
    Ok(Json(channel_row(&row, viewer)?))
}

async fn follow_thread(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(thread_id): Path<Uuid>,
) -> Result<Json<ThreadSubscriptionResponse>, ApiError> {
    set_subscription(state, jar, thread_id, true).await
}

async fn unfollow_thread(
    State(state): State<RuntimeState>,
    jar: CookieJar,
    Path(thread_id): Path<Uuid>,
) -> Result<Json<ThreadSubscriptionResponse>, ApiError> {
    set_subscription(state, jar, thread_id, false).await
}

async fn set_subscription(
    state: RuntimeState,
    jar: CookieJar,
    thread_id: Uuid,
    following: bool,
) -> Result<Json<ThreadSubscriptionResponse>, ApiError> {
    let row = sqlx::query("SELECT space_id,channel_id FROM threads WHERE id=$1")
        .bind(thread_id)
        .fetch_optional(&state.pool)
        .await
        .map_err(map_sqlx)?
        .ok_or_else(ApiError::not_found)?;
    let actor_id = current_member(&state, &jar, row.get("space_id")).await?;
    let mut storage = state.storage.clone();
    let is_following = SetThreadSubscription::execute(
        &mut storage,
        MemberId::from_uuid(actor_id),
        ThreadId::from_uuid(thread_id),
        following,
        OffsetDateTime::now_utc(),
    )
    .await
    .map_err(application_error)?;
    Ok(Json(ThreadSubscriptionResponse {
        thread_id,
        channel_id: row.get("channel_id"),
        is_following,
    }))
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
    use crate::ids::AgentId;

    struct CapabilityFixture {
        state: RuntimeState,
        admin: PgConnection,
        database_name: String,
        _objects: TempDir,
        headers: HeaderMap,
        computer_id: Uuid,
        owner_id: Uuid,
        channel_id: Uuid,
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
                session_lifetime: SessionLifetime::from_hours(1).unwrap(),
                attachment_max_bytes: 100 * 1024 * 1024,
                queries: QueryRegistry::default(),
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
                owner_id,
                channel_id,
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
    async fn agent_activity_and_last_error_code_come_from_run_and_inbox_facts() {
        let fixture = CapabilityFixture::create().await;
        let agent_id = fixture.context.agent_id.into_uuid();
        let focus_id = fixture.context.focus_thread_id.into_uuid();

        async fn read_agent(fixture: &CapabilityFixture, agent_id: Uuid) -> AgentResponse {
            let row = sqlx::query(&format!(
                "SELECT a.*,m.display_name,m.handle,m.access_level,c.connection_status,\
                 c.deleted_at AS computer_deleted_at,{ACTIVITY_COLUMNS} \
                 FROM agents a JOIN members m ON m.id=a.member_id \
                 LEFT JOIN computers c ON c.id=a.computer_id {ACTIVITY_JOINS} \
                 WHERE a.member_id=$1"
            ))
            .bind(agent_id)
            .fetch_one(&fixture.state.pool)
            .await
            .unwrap();
            agent_row(&row).unwrap()
        }

        // 没有失败事实时 last_error_code 为空，不再由 lifecycle 猜测。
        let initial = read_agent(&fixture, agent_id).await;
        assert_eq!(initial.last_error_code, None);

        // 活跃 Run 的 status 与 Focus 地址进入 activity。fixture 已建立该 Run。
        let run_id = fixture.context.run_id.into_uuid();
        let running = read_agent(&fixture, agent_id).await;
        let activity = running.activity.as_ref().unwrap();
        assert_eq!(activity.kind, "running");
        // Focus 地址定位 Channel 与 Root Message 序号，不含 Message 正文。
        assert!(activity.label.contains("#general:1"), "{}", activity.label);
        assert!(matches!(
            running.activity_status,
            AgentActivityStatus::Running
        ));

        // pending Item 上的领取错误在没有失败 Run 时作为 last_error_code。
        let item_id = Uuid::now_v7();
        sqlx::query(
            "INSERT INTO inbox_items(id,space_id,agent_id,message_id,thread_id,kind,strength,\
             status,available_at,last_error_code,created_at) \
             VALUES($1,$2,$3,$4,$4,'mention','hard','pending',now(),'run_claim_unavailable',now())",
        )
        .bind(item_id)
        .bind(fixture.context.space_id.into_uuid())
        .bind(agent_id)
        .bind(focus_id)
        .execute(&fixture.state.pool)
        .await
        .unwrap();
        let with_item_error = read_agent(&fixture, agent_id).await;
        assert_eq!(
            with_item_error.last_error_code.as_deref(),
            Some("run_claim_unavailable")
        );

        // 失败 Run 的错误码优先于 Item 上的领取错误。
        sqlx::query(
            "UPDATE agent_runs SET status='failed',outcome_code='failed',\
             error_code='session_lost',finished_at=now() WHERE id=$1",
        )
        .bind(run_id)
        .execute(&fixture.state.pool)
        .await
        .unwrap();
        let failed = read_agent(&fixture, agent_id).await;
        assert_eq!(failed.last_error_code.as_deref(), Some("session_lost"));
        // 终态 Run 不再是活跃 Run，activity 回到空。
        assert!(failed.activity.is_none());

        fixture.destroy().await;
    }

    #[tokio::test]
    async fn run_claim_failure_is_projected_once_on_its_source_message() {
        let fixture = CapabilityFixture::create().await;
        let item_id = Uuid::now_v7();
        let message_id = fixture.context.focus_thread_id.into_uuid();
        sqlx::query(
            "INSERT INTO inbox_items(id,space_id,agent_id,message_id,thread_id,kind,strength, \
             status,available_at,created_at) VALUES($1,$2,$3,$4,$4,'mention','hard','pending',now(),now())",
        )
        .bind(item_id)
        .bind(fixture.context.space_id.into_uuid())
        .bind(fixture.context.agent_id.into_uuid())
        .bind(message_id)
        .execute(&fixture.state.pool)
        .await
        .unwrap();

        assert!(
            record_run_claim_failure(
                &fixture.state.pool,
                item_id,
                Some(message_id),
                fixture.channel_id,
                "run_claim_unavailable",
            )
            .await
            .unwrap()
        );
        assert!(
            !record_run_claim_failure(
                &fixture.state.pool,
                item_id,
                Some(message_id),
                fixture.channel_id,
                "run_claim_unavailable",
            )
            .await
            .unwrap()
        );

        let row = sqlx::query("SELECT * FROM messages WHERE id=$1")
            .bind(message_id)
            .fetch_one(&fixture.state.pool)
            .await
            .unwrap();
        let projected = message_row(&fixture.state.pool, &row, fixture.owner_id)
            .await
            .unwrap();
        let failures = &projected.attention_failures;
        assert_eq!(failures.len(), 1);
        assert_eq!(
            failures[0].agent_member_id,
            fixture.context.agent_id.into_uuid()
        );
        assert_eq!(failures[0].agent_handle, "agent");
        assert_eq!(failures[0].error_code, "run_claim_unavailable");
        assert!(failures[0].retrying);
        let event_count: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM outbox_events WHERE kind='message.updated' \
             AND payload_json->>'resource_id'=$1",
        )
        .bind(message_id.to_string())
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
        assert_eq!(event_count, 1);
        fixture.destroy().await;
    }

    #[tokio::test]
    async fn message_hard_items_attach_same_focus_and_notice_different_focus() {
        let fixture = CapabilityFixture::create().await;
        let focus_id = fixture.context.focus_thread_id.into_uuid();
        insert_message(
            &fixture.state,
            fixture.channel_id,
            fixture.owner_id,
            MessageWriteContext {
                idempotency_key: crate::ids::IdempotencyKey::from_uuid(Uuid::now_v7()),
                thread_id: Some(focus_id),
                handled_item: None,
                expected_snapshot: None,
            },
            CreateMessageBody {
                body_markdown: "same Focus".into(),
                mentions: vec![fixture.context.agent_id.into_uuid()],
                attachment_ids: Vec::new(),
                reply_to_message_id: None,
            },
        )
        .await
        .unwrap();
        let same_focus: (Uuid, String, Option<Uuid>, i64, i64) = sqlx::query_as(
            "SELECT i.id,i.status,i.lease_run_id,ri.delivery_seq, \
             (SELECT count(*) FROM computer_commands WHERE kind='run.attach_item') \
             FROM inbox_items i JOIN run_items ri ON ri.inbox_item_id=i.id \
             WHERE i.message_id=(SELECT id FROM messages WHERE body_markdown='same Focus')",
        )
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
        assert_eq!(same_focus.1, "leased");
        assert_eq!(same_focus.2, Some(fixture.context.run_id.into_uuid()));
        assert_eq!(same_focus.3, 3);
        assert_eq!(same_focus.4, 1);
        let mut storage = fixture.state.storage.clone();
        RouteHardItem::execute(
            &mut storage,
            RouteHardItemInput {
                item_id: InboxItemId::from_uuid(same_focus.0),
            },
        )
        .await
        .unwrap();
        let attach_commands: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM computer_commands WHERE kind='run.attach_item'",
        )
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
        assert_eq!(attach_commands, 1);

        insert_message(
            &fixture.state,
            fixture.channel_id,
            fixture.owner_id,
            MessageWriteContext {
                idempotency_key: crate::ids::IdempotencyKey::from_uuid(Uuid::now_v7()),
                thread_id: None,
                handled_item: None,
                expected_snapshot: None,
            },
            CreateMessageBody {
                body_markdown: "different Focus".into(),
                mentions: vec![fixture.context.agent_id.into_uuid()],
                attachment_ids: Vec::new(),
                reply_to_message_id: None,
            },
        )
        .await
        .unwrap();
        let different_focus: (String, i64, Value) = sqlx::query_as(
            "SELECT i.status, \
             (SELECT count(*) FROM computer_commands WHERE kind='run.notice'), \
             (SELECT payload_json FROM computer_commands WHERE kind='run.notice' ORDER BY computer_seq DESC LIMIT 1) \
             FROM inbox_items i WHERE i.message_id=(SELECT id FROM messages WHERE body_markdown='different Focus')",
        )
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
        assert_eq!(different_focus.0, "pending");
        assert_eq!(different_focus.1, 1);
        assert!(!different_focus.2.to_string().contains("different Focus"));
        assert!(!different_focus.2.to_string().contains("body_markdown"));
        sqlx::query("UPDATE agent_runs SET status='finalizing' WHERE id=$1")
            .bind(fixture.context.run_id.into_uuid())
            .execute(&fixture.state.pool)
            .await
            .unwrap();
        insert_message(
            &fixture.state,
            fixture.channel_id,
            fixture.owner_id,
            MessageWriteContext {
                idempotency_key: crate::ids::IdempotencyKey::from_uuid(Uuid::now_v7()),
                thread_id: Some(focus_id),
                handled_item: None,
                expected_snapshot: None,
            },
            CreateMessageBody {
                body_markdown: "finalizing race".into(),
                mentions: vec![fixture.context.agent_id.into_uuid()],
                attachment_ids: Vec::new(),
                reply_to_message_id: None,
            },
        )
        .await
        .unwrap();
        let finalizing: (String, i64, i64) = sqlx::query_as(
            "SELECT i.status, \
             (SELECT count(*) FROM computer_commands WHERE kind='run.attach_item'), \
             (SELECT count(*) FROM computer_commands WHERE kind='run.notice') \
             FROM inbox_items i WHERE i.message_id=(SELECT id FROM messages WHERE body_markdown='finalizing race')",
        )
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
        assert_eq!(finalizing, ("pending".into(), 1, 1));
        let events = fixture
            .state
            .storage
            .browser_events(fixture.context.space_id, None)
            .await
            .unwrap()
            .unwrap();
        assert_eq!(
            events
                .iter()
                .filter(|event| event.event_type == "message.created")
                .count(),
            3
        );
        let last_event_id = events.last().unwrap().event_id;
        assert!(
            fixture
                .state
                .storage
                .browser_events(fixture.context.space_id, Some(last_event_id))
                .await
                .unwrap()
                .unwrap()
                .is_empty()
        );
        fixture.destroy().await;
    }

    #[tokio::test]
    async fn agent_retirement_cancels_run_before_computer_deletion_revokes_token() {
        let fixture = CapabilityFixture::create().await;
        let mut storage = fixture.state.storage.clone();
        RetireAgent::execute(
            &mut storage,
            MemberId::from_uuid(fixture.owner_id),
            MemberId::from_uuid(fixture.context.agent_id.into_uuid()),
            crate::ids::IdempotencyKey::from_uuid(Uuid::now_v7()),
            OffsetDateTime::now_utc(),
        )
        .await
        .unwrap();
        let retired: (String, Option<Uuid>, String, i64, i64) = sqlx::query_as(
            "SELECT a.lifecycle,a.computer_id,r.status, \
             (SELECT count(*) FROM members WHERE id=a.member_id AND retired_at IS NOT NULL), \
             (SELECT count(*) FROM computer_commands WHERE kind='agent.retire') \
             FROM agents a JOIN agent_runs r ON r.agent_id=a.member_id WHERE a.member_id=$1",
        )
        .bind(fixture.context.agent_id.into_uuid())
        .fetch_one(&fixture.state.pool)
        .await
        .unwrap();
        assert_eq!(retired, ("retired".into(), None, "canceled".into(), 1, 1));

        DeleteComputer::execute(
            &mut storage,
            MemberId::from_uuid(fixture.owner_id),
            ComputerId::from_uuid(fixture.computer_id),
            crate::ids::IdempotencyKey::from_uuid(Uuid::now_v7()),
            OffsetDateTime::now_utc(),
        )
        .await
        .unwrap();
        let deleted: (Option<String>, Option<OffsetDateTime>) =
            sqlx::query_as("SELECT token_hash,deleted_at FROM computers WHERE id=$1")
                .bind(fixture.computer_id)
                .fetch_one(&fixture.state.pool)
                .await
                .unwrap();
        assert_eq!(deleted.0, None);
        assert!(deleted.1.is_some());
        fixture.destroy().await;
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
        let attachment_id = created.1.0.id;
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
        assert_eq!(replayed.1.0.id, attachment_id);

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
        assert!(matches!(completed_again.0.status, AttachmentStatus::Ready));
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
