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

- [ ] 确认并记录 Web 技术栈，完成 Rust/Web 工具链、统一任务命令和配置加载。
- [ ] 建立单一 `sumi` binary 的命令树、模块边界、PostgreSQL/SQLite migrations 和测试基础设施。
- [ ] 在本机安装并验证 PostgreSQL，保证测试可创建隔离 database 或 schema 并自动清理。

### 1. Human 与 Space

- [ ] 实现注册、登录、退出、Session 与限速。
- [ ] 实现 Space 创建、全局唯一 slug、Owner、general 初始化和 Space shell UI。
- [ ] 实现 Member 列表、邀请、Owner/Admin/Member 与显式 permissions。

### 2. 协作主路径

- [ ] 实现 public/private Channel、DM、membership 和归档。
- [ ] 实现 Message、mention、Channel 内数字 Thread ID、分页与 context snapshot。
- [ ] 实现 Attachment 上传、完成、关联、下载和本地/S3-compatible storage adapter。
- [ ] 实现 transactional outbox、Browser SSE 重放和 Human Inbox。
- [ ] 完成 Channel/Thread/DM 的桌面与移动 UI、composer 和关键组件测试。

### 3. Computer

- [ ] 实现 `sumi computer` 初始化、浏览器配对、受限 `secrets.json` 和撤销。
- [ ] 实现 Computer WebSocket、heartbeat、离线判定、重连、持久 command sequence、ACK 与幂等重放。
- [ ] 实现 daemon SQLite、本地目录、Unix socket、Agent Home 隔离和进程 supervisor。
- [ ] 完成 Computer 管理 UI 和崩溃/重启恢复测试。

### 4. Agent 与 Codex

- [ ] 实现 Agent 创建、配置、暂停、恢复、退役、Role revision 和 Memory。
- [ ] 实现通用 Driver 契约与 Codex Driver；Driver 私有状态不得成为 Agent 身份或 Memory。
- [ ] 实现完整 `sumi agent` 命令树、run capability、结构化 JSON、权限校验和幂等写入。
- [ ] 在设计 Agent run prompt 前探索 `.slock` 的 Agent prompt，按 `docs/design.md` 约束吸收适合 Sumi 的结构。
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

- 设计阶段完成了 `GLOSSARY.md`、`docs/design.md` 和 Raft blog references。
- 实现尚未开始；所有完成项必须从未勾选状态以实际代码和测试推进。
- 下一步：执行 Phase 0，先消除仍未确定的 Web 技术栈并建立可运行工程基线。

## 验证记录

尚无。格式：`YYYY-MM-DD | 阶段/项目 | 命令或证据 | 结果`。
