//! Live benchmark for the Builtin agent harness, compared against the codex CLI harness.
//!
//! The benchmark runs the same scripted long conversation through Sumi once per driver:
//! - `builtin`: the in-process Builtin driver backed by an OpenAI-compatible provider.
//! - `codex`: the installed codex CLI (app-server mode) driven through Sumi's codex driver.
//!
//! Both legs use the same provider endpoint and the same model (`deepseek-v4-flash` by
//! default), the same Sumi product flow, and the same scripted conversation. The first
//! message is a Channel root message and every later message is posted as a reply in the
//! same Thread, because Sumi scopes Provider Sessions per Thread and the benchmark must
//! exercise one continuous provider conversation. The test then measures three harness
//! qualities from durable artifacts:
//! - prompt cache rate: `cached_input_tokens / input_tokens` per inference call;
//! - context compression: compaction count, reason, and source/summary token estimates;
//! - long-conversation focus: probe accuracy, ledger completeness/duplicates, and forbidden
//!   `/tmp` attempts.
//!
//! Builtin metrics come from the persisted provider Session (per-call usage plus compaction
//! records). Codex metrics come from the codex CLI rollout JSONL (`event_msg` token_count
//! events and `compacted` lines). The report is written as JSON and Markdown under
//! `SUMI_HARNESS_REPORT_DIR` (default `target/harness-report`).
//!
//! Environment:
//! - `SUMI_HARNESS_BUILTIN_API_BASE` (default `https://opencode.ai/zen/go/v1`)
//! - `SUMI_HARNESS_BUILTIN_MODEL` (default `deepseek-v4-flash`)
//! - `SUMI_HARNESS_BUILTIN_TOKEN` (required for the builtin leg)
//! - `SUMI_TEST_CODEX_HOME` (required for the codex leg; points at a codex profile whose
//!   config.toml/auth.json select the same provider and model)
//! - `SUMI_HARNESS_DRIVER` (`builtin`, `codex`, or `both`; default `both`)
//! - `SUMI_HARNESS_REPORT_DIR` (default `target/harness-report`)
//! - `SUMI_HARNESS_BUILTIN_CONTEXT_WINDOW` (default `16000`; the builtin compaction trigger
//!   is derived from this window so the benchmark deterministically exercises compression)
//! - `SUMI_HARNESS_COMPACTION_RATIO` (default `0.75`)
//! - `SUMI_HARNESS_KEEP_RECENT_TOKENS` (default `8000`; recent context tokens kept
//!   unsummarized by compaction, mirroring codex and pi)
//! - `SUMI_HARNESS_ENFORCE_THRESHOLDS` (`1` gates on quality thresholds; default off)
//! - `SUMI_HARNESS_MIN_CACHE_RATE` (default `0.3`)
//! - `SUMI_HARNESS_MIN_PROBE_ACCURACY` (default `0.6`)
//!
//! Output never contains Message, Attachment, Memory, or Secret bodies; only keys, values,
//! counts, and ratios are reported.

mod support;

use std::{
    collections::{BTreeMap, HashSet},
    net::SocketAddr,
    path::{Path, PathBuf},
    time::{Duration, Instant},
};

use anyhow::{Context, Result, bail, ensure};
use reqwest::{Client, StatusCode, header};
use serde::Serialize;
use serde_json::Value;
use sqlx::PgPool;
use tempfile::{TempDir, tempdir};
use url::Url;
use uuid::Uuid;

use support::{
    HarnessBuiltinConfig, SumiProcess, TestDatabase, confirm_pairing, create_space,
    pairing_url_from_daemon, register_with, reserve_local_port, short_temp_root,
    spawn_default_codex_computer, spawn_server, wait_for_computer_status_for, wait_for_health,
    write_harness_computer_config, write_server_config,
};

const AGENT_READY_TIMEOUT: Duration = Duration::from_secs(300);
const RUN_TIMEOUT: Duration = Duration::from_secs(1200);
const RUN_RETRY_WINDOW: Duration = Duration::from_secs(300);
const POLL_INTERVAL: Duration = Duration::from_millis(500);
const LEDGER_HEADER: &str = "# Northstar Ledger";
const EXPECTED_LEDGER_ENTRIES: [&str; 7] = [
    "1 setup done",
    "2 meeting01 done",
    "3 reference01 done",
    "4 meeting02 done",
    "5 reference02 done",
    "6 meeting03 done",
    "7 wrapup done",
];

#[derive(Clone, Copy, Debug)]
struct DriverProfile {
    name: &'static str,
    driver: &'static str,
    channel_slug: &'static str,
}

const DRIVER_PROFILES: [DriverProfile; 2] = [
    DriverProfile {
        name: "BeiChen",
        driver: "builtin",
        channel_slug: "harness-builtin",
    },
    DriverProfile {
        name: "BeiChenCx",
        driver: "codex",
        channel_slug: "harness-codex",
    },
];

#[derive(Clone, Debug)]
struct ScenarioStep {
    text: String,
    probe: Option<(&'static str, &'static str)>,
}

struct ScenarioPaths {
    ledger: &'static str,
    notes: &'static str,
}

fn scenario_paths(driver: &str) -> ScenarioPaths {
    match driver {
        // The codex driver runs with cwd already set to the agent workspace.
        "codex" => ScenarioPaths {
            ledger: "ledger.md",
            notes: "notes/",
        },
        // The builtin shell starts at the agent home root.
        _ => ScenarioPaths {
            ledger: "workspace/ledger.md",
            notes: "workspace/notes/",
        },
    }
}

