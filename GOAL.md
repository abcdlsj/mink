# Sumi v1 Goal

## 最终目标

从当前全新实现出发，持续开发直到 Sumi v1 成为可在一台 macOS 或 Linux 机器上安装、运行和完整测试的产品：Human 可以注册并创建 Space，Human 与 Agent 能在 Channel、DM 和 Thread 中平等协作，Computer daemon 能可靠承载使用 Codex Driver 的 Agent，Agent 只能通过统一的 `sumi agent` CLI 获取上下文并行动。

完成意味着 `docs/design.md` 第 22 节全部验收场景真实通过，而不是只存在页面、类型、mock、占位实现或尚未运行的测试。

## 开始和续跑规则

1. 每次开始或续跑前，必须完整阅读 `AGENTS.md`，随后阅读本文件、`GLOSSARY.md`、`docs/design.md` 及当前代码。
2. 当前工作区是唯一基线；禁止查看 Git 历史或寻找被删除的旧实现。
3. 从最早的未完成阶段继续，保持可运行的纵向闭环；除真正阻塞外持续实现、验证和修复，不因阶段性汇报停工。
4. `docs/design.md` 明确留给实现者的工程选择，采用最简单、成熟且维护活跃的方案，并把选择补回设计文档；不得借机改变产品语义。
5. 每完成一项，在下方勾选并在“验证记录”追加简短证据。验证失败就继续修，不得提前标记完成。
6. 写完可提交基线，可以先 commit 代码。

## 完成定义

- [ ] 一个 Rust Cargo package 产出唯一 `sumi` binary，`sumi server`、`sumi computer`、`sumi agent` 三个入口可用。
- [ ] macOS 可在不依赖 Docker、Kubernetes 或远程服务的情况下安装 PostgreSQL、完成构建和全部测试。
- [ ] Linux 构建和测试通过；macOS/Linux 的本地目录、权限和 Unix socket 行为一致。
- [ ] PostgreSQL migrations、约束、事务、outbox 和 repository integration tests 覆盖规范中的关键不变量。
- [ ] WebUI 完成 Neo-Brutalism 与 pixel art avatar 设计，桌面和移动端可用，且不复刻 Raft。
- [ ] Human 注册、Session、Space、Member、权限、Channel、DM、Thread、Message、Attachment 和 Human Inbox 闭环可用。
- [ ] Browser HTTP 写入与 SSE 推送支持重连和补偿，断线不影响业务正确性。
- [ ] Computer 配对、WebSocket、heartbeat、重连、command ACK/重放、撤销和本地状态恢复可用。
- [ ] Agent 创建、Role、Memory、生命周期、Codex Driver 和受限本地 CLI 可用。
- [ ] DM、mention、ambient attention、Thread subscription、lease/retry/dead、context freshness 和 send-and-handle 语义通过故障测试。
- [ ] Agent 创建 Agent 的 Human Approval、Agent Admin 例外和 private Channel 权限通过安全测试。
- [ ] `docs/design.md` 第 22 节所有端到端验收通过，统一 test command 成功，文档与实际行为一致。

## 实施阶段

### 0. 工程基线

- [x] 确认并记录 Web 技术栈，完成 Rust/Web 工具链、统一任务命令和配置加载。
- [x] 建立单一 `sumi` binary 的命令树、模块边界、PostgreSQL/SQLite migrations 和测试基础设施。
- [x] 在本机安装并验证 PostgreSQL，保证测试可创建隔离 database 或 schema 并自动清理。

### 1. Human 与 Space

- [x] 实现注册、登录、退出、Session 与限速。
- [x] 实现 Space 创建、全局唯一 slug、Owner、general 初始化和 Space shell UI。
- [x] 实现 Member 列表、邀请、Owner/Admin/Member 与显式 permissions。

### 2. 协作主路径

- [x] 实现 public/private Channel、DM、membership 和归档。
- [x] 实现 Message、mention、Channel 内数字 Thread ID、分页与 context snapshot。
- [x] 实现 Attachment 上传、完成、关联、下载和本地/S3-compatible storage adapter。
- [x] 实现 transactional outbox、Browser SSE 重放和 Human Inbox。
- [x] 完成 Channel/Thread/DM 的桌面与移动 UI、composer 和关键组件测试。

