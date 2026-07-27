# Sumi v1 当前目标

## 产品结果

先完成一个真实可用的 Human 与 Agent 协作产品：Human 配对一台 Computer，在该 Computer 上使用本地配置创建 Builtin Agent，并在 DM、Channel 和 Thread 中触发 Agent；Agent 必须通过 `sumi agent` CLI 读取上下文、回复或明确 ack/defer，且 Inbox 状态与 Message 写入满足设计中的事务和恢复不变量。

当前 WebUI、Server API、daemon、CLI 和 Driver 都已有局部实现，但“模块存在”和“测试通过”不等于产品闭环完成。此前“Phase 0–5 已完成”的判断作废；以下真实纵向验收是唯一完成依据。

## 当前基线判断

- Human、Space、Channel、DM、Thread、Message、Attachment、Computer、Agent、Inbox 和 Approval 已有 schema、API 或 UI 基线，保留现有已通过测试作为回归保护。
- Computer 身份已收敛为单一 Computer Token：raw Token 仅保存在本机受限 `secrets.json`，Server 与配对记录只保存 hash，配对页只显示不可逆短 fingerprint；重连与删除生命周期已通过真实进程验收。
- Builtin 已从显式 Computer-local source paths 加载 Pi-compatible settings/models/auth，选中并规范化 provider/model，只接受声明的 OpenAI-compatible completions；认证缓存限制在本机 `secrets.json` 与 daemon 所需内存，旧环境变量入口已删除。
- 真实进程 harness 已启动隔离 PostgreSQL、Server、Computer daemon、Builtin fake provider 和 `sumi agent` CLI，覆盖 DM、Channel、Thread、context freshness、崩溃恢复与权限边界；产品能力收口仍需为未覆盖能力补齐同等级证据。
- 本机 Pi 配置可作为首个真实验收样本：默认 `deepseek/deepseek-v4-pro`，模型协议为 `openai-completions`，认证按 provider 单独保存；任何测试和日志都不得输出认证值。

## 执行规则

1. 完整阅读 `AGENTS.md`、本文件、`GLOSSARY.md` 和 `docs/design.md`；具体开发前重读对应设计章节。
2. 从最早未完成项开始，按“审计现状 -> 完成一条真实纵向路径 -> 定向测试 -> 修复 -> 更新进度”执行。
3. 当前工作区是唯一实现基线；保留用户和其他 Agent 的未提交修改，不读取或恢复 Git 历史旧实现。
4. 行为、协议、数据模型或领域名词改变时，先更新 `docs/design.md` 或 `GLOSSARY.md`，不做兼容层、双写或旧 schema 迁移。
5. 只有真实进程和真实 SQL 通过对应验收后才能勾选；handler 存在、mock WebSocket frame 或孤立单元测试不能替代纵向闭环。
6. 每完成一部分可独立提交的基线（纵向实现、定向测试与进度证据均完整），必须立即停止扩展当前范围，先更新本文件并提交本次基线对应的代码与文档；确认提交成功且没有遗漏本次改动后，再生成非空 handoff 文档，并在当前 Superset workspace 创建新的承接 session 接力后续最早未完成项。不得把多段可提交基线积压在同一 session，也不得把未提交的开发基线交给下一 session。

## 剩余工作

### 0. 产品模型重置

- [x] 从 v1 删除 BYOK、Secret Envelope、Server 模型 Secret 和 Browser API key 输入；Codex 只使用 Computer 本地既有登录。
- [x] 将 Computer 身份统一定义为本机长期保存的 Computer Token；断线只变 offline，删除 Computer 才撤销 Token 和 Pair。
- [x] 将 metrics、性能基准和 p95 门槛移出 v1，只保留产品状态、结构化诊断日志与正确性验收。
- [x] 将 Builtin 本地配置定义为 Pi-compatible 的 provider/model/auth 三文件输入，并明确不读取 Pi session、extension 或 prompt。

### 1. Computer Token 与本地 Driver 配置

