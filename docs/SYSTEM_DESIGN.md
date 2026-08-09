# SYSTEM_DESIGN

本文件定义 Sumi 系统要求。产品要求与领域词汇见 `DESIGN.md`。

## 运行边界

Server：

- 持有 Space、Member、权限、Channel、Message、Thread、Attachment、Task、Inbox Item、Run 状态。
- 执行领域状态转换与事务；持久化 outbox；投递 Computer command；提供 Browser API、SSE 和 Computer WebSocket。
- 不执行模型，不保存 Provider Session、Memory、workspace 或模型凭据。

Computer：

- 持有 Agent Home、Memory、workspace、Provider Session、Driver 进程、sandbox、本地 outbox 和凭据。
- 不持有 Server 事实的正式状态，不决定 Task 归属；缓存的 Run 快照只用于执行。

Agent CLI：

- 是当前 Run 内唯一的 Sumi 操作入口；Agent、Space、Task、Focus、Run 由 Driver token 推导，不要求 Agent 重复提交。
- 不保存领域状态。

Driver：

- 只把 Run 和 Session 语义映射到 provider；Driver 输出不能直接成为 Message、Result 或 Memory。

## 协议与投递

- 当前 Computer 协议 schema 为 v3，Server 与 Computer 只宣告当前版本；wire required field 变更时提升版本，不在同一版本中兼容两套 schema。
- 双方无共同版本时拒绝连接。
- 写命令先持久化（稳定 command ID + 每 Computer 递增序号）再投递；Computer 先落本地再 ACK；重复命令按 ID 幂等。
- query 是独立请求-响应通道：不持久化、不重放、不进 command 序号。
- 业务写入与 outbox 属于同一事务；外部发送失败由 outbox 重试。
- Computer 重连握手同步 daemon session ID、command watermark 和本地 Run 实况；上一连接会话的残留帧必须被丢弃。
- 所有 HTTP 写操作使用 idempotency key；started、delivery、result、receipt 使用稳定 event ID 幂等。

## Run

- 状态为 dispatched → working → completed / yielded / failed / canceled。不设置 queued、starting、finalizing、stopping。
- 失败错误码固定为 driver_error、driver_lost、computer_restarted、session_unavailable、agent_unavailable、invalid_command、internal。
- Driver 临时错误在 Computer 内最多自动重试 3 次；最终失败才上报，Server 只对该 Run 计一次 Item retry。
- Computer 离线是可达性问题，不是 Run 失败；离线期间 Run 保持原状态。
- `computer.max_concurrent_runs` 只是内存保护阈值，不是调度器；超过上限的 Run 排队等待，不因等待失败。
- Agent restart 先请求 active Run graceful stop；有限等待后按 computer_restarted 收尾并保留结果 outbox。
- yield 对本次 Items 逐条给出 handled、released 或 deferred；deferred 用 Item 的 available_at 表达。

## Inbox 与注意力

- Inbox Item 是持久注意力事实，不复制 Message 正文。
- hard Item 来源：DM、mention、`@all`、reply、Linked Thread 新消息、system 错误。必须显式处理，Driver 最终回复不构成处理。
- ambient Item 来源：Agent 订阅的 Thread 更新和所在 Channel 的活动。按 Agent + Thread 或 Agent + Channel 聚合，用 debounce 和 force 上限防止无限推迟。
- 同一 Message 对同一 Member 只生成一个最高强度 Item；发送者不为自己生成 Message Item。
- Human 可通过一次命令把所有 pending 的本人 Item 标记为 handled；assigned、deferred、dead 不进入该命令，Item 不复制 Message 正文的不变式不受影响。
- 与 active Run 的 Agent、Task scope、Focus 一致的 hard Item 尝试 attach；不一致的保持 pending 并发送 notice，notice 不泄露正文。
- Run 失败时未处理 Items 返回 pending 并增加 retry_count；超过 max_retry_count 进入 dead，并创建不含正文的 system Item。
- Agent 显式 release 的 Item 不增加 retry_count；重复 command、receipt 丢失和重复 result 不重复计数。
- 终端结果中的默认 Released 不覆盖同一 Run 已由 Server 记录的显式处置（handled/deferred）；冲突时以 Server 记录为准。
- dead Item 可由治理者 requeue 回 pending 并重置 retry_count；同一事务递增 requeue_count 并写 audit。
- Computer 拒绝 attach 或 start 时，Server 先完成补偿事务（释放 Item 或终结 Run），补偿成功后才 ACK command；重复回执不重复释放或计数。

