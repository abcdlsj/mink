# Agent create Approval 接力

## 当前状态

- 分支：`sumi_dev_hm`
- 最新提交：`4d999ef fix: allow offline agent approval requests`
- 工作区：本次提交后应保持干净。
- 已完整阅读：`AGENTS.md`、`GOAL.md`、`GLOSSARY.md`、`docs/design.md` 及用户交接附件。

## 已完成

审计发现 `src/server/approval.rs` 在创建 pending Approval 时错误调用了在线 Computer 校验，导致目标 Computer offline 时无法提交合法申请。现已改为只校验 Computer 属于当前 Space；审批决议路径仍调用 `require_online_computer`，只有 Human approve 且目标 online 时才写入 Agent identity、provisioning command 和 transactional outbox。reject 不创建 Agent Member/Home/command。

## 验证

`cargo fmt --all -- --check`

`cargo clippy --all-targets --all-features -- -D warnings`

`cargo test --all-features`：56 常规 Rust tests、9 Agent 真实进程闭环、Agent lifecycle、Memory、Attachment、CLI、Computer lifecycle、migration、registration/Space 全通过；live provider smoke 按设计 ignored。

## 下一步

继续 `GOAL.md` 最早未完成项：补齐 `docs/design.md` 22.2/22.9 的真实独立进程 Approval 闭环。必须使用真实 `sumi server`、`sumi computer`、Builtin fake provider 和真实 `sumi agent create` CLI，覆盖：

1. 有 `agent:create` 的 Agent 只能得到 pending Approval，不产生 Agent Member/Home/provision command。
2. Agent 即使是 Admin 也不能 approve；requester self-approval 和 Computer Token 直接审批必须拒绝。
3. Human Admin approve 后，以同一事务写 Approval 决议与 provisioning command outbox；目标 online 才下发，offline 需保留 pending/approved 边界并可重试。
4. 真实 daemon 创建 Agent Home、完成 Driver/sandbox validate 后 active；reject 不留下 Member、本地目录或 command。
5. SQL 验证 Approval、Human Inbox、audit、outbox、idempotency、复合约束和日志 redaction。

不要勾选 GOAL 中 Approval 项，直到以上真实进程与真实 PostgreSQL 证据全部通过。