- [x] 将 daemon、Server、PostgreSQL schema、API types、WebUI 和测试中的 `private_key`、`pairing_secret`、`computer_credential` 收敛为单一 Computer Token：raw Token 只在本机持久化并仅通过 HTTPS/WSS 用于认证，Server 只保存 hash，配对页只显示不可逆短 fingerprint。
- [x] 验证首次 Pair、daemon 正常退出、网络中断、Server 重启和 daemon 重启：除 Delete Computer 外始终复用同一 Computer ID/Token，状态只在 online/offline 间变化，不重新 Pair。
- [x] 验证 Delete Computer 撤销旧 Token、终止在线 daemon、拒绝离线 daemon 下次连接，并让下一次启动生成新 Token 重新 Pair；Agent Homes 和历史身份保留。
- [x] 实现 Builtin Computer-local 配置加载与校验：显式 source paths、Pi-compatible settings/models/auth、选中 provider/model、只支持已声明的 OpenAI-compatible completions、认证 redaction 和受限权限。
- [x] 用本机 Pi 的 `deepseek/deepseek-v4-pro` 配置完成一次不泄露认证的 Builtin provider smoke check；自动测试使用本地 fake provider，不连接收费服务。

### 2. Agent DM 真实闭环

- [x] 增加真实进程级测试：启动临时 PostgreSQL、Sumi Server、Computer daemon、本地 fake OpenAI-compatible provider 和 Builtin Agent，由 Human 发送 DM，不能用手工 command frame 代替 daemon。
- [x] 验证 daemon claim hard Inbox、启动唯一 Agent Run，Builtin 通过 sandbox 内真实 `sumi agent inbox current` 与 `channel read` 读取 DM，再用 `message send --handle` 回复。
- [x] 验证 Agent Message 作者、Channel sequence、结构化地址、SSE 更新和 Inbox handled；Message 与 handled 必须在同一 PostgreSQL 事务提交。
- [x] 验证模型最终文本和 Driver stdout 不会自动成为 Message；只有 `sumi agent message send` 能发布。
- [x] 验证发送前 Driver 失败会 release/retry，发送并 handle 后 daemon 崩溃不会重复回复，连续失败最终 dead 并通知 Human Admin/Owner。

### 3. Channel 与 Thread Agent 闭环

- [x] Channel mention：Agent 读取 `#channel` 上下文、结构化 mentions 和 snapshot sequence，并能回复、ack 或 defer。
- [x] Channel ambient：连续普通 Message 聚合为一个 Inbox Item，只启动一次 run；Agent 自己判断是否回应，daemon 不做内容分类。
- [x] Thread：Agent 区分 `#channel` 与 `#channel:{thread-id}`，`thread read` 返回 root、replies、Channel 背景和 snapshot，回复落在正确 Thread。
- [x] Context freshness：Human 在 Agent 读取后追加 Message 时，旧 `--based-on` 返回 `context_changed` 且不创建 Message；Agent 重读后可成功回复。
- [x] 权限边界：private Channel 只认 membership；Agent Admin、其他 Computer、其他 Agent Home 和伪造 run token 都不能越权。
- [x] 用同一个真实进程 harness 覆盖 DM、mention、ambient、Thread 与 context_changed，避免为每种场景复制整套基础设施。

### 4. 产品能力收口

- [x] 按 `docs/design.md` 第 22.1 节完成注册/Space 真实闭环：真实 Server 与 PostgreSQL 验证 Session、`sumi-lab`、Owner/general 原子初始化、大小写等价 slug 拒绝、audit/outbox，并验证未登录深链经登录回到原 URL。
- [x] 完成 Attachment 真实进程闭环：Browser 上传、对象存储长度/摘要校验、ready 后 Message 单次关联、Channel membership 下载授权，以及失败时 Message/sequence 事务回滚。
- [x] 完成 Agent lifecycle 真实进程闭环：同一 Agent identity 的 `stop_after_current`、`cancel_now`、恢复和退役，以及历史、Agent Home、持久 command、audit 与 outbox 保留。
- [x] 完成 Memory 真实进程闭环：daemon 回报文件 metadata，Owner 在线临时读取正文，Computer offline 时正文不可用；正文不进入 PostgreSQL、daemon SQLite、idempotency、outbox、audit 或日志，并验证 1 MiB、UTF-8 与 symlink/canonical path 边界。
- [x] 完成 Agent Channel create 真实进程闭环：无权限拒绝、显式 `channel:create`、Agent Admin 默认权限、public/private 创建者 membership、audit、outbox 与幂等事务事实。
- [x] 完成 `docs/design.md` 第 22.9 节 Agent create Approval 真实闭环：Agent Admin 仍只能申请、Human 审批、offline 重试、真实 provision 与 reject 零残留。
- [ ] 继续审计 `docs/design.md` 第 22.2 节，补齐剩余 Agent Admin 治理行为的真实闭环。
- [ ] 补齐关键并发与幂等不变量：同 Agent 单 active run、Computer 并发上限、Thread/Message sequence、Inbox lease 竞争、重复 command、重复 Message/Attachment 和幂等 key payload 冲突。
- [ ] 使用真实 PostgreSQL integration tests 验证 schema、复合外键、唯一约束、事务回滚和 transactional outbox；不得以内存 fake 替代 SQL 验收。
- [ ] 完成 Message、Attachment、Memory、Computer Token 和模型认证的日志 redaction，补齐治理与敏感操作 audit、注册登录及高风险写操作 rate limit、prompt injection 边界和删除/保留策略。

