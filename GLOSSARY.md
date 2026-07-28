# Sumi

Sumi 是 Human 与 Agent 以 Member 身份在同一个 Space 中长期协作的系统。以下词汇是产品、设计、代码和 API 共用的规范语言。

## 身份与协作者

**Member**：
属于某个 Space 的协作者；Human 与 Agent 都是 Member，并共享协作与权限模型。
_Avoid_：Participant、Actor、Principal

**Human**：
由人类控制、通过账号进入一个或多个 Space 的 Member。
_Avoid_：User（仅表示登录账号时可用）、真人成员

**Agent**：
具有持续身份、Role 和 Memory，并由一台 Computer 承载的 AI Member；Agent 不等同于 Driver、模型或临时会话。
_Avoid_：Bot、Assistant、Codex Agent、Claude Agent

**Role**：
Agent 在 Space 中承担的职责与行为边界，独立于其 Access Level。
_Avoid_：Mission、Persona、Access Level、系统提示词

**Memory**：
归属于一个 Agent、跨多次 Agent Run 持续存在的个人知识与状态。
_Avoid_：Channel History、Driver Session、Context

## 协作

**Space**：
Members、Channels、Computers 和治理共同归属的协作边界。
_Avoid_：Team、Organization、Workspace、Server

**Channel**：
Space 中一组 Members 共享的长期对话空间；DM 是一种特殊 Channel。
_Avoid_：Room、Group、Conversation

**DM**：
恰好由两个 Members 组成的直接对话 Channel。
_Avoid_：Private Chat、Direct Thread

**Thread**：
从 Channel 主时间线的一条根 Message 展开的讨论支线，继承所属 Channel 的可见性。
_Avoid_：Agent Session、Conversation Session、子 Channel

**Message**：
Member 发布在 Channel 主时间线或 Thread 中的协作内容。
_Avoid_：Prompt、Event、Agent Output

**Attachment**：
由 Member 上传、可附加到 Message 的持久文件。
_Avoid_：File、Blob、Artifact

**Task**：
由 Agent 创建并锚定到一条 Channel 主时间线根 Message 的轻量协作事项；Agent 可领取、分配和流转 Task，Human 在集中页面查看并跳回来源 Message。
_Avoid_：Job、Agent Run、Inbox Item、Workflow

## 运行与注意力

**Computer**：
与 Space 配对、运行 Sumi daemon 并承载本机 Agents 的计算机。
_Avoid_：Node、Worker、Runner、Device

**Computer Token**：
由 Computer 首次配对时在本机生成并长期持有、用于向 Server 证明该 Computer 身份的凭据；离线和重连不改变配对关系，只有删除 Computer 才使其失效。
_Avoid_：Pairing Code、Session Token、API Key、Computer Credential

**Agent Home**：
归属于一个 Agent、保存其 Memory、workspace 和 Driver 私有状态的本地持久边界。
_Avoid_：Workspace、Computer Home、Driver Home

**Driver**：
Agent 当前用于推理和行动的可替换执行能力；更换 Driver 不改变 Agent 的身份、Role 或 Memory。
_Avoid_：Agent Type、Engine、Model

**Agent Run**：
Agent 处理一批已领取 Inbox Items 的一次执行；它是临时活动，不是 Agent 身份或对话历史。
_Avoid_：Agent Session、Job、Task

**Inbox**：
Member 接收待关注信息的持久入口。
_Avoid_：Event Queue、Notification Stream、Prompt Buffer

**Inbox Item**：
Inbox 中一条可被处理、延后或重试的待关注信息。
_Avoid_：Job、Task、Trigger

## 权限与治理

**Access Level**：
Member 在 Space 中的治理级别，取 Owner、Admin 或 Member；它不表示 Agent 的 Role。
_Avoid_：Role、Agent Role

**Permission**：
在 Access Level 之外单独授予 Member 的特定能力。
_Avoid_：Role、Access Level、Capability（仅运行时能力可用）

**Owner**：
对 Space 承担最终控制与恢复责任的唯一 Human Member。
_Avoid_：Super Admin、Creator

**Admin**：
可授予 Human 或 Agent 的 Space 管理 Access Level；部分治理动作仍只允许 Human 执行。
_Avoid_：Agent Admin、Human Admin（仅强调 Human-only 例外时可用）

**Approval**：
具有明确申请人、审批人和结果的治理决定。
_Avoid_：Confirmation Message、Permission Prompt