#[derive(Clone, Debug, Default, Serialize)]
struct CompactionReport {
    reason: String,
    through: usize,
    source_tokens: usize,
    summary_tokens: usize,
    split_turn: bool,
    kept_tokens: usize,
}

#[derive(Clone, Debug, Default, Serialize)]
struct ProbeResult {
    key: String,
    expected: String,
    matched: bool,
}

#[derive(Clone, Debug, Default, Serialize)]
struct DriverReport {
    driver: String,
    model: String,
    provider_base: String,
    context_window_tokens: usize,
    compaction_trigger_ratio: f64,
    runs_completed: usize,
    failed_runs: usize,
    total_input_tokens: i64,
    total_output_tokens: i64,
    cached_input_tokens: i64,
    cache_rate: f64,
    cache_hit_calls: usize,
    total_calls: usize,
    compactions: usize,
    compaction_reasons: BTreeMap<String, usize>,
    compaction_records: Vec<CompactionReport>,
    compression_ratio_avg: Option<f64>,
    final_projected_context_tokens: Option<usize>,
    probes_correct: usize,
    probes_total: usize,
    probe_accuracy: f64,
    probe_results: Vec<ProbeResult>,
    ledger_lines: usize,
    ledger_duplicates: usize,
    ledger_missing: usize,
    tmp_attempts: usize,
}

#[derive(Clone, Debug, Default, Serialize)]
struct HarnessReport {
    model: String,
    provider_base: String,
    drivers: BTreeMap<String, DriverReport>,
}

#[tokio::test]
#[ignore = "requires a live deepseek-v4-flash provider and the codex CLI"]
async fn builtin_harness_benchmark_measures_cache_compression_and_focus() -> Result<()> {
    let database = TestDatabase::create("sumi_harness_benchmark").await?;
    let result = run_harness(&database).await;
    database.drop().await?;
    result
}

async fn run_harness(database: &TestDatabase) -> Result<()> {
    let profiles = selected_drivers()?;
    let builtin = builtin_config()?;
    let codex_home = std::env::var_os("SUMI_TEST_CODEX_HOME")
        .filter(|value| !value.is_empty())
        .map(PathBuf::from);
    ensure!(
        profiles.iter().all(|profile| profile.driver == "builtin") || codex_home.is_some(),
        "SUMI_TEST_CODEX_HOME is required for the codex leg"
    );
    let report_dir = std::env::var("SUMI_HARNESS_REPORT_DIR")
        .unwrap_or_else(|_| "target/harness-report".to_owned());
    let enforce = std::env::var("SUMI_HARNESS_ENFORCE_THRESHOLDS").as_deref() == Ok("1");
    let min_cache_rate = env_parse("SUMI_HARNESS_MIN_CACHE_RATE", 0.3)?;
    let min_probe_accuracy = env_parse("SUMI_HARNESS_MIN_PROBE_ACCURACY", 0.6)?;

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
        "HarnessOwner",
        &format!("harness-{}@example.test", Uuid::now_v7()),
    )
    .await?;
    let space = create_space(&client, &server_url, &owner).await?;

    let state_root = TempDir::with_prefix_in("sumi-harness-", short_temp_root()?)?;
    let state_dir = state_root.path().join("computer");
    std::fs::create_dir(&state_dir)?;
    let computer_config = root.path().join("computer.toml");
    write_harness_computer_config(
        &computer_config,
        &server_url,
        &state_dir,
        1,
        &builtin,
        codex_home.as_deref(),
    )?;
    let mut computer = spawn_default_codex_computer(&computer_config)?;
    let pairing_url = pairing_url_from_daemon(&mut computer)
        .await
        .context("computer daemon did not emit a pairing URL")?;
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
    .context("computer did not become online")?;

    let computer_home = state_dir
        .join(space.id.to_string())
        .join(paired.id.to_string());
    let pool = PgPool::connect(&database.url).await?;
    let steps = scenario(profiles.first().context("no selected drivers")?.driver);
    let mut report = HarnessReport {
        model: builtin.model.clone(),
        provider_base: builtin.api_base.clone(),
        drivers: BTreeMap::new(),
    };

    for profile in &profiles {
        eprintln!("HARNESS starting driver={}", profile.driver);
        let agent_id =
            create_agent(&client, &server_url, &owner, space.id, paired.id, *profile).await?;
        wait_for_agent_ready(&client, &server_url, &owner, space.id, agent_id, *profile).await?;
        let channel_id =
            create_harness_channel(&client, &server_url, &owner, space.id, agent_id, *profile)
                .await?;

        let mut last_run_id = None;
        let mut failed_runs = 0;
        let mut thread_id = None;
        for (index, step) in steps.iter().enumerate() {
            let message_id = if index == 0 {
                post_message(&client, &server_url, &owner, channel_id, agent_id, step).await?
            } else {
                post_thread_message(
                    &client,
                    &server_url,
                    &owner,
                    thread_id.context("scenario Thread is missing")?,
                    agent_id,
                    step,
                )
                .await?
            };
            if index == 0 {
                thread_id = Some(message_thread_id(&pool, message_id).await?);
            }
            let (run_id, had_failure) = wait_for_run(
                &pool,
                agent_id,
                last_run_id,
                *profile,
                &mut server,
                &mut computer,
            )
            .await?;
            failed_runs += usize::from(had_failure);
            if let Some(run_id) = run_id {
                last_run_id = Some(run_id);
            }
        }
        let replies = agent_replies(&pool, channel_id, agent_id).await?;
        let ledger_path = computer_home
            .join("agents")
            .join(agent_id.to_string())
            .join("workspace")
            .join("ledger.md");
        let driver_report = match profile.driver {
            "builtin" => {
                let agent_home = computer_home.join("agents").join(agent_id.to_string());
                let mut driver_report = builtin_metrics(
                    &agent_home,
                    profile.driver,
                    &builtin,
                    &steps,
                    &replies,
                    &ledger_path,
                    &computer,
                )
                .await?;
                driver_report.runs_completed = completed_run_count(&pool, agent_id).await?;
                driver_report.failed_runs = failed_runs;
                driver_report
            }
            "codex" => {
                let agent_home = computer_home.join("agents").join(agent_id.to_string());
                let mut driver_report = codex_metrics(
                    &agent_home,
                    profile.driver,
                    &builtin,
                    &steps,
                    &replies,
                    &ledger_path,
                )
                .await?;
                driver_report.runs_completed = completed_run_count(&pool, agent_id).await?;
                driver_report.failed_runs = failed_runs;
                driver_report
            }
            other => bail!("unsupported driver {other}"),
        };
        eprintln!(
            "HARNESS completed driver={} cache_rate={:.3} compactions={} probes={}/{} ledger_lines={} tmp_attempts={}",
            profile.driver,
            driver_report.cache_rate,
            driver_report.compactions,
            driver_report.probes_correct,
            driver_report.probes_total,
            driver_report.ledger_lines,
            driver_report.tmp_attempts,
        );
        report
            .drivers
            .insert(profile.driver.to_owned(), driver_report);
    }

    std::fs::create_dir_all(&report_dir)?;
    let json_path = Path::new(&report_dir).join("report.json");
    std::fs::write(&json_path, serde_json::to_string_pretty(&report)?)?;
    let markdown = render_markdown(&report);
    std::fs::write(Path::new(&report_dir).join("report.md"), markdown)?;
    eprintln!("HARNESS report written to {}", json_path.display());

    assert_hard(
        &report,
        &steps,
        &profiles,
        enforce,
        min_cache_rate,
        min_probe_accuracy,
    )?;

    pool.close().await;
    // The benchmark measurements are complete here; shutdown is best-effort so a slow
    // daemon (for example one still draining a codex app-server) cannot fail the run.
    if let Err(error) = computer.interrupt().await {
        eprintln!("HARNESS computer shutdown warning: {error}");
    }
    if let Err(error) = server.interrupt().await {
        eprintln!("HARNESS server shutdown warning: {error}");
    }
    Ok(())
}