### 5. 平台与最终验收

- [ ] 在 macOS 和 Linux 完成构建、测试与 sandbox 验收，确认 `sandbox-exec`/bubblewrap、目录权限、Unix socket、进程组取消和重连行为。
- [ ] 按 `docs/design.md` 第 22 节逐项运行真实端到端验收，记录可复现命令和结果；不得用 handler 存在、mock 或单元测试冒充产品验收。
- [ ] 使用 Playwright 验收 1440x900、1024x768、390x844，覆盖 Channel、Attachment、Thread、长内容、offline/deleted、响应式、键盘、reduced motion 和 screen reader labels。
- [ ] 执行统一 format、clippy `-D warnings`、typecheck、lint、Rust unit/integration、真实 PostgreSQL、CLI、Web component、production build 和 E2E 测试。
- [ ] 清除占位实现、无主 TODO、过期入口和文档偏差；最终 `git diff --check` 通过。

## 完成定义

Sumi v1 只有同时满足以下条件才算完成：

- Human 可以从空环境完成 Space 创建、Computer Pair、Builtin Agent 创建，并在 DM、Channel 和 Thread 与 Agent 完整对话。
- Computer offline/reconnect 不丢失 Pair；Delete 后旧 Token 永久失效。
- Agent 注意力、CLI 读取/发送、Inbox handle、重试、幂等和权限边界全部由真实进程与真实 PostgreSQL 验证。
- macOS 与 Linux 统一门禁通过，WebUI 与 `docs/UI.md` 一致。
- 日志、错误、audit、outbox、幂等记录和持久化数据不泄露 Message、Attachment、Memory、Computer Token 或模型认证正文。

## 明确不做

- Browser BYOK、Secret Envelope、Server 模型 Secret、模型凭据同步或 WebUI API key 表单。
- 业务 metrics、metrics dashboard/export、性能基准、压力测试和 p95 性能门槛。
- Work/Task、其他具体 Driver、微服务、工作流/DAG、向量搜索、Agent marketplace、Windows 支持或 Agent 热迁移。

## 验证记录

格式：`YYYY-MM-DD | 项目 | 命令或证据 | 结果`。

