# Driver 与 Agent CLI

[返回设计索引](../design.md)

## 1. Driver 契约

Driver adapter 只负责将 Sumi 的 Run 和 Session 语义映射到 provider。所有 Drivers 实现统一接口：

```text
validate(agent_home, config) -> capabilities
open_or_resume(session_spec) -> session
start_turn(session, run_input) -> turn
steer(turn, item_input) -> accepted | too_late | unsupported
interrupt(turn, reason) -> outcome
observe(turn) -> normalized events
close(session, reason) -> outcome
```

规范事件包括 `session_opened`、`session_resumed`、`turn_started`、`activity`、`turn_completed`、`turn_failed`、`turn_interrupted` 和 `session_closed`。

Driver stdout、final response 和 tool output 都不能自动创建 Message、Task Result 或 Memory。只有 Agent 通过 Sumi capability 提交协作事实。

## 2. Run 输入

Computer 传给 Driver 的输入分为四块：

1. `global_contract`：安全规则、Sumi 能力和沟通约束。
2. `agent_profile`：Agent identity、Role 和 Memory 投影，见 [Computer 与 Agent](04-computer-agent.md) 的 Memory 定义。
3. `work_context`：可选 Task、Linked Threads、Session scope 和已有公开结果。
4. `run_context`：Run、Focus、本 Run 的 Items 和当前消息窗口。

注入内容采用固定结构并标注读取优先级：除 reference 标识外的块必须读取；Agent ID、Space ID 等身份标识只作为 reference 提供。`message_snapshot_sequence`、`role_revision` 等内部同步字段和空值字段不注入。Item 正文与窗口内 focus 消息重复时不重复注入。

稳定内容和动态内容必须保持结构化边界。Driver 可以按 provider 协议映射缓存，但不能修改产品语义。

Provider Session resume 后仍必须注入本 Run 的 `run_context`。Session 历史不能替代 Server 的最新可选 Task、Message、权限和 Inbox 事实。

`run_context`中的 focus 消息包含稳定 Message ID、作者 ID 和正文。注入采用有界窗口：Root 与最近 5 条 reply 注入全文，更早消息不注入；Agent 需要更早消息时用 `thread.read` 按需读取。Item 的来源正文只在来源消息不在窗口内时注入，来源消息在窗口内时只注入 Item 标识与引用关系。

`global_contract` 必须要求 Agent 处理本 Run 的每个 Item。hard Item 必须通过 Sumi capability 执行 `message send --handle <item-id> --body <text>`、`ack`、`defer` 或 `yield`；Driver final response 不构成 Item 处理结果。

`global_contract`还必须执行[Computer 与 Agent](04-computer-agent.md)定义的 Memory 维护规则，并明确给出`MEMORY.md`的读取和写入命令。Run 输入中的 Memory 投影只用于发现文件，不能替代 Agent 在每个 Run 开始时读取主文件。

## 3. Codex Driver

Codex Driver 使用 Codex app-server 或等价的结构化本地协议，以支持：

- 创建 thread。
- 使用本地 thread/session ID resume。
- 启动 turn。
- 向 active turn steer 新输入。
- interrupt turn。
- 读取结构化状态和最终结果。

Codex thread/session ID 只保存在 Computer Session registry。Server、Message 和 Task API 不得出现该 ID。

普通 Thread 首次运行时创建 Codex thread。Agent 在该 Run 创建 Task 时，Computer 把 Codex thread 提升为 Task Session。

后续 Task Run 在兼容条件下 resume 同一 thread。Session reset 创建新 generation 和新 Codex thread，但 Task ID 不变。

Codex Driver 必须使用 Agent 专属的`CODEX_HOME`。daemon 只复制明确允许的 provider、model 配置和显式认证源。

Codex Driver 必须为每个 Run 向工具子进程注入当前 capability socket 和 Run token，分别使用 `SUMI_SOCKET` 和 `SUMI_RUN_TOKEN`。Run token 只存在于该 Run 的进程环境，不得写入配置文件、日志或 provider session。重新启动 app-server 时，Driver 必须 resume 原 Codex thread 后再启动 turn。

Codex Driver 启动 app-server 时使用 `--dangerously-bypass-approvals-and-sandbox`，turn 使用 `dangerFullAccess`。Computer 通过 Agent 专属目录、Run Secret 和 capability 授权控制协作写入。Run Secret 只用于本机 capability socket 的调用者认证，由 daemon 生成，不出本机，也不表达执行资格。

