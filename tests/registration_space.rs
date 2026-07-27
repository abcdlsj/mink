mod support;

use std::{net::SocketAddr, str::FromStr};

use anyhow::{Context, Result, ensure};
use reqwest::{Client, StatusCode, header};
use serde::Deserialize;
use sqlx::{PgPool, postgres::PgConnectOptions};
use tempfile::tempdir;
use url::Url;
use uuid::Uuid;

use support::{
    TestDatabase, reserve_local_port, spawn_server, wait_for_health, write_server_config,
};

#[derive(Deserialize)]
struct RegistrationResponse {
    user: UserResponse,
    next: String,
}

#[derive(Deserialize)]
struct UserResponse {
    id: Uuid,
    display_name: String,
    email: String,
}

#[derive(Deserialize)]
struct SpaceResponse {
    id: Uuid,
    slug: String,
    owner_member_id: Uuid,
    current_member_id: Uuid,
    general_channel_id: Uuid,
}

#[tokio::test]
async fn registration_and_space_creation_form_a_real_process_transactional_flow() -> Result<()> {
    let database = TestDatabase::create("sumi_registration_space").await?;
    let result = run_registration_space_flow(&database).await;
    database.drop().await?;
    result
}

async fn run_registration_space_flow(database: &TestDatabase) -> Result<()> {
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
    let registration = client
        .post(server_url.join("/api/v1/auth/register")?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .json(&serde_json::json!({
            "display_name": "Ada Lovelace",
            "email": "ADA@EXAMPLE.TEST",
            "password": "correct horse battery staple"
        }))
        .send()
        .await?;
    ensure!(registration.status() == StatusCode::CREATED);
    let cookie = session_cookie(&registration)?;
    let registration: RegistrationResponse = registration.json().await?;
    ensure!(registration.user.display_name == "Ada Lovelace");
    ensure!(registration.user.email == "ada@example.test");
    ensure!(registration.next == "/spaces/new");

    let current_user: UserResponse = client
        .get(server_url.join("/api/v1/auth/me")?)
        .header(header::COOKIE, &cookie)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(current_user.id == registration.user.id);

    let created = create_space(&client, &server_url, &cookie, "sumi-lab").await?;
    ensure!(created.slug == "sumi-lab");
    ensure!(created.owner_member_id == created.current_member_id);

    let fetched: SpaceResponse = client
        .get(server_url.join("/api/v1/spaces/by-slug/sumi-lab")?)
        .header(header::COOKIE, &cookie)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(fetched.id == created.id);
    ensure!(fetched.general_channel_id == created.general_channel_id);

    let second_cookie = register_second_human(&client, &server_url).await?;
    let uppercase_conflict = client
        .post(server_url.join("/api/v1/spaces")?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &second_cookie)
        .json(&serde_json::json!({
            "name": "Conflicting Lab",
            "slug": "SUMI-LAB",
            "accent": "#FE7DA8"
        }))
        .send()
        .await?;
    ensure!(uppercase_conflict.status() == StatusCode::BAD_REQUEST);

    let exact_conflict = client
        .post(server_url.join("/api/v1/spaces")?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &second_cookie)
        .json(&serde_json::json!({
            "name": "Conflicting Lab",
            "slug": "sumi-lab",
            "accent": "#FE7DA8"
        }))
        .send()
        .await?;
    ensure!(exact_conflict.status() == StatusCode::CONFLICT);

    let pool = PgPool::connect_with(PgConnectOptions::from_str(&database.url)?).await?;
    let facts: (i64, i64, i64, i64, i64, i64) = sqlx::query_as(
        "SELECT \
           (SELECT count(*) FROM spaces WHERE id = $1 AND slug = 'SUMI-LAB'::citext), \
           (SELECT count(*) FROM members WHERE id = $2 AND space_id = $1 \
              AND kind = 'human' AND access_level = 'owner'), \
           (SELECT count(*) FROM human_members WHERE member_id = $2 AND space_id = $1 \
              AND user_id = $4), \
           (SELECT count(*) FROM channels WHERE id = $3 AND space_id = $1 \
              AND kind = 'public' AND slug = 'general'), \
           (SELECT count(*) FROM channel_members WHERE channel_id = $3 \
              AND member_id = $2 AND space_id = $1), \
           (SELECT count(*) FROM audit_events WHERE space_id = $1 \
              AND actor_member_id = $2 AND action = 'space.created')",
    )
    .bind(created.id)
    .bind(created.owner_member_id)
    .bind(created.general_channel_id)
    .bind(registration.user.id)
    .fetch_one(&pool)
    .await?;
    ensure!(facts == (1, 1, 1, 1, 1, 1));

    let channel_event_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM outbox_events \
         WHERE aggregate_id = $1 AND topic = 'channel.created'",
    )
    .bind(created.general_channel_id)
    .fetch_one(&pool)
    .await?;
    ensure!(channel_event_count == 1);

    let conflicting_space_count: i64 =
        sqlx::query_scalar("SELECT count(*) FROM spaces WHERE name = 'Conflicting Lab'")
            .fetch_one(&pool)
            .await?;
    ensure!(conflicting_space_count == 0);

    pool.close().await;
    server.ensure_running()?;
    server.interrupt().await?;
    Ok(())
}

async fn create_space(
    client: &Client,
    server: &Url,
    cookie: &str,
    slug: &str,
) -> Result<SpaceResponse> {
    Ok(client
        .post(server.join("/api/v1/spaces")?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({
            "name": "Sumi Lab",
            "slug": slug,
            "accent": "#FE7DA8"
        }))
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?)
}

async fn register_second_human(client: &Client, server: &Url) -> Result<String> {
    let response = client
        .post(server.join("/api/v1/auth/register")?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .json(&serde_json::json!({
            "display_name": "Grace Hopper",
            "email": "grace@example.test",
            "password": "another correct horse battery staple"
        }))
        .send()
        .await?;
    ensure!(response.status() == StatusCode::CREATED);
    session_cookie(&response)
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
