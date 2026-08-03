# Agent Run

[返回设计索引](../design.md)

本文档定义 Run 的模型、状态、失败判定和 Waiting 唤醒。取代 `04-agent-lifecycle-reliability.md`。

## 1. Run 是什么

Run 是 Agent 围绕一个 Focus 完成一次处理的有界执行。

Run 包含：

- 一个 `agent_id`。
- 一个可空 `task_id`。
- 一个不可变 `focus_thread_id`。
- 一组本次处理的 Inbox Items。
- 一个触发来源。
- 一个状态和终态结果。

Run 不包含所有权凭据、期限或执行资格。执行 Run 不需要证明资格：Trigger 指定 Agent，Agent 归属一台 Computer，该 Computer 上的 Driver 执行。链条每一环都唯一确定，不存在第二个候选执行者。

Run 不是 Agent 身份、Task、Provider Session 或 Driver process。

## 2. Trigger

Trigger 是要求某个 Agent 开始工作的明确事件。Trigger 记录种类和来源引用。

当前种类：mention、DM、task_activity、thread_activity、channel_activity、schedule。

Run 的行为不因 Trigger 种类分叉。种类只决定 Inbox Item 的 strength 和聚合方式，见 [Inbox 与本地凭据](06-inbox-credentials.md)。新增种类不修改 Run 逻辑。

## 3. Run 状态

```text
Trigger -> dispatched -> working -> completed
                                 -> yielded
                                 -> failed
                                 -> canceled
```

- `dispatched`：Server 已把 run_start 命令写入该 Computer 的命令 outbox。
- `working`：Driver 已开始处理。
- `completed`：本次 Items 已处理完毕。
- `yielded`：Agent 主动结束本次执行，保留未完成工作。
- `failed`：Computer 上报了具体失败原因。
- `canceled`：停止已确认。

Run 没有 `queued`、`starting`、`finalizing`、`stopping` 状态。前两者原本表达等待执行槽和启动 Driver，这两段时间不需要独立状态，因为没有任何判定依赖它们。后两者原本服务于租约回收和进程退出确认，租约已移除。

Run 上没有任何期限字段。Server 不因时间流逝改变 Run 状态。

Computer 离线时 Run 停在 `dispatched` 或 `working`，等待重连，不进入终态。

## 4. 状态变更的唯一来源

除人工取消，所有 Run 状态变更都由 Computer 上报触发。Server 不推断、不推测、不定时扫描。

Server 的输入只有两项：Computer 的上报，和 Computer 连接的通断。连接断开只改变 Computer 的可达性，不改变任何 Run。

人工取消在 `working` Run 上设置 `cancel_requested` 标记并投递停止命令。Run 转 `canceled` 仍由 Computer 上报确认。

## 5. 失败判定

失败只由掌握直接证据的一方宣告。Driver 的状况只有承载它的 Computer 知道，Server 永远是听说。因此失败判定完全在 Computer 侧，且立即上报，不等待。

`error_code` 取值：

| 取值 | 含义 | 发现方式 |
| --- | --- | --- |
| `driver_error` | Driver 返回错误，含 LLM 调用失败、配额超限、工具异常 | Driver 返回值 |
| `driver_lost` | Driver 进程消失，daemon 仍在运行 | 进程句柄已终止 |
| `computer_restarted` | daemon 启动时发现本地有 `working` Run 但无对应进程 | 启动检查 |
| `session_unavailable` | Provider Session 无法打开或恢复 | 打开 Session 失败 |
| `agent_unavailable` | Agent 已退役或配置缺失 | 本地 Agent 状态 |
| `invalid_command` | Server 下发的命令无法执行，例如引用了本地不存在的 Agent | 命令校验 |
| `internal` | Computer 内部不变量被破坏 | 本地校验 |

Computer 离线不是失败，不写入 Run。它是 Computer 的属性。UI 同时展示 Run 状态和承载 Computer 的可达性：Run 正在工作，承载它的 Computer 暂时联系不上。这与实际情况一致。

Computer 永久不回来时，Run 停在 `working` 且 Computer 不可达。这是事实，不伪造成失败。出口是人工取消或退役该 Agent。不可达超过阈值可以发通知，通知不改变状态。

## 6. 投递

