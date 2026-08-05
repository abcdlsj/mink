mod support;

use std::net::SocketAddr;

use anyhow::{Context, Result, ensure};
use reqwest::{Client, StatusCode, header};
use serde_json::Value;
use tempfile::tempdir;
use url::Url;
use uuid::Uuid;

use support::{
    TestDatabase, reserve_local_port, spawn_server, wait_for_health, write_server_config,
};

#[tokio::test]
async fn governance_routes_apply_their_authorization_and_state_rules() -> Result<()> {
    let database = TestDatabase::create("sumi_governance_routes").await?;
    let result = run_governance_flow(&database).await;
    database.drop().await?;
    result
}

async fn run_governance_flow(database: &TestDatabase) -> Result<()> {
    let root = tempdir()?;
    let web_dist = root.path().join("web");
    let attachments = root.path().join("attachments");
    std::fs::create_dir(&web_dist)?;
    std::fs::write(
        web_dist.join("index.html"),
        "<!doctype html><title>Sumi</title>",
    )?;

    let bind = SocketAddr::from(([127, 0, 0, 1], reserve_local_port()?));
    let server_url = Url::parse(&format!("http://{bind}"))?;
    let config = root.path().join("server.toml");
    write_server_config(&config, bind, &database.url, &attachments, &web_dist)?;
    let mut server = spawn_server(&config)?;
    wait_for_health(&server_url).await?;

    let client = Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .build()?;
    let owner = register(&client, &server_url, "Ada_Lovelace", "ada@example.test").await?;
    let space = create_space(&client, &server_url, &owner, "sumi-lab").await?;
    let space_id = space["id"].as_str().context("Space ID")?.to_owned();
    let general_id = space["general_channel_id"]
        .as_str()
        .context("general Channel ID")?
        .to_owned();

    let member = invite_and_accept(&client, &server_url, &owner, &space_id).await?;
    let members: Value = client
        .get(server_url.join(&format!("/api/v1/spaces/{space_id}/members"))?)
        .header(header::COOKIE, &owner)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let member_id = members
        .as_array()
        .context("Member list")?
        .iter()
        .find(|entry| entry["access_level"] == "member")
        .and_then(|entry| entry["id"].as_str())
        .context("invited Member ID")?
        .to_owned();

    let promoted: Value = client
        .patch(server_url.join(&format!("/api/v1/spaces/{space_id}/members/{member_id}"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner)
        .json(&serde_json::json!({"access_level": "admin"}))
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(promoted["access_level"] == "admin", "{promoted}");

    let owner_member_id = space["owner_member_id"].as_str().context("owner Member")?;
    let demote_owner = client
        .patch(server_url.join(&format!(
            "/api/v1/spaces/{space_id}/members/{owner_member_id}"
        ))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner)
        .json(&serde_json::json!({"access_level": "member"}))
        .send()
        .await?;
    ensure!(
        demote_owner.status() == StatusCode::FORBIDDEN,
        "{}",
        server.log_text()
    );

    let grant_owner = client
        .patch(server_url.join(&format!("/api/v1/spaces/{space_id}/members/{member_id}"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner)
        .json(&serde_json::json!({"access_level": "owner"}))
        .send()
        .await?;
    ensure!(grant_owner.status() == StatusCode::BAD_REQUEST);

    let invalid_channel = client
        .post(server_url.join(&format!("/api/v1/spaces/{space_id}/channels"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner)
        .json(&serde_json::json!({
            "slug": "产品讨论",
            "topic": "产品讨论",
            "kind": "public",
            "agent_member_ids": []
        }))
        .send()
        .await?;
    ensure!(invalid_channel.status() == StatusCode::BAD_REQUEST);
    let invalid_channel_error: Value = invalid_channel.json().await?;
    ensure!(invalid_channel_error["error"]["code"] == "invalid_argument");
    ensure!(
        invalid_channel_error["error"]["message"]
            .as_str()
            .is_some_and(|message| message.contains("Use topic") && message.contains("Unicode")),
        "{invalid_channel_error}"
    );

    let private: Value = client
        .post(server_url.join(&format!("/api/v1/spaces/{space_id}/channels"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner)
        .json(&serde_json::json!({
            "slug": "private", "topic": "产品讨论", "kind": "private", "agent_member_ids": []
        }))
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(private["slug"] == "private", "{private}");
    ensure!(private["topic"] == "产品讨论", "{private}");
    ensure!(private.get("name").is_none(), "{private}");
    let private_id = private["id"].as_str().context("private Channel ID")?;

    let public: Value = client
        .post(server_url.join(&format!("/api/v1/spaces/{space_id}/channels"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner)
        .json(&serde_json::json!({
            "slug": "lounge", "kind": "public", "agent_member_ids": []
        }))
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let public_id = public["id"].as_str().context("public Channel ID")?;
    client
        .post(server_url.join(&format!("/api/v1/channels/{public_id}/members/me"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &member)
        .send()
        .await?
        .error_for_status()?;
    let public_messages: Value = client
        .get(server_url.join(&format!("/api/v1/channels/{public_id}/messages"))?)
        .header(header::COOKIE, &member)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(
        public_messages["messages"]
            .as_array()
            .is_some_and(|messages| {
                messages.iter().any(|message| {
                    message["content"]["type"] == "system_notice"
                        && message["content"]["body_markdown"] == "Grace_Hopper joined the channel"
                })
            })
    );

    let joined: Value = client
        .post(server_url.join(&format!("/api/v1/channels/{general_id}/members/me"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &member)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(joined["joined"] == true, "{joined}");
    client
        .post(server_url.join(&format!("/api/v1/channels/{general_id}/members/me"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &member)
        .send()
        .await?
        .error_for_status()?;

    let refused = client
        .post(server_url.join(&format!("/api/v1/channels/{private_id}/members/me"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &member)
        .send()
        .await?;
    ensure!(
        refused.status() == StatusCode::CONFLICT,
        "{}",
        server.log_text()
    );

    let message: Value = client
        .post(server_url.join(&format!("/api/v1/channels/{general_id}/messages"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner)
        .json(&serde_json::json!({"body_markdown": "Root Message"}))
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let thread_id = message["thread_id"].as_str().context("Thread ID")?;

    let followed: Value = client
        .put(server_url.join(&format!("/api/v1/threads/{thread_id}/subscription"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(followed["is_following"] == true, "{followed}");
    let thread: Value = client
        .get(server_url.join(&format!("/api/v1/threads/{thread_id}"))?)
        .header(header::COOKIE, &owner)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(thread["is_following"] == true, "{}", thread["is_following"]);

    let unfollowed: Value = client
        .delete(server_url.join(&format!("/api/v1/threads/{thread_id}/subscription"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(unfollowed["is_following"] == false);

    let archived: Value = client
        .post(server_url.join(&format!("/api/v1/channels/{private_id}/archive"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(!archived["archived_at"].is_null(), "{archived}");
    let again = client
        .post(server_url.join(&format!("/api/v1/channels/{private_id}/archive"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner)
        .send()
        .await?;
    ensure!(
        again.status() == StatusCode::CONFLICT,
        "{}",
        server.log_text()
    );

    server.interrupt().await?;
    Ok(())
}

async fn invite_and_accept(
    client: &Client,
    server: &Url,
    owner: &str,
    space_id: &str,
) -> Result<String> {
    let created: Value = client
        .post(server.join(&format!("/api/v1/spaces/{space_id}/invites"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, owner)
        .json(&serde_json::json!({"email": "grace@example.test"}))
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let token = created["token"].as_str().context("plaintext token")?;
    let cookie = register(client, server, "Grace_Hopper", "grace@example.test").await?;
    client
        .post(server.join(&format!("/api/v1/invites/{token}/accept"))?)
        .header(header::COOKIE, &cookie)
        .send()
        .await?
        .error_for_status()?;
    Ok(cookie)
}

async fn register(
    client: &Client,
    server: &Url,
    display_name: &str,
    email: &str,
) -> Result<String> {
    let response = client
        .post(server.join("/api/v1/auth/register")?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .json(&serde_json::json!({
            "display_name": display_name,
            "email": email,
            "password": "correct horse battery staple"
        }))
        .send()
        .await?;
    ensure!(response.status() == StatusCode::CREATED);
    session_cookie(&response)
}

async fn create_space(client: &Client, server: &Url, cookie: &str, slug: &str) -> Result<Value> {
    Ok(client
        .post(server.join("/api/v1/spaces")?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({"name": slug, "slug": slug, "accent": "#F0602F"}))
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?)
}

fn session_cookie(response: &reqwest::Response) -> Result<String> {
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