Human 的 MCP、hook、project trust、header 和其他全局配置不得隐式进入 Agent 环境。

如果当前 Codex 接口不支持 active turn steer，adapter 返回 `unsupported`。Sumi 保留 Item pending，不得通过启动第二个并行 Codex 进程伪造 steer。

## 4. Builtin Driver

Builtin Driver 实现与 Codex 相同的 Session 和 Run 契约。它可以在本地保存 provider conversation state，但该状态仍属于 Provider Session 缓存。

Builtin 只接入 OpenAI Chat Completions 兼容协议。Computer 配置使用`api_base`、`token`和`model`三个字段；`api_base`是 API 根路径，Driver 请求其`/chat/completions`端点。三个字段必须同时存在，不读取其他工具的 settings、models 或 auth 文件。

```toml
[computer.builtin]
api_base = "https://api.example.com/v1"
token = "replace-with-provider-token"
model = "provider-model-id"
```

环境变量使用`SUMI_COMPUTER__BUILTIN__API_BASE`、`SUMI_COMPUTER__BUILTIN__TOKEN`和`SUMI_COMPUTER__BUILTIN__MODEL`。

Builtin 的模型凭据只存在于 Computer。文件和 shell tool 只能访问当前 Agent 的 workspace、Memory 和运行临时目录。

工具子进程不能获得 Computer Token 或模型 API key。

Builtin 把 append-only provider conversation history 与发送给模型的 active context 分开保存。active context 接近实现阈值时，Builtin 使用同一 provider 把较早历史压缩为 provider-only summary，并保留最近消息作为尾部。summary、压缩边界和完整历史都只属于 Provider Session 缓存，不成为 Message、Memory 或 Task 事实。压缩遵守[Computer 与 Agent](04-computer-agent.md)定义的 Session generation 规则。

Provider 明确返回 context limit 错误时，Builtin 可以在同一个 Turn 内执行一次压缩并重试。该恢复不创建新 Run 或 Session generation。压缩失败、重试仍失败或没有可压缩历史时，Turn 失败。

Chat Completions stream 只有在收到正常`finish_reason`后才能完成。连接提前结束、输出因长度截断或异常终止时，Builtin 必须报告 Driver 失败。Computer 为压缩和失败记录不含 Message、Memory、tool output 或 Provider transcript 正文的诊断码。

Builtin 不支持某项能力时必须报告 capability。daemon 不能静默回退到 Codex，也不能改变 Run 语义。

## 5. Agent capability

`sumi agent` 是 Run 内唯一 Sumi 操作入口。daemon 注入：

- `SUMI_SOCKET`
- `SUMI_RUN_TOKEN`

Run token 隐式确定：

- Agent 身份。
- Space。
- 可选 Task。
- Focus Thread。
- Run。

CLI 不得要求 Agent 重复传入这些字段。Agent 也不能通过参数切换身份、Task 或 Focus。

所有自动化调用使用 `--json`。输出 envelope：

```json
{
  "schema_version": 1,
  "ok": true,
  "data": {},
  "error": null
}
```

失败也必须向 stdout 输出一个 JSON envelope：

```json
{
  "schema_version": 1,
  "ok": false,
  "data": null,
  "error": {
    "code": "permission_denied",
    "message": "channel.create permission is required",
    "retryable": false,
    "details": {
      "action": "channel.create"
    }
  }
}
```

`--json`模式下，参数校验、认证、权限、冲突、IPC、Server 和内部错误都使用该 envelope。stdout 只能包含一个 JSON 文档；诊断输出写入 stderr。

`error.code`是 Agent 判断分支的稳定字段。首批通用错误码为`invalid_argument|unauthenticated|permission_denied|not_found|conflict|context_changed|rate_limited|unavailable|internal`。

`message`只用于解释错误，不能作为程序判断依据。`details`只能包含错误处理需要的结构化标识，不得包含 Message、Attachment、Memory、Secret 或 Provider transcript 正文。

成功退出码为`0`，失败退出码非零。Agent 不能只依赖退出码理解错误原因。

### 5.1 调用编排

Agent 必须按数据依赖和写入冲突，把当前工作需要的 Sumi CLI 调用分成多个依赖波次。同一波内的调用不依赖彼此结果、不修改同一资源且没有可见顺序要求时，Agent 必须在一次 Driver tool-call batch 中分别发出这些调用。Agent 不得在同一波的调用之间插入模型推理。daemon 可以并发或排队执行同一波调用；该规则保证减少模型往返，不保证调用同时完成。后续波次只处理依赖前一波结果的调用。

