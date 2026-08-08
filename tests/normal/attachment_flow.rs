#[path = "../support/mod.rs"]
mod support;

use std::{net::SocketAddr, str::FromStr};

use anyhow::{Context, Result, ensure};
use reqwest::{Client, StatusCode, header};
use serde::Deserialize;
use sha2::{Digest, Sha256};
use sqlx::{PgPool, postgres::PgConnectOptions};
use tempfile::tempdir;
use url::Url;
use uuid::Uuid;

use support::{
    TestDatabase, create_space, register_human, reserve_local_port, spawn_server, wait_for_health,
};

const ATTACHMENT_BODY: &[u8] = b"exact-payload";
const OVERSIZED_BODY: &[u8] = b"payload-over-limit";

#[derive(Deserialize)]
struct AttachmentResponse {
    id: Uuid,
    uploader_member_id: Uuid,
    original_name: String,
    media_type: String,
    size: Option<i64>,
    sha256: Option<String>,
    status: String,
    upload_path: Option<String>,
    download_path: Option<String>,
}

#[derive(Deserialize)]
struct MessageResponse {
    id: Uuid,
    seq: i64,
    attachments: Vec<AttachmentResponse>,
}

#[tokio::test]
async fn attachment_upload_link_and_download_is_a_real_process_transactional_flow() -> Result<()> {
    let database = TestDatabase::create("sumi_attachment_flow").await?;
    let result = run_attachment_flow(&database).await;
    database.drop().await?;
    result
}

async fn run_attachment_flow(database: &TestDatabase) -> Result<()> {
    let root = tempdir()?;
    let web_dist = root.path().join("web-dist");
    std::fs::create_dir(&web_dist)?;
    std::fs::write(
        web_dist.join("index.html"),
        "<!doctype html><title>Sumi</title>",
    )?;
    let attachment_dir = root.path().join("attachments");
    let bind = SocketAddr::from(([127, 0, 0, 1], reserve_local_port()?));
    let server_url = Url::parse(&format!("http://{bind}"))?;
    let config = root.path().join("server.toml");
    std::fs::write(
        &config,
        format!(
            "[server]\nbind = '{bind}'\ndatabase_url = '{}'\nattachment_dir = '{}'\nattachment_max_bytes = 16\nweb_dist = '{}'\n",
            database.url,
            attachment_dir.display(),
            web_dist.display(),
        ),
    )?;
    let mut server = spawn_server(&config)?;
    wait_for_health(&server_url).await?;

    let client = Client::new();
    let owner_cookie = register_human(&client, &server_url).await?;
    let space = create_space(&client, &server_url, &owner_cookie).await?;
    let outsider_cookie = register_human(&client, &server_url).await?;
    let pool = PgPool::connect_with(PgConnectOptions::from_str(&database.url)?).await?;
    let owner_member_id: Uuid =
        sqlx::query_scalar("SELECT owner_member_id FROM spaces WHERE id = $1")
            .bind(space.id)
            .fetch_one(&pool)
            .await?;

    let created = client
        .post(server_url.join("/api/v1/attachments/uploads")?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner_cookie)
        .json(&serde_json::json!({
            "space_id": space.id,
            "original_name": " evidence.txt ",
            "media_type": " text/plain "
        }))
        .send()
        .await?;
    ensure!(created.status() == StatusCode::CREATED);
    let uploading: AttachmentResponse = created.json().await?;
    ensure!(
        uploading.uploader_member_id == owner_member_id
            && uploading.original_name == "evidence.txt"
            && uploading.media_type == "text/plain"
            && uploading.status == "uploading"
            && uploading.size.is_none()
            && uploading.sha256.is_none()
            && uploading.upload_path.is_some()
            && uploading.download_path.is_none()
    );

    let premature = send_message(
        &client,
        &server_url,
        &owner_cookie,
        space.general_channel_id,
        uploading.id,
        "must roll back before ready",
    )
    .await?;
    ensure!(premature.status() == StatusCode::CONFLICT);
    assert_channel_unchanged(&pool, space.general_channel_id).await?;

    let oversized = client
        .put(server_url.join(&format!("/api/v1/attachments/{}/content", uploading.id))?)
        .header(header::COOKIE, &owner_cookie)
        .body(OVERSIZED_BODY)
        .send()
        .await?;
    ensure!(oversized.status() == StatusCode::BAD_REQUEST);

    let content = client
        .put(server_url.join(&format!("/api/v1/attachments/{}/content", uploading.id))?)
        .header(header::COOKIE, &owner_cookie)
        .body(ATTACHMENT_BODY)
        .send()
        .await?;
    ensure!(content.status() == StatusCode::NO_CONTENT);

    let unlinked_download = download(&client, &server_url, &owner_cookie, uploading.id).await?;
    ensure!(unlinked_download.status() == StatusCode::NOT_FOUND);

    let declared_sha = hex::encode(Sha256::digest(ATTACHMENT_BODY));
    let mismatch = complete(
        &client,
        &server_url,
        &owner_cookie,
        uploading.id,
        ATTACHMENT_BODY.len(),
        &"00".repeat(32),
        Uuid::now_v7(),
    )
    .await?;
    ensure!(mismatch.status() == StatusCode::BAD_REQUEST);
    let incomplete_facts: (String, i64) = sqlx::query_as(
        "SELECT status, (SELECT count(*) FROM outbox_events \
         WHERE kind = 'attachment.ready' AND payload_json->>'attachment_id' = attachments.id::text) \
         FROM attachments WHERE id = $1",
    )
    .bind(uploading.id)
    .fetch_one(&pool)
    .await?;
    ensure!(incomplete_facts == ("uploading".to_owned(), 0));

    let complete_key = Uuid::now_v7();
    let completed = complete(
        &client,
        &server_url,
        &owner_cookie,
        uploading.id,
        ATTACHMENT_BODY.len(),
        &declared_sha,
        complete_key,
    )
    .await?;
    ensure!(completed.status() == StatusCode::OK);
    let ready: AttachmentResponse = completed.json().await?;
    ensure!(
        ready.status == "ready"
            && ready.size == Some(ATTACHMENT_BODY.len() as i64)
            && ready.sha256.as_deref() == Some(declared_sha.as_str())
            && ready.upload_path.is_none()
            && ready.download_path.is_some()
    );

    let complete_retry = complete(
        &client,
        &server_url,
        &owner_cookie,
        uploading.id,
        ATTACHMENT_BODY.len(),
        &declared_sha,
        complete_key,
    )
    .await?;
    ensure!(complete_retry.status() == StatusCode::OK);

    let linked = send_message(
        &client,
        &server_url,
        &owner_cookie,
        space.general_channel_id,
        ready.id,
        "ready Attachment",
    )
    .await?;
    ensure!(linked.status() == StatusCode::CREATED);
    let linked: MessageResponse = linked.json().await?;
    ensure!(
        linked.seq == 1 && linked.attachments.len() == 1 && linked.attachments[0].id == ready.id
    );

    let owner_download = download(&client, &server_url, &owner_cookie, ready.id).await?;
    ensure!(owner_download.status() == StatusCode::OK);
    ensure!(
        owner_download
            .headers()
            .get(header::CONTENT_DISPOSITION)
            .context("download omitted Content-Disposition")?
            == "attachment; filename=\"evidence.txt\""
    );
    ensure!(owner_download.bytes().await?.as_ref() == ATTACHMENT_BODY);
    let outsider_download = download(&client, &server_url, &outsider_cookie, ready.id).await?;
    ensure!(outsider_download.status() == StatusCode::NOT_FOUND);

    let duplicate_link = send_message(
        &client,
        &server_url,
        &owner_cookie,
        space.general_channel_id,
        ready.id,
        "must not link twice",
    )
    .await?;
    ensure!(duplicate_link.status() == StatusCode::CONFLICT);

    let facts: (String, i64, i64, i64, i64, i64) = sqlx::query_as(
        "SELECT attachments.status, \
           (SELECT count(*) FROM message_attachments WHERE attachment_id = $1), \
           (SELECT count(*) FROM messages WHERE channel_id = $2), \
           (SELECT next_seq FROM channels WHERE id = $2), \
           (SELECT count(*) FROM outbox_events WHERE kind = 'attachment.ready' AND payload_json->>'attachment_id' = $1::text), \
           (SELECT count(*) FROM outbox_events WHERE kind = 'message.created' AND payload_json->>'resource_id' = $3::text) \
         FROM attachments WHERE attachments.id = $1",
    )
    .bind(ready.id)
    .bind(space.general_channel_id)
    .bind(linked.id)
    .fetch_one(&pool)
    .await?;
    ensure!(
        facts == ("ready".to_owned(), 1, 1, 2, 1, 1),
        "unexpected persisted facts: {facts:?}"
    );
    ensure!(!server.logs_contain("exact-payload"));

    server.ensure_running()?;
    server.interrupt().await?;
    pool.close().await;
    Ok(())
}