### 3. Computer

- [x] 实现 `sumi computer` 初始化、浏览器配对、受限 `secrets.json` 和撤销。
- [x] 实现 Computer WebSocket、heartbeat、离线判定、重连、持久 command sequence、ACK 与幂等重放。
- [x] 实现 daemon SQLite、本地目录、Unix socket、Agent Home 隔离和进程 supervisor。
- [x] 完成 Computer 管理 UI 和崩溃/重启恢复测试。

### 4. Agent 与 Codex

- [ ] 实现 Agent 创建、配置、暂停、恢复、退役、Role revision 和 Memory。
- [ ] 实现通用 Driver 契约与 Codex Driver；Driver 私有状态不得成为 Agent 身份或 Memory。
- [ ] 实现完整 `sumi agent` 命令树、run capability、结构化 JSON、权限校验和幂等写入。
- [x] 在设计 Agent run prompt 前探索 `.slock` 的 Agent prompt，按 `docs/design.md` 约束吸收适合 Sumi 的结构。
- [ ] 打通 Human DM Agent -> Inbox -> Codex -> CLI read -> CLI send-and-handle -> Human 收到回复。

### 5. 注意力与治理

- [ ] 实现 DM/@mention hard Inbox、普通 Channel ambient 聚合和 Agent 自主 ack/defer/reply。
- [ ] 实现 Thread subscription、跨授权 Channel 读取、lease 续期、retry/dead 和重启恢复。
- [ ] 实现 `--based-on` context freshness，避免 Agent 基于过期 Thread 上下文强发。
- [ ] 实现 Channel create permission、Agent create Approval、Human-only 审批和 Agent Admin 例外。

### 6. 安全、可靠性与最终验收

- [ ] 实现 BYOK envelope、redaction、audit、rate limit、删除/保留和 prompt injection 边界。
- [ ] 完成并发、崩溃、断线、重复 command、重复 Message、权限越界和数据约束故障测试。
- [ ] 达到 `docs/design.md` 的关键性能目标，或记录可复现证据与已接受偏差。
- [ ] 运行最终 Playwright desktop/mobile 验收和必要截图，逐项通过 `docs/design.md` 第 22 节。
- [ ] 运行统一 format、lint、typecheck、unit、integration、CLI、Web 和 E2E 命令，清除占位实现与无主 TODO。

## 测试预算

Playwright 不是日常开发循环。默认只安排两次完整浏览器检查：协作 UI 主路径稳定后的里程碑 smoke，以及 Phase 6 最终验收。期间只在修复明确浏览器问题时运行相关 spec、单一 viewport；布局探索优先组件测试和人工快速查看，禁止反复全量截图消耗上下文。

Rust 和数据正确性不能因此偷懒：修改后先跑最小相关测试，每个阶段结束跑阶段完整测试，最终运行统一 test command。PostgreSQL 事务、约束和 SQL 必须由真实 PostgreSQL integration tests 验证。

## 明确不做

遵守 `docs/design.md` 第 4.2 节。尤其不实现 Work/Task、Builtin Driver、其他具体 Driver、微服务、工作流/DAG、向量搜索、Agent marketplace、Windows 支持或 Agent 热迁移，不用这些旁支拖延 v1 主闭环。

## 当前状态

