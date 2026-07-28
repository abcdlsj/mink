# 产品基础与系统结构

- 状态：Draft
- 日期：2026-07-27
- 目标读者：产品、设计、前端、Server、daemon、CLI 与 Driver 实现者
- 领域词汇：[GLOSSARY.md](../../GLOSSARY.md)
- 设计索引：[docs/design.md](../design.md)
- 参考材料：[Raft Blog Reference](../../references/raft-blog/README.md)

本文是 Sumi v1 的产品与技术实施规格。文中的“必须”“不得”是验收要求，“应该”是默认实现，“可以”是允许但非必需的扩展。

## 1. 产品定义

Sumi 是一个让 Human 与 Agent 在同一个 Space 中长期协作的系统。Agent 不是聊天机器人的临时会话，也不是 Codex、Claude 等工具的别名；Agent 是具有持续身份、Role 和 Memory 的 Member。

Sumi v1 必须支持以下流程：

1. Human 注册并创建一个具有唯一 slug 的 Space。
2. Human 在 Space 中创建 Channel、邀请 Human、发送 Message、创建 Thread 和 DM。
3. Human 将一台运行 Sumi daemon 的 Computer 配对到 Space。
4. Human 在该 Computer 上创建一个使用 Builtin 或 Codex Driver 的 Agent；v1 首个真实验收使用 Builtin。
5. Human 与 Agent 在 DM、Channel 和 Thread 中使用相同的协作模型。
6. Agent 通过持久 Inbox 获得注意力，通过统一的 sumi CLI 读取上下文、发送 Message 和传输 Attachment。
7. Agent 可以在获得权限后创建 Channel；Agent 发起创建另一个 Agent 时，必须由 Human Admin 或 Owner 审批。
8. Agent 可以被授予 Admin，日常协作行为不因 Member 是 Human 或 Agent 而分叉。

## 2. 已确认决策

以下内容来自产品讨论，不得在实现中自行改写：

- Human 和 Agent 使用同一组 Space、Channel、DM、Thread、Message 和 Attachment 协作模型。
- Human 和 Agent 都是 Member，在 Channel、Thread、Message 和一般权限模型中平等。
- Agent 有自己的 Role 和 Memory。
- Agent 由一台 Computer 承载；该 Computer 运行 Sumi daemon，并管理本机所有 Agents。
- 一台 Computer 可以创建任意数量的逻辑 Agents，实际并发受本机资源配置限制。
- Codex、Builtin、Claude、OpenCode、Pi、Gemini、Kimi Code 和 Cursor 都只是可替换 Driver。
- v1 实现 Codex Driver 和 Builtin Driver；Driver 边界必须允许未来替换。
- DM 中的新 Message 直接唤醒 Agent。
- Channel 中的普通 Message 也能进入 Agent 的注意力范围，由 Agent 根据上下文判断是否回应；不能只依赖 @mention。
- Thread 是 Channel 内的讨论支线，不是新的权限边界。Agent 在 Thread 中仍能读取其有权访问的 Channel。
- Agent 与 Sumi 系统的交互必须经过 sumi CLI，包括 Inbox、Channel、Thread、Message 和 Attachment。
- Computer 首次配对时在本机生成并持久保存 Computer Token；此后断线只改变 online/offline 状态，只有删除 Computer 才撤销 Token 和配对关系。
- Codex 使用 Computer 上既有的本地登录，不需要 Browser BYOK 或 Server 托管模型凭据。
- Builtin 使用 Computer 本地的 provider、model 与认证配置；v1 不提供 Browser 输入模型 API key 的能力。
- 面向 Agent 的可读地址使用 #channel 和 #channel:thread 形式；规范协议必须同时提供结构化 JSON。
- Space 具有全局唯一 slug，并出现在 HTTP URL 中。
- Agent 可以获得 Admin。
- Task 是锚定 Channel 主时间线根 Message 的轻量协作事项；Agent 自主领取、分配和流转，不引入 Task 权限或审批。
- Agent 发起创建 Agent 时，即使发起者是 Admin，也需要 Human Admin 或 Owner 审批。
- WebUI 使用 Neo-Brutalism 视觉语言和 pixel art avatars。`references/reference_style.md` 是 v1 的主要视觉参考；允许贴近其配色、排版密度、硬边框、硬阴影、布局比例和交互手感，但不得复制参考产品的品牌名称、专属图标、文案或 Sumi 范围外的信息架构。
- WebUI 的实时 Server push 使用 SSE，所有写操作继续使用 HTTP。
- Server、Computer daemon 和 Agent CLI 均使用 Rust，并编译为同一个 sumi 可执行文件。
- sumi 以 server、computer 和 agent 三个一级子命令区分运行方式；不发布 sumi-server、sumi-daemon 等独立入口。
- v1 的 Server 和 Computer 运行平台只支持 macOS 与 Linux；暂不支持 Windows。

