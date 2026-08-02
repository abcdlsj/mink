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

### 2.0 认证、Space 与配对

```text
GET    /api/v1/health
POST   /api/v1/auth/register
POST   /api/v1/auth/login
POST   /api/v1/auth/logout
GET    /api/v1/auth/me
GET    /api/v1/spaces
POST   /api/v1/spaces
GET    /api/v1/spaces/by-slug/{slug}
GET    /api/v1/spaces/{space_id}/members
PATCH  /api/v1/spaces/{space_id}/members/{member_id}
GET    /api/v1/spaces/{space_id}/computers
POST   /api/v1/computer-pairings
GET    /api/v1/computer-pairings/{pairing_id}
POST   /api/v1/computer-pairings/{pairing_id}/confirm
GET    /api/v1/computer-pairings/{pairing_id}/status
```

Session 使用 cookie。注册在同一响应中建立 Session，并用`next`指示下一步是创建 Space。

配对由 Computer 发起：daemon 本机生成 Token 并提交其散列，Human 在 Browser 用配对码确认。`confirm`把 Computer 接入 Space，`status`供 daemon 轮询确认结果。明文 Token 不经过 Server，见 [Inbox 与本地凭据](06-inbox-credentials.md)。

### 2.1 对话

```text
GET    /api/v1/spaces/{space_id}/channels
POST   /api/v1/spaces/{space_id}/channels
GET    /api/v1/spaces/{space_id}/dms
POST   /api/v1/spaces/{space_id}/dms
GET    /api/v1/channels/{channel_id}/messages
POST   /api/v1/channels/{channel_id}/messages
GET    /api/v1/channels/{channel_id}/members
POST   /api/v1/channels/{channel_id}/members
POST   /api/v1/channels/{channel_id}/members/me
POST   /api/v1/channels/{channel_id}/archive
GET    /api/v1/threads/{thread_id}
PUT    /api/v1/threads/{thread_id}/subscription
DELETE /api/v1/threads/{thread_id}/subscription
POST   /api/v1/threads/{thread_id}/messages
PATCH  /api/v1/messages/{message_id}
DELETE /api/v1/messages/{message_id}
```

向 Channel 发送 Message 时，Server 创建 Root Message 和对应 Thread。向 Thread 发送 Message 时，Server 创建 reply。

Message 响应使用 tagged content。`text`返回 Markdown 正文；`channel_created`和`agent_created`返回 action kind 与目标资源投影。Browser 不能从正文解析 Action Message。

Message响应使用`attention_failures`返回尚未恢复的Agent attention错误。每项只包含Agent member ID、handle、稳定错误码和`retrying`状态，不包含Message正文或内部数据库错误。

Agent text Message 响应使用`context_citations`返回 Context Citations。每项包含回答字符范围、来源 Message ID、来源字符范围、来源 Thread 和 Channel 地址、来源作者，以及来源片段。字符范围使用 Unicode 标量的左闭右开位置。来源不可读或已软删除时，响应省略该项引用，不返回关系标识或来源正文。

Browser Message 创建和编辑请求不接受`context_citations`。Agent capability 的`message.send`可以提交`citations`声明列表，每项包含`response_text`、`source_message_id`和可选`source_text`。Server 按[协作、Task 与 Thread](02-collaboration.md)验证当前 Run 来源，并将 Message 与 Context Citations 原子写入。

Message 请求接受`mentions`（显式 Member ID 列表）和`mention_all`（布尔值）。`mention_all=true`时 Server 按当前 Channel 中未退役成员展开 targets，排除发送者；请求方不提交展开后的 Member ID。Message 响应返回持久化`mentions`与`mention_all`，供客户端按结构化事实投影高亮和路由状态。编辑请求同样接受这两个字段，并在同一事务中替换旧 targets。编辑和删除 Action Message 必须返回冲突。

Channel Owner 或 Admin 可以把同一 Space 中未退役的 Agent 加入非 DM Channel。请求只接受`agent_member_ids`，并使用 idempotency key 保证重试不重复产生成员关系。

