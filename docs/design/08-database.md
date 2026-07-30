# 数据库设计

[返回设计索引](../design.md)

## 1. 设计目标

数据库只保存恢复、并发控制、授权或正式协作需要的事实。一个事实只能有一个权威表示。

设计必须满足以下要求：

- 领域写入在一个事务中完成。
- 可由关联关系推导的数据不重复保存。
- 稳定领域字段使用明确列和约束，不藏在 JSON 中。
- 查询索引和读取投影可以持续优化，不改变写模型。
- 新版本从空数据库建立基线，不迁移旧 schema。
- 基线发布后的 schema 变更使用版本化的前向 migration。

## 2. 原子性

### 2.1 事实原子性

一个字段只表达一个事实。一个关系只由一个外键或关联表表达。

以下数据不单独保存：

- Thread 的 Root Message 映射以 `threads.root_message_id` 为准。
- Task 的 Source 关系以 `tasks.source_thread_id` 为准。
- Result 正文以 `tasks.result_message_id` 指向的 Message 为准。
- Task 的运行中和等待状态从 Run 与 outcome 推导。
- different-Focus notice 从 pending hard Item 推导。
- Session continuity 从在线 Computer 读取。
- Task 状态历史使用 audit event，不建立专用 history 表。

### 2.2 事务原子性

一个领域命令只允许一个 transaction service。该 service 负责锁、约束、写模型、audit 和 outbox。

事务提交后才能发送 SSE、WebSocket command 或外部请求。外部发送失败时，outbox 负责重试。

### 2.3 并发原子性

- 资源创建使用唯一约束和 idempotency key。
- 状态转换锁定聚合根，并校验旧状态。
- Thread 关联操作先锁定 Thread，再检查未结束 Task。
- Run ownership 使用 lease 和 fencing token。
- 一个 Agent 的非终态 Run 使用 partial unique index 保证唯一。

## 3. PostgreSQL 约定

- 业务 ID 使用 UUIDv7。
- 时间使用 `timestamptz`，并由 Server 写入 UTC。
- 稳定状态使用 `text` 和 `CHECK`。状态增加时通过 migration 更新约束。
- 外键默认使用 `RESTRICT`。软删除实体不级联删除历史事实。
- 只有临时传输 payload、Driver 配置和 audit metadata 可以使用 JSON。
- 正文、Secret 和 Provider transcript 不得进入 audit、idempotency 或普通 outbox metadata。
- `space_id`可以在租户数据上重复保存。该字段用于复合外键、授权过滤和租户索引。

## 4. 身份与 Space

### 4.1 `users`

- `id`
- `email_normalized`
- `password_hash`
- `display_name`
- `created_at`
- `disabled_at`

`email_normalized`必须唯一。

### 4.2 `browser_sessions`

- `id`
- `user_id`
- `token_hash`
- `expires_at`
- `last_seen_at`
- `created_at`

只保存 token hash。`token_hash`必须唯一。

### 4.3 `spaces`

- `id`
- `slug`
- `name`
- `owner_member_id`
- `created_at`
- `deleted_at`

`slug`必须全局唯一。Owner 必须是该 Space 中的 Human Member。

### 4.4 `members`

- `id`
- `space_id`
- `kind=human|agent`
- `display_name`
- `handle`
- `access_level=owner|admin|member`
- `created_at`
- `retired_at`

`(space_id, lower(handle))`必须唯一。

### 4.5 `human_members`

- `member_id`
- `user_id`

`member_id`是主键。一个 User 在同一 Space 只能对应一个 Human Member。

### 4.6 `member_permissions`

- `member_id`
- `action_code`
- `granted_by_member_id`
- `created_at`

主键是`(member_id, action_code)`。Permission 只授予一个稳定 Action，不保存 Role、reviewer 或资源可见范围。

### 4.7 `space_invitations`

- `id`
- `space_id`
- `email_normalized`
- `token_hash`
- `status=pending|accepted|expired`
- `expires_at`
- `created_by_member_id`
- `accepted_by_member_id`
- `created_at`
- `accepted_at`

