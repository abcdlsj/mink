# Sumi GOAL 任务 5 → 任务 6 交接

## 当前状态

- Superset Project：`sumi`（`7441539a-df4b-49ef-a07a-b3693c4b82d5`）。
- Superset workspace：`fix/claude-stateless-history`（`407c2aeb-eeca-4b22-8b0f-3292132a8cd3`），分支为 `main`。
- 任务 5 已完成并提交：`68c3ce7 perf: batch message hydration queries`。
- `GOAL.md` 中任务 5 已标记为“已完成”，下一个任务是任务 6“代码简化与架构整理”。

## 任务 5 实现结果

- `src/server/message_hydration.rs` 是列表 Message hydration 的共享读取边界；一次接收 Message ID 集合，并通过一次 PostgreSQL 查询读取 Attachment、mention 和 Task summary。
- Attachment 按 `message_attachments.position` 排序，mention 按 `member_id` 排序；Task summary 仍只投影到 Browser Message，Agent read JSON 未新增 `task` 字段。
- Browser Channel page、Browser Thread read 和 Agent read 均复用共享批量 hydration。Agent Thread 的 root、replies 与 Channel background 合并为一次 hydration。
- 旧的 `attachments_for_message_pool`、`hydrate_task_summaries_pool` 和逐 Message `enrich_agent_messages` 已删除；事务内 Message 写入响应仍使用事务连接读取单条 Message。
- PostgreSQL schema、HTTP/Agent wire JSON、权限与 Message 排序未修改。
- 新测试比较 1 条与 100 条 Message 的 hydration，二者均只执行一次批量查询。

## 已通过验证

- `mise run test-server`：75 个单元/Server 测试通过，1 个真实 provider smoke test按设计 ignored；11 个 Agent DM/Channel/Thread 真实 CLI 测试及其他集成测试全部通过。
- `cargo fmt --all -- --check`。
- `cargo clippy --all-targets --all-features -- -D warnings`。
- `git diff --check`。

## 任务 6 下一步

1. 依次阅读 `AGENTS.md`、`GLOSSARY.md`、`docs/design.md` 和 `GOAL.md`；按实际整理范围读取 `docs/design.md` 链接的相关主题。
2. 检查工作区状态，以当前文件为基线，保留用户或其他 Agent 的未提交修改。
3. 任务 6 范围很广，先用可验证的重复事实来源、无效兼容入口、重复状态和无业务边界抽象建立清单；每项必须说明当前所有者、调用方和删除后的唯一事实来源。
4. 优先处理不与任务 7 重叠的简化。不要在任务 6 提前拆分 `src/computer.rs` 或 `ChannelPage.tsx`；这两项属于任务 7。
5. 不以文件行数作为完成依据，不新增兼容层、repository trait、wrapper、factory 或预留扩展点。
6. 每完成一组相关删除或合并就运行最小定向测试；阶段结束运行主要功能测试、Rust fmt 和 Clippy。涉及 Web 时运行 Web build、lint 和定向测试。
7. 任务 6 单独提交，随后按 `GOAL.md` 生成交接并创建新 session，从任务 7 继续。