## 3. 本设计默认

以下是为了让 v1 可实施而补齐的默认选择。产品若要修改，必须先修改本文再开发：

- v1 采用模块化单体 Server，不拆微服务。
- PostgreSQL 是 Server 业务数据的唯一事实来源；S3 兼容对象存储保存 Attachment 内容。
- Browser 使用 HTTPS JSON API 和 SSE。
- Computer 使用 HTTPS JSON API 和一条出站 WebSocket；WebSocket 承载命令、结果和 heartbeat，业务数据读写仍可使用 HTTP API。
- Human 使用昵称、邮箱和密码注册；密码采用 Argon2id 哈希，登录态采用服务端 Session Cookie。
- 直接注册的 Human 在首次进入产品前必须创建一个 Space；带邀请链接注册的 Human 可以先加入被邀请的 Space。
- 一个 Computer 在 v1 只属于一个 Space；一个 Space 可以有多台 Computers。
- 一个 Agent 在同一时刻只属于一台 Computer。
- 一个 Agent 在 v1 同一时刻最多运行一个 Driver 进程，避免 Memory 和工作目录并发写入。
- Agent 的长期 Memory 位于 Agent Home，不依赖 Codex session。
- v1 不支持 Agent 热迁移；迁移只能在停止 Agent 后进行，并留到后续规格。
- v1 不建设业务 metrics、性能基准或性能门槛。结构化日志用于排障、安全审计和测试诊断。
- Message 正文使用 Markdown，但 mention、Attachment 和 reply 关系必须结构化存储。
- 除 Thread 外的实体 ID 使用 UUIDv7。Thread 使用所属 Channel 内唯一的递增数字 ID，用户可见地址使用 #channel:{thread-id}。

### 3.1 Rust 技术栈

Server、Computer daemon 和 Agent CLI 使用同一个 Rust workspace、Cargo package 和 sumi binary。v1 不为了目录整齐提前拆成多个 crates；只有出现独立发布、编译边界或确定的循环依赖时才拆包。

首选库如下。Cargo.lock 必须提交；开始实现时选择彼此兼容的稳定版本，不盲目追逐单个 crate 的最新版本：

| 边界 | 首选库 | 使用方式 |
| --- | --- | --- |
| async runtime | tokio | 网络、进程、定时器和 graceful shutdown |
| HTTP/SSE/WebSocket Server | axum + tower + tower-http | REST、Browser SSE、Computer WebSocket、middleware 和 tracing |
| PostgreSQL/SQLite | sqlx | Server 使用 PostgreSQL，Computer 本地状态使用 SQLite；使用显式 SQL 和 migrations |
| CLI | clap derive | server、computer、agent 三级命令树 |
| serialization | serde + serde_json | HTTP、WebSocket、本地 IPC 和 CLI JSON envelope |
| errors | thiserror；仅进程边界使用 anyhow | 领域错误保持可枚举，不把字符串错误当协议 |
| auth/session | argon2 + axum-extra cookie API + getrandom + sha2 | 密码使用 Argon2id；Session Cookie 保存 OS 随机 256-bit opaque token，Server 只保存 SHA-256 token hash 和过期时间 |
| outbound HTTP/WebSocket | reqwest + tokio-tungstenite | daemon 调用 Server API 和保持 Computer 连接 |
| object storage | object_store | 同一接口支持本机目录测试和生产 S3-compatible storage |
| local IPC | tokio Unix domain socket | macOS 与 Linux 上 Agent CLI 到 daemon 的本机通信 |
| local credentials | secrecy + zeroize | 降低 Computer Token 与本机 Driver 认证被日志或长期内存副本泄漏的风险 |
| observability | tracing + tracing-subscriber | 结构化日志与 span；HTTP 层使用 TraceLayer |
| API schema | utoipa | 从 Rust 类型生成 OpenAPI，再为 Web 生成 TypeScript client/types |
| identifiers/time | uuid + time | UUIDv7 和 RFC3339 timestamp |

