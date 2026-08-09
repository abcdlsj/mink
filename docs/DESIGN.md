# DESIGN

Sumi 让 Human 与 Agent 在同一个 Space 中持续协作。Agent 是持续在线、可被新消息打断或转向的协作者；Agent 身份、Message、Task、Result 和 Memory 在 Run、Provider Session、Computer 生命周期之外持续存在。

本文件定义产品要求。系统要求见 `SYSTEM_DESIGN.md`，界面要求见 `UI_DESIGN.md`。代码不是事实来源；实现与要求冲突时必须修改实现，禁止用兼容层保留两套行为。

## 领域词汇

以下词汇是代码、协议、API 和 UI 共用的规范语言。使用 _Avoid_ 中的词会引入第二套概念。

| 词 | 定义 | Avoid |
| --- | --- | --- |
| Member | 属于一个 Space 的协作者，Human 与 Agent 共用该模型 | Participant、Actor |
| Display Name | Member 在 Space 中的唯一寻址名称，由字母和下划线组成，不含空格 | Handle、Username |
| Human | 由人控制并通过账号进入 Space 的 Member | User |
| Agent | 具有持续身份、Role、Memory，并由一台 Computer 承载的 AI Member | Bot、Assistant |
| Role | Agent 在 Space 中承担的职责边界，不表示治理权限 | Persona |
| Memory | 归属于一个 Agent、跨 Task 和 Run 持续存在的本地知识 | Session、Context |
| Space | Members、Channels、Computers 和治理共同归属的协作边界 | Team、Workspace |
| Channel | Space 中一组 Members 共享的长期对话空间，DM 是特殊 Channel | Room、Group |
| DM | 恰好由两个 Members 组成的直接对话 Channel | Private Chat |
| Message | Member 发布在 Channel 主时间线或 Thread 中的协作内容 | Prompt、Event |
| Root Message | 发布在 Channel 主时间线的 Message，每条 Root Message 是一个 Thread 的根 | Top-level Message |
| Thread | 一条 Root Message 及其 replies 组成的讨论支线，继承 Channel 可见范围 | Session、Conversation |
| Attachment | 由 Member 上传并附加到 Message 的持久文件 | Blob |
| Action Message | Agent 完成领域 Action 时由同一事务创建的结构化 Message | System Message、Audit Event |
| System Notice | Server 因 Channel 成员关系变化写入时间线的结构化 Message | System Message |
| Task | 从一条 Root Message 原子创建的持续工作记录，可关联多个 Thread | Job、Workflow |
| Task Reference | 以 `!<seq>` 写在 Message 正文中的 Task 引用，seq 是 Space 内自增短序号 | Task UUID、Task ID |
| Task Status | Task 的工作流位置，只取 TODO、In Progress、In Review、Done、Closed | Run Status |
| Source Thread | 创建 Task 的 Root Message 所定义的 Thread，必备且不可更换 | Origin Conversation |
| Linked Thread | 与 Task 中同一项工作直接相关的 Thread，Source Thread 是第一个 | Related Conversation |
| Focus | 一个 Run 当前处理的唯一 Thread；Run 绑定 Task 时必须是 Linked Thread | Scope、Current Task |
| Result | Task 对协作者公开的正式工作结论，是一条由 Task 指定的 Message | Run Output |
| Computer | 与 Space 配对、运行 Sumi daemon 并承载本机 Agents 的计算机 | Node、Worker |
| Agent Home | 归属于一个 Agent，保存 Memory、workspace 和 Driver 私有状态的本地边界 | Workspace、Computer Home |
| Driver | Agent 用于推理和行动的可替换执行能力 | Engine、Model |
| Provider Session | Computer 为一个 Agent 处理一个 Thread 或 Task 时保存的 Driver 对话缓存 | Agent Session |
| Run | Agent 围绕一个 Focus 完成一次有界处理的执行 | Job、Turn |
| Trigger | 要求某个 Agent 开始工作的事件，产生 Run，本身不是队列项 | Inbox Item、Claim |
| Waiting | Agent 没有活跃 Run 的执行状态，是正常状态 | Idle、Sleeping |
| Inbox | Member 接收待关注信息的持久入口 | Event Queue、Notification Stream |
| Inbox Item | Inbox 中一条等待处理、延后或重试的注意力事实 | Job、Event |
| Hard Item | 需要 Agent 明确处理的 Inbox Item，例如 DM、mention、reply | Urgent Message |
| Ambient Item | 由一段 Channel Activity 聚合形成的 Inbox Item，可以延迟处理 | Background Message |
| Yield | Agent 结束当前 Run 并保留未完成工作的明确决定，不改变 Task 状态 | Cancel、Pause |
| Access Level | Member 在 Space 中的治理级别，取 Owner、Admin、Member | Role、Agent Role |
| Permission | 授予 Member 执行一个特定 Action 的能力 | Role、Access Level |
| Owner | 对 Space 承担最终控制与恢复责任的唯一 Human Member | Super Admin |
| Admin | 可以授予 Human 或 Agent 的 Space 管理 Access Level | Agent Admin |
| Computer Token | Computer 首次配对时生成并长期持有、用于向 Server 证明身份的凭据 | API Key |
| Driver Token | daemon 为每个 Agent 从内存 secret 派生的本地 capability 凭据 | API Key |

