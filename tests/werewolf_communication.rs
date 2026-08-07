//! Live collaboration acceptance test using a Werewolf scenario.
//!
//! The test initializes a Space, five Agents, and one public Channel. From that
//! point on, the God Agent owns the game. The test only observes persisted
//! collaboration facts and waits for the God Agent's structured completion
//! signal; it never advances a phase, retries an Agent, or caps Agent turns.

mod support;

use std::{
    collections::{BTreeMap, BTreeSet},
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
    SumiProcess, TestDatabase, default_codex_home, register_with, reserve_local_port,
    short_temp_root, spawn_default_codex_computer, spawn_server, wait_for_computer_status_for,
    wait_for_health, write_default_codex_computer_config, write_server_config,
};

const AGENT_READY_TIMEOUT: Duration = Duration::from_secs(120);
const POLL_INTERVAL: Duration = Duration::from_millis(500);
const MONITOR_HTML: &str = r##"<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Werewolf Agent Monitor</title>
  <style>
    :root { color-scheme:light; --ink:#241f1a; --paper:#fffbf4; --panel:#f5eee1; --strong:#efe5d5; --accent:#f0602f; --accent-soft:#fde3d5; --indigo:#3d5aa9; --green:#5b9253; --orange:#e08b2c; --red:#d33f2e; --stone:#b4ab9d; --muted:#8a8071; --line:#e8decd; --line-strong:#c6b9a6; }
    * { box-sizing: border-box; }
    body { margin:0; background:var(--paper); color:var(--ink); font:14px/1.5 Manrope,"Noto Sans SC",sans-serif; }
    main { width:min(1120px,100%); margin-inline:auto; padding:clamp(20px,4vw,48px); }
    header { display:flex; flex-wrap:wrap; align-items:flex-end; justify-content:space-between; gap:24px; margin-block-end:32px; }
    h1 { margin:0; font-size:18px; line-height:1.25; }
    .eyebrow { margin:0 0 6px; color:var(--accent); font-size:11px; font-weight:700; letter-spacing:.12em; text-transform:uppercase; }
    .lede { max-width:68ch; margin:8px 0 0; color:var(--muted); }
    button { min-height:40px; padding-inline:16px; border:2px solid var(--ink); border-radius:7px; background:var(--paper); color:var(--ink); font:600 13px/1 inherit; box-shadow:3px 3px 0 var(--ink); cursor:pointer; }
    button:active { scale:.96; }
    button:focus-visible { outline:2px solid var(--accent); outline-offset:3px; }
    .section-head { display:flex; flex-wrap:wrap; align-items:baseline; justify-content:space-between; gap:8px 24px; margin-block-end:16px; }
    h2 { margin:0; font-size:12px; letter-spacing:.08em; text-transform:uppercase; }
    #connection { color:var(--muted); font-size:13px; }
    #connection[data-tone="ok"]::before { content:"● "; color:var(--green); }
    #connection[data-tone="error"]::before { content:"▲ "; color:var(--red); }
    .stream-note { margin:0 0 18px; color:var(--muted); font-size:12px; }
    ol { display:grid; gap:18px; margin:0; padding:0; list-style:none; }
    .event { display:grid; grid-template-columns:48px minmax(0,1fr); gap:10px; align-items:start; overflow-wrap:anywhere; }
    .avatar { display:block; width:40px; height:40px; margin-block-start:20px; border:2px solid var(--ink); border-radius:6px; background:var(--paper); box-shadow:2px 2px 0 var(--ink); image-rendering:pixelated; overflow:hidden; }
    .avatar-human { place-items:center; background:var(--strong); color:var(--muted); font:700 18px/1 "JetBrains Mono",monospace; }
    .event[data-god="true"] .avatar { border-color:var(--accent); box-shadow:2px 2px 0 var(--accent); }
    .event-content { min-width:0; max-width:840px; }
    .event-head { display:flex; flex-wrap:wrap; align-items:baseline; gap:4px 10px; min-height:20px; margin-block-end:5px; }
    .actor { font-weight:700; }
    .route { color:var(--muted); font-size:12px; }
    .kind-label { color:var(--accent); font:700 10px/1.5 "JetBrains Mono",monospace; letter-spacing:.06em; text-transform:uppercase; }
    .god-badge { display:inline-flex; margin-inline-start:6px; padding:2px 5px; border:1px solid var(--accent); border-radius:3px; color:var(--accent); font:700 10px/1 "JetBrains Mono",monospace; letter-spacing:.06em; vertical-align:middle; }
    .bubble { width:fit-content; max-width:100%; padding:12px 14px; border-radius:4px 12px 12px; background:var(--panel); }
    .message-body { margin:0; white-space:pre-wrap; overflow-wrap:anywhere; font-size:14px; line-height:1.65; }
    .message-body[data-redacted="true"] { color:var(--muted); font-style:italic; }
    .meta { display:flex; flex-wrap:wrap; gap:5px 12px; margin-block-start:8px; color:var(--muted); font:600 11px/1.45 "JetBrains Mono",monospace; }
    .meta span { min-width:0; }
    .meta span + span::before { content:"·"; margin-inline-end:12px; color:var(--stone); }
    .system-card { padding:10px 12px; border-radius:7px; background:var(--strong); }
    .system-line { display:flex; flex-wrap:wrap; align-items:center; gap:7px 10px; }
    .system-line strong { font-size:13px; }
    .status { display:inline-flex; align-items:center; gap:5px; min-height:22px; padding-inline:8px; border-radius:999px; background:var(--paper); font:700 11px/1 "JetBrains Mono",monospace; }
    .status::before { content:"●"; color:var(--stone); }
    .status[data-tone="working"]::before,.status[data-tone="assigned"]::before { color:var(--orange); }
    .status[data-tone="completed"]::before,.status[data-tone="handled"]::before { color:var(--green); }
    .status[data-tone="failed"]::before,.status[data-tone="dead"]::before { content:"▲"; color:var(--red); }
    .status[data-tone="pending"]::before,.status[data-tone="dispatched"]::before { color:var(--indigo); }
    .empty { padding:24px; border-radius:9px; background:var(--panel); color:var(--muted); }
    .conversation-layout { display:grid; grid-template-columns:minmax(0,1fr) 300px; gap:32px; align-items:start; }
    .public-pane { min-width:0; }
    .direct-pane { position:sticky; top:24px; min-width:0; }
    .direct-list { display:grid; gap:8px; margin:0; padding:0; list-style:none; }
    .direct-item { display:grid; grid-template-columns:34px minmax(0,1fr); gap:10px; align-items:center; min-width:0; padding:10px; border-radius:8px; background:var(--panel); }
    .direct-item[data-god="true"] { background:var(--accent-soft); }
    .direct-item .avatar { width:32px; height:32px; margin:0; }
    .direct-title { min-width:0; font-weight:700; overflow-wrap:anywhere; }
    .direct-preview { margin-block-start:2px; color:var(--muted); font-size:12px; }
    .direct-meta { margin-block-start:4px; color:var(--muted); font:600 10px/1.4 "JetBrains Mono",monospace; }
    .direct-empty { padding:14px 0; color:var(--muted); font-size:13px; }
    @media (max-width:520px) {
      main { padding-inline:16px; }
      header { align-items:flex-start; }
      header button { width:100%; }
      .event { grid-template-columns:38px minmax(0,1fr); gap:8px; }
      .avatar { width:34px; height:34px; margin-block-start:21px; }
      .kind-label { width:100%; }
      .bubble,.system-card { width:100%; }
      .meta span + span::before { content:none; margin:0; }
    }
    @media (max-width:760px) {
      .conversation-layout { grid-template-columns:1fr; gap:28px; }
      .direct-pane { position:static; }
    }
    @media (forced-colors:active) { button { box-shadow:none; } }
  </style>
</head>
<body>
  <main>
    <header>
      <div><p class="eyebrow">Live acceptance test</p><h1>Werewolf Agent Conversation</h1><p class="lede">只读展示公开频道的完整对话。私聊内容仅对其 Channel 成员可见，Human 身份不会显示。</p></div>
      <button id="pause" type="button" aria-pressed="false">暂停刷新</button>
    </header>
    <div class="conversation-layout">
      <section class="public-pane" aria-labelledby="events-heading">
        <div class="section-head"><h2 id="events-heading">公开对话</h2><span id="connection" role="status" aria-live="polite">正在连接</span></div>
        <p class="stream-note">最新消息在上。这里显示 Agent 的实际公开发言。</p>
        <ol id="events"></ol>
      </section>
      <aside class="direct-pane" aria-labelledby="direct-heading">
        <div class="section-head"><h2 id="direct-heading">私聊会话</h2><span id="direct-count" class="route">0</span></div>
        <ul id="directs" class="direct-list"></ul>
      </aside>
    </div>
  </main>
  <script>
    const events=document.querySelector("#events"), directs=document.querySelector("#directs"), directCount=document.querySelector("#direct-count"), connection=document.querySelector("#connection"), pause=document.querySelector("#pause");
    const avatarColors=["#f0602f","#e8b42d","#3c9e8f","#6c5ce7","#3d5aa9","#5b9253"];
    let paused=false, renderedEventCount=-1, renderedDirectKey="";
    pause.addEventListener("click",()=>{paused=!paused; pause.setAttribute("aria-pressed",String(paused)); pause.textContent=paused?"继续刷新":"暂停刷新"; connection.textContent=paused?"刷新已暂停":"正在连接"; if(!paused) refresh();});
    function node(tag,className,text){const element=document.createElement(tag); if(className)element.className=className; if(text!==undefined)element.textContent=text; return element;}
    function hashName(name){let hash=2166136261; for(const character of name){hash^=character.codePointAt(0); hash=Math.imul(hash,16777619);} return hash>>>0;}
    function avatar(name){
      if(name==="<human>"){const face=node("div","avatar avatar-human","?"); face.setAttribute("aria-hidden","true"); return face;}
      const face=node("canvas","avatar"),hash=hashName(name),context=face.getContext("2d"); face.width=8; face.height=8; face.setAttribute("aria-hidden","true"); context.fillStyle=avatarColors[hash%avatarColors.length];
      for(let row=0;row<8;row++)for(let column=0;column<8;column++){const mirror=column<4?column:7-column,bit=(row*4+mirror)%32;if(((hash>>>bit)&1)===1)context.fillRect(column,row,1,1);}
      return face;
    }
    function meta(values){const box=node("div","meta"); for(const value of values.filter(Boolean))box.append(node("span","",value)); return box;}
    function legacyMeta(detail){
      const fields=[],pattern=/(?:^| )([a-z_]+)=/g; let match,key,start;
      while((match=pattern.exec(detail))!==null){if(key)fields.push(`${key} ${detail.slice(start,match.index).trim()}`);key=match[1];start=pattern.lastIndex;}
      if(key)fields.push(`${key} ${detail.slice(start).trim()}`); return fields.length?fields:[detail];
    }
    function shell(event,label){
      const item=node("li","event"), content=node("div","event-content"), head=node("div","event-head"), actor=node("span","actor",event.actor??"系统"), route=node("span","route",event.route??""), kind=node("span","kind-label",label);
      if(event.actor==="God"){item.dataset.god="true";const badge=node("span","god-badge","GOD");badge.title="游戏主持";head.append(actor,badge);}else head.append(actor); if(route.textContent)head.append(route); head.append(kind); content.append(head); item.append(avatar(event.actor??"系统"),content); return {item,content};
    }
    function renderMessage(event){
      const isDirect=event.channel_kind==="direct",{item,content}=shell(event,isDirect?"DM":"Message"),bubble=node("div","bubble"),body=node("p","message-body",isDirect?"私聊内容受 Channel 权限保护。":event.body??"消息正文不可用");
      body.dataset.redacted=String(isDirect); bubble.append(body,meta([event.content_kind,event.reply?"回复":"根消息",event.mention_all?"@all":event.mentions?.length?`提及 ${event.mentions.join("、")}`:null,Number.isInteger(event.channel_seq)?`seq ${event.channel_seq}`:null])); content.append(bubble); return item;
    }
    function renderRun(event){
      const {item,content}=shell(event,"Run"),card=node("div","system-card"),line=node("div","system-line"),description=node("strong","",`处理 ${event.trigger_kind??"未知"} 触发`),status=node("span","status",event.status??"unknown");
      status.dataset.tone=event.status??"unknown"; line.append(description,status); card.append(line,meta(event.legacy_meta??[event.focus_thread_id?`focus ${event.focus_thread_id}`:null,event.outcome_code?`outcome ${event.outcome_code}`:null,event.error_code?`error ${event.error_code}`:null,event.id])); content.append(card); return item;
    }
    function renderInbox(event){
      const {item,content}=shell(event,"Inbox"),card=node("div","system-card"),line=node("div","system-line"),description=node("strong","",`收到 ${event.strength??"未知"} ${event.inbox_kind??"Item"}`),status=node("span","status",event.status??"unknown");
      status.dataset.tone=event.status??"unknown"; line.append(description,status); card.append(line,meta(event.legacy_meta??[event.message_id?`message ${event.message_id}`:null,event.thread_id?`thread ${event.thread_id}`:null,event.assigned_run_id?`run ${event.assigned_run_id}`:null,`retry ${event.retry_count??0}`,`requeue ${event.requeue_count??0}`,event.error_code?`error ${event.error_code}`:null,event.id])); content.append(card); return item;
    }
    function normalizeEvent(event){
      if(event.actor)return event;
      const parts=String(event.title??"系统").split(" · "),normalized={...event,actor:parts[0]||"系统",legacy_meta:legacyMeta(event.detail??"")};
      if(event.kind==="Message")return {...normalized,channel_kind:parts[1]==="direct"?"direct":"public",route:parts[1]==="direct"?"→ DM":`→ #${parts[1]??"channel"}`};
      if(event.kind==="Run")return {...normalized,route:"→ Driver",status:parts[1]??"unknown",trigger_kind:event.detail?.match(/trigger=([^ ]+)/)?.[1]};
      return {...normalized,route:"← Inbox",inbox_kind:parts[1]??"Item",status:parts[2]??"unknown",strength:event.detail?.match(/strength=([^ ]+)/)?.[1]};
    }
    function renderEvent(rawEvent){const event=normalizeEvent(rawEvent);if(event.kind==="Message")return renderMessage(event); if(event.kind==="Run")return renderRun(event); return renderInbox(event);}
    function renderEvents(rawEvents){
      if(rawEvents.length===renderedEventCount)return;
      if(renderedEventCount>0&&rawEvents.length>renderedEventCount)events.prepend(...rawEvents.slice(renderedEventCount).reverse().map(renderEvent));
      else events.replaceChildren(...(rawEvents.length?rawEvents.slice().reverse().map(renderEvent):[Object.assign(document.createElement("li"),{className:"empty",textContent:"等待第一个交互事件。"})]));
      renderedEventCount=rawEvents.length;
    }
    function renderDirects(rawEvents){
      const groups=new Map();
      for(const event of rawEvents){
        const peers=event.participants?.length?event.participants:[String(event.route??"").replace(/^[^A-Za-z<]+/,"")],participants=[event.actor,...peers].filter(Boolean).sort(),key=participants.join("|");
        const current=groups.get(key)??{participants,count:0,latest:event}; current.count+=1; current.latest=event; groups.set(key,current);
      }
      const nextKey=[...groups].map(([key,group])=>`${key}:${group.count}:${group.latest.id}`).join(";");
      if(nextKey===renderedDirectKey)return;
      directCount.textContent=`${groups.size} 个会话`;
      directs.replaceChildren(...(groups.size?[...groups.values()].reverse().map(group=>{
        const item=node("li","direct-item"),latest=group.latest,title=node("div","direct-title",group.participants.join(" · ")),preview=node("div","direct-preview","内容受 Channel 权限保护"),detail=node("div","direct-meta",`${group.count} 条消息 · 最近 seq ${latest.channel_seq??"?"}`),content=node("div");
        if(group.participants.includes("God")){item.dataset.god="true";const badge=node("span","god-badge","GOD");badge.title="游戏主持";title.append(badge);}
        content.append(title,preview,detail); item.append(avatar(group.participants[0]??"系统"),content); return item;
      }):[node("li","direct-empty","暂无私聊会话。")]));
      renderedDirectKey=nextKey;
    }
    function render(data){
      const messages=(data.events??[]).filter(event=>event.kind==="Message");
      renderEvents(messages.filter(event=>event.channel_kind!=="direct"));
      renderDirects(messages.filter(event=>event.channel_kind==="direct"));
      connection.dataset.tone="ok"; connection.textContent=`已同步 ${messages.length} 条对话`;
    }
    async function refresh(){if(paused)return;try{const response=await fetch(`/werewolf-trace.json?t=${Date.now()}`,{cache:"no-store"});if(!response.ok)throw new Error(String(response.status));render(await response.json());}catch(error){connection.dataset.tone="error";connection.textContent="监测数据暂不可用";}}
    refresh(); setInterval(refresh,1000);
  </script>
</body>
</html>"##;

#[derive(Clone, Copy)]
struct AgentProfile {
    name: &'static str,
    driver: &'static str,
    is_god: bool,
}

#[derive(Clone, Copy)]
struct LiveAgent {
    profile: AgentProfile,
    member_id: Uuid,
}

#[derive(sqlx::FromRow)]
struct MessageTraceRow {
    id: Uuid,
    channel_id: Uuid,
    channel_kind: String,
    channel_label: String,
    thread_id: Uuid,
    reply_to_message_id: Option<Uuid>,
    channel_seq: i64,
    placement: String,
    content_kind: String,
    author_name: String,
    recipient_names: Vec<String>,
    mention_names: Vec<String>,
    mention_all: bool,
    public_body: Option<String>,
    body_bytes: Option<i32>,
    attachment_count: i64,
}

#[derive(sqlx::FromRow)]
struct RunTraceRow {
    id: Uuid,
    agent_name: String,
    focus_thread_id: Uuid,
    trigger_kind: String,
    status: String,
    outcome_code: Option<String>,
    error_code: Option<String>,
}

#[derive(sqlx::FromRow)]
struct InboxTraceRow {
    id: Uuid,
    receiver_name: String,
    message_id: Option<Uuid>,
    thread_id: Uuid,
    kind: String,
    strength: String,
    status: String,
    assigned_run_id: Option<Uuid>,
    retry_count: i32,
    requeue_count: i32,
    last_error_code: Option<String>,
}

struct InteractionObserver {
    trace_path: PathBuf,
    seen_messages: BTreeSet<Uuid>,
    run_states: BTreeMap<Uuid, String>,
    inbox_states: BTreeMap<Uuid, String>,
    last_summary: Option<String>,
    events: Vec<Value>,
}

#[derive(sqlx::FromRow)]
struct GameProgress {
    completion_signals: i64,
    assigned_players: i64,
    public_player_authors: i64,
    dm_exchanges: i64,
    agents_with_runs: i64,
    live_runs: i64,
    open_hard_items: i64,
    dead_items: i64,
    failed_runs: i64,
}

const GOD_PROFILE: AgentProfile = AgentProfile {
    name: "God",
    driver: "builtin",
    is_god: true,
};

const PLAYER_PROFILES: [AgentProfile; 4] = [
    AgentProfile {
        name: "Aster",
        driver: "codex",
        is_god: false,
    },
    AgentProfile {
        name: "Briar",
        driver: "codex",
        is_god: false,
    },
    AgentProfile {
        name: "Cedar",
        driver: "codex",
        is_god: false,
    },
    AgentProfile {
        name: "Dawn",
        driver: "codex",
        is_god: false,
    },
];

const SHARED_GAME_CONTEXT: &str = concat!(
    "本局在 #werewolf 频道进行。God 是主持游戏的 Agent，不是玩家。",
    "Human 只初始化环境并观察，不参与游戏，也不会推进流程。",
    "需要其他 Agent 处理的消息必须使用结构化 mention、reply 或 DM 路由。",
    "发言、协商和阶段都没有预设轮次上限；应按当前事实继续交流，直到 God 判定游戏结束。",
);

fn role_text(profile: AgentProfile) -> String {
    if profile.is_god {
        format!(
            concat!(
                "你是本局狼人杀的上帝 Agent，独立掌控规则、阶段、夜间行动、投票、淘汰、胜负判定和最终公布。",
                "{shared}",
                "四名玩家是 Aster、Briar、Cedar、Dawn；他们没有预设身份。开局后由你自行生成本局身份配置，并通过 DM 分别秘密分配。",
                "收到环境就绪消息后立即自主开局。你决定需要多少阶段和多少次交流，不得要求 Human 作决定或代你推进。",
                "公开信息在 #werewolf 发布；私密行动使用 DM；每次希望 Agent 回应时使用结构化 mention 或 reply。",
                "要求你后续裁决的公开行动必须让玩家在回复中结构化提及 God，确保该事实进入你的 Inbox。",
                "持续主持，直到你根据实际互动判定一方获胜。最终公开消息必须使用结构化 @all 公布结果；",
                "@all 是结束信号，开局和中途不得使用。不得提及、询问或披露 Human 的身份。"
            ),
            shared = SHARED_GAME_CONTEXT,
        )
    } else {
        format!(
            concat!(
                "你是狼人杀玩家 {name}。{shared}",
                "你的身份尚未分配，只接受 God 通过 DM 给你的本局身份；在 God 公布结果前不得把身份或私聊内容写入公开频道或 Memory。",
                "God 负责主持；你根据收到的公开消息、reply、mention 和 DM 自主判断、公开讨论、私下协商并行动。",
                "任何需要 God 裁决或据此推进游戏的公开行动，都必须在消息中结构化提及 God；玩家之间的其他交流对象由你自行决定。",
                "你可以主动联系任何 Agent，也可以进行任意多次交流；不要等待 Human 推进游戏，也不得询问或披露 Human 的身份。"
            ),
            name = profile.name,
            shared = SHARED_GAME_CONTEXT,
        )
    }
}

#[tokio::test]
#[ignore = "requires the default Codex home and a configured Builtin provider"]
async fn werewolf_agents_run_the_game_while_the_test_only_observes() -> Result<()> {
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
    let trace_path = web_dist.join("werewolf-trace.json");
    std::fs::write(web_dist.join("index.html"), MONITOR_HTML)?;
    std::fs::write(&trace_path, r#"{"summary":{},"events":[]}"#)?;

    let bind = SocketAddr::from(([127, 0, 0, 1], reserve_local_port()?));
    let server_url = Url::parse(&format!("http://{bind}"))?;
    let server_config = root.path().join("server.toml");
    write_server_config(&server_config, bind, &database.url, &attachments, &web_dist)?;
    let mut server = spawn_server(&server_config)?;
    wait_for_health(&server_url).await?;
    eprintln!("WEREWOLF monitor_url={server_url}");

    let client = Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .build()?;
    let owner = register_with(
        &client,
        &server_url,
        "Observer",
        &format!("observer-{}@example.test", Uuid::now_v7()),
    )
    .await?;
    let space = support::create_space(&client, &server_url, &owner).await?;

    let state_root = TempDir::with_prefix_in("sumi-werewolf-", short_temp_root()?)?;
    let state_dir = state_root.path().join("computer");
    std::fs::create_dir(&state_dir)?;
    let computer_config = root.path().join("computer.toml");
    let agent_count = PLAYER_PROFILES.len() + 1;
    write_default_codex_computer_config(&computer_config, &server_url, &state_dir, agent_count)?;
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

    let mut players = Vec::with_capacity(PLAYER_PROFILES.len());
    for profile in PLAYER_PROFILES {
        players.push(
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
    let god = create_agent(
        &client,
        &server_url,
        &owner,
        space.id,
        computer_identity.id,
        GOD_PROFILE,
    )
    .await?;
    let mut agents = players.clone();
    agents.push(god);
    ensure_driver_mix(&agents)?;
    wait_for_agents_ready(
        &client,
        &server_url,
        &owner,
        space.id,
        &agents,
        AGENT_READY_TIMEOUT,
    )
    .await?;

    let pool = PgPool::connect(&database.url).await?;
    let group_channel_id =
        create_werewolf_group(&client, &server_url, &owner, space.id, &agents).await?;
    assert_group_setup(&pool, group_channel_id, &agents).await?;

    let initialization = post_root_message(
        &client,
        &server_url,
        &owner,
        group_channel_id,
        "狼人杀环境已初始化。后续游戏进程由 God Agent 自主掌控，Human 只观察。",
        &[god.member_id],
    )
    .await?;
    let initialization_message_id = uuid_field(&initialization, "id")?;
    eprintln!(
        "WEREWOLF initialization message_id={initialization_message_id} god={} players={:?}",
        god.member_id,
        players
            .iter()
            .map(|player| player.member_id)
            .collect::<Vec<_>>()
    );

    observe_until_game_finished(
        &pool,
        space.id,
        group_channel_id,
        god,
        &players,
        &agents,
        &mut server,
        &mut computer,
        trace_path,
    )
    .await?;

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
    ensure!(builtin == 1, "Werewolf requires one Builtin God Agent");
    ensure!(codex == 4, "Werewolf requires four Codex player Agents");
    Ok(())
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
        ensure!(
            Instant::now() < deadline,
            "Agents did not become ready before the startup timeout"
        );
        tokio::time::sleep(POLL_INTERVAL).await;
    }
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
            "topic": "由 God Agent 自主主持的狼人杀协作测试",
            "agent_member_ids": agent_ids(agents),
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
        "post initialization Message returned {}",
        response.status()
    );
    Ok(response.json().await?)
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
    Ok(())
}

#[allow(clippy::too_many_arguments)]
async fn observe_until_game_finished(
    pool: &PgPool,
    space_id: Uuid,
    group_channel_id: Uuid,
    god: LiveAgent,
    players: &[LiveAgent],
    agents: &[LiveAgent],
    server: &mut SumiProcess,
    computer: &mut SumiProcess,
    trace_path: PathBuf,
) -> Result<()> {
    let mut observer = InteractionObserver::new(trace_path);
    loop {
        server.ensure_running()?;
        computer.ensure_running()?;
        observer.emit(pool, space_id).await?;

        let progress =
            game_progress(pool, group_channel_id, god.member_id, players, agents).await?;
        observer.emit_summary(&progress)?;

        ensure!(
            progress.dead_items == 0,
            "Werewolf interaction produced a dead Inbox Item; the observer does not requeue Agents"
        );
        if progress.completion_signals > 0
            && progress.assigned_players == i64::try_from(players.len())?
            && progress.public_player_authors == i64::try_from(players.len())?
            && progress.dm_exchanges > 0
            && progress.agents_with_runs == i64::try_from(agents.len())?
        {
            observer.emit(pool, space_id).await?;
            eprintln!("WEREWOLF completed autonomously");
            return Ok(());
        }

        tokio::time::sleep(POLL_INTERVAL).await;
    }
}

async fn game_progress(
    pool: &PgPool,
    group_channel_id: Uuid,
    god_member_id: Uuid,
    players: &[LiveAgent],
    agents: &[LiveAgent],
) -> Result<GameProgress> {
    let player_ids = agent_ids(players);
    let all_agent_ids = agent_ids(agents);
    sqlx::query_as(
        "SELECT \
           (SELECT count(*) FROM messages m \
            WHERE m.channel_id=$1 AND m.author_member_id=$2 AND m.content_kind='text' \
              AND m.mention_all) AS completion_signals, \
           (SELECT count(DISTINCT peer.id) FROM messages m \
            JOIN channels c ON c.id=m.channel_id \
            JOIN channel_members cm ON cm.channel_id=c.id AND cm.member_id<>$2 \
            JOIN members peer ON peer.id=cm.member_id \
            WHERE c.kind='direct' AND m.author_member_id=$2 AND m.content_kind='text' \
              AND peer.id=ANY($3)) AS assigned_players, \
           (SELECT count(DISTINCT author_member_id) FROM messages \
            WHERE channel_id=$1 AND content_kind='text' AND author_member_id=ANY($3)) \
              AS public_player_authors, \
           (SELECT count(*) FROM ( \
              SELECT m.channel_id FROM messages m JOIN channels c ON c.id=m.channel_id \
              WHERE c.kind='direct' AND m.content_kind='text' AND m.author_member_id=ANY($4) \
                AND (SELECT count(*) FROM channel_members cm JOIN members peer ON peer.id=cm.member_id \
                     WHERE cm.channel_id=c.id AND peer.kind='agent' AND peer.id=ANY($4))=2 \
              GROUP BY m.channel_id HAVING count(DISTINCT m.author_member_id)>=2 \
            ) exchanges) AS dm_exchanges, \
           (SELECT count(DISTINCT agent_id) FROM agent_runs WHERE agent_id=ANY($4)) \
              AS agents_with_runs, \
           (SELECT count(*) FROM agent_runs WHERE agent_id=ANY($4) \
              AND status IN ('dispatched','working')) AS live_runs, \
           (SELECT count(*) FROM inbox_items WHERE member_id=ANY($4) AND strength='hard' \
              AND status<>'handled') AS open_hard_items, \
           (SELECT count(*) FROM inbox_items WHERE member_id=ANY($4) AND status='dead') \
              AS dead_items, \
           (SELECT count(*) FROM agent_runs WHERE agent_id=ANY($4) AND status='failed') \
              AS failed_runs",
    )
    .bind(group_channel_id)
    .bind(god_member_id)
    .bind(player_ids)
    .bind(all_agent_ids)
    .fetch_one(pool)
    .await
    .map_err(Into::into)
}

impl InteractionObserver {
    fn new(trace_path: PathBuf) -> Self {
        Self {
            trace_path,
            seen_messages: BTreeSet::new(),
            run_states: BTreeMap::new(),
            inbox_states: BTreeMap::new(),
            last_summary: None,
            events: Vec::new(),
        }
    }

    async fn emit(&mut self, pool: &PgPool, space_id: Uuid) -> Result<()> {
        self.emit_messages(pool, space_id).await?;
        self.emit_runs(pool, space_id).await?;
        self.emit_inbox(pool, space_id).await?;
        Ok(())
    }

    async fn emit_messages(&mut self, pool: &PgPool, space_id: Uuid) -> Result<()> {
        let rows: Vec<MessageTraceRow> = sqlx::query_as(
            "SELECT m.id,m.channel_id,c.kind AS channel_kind, \
                    COALESCE(c.slug,'direct') AS channel_label,m.thread_id,m.reply_to_message_id, \
                    m.channel_seq,m.placement,m.content_kind, \
                    CASE WHEN author.kind='human' THEN '<human>' ELSE author.display_name END \
                      AS author_name, \
                    COALESCE((SELECT array_agg( \
                                CASE WHEN recipient.kind='human' THEN '<human>' \
                                     ELSE recipient.display_name END \
                                ORDER BY recipient.display_name) \
                              FROM channel_members cm JOIN members recipient ON recipient.id=cm.member_id \
                              WHERE cm.channel_id=m.channel_id AND recipient.id<>author.id), \
                             ARRAY[]::text[]) AS recipient_names, \
                    COALESCE((SELECT array_agg( \
                                CASE WHEN target.kind='human' THEN '<human>' ELSE target.display_name END \
                                ORDER BY target.display_name) \
                              FROM message_mentions mm JOIN members target ON target.id=mm.member_id \
                              WHERE mm.message_id=m.id),ARRAY[]::text[]) AS mention_names, \
                    m.mention_all, \
                    CASE WHEN c.kind='direct' THEN NULL ELSE m.body_markdown END AS public_body, \
                    octet_length(m.body_markdown) AS body_bytes, \
                    (SELECT count(*) FROM message_attachments ma WHERE ma.message_id=m.id) \
                      AS attachment_count \
             FROM messages m JOIN channels c ON c.id=m.channel_id \
             JOIN members author ON author.id=m.author_member_id \
             WHERE m.space_id=$1 ORDER BY m.created_at,m.id",
        )
        .bind(space_id)
        .fetch_all(pool)
        .await?;
        for row in rows {
            if self.seen_messages.insert(row.id) {
                eprintln!(
                    concat!(
                        "WEREWOLF message id={} channel={} channel_kind={} channel_label={} seq={} ",
                        "thread={} reply_to={:?} placement={} content_kind={} author={} mentions={:?} ",
                        "mention_all={} body_bytes={:?} attachments={}"
                    ),
                    row.id,
                    row.channel_id,
                    row.channel_kind,
                    row.channel_label,
                    row.channel_seq,
                    row.thread_id,
                    row.reply_to_message_id,
                    row.placement,
                    row.content_kind,
                    row.author_name,
                    row.mention_names,
                    row.mention_all,
                    row.body_bytes,
                    row.attachment_count,
                );
                self.events.push(serde_json::json!({
                    "kind": "Message",
                    "actor": row.author_name,
                    "channel_kind": row.channel_kind,
                    "participants": row.recipient_names,
                    "route": if row.channel_kind == "direct" {
                        format!("→ {}", row.recipient_names.join("、"))
                    } else {
                        format!("→ #{}", row.channel_label)
                    },
                    "summary": if row.channel_kind == "direct" {
                        "发送一条私信"
                    } else {
                        "发布一条频道消息"
                    },
                    "id": row.id,
                    "channel_seq": row.channel_seq,
                    "placement": row.placement,
                    "content_kind": row.content_kind,
                    "reply": row.reply_to_message_id.is_some(),
                    "mentions": row.mention_names,
                    "mention_all": row.mention_all,
                    "body": row.public_body,
                    "body_bytes": row.body_bytes,
                    "attachment_count": row.attachment_count,
                }));
            }
        }
        Ok(())
    }

    async fn emit_runs(&mut self, pool: &PgPool, space_id: Uuid) -> Result<()> {
        let rows: Vec<RunTraceRow> = sqlx::query_as(
            "SELECT r.id,agent.display_name AS agent_name,r.focus_thread_id,r.trigger_kind, \
                    r.status,r.outcome_code,r.error_code \
             FROM agent_runs r JOIN members agent ON agent.id=r.agent_id \
             WHERE r.space_id=$1 ORDER BY r.created_at,r.id",
        )
        .bind(space_id)
        .fetch_all(pool)
        .await?;
        for row in rows {
            let state = format!("{}|{:?}|{:?}", row.status, row.outcome_code, row.error_code);
            if self.run_states.get(&row.id) != Some(&state) {
                eprintln!(
                    concat!(
                        "WEREWOLF run id={} agent={} focus_thread={} trigger={} status={} ",
                        "outcome={:?} error={:?}"
                    ),
                    row.id,
                    row.agent_name,
                    row.focus_thread_id,
                    row.trigger_kind,
                    row.status,
                    row.outcome_code,
                    row.error_code,
                );
                self.events.push(serde_json::json!({
                    "kind": "Run",
                    "actor": row.agent_name,
                    "route": "→ Driver",
                    "id": row.id,
                    "focus_thread_id": row.focus_thread_id,
                    "trigger_kind": row.trigger_kind,
                    "status": row.status,
                    "outcome_code": row.outcome_code,
                    "error_code": row.error_code,
                }));
                self.run_states.insert(row.id, state);
            }
        }
        Ok(())
    }

    async fn emit_inbox(&mut self, pool: &PgPool, space_id: Uuid) -> Result<()> {
        let rows: Vec<InboxTraceRow> = sqlx::query_as(
            "SELECT i.id,receiver.display_name AS receiver_name,i.message_id,i.thread_id,i.kind, \
                    i.strength,i.status,i.assigned_run_id,i.retry_count,i.requeue_count, \
                    i.last_error_code \
             FROM inbox_items i JOIN members receiver ON receiver.id=i.member_id \
             WHERE i.space_id=$1 ORDER BY i.created_at,i.id",
        )
        .bind(space_id)
        .fetch_all(pool)
        .await?;
        for row in rows {
            let state = format!(
                "{}|{:?}|{}|{}|{:?}",
                row.status,
                row.assigned_run_id,
                row.retry_count,
                row.requeue_count,
                row.last_error_code,
            );
            if self.inbox_states.get(&row.id) != Some(&state) {
                eprintln!(
                    concat!(
                        "WEREWOLF inbox id={} receiver={} message={:?} thread={} kind={} strength={} ",
                        "status={} run={:?} retries={} requeues={} error={:?}"
                    ),
                    row.id,
                    row.receiver_name,
                    row.message_id,
                    row.thread_id,
                    row.kind,
                    row.strength,
                    row.status,
                    row.assigned_run_id,
                    row.retry_count,
                    row.requeue_count,
                    row.last_error_code,
                );
                self.events.push(serde_json::json!({
                    "kind": "Inbox",
                    "actor": row.receiver_name,
                    "route": "← Inbox",
                    "id": row.id,
                    "message_id": row.message_id,
                    "thread_id": row.thread_id,
                    "inbox_kind": row.kind,
                    "strength": row.strength,
                    "status": row.status,
                    "assigned_run_id": row.assigned_run_id,
                    "retry_count": row.retry_count,
                    "requeue_count": row.requeue_count,
                    "error_code": row.last_error_code,
                }));
                self.inbox_states.insert(row.id, state);
            }
        }
        Ok(())
    }

    fn emit_summary(&mut self, progress: &GameProgress) -> Result<()> {
        let summary = format!(
            concat!(
                "completion_signals={} assigned_players={} public_player_authors={} dm_exchanges={} agents_with_runs={} ",
                "live_runs={} open_hard_items={} dead_items={} failed_runs={}"
            ),
            progress.completion_signals,
            progress.assigned_players,
            progress.public_player_authors,
            progress.dm_exchanges,
            progress.agents_with_runs,
            progress.live_runs,
            progress.open_hard_items,
            progress.dead_items,
            progress.failed_runs,
        );
        if self.last_summary.as_deref() != Some(summary.as_str()) {
            eprintln!("WEREWOLF summary {summary}");
            self.last_summary = Some(summary);
        }
        self.write_snapshot(progress)
    }

    fn write_snapshot(&self, progress: &GameProgress) -> Result<()> {
        let snapshot = serde_json::to_vec(&serde_json::json!({
            "summary": {
                "completion_signals": progress.completion_signals,
                "assigned_players": progress.assigned_players,
                "public_player_authors": progress.public_player_authors,
                "dm_exchanges": progress.dm_exchanges,
                "agents_with_runs": progress.agents_with_runs,
                "live_runs": progress.live_runs,
                "open_hard_items": progress.open_hard_items,
                "dead_items": progress.dead_items,
                "failed_runs": progress.failed_runs,
            },
            "events": &self.events,
        }))?;
        let temporary_path = self.trace_path.with_extension("json.tmp");
        std::fs::write(&temporary_path, snapshot).with_context(|| {
            format!(
                "write Werewolf monitor snapshot at {}",
                temporary_path.display()
            )
        })?;
        std::fs::rename(&temporary_path, &self.trace_path).with_context(|| {
            format!(
                "publish Werewolf monitor snapshot at {}",
                self.trace_path.display()
            )
        })?;
        Ok(())
    }
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
