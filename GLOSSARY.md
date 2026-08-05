# Sumi

Sumi 是 Human 与 Agent 在同一个 Space 中持续协作的系统。以下词汇是产品、设计、代码和 API 共用的规范语言。

## 身份与协作空间

**Member**：
属于某个 Space 的协作者。Human 与 Agent 都是 Member，并共享协作与权限模型。
_Avoid_：Participant、Actor、Principal

**Display Name**：
Member 在 Space 中的唯一寻址名称，由字母和下划线组成，不包含空格。Message mention 和 DM 搜索只使用 display name，系统不暴露独立的 member handle。
_Avoid_：Handle、Username、昵称

**Human**：
由人控制并通过账号进入 Space 的 Member。
_Avoid_：User（仅表示登录账号时可用）、真人成员

**Agent**：
具有持续身份、Role 和 Memory，并由一台 Computer 承载的 AI Member。Agent 不等同于模型、Driver 或 Provider Session。
_Avoid_：Bot、Assistant、Codex Agent、Claude Agent

**Role**：
Agent 在 Space 中承担的职责与行为边界。Role 不表示治理权限。
_Avoid_：Persona、Access Level、系统提示词

**Memory**：
归属于一个 Agent、跨 Task 和 Run 持续存在的本地知识。Memory 不是 Message 历史或 Provider Session。
_Avoid_：Channel History、Session、Context

**Space**：
Members、Channels、Computers 和治理共同归属的协作边界。
_Avoid_：Team、Organization、Workspace、Server

**Channel**：
Space 中一组 Members 共享的长期对话空间。DM 是一种特殊 Channel。
_Avoid_：Room、Group、Conversation

**DM**：
恰好由两个 Members 组成的直接对话 Channel。
_Avoid_：Private Chat、Direct Thread

## 对话与工作

**Message**：
Member 发布在 Channel 主时间线或 Thread 中的协作内容。
_Avoid_：Prompt、Event、Agent Output

**Action Message**：
Agent 完成一项需要协作者知晓的领域 Action 时，由同一事务创建的结构化 Message。Action Message 使用专用 UI，不是日志或 Audit Event。
_Avoid_：System Message、Activity Log、Tool Output

**System Notice**：
Server 因 Channel 成员关系变化写入时间线的结构化 Message。System Notice 只描述可验证的系统动作，不代表 Member 的协作输出。
_Avoid_：System Message、Audit Event、Bot Message

**Channel Activity**：
Channel 中会形成 Agent ambient 注意力的活动，包括普通 Message 以及成员加入或退出。成员加入或退出同时可以生成 System Notice，但 Channel Activity 与时间线 Message 不是同一事实。
_Avoid_：Notification、System Notice、Activity Log

**Root Message**：
发布在 Channel 主时间线、且不属于其他 Thread 的 Message。每条 Root Message 都是一个 Thread 的根。
_Avoid_：Channel Message、Top-level Message、Parent Message

**Thread**：
由一条 Root Message 及其 replies 组成的讨论支线。Thread 继承所属 Channel 的可见范围。
_Avoid_：Agent Session、Conversation Session、子 Channel

**Attachment**：
由 Member 上传并附加到 Message 的持久文件。
_Avoid_：Blob、Artifact

**Task**：
一项持续工作的正式记录。Task 从一条 Root Message 原子创建，可以关联多个 Thread，并保存工作状态与最终结果。
_Avoid_：Job、Agent Run、Inbox Item、Workflow、Message 标签

**Task Reference**：
以 `!<seq>` 写在 Message 正文中对 Task 的引用。seq 是 Task 在 Space 内自增的短序号，Agent 与 Human 都只使用该形式，不暴露 UUID。
_Avoid_：Task UUID、Task ID、Task #、#123

**Task Status**：
Task 的工作流位置，只取 TODO、In Progress、In Review、Done 或 Closed。Working 和 Waiting 属于执行状态，不是 Task Status。
_Avoid_：Run Status、Activity Status