- Phase 0 工程基线已完成：单 Cargo package、Rust/Web 工具链、PostgreSQL/SQLite migrations、统一 mise tasks 和同源 Web shell 可运行。
- Phase 1 Human 与 Space 已完成：注册/Session、Space 初始化、统一 Member 列表、邮箱邀请和权限更新形成可运行闭环。
- Phase 2 协作主路径已完成：Channel/DM/Thread/Message/Attachment、SSE replay、Human Inbox 与桌面/移动 UI 形成闭环。
- Phase 3 Computer 已完成：配对、在线状态、管理 UI、SQLite/Unix socket、command 恢复、真实 Codex Driver supervisor 与同机 Agent sandbox 已形成闭环。
- 当前最早未完成阶段为 Phase 4 Agent 与 Codex；下一步实现 Agent lifecycle/Role/Memory 与完整 `sumi agent` 命令，并打通 Human DM Agent 的注意力纵向路径。
- Phase 4 Agent lifecycle/Role/Memory 后端与管理 UI 已形成纵向路径；下一步完成 `sumi agent` 命令和 Human DM Agent 注意力闭环，整项验收通过前不勾选 Phase 4 主项。
- Phase 4 Human DM Agent 的 Server/Computer/CLI 动作纵向路径已打通；仍需补齐完整 CLI 命令面和真实 daemon/Driver 里程碑验收，未完成前不勾选 Phase 4。
- Phase 4 Agent Attachment CLI 已形成 upload/info/download 与 Message 关联纵向路径；下一步补齐 channel create、agent create 及 channel read 的剩余分页参数，再做真实 daemon/Codex 里程碑验收。

## 验证记录

格式：`YYYY-MM-DD | 阶段/项目 | 命令或证据 | 结果`。

