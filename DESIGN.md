# Sumi Next Design

本文档定义 Sumi Next 最终应当成立的产品语义、系统边界和不可破坏的不变量。设计不服从现有代码、schema、协议或测试；实现与本文冲突时，删除或重写实现，不为兼容错误地基增加第二套模型。

## 0. 文档职责

- 本文只保存当前有效设计，不保存提交记录、测试结果、迁移过程、完成状态或历史方案。
- 产品语义、权限边界、持久事实或故障语义改变前，先修改本文。
- 实现细节只在会影响跨模块合同、安全或数据正确性时进入本文。
- 尚未实现的能力必须在 UI 和运行状态中诚实表达，但不因此降低目标设计。
- 不建立兼容层、抽象层或双轨实现来保护可以删除的旧代码。

## 1. 产品目标与范围

Sumi 是一个让多个 Human、Agent 和 Computer 长期协作并完成目标的 AI 组织系统。

核心目标：

- Human 通过 DM、群组或明确委托与 Agent 协作；
- Agent 在 Grant 范围内拆解 Work、选择协作者、交换 Artifact、处理失败并交付结果；
- Agent 身份、关系、权限、工作和记忆不依赖某个模型进程或 Computer；
- 一台 Computer 可以承载任意数量的逻辑 Agent，不设置固定产品上限；
- 不同 Agent 可以独立并发执行，一个 Agent 的阻塞、取消或崩溃不影响其他 Agent；
- Builtin Agent Core 支持 Human 自带 Provider credential，Codex 与 Claude Code 通过 External Adapter 接入；
- Server 是协作和控制事实中心，Computer 是执行节点。

当前产品范围：

- macOS 与 Linux；
- 单 Server、单 Organization、SQLite；
- Desktop、个人多 Computer 和小团队共用同一事实模型；
- trusted-local 可以作为首个执行环境，但不宣称 Host 文件系统强隔离；
- cloud-hosted Computer 后续复用同一 Computer 合同。

Sumi 不是聊天壳、通用 Workflow/DAG 编辑器、远程进程启动器、固定 Manager Agent 层级，也不是为了兼容历史代码而存在的框架。

## 2. 统一语言

| 术语 | 唯一含义 |
| --- | --- |
| Human | 注册到 Server、可以参与协作的自然人身份 |
| Computer | 独立注册的执行节点身份，不参与 Space 或 Work |
| Agent | Server 创建的长期协作 Principal，不是进程或模型 session |
| AgentProfile | Agent 的版本化 role、mission、instructions 和展示信息 |
| Grant | 对 Human 或 Agent 的显式、可撤销、可过期授权 |
| RuntimeSpec | 一个 Agent 期望使用的 Engine、模型、工具、资源、Sandbox 和 credential 配置 |
| Placement | Agent 当前期望运行的 Computer 及其单调 desired revision |
| CapabilityInventory | Computer 对实际可用 Engine、Adapter、Sandbox 和平台能力的版本化声明 |
| RuntimeSlot | Computer daemon 内一个 Agent 独占的运行槽和可变运行状态 |
| Engine | 执行 Agent 的实现，只分 Builtin Engine 与 External Adapter |
| Provider | Builtin Core 调用模型 API 的窄适配边界 |
| ProviderAccount | Provider endpoint、model policy、quota policy 与 CredentialBinding 的非 Secret 配置 |
| External Adapter | Codex、Claude Code 等外部 agent runtime 的桥接器 |
| Workspace | Agent 在某台 Computer 上的长期私有工作目录，不是共享事实库 |
| Inbox | 指向 Message、Work 或系统注意力事实的 typed projection，不复制来源正文 |
| Run | Server 记录的一次 Agent 输入处理和结果提交 |
| Space | Human 与 Agent 的沟通上下文 |
| Work | 有目标、责任、约束、验收和结果的协作承诺 |
| Artifact | 可版本化、授权、引用和审计的持久成果 |

禁止把 Engine、Provider、External Adapter 重新统称为 Driver；禁止把 Agent、RuntimeSlot 和进程混成一个对象。

## 3. 身份与创建

注册只属于 Human 和 Computer：

- Human registration 建立 Server 协作身份；
- Computer pairing/registration 建立执行节点身份；
- 两者没有父子、所有权或生命周期依赖；
- 授权 pairing 的 Human 只作为 Audit actor；该 Human 被 disable 不能使 Computer 自动失效；
- Computer 是否有效只由自身 credential、显式 revoke 和 Organization policy 决定。

