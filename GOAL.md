# Sumi v1 当前目标

## 产品结果

交付一个真实可用的 Human 与 Agent 协作产品：Human 从空环境创建 Space、配对 Computer、使用本机配置创建 Builtin Agent，并在 DM、Channel 和 Thread 中与 Agent 协作。Agent 必须通过 `sumi agent` CLI 读取上下文、回复或明确 ack/defer；Inbox、Message、Attachment、Approval 和治理写入必须满足设计中的事务、权限、幂等与恢复不变量。

“模块存在”和“测试通过”不等于产品闭环完成。以下真实纵向验收和最终平台门禁是唯一完成依据。

## 当前基线

- Computer 身份已统一为长期 Computer Token；raw Token 只在本机受限 `secrets.json`，Server 只保存 hash。Pair、offline/reconnect、Server restart 和 Delete 后撤销均有真实进程证据。
- Builtin 从显式 Computer-local Pi-compatible settings/models/auth 加载 provider；只接受声明的 OpenAI-compatible completions，认证不进入 Agent Home、工具环境或日志。
- 同一真实进程 harness 已覆盖 DM、Channel mention/ambient、Thread、context freshness、权限边界、崩溃恢复、Channel create 和 Agent create Approval。
- 注册/Space、Attachment、Agent lifecycle、Memory 另有真实 Server/daemon/PostgreSQL 闭环。
- Agent Admin 治理与并发、幂等、PostgreSQL 不变量均已有真实协议和数据库证据；当前定义的 v1 交付目标已闭环。

## 剩余工作

### 0. WebUI 协作体验收口

- [x] 在 Channel/Thread 消息行中标识已转为 Task 的根 Message，hover 可查看当前处理 Agent。
- [x] 将最左上角品牌标识收口为 paper 品牌砖、轻量深梅色 S 与固定粉色硬阴影，与黄色 rail 和白色选中态形成统一层级。
- [x] 缩小 Conversation navigation 关闭 X，点击后收起左侧导航，并允许通过最左 Space rail 任意空白区重新展开。
- [x] 统一 Channel 与 Thread composer 尺寸，缩小 Thread placeholder 文字并对齐输入与发送控件。
- [x] 支持桌面端拖动调整 Thread pane 宽度，保持 Channel 最小宽度与移动端全屏行为。
- [x] 将 Agent 头像收口为由 Member ID 稳定生成、可区分的 GitHub identicon 风格像素印章。
- [ ] 补齐 DM WebUI 闭环：可从导航创建/打开 DM，并沿用 Channel 的 Message、Thread 与 Agent 注意力模型。

验证：每项改动运行最小相关 Web tests；阶段完成后运行 `pnpm --dir web test`、`pnpm --dir web lint`、`pnpm --dir web build`，并在 1440x900、1024x768、390x844 验证 Channel、Thread、侧边栏、Tasks 与 DM 无溢出、无 page/console error。若改动 Rust/协议，额外运行 `cargo fmt --all -- --check`、`cargo clippy --all-targets --all-features -- -D warnings` 与定向集成测试。

Task Message 标识验证：`cargo test task_flow_is_channel_scoped_and_permissionless -- --nocapture`、`pnpm --dir web test -- ChannelPage.test.tsx`、`pnpm --dir web lint`、`pnpm --dir web build`、`cargo fmt --all -- --check` 与 `cargo clippy --all-targets --all-features -- -D warnings` 通过；组件测试同时覆盖 Channel 根 Message、Thread root、TASK 标识和 assignee hover/focus 文本。

Composer 统一验证：`pnpm --dir web test`（8 files、19 tests）、`pnpm --dir web lint`、`pnpm --dir web build` 与 `node web/e2e/thread-verify.mjs` 通过；真实 1440x900 / 1024x768 / 390x844 中 Channel 与 Thread composer 分别同高 89 / 76 / 76px，输入框均 42px、attach/send 均 30px 且 placeholder 均为 14px，无横向溢出、page/console error，截图位于 `/tmp/sumi-shots-thread/`。

Thread pane resize 验证：`pnpm --dir web test`（8 files、19 tests）、`pnpm --dir web lint`、`pnpm --dir web build` 与 `node web/e2e/thread-verify.mjs` 通过；组件测试覆盖 separator ARIA、8/24px 键盘步进、Home/End 与跨控件边界 pointer drag。真实 1440x900 中 pointer 从 360px 调整到 440px、End 到 480px、Home 回 360px；1024x768 中最大 480px 且 Channel 保持至少 480px；390x844 中 resize handle 不可见且 Thread 保持全屏。三视口均无横向溢出、page/console error，截图位于 `/tmp/sumi-shots-thread/`。

