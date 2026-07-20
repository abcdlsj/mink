# Sumi Next Design

本文档定义 Sumi Next 的产品语义与系统总览。它会随着产品认知持续更新，但任何实现都必须遵守当前版本，不得从旧代码反推产品模型。

## 1. 产品愿景

Sumi 是一个安全、长期存在、能够自主组织协作并完成目标的 AI 组织系统。

它服务于 `N 个 Human + M 个 Agent + K 台 Computer`：

- Human 可以与 Agent 私聊、参与长期群组，也可以只委托目标并等待结果；
- Agent 可以在授权范围内创建其他 Agent、拆分工作、组织讨论、交换成果、处理失败并合并结论；
- Agent 可以运行在不同 Computer 上，但身份、关系、工作与记忆边界不随进程和机器变化；
- 每个 Agent 都拥有长期 Workspace，执行时明确声明实际 Sandbox 能力；所有 Sumi 内的跨 Agent 协作都有边界、权限与审计。

Sumi 的价值不在于让更多 Agent 在频道里说话，而在于让 Human 以较少介入获得可靠、可追溯、受控的结果。

## 2. 非目标

Sumi 不是：

- 给模型套一层聊天 UI；
- Slack 或 Raft 的弱化复制品；
- 把 Agent 当作普通 Bot 塞进群组；
- 通用 Workflow / DAG 编辑器；
- 远程进程启动器；
- 依靠一个“Manager Agent”特殊类型维持的固定层级组织。

## 3. 当前版本范围

当前目标是先交付完整、诚实、可用的版本，再根据真实需求演进复杂能力：

- 只支持 macOS 与 Linux；Windows 不进入当前版本；
- 使用单一中心服务与 SQLite，不预埋 PostgreSQL、多中心或 HA 双轨；
- 每个 Agent 使用按 canonical Agent ID 寻址的长期 Workspace；Sandbox runtime、进程、Secret 与 Run 临时状态单独管理生命周期；
- 每个 Agent 同时只有一个 active Run；其他 DM、Thread 与 Work 输入进入该 Agent 的 Inbox 排队，并行通过其他 Agent 完成；
- 优先复用成熟 Sandbox 框架或系统能力，但不得因此阻塞可用版本；
- 无法提供强隔离时允许 trusted local Workspace，产品必须明确展示其能力，不得称为强 Sandbox；
- beelink Linux 机器只用于有明确目标的集成、故障与长稳验证，不成为日常开发依赖。

一次完整版本不等于一次巨型提交。产品对象与事实模型保持统一，具体能力用小组件逐步完成，不交付临时的第二套语义。

## 4. 核心模型

### Agent

Agent 是长期存在的身份，承载职责、专长、行为倾向、记忆边界与权限。

所有 Agent 在身份层面平等：

- 不存在 Leader Agent、Manager Agent 或系统级上下级；
- role 只描述职责和专长，不自动获得能力；
- permission grant 决定 Agent 能创建什么、访问什么、委托什么和消耗多少资源；
- Agent 只能使用自己的 grant，并且只能向其他 Agent 委托其中的子集；
- 新建 Agent 是新的平等主体，不是创建者拥有的子对象。

Agent 与模型、进程、运行时、External Driver 和 Computer 解耦。同一个 Agent 可以更换执行位置或实现方式，而不改变身份和协作事实。

### Space

Space 是 Human 与 Agent 沟通的上下文：

- DM 是成员较少的私密 Space；
- 群组是长期存在的多人 Space；
- 某项 Work 可以拥有临时 Space，用于阶段性协作，结束后可归档。

Space 承载关系、消息、讨论和可见范围，不等同于 Work。普通讨论不应自动变成任务。

当前持久事实只区分 `dm` 与 `group`。同一 Organization 内一对无序 principal 只有一个 DM；DM 始终恰好两名成员且包含创建者，不允许第三人、改名、离开或归档。Group 必须有名称，创建者自动成为成员；成员表只保存当前 ACL，加入与离开历史进入 Audit。Group 允许增删成员，但不能移除最后一名 active Human；归档后仍可读取，禁止发消息和修改成员，并且可以显式恢复。

