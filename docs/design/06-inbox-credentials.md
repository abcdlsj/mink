# Inbox 与本地凭据

[返回设计索引](../design.md)

## 15. Inbox 与 Agent 注意力

### 15.1 系统保证

Sumi 可以保证：

- 有资格的信息会持久进入 Inbox。
- Inbox Item 在显式处理前不会因为进程退出而消失。
- daemon 会按照优先级和配置尝试唤醒 Agent。
- 同一个处理动作可以幂等重试。
- Agent 回复前可以知道上下文是否变化。

Sumi 不能保证模型一定做出正确的相关性判断。不得在产品文案中声称“绝不漏掉重要消息”；应声明为可靠投递和可追踪处理。

### 15.2 Inbox Item 类型与优先级

| 来源 | 类型 | 优先级 | 行为 |
| --- | --- | --- | --- |
| DM 中对方新 Message | direct | hard | 立即唤醒 |
| Message 显式 mention Agent | mention | hard | 立即唤醒 |
| reply_to 指向 Agent Message | reply | hard | 立即唤醒 |
| Agent 已订阅 Thread 的更新 | thread_activity | ambient | 聚合 |
| Agent 所在 Channel 普通 Message | channel_activity | ambient | 聚合 |
| Approval 需要 Human 处理 | approval | hard，仅 Human | 立即 UI 通知 |
| Computer/Agent 错误 | system | hard，Admin | 立即 UI 通知 |

发送者不为自己创建 Message Inbox Item。一个 Message 同时产生 mention 和 channel_activity 时，对该 Agent 只保留 mention hard Item，不重复创建 ambient Item。

### 15.3 Thread 订阅

Member 在以下情况自动订阅 Thread：

- 在 Thread 中发送 Message。
- 在 Thread 中被 mention。
- 对 Thread 显式 follow。

自动订阅只影响普通 Thread 更新的 Inbox，不改变读取权限。Member 可以 unfollow，但 direct mention 仍创建 hard Item。

Browser 读取 Thread 时响应包含当前 Member 的 `is_following`。显式 follow/unfollow 使用
`PUT/DELETE /api/v1/channels/{channel_id}/threads/{thread_id}/subscription`；两者都要求当前
Member 已加入 Channel，且重复调用保持幂等。unfollow 只将现有订阅标记 muted，不删除历史游标。

### 15.4 Ambient 聚合

普通 Channel Message 不能每条启动一次 Codex。Server 对每个 Agent、Channel 和可选 Thread 维护最多一个 pending ambient Item：

- first_seq：聚合开始序号。
- last_seq：最新序号。
- message_count。
- available_at：第一条消息时间加 debounce，默认 5 秒。
- force_at：第一条消息时间加 max wait，默认 30 秒。

新 Message 到来时更新 last_seq 和 count，但不得把 available_at 无限后移到 force_at 之后。hard Item 到来时，同来源 pending ambient Item 一起加入下一 batch。

### 15.5 状态机

~~~
pending -> leased -> handled
   ^          |
   |          +-> pending  lease expired or run failed
   |
   +------ deferred until available_at

pending/leased -> dead after retry limit
~~~

字段：

- status：pending、leased、deferred、handled、dead。
- available_at。
- lease_id、lease_expires_at。
- retry_count。
- handled_by_run_id、handled_at。
- last_error。

lease 默认 35 分钟，略长于 run timeout。daemon 每 60 秒续租 active run 对应 Items。Server 只能由持有 lease 的 Computer 处理 Item。

### 15.6 daemon 注意力循环

~~~
on inbox notification or periodic poll:
  find active Agents with available pending Items
  if Agent already running:
    leave new Items pending
    notify current run that context changed
    return

  respect Computer concurrency limit
  claim one Agent batch with lease
  build compact run prompt
  start current Driver

  while Driver runs:
    forward structured operational events
    renew lease

  after Driver exits:
    verify every claimed Item is handled or deferred
    if yes:
      finish run
    if no:
      release unhandled Items with retry_count + 1
      apply backoff
      after max retries mark dead and notify Admin
