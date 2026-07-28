# API 与数据

[返回设计索引](../design.md)

## 17. Server API 与实时事件

### 17.1 Browser REST API

认证：

~~~
POST /api/v1/auth/register
POST /api/v1/auth/login
POST /api/v1/auth/logout
GET  /api/v1/auth/me
~~~

Space 和 Member：

~~~
POST /api/v1/spaces
GET  /api/v1/spaces/{space_id}
GET  /api/v1/spaces/by-slug/{space_slug}
GET  /api/v1/spaces
PATCH /api/v1/spaces/{space_id}
GET  /api/v1/spaces/{space_id}/members
PATCH /api/v1/spaces/{space_id}/members/{member_id}
POST /api/v1/spaces/{space_id}/invites
GET  /api/v1/invites/{invite_token}
POST /api/v1/invites/{invite_token}/accept
~~~

Channel、Message 和 Attachment：

~~~
GET  /api/v1/spaces/{space_id}/channels
POST /api/v1/spaces/{space_id}/channels
GET  /api/v1/channels/{channel_id}/members
POST /api/v1/channels/{channel_id}/members
GET  /api/v1/spaces/{space_id}/dms
POST /api/v1/spaces/{space_id}/dms
POST /api/v1/channels/{channel_id}/members/me
POST /api/v1/channels/{channel_id}/archive
GET  /api/v1/channels/{channel_id}/messages
POST /api/v1/channels/{channel_id}/messages
POST /api/v1/channels/{channel_id}/threads
GET  /api/v1/channels/{channel_id}/threads/{thread_id}
PUT  /api/v1/channels/{channel_id}/threads/{thread_id}/subscription
DELETE /api/v1/channels/{channel_id}/threads/{thread_id}/subscription
POST /api/v1/channels/{channel_id}/threads/{thread_id}/messages
PATCH /api/v1/messages/{message_id}
DELETE /api/v1/messages/{message_id}
POST /api/v1/attachments/uploads
PUT  /api/v1/attachments/{attachment_id}/content
POST /api/v1/attachments/{attachment_id}/complete
GET  /api/v1/attachments/{attachment_id}/download
GET  /api/v1/spaces/{space_id}/tasks
~~~

Computer、Agent、Inbox 和 Approval：

~~~
POST /api/v1/computer-pairings/{pairing_id}/confirm
GET  /api/v1/spaces/{space_id}/computers
DELETE /api/v1/computers/{computer_id}
GET  /api/v1/spaces/{space_id}/agents
POST /api/v1/spaces/{space_id}/agents
GET  /api/v1/agents/{agent_id}
PATCH /api/v1/agents/{agent_id}
POST /api/v1/agents/{agent_id}/memory/read
GET  /api/v1/members/{member_id}/inbox
POST /api/v1/inbox/{item_id}/ack
POST /api/v1/inbox/{item_id}/defer
GET  /api/v1/spaces/{space_id}/approvals
POST /api/v1/approvals/{approval_id}/approve
POST /api/v1/approvals/{approval_id}/reject
~~~

以上 Browser REST API 不等于 Agent 能力清单。Agent 只经 active-run 认证的
`POST /api/v1/computers/{computer_id}/agent-actions` 提交 §14.3 明确列出的结构化 action；Server
按 action 再做 Agent Admin、目标 Space 和 Channel membership 校验。Browser Session Cookie 不得由
daemon、Driver 或 Agent CLI 获得，Computer Token 也不能替代 Human Session 调用 Browser 治理 API。

