# Inbox 与本地凭据

[返回设计索引](../design.md)

## 1. Inbox 保证

Inbox 是注意力事实，不是 Message 历史。Sumi 保证：

- 有资格的信息会持久生成 Inbox Item。
- Item 在明确处理前不会因进程退出而消失。
- 分配、附加、释放和完成操作可以幂等重试。
- Agent 能知道 active Run 之外还有新的 hard attention。
- Human 能直接看到与自己相关的 hard attention，不必逐群检查 Channel。

Sumi 不保证模型一定判断正确，也不通过另一个模型替 Agent 决定相关性。

## 2. Item 类型

Agent 与 Human 共享同一 Item 模型。路由只面向发送者的 Channel 中未退役的其他 Member。

Agent 路由：

| 来源 | 类型 | 强度 |
| --- | --- | --- |
| DM 新 Message | direct | hard |
| mention Agent | mention | hard |
| `@all` in a Channel | mention | hard |
| reply 指向 Agent Message | reply | hard |
| Linked Thread 新 Message | task_activity | hard |
| Agent 订阅的普通 Thread 更新 | thread_activity | ambient |
| Agent 所在 Channel 普通 Message | channel_activity | ambient |
| 系统或执行错误 | system | hard |

Human 路由：

| 来源 | 类型 | 强度 |
| --- | --- | --- |
| DM 新 Message | direct | hard |
| mention Human | mention | hard |
| `@all` in a Channel | mention | hard |
| reply 指向 Human Message | reply | hard |
| Linked Thread 新 Message | task_activity | hard |
| Human 订阅的 Thread 更新 | thread_activity | ambient |

Human 不生成`channel_activity`：普通 Channel Message 已在 Channel 时间线可见，不构成额外注意力。Browser 只在 Human Inbox 投影层把多个 hard Item 按同一 DM 或 Thread 叠放，不改变 Item 的持久事实和处理状态。

同一 Message 对同一 Member 只生成一个最高强度 Item。发送者不为自己生成 Message Item。

Message 的 mention targets 是结构化事实。显式 mention 保存为 Message 与 Member 的关系；`mention_all=true` 时，Server 在发送事务中把当前 Channel 中未退役的 Member（发送者除外）保存为 targets，并把其中的 Agent 与 Human 路由为 hard `mention` Item。消费端不得从 Message 正文推断 targets。Task、DM、mention 和 reply 的既有选择顺序保持为 Task > DM > mention > reply；同一 Member 只保留一个最高强度 Item。

## 3. Item 状态

```text
Agent Item:
pending -> assigned -> handled
   ^           |----> deferred
   |           |----> pending
   +-----------+

pending/assigned -> dead

Human Item:
pending -> handled
```

`assigned` 表示该 Item 已归入某个非终态 Run，由 `assigned_run_id` 指向该 Run。它不是租约，没有期限：Item 停留在 `assigned` 直到该 Run 上报终态。

Human Item 不进 `assigned`：没有 Human Run 处理它。Human 打开来源 Message 时，Server 把该 Item 标记为 handled；重复读取已经 handled 的 Item 幂等。Human Item 不进入 deferred 或 dead。

Item 保存来源 Message、Thread、可选 Task、强度、available time、`assigned_run_id`、retry count 和处理结果。Item 不复制 Message 正文。

## 4. Task 路由

Message 位于未完成 Task 的 Linked Thread 时，Server 在发送事务中把`task_id`写入 Item。该关系来自显式 link，不来自正文判断。

未关联 Thread 中的 Item 没有 Task。Server 可以用该 Thread 创建普通 Run。

Agent 决定开始持续工作时，可以在 Run 中调用无来源参数的`task create`。

Server 从 Focus 找到 Root Message。Server 原子创建 Task、绑定当前 Run，并更新该 Run 的 Items。

Thread reply 不能成为 Task source。reply 触发的 Run 调用`task create`时，Server 仍使用 Focus Thread 的 Root Message。

## 5. active Run 路由

hard Item 生成后，attention 模块只做结构化判断：

- 与 active Run 的 Agent、可选 Task scope 和 Focus 一致时，尝试 attach。
- Task 或 Focus 不同，Item 保持 pending 并生成 notice。
- active Run 不是 `working` 时，Item 保持 pending。
- ambient Item 只聚合，不 attach active Run。

