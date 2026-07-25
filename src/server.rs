use std::sync::Arc;

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

use crate::{
    cli::ServerArgs,
    config::{self, ServerConfig},
    database,
};

mod agent_registry;
mod api_error;
mod attachment;
mod auth;
mod channel;
mod computer_registry;
mod idempotency;
mod inbox;
mod member;
mod message;
mod rate_limit;
mod realtime;
mod space;
mod storage;
mod thread;

pub(super) struct AppState {
    pub database: PgPool,
    pub secure_cookies: bool,
    pub session_ttl_hours: i64,
    pub auth_rate_limits: rate_limit::AuthRateLimits,
    pub attachment_store: Arc<dyn ObjectStore>,
    pub attachment_max_bytes: u64,
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
        .route("/api/v1/inbox/{item_id}/ack", post(inbox::ack))
        .route("/api/v1/inbox/{item_id}/defer", post(inbox::defer))
        .route(
            "/api/v1/computer-pairings/start",
            post(computer_registry::start),
        )
        .route(
            "/api/v1/computer-pairings/{pairing_id}/result",
            get(computer_registry::result),
        )
        .route(
            "/api/v1/computer-pairings/{pairing_id}",
            get(computer_registry::details),
        )
        .route(
            "/api/v1/computer-pairings/{pairing_id}/confirm",
            post(computer_registry::confirm),
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
            "/api/v1/computers/{computer_id}",
            axum::routing::delete(computer_registry::revoke),
        )
        .route(
            "/api/v1/computers/{computer_id}/connect",
            get(computer_registry::connect),
        )
        .route(
            "/api/v1/computers/{computer_id}/agent-actions",
            post(computer_registry::agent_action),
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
mod tests {
    use std::{net::SocketAddr, str::FromStr};

    use anyhow::{Context, Result, ensure};
    use axum::{
        Router,
        body::{Body, to_bytes},
        extract::ConnectInfo,
        http::{Request, StatusCode, header},
    };
    use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
    use futures_util::{SinkExt, StreamExt};
    use sha2::Digest;
    use sqlx::{Connection, Executor, PgConnection, postgres::PgConnectOptions};
    use tempfile::tempdir;
    use tokio_tungstenite::tungstenite::client::IntoClientRequest;
    use tower::ServiceExt;
    use url::Url;
    use uuid::Uuid;

    use super::{
        agent_registry::AgentResponse,
        attachment::AttachmentResponse,
        auth::{LoginResponse, RegisterResponse},
        channel::{ChannelListResponse, ChannelResponse, DirectMessageResponse},
        computer_registry::{ComputerResponse, PairingResultResponse, PairingStartResponse},
        inbox::InboxItemResponse,
        member::{InvitationResponse, MemberResponse},
        message::{MessagePageResponse, MessageResponse},
        router,
        space::SpaceResponse,
        thread::{ThreadReadResponse, ThreadResponse},
    };
    use crate::{config::ServerConfig, database};

    #[tokio::test]
    async fn register_and_create_space_is_an_idempotent_http_flow() -> Result<()> {
        let test_database = TestDatabase::create().await?;
        let result = run_human_space_flow(&test_database.url).await;
        test_database.drop().await?;
        result
    }

    #[tokio::test]
    async fn computer_pairing_enforces_human_admin_and_never_stores_raw_credentials() -> Result<()>
    {
        let test_database = TestDatabase::create().await?;
        let result = run_computer_pairing_flow(&test_database.url).await;
        test_database.drop().await?;
        result
    }

    async fn run_computer_pairing_flow(database_url: &str) -> Result<()> {
        let pool = database::connect_postgres(database_url).await?;
        let web_dist = tempdir()?;
        std::fs::write(web_dist.path().join("index.html"), "<!doctype html>")?;
        let app = router(
            pool.clone(),
            ServerConfig {
                database_url: database_url.to_owned(),
                web_dist: web_dist.path().to_owned(),
                attachment_dir: web_dist.path().join("attachments"),
                auth_ip_attempts_per_minute: 100,
                auth_email_attempts_per_minute: 100,
                ..ServerConfig::default()
            },
        )?;
        let owner =
            register_human(&app, "Owner", "owner@example.test", "correct-horse-owner").await?;
        let member = register_human(
            &app,
            "Member",
            "member@example.test",
            "correct-horse-member",
        )
        .await?;
        let space_response = app
            .clone()
            .oneshot(json_request(
                "/api/v1/spaces",
                Uuid::now_v7(),
                &serde_json::json!({
                    "name": "Computer Lab",
                    "slug": "computer-lab",
                    "accent": "#64D9E8"
                }),
                Some(&owner.cookie),
            )?)
            .await?;
        ensure!(space_response.status() == StatusCode::CREATED);
        let space: SpaceResponse = decode_json(space_response).await?;
        let member_id = Uuid::now_v7();
        let now = time::OffsetDateTime::now_utc();
        sqlx::query(
            "INSERT INTO members (id, space_id, kind, display_name, handle, avatar_seed, \
             access_level, created_at) VALUES ($1, $2, 'human', 'Member', 'member', $3, 'member', $4)",
        )
        .bind(member_id)
        .bind(space.id)
        .bind(member_id.to_string())
        .bind(now)
        .execute(&pool)
        .await?;
        sqlx::query("INSERT INTO human_members (member_id, space_id, user_id) VALUES ($1, $2, $3)")
            .bind(member_id)
            .bind(space.id)
            .bind(member.user_id)
            .execute(&pool)
            .await?;

        let private_key = p256::SecretKey::random(&mut p256::elliptic_curve::rand_core::OsRng);
        let public_key = p256::elliptic_curve::sec1::ToEncodedPoint::to_encoded_point(
            &private_key.public_key(),
            false,
        );
        let pairing_secret = [17_u8; 32];
        let credential = URL_SAFE_NO_PAD.encode([23_u8; 32]);
        let start = app
            .clone()
            .oneshot(json_request(
                "/api/v1/computer-pairings/start",
                Uuid::now_v7(),
                &serde_json::json!({
                    "pairing_secret_hash": URL_SAFE_NO_PAD.encode(sha2::Sha256::digest(pairing_secret)),
                    "credential_hash": URL_SAFE_NO_PAD.encode(sha2::Sha256::digest(credential.as_bytes())),
                    "public_key": URL_SAFE_NO_PAD.encode(public_key.as_bytes()),
                    "hostname": "computer-test.local",
                    "os": "macos",
                    "daemon_version": "0.1.0"
                }),
                None,
            )?)
            .await?;
        ensure!(start.status() == StatusCode::CREATED);
        let pairing: PairingStartResponse = decode_json(start).await?;

        let member_denied = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/computer-pairings/{}/confirm", pairing.pairing_id),
                Uuid::now_v7(),
                &serde_json::json!({ "space_id": space.id, "name": "Desk", "code": pairing.code.clone() }),
                Some(&member.cookie),
            )?)
            .await?;
        ensure!(member_denied.status() == StatusCode::FORBIDDEN);

        let wrong_code = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/computer-pairings/{}/confirm", pairing.pairing_id),
                Uuid::now_v7(),
                &serde_json::json!({ "space_id": space.id, "name": "Desk", "code": "wrong" }),
                Some(&owner.cookie),
            )?)
            .await?;
        ensure!(wrong_code.status() == StatusCode::FORBIDDEN);

        let confirm_key = Uuid::now_v7();
        let confirmed = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/computer-pairings/{}/confirm", pairing.pairing_id),
                confirm_key,
                &serde_json::json!({ "space_id": space.id, "name": "Desk", "code": pairing.code.clone() }),
                Some(&owner.cookie),
            )?)
            .await?;
        ensure!(confirmed.status() == StatusCode::CREATED);
        let computer: ComputerResponse = decode_json(confirmed).await?;

        for _ in 0..2 {
            let result = app
                .clone()
                .oneshot(
                    Request::builder()
                        .uri(format!(
                            "/api/v1/computer-pairings/{}/result",
                            pairing.pairing_id
                        ))
                        .header(
                            header::AUTHORIZATION,
                            format!("Bearer {}", URL_SAFE_NO_PAD.encode(pairing_secret)),
                        )
                        .body(Body::empty())?,
                )
                .await?;
            ensure!(result.status() == StatusCode::OK);
            match decode_json::<PairingResultResponse>(result).await? {
                PairingResultResponse::Confirmed {
                    computer_id,
                    space_id,
                } => {
                    ensure!(computer_id == computer.id && space_id == space.id);
                }
                PairingResultResponse::Pending => {
                    anyhow::bail!("confirmed pairing returned pending")
                }
            }
        }
        let command_id = Uuid::now_v7();
        sqlx::query(
            "WITH allocated AS ( \
                 UPDATE computers SET next_command_seq = next_command_seq + 1 WHERE id = $2 \
                 RETURNING next_command_seq - 1 AS computer_seq \
             ) INSERT INTO computer_commands \
             (id, computer_id, computer_seq, kind, payload_json, created_at) \
             SELECT $1, $2, computer_seq, 'agent.provision', $3, now() FROM allocated",
        )
        .bind(command_id)
        .bind(computer.id)
        .bind(serde_json::json!({ "agent_id": Uuid::now_v7() }))
        .execute(&pool)
        .await?;
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await?;
        let address = listener.local_addr()?;
        let server_app = app.clone();
        let server_task = tokio::spawn(async move {
            axum::serve(
                listener,
                server_app.into_make_service_with_connect_info::<SocketAddr>(),
            )
            .await
        });
        let mut request = format!("ws://{address}/api/v1/computers/{}/connect", computer.id)
            .into_client_request()?;
        request.headers_mut().insert(
            header::AUTHORIZATION,
            format!("Bearer {credential}").parse()?,
        );
        let (mut socket, _) = tokio_tungstenite::connect_async(request).await?;
        socket
            .send(tokio_tungstenite::tungstenite::Message::Text(
                serde_json::json!({
                    "type": "hello",
                    "last_acked_computer_seq": 0
                })
                .to_string()
                .into(),
            ))
            .await?;
        let welcome = socket.next().await.context("Computer welcome missing")??;
        ensure!(welcome.to_text()?.contains("\"type\":\"welcome\""));
        let command = socket.next().await.context("Computer command missing")??;
        ensure!(command.to_text()?.contains(&command_id.to_string()));
        for frame in [
            serde_json::json!({
                "type": "command_ack", "command_id": command_id, "computer_seq": 1
            }),
            serde_json::json!({
                "type": "command_result", "command_id": command_id, "computer_seq": 1,
                "ok": true, "result": { "ok": true }
            }),
        ] {
            socket
                .send(tokio_tungstenite::tungstenite::Message::Text(
                    frame.to_string().into(),
                ))
                .await?;
        }
        socket
            .send(tokio_tungstenite::tungstenite::Message::Text(
                serde_json::json!({
                    "type": "heartbeat",
                    "daemon_version": "0.1.0",
                    "os": "macos",
                    "cpu_count": 8,
                    "memory_total_bytes": 16_000_000_000_u64,
                    "agents_count": 0,
                    "active_runs": 0
                })
                .to_string()
                .into(),
            ))
            .await?;
        tokio::time::sleep(std::time::Duration::from_millis(50)).await;
        let online_status: String =
            sqlx::query_scalar("SELECT status FROM computers WHERE id = $1")
                .bind(computer.id)
                .fetch_one(&pool)
                .await?;
        ensure!(online_status == "online");
        let command_status: String =
            sqlx::query_scalar("SELECT status FROM computer_commands WHERE id = $1")
                .bind(command_id)
                .fetch_one(&pool)
                .await?;
        ensure!(command_status == "completed");

        let agent_created = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/spaces/{}/agents", space.id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "computer_id": computer.id,
                    "name": "Lin",
                    "handle": "lin",
                    "role_text": "Review implementation boundaries and report concrete risks.",
                    "access_level": "member",
                    "driver_kind": "codex"
                }),
                Some(&owner.cookie),
            )?)
            .await?;
        ensure!(agent_created.status() == StatusCode::CREATED);
        let agent: AgentResponse = decode_json(agent_created).await?;
        ensure!(agent.status == "provisioning" && agent.name == "Lin");
        let provision = tokio::time::timeout(std::time::Duration::from_secs(2), socket.next())
            .await?
            .context("Agent provision command missing")??;
        let provision: serde_json::Value = serde_json::from_str(provision.to_text()?)?;
        let provision_command_id = Uuid::parse_str(
            provision["command_id"]
                .as_str()
                .context("provision command id missing")?,
        )?;
        ensure!(provision["payload"]["agent_id"] == agent.member_id.to_string());
        let provision_seq = provision["computer_seq"]
            .as_i64()
            .context("provision sequence missing")?;
        for frame in [
            serde_json::json!({
                "type": "command_ack", "command_id": provision_command_id,
                "computer_seq": provision_seq
            }),
            serde_json::json!({
                "type": "command_result", "command_id": provision_command_id,
                "computer_seq": provision_seq, "ok": true, "result": {
                    "ok": true,
                    "memory_files": [{
                        "path": "MEMORY.md",
                        "size": 9,
                        "sha256": hex::encode(sha2::Sha256::digest(b"# Memory\n")),
                        "updated_at": "2026-07-25T00:00:00Z"
                    }]
                }
            }),
        ] {
            socket
                .send(tokio_tungstenite::tungstenite::Message::Text(
                    frame.to_string().into(),
                ))
                .await?;
        }
        let provision_applied = tokio::time::timeout(std::time::Duration::from_secs(2), async {
            loop {
                let status: String =
                    sqlx::query_scalar("SELECT status FROM agents WHERE member_id = $1")
                        .bind(agent.member_id)
                        .fetch_one(&pool)
                        .await
                        .expect("Agent status query must succeed");
                if status == "active" {
                    break;
                }
                tokio::time::sleep(std::time::Duration::from_millis(10)).await;
            }
        })
        .await;
        if provision_applied.is_err() {
            let command_state: (String, Option<serde_json::Value>) =
                sqlx::query_as("SELECT status, result_json FROM computer_commands WHERE id = $1")
                    .bind(provision_command_id)
                    .fetch_one(&pool)
                    .await?;
            let agent_state: String =
                sqlx::query_scalar("SELECT status FROM agents WHERE member_id = $1")
                    .bind(agent.member_id)
                    .fetch_one(&pool)
                    .await?;
            anyhow::bail!(
                "Agent provision result was not applied: command={command_state:?}, agent={agent_state}, server_finished={}",
                server_task.is_finished()
            );
        }
        let agent_invariants: (String, String, i64) = sqlx::query_as(
            "SELECT agents.status, members.kind, \
             (SELECT count(*) FROM channel_members cm JOIN channels c ON c.id = cm.channel_id \
              WHERE cm.member_id = agents.member_id AND c.slug = 'general') \
             FROM agents JOIN members ON members.id = agents.member_id WHERE agents.member_id = $1",
        )
        .bind(agent.member_id)
        .fetch_one(&pool)
        .await?;
        ensure!(agent_invariants == ("active".to_owned(), "agent".to_owned(), 1));

        let agent_detail = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!("/api/v1/agents/{}", agent.member_id))
                    .header(header::COOKIE, &owner.cookie)
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(agent_detail.status() == StatusCode::OK);
        let agent_detail: AgentResponse = decode_json(agent_detail).await?;
        ensure!(agent_detail.memory_files.len() == 1);
        ensure!(agent_detail.memory_files[0].path == "MEMORY.md");

        let configured = app
            .clone()
            .oneshot(json_request_with_method(
                "PATCH",
                &format!("/api/v1/agents/{}", agent.member_id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "role_text": "Review boundaries and enforce the current specification.",
                    "attention_config": {
                        "dm_immediate": true,
                        "mention_immediate": true,
                        "ambient_enabled": false,
                        "ambient_debounce_seconds": 8,
                        "ambient_max_wait_seconds": 40,
                        "max_retry_count": 4
                    }
                }),
                Some(&owner.cookie),
            )?)
            .await?;
        ensure!(configured.status() == StatusCode::OK);
        let configured: AgentResponse = decode_json(configured).await?;
        ensure!(configured.role_revision == 2);
        ensure!(!configured.attention_config.ambient_enabled);
        let configure = socket
            .next()
            .await
            .context("Agent configure command missing")??;
        let configure: serde_json::Value = serde_json::from_str(configure.to_text()?)?;
        ensure!(configure["kind"] == "agent.configure");
        ensure!(configure["payload"]["role_revision"] == 2);
        socket
            .send(tokio_tungstenite::tungstenite::Message::Text(
                serde_json::json!({
                    "type": "command_result",
                    "command_id": configure["command_id"],
                    "computer_seq": configure["computer_seq"],
                    "ok": true,
                    "result": { "ok": true, "memory_files": [] }
                })
                .to_string()
                .into(),
            ))
            .await?;

        let suspended = app
            .clone()
            .oneshot(json_request_with_method(
                "PATCH",
                &format!("/api/v1/agents/{}", agent.member_id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "lifecycle": { "action": "suspend", "mode": "cancel_now" }
                }),
                Some(&owner.cookie),
            )?)
            .await?;
        ensure!(suspended.status() == StatusCode::OK);
        let suspended: AgentResponse = decode_json(suspended).await?;
        ensure!(suspended.status == "suspended");
        let suspend = socket
            .next()
            .await
            .context("Agent suspend command missing")??;
        let suspend: serde_json::Value = serde_json::from_str(suspend.to_text()?)?;
        ensure!(suspend["kind"] == "agent.suspend");
        ensure!(suspend["payload"]["mode"] == "cancel_now");
        socket
            .send(tokio_tungstenite::tungstenite::Message::Text(
                serde_json::json!({
                    "type": "command_result",
                    "command_id": suspend["command_id"],
                    "computer_seq": suspend["computer_seq"],
                    "ok": true,
                    "result": { "ok": true, "memory_files": [] }
                })
                .to_string()
                .into(),
            ))
            .await?;

        let resumed = app
            .clone()
            .oneshot(json_request_with_method(
                "PATCH",
                &format!("/api/v1/agents/{}", agent.member_id),
                Uuid::now_v7(),
                &serde_json::json!({ "lifecycle": { "action": "resume" } }),
                Some(&owner.cookie),
            )?)
            .await?;
        ensure!(resumed.status() == StatusCode::OK);
        let resumed: AgentResponse = decode_json(resumed).await?;
        ensure!(resumed.status == "active");
        let resume = socket
            .next()
            .await
            .context("Agent resume command missing")??;
        let resume: serde_json::Value = serde_json::from_str(resume.to_text()?)?;
        ensure!(resume["kind"] == "agent.resume");
        socket
            .send(tokio_tungstenite::tungstenite::Message::Text(
                serde_json::json!({
                    "type": "command_result",
                    "command_id": resume["command_id"],
                    "computer_seq": resume["computer_seq"],
                    "ok": true,
                    "result": { "ok": true, "memory_files": [] }
                })
                .to_string()
                .into(),
            ))
            .await?;
        tokio::time::sleep(std::time::Duration::from_millis(50)).await;
        let lifecycle_state: (String, i64, i64) = sqlx::query_as(
            "SELECT status, role_revision, \
             (SELECT count(*) FROM audit_events WHERE subject_id = agents.member_id \
              AND action IN ('agent.configured', 'agent.suspended', 'agent.resumed')) \
             FROM agents WHERE member_id = $1",
        )
        .bind(agent.member_id)
        .fetch_one(&pool)
        .await?;
        ensure!(lifecycle_state == ("active".to_owned(), 2, 3));

        let dm = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/spaces/{}/dms", space.id),
                Uuid::now_v7(),
                &serde_json::json!({ "member_id": agent.member_id }),
                Some(&owner.cookie),
            )?)
            .await?;
        ensure!(dm.status() == StatusCode::CREATED);
        let dm: DirectMessageResponse = decode_json(dm).await?;
        let human_message = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/messages", dm.channel_id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "body_markdown": "Please review the current boundary.",
                    "mentions": [],
                    "attachment_ids": []
                }),
                Some(&owner.cookie),
            )?)
            .await?;
        ensure!(human_message.status() == StatusCode::CREATED);
        let human_message: MessageResponse = decode_json(human_message).await?;
        let inbox_item_id: Uuid = sqlx::query_scalar(
            "SELECT id FROM inbox_items WHERE member_id = $1 AND message_id = $2 \
             AND kind = 'direct' AND status = 'pending'",
        )
        .bind(agent.member_id)
        .bind(human_message.id)
        .fetch_one(&pool)
        .await?;

        let claim = app
            .clone()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri(format!(
                        "/api/v1/computers/{}/agents/{}/inbox/claim",
                        computer.id, agent.member_id
                    ))
                    .header(header::AUTHORIZATION, format!("Bearer {credential}"))
                    .header(header::CONTENT_TYPE, "application/json")
                    .body(Body::from("{}"))?,
            )
            .await?;
        ensure!(claim.status() == StatusCode::OK);
        let claim: serde_json::Value = decode_json(claim).await?;
        ensure!(claim["claimed"] == true);
        let run_id = Uuid::parse_str(claim["run_id"].as_str().context("claimed run id missing")?)?;
        let run_command = tokio::time::timeout(std::time::Duration::from_secs(2), socket.next())
            .await?
            .context("Agent run command missing")??;
        let run_command: serde_json::Value = serde_json::from_str(run_command.to_text()?)?;
        ensure!(run_command["kind"] == "agent.run");
        ensure!(
            run_command["payload"]["prompt"]
                .as_str()
                .is_some_and(|prompt| {
                    prompt.contains("sumi agent inbox current --json")
                        && prompt.contains("Review boundaries and enforce")
                        && prompt.contains("@owner")
                })
        );
        socket
            .send(tokio_tungstenite::tungstenite::Message::Text(
                serde_json::json!({
                    "type": "command_ack",
                    "command_id": run_command["command_id"],
                    "computer_seq": run_command["computer_seq"]
                })
                .to_string()
                .into(),
            ))
            .await?;
        tokio::time::timeout(std::time::Duration::from_secs(2), async {
            loop {
                let status: String =
                    sqlx::query_scalar("SELECT status FROM agent_runs WHERE id = $1")
                        .bind(run_id)
                        .fetch_one(&pool)
                        .await
                        .expect("Agent run query must succeed");
                if status == "running" {
                    break;
                }
                tokio::time::sleep(std::time::Duration::from_millis(10)).await;
            }
        })
        .await
        .context("Agent run did not enter running")?;

        let inbox_current = computer_agent_action_request(
            computer.id,
            &credential,
            agent.member_id,
            run_id,
            serde_json::json!({ "action": "inbox_current" }),
        )?;
        let inbox_current = app.clone().oneshot(inbox_current).await?;
        ensure!(inbox_current.status() == StatusCode::OK);
        let inbox_current: serde_json::Value = decode_json(inbox_current).await?;
        ensure!(inbox_current["items"][0]["id"] == inbox_item_id.to_string());

        let channel_read = computer_agent_action_request(
            computer.id,
            &credential,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "channel_read",
                "address": "@owner",
                "before": null,
                "limit": 50
            }),
        )?;
        let channel_read = app.clone().oneshot(channel_read).await?;
        ensure!(channel_read.status() == StatusCode::OK);
        let channel_read: serde_json::Value = decode_json(channel_read).await?;
        ensure!(
            channel_read["messages"][0]["body_markdown"] == "Please review the current boundary."
        );
        let snapshot = channel_read["snapshot_channel_seq"]
            .as_i64()
            .context("snapshot sequence missing")?;

        let changed_context = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/messages", dm.channel_id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "body_markdown": "One more detail arrived after your read.",
                    "mentions": [],
                    "attachment_ids": []
                }),
                Some(&owner.cookie),
            )?)
            .await?;
        ensure!(changed_context.status() == StatusCode::CREATED);
        let changed_context: MessageResponse = decode_json(changed_context).await?;
        let stale_send = computer_agent_action_request(
            computer.id,
            &credential,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "message_send",
                "address": "@owner",
                "body_markdown": "This stale response must not be stored.",
                "based_on": snapshot,
                "handle_inbox_item_id": inbox_item_id,
                "idempotency_key": Uuid::now_v7()
            }),
        )?;
        let stale_send = app.clone().oneshot(stale_send).await?;
        ensure!(stale_send.status() == StatusCode::CONFLICT);
        let unchanged: (String, i64) = sqlx::query_as(
            "SELECT status, (SELECT count(*) FROM messages WHERE channel_id = $2 \
             AND author_member_id = $3) FROM inbox_items WHERE id = $1",
        )
        .bind(inbox_item_id)
        .bind(dm.channel_id)
        .bind(agent.member_id)
        .fetch_one(&pool)
        .await?;
        ensure!(unchanged == ("leased".to_owned(), 0));
        let refreshed = app
            .clone()
            .oneshot(computer_agent_action_request(
                computer.id,
                &credential,
                agent.member_id,
                run_id,
                serde_json::json!({
                    "action": "channel_read",
                    "address": "@owner",
                    "before": null,
                    "limit": 50
                }),
            )?)
            .await?;
        ensure!(refreshed.status() == StatusCode::OK);
        let refreshed: serde_json::Value = decode_json(refreshed).await?;
        let refreshed_snapshot = refreshed["snapshot_channel_seq"]
            .as_i64()
            .context("refreshed snapshot sequence missing")?;

        let send = computer_agent_action_request(
            computer.id,
            &credential,
            agent.member_id,
            run_id,
            serde_json::json!({
                "action": "message_send",
                "address": "@owner",
                "body_markdown": "The boundary matches the current specification.",
                "based_on": refreshed_snapshot,
                "handle_inbox_item_id": inbox_item_id,
                "idempotency_key": Uuid::now_v7()
            }),
        )?;
        let send = app.clone().oneshot(send).await?;
        ensure!(send.status() == StatusCode::OK);
        let send: serde_json::Value = decode_json(send).await?;
        ensure!(send["author"]["id"] == agent.member_id.to_string());
        let atomic_result: (String, Uuid, i64) = sqlx::query_as(
            "SELECT status, handled_by_run_id, \
             (SELECT count(*) FROM messages WHERE channel_id = $2 AND author_member_id = $3) \
             FROM inbox_items WHERE id = $1",
        )
        .bind(inbox_item_id)
        .bind(dm.channel_id)
        .bind(agent.member_id)
        .fetch_one(&pool)
        .await?;
        ensure!(atomic_result == ("handled".to_owned(), run_id, 1));

        socket
            .send(tokio_tungstenite::tungstenite::Message::Text(
                serde_json::json!({
                    "type": "command_result",
                    "command_id": run_command["command_id"],
                    "computer_seq": run_command["computer_seq"],
                    "ok": true,
                    "result": { "ok": true, "run_id": run_id, "status": "completed" }
                })
                .to_string()
                .into(),
            ))
            .await?;
        tokio::time::timeout(std::time::Duration::from_secs(2), async {
            loop {
                let status: String =
                    sqlx::query_scalar("SELECT status FROM agent_runs WHERE id = $1")
                        .bind(run_id)
                        .fetch_one(&pool)
                        .await
                        .expect("Agent run query must succeed");
                if status == "completed" {
                    break;
                }
                tokio::time::sleep(std::time::Duration::from_millis(10)).await;
            }
        })
        .await
        .context("Agent run result was not applied")?;
        let pending_after_completed: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM inbox_items WHERE member_id = $1 AND status = 'pending'",
        )
        .bind(agent.member_id)
        .fetch_one(&pool)
        .await?;
        ensure!(pending_after_completed == 1);

        let retry_claim = app
            .clone()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri(format!(
                        "/api/v1/computers/{}/agents/{}/inbox/claim",
                        computer.id, agent.member_id
                    ))
                    .header(header::AUTHORIZATION, format!("Bearer {credential}"))
                    .header(header::CONTENT_TYPE, "application/json")
                    .body(Body::from("{}"))?,
            )
            .await?;
        ensure!(retry_claim.status() == StatusCode::OK);
        let retry_claim: serde_json::Value = decode_json(retry_claim).await?;
        ensure!(retry_claim["claimed"] == true);
        let retry_run_id = Uuid::parse_str(
            retry_claim["run_id"]
                .as_str()
                .context("retry run id missing")?,
        )?;
        let retry_command = socket.next().await.context("Retry run command missing")??;
        let retry_command: serde_json::Value = serde_json::from_str(retry_command.to_text()?)?;
        socket
            .send(tokio_tungstenite::tungstenite::Message::Text(
                serde_json::json!({
                    "type": "command_ack",
                    "command_id": retry_command["command_id"],
                    "computer_seq": retry_command["computer_seq"]
                })
                .to_string()
                .into(),
            ))
            .await?;
        socket
            .send(tokio_tungstenite::tungstenite::Message::Text(
                serde_json::json!({
                    "type": "command_result",
                    "command_id": retry_command["command_id"],
                    "computer_seq": retry_command["computer_seq"],
                    "ok": false,
                    "result": {
                        "ok": false,
                        "run_id": retry_run_id,
                        "status": "failed",
                        "error_code": "driver_failed"
                    }
                })
                .to_string()
                .into(),
            ))
            .await?;
        tokio::time::timeout(std::time::Duration::from_secs(2), async {
            loop {
                let state: (String, i32) = sqlx::query_as(
                    "SELECT status, retry_count FROM inbox_items WHERE member_id = $1 \
                     AND message_id = $2",
                )
                .bind(agent.member_id)
                .bind(changed_context.id)
                .fetch_one(&pool)
                .await
                .expect("Retry Inbox query must succeed");
                if state == ("pending".to_owned(), 1) {
                    break;
                }
                tokio::time::sleep(std::time::Duration::from_millis(10)).await;
            }
        })
        .await
        .context("Failed run did not release its Inbox lease")?;

        let revoked = app
            .clone()
            .oneshot(json_request_with_method(
                "DELETE",
                &format!("/api/v1/computers/{}", computer.id),
                Uuid::now_v7(),
                &serde_json::json!({}),
                Some(&owner.cookie),
            )?)
            .await?;
        ensure!(revoked.status() == StatusCode::OK);
        let mut revoked_request =
            format!("ws://{address}/api/v1/computers/{}/connect", computer.id)
                .into_client_request()?;
        revoked_request.headers_mut().insert(
            header::AUTHORIZATION,
            format!("Bearer {credential}").parse()?,
        );
        let reconnect = tokio_tungstenite::connect_async(revoked_request).await;
        ensure!(matches!(
            reconnect,
            Err(tokio_tungstenite::tungstenite::Error::Http(response))
                if response.status() == StatusCode::UNAUTHORIZED
        ));
        let status_event_count: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM outbox_events WHERE topic = 'computer.status_changed' \
             AND aggregate_id = $1",
        )
        .bind(computer.id)
        .fetch_one(&pool)
        .await?;
        ensure!(status_event_count == 2);
        let _ = socket.close(None).await;
        server_task.abort();
        let _ = server_task.await;
        let stored_hash: Vec<u8> =
            sqlx::query_scalar("SELECT credential_hash FROM computers WHERE id = $1")
                .bind(computer.id)
                .fetch_one(&pool)
                .await?;
        ensure!(stored_hash == sha2::Sha256::digest(credential.as_bytes()).as_slice());
        ensure!(stored_hash.as_slice() != credential.as_bytes());

        let expired_secret = [31_u8; 32];
        let expired_start = app
            .clone()
            .oneshot(json_request(
                "/api/v1/computer-pairings/start",
                Uuid::now_v7(),
                &serde_json::json!({
                    "pairing_secret_hash": URL_SAFE_NO_PAD.encode(sha2::Sha256::digest(expired_secret)),
                    "credential_hash": URL_SAFE_NO_PAD.encode([1_u8; 32]),
                    "public_key": URL_SAFE_NO_PAD.encode(public_key.as_bytes()),
                    "hostname": "expired.local", "os": "linux", "daemon_version": "0.1.0"
                }),
                None,
            )?)
            .await?;
        let expired: PairingStartResponse = decode_json(expired_start).await?;
        sqlx::query(
            "UPDATE computer_pairings SET expires_at = now() - interval '1 second' WHERE id = $1",
        )
        .bind(expired.pairing_id)
        .execute(&pool)
        .await?;
        let expired_result = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!(
                        "/api/v1/computer-pairings/{}/result",
                        expired.pairing_id
                    ))
                    .header(
                        header::AUTHORIZATION,
                        format!("Bearer {}", URL_SAFE_NO_PAD.encode(expired_secret)),
                    )
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(expired_result.status() == StatusCode::GONE);
        let expired_status: String =
            sqlx::query_scalar("SELECT status FROM computer_pairings WHERE id = $1")
                .bind(expired.pairing_id)
                .fetch_one(&pool)
                .await?;
        ensure!(expired_status == "expired");
        pool.close().await;
        Ok(())
    }

    async fn run_human_space_flow(database_url: &str) -> Result<()> {
        let pool = database::connect_postgres(database_url).await?;
        let web_dist = tempdir()?;
        std::fs::write(
            web_dist.path().join("index.html"),
            "<!doctype html><title>Sumi</title>",
        )?;
        let app = router(
            pool.clone(),
            ServerConfig {
                database_url: database_url.to_owned(),
                web_dist: web_dist.path().to_owned(),
                attachment_dir: web_dist.path().join("attachments"),
                auth_ip_attempts_per_minute: 100,
                auth_email_attempts_per_minute: 100,
                ..ServerConfig::default()
            },
        )?;
        let deep_link = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/s/sumi-lab/members")
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(deep_link.status() == StatusCode::OK);
        let registration_key = Uuid::now_v7();
        let registration_body = serde_json::json!({
            "display_name": "Ada Lovelace",
            "email": "ADA@example.test",
            "password": "correct-horse-battery"
        });

        let registration = app
            .clone()
            .oneshot(json_request(
                "/api/v1/auth/register",
                registration_key,
                &registration_body,
                None,
            )?)
            .await?;
        ensure!(registration.status() == StatusCode::CREATED);
        let mut cookie = response_cookie(&registration)?;
        let registration: RegisterResponse = decode_json(registration).await?;

        let retry = app
            .clone()
            .oneshot(json_request(
                "/api/v1/auth/register",
                registration_key,
                &registration_body,
                None,
            )?)
            .await?;
        ensure!(retry.status() == StatusCode::CREATED);
        let retry: RegisterResponse = decode_json(retry).await?;
        ensure!(retry.user.id == registration.user.id);

        let me = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/api/v1/auth/me")
                    .header(header::COOKIE, &cookie)
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(me.status() == StatusCode::OK);

        let logout = app
            .clone()
            .oneshot(json_request(
                "/api/v1/auth/logout",
                Uuid::now_v7(),
                &serde_json::json!({}),
                Some(&cookie),
            )?)
            .await?;
        ensure!(logout.status() == StatusCode::NO_CONTENT);
        let logged_out = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/api/v1/auth/me")
                    .header(header::COOKIE, &cookie)
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(logged_out.status() == StatusCode::UNAUTHORIZED);

        let login = app
            .clone()
            .oneshot(json_request(
                "/api/v1/auth/login",
                Uuid::now_v7(),
                &serde_json::json!({
                    "email": "ada@example.test",
                    "password": "correct-horse-battery"
                }),
                None,
            )?)
            .await?;
        ensure!(login.status() == StatusCode::OK);
        cookie = response_cookie(&login)?;
        let login: LoginResponse = decode_json(login).await?;
        ensure!(login.user.id == registration.user.id);

        let space_key = Uuid::now_v7();
        let space_body = serde_json::json!({
            "name": "Sumi Lab",
            "slug": "sumi-lab",
            "accent": "#FFD447"
        });
        let created = app
            .clone()
            .oneshot(json_request(
                "/api/v1/spaces",
                space_key,
                &space_body,
                Some(&cookie),
            )?)
            .await?;
        ensure!(created.status() == StatusCode::CREATED);
        let created: SpaceResponse = decode_json(created).await?;

        let retry = app
            .clone()
            .oneshot(json_request(
                "/api/v1/spaces",
                space_key,
                &space_body,
                Some(&cookie),
            )?)
            .await?;
        ensure!(retry.status() == StatusCode::CREATED);
        let retry: SpaceResponse = decode_json(retry).await?;
        ensure!(retry.id == created.id);

        let fetched = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/api/v1/spaces/by-slug/sumi-lab")
                    .header(header::COOKIE, &cookie)
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(fetched.status() == StatusCode::OK);
        let fetched: SpaceResponse = decode_json(fetched).await?;
        ensure!(fetched.id == created.id);

        let invariants: (i64, i64, i64, i64) = sqlx::query_as(
            "SELECT \
               (SELECT count(*) FROM members WHERE space_id = $1 AND kind = 'human' \
                  AND access_level = 'owner'), \
               (SELECT count(*) FROM channels WHERE space_id = $1 AND slug = 'general' \
                  AND kind = 'public'), \
               (SELECT count(*) FROM audit_events WHERE space_id = $1 \
                  AND action = 'space.created'), \
               (SELECT count(*) FROM outbox_events WHERE aggregate_id = $2 \
                  AND topic = 'channel.created')",
        )
        .bind(created.id)
        .bind(created.general_channel_id)
        .fetch_one(&pool)
        .await?;
        ensure!(invariants == (1, 1, 1, 1));
        ensure!(
            sqlx::query_scalar::<_, i64>("SELECT count(*) FROM users")
                .fetch_one(&pool)
                .await?
                == 1
        );

        let grace = register_human(
            &app,
            "Grace Hopper",
            "grace@example.test",
            "compiler-correct-horse",
        )
        .await?;
        let grace_token = URL_SAFE_NO_PAD.encode([7_u8; 32]);
        let grace_invitation = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/spaces/{}/invites", created.id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "email": "GRACE@example.test",
                    "invite_token": grace_token
                }),
                Some(&cookie),
            )?)
            .await?;
        ensure!(grace_invitation.status() == StatusCode::CREATED);
        let grace_invitation: InvitationResponse = decode_json(grace_invitation).await?;
        ensure!(grace_invitation.email == "grace@example.test");

        let preview = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!("/api/v1/invites/{grace_token}"))
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(preview.status() == StatusCode::OK);

        let accept_key = Uuid::now_v7();
        let accepted = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/invites/{grace_token}/accept"),
                accept_key,
                &serde_json::json!({}),
                Some(&grace.cookie),
            )?)
            .await?;
        ensure!(accepted.status() == StatusCode::OK);
        let grace_member: MemberResponse = decode_json(accepted).await?;
        ensure!(grace_member.access_level == "member");

        let accept_retry = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/invites/{grace_token}/accept"),
                accept_key,
                &serde_json::json!({}),
                Some(&grace.cookie),
            )?)
            .await?;
        ensure!(accept_retry.status() == StatusCode::OK);
        let accept_retry: MemberResponse = decode_json(accept_retry).await?;
        ensure!(accept_retry.id == grace_member.id);

        let promote = app
            .clone()
            .oneshot(json_request_with_method(
                "PATCH",
                &format!("/api/v1/spaces/{}/members/{}", created.id, grace_member.id),
                Uuid::now_v7(),
                &serde_json::json!({ "access_level": "admin" }),
                Some(&cookie),
            )?)
            .await?;
        ensure!(promote.status() == StatusCode::OK);

        let alan = register_human(
            &app,
            "Alan Turing",
            "alan@example.test",
            "enigma-correct-horse",
        )
        .await?;
        let alan_token = URL_SAFE_NO_PAD.encode([9_u8; 32]);
        let admin_invite = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/spaces/{}/invites", created.id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "email": "alan@example.test",
                    "invite_token": alan_token
                }),
                Some(&grace.cookie),
            )?)
            .await?;
        ensure!(admin_invite.status() == StatusCode::CREATED);

        let accepted = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/invites/{alan_token}/accept"),
                Uuid::now_v7(),
                &serde_json::json!({}),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(accepted.status() == StatusCode::OK);
        let alan_member: MemberResponse = decode_json(accepted).await?;

        let denied_channel = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/spaces/{}/channels", created.id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "name": "Design",
                    "slug": "design",
                    "kind": "public",
                    "topic": "Product and system design"
                }),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(denied_channel.status() == StatusCode::FORBIDDEN);

        let grant = app
            .clone()
            .oneshot(json_request_with_method(
                "PATCH",
                &format!("/api/v1/spaces/{}/members/{}", created.id, alan_member.id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "permissions": ["channel:create", "agent:create"]
                }),
                Some(&grace.cookie),
            )?)
            .await?;
        ensure!(grant.status() == StatusCode::OK);
        let granted: MemberResponse = decode_json(grant).await?;
        ensure!(granted.permissions == ["agent:create", "channel:create"]);

        let design = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/spaces/{}/channels", created.id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "name": "Design",
                    "slug": "design",
                    "kind": "public",
                    "topic": "Product and system design"
                }),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(design.status() == StatusCode::CREATED);
        let design: ChannelResponse = decode_json(design).await?;
        ensure!(design.joined);

        let private = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/spaces/{}/channels", created.id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "name": "Roadmap",
                    "slug": "roadmap",
                    "kind": "private",
                    "topic": null
                }),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(private.status() == StatusCode::CREATED);
        let private: ChannelResponse = decode_json(private).await?;

        let owner_channels = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!("/api/v1/spaces/{}/channels", created.id))
                    .header(header::COOKIE, &cookie)
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(owner_channels.status() == StatusCode::OK);
        let owner_channels: ChannelListResponse = decode_json(owner_channels).await?;
        ensure!(owner_channels.can_create);
        ensure!(
            owner_channels
                .channels
                .iter()
                .any(|channel| channel.id == design.id && !channel.joined)
        );
        ensure!(
            owner_channels
                .channels
                .iter()
                .all(|channel| channel.id != private.id)
        );

        let join_key = Uuid::now_v7();
        for _ in 0..2 {
            let joined = app
                .clone()
                .oneshot(json_request(
                    &format!("/api/v1/channels/{}/members/me", design.id),
                    join_key,
                    &serde_json::json!({}),
                    Some(&cookie),
                )?)
                .await?;
            ensure!(joined.status() == StatusCode::OK);
            let joined: ChannelResponse = decode_json(joined).await?;
            ensure!(joined.joined);
        }

        let first_message_key = Uuid::now_v7();
        let first_message = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/messages", design.id),
                first_message_key,
                &serde_json::json!({
                    "body_markdown": "Welcome to design.",
                    "mentions": [created.owner_member_id]
                }),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(first_message.status() == StatusCode::CREATED);
        let first_message: MessageResponse = decode_json(first_message).await?;
        ensure!(first_message.seq == 1);

        let message_retry = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/messages", design.id),
                first_message_key,
                &serde_json::json!({
                    "body_markdown": "Welcome to design.",
                    "mentions": [created.owner_member_id]
                }),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(message_retry.status() == StatusCode::CREATED);
        let message_retry: MessageResponse = decode_json(message_retry).await?;
        ensure!(message_retry.id == first_message.id);

        let second_message = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/messages", design.id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "body_markdown": "Second note.",
                    "mentions": []
                }),
                Some(&cookie),
            )?)
            .await?;
        ensure!(second_message.status() == StatusCode::CREATED);
        let second_message: MessageResponse = decode_json(second_message).await?;
        ensure!(second_message.seq == 2);

        let edited_message = app
            .clone()
            .oneshot(json_request_with_method(
                "PATCH",
                &format!("/api/v1/messages/{}", second_message.id),
                Uuid::now_v7(),
                &serde_json::json!({ "body_markdown": "Second note, clarified." }),
                Some(&cookie),
            )?)
            .await?;
        ensure!(edited_message.status() == StatusCode::OK);
        let edited_message: MessageResponse = decode_json(edited_message).await?;
        ensure!(edited_message.edited_at.is_some());

        let non_author_edit = app
            .clone()
            .oneshot(json_request_with_method(
                "PATCH",
                &format!("/api/v1/messages/{}", edited_message.id),
                Uuid::now_v7(),
                &serde_json::json!({ "body_markdown": "Not my Message." }),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(non_author_edit.status() == StatusCode::FORBIDDEN);

        let deleted_message = app
            .clone()
            .oneshot(json_request_with_method(
                "DELETE",
                &format!("/api/v1/messages/{}", edited_message.id),
                Uuid::now_v7(),
                &serde_json::json!({}),
                Some(&cookie),
            )?)
            .await?;
        ensure!(deleted_message.status() == StatusCode::OK);
        let deleted_message: MessageResponse = decode_json(deleted_message).await?;
        ensure!(deleted_message.body_markdown == "Message 已删除");

        let attachment_bytes = b"Attachment content stays exact.";
        let attachment_sha = hex::encode(sha2::Sha256::digest(attachment_bytes));
        let upload = app
            .clone()
            .oneshot(json_request(
                "/api/v1/attachments/uploads",
                Uuid::now_v7(),
                &serde_json::json!({
                    "space_id": created.id,
                    "original_name": "design-note.txt",
                    "media_type": "text/plain"
                }),
                Some(&cookie),
            )?)
            .await?;
        ensure!(upload.status() == StatusCode::CREATED);
        let uploading: AttachmentResponse = decode_json(upload).await?;
        ensure!(uploading.status == "uploading" && uploading.size.is_none());

        let premature_message = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/messages", design.id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "body_markdown": "This must roll back.",
                    "mentions": [],
                    "attachment_ids": [uploading.id]
                }),
                Some(&cookie),
            )?)
            .await?;
        ensure!(premature_message.status() == StatusCode::BAD_REQUEST);

        let content = app
            .clone()
            .oneshot(
                Request::builder()
                    .method("PUT")
                    .uri(format!("/api/v1/attachments/{}/content", uploading.id))
                    .header(header::COOKIE, &cookie)
                    .body(Body::from(attachment_bytes.as_slice()))?,
            )
            .await?;
        ensure!(content.status() == StatusCode::NO_CONTENT);

        let mismatch = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/attachments/{}/complete", uploading.id),
                Uuid::now_v7(),
                &serde_json::json!({ "size": attachment_bytes.len(), "sha256": "00".repeat(32) }),
                Some(&cookie),
            )?)
            .await?;
        ensure!(mismatch.status() == StatusCode::BAD_REQUEST);

        let complete_key = Uuid::now_v7();
        let complete = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/attachments/{}/complete", uploading.id),
                complete_key,
                &serde_json::json!({ "size": attachment_bytes.len(), "sha256": attachment_sha }),
                Some(&cookie),
            )?)
            .await?;
        ensure!(complete.status() == StatusCode::OK);
        let ready: AttachmentResponse = decode_json(complete).await?;
        ensure!(ready.status == "ready" && ready.size == Some(attachment_bytes.len() as i64));
        let complete_retry = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/attachments/{}/complete", uploading.id),
                Uuid::now_v7(),
                &serde_json::json!({ "size": attachment_bytes.len(), "sha256": attachment_sha }),
                Some(&cookie),
            )?)
            .await?;
        ensure!(complete_retry.status() == StatusCode::OK);

        let attachment_message = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/messages", design.id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "body_markdown": "Design note attached.",
                    "mentions": [],
                    "attachment_ids": [ready.id]
                }),
                Some(&cookie),
            )?)
            .await?;
        ensure!(attachment_message.status() == StatusCode::CREATED);
        let attachment_message: MessageResponse = decode_json(attachment_message).await?;
        ensure!(attachment_message.attachments.len() == 1);

        let download = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!("/api/v1/attachments/{}/download", ready.id))
                    .header(header::COOKIE, &cookie)
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(download.status() == StatusCode::OK);
        ensure!(to_bytes(download.into_body(), 1024).await?.as_ref() == attachment_bytes);

        let private_attachment = app
            .clone()
            .oneshot(json_request(
                "/api/v1/attachments/uploads",
                Uuid::now_v7(),
                &serde_json::json!({
                    "space_id": created.id,
                    "original_name": "private.txt",
                    "media_type": "text/plain"
                }),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(private_attachment.status() == StatusCode::CREATED);
        let private_attachment: AttachmentResponse = decode_json(private_attachment).await?;
        let foreign_upload = app
            .clone()
            .oneshot(
                Request::builder()
                    .method("PUT")
                    .uri(format!(
                        "/api/v1/attachments/{}/content",
                        private_attachment.id
                    ))
                    .header(header::COOKIE, &cookie)
                    .body(Body::from("not yours"))?,
            )
            .await?;
        ensure!(foreign_upload.status() == StatusCode::FORBIDDEN);

        let messages = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!("/api/v1/channels/{}/messages?limit=1", design.id))
                    .header(header::COOKIE, &cookie)
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(messages.status() == StatusCode::OK);
        let messages: MessagePageResponse = decode_json(messages).await?;
        ensure!(messages.snapshot_channel_seq == 3);
        ensure!(
            messages.messages.len() == 1
                && messages.messages[0].id == attachment_message.id
                && messages.messages[0].attachments.len() == 1
        );
        ensure!(messages.has_more_before);

        let private_read_denied = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!("/api/v1/channels/{}/messages", private.id))
                    .header(header::COOKIE, &cookie)
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(private_read_denied.status() == StatusCode::FORBIDDEN);

        let thread_key = Uuid::now_v7();
        let thread = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/threads", design.id),
                thread_key,
                &serde_json::json!({ "root_message_id": first_message.id }),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(thread.status() == StatusCode::CREATED);
        let thread: ThreadResponse = decode_json(thread).await?;
        ensure!(thread.thread_id == 1 && thread.root_message_id == first_message.id);

        let thread_retry = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/threads", design.id),
                thread_key,
                &serde_json::json!({ "root_message_id": first_message.id }),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(thread_retry.status() == StatusCode::CREATED);
        let thread_retry: ThreadResponse = decode_json(thread_retry).await?;
        ensure!(thread_retry.thread_id == thread.thread_id);

        let reply = app
            .clone()
            .oneshot(json_request(
                &format!(
                    "/api/v1/channels/{}/threads/{}/messages",
                    design.id, thread.thread_id
                ),
                Uuid::now_v7(),
                &serde_json::json!({
                    "body_markdown": "Thread reply.",
                    "mentions": [alan_member.id],
                    "reply_to_message_id": first_message.id
                }),
                Some(&cookie),
            )?)
            .await?;
        ensure!(reply.status() == StatusCode::CREATED);
        let reply: MessageResponse = decode_json(reply).await?;
        ensure!(reply.seq == 4);

        let thread_read = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!(
                        "/api/v1/channels/{}/threads/{}",
                        design.id, thread.thread_id
                    ))
                    .header(header::COOKIE, &cookie)
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(thread_read.status() == StatusCode::OK);
        let thread_read: ThreadReadResponse = decode_json(thread_read).await?;
        ensure!(thread_read.snapshot_channel_seq == 4);
        ensure!(thread_read.root.id == first_message.id);
        ensure!(thread_read.replies.len() == 1 && thread_read.replies[0].id == reply.id);

        let invalid_nested_root = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/threads", design.id),
                Uuid::now_v7(),
                &serde_json::json!({ "root_message_id": reply.id }),
                Some(&cookie),
            )?)
            .await?;
        ensure!(invalid_nested_root.status() == StatusCode::BAD_REQUEST);

        let dm = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/spaces/{}/dms", created.id),
                Uuid::now_v7(),
                &serde_json::json!({ "member_id": grace_member.id }),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(dm.status() == StatusCode::CREATED);
        let dm: DirectMessageResponse = decode_json(dm).await?;
        ensure!(dm.other_member.id == grace_member.id);

        let reverse_dm = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/spaces/{}/dms", created.id),
                Uuid::now_v7(),
                &serde_json::json!({ "member_id": alan_member.id }),
                Some(&grace.cookie),
            )?)
            .await?;
        ensure!(reverse_dm.status() == StatusCode::OK);
        let reverse_dm: DirectMessageResponse = decode_json(reverse_dm).await?;
        ensure!(reverse_dm.channel_id == dm.channel_id);

        let owner_dms = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!("/api/v1/spaces/{}/dms", created.id))
                    .header(header::COOKIE, &cookie)
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(owner_dms.status() == StatusCode::OK);
        let owner_dms: Vec<DirectMessageResponse> = decode_json(owner_dms).await?;
        ensure!(owner_dms.is_empty());

        let dm_message = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/messages", dm.channel_id),
                Uuid::now_v7(),
                &serde_json::json!({
                    "body_markdown": "@grace-hopper private note.",
                    "mentions": [grace_member.id]
                }),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(dm_message.status() == StatusCode::CREATED);
        let dm_message: MessageResponse = decode_json(dm_message).await?;

        let grace_inbox = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!("/api/v1/members/{}/inbox", grace_member.id))
                    .header(header::COOKIE, &grace.cookie)
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(grace_inbox.status() == StatusCode::OK);
        let grace_inbox: Vec<InboxItemResponse> = decode_json(grace_inbox).await?;
        let direct_item = grace_inbox
            .iter()
            .find(|item| item.message_id == Some(dm_message.id))
            .context("direct Inbox Item missing")?;
        ensure!(
            direct_item.kind == "direct" && direct_item.sender_member_id == Some(alan_member.id)
        );

        let owner_cannot_read_grace_inbox = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!("/api/v1/members/{}/inbox", grace_member.id))
                    .header(header::COOKIE, &cookie)
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(owner_cannot_read_grace_inbox.status() == StatusCode::FORBIDDEN);

        let acked = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/inbox/{}/ack", direct_item.id),
                Uuid::now_v7(),
                &serde_json::json!({}),
                Some(&grace.cookie),
            )?)
            .await?;
        ensure!(acked.status() == StatusCode::OK);
        let acked: InboxItemResponse = decode_json(acked).await?;
        ensure!(acked.status == "handled");

        let event_ids: Vec<Uuid> = sqlx::query_scalar(
            "SELECT id FROM outbox_events WHERE payload_json->>'space_id' = $1::text \
             ORDER BY id DESC LIMIT 2",
        )
        .bind(created.id)
        .fetch_all(&pool)
        .await?;
        ensure!(event_ids.len() == 2);
        let events = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!("/api/v1/spaces/{}/events", created.id))
                    .header(header::COOKIE, &grace.cookie)
                    .header("last-event-id", event_ids[1].to_string())
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(events.status() == StatusCode::OK);
        let first_event = tokio::time::timeout(
            std::time::Duration::from_secs(2),
            events.into_body().into_data_stream().next(),
        )
        .await?
        .context("SSE replay produced no event")??;
        let first_event = String::from_utf8(first_event.to_vec())?;
        ensure!(first_event.contains(&format!("id: {}", event_ids[0])));

        let dm_thread = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/threads", dm.channel_id),
                Uuid::now_v7(),
                &serde_json::json!({ "root_message_id": dm_message.id }),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(dm_thread.status() == StatusCode::CREATED);
        let dm_thread: ThreadResponse = decode_json(dm_thread).await?;

        let dm_reply = app
            .clone()
            .oneshot(json_request(
                &format!(
                    "/api/v1/channels/{}/threads/{}/messages",
                    dm.channel_id, dm_thread.thread_id
                ),
                Uuid::now_v7(),
                &serde_json::json!({
                    "body_markdown": "Reply inside the DM Thread.",
                    "mentions": [alan_member.id],
                    "reply_to_message_id": dm_message.id
                }),
                Some(&grace.cookie),
            )?)
            .await?;
        ensure!(dm_reply.status() == StatusCode::CREATED);
        let dm_reply: MessageResponse = decode_json(dm_reply).await?;
        let dm_reply_inbox_count: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM inbox_items WHERE message_id = $1 \
             AND member_id = $2 AND kind = 'direct' AND priority = 'hard'",
        )
        .bind(dm_reply.id)
        .bind(alan_member.id)
        .fetch_one(&pool)
        .await?;
        ensure!(dm_reply_inbox_count == 1);

        let archive_key = Uuid::now_v7();
        let archived = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/archive", design.id),
                archive_key,
                &serde_json::json!({}),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(archived.status() == StatusCode::OK);
        let archived: ChannelResponse = decode_json(archived).await?;
        ensure!(archived.archived_at.is_some());

        let archive_retry = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/archive", design.id),
                archive_key,
                &serde_json::json!({}),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(archive_retry.status() == StatusCode::OK);

        let archived_read = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!("/api/v1/channels/{}/messages", design.id))
                    .header(header::COOKIE, &cookie)
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(archived_read.status() == StatusCode::OK);

        let archived_write = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/messages", design.id),
                Uuid::now_v7(),
                &serde_json::json!({ "body_markdown": "too late", "mentions": [] }),
                Some(&cookie),
            )?)
            .await?;
        ensure!(archived_write.status() == StatusCode::CONFLICT);

        let general_archive = app
            .clone()
            .oneshot(json_request(
                &format!("/api/v1/channels/{}/archive", created.general_channel_id),
                Uuid::now_v7(),
                &serde_json::json!({}),
                Some(&cookie),
            )?)
            .await?;
        ensure!(general_archive.status() == StatusCode::BAD_REQUEST);

        let admin_cannot_change_owner = app
            .clone()
            .oneshot(json_request_with_method(
                "PATCH",
                &format!(
                    "/api/v1/spaces/{}/members/{}",
                    created.id, created.owner_member_id
                ),
                Uuid::now_v7(),
                &serde_json::json!({ "permissions": ["channel:create"] }),
                Some(&grace.cookie),
            )?)
            .await?;
        ensure!(admin_cannot_change_owner.status() == StatusCode::FORBIDDEN);

        let member_cannot_promote = app
            .clone()
            .oneshot(json_request_with_method(
                "PATCH",
                &format!("/api/v1/spaces/{}/members/{}", created.id, alan_member.id),
                Uuid::now_v7(),
                &serde_json::json!({ "access_level": "admin" }),
                Some(&alan.cookie),
            )?)
            .await?;
        ensure!(member_cannot_promote.status() == StatusCode::FORBIDDEN);

        let members = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(format!("/api/v1/spaces/{}/members", created.id))
                    .header(header::COOKIE, &alan.cookie)
                    .body(Body::empty())?,
            )
            .await?;
        ensure!(members.status() == StatusCode::OK);
        let members: Vec<MemberResponse> = decode_json(members).await?;
        ensure!(members.len() == 3);
        ensure!(
            members
                .iter()
                .filter(|member| member.kind == "human")
                .count()
                == 3
        );

        let invited_invariants: (i64, i64, i64, i64, i64, i64, i64, i64, i64, i64, i64) =
            sqlx::query_as(
                "SELECT \
               (SELECT count(*) FROM human_invitations WHERE space_id = $1 \
                  AND accepted_at IS NOT NULL), \
               (SELECT count(*) FROM channel_members WHERE space_id = $1), \
               (SELECT count(*) FROM member_permissions WHERE member_id = $2), \
               (SELECT count(*) FROM outbox_events WHERE topic = 'member.updated' \
                  AND payload_json->>'space_id' = $1::text), \
               (SELECT count(*) FROM outbox_events WHERE topic IN \
                  ('channel.created', 'channel.joined') \
                  AND payload_json->>'space_id' = $1::text), \
               (SELECT count(*) FROM messages WHERE channel_id = $3), \
               (SELECT count(*) FROM inbox_items WHERE message_id = $4 \
                  AND kind = 'mention' AND priority = 'hard'), \
               (SELECT count(*) FROM threads WHERE channel_id = $3), \
               (SELECT count(*) FROM thread_subscriptions WHERE channel_id = $3 \
                  AND thread_id = 1), \
               (SELECT count(*) FROM channel_members WHERE channel_id = $5), \
               (SELECT count(*) FROM inbox_items WHERE message_id = $6 \
                  AND kind = 'direct' AND priority = 'hard')",
            )
            .bind(created.id)
            .bind(alan_member.id)
            .bind(design.id)
            .bind(first_message.id)
            .bind(dm.channel_id)
            .bind(dm_message.id)
            .fetch_one(&pool)
            .await?;
        ensure!(invited_invariants == (2, 8, 2, 4, 5, 4, 1, 1, 2, 2, 1));

        pool.close().await;
        Ok(())
    }

    fn json_request(
        uri: &str,
        key: Uuid,
        body: &serde_json::Value,
        cookie: Option<&str>,
    ) -> Result<Request<Body>> {
        json_request_with_method("POST", uri, key, body, cookie)
    }

    fn computer_agent_action_request(
        computer_id: Uuid,
        credential: &str,
        agent_member_id: Uuid,
        run_id: Uuid,
        action: serde_json::Value,
    ) -> Result<Request<Body>> {
        Ok(Request::builder()
            .method("POST")
            .uri(format!("/api/v1/computers/{computer_id}/agent-actions"))
            .header(header::AUTHORIZATION, format!("Bearer {credential}"))
            .header(header::CONTENT_TYPE, "application/json")
            .body(Body::from(serde_json::to_vec(&serde_json::json!({
                "agent_member_id": agent_member_id,
                "run_id": run_id,
                "action": action,
            }))?))?)
    }

    fn json_request_with_method(
        method: &str,
        uri: &str,
        key: Uuid,
        body: &serde_json::Value,
        cookie: Option<&str>,
    ) -> Result<Request<Body>> {
        let mut builder = Request::builder()
            .method(method)
            .uri(uri)
            .header(header::CONTENT_TYPE, "application/json")
            .header("idempotency-key", key.to_string());
        if let Some(cookie) = cookie {
            builder = builder.header(header::COOKIE, cookie);
        }
        let mut request = builder.body(Body::from(serde_json::to_vec(body)?))?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:40000".parse::<SocketAddr>()?));
        Ok(request)
    }

    struct RegisteredHuman {
        cookie: String,
        user_id: Uuid,
    }

    async fn register_human(
        app: &Router,
        display_name: &str,
        email: &str,
        password: &str,
    ) -> Result<RegisteredHuman> {
        let response = app
            .clone()
            .oneshot(json_request(
                "/api/v1/auth/register",
                Uuid::now_v7(),
                &serde_json::json!({
                    "display_name": display_name,
                    "email": email,
                    "password": password
                }),
                None,
            )?)
            .await?;
        ensure!(response.status() == StatusCode::CREATED);
        let cookie = response_cookie(&response)?;
        let registration: RegisterResponse = decode_json(response).await?;
        Ok(RegisteredHuman {
            cookie,
            user_id: registration.user.id,
        })
    }

    async fn decode_json<T: serde::de::DeserializeOwned>(
        response: axum::response::Response,
    ) -> Result<T> {
        let body = to_bytes(response.into_body(), 1024 * 1024).await?;
        Ok(serde_json::from_slice(&body)?)
    }

    fn response_cookie(response: &axum::response::Response) -> Result<String> {
        Ok(response
            .headers()
            .get(header::SET_COOKIE)
            .context("response did not set a Session cookie")?
            .to_str()?
            .split(';')
            .next()
            .context("Session cookie is empty")?
            .to_owned())
    }

    struct TestDatabase {
        admin_url: String,
        name: String,
        url: String,
    }

    impl TestDatabase {
        async fn create() -> Result<Self> {
            let admin_url = std::env::var("SUMI_TEST_DATABASE_URL")
                .unwrap_or_else(|_| "postgres://localhost/postgres".to_owned());
            let name = format!("sumi_http_test_{}", Uuid::now_v7().simple());
            let mut admin =
                PgConnection::connect_with(&PgConnectOptions::from_str(&admin_url)?).await?;
            admin
                .execute(format!("CREATE DATABASE \"{name}\"").as_str())
                .await?;
            let mut url = Url::parse(&admin_url)?;
            url.set_path(&format!("/{name}"));
            Ok(Self {
                admin_url,
                name,
                url: url.to_string(),
            })
        }

        async fn drop(self) -> Result<()> {
            let mut admin =
                PgConnection::connect_with(&PgConnectOptions::from_str(&self.admin_url)?).await?;
            admin
                .execute(format!("DROP DATABASE \"{}\" WITH (FORCE)", self.name).as_str())
                .await?;
            Ok(())
        }
    }
}