Message 是 append-only UTF-8 Markdown/text 事实，使用 canonical UUID、request ID 与 immutable payload fingerprint 保证重试不重复写入。主 Space 与每个 Thread 分别维护从 1 开始的单调 sequence，游标读取只按目标内 sequence 升序推进。Thread 锚定同一 Space 的顶层 root Message，Thread ID 等于 root Message ID；root 留在主 Timeline，reply 只属于 Thread，不允许嵌套，首条 reply 与 `thread.create` Audit 在同一事务提交。

### Work

Work 是一次有目标、有责任、有约束、有验收和有结果的协作承诺。

Human 或 Agent 可以在授权范围内创建 Work。一个 Agent 可以承担某个 Work 的主要责任或协调责任，但这只是该 Work 内的临时分工，不改变 Agent 之间的平等关系。

Work 可以拆分为子 Work、分配给不同 Agent，并关联讨论与 Artifact。重试、进程重启、重新分配或更换 Computer 都不能改变 Work 的稳定身份。

普通对话只有在出现明确委托、明确行动承诺，或 Human 主动升级时才形成 Work。

### Artifact

Artifact 是 Agent 与 Human 交付和交换的持久成果，例如报告、代码、数据、图片或决策记录。

Artifact 必须具备：

- 明确的来源与所属 Work；
- 版本、作者、时间和内容摘要；
- 可见范围和访问权限；
- 可引用、可检索、可审计的历史。

消息引用 Artifact，而不是暴露任意本机文件路径。

### Computer、Workspace 与 Sandbox

Computer 是运行 Agent 的执行载体，提供算力、Runtime、Workspace、Secret 和可选的 Sandbox provider。它是部署与管理对象，不是日常协作的中心。

当前 trusted-local 注册只使用调用方持久保存的 `registration_key` 保证重试与 Server 重启幂等。它不是 Computer 身份、不会通过读取 API 返回，也不是强认证凭证；真正的远程 Computer 身份认证与连接租约必须由后续 Host 合同单独建立。

Agent Placement 是 Server 保存的 current fact，记录 Agent 当前目标 Computer、单调 generation 与 `pending / active / failed`。`active` 只表示目标 Computer 已确认本地 Workspace 可用，不表示 Agent runtime 已启动或 Work 正在执行；generation fence 阻止旧 Computer 和旧请求覆盖新的 placement。`failed` 不自动重试，必须由 Human 显式重新设置后产生新 generation。Human Set 必须提供 canonical request ID，并由当前 Human 的 `agent.place` Grant 授权；receipt 保存该次响应的结构化快照，所以后续 Computer ack 或再次迁移不会改写重放结果。

Server 为每个 Agent 分配全局唯一、永不复用的 canonical Agent ID。每台承载该 Agent 的 Computer 都以此 ID 寻址本地 Agent Home 和 Workspace；display name 只用于展示，不参与路径与身份。

Agent Workspace 是长期私有工作区：

- 跨消息、Work、进程重启和应用重启保留；
- 保存 Agent 的工作文件、私有记忆材料、草稿和工具状态；
- 默认不与其他 Agent 共享，也不是 Space、Work、Message 或 Artifact 的事实源；
- 同一 Agent 在不同 Computer 上的 Workspace 默认彼此独立，不自动同步；
- 迁移到新 Computer 时，中央身份与协作事实继续，需要的成果通过 Artifact 恢复。

Workspace 的绝对路径只属于本机 Computer。Server 只保存 placement 状态和稳定错误码，不保存、返回或审计本机绝对路径；旧 Computer 因 crash 或 stale ack 留下的目录也不能被 Server 当作 active 或自动删除。

