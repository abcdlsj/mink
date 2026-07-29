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

Server按Agent和Thread聚合ambient activity。聚合项保存首尾Message序号、数量、available time和force time。新Message不能无限推迟force time。

hard Item优先于ambient Item。ambient Item在Agent有执行容量时才创建Run。

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

Run失败或lease过期时，未处理Items返回pending并增加retry count。达到上限后Item进入dead，并为有权治理该Agent的Human创建不含正文的system Item。

网络错误、receipt丢失和重复command不得重复增加retry count。

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
