# Computer 与 Agent

[返回设计索引](../design.md)

## 11. Computer 与 daemon

### 11.1 Computer 生命周期

Computer 内部状态：

~~~
pairing -> online <-> offline -> deleted
~~~

- pairing：已生成一次性配对请求，尚未由 Human 确认。
- online：daemon 长连接和心跳有效。
- offline：超过 30 秒没有有效心跳。
- deleted：用户执行 Delete Computer 后的内部 tombstone；普通列表不再返回，Computer Token 不能重新连接，重新接入必须生成新 Token 并重新配对。

online 和 offline 记录连接状态，配对关系由 Computer Token 和 Server 绑定记录决定。daemon 退出、网络中断或 Server 重启后，Computer 进入 offline，并在使用原 Token 重连后回到 online。

Delete Computer 前，UI 列出受影响的 Agent，并要求 Human 确认。删除事务取消该 Computer 的 active Run、退役承载的 Agent，并撤销 Computer Token。历史 Member、Message、Attachment 和 audit 保留。在线 daemon 收到终止帧后执行 graceful shutdown。离线 daemon 下次连接时收到终止响应并退出。

daemon 确认 Server 已删除 Computer 或拒绝旧 Token 后，删除本机失效身份，清空 command 和 Run 状态，保留 Agent Home。下一次启动生成新 Token 并重新配对。deleted tombstone 没有恢复入口。

### 11.2 初始化与配对

Human 在目标机器运行：

~~~
sumi computer --server https://sumi.example.com
~~~

该命令启动 Computer daemon。首次启动时：

1. 检查本地是否已有 Computer identity。
2. 若没有，使用 OS CSPRNG 生成 256-bit Computer Token，立即写入权限为 0600 的本机 `secrets.json`。
3. 调用公开的 pairing start API，提交 Computer Token 的 SHA-256、hostname、OS 和 daemon version；Server 不接收或保存 raw Token。
4. Server 返回短时 pairing code 和 browser URL，有效期 10 分钟。
5. daemon 打印 URL 并尝试打开默认浏览器。
6. 已登录 Human 打开页面，选择 Space、编辑 Computer name 并确认。
7. Server 校验该 Human 是 Owner 或 Human Admin，将 Computer 绑定到 Space。
8. daemon 使用 Computer Token 轮询结果，取得已确认的 Computer ID 和 Space ID，并把绑定结果写回本机 `secrets.json`。
9. 后续启动直接复用同一 Computer ID 和 Token；不得因为进程退出、网络断开或 Computer offline 重新配对。
10. daemon 建立出站 WebSocket，完成协议握手后 Computer 变为 online。

daemon 默认尝试打开配对页；无人值守、本地自动化和测试配置可以设置
`computer.open_pairing_browser=false`，此时仍在终端输出配对 URL，由调用方通过同一确认 API 完成配对，
不得因此绕过配对授权或创建另一套 Computer 身份流程。

配对确认页必须显示 hostname、OS、daemon version、Computer Token 的不可逆短 fingerprint 和目标 Space，防止确认错误机器；不得显示 raw Token。

配对 start 请求提交 base64url 编码的 Computer Token SHA-256、hostname、OS 和 daemon version；Server 生成并只保存 pairing code hash，响应返回 pairing_id、一次性 code、`/pair-computer/{pairing_id}?code=...` Browser URL 与 expires_at。daemon 轮询 result 时使用 raw Computer Token 作为 Bearer token，Server 只比对 hash。Human confirm 请求包含目标 space_id、Computer name 与一次性 code；成功响应不返回 Token，result 也只返回 Computer ID 和 Space ID。raw Computer Token 从生成起只持久化在 daemon 的受限 `secrets.json`，通过 HTTPS/WSS 仅用于认证；Server 始终只持久化 hash。result 在配对有效期内可安全幂等重试，避免首次成功响应丢失后 Computer 永久无法恢复。

### 11.3 连接与心跳