每个 tool call 只能包含一次`sumi agent`调用，并保留独立的 JSON envelope。Agent 不得用`;`、`&&`、后台进程或 shell 循环把多次`sumi agent`调用拼成一个 tool call。单次调用仍可以通过 stdin 接收正文。

以下调用可以进入同一波：

- 参数已由 Run 输入提供的独立只读调用，包括读取不同的 Thread、Channel、Message 或 Memory 文件。
- 输出路径不同的 Attachment 下载。
- 对不同 Items 作出的、已经确定且相互独立的`ack`或`defer`。

以下调用之间必须保留顺序屏障：

- 后一调用的参数或决策依赖前一调用结果，例如`discover -> action`、`attachment upload -> message send`和`memory read -> memory write`。
- 调用修改同一个 Item、Task、Memory path 或输出文件。
- Message 或 Task Action 具有协作者可见的顺序。
- 调用改变 Run 或 Task 边界。`run yield`必须是该 Run 的最后一次 Sumi CLI 调用。

Agent 必须检查同一波中每个 JSON envelope。已经成功的调用不得重复。失败的只读调用只有在`error.retryable=true`时才能重试；写调用结果不确定时，Agent 必须先读取权威状态再决定是否重试。没有对应读取能力时，Agent 不得盲目重试写调用。

### 5.2 渐进式发现

Agent 不需要预先知道资源创建所需的环境参数。`discover`是所有需要二次选择的 capability 共用的只读命令：

```text
sumi agent discover {operation} --json
```

`operation`使用稳定的 action code，例如`agent.create`。成功响应是可扩展的 discovery document，至少包含`operation`、字段定义和每个动态字段的`available`选项；字段定义包含字段名、值类型和是否必填。选项只返回当前 Run 所在 Space 中可见且满足资源约束的稳定 ID、显示名和状态。Discovery 不创建资源、不需要 idempotency key，也不把 Message、Secret 或 Provider transcript 放入响应。

首批`agent.create`发现结果包含 Agent display name、Role 文件、当前 Space 中可承载 Agent 的 Computer 列表、支持的 Driver 列表，以及当前 Agent 是否具有`agent.create` Permission。Access Level（包括 Admin）不自动替代该 Permission。

CLI 的静态`--help`只说明命令形状；动态可选值必须通过`discover`读取。创建命令提交 discovery 结果中的稳定值，Server 仍重新校验 Space、在线状态、Driver 和 Permission。

## 6. 启动读取

Run prompt 已经包含当前 Focus、可选 Task 和空间成员 display name 名单，因此 Agent 不需要先调用 whoami、task show 和 thread read 才能理解基本上下文。以下命令用于按需扩展：

```text
sumi agent discover {operation} --json
sumi agent context current --json
sumi agent message read [--before seq] [--after seq] [--limit 50] --json
sumi agent thread read {thread-id} [--after seq] [--limit 50] --json
sumi agent channel read {channel-id} [--around message-id] [--limit 50] --json
sumi agent channel leave {channel-id} --json
sumi agent memory read {path} --json
```

`context current` 一次返回 Agent、Task、Focus、Run、本 Run 的 Items 和 Session continuity 摘要。它不返回 Provider transcript。

## 7. 最小协作命令

### 7.1 Message

```text
sumi agent message send --body "text" [--handle item-id] --json
sumi agent message send --body "text" [--attachment {attachment-id}] --json
sumi agent message send --thread {linked-thread-id} --stdin [--handle item-id] --json
sumi agent message send --channel {channel-id} --stdin --json
sumi agent message send --to {member-id} --body "text" --json
```

省略目标时发送到当前 Focus。发送到其他 Thread 要求该 Thread 已链接到当前 Task。发送到普通 Channel 主时间线必须显式提供目标。`--attachment`可重复，只接受当前 Space 中状态为 ready 的 Attachment，挂载关系与 Message 在同一事务写入。

正文中的`@display_name`会被解析为结构化 mention，目标必须是目标 Channel 的 Member。mention 与 Browser 提交的 mention 使用同一投影和注意力路由，Agent 不需要理解内部 member ID。

`--to`先创建或复用当前 Agent 与目标 Member 的 DM，再发送 Message。它支持 Human 和 Agent 目标，但只能用于确实需要直接通知某个 Member 的场景。Agent-Agent DM 对 Human 不可见，普通进度和协作必须发送到当前 Focus 或共享 Channel。

### 7.2 Task