2026-07-27 | GOAL 重置审计 | 完整阅读 `AGENTS.md`、旧 `GOAL.md`、`GLOSSARY.md`、`docs/design.md`；审计本机 Pi 非敏感配置结构、Builtin/daemon/Server 测试边界 | 确认 BYOK/metrics/性能目标应移出 v1；确认缺少真实 Channel/Thread Agent 对话纵向测试
2026-07-27 | 规范一致性 | `git diff --check`；关键词扫描 BYOK、Secret Envelope、Computer credential、metrics 与性能目标 | diff check 通过；旧概念只保留在“明确不做”和当前代码差距说明中
2026-07-27 | 历史 WebUI 基线 | `pnpm --dir web test && pnpm --dir web lint && pnpm --dir web build`；`PLAYWRIGHT_BASE_URL=http://127.0.0.1:3100 pnpm --dir web test:e2e` | 2026-07-26/27 曾通过 Web tests、production build 和 1440/1024/390 三 viewport；仅作为回归基线，不代表 Agent 对话闭环完成
2026-07-27 | 历史 Rust 基线 | `cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features` | 2026-07-27 曾通过 52 unit + 3 CLI + 1 PostgreSQL migration tests；现有测试未启动真实 Server + daemon + Builtin + Agent CLI 全链路
2026-07-27 | 单一 Computer Token | `cargo test --all-features`；`cargo clippy --all-targets --all-features -- -D warnings`；`pnpm --dir web test -- ComputersPage.test.tsx --run && pnpm --dir web lint && pnpm --dir web build`；`git diff --check` | 52 Rust unit/integration + 3 CLI + 1 真实 PostgreSQL migration tests、12 Web tests、clippy、TypeScript/ESLint 与 production build 全通过；daemon、Server、schema、API types、WebUI 和测试已删除 P-256/pairing secret/credential 三套身份并统一使用 Computer Token
2026-07-27 | Computer 重连生命周期 | `cargo test --test computer_lifecycle -- --nocapture`；`cargo test --all-features`；`cargo clippy --all-targets --all-features -- -D warnings`；`cargo fmt --all -- --check`；`git diff --check` | 真实 PostgreSQL、`sumi server` 和 `sumi computer` 进程验证首次 Pair、正常退出后 offline、daemon 重启、TCP 断网/恢复和 Server 重启；全程复用唯一 Computer ID/Token 和 pairing，状态仅 online/offline；52 unit/integration + 3 CLI + 1 生命周期 + 1 migration tests 全通过
2026-07-27 | Computer 删除生命周期 | `cargo test --test computer_lifecycle -- --nocapture`；`cargo test --all-features`；`cargo clippy --all-targets --all-features -- -D warnings`；`cargo fmt --all -- --check`；`git diff --check` | 真实 PostgreSQL、`sumi server` 和 `sumi computer` 进程验证 Delete 撤销旧 Token、在线 daemon 收终止后清理身份并退出、离线 daemon 下次连接被拒后清理身份并退出；后续启动使用新 Token/Computer ID 重新 Pair，本地 Agent Home、Server Agent/Member/run/pairing 历史保留；52 unit/integration + 3 CLI + 2 生命周期 + 1 migration tests 全通过
2026-07-27 | Builtin Computer-local 配置 | `cargo test builtin_ -- --nocapture`；`cargo test --all-features`；`cargo clippy --all-targets --all-features -- -D warnings`；`cargo fmt --all -- --check`；`git diff --check` | 显式三文件 source paths 加载 Pi-compatible settings/models/auth，精确选择 provider/model 并拒绝非 `openai-completions`；本地 fake provider 验证认证 Header 与 SSE tool loop，auth source/`secrets.json` 权限、日志 redaction、工具环境隔离和旧环境变量入口清除；56 unit/integration + 3 CLI + 2 生命周期 + 1 migration tests 全通过
2026-07-27 | Builtin provider smoke | `cargo test live_builtin_provider_smoke_from_pi_sources -- --ignored` | 从权限 0600 的本机 Pi auth source 在进程内加载认证，确认选择 `deepseek/deepseek-v4-pro` 与 `https://api.deepseek.com`，真实 OpenAI-compatible SSE 请求通过；测试未输出认证、请求正文或响应正文，常规自动测试仍只使用本地 fake provider
2026-07-27 | Agent DM 真实进程 harness | `cargo test --test agent_dm -- --nocapture` | 启动隔离 PostgreSQL、真实 `sumi server`、真实 `sumi computer` 和本地 fake OpenAI-compatible SSE provider；通过 Human API 配对 Computer、provision Builtin Agent、创建 DM 并发送 Message，provider 收到真实请求，PostgreSQL 验证恰好一个 `builtin/running` Agent Run 和一个 `leased` hard Inbox Item；未使用手写 WebSocket command frame
2026-07-27 | Agent DM CLI 闭环 | `cargo test --test agent_dm -- --nocapture`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && git diff --check` | fake provider 通过 SSE 依次要求 Builtin sandbox 执行真实 `sumi agent inbox current --json`、`channel read` 和 `message send --handle`；真实 PostgreSQL 验证唯一 `builtin/completed` run、同 run 领取并 handle 唯一 `direct/hard` Inbox Item、Agent 回复为 Channel seq=2 且 Channel 仅有 Human 原消息与 CLI 回复；完整门禁通过 56 个常规 Rust tests、1 个真实 DM、3 个 CLI、2 个 Computer lifecycle 和 1 个 migration test，1 个手工 live-provider smoke 按设计 ignored
2026-07-27 | Agent DM 事务与实时更新 | `cargo test --test agent_dm -- --nocapture`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && git diff --check` | 在真实 Server、daemon、Builtin、Agent CLI 与 PostgreSQL 链路中，测试库延迟约束强制首次 send-and-handle 在提交点失败，验证 Agent Message、Inbox handled、Channel sequence 和对应 outbox 全部回滚；同 run 重试成功后，CLI 结构化 DM 地址、Browser API Agent 作者与 seq=2 一致，Space SSE 收到不含 Message 正文的 `inbox.changed` 和 `message.created`；完整 Rust 门禁全部通过
2026-07-27 | Agent DM 输出发布边界 | `cargo test --test agent_dm -- --nocapture`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && git diff --check` | 真实 Builtin sandbox shell 子进程产生与回复不同的 stdout，fake provider 返回另一段确定性最终模型文本；真实 PostgreSQL 与 Browser Message API 分别验证两者均未成为 Message，Server/daemon 日志不含 Human Message、CLI 回复、stdout、最终模型文本或 provider 认证，唯一 Agent Message 仍由真实 `sumi agent message send --handle` 发布
2026-07-27 | Agent DM 崩溃恢复与 dead 通知 | `cargo test --test agent_dm -- --nocapture`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && git diff --check` | 真实 Server、daemon、Builtin、Agent CLI 与 PostgreSQL 验证发送前 Driver 失败只 release/retry 一次且原 Item 可被后续唯一 run reclaim；`message send --handle` 提交后强杀并重启 daemon，`process_lost` 恢复不增加 retry、不重领 handled Item 且 Agent 回复保持唯一；连续两次真实 provider 失败达到配置上限后原 Item 精确变为 `dead/retry_count=2`，并在同一恢复路径为 Human Owner/Admin 各创建一条不含 Channel、Thread、Message 或 Attachment 坐标的 system hard Inbox 与 outbox 事件；Server/daemon 日志未泄露 Human Message、CLI 回复或 provider 认证；完整 Rust 门禁通过
2026-07-27 | Channel mention 真实闭环 | `cargo test --test agent_dm -- --nocapture`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && git diff --check` | 同一真实 PostgreSQL、Server、daemon、Builtin 与 Agent CLI harness 验证 Agent 在 `#general` 的三个独立 hard mention run 中读取结构化 mentions 与 snapshot sequence，并分别完成 reply+handle、显式 ack 和 RFC3339 defer；真实 SQL 验证 Message 作者/sequence、唯一 run/lease 关联、handled/deferred 状态与每项唯一 `inbox.changed` outbox，日志未泄露 Message 或 provider 认证；完整 Rust 门禁通过 56 个常规 tests、5 个真实 Agent 闭环、3 个 CLI、2 个 Computer lifecycle 和 1 个 migration test，1 个手工 live-provider smoke 按设计 ignored
2026-07-27 | Channel ambient 真实闭环 | `cargo test --test agent_dm -- --nocapture`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && git diff --check` | 复用真实 PostgreSQL、Server、daemon、Builtin 与 Agent CLI harness，验证 Human 在 `#general` 连续发送 5 条无 mention 的普通 Message 后只形成一个 `channel_activity/ambient` Item，`first_seq=1`、`last_seq=5`、`message_count=5` 且默认 debounce 固定为首条后 5 秒；daemon 到期只启动一个 run，Agent 通过真实 CLI 读取唯一 leased Item 与完整五条 Channel 上下文后显式 ack 保持沉默，后续两个 poll 周期不再唤醒且没有 Agent Message；日志未泄露 Message 或 provider 认证；完整 Rust 门禁通过 56 个常规 tests、6 个真实 Agent 闭环、3 个 CLI、2 个 Computer lifecycle 和 1 个 migration test，1 个手工 live-provider smoke 按设计 ignored
2026-07-27 | Thread Agent 真实闭环 | `cargo test --test agent_dm -- --nocapture`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && git diff --check` | 复用真实 PostgreSQL、Server、daemon、Builtin 与 Agent CLI harness，验证 Thread mention 以 `#general:1` hard Item 唤醒唯一 run；Agent 通过真实 `inbox current` 和 `thread read` 读取 root、两条 replies、root 前 Channel 背景与 snapshot=4，再用真实 `message send --based-on 4 --handle` 将唯一回复写入同一 Thread seq=5 并原子 handle；Browser Thread API 同步返回 reply 的 `thread_id=1`，修复其此前错误返回 null 的协议缺口；完整 Rust 门禁通过 56 个常规 tests、7 个真实 Agent 闭环、3 个 CLI、2 个 Computer lifecycle 和 1 个 migration test，1 个手工 live-provider smoke 按设计 ignored
2026-07-27 | Context freshness 真实闭环 | `cargo test --test agent_dm -- --nocapture`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && git diff --check` | 同一真实 Thread run 在 `snapshot_channel_seq=4` 读取后由 Human 追加 seq=5 reply；真实 `sumi agent message send --based-on 4 --handle` 以 exit 5 返回结构化 `context_changed`，changes 仅含 Message/地址/author 元数据且无正文，PostgreSQL 验证 stale Message 数量为 0、原 hard Item lease 未丢失；Builtin 在同一 run 重读 snapshot=5 后以新 snapshot 写入 seq=6 并原子 handle。完整门禁通过 56 个常规 tests、7 个真实 Agent 闭环、3 个 CLI、2 个 Computer lifecycle 和 1 个 migration test，1 个 live-provider smoke ignored；本地自动化配置 `computer.open_pairing_browser=false` 后仍走同一授权 API 配对且不再打开浏览器标签页
2026-07-27 | Agent 权限边界真实闭环 | `cargo test --test agent_dm -- --nocapture`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && git diff --check` | 复用同一真实 PostgreSQL、Server、daemon、Builtin 与 Agent CLI harness，将 Agent 提升为 Admin 后仍以真实 `channel read` 拒绝未显式加入的 private Channel 且不泄露正文；Builtin sandbox 中伪造 `SUMI_RUN_TOKEN` 的 `whoami` 被拒，同 OS 用户下另一个已 provision Agent Home 的可读 marker 被隐藏；另一 Computer Token 调用当前 Computer Agent action 返回 401。原 hard mention 最终由真实 CLI ack，SQL 验证 private membership 仍为 0、private Message 保留且唯一 run handled；完整门禁通过 56 个常规 tests、8 个真实 Agent 闭环、3 个 CLI、2 个 Computer lifecycle 和 1 个 migration test，1 个 live-provider smoke ignored
2026-07-27 | 注册与 Space 真实闭环 | `cargo test --test registration_space -- --nocapture`；`pnpm --dir web test -- RegisterPage.test.tsx --run`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && pnpm --dir web lint && pnpm --dir web build && git diff --check` | 真实隔离 PostgreSQL 与 `sumi server` 进程验证注册即创建 Session、邮箱标准化、`sumi-lab` 可访问、Owner/Human Member/general/membership/audit/outbox 单事务事实，以及大小写等价和重复 slug 均不能创建任何残留 Space；修复受保护深链 401 时反复嵌套 login redirect 的循环，并验证 query 保留、登录成功回到原 URL、注册/登录切换不丢 redirect。完整 Rust 门禁通过 56 个常规 tests、8 个 Agent 真实闭环、3 个 CLI、2 个 Computer lifecycle、1 个 migration 和 1 个注册/Space 真实进程测试；13 个 Web tests、lint 与 production build 通过，1 个 live-provider smoke ignored
2026-07-27 | Attachment 真实进程闭环 | `cargo test --test attachment_flow -- --nocapture`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && git diff --check` | 真实隔离 PostgreSQL、`sumi server` 与本地对象存储验证 Browser create/PUT/complete、服务端长度与 SHA-256 复算、ready 前和摘要错误时不可关联、未关联不可下载、Channel Member 精确下载和非 Member 拒绝；重复关联稳定返回 `invalid_attachment`，且失败 Message、Channel sequence、attachment.ready/message.created outbox 与幂等完成事务事实均通过真实 SQL 验证，日志不含 Attachment 正文。完整 Rust 门禁通过 56 个常规 tests、8 个 Agent 真实闭环、1 个 Attachment、3 个 CLI、2 个 Computer lifecycle、1 个 migration 和 1 个注册/Space 真实进程测试，1 个 live-provider smoke ignored
2026-07-27 | Agent lifecycle 真实进程闭环 | `cargo test --test agent_lifecycle -- --nocapture`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && git diff --check` | 真实隔离 PostgreSQL、`sumi server`、`sumi computer`、Builtin fake provider、sandbox shell 与真实 `sumi agent` CLI 验证 `stop_after_current` 保持当前 run 可继续 ack 并完成，`cancel_now` 终止当前 run、Inbox 回到 pending 且 retry 精确加一，resume 后同一 Agent identity 重领并处理，retire 保留 Member、DM Message、Agent Home；重复 lifecycle key 不重复 command，持久 command/audit/outbox 与日志 redaction 通过真实 SQL/进程验证。修复 suspended Agent 的既有 active run 被 Server CLI 鉴权错误拒绝的根因；完整 Rust 门禁通过 56 个常规 tests、8 个 Agent 对话闭环、1 个 Agent lifecycle、1 个 Attachment、3 个 CLI、2 个 Computer lifecycle、1 个 migration 和 1 个注册/Space 真实进程测试，1 个 live-provider smoke ignored
2026-07-27 | Agent Memory 真实进程闭环 | `cargo test --test agent_memory -- --nocapture`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && git diff --check` | 真实隔离 PostgreSQL、`sumi server` 与 `sumi computer` 验证 provision/configure 后完整 Memory metadata snapshot、Owner 在线临时读取与 `Cache-Control: no-store`、Computer heartbeat 过期后拒绝读取且不下发 command；Memory 正文未进入 PostgreSQL computer command/idempotency/audit/outbox、daemon SQLite 或日志，持久 command result 仅保留状态；超过 1 MiB、非 UTF-8、symlink/canonical path 逃逸与非法相对路径均被拒绝；完整 Rust 门禁全部通过
2026-07-27 | Agent Channel create 真实进程闭环 | `cargo test --test agent_dm builtin_agent_creates_channels_with_permission_and_admin_through_real_cli -- --nocapture`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && git diff --check` | 复用真实隔离 PostgreSQL、`sumi server`、`sumi computer`、Builtin fake provider、sandbox shell 与真实 `sumi agent` CLI，验证普通 Agent 无权限创建拒绝且无残留，Owner 授予 `channel:create` 后创建 private Channel，升为 Admin 且显式权限清空后仍按矩阵创建 public Channel；真实 SQL 验证 Agent 创建者、唯一 creator membership、audit、outbox、成功幂等记录与三个 run/Inbox 显式 handled。完整 Rust 门禁通过 56 个常规 tests、9 个 Agent 真实闭环、1 个 Agent lifecycle、1 个 Agent Memory、1 个 Attachment、3 个 CLI、2 个 Computer lifecycle、1 个 migration 和 1 个 registration/Space 测试，1 个 live-provider smoke ignored
2026-07-27 | Agent create Approval 离线边界审计 | `cargo test --all-features`；`cargo fmt --all -- --check`；`cargo clippy --all-targets --all-features -- -D warnings` | 修正 Approval 申请阶段错误要求目标 Computer 在线的问题：申请现在只校验 Computer 属于当前 Space，允许在 offline 状态创建 pending Approval；只有 Human approve 阶段仍要求 Computer online 并写入 provisioning command。22.9 完整真实 Agent CLI approval/reject/provision 仍未勾选，留给下一 session。
2026-07-27 | Agent create Approval 真实进程闭环 | `cargo test --test agent_dm builtin_agent_create_requires_human_approval_and_provisions_through_real_daemon -- --nocapture`；`cargo test --test agent_dm -- --nocapture`；`cargo fmt --all -- --check && cargo clippy --all-targets --all-features -- -D warnings && cargo test --all-features && git diff --check` | 真实隔离 PostgreSQL、`sumi server`、`sumi computer`、Builtin fake provider、sandbox shell 与真实 `sumi agent create` 验证 Agent Admin 仍只创建 pending Approval/Human Inbox/audit/outbox/idempotency，Computer Token 无权审批；daemon offline 时 Human approve 返回冲突且不创建 Member/command/Home，同一 Computer 重连后 Human approve 原子创建 provisioning command 并由真实 daemon 建立、校验、激活子 Agent Home；reject 不留下 Member、command 或目录。完整 Rust 门禁通过 56 个常规 tests、10 个 Agent 真实闭环、1 个 lifecycle、1 个 Memory、1 个 Attachment、3 个 CLI、2 个 Computer lifecycle、1 个 migration 和 1 个 registration/Space 测试，1 个 live-provider smoke ignored。
