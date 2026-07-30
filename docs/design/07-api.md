# API 与事件

[返回设计索引](../design.md)

## 1. API 原则

- Browser、Computer 和 Agent 使用不同的认证面。
- API 按领域行为命名，不暴露数据库表操作。
- 所有写操作接收 idempotency key。
- Server 从认证信息和资源关系推导 Space、Agent、Task、Focus 和权限。
- SSE 和 WebSocket 只运输事件，不是事实来源。
- 新版本从 `/api/v1` 开始。该版本不兼容旧 `/api/v1` 行为。

## 2. Browser API

### 2.1 对话

```text
GET    /api/v1/spaces/{space_id}/channels
POST   /api/v1/spaces/{space_id}/channels
GET    /api/v1/channels/{channel_id}/messages
POST   /api/v1/channels/{channel_id}/messages
GET    /api/v1/channels/{channel_id}/members
POST   /api/v1/channels/{channel_id}/members
GET    /api/v1/threads/{thread_id}
POST   /api/v1/threads/{thread_id}/messages
PATCH  /api/v1/messages/{message_id}
DELETE /api/v1/messages/{message_id}
```

向 Channel 发送 Message 时，Server 创建 Root Message 和对应 Thread。向 Thread 发送 Message 时，Server 创建 reply。

Message 响应使用 tagged content。`text`返回 Markdown 正文；`channel_created`和`agent_created`返回 action kind 与目标资源投影。Browser 不能从正文解析 Action Message。

Message响应使用`attention_failures`返回尚未恢复的Agent attention错误。每项只包含Agent member ID、handle、稳定错误码和`retrying`状态，不包含Message正文或内部数据库错误。

Message 编辑请求只接受`body_markdown`。编辑和删除 Action Message 必须返回冲突。

Channel Owner 或 Admin 可以把同一 Space 中未退役的 Agent 加入非 DM Channel。请求只接受`agent_member_ids`，并使用 idempotency key 保证重试不重复产生成员关系。

### 2.2 Task

```text
POST   /api/v1/root-messages/{message_id}/task
GET    /api/v1/spaces/{space_id}/tasks
GET    /api/v1/tasks/{task_id}
PATCH  /api/v1/tasks/{task_id}
POST   /api/v1/tasks/{task_id}/threads
DELETE /api/v1/tasks/{task_id}/threads/{thread_id}
POST   /api/v1/tasks/{task_id}/start
POST   /api/v1/tasks/{task_id}/submit-review
POST   /api/v1/tasks/{task_id}/request-changes
POST   /api/v1/tasks/{task_id}/done
POST   /api/v1/tasks/{task_id}/close
POST   /api/v1/tasks/{task_id}/reset-session
```

Task 创建请求不接受 `source_thread_id`、`channel_id` 或 links。Server 从 Root Message 推导 Source Thread。

Task link 请求只接受一个 Related Thread。Source Thread 不能通过 link API 修改。

### 2.3 Agent、Run 与 Inbox

```text
GET /api/v1/spaces/{space_id}/agents
POST /api/v1/spaces/{space_id}/agents
GET /api/v1/agents/{agent_id}
DELETE /api/v1/agents/{agent_id}
GET /api/v1/agents/{agent_id}/runs/current
GET /api/v1/tasks/{task_id}/runs
GET /api/v1/members/{member_id}/inbox
PUT /api/v1/members/{member_id}/permissions/{action_code}
DELETE /api/v1/members/{member_id}/permissions/{action_code}
DELETE /api/v1/computers/{computer_id}
```

Browser 可以读取 Run 状态、Focus、时间和错误代码。Browser 不得读取 Provider locator、transcript、隐藏推理或未授权的 Message 正文。

删除 Agent 表示退役并清除 Computer assignment，不删除历史 Member、Message、Task 或 Result。

Computer 仍有已分配 Agent 时，删除请求返回`computer_has_agents`冲突。Server 不得在该请求中自动退役或迁移 Agent。

Permission API 只接受 Server 已知的 action code。只有 Human Owner/Admin 可以授予或撤销 Permission。

### 2.4 Invitation

Invitation 是 Human 加入既有 Space 的唯一途径。Space 创建者通过注册流程成为 Owner，其余 Human 只能通过 Invitation 取得 Human Member 身份。

```text
POST /api/v1/spaces/{space_id}/invites
GET  /api/v1/invites/{invite_token}
POST /api/v1/invites/{invite_token}/accept
```

#### 2.4.1 创建 Invitation

请求只接受`email`。Server 不接受调用方提供的 token，因为客户端的熵源不可验证。

Server 生成 token，按 `browser_sessions.token_hash` 与 `computers.token_hash` 的既有惯例只持久化其 SHA-256 散列。响应在创建时返回一次明文 token，此后任何读取路径都不能再取得明文。Owner/Admin 丢失链接时只能创建新 Invitation。

只有 Human Owner 或 Admin 可以创建 Invitation。该规则与 `PUT|DELETE /members/{member_id}/permissions/{action_code}` 一致，都属于 [Space 治理动作](09-security-operations.md)。Agent 不能创建 Invitation，因为 Invitation 授予 Human 身份而非 Action 能力。

有效期为 7 天，由 Server 的领域常量决定。请求不接受自定义`expires_at`，避免调用方绕过窗口上限。

请求携带 idempotency key。同一 key 重放返回首次创建的 Invitation 投影，且不返回明文 token，因为明文只在首次生成时存在。

