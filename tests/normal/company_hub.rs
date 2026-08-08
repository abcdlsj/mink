#[path = "../support/mod.rs"]
mod support;

use std::{net::SocketAddr, str::FromStr, time::Duration};

use anyhow::{Context, Result, ensure};
use reqwest::{Client, StatusCode, header};
use sha2::{Digest, Sha256};
use sqlx::{PgPool, postgres::PgConnectOptions};
use tempfile::tempdir;
use url::Url;
use uuid::Uuid;

use support::{
    TestDatabase, create_space, register_human, reserve_local_port, short_temp_root, spawn_server,
    wait_for_health, write_computer_config, write_server_config,
};

const SHARED_BODY: &[u8] = b"shared bytes";

#[derive(serde::Deserialize)]
struct ChannelList {
    channels: Vec<Channel>,
}

#[derive(serde::Deserialize)]
struct Channel {
    slug: String,
    joined: bool,
}

#[derive(Debug, serde::Deserialize)]
struct CompanyFile {
    id: Uuid,
    name: String,
    size: i64,
    uploader_name: String,
    download_path: String,
}

#[derive(serde::Deserialize)]
struct CreatedMessage {
    id: Uuid,
}

#[derive(serde::Deserialize)]
struct CreatedTask {
    id: Uuid,
    seq: i64,
    status: String,
}

#[derive(serde::Deserialize)]
struct CapabilityEnvelope {
    ok: bool,
    data: Option<serde_json::Value>,
    error: Option<serde_json::Value>,
}

#[tokio::test]
async fn company_hub_shares_files_into_agent_workspaces_and_supports_task_claim() -> Result<()> {
    let database = TestDatabase::create("sumi_company_hub").await?;
    let result = run_company_hub(&database).await;
    database.drop().await?;
    result
}