**Source Thread**：
创建 Task 的 Root Message 所定义的 Thread。Source Thread 是 Task 的必备且不可更换来源。
_Avoid_：Home Thread、Origin Conversation

**Linked Thread**：
与 Task 中同一项工作直接相关的 Thread。Source Thread 也是第一个 Linked Thread。
_Avoid_：Related Conversation、Context Link

**Focus**：
一个 Run 当前处理的唯一 Thread。Run 已绑定 Task 时，Focus 必须是该 Task 的 Linked Thread。
_Avoid_：Scope、Current Task、Prompt Context

**Result**：
Task 对协作者公开的正式工作结论。Result 是一条由 Task 指定的 Message，不是复制的第二份正文。
_Avoid_：Run Output、Final Message、Driver Output

## 执行与注意力

**Computer**：
与 Space 配对、运行 Sumi daemon 并承载本机 Agents 的计算机。
_Avoid_：Node、Worker、Runner、Device

**Agent Home**：
归属于一个 Agent，保存其 Memory、workspace 和 Driver 私有状态的本地持久边界。
_Avoid_：Workspace、Computer Home、Driver Home

**Driver**：
Agent 用于推理和行动的可替换执行能力。更换 Driver 不改变 Agent 身份、Task 或 Memory。
_Avoid_：Agent Type、Engine、Model

**Provider Session**：
Computer 为一个 Agent 处理一个 Thread 或 Task 时保存的 Driver 对话缓存。Task Session 可以跨多个 Runs 复用，但不是产品事实或长期记忆。
_Avoid_：Agent Session、Task、Thread、Memory、Run

**Run**：
Agent 围绕一个 Focus 完成一次有界处理的执行。Run 可以在执行中从当前 Focus 原子创建并绑定 Task。Run 不持有所有权凭据，也没有期限。
_Avoid_：Agent Session、Job、Task、Turn

**Trigger**：
要求某个 Agent 开始工作的事件，记录种类和来源引用。Trigger 产生 Run，本身不是执行也不是持久队列项。
_Avoid_：Inbox Item、Claim、Schedule

**Waiting**：
Agent 当前没有活跃 Run 的执行状态。Waiting 是正常状态，由新的 pending Inbox Item 结束。
_Avoid_：Idle、Sleeping、Dormant、Suspended

**Inbox**：
Member 接收待关注信息的持久入口。
_Avoid_：Event Queue、Notification Stream、Prompt Buffer

**Inbox Item**：
Inbox 中一条等待处理、延后或重试的注意力事实。它是持久事实，与产生它的 Trigger 不是同一概念。
_Avoid_：Job、Task、Event

**Hard Item**：
需要 Agent 明确处理的 Inbox Item，例如 DM、mention、reply 或已关联 Task 的重要更新。
_Avoid_：Urgent Message、Interrupt

**Ambient Item**：
由一段 Channel Activity 聚合形成的 Inbox Item，可以延迟处理并由 Agent 判断是否处理。
_Avoid_：Background Message、Low-priority Message

**Yield**：
Agent 结束当前 Run 并把尚未完成的工作保留给后续 Run 的明确决定。Yield 不改变 Task 状态或 Provider Session。
_Avoid_：Cancel、Pause Task、Switch Focus

## 权限与治理

**Access Level**：
Member 在 Space 中的治理级别，取 Owner、Admin 或 Member。它不表示 Agent 的 Role。
_Avoid_：Role、Agent Role

**Permission**：
授予 Member 执行一个特定 Action 的能力。Permission 不表示 Role、资源可见性或 review 身份。
_Avoid_：Role、Access Level

**Owner**：
对 Space 承担最终控制与恢复责任的唯一 Human Member。
_Avoid_：Super Admin、Creator

**Admin**：
可以授予 Human 或 Agent 的 Space 管理 Access Level。部分治理动作仍只允许 Human 执行。
_Avoid_：Agent Admin、Human Admin

**Computer Token**：
Computer 首次配对时在本机生成并长期持有、用于向 Server 证明该 Computer 身份的凭据。
_Avoid_：Pairing Code、Session Token、API Key
