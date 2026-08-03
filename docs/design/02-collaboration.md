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

## 2. Task 模型

Task 是一项持续工作的正式记录。Task 至少包含：

- `id`
- `seq`：Space 内自增的短序号，是 Task 的唯一外显引用，UUID 不对外暴露。
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

- `todo`：Task 已经成立，尚未开始处理。
- `in_progress`：assignee 正在推进 Task。该状态不表示此刻一定存在 active Run。
- `in_review`：工作结果已经提交，正在等待另一位 Human 或 Agent Member 确认。该阶段可以跳过。
- `done`：Result 已经确认，Task 正常完成。
- `closed`：Task 因错误、重复、无用或废弃而终止。

进入`in_progress`前必须有 assignee。`in_progress`可以直接进入`done`，也可以先进入`in_review`。`done`和`closed`是终态。

`closed`必须保存结构化原因和可选说明。原因取`invalid|duplicate|not_needed|obsolete|other`。

等待 Human、外部系统或未来事件不改变 Task 状态。UI 从最近 Run outcome 和 pending Item 显示等待原因。

状态转换规则固定为：

- Assigned Agent 的首个 Task Run 进入`running`时，Server 把`todo`改为`in_progress`。
- assignee 可以把`in_progress`改为`in_review`。
- assignee 可以在不需要复核时把`in_progress`直接改为`done`。
- 除 assignee 外，能读取 Task 的 Human 或 Agent Member 可以把`in_review`改为`done`或退回`in_progress`。
- creator、assignee 或有权治理且能读取 Source Thread 的 Human 可以把未结束 Task 改为`closed`。
- `done`和`closed`在 v1 不可重新打开。需要继续工作时创建新的 Root Message 和 Task。

Task 不包含子任务、依赖、优先级、截止时间、评论或单独权限模型。

Review 不使用 Permission、reviewer Role 或 reviewer 绑定。Assignee 根据工作上下文在 review Message 中通知合适的 Human 或 Agent。系统不保存 reviewer 字段，也不自动选择 reviewer。

### 2.1 Task 引用

Message 正文用`!<seq>`引用 Task，例如`!3`。seq 由 Server 在 Task 创建时从 Space 级序列分配，创建后不变。`!<seq>`与`#slug`（Channel）、`#slug:seq`（Channel 内 Thread）、`@display_name`（Member）并列，是四种结构化引用之一。

只有 Server 在发送或投影时解析到当前 Space 中存在的 Task，`!<seq>`才成为结构化 Task Reference。未被识别的正文保持普通文本。Agent 回复、Result 和 Human 正文都使用同一语法。

## 3. 从来源创建 Task

只有 Root Message 可以成为 Task source。Thread reply、Attachment、Inbox Item 和 Run 都不能成为来源。

Agent 在 Run 中发起创建时只提交：

- 可选 `title`
- 可选 `assignee_agent_member_id`
- `idempotency_key`

Server 从当前 Run 的 Focus 推导 Root Message、`space_id`、`channel_id`、`source_thread_id` 和可见范围。Server 在一个事务中：

1. 校验调用 Agent 可以读取 Root Message。
2. 校验 Message 位于 Channel 主时间线。
3. 锁定 Root Message 和 Thread。
4. 创建 Task，并把推导出的 Thread 直接写入`source_thread_id`。
5. 将当前 Run 和它同 Focus 的 Items 绑定到 Task。
6. 写入本地 Session 提升 command、audit 和 outbox。
7. 返回包含 Source Thread 的完整 Task。

事务中的任一步失败时，不得留下 Task 或半绑定 Run。Agent 不调用第二个 bind 命令，也不提交 Message ID 或 Thread ID。

Human 创建的 Task 初始为`todo`。Agent 在普通 Run 中创建 Task 时，Server 同时绑定当前 Run 和当前 Agent；Run 已经是`running`，所以 Task 直接进入`in_progress`。

同一 Root Message 最多创建一个 Task。相同 idempotency key 重试必须返回同一 Task。不同请求并发创建时，一个成功，另一个返回现有 Task 冲突。

Human WebUI 使用同一领域命令。Browser 的“一步创建”与 Agent CLI 不得实现两套事务。

## 4. Linked Threads

Task 创建后可以关联额外 Thread。关联表达“该 Thread 正在讨论同一项工作”，不复制 Message，也不改变 Thread 权限。

Source Thread 直接保存在 Task，不创建 link 行。每个 Related link 包含：

- `task_id`
- `thread_id`
- `linked_by_member_id`
- `linked_at`