async fn send_message(
    client: &Client,
    server: &Url,
    cookie: &str,
    channel_id: Uuid,
    attachment_id: Uuid,
    body: &str,
) -> Result<reqwest::Response> {
    Ok(client
        .post(server.join(&format!("/api/v1/channels/{channel_id}/messages"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({
            "body_markdown": body,
            "mentions": [],
            "attachment_ids": [attachment_id]
        }))
        .send()
        .await?)
}

async fn complete(
    client: &Client,
    server: &Url,
    cookie: &str,
    attachment_id: Uuid,
    size: usize,
    sha256: &str,
    key: Uuid,
) -> Result<reqwest::Response> {
    Ok(client
        .post(server.join(&format!("/api/v1/attachments/{attachment_id}/complete"))?)
        .header("idempotency-key", key.to_string())
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({ "size": size, "sha256": sha256 }))
        .send()
        .await?)
}

async fn download(
    client: &Client,
    server: &Url,
    cookie: &str,
    attachment_id: Uuid,
) -> Result<reqwest::Response> {
    Ok(client
        .get(server.join(&format!("/api/v1/attachments/{attachment_id}/download"))?)
        .header(header::COOKIE, cookie)
        .send()
        .await?)
}

async fn assert_channel_unchanged(pool: &PgPool, channel_id: Uuid) -> Result<()> {
    let state: (i64, i64) = sqlx::query_as(
        "SELECT next_seq, (SELECT count(*) FROM messages WHERE channel_id = $1) \
         FROM channels WHERE id = $1",
    )
    .bind(channel_id)
    .fetch_one(pool)
    .await?;
    ensure!(state == (1, 0));
    Ok(())
}