daemon 和 Server 不得读取正文决定 attach 或 notice。

## 6. Ambient 聚合

Server 按 Member 和 Thread 聚合 ambient activity。一个 Member 在一个 Thread 上最多有一个 pending 聚合项，因此该 Thread 的连续普通 Message 只占一条 Item。聚合项保存首尾 Message 序号、数量、available time 和 force time，见 [数据库设计](08-database.md) 的`inbox_items`。

聚合项代表一个 Message 区间，因此不指向单条 Message，也不复制正文。Agent 通过区间读取该 Thread 的 Message；Human 的聚合项保持 pending，直到打开来源。

available time 在每条新 Message 到达时重置为该时刻加`ambient_debounce_seconds`。force time 在聚合项创建时确定为该时刻加`ambient_max_wait_seconds`，之后任何 Message 都不能改写它。available time 取两者较小值，因此持续活跃的 Thread 最迟在 force time 变为可派发。该上限是防止新 Message 无限推迟处理的唯一机制。

已进入 `assigned` 的聚合项不再接受新 Message。该 Agent 已经收到这个区间，后续 Message 进入下一个聚合项。

hard Item 优先于 ambient Item。派发要求该 Agent 没有非终态 Run，且同一批候选中 hard Item 先被取走，见 [Agent Run](04-agent-run.md) 的投递。

## 7. notice

不同 Focus hard Item 到达时，Server 向 active Run 发送 notice。notice 用于让 Agent 决定是否 yield，不把该 Item 归入当前 Run。

notice 包含：

- `notice_id`
- Item 类型和强度
- 可公开的 Task ID 和 Thread ID
- 到达时间
- 是否来自 Human 明确转向请求

Agent 当前无权读取来源时，notice 只显示“另一个受限位置有待处理事项”。Agent 不能通过 notice 读取 Message 正文或 Attachment。

## 8. 重试和 dead

Run 转 `failed` 时，Server 在同一事务把该 Run 未处理的 Items 返回 pending 并增加 retry count。retry count 超过`max_retry_count`后 Item 进入 dead，并在同一事务创建不含正文的 system Item。失败由 Computer 上报，判定规则见 [Agent Run](04-agent-run.md)。

该 system Item 只面向 Agent 执行错误，归属该 Agent。Space 治理者通过读取 Agent Inbox 看到它，见 [API 与事件](07-api.md) 的 Inbox 读取授权。

只有 Run 失败计入 retry count。Agent 显式 release 一个 Item 是它的处理决定，不是失败尝试，因此不增加计数。网络错误、receipt 丢失和重复 command 同样不得重复增加 retry count。

Run 终态与 Item 释放在同一事务写入，因此该 Agent 不再持有非终态 Run，后续 Run 不再被 partial unique index 阻止。

Server 分配 Item 或创建 Run 失败时，Item 保持`pending`并记录稳定的`last_error_code`。来源 Message 的可见投影必须向有权读取该 Message 的 Human 显示对应 Agent、错误码和自动重试状态。该提示是运行状态，不创建 Message，也不冒充 Member 发言。

后续派发成功时，Server 必须在同一事务中清除`last_error_code`。运行状态变化必须触发来源 Message 投影刷新。

## 9. 本地凭据

Server 不接收、不保存模型 API key。以下 Secret 只存在于 Computer 受限文件和必要进程内存：

- raw Computer Token。
- Codex 本地认证。
- Builtin provider 认证。
- Driver token：daemon 启动时生成 capability secret 并为每个 Agent 派生，只注入该 Agent 的 app-server 进程环境；不出本机、不落盘，工具子进程不能读取其他 Agent 的 Driver token。

Server 只保存 Computer Token hash。Browser 不提供模型 Secret 表单。Agent CLI 和 Driver 工具进程不能读取 Computer Token。Builtin 工具进程不能读取模型 API key。

本地文件权限和 sandbox 只能降低误读风险，不能防御 root、同一 OS 用户下的恶意进程或已失陷 Computer。产品文案必须说明该边界。

## 10. 日志

日志和错误不得包含：

- Message、Attachment 或 Memory 正文。
- Provider transcript。
- Task Result 正文。
- Computer Token 或模型凭据。
- 完整命令参数和环境变量。

诊断使用稳定 ID、状态、错误代码、计数、时间和 hash。
