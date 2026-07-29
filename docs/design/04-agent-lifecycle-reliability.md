# Agent Run 可靠性

[返回设计索引](../design.md)

## 1. Run 定义

Run 是 Agent 围绕一个 Focus 完成一次处理的有界执行。Run包含：

- 一个 `agent_id`。
- 一个可空 `task_id`。
- 一个不可变 `focus_thread_id`。
- 一组已领取 Inbox Items。
- 一个 ownership lease 和 fencing token。
- 零个或一个本地 Driver turn。
- 一个终态结果。

Run 不是 Agent 身份、Task、Provider Session 或 Driver process。

## 2. Run 状态

```text
queued -> starting -> running -> finalizing -> completed
                         |            |------> yielded
                         |            |------> failed
                         +-> stopping -------> canceled
```

- `queued`：Server 已创建 Run，Computer 尚未取得执行槽。
- `starting`：Computer 正在解析 Session、校验环境或启动 Driver。
- `running`：Driver 已开始处理，Run 可以接收同 Focus hard Item。
- `finalizing`：Driver turn 已结束，接收集合被冻结，Server 正在提交处理结果。
- `completed`：本次领取的 Items 已处理，Task 可以继续存在。
- `yielded`：Agent 主动释放执行权，保留尚未完成的 Task 工作。
- `failed`：执行失败，未处理 Items 按策略重试。
- `stopping`：已请求停止，进程退出尚未确认。
- `canceled`：停止已确认。

所有 Runs必须到达一个终态。Run 终态不自动等于 Task 终态。

## 3. 有界条件

Run 的边界由一次处理周期决定，不由 token 数量决定。

以下事件开始 finalizing：

- Driver turn 正常结束。
- Agent 显式 yield。
- Task 完成或取消命令已提交。
- Human 请求 stop after current。
- Run 达到执行安全上限。

进入 finalizing 后，Run 不再接受新 Inbox Item。新 Item 保持 pending，并等待后续 Run。

该冻结点替代公开的`settle`概念。Agent 不需要调用额外的 settle 命令。

执行安全上限用于回收失控进程和租约，不决定 Provider Session 是否换新。

Run 达到上限时可以 yield 或 failed。后续 Run 仍可 resume 同一 Session。

## 4. active Run 接收新 hard Item

新 hard Item 到达时，Server按以下规则处理：

```text
没有 active Run
  -> Item 保持 pending，scheduler 创建 Run

存在 running Run，Item 属于同一Task scope和Focus
  -> 原子加入 Run
  -> 推送完整 Item 给 Computer
  -> Driver adapter steer 当前 Provider Session

存在 running Run，Item 属于不同Focus或不同Task scope
  -> Item 保持 pending
  -> 只向当前 Run 发送 attention notice

Run 已 finalizing/stopping
  -> Item 保持 pending
```

attention notice 只包含来源类型、Task/Thread 标识、优先级和到达时间。当前 Run 未获授权时不得包含正文或 private Channel 地址。

Agent收到不同 Focus notice 后可以继续当前 Run，也可以 yield。系统不得自动切换 Focus、Task 或 Provider Session。

## 5. 同 Focus attach 事务

将 hard Item 加入 active Run 必须在 Server 事务中：

1. 锁定 Run 和 Item。
2. 校验 Run 仍为 `running`。
3. 校验Item的可选Task和Thread与Run scope相同。
4. 将 Item 从 `pending` 改为 `leased`。
5. 创建 `run_items` 关系。
6. 分配递增 `run_delivery_seq`。
7. 写入 Computer command 和 outbox。

Computer按 `run_delivery_seq` 幂等接收。重复交付不得重复 steer。Run在事务前进入 finalizing 时，Item保持 pending。

普通 Run 的`task_id`为空。Agent 从当前 Focus 创建 Task 时，Server 在 Task 事务中填写 Run 和已领取 Item 的`task_id`。

