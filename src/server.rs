use std::{collections::HashMap, sync::Arc};

use anyhow::{Context, Result};
use axum::{
    Json, Router,
    http::StatusCode,
    routing::{get, post, put},
};
use object_store::ObjectStore;
use serde::Serialize;
use sqlx::PgPool;
use tower_http::{
    services::{ServeDir, ServeFile},
    trace::TraceLayer,
};
use uuid::Uuid;

use crate::{
    cli::ServerArgs,
    config::{self, ServerConfig},
    database,
};

mod agent_gateway;
mod agent_prompt;
mod agent_registry;
mod api_error;
mod approval;
mod attachment;
mod audit;
mod auth;
mod channel;
mod channel_access;
mod computer_auth;
mod computer_pairing;
mod computer_protocol;
mod computer_registry;
mod idempotency;
mod inbox;
mod member;
mod message;
mod outbox;
mod rate_limit;
mod realtime;
mod space;
mod storage;
mod thread;
mod validation;

pub(super) struct AppState {
    pub database: PgPool,
    pub secure_cookies: bool,
    pub session_ttl_hours: i64,
    pub auth_rate_limits: rate_limit::AuthRateLimits,
    pub attachment_store: Arc<dyn ObjectStore>,
    pub attachment_max_bytes: u64,
    pub memory_read_waiters:
        tokio::sync::Mutex<HashMap<Uuid, tokio::sync::oneshot::Sender<(bool, serde_json::Value)>>>,
}

#[derive(Serialize)]
struct HealthResponse {
    status: &'static str,
    database: &'static str,
}

pub async fn run(args: ServerArgs) -> Result<()> {
    let config = config::load(args.config.as_ref())?;
    let database = database::connect_postgres(&config.server.database_url).await?;
    let offline_monitor = tokio::spawn(computer_registry::monitor_offline(database.clone()));
    tokio::fs::create_dir_all(&config.server.attachment_dir)
        .await
        .context("failed to create attachment directory")?;

    let app = router(database, config.server.clone())?;
    let listener = tokio::net::TcpListener::bind(config.server.bind)
        .await
        .context("failed to bind Server listener")?;
    tracing::info!(address = %config.server.bind, "Sumi Server listening");
    let result = axum::serve(
        listener,
        app.into_make_service_with_connect_info::<std::net::SocketAddr>(),
    )
    .with_graceful_shutdown(shutdown_signal())
    .await;
    offline_monitor.abort();
    result.context("Sumi Server stopped unexpectedly")
}

