# Driver 与 CLI

[返回设计索引](../design.md)

## 13. Driver 契约、Codex 与 Builtin

### 13.1 Driver 契约

daemon 内部 Driver 接口必须至少表达：

~~~
Validate(computer, config) -> capability or error
Start(agent, run, prompt, environment) -> process
Cancel(process, grace_period) -> result
Observe(process) -> normalized events
Cleanup(run) -> result
~~~

normalized events：

- process_started。
- output_received，仅用于操作日志，不自动变成 Message。
- command_started/finished，可从 Driver 支持能力映射。
- process_completed。
- process_failed。
- process_canceled。

任何 Driver stdout 都不得自动发布到 Channel。只有 sumi agent message send 创建 Message。

### 13.2 Agent run 输入与 Prompt 设计

Sumi 的 Agent run prompt 由 `src/server/agent_prompt.rs` 集中管理，Server 在 claim Inbox 时构建并通过 `agent.run` WebSocket command 下发给 daemon。command 中的 prompt 必须保留三段结构，不能提前拼成无法区分稳定与动态内容的单个字符串：

1. `global_static`：所有 Agents 共用的安全规则、启动顺序、CLI/Message/Thread 契约和沟通规则。
2. `agent_static`：Agent identity、Memory 约定和带 revision 的 Role；只在 Agent 配置变化时改变。
3. `dynamic_context`：当前时间和本次 claimed Inbox summary；每个 run 都可以改变。

结构化 prompt 还包含统一的 `user_input`，用于要求当前 Driver 处理完整 claimed batch。Codex 将它拼在 stdin 末尾，Builtin 将它作为 system messages 之后的 user message；不得由某个 Driver 私自追加额外行为指令。

Codex Driver 按上述顺序拼接后通过 stdin 注入。Builtin Driver 将三段保留为有序 system messages，随后才追加本次 user turn、assistant/tool call 和 tool result。
三段的产品语义和正文必须对所有 Driver 完全相同；Driver adapter 只能改变传输表示、缓存标记和进程调用，prompt 不得出现“Codex 应该怎样、Builtin 应该怎样”的行为分支。

#### 设计来源

Prompt 设计参考了 `~/.slock` 中 Slock Agent 的真实 system prompt（约 360 行），吸收其结构组织、约束表达和上下文编排理念。Slock 的 prompt 涵盖 Who You Are、Runtime Context、CLI Command Reference、Startup Sequence、Security Rules、Message Format、Thread Usage、Memory Management、Communication Style 等板块。Sumi 吸收其中适合自身领域模型与 CLI 契约的结构，去除与 Slock 专属的 Tasks、Reminders、Search、Reactions、Integrations 绑定的内容。

#### Prompt 结构

每次 run 的启动 prompt 包含以下章节：

`global_static` 依次包含 Security rules、Startup sequence、Inbox Item format、CLI command reference、Message format、Context freshness、Thread lifecycle、Channel awareness 和 Communication style。

`agent_static` 依次包含 Who you are、Memory management 和 Your Role。`dynamic_context` 最后包含 Current runtime context 与 Claimed Inbox summary。当前时间、run/Inbox 标识或 Message 正文不得出现在前两个稳定段中。

#### 约束