## Task 与协作事务

- 同一 Root Message 最多一个 Task；相同 idempotency key 返回同一 Task。
- Agent CLI 的 `task create` 可携带单个 `--source` 指定 Source Thread，不指定时用当前 Focus；Source Thread 必须是 Root Message，且该 Thread 已存在 Task（含终态）时报 Conflict；`--thread` 可重复，在创建事务内完成 Linked Thread 关联；`task link-thread` 只用于对已存在 Task 补充关联。
- Agent 用 `--source` 创建不在当前 Focus 的 Task 时，Task 为 TODO，并在同一事务为 assignee 创建 pending hard TaskActivity Item（thread_id 与 task_id 指向新 Task）；当前 Run 结束后该 Item 经 claim 启动绑定新 Task 的 Run，Run 进入 working 时 TODO 推进为 In Progress。
- Related Thread 只能关联可见范围兼容的 Thread；一个 Thread 同时最多关联一个未结束 Task。
- 状态转换：TODO → In Progress → In Review → Done；TODO / In Progress / In Review → Closed。
- 第一个 Task Run 进入 working 时，TODO 推进为 In Progress；In Progress 可直接进入 Done。
- Done 必须已有 Result Message；Closed 必须保存结构化原因；In Progress 和 In Review 必须有 assignee。
- Review 可由除 assignee 外能读取 Task 的 Human 或 Agent 确认 Done 或退回 In Progress；不保存 reviewer 字段，不使用 Permission。
- submit_review、done、close 只有一个事务入口，Browser 与 Agent CLI 共用。
- 普通 Message API 不能创建 Action Message；创建 Channel 或 Agent 的入口必须生成对应 Action Message。
- Channel 成员加入或离开与 System Notice 在同一事务写入。
- Agent CLI 的 `channel invite` 与 `channel remove` 分别执行 `channel.invite` 与 `channel.remove`；目标必须是同一 Space 中仍有效的 Member，成员关系变更、System Notice、Inbox Activity 和审计记录在同一事务写入。
- Run 默认只注入当前 Focus 所属 Channel 的 active Members；Space Members 与 Agent 所属的任意 Channel Members 通过 `space members` 和 `channel members` 只读命令按需查询。
- 所有写操作只有 Server 一个事务入口；Agent CLI 与 Browser 不得各自实现一套终态事务。

## Provider Session 与 Memory

- Provider Session 按 (Agent, scope, generation) 复用。
- 复用由 Driver、workspace、Role、audience fingerprint 决定；不兼容时创建新 generation。
- 必须换新 generation：Task 终态、Linked Threads 成员集合不兼容、Driver/Role/workspace 变化、locator 丢失或 resume 失败、显式 reset。
- 不得单独触发换新：token 量达到阈值、Run 数量、固定时间、Server 或 daemon 重启、yield 等待。
- Session 丢失后 Computer 创建新 generation，并从 Server 事实、Agent Memory 和结构化 Run 结果重建执行上下文。
- Builtin Provider Session 持久化每次模型调用的 token usage 与嵌入方写入的上下文元数据；`sumi-builtin-agent` harness 把每次压缩记录（触发原因、边界、估算 source/summary token）写入 session metadata，供运行期观测与 harness 基准测试使用。
- `sumi-agent-core` 是通用 agent runtime，不拥有 prompt 与压缩策略；`sumi-builtin-agent`（Sumi Computer）与 `sumi-telegram-agent`（Telegram）各自实现 `ContextStrategy`，提示词完全独立。
- Builtin 上下文压缩按模型上下文窗口与触发比例（`computer.builtin.context_window_tokens`、`compaction_trigger_ratio`）预判触发，provider 返回上下文超限错误时再触发一次压缩重试；两种路径都写入压缩记录。
- Builtin 压缩保留最近 `compaction_keep_recent_tokens`（默认 20000）token 的原始消息，切割点只落在 user/assistant 消息上；切到 turn 中间时为 turn 前缀单独生成摘要，并在摘要后附加被压缩消息中的文件读写清单。
- Builtin 上下文用量估算优先使用 provider 最近一次调用上报的 input tokens，再按字符数估算其后追加的消息；没有用量数据时才全部按字符估算。
- `memory/MEMORY.md` 是每个 Run 开始必须读取的主文件；产生影响后续协作的新知识时，Agent 必须在相关对外动作前写入。
- Builtin 文件工具与 bash 以 Agent Home 根为路径基准：文件工具使用 `workspace/<path>` 或 `memory/<path>`，裸路径（`MEMORY.md`、`notes/<topic>.md`）默认落在 Memory 根，绝对路径仅接受落在 `workspace/` 或 `memory/` 内的；bash 使用同一路径，shell 写入允许 `workspace/`、`runs/`（`TMPDIR`）与系统临时目录 `/tmp`（macOS 沙箱放行 `/private/tmp`，Linux 沙箱挂载私有 `/tmp`），持久文件放 `workspace/`，`/tmp` 只作 scratch。macOS 系统 bash 3.2 的 here-doc/here-string 临时文件固定写 `/tmp` 且忽略 `TMPDIR`，因此放行 `/tmp` 是 heredoc 可用的前提；有 Homebrew bash（`/opt/homebrew/bin/bash` 或 `/usr/local/bin/bash`）时优先使用，其临时文件遵循 `TMPDIR` 落入 `runs/`。Memory CLI 的 `path` 相对 Memory 根（如 `MEMORY.md`、`notes/<topic>.md`）。
- Memory 与 workspace 不上传 Server；Server 只保存投影（文件名、大小、SHA-256、更新时间）并在需要时查询在线 Computer；正文读取设置 no-store。
- Memory 不复制 Message 历史或 Provider transcript；symlink 可能指向 Memory 根之外，投影和正文读取不跟随。
- Agent 退役保留身份、Message、Task、Result；Memory 和 workspace 可能丢失，UI 必须说明该限制。

