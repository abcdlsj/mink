//! Live one-shot Agent smoke test ("brain in a vat").
//!
//! The test starts an isolated Server, PostgreSQL database, and Computer; runs
//! the same disposable-Agent checklist against both the special-home Codex
//! driver and the Builtin driver; then destroys the whole environment.
//!
//! The checklist exercises the Agent CLI contract, workspace path handling,
//! memory access, discovery, role-file based Agent creation, message send,
//! inbox ack, and yield. It fails when a tool failure or a failed Run appears.

mod support;

use std::{
    net::SocketAddr,
    time::{Duration, Instant},
};

use anyhow::{Context, Result, ensure};
use reqwest::{Client, StatusCode, header};
use serde_json::Value;
use sqlx::PgPool;
use tempfile::{TempDir, tempdir};
use url::Url;
use uuid::Uuid;

use support::{
    SumiProcess, TestDatabase, confirm_pairing, create_space, ensure_default_builtin_config,
    ensure_special_codex_home, pairing_url_from_daemon, register_with, reserve_local_port,
    short_temp_root, spawn_default_codex_computer, spawn_server, wait_for_computer_status_for,
    wait_for_health, write_default_codex_computer_config, write_server_config,
};

const AGENT_READY_TIMEOUT: Duration = Duration::from_secs(120);
const BRAIN_VAT_TIMEOUT: Duration = Duration::from_secs(600);
const POLL_INTERVAL: Duration = Duration::from_millis(500);
const COMPLETION_MARKER: &str = "BRAIN_VAT_DONE";
const ROLE_FILE_NAME: &str = "brain-vat-role.md";

#[derive(Clone, Copy)]
struct DriverProfile {
    name: &'static str,
    driver: &'static str,
    child_name: &'static str,
    channel_slug: &'static str,
}

const DRIVER_PROFILES: [DriverProfile; 2] = [
    DriverProfile {
        name: "BrainVatCodex",
        driver: "codex",
        child_name: "BrainVatChildCodex",
        channel_slug: "brain-vat-codex",
    },
    DriverProfile {
        name: "BrainVatBuiltin",
        driver: "builtin",
        child_name: "BrainVatChildBuiltin",
        channel_slug: "brain-vat-builtin",
    },
];

#[tokio::test]
#[ignore = "requires a special Codex home and a configured Builtin provider"]
async fn brain_in_vat_agents_use_cli_and_workspace_without_failures() -> Result<()> {
    ensure_special_codex_home()?;
    ensure_default_builtin_config()?;
    let database = TestDatabase::create("sumi_brain_in_vat").await?;
    let result = run_brain_in_vat(&database).await;
    database.drop().await?;
    result
}

async fn run_brain_in_vat(database: &TestDatabase) -> Result<()> {
    let root = tempdir()?;
    let web_dist = root.path().join("web");
    let attachments = root.path().join("attachments");
    std::fs::create_dir_all(&web_dist)?;
    std::fs::create_dir(&attachments)?;

    let bind = SocketAddr::from(([127, 0, 0, 1], reserve_local_port()?));
    let server_url = Url::parse(&format!("http://{bind}"))?;
    let server_config = root.path().join("server.toml");
    write_server_config(&server_config, bind, &database.url, &attachments, &web_dist)?;
    let mut server = spawn_server(&server_config)?;
    wait_for_health(&server_url).await?;

    let client = Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .build()?;
    let owner = register_with(
        &client,
        &server_url,
        "BrainVatObserver",
        &format!("brain-vat-{}@example.test", Uuid::now_v7()),
    )
    .await?;
    let space = create_space(&client, &server_url, &owner).await?;

    let state_root = TempDir::with_prefix_in("sumi-brain-vat-", short_temp_root()?)?;
    let state_dir = state_root.path().join("computer");
    std::fs::create_dir(&state_dir)?;
    let computer_config = root.path().join("computer.toml");
    write_default_codex_computer_config(&computer_config, &server_url, &state_dir, 1)?;
    let mut computer = spawn_default_codex_computer(&computer_config)?;
    let pairing_url = pairing_url_from_daemon(&mut computer)
        .await
        .with_context(|| format!("daemon logs: {}", computer.log_text()))?;
    let paired = confirm_pairing(&client, &server_url, &owner, space.id, &pairing_url).await?;
    wait_for_computer_status_for(
        &client,
        &server_url,
        &owner,
        space.id,
        "online",
        Duration::from_secs(30),
    )
    .await
    .with_context(|| format!("computer daemon logs: {}", computer.log_text()))?;

    let computer_home = state_dir
        .join(space.id.to_string())
        .join(paired.id.to_string());
    let pool = PgPool::connect(&database.url).await?;

    for profile in DRIVER_PROFILES {
        eprintln!("BRAIN_VAT starting driver={}", profile.driver);
        let agent_id =
            create_agent(&client, &server_url, &owner, space.id, paired.id, profile).await?;
        grant_agent_create(&client, &server_url, &owner, agent_id).await?;
        wait_for_agent_ready(&client, &server_url, &owner, space.id, agent_id, profile).await?;
        let channel_id =
            create_brain_channel(&client, &server_url, &owner, space.id, agent_id, profile).await?;
        let task_message_id =
            post_task_message(&client, &server_url, &owner, channel_id, agent_id, profile).await?;
        observe_completion(
            &pool,
            channel_id,
            agent_id,
            &mut server,
            &mut computer,
            profile,
        )
        .await?;
        assert_no_builtin_tool_failures(&computer, profile)?;
        assert_role_file_exists(&computer_home, agent_id)?;
        assert_child_agent_exists(&pool, space.id, profile.child_name).await?;
        assert_item_handled(&pool, agent_id, task_message_id).await?;
        eprintln!("BRAIN_VAT completed driver={}", profile.driver);
    }

    pool.close().await;
    computer.interrupt().await?;
    server.interrupt().await?;
    Ok(())
}

