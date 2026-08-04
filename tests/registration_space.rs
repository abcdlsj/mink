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
            "display_name": "Ada_Lovelace",
            "email": "ADA@EXAMPLE.TEST",
            "password": "correct horse battery staple"
        }))
        .send()
        .await?;
    ensure!(registration.status() == StatusCode::CREATED);
    let cookie = session_cookie(&registration)?;
    let registration: RegistrationResponse = registration.json().await?;
    ensure!(registration.user.display_name == "Ada_Lovelace");
    ensure!(registration.user.email == "ada@example.test");
    ensure!(registration.next == "create_space");

    let current_user: UserResponse = client
        .get(server_url.join("/api/v1/auth/me")?)
        .header(header::COOKIE, &cookie)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(current_user.id == registration.user.id);

    let create_key = Uuid::now_v7();
    let created = create_space(&client, &server_url, &cookie, "sumi-lab", create_key).await?;
    ensure!(created.slug == "sumi-lab");
    ensure!(created.owner_member_id == created.current_member_id);
    let replayed = create_space(&client, &server_url, &cookie, "sumi-lab", create_key).await?;
    ensure!(replayed.id == created.id);

    let channel_key = Uuid::now_v7();
    let create_channel = || {
        client
            .post(
                server_url
                    .join(&format!("/api/v1/spaces/{}/channels", created.id))
                    .expect("valid Channel URL"),
            )
            .header("idempotency-key", channel_key.to_string())
            .header(header::COOKIE, &cookie)
            .json(&serde_json::json!({
                "slug": "design",
                "kind": "private",
                "agent_member_ids": []
            }))
    };
    let channel: serde_json::Value = create_channel()
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let replayed_channel: serde_json::Value = create_channel()
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(channel["id"] == replayed_channel["id"]);

    let message_key = Uuid::now_v7();
    let create_message = || {
        client
            .post(
                server_url
                    .join(&format!(
                        "/api/v1/channels/{}/messages",
                        created.general_channel_id
                    ))
                    .expect("valid Message URL"),
            )
            .header("idempotency-key", message_key.to_string())
            .header(header::COOKIE, &cookie)
            .json(&serde_json::json!({"body_markdown":"Idempotent Root Message"}))
    };
    let message_response = create_message().send().await?;
    ensure!(
        message_response.status().is_success(),
        "{}",
        server.log_text()
    );
    let message: serde_json::Value = message_response.json().await?;
    let replayed_message_response = create_message().send().await?;
    ensure!(
        replayed_message_response.status().is_success(),
        "{}",
        server.log_text()
    );
    let replayed_message: serde_json::Value = replayed_message_response.json().await?;
    ensure!(message["id"] == replayed_message["id"]);

    let task_key = Uuid::now_v7();
    let create_task = || {
        client
            .post(
                server_url
                    .join(&format!(
                        "/api/v1/root-messages/{}/task",
                        message["id"].as_str().expect("Message ID")
                    ))
                    .expect("valid Task URL"),
            )
            .header("idempotency-key", task_key.to_string())
            .header(header::COOKIE, &cookie)
            .json(&serde_json::json!({"title":"Idempotent Task"}))
    };
    let task: serde_json::Value = create_task()
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let replayed_task: serde_json::Value = create_task()
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(task["id"] == replayed_task["id"]);

    let runs: serde_json::Value = client
        .get(server_url.join(&format!(
            "/api/v1/tasks/{}/runs",
            task["id"].as_str().unwrap()
        ))?)
        .header(header::COOKIE, &cookie)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(runs.as_array().is_some_and(Vec::is_empty));

    let derived_source = client
        .post(server_url.join(&format!(
            "/api/v1/root-messages/{}/task",
            message["id"].as_str().expect("Message ID")
        ))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &cookie)
        .json(&serde_json::json!({
            "title":"Invalid Task",
            "source_thread_id": message["thread_id"]
        }))
        .send()
        .await?;
    ensure!(derived_source.status() == StatusCode::UNPROCESSABLE_ENTITY);

    let edit_key = Uuid::now_v7();
    let edit_message = || {
        client
            .patch(
                server_url
                    .join(&format!(
                        "/api/v1/messages/{}",
                        message["id"].as_str().expect("Message ID")
                    ))
                    .expect("valid Message URL"),
            )
            .header("idempotency-key", edit_key.to_string())
            .header(header::COOKIE, &cookie)
            .json(&serde_json::json!({"body_markdown":"Edited Root Message"}))
    };
    let edited: serde_json::Value = edit_message()
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let replayed_edit: serde_json::Value = edit_message()
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(edited["content"] == replayed_edit["content"]);

    let permission_key = Uuid::now_v7();
    let permission_url = server_url.join(&format!(
        "/api/v1/members/{}/permissions/channel.create",
        created.owner_member_id
    ))?;
    let granted: serde_json::Value = client
        .put(permission_url.clone())
        .header("idempotency-key", permission_key.to_string())
        .header(header::COOKIE, &cookie)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(granted["permissions"] == serde_json::json!(["channel.create"]));
    let replayed_grant: serde_json::Value = client
        .put(permission_url.clone())
        .header("idempotency-key", permission_key.to_string())
        .header(header::COOKIE, &cookie)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(replayed_grant["permissions"] == granted["permissions"]);
    let revoked: serde_json::Value = client
        .delete(permission_url)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &cookie)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(revoked["permissions"] == serde_json::json!([]));

    let delete_key = Uuid::now_v7();
    let delete_message = || {
        client
            .delete(
                server_url
                    .join(&format!(
                        "/api/v1/messages/{}",
                        message["id"].as_str().expect("Message ID")
                    ))
                    .expect("valid Message URL"),
            )
            .header("idempotency-key", delete_key.to_string())
            .header(header::COOKIE, &cookie)
    };
    let deleted: serde_json::Value = delete_message()
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let replayed_delete: serde_json::Value = delete_message()
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(deleted["deleted_at"].is_string());
    ensure!(deleted["deleted_at"] == replayed_delete["deleted_at"]);

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
    ensure!(uppercase_conflict.status() == StatusCode::CONFLICT);

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
    let initialized_channel_id: Uuid = channel["id"]
        .as_str()
        .context("initialized Channel ID")?
        .parse()?;
    let initial_notice_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM messages WHERE channel_id=$1 AND content_kind='system_notice'",
    )
    .bind(initialized_channel_id)
    .fetch_one(&pool)
    .await?;
    ensure!(
        initial_notice_count == 0,
        "initial Channel members must not create System Notices"
    );
    let computer_id = Uuid::now_v7();
    let agent_id = Uuid::now_v7();
    sqlx::query("INSERT INTO computers(id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) VALUES($1,$2,'Test Computer','localhost','linux','test-token-hash','online',1,now())")
        .bind(computer_id)
        .bind(created.id)
        .execute(&pool)
        .await?;
    sqlx::query("INSERT INTO members(id,space_id,kind,display_name,access_level,created_at) VALUES($1,$2,'agent','Lin','member',now())")
        .bind(agent_id)
        .bind(created.id)
        .execute(&pool)
        .await?;
    sqlx::query("INSERT INTO agents(member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) VALUES($1,$2,$3,'Review changes',1,'active','codex',now())")
        .bind(agent_id)
        .bind(created.id)
        .bind(computer_id)
        .execute(&pool)
        .await?;
    let add_member_key = Uuid::now_v7();
    let add_agent = || {
        client
            .post(
                server_url
                    .join(&format!(
                        "/api/v1/channels/{}/members",
                        created.general_channel_id
                    ))
                    .expect("valid Channel member URL"),
            )
            .header("idempotency-key", add_member_key.to_string())
            .header(header::COOKIE, &cookie)
            .json(&serde_json::json!({"agent_member_ids": [agent_id]}))
    };
    let members: serde_json::Value = add_agent().send().await?.error_for_status()?.json().await?;
    let replayed_members: serde_json::Value =
        add_agent().send().await?.error_for_status()?.json().await?;
    ensure!(members == replayed_members);
    ensure!(members["members"].as_array().is_some_and(|members| {
        members
            .iter()
            .any(|member| member["id"] == agent_id.to_string())
    }));
    let membership_facts: (i64, i64, i64, i64, i64) = sqlx::query_as(
        "SELECT \
           (SELECT count(*) FROM channel_members WHERE channel_id=$1 AND member_id=$2), \
           (SELECT count(*) FROM idempotency_records WHERE actor_member_id=$3 AND action='channel.members.add' AND idempotency_key=$4), \
           (SELECT count(*) FROM audit_events WHERE actor_member_id=$3 AND action='channel.members_added' AND subject_id=$1), \
           (SELECT count(*) FROM outbox_events WHERE kind='member.changed' AND payload_json->>'resource_id'=$2::text), \
           (SELECT count(*) FROM messages WHERE channel_id=$1 AND content_kind='system_notice' AND body_markdown='Lin joined the channel')",
    )
    .bind(created.general_channel_id)
    .bind(agent_id)
    .bind(created.owner_member_id)
    .bind(add_member_key)
    .fetch_one(&pool)
    .await?;
    ensure!(membership_facts == (1, 1, 1, 1, 1));

    let facts: (i64, i64, i64, i64, i64, i64) = sqlx::query_as(
        "SELECT \
           (SELECT count(*) FROM spaces WHERE id = $1 AND slug = 'sumi-lab'), \
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
    ensure!(
        facts == (1, 1, 1, 1, 1, 1),
        "unexpected persisted facts: {facts:?}"
    );

    let channel_event_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM outbox_events \
         WHERE kind = 'channel.created' AND payload_json->>'resource_id' = $1::text",
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

    let idempotent_resources: (i64, i64, i64) = sqlx::query_as(
        "SELECT \
           (SELECT count(*) FROM channels WHERE slug='design'), \
           (SELECT count(*) FROM messages WHERE id=$1 AND body_markdown='' AND deleted_at IS NOT NULL), \
           (SELECT count(*) FROM tasks WHERE title='Idempotent Task')",
    )
    .bind(Uuid::parse_str(message["id"].as_str().expect("Message ID"))?)
    .fetch_one(&pool)
    .await?;
    ensure!(idempotent_resources == (1, 1, 1));

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
    key: Uuid,
) -> Result<SpaceResponse> {
    Ok(client
        .post(server.join("/api/v1/spaces")?)
        .header("idempotency-key", key.to_string())
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
            "display_name": "Grace_Hopper",
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