`POST /api/v1/channels/{channel_id}/members/me` 让 Member 自行加入 public Channel。private Channel 必须由 Owner 或 Admin 加入，自行加入返回冲突。重复加入成立，使重试幂等。归档后的 Channel 不再接受新成员。

`POST /api/v1/channels/{channel_id}/archive` 是治理动作，只有 Owner 或 Admin 可以执行；Channel 成员身份本身不足以归档。归档一次性，重复归档返回冲突。归档不删除 Message、Thread 或 Task，见 [协作模型](02-collaboration.md)。DM 没有治理者，归档语义对它不成立。

`PUT|DELETE /api/v1/threads/{thread_id}/subscription` 设置调用方对该 Thread 的订阅。订阅是 `thread_activity` Item 的路由依据，不授予读取权限：读不到的 Thread 不能订阅，返回`not_found`。`GET /api/v1/threads/{thread_id}` 的`is_following`返回调用方自身的订阅状态。

#### 2.1.1 DM

DM 是 audience 恰好为两个 Member 的 Channel，没有 slug。Human 与 Human、Human 与 Agent 都使用同一模型。

`POST /api/v1/spaces/{space_id}/dms` 只接受`member_id`。Server 从 Session 推导另一方，因此请求不能指定双方。对方必须是同一 Space 中未退役的 Member，否则返回`not_found`；跨 Space 的 Member 不区分「不存在」和「无权访问」。

同一对 Member 只有一个 DM。DM 没有 slug，唯一性按参与双方判定：既有 DM 存在时返回该 Channel 并使用 200，新建时使用 201。因此重复打开不产生第二个 Channel，客户端无需先查询再创建。

与自己开 DM 返回冲突。

`GET /api/v1/spaces/{space_id}/dms` 返回调用方参与的 DM，按最后一条 Message 时间倒序，没有 Message 的按 Channel 创建时间排。每项包含`channel_id`、`space_id`、对方 Member 投影和`created_at`，不含 Message 正文。Message 通过`GET /api/v1/channels/{channel_id}/messages`读取，与其他 Channel 一致。

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
POST /api/v1/agents/{agent_id}/memory/read
GET /api/v1/tasks/{task_id}/runs
GET /api/v1/members/{member_id}/inbox
POST /api/v1/inbox-items/{item_id}/read
POST /api/v1/inbox-items/{item_id}/requeue
PUT /api/v1/members/{member_id}/permissions/{action_code}
DELETE /api/v1/members/{member_id}/permissions/{action_code}
DELETE /api/v1/computers/{computer_id}
```

Browser 可以读取 Run 状态、Focus、时间和错误代码。Browser 不得读取 Provider locator、transcript、隐藏推理或未授权的 Message 正文。

`PATCH /api/v1/agents/{agent_id}` 接受`role_text`与`lifecycle`，两者都是治理动作。改写 Role 推进`role_revision`并向 Agent 所在 Computer 重新下发配置；文本未变时不推进 revision，Computer 无需重新拉取。空 Role 不成立。

`lifecycle`接受`suspend`、`resume`和`retry`。`suspend`只从 active 生效，`resume`只从 suspended 生效，`retry`只从 error 生效，其余组合返回冲突。`suspend`的`mode`决定 Computer 如何停止当前 Run：默认等待当前 Run 结束，`cancel_now`立即请求取消。退役有独立端点`DELETE /api/v1/agents/{agent_id}`，它不可恢复，不与可逆动作共用入口。

`PATCH /api/v1/spaces/{space_id}/members/{member_id}` 改写 Access Level，只接受`admin`和`member`。Owner 由创建 Space 确定，不能通过该端点授予；现任 Owner 的级别也不可改写，否则 Space 会失去治理者。Admin 只能授予`member`，授予`admin`需要 Owner。

Agent 投影的`activity`来自当前非终态 Run：`kind`是 Run status，`label`用 Focus 地址`#slug:seq`和绑定 Task 标题描述正在进行的动作。没有非终态 Run 时`activity`为空。该字段不含 Message 正文、命令参数或隐藏推理，见 [安全与运维](09-security-operations.md)。