async fn run_company_hub(database: &TestDatabase) -> Result<()> {
    let root = tempdir()?;
    let web_dist = root.path().join("web");
    let attachment_dir = root.path().join("attachments");
    std::fs::create_dir(&web_dist)?;
    std::fs::write(
        web_dist.join("index.html"),
        "<!doctype html><title>Sumi</title>",
    )?;
    let bind = SocketAddr::from(([127, 0, 0, 1], reserve_local_port()?));
    let server_url = Url::parse(&format!("http://{bind}"))?;
    let server_config = root.path().join("server.toml");
    write_server_config(
        &server_config,
        bind,
        &database.url,
        &attachment_dir,
        &web_dist,
    )?;
    let mut server = spawn_server(&server_config)?;
    wait_for_health(&server_url).await?;

    let client = Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .build()?;
    let owner = register_human(&client, &server_url).await?;
    let space = create_space(&client, &server_url, &owner).await?;
    let pool = PgPool::connect_with(PgConnectOptions::from_str(&database.url)?).await?;

    // Every Space gets an all-Member HQ channel.
    let channels: ChannelList = client
        .get(server_url.join(&format!("/api/v1/spaces/{}/channels", space.id))?)
        .header(header::COOKIE, &owner)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let hq = channels
        .channels
        .iter()
        .find(|channel| channel.slug == "hq")
        .context("Space must have an #hq channel")?;
    ensure!(hq.joined, "Owner must be auto-joined to #hq");

    // A Member uploads into the Company Drive; the file round-trips with verified bytes.
    let upload_key = Uuid::now_v7();
    let uploaded = upload_company_file(
        &client,
        &server_url,
        &owner,
        space.id,
        "shared.txt",
        "text/plain",
        SHARED_BODY,
        upload_key,
    )
    .await?;
    ensure!(uploaded.status() == StatusCode::CREATED);
    let file: CompanyFile = uploaded.json().await?;
    ensure!(
        file.name == "shared.txt"
            && file.size == SHARED_BODY.len() as i64
            && file.uploader_name == "Process_Test_Owner"
    );

    let replay = upload_company_file(
        &client,
        &server_url,
        &owner,
        space.id,
        "shared.txt",
        "text/plain",
        SHARED_BODY,
        upload_key,
    )
    .await?;
    ensure!(replay.status() == StatusCode::CREATED);
    let replayed: CompanyFile = replay.json().await?;
    ensure!(
        replayed.id == file.id,
        "upload replay must return the same file"
    );

    let listed: Vec<CompanyFile> = client
        .get(server_url.join(&format!("/api/v1/spaces/{}/company/files", space.id))?)
        .header(header::COOKIE, &owner)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(listed.len() == 1 && listed[0].id == file.id);

    let downloaded = client
        .get(server_url.join(&file.download_path)?)
        .header(header::COOKIE, &owner)
        .send()
        .await?;
    ensure!(downloaded.status() == StatusCode::OK);
    ensure!(
        downloaded
            .headers()
            .get(header::CONTENT_DISPOSITION)
            .context("download omitted Content-Disposition")?
            == "inline; filename=\"shared.txt\""
    );
    ensure!(downloaded.bytes().await?.as_ref() == SHARED_BODY);

    // A paired Computer mounts the drive into every Agent's workspace.
    let mount_file = mount_company_drive(
        &client,
        &server_url,
        &owner,
        space.id,
        &attachment_dir.join("company"),
        &root,
    )
    .await?;

    // Deleting removes the file from the API and from the drive.
    let deleted = client
        .delete(server_url.join(&format!(
            "/api/v1/spaces/{}/company/files/{}",
            space.id, file.id
        ))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, &owner)
        .send()
        .await?;
    ensure!(deleted.status() == StatusCode::NO_CONTENT);
    let listed: Vec<CompanyFile> = client
        .get(server_url.join(&format!("/api/v1/spaces/{}/company/files", space.id))?)
        .header(header::COOKIE, &owner)
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    ensure!(
        listed.iter().all(|entry| entry.id != file.id),
        "deleted file must leave the list: {listed:?}"
    );
    ensure!(
        !mount_file.join("shared.txt").exists(),
        "deleted file must leave the mounted drive"
    );

    // An Agent can open the board and claim a TODO Task through the capability transport.
    let task = task_open_and_claim(&client, &server_url, &owner, &pool, space).await?;
    ensure!(task["status"] == "in_progress");

    server.ensure_running()?;
    server.interrupt().await?;
    pool.close().await;
    Ok(())
}

#[allow(clippy::too_many_arguments)]
async fn upload_company_file(
    client: &Client,
    server: &Url,
    cookie: &str,
    space_id: Uuid,
    name: &str,
    media_type: &str,
    body: &[u8],
    key: Uuid,
) -> Result<reqwest::Response> {
    Ok(client
        .post(server.join(&format!(
            "/api/v1/spaces/{space_id}/company/files?name={name}&media_type={media_type}"
        ))?)
        .header("idempotency-key", key.to_string())
        .header(header::COOKIE, cookie)
        .body(body.to_vec())
        .send()
        .await?)
}