fn role_text(profile: DriverProfile) -> String {
    format!(
        concat!(
            "你是“缸中之脑”一次性冒烟测试 Agent（{}）。严格按顺序执行以下清单，不要做额外动作，不要创建除 {} 以外的实体。\n",
            "1. 运行 `sumi agent context current --json`，记录 claimed item id。\n",
            "2. 运行 `sumi agent memory read MEMORY.md --json`，确认返回内容以 `#` 开头。\n",
            "{}",
            "3. 运行 `sumi agent discover agent.create --json`，记录 available 的 computer_id 与 driver。\n",
            "4. 用文件工具在你的 workspace 根目录创建 `{}`，内容为 `You are a disposable child agent.`；之后用 shell 能解析的同一路径传给 `--role-file`。\n",
            "5. 运行 `sumi agent agent create {} --role-file <该路径> --driver <available driver> --computer-id <available computer_id> --json`。\n",
            "6. 运行 `sumi agent message send --body \"BRAIN_VAT_DONE driver={} child={}\" --json`，不要带 `--ack`。\n",
            "7. 对第 1 步的 item 运行 `sumi agent inbox ack <item-id> --json`（不是 `message ack`）。\n",
            "8. 最后运行 `sumi agent run yield --json`。\n",
            "若返回 `error.details.next_action`，按它执行一次；不要猜测变体重试。"
        ),
        profile.driver,
        profile.child_name,
        ROLE_FILE_NAME,
        if profile.driver == "builtin" {
            "2b. 再用 `read` 工具读取 `memory/MEMORY.md`，确认成功。\n"
        } else {
            ""
        },
        profile.child_name,
        profile.driver,
        profile.child_name,
    )
}