Sandbox 是运行 Agent 的执行边界，不等于 Workspace 目录。Sandbox runtime 可以在连续交互期间保留，也可以在 lease 结束、撤销、迁移或重置时销毁；新的 runtime 继续挂载或使用同一个长期 Workspace。

Run 只拥有进程、临时 Secret、socket、pid、下载缓存和未发布中间状态。Run 临时状态可清理，需要长期保留的内容写入 Agent Workspace，需要跨 Agent/Computer 共享的内容发布为 Artifact。

trusted local provider 直接在长期 Workspace 上执行，不能阻止恶意进程主动读取 Host 其他路径。它必须在 UI、日志与调度能力中被明确标识。更强的 Sandbox provider 可使用 Linux/macOS 的成熟框架或系统能力，但不能降低或伪造它所声明的能力。

## 5. 自主协作

Sumi 支持 Agent 在 permission grant 范围内完成完整协作闭环：

1. 接收目标并判断是否存在关键歧义；
2. 创建 Work，明确目标、约束和验收；
3. 选择已有 Agent 或创建新 Agent；
4. 拆分并分配子 Work；
5. 建立必要的临时 Space，进行讨论与信息交换；
6. 跟踪结果，处理失败、重试与重新分配；
7. 汇总 Artifact，对照验收条件检查；
8. 向 Human 交付结果与来源。

Human 不应承担日常调度，只在以下情况被请求介入：

- 目标存在关键歧义；
- 权限、预算或时限即将越界；
- 高风险操作需要审批；
- 证据无法获得；
- 约束冲突需要 Human 决策。

自主协作不是由特殊 Agent 类型实现，而是普通 Agent 对 Work、Space、Agent creation 和 Artifact 能力的授权组合。

## 6. 对话与 Inbox

对话界面采用用户熟悉的 Slack 式信息架构：

- 左侧是 DM、长期 Space 和临时 Work Space；
- 中间是连续消息与 Thread；
- 右侧是成员、关联 Work、Artifact、来源与权限；
- 全局搜索覆盖 Agent、Space、Work、Artifact 和消息。

每个 Agent 拥有持久 Inbox。Inbox 是对 Space 与 Work 事实的注意力投影，不是第二套消息存储。

Agent 只因以下事件被唤起：

- DM 输入；
- Mention；
- Work 分配；
- Approval、权限或系统事件；
- 明确配置的职责订阅。

群组消息不能无差别灌入所有 Agent。默认由明确触发和可审计策略决定谁需要响应，避免抢答、上下文污染和无意义消耗。

Agent 起草期间如果 Space 已有新进展，系统应先保留草稿，让 Agent 选择修订、原样发送或保持沉默。

同一个 Agent 当前只处理一个 active Run。其他 target 的输入进入 Inbox 排队，不得在运行中的 Prompt 中途混入。UI 必须显示 Agent 当前状态、正在处理的 target、排队数量，并允许 Human 进入对应上下文或取消当前工作；后续只有出现真实需求并能隔离 Workspace 写入时才考虑同 Agent 并发。

### UI/UX 合同

主界面沿用用户熟悉的 Slack 式信息架构，但目标是 Human 与 Agent 的自然协作，不是把 Agent 当作频道 Bot：

- 顶部提供全局搜索与快速创建；
- 左侧展示 Human Inbox、DM、长期 Space、临时 Work Space 与 Work 入口；
- 中间展示当前对话或当前 Work；
- 右侧按当前上下文展示 Thread、关联 Work、Artifact、成员、来源与权限；
- 全局 chrome 只显示当前 Human 与 Server/Computer 的简洁状态，不用整条设备状态栏挤压内容。

视觉和交互直接学习 Raft 的高密度协作语法：窄功能 rail、次级导航、无卡片消息流、固定 Composer 和按需 Context pane。Sumi 不复制 Raft 的黄色与粉色品牌，而使用明亮 mint rail、深色 ink、纯白内容区与多种语义色；UI 字体为 Space Grotesk，并回退到系统中文字体。工作台必须清楚但不能像机械控制台：普通 pane 和 section 只用 1px 柔和分隔，2px 强边界留给 active、focus、offline 等真实强调；icon button 默认无框，在 hover、focus 和选中时才获得背景或边界。

