# Agent 生命周期可靠性

- [返回设计索引](../design.md)
- [Computer 与 Agent 基础设计](./04-computer-agent.md)
- [Raft Computer 实现参考](../../references/raft-computer-lifecycle.md)

本文补充 §11 和 §12，定义 Agent Run 在断线、daemon 重启、Computer 失联和 Driver 停止失败时的状态变化。

## 完成要求

每个 Run 必须得到一个 `completed`、`failed` 或 `canceled` 结果。结果写入 Server 后，Inbox 必须进入 handled、pending 或 dead。WebSocket 断线、daemon 重启和 HTTP 超时不能丢失结果。

PostgreSQL 保存 Server 已确认的 Run 和 Inbox 状态。SQLite 保存 daemon 已接收的 command、本地 Run 状态和待上报结果。WebSocket、HTTP、tokio channel 和 PID 只提供传输或进程信息。

协议使用 at-least-once delivery。每个状态事件具有稳定 ID，接收方按 ID 幂等处理。

## 标识

| 标识 | 有效期 | 用途 |
| --- | --- | --- |
| `agent_member_id` | Agent 存续期间 | 标识 Space 中的 Agent |
| `computer_id` | Computer 配对期间 | 标识配对的 Computer |
| `daemon_session_id` | 一次 daemon 启动 | 区分 daemon 重启前后的执行者 |
| `connection_id` | 一条 WebSocket | 记录连接诊断信息 |
| `run_id` | 一次 Inbox 执行 | 关联 command、Inbox 和结果 |
| `run_attempt` | 一次本地执行尝试 | 区分同一 Run 的进程尝试 |
| `process_instance_id` | 一个 Driver 进程 | 关联启动、activity、停止和退出证据 |

Server 为 Run 生成 ownership lease 和 fencing token。fencing token 是一次 Run 所有权的随机值或递增版本。renew、`run_started`、activity 和 Run result 必须携带该值。Server 使 token 失效后，旧 daemon 的消息只写诊断记录。

Raft Computer 使用 `RuntimeProcessBindingFence`、`AgentLifecycleRecords.stopEpoch()` 和 `process_instance_id` 隔离旧进程的回调。Sumi 采用相同约束。

## 状态

Agent 的 desired lifecycle 表示治理意图：

```text
active | suspended | retired
```

- `active` 允许 claim Inbox。
- `suspended` 禁止 claim。`stop_after_current` 允许当前 Run 完成，`cancel_now` 要求停止当前 Run。
- `retired` 禁止新 Run，并停止当前 Run。

Run 的 observed execution 表示执行进度：

```text
queued -> starting -> running -> stopping -> completed | failed | canceled
```

- `queued` 表示 Server 已创建 Run，daemon 还没有获得执行槽。
- `starting` 表示 daemon 已获得执行槽，正在校验环境或启动 Driver。
- `running` 表示 Driver 已启动，SQLite 已保存 `process_instance_id`。
- `stopping` 表示 daemon 已接受停止请求，进程退出尚未确认。
- `completed`、`failed` 和 `canceled` 是终态。终态需要启动前错误、进程退出或 ownership lease 过期作为证据。

UI 必须组合显示两组状态。例如，`suspended + running` 表示当前 Run 将完成后暂停，`suspended + stopping` 表示正在取消，`active + unreachable` 表示 Computer 失联且 lease 仍有效。

## Command ACK 和 Run started

daemon 把 Server command 写入 SQLite 后返回 `command_ack`。ACK 表示 daemon 已接收 command。Server 此时保留 Run 的 `queued` 状态。

daemon 获得执行槽后把本地 Run 写为 `starting`。Driver 启动且 SQLite 写入 `process_instance_id` 后，daemon 将 `run_started` 写入本地 outbox。事件包含：

- `event_id`
- `run_id`
- `run_attempt`
- `process_instance_id`
- fencing token
- daemon observed timestamp

Server 应用 `run_started` 后把 Run 改为 `running`，用 daemon observed timestamp 填写 `started_at`，并返回只包含同一 `event_id` 的 `started_receipt`。同一 `event_id` 的重复 `run_started` 返回相同回执，不重复发布 Run 状态事件。daemon 收到该回执后才激活本地 Run token 并向 Driver 交付首个 prompt。这个顺序保证 Driver 第一次调用 Agent API 时，Server 已接受 running 状态。

## Run result outbox

Run 结束时，daemon 在一个 SQLite 事务中完成三次写入：

1. 把 `local_agent_runs` 写为终态。
2. 保存 status、error code、memory metadata 和进程退出证据。
3. 向 result outbox 插入待上报记录。