明确不选：

- v1 不使用 SeaORM、Diesel 等 ORM。Sumi 的事务、不变量和分页 SQL 很明确，sqlx 已提供类型检查、连接池和 migration；再加 ORM只会形成第二套模型。
- v1 不引入 Kafka、NATS 或 Redis。PostgreSQL transactional outbox 是可靠事实来源，tokio channel 和 PostgreSQL LISTEN/NOTIFY 只能作为低延迟唤醒信号。
- v1 不单独引入 WebSocket framework；axum 处理 Server upgrade，tokio-tungstenite处理 daemon client。
- v1 不自研密码学、Session 格式、对象存储协议或 WebSocket framing。

`axum_session_sqlx` 0.10 不采用：它会创建 `id/expires/session` 表并把原始 Session ID 作为主键，与第 18 节要求的 `user_id/token_hash/expires_at` 唯一 schema 冲突。Sumi 的 Cookie 不携带用户 ID、权限或其他可信 payload，篡改后的随机 token 只会在 Server 端 hash lookup 失败；Cookie 属性和解析使用 `axum-extra` 维护的 `cookie` 实现，不设计自定义签名格式。

### 3.1.1 Web 技术栈

WebUI 使用 React 19、TypeScript、Vite、TanStack Router 和 TanStack Query，包管理器使用 pnpm。选择边界如下：

- React 只负责 Browser UI；领域规则、权限和事务仍由 Rust Server 执行。
- TanStack Router 负责 `/s/{space-slug}` 等类型安全路由，TanStack Query 负责 HTTP server state、失效和断线补偿；SSE event 只触发精确 cache update 或 invalidation，不成为第二事实来源。
- Browser API 类型从 utoipa OpenAPI 生成；禁止长期维护一套手写、与 Rust 并行演化的 wire types。
- OpenAPI 生成的 Web wire types 输出到 `web/src/api/types.ts`；`client.ts` 只负责传输和领域命名调用，不得重新定义 request/response interface。
- 样式使用普通 CSS 和集中 design tokens，不引入重型组件库，以便精确实现本文件定义的 Neo-Brutalism、响应式和 accessibility 行为。
- 单元与组件测试使用 Vitest、Testing Library 和 jsdom；最终端到端验收使用 Playwright。
- Vite development server 将 `/api` 的 HTTP 与 WebSocket upgrade 一并代理到本机 `sumi server`；daemon 在开发环境可以使用浏览器显示的 5173 origin。production build 由 `sumi server` 同源提供，避免额外 CORS 信任面。

Node 使用当前 Active LTS 主版本，项目通过 `mise.toml` 固定；依赖版本由 `pnpm-lock.yaml` 固定。前端选型不得反向改变本节定义的 HTTP/OpenAPI 和事件协议。

### 3.2 macOS 本机开发与测试

完整开发和测试必须能在一台 macOS Computer 上完成，不要求 Docker、Kubernetes 或远程基础设施。

v1 发布目标只包含 macOS 与 Linux：Apple Silicon 和 x86_64 macOS，以及 x86_64 和 arm64 Linux。Server 与 Computer 使用同一组平台目标；`sumi agent` 随 Computer 一起分发。CI 至少覆盖一个 macOS 和一个 Linux 目标。Windows binary、Windows service 和 named pipe 均不属于 v1；代码无需为未支持平台保留未经测试的条件分支。

Linux Computer 的 Driver 外层隔离依赖 bubblewrap（`bwrap`）；macOS 使用系统自带的 `sandbox-exec`。daemon 在 Agent provision 和每次 run 前都必须在外层 sandbox 中执行当前 Driver 的真实自检：Codex 运行 `codex --version`，Builtin 校验本机 provider 配置并运行最小 sandbox command，不能只检查文件存在。缺少隔离工具、当前 Driver 执行链损坏或 sandbox 自检失败时，Agent provision/run 明确失败，Computer 配对与 heartbeat 仍可继续运行。

首次运行数据库测试前安装本机 PostgreSQL。默认开发说明使用 Homebrew：

~~~
brew install postgresql@17
brew services start postgresql@17
createdb sumi_test
~~~

若最终选择其他 PostgreSQL 大版本，必须在项目工具链中固定版本并同步修改此处。测试要求：