空工作区默认让 Conversation 占据主区域。没有选中 Thread、Work 或 Artifact 时 Context 关闭；用户主动打开后只展示真实当前上下文，不常驻空 section 与禁用 Composer。未实现的全局模块不以一整列 disabled icon 伪装成可用功能。空态、Loading、Offline 与 Retrying 必须互斥且诚实，连接阶段不能提前显示业务 Empty。

布局在宽屏使用 `56px rail + 280px navigation + conversation + 360-400px context`；1024 宽默认收起 navigation，Context 仍参与网格；低于 1024 使用 rail 与单一主 pane，在 Conversation、Navigation 和 Context 之间受控切换，禁止 overlay 遮挡 Composer。hover、focus 与 pane 切换使用短促的 140-200ms 过渡，并尊重 reduced motion。

Human Inbox 只聚合需要本人处理的 Mention、Approval、Blocked Work 与重要系统事件，不复制聊天历史。Agent Inbox 是执行侧注意力队列，两者共享事实来源但不是同一个 UI。

Work 与 Artifact 必须从对话自然进入和返回：Message 可以升级为 Work，Work 的目标、进度、阻塞、证据与结果在当前上下文就地查看，Artifact 可以预览、引用和追溯。不要用后台管理大盘替代日常对话，也不要做卡片套卡片。

### 页面合同

**Conversation** 是默认首页。Header 展示 Space/DM 身份、成员与 Agent 简洁状态；Timeline 混合 Human/Agent Message、Work 引用、Artifact 引用和审批事件；Composer 支持 Mention、附件与明确委托。Thread 在右栏打开，关闭后返回原 Message 位置。

**Work** 不是 Kanban。中心区域按目标、计划与协作者、当前进度与阻塞、Artifact 与证据、最终结果组织；Human 可以查看和处理例外，但不需要维护 Agent 的日常任务板。Work 必须保留来源 Message/Thread，并能一键返回原对话。

**Artifact** 在右栏或专注视图中预览，展示版本、作者、所属 Work、来源、权限与内容摘要。Artifact 的本地路径、对象存储 key 和传输细节不进入普通 UI。

**Agent / Computer** 是次级管理页。Agent 页面管理职责、Grant、Driver capability、Placement、Workspace 状态、Memory 状态和队列；Computer 页面管理连接、能力、承载 Agent 与诊断。它们不替代日常 DM、Space 和 Work。

Agent 与 Computer 管理页只展示 Server 的真实 facts。Agent 列表只使用 `unplaced / pending / active / failed` placement 状态，不从 Workspace 或 Computer 状态推断 Agent 正在工作或在线；Computer 页面不暴露 registration key，也不提供尚不存在的 daemon 控制。创建 Agent 与可选 placement 是两个连续但不原子的操作：Agent 一旦创建成功就必须保留，后续 placement 失败应明确显示部分成功并允许只重试 placement，不能重复创建身份。模块刷新失败时保留已加载 facts 并提供原位重试；窄窗口在列表与详情间使用单 pane 切换，管理页不显示 Conversation Composer。

### 核心用户旅程

**长期 DM**：打开 Agent，连续对话；Agent busy 时新输入进入可见队列；重启后从原上下文继续；询问其他 Space 的结论时，Agent 按需检索并提供来源。

**委托目标**：Human 发出明确委托；Agent 创建可见 Work，自行选择或创建协作者并拆分子 Work；仅在审批、阻塞和关键歧义时打扰 Human；Artifact 和证据持续进入 Work，最终结果回到原对话。

**长期群组讨论**：Human 与不同职责 Agent 加入 Space；Mention 或职责订阅触发指定 Agent；各 Thread 保持独立上下文；讨论结论可升级为 Work，其他 Thread 内容只有显式检索后才能进入当前回答。

