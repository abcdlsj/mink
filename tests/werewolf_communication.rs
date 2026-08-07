//! Live collaboration acceptance test using a Werewolf scenario.
//!
//! Sumi is a Human-Agent collaboration platform. Werewolf is only a scenario
//! used to exercise role-based collaboration and action-driven communication;
//! the platform has no game-specific feature and this test must not invent one.
//!
//! Core principle:
//! - Agents are never made to obey behavior through prompt text.
//! - Agents collaborate through the rules carried by their Role and through
//!   generic platform actions (messages, private DMs, memory, structured
//!   mentions). The host only publishes game-state facts.
//! - Collaboration must emerge naturally: the test waits for the facts the
//!   agents produce and never narrates step-by-step instructions.

mod support;

use std::{
    collections::BTreeSet,
    net::SocketAddr,
    path::PathBuf,
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
    TestDatabase, default_codex_home, reserve_local_port, spawn_default_codex_computer,
    spawn_server, wait_for_computer_status_for, wait_for_health,
    write_default_codex_computer_config, write_server_config,
};

const PHASE_TIMEOUT: Duration = Duration::from_secs(600);
const SETTLE_TIMEOUT: Duration = Duration::from_secs(600);
const POLL_INTERVAL: Duration = Duration::from_millis(500);

#[derive(Clone, Copy)]
struct AgentProfile {
    name: &'static str,
    driver: &'static str,
    secret_role: &'static str,
}

#[derive(Clone, Copy)]
struct LiveAgent {
    profile: AgentProfile,
    member_id: Uuid,
}

#[derive(Clone, Copy)]
struct PublicPhase {
    message_id: Uuid,
    thread_id: Uuid,
}

#[derive(sqlx::FromRow)]
struct ItemLifecycleRow {
    item_status: String,
    assigned_run_id: Option<Uuid>,
    run_status: Option<String>,
    outcome_code: Option<String>,
    error_code: Option<String>,
}

#[derive(sqlx::FromRow)]
struct OpenAgentDmItemRow {
    message_id: Uuid,
    channel_id: Uuid,
    author_name: String,
    receiver_name: String,
    item_status: String,
    assigned_run_id: Option<Uuid>,
    run_status: Option<String>,
}

struct GameScenario<'a> {
    client: &'a Client,
    server: &'a Url,
    owner: &'a str,
    agents: &'a [LiveAgent],
    pool: &'a PgPool,
}

const AGENT_PROFILES: [AgentProfile; 4] = [
    AgentProfile {
        name: "Aster",
        driver: "builtin",
        secret_role: "预言家",
    },
    AgentProfile {
        name: "Briar",
        driver: "codex",
        secret_role: "狼人",
    },
    AgentProfile {
        name: "Cedar",
        driver: "codex",
        secret_role: "女巫",
    },
    AgentProfile {
        name: "Dawn",
        driver: "codex",
        secret_role: "村民",
    },
];

const PUBLIC_RULES: &str = concat!(
    "公开规则（所有玩家共同遵守）：\n",
    "1. 本局共四名玩家：Aster、Briar、Cedar、Dawn；主持人 Process_Test_Owner 不是玩家。\n",
    "2. 阶段按顺序推进：白天公开讨论、夜晚私下协商、公开投票、主持人公布结果。\n",
    "3. 白天在 #werewolf 频道进行公开讨论，发言只能基于公开事实。\n",
    "4. 夜晚进行私下协商：公开投票前，每名玩家至少与一名其他玩家完成一次私下信息交换；对象和内容自行决定，通过私信完成。\n",
    "5. 公开投票时，在投票消息的线程中回复，正文用 @玩家名 提及你投票的对象；只能投一名玩家，不能投自己。\n",
    "6. 秘密身份和私聊内容在结果公布前属于私人事实，不得写入公开频道、公开消息或 Memory。\n",
);

fn role_text(profile: AgentProfile) -> String {
    format!(
        "你是玩家 {}，参加一局四人狼人杀。\n\n{}\n你的秘密身份是{}，在结果公布前不得公开。",
        profile.name, PUBLIC_RULES, profile.secret_role
    )
}

fn secret_identity_phrase(profile: AgentProfile) -> String {
    format!("你的秘密身份是{}", profile.secret_role)
}

#[tokio::test]
#[ignore = "requires the default Codex home and a configured Builtin provider"]
async fn werewolf_communication_test_runs_a_complete_group_and_dm_game() -> Result<()> {
    let codex_home = default_codex_home()?;
    ensure!(
        codex_home.join("config.toml").is_file(),
        "default Codex home must contain config.toml"
    );
    ensure!(
        codex_home.join("auth.json").is_file(),
        "default Codex home must contain auth.json"
    );
    ensure_default_builtin_config()?;

    let codex_status = tokio::process::Command::new("codex")
        .env_remove("CODEX_HOME")
        .arg("--version")
        .output()
        .await
        .context("run the default codex command")?;
    ensure!(
        codex_status.status.success(),
        "the default codex command is unavailable"
    );

    let database = TestDatabase::create("sumi_werewolf_communication").await?;
    let result = run_werewolf_game(&database).await;
    database.drop().await?;
    result
}