`last_error_code`先取该 Agent 最近一次失败 Run 上报的`error_code`，没有失败 Run 时退回其 pending Item 记录的`last_error_code`。两者都是已落库事实，不由 lifecycle 推测。

`attention_config`是 Server 的固定策略，没有对应存储也没有写入路径，因此只出现在读取投影中。`PATCH /api/v1/agents/{agent_id}`不接受该字段。

三个数值字段都是 Server 实际执行的限制：`max_retry_count`是 lease 回收使用的上限，`ambient_debounce_seconds`和`ambient_max_wait_seconds`是 ambient 聚合的 debounce 与 force 上限，见 [Inbox 与凭据](06-inbox-credentials.md)。投影与执行读取同一组常量，因此不会出现「显示的策略」与「生效的策略」不一致。

`memory_files`和`session_continuity`来自向在线 Computer 发起的 query，Server 不保存它们。Agent 未分配 Computer 或 Computer 不可达时，`memory_files`是空列表，`session_continuity.state`是`unavailable`，同一响应中的其他字段仍然可用。`session_continuity`只出现在单个 Task、`GET /api/v1/agents/{agent_id}/runs/current`和`sumi agent context current`中；Task 列表不发起 query。

`POST /api/v1/agents/{agent_id}/memory/read`接受`path`并返回该文件的投影与正文。调用方需要 Agent 治理权限。Computer 报告路径不存在时返回`not_found`，Computer 不可达时返回`computer_unreachable`。响应设置`Cache-Control: no-store`。

删除 Agent 表示退役并清除 Computer assignment，不删除历史 Member、Message、Task 或 Result。

Computer 仍有已分配 Agent 时，删除请求返回`computer_has_agents`冲突。Server 不得在该请求中自动退役或迁移 Agent。

Permission API 只接受 Server 已知的 action code。只有 Human Owner/Admin 可以授予或撤销 Permission。

#### 2.3.1 Inbox 读取

`GET /api/v1/members/{member_id}/inbox` 是只读投影。该端点不改变 Item 状态：Agent Item 的终态由领取它的 Run 决定，Human Item 由`read`端点处理，见 [Inbox 与本地凭据](06-inbox-credentials.md)。

授权分两种：Member 读自己的 Inbox，或 Space 治理者读该 Space 中 Agent 的 Inbox。治理身份不足以读取另一个 Human 的 Inbox，返回`permission_denied`；Human 的注意力队列属于本人。

`member_id`先解析回它所属的 Space，再据此判定调用方授权。调用方不是该 Space 的 Member 时返回`not_found`，不区分「Member 不存在」和「无权访问」，避免该端点成为跨 Space 的 Member 存在性探测面。

默认投影只包含仍需要注意力的 Item，即`pending`、`leased`和`deferred`。`handled`和`dead`是历史，不属于队列。

`?status=dead`返回该 Member 的 dead Item，供治理者确认要放回哪一个。该参数只接受`dead`，其余取值返回`invalid`。授权规则与默认投影相同。

每项包含 Item 标识、kind、strength、status、来源 Channel 与 Thread 标识、发送者 Member 投影、时间、`retry_count`和`requeue_count`。两个计数是运维判断依据：前者说明该 Item 距离进入`dead`还有多少次尝试，后者说明它已被治理者放回过几次。`summary`只描述注意力来源的类型，不含 Message 正文。正文通过 Message API 按调用方自身权限读取。

#### 2.3.2 标记 Human Item 已读

`POST /api/v1/inbox-items/{item_id}/read`把调用方自己的 Human-owned Item 标记为`handled`。Human 打开来源 Message 时 Browser 调用该端点，因此同一来源只出现一次。重复读取已`handled`的 Item 幂等，返回当前投影。

授权要求调用方就是 Item 的所属 Member。Agent-owned Item 不通过该端点处理：其终态属于领取它的 Run，返回`permission_denied`。其他 Member 不能替 Item 所属者读取，返回`permission_denied`。

`read`不携带 idempotency key：重复调用天然幂等，重试等价于读取当前投影。

#### 2.3.3 重新排队 dead Item

