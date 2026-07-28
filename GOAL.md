# Sumi v1 当前目标

## 产品结果

Human 可以从空环境创建 Space、配对 Computer，并创建使用本机 provider 配置的 Builtin Agent。Human 可以在 DM、Channel 和 Thread 中与 Agent 协作。Agent 通过 `sumi agent` 读取上下文、发送回复、处理 Inbox 和执行权限允许的治理操作。

完成结果必须同时满足：

- PostgreSQL 事务维持 Message、Inbox、Attachment、Approval、权限、幂等和 outbox 不变量。
- Computer 断线或重启后保留配对身份，并补报已经完成的 Run。
- Driver 卡死、取消失败或 Computer 永久离线时，Run 最终离开 active 状态，Inbox 可以重试。
- Browser 在 1440x900、1024x768 和 390x844 三种视口完成主要流程，没有横向溢出和 page 或 console error。

## 当前状态

以下流程已有实现和测试：

- 注册、Space、Computer Pair、Computer Delete、Agent provision、Agent lifecycle、Memory 和 Attachment。
- Builtin Agent 的 DM、Channel mention、Channel ambient、Thread、context freshness 和崩溃恢复。
- Agent Admin 治理、private Channel 权限、Approval、Task、Inbox lease、command 幂等和 PostgreSQL 并发约束。
- Channel、Thread、Members、Agent detail、Inbox、Computer、Dialog 和 onboarding 的三视口 WebUI。

当前缺口是跨连接 Run 结果补报、失联 Run 回收、进程停止证据，以及 DM WebUI 入口。

## 下一步

### 1. Agent Run 生命周期可靠性

规格见 [Agent 生命周期可靠性](docs/design/04-agent-lifecycle-reliability.md)。按以下顺序实现：

- [x] 在 SQLite 增加 durable result outbox。Run 结束时原子写入终态和待上报结果。
- [x] 增加 `result_receipt`。daemon 收到回执前持续重发，Server 幂等应用结果。
- [x] 增加 `run_started`。command ACK 不再把 queued Run 改成 running。
- [x] 增加 Run ownership lease、fencing token 和 Server reconciler。过期 Run 释放 Inbox 和 active Run 唯一约束。
- [x] 将 WebSocket、result sender、attention scheduler、lease renewer 和 heartbeat 拆成独立 task。
- [ ] 分开 Agent desired lifecycle 与 Run observed execution。
- [ ] 完成 SIGTERM、SIGKILL、reap 和 orphaned 状态处理。
- [ ] 通过该设计文档列出的 12 个故障注入测试。

### 2. DM WebUI

- [ ] 从导航创建和打开 DM。
- [ ] 复用 Channel 的 Message、Thread 和 Agent 注意力模型。
- [ ] 通过 Web 单元测试、lint、build 和三视口浏览器验证。

## 验证命令

生命周期改动至少运行：

```sh
cargo fmt --all -- --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test --all-features
```

WebUI 改动至少运行：

```sh
pnpm --dir web test
pnpm --dir web lint
pnpm --dir web build
```

涉及 PostgreSQL、daemon 或 Driver 的完成结论必须来自对应集成测试。涉及布局和交互的完成结论必须包含三种目标视口的浏览器验证。

本项验证记录：

- `cargo fmt --all -- --check`：通过。
- `cargo clippy --all-targets --all-features -- -D warnings`：通过。
- `cargo test --all-features`：通过；67 个单元测试、21 个进程和数据库集成测试通过，1 个手动 provider smoke test 忽略。

## 完成条件

- Human 可以完成产品结果中列出的完整流程。
- 生命周期故障注入测试全部通过。
- Computer offline 和 reconnect 不改变配对身份。Delete 后旧 Computer Token 失效。
- 全量 Rust 和 Web 门禁通过。

## v1 范围外

- Browser BYOK、Server 模型 Secret 和模型凭据同步。
- 业务 metrics、性能基准、压力测试和 p95 门槛。
- Windows、Agent 热迁移、微服务、向量搜索和 Agent marketplace。
- Work 和 Task DAG。