Invitation 是 Human 加入既有 Space 的唯一途径。Space 创建者通过注册成为 Owner，其余 Human 都经由 Invitation 取得 Human Member 身份。

token 由 Server 生成，表中只保存 SHA-256 散列，与`browser_sessions`和`computers`一致。明文只在创建响应中返回一次。`token_hash`必须唯一，使按散列查找是点查。

`email_normalized`按`users.email_normalized`的同一规则规范化，因此接受时可以直接比较收件人与登录 User。

`status`与时间字段的一致性由 CHECK 表达：`accepted`必须同时具有`accepted_at`和`accepted_by_member_id`，`pending`和`expired`两者都必须为空。该约束使「已接受」无法在缺少接受者的情况下存在。

`(space_id, email_normalized)`在`status='pending'`上唯一。同一收件人在同一 Space 只能持有一个可用链接，已接受和已过期的记录保留为历史事实并不占用该唯一性。

`created_by_member_id`和`accepted_by_member_id`都使用`(member_id, space_id)`复合外键，保证两个 Member 都属于该 Space。两者必须是 Human Member，该条件由触发器校验，因为复合外键不能表达`kind`。

过期在读取时判定，不依赖后台任务。`expires_at > created_at`由 CHECK 保证。

见`0003_invitations.sql`。

## 5. 对话

### 5.1 `channels`

- `id`
- `space_id`
- `kind=public|private|direct`
- `slug`
- `topic`
- `next_seq`
- `created_at`
- `archived_at`

非 DM Channel 的`(space_id, slug)`必须唯一。

### 5.2 `channel_members`

- `channel_id`
- `member_id`
- `joined_at`
- `last_read_seq`

主键是`(channel_id, member_id)`。

### 5.3 `messages`

- `id`
- `space_id`
- `channel_id`
- `thread_id`
- `channel_seq`
- `placement=root|reply`
- `content_kind=text|channel_created|agent_created`
- `reply_to_message_id`
- `author_member_id`
- `body_markdown`
- `action_channel_id`
- `action_agent_member_id`
- `created_at`
- `edited_at`
- `deleted_at`

`(channel_id, channel_seq)`必须唯一。Root Message 的`thread_id`等于自身 ID。Reply 必须与目标 Message 属于同一 Thread。

`text`具有`body_markdown`，且不具有 action target。Action Message 的`body_markdown`为空。`channel_created`只具有`action_channel_id`。`agent_created`只具有`action_agent_member_id`。CHECK 必须拒绝其他组合。

Action Message 的`placement`只能是`reply`。该约束避免 Action Message 成为 Thread root 或 Task source。

Action Message 与目标资源在同一领域事务中创建。普通 Message 写入入口只能创建`text`。

### 5.4 `threads`

- `id`
- `space_id`
- `channel_id`
- `root_message_id`
- `created_at`

`id`等于`root_message_id`。`root_message_id`必须唯一。

Message 与 Thread 之间的循环外键使用`DEFERRABLE INITIALLY DEFERRED`。Root Message 和 Thread 因此可以在一个事务中创建。

### 5.5 Attachment

`attachments`保存上传者、名称、媒体类型、长度、hash、对象 key、状态和时间。`message_attachments`只保存 Message 与 Attachment 的关系。

对象内容不进入 PostgreSQL。Message 只能关联同一 Space、状态为 ready 的 Attachment。

## 6. Task

### 6.1 `tasks`

- `id`
- `space_id`
- `title`
- `status=todo|in_progress|in_review|done|closed`
- `source_thread_id`
- `creator_member_id`
- `assignee_agent_member_id`
- `result_message_id`
- `close_reason_code`
- `close_reason_note`
- `created_at`
- `updated_at`
- `finished_at`

`source_thread_id`必须唯一。Task 不重复保存 Root Message ID。

状态约束必须保证：

- `done`具有`result_message_id`和`finished_at`。
- `closed`具有`close_reason_code`和`finished_at`。
- 其他状态不具有 Result、close reason 或`finished_at`。
- `in_progress`和`in_review`具有 assignee。