fn selected_drivers() -> Result<Vec<DriverProfile>> {
    let raw = std::env::var("SUMI_HARNESS_DRIVER").unwrap_or_else(|_| "both".to_owned());
    let selected = DRIVER_PROFILES
        .iter()
        .copied()
        .filter(|profile| raw == "both" || profile.driver == raw)
        .collect::<Vec<_>>();
    ensure!(
        !selected.is_empty(),
        "SUMI_HARNESS_DRIVER must be builtin, codex, or both"
    );
    Ok(selected)
}

fn builtin_config() -> Result<HarnessBuiltinConfig> {
    let api_base = std::env::var("SUMI_HARNESS_BUILTIN_API_BASE")
        .unwrap_or_else(|_| "https://opencode.ai/zen/go/v1".to_owned());
    let model = std::env::var("SUMI_HARNESS_BUILTIN_MODEL")
        .unwrap_or_else(|_| "deepseek-v4-flash".to_owned());
    let token = std::env::var("SUMI_HARNESS_BUILTIN_TOKEN")
        .context("SUMI_HARNESS_BUILTIN_TOKEN is required for the builtin leg")?;
    let context_window_tokens = env_parse("SUMI_HARNESS_BUILTIN_CONTEXT_WINDOW", 16_000usize)?;
    let compaction_trigger_ratio = env_parse("SUMI_HARNESS_COMPACTION_RATIO", 0.75f64)?;
    let compaction_keep_recent_tokens = env_parse("SUMI_HARNESS_KEEP_RECENT_TOKENS", 8_000usize)?;
    ensure!(
        (0.0..=1.0).contains(&compaction_trigger_ratio) && compaction_trigger_ratio > 0.0,
        "SUMI_HARNESS_COMPACTION_RATIO must be in (0, 1]"
    );
    Ok(HarnessBuiltinConfig {
        api_base,
        model,
        token,
        context_window_tokens,
        compaction_trigger_ratio,
        compaction_keep_recent_tokens,
    })
}

fn env_parse<T: std::str::FromStr>(name: &str, default: T) -> Result<T> {
    match std::env::var(name) {
        Ok(raw) => raw
            .parse::<T>()
            .map_err(|_| anyhow::anyhow!("{name} must be a number")),
        Err(_) => Ok(default),
    }
}

