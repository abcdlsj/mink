# Sumi GOAL 任务 6 → 任务 7 交接

## 当前状态

- Superset Project：`sumi`（`7441539a-df4b-49ef-a07a-b3693c4b82d5`）。
- 当前 Superset workspace：`fix/claude-stateless-history`（`407c2aeb-eeca-4b22-8b0f-3292132a8cd3`），分支为 `main`。
- 任务 6 已完成并提交：`8881c10 refactor: remove duplicate agent configuration paths`。
- `GOAL.md` 中任务 6 已标记为“已完成”，下一个任务是任务 7“Computer 与前端职责拆分”。

## 任务 6 实现结果

- `src/agent_config.rs` 现在是 Agent attention 配置、默认值、校验规则和暂停模式的唯一所有者；Browser API 与 Computer 协议直接复用该类型，不再各自声明镜像类型或执行逐字段转换。
- Attention SQL 不再容忍 Agent 配置缺字段；Human Thread subscription 的固定 5 秒 debounce 使用显式 `CASE`，没有与旧 Agent 配置兼容混用。
- daemon 只接受当前 `ComputerSecrets`、Agent profile 和 Driver config schema；profile 缺少当前 lifecycle 时明确失败，不再静默推断为 active。
- Run 崩溃恢复只通过持久化的 `command_id` 和 `result_event_id` 关联当前记录，删除扫描 command JSON 和拼接旧 result event ID 的回退入口；测试夹具改为写完整当前记录。
- 删除单调用方 `src/server/agent_prompt.rs` 透传层；Run claim 边界直接构造 Prompt，Inbox summary 序列化失败不再伪造为空数组。
- Local Agent JSON envelope 的 schema version 由一个常量维护。
- 未拆分 `src/computer.rs` 或 `ChannelPage.tsx`，这两项保留给任务 7。

## 已通过验证

- `mise run test-server`：77 个单元/Server 测试通过，1 个真实 provider smoke test按设计 ignored；11 个 Agent DM/Channel/Thread 真实 CLI 测试及其他集成测试全部通过。
- `mise run check-api-types`。
- `cargo fmt --all -- --check`。
- `cargo clippy --all-targets --all-features -- -D warnings`。
- `git diff --check`。

## 任务 7 下一步

1. 依次阅读 `AGENTS.md`、`GLOSSARY.md`、`docs/design.md` 和 `GOAL.md`，再阅读 `docs/design/04-computer-agent.md`、`docs/design/04-agent-lifecycle-reliability.md`、`docs/design/03-web-ui.md` 及涉及的数据流主题。
2. 检查工作区状态，以当前文件为基线，保留用户或其他 Agent 的未提交修改；`web/package-lock.json` 不属于本目标，不得提交。
3. 按 pairing/credentials、connection、command executor、attention scheduler、lease/recovery 拆分 `src/computer.rs`。先画出现有状态所有者与调用方向，再移动代码；不得引入单实现 trait、wrapper、factory 或第二份状态。
4. 从 `ChannelPage.tsx` 提取 Message composer 状态与 Timeline、ThreadPane、Composer 组件。URL 和 TanStack Query cache 继续作为路由与 Server state 的事实来源，不复制到组件 state。
5. CSS 只在组件边界稳定后按 feature 拆分；删除失效覆盖时必须用组件测试或实际选择器调用方证明。
6. 每完成一个职责边界运行最小定向测试；阶段结束运行 Rust 全量测试、Web 单元测试、build、lint、Rust fmt 和 Clippy。
7. 任务 7 完成后更新 `GOAL.md` 并单独提交；七项任务全部完成后运行 `mise run lint` 和 `mise run test`。