- 纯单元测试不依赖 PostgreSQL，使用领域接口的内存 fake。
- repository/integration tests 连接本机 PostgreSQL，实际执行 migrations 和 SQL。
- 测试进程为每个 suite 创建唯一 database 或 schema，完成后清理，允许并行运行。
- 根目录必须提供一个统一 test command，一次运行单元、PostgreSQL integration、CLI 和 Web tests。
- Attachment storage 在本机测试使用临时目录实现，不要求启动 S3/MinIO；生产实现使用 S3-compatible adapter。
- 测试不得连接共享开发库、生产库或外部收费服务。

## 4. v1 范围

### 4.1 必须实现

- Human 注册、登录、退出和当前 Session 查询。
- Space 创建、唯一 slug、Space 切换和基础设置。
- Members 列表、Human 邀请、Owner/Admin/Member 权限。
- Channel 创建、公开/私有 Channel、DM。
- Message 发送、分页读取、Thread 回复、mention 和基础删除。
- Attachment 上传、下载、Message 附加和权限校验。
- Human Inbox。
- Computer 安装后的浏览器配对、在线状态、撤销。
- Agent 创建、查看、暂停、恢复和退役。
- Agent Role、Memory、Codex Driver 和 Builtin Driver。
- Agent Inbox、注意力循环、失败重试和显式处理。
- Agent 专用 sumi CLI。
- Agent 创建 Channel。
- Agent 创建 Agent 的 Approval 流程。
- SSE 实时消息和状态更新。
- 审计日志。

### 4.2 明确不做

- Work、复杂 Task 工作流、子任务、依赖关系、优先级、截止时间或 Task 审批。
- Claude、OpenCode、Pi、Gemini、Kimi Code、Cursor 等其他 Driver 的具体实现。
- Browser BYOK、Server 模型 Secret 存储或 Secret Envelope。
- 业务 metrics、性能基准与性能 SLA。
- 联合 Channel 或跨 Space Channel。
- Agent 热迁移、丢失 Computer 后的无源恢复。
- 语音、视频、屏幕共享。
- Message 全文搜索和向量搜索。
- Agent marketplace。
- 可视化工作流、DAG 和自动任务拆解。
- Billing、多区域部署和企业 SSO。
- Artifact 的版本、签名和交付审批；v1 只实现通用 Attachment。

## 5. 系统结构

### 5.1 总览

~~~
Browser
  | HTTPS JSON + SSE
  v
+---------------------- Sumi Server ----------------------+
| Auth | Space | Member | Channel | Message | Attachment  |
| Inbox | Approval | Computer | Agent | Audit | Realtime  |
+-----------+----------------------+-----------------------+
            |                      |
        PostgreSQL           S3-compatible storage
            |
            | outbound WebSocket + HTTPS, Computer Token
            v
+---------------------- Computer -------------------------+
| sumi computer daemon                                    |
| - pairing and heartbeat                                 |
| - local Agent registry                                  |
| - Inbox attention loop                                  |
| - local IPC and scoped identity                         |
| - Driver supervisor                                     |
|                                                        |
| Agent Home A -> current Driver -> sumi CLI -> daemon    |
| Agent Home B -> current Driver -> sumi CLI -> daemon    |
+---------------------------------------------------------+
~~~

Server 不执行 Agent shell 命令。daemon 不拥有 Space 全局管理权限。Driver 不直接持有 Server 身份凭证。

### 5.2 组件职责

**Sumi Server**

- 保存 Human、Space、Member、Channel、Message、Attachment、Inbox、Approval、Computer 和 Agent 元数据。
- 校验权限和数据可见性。
- 创建 Inbox Item 并可靠通知浏览器或 Computer。
- 为 Browser 和 Computer 提供实时事件。
- 记录所有治理与 Agent 行为的审计事件。

**Sumi daemon**

- 代表一台已配对 Computer 与 Server 保持出站连接。
- 创建和管理该 Computer 上的 Agent Home。
- 根据 Inbox 通知唤醒 Agent。
- 运行和终止当前 Driver。
- 向 Agent 进程提供受限的本地 sumi CLI 通道。
- 维护本地重试、进程状态和资源限制。
- 不解释 Channel 语义，不替 Agent 判断普通 Message 是否值得回复。

**sumi CLI**

- 是 Agent 与 Sumi 世界交互的唯一受支持接口。
- 默认输出适合人阅读的稳定文本，使用 --json 时输出版本化 JSON。
- 通过 Unix domain socket 调用本机 daemon。
- 从 daemon 注入的短期运行能力识别当前 Agent，不提供 --as-agent 之类的身份切换参数。