fn scenario(driver: &str) -> Vec<ScenarioStep> {
    let paths = scenario_paths(driver);
    let brief = concat!(
        "你是项目「北辰」(Northstar) 的项目助理，从今天开始维护项目台账和资料。规则：\n",
        "1. 事实性查询的回复第一行必须是 `RESULT <key>=<value>`。\n",
        "2. 台账文件是 {ledger}；每完成一个任务就追加一行 `<n> <task> done`，n 从 1 递增，禁止重复追加。\n",
        "3. 资料写入 {notes}，禁止写 /tmp，禁止使用绝对路径。\n",
        "项目事实：代号 northstar；P1 原型 2026-09-01；P2 内测 2026-11-15；P3 公测 2027-02-28；",
        "预算上限 480000 RMB；成员 林一(owner)、周舟(frontend)、郑远(backend)、苏晴(design)、何川(ops)。\n",
        "先回复「收到」，不要创建任何文件。\n",
    )
    .replace("{ledger}", paths.ledger)
    .replace("{notes}", paths.notes);
    let meeting01 = format!(
        "把下面的会议纪要原样保存为 {}meeting-01.md，然后给台账追加 `2 meeting01 done`。\n{}",
        paths.notes,
        meeting_doc(
            "Meeting 01",
            &[
                ("D1", "adopt option A for auth"),
                ("Scope", "no billing changes")
            ],
            2_500
        )
    );
    let reference01 = format!(
        "把下面的参考资料原样保存为 {}reference-01.md，然后给台账追加 `3 reference01 done`。\n{}",
        paths.notes,
        reference_doc(
            "Reference 01",
            &[
                ("Frontend owner", "ZhouZhou"),
                ("Release window", "2026-12-01"),
                ("Uptime target", "99.95"),
                ("Primary region", "ap-shanghai"),
            ],
            8_000,
        )
    );
    let meeting02 = format!(
        "把下面的会议纪要原样保存为 {}meeting-02.md，然后给台账追加 `4 meeting02 done`。\n{}",
        paths.notes,
        meeting_doc(
            "Meeting 02",
            &[("D2", "cache TTL 300s"), ("Retry policy", "3 attempts")],
            2_500
        )
    );
    let reference02 = format!(
        "把下面的参考资料原样保存为 {}reference-02.md，然后给台账追加 `5 reference02 done`。\n{}",
        paths.notes,
        reference_doc(
            "Reference 02",
            &[
                ("Incident owner", "HeChuan"),
                ("Rollback plan", "staged by service group"),
                ("Pager rotation", "24x7"),
            ],
            8_000,
        )
    );
    let meeting03 = format!(
        "把下面的会议纪要原样保存为 {}meeting-03.md，然后给台账追加 `6 meeting03 done`。\n{}",
        paths.notes,
        meeting_doc(
            "Meeting 03",
            &[("D3", "use Postgres 17"), ("Migration", "forward only")],
            2_500
        )
    );

    vec![
        ScenarioStep {
            text: brief,
            probe: None,
        },
        ScenarioStep {
            text: format!(
                "现在初始化台账：创建 {ledger}，第一行写 `{LEDGER_HEADER}`，然后追加 `1 setup done`。直接执行，不需要确认。",
                ledger = paths.ledger,
            ),
            probe: None,
        },
        ScenarioStep {
            text: meeting01,
            probe: None,
        },
        ScenarioStep {
            text: reference01,
            probe: None,
        },
        ScenarioStep {
            text: meeting02,
            probe: None,
        },
        ScenarioStep {
            text: "不算台账任务。单独回答：1 到 100 之间有多少个质数？只回复数字，不要创建或修改任何文件。".to_owned(),
            probe: None,
        },
        ScenarioStep {
            text: reference02,
            probe: None,
        },
        ScenarioStep {
            text: "回答一个问题，第一行按约定写 `RESULT phase2_date=<值>`，不要修改任何文件：阶段二(P2)的内测日期是哪一天？".to_owned(),
            probe: Some(("phase2_date", "2026-11-15")),
        },
        ScenarioStep {
            text: meeting03,
            probe: None,
        },
        ScenarioStep {
            text: "回答一个问题，第一行按约定写 `RESULT frontend_owner=<值>`，不要修改任何文件：负责前端的成员是谁？".to_owned(),
            probe: Some(("frontend_owner", "ZhouZhou")),
        },
        ScenarioStep {
            text: "回答一个问题，第一行按约定写 `RESULT budget_cap=<值>`，不要修改任何文件：项目预算上限是多少？".to_owned(),
            probe: Some(("budget_cap", "480000")),
        },
        ScenarioStep {
            text: format!(
                "给台账 {ledger} 追加 `7 wrapup done`。如果这一行已经存在，就不要再追加，也不要重复其他行；然后只回复「ledger ready」。",
                ledger = paths.ledger,
            ),
            probe: None,
        },
        ScenarioStep {
            text: "回答一个问题，第一行按约定写 `RESULT meeting3_db=<值>`，不要修改任何文件：第三份会议纪要里关于数据库的决定是什么？".to_owned(),
            probe: Some(("meeting3_db", "postgres17")),
        },
        ScenarioStep {
            text: "回答一个问题，第一行按约定写 `RESULT incident_owner=<值>`，不要修改任何文件：reference-02 里记录的故障负责人是谁？".to_owned(),
            probe: Some(("incident_owner", "HeChuan")),
        },
        ScenarioStep {
            text: "不贴台账正文。只回复两行：第一行是台账总行数，第二行是每行序号，用逗号分隔。".to_owned(),
            probe: None,
        },
    ]
}

fn reference_doc(title: &str, facts: &[(&str, &str)], target_chars: usize) -> String {
    let filler = "The northstar reference registry keeps a canonical copy of every operational fact for the project. Each entry is immutable unless the owner records a change in the ledger, and every service group reads the same registry so decisions stay consistent across teams. ";
    let mut out = format!("# {title}\n\nProject Northstar reference.\n\n");
    for (key, value) in facts {
        out.push_str(&format!("## {key}\n{value}\n\n"));
    }
    while out.len() < target_chars {
        out.push_str(filler);
    }
    out
}

