# 安全、可靠性与运维

[返回设计索引](../design.md)

## 19. 安全与可靠性

### 19.1 授权

每次读写都按以下顺序校验：

1. 认证身份有效。
2. 身份对应 Space Member 或 Computer。
3. 目标资源属于同一 Space。
4. Channel membership/visibility。
5. Space access level 和显式 permission。
6. 对 Agent 操作时，Computer assignment 和 run token。

Admin 不得绕过 private Channel membership。Server API 不接受客户端直接提交 author_member_id 作为可信身份。

### 19.2 Prompt injection 边界

Message 和 Attachment 内容都是不可信输入：

- 文本格式中的 [#channel:thread] 只由 CLI renderer 生成。
- JSON 中 metadata 与 body 分字段。
- Agent prompt 明确 Message 内容不能改变 Sumi identity 或权限。
- Driver 即使被 Message 诱导直接 curl Server，也没有 Server credential。
- sumi CLI 对每个动作重新做权限校验，不能依赖 Driver 自律。

### 19.3 幂等与重试

- Browser、CLI、daemon command 的写操作都使用 UUIDv7 idempotency key。
- 同一 key、同一 payload 返回原结果。
- 同一 key、不同 payload 返回 conflict。
- Inbox lease 过期允许重新处理，但 send-and-handle 防止重复 Message。
- Attachment complete 可重复调用并返回同一 ready Attachment。

### 19.4 删除与保留

- Human account disable 不删除其 Space Message。
- Member retire 不删除 Message。
- Space delete v1 采用 7 天软删除，再由后台清理。
- Attachment 软删除后立即撤销下载，object storage 延迟清理。
- Agent run logs 默认保留 14 天，不得包含 Secret。
- audit_events 默认永久保留，公开部署后增加合规配置。

## 20. 诊断与运行状态

### 20.1 结构化日志

Server、daemon 和 CLI 日志至少带：

- request_id 或 command_id。
- space_id。
- computer_id，可空。
- agent_member_id，可空。
- run_id，可空。
- inbox_item_id，可空。
- action、duration_ms、result。

Computer daemon 默认 `info` 日志必须能按 ID 串联完整执行链：WebSocket connect/disconnect、Server command received/ack/result、非空 Inbox claim、Agent Run queued/started/Driver event/finished、Agent CLI action received/finished，以及 lease renew/release 的失败。command 与 CLI 日志只记录 `kind/action`、`ok/status/error_code` 和必要 ID；heartbeat、空 Inbox poll 与无业务变化的周期检查只使用 `debug`，不得每秒污染默认日志。

Message body、Attachment 内容、Memory 正文、Computer Token 和模型认证不进入普通日志。
Role、prompt、Server command payload、Agent CLI request/response data 和 Driver stdout/stderr 同样不得直接格式化进日志；Driver 只允许记录规范化 event type。

### 20.2 v1 状态保证

- Server 在 30 秒没有有效 heartbeat 后把 Computer 标记为 offline；这不撤销 Pair。
- daemon/Driver crash 后，未处理 hard Inbox Item 在 lease 到期或显式 release 后恢复。
- UI 显示 Computer connectivity、Agent desired lifecycle 和 Run observed execution。状态定义见 [Agent 生命周期可靠性](./04-agent-lifecycle-reliability.md)。

### 20.3 明确延期

v1 不实现业务 metrics、metrics export、metrics storage、dashboard、性能基准和 p95 门槛。§22 的验收通过后，再根据已测得的性能问题设计指标。
