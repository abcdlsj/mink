# Sumi GOAL 任务 4 → 任务 5 交接

## 当前状态

- Superset Project：`sumi`（`7441539a-df4b-49ef-a07a-b3693c4b82d5`）。
- Superset workspace：`fix/claude-stateless-history`（`407c2aeb-eeca-4b22-8b0f-3292132a8cd3`），分支为 `main`。
- 任务 4 已完成并提交：`8698fef refactor: share typed computer protocol`。
- `GOAL.md` 中任务 4 已标记为“已完成”，下一个任务是任务 5“Message hydration 批量查询”。

## 任务 4 实现结果

- `src/computer_protocol.rs` 是 Server 与 daemon 共用的 Computer WebSocket 协议事实来源；旧的 `src/server/computer_protocol.rs` 和 `src/computer.rs` 镜像 Frame 已删除。
- `ComputerCommand` 使用 `kind` tagged enum；provision、configure、suspend、resume、retire、run、cancel 和 memory read 均使用强类型 payload。
- command result、Memory 元数据、Attention 配置、Driver 配置和 run prompt 使用共享 struct；协议类型拒绝未知字段，不保留旧 payload 解析入口。
- `run_attempt` 两端统一为 `i64`。
- Server 仅在 PostgreSQL `kind + payload_json` 存储边界拆分或组装 `ComputerCommand`；daemon 仅在 SQLite JSON 存储边界序列化或反序列化共享类型。
- Supervisor 的 StartRun 和 MemoryFileMetadata 复用共享协议类型。
- HTTP、CLI、PostgreSQL 和 SQLite schema 未修改。

## 已通过验证

- `mise run test-server`：完整 Rust 测试通过；74 个单元/Server 测试通过，1 个真实 provider smoke test 按原设计 ignored；Agent DM、Agent lifecycle、Memory、Attachment、CLI、Computer lifecycle、migration 和注册集成测试全部通过。
- `cargo test computer::tests --all-features`：22 个 daemon 协议、重连、result outbox、run started、恢复和 lifecycle 单元测试通过。
- `cargo test server::tests::computer_flow_enforces_security_boundaries --all-features`：Computer command replay、run started/result 和 receipt 流程通过。
- `cargo fmt --all -- --check`。
- `cargo clippy --all-targets --all-features -- -D warnings`。
- `git diff --check`。

## 任务 5 下一步

1. 依次阅读 `AGENTS.md`、`GLOSSARY.md`、`docs/design.md` 和 `GOAL.md`。
2. 阅读 `docs/design/02-collaboration.md`、`docs/design/07-api-data.md` 和 `docs/design/09-delivery-acceptance.md` 中 Message、Attachment、mention、Task summary 与读取验收要求。
3. 仓库当前没有 `.codegraph/`；直接定位 Browser Channel page、Thread read 和 Agent read 的 Message hydration 查询。
4. 先记录三条读取路径当前每条 Message 执行的 Attachment、mention 和 Task summary 查询，再设计按 Message ID 集合批量读取的共享 hydration 服务。
5. 保持返回 JSON、排序、权限和 PostgreSQL schema 不变；不要保留逐条查询兼容路径。
6. 增加查询次数不随 Message 数量线性增长的测试，并运行协作、Thread、Agent read 定向测试、Rust fmt 和 Clippy。
7. 任务 5 单独提交，随后继续按 `GOAL.md` 生成交接并创建新 session。
