# Sumi v1 当前目标

## 产品结果

交付一个真实可用的 Human 与 Agent 协作产品：Human 从空环境创建 Space、配对 Computer、使用本机配置创建 Builtin Agent，并在 DM、Channel 和 Thread 中与 Agent 协作。Agent 必须通过 `sumi agent` CLI 读取上下文、回复或明确 ack/defer；Inbox、Message、Attachment、Approval 和治理写入必须满足设计中的事务、权限、幂等与恢复不变量。

“模块存在”和“测试通过”不等于产品闭环完成。以下真实纵向验收和最终平台门禁是唯一完成依据。

## 当前基线

- Computer 身份已统一为长期 Computer Token；raw Token 只在本机受限 `secrets.json`，Server 只保存 hash。Pair、offline/reconnect、Server restart 和 Delete 后撤销均有真实进程证据。
- Builtin 从显式 Computer-local Pi-compatible settings/models/auth 加载 provider；只接受声明的 OpenAI-compatible completions，认证不进入 Agent Home、工具环境或日志。
- 同一真实进程 harness 已覆盖 DM、Channel mention/ambient、Thread、context freshness、权限边界、崩溃恢复、Channel create 和 Agent create Approval。
- 注册/Space、Attachment、Agent lifecycle、Memory 另有真实 Server/daemon/PostgreSQL 闭环。
- 当前最早缺口是 Agent Admin 产品承诺与 Agent CLI 治理能力不一致；必须先修规范，不能用 Human Browser Session 或手写 Computer frame 冒充 Agent 能力。

## 执行规则

1. 每个开发 session 或上下文压缩续跑后，完整阅读 `AGENTS.md`、本文件、`GLOSSARY.md` 和 `docs/design.md`；具体开发前重读对应设计章节。
2. 从最早未完成项开始，执行“审计现状 -> 修正规范冲突 -> 完成真实纵向路径 -> 定向测试 -> 完整门禁 -> 更新进度”。
3. 当前工作区是唯一实现基线；保留用户和其他 Agent 的未提交修改，不读取、恢复或推断 Git 历史旧实现。
4. 行为、协议、数据模型或领域名词改变时，先更新 `docs/design.md` 或 `GLOSSARY.md`；不做兼容层、双写或旧 schema 迁移。
5. 只有真实进程和真实 SQL 通过对应验收后才能勾选；handler 存在、mock WebSocket frame 或孤立单元测试不能替代纵向闭环。
6. 每个语义完整、可独立回滚的基线应及时更新本文件并提交，但不得为了提交机械切碎纵向路径。
7. 默认在当前 session 连续推进；只有用户明确要求、上下文已无法可靠承载，或必须切换到独立平台/权限环境时才 handoff 或新建 session。不得自动创建承接 session。

## 已完成基线

- [x] 产品模型收敛：v1 删除 BYOK、Secret Envelope、Server 模型 Secret、业务 metrics、性能基准和 p95 门槛；Builtin 使用 Computer-local Pi-compatible 配置。
- [x] Computer 与 Driver：Computer Token 单一身份、Pair/reconnect/Delete 生命周期、Builtin provider/sandbox/认证边界和本机 live provider smoke。
- [x] Agent 对话：DM、Channel mention/ambient、Thread、context freshness、send-and-handle 原子性、Driver 输出发布边界、retry/dead/crash recovery 和权限隔离。
- [x] 产品纵向能力：注册/Space、Attachment、Agent lifecycle、Memory 均通过真实 Server/daemon/PostgreSQL 验收。
- [x] Agent Channel create：显式 `channel:create`、Agent Admin 默认能力、public/private membership、audit、outbox 和幂等事务事实。
- [x] Agent create Approval：真实 `sumi agent create` 只创建 pending；Human 审批；offline 保持 pending 并可重试；approve 后真实 daemon provision；reject 零 Member/command/Home 残留。

## 剩余工作

### 1. Agent Admin 规范与治理闭环

