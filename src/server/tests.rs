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
use sha2::{Digest, Sha256};
use sqlx::{Connection, Executor, PgConnection, postgres::PgConnectOptions};
use tempfile::tempdir;
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tower::ServiceExt;
use url::Url;
use uuid::Uuid;

use super::{
    agent_registry::{AgentResponse, MemoryContentResponse},
    approval::ApprovalResponse,
    attachment::AttachmentResponse,
    auth::{LoginResponse, RegisterResponse},
    channel::{ChannelListResponse, ChannelResponse, DirectMessageResponse},
    computer_pairing::{ComputerResponse, PairingResultResponse, PairingStartResponse},
    inbox::InboxItemResponse,
    member::{InvitationResponse, MemberResponse},
    message::{MessagePageResponse, MessageResponse},
    router,
    space::SpaceResponse,
    thread::{ThreadReadResponse, ThreadResponse},
};
use crate::{config::ServerConfig, database};

mod collaboration_flow;
mod computer_flow;

#[tokio::test]
async fn collaboration_flow_is_idempotent() -> Result<()> {
    let test_database = TestDatabase::create().await?;
    let result = collaboration_flow::run(&test_database.url).await;
    test_database.drop().await?;
    result
}

#[tokio::test]
async fn computer_flow_enforces_security_boundaries() -> Result<()> {
    let test_database = TestDatabase::create().await?;
    let result = computer_flow::run(&test_database.url).await;
    test_database.drop().await?;
    result
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

fn computer_json_request(
    method: &str,
    uri: &str,
    credential: &str,
    body: &serde_json::Value,
) -> Result<Request<Body>> {
    Ok(Request::builder()
        .method(method)
        .uri(uri)
        .header(header::AUTHORIZATION, format!("Bearer {credential}"))
        .header(header::CONTENT_TYPE, "application/json")
        .body(Body::from(serde_json::to_vec(body)?))?)
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