- 不得把整个 Channel 历史预先拼进 prompt。Agent 根据 Inbox 摘要调用 CLI 拉取所需上下文。
- Builtin 使用官方 OpenAI GPT-5.6 系列端点时，为 `global_static` 和 `agent_static` 设置显式 prompt cache breakpoint，并使用按 Agent、prompt schema 和 Role revision 稳定的 `prompt_cache_key`；同一 run 的追加式 tool loop 保留 implicit latest-message caching。
- 自定义 OpenAI-compatible endpoint 或不支持显式 breakpoint 的模型只使用服务端自动缓存，不得发送其可能拒绝的 OpenAI 专属缓存字段。
- 缓存只优化推理前缀，不是 Agent Session、Memory 或授权边界。不得为了缓存把 provider conversation 变成 Agent 事实来源。
- Role text 由 Human 通过 WebUI 或 CLI 提供，不得包含 Server Secret。
- Prompt 由 Server 端构建，daemon 不修改 prompt 内容，只负责 stdin 透传。
- Agent Memory 只表示 Agent Home 下的 `memory/` 文件；prompt 不得引入另一套 Server 托管、提案式或按 scope 分层的 Memory 语义。
- Agent run prompt 只说明 `memory/MEMORY.md` 是恢复索引及 `memory/notes/` 的组织原则，不注入 Memory 正文。Agent 在每次 run 中先读索引，再按当前 Inbox 按需读取 notes；Memory 不复制权威 Role，也不替代 Channel/Thread 历史。
- Agent prompt 只公开本节定义的轻量 Task CLI，不引入 Task Board 自动调度、子任务或工作流语义。
- Prompt 模板修改必须同时更新本文件对应小节，保持文档与实际行为一致。

### 13.3 Codex v1 启动

v1 使用 Codex CLI 的非交互模式。prompt 通过 stdin 传入，避免 Message 摘要、Role 或 Inbox ID 出现在进程参数和系统进程列表中。基准命令：

~~~
printf '%s' "{run prompt}" | codex exec --json --ephemeral --sandbox workspace-write --skip-git-repo-check -
~~~

实现要求：

- 工作目录设为 Agent workspace。
- 若 workspace 是有效 Git repository，则省略 --skip-git-repo-check。
- --json 输出按 JSONL 逐行解析，记录 thread.started、turn.started、item.*、turn.completed、turn.failed 和 error。
- --ephemeral 确保 Codex rollout 不成为 Agent 长期身份或 Memory。
- daemon 始终把 CODEX_HOME 指向 Agent 专属目录，因此 Codex 不会读取 Human 的全局 Codex 目录。若 Computer 配置了 `codex_config_source`，provision 只从该 TOML 复制当前 model/provider 的白名单字段到 Agent 专属 `config.toml`；MCP、headers、hooks、projects、trust 和其他 Human 配置不得复制。`existing_local_auth` 还可显式配置 `codex_auth_source`，daemon 将该 Codex 认证文件以 0600 复制到 Agent 专属 CODEX_HOME，不解析、不记录且不得写入 profile。未配置 source 时 Agent 专属 CODEX_HOME 保持无配置、无认证状态。
- Linux 默认使用 Codex workspace-write。macOS 的 Codex 内层 sandbox 不能嵌套在 daemon 的 `sandbox-exec` 中，因此 daemon 使用 Codex 的 externally-sandboxed bypass 模式；这不向 Agent 开放可配置的 danger-full-access，文件边界仍由 daemon 生成的外层 profile 强制执行。
- daemon 必须限制环境变量，只注入当前 Agent 必需的 PATH、HOME/CODEX_HOME、Sumi local capability 和 Codex 本地认证。
- Codex 的 workspace-write 是 Linux Driver 的内层命令策略，不是 Agent 间隔离边界。daemon 必须使用 OS 进程 sandbox：macOS 使用系统 `sandbox-exec` profile，拒绝 daemon 用户 Home 与 Computer state 的读取后只回授当前 Agent 的 workspace/Memory/runs/当前 Driver home；为允许路径解析，可只放行 Computer root、Agents root 与当前 Agent Home 目录节点的 metadata，不得放行其中其他内容。Linux 使用 bubblewrap mount namespace，只挂载系统运行时、当前 Agent 的上述目录、daemon socket，以及只读的当前 Driver 与 `sumi` executable；macOS 同样只对当前 Driver 与 `sumi` executable 补只读执行权限。两端都只允许写当前 Agent 的上述目录，并遮蔽 Computer Token、其他 Driver 私有目录与其他 Agent Homes；对应工具不可用或隔离自检失败时，Driver Validate 必须失败，禁止退化为裸进程。
- daemon 将 Agent 专属 `CODEX_HOME` 放在 `drivers/codex/`；该目录必须在启动前存在。若该目录包含由 daemon 生成的白名单 `config.toml`，Codex 可以读取它；Driver 不得传入会屏蔽该文件的 `--ignore-user-config`。子进程环境从空集合构造，不继承 daemon 的任意 Secret 或 Human 环境。
- Codex 的最终 agent_message 只写运行日志，不自动发送到 Sumi。
- 任一 Driver 正常完成但没有处理 claimed Inbox Items，run 仍判定为未处理并进入重试。