Server 在处理 Trigger 的同一事务内把 run_start 命令写入该 Computer 的命令 outbox，并在连接可用时立即推送。Computer 不轮询请求工作。

命令 outbox 保证：每台 Computer 单调递增序号、持久化、重连重放未确认命令、接收方按命令 ID 幂等处理。

Computer 收到 run_start 后启动 Driver，上报 `run_started`，Run 转 `working`。

## 7. 重连同步

Computer 重连后，双方同步一次实际状态：

1. Computer 握手时带 daemon session ID 和命令 watermark。
2. Server 重放未确认命令。
3. Computer 在握手中报告本地仍持有的非终态 Run，以及结果尚未收到回执的终态 Run；随后上报每个非终态 Run 的实况：仍在处理、已结束（附结果）、已失败（附 `error_code`）。
4. Computer 重发已结束但未收到回执的结果。
5. Server 按上报更新，并对本地已不存在的 Run 按 `computer_restarted` 处理。

Server 发现 `dispatched` Run 仍有未确认的 `run.start` command 时保留该 Run，先重放 command；该 Run 不能在 command 被 Computer 处理前按 `computer_restarted` 终结。

daemon session ID 标识连接会话，用于丢弃上一会话的残留帧。它属于连接，不属于 Run，不表达执行资格。

## 8. Waiting 与唤醒

Agent 没有活跃 Run 时处于 Waiting。Waiting 是正常执行状态，不是失败，见 [术语表](../../GLOSSARY.md)。

Waiting 的 Agent 由新的 pending Inbox Item 唤醒。唤醒不需要独立机制：Item 到达即产生 Trigger，Trigger 产生 Run。

## 9. Yield 与 schedule

Agent yield 时对本次 Items 逐条给出 disposition：

- `handled`：已完成。
- `released`：未完成，立即回到 pending。
- `deferred`：未完成，到指定时间点再次可取。

`deferred` 通过设置 Item 的 `available_at` 表达，见 [Inbox 与本地凭据](06-inbox-credentials.md)。Agent 提交 defer 时给出时间点，Server 写入 `available_at`。该时间到达后 Item 重新成为 pending，唤醒 Agent。

如果 Server 在 Computer 已结束 Run 后才投递同一 Focus 的 `run.attach_item`，Computer 必须保存 `TooLate` delivery receipt。Server 收到该 receipt 后释放对应 Item；该 Item 不得进入已结束 Run 的结果列表。

这使 Agent 能表达「我在等构建结果，20 分钟后再看」。Server 不解释等待原因，只按时间点使 Item 重新可取。

Yield 保留 Provider Session 供后续同 scope Run 复用。Yield 不改变 Task 状态；等待原因保存在 Run outcome。

Yield 不是 cancel，不是 Session reset。

## 10. 并发上限

`computer.max_concurrent_runs` 是内存保护阈值，防止一台 Computer 同时运行过多 Driver 进程耗尽内存。默认 32。

它不是调度器，不用于性能调优，取值不依据 CPU 核数。Agent 执行以等待 Driver 返回为主，不以本机计算为主。

超过上限的 Run 停在 `dispatched` 等待，不会因等待失败。没有任何定时判定依赖执行时机，等待因此是安全的。

## 11. 授权

Agent 执行 Action 的授权由它在 Space 中的 Permission 决定，见 [产品基础与系统结构](01-foundations.md)。授权不依赖 Run 状态、不依赖任何 Run 凭据。

Run 只表达一次执行，不表达权限。

## 12. 必测故障

- Driver 报错时 Run 转 `failed` 并携带 `driver_error`。
- Driver 进程被外部终止时 Run 转 `failed` 并携带 `driver_lost`。
- daemon 重启后本地 `working` Run 上报 `computer_restarted`。
- Computer 离线期间 Run 保持原状态，Server 不改变它。
- Computer 离线超过任意时长后重连，已完成的结果仍被接受。
- 命令重复投递和结果重复上报按 ID 幂等。
- 上一 daemon session 的残留帧被丢弃。
- Agent defer Item 到指定时间点后，该时间到达时 Item 重新可取并唤醒 Agent。
- 并发上限内排队的 Run 在槽位释放后正常开始，且期间不失败。
