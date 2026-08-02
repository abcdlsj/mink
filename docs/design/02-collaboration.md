# 协作、Task 与 Thread

[返回设计索引](../design.md)

## 1. 对话模型

Channel 主时间线只包含 Root Message。每条 Root Message 都定义一个 Thread，即使该 Thread 还没有 reply。

Thread reply 通过`thread_id`归属于该 Thread。

```text
Channel
  +-- Root Message A == Thread A root
  |     +-- Reply A1
  |     +-- Reply A2
  +-- Root Message B == Thread B root
```

Thread 继承 Channel 的可见范围。Thread 不拥有独立成员表，也不能扩大 Channel 权限。

Message reply 可以引用同一 Thread 的 root 或 reply。跨 Thread reply 必须被 Server 拒绝。

### 1.1 Action Message

Agent 创建 Channel、创建 Agent 或执行其他需要协作者知晓的领域 Action 时，Server 必须在当前 Focus 中创建 Action Message。

Action Message 与领域 Action 属于同一事务。Action 失败时不创建 Message；Message 写入失败时不提交 Action。

Action Message 保存 actor、action kind 和目标资源 ID。普通 Message API 不能伪造 Action Message。Action Message 不替代 audit。

Agent Action Message 是当前 Focus 的 reply，不能成为 Root Message 或 Task source。该 Message 不允许编辑。

首批 action kind 只包含`channel_created`和`agent_created`。新增 kind 必须同时定义领域事务、权限、Message 投影和 UI。

### 1.2 Context Citation

Context Citation 把 Agent Message 中一段连续原文指向一条来源 Message。一个回答片段可以指向多条来源 Message；一条 Agent Message 可以包含多个回答片段。

Context Citation 只能随 Agent 的普通 Message 原子创建。它不能由 Browser 补写，也不能单独编辑。每项声明包含回答原文、来源 Message ID 和可选来源原文。Server 将原文解析为 Unicode 标量位置，并拒绝空文本、找不到的文本或在同一正文中出现多次的文本。

来源 Message 必须满足以下条件之一：

- 它属于当前 Run 的 Focus，且已包含在该 Run 的消息快照中。
- 它是当前 Run 已领取 Inbox Item 的来源 Message。

来源必须是调用 Agent 可读的公开 text Message。Server 不接受 Provider Session transcript、Memory、workspace 文件、Attachment 正文、不同 Focus notice或隐藏推理作为 Context Citation 来源。

Context Citation 是 Agent 对公开证据的声明。它证明该来源已进入本次 Run，并且 Agent 把它关联到回答片段；它不证明模型内部 attention、完整因果链或来源事实正确。

## 2. Task 模型

Task 是一项持续工作的正式记录。Task 至少包含：

- `id`
- `space_id`
- `title`
- `status`
- `source_thread_id`
- `creator_member_id`
- `assignee_agent_member_id`
- `result_message_id`
- `close_reason_code`、`close_reason_note`
- `created_at`、`updated_at`、`finished_at`

Task 状态固定为：

```text
TODO -> In Progress -> In Review -> Done
                     \-----------> Done

TODO / In Progress / In Review -> Closed
```

- `todo`：Task已经成立，尚未开始处理。
- `in_progress`：assignee正在推进Task。该状态不表示此刻一定存在active Run。
- `in_review`：工作结果已经提交，正在等待另一位 Human 或 Agent Member 确认。该阶段可以跳过。
- `done`：Result已经确认，Task正常完成。
- `closed`：Task因错误、重复、无用或废弃而终止。

进入`in_progress`前必须有assignee。`in_progress`可以直接进入`done`，也可以先进入`in_review`。`done`和`closed`是终态。

`closed`必须保存结构化原因和可选说明。原因取`invalid|duplicate|not_needed|obsolete|other`。

等待 Human、外部系统或未来事件不改变 Task 状态。UI 从最近 Run outcome 和 pending Item 显示等待原因。

状态转换规则固定为：

