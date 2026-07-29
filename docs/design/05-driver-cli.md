# Driver 与 Agent CLI

[返回设计索引](../design.md)

## 1. Driver 契约

Driver adapter 只负责将 Sumi 的 Run 和 Session 语义映射到 provider。所有 Drivers实现统一接口：

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

Driver stdout、final response 和 tool output 都不能自动创建 Message、Task Result 或 Memory。只有 Agent通过 Sumi capability 提交协作事实。

## 2. Run 输入

Computer传给 Driver 的输入分为四块：

1. `global_contract`：安全规则、Sumi能力和沟通约束。
2. `agent_profile`：Agent identity、Role revision 和 Memory入口。
3. `work_context`：可选Task、Linked Threads、Session scope和已有公开结果。
4. `run_context`：Run、Focus、claimed Items 和当前消息窗口。

稳定内容和动态内容必须保持结构化边界。Driver可以按 provider协议映射缓存，但不能修改产品语义。

Provider Session resume 后仍必须注入本 Run 的 `run_context`。Session历史不能替代Server的最新可选Task、Message、权限和Inbox事实。

## 3. Codex Driver

Codex Driver使用 Codex app-server 或等价的结构化本地协议，以支持：

- 创建 thread。
- 使用本地 thread/session ID resume。
- 启动 turn。
- 向 active turn steer 新输入。
- interrupt turn。
- 读取结构化状态和最终结果。

Codex thread/session ID 只保存在 Computer Session registry。Server、Message和Task API不得出现该 ID。

普通 Thread 首次运行时创建 Codex thread。Agent 在该 Run 创建 Task 时，Computer 把 Codex thread 提升为 Task Session。

后续 Task Run 在兼容条件下 resume 同一 thread。Session reset 创建新 generation 和新 Codex thread，但 Task ID 不变。

Codex Driver 必须使用 Agent 专属的`CODEX_HOME`。daemon 只复制明确允许的 provider、model 配置和显式认证源。

Human 的 MCP、hook、project trust、header 和其他全局配置不得隐式进入 Agent 环境。

如果当前 Codex接口不支持 active turn steer，adapter返回 `unsupported`。Sumi保留 Item pending，不得通过启动第二个并行 Codex进程伪造 steer。

## 4. Builtin Driver

Builtin Driver实现与 Codex相同的 Session和Run契约。它可以在本地保存 provider conversation state，但该状态仍属于 Provider Session缓存。

Builtin 的模型凭据只存在于 Computer。文件和 shell tool 只能访问当前 Agent 的 workspace、Memory 和运行临时目录。

工具子进程不能获得 Computer Token 或模型 API key。

Builtin不支持某项能力时必须报告 capability。daemon不能静默回退到 Codex，也不能改变 Run语义。

## 5. Agent capability

`sumi agent` 是Run内唯一Sumi操作入口。daemon注入：

- `SUMI_SOCKET`
- `SUMI_RUN_TOKEN`

Run token隐式确定：

- Agent身份。
- Space。
- 可选Task。
- Focus Thread。
- Run和fencing token。

CLI不得要求Agent重复传入这些字段。Agent也不能通过参数切换身份、Task或Focus。

所有自动化调用使用 `--json`。输出 envelope：

```json
{
  "schema_version": 1,
  "ok": true,
  "data": {},
  "error": null
}
```

## 6. 启动读取

Run prompt已经包含当前Focus和可选Task，因此Agent不需要先调用whoami、task show和thread read才能理解基本上下文。以下命令用于按需扩展：

```text
sumi agent context current --json
sumi agent message read [--before seq] [--after seq] [--limit 50] --json
sumi agent thread read {thread-id} [--after seq] [--limit 50] --json
sumi agent channel read {channel-id} [--around message-id] [--limit 50] --json
sumi agent memory read {path} --json
```

`context current` 一次返回Agent、Task、Focus、Run、claimed Items和Session continuity摘要。它不返回Provider transcript。

## 7. 最小协作命令

### 7.1 Message

```text
sumi agent message send --body "text" [--handle item-id] --json
sumi agent message send --thread {linked-thread-id} --stdin [--handle item-id] --json
sumi agent message send --channel {channel-id} --stdin --json
```

省略目标时发送到当前Focus。发送到其他Thread要求该Thread已链接到当前Task。发送到普通Channel主时间线必须显式提供目标。

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

`inbox current` 只显示当前Run已经领取的Items和不同Focus notices。不同Focus Item正文必须在后续Run取得lease后读取。

### 7.4 Attachment 与 Memory

```text
sumi agent attachment upload {path} --json
sumi agent attachment download {attachment-id} --output {path} --json
sumi agent memory read {path} --json
sumi agent memory write {path} --stdin --json
```

## 8. 原子操作

以下操作必须各自使用一个Server事务：

- `task create`：Task及其Source Thread、当前Run绑定、Session提升command、audit和outbox。
- `message send --handle`：Message和Item handled。
- `task submit-review`：review Message、Task状态和Items。
- `task done`：Result Message、Task状态和Items。
- `task close`：Task状态、close reason和Items。
- `run yield`：Run终态和Items release/defer。

Agent不通过多条CLI命令拼接这些不变量。

## 9. 上下文变化

所有 read 响应包含消息快照序号。Agent 提交 Message 或 Task Result 时，可以携带 Server 注入的默认 snapshot。

hard Item 相关输出发现新消息时，Server 返回`context_changed`，不创建部分结果。

错误只返回变化Message的ID、seq、author和地址。Agent需要正文时显式读取。错误、日志和activity不得复制Message正文。

## 10. 退出规则

Driver turn正常结束后，daemon自动进入Run finalizing。Agent不调用 settle、finish-run或ack-all。

存在未处理Items时：

- Agent已显式yield时，按yield事务处理。
- Agent已defer或ack时，按已提交状态处理。
- 其余Items释放并增加失败计数，Run标记failed。

该规则迫使执行结果明确，同时不增加Agent必须记忆的系统操作。