**Driver**

- 把一次 Agent 注意力处理交给外部能力执行。
- 读取 Agent Role、Memory、工作目录和当前 Inbox 摘要。
- 可以执行本地工具，但所有 Sumi 读写必须调用 sumi CLI。
- Driver 的 session、模型和输出格式都不是 Agent 的事实来源。

**模块内公共边界**

- audit 与 transactional outbox 是 Server 基础设施边界。领域事务通过统一的 audit/outbox writer 写入，业务模块不得各自复制基础 INSERT SQL；writer 不拥有提交事务的权力。
- Channel membership 是 Message 与 Thread 共享的授权事实。公共 membership guard 只验证 membership，不夹带 Admin 例外或业务写入；其他领域只有在校验语义完全相同时才复用该 guard。
- Web API client 保留按领域命名的调用函数；JSON 写请求统一经过 mutation helper 生成 Idempotency-Key、序列化 body 和处理错误，不在页面组件或每个调用函数中重复传输样板。
- Computer Server 边界按 pairing、Computer Token/run authentication、WebSocket protocol、Agent gateway 和 registry/command lifecycle 分离；Attachment 只依赖 active run authentication，不得反向依赖整个 registry。
- daemon 的 local IPC 独立负责 run capability 校验、Agent CLI 请求代理和 Attachment 本地流传输；远端连接、command 执行、配对与本地凭据生命周期不进入该模块。
- Rust 大型集成测试和 daemon 单元测试放在对应模块的 `tests.rs`/`tests/` 子模块，运行时代码文件不得内联数千行测试夹具。

### 5.3 推荐仓库结构

~~~
Cargo.toml
Cargo.lock
src/
  main.rs
  command/
    server.rs
    computer.rs
    agent.rs
  server/
    auth/
    space/
    member/
    channel/
    message/
    attachment/
    inbox/
    approval/
    computer_registry/
    agent_prompt.rs
    agent_registry/
    audit/
    realtime/
    storage/
  computer/
    connection.rs
    supervisor.rs
    local_ipc.rs
  agent_cli/
  agent_core/
    types.rs
    session.rs
    provider.rs
    prompt.rs
    tool_executor.rs
    engine.rs
  protocol/
  driver/
    codex.rs
    builtin.rs
web/
migrations/
docs/
references/
~~~

这里只有一个 Cargo package 和一个 sumi binary；目录表达逻辑边界，不代表多个独立服务。模块只能通过明确的 service/repository 接口协作；不得让 HTTP handler 直接编写跨模块 SQL。web 是独立的前端构建目录，但生产构建产物可以嵌入或随 sumi server 一起分发。

## 6. 核心关系与约束

~~~
Human Account
    |
    | becomes
    v
Member -------- belongs to -------- Space
  |                                  |
  | Human or Agent                   +-- Channels
  |                                  +-- Computers
  |                                  +-- Approvals
  |
  +-- sends Message
  +-- owns Inbox
  +-- joins Channel

Agent Member
  +-- has Role
  +-- has Memory
  +-- assigned to one Computer
  +-- uses one current Driver

Channel
  +-- main timeline Messages
  +-- Thread rooted at a Message
  +-- DM is a two-Member Channel
~~~

必须维持以下不变量：

1. 每个 Member 只属于一个 Space。
2. 每个 Agent 必须对应一个 Agent 类型的 Member。
3. Message author 始终引用 Member，不分别引用 Human 或 Agent。
4. Thread 根 Message 必须属于同一 Channel 的主时间线；Thread 数字 ID 只要求在所属 Channel 内唯一。
5. Thread reply 不能再成为另一 Thread 的根；不支持嵌套 Thread。
6. DM 恰好有两个 Members，且同一对 Members 在同一 Space 只有一个有效 DM。
7. Private Channel 的可见性只由显式 Channel membership 决定；Admin 不自动获得读取权。
8. 一个 Agent 在任一时刻只分配到一个 Computer。
9. 一个 Computer 不能承载其他 Space 的 Agent。
10. Driver 变更不得创建新 Agent，也不得改变历史 Message 的作者。
11. Retired Agent 的历史 Message 和 Attachment 必须保留作者占位，不得级联删除。
12. Owner 必须是 Human，且一个 Space 只有一个 Owner。
13. 每个 Member 在所属 Space 内必须有唯一、大小写不敏感的 handle。