- Assigned Agent的首个Task Run进入`running`时，Server把`todo`改为`in_progress`。
- assignee可以把`in_progress`改为`in_review`。
- assignee可以在不需要复核时把`in_progress`直接改为`done`。
- 除 assignee 外，能读取 Task 的 Human 或 Agent Member 可以把`in_review`改为`done`或退回`in_progress`。
- creator、assignee或有权治理且能读取Source Thread的Human可以把未结束Task改为`closed`。
- `done`和`closed`在v1不可重新打开。需要继续工作时创建新的Root Message和Task。

Task 不包含子任务、依赖、优先级、截止时间、评论或单独权限模型。

Review 不使用 Permission、reviewer Role 或 reviewer 绑定。Assignee 根据工作上下文在 review Message 中通知合适的 Human 或 Agent。系统不保存 reviewer 字段，也不自动选择 reviewer。

## 3. 从来源创建 Task

只有 Root Message 可以成为 Task source。Thread reply、Attachment、Inbox Item 和 Run 都不能成为来源。

Agent在Run中发起创建时只提交：

- 可选 `title`
- 可选 `assignee_agent_member_id`
- `idempotency_key`

Server 从当前Run的Focus推导Root Message、`space_id`、`channel_id`、`source_thread_id` 和可见范围。Server 在一个事务中：

1. 校验调用 Agent 可以读取 Root Message。
2. 校验 Message 位于 Channel 主时间线。
3. 锁定Root Message和Thread。
4. 创建Task，并把推导出的Thread直接写入`source_thread_id`。
5. 将当前Run和它领取的同Focus Items绑定到Task。
6. 写入本地Session提升command、audit和outbox。
7. 返回包含Source Thread的完整Task。

事务中的任一步失败时，不得留下Task或半绑定Run。Agent不调用第二个bind命令，也不提交Message ID或Thread ID。

Human创建的Task初始为`todo`。Agent在普通Run中创建Task时，Server同时绑定当前Run和当前Agent；Run已经是`running`，所以Task直接进入`in_progress`。

同一Root Message最多创建一个Task。相同idempotency key重试必须返回同一Task。不同请求并发创建时，一个成功，另一个返回现有Task冲突。

Human WebUI 使用同一领域命令。Browser 的“一步创建”与 Agent CLI 不得实现两套事务。

## 4. Linked Threads

Task 创建后可以关联额外 Thread。关联表达“该 Thread 正在讨论同一项工作”，不复制 Message，也不改变 Thread 权限。

Source Thread直接保存在Task，不创建link行。每个Related link包含：

- `task_id`
- `thread_id`
- `linked_by_member_id`
- `linked_at`

Source Thread不可删除或更换。Related link可以由Task assignee或有权读取两个对象的Human添加和移除。

一个 Thread 同时最多关联一个未结束 Task。Task 进入`done`或`closed`后，link 保留为历史。

需要开始另一项工作时，Member 必须发送新的 Root Message，再从新 Thread 创建 Task。

Server 不使用标题相似度、mention、共享 Attachment 或正文分类器自动关联。

UI 可以显示由 Human 或 Agent 明确触发的建议。建议不能产生 link。

### 4.1 可见范围兼容

v1 只允许 Task 关联可见范围兼容的 Threads。兼容表示各 Thread 所属 Channel 的有效成员集合相同。

该限制确保一个 Provider Session 中的 Task 内容可以安全用于任一 Linked Thread。Channel membership 变化导致集合不再相同时：

1. Server 保留历史 link。
2. Task状态保持不变，并记录可见的runtime issue。
3. active Run停止接收新Item。
4. Computer 关闭该 Task 的 Provider Session。
5. assignee 或 Human 移除不兼容 link，或在统一成员集合后继续 Task。

v1 不通过隐藏部分 links 或按成员生成多个 Task 投影绕过该限制。

## 5. Focus

每个 Run 必须选择一个 Thread 作为 Focus。Run绑定Task后，该Focus必须是Task的Linked Thread。Focus决定：