async fn mount_company_drive(
    client: &Client,
    server: &Url,
    cookie: &str,
    space_id: Uuid,
    drive_root: &std::path::Path,
    root: &tempfile::TempDir,
) -> Result<std::path::PathBuf> {
    let state_root = tempfile::TempDir::with_prefix_in("sumi-", short_temp_root()?)?;
    let state_dir = state_root.path().join("s");
    std::fs::create_dir(&state_dir)?;
    let computer_config = root.path().join("computer.toml");
    write_computer_config(&computer_config, server, &state_dir, Some(drive_root))?;
    let mut daemon = support::spawn_computer(&computer_config)?;
    let pairing_url = support::pairing_url_from_daemon(&mut daemon)
        .await
        .context("daemon must print a pairing URL")?;
    let computer = support::confirm_pairing(client, server, cookie, space_id, &pairing_url)
        .await
        .context("confirm daemon pairing")?;
    support::wait_for_computer_status(client, server, cookie, space_id, "online")
        .await
        .context("paired daemon must come online")?;

    let created = client
        .post(server.join(&format!("/api/v1/spaces/{space_id}/agents"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({
            "computer_id": computer.id,
            "name": "Mounter",
            "role_text": "Verify the Company Drive mount",
            "access_level": "member",
            "driver_kind": "builtin",
        }))
        .send()
        .await?;
    ensure!(
        created.status() == StatusCode::CREATED,
        "create Agent: {}",
        created.status()
    );
    let agent: serde_json::Value = created.json().await?;
    let agent_id: Uuid = agent["member_id"]
        .as_str()
        .context("Agent member_id")?
        .parse()?;

    let agent_home = state_dir
        .join(space_id.to_string())
        .join(computer.id.to_string())
        .join("agents")
        .join(agent_id.to_string());
    let mount = agent_home.join("workspace/company");
    let deadline = std::time::Instant::now() + Duration::from_secs(30);
    while !mount.exists() {
        ensure!(
            std::time::Instant::now() < deadline,
            "Agent Home did not mount the Company Drive; home={} logs={}",
            agent_home.display(),
            daemon.log_text()
        );
        tokio::time::sleep(Duration::from_millis(200)).await;
    }
    ensure!(
        std::fs::symlink_metadata(&mount)
            .context("workspace/company metadata")?
            .file_type()
            .is_symlink(),
        "workspace/company must be a symlink"
    );
    let drive_dir = drive_root
        .join("spaces")
        .join(space_id.to_string())
        .join("company");
    ensure!(
        std::fs::canonicalize(&mount).context("canonicalize mount")?
            == std::fs::canonicalize(&drive_dir).context("canonicalize drive")?,
        "workspace/company must resolve to the Company Drive"
    );

    upload_company_file(
        client,
        server,
        cookie,
        space_id,
        "mount.txt",
        "text/plain",
        b"mounted",
        Uuid::now_v7(),
    )
    .await?
    .error_for_status()?;
    let mounted = std::fs::read(mount.join("mount.txt")).with_context(|| {
        let entries = std::fs::read_dir(&drive_dir)
            .map(|iterator| {
                iterator
                    .filter_map(|entry| {
                        entry
                            .ok()
                            .map(|entry| entry.file_name().to_string_lossy().into_owned())
                    })
                    .collect::<Vec<_>>()
                    .join(", ")
            })
            .unwrap_or_else(|error| format!("unreadable: {error}"));
        format!(
            "read through mount failed; drive={} entries=[{entries}]",
            drive_dir.display()
        )
    })?;
    ensure!(mounted == b"mounted", "uploaded bytes must match");

    drop(daemon);
    Ok(drive_dir)
}

async fn task_open_and_claim(
    client: &Client,
    server: &Url,
    cookie: &str,
    pool: &PgPool,
    space: support::SpaceResponse,
) -> Result<serde_json::Value> {
    let message = client
        .post(server.join(&format!(
            "/api/v1/channels/{}/messages",
            space.general_channel_id
        ))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({
            "body_markdown": "Build the company hub",
            "mentions": [],
            "attachment_ids": [],
        }))
        .send()
        .await?
        .error_for_status()?;
    let message: CreatedMessage = message.json().await?;

    let task = client
        .post(server.join(&format!("/api/v1/root-messages/{}/task", message.id))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({ "title": "Company task", "assignee_agent_member_id": null }))
        .send()
        .await?
        .error_for_status()?;
    let task: CreatedTask = task.json().await?;
    ensure!(task.status == "todo", "fresh Task must be TODO");

    let computer_id = Uuid::now_v7();
    let agent_id = Uuid::now_v7();
    let run_id = Uuid::now_v7();
    let token = format!("company-test-token-{}", Uuid::now_v7().simple());
    let token_hash = hex::encode(<Sha256 as Digest>::digest(token.as_bytes()));
    sqlx::query(
        "INSERT INTO computers \
         (id,space_id,name,hostname,os,token_hash,connection_status,next_command_seq,created_at) \
         VALUES ($1,$2,$3,'localhost','macos',$4,'online',1,now())",
    )
    .bind(computer_id)
    .bind(space.id)
    .bind("Company Test Computer")
    .bind(&token_hash)
    .execute(pool)
    .await?;
    sqlx::query(
        "INSERT INTO members (id,space_id,kind,display_name,access_level,created_at) \
         VALUES ($1,$2,'agent','Coder','member',now())",
    )
    .bind(agent_id)
    .bind(space.id)
    .execute(pool)
    .await?;
    sqlx::query(
        "INSERT INTO agents \
         (member_id,space_id,computer_id,role_text,role_revision,lifecycle,driver_kind,created_at) \
         VALUES ($1,$2,$3,'Build',1,'active','codex',now())",
    )
    .bind(agent_id)
    .bind(space.id)
    .bind(computer_id)
    .execute(pool)
    .await?;
    sqlx::query(
        "INSERT INTO channel_members (channel_id,space_id,member_id,joined_at,last_read_seq) \
         VALUES ($1,$2,$3,now(),0)",
    )
    .bind(space.general_channel_id)
    .bind(space.id)
    .bind(agent_id)
    .execute(pool)
    .await?;
    sqlx::query(
        "INSERT INTO agent_runs \
         (id,space_id,agent_id,task_id,focus_thread_id,status,trigger_kind,created_at,started_at) \
         VALUES ($1,$2,$3,NULL,$4,'working','mention',now(),now())",
    )
    .bind(run_id)
    .bind(space.id)
    .bind(agent_id)
    .bind(message.id)
    .execute(pool)
    .await?;

    let context = serde_json::json!({
        "agent_id": agent_id,
        "space_id": space.id,
        "task_id": null,
        "focus_thread_id": message.id,
        "run_id": run_id,
        "message_snapshot_sequence": 1,
    });
    let open = capability_call(
        client,
        server,
        computer_id,
        &token,
        &context,
        serde_json::json!({ "type": "task_open" }),
        None,
    )
    .await?;
    ensure!(open.ok, "task open failed: {:?}", open.error);
    let tasks_value = open.data.context("task open value")?;
    let tasks = tasks_value
        .as_array()
        .context("task open must return an array")?;
    ensure!(
        tasks.iter().any(|row| {
            row["seq"].as_i64() == Some(task.seq)
                && row["status"] == "todo"
                && row["title"] == "Company task"
        }),
        "task open must list the visible TODO Task: {tasks:?}"
    );

    let claim = capability_call(
        client,
        server,
        computer_id,
        &token,
        &context,
        serde_json::json!({ "type": "task_start", "input": { "task": task.seq } }),
        Some(Uuid::now_v7()),
    )
    .await?;
    ensure!(claim.ok, "task claim failed: {:?}", claim.error);
    let claimed = claim.data.context("task claim value")?;
    let agent_id_string = agent_id.to_string();
    ensure!(
        claimed["status"] == "in_progress"
            && claimed["assignee_agent_member_id"].as_str() == Some(&agent_id_string),
        "claim must start the Task for the Agent: {claimed}"
    );

    let row: (String, Option<Uuid>) =
        sqlx::query_as("SELECT status,assignee_agent_member_id FROM tasks WHERE id=$1")
            .bind(task.id)
            .fetch_one(pool)
            .await?;
    ensure!(row == ("in_progress".to_owned(), Some(agent_id)));
    Ok(claimed)
}

async fn capability_call(
    client: &Client,
    server: &Url,
    computer_id: Uuid,
    token: &str,
    context: &serde_json::Value,
    action: serde_json::Value,
    idempotency_key: Option<Uuid>,
) -> Result<CapabilityEnvelope> {
    let mut body = serde_json::json!({
        "context": context,
        "action": action,
    });
    if let Some(key) = idempotency_key {
        body["idempotency_key"] = serde_json::json!(key);
    }
    let response = client
        .post(server.join(&format!("/api/v1/computers/{computer_id}/agent-actions"))?)
        .bearer_auth(token)
        .json(&body)
        .send()
        .await?;
    ensure!(
        response.status().is_success(),
        "capability call transport: {} body={}",
        response.status(),
        response.text().await.unwrap_or_default()
    );
    Ok(response.json().await?)
}
