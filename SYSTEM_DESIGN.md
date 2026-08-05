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
- 与 active Run 的 Agent、Task scope、Focus 一致的 hard Item 尝试 attach；不一致的保持 pending 并发送 notice，notice 不泄露正文。
- Run 失败时未处理 Items 返回 pending 并增加 retry_count；超过 max_retry_count 进入 dead，并创建不含正文的 system Item。
- Agent 显式 release 的 Item 不增加 retry_count；重复 command、receipt 丢失和重复 result 不重复计数。
- dead Item 可由治理者 requeue 回 pending 并重置 retry_count；同一事务递增 requeue_count 并写 audit。
- Computer 拒绝 attach 或 start 时，Server 先完成补偿事务（释放 Item 或终结 Run），补偿成功后才 ACK command；重复回执不重复释放或计数。

## Task 与协作事务

- 同一 Root Message 最多一个 Task；相同 idempotency key 返回同一 Task。
- Related Thread 只能关联可见范围兼容的 Thread；一个 Thread 同时最多关联一个未结束 Task。
- 状态转换：TODO → In Progress → In Review → Done；TODO / In Progress / In Review → Closed。
- 第一个 Task Run 进入 working 时，TODO 推进为 In Progress；In Progress 可直接进入 Done。
- Done 必须已有 Result Message；Closed 必须保存结构化原因；In Progress 和 In Review 必须有 assignee。
- Review 可由除 assignee 外能读取 Task 的 Human 或 Agent 确认 Done 或退回 In Progress；不保存 reviewer 字段，不使用 Permission。
- submit_review、done、close 只有一个事务入口，Browser 与 Agent CLI 共用。
- 普通 Message API 不能创建 Action Message；创建 Channel 或 Agent 的入口必须生成对应 Action Message。
- Channel 成员加入或离开与 System Notice 在同一事务写入。
- 所有写操作只有 Server 一个事务入口；Agent CLI 与 Browser 不得各自实现一套终态事务。

## Provider Session 与 Memory

- Provider Session 按 (Agent, scope, generation) 复用。
- 复用由 Driver、workspace、Role、audience fingerprint 决定；不兼容时创建新 generation。
- 必须换新 generation：Task 终态、Linked Threads 成员集合不兼容、Driver/Role/workspace 变化、locator 丢失或 resume 失败、显式 reset。
- 不得单独触发换新：token 量达到阈值、Run 数量、固定时间、Server 或 daemon 重启、yield 等待。
- Session 丢失后 Computer 创建新 generation，并从 Server 事实、Agent Memory 和结构化 Run 结果重建执行上下文。
- `memory/MEMORY.md` 是每个 Run 开始必须读取的主文件；产生影响后续协作的新知识时，Agent 必须在相关对外动作前写入。
- Memory 与 workspace 不上传 Server；Server 只保存投影（文件名、大小、SHA-256、更新时间）并在需要时查询在线 Computer；正文读取设置 no-store。
- Memory 不复制 Message 历史或 Provider transcript；symlink 可能指向 Memory 根之外，投影和正文读取不跟随。
- Agent 退役保留身份、Message、Task、Result；Memory 和 workspace 可能丢失，UI 必须说明该限制。

## Agent CLI

- 所有自动化调用使用 `--json`；stdout 只能输出一个 JSON envelope，诊断输出写入 stderr。
- 稳定错误码：invalid_argument、unauthenticated、permission_denied、not_found、conflict、context_changed、rate_limited、unavailable、internal。
- `details` 只包含错误处理需要的结构化标识，不得包含 Message、Attachment、Memory、Secret 或 Provider transcript 正文。
- read 响应携带消息快照序号；提交 Message 或 Task Result 时可携带默认 snapshot。
- hard Item 相关输出发现新消息时，Server 返回 context_changed，不创建部分结果。
- 同一波内相互独立的调用必须在一次 tool-call batch 中发出；有数据依赖、写入冲突或可见顺序的调用保留屏障；run yield 必须是该 Run 最后一次调用。

## 安全与内容保护

- Browser Session、Computer Token、Driver token 三个认证面不可互换；Server 只保存 token hash。
- Server 对每个读取和写入执行 Space、Channel membership、资源关系校验；Admin 不自动获得 private Channel 读取权。
- Task 不引入独立 ACL；可见范围由兼容的 Linked Threads 成员集合决定。
- Permission 只控制一个特定 Action；首批 Agent Permission 为 channel.create 和 agent.create；只有 Human Owner/Admin 可授予或撤销。
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
- 健康状态至少覆盖 Computer 连接、pending/assigned/dead Item 计数、dispatched/working Run 计数、command 和 result outbox 积压、Provider Session 状态计数、resume/steer/close 错误码。
- 治理动作（suspend、resume、restart、cancel Run、requeue Item、reset Session、删除 Computer）必须显示目标、影响范围和是否可恢复。