fn meeting_doc(title: &str, decisions: &[(&str, &str)], target_chars: usize) -> String {
    let filler = "Minutes record the decisions the group agreed on and the constraints that follow. The owner distributes the minutes after the call, and the assistant stores them under notes for the whole team. ";
    let mut out = format!("# {title}\n\nProject Northstar meeting minutes.\n\n");
    for (key, value) in decisions {
        out.push_str(&format!("## {key}\n{value}\n\n"));
    }
    while out.len() < target_chars {
        out.push_str(filler);
    }
    out
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
        "create {} Agent returned {}",
        profile.driver,
        response.status()
    );
    let body: Value = response.json().await?;
    uuid_field(&body, "member_id")
}

fn role_text(profile: DriverProfile) -> String {
    let paths = scenario_paths(profile.driver);
    format!(
        concat!(
            "你是项目「北辰」的项目助理{}，负责维护台账 {} 和 {} 下的资料。",
            "事实性查询第一行必须按 `RESULT <key>=<value>` 回答；完成任务后按 `<n> <task> done` 追加台账；",
            "禁止写 /tmp；不要重复追加同一行。",
        ),
        if profile.driver == "builtin" {
            "（Builtin）"
        } else {
            "（Codex）"
        },
        paths.ledger,
        paths.notes.trim_end_matches('/'),
    )
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

async fn create_harness_channel(
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
            "topic": "北辰项目助理",
            "agent_member_ids": [agent_id],
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "create harness Channel returned {}",
        response.status()
    );
    let body: Value = response.json().await?;
    uuid_field(&body, "id")
}