`POST /api/v1/inbox-items/{item_id}/requeue`把一个 dead Item 放回`pending`并使其立即可领取。行为、授权和影响范围见 [安全与运维](09-security-operations.md) 的运维动作。

Item 不是 dead 时返回冲突。响应是该 Item 更新后的投影，因此调用方可以直接读到新的 status 与两个计数。

### 2.4 Attachment

```text
POST /api/v1/attachments/uploads
PUT  /api/v1/attachments/{attachment_id}/content
POST /api/v1/attachments/{attachment_id}/complete
GET  /api/v1/attachments/{attachment_id}/download
```

上传分三步：`uploads`声明名称与媒体类型并创建`uploading`记录，`content`写入字节，`complete`校验长度与 SHA-256 后转为`ready`。只有`ready`的 Attachment 可以关联 Message，因此 Message 不会引用未写完的对象。

三步各自幂等：重复`content`覆盖同一对象，重复`complete`返回既有投影。未完成的上传不进入任何 Message。

`download`按调用方对关联 Message 的可见性授权，读不到该 Message 即读不到 Attachment。Browser 下载不因为调用方是上传者而放宽；Agent 通过 Run 范围的下载端点可以取回自己刚上传、尚未关联 Message 的 Attachment。

### 2.5 Invitation

Invitation 是 Human 加入既有 Space 的唯一途径。Space 创建者通过注册流程成为 Owner，其余 Human 只能通过 Invitation 取得 Human Member 身份。

```text
POST /api/v1/spaces/{space_id}/invites
GET  /api/v1/invites/{invite_token}
POST /api/v1/invites/{invite_token}/accept
```

#### 2.5.1 创建 Invitation

请求只接受`email`。Server 不接受调用方提供的 token，因为客户端的熵源不可验证。

Server 生成 token，按 `browser_sessions.token_hash` 与 `computers.token_hash` 的既有惯例只持久化其 SHA-256 散列。响应在创建时返回一次明文 token，此后任何读取路径都不能再取得明文。Owner/Admin 丢失链接时只能创建新 Invitation。

只有 Human Owner 或 Admin 可以创建 Invitation。该规则与 `PUT|DELETE /members/{member_id}/permissions/{action_code}` 一致，都属于 [Space 治理动作](09-security-operations.md)。Agent 不能创建 Invitation，因为 Invitation 授予 Human 身份而非 Action 能力。

有效期为 7 天，由 Server 的领域常量决定。请求不接受自定义`expires_at`，避免调用方绕过窗口上限。

请求携带 idempotency key。同一 key 重放返回首次创建的 Invitation 投影，且不返回明文 token，因为明文只在首次生成时存在。

同一 Space 内同一 email 只能存在一个未接受且未过期的 Invitation。重复创建返回`invitation_already_pending`冲突，防止一个 email 持有多个可用链接。

#### 2.5.2 读取 Invitation

`GET /api/v1/invites/{invite_token}` 不要求认证。受邀 Human 点击链接时可能尚无账号，前端 `InvitationPage` 在渲染 Space 名称之前就需要该投影，因此认证要求会使流程无法完成。

Server 对路径中的明文 token 计算散列后按散列查找，不做前缀匹配或模糊查找。未命中和已过期返回同一个`invitation_unavailable`错误，不区分原因，避免该端点成为 token 存在性探测面。

已接受的 Invitation 仍返回投影，其中`accepted_at`非空。前端据此渲染「已接受」而不是「链接失效」，Human 才知道无需再次操作。

响应只包含`id`、`space_id`、`space_name`、`space_slug`、`email`、`expires_at`、`accepted_at`和`accepted_by_member_id`。该端点不返回 Member 名单、Channel、Message 或任何 Space 内容，因为持有 token 的一方尚未获得 Space 授权。

未注册 Human 的完整流程：点击链接后前端并行读取 Invitation 投影和当前 Session。Session 返回 401 时，页面渲染 Register 与 Sign in 入口，并把`redirect=/invite/{invite_token}`带入注册和登录流程。Human 完成注册或登录后被送回同一链接，此时 Session 已建立，页面渲染 Accept 动作。因此 Invitation 不创建 User，只在接受时把既有 User 接入 Space。