## 独立 Telegram Agent

- Telegram update 只能在对话 worker 完成处理并持久化会话进度后推进 offset；仅放入内存队列不构成确认。同一 chat 串行处理，不同 chat 可并行。
- 多个 chat 共享的会话索引只有一个进程内写入入口；会话 locator 与已处理 message ID 在同一次持久化中更新，并发 chat 不得相互覆盖。
- 到期 scheduled task 在执行成功前保留可恢复状态；进程退出、turn 启动失败或执行失败后仍可重试。循环任务从当前时间推进到下一个未来周期，不逐次补跑离线期间的历史周期。

## Agent CLI

- 所有自动化调用使用 `--json`；stdout 只能输出一个 JSON envelope，诊断输出写入 stderr。
- 稳定错误码：invalid_argument、unauthenticated、permission_denied、not_found、conflict、context_changed、rate_limited、unavailable、internal。
- `details` 只包含错误处理需要的结构化标识，不得包含 Message、Attachment、Memory、Secret 或 Provider transcript 正文。
- read 响应携带消息快照序号；提交 Message 或 Task Result 时可携带默认 snapshot。
- hard Item 相关输出发现新消息时，Server 返回 context_changed，不创建部分结果。
- 同一波内相互独立的调用必须在一次 tool-call batch 中发出；有数据依赖、写入冲突或可见顺序的调用保留屏障；run yield 必须是该 Run 最后一次调用。
- `details.next_action` 是给 Driver 的单一建议动作；Driver 对 `invalid_argument`、`permission_denied` 和非 retryable `conflict` 不得变体重试，需要 Human 时停止询问。

## 安全与内容保护

- Browser Session、Computer Token、Driver token 三个认证面不可互换；Server 只保存 token hash。
- Server 对每个读取和写入执行 Space、Channel membership、资源关系校验；Admin 不自动获得 private Channel 读取权。
- Task 不引入独立 ACL；可见范围由兼容的 Linked Threads 成员集合决定。
- Permission 只控制一个特定 Action；Agent Permission 包含 channel.create、channel.invite、channel.remove 和 agent.create；只有 Human Owner/Admin 可授予或撤销。
- 模型凭据只存在于 Computer 受限文件和必要进程内存；Driver token 只存在于 daemon 内存与该 Agent 的 app-server 环境，工具子进程不能读取其他 Agent 的凭据。
- Driver 和工具进程只能访问当前 Agent Home 允许的路径、本地 capability socket 和最小运行环境；sandbox 不可用或自检失败时 Driver validation 失败，不退化裸进程。
- Message、Attachment、网页、工具输出是不可信内容；prompt 不能授予权限，Server 权限检查和 sandbox 不依赖模型自律。
- 日志、audit、error details、metrics label 不得包含 Message、Attachment、Memory、workspace 文件、Task Result、Provider transcript、Secret 或完整环境变量；诊断使用稳定 ID、错误码、计数、时间和 hash。
- Message 使用软删除并保留 Thread 与 Task 引用；Computer 仍有 assigned Agent 时不可删除；Agent 退役保留历史；产品不得把 Computer 删除描述为远程擦除本地数据。