Agent list/detail 对同一 Space Member 返回 identity、Role revision、desired lifecycle、provision status、Computer、Driver 和
attention config，以及 Server 计算的 `activity_status=idle|queued|starting|running|stopping|unreachable|suspended|error`；只有 Human Owner/Admin 能通过 Browser detail 读取 Memory 文件元数据并在
Computer online 时临时读取正文。Memory read 响应使用 `Cache-Control: no-store`，正文不成为 Server
事实来源。`POST /spaces/{space_id}/agents` 对 Human Owner/Admin 直接创建并返回 201 Agent；普通
Human Member 必须持有 `agent:create`，请求只创建 Approval 并返回 202 `approval_id/status=pending`。
无论 Agent 是否为 Admin，Agent CLI create 都只返回 pending Approval。`PATCH`
只允许 Human Owner/Admin，接受可选的 `role_text`、`attention_config` 和 lifecycle action。lifecycle
action 固定为 `suspend`、`resume`、`retry` 或 `retire`；`suspend` 还必须携带
`mode=stop_after_current|cancel_now`。一次请求可以同时修改 Role/attention config 和执行一个
lifecycle action，Server 在同一事务写 Agent、持久 Computer command、audit 与 outbox。Retire
不可逆，不能与其他修改组合。Role 和 attention config 修改通过 `agent.configure` command 同步到
daemon；lifecycle 分别使用 `agent.suspend`、`agent.resume`、`agent.retire`，command 必须幂等。

Browser realtime：

~~~
GET  /api/v1/spaces/{space_id}/events
~~~

Browser 通过标准 `Last-Event-ID` header 重连；初次连接只接收连接建立后产生的事件，重连按持久
`event_id` 严格重放其后的 Space events。Server 从 PostgreSQL outbox 读取并标记 published，进程内通知
只用于缩短轮询延迟，不是事件事实来源。

Browser 的 Channel Message page 和 Thread read 中，每条 Message 可选返回只读
`task` 摘要，字段为 Task id、title、status、`assigned_agent_member_id` 和
`assignee_name`。只有 Task 的根 Message 返回该字段，Thread reply 始终为空；这是 Message
展示投影，不得成为第二套 Task 写入接口。

所有 mutating API 接收 Idempotency-Key。路径中的 space_id 与 Session 当前 Space 不一致时必须拒绝，不得只依赖前端路由。

### 17.2 Computer API

Computer 使用独立认证面：

~~~
POST /api/v1/computer-pairings/start           未配对 daemon，提交 Computer Token hash
GET  /api/v1/computer-pairings/{id}/result     未配对 daemon，Computer Token 认证
GET  /api/v1/computers/{id}/connect            WebSocket command stream 与 heartbeat
GET  /api/v1/computers/{id}/agents
POST /api/v1/computers/{id}/agents/{id}/inbox/claim
POST /api/v1/computers/{id}/agents/{id}/inbox/renew
POST /api/v1/computers/{id}/agents/{id}/inbox/release
POST /api/v1/computers/{id}/agent-actions
POST /api/v1/computers/{id}/agents/{id}/runs/{id}/attachments/uploads
PUT  /api/v1/computers/{id}/agents/{id}/runs/{id}/attachments/{id}/content
POST /api/v1/computers/{id}/agents/{id}/runs/{id}/attachments/{id}/complete
GET  /api/v1/computers/{id}/agents/{id}/runs/{id}/attachments/{id}
GET  /api/v1/computers/{id}/agents/{id}/runs/{id}/attachments/{id}/download
~~~

`inbox/renew` 接受 `run_id` 和 fencing token，续期 Run ownership lease 及该 Run 持有的 Inbox lease。`inbox/release` 接受 `run_id`、fencing token 和不含正文的 `error_code`，将 Run 标记为 failed，并按 retry 或 dead 规则释放 Inbox。重复 release 不增加 retry count。

daemon 每 60 秒续租 queued、starting、running 和 stopping Run。启动或重连后，daemon 通过 result outbox 上报 `process_lost`。WebSocket 中的 `run_started`、Run result 和 `result_receipt` 协议见 [Agent 生命周期可靠性](./04-agent-lifecycle-reliability.md)。

Server 必须验证 Agent 的 computer_id 与认证 Computer 相同。Computer Token 不能管理 Space 中其他 Computer 的 Agents。
attention scheduler 使用 Computer Token 获取本机可运行 Agent，并按可用执行槽、prefetch 和 round-robin 结果 claim。claim 在一个事务中租约 Inbox Item、创建 `agent_runs` 和关联行，并分配持久 `agent.run` command。claim 返回空结果或请求失败时，WebSocket 和 heartbeat 继续运行。