### 6.2 `task_threads`

- `task_id`
- `thread_id`
- `linked_by_member_id`
- `linked_at`

主键是`(task_id, thread_id)`。该表只保存 Related Thread。Source Thread 不写入该表。

关联操作必须锁定目标 Thread。事务在持锁期间检查该 Thread 是否属于另一个未结束 Task。该规则避免维护第二份“当前 Task”指针。

## 7. Agent、Inbox 与 Run

### 7.1 `agents`

- `member_id`
- `computer_id`
- `role_text`
- `role_revision`
- `lifecycle=active|suspended|retired`
- `driver_kind`
- `driver_config_json`
- `created_at`
- `retired_at`

`member_id`是主键，并引用`kind=agent`的 Member。

`computer_id`对 active 和 suspended Agent 必填。Agent 退役事务将 lifecycle 改为`retired`、填写`retired_at`并清空`computer_id`。

Agent assignment 事务必须拒绝`deleted`Computer。

### 7.2 `inbox_items`

- `id`
- `space_id`
- `agent_id`
- `message_id`
- `thread_id`
- `task_id`
- `kind`
- `strength=hard|ambient`
- `status=pending|leased|deferred|handled|dead`
- `available_at`
- `lease_id`
- `lease_expires_at`
- `retry_count`
- `handled_at`
- `last_error_code`
- `created_at`

Item 不复制 Message 正文。`task_id`是创建或绑定 Task 后确定的路由事实。

### 7.3 `agent_runs`

- `id`
- `space_id`
- `agent_id`
- `task_id`
- `focus_thread_id`
- `status=queued|starting|running|finalizing|completed|yielded|failed|stopping|canceled`
- `fencing_token_hash`
- `lease_expires_at`
- `outcome_code`
- `error_code`
- `continuation_note`
- `created_at`
- `started_at`
- `finished_at`

`task_id`可以为空。Run 绑定 Task 时，Focus 必须是 Source Thread 或 Related Thread。

一个 Agent 只能有一个非终态 Run。该规则使用 partial unique index 表达。

`error_code`保存 Computer 上报的稳定错误码，取值域是`invalid_command`、`agent_unavailable`、`process_lost`、`session_lost`、`sandbox_unavailable`、`driver_unavailable`、`internal`，与协议的`ComputerErrorCode`一致。稳定取值使 Browser 与运维统计可以按错误码归类失败，无需解析文本。

`outcome_code`回答 Run 以哪种终态结束，`error_code`回答失败的机器可读原因。只有`outcome_code='failed'`允许非空`error_code`；`completed`、`yielded`、`canceled`不是失败终态，`error_code`必须为空。该约束由 CHECK 表达，见`0002_run_error_code.sql`。

### 7.4 `thread_subscriptions`

- `thread_id`
- `space_id`
- `member_id`
- `created_at`

订阅是 Member 对单个 Thread 的显式关注，是`thread_activity`Item 的路由依据。主键`(thread_id, member_id)`使重复订阅成为无操作。

订阅不改变 Channel membership，也不授予读取权限。没有 Channel 成员身份的 Member 即使有订阅行也读不到 Thread，因此权限判定不查该表。

两个外键都使用`(id, space_id)`复合形式，保证 Thread 与 Member 属于同一 Space。

见`0004_thread_subscriptions.sql`。

### 7.5 `run_items`

- `run_id`
- `inbox_item_id`
- `delivery_seq`
- `attached_at`
- `disposition`

`(run_id, inbox_item_id)`和`(run_id, delivery_seq)`必须唯一。

## 8. Computer、交付与审计

### 8.1 `computers`

保存 Space、名称、token hash、连接状态、daemon 版本、下一个 command sequence、last seen 和`deleted_at`。Raw Computer Token 不得进入数据库。

Computer 删除是一个软删除事务。事务锁定 Computer 和全部 assigned Agents；仍存在 assignment 时返回冲突，否则填写`deleted_at`并撤销 token hash。