- daemon 只发起出站 TLS 连接，不监听公网端口。
- Computer WebSocket 使用 Computer Token 认证，承载 Server command、Run 事件、result receipt 和 heartbeat。
- daemon 每 10 秒发送 heartbeat。
- heartbeat 包含 daemon version、OS、Agents 数量和 active runs；不采集 CPU、memory 等资源 metrics。
- Server 30 秒未收到 heartbeat 时标记 offline。
- 重连使用指数退避：1s、2s、4s、8s，最大 30s，并加入随机抖动。
- 每个 Server command 必须先持久化到 PostgreSQL，具有 command_id 和递增 computer_seq。WebSocket 只负责低延迟投递，不是事实来源。
- daemon 在 SQLite 保存 command、Run 状态和 result outbox。重复 command 返回已保存的结果。
- command ACK 只表示 daemon 已持久化 command。Run 在 daemon 上取得执行槽并启动 Driver 后，通过 `run_started` 进入 running。
- 新 WebSocket 建立时，Server 重放 pending 和 acked command。daemon 的 result sender 独立重发 result outbox，直到收到 `result_receipt`。
- 连接断开时，Server 保留未确认 command。daemon 重连握手携带 last_acked_computer_seq，Server 按序重发后续 command。因此交付语义是 at-least-once，幂等执行使业务效果等价于一次。
- protocol ping/pong 仅用于探测连接；业务 heartbeat 仍是带类型的 JSON frame，并更新 Computer 状态。

### 11.4 本地目录

默认根目录固定为当前用户的 `~/.sumi`，不得使用当前登录用户随意可写的临时目录：

~~~
~/.sumi/
  computer/
    daemon.db
    secrets.json
    logs/
    agents/
      {agent-id}/
        profile.json
        memory/
        workspace/
        drivers/
          codex/
          builtin/
        runs/
        logs/
  runtime/
    daemon.sock
~~~

- daemon.db 使用 SQLite，保存 Computer 本地状态、Server command 结果、Agent 运行状态和本地重试队列。
- secrets.json 保存 Computer Token 与本机 Driver 认证。它不得进入 daemon.db、日志、Agent Home 或备份导出。
- `~/.sumi` 是本机 Sumi 文件的唯一根目录；`computer/` 只保存持久状态和 Agent Home，`runtime/` 只保存 UDS socket 与运行时临时文件。
- `computer/` 和 `runtime/` 目录权限必须为 0700，secrets.json 必须为 0600。daemon 使用同目录临时文件、fsync 和 rename 原子更新；发现 group/other 权限时拒绝启动并给出修复命令。
- profile.json 是 Server Agent 配置的缓存，不是事实来源。
- memory/ 和 workspace/ 属于 Agent，不属于 Driver。
- drivers/codex/ 与 drivers/builtin/ 只保存各自 Driver 的私有状态。
- runs/ 保存临时运行输出，按保留策略清理。

每个 Agent 目录权限必须限制为 daemon 运行用户。不同 Agent 进程不能访问对方目录。
目录权限 0700/文件权限 0600 只隔离其他 OS 用户，不能隔离同一 daemon 用户启动的不同 Driver 进程；
因此 daemon 启动 Driver 时还必须用进程 sandbox 将可写路径限制到当前 Agent Home，并拒绝其他 Agent Home。

### 11.5 资源管理

一台 Computer 可以注册任意数量 Agents，但 daemon 必须配置：

- max_concurrent_runs：默认 max(1, CPU 核心数 / 2)，向下取整。
- per_agent_timeout：默认 30 分钟。
- per_agent_memory_limit：平台支持时启用。
- shutdown_grace_period：默认 20 秒。

v1 每个 Agent 最多一个 active Run。attention scheduler 只 claim 可用执行槽加固定 prefetch 数量的 Run。超过该容量的 Inbox Item 保持 pending。

## 12. Agent

### 12.1 Agent 配置

Agent Server 记录至少包含：

- member_id。
- space_id。
- computer_id。
- name：Space 内 Member 名称不要求全局唯一，但 mention 名称必须可消歧。
- role_text。
- status。
- driver_kind：codex 或 builtin。
- driver_config：版本化 JSON，只放非 Secret 配置。
- attention_config。
- created_by_member_id。
- created_at、updated_at、retired_at。

attention_config v1 字段：

- dm_immediate：固定 true。
- mention_immediate：固定 true。
- ambient_enabled：默认 true。
- ambient_debounce_seconds：默认 5，允许 1 至 60。
- ambient_max_wait_seconds：默认 30，允许 5 至 300。
- max_retry_count：默认 3。

### 12.2 创建流程

Human 直接创建：

1. 选择 online Computer。
2. 输入 Agent name 和 Role。
   Agent handle 由 name 自动生成，并允许创建者在提交前修改。
