# Inbox 与本地凭据

[返回设计索引](../design.md)

## 1. Inbox 保证

Inbox是注意力事实，不是Message历史。Sumi保证：

- 有资格的信息会持久生成Inbox Item。
- Item在明确处理前不会因进程退出而消失。
- 领取、附加、释放和完成操作可以幂等重试。
- Agent能知道active Run之外还有新的hard attention。

Sumi不保证模型一定判断正确，也不通过另一个模型替Agent决定相关性。

## 2. Item 类型

| 来源 | 类型 | 强度 |
| --- | --- | --- |
| DM新Message | direct | hard |
| mention Agent | mention | hard |
| reply指向Agent Message | reply | hard |
| Linked Thread新Message | task_activity | hard |
| Agent订阅的普通Thread更新 | thread_activity | ambient |
| Agent所在Channel普通Message | channel_activity | ambient |
| 系统或执行错误 | system | hard |

同一Message对同一Agent只生成一个最高强度Item。发送者不为自己生成Message Item。

## 3. Item 状态

```text
pending -> leased -> handled
   ^          |----> deferred
   |          |----> pending
   +----------+

pending/leased -> dead
```

Item保存来源Message、Thread、可选Task、强度、available time、lease、retry count和处理结果。Item不复制Message正文。

## 4. Task 路由

Message位于未完成Task的Linked Thread时，Server在发送事务中把`task_id`写入Item。该关系来自显式link，不来自正文判断。

未关联 Thread 中的 Item 没有 Task。Server 可以用该 Thread 创建普通 Run。

Agent 决定开始持续工作时，可以在 Run 中调用无来源参数的`task create`。

Server 从 Focus 找到 Root Message。Server 原子创建 Task、绑定当前 Run，并更新已领取 Item。

Thread reply不能成为Task source。reply触发的Run调用`task create`时，Server仍使用Focus Thread的Root Message。

## 5. active Run 路由

hard Item生成后，attention模块只做结构化判断：

- 与active Run的Agent、可选Task scope和Focus一致时，尝试attach。
- Task或Focus不同，Item保持pending并生成notice。
- active Run不是running时，Item保持pending。
- ambient Item只聚合，不attach active Run。

daemon和Server不得读取正文决定attach或notice。

## 6. Ambient 聚合

Server 按 Agent 和 Thread 聚合 ambient activity。一个 Agent 在一个 Thread 上最多有一个 pending 聚合项，因此该 Thread 的连续普通 Message 只占一条 Item。聚合项保存首尾 Message 序号、数量、available time 和 force time，见 [数据库设计](08-database.md) 的`inbox_items`。

聚合项代表一个 Message 区间，因此不指向单条 Message，也不复制正文。Agent 通过区间读取该 Thread 的 Message。

available time 在每条新 Message 到达时重置为该时刻加`ambient_debounce_seconds`。force time 在聚合项创建时确定为该时刻加`ambient_max_wait_seconds`，之后任何 Message 都不能改写它。available time 取两者较小值，因此持续活跃的 Thread 最迟在 force time 变为可领取。该上限是防止新 Message 无限推迟处理的唯一机制。

已领取的聚合项不再接受新 Message。该 Agent 已经收到这个区间，后续 Message 进入下一个聚合项。

hard Item 优先于 ambient Item。ambient Item 在 Agent 有执行容量时才创建 Run：领取要求该 Agent 没有非终态 Run，且同一批候选中 hard Item 先被取走。

## 7. notice

不同Focus hard Item到达时，Server向active Run发送notice。notice用于让Agent决定是否yield，不授予该Item lease。

notice包含：

- `notice_id`
- Item类型和强度
- 可公开的Task ID和Thread ID
- 到达时间
- 是否来自Human明确转向请求

Agent当前无权读取来源时，notice只显示“另一个受限位置有待处理事项”。Agent不能通过notice读取Message正文或Attachment。

## 8. 重试和dead

lease过期时，Server把该Run未处理的Items返回pending并增加retry count，同时把Run置为`failed`并记录`process_lost`。retry count超过`max_retry_count`后Item进入dead，并在同一事务创建不含正文的system Item。

该system Item归属该Agent，因为`inbox_items.agent_id`引用`agents`，Human不能持有Item。Space治理者通过读取Agent Inbox看到它，见 [API 与事件](07-api.md) 的 Inbox 读取授权。

只有lease过期计入retry count。Agent显式release一个Item是它的处理决定，不是失败尝试，因此不增加计数。网络错误、receipt丢失和重复command同样不得重复增加retry count。

Run终态由回收事务写入，因此该Agent的非终态Run被释放，后续Run不再被 partial unique index 阻止，见 [Agent Run 可靠性](04-agent-lifecycle-reliability.md)。

Server领取Item或创建Run失败时，Item保持`pending`并记录稳定的`last_error_code`。来源Message的可见投影必须向有权读取该Message的Human显示对应Agent、错误码和自动重试状态。该提示是运行状态，不创建Message，也不冒充Member发言。

后续领取成功时，Server必须在同一事务中清除`last_error_code`。运行状态变化必须触发来源Message投影刷新。

## 9. 本地凭据

Server不接收、不保存模型API key。以下Secret只存在于Computer受限文件和必要进程内存：

- raw Computer Token。
- Codex本地认证。
- Builtin provider认证。

Server只保存Computer Token hash。Browser不提供模型Secret表单。Agent CLI和Driver工具进程不能读取Computer Token。Builtin工具进程不能读取模型API key。

本地文件权限和sandbox只能降低误读风险，不能防御root、同一OS用户下的恶意进程或已失陷Computer。产品文案必须说明该边界。

## 10. 日志

日志和错误不得包含：

- Message、Attachment或Memory正文。
- Provider transcript。
- Task Result正文。
- Computer Token或模型凭据。
- 完整命令参数和环境变量。

诊断使用稳定ID、状态、错误代码、计数、时间和hash。