### 1. Task 原型

- [x] 交付以根 Message 为来源的最小 Task 闭环：Agent CLI 可从已有根 Message 转换或创建新 Message + Task，可无额外权限地 claim、assign 和流转 `open / in_progress / done / canceled`；WebUI 提供集中列表并可跳回来源 Message。

验证：`cargo test task_flow_is_channel_scoped_and_permissionless -- --nocapture`；`cargo test --all-features`；`pnpm --dir web test`；`pnpm --dir web lint`；`pnpm --dir web build`；`cargo fmt --all -- --check`；`cargo clippy --all-targets --all-features -- -D warnings`。真实 `sumi-dev` 在 1440x900 验证四状态看板无溢出、无 page/console error；390x844 验证来源链接跳转并聚焦根 Message，截图 `/tmp/sumi-task-prototype.png`。

### 2. Reference-directed WebUI 收口

- [x] 将 `references/reference_style.md` 提炼为唯一设计语言，并完成全局 tokens、Space shell、Channel timeline 与 composer 的首个真实纵向改造；保留 Sumi 的领域结构，不引入参考产品的品牌或范围外入口。
- [x] 按同一语言收口 Members 与 Agent detail：成员导航按 Agent/Human 分组，统一目录治理控件、Agent 状态、详情页签与 Overview/Memory/Inbox/Settings，并完成真实 seed 多视口验证。
- [x] 按同一语言收口 Human Inbox：按 Approvals、DM & mentions、Replies、Channel activity 分组，统一发送者头像、来源、摘要、时间、审批与 complete/defer 操作，并完成空态/非空态真实三视口验证。
- [x] 按同一语言收口 Computer：统一 machine list/detail、workload/runtime、空态、Pair 与 Delete 确认，修正 Human 治理权限和删除后的身份语义，并完成真实 seed 多视口验证。
- [x] 按同一语言继续收口 Thread，补齐对应多视口视觉和交互证据。
- [x] 按同一语言继续收口 Dialog，补齐对应多视口视觉和交互证据。
- [x] 按同一语言继续收口 onboarding，补齐对应多视口视觉和交互证据。

### 3. Agent Admin 规范与治理闭环

- [x] 先修正 `docs/design.md` §8.2、§14.3、§17 和 §22.2 的冲突：逐项明确 Agent Admin 在 v1 可通过 `sumi agent` 到达的治理动作，以及必须保持 Human-only 的动作。当前缺失入口包括 Space name/accent、Human 邀请/移除、Channel 成员/归档、Agent suspend/resume、audit 读取；不得默认把所有 Browser API 原样暴露给 Agent。
- [x] 按修正后的唯一规范补齐最小治理协议、权限校验、audit/outbox/idempotency，并用真实 Builtin + `sumi agent` + PostgreSQL 证明 Agent Admin 可执行允许动作、不能执行 Human-only 动作、不能绕过 private Channel membership。

验证：`cargo test --test agent_dm builtin_agent_admin_executes_governance_and_respects_human_private_boundaries -- --nocapture`；`cargo test --test agent_dm -- --nocapture`（11 个真实 Agent 场景）；`cargo test --all-features`；`cargo clippy --all-targets --all-features -- -D warnings`。

### 4. 并发、幂等与 PostgreSQL 不变量

- [x] 补齐同 Agent 单 active run、Computer 并发上限、Thread/Message sequence、Inbox lease 竞争、重复 command、重复 Message/Attachment 和幂等 key payload 冲突的并发证据。
- [x] 用真实 PostgreSQL integration tests 系统验证 schema、复合外键、唯一约束、事务回滚和 transactional outbox；不得以内存 fake 替代 SQL 验收。

验证：`cargo test postgres_concurrency_and_transaction_invariants_hold -- --nocapture`；`cargo test concurrency_limit_queues_other_agents_and_rejects_same_agent_overlap -- --nocapture`；`cargo test duplicate_commands_reuse_sqlite_state_and_conflicting_payloads_fail -- --nocapture`；`cargo test --test postgres_migrations -- --nocapture`；`cargo test --test attachment_flow -- --nocapture`；`cargo test --all-features`；`cargo clippy --all-targets --all-features -- -D warnings`。

## 最新验证基线