fn ensure_default_builtin_config() -> Result<()> {
    let home = std::env::var_os("HOME").context("HOME is required for the live test")?;
    let config_path = PathBuf::from(home).join(".sumi/config.toml");
    ensure!(
        config_path.is_file(),
        "default Sumi config must contain the configured Builtin provider"
    );
    let encoded = std::fs::read_to_string(&config_path)
        .with_context(|| format!("read default Sumi config at {}", config_path.display()))?;
    let config: toml::Value = toml::from_str(&encoded)
        .with_context(|| format!("parse default Sumi config at {}", config_path.display()))?;
    ensure!(
        config
            .get("computer")
            .and_then(toml::Value::as_table)
            .and_then(|computer| computer.get("builtin"))
            .is_some(),
        "default Sumi config must contain [computer.builtin]"
    );
    Ok(())
}

async fn run_werewolf_game(database: &TestDatabase) -> Result<()> {
    let root = tempdir()?;
    let web_dist = root.path().join("web");
    let attachments = root.path().join("attachments");
    std::fs::create_dir_all(&web_dist)?;
    std::fs::create_dir(&attachments)?;
    std::fs::write(
        web_dist.join("index.html"),
        "<!doctype html><title>Sumi</title>",
    )?;

    let bind = SocketAddr::from(([127, 0, 0, 1], reserve_local_port()?));
    let server_url = Url::parse(&format!("http://{bind}"))?;
    let server_config = root.path().join("server.toml");
    write_server_config(&server_config, bind, &database.url, &attachments, &web_dist)?;
    let mut server = spawn_server(&server_config)?;
    wait_for_health(&server_url).await?;

    let client = Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .build()?;
    let owner = support::register_human(&client, &server_url).await?;
    let space = support::create_space(&client, &server_url, &owner).await?;

    let state_root = TempDir::with_prefix_in("sumi-werewolf-", short_temp_root())?;
    let state_dir = state_root.path().join("computer");
    std::fs::create_dir(&state_dir)?;
    let computer_config = root.path().join("computer.toml");
    write_default_codex_computer_config(
        &computer_config,
        &server_url,
        &state_dir,
        AGENT_PROFILES.len(),
    )?;
    let mut computer = spawn_default_codex_computer(&computer_config)?;
    let pairing_url = support::pairing_url_from_daemon(&mut computer).await?;
    let computer_identity =
        support::confirm_pairing(&client, &server_url, &owner, space.id, &pairing_url).await?;
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

    let mut agents = Vec::with_capacity(AGENT_PROFILES.len());
    for profile in AGENT_PROFILES {
        agents.push(
            create_agent(
                &client,
                &server_url,
                &owner,
                space.id,
                computer_identity.id,
                profile,
            )
            .await?,
        );
    }
    ensure_driver_mix(&agents)?;
    wait_for_agents_ready(
        &client,
        &server_url,
        &owner,
        space.id,
        &agents,
        PHASE_TIMEOUT,
    )
    .await?;

    let pool = PgPool::connect(&database.url).await?;
    let scenario = GameScenario {
        client: &client,
        server: &server_url,
        owner: &owner,
        agents: &agents,
        pool: &pool,
    };

    let group_channel_id =
        create_werewolf_group(&client, &server_url, &owner, space.id, &agents).await?;
    assert_group_setup(&pool, group_channel_id, &agents).await?;

    // The host publishes game-state facts only. Every rule that shapes behavior
    // lives in the Agents' Role, so no phase message tells anyone what to do.
    let dm_baseline = list_agent_agent_dm_message_ids(&pool, space.id, &agents).await?;
    let day = scenario
        .post_public_phase(
            group_channel_id,
            "白天讨论阶段。四名玩家都在场，主持人尚未公布任何结果。",
            true,
        )
        .await?;
    assert_no_secret_role_leak(&pool, group_channel_id, &agents).await?;

    let night = scenario
        .post_public_phase(
            group_channel_id,
            "夜幕降临。四名玩家都在场，公开事实不变。",
            false,
        )
        .await?;
    assert_no_secret_role_leak(&pool, group_channel_id, &agents).await?;

    wait_for_dm_coverage(&pool, space.id, &agents, &dm_baseline, PHASE_TIMEOUT).await?;
    wait_for_all_agent_dm_items_handled(&pool, space.id, &agents, PHASE_TIMEOUT).await?;
    assert_agent_dm_coverage(&pool, space.id, &agents, &dm_baseline).await?;

    let vote = scenario
        .post_public_phase(
            group_channel_id,
            "公开投票阶段。四名玩家都在场，公开事实不变。",
            false,
        )
        .await?;
    assert_no_secret_role_leak(&pool, group_channel_id, &agents).await?;
    wait_for_agent_votes(&pool, vote.thread_id, &agents, PHASE_TIMEOUT).await?;
    assert_agent_votes(&pool, vote.thread_id, &agents).await?;

    let result = scenario
        .post_public_phase(
            group_channel_id,
            "主持人公布结果：Briar 被淘汰，他的身份是狼人；村民获胜。本局结束。",
            false,
        )
        .await?;
    assert_no_secret_role_leak(&pool, group_channel_id, &agents).await?;

    assert!(day.thread_id != night.thread_id);
    assert!(night.thread_id != vote.thread_id);
    assert!(vote.thread_id != result.thread_id);
    assert_public_thread_is_group(&pool, group_channel_id, day).await?;
    assert_public_thread_is_group(&pool, group_channel_id, night).await?;
    assert_public_thread_is_group(&pool, group_channel_id, vote).await?;
    assert_public_thread_is_group(&pool, group_channel_id, result).await?;
    assert_public_agent_participation(&pool, group_channel_id, &agents).await?;

    wait_for_game_settled(&pool, &agents, SETTLE_TIMEOUT).await?;
    assert_all_game_hard_items_handled(&pool, &agents).await?;
    assert_no_dead_agent_inbox_items(&pool, &agents).await?;
    assert_all_agent_runs_terminal(&pool, &agents).await?;

    pool.close().await;
    computer.interrupt().await?;
    server.interrupt().await?;
    Ok(())
}