3. 选择 Driver（Codex 或 Builtin）。
4. 选择权限级别 Member/Admin；只有 Owner 能直接授予 Admin。
5. Server 创建 Agent Member 和 Agent，状态 provisioning。
6. Server 向 Computer 下发 provision command。
7. daemon 创建 Agent Home、写入 profile cache 并验证所选 Driver 的本地配置、认证与 sandbox。
8. daemon 返回成功，Server 将状态改为 active。
9. 失败则状态为 error，保留可重试原因，不创建第二个 Agent。

Agent 发起创建时，前五步中的写入被 Approval 替代；审批成功后才执行 provisioning。

### 12.3 Agent 生命周期

Agent 使用两个字段记录治理意图和本地准备结果：

- `desired_lifecycle` 为 `active|suspended|retired`。
- `provision_status` 为 `provisioning|ready|error`。

创建 Agent 时，Server 写入 `desired_lifecycle=active` 和 `provision_status=provisioning`。Computer 完成本地目录、Driver 配置和 sandbox 校验后写入 ready。失败时写入 error 和不含正文的 `last_error_code`。

只有 `active + ready` 的 Agent 可以 claim Inbox。suspended Agent 保留 pending Inbox。retired Agent 永久停止新协作，并保留历史身份。

暂停时，UI 让 Human 选择：

- `stop_after_current` 允许当前 Run 完成。
- `cancel_now` 将当前 Run 改为 stopping，并执行 Driver 停止流程。

Retire 撤销本地运行能力并从在线列表移除。历史 Message、Attachment、Approval 和 audit 保留。

Owner 或 Admin 可以对 provision error 执行 `retry`。Server 复用 Agent identity 和 Agent Home，重新发送幂等 `agent.provision` command。成功后清除错误，失败后更新 `last_error_code`。

Run 的 observed execution、ownership lease、result receipt 和故障恢复见 [Agent 生命周期可靠性](./04-agent-lifecycle-reliability.md)。

### 12.4 Role

Role 是 Agent 的职责与边界，不是任意 Driver prompt。Role 修改：

- 立即写入 Server 并增加 revision。
- active run 继续使用启动时 revision。
- 下一次 run 使用最新 revision。
- 审计记录修改者和前后摘要。

Role prompt 不得包含 Server Secret。UI 显示当前 Role 和 revision 更新时间。

### 12.5 Memory

v1 Memory 是 Agent Home 下由 Agent 持续维护的文件集合：

~~~
memory/
  MEMORY.md
  notes/
~~~

约束：

- MEMORY.md 是默认入口，可不存在；daemon 首次创建空模板。
- Driver 可以读写自己的 Agent Memory，不得读写其他 Agent Memory。
- Channel 和 Thread 历史不复制为 Memory；Agent 只有主动总结后才写入。
- Driver 切换时继续使用同一 Memory。
- Server 仅保存 Memory 文件名、大小、更新时间和 hash，不保存正文。
- Owner/Admin 通过 UI 请求查看时，由 daemon 在线读取；Computer offline 时正文不可用。
- Browser 通过 `POST /api/v1/agents/{agent_id}/memory/read` 提交相对 `memory/` 的 `path`。
  Server 只向 Agent 当前 Computer 下发 `agent.memory.read` command，并在当前进程内临时转发结果；
  Memory 正文不得写入 PostgreSQL、idempotency record、outbox、audit 或日志。Computer 与 Server
  的持久 command result 只保存成功/失败状态，不保存正文。读取仅支持不超过 1 MiB 的 UTF-8
  普通文件，daemon 必须拒绝绝对路径、`..`、symlink 和 canonical path 逃逸。

v1 不承诺 Computer 丢失后的 Memory 恢复。该限制必须在 UI 中明确，后续通过端到端加密快照解决，不得在 v1 偷偷把 Memory 明文上传 Server。

### 12.6 Driver 切换

v1 UI 允许展示 Driver selector，可选 codex 和 builtin。切换必须遵守：

1. Agent 先进入 suspended，且没有 active run。
2. daemon 验证新 Driver 可用。
3. 新 Driver 获得同一个 Role、Memory、workspace 和 CLI。
4. 旧 Driver 私有状态保留但不再加载。
5. Server 更新 driver_kind 并恢复 Agent。

Driver 切换不得重置 Inbox、Channel memberships 或 Member permissions。