~~~

达到 `max_retry_count` 时，Server 在同一事务把 Item 标记为 dead，并为 Space 的 Human Owner/Admin
各创建一个不携带 Message 正文、Attachment 或 private Channel 地址的 system hard Inbox Item。

同一 Agent 不得并行处理两个 batch。不同 Agents 可以按 Computer concurrency 并行。

### 15.7 “Agent 自己判断”原则

daemon 只判断何时唤醒，不判断 Message 内容是否相关。当前 Driver 读取 compact Inbox 摘要和必要上下文后，选择：

- 回复并 handle。
- 不回复并 ack。
- 稍后处理并 defer。
- 读取更多 Channel、Thread 或其他已授权 Channel 后再决定。

v1 不增加廉价分类模型、关键词 router 或第二个“注意力 Agent”。这些机制会让实际 Agent 在尚未看到 Message 前就被替它做决定，并产生不可解释漏判。

### 15.8 上下文变化与 held draft

所有 read 响应返回 snapshot_channel_seq。Agent 发送时可以传 --based-on：

- 当前 Channel/Thread 没有新 Message：直接发送。
- 有新 Message，且本次 send 要 handle hard Inbox Item：Server 返回 context_changed，不创建 Message，并附最新序号与变化摘要。
- Agent 重新读取后可再次发送。
- 未来可增加 --force；v1 不开放 force，避免 Agent 在过期上下文中强发。

daemon 将 context_changed 呈现给 Driver，不把它算作 run failure。草稿留在 Agent workspace，由 Driver 决定修改或沉默。

`context_changed` 的 JSON error `details` 固定包含 `snapshot_channel_seq`、
`latest_channel_seq`、按 seq 升序的最多 10 条 `changes` 元数据（Message ID、seq、地址、Thread ID
和 author 摘要）以及 `has_more`。变化摘要不得把 Message 正文塞进错误或日志；Agent 使用返回的
地址与最新序号再次调用 channel/thread read。Server 必须先确认 `--handle` Item 是当前 run 持有的
hard lease，再做 freshness 判断；ambient Item 和不带 `--handle` 的普通发送不使用该强制门禁。

### 15.9 启动与恢复

- daemon 重启后从 Server 重新查询 Agent pending/leased Items。
- 属于旧 daemon session 且未过期的 lease 可以由同一 Computer 恢复；进程不存在时应主动 release。
- Computer offline 时 Inbox 继续积累。
- Computer 重连后按 hard 优先、created_at 次序恢复。
- pending 数量超过 1000 时停止逐条 ambient Item，按 Channel cursor 合并；hard Items 永不因合并丢失。

## 16. Computer 本地凭据

### 16.1 信任边界

v1 的 Server 不接收、不保存也不转发模型 API key。Computer Token、Codex 本地认证与 Builtin provider 认证只存在于 Computer 的权限受限本地文件和所需进程内存；Browser 没有模型 Secret 表单或 API。v1 不接入 macOS Keychain 或 Linux Secret Service。这只能降低其他普通 OS 用户误读的风险，不防御 root、同一 OS 用户下的恶意进程或已失陷的 Computer；部署文档必须如实说明，不能宣称本地静态加密。

### 16.2 本地认证规则

- Codex 只复制显式 `codex_auth_source` 的既有认证到 Agent 专属 CODEX_HOME，权限必须为 0600。
- Builtin 只读取显式配置的 Pi-compatible auth source 中当前 provider 的认证，认证不得写入 Agent Home。
- Driver 子进程与工具进程不得获得 Computer Token；Builtin 工具进程也不得获得模型 API key。
- Computer 删除后删除本机失效 Token 与规范化认证缓存，但保留 Agent Homes；外部 auth source 不属于 Sumi，不得删除。

### 16.3 日志规则

- Computer Token 和模型认证字段使用 redaction type，不得实现普通 Stringer。
- CLI 不提供读取本机凭据命令。
- daemon 环境日志不得输出完整 env。
- Driver 请求结束后清理包含 key 的环境和内存引用。