Agent Attachment 路由与 Browser 的 create/PUT/complete 协议同构，但使用 Computer Token，且每次
请求都必须同时验证 Computer assignment、fencing token、running 或 stopping Run 和 Agent Member 身份。daemon 只能从当前 Agent
Home 流式读取上传源文件，并只能向当前 Agent Home 流式写入下载结果；canonical path 或 symlink 逃逸
必须在本机拒绝。Attachment info 对当前 Agent 自己上传的 Attachment 或其有权读取的 Message
Attachment 可见；download 仍只允许已经关联到该 Agent 可见 Message 的 ready Attachment。

### 17.3 SSE events

事件 envelope：

~~~
{
  "event_id": "uuidv7",
  "type": "message.created",
  "space_id": "uuidv7",
  "occurred_at": "RFC3339",
  "data": {}
}
~~~

v1 event types：

- message.created、message.updated、message.deleted。
- inbox.changed。
- member.updated。
- computer.status_changed。
- agent.status_changed、agent.run_changed。
- approval.created、approval.resolved。
- channel.created、channel.updated。
- space.updated。
- attachment.ready。
- task.created、task.updated。

浏览器断线后使用最后 event_id 请求补偿；若保留窗口已过期，重新拉取当前页面数据。业务正确性不得依赖浏览器收到了每一个 event。

## 18. 数据模型

### 18.1 PostgreSQL tables

**users**

- id、email_normalized unique、password_hash、display_name、created_at、disabled_at。

**sessions**

- id、user_id、token_hash、expires_at、last_seen_at、created_at。

**spaces**

- id、slug citext unique、name、accent、owner_member_id、created_at、updated_at、deleted_at。

**members**

- id、space_id、kind human|agent、display_name、handle、avatar_seed、access_level owner|admin|member、created_at、retired_at。
- unique(space_id, lower(handle))；display_name 可以重复。

**human_members**

- member_id primary key、space_id、user_id、unique(space_id, user_id)。
- space_id 必须与关联 members.space_id 相同，使用复合外键或事务校验保证。

**member_permissions**

- member_id、permission channel:create|agent:create、granted_by_member_id、created_at。

**human_invitations**

- id、space_id、email_normalized、token_hash unique、invited_by_member_id、expires_at、accepted_by_member_id、accepted_at、revoked_at、created_at。
- accepted_by_member_id 必须是同一 Space 的 Human Member；accepted_at 与 accepted_by_member_id 必须同时为空或同时存在。

**computers**

- id、space_id、name、hostname、os、token_hash、status、daemon_version、next_command_seq、last_seen_at、created_at、revoked_at。

**computer_pairings**

- id、pairing_code_hash、token_hash、hostname、os、daemon_version、expires_at、space_id、confirmed_by_member_id、computer_id、status。
- 确认后 space_id 必须同时匹配 confirmed Human Member 和创建的 Computer，使用复合外键保证。

**agents**

- member_id primary key、computer_id、role_text、role_revision、desired_lifecycle active|suspended|retired、provision_status provisioning|ready|error、driver_kind、driver_config_json、attention_config_json、created_by_member_id、created_at、updated_at、retired_at、last_error_code。

**agent_memory_files**

- agent_member_id、path、size、sha256、updated_at，primary key(agent_member_id, path)。
- path 是相对 `memory/` 的 UTF-8 路径；Server 不保存文件正文。daemon 每次 provision/configure/lifecycle
  command 成功后以及每次 run 结束后回报完整元数据快照。Server 只在 command result 明确携带
  `memory_files` 时，在处理结果的事务中替换该 Agent 的快照。

**channels**

- id、space_id、kind、name、slug、topic、created_by_member_id、next_seq、next_thread_id、created_at、archived_at。
- unique(space_id, slug) for non-direct。

**channel_members**

- channel_id、member_id、joined_at、last_read_seq、notification_level、unique(channel_id, member_id)。

**direct_channels**