2026-07-28：

- Agent identicon 验证：`pnpm --dir web test`（9 files、22 tests）、`pnpm --dir web lint`、`pnpm --dir web build` 与 `node web/e2e/avatar-verify.mjs` 通过；生成测试覆盖 Member ID 稳定性、rename 不变、水平镜像、8x8 边界、Agent 区分与 Human 分流，Channel、Members、Inbox、Computer、Agent detail 组件消费覆盖通过。
- `mise run dev-seed` + `node web/e2e/avatar-verify.mjs` + Chromium：真实 PM/Coder/Reviewer 在 Channel、Members 与 Computer 三类页面、1440x900 / 1024x768 / 390x844 中均保持 3 个稳定且互不碰撞的 identicon，无横向溢出、无 page/console error；截图位于 `/tmp/sumi-shots-avatar/`。
- `pnpm --dir web test -- ChannelPage.test.tsx`：桌面 Conversation navigation 折叠与 Space rail 重开展开覆盖在内的 8 个 test files、19 个 Web tests 通过；`pnpm --dir web lint` 与 `pnpm --dir web build` 通过。
- `node web/e2e/navigation-verify.mjs` + Chromium：在 1440x900 验证 26px 关闭 X、折叠后 Channel 释放 294px navigation 栏位并由 rail 空白区重开；在 1024x768 验证 rail 打开 x=52 抽屉；在 390x844 验证 header 打开 x=0 抽屉。三视口均无横向溢出、无 page/console error，关闭后焦点回落正确；截图位于 `/tmp/sumi-shots-navigation/`。
- `pnpm --dir web test -- ChannelPage.test.tsx`：品牌链接可访问名称与 S 字形覆盖在内的 8 个 test files、19 个 Web tests 通过；`pnpm --dir web lint` 与 `pnpm --dir web build` 通过。最终生效的 Space rail 品牌标识已由通用 Asterisk 收口为 paper 品牌砖、轻量深梅色 S、固定粉色硬阴影和左倾 2° 的标识框，不再随 Space accent 改色。
- `pnpm --dir web test`：Register 01、Create Space 02、实时 canonical URL、唯一 palette、进入 general、Space 内真实 setup 状态与 Pair CTA 覆盖在内的 7 个 test files、18 个 Web tests 通过；`pnpm --dir web lint` 与 `pnpm --dir web build` 通过。
- `mise run dev-seed` + `node web/e2e/onboarding-verify.mjs` + Chromium：从真实 Register、Create Space 事务进入新 Space 的 `#general`；在 1440x900、1024x768、390x844 验证 Register、Create Space 与 setup strip，均无横向溢出、无 page/console error；初始焦点、实时 slug、accent 选择、注册错误态、redirect 与 setup Pair Dialog 真实通过。截图位于 `/tmp/sumi-shots-onboarding/`。
- onboarding 收口后的完整门禁：`cargo test --test registration_space -- --nocapture`、`mise run test-dev-seed`、`cargo test --all-features`、`cargo fmt --all -- --check` 与 `cargo clippy --all-targets --all-features -- -D warnings` 通过；Space accent 的 Browser/Server/Agent CLI 唯一 palette 已与设计 tokens 对齐。
- `pnpm --dir web test`：统一 Dialog frame 的初始焦点、焦点循环/守卫、Escape、遮罩关闭、滚动锁定与触发器回退覆盖在内的 7 个 test files、17 个 Web tests 通过；`pnpm --dir web lint` 与 `pnpm --dir web build` 通过；`cargo fmt --all -- --check` 与 `cargo clippy --all-targets --all-features -- -D warnings` 通过。
- `mise run dev-seed` + `node web/e2e/dialog-verify.mjs` + Chromium：真实 `sumi-dev`、在线 Dev Computer 与 PM/Coder/Reviewer；在 1440x900、1024x768、390x844 验证 Create Channel、Add Agents、Pair Computer、Create Agent 和 Delete Computer，16 个场景均在视口内且无横向溢出、无 page/console error；初始焦点、Escape、遮罩关闭、焦点回退、错误态与危险强确认真实通过。截图位于 `/tmp/sumi-shots-dialog/`。
- `pnpm --dir web test`：Thread 最近三条回复 preview、desktop/tablet pane、mobile 返回、主时间线变化提示、follow/unfollow、Escape 与焦点回落覆盖在内的 6 个 test files、16 个 Web tests 通过；`pnpm --dir web lint` 与 `pnpm --dir web build` 通过；`cargo fmt --all -- --check` 与 `cargo clippy --all-targets --all-features -- -D warnings` 通过。
- `mise run dev-seed` + `node web/e2e/thread-verify.mjs` + Chromium：真实 `sumi-dev`、在线 Dev Computer 与 PM/Coder/Reviewer；在 1440x900、1024x768、390x844 验证 Thread preview 与 pane，桌面/平板保持 Channel 并列、手机全屏返回，三视口无横向溢出且关闭后滚动位置恢复；follow/unfollow、主时间线变化提示与返回最新焦点均真实通过，无 page/console error。截图位于 `/tmp/sumi-shots-thread/`。
- `pnpm --dir web test`：Computer 治理权限、Pair 键盘交互、受影响 Agent 强确认和删除身份语义覆盖在内的 6 个 test files、16 个 Web tests 通过；`pnpm --dir web lint` 与 `pnpm --dir web build` 通过；`cargo fmt --all -- --check` 与 `cargo clippy --all-targets --all-features -- -D warnings` 通过。
- `mise run dev-seed` + Chromium：真实 `sumi-dev` Space、在线 Dev Computer 与 PM/Coder/Reviewer；在 1440x900、1024x768、390x844 验证 Computer detail、Pair 和 Delete，均无横向溢出、无 page/console error，复制命令、Escape/焦点循环、3 个受影响 Agent 和确认前禁用均真实通过。截图位于 `/tmp/sumi-shots-computers/`。
- `pnpm --dir web test`：Inbox 分组顺序、发送者身份、Approval 操作和空态覆盖在内的 6 个 test files、15 个 Web tests 通过；`pnpm --dir web lint` 与 `pnpm --dir web build` 通过。
- `mise run dev-seed` + Chromium：真实 `sumi-dev` Space 与 PM/Coder/Reviewer seed；通过 Browser route 构造仅用于视觉验证的非持久 Inbox/Approval 响应，在 1440x900、1024x768、390x844 验证空态和四组非空态，无横向溢出、无 page/console error，手机可滚动到 Channel activity，Defer 与 Approve 请求均真实触发。截图位于 `/tmp/sumi-shots-inbox/`。
- `pnpm --dir web test`：Members/Agent detail 定向覆盖在内的 6 个 test files、14 个 Web tests 通过；`pnpm --dir web lint` 与 `pnpm --dir web build` 通过。
- `mise run dev-seed` + Chromium：使用真实 `sumi-dev` Space、Dev Computer 与 PM/Coder/Reviewer，在 1440x900、1024x768、390x844 验证 Members 与 Agent Overview/Memory/Inbox/Settings；所有视口无横向溢出、无 Browser page/console error，手机治理控件完整可达。截图位于 `/tmp/sumi-shots-members-agent/`。
- `pnpm --dir web test`：6 个 test files、14 个 Web tests 通过。
- `pnpm --dir web lint` 与 `pnpm --dir web build` 通过；Space Mono 随 production bundle 本地发布。
- `node web/e2e/shot.mjs`：真实注册、Space、Message、Thread 流程通过，并人工复核 1440x900、1024x768、390x844 的 Channel 与 Members 截图；shell、timeline、composer 和响应式布局无溢出或空白栏。
- `mise run test-dev-seed`：6 个开发 seed 任务与状态恢复测试通过。
- `mise run dev-seed`：从缺失 `sumi_dev` 的首次启动路径创建数据库并完成 migration；数据库重建后的旧 Dev Computer 状态被保留归档，新 Computer 配对、PM/Coder/Reviewer provision 和 `#general` membership 均通过。

2026-07-27：

- `cargo fmt --all -- --check`
- `cargo clippy --all-targets --all-features -- -D warnings`
- `cargo test --all-features`：61 个常规 tests、11 个 Agent 真实闭环、1 lifecycle、1 Memory、1 Attachment、3 CLI、2 Computer lifecycle、1 migration、1 registration/Space 通过；1 个手工 live provider smoke ignored。
- `cargo test postgres_concurrency_and_transaction_invariants_hold -- --nocapture`：真实 PostgreSQL 并发 claim、active run、Message/Thread sequence、重复 Message/Attachment、幂等 payload 冲突、复合外键、事务回滚与 outbox 验收通过。
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
- Work、复杂 Task 工作流/DAG、其他具体 Driver、微服务、向量搜索、Agent marketplace、Windows 支持或 Agent 热迁移。