Source Thread 不可删除或更换。Related link 可以由 Task assignee 或有权读取两个对象的 Human 添加和移除。

一个 Thread 同时最多关联一个未结束 Task。Task 进入`done`或`closed`后，link 保留为历史。

需要开始另一项工作时，Member 必须发送新的 Root Message，再从新 Thread 创建 Task。

Server 不使用标题相似度、mention、共享 Attachment 或正文分类器自动关联。

UI 可以显示由 Human 或 Agent 明确触发的建议。建议不能产生 link。

### 4.1 可见范围兼容

v1 只允许 Task 关联可见范围兼容的 Threads。兼容表示各 Thread 所属 Channel 的有效成员集合相同。

该限制确保一个 Provider Session 中的 Task 内容可以安全用于任一 Linked Thread。Channel membership 变化导致集合不再相同时：

1. Server 保留历史 link。
2. Task 状态保持不变，并记录可见的 runtime issue。
3. active Run 停止接收新 Item。
4. Computer 关闭该 Task 的 Provider Session。
5. assignee 或 Human 移除不兼容 link，或在统一成员集合后继续 Task。

v1 不通过隐藏部分 links 或按成员生成多个 Task 投影绕过该限制。

## 5. Focus

每个 Run 必须选择一个 Thread 作为 Focus。Run 绑定 Task 后，该 Focus 必须是 Task 的 Linked Thread。Focus 决定：

- 本 Run 默认读取的消息窗口。
- 新 hard Item 是否可以加入 active Run。
- Agent 的普通回复默认发送到哪个 Thread。
- UI 显示 Agent 正在处理的位置。

Focus 不限制 Agent 读取其他已授权 Channel。Agent 读取其他位置不会自动改变 Focus，也不会创建 Task link。

Run 期间不得切换 Focus。Agent 要处理另一个 Focus 时，必须 yield 或完成当前 Run，再由 Server 创建新 Run。

## 6. Result 与协作输出

普通 Agent Message 是对话输出。Task Result 引用一条 Message，不复制正文。进入`done`时，Server 在一个事务中：

1. 在 Source Thread 或当前 Focus 发送结果 Message。
2. 将该 Message ID 写入`result_message_id`。
3. 将 Task 标记为`done`并写入`finished_at`。
4. 处理对应 Inbox Items。
5. 写入 audit 和 outbox。

Result Message 必须保存在 Server。Driver 的 final output、stdout 或 Provider Session 内容不能自动成为 Result。

Task 进入`in_review`时应该发送可审阅的 Message，但不填写`result_message_id`。

除 assignee 外，能读取 Task 的 Human 或 Agent Member 可以将其退回`in_progress`或确认进入`done`。

不需要独立复核时，assignee 可以从`in_progress`直接进入`done`。

Task 进入`done`或`closed`后，系统保留 Source Thread、Linked Thread、Run 和 Result 历史。

Computer 应关闭对应的 Provider Session。关闭失败不能回滚 Task 终态。

`in_review`、`done`和`closed`由同一个事务入口写入，见 [产品基础与系统结构](01-foundations.md)。Agent 从 Run 内提交时，同一事务额外处理该 Run 的 Items 并完成 Run；Human 从 Browser 提交时不持有 Run。两条路径不得各自实现一套终态事务。

## 7. 权限

- 创建 Task 的 Member 必须能读取 source Root Message。
- assignee 必须是同一 Space 的 active Agent，并属于 source Channel。
- 添加 Related Thread 的 Member 必须能读取 Task 的全部现有 Linked Threads 和目标 Thread。
- Task 可见性等于其 Linked Threads 的兼容成员集合。
- Access Level 不自动绕过 private Channel membership。
- Task 不引入独立 ACL。

权限检查发生在 Server。Browser、daemon 和 Agent CLI 的本地检查只用于提前返回可理解错误。

## 8. 删除与编辑

Root Message 成为 Task source 后不得硬删除。作者或 Admin 可以软删除正文，但必须保留来源占位、Message ID、Thread 和 Task。

编辑 source Root Message 不修改 Task title。Task title 是独立事实，只能通过 Task update 修改。

删除 reply 不改变 Task link。归档 Channel 不删除 Task，也不改写 Task 状态。系统阻止该 Task 的新 Run，直到 Channel 恢复或 Task 进入`closed`。

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

### 9.3 Agent 在 reply 触发的 Run 中创建 Task

Agent 显式调用`task create`后，Server 使用 Focus Thread 的 Root Message 作为 source。reply 只触发 Run，不成为 Task source。Agent 不提交或绑定 reply ID。
