mod support;

use std::net::SocketAddr;

use anyhow::{Context, Result, ensure};
use reqwest::{Client, StatusCode, header};
use serde_json::Value;
use sqlx::PgPool;
use tempfile::tempdir;
use url::Url;
use uuid::Uuid;

use support::{
    TestDatabase, reserve_local_port, spawn_server, wait_for_health, write_server_config,
};

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
    let owner = register(&client, &server_url, "Ada_Lovelace", "ada@example.test").await?;
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

    let replayed: Value = invite().send().await?.error_for_status()?.json().await?;
    ensure!(replayed["id"] == created["id"]);
    ensure!(replayed["token"].is_null());

    let projection: Value = client
        .get(server_url.join(&format!("/api/v1/invites/{token}"))?)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(projection["space_name"] == "sumi-lab");
    ensure!(projection.get("members").is_none());
    ensure!(projection.get("channels").is_none());

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

    let unknown = client
        .get(server_url.join("/api/v1/invites/not-a-real-token")?)
        .send()
        .await?;
    ensure!(unknown.status() == StatusCode::NOT_FOUND);
    let unknown: Value = unknown.json().await?;
    ensure!(unknown["error"]["code"] == "invitation_unavailable");

    let stranger = register(&client, &server_url, "Alan_Turing", "alan@example.test").await?;
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

    let recipient = register(&client, &server_url, "Grace_Hopper", "grace@example.test").await?;
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
    ensure!(accepted["kind"] == "human");
    ensure!(
        accepted["permissions"]
            .as_array()
            .context("permissions")?
            .is_empty()
    );
    ensure!(accepted["access_level"] == "member");

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

    let general_channel_id: Uuid = space["general_channel_id"]
        .as_str()
        .context("general Channel ID")?
        .parse()?;
    let pool = PgPool::connect(&database.url).await?;
    let notice: (String, String, Uuid) = sqlx::query_as(
        "SELECT content_kind,body_markdown,author_member_id \
         FROM messages WHERE channel_id=$1 ORDER BY channel_seq DESC LIMIT 1",
    )
    .bind(general_channel_id)
    .fetch_one(&pool)
    .await?;
    ensure!(
        notice.0 == "system_notice"
            && notice.1 == "Grace_Hopper joined the channel"
            && notice.2
                == accepted["id"]
                    .as_str()
                    .context("accepted Member ID")?
                    .parse::<Uuid>()?,
        "membership notice projection is invalid"
    );
    let member_event_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM outbox_events \
         WHERE kind='member.changed' AND payload_json->>'channel_id'=$1",
    )
    .bind(general_channel_id.to_string())
    .fetch_one(&pool)
    .await?;
    ensure!(
        member_event_count == 1,
        "expected one member.changed event, got {member_event_count}"
    );
    pool.close().await;

    let retried: Value = client
        .post(server_url.join(&format!("/api/v1/invites/{token}/accept"))?)
        .header(header::COOKIE, &recipient)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(retried["id"] == accepted["id"]);

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