## 数据与事务

- 可由关联关系推导的数据不重复保存。
- 事务提交后才发送 SSE、WebSocket command 或外部请求。
- 稳定状态使用 text 和 CHECK 约束；正文、Secret、Provider transcript 不进入 idempotency 或 outbox metadata。
- 基线后 schema 变更使用前向 migration，不得修改已应用 migration。
- 性能优化先通过查询、索引和可重建投影解决；写模型保持规范化。

## 运维与诊断

- 诊断必须能沿 message → inbox item → task → run → command → result event 串联。
- 日志使用 tracing fmt 文本格式，默认输出到 stderr，行为 `<timestamp> <LEVEL> <target>: <message> field=value…`；默认级别 `sumi=info,tower_http=info`，可用 `RUST_LOG` 覆盖；ANSI 颜色仅在 stderr 为终端时启用。
- 事件消息使用 `<对象> <动作>` 的完成时句式；关联字段使用稳定 ID（run_id、command_id、item_id、task_id、channel_id、thread_id、message_id、computer_id、agent_member_id）和稳定 error_code，错误上下文优先用 Display 而非 Debug 栈。
- 生命周期事件命名：server 侧为 `Run dispatched to Computer`、`Run started on Computer`、`Run reached a terminal outcome`、`Computer connected`、`Computer disconnected`；computer 侧为 `Computer connected to Server`、`Computer disconnected from Server on shutdown`、`Agent Run started`、`Agent Run finalized`；协议事件为 `Computer command received/rejected`、`Computer command result`。
- 健康状态至少覆盖 Computer 连接、pending/assigned/dead Item 计数、dispatched/working Run 计数、command 和 result outbox 积压、Provider Session 状态计数、resume/steer/close 错误码。
- 治理动作（suspend、resume、restart、cancel Run、requeue Item、reset Session、删除 Computer）必须显示目标、影响范围和是否可恢复。

## Agent 关系图谱

- `GET /api/v1/spaces/{space_id}/agent-graph` 返回只读图谱：节点为 Space 中未退役 Agent；边为 Agent 对之间的互动统计，边方向只区分来源（A→B mention/reply），总数与最后消息时间用于排序。
- 互动只使用结构化事实，不解析正文：DM channel 成员关系及其 text 消息、`message_mentions`、`reply_to_message_id` 指向的父消息作者；软删除消息不计入。
- 可见性规则：节点对全部 Space Member 可见；互动统计对 Space Member 可见其所在 channel 的部分，Owner/Admin 作为 governor 可见全部统计；recent_messages 正文只返回请求者已是 channel 成员的消息，governor 不因治理权限获得正文。
- 该接口不写库、不改变领域状态。统计不是实时计算：Server 按 Space 在进程内缓存原始 channel 级聚合，TTL 2 小时；每次请求只做可见性过滤，并按请求者可见范围实时查询最近消息正文。

## LLM usage 本地遥测

- Computer 在本地 daemon 数据库（`llm_usage` 表）记录每次 LLM 调用的 token 用量：run_id、agent_id、driver_kind、model、input/output/cached/cache_write tokens、耗时与时间；Agent runtime 返回当前 Run 内每次 provider 调用的独立 usage（包含上下文压缩调用），builtin Driver 逐条写入，Codex Driver 暂不暴露 token 用量。
- 这些行只存在 Computer 本地，不上传 Server，不进 outbox/command metadata；Server 不持久化任何 usage 数据。
- `GET /api/v1/computers/{computer_id}/llm-usage?range=24h|7d|30d` 是只读代理查询：Server 校验请求者为该 Computer 的 Owner/Admin 后，经现有 query 通道向在线 Computer 实时取数并聚合；Computer 离线时返回 `computer_unreachable`。
- 聚合在 Computer 侧完成：总量、cache hit rate（cached / input）、按小时（≤48h）或按天的曲线 bucket、按 model 与按 agent 的分组，以及按 agent 的独立曲线序列（`by_agent_series`）和 model 分组（`by_agent_model`），供 Agent 维度统计页使用。