### 8.2 `computer_commands`

- `id`
- `computer_id`
- `computer_seq`
- `kind`
- `payload_json`
- `acked_at`
- `result_event_id`
- `created_at`

`(computer_id, computer_seq)`和`result_event_id`必须唯一。payload 只保存交付所需数据，并按保留策略清理正文。

### 8.3 `outbox_events`

- `id`
- `space_id`
- `kind`
- `payload_json`
- `created_at`
- `published_at`

业务写入和 outbox 写入属于同一事务。

### 8.4 `idempotency_records`

按认证主体、action 和 idempotency key 唯一。记录只保存响应代码、资源 ID 和结果 hash，不保存正文。

### 8.5 `audit_events`

保存 actor、action、subject、时间和安全 metadata。Audit 不承担当前状态查询，也不保存正文。

## 9. Computer SQLite

SQLite 只保存本地恢复需要的状态：

- `local_commands`
- `local_runs`
- `provider_sessions`
- `run_deliveries`
- `result_outbox`
- `schema_meta`

SQLite 启用 foreign keys 和 WAL。一次本地状态转换及其 outbox 写入必须属于同一事务。

Provider Session 保存 Agent、scope、generation、Driver、locator、fingerprint、状态和时间。scope 包含`scope_kind=thread|task`和 scope ID。

SQLite 不得保存 Computer Token 或模型凭据。

## 10. 关键事务

### 10.1 发送 Root Message

1. 锁定 Channel sequence。
2. 创建 Root Message。
3. 创建同 ID 的 Thread。
4. 创建 mention、Attachment 关系和 Inbox Item。
5. 写入 audit 和 outbox。

### 10.2 创建 Task

1. 锁定 Root Message 和 Thread。
2. 验证 Source 和权限。
3. 创建 Task，并写入`source_thread_id`。
4. Agent Run 发起时，绑定 Run 和已领取 Item。
5. 写入`run.task_bound` command、audit 和 outbox。

### 10.3 关联 Thread

1. 锁定 Task 和目标 Thread。
2. 验证权限、可见范围和未结束 Task 唯一性。
3. 创建 Related link。
4. 写入 audit 和 outbox。

### 10.4 Attach hard Item

1. 锁定 Run 和 Item。
2. 验证 Run 仍是 running，且 Task 和 Focus 一致。
3. 租约 Item，并创建`run_items`。
4. 分配 delivery sequence。
5. 写入 Computer command 和 outbox。

### 10.5 完成 Task

1. 创建 Result Message。
2. 将 Result Message ID 写入 Task，并进入 done。
3. 处理本 Run 的 Item。
4. 完成 Run。
5. 写入 Session close command、audit 和 outbox。

Session 关闭失败不能回滚已完成 Task。

## 11. 持续优化

### 11.1 Schema 基线

新版本建立一个干净基线。项目不提供从旧版本到该基线的 migration。

基线进入共享环境后，每次 schema 变更必须新增前向 migration。不得修改已经应用的 migration。

### 11.2 Expand and contract

需要在线变更时，按以下顺序执行：

1. Expand：增加可空列、表或索引。
2. Backfill：使用可重试批次填充数据。
3. Switch：切换单一写路径和读取路径。
4. Contract：删除旧列、旧索引和临时代码。

同一发布阶段不得长期双写两个事实模型。Switch 完成后，旧路径必须在下一阶段删除。

### 11.3 可替换投影

列表计数、搜索、活跃状态和 Session continuity 属于读取投影。投影可以重建、增加索引或迁移到专用存储，但不能接受领域写入。

写模型保持规范化。性能问题先通过查询、索引和投影解决。只有测量证明规范化写模型无法满足目标时，才允许增加受约束的冗余列。

### 11.4 Migration 验证

每个 migration 必须验证：

- 空数据库可以建立完整 schema。
- migration 前后的约束都成立。
- migration 可以在测试数据量下完成。
- backfill 可以中断和重试。
- 新旧应用版本的并行窗口有明确边界。
- rollback 不能安全完成时，提供 forward fix。