```text
sumi agent task create [--title text] [--assign member-id] --json
sumi agent task link-thread {thread-id} --json
sumi agent task unlink-thread {thread-id} --json
sumi agent task update [--title text] --json
sumi agent task submit-review --body-file {path} [--post-to focus|source] --json
sumi agent task done --result-file {path} [--post-to focus|source] --json
sumi agent task close --reason invalid|duplicate|not_needed|obsolete|other [--note text] --json
sumi agent run yield [--note "text"] --json
```

`task create`是创建 Task 的唯一 Agent 命令。它不接受 Message ID 或 Thread ID。

Server 从当前 Run 的 Focus 推导 Root Message，并原子创建 Task、Source Thread 和 Run 绑定。

除`task create`外，Task 命令只在当前 Run 已绑定 Task 时可用。这些命令不接受 Task ID。

`submit-review`进入`in_review`。不需要复核时可以直接调用`done`。管理其他 Task 属于 Human UI 或未来的显式治理能力。

### 7.3 Inbox

```text
sumi agent inbox current --json
sumi agent inbox ack {item-id} [--reason text] --json
sumi agent inbox defer {item-id} --until timestamp --json
```

`inbox current` 只显示当前 Run 的 Items 和不同 Focus notices。不同 Focus Item 正文必须在处理该 Item 的后续 Run 中读取。

### 7.4 Attachment 与 Memory

```text
sumi agent attachment upload {path} --json
sumi agent attachment download {attachment-id} --output {path} --json
sumi agent memory read {path} --json
sumi agent memory write {path} --stdin --json
```

`attachment upload`只创建 Attachment，不把它挂到任何 Message。挂载必须由 `message send --attachment {attachment-id}`显式完成。非上传者只能下载已挂载到可见 Channel Message 的 Attachment。

### 7.5 特定 Action

Agent capability 必须提供创建 Channel 和创建 Agent 的领域命令。命令参数只描述目标资源，不接受 Action Message 字段。

```text
sumi agent channel create {slug} [--topic {text}] [--private] --json
sumi agent agent create {name} --role-file {path} --computer-id {computer-id} --driver {driver} --json
```

`channel create`要求 Agent 显式提交 Channel slug。可选的`topic`是面向 Member 的说明，可以使用中文。slug 和 topic 的含义与约束由[数据库设计](08-database.md#51-channels)定义；CLI 不接受 Channel name，也不根据 topic 猜测 slug。

`agent agent create`的`computer-id`和`driver`来自`discover agent.create`，不能由当前承载 Agent 的 Computer 或 Driver 隐式推导。

`channel leave` 允许 Agent 主动退出普通 Channel。它要求目标 Channel 已包含当前 Agent，不能用于 DM；Server 在同一事务中移除成员、写入 `system_notice`、发送成员变更事件并记录 idempotency。

Server 从 Run token 推导 actor 和当前 Focus。Action 成功时，Server 在同一事务中创建目标资源和对应 Action Message。

Agent 必须分别具有`channel.create`或`agent.create`Permission。Review 不需要 Permission。

## 8. 原子操作

以下操作必须各自使用一个 Server 事务：

- `task create`：Task 及其 Source Thread、当前 Run 绑定、Session 提升 command、audit 和 outbox。
- `message send --handle`：Message 和 Item handled。
- `task submit-review`：review Message、Task 状态和 Items。
- `task done`：Result Message、Task 状态和 Items。
- `task close`：Task 状态、close reason 和 Items。
- `run yield`：Run 终态和 Items release/defer。

Agent 不通过多条 CLI 命令拼接这些不变量。

## 9. 上下文变化

所有 read 响应包含消息快照序号。Agent 提交 Message 或 Task Result 时，可以携带 Server 注入的默认 snapshot。

hard Item 相关输出发现新消息时，Server 返回`context_changed`，不创建部分结果。

错误只返回变化 Message 的 ID、seq、author 和地址。Agent 需要正文时显式读取。错误和日志不得复制 Message 正文。Activity 只按[API 与事件](07-api.md)展示 Server 生成的语义参数和有界 Message preview；其他正文仍由原资源的授权读取取得。

## 10. 退出规则

Driver turn 正常结束后，daemon 自动结算该 Run。Agent 不调用 settle、finish-run 或 ack-all。

存在未处理 Items 时：

- Agent 已显式 yield 时，按 yield 事务处理。
- Agent 已 defer 或 ack 时，按已提交状态处理。
- 其余 Items 释放并增加失败计数，Run 标记 failed。

该规则迫使执行结果明确，同时不增加 Agent 必须记忆的系统操作。
