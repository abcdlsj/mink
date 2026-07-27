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

## 剩余工作

### 1. Agent Admin 规范与治理闭环

- [x] 先修正 `docs/design.md` §8.2、§14.3、§17 和 §22.2 的冲突：逐项明确 Agent Admin 在 v1 可通过 `sumi agent` 到达的治理动作，以及必须保持 Human-only 的动作。当前缺失入口包括 Space name/accent、Human 邀请/移除、Channel 成员/归档、Agent suspend/resume、audit 读取；不得默认把所有 Browser API 原样暴露给 Agent。
- [x] 按修正后的唯一规范补齐最小治理协议、权限校验、audit/outbox/idempotency，并用真实 Builtin + `sumi agent` + PostgreSQL 证明 Agent Admin 可执行允许动作、不能执行 Human-only 动作、不能绕过 private Channel membership。

验证：`cargo test --test agent_dm builtin_agent_admin_executes_governance_and_respects_human_private_boundaries -- --nocapture`；`cargo test --test agent_dm -- --nocapture`（11 个真实 Agent 场景）；`cargo test --all-features`；`cargo clippy --all-targets --all-features -- -D warnings`。

### 2. 并发、幂等与 PostgreSQL 不变量

- [ ] 补齐同 Agent 单 active run、Computer 并发上限、Thread/Message sequence、Inbox lease 竞争、重复 command、重复 Message/Attachment 和幂等 key payload 冲突的并发证据。
- [ ] 用真实 PostgreSQL integration tests 系统验证 schema、复合外键、唯一约束、事务回滚和 transactional outbox；不得以内存 fake 替代 SQL 验收。

## 最新验证基线

2026-07-27：

- `cargo fmt --all -- --check`
- `cargo clippy --all-targets --all-features -- -D warnings`
- `cargo test --all-features`：60 个常规 tests、11 个 Agent 真实闭环、1 lifecycle、1 Memory、1 Attachment、3 CLI、2 Computer lifecycle、1 migration、1 registration/Space 通过；1 个手工 live provider smoke ignored。
- `cargo test --test agent_dm -- --nocapture`：11 个真实 Agent 对话与治理场景通过。
- `cargo test --test agent_lifecycle -- --nocapture`
- `cargo test --test agent_memory -- --nocapture`
- `cargo test --test attachment_flow -- --nocapture`
- `cargo test --test registration_space -- --nocapture`
- `cargo test --test computer_lifecycle -- --nocapture`
- 历史 Web 基线：`pnpm --dir web test && pnpm --dir web lint && pnpm --dir web build`。

## 完成定义

- Human 可从空环境完成 Space 创建、Computer Pair、Builtin Agent 创建，并在 DM、Channel 和 Thread 与 Agent 完整协作。
- Computer offline/reconnect 不丢 Pair；Delete 后旧 Token 永久失效。
- Agent 注意力、CLI 读取/写入、治理、Inbox handle、重试、幂等和权限边界均由真实进程与真实 PostgreSQL 验证。

## 明确不做

- Browser BYOK、Secret Envelope、Server 模型 Secret、模型凭据同步或 WebUI API key 表单。
- 业务 metrics、metrics dashboard/export、性能基准、压力测试和 p95 性能门槛。
- 额外安全与治理收口：全量日志 redaction 审计、敏感操作 audit 补齐、rate limit、prompt injection 专项以及删除/保留策略。
- 平台与最终验收专项：macOS/Linux 构建和 sandbox 矩阵、`docs/design.md` §22 全量 E2E、Playwright 多视口与无障碍验收、统一全仓门禁复跑及占位/TODO/过期入口清扫。
- Work/Task、其他具体 Driver、微服务、工作流/DAG、向量搜索、Agent marketplace、Windows 支持或 Agent 热迁移。