同一 Space 内同一 email 只能存在一个未接受且未过期的 Invitation。重复创建返回`invitation_already_pending`冲突，防止一个 email 持有多个可用链接。

#### 2.4.2 读取 Invitation

`GET /api/v1/invites/{invite_token}` 不要求认证。受邀 Human 点击链接时可能尚无账号，前端 `InvitationPage` 在渲染 Space 名称之前就需要该投影，因此认证要求会使流程无法完成。

Server 对路径中的明文 token 计算散列后按散列查找，不做前缀匹配或模糊查找。未命中和已过期返回同一个`invitation_unavailable`错误，不区分原因，避免该端点成为 token 存在性探测面。

已接受的 Invitation 仍返回投影，其中`accepted_at`非空。前端据此渲染「已接受」而不是「链接失效」，Human 才知道无需再次操作。

响应只包含`id`、`space_id`、`space_name`、`space_slug`、`email`、`expires_at`、`accepted_at`和`accepted_by_member_id`。该端点不返回 Member 名单、Channel、Message 或任何 Space 内容，因为持有 token 的一方尚未获得 Space 授权。

未注册 Human 的完整流程：点击链接后前端并行读取 Invitation 投影和当前 Session。Session 返回 401 时，页面渲染 Register 与 Sign in 入口，并把`redirect=/invite/{invite_token}`带入注册和登录流程。Human 完成注册或登录后被送回同一链接，此时 Session 已建立，页面渲染 Accept 动作。因此 Invitation 不创建 User，只在接受时把既有 User 接入 Space。

#### 2.4.3 接受 Invitation

`POST /api/v1/invites/{invite_token}/accept` 要求 Browser Session。响应返回新建的 Human Member 投影。

接受不携带 idempotency key。token 与 Session 所属 User 共同标识一次接受，重试同一请求返回同一个 Member，因此不需要客户端提供额外的键。Server 用行锁把并发接受串行化。

Server 校验 Session 所属 User 的`email_normalized`与 Invitation 的 email 一致。不一致返回`invitation_email_mismatch`，因为 Invitation 指向一个具体收件人，而非任何持有链接的人。该端点要求 Session，调用方已经证明了自己的身份，因此区分收件人不符是 Human 纠正登录账号所必需的，不构成新的探测面。

已过期的 Invitation 不能接受，返回`invitation_unavailable`。

接受在一个事务内写入 Invitation 的`accepted_at`与`accepted_by_member_id`、新建 `members` 行、新建 `human_members` 行、把新 Member 加入 general Channel 并写入 audit event。新 Member 的`access_level`固定为`member`。提升为 Admin 属于独立的治理动作。

同一 User 已是该 Space 的 Human Member 时，若该 Member 正是本 Invitation 的接受者，则返回同一个 Member，使重试成立。若是通过其他途径已经加入，则返回`already_member`冲突而不新建 Member，该约束由 `human_members` 的 `(space_id, user_id)` 唯一键兜底。

## 3. Computer API

Computer 使用 Computer Token 认证。

```text
GET  /api/v1/computers/{computer_id}/connect
GET  /api/v1/computers/{computer_id}/agents
POST /api/v1/computers/{computer_id}/runs/claim
POST /api/v1/computers/{computer_id}/runs/{run_id}/started
POST /api/v1/computers/{computer_id}/runs/{run_id}/renew
POST /api/v1/computers/{computer_id}/runs/{run_id}/delivery-receipts
POST /api/v1/computers/{computer_id}/runs/{run_id}/result
POST /api/v1/computers/{computer_id}/agent-actions
```

Server 必须验证 Agent assignment 和 fencing token。Computer 不能操作其他 Computer 的 Agent。

`started`、`delivery-receipts`和`result`使用请求中的稳定`event_id`执行幂等重放。

`agent-actions`接收版本化 tagged union。`channel.create`和`agent.create`必须校验对应 Permission，并在领域 Action 事务中创建 Action Message。

## 4. WebSocket command

Server 先持久化 command，再通过 WebSocket 投递。

- `agent.provision`
- `agent.configure`
- `agent.suspend`
- `agent.retire`
- `run.start`
- `run.task_bound`
- `run.attach_item`
- `run.notice`
- `run.stop`
- `session.reset`
- `session.close`

每个 command 具有稳定 ID 和 Computer 内递增序号。daemon 先把 command 写入 SQLite，再返回 ACK。重复 command 必须返回已保存的结果。

`run.start` 包含可选 Task 和 Focus 的结构化快照。`run.attach_item` 包含递增的 delivery sequence。`run.notice` 不改变 Inbox Item lease。

## 5. Browser SSE

```text
GET /api/v1/spaces/{space_id}/events
```

事件 envelope 包含 `event_id`、`type`、`space_id`、`occurred_at` 和 `data`。

- `message.created|updated|deleted`
- `thread.updated`
- `task.created|updated|linked|finished`
- `inbox.changed`
- `run.changed|activity_changed`
- `agent.changed`
- `computer.changed`
- `member.changed`

Browser 使用 `Last-Event-ID` 重连。事件超过保留窗口时，Browser 重新读取当前页面投影。

## 6. 内容和分页

Message 按 Channel sequence 使用 cursor 分页。Task 和 Run 按更新时间与 ID 使用 cursor 分页。

错误响应、日志和事件摘要不得复制 Message、Result、Attachment 或 Memory 正文。需要正文的响应必须经过原资源的授权检查。
