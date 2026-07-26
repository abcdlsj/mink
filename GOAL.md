# Sumi v1 当前目标

## 产品结果

先完成一个真实可用的 Human 与 Agent 协作产品：Human 配对一台 Computer，在该 Computer 上使用本地配置创建 Builtin Agent，并在 DM、Channel 和 Thread 中触发 Agent；Agent 必须通过 `sumi agent` CLI 读取上下文、回复或明确 ack/defer，且 Inbox 状态与 Message 写入满足设计中的事务和恢复不变量。

当前 WebUI、Server API、daemon、CLI 和 Driver 都已有局部实现，但“模块存在”和“测试通过”不等于产品闭环完成。此前“Phase 0–5 已完成”的判断作废；以下真实纵向验收是唯一完成依据。

## 当前基线判断

- Human、Space、Channel、DM、Thread、Message、Attachment、Computer、Agent、Inbox 和 Approval 已有 schema、API 或 UI 基线，保留现有已通过测试作为回归保护。
- Computer 身份已收敛为单一 Computer Token：raw Token 仅保存在本机受限 `secrets.json`，Server 与配对记录只保存 hash，配对页只显示不可逆短 fingerprint；重连与删除生命周期已通过真实进程验收。
- Builtin 已从显式 Computer-local source paths 加载 Pi-compatible settings/models/auth，选中并规范化 provider/model，只接受声明的 OpenAI-compatible completions；认证缓存限制在本机 `secrets.json` 与 daemon 所需内存，旧环境变量入口已删除。
- 现有 Server 集成测试以手工 WebSocket frame 模拟 daemon，Builtin 测试以 mock provider 验证文件工具；没有测试启动真实 Server、daemon、Builtin 和 `sumi agent` CLI 跑通一条 DM、Channel 或 Thread 对话。
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
- [ ] 用本机 Pi 的 `deepseek/deepseek-v4-pro` 配置完成一次不泄露认证的 Builtin provider smoke check；自动测试使用本地 fake provider，不连接收费服务。

### 2. Agent DM 真实闭环

- [ ] 增加真实进程级测试：启动临时 PostgreSQL、Sumi Server、Computer daemon、本地 fake OpenAI-compatible provider 和 Builtin Agent，由 Human 发送 DM，不能用手工 command frame 代替 daemon。
- [ ] 验证 daemon claim hard Inbox、启动唯一 Agent Run，Builtin 通过 sandbox 内真实 `sumi agent inbox current` 与 `channel read` 读取 DM，再用 `message send --handle` 回复。
- [ ] 验证 Agent Message 作者、Channel sequence、结构化地址、SSE 更新和 Inbox handled；Message 与 handled 必须在同一 PostgreSQL 事务提交。
- [ ] 验证模型最终文本和 Driver stdout 不会自动成为 Message；只有 `sumi agent message send` 能发布。
- [ ] 验证发送前 Driver 失败会 release/retry，发送并 handle 后 daemon 崩溃不会重复回复，连续失败最终 dead 并通知 Human Admin/Owner。

### 3. Channel 与 Thread Agent 闭环

- [ ] Channel mention：Agent 读取 `#channel` 上下文、结构化 mentions 和 snapshot sequence，并能回复、ack 或 defer。
- [ ] Channel ambient：连续普通 Message 聚合为一个 Inbox Item，只启动一次 run；Agent 自己判断是否回应，daemon 不做内容分类。
- [ ] Thread：Agent 区分 `#channel` 与 `#channel:{thread-id}`，`thread read` 返回 root、replies、Channel 背景和 snapshot，回复落在正确 Thread。
- [ ] Context freshness：Human 在 Agent 读取后追加 Message 时，旧 `--based-on` 返回 `context_changed` 且不创建 Message；Agent 重读后可成功回复。
- [ ] 权限边界：private Channel 只认 membership；Agent Admin、其他 Computer、其他 Agent Home 和伪造 run token 都不能越权。
- [ ] 用同一个真实进程 harness 覆盖 DM、mention、ambient、Thread 与 context_changed，避免为每种场景复制整套基础设施。

### 4. 产品能力收口

- [ ] 逐项审计 `docs/design.md` 第 22.1、22.2、22.5–22.9 节，补齐尚未形成真实闭环的注册/Space、Attachment、Agent lifecycle、Memory、Channel create、Agent create Approval 和 Admin 治理行为。
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
