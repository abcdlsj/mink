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

/// Invitation 是 Human 加入既有 Space 的唯一途径，因此该流程跨越两个账号、
/// 一次治理动作和一次身份建立，必须在真实进程与真实 PostgreSQL 上验证。
#[tokio::test]
async fn inviting_a_human_grants_membership_only_to_the_named_recipient() -> Result<()> {
    let database = TestDatabase::create("sumi_space_invitation").await?;
    let result = run_invitation_flow(&database).await;
    database.drop().await?;
    result
}

async fn run_invitation_flow(database: &TestDatabase) -> Result<()> {
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

    let invite_key = Uuid::now_v7();
    let invite = || {
        client
            .post(
                server_url
                    .join(&format!("/api/v1/spaces/{space_id}/invites"))
                    .expect("valid Invitation URL"),
            )
            .header("idempotency-key", invite_key.to_string())
            .header(header::COOKIE, &owner)
            .json(&serde_json::json!({"email": "GRACE@EXAMPLE.TEST"}))
    };
    let created = invite().send().await?;
    ensure!(
        created.status() == StatusCode::CREATED,
        "{}",
        server.log_text()
    );
    let created: Value = created.json().await?;
    let token = created["token"]
        .as_str()
        .context("plaintext token")?
        .to_owned();
    ensure!(created["email"] == "grace@example.test");
    ensure!(created["space_slug"] == "sumi-lab");

    // 同一 key 重放返回同一投影，但明文 token 只在首次创建时存在。
    let replayed: Value = invite().send().await?.error_for_status()?.json().await?;
    ensure!(replayed["id"] == created["id"]);
    ensure!(replayed["token"].is_null());

    // 读取不要求认证：受邀 Human 点击链接时可能尚无账号。
    let projection: Value = client
        .get(server_url.join(&format!("/api/v1/invites/{token}"))?)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(projection["space_name"] == "sumi-lab");
    // 投影不含 Space 内容：持有 token 的一方尚未获得授权。
    ensure!(projection.get("members").is_none());
    ensure!(projection.get("channels").is_none());

    // 换一个 key 重复邀请同一 email 被拒绝：一个收件人只能持有一个可用链接。
    let duplicate = client
        .post(server_url.join(&format!("/api/v1/spaces/{space_id}/invites"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner)
        .json(&serde_json::json!({"email": "grace@example.test"}))
        .send()
        .await?;
    ensure!(
        duplicate.status() == StatusCode::CONFLICT,
        "{}",
        server.log_text()
    );
    let duplicate: Value = duplicate.json().await?;
    ensure!(duplicate["error"]["code"] == "invitation_already_pending");

    // 未知 token 与不可用 token 返回同一个错误码，该端点不是探测面。
    let unknown = client
        .get(server_url.join("/api/v1/invites/not-a-real-token")?)
        .send()
        .await?;
    ensure!(unknown.status() == StatusCode::NOT_FOUND);
    let unknown: Value = unknown.json().await?;
    ensure!(unknown["error"]["code"] == "invitation_unavailable");

    // 收件人以外的账号不能接受，即使持有链接。
    let stranger = register(&client, &server_url, "Alan Turing", "alan@example.test").await?;
    let rejected = client
        .post(server_url.join(&format!("/api/v1/invites/{token}/accept"))?)
        .header(header::COOKIE, &stranger)
        .send()
        .await?;
    ensure!(
        rejected.status() == StatusCode::FORBIDDEN,
        "{}",
        server.log_text()
    );
    let rejected: Value = rejected.json().await?;
    ensure!(rejected["error"]["code"] == "invitation_email_mismatch");

    let recipient = register(&client, &server_url, "Grace Hopper", "grace@example.test").await?;
    let accepted = client
        .post(server_url.join(&format!("/api/v1/invites/{token}/accept"))?)
        .header(header::COOKIE, &recipient)
        .send()
        .await?;
    ensure!(
        accepted.status() == StatusCode::CREATED,
        "{}",
        server.log_text()
    );
    let accepted: Value = accepted.json().await?;
    ensure!(accepted["space_id"].as_str() == Some(space_id.as_str()));
    ensure!(accepted["kind"] == "human");
    // 新 Member 的级别固定为 member，提升为 Admin 是独立的治理动作。
    ensure!(accepted["access_level"] == "member");

    // 接受后取得 Space 授权，Member 名单包含两人。
    let members: Value = client
        .get(server_url.join(&format!("/api/v1/spaces/{space_id}/members"))?)
        .header(header::COOKIE, &recipient)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let members = members.as_array().context("Member list")?;
    ensure!(members.len() == 2, "{members:?}");

    // 重试同一次接受返回同一个 Member，不建立第二个。
    let retried: Value = client
        .post(server_url.join(&format!("/api/v1/invites/{token}/accept"))?)
        .header(header::COOKIE, &recipient)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(retried["id"] == accepted["id"]);

    // 非治理成员不能签发 Invitation。
    let forbidden = client
        .post(server_url.join(&format!("/api/v1/spaces/{space_id}/invites"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &recipient)
        .json(&serde_json::json!({"email": "linus@example.test"}))
        .send()
        .await?;
    ensure!(
        forbidden.status() == StatusCode::FORBIDDEN,
        "{}",
        server.log_text()
    );

    server.interrupt().await?;
    Ok(())
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