async fn post_message(
    client: &Client,
    server: &Url,
    owner: &str,
    channel_id: Uuid,
    agent_id: Uuid,
    step: &ScenarioStep,
) -> Result<Uuid> {
    let response = client
        .post(server.join(&format!("/api/v1/channels/{channel_id}/messages"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, owner)
        .json(&serde_json::json!({
            "body_markdown": step.text,
            "mentions": [agent_id],
            "mention_all": false,
            "attachment_ids": [],
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "post harness message returned {}",
        response.status()
    );
    let body: Value = response.json().await?;
    uuid_field(&body, "id")
}

async fn post_thread_message(
    client: &Client,
    server: &Url,
    owner: &str,
    thread_id: Uuid,
    agent_id: Uuid,
    step: &ScenarioStep,
) -> Result<Uuid> {
    let response = client
        .post(server.join(&format!("/api/v1/threads/{thread_id}/messages"))?)
        .header("idempotency-key", Uuid::now_v7().to_string())
        .header(header::COOKIE, owner)
        .json(&serde_json::json!({
            "body_markdown": step.text,
            "mentions": [agent_id],
            "mention_all": false,
            "attachment_ids": [],
        }))
        .send()
        .await?;
    ensure!(
        response.status() == StatusCode::CREATED,
        "post harness Thread message returned {}",
        response.status()
    );
    let body: Value = response.json().await?;
    uuid_field(&body, "id")
}

async fn message_thread_id(pool: &PgPool, message_id: Uuid) -> Result<Uuid> {
    sqlx::query_scalar("SELECT thread_id FROM messages WHERE id=$1")
        .bind(message_id)
        .fetch_one(pool)
        .await
        .context("read scenario message Thread")
}

async fn wait_for_run(
    pool: &PgPool,
    agent_id: Uuid,
    previous: Option<Uuid>,
    profile: DriverProfile,
    server: &mut SumiProcess,
    computer: &mut SumiProcess,
) -> Result<(Option<Uuid>, bool)> {
    let deadline = Instant::now() + RUN_TIMEOUT;
    let mut retry_deadline = None;
    loop {
        server.ensure_running()?;
        computer.ensure_running()?;
        let run = sqlx::query_as::<_, (Uuid, String, Option<String>, Option<String>)>(
            "SELECT id, status, outcome_code, error_code FROM agent_runs \
             WHERE agent_id=$1 ORDER BY created_at DESC, id DESC LIMIT 1",
        )
        .bind(agent_id)
        .fetch_optional(pool)
        .await?;
        if let Some((run_id, status, outcome, error)) = run {
            if status == "failed" {
                if retry_deadline.is_none() {
                    eprintln!(
                        "HARNESS run failed driver={} run={run_id} outcome={outcome:?} error={error:?}; waiting for the server retry",
                        profile.driver
                    );
                    retry_deadline = Some(Instant::now() + RUN_RETRY_WINDOW);
                }
            } else if (status == "completed" || status == "yielded") && previous != Some(run_id) {
                return Ok((Some(run_id), retry_deadline.is_some()));
            }
        }
        if let Some(retry_deadline) = retry_deadline
            && Instant::now() >= retry_deadline
        {
            eprintln!(
                "HARNESS no retry run for driver={}; continuing with the next scenario step",
                profile.driver
            );
            return Ok((None, true));
        }
        ensure!(
            Instant::now() < deadline,
            "timed out waiting for {} harness Run; inspect server/computer logs",
            profile.driver
        );
        tokio::time::sleep(POLL_INTERVAL).await;
    }
}

async fn completed_run_count(pool: &PgPool, agent_id: Uuid) -> Result<usize> {
    let count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM agent_runs \
         WHERE agent_id=$1 AND status IN ('completed', 'yielded')",
    )
    .bind(agent_id)
    .fetch_one(pool)
    .await?;
    Ok(count as usize)
}

async fn agent_replies(pool: &PgPool, channel_id: Uuid, agent_id: Uuid) -> Result<Vec<String>> {
    Ok(sqlx::query_scalar(
        "SELECT body_markdown FROM messages \
         WHERE channel_id=$1 AND author_member_id=$2 AND content_kind='text' \
         ORDER BY channel_seq",
    )
    .bind(channel_id)
    .bind(agent_id)
    .fetch_all(pool)
    .await?)
}

async fn builtin_metrics(
    agent_home: &Path,
    driver: &str,
    builtin: &HarnessBuiltinConfig,
    steps: &[ScenarioStep],
    replies: &[String],
    ledger_path: &Path,
    computer: &SumiProcess,
) -> Result<DriverReport> {
    let session_files = find_files(agent_home.join("drivers/builtin/sessions"), "json")?;
    ensure!(
        !session_files.is_empty(),
        "no builtin session artifact found"
    );
    let mut best = None;
    for session_file in &session_files {
        let Ok(value) = serde_json::from_str::<Value>(
            &std::fs::read_to_string(session_file).unwrap_or_default(),
        ) else {
            continue;
        };
        let message_count = value["messages"]
            .as_array()
            .map_or(0, |messages| messages.len());
        if best
            .as_ref()
            .is_none_or(|(_, best_count)| message_count > *best_count)
        {
            best = Some((session_file.clone(), message_count));
        }
    }
    let (session, _) = best.context("no parseable builtin session artifact")?;
    let value: Value = serde_json::from_str(
        &std::fs::read_to_string(&session)
            .with_context(|| format!("read builtin session {}", session.display()))?,
    )
    .context("parse builtin session")?;
    let messages = value["messages"].as_array().context("builtin messages")?;
    let mut report = base_report(driver, builtin, steps, replies, ledger_path)?;

    for message in messages {
        let Some(usage) = message["usage"].as_object() else {
            continue;
        };
        let input = usage["input_tokens"].as_i64().unwrap_or(0);
        let cached = usage["cached_input_tokens"].as_i64().unwrap_or(0);
        report.total_input_tokens += input;
        report.total_output_tokens += usage["output_tokens"].as_i64().unwrap_or(0);
        report.cached_input_tokens += cached;
        report.total_calls += 1;
        if cached > 0 {
            report.cache_hit_calls += 1;
        }
    }
    if report.total_input_tokens > 0 {
        report.cache_rate = report.cached_input_tokens as f64 / report.total_input_tokens as f64;
    }

    let metadata = value["metadata"].as_object().cloned().unwrap_or_default();
    let compaction = metadata["compaction"].as_object();
    let through = compaction
        .and_then(|compaction| compaction["through"].as_u64())
        .unwrap_or(0) as usize;
    let summary = compaction
        .and_then(|compaction| compaction["summary"].as_str())
        .unwrap_or_default();
    let retained = messages.get(through..).unwrap_or_default();
    report.final_projected_context_tokens =
        Some(summary.len().div_ceil(4) + estimate_messages(retained));

    if let Some(records) = metadata["compactions"].as_array() {
        for record in records {
            let source_tokens = record["source_tokens"].as_u64().unwrap_or(0) as usize;
            let summary_tokens = record["summary_tokens"].as_u64().unwrap_or(0) as usize;
            let reason = record["reason"].as_str().unwrap_or_default().to_owned();
            report.compactions += 1;
            *report.compaction_reasons.entry(reason.clone()).or_default() += 1;
            report.compaction_records.push(CompactionReport {
                reason,
                through: record["through"].as_u64().unwrap_or(0) as usize,
                source_tokens,
                summary_tokens,
                split_turn: record["split_turn"].as_bool().unwrap_or(false),
                kept_tokens: record["kept_tokens"].as_u64().unwrap_or(0) as usize,
            });
        }
    }
    let compressive = report
        .compaction_records
        .iter()
        .filter(|record| record.source_tokens > record.summary_tokens)
        .count();
    if compressive > 0 {
        report.compression_ratio_avg = Some(
            report
                .compaction_records
                .iter()
                .map(|record| {
                    (record.source_tokens - record.summary_tokens) as f64
                        / record.source_tokens as f64
                })
                .sum::<f64>()
                / report.compaction_records.len() as f64,
        );
    }
    report.tmp_attempts = count_tmp_attempts(computer);
    Ok(report)
}

async fn codex_metrics(
    agent_home: &Path,
    driver: &str,
    builtin: &HarnessBuiltinConfig,
    steps: &[ScenarioStep],
    replies: &[String],
    ledger_path: &Path,
) -> Result<DriverReport> {
    let mut report = base_report(driver, builtin, steps, replies, ledger_path)?;
    let mut rollouts = find_files(agent_home.join("drivers/codex/sessions"), "jsonl")?;
    rollouts.sort();
    let mut before_total = 0_i64;
    let mut before_calls = 0_usize;
    let mut after_total = 0_i64;
    let mut after_calls = 0_usize;
    let mut compacted = false;
    for rollout in rollouts {
        let contents = std::fs::read_to_string(&rollout)
            .with_context(|| format!("read codex rollout {}", rollout.display()))?;
        for line in contents.lines() {
            let Ok(event) = serde_json::from_str::<Value>(line) else {
                continue;
            };
            match event["type"].as_str() {
                Some("event_msg") if event["payload"]["type"].as_str() == Some("token_count") => {
                    let info = &event["payload"]["info"];
                    let usage = info["last_token_usage"]
                        .as_object()
                        .or_else(|| info["total_token_usage"].as_object());
                    let Some(usage) = usage else {
                        continue;
                    };
                    let input = usage["input_tokens"].as_i64().unwrap_or(0);
                    let cached = usage["cached_input_tokens"].as_i64().unwrap_or(0);
                    report.total_input_tokens += input;
                    report.total_output_tokens += usage["output_tokens"].as_i64().unwrap_or(0);
                    report.cached_input_tokens += cached;
                    report.total_calls += 1;
                    if cached > 0 {
                        report.cache_hit_calls += 1;
                    }
                    if compacted {
                        after_total += input;
                        after_calls += 1;
                    } else {
                        before_total += input;
                        before_calls += 1;
                    }
                }
                Some("compacted") => {
                    if !compacted {
                        compacted = true;
                    }
                    report.compactions += 1;
                    *report
                        .compaction_reasons
                        .entry("codex_auto".to_owned())
                        .or_default() += 1;
                    if let Some(message) = event["payload"]["message"].as_str() {
                        report.compaction_records.push(CompactionReport {
                            reason: "codex_auto".to_owned(),
                            through: 0,
                            source_tokens: 0,
                            summary_tokens: message.len().div_ceil(4),
                            split_turn: false,
                            kept_tokens: 0,
                        });
                    }
                }
                _ => {}
            }
        }
    }
    if report.total_input_tokens > 0 {
        report.cache_rate = report.cached_input_tokens as f64 / report.total_input_tokens as f64;
    }
    if before_calls > 0 && after_calls > 0 {
        let before_avg = before_total as f64 / before_calls as f64;
        let after_avg = after_total as f64 / after_calls as f64;
        report.compression_ratio_avg = Some(1.0 - after_avg / before_avg);
    }
    Ok(report)
}

fn base_report(
    driver: &str,
    builtin: &HarnessBuiltinConfig,
    steps: &[ScenarioStep],
    replies: &[String],
    ledger_path: &Path,
) -> Result<DriverReport> {
    let (probes_correct, probes_total, probe_results) = grade_probes(steps, replies);
    let (ledger_lines, ledger_duplicates, ledger_missing) = ledger_metrics(ledger_path)?;
    Ok(DriverReport {
        driver: driver.to_owned(),
        model: builtin.model.clone(),
        provider_base: builtin.api_base.clone(),
        context_window_tokens: builtin.context_window_tokens,
        compaction_trigger_ratio: builtin.compaction_trigger_ratio,
        probes_correct,
        probes_total,
        probe_results,
        probe_accuracy: if probes_total > 0 {
            probes_correct as f64 / probes_total as f64
        } else {
            0.0
        },
        ledger_lines,
        ledger_duplicates,
        ledger_missing,
        ..DriverReport::default()
    })
}

fn grade_probes(steps: &[ScenarioStep], replies: &[String]) -> (usize, usize, Vec<ProbeResult>) {
    let mut correct = 0;
    let mut total = 0;
    let mut results = Vec::new();
    for step in steps {
        let Some((key, expected)) = step.probe else {
            continue;
        };
        total += 1;
        let expected = normalize(expected);
        let key = normalize(key);
        let found = replies.iter().any(|body| {
            body.lines().any(|line| {
                let normalized = normalize(line);
                let Some(rest) = normalized.strip_prefix("result") else {
                    return false;
                };
                let Some((found_key, value)) = rest.split_once('=') else {
                    return false;
                };
                found_key.trim() == key && value.trim() == expected
            })
        });
        if found {
            correct += 1;
        }
        results.push(ProbeResult {
            key: key.to_owned(),
            expected: expected.to_owned(),
            matched: found,
        });
    }
    (correct, total, results)
}

fn normalize(value: &str) -> String {
    value
        .trim()
        .to_lowercase()
        .chars()
        .filter(|character| !character.is_whitespace())
        .collect()
}

fn ledger_metrics(ledger_path: &Path) -> Result<(usize, usize, usize)> {
    if !ledger_path.is_file() {
        return Ok((0, 0, EXPECTED_LEDGER_ENTRIES.len()));
    }
    let content = std::fs::read_to_string(ledger_path)?;
    let lines = content
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty())
        .collect::<Vec<_>>();
    let entries = lines.iter().skip(1).copied().collect::<Vec<_>>();
    let unique = entries.iter().copied().collect::<HashSet<_>>();
    let missing = EXPECTED_LEDGER_ENTRIES
        .iter()
        .filter(|expected| !unique.contains(**expected))
        .count();
    Ok((lines.len(), entries.len() - unique.len(), missing))
}

fn count_tmp_attempts(computer: &SumiProcess) -> usize {
    computer
        .log_text()
        .lines()
        .filter(|line| line.contains("Builtin tool failed") && line.contains("/tmp"))
        .count()
}

fn estimate_messages(messages: &[Value]) -> usize {
    messages
        .iter()
        .map(|message| serde_json::to_string(message).map_or(0, |value| value.len()))
        .sum::<usize>()
        .div_ceil(4)
}

fn find_files(directory: PathBuf, extension: &str) -> Result<Vec<PathBuf>> {
    let mut found = Vec::new();
    if !directory.is_dir() {
        return Ok(found);
    }
    for entry in walk(&directory)? {
        if entry.extension().is_some_and(|ext| ext == extension) {
            found.push(entry);
        }
    }
    Ok(found)
}

fn walk(directory: &Path) -> Result<Vec<PathBuf>> {
    let mut paths = Vec::new();
    for entry in std::fs::read_dir(directory)? {
        let entry = entry?;
        let path = entry.path();
        if path.is_dir() {
            paths.extend(walk(&path)?);
        } else {
            paths.push(path);
        }
    }
    Ok(paths)
}

fn render_markdown(report: &HarnessReport) -> String {
    let mut out = format!(
        "# Builtin harness benchmark\n\nModel: {} · Provider: {}\n\n",
        report.model, report.provider_base
    );
    let metrics = [
        ("runs completed", "runs_completed"),
        ("failed runs", "failed_runs"),
        ("total input tokens", "total_input_tokens"),
        ("cached input tokens", "cached_input_tokens"),
        ("cache rate", "cache_rate"),
        ("cache hit calls", "cache_hit_calls"),
        ("total calls", "total_calls"),
        ("compactions", "compactions"),
        ("split-turn compactions", "split_turn_compactions"),
        ("compression ratio avg", "compression_ratio_avg"),
        (
            "final projected context tokens",
            "final_projected_context_tokens",
        ),
        ("probe accuracy", "probe_accuracy"),
        ("ledger lines", "ledger_lines"),
        ("ledger duplicates", "ledger_duplicates"),
        ("ledger missing entries", "ledger_missing"),
        ("/tmp attempts", "tmp_attempts"),
    ];
    out.push_str("| metric | builtin | codex |\n|---|---:|---:|\n");
    for (label, key) in metrics {
        let builtin = report
            .drivers
            .get("builtin")
            .map(|report| format_value(key, report))
            .unwrap_or_else(|| "-".to_owned());
        let codex = report
            .drivers
            .get("codex")
            .map(|report| format_value(key, report))
            .unwrap_or_else(|| "-".to_owned());
        out.push_str(&format!("| {label} | {builtin} | {codex} |\n"));
    }
    out
}

fn format_value(key: &str, report: &DriverReport) -> String {
    match key {
        "runs_completed" => report.runs_completed.to_string(),
        "failed_runs" => report.failed_runs.to_string(),
        "total_input_tokens" => report.total_input_tokens.to_string(),
        "cached_input_tokens" => report.cached_input_tokens.to_string(),
        "cache_rate" => format!("{:.3}", report.cache_rate),
        "cache_hit_calls" => report.cache_hit_calls.to_string(),
        "total_calls" => report.total_calls.to_string(),
        "compactions" => report.compactions.to_string(),
        "split_turn_compactions" => report
            .compaction_records
            .iter()
            .filter(|record| record.split_turn)
            .count()
            .to_string(),
        "compression_ratio_avg" => report
            .compression_ratio_avg
            .map_or_else(|| "-".to_owned(), |value| format!("{:.3}", value)),
        "final_projected_context_tokens" => report
            .final_projected_context_tokens
            .map_or_else(|| "-".to_owned(), |value| value.to_string()),
        "probe_accuracy" => format!("{:.3}", report.probe_accuracy),
        "ledger_lines" => report.ledger_lines.to_string(),
        "ledger_duplicates" => report.ledger_duplicates.to_string(),
        "ledger_missing" => report.ledger_missing.to_string(),
        "tmp_attempts" => report.tmp_attempts.to_string(),
        _ => "-".to_owned(),
    }
}

fn assert_hard(
    report: &HarnessReport,
    steps: &[ScenarioStep],
    profiles: &[DriverProfile],
    enforce: bool,
    min_cache_rate: f64,
    min_probe_accuracy: f64,
) -> Result<()> {
    let probes_total = steps.iter().filter(|step| step.probe.is_some()).count();
    for profile in profiles {
        let driver = report
            .drivers
            .get(profile.driver)
            .with_context(|| format!("missing {} report", profile.driver))?;
        ensure!(
            driver.runs_completed + driver.failed_runs >= steps.len(),
            "{} completed {} runs with {} failures, expected at least {}",
            profile.driver,
            driver.runs_completed,
            driver.failed_runs,
            steps.len()
        );
        ensure!(
            driver.total_input_tokens > 0,
            "{} produced no measurable input tokens",
            profile.driver
        );
        ensure!(
            driver.total_calls > 0,
            "{} produced no inference calls",
            profile.driver
        );
        ensure!(
            driver.probes_total == probes_total,
            "{} graded {} probes, expected {}",
            profile.driver,
            driver.probes_total,
            probes_total
        );
        if enforce {
            ensure!(
                driver.cache_rate >= min_cache_rate,
                "{} cache rate {:.3} below threshold {min_cache_rate}",
                profile.driver,
                driver.cache_rate
            );
            ensure!(
                driver.probe_accuracy >= min_probe_accuracy,
                "{} probe accuracy {:.3} below threshold {min_probe_accuracy}",
                profile.driver,
                driver.probe_accuracy
            );
        }
    }
    if let Some(builtin) = report.drivers.get("builtin") {
        ensure!(
            builtin.compactions >= 1,
            "builtin benchmark did not exercise context compression"
        );
        let mut previous_through = 0;
        for record in &builtin.compaction_records {
            ensure!(
                record.through >= previous_through,
                "builtin compaction boundary went backwards"
            );
            previous_through = record.through;
            ensure!(
                record.source_tokens > record.summary_tokens,
                "builtin compaction did not compress (source {} <= summary {})",
                record.source_tokens,
                record.summary_tokens
            );
        }
    }
    Ok(())
}

fn uuid_field(body: &Value, field: &str) -> Result<Uuid> {
    body.get(field)
        .and_then(Value::as_str)
        .with_context(|| format!("missing {field}"))?
        .parse()
        .context("invalid UUID field")
}