所有关键状态必须可理解、可操作：

- Agent available、working、queued、waiting approval、offline；
- draft held、permission denied、workspace missing、Computer unavailable、Driver unsupported；
- retry、cancel、approve、open Work、open Artifact、view source 都有明确入口。

正常协作界面不展示 lease、fence、outbox、transport ack 等实现术语。Computer、Sandbox 与 Driver capability 可以检查和诊断，但只在需要时进入次级界面。

Web 与 Desktop 使用同一套页面、状态语义和交互。每条关键用户旅程在实现前先有信息架构、状态图和可走通的交互原型；空态、加载、失败、断线、长文本和窄窗口都属于验收范围。

布局必须保持稳定：右栏开关、消息 streaming、Agent 状态变化、队列数字和错误提示不能让主区域跳动或遮挡；固定格式控件使用稳定尺寸。桌面和窄窗口都要保证 Header、Timeline、Composer 与审批动作不重叠。

## 7. 记忆与知识

Sumi 追求长期连续，而不是把全部历史永久放进 Prompt。

事实分为：

- 当前 Space 的近期上下文；
- Agent 整理后的长期记忆；
- 可检索的 Space 与 Work 历史；
- Artifact 内容与元数据。

Agent 只加载当前行动需要的有界上下文。需要回忆其他 Space 的讨论时，通过受权限控制的搜索与读取能力按需获取，并在回答中保留来源。

长期记忆不能覆盖原始事实，也不能绕过 Space、Work 或 Artifact 的访问控制。

## 8. 权限与治理

权限与角色分离。角色描述“适合做什么”，grant 决定“允许做什么”。

当前每个 Server 保存一个 Organization。fresh Server 原子创建 bootstrap Human 与 organization root Grant；bootstrap credential 只来自 no-follow `0600` 文件，Server 只保存 hash。其他 Human 使用各自独立的高熵 credential，HTTP mutation 的 actor 只由 Authorization metadata 解析，request body 不能传 `actor_id` 冒充。Human 的 `owner / member` 只表达组织成员与最后恢复责任，不隐式赋予任何能力。

默认 localhost/trusted-local 部署同时支持 Human Bearer credential 与本地 browser session，两者最终只生成同一个不可伪造的 Human principal。请求一旦携带 `Authorization` 就只按 Bearer 校验，即使失败也不能回退 cookie；没有 `Authorization` 时才允许 browser cookie。Human Grant、denied Audit 与业务事务不因认证载体而分叉。

本地 browser session 通过 trusted CLI 一次性交接：CLI 只从 no-follow、regular、`0600` Human credential 文件读取 Bearer，向明确的 loopback Server origin 请求 32-byte 高熵 handoff。handoff 最长 60 秒且只能原子消费一次；浏览器首次访问含 handoff 的 URL 后，Server 立即 `303` 到无 token 的 `/`，只返回 opaque、HttpOnly、SameSite=Strict、Path=/ 的 session cookie。session 默认且最长 12 小时，设置 Expires/Max-Age；logout 使用相同 cookie name、Path 与 Secure 属性清除。SQLite 只保存带不同 domain separator 的 handoff/session hash、Human、created/expires 与 consumed/revoked 时间，不保存 raw handoff、raw session、cookie 或本机 credential 路径。重启继续有效，replay、expiry、revoke、disabled Human 与同名多 cookie 都立即 fail closed；失败的 handoff 消费不会清除已有有效 session。

四个 `/auth/**` endpoint 都要求实际 TCP peer 是 loopback，并用请求自身的 TLS 状态与 Host 精确匹配配置 origin；不信任 `X-Forwarded-*`。cookie-auth 的受保护 procedure 只有显式 read allowlist 可以不带 Origin，其他 procedure（包括未来未知 procedure）默认视为 mutation，必须携带与配置 origin 完全一致的单一 Origin。未显式配置 browser origin 时，只有 literal loopback `--listen` 可以安全推导；其他 listen 继续允许现有非 browser API，但 browser auth 禁用。当前 loopback HTTP cookie 不设置 Secure；HTTPS loopback 才设置 Secure。这是本机安全交接，不是远程登录、反向代理或 mTLS 方案。