## 产品不变量

- Human 与 Agent 使用同一套 Space、Channel、DM、Thread、Message、Attachment 模型。
- Task 必须从 Root Message 原子创建，Source Thread 绑定后不可更换；Thread reply 不能成为 Source。
- 一个 Task 只能有一个 Source Thread，Source Thread 在 Task 结束后仍不可被其他 Task 作为 Source 复用；一个 Task 可以关联多个 Linked Thread；一个 Run 只处理一个 Focus；一个 Agent 同时最多有一个 active Run。
- Task 创建与开始执行分离：绑定当前 Run 时创建即 In Progress；未绑定当前 Run 时保持 TODO，由 pending hard TaskActivity Item 驱动的首个 Run 进入 working 后推进为 In Progress。
- Task 创建后 title 与 assignee 不可修改；对 Task 的操作只有状态流转（TODO → In Progress → In Review → Done / Closed）与 Close（记录原因）。
- Run 有界、无期限、不持有执行凭据；Server 不因时间改变 Run 状态；失败只由 Computer 上报。
- Provider Session 是 Computer 本地缓存，丢失或更换 Driver 不影响 Task、Message、Result、Inbox。
- Server 与 Computer 只通过版本化协议交换命令、快照、回执和查询。
- 路由和归属来自结构化事实（mention targets、Task link、Thread 订阅、Item strength），不解析 Message 正文。
- Agent 发布的普通 Message 不触发其他 Agent 的 ambient；mention、reply、DM 和 Task Activity 等显式 hard 路由仍然有效。
- 每个事实只有一个写入入口，一个领域命令只在一个事务中完成。
- Agent 不负责补写可由系统从请求上下文推导的关系。
- Agent 对需要跨 Run 长时间跟踪状态的工作必须创建 Task，并持续用 Source Thread、Linked Threads 和状态流转记录流程与进展；单次回复或同一 Run 内完成的工作不创建 Task。
- Channel 的 `channel_seq` 是该 Channel 内所有 Message（Root、reply、Action Message 和 System Notice）共用的唯一递增坐标。Thread 保持独立的 `thread_id`，由 Root Message 建立并包含该 Root 及其 replies；Channel 主时间线只展示 Root，Thread 视图展示该 Thread 的完整内容。`channel_seq` 是引用坐标，不是当前列表行号。
- Agent Run 的动态 Channel Activity 按 Agent + Channel 以 `through_seq` 做增量摄入；当前 Focus Thread 和 claimed Hard Item 保留原文，已摄入的普通活动不得在后续 Run 中重复追加。只有 Completed 或 Yielded Run 推进 `through_seq`；Failed 或 Canceled Run 保留待摄入活动。
- 运行中到达的 Hard Item 必须获得明确 delivery outcome。Builtin Driver 在模型调用、工具批次和最终完成屏障之间通过有序 mailbox 接收新 Item；返回 Accepted 后该 Item 必须进入当前 Run，否则返回 TooLate。TooLate 或 Unsupported 不使原 Run Failed，并自动释放该 Item。

## 范围边界

不做：

- 子任务、Task 依赖、工时、截止时间、优先级、审批流。
- 旧数据迁移、旧 API 兼容、Windows 运行支持。

## 验收底线

- 空 PostgreSQL 与空 Computer 本地状态能完成核心流程。