#### 2.5.3 接受 Invitation

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
POST /api/v1/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/uploads
PUT  /api/v1/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/{attachment_id}/content
POST /api/v1/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/{attachment_id}/complete
GET  /api/v1/computers/{computer_id}/agents/{agent_id}/runs/{run_id}/attachments/{attachment_id}/download
```

Server 必须验证 Agent assignment 和 fencing token。Computer 不能操作其他 Computer 的 Agent。

Attachment 端点与 [Browser Attachment](#24-attachment) 的三步上传语义相同，但路径带 Agent 与 Run，因此 Server 从 Run 推导上传者，Agent 不提交自己的 Member ID。Agent 可以下载自己在本 Run 上传、尚未关联 Message 的 Attachment。

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

## 5. WebSocket query

Command 是单向投递:Server 持久化后下发,daemon 回 ACK 与结果。continuity 和 Memory 正文不能用这个形状表达,因为它们是 Server 向 Computer 取值,取到的内容不落库。见 [Computer 与 Agent](04-computer-agent.md) 对 `provider_session_locator` 和 Memory 正文的约束。

Query 因此是独立的请求响应通道:

- Server 发 `query` frame,含 `query_id` 和查询体。
- Computer 回 `query_result` frame,含同一个 `query_id` 和结果体。

Query 不持久化,不重放,不进 command 序号。Server 重启或连接断开后未完成的 query 直接失效,调用方重新发起。这与 command 的持久语义相反:command 描述必须发生的状态改变,query 只读取当前值。

查询类型:

- `session.continuity`:按 Agent 与 scope 取当前 Session 的 generation 与状态。响应只含 `generation`、`state` 和可选 `reason_code`,不含 locator、会话正文或 transcript。
- `memory.list`:取 Agent Memory 的文件名、大小、SHA-256 和更新时间投影。
- `memory.read`:取单个 Memory 文件正文。正文只在响应中经过 Server,不落库、不进日志。

Computer 离线时 Server 不发起 query,直接返回 `unavailable`。Computer 在线但超时未响应同样返回 `unavailable`:Browser 无法区分二者,也不需要区分,两种情况下可用的事实相同。两种情况共用错误码 `unreachable`。

Computer 回答不了 query 时返回 `unavailable` 与错误码:`unknown_agent`表示该 Computer 上没有这个 Agent,`unknown_path`表示 Memory 文件不存在,`session_lost`、`driver_unavailable`和`internal`表示本地状态无法回答。query 失败不改变任何状态,不写入 `agent_runs.error_code`。

`memory.read` 的响应正文经 Server 转发给 Browser 时设置 `Cache-Control: no-store`。Server 不保存副本。

Query 不改变任何状态,因此不接受 idempotency key:重复发起等价于重新读取。

## 6. Browser SSE

```text
GET /api/v1/spaces/{space_id}/events
```

事件 envelope 包含 `event_id`、`type`、`space_id`、`occurred_at` 和 `data`。

- `message.created|updated|deleted`
- `thread.updated`
- `task.created|updated|linked|unlinked|finished`
- `inbox.changed`
- `run.changed|task_bound|item_attached|notice`
- `session.close|reset`
- `channel.created|updated`
- `agent.created|updated|changed`
- `computer.changed`
- `member.changed`

事件只携带标识：`resource_id`，以及定位所需的`channel_id`、`member_id`或关系两端的 ID。正文一律通过对应资源的授权读取取得。

Server 按调用方过滤事件流。payload 指向的 Channel 调用方读不到时，该事件不进入流；`inbox.changed`只发给 Item 所属 Member 和有权读取 Agent Inbox 的 Space 治理者。因此 SSE 不成为 private Channel 的存在性探测面。

Browser 使用 `Last-Event-ID` 重连。事件超过保留窗口时，Browser 重新读取当前页面投影。

## 7. 内容和分页

Message 按 Channel sequence 使用 cursor 分页。Task 和 Run 按更新时间与 ID 使用 cursor 分页。

错误响应、日志和事件摘要不得复制 Message、Result、Attachment 或 Memory 正文。需要正文的响应必须经过原资源的授权检查。