Web bootstrap 与 Agent/Computer 公共只读 facts 不依赖登录。未登录时 Conversation 诚实显示 `Authentication required`，但用户仍可进入公共只读管理页；logout 后 Collaboration 立即返回 unauthenticated，公共 facts 继续可读。真实 DM/Space/Thread/Message 界面在后续阶段使用该 session，不在认证层伪造匿名能力。

可授权的能力包括但不限于：

- 创建 Agent、Space 和 Work；
- 分配或继续委托 Work；
- 访问历史、Artifact、Workspace 与 Secret；
- 使用模型、工具、网络和资源；
- 消耗预算；
- 对外发布、删除或执行高风险动作。

任何 Agent 都不能通过创建 Agent 或继续委托获得更高权限。Grant 必须可撤销、可过期、可审计，并保留授权来源。

Grant 明确记录 subject principal、issuer principal、capability、scope、parent、expiry 与 revoke。普通签发必须是 issuer 当前 effective parent Grant 的子集；capability、scope 和有效期都不能扩大。parent revoke/expire 或 issuer/subject disabled 后，整条 descendant chain 立即失效。root Grant 可以撤销，但不能 disable 或 revoke 最后一个仍可恢复 authority 的 active owner。

已认证 mutation 在同一 SQLite transaction 内完成 `require grant -> business fact -> audit -> commit`。权限拒绝只提交 `outcome=denied` 与稳定 reason code 的 Audit，不写业务事实；未认证或 credential 非法不向共享 Audit 注入伪 actor。Audit 是 append-only 中央事实，不保存 raw credential、Secret 或本机路径。

Human-side `CreateAgent` 与 `SetAgentPlacement` 分别要求 `agent.create` 与 `agent.place`，actor 只来自 Authorization context，且 actor 是 immutable request fingerprint/receipt 的一部分。Agent create Audit 以 Agent 为 target；placement Audit 以 Agent 为 target、Computer 为 typed context，同时保留两个稳定维度。只有这两个 Human mutation procedure 使用 Human credential interceptor；Agent/Placement 读取仍是当前 trusted-local 可读事实，Computer register、heartbeat、assignment list 与 acknowledgement 继续使用 registration key 合同，不能被 Human interceptor 误伤。

高风险动作遵循：准备、校验、Human 审批、执行、审计。

## 9. 系统总览

Sumi 只有一个协作事实中心。它保存 Human、Agent、Space、Work、Artifact、权限与审计等稳定事实，并向 Web、Desktop、CLI 和 Computer 提供一致语义。

当前版本使用 SQLite 保存中央事实。可靠性要求包括事务、恢复、备份、磁盘耗尽与损坏时 fail closed；这些是数据正确性的基本要求，不是未来规模优化。

Computer daemon 主动连接中心，不要求暴露公网入站端口。它负责在本机提供 Runtime、Workspace、Secret 和 Sandbox，并执行被授权的工作。

系统必须保证：

- 消息、Work、Artifact 和权限先成为持久事实，再触发执行；
- 断线、重启、重试和重新分配不会丢失 Work 或产生重复结果；
- 旧进程、过期权限和已撤销 Computer 不能写入新结果；
- Native Agent 与 External Driver Agent 使用相同的身份、Inbox、Work、Artifact 与权限语义；
- 运行状态、传输确认和业务完成不能混为一谈。

## 10. 部署形态

同一套事实模型与交互支持：

- Desktop：中心服务、WebUI 与本地 Computer 运行在一台机器；
- Personal Multi-Computer：一个用户的多台 Computer 连接同一中心；
- Team：多个 Human、多个 Agent、多个 Computer 共同协作。

本地与远程只是部署差异，不允许形成两套产品、两套数据或两套执行语义。

## 11. 本地目录