result sender 扫描没有 `reported_at` 的记录，并在连接可用时发送。Server 在一个 PostgreSQL 事务中更新 command、Run、Inbox 和 Server outbox，然后返回 `result_receipt`。daemon 收到回执后填写 `reported_at`。

daemon 使用 `run_result` 帧上报 Run 结果。该帧包含 `event_id`、`command_id`、`computer_seq`、`ok` 和 `result`。Server 只允许非 Run command 使用 `command_result`。Server 提交结果事务后返回只包含同一 `event_id` 的 `result_receipt`。同一 `event_id` 的重复 `run_result` 必须返回回执，且不得再次更新 Inbox retry count 或发布 Run 状态事件。

```text
SQLite Run 终态和 result outbox
  -> result sender 重试
  -> PostgreSQL 幂等事务
  -> result_receipt
  -> SQLite reported_at
```

completion channel 只用于通知 result sender 扫描 outbox。channel 关闭或连接更换不能删除待发结果。

## daemon task

daemon 使用独立 task 处理以下职责：

- WebSocket reader 和 writer 处理协议帧和重连。
- result sender 上报 `run_started` 和 Run result，并处理 event receipt。
- attention scheduler 按容量 claim Inbox。
- lease renewer 续租 ownership 和 Inbox lease。
- Supervisor 管理本地 Run 和 Driver。
- heartbeat reporter 上报 daemon 状态和容量。

这些 task 共享 cancellation token，并按 shutdown 顺序退出。HTTP 使用一个配置了 connect timeout 和 request timeout 的 client。claim 或 renew 请求超时不能停止 heartbeat、WebSocket 读取或 result sender。

attention scheduler 的 claim 上限为可用执行槽加固定 prefetch 数。已 queued 的本地 Run 占用 prefetch。相同优先级的 Agent 按 round-robin 调度。

Raft Computer 的 `AgentStartCoordinator` 限制并发启动，`AgentStartPendingDeliveryBuffer` 保存启动期间收到的消息。Sumi 使用这两个实现位置作为容量控制参考。

## WebSocket 重连

WebSocket 断线不改变 Run、ownership lease 和 result outbox。重连后按以下顺序恢复：

1. daemon 发送 command watermark 和 `daemon_session_id`。
2. Server 重放未完成 command。
3. daemon 为本地终态 command 重发 result。
4. daemon 收到本地 running command 的重放时保留现有进程。
5. result sender 继续等待 `result_receipt`。

Run 的完成处理必须写入 result outbox。完成通知不能持有某条 WebSocket 专用的 sender 作为唯一上报路径。

## daemon 重启

daemon 接收新 command 前校正 SQLite 状态：

- queued Run 没有启动证据时，按重试策略重新启动或写入 `process_lost`。
- running 或 stopping Run 无法证明进程仍受当前 daemon 管理时，写入 `process_lost`。
- 终态 Run 没有 receipt 时，保留原结果并继续上报。
- command 与 Run 状态不一致时，根据 Run 终态重建 result outbox，并写入 invariant violation 日志。

`process_lost` 只用于缺少进程证据的非终态 Run。它不能覆盖已有终态结果。

## Computer 失联

Computer 超过 30 秒没有 heartbeat 时进入 offline。该状态不结束 Run。Run ownership lease 独立计时：

- lease 有效时，Run 显示 `unreachable`，其他执行者不能接管。
- lease 过期时，Server 使 fencing token 失效，把 Run 写为 failed，并释放 Inbox。
- 旧 daemon 之后发送的 started 或 result 不能修改 Run 和 Inbox。

Server 定时扫描过期 ownership lease。该任务保证失联 Run 最终释放 active Run 唯一约束。

Raft Computer 的 `rehydrateRunnerRecord()`、`nextRunnerStateOnExit()` 和 `degraded` 状态提供了重启恢复和重复崩溃暂停的实现参考。

## Suspend、retire 和 cancel

- `suspend/stop_after_current` 更新 desired lifecycle，禁止新 claim，保留当前 Run。
- `suspend/cancel_now` 更新 desired lifecycle，禁止新 claim，并把当前 Run 改为 `stopping`。
- `retire` 禁止新 Run，把当前 Run 改为 `stopping`，并在进程退出后清理可再生目录。
- `resume` 更新 desired lifecycle，不改变历史 Run。

停止 Driver 时按以下顺序执行：