此后，只有同 Task 和 Focus 的 hard Item 可以 attach。

## 6. Driver turn 与 steer

Provider Session 可以在一个 Run 中包含初始 turn 和后续 steering 输入。Driver adapter 必须暴露：

```text
open_or_resume(session_spec) -> session
start_turn(session, run_input) -> turn
steer(turn, item_input) -> accepted | too_late
interrupt(turn, reason) -> outcome
observe(turn) -> events
close(session, reason) -> outcome
```

`too_late` 表示 Driver turn 已完成。Computer 将 Item delivery 保持未消费并报告 Server；Server把 Item释放回 pending，后续 Run处理。

Driver不支持 steer 时，Computer发送 attention notice并让 Item保持 pending。Driver能力差异不能改变 Server 的 Task 和 Run 事实。

## 7. yield

Yield 用于释放当前 Focus，让 Agent转向另一个 waiting item。Agent提交 yield 时可以：

- handle 已完成的 Items。
- defer 尚不需要处理的 Items。
- release 未完成的 Items。
- 写入不含隐藏推理的简短 continuation note。

Server原子提交Item状态和Run `yielded` 结果。Yield不改变Task状态；等待原因保存在Run outcome。Provider Session保持ready，供后续同scope Run resume。

Yield 不是 Session reset，也不是 Task cancel。

## 8. 完成与结果

Run 完成事务必须：

1. 验证 ownership fencing token。
2. 验证所有 `run_items` 已 handled、deferred 或 released。
3. 保存 Run 终态和结构化 outcome。
4. 更新 Inbox Items。
5. 可选更新 Task 状态和 Result。
6. 写入 audit 和 Server outbox。
7. 返回 result receipt。

Computer先在 SQLite 原子保存本地终态和 result outbox，再重试上报直到收到 receipt。WebSocket 断开不能丢失结果。

## 9. 租约与 fencing

Server为 Run生成 ownership lease 和 fencing token。`run_started`、renew、Item delivery receipt、activity 和 result 都必须携带 token。

lease 过期后，Server可以使 token 失效并释放 Items。旧 Computer 后续上报只能写诊断记录，不能改变 Task、Run 或 Inbox。

Command delivery 使用 at-least-once。所有 command、event 和 receipt 都有稳定 ID，并由接收方幂等处理。

## 10. daemon 重启与断线

WebSocket 断开不改变本地 Driver、Run、Session 或 result outbox。重连时：

1. daemon发送 command watermark 和 daemon session ID。
2. Server重放未确认 commands。
3. daemon重发 started 和 result events。
4. 同一 fencing token 的 running Run保留现有进程。
5. Session registry继续使用本地 locator。

daemon重启时：

- 有进程所有权证据的 Run可以重新接管。
- 无法证明进程仍受控的 Run写入 `process_lost`。
- 已结束但未回执的结果继续上报。
- ready Provider Session保留；resume失败时创建新 generation。

## 11. 公平调度

Computer scheduler按本机执行槽限制并发。一个 Agent最多占一个 active Run。不同 Agents按 round-robin 取得执行槽，hard Item优先于 ambient Item。

同一 Agent有多个 pending Focus 时，选择顺序为：

1. Human明确要求切换的 Item。
2. hard Item 的到达时间。
3. 已有 Task 的连续性。
4. ambient Item 的聚合时间。

Server不根据正文、标题或模型判断优先级。

## 12. 必测故障

- hard Item 与 Run finalizing 并发。
- 同 Focus Item重复交付和重复 steer。
- 不同 Focus notice 到达后 Agent yield。
- `run_started` 成功但 receipt 丢失。
- result提交成功但 receipt 丢失。
- WebSocket断线期间 Driver完成。
- daemon重启后进程存在和进程丢失两种情况。
- lease过期后旧 fencing token上报。
- Provider Session resume失败后新 generation恢复 Task。
- Task完成后 Session close失败。
- 多 Agent竞争执行槽且无长期饥饿。