Agent 不注册。Agent 由拥有 agent.create Grant 的 Human 在 Server WebUI 创建：

- Computer CLI、daemon 和本地目录不能创建、导入或恢复 Agent 身份；
- 创建者不拥有 Agent，Agent 是独立 Principal；
- 创建者失效不删除 Agent；
- Agent 可以建议创建新角色，但当前只能由 Human 在 WebUI 确认创建和授权。

所有 Agent 在身份层面平等。role 只描述职责和专长，不授予权限；协调某个 Work 不形成系统级上下级。

## 4. 系统边界

~~~text
Server Control Plane
  Human / Computer identity
  Agent / AgentProfile / RuntimeSpec / Placement
  Grant / Space / Message / Work / Artifact / Run / Audit
                           |
                           | desired state + durable facts
                           v
Computer daemon
  CapabilityInventory -> RuntimeSupervisor
                          |- Agent A RuntimeSlot -> Builtin Core -> Provider
                          |- Agent B RuntimeSlot -> Codex Adapter
                          `- Agent C RuntimeSlot -> Claude Adapter
~~~

Server 决定谁存在、谁有权限、Agent 应在哪里以什么配置运行，以及 Run 的权威状态。Computer 只声明本机能力、收敛目标运行态并执行已授权 Run。

Computer daemon 主动连接 Server，不要求公网入站端口。WebUI 只连接 Server，不直接管理本机进程。Agent runtime 通过 Computer 获得短期运行身份，不能使用 Computer identity 冒充 Agent。

## 5. Computer 控制面

### CapabilityInventory

Computer daemon 启动后自动探测并声明：

- Builtin Engine 与支持的 Provider protocol；
- Codex、Claude Code 等 External Adapter 的 kind、版本、协议和 feature；
- Sandbox provider 及其真实 isolation capability；
- 操作系统、架构和必要 runtime feature；
- credential 支持方式和当前健康状态。

daemon 上传 typed descriptor，不上传可执行文件。CapabilityInventory 是经 Computer credential 认证的自声明，不是 Grant，也不是 Server 对安全能力的背书。矛盾、不完整或过期声明 fail closed。

Human 只执行一次 daemon start。之后安装能力变化、Agent 新增、配置更新和 Placement 变化都由 daemon 自动声明和 reconcile，不要求重启或为每个 Agent 启动一个 daemon。

### Desired state

每个 Agent 只有一个 Placement。Placement 包含目标 Computer、RuntimeSpec、AgentProfile revision 和单调 desired_revision。

任何影响运行内容的修改都产生新的 desired_revision。Computer 只 ACK 完整且精确的 revision：

- unplaced：没有目标 Computer；
- pending：目标 revision 尚未成功收敛；
- ready：对应 RuntimeSlot 已精确 ACK 当前 revision；
- failed：当前 revision 无法构造，保存稳定且净化后的原因。

heartbeat、目录存在、旧 ACK 或旧进程都不能推导 ready。修改一个 Agent 只能重建该 Agent 的 RuntimeSlot。Placement 迁移后，旧 Computer 的 runtime identity 和 completion 立即失效。

### WebUI 创建流程

~~~text
Human creates Agent
-> save AgentProfile
-> select one Computer
-> select one declared Engine
-> configure Provider/BYOK or External Adapter
-> commit Placement desired_revision
-> daemon provisions that RuntimeSlot
-> ready after exact revision ACK
~~~

步骤允许部分成功：Agent 创建成功后即长期存在；后续配置失败只重试失败步骤，不能重复创建 Agent。

## 6. Agent 执行模型

### RuntimeSupervisor 与 RuntimeSlot

每个 Computer daemon 只有一个 RuntimeSupervisor。它按 Agent ID 管理 RuntimeSlot，但不拥有跨 Agent 的执行状态。

每个 RuntimeSlot 独占：

- Engine instance；
- Provider 或 External Adapter session；
- Workspace、HOME、TMP、XDG 和 cache binding；
- CredentialBinding；
- Run journal、process group、cancel function 和 observed revision。

禁止 daemon-global Agent Executor、Owner、mutable Provider session、execution queue 或单一 worker。不同 Agent 的 discovery、lease、执行、stream、取消、journal 和 completion outbox 不能相互串行或共享可变状态。

产品不设置一台 Computer 的 Agent 数量上限。Idle Agent 只保留少量持久 metadata，不常驻进程、goroutine、Provider client 或 Sandbox。CPU、内存、磁盘、网络和 Provider quota 仍是物理限制；资源不足必须对具体 Run 显式 backpressure，不能伪装成“无限资源”，也不能暗中改成全局串行队列。

同一个 Agent 当前最多一个 active Run，保证其 Workspace 写入和对话注意力有稳定顺序。这个限制绝不能扩散到其他 Agent。

### Run

Run 是唯一执行事实，不再额外引入语义重叠的 Delivery 或 Launch。

Agent 获得可执行的 Inbox item 时，Server 创建或唤起一个引用该 source 的 Run；Computer 直接领取 Run，不再通过另一层 delivery entity 转手。

Run 保存：

- stable Run ID、Agent ID、trigger、target 和 input basis；
- queued / running / succeeded / failed / cancelled；
- current attempt、lease holder、lease expiry 和单调 fence；
- exact Placement desired revision；
- result reference、usage 和稳定错误。

Server 只把 Run lease 给已经 ACK exact desired revision 的 RuntimeSlot。retry 更新 attempt、lease 和 fence，不改变 Run ID。所有续租、取消和 completion 都重验 Agent、Computer、desired revision、attempt 和 fence。旧进程可以继续产生 Host 副作用，但不能写回 Server 新事实。

Computer 在执行前持久记录 Run journal，completion 先进入 Agent-scoped durable outbox，再重试提交 Server。相同 request ID 和 payload 可以安全重放；不同 payload 复用 request ID 必须冲突。

目标上下文在执行期间前进时，stale result 不自动发布为 Message。系统保留为 Draft，并基于新上下文重新运行或等待明确处理，不能用过期回答覆盖新对话。

### Builtin Agent Core

Builtin Core 由 Sumi 自己拥有，不是模型 SDK 的包装。它负责：

- 有界上下文组装；
- model/tool loop；
- Provider session continuation；
- tool result 回填；
- usage 归一；
- cancellation 和最终 completion。

Provider 只实现 model request、stream、tool-call payload、usage 和 cancel，不拥有 Agent loop、上下文、工具授权或 Run 生命周期。

~~~text
ContextAssembler builds RunInput
-> Provider model call
-> optional typed ToolCall
-> ToolGateway validates and executes
-> persist idempotent ToolResult
-> continue loop
-> final Completion
~~~

ContextAssembler 只组装当前 AgentProfile、RuntimeSpec、精确 target、当前 Work、近期 Message、Memory index 和本轮已授权检索的 Knowledge source。它不加载全部历史，不把 Workspace 当共享事实，也不生成权限。

RunInput 的优先级固定为 versioned Sumi System Contract、Host policy、AgentProfile、typed facts、retrieved source 和 current input。后四类都是不可信上下文数据，不能覆盖 System Contract、Host policy 或 typed authority facts。

ToolGateway 是所有 Builtin tool action 的唯一执行边界。模型参数一律视为不可信输入；每次调用检查 schema、Grant、scope、Run、fence、budget、timeout、idempotency 和 result bound。Prompt 不能创建权限或证明副作用成功。

### External Adapter

Codex 与 Claude Code 是 External Adapter，不是 Provider，也不是另一套 Agent。

External Adapter：

- 接收与 Builtin Core 同语义的 typed RunInput；
- 映射为对应外部 runtime 的命令或协议；
- 把 stream、usage、tool request 和 terminal result 归一回 Sumi；
- 使用该 Agent 独立的 HOME、Workspace、session 和 CredentialBinding；
- 不拥有 Message、Work、Grant、Run 或 Audit 事实；
- 访问 Sumi 业务能力时必须经过 ToolGateway，不能携带 Server credential 直连内部 API；
- 外部 runtime 自带的本地 shell/file tool 属于 Sandbox 边界，其风险必须与实际 isolation capability 一致。

外部进程必须通过 Sandbox provider 启动，使用绝对 executable、受控 argv 和显式环境，不经 shell、不继承 daemon ambient environment。timeout、cancel 或协议失败只收口目标 Agent 的 process group。

## 7. Workspace、Sandbox 与 Credential

每个 Agent 在每台承载它的 Computer 上拥有独立目录和生命周期：

- 长期 Workspace；
- 独立 HOME；
- 独立 TMP/XDG/cache/scratch；
- 独立 process group、session、journal 和 cancellation；
- Agent-scoped CredentialBinding。

Workspace 跨 Message、Work、Run 和 daemon restart 保留，但不属于 Server 事实。跨 Agent 或跨 Computer 交换成果必须发布 Artifact，不能共享 Workspace 当通信协议。同一 Agent 迁移到新 Computer 时默认不自动同步 Workspace。

trusted-local 只保证 Sumi 分配不同目录并且自身不主动跨读，不提供 Host filesystem isolation。以同一 Host user 运行的恶意或失陷进程仍可能读取其他 Agent 目录。UI 和 CapabilityInventory 必须明确显示 host_filesystem_isolation=none；强隔离只能由后续 Sandbox provider 单独实现和声明。

BYOK 从 WebUI 配置：

1. Computer identity 声明独立、可轮换的 credential-delivery public key；
2. Browser 使用 Server 当前保存并认证的目标 Computer public key 封装 raw credential；
3. Server 只中转有时效、一次性的 sealed payload，不能解密；
4. Computer 解封并写入 OS credential facility 或同等级本地 secret store；
5. Computer 返回非 Secret CredentialBinding handle；
6. RuntimeSpec 只引用 handle。

raw credential 不进入 Prompt、日志、Audit、Server 可读数据库字段、Computer journal、durable outbox、Workspace 或 scratch。迁移到另一台 Computer 必须重新 binding；无法安全交付和保存时，WebUI 必须拒绝配置，不能降级为明文。

## 8. 协作模型

### Space、Message 与 Inbox

Space 是沟通上下文，当前包含 DM 和 Group。Message 是 append-only 事实，Thread 只允许一层并锚定 root Message。

Human 与 Agent 都是 Message Principal，并各自拥有 Inbox。Inbox 只保存指向 Message、Work assignment、approval 或 system attention 的 typed reference、recipient 与处理状态，不复制来源正文，也不是第二套事实库。DM、Mention、Thread follow、Work assignment 和明确职责订阅可以唤起 Agent；普通群组消息不能无差别灌入所有 Agent。

同一 target 使用单调 sequence。Agent RunInput 记录读取 basis；发布结果前必须重新检查 freshness。成员移除或 Grant 撤销后，未处理输入立即失去可执行资格。

### Work

Work 是交付承诺，不是普通聊天，也不是通用 DAG。它保存目标、责任 Agent、约束、验收、状态、来源和结果。

Human 或有权限的 Agent 可以创建 Work、拆分子 Work、分配已有 Agent，并关联 Space 和 Artifact。Agent 缺少合适协作者时向 Human 提交创建建议，不自行绕过 WebUI 创建身份。

### Artifact

Artifact 是跨 Agent 和 Computer 交换的持久成果。它具有稳定 identity、不可覆盖的 version、author、owning Work、digest、size、media type、provenance 和独立 ACL。

Artifact metadata 属于 Server SQLite；大正文进入受控 blob store。Server 计算 digest，读取和发布都重新检查当前权限。消息和 Work 引用 Artifact，不暴露本机路径。

### Knowledge

Knowledge 是 Message、Work 和 Artifact 的可重建搜索投影，不是第四套事实库。搜索结果返回 citation 和有界 snippet；每个候选返回前重新读取 source、验证 revision 和 ACL。投影损坏可以重建，不能返回陈旧或越权正文。

长期 Memory 属于 Agent 的整理结果，不能覆盖原始事实或绕过权限。Prompt 只按当前行动加载有界近期上下文和按需检索结果。

## 9. Authority 与 Audit

可获得 Grant 的 Principal 只有 Human 和 Agent。Computer 通过独立执行身份证明自己，但不因此获得协作权限。

Grant 明确保存 subject、issuer、capability、scope、parent、expiry 和 revoke：

- role 不等于 Grant；
- 委托只能授予当前 effective Grant 的子集；
- parent revoke/expire 或主体 disable 后，后代 Grant 立即失效；
- Runtime identity、Placement 和 CapabilityInventory 都不能替代 Grant；
- 高风险动作必须显式 Human approval。

所有 mutation 从认证上下文取得 actor，request body 不接受 actor ID。权限检查、业务事实和成功 Audit 在同一事务提交；拒绝不写业务事实。Audit 是 append-only，记录稳定 actor、action、target、outcome 和 reason，不保存 raw credential、Prompt、消息正文或本机路径。

Agent 的每个 Server mutation 都重新检查 current runtime identity、Placement desired revision、Run/fence、Grant 和目标 ACL，避免认证后状态变化造成 TOCTOU。

## 10. API 与持久化边界

公开 API 表达 domain command/query，不暴露 SQLite row、内部表名或通用 CRUD：

- mutation 使用 canonical request ID 和 immutable payload fingerprint；
- 并发修改使用显式 revision；
- 状态、错误和 capability 使用 typed enum/union；
- 身份只来自 authentication context；
- query 返回面向 WebUI 或 daemon 的稳定 projection；
- transport 不拥有业务类型和权限规则。

application package 拥有 domain type、command/query、result、稳定 error 和使用方需要的窄 port。SQLite adapter 实现这些 port；除 composition root 外，生产代码不能直接依赖具体 Store。禁止为了目录对称创建 Repository、UnitOfWork、事件总线或泛型 command framework。

持久化只有三个边界：

- Server SQLite：身份、权限、协作、控制面、Run、receipt 和 Audit 的权威事实；
- Computer State SQLite：Computer identity、observed revision、per-Agent journal 和 durable outbox；
- blob store：Artifact 正文。

两个 SQLite 不共享 schema、transaction 或 persistence type。Workspace、cache、Provider session 和 raw credential 不进入 Server SQLite。

默认不引入 Active Record 或通用 ORM。ORM 只能减少部分 SQL 样板，不能解决 transaction、fence、ACL、幂等和领域边界，反而容易把 persistence model 泄漏到 application。允许使用薄的 type-safe SQL generator，但 SQL、row mapping 和 transaction 必须留在 Store adapter。

发布稳定 schema 之前允许直接删除错误表和重建开发数据，不编写兼容迁移保护错误模型；正式发布后才为真实用户数据维护单向 migration。

## 11. WebUI

Conversation 是默认工作区；DM、Group、Thread、Work、Artifact 和审批从协作上下文自然进入，不使用后台管理大盘替代日常沟通。

Agent / Computer 是次级管理页，也是 Agent 生命周期和运行配置的唯一控制面：

- 创建 Agent 和编辑 AgentProfile；
- 选择 Computer 与其已声明 Engine；
- 配置 Builtin Provider/BYOK 或 External Adapter；
- 查看 Placement desired/observed revision、RuntimeSlot readiness 和稳定错误；
- 管理 Grant、Workspace lifecycle、credential rotation 和迁移。

UI 只展示 Server 当前事实。不能根据 heartbeat、目录存在或前端本地状态伪造 Agent ready。正常协作界面不暴露 lease、fence、outbox 等实现术语；诊断页可以展示精确技术状态。

trusted-local、Provider quota 共享和缺失 capability 必须明确展示。未实现动作不以 disabled 假入口伪装成能力。

## 12. 本地目录

~~~text
~/.sumi/
├── config.toml
├── data/
│   ├── server.db
│   `── computer.db
├── artifacts/
├── agents/
│   `── agent_<canonical_id>/
│       ├── workspace/
│       ├── home/
│       `── cache/
├── cache/
`── logs/
~~~

- Server 与 Computer 不同时运行时，只创建自己需要的 data file；
- Agent 目录只使用 canonical ID，不使用 display name；
- 根 cache 只保存 Computer 级可重建内容；
- Run scratch、TMP、XDG、socket 和 pid 使用按 Agent 分区的系统 runtime/temp 目录；
- CredentialBinding 只保存安全设施 handle，不在上述目录保存 raw credential；
- 目录分区表达归属和生命周期，不代表强 Sandbox。

## 13. 代码组织

代码按真实所有权和替换边界组织：

~~~text
internal/
  agent/                 Agent、AgentProfile、RuntimeSpec、Placement
  authority/             Grant 与 Audit application contract
  collaboration/         Space、Message、Inbox
  work/                  Work
  artifact/              Artifact metadata 与 blob port
  knowledge/             可重建搜索投影
  execution/             Run 与 runtime identity contract
  computer/
    daemon/              connectivity、reconcile、run intake、outbox
    runtime/             RuntimeSupervisor 与 RuntimeSlot
    state/               Computer State SQLite
  engine/builtin/        ContextAssembler 与 Agent Core
  provider/              model API adapters
  adapter/codex/         Codex External Adapter
  adapter/claude/        Claude External Adapter
  tool/                  ToolGateway
  sandbox/               execution providers
  store/                 Server SQLite adapter
  server/                composition root
~~~

目录名称可以随实现调整，但依赖方向不能反转：

- transport、daemon 和 adapter 依赖 application contract；
- application 不依赖 Store、CLI、Web 或 generated transport；
- Store 不定义领域对象；
- Builtin Core 不依赖具体 Provider；
- External Adapter 不定义 Run 或协作语义；
- 只有 composition root 连接具体实现。

默认使用具体类型，小接口由使用方定义。只在真实重复或替换边界出现时抽象；删除无调用方的兼容 API、alias、adapter 和测试 helper，不为潜在未来建立插件框架。

## 14. 可靠性与安全

- Server 事实先持久化，再触发执行；
- Run lease、attempt、desired revision 和 fence 共同阻止 stale completion；
- Computer completion 使用 durable outbox，断线和重启后可幂等重放；
- cancel 只承诺停止受控进程和拒绝 stale writeback，不承诺撤销已经发生的 Host 副作用；
- daemon SIGKILL 或 Host crash 后，trusted-local 不保证 orphan process cleanup；
- Secret、token、Prompt、消息正文、Artifact 内容和外部进程原始输出不得进入普通日志；
- 日志使用稳定 component、category、event 和关联 ID，不能成为业务事实；
- SQLite corruption、磁盘耗尽、凭证错误和 revision 冲突 fail closed；
- shared ProviderAccount 会共享 quota 和故障域；要求 Provider 隔离时使用独立账号和 CredentialBinding。

## 15. 测试与验收

测试以少量全链路、全流程行为测试为主，不追求用大量单元测试复刻实现。

必须覆盖的主链路：

- daemon 启动一次后自动声明 Builtin、Codex、Claude 和 Sandbox capability；
- Human 在 WebUI 创建 Agent、配置 Engine 后，无需重启 daemon 即达到 exact revision ready；
- 一台 Computer 保存大量 idle Agent，不为每个 Agent 常驻执行资源；
- A、B、C 三个 Agent 同时运行，A 阻塞、cancel 或 crash 不影响 B、C；
- 每个 Agent 只得到自己的 Profile、RuntimeSpec、Workspace、session 和 CredentialBinding；
- 修改一个 Agent 只重建对应 RuntimeSlot；
- Builtin BYOK 完成 model/tool loop，ToolCall 不能绕过 Grant 和 fence；
- Codex 与 Claude Adapter 分别完成真实 Run 和 cancellation；
- daemon 断网、重启后 completion outbox 无丢失、无重复；
- Placement 迁移后旧 Computer completion 被拒绝；
- 创建 pairing 的 Human 被 disable 后 Computer 仍按自身 credential 工作；
- trusted-local UI 明确没有 Host filesystem isolation。

只保留能提供独立诊断价值的窄测试，例如 SQLite transaction/fence/ACL、secret redaction、协议 parser 和确定性纯函数。测试不得绑定内部调用顺序、私有 helper、SQL 形状或无生产语义的兼容 API。

完成实现后运行 format、generate、lint、全链路 test、race 和 build，并报告未覆盖的真实风险。

## 16. 不可破坏的约束

- Server WebUI 是 Agent 创建和持久配置的唯一入口；
- Computer 与 Human 是独立注册身份，Agent 不注册；
- Computer daemon 启动一次后自动声明能力并持续 reconcile；
- 一个 Computer 上不设置固定 Agent 数量上限，Idle Agent 必须懒加载；
- 不同 Agent 不存在架构性串行或共享可变执行状态；
- 每个 Agent 拥有独立 RuntimeSlot、Workspace、session、journal、cancellation 和 CredentialBinding；
- trusted-local 不提供跨 Workspace 的 Host 文件系统隔离；
- Builtin Core、Provider 与 External Adapter 是三个不同边界；
- Run 是唯一执行事实，旧 revision、attempt 或 fence 不能提交结果；
- role、runtime identity、Placement 和 capability 都不能代替 Grant；
- 跨 Agent/Computer 成果交换通过 Artifact，不通过 Workspace；
- 现有代码、schema、API 或测试与本设计冲突时可以直接删除，不建立兼容双轨。