async fn create_agent(
    client: &Client,
    server: &Url,
    owner: &str,
    space_id: Uuid,
    computer_id: Uuid,
    profile: AgentProfile,
) -> Result<LiveAgent> {
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
        "create Agent {} returned {}",
        profile.name,
        response.status()
    );
    let body: Value = response.json().await?;
    let member_id = uuid_field(&body, "member_id")?;
    ensure!(
        body["driver_kind"] == profile.driver,
        "Agent {} has an unexpected Driver",
        profile.name
    );
    Ok(LiveAgent { profile, member_id })
}

fn ensure_driver_mix(agents: &[LiveAgent]) -> Result<()> {
    let builtin = agents
        .iter()
        .filter(|agent| agent.profile.driver == "builtin")
        .count();
    let codex = agents
        .iter()
        .filter(|agent| agent.profile.driver == "codex")
        .count();
    ensure!(
        builtin == 1,
        "Werewolf test requires exactly one Builtin Agent"
    );
    ensure!(
        codex == 3,
        "Werewolf test requires exactly three Codex Agents"
    );
    Ok(())
}

async fn create_werewolf_group(
    client: &Client,
    server: &Url,
    owner: &str,
    space_id: Uuid,
    agents: &[LiveAgent],
) -> Result<Uuid> {
    let response = client
        .post(server.join(&format!("/api/v1/spaces/{space_id}/channels"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, owner)
        .json(&serde_json::json!({
            "slug": "werewolf",
            "kind": "public",
            "topic": "狼人杀公开讨论",
            "agent_member_ids": agent_ids(agents)
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "create the Werewolf group returned {}",
        response.status()
    );
    let body: Value = response.json().await?;
    uuid_field(&body, "id")
}

async fn wait_for_agents_ready(
    client: &Client,
    server: &Url,
    owner: &str,
    space_id: Uuid,
    agents: &[LiveAgent],
    timeout: Duration,
) -> Result<()> {
    let deadline = Instant::now() + timeout;
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
        let all_ready = agents.iter().all(|agent| {
            let member_id = agent.member_id.to_string();
            listed.iter().any(|candidate| {
                candidate["member_id"].as_str() == Some(member_id.as_str())
                    && candidate["provision_status"] == "ready"
                    && candidate["driver_kind"] == agent.profile.driver
            })
        });
        if all_ready {
            return Ok(());
        }
        if Instant::now() >= deadline {
            ensure!(
                false,
                "Agents did not become ready before the Werewolf timeout"
            );
        }
        tokio::time::sleep(POLL_INTERVAL).await;
    }
}

impl<'a> GameScenario<'a> {
    async fn post_public_phase(
        &self,
        channel_id: Uuid,
        body: &str,
        require_replies: bool,
    ) -> Result<PublicPhase> {
        let root = post_root_message(
            self.client,
            self.server,
            self.owner,
            channel_id,
            body,
            &agent_ids(self.agents),
        )
        .await?;
        let phase = PublicPhase {
            message_id: uuid_field(&root, "id")?,
            thread_id: uuid_field(&root, "thread_id")?,
        };
        if require_replies {
            self.wait_for_public_replies(phase.thread_id, PHASE_TIMEOUT)
                .await?;
        }
        for agent in self.agents {
            wait_for_item_handled(self.pool, phase.message_id, agent.member_id, PHASE_TIMEOUT)
                .await?;
        }
        Ok(phase)
    }

    async fn wait_for_public_replies(&self, thread_id: Uuid, timeout: Duration) -> Result<()> {
        let deadline = Instant::now() + timeout;
        loop {
            let reply_count: i64 = sqlx::query_scalar(
                "SELECT count(DISTINCT author_member_id) FROM messages \
                 WHERE thread_id=$1 AND placement='reply' AND content_kind='text' \
                   AND author_member_id=ANY($2)",
            )
            .bind(thread_id)
            .bind(agent_ids(self.agents))
            .fetch_one(self.pool)
            .await?;
            if reply_count >= 2 {
                return Ok(());
            }
            if Instant::now() >= deadline {
                let missing = missing_thread_replies(self.pool, thread_id, self.agents).await?;
                let summary = agent_failure_summary(self.pool, self.agents).await?;
                ensure!(
                    false,
                    "public Werewolf Thread {thread_id} has only {reply_count} Agent replies; missing replies from {missing:?}; {summary}"
                );
            }
            tokio::time::sleep(POLL_INTERVAL).await;
        }
    }
}

async fn post_root_message(
    client: &Client,
    server: &Url,
    cookie: &str,
    channel_id: Uuid,
    body: &str,
    mentions: &[Uuid],
) -> Result<Value> {
    let response = client
        .post(server.join(&format!("/api/v1/channels/{channel_id}/messages"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, cookie)
        .json(&serde_json::json!({
            "body_markdown": body,
            "mentions": mentions,
            "mention_all": false,
            "attachment_ids": [],
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "post root message returned {}",
        response.status()
    );
    Ok(response.json().await?)
}

async fn missing_thread_replies(
    pool: &PgPool,
    thread_id: Uuid,
    agents: &[LiveAgent],
) -> Result<Vec<&'static str>> {
    let replied: Vec<Uuid> = sqlx::query_scalar(
        "SELECT DISTINCT author_member_id FROM messages \
         WHERE thread_id=$1 AND placement='reply' AND content_kind='text' \
           AND author_member_id=ANY($2)",
    )
    .bind(thread_id)
    .bind(agent_ids(agents))
    .fetch_all(pool)
    .await?;
    Ok(agents
        .iter()
        .filter(|agent| !replied.contains(&agent.member_id))
        .map(|agent| agent.profile.name)
        .collect())
}

async fn wait_for_item_handled(
    pool: &PgPool,
    message_id: Uuid,
    member_id: Uuid,
    timeout: Duration,
) -> Result<()> {
    let deadline = Instant::now() + timeout;
    loop {
        let status: Option<String> = sqlx::query_scalar(
            "SELECT status FROM inbox_items WHERE message_id=$1 AND member_id=$2 ORDER BY created_at DESC LIMIT 1",
        )
        .bind(message_id)
        .bind(member_id)
        .fetch_optional(pool)
        .await?;
        if status.as_deref() == Some("handled") {
            return Ok(());
        }
        if Instant::now() >= deadline {
            let lifecycle = item_lifecycle_summary(pool, message_id, member_id).await?;
            ensure!(
                false,
                "Inbox Item for Agent {member_id} and Message {message_id} was not handled; status={status:?}; lifecycle={lifecycle}"
            );
        }
        tokio::time::sleep(POLL_INTERVAL).await;
    }
}

async fn item_lifecycle_summary(
    pool: &PgPool,
    message_id: Uuid,
    member_id: Uuid,
) -> Result<String> {
    let rows: Vec<ItemLifecycleRow> = sqlx::query_as(
        "SELECT i.status AS item_status,i.assigned_run_id,r.status AS run_status,\
                r.outcome_code,r.error_code \
             FROM inbox_items i LEFT JOIN agent_runs r ON r.id=i.assigned_run_id \
             WHERE i.message_id=$1 AND i.member_id=$2 ORDER BY i.created_at DESC",
    )
    .bind(message_id)
    .bind(member_id)
    .fetch_all(pool)
    .await?;
    let entries = rows
        .into_iter()
        .map(|row| {
            format!(
                "(item_status={:?},assigned_run_id={:?},run_status={:?},outcome_code={:?},error_code={:?})",
                row.item_status,
                row.assigned_run_id,
                row.run_status,
                row.outcome_code,
                row.error_code,
            )
        })
        .collect::<Vec<_>>();
    Ok(format!("[{}]", entries.join(",")))
}

async fn assert_group_setup(pool: &PgPool, channel_id: Uuid, agents: &[LiveAgent]) -> Result<()> {
    let member_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM channel_members WHERE channel_id=$1 AND member_id=ANY($2)",
    )
    .bind(channel_id)
    .bind(agent_ids(agents))
    .fetch_one(pool)
    .await?;
    ensure!(
        member_count == i64::try_from(agents.len())?,
        "the public group does not contain every Agent"
    );
    let dead: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM inbox_items WHERE ambient_channel_id=$1 \
         AND member_id=ANY($2) AND status='dead'",
    )
    .bind(channel_id)
    .bind(agent_ids(agents))
    .fetch_one(pool)
    .await?;
    ensure!(dead == 0, "group setup produced dead ambient Inbox Items");
    Ok(())
}

async fn assert_no_secret_role_leak(
    pool: &PgPool,
    group_channel_id: Uuid,
    agents: &[LiveAgent],
) -> Result<()> {
    let private_phrases = agents
        .iter()
        .map(|agent| secret_identity_phrase(agent.profile))
        .collect::<Vec<_>>();
    let leaked: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM messages \
         WHERE channel_id=$1 AND content_kind='text' AND body_markdown ILIKE ANY($2)",
    )
    .bind(group_channel_id)
    .bind(private_phrases)
    .fetch_one(pool)
    .await?;
    ensure!(
        leaked == 0,
        "private role rule text appeared in the public group"
    );
    Ok(())
}

async fn assert_public_thread_is_group(
    pool: &PgPool,
    group_channel_id: Uuid,
    phase: PublicPhase,
) -> Result<()> {
    let channel_id: Option<Uuid> = sqlx::query_scalar(
        "SELECT channel_id FROM messages WHERE id=$1 AND thread_id=$2 AND placement='root'",
    )
    .bind(phase.message_id)
    .bind(phase.thread_id)
    .fetch_optional(pool)
    .await?;
    ensure!(
        channel_id == Some(group_channel_id),
        "public phase Message {message_id} is not rooted in the public group",
        message_id = phase.message_id
    );
    let mentioned: i64 =
        sqlx::query_scalar("SELECT count(*) FROM message_mentions WHERE message_id=$1")
            .bind(phase.message_id)
            .fetch_one(pool)
            .await?;
    ensure!(
        mentioned == i64::try_from(AGENT_PROFILES.len())?,
        "public phase Message {message_id} did not address every Agent",
        message_id = phase.message_id
    );
    Ok(())
}

async fn assert_public_agent_participation(
    pool: &PgPool,
    group_channel_id: Uuid,
    agents: &[LiveAgent],
) -> Result<()> {
    let authors: Vec<Uuid> = sqlx::query_scalar(
        "SELECT DISTINCT author_member_id FROM messages \
         WHERE channel_id=$1 AND placement='reply' AND content_kind='text' \
           AND author_member_id=ANY($2)",
    )
    .bind(group_channel_id)
    .bind(agent_ids(agents))
    .fetch_all(pool)
    .await?;
    let missing: Vec<&'static str> = agents
        .iter()
        .filter(|agent| !authors.contains(&agent.member_id))
        .map(|agent| agent.profile.name)
        .collect();
    ensure!(
        missing.is_empty(),
        "public Werewolf group did not receive a reply from every Agent: missing={missing:?}"
    );
    Ok(())
}

async fn list_agent_agent_dm_message_ids(
    pool: &PgPool,
    space_id: Uuid,
    agents: &[LiveAgent],
) -> Result<Vec<Uuid>> {
    sqlx::query_scalar(
        "SELECT m.id FROM messages m JOIN channels c ON c.id=m.channel_id \
         WHERE m.space_id=$1 AND m.content_kind='text' AND c.kind='direct' \
           AND (SELECT count(*) FROM channel_members cm WHERE cm.channel_id=c.id)=2 \
           AND (SELECT count(*) FROM channel_members cm JOIN members peer ON peer.id=cm.member_id \
                WHERE cm.channel_id=c.id AND peer.kind='agent' AND peer.id=ANY($2))=2 \
         ORDER BY m.created_at,m.id",
    )
    .bind(space_id)
    .bind(agent_ids(agents))
    .fetch_all(pool)
    .await
    .context("list Agent-to-Agent DM messages")
}

async fn new_agent_agent_dm_messages(
    pool: &PgPool,
    space_id: Uuid,
    agents: &[LiveAgent],
    previous_ids: &[Uuid],
) -> Result<Vec<(Uuid, Uuid, Uuid)>> {
    sqlx::query_as(
        "SELECT m.id,m.channel_id,m.author_member_id FROM messages m \
         JOIN channels c ON c.id=m.channel_id \
         WHERE m.space_id=$1 AND m.content_kind='text' AND c.kind='direct' \
           AND (SELECT count(*) FROM channel_members cm WHERE cm.channel_id=c.id)=2 \
           AND (SELECT count(*) FROM channel_members cm JOIN members peer ON peer.id=cm.member_id \
                WHERE cm.channel_id=c.id AND peer.kind='agent' AND peer.id=ANY($2))=2 \
           AND m.id <> ALL($3) \
         ORDER BY m.created_at,m.id",
    )
    .bind(space_id)
    .bind(agent_ids(agents))
    .bind(previous_ids)
    .fetch_all(pool)
    .await
    .context("read new Agent-to-Agent DM messages")
}

async fn dm_peer(pool: &PgPool, channel_id: Uuid, author_id: Uuid) -> Result<Option<Uuid>> {
    sqlx::query_scalar(
        "SELECT member_id FROM channel_members WHERE channel_id=$1 AND member_id<>$2",
    )
    .bind(channel_id)
    .bind(author_id)
    .fetch_optional(pool)
    .await
    .map_err(Into::into)
}

async fn wait_for_dm_coverage(
    pool: &PgPool,
    space_id: Uuid,
    agents: &[LiveAgent],
    baseline: &[Uuid],
    timeout: Duration,
) -> Result<()> {
    let deadline = Instant::now() + timeout;
    loop {
        let messages = new_agent_agent_dm_messages(pool, space_id, agents, baseline).await?;
        let mut sent = BTreeSet::new();
        let mut received = BTreeSet::new();
        for (_, channel_id, author_id) in &messages {
            sent.insert(*author_id);
            if let Some(peer) = dm_peer(pool, *channel_id, *author_id).await? {
                received.insert(peer);
            }
        }
        let covered = agents
            .iter()
            .all(|agent| sent.contains(&agent.member_id) && received.contains(&agent.member_id));
        if covered {
            return Ok(());
        }
        if Instant::now() >= deadline {
            let diagnostics =
                dm_coverage_summary(pool, space_id, agents, baseline, &sent, &received).await?;
            ensure!(
                false,
                "Agents did not reach private DM coverage (every Agent sent and received at least one DM); {diagnostics}"
            );
        }
        tokio::time::sleep(POLL_INTERVAL).await;
    }
}

async fn dm_coverage_summary(
    pool: &PgPool,
    space_id: Uuid,
    agents: &[LiveAgent],
    baseline: &[Uuid],
    sent: &BTreeSet<Uuid>,
    received: &BTreeSet<Uuid>,
) -> Result<String> {
    let missing = agents
        .iter()
        .filter(|agent| !sent.contains(&agent.member_id) || !received.contains(&agent.member_id))
        .map(|agent| {
            format!(
                "{}: sent={},received={}",
                agent.profile.name,
                sent.contains(&agent.member_id),
                received.contains(&agent.member_id)
            )
        })
        .collect::<Vec<_>>();
    let messages = new_agent_agent_dm_messages(pool, space_id, agents, baseline).await?;
    let open_items = open_agent_dm_item_summary(pool, space_id, agents).await?;
    Ok(format!(
        "missing={missing:?}, dm_messages={}, open_items={open_items}",
        messages.len()
    ))
}

async fn assert_agent_dm_coverage(
    pool: &PgPool,
    space_id: Uuid,
    agents: &[LiveAgent],
    baseline: &[Uuid],
) -> Result<()> {
    let messages = new_agent_agent_dm_messages(pool, space_id, agents, baseline).await?;
    let mut sent = BTreeSet::new();
    let mut received = BTreeSet::new();
    for (_, channel_id, author_id) in &messages {
        sent.insert(*author_id);
        if let Some(peer) = dm_peer(pool, *channel_id, *author_id).await? {
            received.insert(peer);
        }
    }
    for agent in agents {
        ensure!(
            sent.contains(&agent.member_id),
            "Agent {} did not send an Agent-to-Agent DM",
            agent.profile.name
        );
        ensure!(
            received.contains(&agent.member_id),
            "Agent {} did not receive an Agent-to-Agent DM",
            agent.profile.name
        );
    }
    Ok(())
}

async fn wait_for_all_agent_dm_items_handled(
    pool: &PgPool,
    space_id: Uuid,
    agents: &[LiveAgent],
    timeout: Duration,
) -> Result<()> {
    let deadline = Instant::now() + timeout;
    loop {
        let open: Vec<(String, i64)> = sqlx::query_as(
            "SELECT i.status,count(*) FROM inbox_items i JOIN messages m ON m.id=i.message_id \
             JOIN channels c ON c.id=m.channel_id \
             WHERE i.space_id=$1 AND i.member_id=ANY($2) AND c.kind='direct' \
               AND (SELECT count(*) FROM channel_members cm WHERE cm.channel_id=c.id)=2 \
               AND NOT EXISTS (SELECT 1 FROM channel_members peer JOIN members pm ON pm.id=peer.member_id \
                              WHERE peer.channel_id=c.id AND pm.kind='human') \
               AND i.status<>'handled' \
             GROUP BY i.status ORDER BY i.status",
        )
        .bind(space_id)
        .bind(agent_ids(agents))
        .fetch_all(pool)
        .await?;
        if open.is_empty() {
            return Ok(());
        }
        if Instant::now() >= deadline {
            let diagnostics = open_agent_dm_item_summary(pool, space_id, agents).await?;
            ensure!(
                false,
                "Agent-to-Agent DM Inbox Items remained open: {open:?}; {diagnostics}"
            );
        }
        tokio::time::sleep(POLL_INTERVAL).await;
    }
}

async fn open_agent_dm_item_summary(
    pool: &PgPool,
    space_id: Uuid,
    agents: &[LiveAgent],
) -> Result<String> {
    let rows: Vec<OpenAgentDmItemRow> = sqlx::query_as(
        "SELECT msg.id AS message_id,msg.channel_id,author.display_name AS author_name,\
                receiver.display_name AS receiver_name,i.status AS item_status,\
                i.assigned_run_id,r.status AS run_status \
         FROM inbox_items i JOIN messages msg ON msg.id=i.message_id \
         JOIN channels c ON c.id=msg.channel_id \
         JOIN members author ON author.id=msg.author_member_id \
         JOIN members receiver ON receiver.id=i.member_id \
         LEFT JOIN agent_runs r ON r.id=i.assigned_run_id \
         WHERE i.space_id=$1 AND i.member_id=ANY($2) AND c.kind='direct' \
           AND NOT EXISTS (SELECT 1 FROM channel_members cm JOIN members peer ON peer.id=cm.member_id \
                          WHERE cm.channel_id=c.id AND peer.kind='human') \
           AND i.status<>'handled' ORDER BY msg.created_at,msg.id",
    )
    .bind(space_id)
    .bind(agent_ids(agents))
    .fetch_all(pool)
    .await?;
    let entries = rows
        .into_iter()
        .map(|row| {
            format!(
                "(message_id={},channel_id={},author={},receiver={},item_status={:?},assigned_run_id={:?},run_status={:?})",
                row.message_id,
                row.channel_id,
                row.author_name,
                row.receiver_name,
                row.item_status,
                row.assigned_run_id,
                row.run_status,
            )
        })
        .collect::<Vec<_>>();
    Ok(format!("open_items=[{}]", entries.join(",")))
}

async fn first_agent_vote(
    pool: &PgPool,
    thread_id: Uuid,
    agent: LiveAgent,
    agents: &[LiveAgent],
) -> Result<Option<(Uuid, Uuid)>> {
    let replies: Vec<(Uuid, i64)> = sqlx::query_as(
        "SELECT id,channel_seq FROM messages \
         WHERE thread_id=$1 AND author_member_id=$2 AND placement='reply' \
           AND content_kind='text' ORDER BY channel_seq",
    )
    .bind(thread_id)
    .bind(agent.member_id)
    .fetch_all(pool)
    .await?;
    for (message_id, _) in replies {
        let targets: Vec<Uuid> = sqlx::query_scalar(
            "SELECT mm.member_id FROM message_mentions mm JOIN members m ON m.id=mm.member_id \
             WHERE mm.message_id=$1 AND m.kind='agent' AND m.id<>$2",
        )
        .bind(message_id)
        .bind(agent.member_id)
        .fetch_all(pool)
        .await?;
        if targets.len() == 1
            && agents
                .iter()
                .any(|candidate| candidate.member_id == targets[0])
        {
            return Ok(Some((message_id, targets[0])));
        }
    }
    Ok(None)
}

async fn wait_for_agent_votes(
    pool: &PgPool,
    thread_id: Uuid,
    agents: &[LiveAgent],
    timeout: Duration,
) -> Result<()> {
    let deadline = Instant::now() + timeout;
    loop {
        let mut votes = Vec::with_capacity(agents.len());
        for agent in agents {
            votes.push(first_agent_vote(pool, thread_id, *agent, agents).await?);
        }
        if votes.iter().all(Option::is_some) {
            return Ok(());
        }
        if Instant::now() >= deadline {
            let missing = agents
                .iter()
                .zip(&votes)
                .filter_map(|(agent, vote)| vote.is_none().then_some(agent.profile.name))
                .collect::<Vec<_>>();
            let summary = agent_failure_summary(pool, agents).await?;
            ensure!(
                false,
                "Agents did not all cast a structured vote in Thread {thread_id}; missing={missing:?}; {summary}"
            );
        }
        tokio::time::sleep(POLL_INTERVAL).await;
    }
}

async fn assert_agent_votes(pool: &PgPool, thread_id: Uuid, agents: &[LiveAgent]) -> Result<()> {
    for agent in agents {
        let vote = first_agent_vote(pool, thread_id, *agent, agents).await?;
        let (message_id, target_id) =
            vote.with_context(|| format!("Agent {} cast no structured vote", agent.profile.name))?;
        ensure!(
            agents
                .iter()
                .any(|candidate| candidate.member_id == target_id),
            "Agent {} voted for a member that is not a live game Agent",
            agent.profile.name
        );
        ensure!(
            target_id != agent.member_id,
            "Agent {} voted for itself",
            agent.profile.name
        );
        let in_thread: i64 =
            sqlx::query_scalar("SELECT count(*) FROM messages WHERE id=$1 AND thread_id=$2")
                .bind(message_id)
                .bind(thread_id)
                .fetch_one(pool)
                .await?;
        ensure!(
            in_thread == 1,
            "Agent {} vote Message {message_id} is not in the vote Thread",
            agent.profile.name
        );
    }
    Ok(())
}

async fn wait_for_game_settled(
    pool: &PgPool,
    agents: &[LiveAgent],
    timeout: Duration,
) -> Result<()> {
    let deadline = Instant::now() + timeout;
    loop {
        let working: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM agent_runs WHERE agent_id=ANY($1) \
             AND status IN ('dispatched','working')",
        )
        .bind(agent_ids(agents))
        .fetch_one(pool)
        .await?;
        let open_hard: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM inbox_items WHERE member_id=ANY($1) \
             AND strength='hard' AND status<>'handled'",
        )
        .bind(agent_ids(agents))
        .fetch_one(pool)
        .await?;
        if working == 0 && open_hard == 0 {
            return Ok(());
        }
        if Instant::now() >= deadline {
            let summary = agent_failure_summary(pool, agents).await?;
            ensure!(
                false,
                "Werewolf Agents did not settle: working_runs={working}, open_hard_items={open_hard}; {summary}"
            );
        }
        tokio::time::sleep(POLL_INTERVAL).await;
    }
}

async fn assert_all_game_hard_items_handled(pool: &PgPool, agents: &[LiveAgent]) -> Result<()> {
    let open: Vec<(String, i64)> = sqlx::query_as(
        "SELECT kind,count(*) FROM inbox_items WHERE member_id=ANY($1) \
         AND strength='hard' AND status<>'handled' GROUP BY kind ORDER BY kind",
    )
    .bind(agent_ids(agents))
    .fetch_all(pool)
    .await?;
    ensure!(
        open.is_empty(),
        "game Hard Inbox Items remained open: {open:?}"
    );
    Ok(())
}

async fn assert_no_dead_agent_inbox_items(pool: &PgPool, agents: &[LiveAgent]) -> Result<()> {
    let dead: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM inbox_items WHERE member_id=ANY($1) AND status='dead'",
    )
    .bind(agent_ids(agents))
    .fetch_one(pool)
    .await?;
    ensure!(dead == 0, "Agent Inbox contains dead Items");
    Ok(())
}

