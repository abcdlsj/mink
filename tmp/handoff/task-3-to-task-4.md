# Sumi GOAL 任务 3 → 任务 4 交接

## 当前状态

- 当前 Superset Project：`sumi`（`7441539a-df4b-49ef-a07a-b3693c4b82d5`）。
- 当前 workspace：`fix/claude-stateless-history`（`407c2aeb-eeca-4b22-8b0f-3292132a8cd3`），分支为 `main`。
- 任务 3 已完成并提交：`d7f6ed5 refactor: generate browser api types from rust schema`。
- `GOAL.md` 中任务 3 已标记为“已完成”，下一个任务是任务 4“Computer 共享强类型协议”。

## 任务 3 实现结果

- Browser API DTO 使用 `utoipa::ToSchema` 导出 OpenAPI component schema。
- 新增隐藏开发工具命令 `sumi schema browser-openapi`，不增加独立二进制或运行模式。
- `web/scripts/generate-api-types.mjs` 使用固定版本 `openapi-typescript` 生成 `web/src/api/types.ts`；生成文件不再保存手写字段定义。
- 新增 `mise run generate-api-types` 和 `mise run check-api-types`，Web lint 会先检查生成结果一致性。
- `web/src/api/client.ts` 删除手写 Error、认证响应、配对请求和 Agent 创建请求类型，只保留传输 helper 与领域命名函数。
- Rust schema 为状态字段补齐枚举约束，并将 Approval payload 声明为结构化 `AgentCreateApprovalPayload`。
- `web/package-lock.json` 未修改、未提交。

## 已通过验证

- `mise run check-api-types`
- `cargo test server::api_schema::tests::browser_schema_contains_every_web_wire_type --all-features`
- `pnpm --dir web lint`
- `pnpm --dir web test`（9 个文件、23 个测试）
- `pnpm --dir web build`
- `cargo fmt --all -- --check`
- `cargo clippy --all-targets --all-features -- -D warnings`
- `git diff --check`

## 下一步

1. 依次阅读 `AGENTS.md`、`GLOSSARY.md`、`docs/design.md`、`GOAL.md`。
2. 任务 4 开始前阅读 `docs/design/01-foundations.md`、`docs/design/04-computer-agent.md`、`docs/design/04-agent-lifecycle-reliability.md`、`docs/design/05-driver-cli.md`、`docs/design/07-api-data.md` 和 `docs/design/09-delivery-acceptance.md`。
3. 检查 `src/server/computer_protocol.rs` 与 `src/computer.rs` 的镜像 Frame、`run_attempt` 字段和稳定 command payload/result，先核对 wire 行为再移动到共享 protocol 模块。
4. 保留现有 HTTP、CLI 和 PostgreSQL schema；任务 4 只统一 Computer WebSocket 强类型协议。
5. 按 `GOAL.md` 运行重连、command replay、run started/result、lifecycle 和崩溃恢复测试，并执行 Rust fmt、Clippy。
6. 每个 GOAL 任务继续单独提交和交接。