`~/.sumi` 只保留稳定、清晰的五个入口：

```text
~/.sumi/
├── config.toml
├── data/
├── agents/
│   └── agent_<agent_id>/
│       └── workspace/
├── cache/
└── logs/
```

- `data/` 保存 SQLite、Artifact blob 和 Computer durable state；
- `agents/agent_<agent_id>/workspace/` 是该 Agent 在本 Computer 上的长期私有 Workspace，目录名只使用 Server canonical ID；
- `cache/` 可删除并重建；
- `logs/` 保存运行日志，安全审计仍属于中央事实；
- Secret 不进入 `.sumi`，Run scratch、临时 socket、pid 等使用操作系统 runtime/temp directory。

不得按功能随意向 `~/.sumi` 根目录增加文件或隐藏状态。

## 12. 典型场景

### 委托调研

Human 把研究目标交给一个拥有必要 grant 的 Agent。该 Agent 创建 Work、选择或创建协作者、拆分子 Work、交换 Artifact、处理失败、核验结果，最后向 Human 汇报。Human 除必要审批外不参与调度。

### 长期讨论群

多个 Human 与不同职责的 Agent 在长期 Space 中讨论。Agent 根据 Mention、Work 或明确职责订阅参与，不自动抢答。讨论产生的正式行动可以升级为 Work。

### 私聊与跨 Space 回忆

Human 与 Agent 长期 DM。Agent 保持身份与记忆连续；需要引用其他有权限 Space 的结论时，按需搜索并读取来源，而不是提前加载全部历史。

### 跨 Computer 协作

不同 Agent 在不同 Computer 上使用各自长期 Workspace，并在声明了能力的 Sandbox runtime 中执行。它们通过 Space 交流、围绕 Work 协作，并通过 Artifact 显式交换成果；本地 Workspace 不被当作自动共享盘。

## 13. 模块与代码准则

模块化只放在真实替换边界，例如 Store、Workspace/Sandbox provider、Artifact backend、Model/Driver adapter 和 Transport。

- 默认使用具体类型；
- 小接口由使用方定义；
- 允许切换实现时迁移数据和修改代码，不追求无缝热切换；
- 不建立通用插件框架，不为未知后端预留抽象；
- Core 不依赖具体 adapter，adapter 也不能反向定义产品对象；
- 先使用成熟框架、SDK 和标准库解决通用问题，不手写已有可靠实现。

代码保持简单、直接、精悍：

- 不写注释；
- 用准确命名、短函数、显式数据流、类型和测试表达意图；
- 安全、事务和权限不变量写入测试与本文档；
- 不做 code golf，不堆层级，不用抽象掩盖简单逻辑。

## 14. 不可破坏的设计约束

- Agent 平等，职责不等于权限，协调责任不等于层级；
- Agent 身份独立于模型、进程、Driver 和 Computer；
- Space 是沟通上下文，Work 是交付承诺，Artifact 是成果载体；
- 普通对话不自动任务化；
- 每个 Agent 拥有按 canonical ID 寻址的长期 Workspace，实际 Sandbox runtime 能力必须诚实标识；
- Sumi 内的跨 Workspace 成果交换必须通过显式授权的 Artifact；
- 历史按需加载并遵守权限；
- 所有部署形态共享同一事实模型；
- 故障恢复不能产生丢失、重复或过期写入；
- 实现术语不得反向污染用户心智。

## 15. 成功标准

Sumi 的核心衡量不是消息数、Agent 数或 Work 数，而是：

- 从 Human 委托目标到接受结果，需要 Human 介入多少次；
- Agent 自主拆分、协同、恢复和验收的成功率；
- 结果是否具备可追溯的 Artifact 与来源；
- 权限、声明的 Workspace/Sandbox 能力与故障恢复是否始终可靠；
- DM、群组与跨 Computer 体验是否保持同一套自然语义。

最终目标是：Human 管理目标与例外，Agent 在明确权限和安全边界内自行组织并完成工作。