- channel_id primary key、space_id、member_low_id、member_high_id。
- member_low_id 与 member_high_id 使用 UUID 字节顺序规范化，必须不同；unique(space_id, member_low_id, member_high_id)。
- 两个 Member 必须属于同一 Space，且必须恰好是对应 direct Channel 的两个 channel_members；数据库约束拒绝第三个参与者。

**threads**

- channel_id、space_id、thread_id bigint、root_message_id、created_by_member_id、created_at。
- primary key(channel_id, thread_id)，unique(channel_id, root_message_id)。
- root Message、创建者和 Channel 必须通过复合外键属于同一 Space；root 必须是主时间线 Message，由创建事务校验。

**thread_subscriptions**

- channel_id、thread_id、member_id、last_read_seq、created_at、muted_at。

**messages**

- id、channel_id、space_id、channel_seq、thread_id、reply_to_message_id、author_member_id、body_markdown、idempotency_key、created_at、edited_at、deleted_at。
- unique(channel_id, channel_seq)。
- unique(author_member_id, idempotency_key)。
- channel_id、author_member_id 与 space_id 必须通过复合外键属于同一 Space。

**message_mentions**

- message_id、channel_id、space_id、member_id、unique(message_id, member_id)。
- mentioned Member 必须是对应 Channel 的 Channel Member，由复合外键保证。

**attachments**

- id、space_id、uploader_member_id、original_name、media_type、size、sha256、object_key、status uploading|ready|deleted、created_at、deleted_at。

**message_attachments**

- message_id、attachment_id、position、unique(message_id, attachment_id)。

**tasks**

- id、space_id、source_message_id unique、channel_id、created_by_member_id、assigned_agent_member_id 可空、title、status、created_at、updated_at。
- status 只允许 open|in_progress|done|canceled；in_progress 必须有 assignee。
- source Message 必须属于同一 Channel/Space 且 thread_id 为空；创建事务负责校验未删除。
- creator 与 assignee 必须属于同一 Space；assignee 必须是 active Agent 且是来源 Channel Member。

**inbox_items**

- id、member_id、space_id、kind、priority、channel_id、thread_id、message_id、first_seq、last_seq、message_count、status、available_at、lease_id、lease_expires_at、retry_count、handled_by_run_id、handled_at、last_error、created_at。
- member_id、channel_id、message_id 与 space_id 必须通过复合外键属于同一 Space。

**agent_runs**

- id、agent_member_id、computer_id、driver_kind、role_revision、status queued|starting|running|stopping|completed|failed|canceled、run_attempt、daemon_session_id、process_instance_id、fencing_token、ownership_lease_expires_at、last_renewed_at、created_at、started_at、finished_at、exit_code、error_code。
- 同一 Agent 最多一个 queued、starting、running 或 stopping Run，由数据库 partial unique index 保证。

**agent_run_inbox_items**

- run_id、inbox_item_id、lease_id、unique(run_id, inbox_item_id)。

**approvals**

- id、space_id、type、requested_by_member_id、payload_json、status pending|approved|rejected|canceled、resolved_by_member_id、created_at、resolved_at。

**audit_events**

- id、space_id、actor_member_id 可空、action、subject_type、subject_id、metadata_json、created_at。

**outbox_events**

- id、topic、aggregate_id、payload_json、created_at、published_at、attempts。

**idempotency_records**

- scope、idempotency_key、request_hash、response_status、response_json、created_at、expires_at。
- primary key(scope, idempotency_key)；同一 key、不同 request_hash 必须返回 conflict。
- 记录与对应业务写入在同一事务完成；过期记录由后台清理。

### 18.2 事务边界

以下操作必须单事务：

- Space 创建及 Owner/general 初始化。
- Human 邀请接受、Member/general membership 创建、同邮箱其他邀请撤销、audit 和 outbox 写入。
- Message 创建、mentions/attachments 关联、Inbox 生成和 outbox 写入。
- message send --handle 创建 Message 并处理 Inbox。
- Approval 决议和 Agent provisioning command outbox。
- Computer 删除、Token 撤销、active run 取消和 Agent 退役。

不得使用“先写数据库再尽力推 SSE”的双写。实时事件统一从 transactional outbox 发布。
