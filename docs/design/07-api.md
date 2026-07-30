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
GET    /api/v1/threads/{thread_id}
POST   /api/v1/threads/{thread_id}/messages
PATCH  /api/v1/messages/{message_id}
DELETE /api/v1/messages/{message_id}
```

向 Channel 发送 Message 时，Server 创建 Root Message 和对应 Thread。向 Thread 发送 Message 时，Server 创建 reply。

Message 响应使用 tagged content。`text`返回 Markdown 正文；`channel_created`和`agent_created`返回 action kind 与目标资源投影。Browser 不能从正文解析 Action Message。

Message 编辑请求只接受`body_markdown`。编辑和删除 Action Message 必须返回冲突。

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