async fn create_agent(
    client: &Client,
    server: &Url,
    owner: &str,
    space_id: Uuid,
    computer_id: Uuid,
    profile: DriverProfile,
) -> Result<Uuid> {
    let response = client
        .post(server.join(&format!("/api/v1/spaces/{space_id}/agents"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, owner)
        .json(&serde_json::json!({
            "computer_id": computer_id,
            "name": profile.name,
            "role_text": role_text(profile),
            "access_level": "member",
            "driver_kind": profile.driver,
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "create {} Agent returned {} {}",
        profile.driver,
        response.status(),
        response.text().await.unwrap_or_default()
    );
    let body: Value = response.json().await?;
    uuid_field(&body, "member_id")
}

async fn grant_agent_create(
    client: &Client,
    server: &Url,
    owner: &str,
    agent_id: Uuid,
) -> Result<()> {
    let response = client
        .put(server.join(&format!(
            "/api/v1/members/{agent_id}/permissions/agent.create"
        ))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, owner)
        .send()
        .await?;
    ensure!(
        response.status().is_success(),
        "grant agent.create returned {} {}",
        response.status(),
        response.text().await.unwrap_or_default()
    );
    Ok(())
}

async fn wait_for_agent_ready(
    client: &Client,
    server: &Url,
    owner: &str,
    space_id: Uuid,
    agent_id: Uuid,
    profile: DriverProfile,
) -> Result<()> {
    let deadline = Instant::now() + AGENT_READY_TIMEOUT;
    loop {
        let response = client
            .get(server.join(&format!("/api/v1/spaces/{space_id}/agents"))?)
            .header(header::COOKIE, owner)
            .send()
            .await?;
        ensure!(
            response.status().is_success(),
            "list Agents returned {} while waiting for provisioning",
            response.status()
        );
        let listed: Vec<Value> = response.json().await?;
        let target = agent_id.to_string();
        let ready = listed.iter().any(|candidate| {
            candidate["member_id"].as_str() == Some(target.as_str())
                && candidate["provision_status"] == "ready"
                && candidate["driver_kind"] == profile.driver
        });
        if ready {
            return Ok(());
        }
        ensure!(
            Instant::now() < deadline,
            "{} Agent did not become ready before the startup timeout",
            profile.driver
        );
        tokio::time::sleep(POLL_INTERVAL).await;
    }
}

async fn create_brain_channel(
    client: &Client,
    server: &Url,
    owner: &str,
    space_id: Uuid,
    agent_id: Uuid,
    profile: DriverProfile,
) -> Result<Uuid> {
    let response = client
        .post(server.join(&format!("/api/v1/spaces/{space_id}/channels"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, owner)
        .json(&serde_json::json!({
            "slug": profile.channel_slug,
            "kind": "public",
            "topic": format!("{} 缸中之脑冒烟测试", profile.driver),
            "agent_member_ids": [agent_id],
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "create brain-in-vat Channel returned {} {}",
        response.status(),
        response.text().await.unwrap_or_default()
    );
    let body: Value = response.json().await?;
    uuid_field(&body, "id")
}

async fn post_task_message(
    client: &Client,
    server: &Url,
    owner: &str,
    channel_id: Uuid,
    agent_id: Uuid,
    profile: DriverProfile,
) -> Result<Uuid> {
    let response = client
        .post(server.join(&format!("/api/v1/channels/{channel_id}/messages"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, owner)
        .json(&serde_json::json!({
            "body_markdown": format!(
                "请立即执行你的缸中之脑清单。最终消息必须以 {} 开头，子 Agent 名称必须是 {}。",
                COMPLETION_MARKER, profile.child_name
            ),
            "mentions": [agent_id],
            "mention_all": false,
            "attachment_ids": [],
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "post brain-in-vat task returned {} {}",
        response.status(),
        response.text().await.unwrap_or_default()
    );
    let body: Value = response.json().await?;
    uuid_field(&body, "id")
}

async fn observe_completion(
    pool: &PgPool,
    channel_id: Uuid,
    agent_id: Uuid,
    server: &mut SumiProcess,
    computer: &mut SumiProcess,
    profile: DriverProfile,
) -> Result<()> {
    let deadline = Instant::now() + BRAIN_VAT_TIMEOUT;
    loop {
        server.ensure_running()?;
        computer.ensure_running()?;
        let run = sqlx::query_as::<_, (String, Option<String>, Option<String>)>(
            "SELECT status, outcome_code, error_code FROM agent_runs \
             WHERE agent_id=$1 ORDER BY created_at DESC, id DESC LIMIT 1",
        )
        .bind(agent_id)
        .fetch_optional(pool)
        .await?;
        if let Some((status, outcome, error)) = &run {
            ensure!(
                status != "failed",
                "{} brain-in-vat Run failed: status={status} outcome={outcome:?} error={error:?}",
                profile.driver
            );
        }
        let completed: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM messages \
             WHERE channel_id=$1 AND author_member_id=$2 AND body_markdown LIKE $3",
        )
        .bind(channel_id)
        .bind(agent_id)
        .bind(format!("%{COMPLETION_MARKER}%"))
        .fetch_one(pool)
        .await?;
        if completed > 0
            && let Some((status, _, _)) = run
            && (status == "completed" || status == "yielded")
        {
            return Ok(());
        }
        ensure!(
            Instant::now() < deadline,
            "timed out waiting for {} brain-in-vat completion; server logs: {}; computer logs: {}",
            profile.driver,
            server.log_text(),
            computer.log_text()
        );
        tokio::time::sleep(POLL_INTERVAL).await;
    }
}

fn assert_no_builtin_tool_failures(computer: &SumiProcess, profile: DriverProfile) -> Result<()> {
    if profile.driver == "builtin" {
        ensure!(
            !computer.logs_contain("Builtin tool failed"),
            "Builtin brain-in-vat produced tool failures:\n{}",
            computer.log_text()
        );
    }
    Ok(())
}

fn assert_role_file_exists(computer_home: &std::path::Path, agent_id: Uuid) -> Result<()> {
    let path = computer_home
        .join("agents")
        .join(agent_id.to_string())
        .join("workspace")
        .join(ROLE_FILE_NAME);
    ensure!(
        path.is_file(),
        "brain-in-vat role file is missing at {}",
        path.display()
    );
    Ok(())
}

async fn assert_child_agent_exists(pool: &PgPool, space_id: Uuid, child_name: &str) -> Result<()> {
    let count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM agents a JOIN members m ON m.id = a.member_id \
         WHERE m.display_name=$1 AND m.space_id=$2",
    )
    .bind(child_name)
    .bind(space_id)
    .fetch_one(pool)
    .await?;
    ensure!(
        count == 1,
        "child Agent {child_name} was not created by the brain-in-vat checklist"
    );
    Ok(())
}

async fn assert_item_handled(pool: &PgPool, agent_id: Uuid, message_id: Uuid) -> Result<()> {
    let handled: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM inbox_items \
         WHERE member_id=$1 AND message_id=$2 AND status='handled'",
    )
    .bind(agent_id)
    .bind(message_id)
    .fetch_one(pool)
    .await?;
    ensure!(
        handled == 1,
        "the brain-in-vat Inbox Item was not explicitly handled"
    );
    Ok(())
}

fn uuid_field(body: &Value, field: &str) -> Result<Uuid> {
    body.get(field)
        .and_then(Value::as_str)
        .context("missing UUID field")?
        .parse()
        .context("invalid UUID field")
}