- [ ] 先修正 `docs/design.md` §8.2、§14.3、§17 和 §22.2 的冲突：逐项明确 Agent Admin 在 v1 可通过 `sumi agent` 到达的治理动作，以及必须保持 Human-only 的动作。当前缺失入口包括 Space name/accent、Human 邀请/移除、Channel 成员/归档、Agent suspend/resume、audit 读取；不得默认把所有 Browser API 原样暴露给 Agent。
- [ ] 按修正后的唯一规范补齐最小治理协议、权限校验、audit/outbox/idempotency，并用真实 Builtin + `sumi agent` + PostgreSQL 证明 Agent Admin 可执行允许动作、不能执行 Human-only 动作、不能绕过 private Channel membership。

### 2. 并发、幂等与 PostgreSQL 不变量

- [ ] 补齐同 Agent 单 active run、Computer 并发上限、Thread/Message sequence、Inbox lease 竞争、重复 command、重复 Message/Attachment 和幂等 key payload 冲突的并发证据。
- [ ] 用真实 PostgreSQL integration tests 系统验证 schema、复合外键、唯一约束、事务回滚和 transactional outbox；不得以内存 fake 替代 SQL 验收。

### 3. 安全、治理与保留策略

- [ ] 系统审计 Message、Attachment、Memory、Computer Token 和模型认证的日志 redaction。
- [ ] 补齐治理与敏感操作 audit、注册登录及高风险写操作 rate limit、prompt injection 边界，以及删除/保留策略。

### 4. 平台与最终验收

- [ ] 在 macOS 和 Linux 完成构建、测试与 sandbox 验收，确认 `sandbox-exec`/bubblewrap、目录权限、Unix socket、进程组取消和重连行为。
- [ ] 按 `docs/design.md` §22 逐项运行真实端到端验收并记录可复现结果。
- [ ] 使用 Playwright 验收 1440x900、1024x768、390x844，覆盖 Channel、Attachment、Thread、长内容、offline/deleted、响应式、键盘、reduced motion 和 screen reader labels。
- [ ] 执行统一 format、clippy `-D warnings`、typecheck、lint、Rust unit/integration、真实 PostgreSQL、CLI、Web component、production build 和 E2E 门禁。
- [ ] 清除占位实现、无主 TODO、过期入口和文档偏差；最终 `git diff --check` 通过。

## 最新验证基线

2026-07-27：

- `cargo fmt --all -- --check`
- `cargo clippy --all-targets --all-features -- -D warnings`
- `cargo test --all-features`：56 个常规 tests、10 个 Agent 真实闭环、1 lifecycle、1 Memory、1 Attachment、3 CLI、2 Computer lifecycle、1 migration、1 registration/Space 通过；1 个手工 live provider smoke ignored。
- `cargo test --test agent_dm -- --nocapture`：10 个真实 Agent 对话与治理场景通过。
- `cargo test --test agent_lifecycle -- --nocapture`
- `cargo test --test agent_memory -- --nocapture`
- `cargo test --test attachment_flow -- --nocapture`
- `cargo test --test registration_space -- --nocapture`
- `cargo test --test computer_lifecycle -- --nocapture`
- 历史 Web 基线：`pnpm --dir web test && pnpm --dir web lint && pnpm --dir web build`；最终仍需重新运行完整 Web/Playwright 门禁。

## 完成定义

- Human 可从空环境完成 Space 创建、Computer Pair、Builtin Agent 创建，并在 DM、Channel 和 Thread 与 Agent 完整协作。
- Computer offline/reconnect 不丢 Pair；Delete 后旧 Token 永久失效。
- Agent 注意力、CLI 读取/写入、治理、Inbox handle、重试、幂等和权限边界均由真实进程与真实 PostgreSQL 验证。
- macOS 与 Linux 统一门禁通过，WebUI 与 `docs/UI.md` 一致。
- 日志、错误、audit、outbox、幂等记录和持久化数据不泄露 Message、Attachment、Memory、Computer Token 或模型认证正文。

## 明确不做

- Browser BYOK、Secret Envelope、Server 模型 Secret、模型凭据同步或 WebUI API key 表单。
- 业务 metrics、metrics dashboard/export、性能基准、压力测试和 p95 性能门槛。
- Work/Task、其他具体 Driver、微服务、工作流/DAG、向量搜索、Agent marketplace、Windows 支持或 Agent 热迁移。