async fn assert_all_agent_runs_terminal(pool: &PgPool, agents: &[LiveAgent]) -> Result<()> {
    let statuses: Vec<(String, i64)> = sqlx::query_as(
        "SELECT status,count(*) FROM agent_runs WHERE agent_id=ANY($1) \
         GROUP BY status ORDER BY status",
    )
    .bind(agent_ids(agents))
    .fetch_all(pool)
    .await?;
    ensure!(!statuses.is_empty(), "Werewolf Agents produced no Runs");
    ensure!(
        statuses
            .iter()
            .all(|(status, _)| matches!(status.as_str(), "completed" | "yielded")),
        "Werewolf Agents did not finish every Run successfully: {statuses:?}"
    );
    let unfinished: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM agent_runs WHERE agent_id=ANY($1) \
         AND (status NOT IN ('completed','yielded') \
              OR outcome_code NOT IN ('completed','yielded') OR finished_at IS NULL)",
    )
    .bind(agent_ids(agents))
    .fetch_one(pool)
    .await?;
    ensure!(unfinished == 0, "Werewolf has unfinished or failed Runs");
    Ok(())
}

async fn agent_failure_summary(pool: &PgPool, agents: &[LiveAgent]) -> Result<String> {
    let failed_runs: Vec<(String, i64)> = sqlx::query_as(
        "SELECT COALESCE(error_code, '<none>'),count(*) FROM agent_runs \
         WHERE agent_id=ANY($1) AND status='failed' GROUP BY error_code ORDER BY error_code",
    )
    .bind(agent_ids(agents))
    .fetch_all(pool)
    .await?;
    let inbox: Vec<(String, i64)> = sqlx::query_as(
        "SELECT status,count(*) FROM inbox_items WHERE member_id=ANY($1) \
         GROUP BY status ORDER BY status",
    )
    .bind(agent_ids(agents))
    .fetch_all(pool)
    .await?;
    Ok(format!("failed_runs={failed_runs:?}, inbox={inbox:?}"))
}

fn agent_ids(agents: &[LiveAgent]) -> Vec<Uuid> {
    agents.iter().map(|agent| agent.member_id).collect()
}

fn uuid_field(value: &Value, field: &str) -> Result<Uuid> {
    value[field]
        .as_str()
        .with_context(|| format!("Response is missing {field}"))?
        .parse()
        .with_context(|| format!("Response field {field} is not a UUID"))
}

fn short_temp_root() -> PathBuf {
    let candidate = PathBuf::from("/tmp");
    if candidate.is_dir() {
        candidate
    } else {
        std::env::temp_dir()
    }
}