2026-07-25 | Phase 0 / 工具链与统一任务 | `mise run install`; `mise run build`; `mise run lint`; `mise run test` | Rust/Web 构建、clippy `-D warnings`、typecheck、组件测试、CLI/SQLite/PostgreSQL tests 全部通过。
2026-07-25 | Phase 0 / PostgreSQL | Homebrew PostgreSQL 17.10；integration test 创建独立 database、执行 migration、校验约束并自动 drop | 通过。
2026-07-25 | Phase 0 / 运行入口 | `mise run run`; `GET /api/v1/health`; `sumi computer --server http://127.0.0.1:3000` | Server/数据库/WebUI 同源启动成功，Computer SQLite 初始化并正常退出。
2026-07-25 | Phase 1 / Auth 与 Space | 隔离 PostgreSQL HTTP flow：注册重试、logout/login、Session、Space/Owner/general/audit/outbox 与 slug 查询；`mise run test` | 通过。
2026-07-25 | Phase 1 / Member、邀请与权限 | Owner 邀请并授予 Admin、Admin 邀请并授予显式权限、越权更新 403、邀请单次接受/general membership；migration constraints 与 Web 组件测试 | 通过。
2026-07-25 | Phase 1 / 统一门禁与运行态 | `mise run lint`; `mise run test`; `mise run build`; `mise run run`; `GET /api/v1/health`; `GET /s/sumi-lab/members` | 严格 clippy、Rust/PostgreSQL/Web tests、production build 及 SPA deep-link smoke 全部通过。
2026-07-25 | Phase 2 / Channel 与 Message 首条路径 | 隔离 PostgreSQL HTTP flow：channel:create 权限、public Discover/Join、private 隔离、Message seq/幂等/分页、mention Inbox；Web Channel/composer 组件测试 | 通过，Thread/DM/归档尚未完成，未勾 Phase 2 项。
2026-07-25 | Phase 2 / Thread 后端 | 隔离 PostgreSQL HTTP flow：原子数字 Thread ID、root 约束、reply 共用 Channel seq、订阅、mention、snapshot read 与嵌套 root 拒绝 | 通过，Thread pane 尚未完成。
2026-07-25 | Phase 2 / Thread UI | Web 组件交互：打开既有 Thread、显示 root/replies、发送回复并更新 pane；desktop 并列/mobile 全屏 CSS | 通过，最终浏览器 viewport 验收留到 UI 里程碑。
2026-07-25 | Phase 2 / DM 与归档 | 对称 DM 创建与两人约束、主线/Thread direct hard Inbox 去重、Channel 单向归档及历史只读；`cargo clippy --all-targets --all-features -- -D warnings`; `cargo test --all-features`; Web lint/5 个组件测试 | 通过，Phase 2 尚有 Attachment、SSE 和 Human Inbox，未勾阶段项。
2026-07-25 | Phase 2 / Attachment | 本地目录与 S3-compatible `object_store` adapter、分块上传、size/SHA-256 完成校验、ready 后 Message/Thread 关联、可见性下载；严格 Rust/真实 PostgreSQL/Web 门禁 | 通过，Phase 2 尚有 SSE 和 Human Inbox，未勾阶段项。
2026-07-25 | Phase 2 / SSE、Human Inbox 与 Message lifecycle | 持久 outbox event ID/Last-Event-ID replay、Human 私有 Inbox ack/defer、Message 编辑/软删除；真实 HTTP/PostgreSQL flow 与 6 个 Web 组件测试 | 通过。
2026-07-25 | Phase 2 / 阶段验收 | `pnpm --dir web test:e2e`; `mise run lint`; `mise run test`; `mise run build`; 运行态 migration 7、health、Inbox/DM deep-link | Playwright desktop smoke、严格 clippy、12 个 Rust/DB/CLI tests、6 个 Web tests及 production build 全部通过，Phase 2 完成。
2026-07-25 | Phase 3 / Computer 配对与本地安全 | 真实 PostgreSQL/HTTP flow：Member 403、错 code、Owner 确认、hash-only credential、过期落库；本机 state/secrets/socket 权限与 capability tests | 通过，配对 result 可幂等恢复且 revoke 后旧 credential 401。
2026-07-25 | Phase 3 / 连接与恢复 | 真实 WebSocket hello/heartbeat/command ACK+result/revoke；SQLite command 去重、冲突拒绝、process_lost 与 provision 重启恢复 | 通过，10 秒 heartbeat、30 秒 offline monitor、指数退避+jitter 和 at-least-once 重放生效。
2026-07-25 | Phase 3 / UI 与运行态 | Computer 配对/列表/状态/Revoke 组件测试；独立 Server+真实 daemon 配对，online 后停止 daemon，38 秒自动 offline；`mise run lint/test/build` | 通过；17 Rust tests、2 CLI tests、真实 PostgreSQL migration、8 Web tests、production build 全绿。
2026-07-25 | Phase 3 / Driver supervisor 与 Agent sandbox | 真实 macOS `sandbox-exec` 子进程和已安装 `codex --version`；并发排队、单 Agent 串行、timeout、daemon shutdown/graceful cancel/process group kill、SQLite process_lost；`mise run lint/test/build` | 通过；Computer credential/其他 Agent Home 不可读，prompt 走 stdin，环境变量白名单，24 Rust tests、2 CLI、真实 PostgreSQL migration、8 Web tests 与 production build 全绿。
2026-07-25 | Phase 4 / Agent prompt 前置探索 | 当前工作区检查 `.slock` 不存在；`docs/design.md` 第 13.2 节记录事实与后续约束 | 通过；无可吸收的既有 prompt，按 Sumi 最小 run 输入独立设计。
2026-07-25 | Phase 4 / Agent lifecycle、Role 与 Memory 元数据 | 真实 PostgreSQL/WebSocket flow：Role revision、configure/suspend/resume command、Memory SHA-256 快照；daemon profile/sandbox run 门禁；Agent detail 组件；`mise run lint/test/build` | 通过；25 Rust tests、2 CLI、migration、9 Web tests和 production build 全绿，Phase 4 主闭环尚未完成。
2026-07-25 | Phase 4 / DM Inbox 与 send-and-handle | 真实 PostgreSQL/HTTP/WebSocket flow：Human DM hard Inbox、claim/run lease、Role prompt、Inbox/DM read、context_changed、原子 Message+handled、失败 release/retry；`mise run lint/test/build` | 通过；25 Rust tests、2 CLI、migration、9 Web tests和 production build 全绿，完整 CLI 尚未完成。
2026-07-25 | Phase 4 / Agent Attachment CLI | Agent Home canonical path/symlink 边界；Computer credential + active run scoped create/PUT/complete/info/download；Agent 上传、Message 关联、未关联下载拒绝；`mise run lint/test/build` | 通过；26 Rust tests、2 CLI、真实 PostgreSQL integration、9 Web tests和 production build 全绿，完整 CLI 尚未完成。