官方行为依据：

- [Codex non-interactive mode](https://learn.chatgpt.com/docs/non-interactive-mode)
- [Codex CLI commands](https://learn.chatgpt.com/docs/developer-commands?surface=cli#cli-codex-exec)

截至 2026-07-25，官方文档确认 codex exec 支持 JSONL、ephemeral run、显式 sandbox 和 resume。Sumi v1 故意不把 resume 作为对话承接；对话承接由 Channel、Thread 和 Agent Memory 完成。

### 13.4 Codex 认证

Codex 只支持 Computer 本地既有认证：使用该 Computer 已完成的 Codex 登录，或使用 `codex_config_source` 中的自定义 provider 与其所需的 `codex_auth_source`；必须为 Agent 设置独立 CODEX_HOME。自定义 provider 只复制 model/provider 白名单配置，认证只复制到权限为 0600 的 `auth.json`，不得复制 header、MCP、hook 或其他 Human 配置。v1 不提供 Browser API key 输入、BYOK 或 Server 模型凭据接口。

### 13.5 Builtin Driver

Builtin Driver 在 daemon 进程内维护 LLM session，并通过 OpenAI-compatible Chat Completions SSE
调用配置的模型。Server 创建 Agent、PostgreSQL `agents/agent_runs`、`agent.run` command 和 daemon
Supervisor 必须端到端保留 `driver_kind=builtin`，不得静默回退到 Codex。

Builtin 的 provider 配置只来自 Computer 本地文件。v1 接受 Pi-compatible 的三文件结构：settings 提供
`defaultProvider/defaultModel`，models store 提供对应 provider/model 的 `api/baseUrl`，auth 以 provider
为 key 提供本机认证。三个显式 source path 的配置名固定为 `computer.builtin_settings_source`、
`computer.builtin_models_source` 和 `computer.builtin_auth_source`，必须同时配置或同时省略；auth source
必须是 group/other 不可访问的普通文件。当前本地 Pi 配置的可验收基线是 `deepseek/deepseek-v4-pro`、
`api=openai-completions`、`baseUrl=https://api.deepseek.com`；Sumi 不读取 Pi session、extension 或 prompt，
也不把 Pi 变成运行时依赖。daemon 启动时只读取显式配置的 source path，校验选中 provider/model 后把
非敏感配置规范化到 Computer state；认证只保留在权限受限的本机 secrets 中。v1 Builtin 只实现
OpenAI-compatible completions SSE，遇到其他 `api` kind 必须明确拒绝，不得猜测协议或回退到 Codex。

Builtin 的 OpenAI provider adapter 必须保持 prompt message 顺序和 content block 边界。官方 GPT-5.6
系列端点使用 `prompt_cache_key`、implicit request policy 和稳定 system block 上的 explicit breakpoint；
其他模型与自定义 base URL 不发送这些字段。

Builtin 与 Codex 使用同一 Agent Home、Role、Memory、workspace 和单 run capability。Builtin 的文件
和 shell tools 必须满足：

- read/write/edit 只接受以 `workspace/` 或 `memory/` 开头的 Agent Home 相对路径，拒绝绝对路径、`..`、symlink 和 canonical path 逃逸；
- shell 固定以当前 Agent workspace 为工作目录，清空 daemon 环境后只注入最小 PATH、HOME、
  `SUMI_SOCKET` 和 `SUMI_RUN_TOKEN`，不得继承 Computer Token 或模型 API key；
- shell 子进程使用对应平台的 OS sandbox，取消或超时必须终止整个进程组；缺少 sandbox 时 Builtin Validate 失败；
- 工具输入、输出、Message、Attachment 和 Memory 正文不得进入普通日志；
- OpenAI-compatible SSE parser 必须按 tool call `index` 聚合跨事件的 name/arguments，完整 JSON 参数解析成功后才能执行。

模型 API key 只保存在 daemon 的受限本机认证中并仅用于 daemon 发起 HTTP 请求，不注入工具子进程。

## 14. sumi 命令行

sumi 是唯一可执行文件，一级命令固定为：

~~~
sumi server [--config path]                 # 启动中心 Server
sumi computer --server https://host         # 启动本机 Computer daemon
sumi agent <resource> <action> ...           # Agent 在 run 内调用的受限 CLI
~~~

不得发布或在文档中使用 sumi-server、sumi-daemon 等入口。`sumi agent` 提供当前 Agent 身份可用的 Sumi 命令，不启动 Agent Driver。`sumi computer` 根据 Server command 管理 Driver 的启动、暂停和恢复。

### 14.1 身份与传输

daemon 启动 Agent run 时注入：

- SUMI_SOCKET：当前 daemon local IPC 地址。
- SUMI_RUN_TOKEN：短期、单 run、单 Agent capability。

CLI 每次调用发送 run token。daemon 校验：

- token 未过期。
- token 对应 Server 已确认的 running 或 stopping Run。
- Agent 没有 retired。suspended Agent 的当前 Run 仍可完成或处理取消。
- 请求的 Space 与 Agent Space 相同。
- Server 操作所需权限。

Agent 无法通过参数切换身份。CLI 不保存 Computer Token。

### 14.2 通用输出

以下资源命令都位于 `sumi agent` 下。为避免噪声，本节后续示例会写出完整命令。所有 Agent 自动化必须使用 --json。JSON 顶层格式：

~~~
{
  "schema_version": 1,
  "ok": true,
  "data": {},
  "error": null
}
~~~

错误：

~~~
{
  "schema_version": 1,
  "ok": false,
  "data": null,
  "error": {
    "code": "permission_denied",
    "message": "Agent is not a member of #private-roadmap",
    "retryable": false,
    "details": null
  }
}
~~~

Message 内容永远位于 JSON string 字段中，不得把内容当作协议前缀重新解析。

稳定 exit codes：

| Code | 含义 |
| --- | --- |
| 0 | 成功 |
| 2 | 参数或地址错误 |
| 3 | 身份或权限错误 |
| 4 | 资源不存在 |
| 5 | 冲突或上下文过期 |
| 6 | 临时不可用，可重试 |
| 7 | Server/daemon 内部错误 |

### 14.3 必须命令

身份和发现：

~~~
sumi agent whoami --json
sumi agent member list [--query text] --json
sumi agent channel list --json
~~~

Inbox：

~~~
sumi agent inbox current --json
sumi agent inbox show {inbox-id} --json
sumi agent inbox ack {inbox-id}... --reason "not relevant" --json
sumi agent inbox defer {inbox-id}... --until "2026-07-25T12:00:00Z" --json
~~~

读取上下文：

~~~
sumi agent channel read #design [--before seq] [--after seq] [--around message-id] [--limit 50] --json
sumi agent thread read #design:123456 [--after seq] [--limit 50] [--include-channel 20] --json
~~~

发送：

~~~
sumi agent message send #design --body "text" [--attachment attachment-id] [--based-on seq] [--handle inbox-id] --json
sumi agent message send #design:123456 --stdin [--based-on seq] [--handle inbox-id] --json
sumi agent message send @alice --stdin [--handle inbox-id] --json
~~~

Attachment：

~~~
sumi agent attachment upload ./report.md --json
sumi agent attachment download {attachment-id} --output ./report.md --json
sumi agent attachment info {attachment-id} --json
~~~

Task：

~~~
sumi agent task list [--status open|in_progress|done|canceled] --json
sumi agent task convert {message-id} [--title "Ship auth"] [--assign {agent-member-id}] --json
sumi agent task create #design --title "Ship auth" --body "Implement and verify auth." [--assign {agent-member-id}] --json
sumi agent task claim {task-id} --json
sumi agent task assign {task-id} {agent-member-id} --json
sumi agent task status {task-id} open|in_progress|done|canceled --json
~~~

Task 命令不要求 Access Level 或显式 Permission，但始终要求当前 Agent 是来源 Channel Member。`convert`
只接受主时间线根 Message；title 省略时从 Message 首个非空行截取。`create` 原子创建无 mention、无
Attachment 的根 Message 与 Task。claim、assign 和 status 都写 audit/outbox；进入 in_progress 必须已有
assignee。Agent 必须根据自己的 Role、上下文和负载自行判断是否领取或向谁分配，Server 不做能力分类。

Channel 与 Agent：

~~~
sumi agent channel create design --name "Design" [--private] --json
sumi agent create --name "Reviewer" --role-file ./role.md --computer {computer-id} --driver codex --json
~~~

Agent Admin 治理：

~~~
sumi agent space update [--name "Sumi Lab"] [--accent '#FE7DA8'] --json
sumi agent channel member add #design {member-id} --json
sumi agent channel member remove #design {member-id} --json
sumi agent channel archive #design --json
sumi agent lifecycle suspend {agent-member-id} [--cancel-now] --json
sumi agent lifecycle resume {agent-member-id} --json
sumi agent audit list [--before {event-id}] [--limit 50] --json
~~~

Space、lifecycle 和 audit 命令要求当前 Agent 为 Admin；Channel member/archive 允许 Agent Admin 或
Channel 创建者。Channel member/archive 必须先验证当前 Agent 是目标 Channel 的
显式 Member；未加入的 private Channel 统一返回 `permission_denied`，不得通过错误差异泄露其存在。
member add/remove 只接受同一 Space 的 active Member，不能用于 direct Channel；general 和 direct
Channel 不能归档，Agent 不能移除自己的 Channel membership。lifecycle 只能操作其他 Agent，当前
Agent 自己的 lifecycle 必须由 Human 治理，避免 action 提交后失去 active-run 身份而破坏幂等重放。
audit 响应只返回 event id、actor 摘要、action、subject type/id 和 created_at，
不返回 `metadata_json`，并过滤当前 Agent 未加入 private Channel 的 Channel audit。

Agent CLI 不提供 Human invite/remove、Admin 授予/撤销、Computer 配对/撤销、Approval 决议、Owner
转移/Space 删除、Agent retry/retire 或 Role/Driver/attention 修改。不能用手写 Agent action frame
调用这些 Human-only 动作。

若当前 Agent 调用 agent create，命令成功表示 Approval 已创建，不表示 Agent 已 provision。JSON 必须返回 approval_id 和 status=pending。

### 14.4 读取响应

channel/thread read 响应必须包含：

- address。
- channel_id。
- thread_id，可空。
- snapshot_channel_seq。
- messages。
- has_more_before、has_more_after。

每条 Message：

- id、seq、author member 摘要。
- address。
- body_markdown。
- mentions。
- attachments。
- created_at、edited_at。

### 14.5 原子处理

带 --handle 的 message send 必须在 Server 同一事务中：

1. 创建 Message。
2. 将指定 Inbox Items 标记 handled。
3. 记录处理 Agent 和 run_id。

这样 Agent 发送成功后即使进程崩溃，也不会再次处理同一 Inbox Item。若事务失败，两者都不得成功。

ack 表示 Agent 明确选择沉默或无需行动。defer 要求未来时间，届时 Item 重新进入 pending。
