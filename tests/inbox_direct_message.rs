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

/// Inbox 与 DM 之前是返回空数组的假实现。该测试证明两者投影真实事实，
/// 并且 Inbox 的授权边界拒绝跨 Space 与跨 Human 读取。
#[tokio::test]
async fn inbox_projects_routed_attention_and_refuses_foreign_readers() -> Result<()> {
    let database = TestDatabase::create("sumi_inbox_dm").await?;
    let result = run_inbox_flow(&database).await;
    database.drop().await?;
    result
}

async fn run_inbox_flow(database: &TestDatabase) -> Result<()> {
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
    let owner = register(&client, &server_url, "Ada Lovelace", "ada@example.test").await?;
    let space = create_space(&client, &server_url, &owner, "sumi-lab").await?;
    let space_id = space["id"].as_str().context("Space ID")?.to_owned();
    let owner_member_id = space["owner_member_id"]
        .as_str()
        .context("owner Member ID")?
        .to_owned();

    // 邀请第二个 Human，用于验证 DM 的两方 audience 和 Inbox 的跨 Human 拒绝。
    let recipient_cookie = invite_and_accept(
        &client,
        &server_url,
        &owner,
        &space_id,
        "grace@example.test",
    )
    .await?;
    let members: Value = client
        .get(server_url.join(&format!("/api/v1/spaces/{space_id}/members"))?)
        .header(header::COOKIE, &owner)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let recipient_member_id = members
        .as_array()
        .context("Member list")?
        .iter()
        .find(|member| {
            member["handle"] != space["owner_member_id"]
                && member["id"] != Value::String(owner_member_id.clone())
        })
        .and_then(|member| member["id"].as_str())
        .context("recipient Member ID")?
        .to_owned();

    // DM 列表初始为空，打开后返回同一个 Channel。
    let empty: Value = client
        .get(server_url.join(&format!("/api/v1/spaces/{space_id}/dms"))?)
        .header(header::COOKIE, &owner)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(empty.as_array().context("DM list")?.is_empty());

    let open = || {
        client
            .post(
                server_url
                    .join(&format!("/api/v1/spaces/{space_id}/dms"))
                    .expect("valid DM URL"),
            )
            .header("idempotency-key", Uuid::now_v7().to_string())
            .header(header::COOKIE, &owner)
            .json(&serde_json::json!({"member_id": recipient_member_id}))
    };
    let opened = open().send().await?;
    ensure!(
        opened.status() == StatusCode::CREATED,
        "{}",
        server.log_text()
    );
    let opened: Value = opened.json().await?;
    ensure!(opened["other_member"]["id"].as_str() == Some(recipient_member_id.as_str()));

    // 再次打开返回既有 DM 而不是第二个 Channel。
    let reopened = open().send().await?;
    ensure!(reopened.status() == StatusCode::OK, "{}", server.log_text());
    let reopened: Value = reopened.json().await?;
    ensure!(reopened["channel_id"] == opened["channel_id"]);

    // 双方都能看到这个 DM。
    for cookie in [&owner, &recipient_cookie] {
        let conversations: Value = client
            .get(server_url.join(&format!("/api/v1/spaces/{space_id}/dms"))?)
            .header(header::COOKIE, cookie)
            .send()
            .await?
            .error_for_status()?
            .json()
            .await?;
        let conversations = conversations.as_array().context("DM list")?;
        ensure!(conversations.len() == 1, "{conversations:?}");
        ensure!(conversations[0]["channel_id"] == opened["channel_id"]);
    }

    // 与自己开 DM 不成立。
    let self_dm = client
        .post(server_url.join(&format!("/api/v1/spaces/{space_id}/dms"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner)
        .json(&serde_json::json!({"member_id": owner_member_id}))
        .send()
        .await?;
    ensure!(
        self_dm.status() == StatusCode::CONFLICT,
        "{}",
        server.log_text()
    );

    // Inbox 只有 Agent 会收到 Item，Human 自己的队列为空但可读。
    let own_inbox: Value = client
        .get(server_url.join(&format!("/api/v1/members/{owner_member_id}/inbox"))?)
        .header(header::COOKIE, &owner)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(own_inbox.as_array().context("Inbox")?.is_empty());

    // 治理身份不足以读取另一个 Human 的 Inbox。
    let foreign = client
        .get(server_url.join(&format!("/api/v1/members/{recipient_member_id}/inbox"))?)
        .header(header::COOKIE, &owner)
        .send()
        .await?;
    ensure!(
        foreign.status() == StatusCode::FORBIDDEN,
        "{}",
        server.log_text()
    );

    // 另一个 Space 的 Member 不区分「不存在」和「无权访问」。
    let outsider = register(&client, &server_url, "Alan Turing", "alan@example.test").await?;
    let other_space = create_space(&client, &server_url, &outsider, "other-lab").await?;
    let other_member_id = other_space["owner_member_id"]
        .as_str()
        .context("other Space owner")?;
    let cross_space = client
        .get(server_url.join(&format!("/api/v1/members/{other_member_id}/inbox"))?)
        .header(header::COOKIE, &owner)
        .send()
        .await?;
    ensure!(
        cross_space.status() == StatusCode::NOT_FOUND,
        "{}",
        server.log_text()
    );
    // 反向同样被拒：另一个 Space 的 Human 读不到本 Space 的 Member。
    let reverse = client
        .get(server_url.join(&format!("/api/v1/members/{owner_member_id}/inbox"))?)
        .header(header::COOKIE, &outsider)
        .send()
        .await?;
    ensure!(
        reverse.status() == StatusCode::NOT_FOUND,
        "{}",
        server.log_text()
    );

    // 未认证的调用不能读取任何 Inbox。
    let anonymous = client
        .get(server_url.join(&format!("/api/v1/members/{owner_member_id}/inbox"))?)
        .send()
        .await?;
    ensure!(anonymous.status() == StatusCode::UNAUTHORIZED);

    server.interrupt().await?;
    Ok(())
}

async fn invite_and_accept(
    client: &Client,
    server: &Url,
    owner: &str,
    space_id: &str,
    email: &str,
) -> Result<String> {
    let created: Value = client
        .post(server.join(&format!("/api/v1/spaces/{space_id}/invites"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, owner)
        .json(&serde_json::json!({"email": email}))
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let token = created["token"].as_str().context("plaintext token")?;
    let cookie = register(client, server, "Grace Hopper", email).await?;
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
        .json(&serde_json::json!({"name": slug, "slug": slug, "accent": "#FE7DA8"}))
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