1. 持久化 `stopping` 和 stop epoch。
2. 向进程组发送 SIGTERM，并检查返回值。
3. 在 grace period 内等待进程退出和 reap。
4. 超时后发送 SIGKILL，并在第二个 timeout 内等待 reap。
5. 第二次等待超时后写入 `orphaned`，由后台 reaper 继续处理。

`orphaned` 表示数据库尚未取得进程退出证据。Supervisor 在 reaper 完成前保留该 Run。进程退出回调、自动重启 timer 和延迟 cancel 必须校验 `process_instance_id` 和 stop epoch。

Raft Computer 的 `AgentProcessManager.stopAgent()`、`RuntimeProcessBindingFence` 和 stalled recovery 使用了上述信号顺序和进程绑定检查。

## 数据字段

SQLite 需要保存：

- `daemon_session_id`
- `run_attempt`
- `process_instance_id`
- ownership lease 和 fencing token
- result outbox 的 payload、attempt count、next attempt、last error 和 `reported_at`
- started 和 result 的稳定 event ID
- stopping、orphaned 和 reap 证据

PostgreSQL 需要保存：

- Agent desired lifecycle
- Run observed execution
- ownership lease、fencing token 和 last renewal
- daemon observed timestamp 和 Server received timestamp
- result event ID 或等价去重键
- 旧 token 消息的诊断记录

同一 Agent 只能存在一个非终态 Run。Server 的 lease 扫描任务负责使过期 Run 离开该集合。

## 日志与不变量

日志和 trace 使用 `agent_member_id`、`run_id`、`run_attempt`、`process_instance_id`、`daemon_session_id` 和 `command_id` 关联事件。必须记录 command persisted、command acked、slot acquired、Driver started、lease renewed、lease expired、result persisted、result sent、result receipted、SIGTERM、SIGKILL、reaped 和 orphaned。

校正任务每次运行后检查：

- 每个本地终态 Run 都有 result outbox 或 receipt。
- 每个 running 或 stopping Run 都有当前进程绑定，或处于 ownership lease 有效的 unreachable 状态。
- Supervisor active map 中的 Run 都有 SQLite 非终态记录。
- WebSocket 更换不删除本地 Run 的完成上报路径。
- 过期 ownership lease 不占用 Server 的 active Run 唯一约束。

Raft Computer 的 `AgentNoProcessResidency.assertInvariant()` 检查无进程状态，`AgentVisibleDeliveryLedger` 记录 Agent 已看到的投递。Sumi 的校正任务采用同类检查方式。

## 故障注入测试

1. Run 在连接 A 启动。A 断线，连接 B 在 Run 仍 running 时重连。Run 完成后 Server 进入 completed。
2. 第一次发送 Run result 失败。重连后 daemon 补报，Server 只应用一次。
3. Server 已应用 result，`result_receipt` 丢失。daemon 重发后 Inbox retry count 和事件数量不变。
4. claim HTTP 不返回。heartbeat、WebSocket reader 和 result sender 继续工作。
5. command ACK 后 Run 等待 semaphore。Server 保持 queued 或 starting，`started_at` 为空。
6. daemon 崩溃后重启。已有终态结果继续上报，中断 Run 写入 `process_lost`。
7. Computer 永久离线。ownership lease 过期后 Server 释放 Run 和 Inbox。
8. Server 使 token 失效后收到旧 daemon 的 result。Run 和 Inbox 状态不变。
9. suspend stop_after_current 后，当前 Run 仍可使用 Agent API，结束后不再 claim。
10. 分别注入 SIGTERM、SIGKILL 和 reap 失败。数据库不能在缺少退出证据时写入 completed 或 canceled。
11. Agent 数量超过本地并发上限。claim 数不超过执行槽加 prefetch，Agent 不因固定顺序长期得不到执行。
12. daemon shutdown 等待 Run 进入终态或 orphaned，并保留所有待上报结果。

## 当前差距

截至 2026-07-28：

- `src/computer.rs` 的 completion channel 属于单次 `connect_once`。running command 重放不会为新连接恢复完成上报。
- `run_started` 已使用 durable outbox 和 `started_receipt`，但尚未携带 ownership fencing token。offline monitor 也没有处理 active Run。
- `src/supervisor.rs` 在 Driver cancel 失败后可能从 active map 删除 Run。
- `src/driver/codex.rs` 没有检查 kill 返回值，SIGKILL 后的 reap 没有第二个 timeout。
- `migrations/postgres/0009_agents.sql` 约束同一 Agent 只有一个 active Run，但 Server 没有 ownership lease 过期处理。

实施顺序见 [GOAL.md](../../GOAL.md)。