- 本 Run 默认读取的消息窗口。
- 新 hard Item 是否可以加入 active Run。
- Agent 的普通回复默认发送到哪个 Thread。
- UI 显示 Agent 正在处理的位置。

Focus 不限制 Agent读取其他已授权 Channel。Agent读取其他位置不会自动改变 Focus，也不会创建 Task link。

Run 期间不得切换 Focus。Agent要处理另一个 Focus 时，必须 yield 或完成当前 Run，再由 Server 创建新 Run。

## 6. Result 与协作输出

普通Agent Message是对话输出。Task Result引用一条Message，不复制正文。进入`done`时，Server在一个事务中：

1. 在 Source Thread 或当前 Focus 发送结果 Message。
2. 将该Message ID写入`result_message_id`。
3. 将Task标记为`done`并写入`finished_at`。
4. 处理对应 Inbox Items。
5. 写入 audit 和 outbox。

Result Message必须保存在Server。Driver的final output、stdout或Provider Session内容不能自动成为Result。

Task 进入`in_review`时应该发送可审阅的 Message，但不填写`result_message_id`。

除 assignee 外，能读取 Task 的 Human 或 Agent Member 可以将其退回`in_progress`或确认进入`done`。

不需要独立复核时，assignee 可以从`in_progress`直接进入`done`。

Task 进入`done`或`closed`后，系统保留 Source Thread、Linked Thread、Run 和 Result 历史。

Computer 应关闭对应的 Provider Session。关闭失败不能回滚 Task 终态。

`in_review`、`done`和`closed`由同一个事务入口写入，见 [产品基础与系统结构](01-foundations.md)。Agent 从 Run 内提交时，同一事务额外校验 fencing token、处理已领取 Items 并完成 Run；Human 从 Browser 提交时不持有 Run。两条路径不得各自实现一套终态事务。

## 7. 权限

- 创建 Task 的 Member 必须能读取 source Root Message。
- assignee 必须是同一 Space 的 active Agent，并属于 source Channel。
- 添加 Related Thread 的 Member 必须能读取 Task 的全部现有 Linked Threads 和目标 Thread。
- Task 可见性等于其 Linked Threads 的兼容成员集合。
- Access Level 不自动绕过 private Channel membership。
- Task 不引入独立 ACL。

权限检查发生在 Server。Browser、daemon 和 Agent CLI 的本地检查只用于提前返回可理解错误。

## 8. 删除与编辑

Root Message成为Task source后不得硬删除。作者或Admin可以软删除正文，但必须保留来源占位、Message ID、Thread和Task。

来源 Message 软删除后，Context Citation 关系保留，读取投影不返回该项引用或已删除正文。Agent capability 当前没有编辑 Message 的入口。

编辑回答 Message 或来源 Message 时，Server 在同一事务中删除涉及该 Message 的 Context Citations。字符范围只描述创建引用时的正文，正文改变后不得继续投影旧范围。

编辑 source Root Message 不修改 Task title。Task title 是独立事实，只能通过 Task update 修改。

删除reply不改变Task link。归档Channel不删除Task，也不改写Task状态。系统阻止该Task的新Run，直到Channel恢复或Task进入`closed`。

## 9. 典型流程

### 9.1 Agent 从 Channel 消息开始工作

```text
Human 发送 Root Message
  -> Agent 收到 hard/ambient Item
  -> Server 创建以该Thread为Focus的普通Run
  -> Agent 调用 task create
  -> Server原子创建Task并绑定Source Thread和当前Run
  -> Computer将当前Thread Session提升为Task Session
```

### 9.2 同一 Task 出现另一条讨论

```text
Human 或 assignee 显式关联 Related Thread
  -> Server 校验可见范围和未完成 Task 唯一性
  -> 后续 Run 可以选择该 Thread 为 Focus
  -> Provider Session 仍按同一 Task 复用
```

### 9.3 Agent在reply触发的Run中创建Task

Agent显式调用`task create`后，Server使用Focus Thread的Root Message作为source。reply只触发Run，不成为Task source。Agent不提交或绑定reply ID。