fn router(database: PgPool, config: ServerConfig) -> Result<Router> {
    let index = config.web_dist.join("index.html");
    let rate_limits = rate_limit::AuthRateLimits::new(
        config.auth_ip_attempts_per_minute,
        config.auth_email_attempts_per_minute,
    );
    let attachment_store = storage::build(&config)?;
    Ok(Router::new()
        .route("/api/v1/health", get(health))
        .route("/api/v1/auth/register", post(auth::register))
        .route("/api/v1/auth/login", post(auth::login))
        .route("/api/v1/auth/logout", post(auth::logout))
        .route("/api/v1/auth/me", get(auth::me))
        .route("/api/v1/spaces", get(space::list).post(space::create))
        .route("/api/v1/spaces/by-slug/{space_slug}", get(space::by_slug))
        .route("/api/v1/spaces/{space_id}/members", get(member::list))
        .route(
            "/api/v1/spaces/{space_id}/channels",
            get(channel::list).post(channel::create),
        )
        .route(
            "/api/v1/spaces/{space_id}/dms",
            get(channel::list_direct_messages).post(channel::create_direct_message),
        )
        .route(
            "/api/v1/channels/{channel_id}/members/me",
            post(channel::join),
        )
        .route(
            "/api/v1/channels/{channel_id}/archive",
            post(channel::archive),
        )
        .route(
            "/api/v1/channels/{channel_id}/messages",
            get(message::list).post(message::create),
        )
        .route(
            "/api/v1/messages/{message_id}",
            axum::routing::patch(message::update).delete(message::delete),
        )
        .route(
            "/api/v1/channels/{channel_id}/threads",
            post(thread::create),
        )
        .route(
            "/api/v1/channels/{channel_id}/threads/{thread_id}",
            get(thread::read),
        )
        .route(
            "/api/v1/channels/{channel_id}/threads/{thread_id}/subscription",
            axum::routing::put(thread::follow).delete(thread::unfollow),
        )
        .route(
            "/api/v1/channels/{channel_id}/threads/{thread_id}/messages",
            post(thread::reply),
        )
        .route(
            "/api/v1/attachments/uploads",
            post(attachment::create_upload),
        )
        .route(
            "/api/v1/attachments/{attachment_id}/content",
            put(attachment::upload_content),
        )
        .route(
            "/api/v1/attachments/{attachment_id}/complete",
            post(attachment::complete_upload),
        )
        .route(
            "/api/v1/attachments/{attachment_id}/download",
            get(attachment::download),
        )
        .route("/api/v1/spaces/{space_id}/events", get(realtime::events))
        .route("/api/v1/members/{member_id}/inbox", get(inbox::list))
        .route(
            "/api/v1/spaces/{space_id}/approvals",
            get(approval::list),
        )
        .route(
            "/api/v1/approvals/{approval_id}/approve",
            post(approval::approve),
        )
        .route(
            "/api/v1/approvals/{approval_id}/reject",
            post(approval::reject),
        )
        .route("/api/v1/inbox/{item_id}/ack", post(inbox::ack))
        .route("/api/v1/inbox/{item_id}/defer", post(inbox::defer))
        .route(
            "/api/v1/computer-pairings/start",
            post(computer_pairing::start),
        )
        .route(
            "/api/v1/computer-pairings/{pairing_id}/result",
            get(computer_pairing::result),
        )
        .route(
            "/api/v1/computer-pairings/{pairing_id}",
            get(computer_pairing::details),
        )
        .route(
            "/api/v1/computer-pairings/{pairing_id}/confirm",
            post(computer_pairing::confirm),
        )
        .route(
            "/api/v1/spaces/{space_id}/computers",
            get(computer_registry::list),
        )
        .route(
            "/api/v1/spaces/{space_id}/agents",
            get(agent_registry::list).post(agent_registry::create),
        )
        .route(
            "/api/v1/agents/{agent_id}",
            get(agent_registry::get).patch(agent_registry::update),
        )
        .route(
            "/api/v1/agents/{agent_id}/memory/read",
            post(agent_registry::read_memory),
        )
        .route(
            "/api/v1/computers/{computer_id}",
            axum::routing::delete(computer_registry::revoke),
        )
        .route(
            "/api/v1/computers/{computer_id}/connect",
            get(computer_registry::connect),
        )
        .route(
            "/api/v1/computers/{computer_id}/agent-actions",
            post(agent_gateway::agent_action),
        )
        .route(
            "/api/v1/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/uploads",
            post(attachment::agent_create_upload),
        )
        .route(
            "/api/v1/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/{attachment_id}/content",
            put(attachment::agent_upload_content),
        )
        .route(
            "/api/v1/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/{attachment_id}/complete",
            post(attachment::agent_complete_upload),
        )
        .route(
            "/api/v1/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/{attachment_id}",
            get(attachment::agent_info),
        )
        .route(
            "/api/v1/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/{attachment_id}/download",
            get(attachment::agent_download),
        )
        .route(
            "/api/v1/computers/{computer_id}/agents",
            get(computer_registry::list_hosted_agents),
        )
        .route(
            "/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/claim",
            post(computer_registry::claim_agent_inbox),
        )
        .route(
            "/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/renew",
            post(computer_registry::renew_agent_inbox),
        )
        .route(
            "/api/v1/computers/{computer_id}/agents/{agent_id}/inbox/release",
            post(computer_registry::release_agent_inbox),
        )
        .route(
            "/api/v1/spaces/{space_id}/members/{member_id}",
            axum::routing::patch(member::update),
        )
        .route(
            "/api/v1/spaces/{space_id}/invites",
            post(member::create_invitation),
        )
        .route("/api/v1/invites/{invite_token}", get(member::invitation))
        .route(
            "/api/v1/invites/{invite_token}/accept",
            post(member::accept_invitation),
        )
        .fallback_service(ServeDir::new(config.web_dist).fallback(ServeFile::new(index)))
        .with_state(Arc::new(AppState {
            database,
            secure_cookies: config.secure_cookies,
            session_ttl_hours: config.session_ttl_hours,
            auth_rate_limits: rate_limits,
            attachment_store,
            attachment_max_bytes: config.attachment_max_bytes,
            memory_read_waiters: tokio::sync::Mutex::new(HashMap::new()),
        }))
        .layer(TraceLayer::new_for_http()))
}

async fn health(
    axum::extract::State(state): axum::extract::State<Arc<AppState>>,
) -> (StatusCode, Json<HealthResponse>) {
    match sqlx::query_scalar::<_, i32>("SELECT 1")
        .fetch_one(&state.database)
        .await
    {
        Ok(_) => (
            StatusCode::OK,
            Json(HealthResponse {
                status: "ok",
                database: "ok",
            }),
        ),
        Err(error) => {
            tracing::error!(error = %error, "PostgreSQL health check failed");
            (
                StatusCode::SERVICE_UNAVAILABLE,
                Json(HealthResponse {
                    status: "degraded",
                    database: "unavailable",
                }),
            )
        }
    }
}

async fn shutdown_signal() {
    if let Err(error) = tokio::signal::ctrl_c().await {
        tracing::error!(error = %error, "failed to install shutdown signal handler");
    }
}

#[cfg(test)]
mod tests;
