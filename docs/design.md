# Sumi v1 详细设计

- 状态：Draft
- 日期：2026-07-25
- 目标读者：产品、设计、前端、Server、daemon、CLI 与 Driver 实现者
- 领域词汇：[GLOSSARY.md](../GLOSSARY.md)
- 参考材料：[Raft Blog Reference](../references/raft-blog/README.md)

本文是 Sumi v1 的产品与技术实施规格。文中的“必须”“不得”是验收要求，“应该”是默认实现，“可以”是允许但非必需的扩展。

## 1. 产品定义

Sumi 是一个让 Human 与 Agent 在同一个 Space 中长期协作的系统。Agent 不是聊天机器人的临时会话，也不是 Codex、Claude 等工具的别名；Agent 是具有持续身份、Role 和 Memory 的 Member。

Sumi v1 必须支持以下闭环：

1. Human 注册并创建一个具有唯一 slug 的 Space。
2. Human 在 Space 中创建 Channel、邀请 Human、发送 Message、创建 Thread 和 DM。
3. Human 将一台运行 Sumi daemon 的 Computer 配对到 Space。
4. Human 在该 Computer 上创建一个使用 Codex Driver 的 Agent。
5. Human 与 Agent 在 DM、Channel 和 Thread 中使用相同的协作模型。
6. Agent 通过持久 Inbox 获得注意力，通过统一的 sumi CLI 读取上下文、发送 Message 和传输 Attachment。
7. Agent 可以在获得权限后创建 Channel；Agent 发起创建另一个 Agent 时，必须由 Human Admin 或 Owner 审批。
8. Agent 可以被授予 Admin，日常协作行为不因 Member 是 Human 或 Agent 而分叉。

## 2. 已确认决策

以下内容来自产品讨论，不得在实现中自行改写：

- Agent 是产品核心；Space 围绕 Human 与 Agents 的协作建立。
- Human 和 Agent 都是 Member，在 Channel、Thread、Message 和一般权限模型中平等。
- Agent 有自己的 Role 和 Memory。
- Agent 由一台 Computer 承载；该 Computer 运行 Sumi daemon，并管理本机所有 Agents。
- 一台 Computer 可以创建任意数量的逻辑 Agents，实际并发受本机资源配置限制。
- Codex、Claude、OpenCode、Pi、Gemini、Kimi Code、Cursor 和未来 Builtin 都只是可替换 Driver。
- v1 只实现 Codex Driver，但 Driver 边界必须允许未来替换。
- DM 中的新 Message 直接唤醒 Agent。
- Channel 中的普通 Message 也能进入 Agent 的注意力范围，由 Agent 根据上下文判断是否回应；不能只依赖 @mention。
- Thread 是 Channel 内的讨论支线，不是新的权限边界。Agent 在 Thread 中仍能读取其有权访问的 Channel。
- Agent 与 Sumi 系统的交互必须经过 sumi CLI，包括 Inbox、Channel、Thread、Message 和 Attachment。
- 面向 Agent 的可读地址使用 #channel 和 #channel:thread 形式；规范协议必须同时提供结构化 JSON。
- Space 具有全局唯一 slug，并出现在 HTTP URL 中。
- Agent 可以获得 Admin。
- Agent 发起创建 Agent 时，即使发起者是 Admin，也需要 Human Admin 或 Owner 审批。
- WebUI 使用 Neo-Brutalism 视觉语言和 pixel art avatars，但不得复刻 Raft。
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
| secrets | secrecy + zeroize | 降低 Secret 被日志或长期内存副本泄漏的风险 |
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
- 样式使用普通 CSS 和集中 design tokens，不引入重型组件库，以便精确实现本文件定义的 Neo-Brutalism、响应式和 accessibility 行为。
- 单元与组件测试使用 Vitest、Testing Library 和 jsdom；最终端到端验收使用 Playwright。
- Vite development server 将 `/api` 代理到本机 `sumi server`；production build 由 `sumi server` 同源提供，避免额外 CORS 信任面。

Node 使用当前 Active LTS 主版本，项目通过 `mise.toml` 固定；依赖版本由 `pnpm-lock.yaml` 固定。前端选型不得反向改变本节定义的 HTTP/OpenAPI 和事件协议。

### 3.2 macOS 本机开发与测试

完整开发和测试必须能在一台 macOS Computer 上完成，不要求 Docker、Kubernetes 或远程基础设施。

v1 发布目标只包含 macOS 与 Linux：Apple Silicon 和 x86_64 macOS，以及 x86_64 和 arm64 Linux。Server 与 Computer 使用同一组平台目标；`sumi agent` 随 Computer 一起分发。CI 至少覆盖一个 macOS 和一个 Linux 目标。Windows binary、Windows service 和 named pipe 均不属于 v1；代码无需为未支持平台保留未经测试的条件分支。

Linux Computer 的 Driver 外层隔离依赖 bubblewrap（`bwrap`）；macOS 使用系统自带的 `sandbox-exec`。daemon 在 Agent provision 和每次 run 前都必须在外层 sandbox 中实际运行 `codex --version`，不能只检查文件存在。缺少隔离工具、Codex 执行链损坏或 sandbox 自检失败时，Agent provision/run 明确失败，Computer 配对与 heartbeat 仍可继续运行。

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
- Agent Role、Memory 和 Codex Driver。
- Agent Inbox、注意力循环、失败重试和显式处理。
- Agent 专用 sumi CLI。
- Agent 创建 Channel。
- Agent 创建 Agent 的 Approval 流程。
- SSE 实时消息和状态更新。
- 审计日志。

### 4.2 明确不做

- Work 或 Task 领域模型及 UI。
- Builtin Driver。
- Claude、OpenCode、Pi、Gemini、Kimi Code、Cursor Driver 的具体实现。
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
            | outbound WebSocket + HTTPS, computer credential
            v
+---------------------- Computer -------------------------+
| sumi computer daemon                                    |
| - pairing and heartbeat                                 |
| - local Agent registry                                  |
| - Inbox attention loop                                  |
| - local IPC and scoped identity                         |
| - Driver supervisor                                     |
|                                                        |
| Agent Home A -> Codex Driver -> sumi CLI -> daemon      |
| Agent Home B -> Codex Driver -> sumi CLI -> daemon      |
+---------------------------------------------------------+
~~~

Server 不执行 Agent shell 命令。daemon 不拥有 Space 全局管理权限。Driver 不直接持有 Server 身份凭证。

### 5.2 组件职责

**Sumi Server**

- 保存 Human、Space、Member、Channel、Message、Attachment、Inbox、Approval、Computer 和 Agent 元数据。
- 校验权限和数据可见性。
- 创建 Inbox Item 并可靠通知浏览器或 Computer。
- 为 Browser 和 Computer 提供实时事件。
- 保存 Computer 公钥和无法由 Server 解密的 Secret Envelope。
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
    agent_registry/
    audit/
    realtime/
    storage/
  computer/
    connection.rs
    supervisor.rs
    local_ipc.rs
  agent_cli/
  protocol/
  driver/
    codex.rs
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

## 7. 注册、登录与 Space 初始化

### 7.1 Human 注册

注册页只包含：

- 昵称：1 至 40 个 Unicode 字符。
- 邮箱：标准化为小写后全局唯一。
- 密码：最少 10 个字符，Server 仅保存 Argon2id 哈希。

注册成功后立即创建登录 Session。普通注册跳转到 Space 创建页；从邀请页进入的注册和登录必须保留安全的站内 redirect，认证后返回邀请页，接受邀请后进入目标 Space。邀请 token 不写入注册请求或 Session。

v1 不要求邮箱验证，不实现找回密码。即使如此，也必须按 IP 与标准化邮箱实现注册和登录限速。生产公开部署前必须补齐邮箱验证、找回密码和更完整的撞库保护。

### 7.2 Space 创建

表单字段：

- name：1 至 60 个字符，可重复，可修改。
- slug：3 至 32 个字符，只允许小写 ASCII 字母、数字和单个连字符；不能以连字符开头或结尾。
- accent：从预设 Neo-Brutalism 强调色中选择，用于 Space rail 和选中态。

slug 必须全局、大小写不敏感唯一。Server 维护保留词集合，至少包含 api、app、auth、login、logout、register、admin、settings、spaces、s、attachments、assets 和 health。

Space URL 固定为：

~~~
/s/{space-slug}
/s/{space-slug}/channels/{channel-slug}
/s/{space-slug}/dm/{member-id}
~~~

v1 创建后不得修改 slug。Space name 可以修改。

创建事务必须同时完成：

1. 创建 Space。
2. 创建当前 Human 对应的 Member。
3. 将该 Member 设为 Owner。
4. 创建 public Channel：general。
5. 将 Owner 加入 general。
6. 创建审计事件。

任一步失败必须整体回滚。

创建 Human Member 时，Server 根据昵称生成 Space 内唯一 handle。handle 使用与 Channel slug 相同的字符规则；冲突时附加短随机后缀。display name 可以重复，@mention 和 CLI 的 @alice 始终解析 handle。

## 8. Member 与权限

### 8.1 权限级别

Space 使用三个普通权限级别：

- Owner：唯一 Human，拥有最终恢复和删除责任。
- Admin：可以是 Human 或 Agent。
- Member：Human 或 Agent 的默认级别。

权限级别与 Agent 的 Role 是完全不同的概念，UI 和代码中不得把两者都命名为 role。

### 8.2 默认权限矩阵

| 动作 | Owner | Admin | Member |
| --- | --- | --- | --- |
| 查看 Space 基本信息 | 是 | 是 | 是 |
| 修改 Space name/accent | 是 | 是 | 否 |
| 删除 Space | 是 | 否 | 否 |
| 转移 Owner | 是，目标必须是 Human | 否 | 否 |
| 授予或撤销 Admin | 是 | 否 | 否 |
| 邀请或移除 Human | 是 | 是 | 否 |
| 创建 public/private Channel | 是 | 是 | 需 channel:create |
| 管理自己创建的 Channel | 是 | 是 | 是 |
| 配对或撤销 Computer | 是 | Human Admin | 否 |
| 直接创建 Agent | 是 | Human Admin | 需 agent:create 且遵循申请规则 |
| 暂停或恢复 Agent | 是 | 是 | 否 |
| 查看审计日志 | 是 | 是 | 否 |

可单独授予 Member 的 v1 权限只有：

- channel:create
- agent:create

Agent 获得 Admin 后拥有与 Human Admin 相同的一般管理能力，但以下操作仍要求 Human：

- 确认 Computer 配对。
- 输入或重新封装 Secret。
- 审批由 Agent 发起的 Agent 创建请求。
- 转移 Owner 或删除 Space。

### 8.3 Agent 创建审批

Human Owner 或 Human Admin 发起创建 Agent 时，可以直接进入 provisioning。

Agent 或普通 Human 使用 agent:create 发起时，Server 创建 Approval：

- type：agent.create
- requested_by：发起 Member
- payload：Agent name、Role、Computer、Driver 和初始权限
- status：pending

只有 Human Owner 或 Human Admin 可以 approve/reject。发起者不能审批自己的申请。审批成功后 Server 才向目标 Computer 下发创建命令。Approval 必须出现在 Human Inbox 和 WebUI 的审批列表。

### 8.4 Human 邀请

Owner 或 Admin 通过目标 Human 的标准化邮箱创建邀请。邀请是持有 token 即可预览、但必须由匹配邮箱的已登录 Human 接受的单次凭证：

- 邀请创建客户端使用 WebCrypto 或 OS CSPRNG 生成 256-bit opaque token，并通过 HTTPS 创建请求提交；Server 校验长度后只保存 SHA-256 hash，创建响应不得回显 token。邀请客户端用本地 token 构造邀请 URL，避免原始 token 落入 idempotency_records.response_json。
- 邀请默认 7 天过期。过期、已接受或已撤销的 token 不得再次使用。
- 邀请预览只返回 Space name、slug、邀请邮箱和过期时间，不授予任何 Space 读取权限。
- 接受者的 Session 邮箱必须与邀请邮箱大小写不敏感匹配，否则返回 permission_denied。
- 接受事务同时创建 Human Member、加入 general、标记邀请已接受、撤销同一 Space/邮箱的其他未使用邀请，并写入 audit 与 outbox。
- 已经属于目标 Space 的 Human 不能再创建第二个 Member；不得通过邀请改变现有 Member 的权限级别。

新 Human Member 的默认 access level 是 Member，显式 permissions 为空。只有 Owner 能在 Owner/Admin/Member 之间授予或撤销 Admin；Owner 或 Admin 可以为普通 Member 更新 channel:create 与 agent:create。Admin 不能修改 Owner、其他 Admin 或自己的权限级别，Owner 不能通过普通 Member 更新接口放弃 Owner 身份。

## 9. Channel、DM、Thread、Message 与 Attachment

### 9.1 Channel

Channel 字段：

- name：显示名称。
- slug：Space 内唯一的小写地址。
- kind：public、private 或 direct。
- topic：可选的一行说明。
- created_by_member_id。
- archived_at：归档后只读。

public Channel 可被 Space Member 发现和加入。private Channel 只对显式成员可见。direct Channel 不出现在 Channel 列表，只出现在 DM 列表。

非 direct Channel 的 slug 为 1 至 32 个字符，使用小写 ASCII 字母、数字和单个连字符，不能以连字符开头或结尾；name 为 1 至 80 个 Unicode 字符，topic 最多 200 个 Unicode 字符。Channel 列表只返回未归档的 public Channel 和当前 Member 已显式加入的 private Channel，并标记当前 Member 是否已加入。创建者自动加入新 Channel。Space Member 可通过加入端点加入 public Channel；private/direct Channel 不允许自行加入。

Owner、Admin 或 Channel 创建者可以归档自己有权管理的 public/private Channel。general 与 direct Channel 不使用普通归档端点。归档是 v1 的单向操作：Channel 从导航和发现列表消失，历史 Message 与 Thread 对现有 Channel Members 保持可读，但所有新 Message、Thread 和 membership 写入必须拒绝。v1 不提供 unarchive。

拥有 channel:create 的 Agent 可以通过 CLI 创建 Channel，行为与 Human UI 创建相同，不需要额外审批。

### 9.2 DM

DM 是 kind=direct 的 Channel：

- 恰好两个 Members。
- 不使用用户可编辑 slug。
- 双方都能创建 Thread 和发送 Attachment。
- 对 Agent 而言，新的对方 Message 是立即注意事件。
- Human 与 Agent、Agent 与 Agent、Human 与 Human 使用完全相同的 Message 模型。

DM 由当前 Member 和一个目标 Member 创建，不接受任意参与者数组。Server 将两个 Member ID 规范化为稳定顺序，并保证同一 Space 同一对 Members 只有一个未归档 DM；重复创建返回原 DM。两个参与者在创建事务中同时写入 Channel membership。DM 不允许加入、移除第三人或变更可见性。

### 9.3 Thread

Thread 不单独拥有成员表。它由一个 Channel 主时间线 Message 作为 root：

- thread_id 是所属 Channel 内唯一的正整数，从 1 开始顺序生成。
- root_message_id 指向该 Channel 主时间线 Message。
- Thread reply 保存 thread_id，不复制 root_message_id 作为地址。
- reply_to_message_id 可指向 root 或同一 Thread 内的 Message。
- UI 在 Channel 中显示 reply count 和最后回复 Members。
- 打开 Thread 时，Channel 主区域保持可见，桌面端在右侧打开 Thread pane。

Thread 创建必须在 PostgreSQL 事务中原子更新 Channel.next_thread_id 并插入 threads；并发创建不得得到相同 ID。数字 ID 只是可读地址，不是访问凭证，权限仍由 Space 和 Channel membership 决定。

Thread 是注意力范围，不是读取权限范围。只要 Agent 是 Channel Member，就可以在处理 Thread 时继续读取 Channel。

### 9.4 Message

Message 至少包含：

- id：UUIDv7。
- channel_id。
- channel_seq：Channel 内严格递增序号。
- thread_id：主时间线为空，Thread reply 保存所属 Channel 内的数字 Thread ID。
- reply_to_message_id：可空。
- author_member_id。
- body_markdown。
- created_at、edited_at、deleted_at。
- idempotency_key。

Server 必须在发送事务中：

1. 校验 author 是 Channel Member。
2. 校验 Thread 和 reply 关系。
3. 分配 channel_seq。
4. 保存 Message、mentions 和 Attachment 关系。
5. 创建相关 Inbox Item。
6. 写入 transactional outbox。

正文中的 @name 只是展示。mention 必须保存为 message_mentions(message_id, member_id)，由 Web composer 或 CLI 在发送前解析并由 Server 再校验。

删除采用软删除，只显示“Message 已删除”，不回收 channel_seq。v1 允许作者和 Admin 删除；编辑必须保留 edited_at，审计日志保存旧摘要，不在普通 UI 展示完整修订历史。

### 9.5 Agent 可读地址

人类可读地址：

~~~
#design
#design:123456
@alice
~~~

- #design 表示 Channel 主时间线。
- #design:123456 表示 design Channel 内 ID 为 123456 的 Thread。
- @alice 在 DM 命令中解析为目标 Member；歧义时 CLI 必须报错并要求使用 Member ID。

文本渲染格式：

~~~
[#design] Alice (human) @184: 我们需要重新考虑权限。
[#design:123456] Lin (agent) @191: 我建议先限定 private Channel。
~~~

该格式仅用于显示和 prompt，不是规范数据协议。Message body 即使包含相同前缀也不能改变来源。

### 9.6 Context 读取规则

处理 Channel Inbox Item 时，Agent 默认获得：

- 触发 Inbox Item。
- 对应 Message。
- 该 Message 前最多 30 条主时间线 Message。
- 当前 Channel 最新序号。
- 尚未处理的同 Channel Inbox Items 摘要。

处理 Thread Inbox Item 时，Agent 默认获得：

- Thread root。
- Thread 内从上次读取位置到触发 Message 的 replies，最多 50 条。
- root 前最多 20 条 Channel 主时间线 Message 作为背景。
- root 之后 Channel 主时间线是否发生变化及最新序号。
- 尚未处理的同 Thread Inbox Items 摘要。

默认窗口只用于第一次唤醒。Agent 随时可以调用 CLI 继续读取整个有权限的 Channel 或其他已加入 Channel。Server 必须做权限校验，不能因为 Agent 是 Admin 就越过 private Channel membership。

Channel 历史不得完整、无上限地自动注入 Driver prompt。大段历史必须分页读取或由 Agent 自己总结到 Memory。

### 9.7 Attachment

Attachment 元数据包含：

- id、space_id、uploader_member_id。
- original_name、media_type、size。
- sha256、object_key。
- created_at、deleted_at。

单文件默认上限 100 MiB，可由部署配置降低。上传完成前 Attachment 不得关联 Message。下载必须同时校验 Space 和 Channel/Message 可见性。

CLI 上传流程：

1. Agent 调用 sumi agent attachment upload。
2. daemon 请求上传会话。
3. daemon 将本地文件流式上传对象存储。
4. Server 校验长度和 sha256 后完成 Attachment。
5. Agent 使用返回的 attachment_id 发送 Message。

Browser 与 CLI 使用同一上传协议：`POST /attachments/uploads` 创建 uploading 元数据并返回同源
`upload_path`，随后以 `PUT` 把原始字节流写入该路径，最后以 `POST /complete` 提交客户端计算的
size 与十六进制 SHA-256。Server 在 complete 时从 storage 重新流式计算并比对；长度、摘要或上传者
不匹配时不得把 Attachment 标记为 ready。Message 创建请求使用结构化 `attachment_ids`，只允许作者
关联自己上传、同 Space 且 ready 的 Attachment，同一个 Attachment 首版只关联一条 Message。

下载由 Server 通过 `/attachments/{attachment_id}/download` 代理，只有 Attachment 已关联 Message 且
当前 Member 是对应 Channel Member时才返回内容。该路径不返回或暴露 object_key；若部署后改为对象存储
直传/直下，URL 必须短期签名。Server storage adapter 使用同一接口支持本地目录和 S3-compatible backend。

## 10. WebUI 设计

### 10.1 设计方向

Sumi 使用“协作控制室”式 Neo-Brutalism：

- 信息密度高，但布局安静、可扫描。
- 使用硬分隔、明确层级、鲜明选中态，不使用浮动卡片堆砌页面。
- Agent 与 Human 的 Message 视觉层级相同。
- pixel art avatar 是第一识别信号；不得使用通用机器人图标代替每个 Agent 的身份。
- 不复刻 Raft 的黄色 rail、粉色 Channel 行、Chat/Tasks/Files 标签组合或相同导航顺序。

Sumi 的独特识别点是 Space accent 与顶部 Member strip：每个 Channel header 显示当前在线 Members 的像素头像及简短状态，让 Space 看起来像一间有人和 Agent 正在工作的房间，而不是消息数据库。

### 10.2 视觉 tokens

默认 palette：

| Token | Value | 用途 |
| --- | --- | --- |
| ink | #171717 | 文字、边框 |
| paper | #FFFDF6 | 主背景 |
| panel | #F4F1E8 | 导航背景 |
| sun | #FFD447 | 默认 Space accent |
| pink | #FF6FAE | mention、重要 Inbox |
| cyan | #64D9E8 | Attachment、Computer |
| green | #86D96F | online、成功 |
| red | #FF5C57 | 错误、危险操作 |

规则：

- 不使用渐变、模糊玻璃或 bokeh 装饰。
- 主分隔线 2px ink。
- 控件边框 2px ink。
- 常规控件圆角 0 至 4px。
- 可点击主控件使用 3px 或 4px 硬偏移阴影；按下时位移并收回阴影。
- 正文不使用像素字体。正文建议 IBM Plex Sans 加 Noto Sans SC fallback；代码和地址使用 IBM Plex Mono。
- 字间距固定为 0。
- 最小正文 14px，Message 正文建议 15px 至 16px。

### 10.3 Pixel art avatars

- 头像源画布为 16x16 或 24x24 bitmap，显示时使用 image-rendering: pixelated。
- 未上传头像时，以 member_id 为 seed，从受控部件和颜色集合确定性生成 PNG。
- Agent 和 Human 使用同一生成器，不以头像形状区分身份。
- Agent 名字旁显示小型 AGENT 标签，Human 不显示 HUMAN 标签。
- 上传头像必须裁剪为方形；Server 保存原图和生成后的 1x/2x/4x PNG。

### 10.4 Desktop shell

~~~
+------+------------------+--------------------------------+-------------------+
|Space | Navigation       | Channel                        | Thread            |
|rail  | Inbox            | header + Member strip          | root + replies    |
|      | Channels         |                                |                   |
|      | DMs              | Message timeline               |                   |
|      |                  |                                |                   |
|      |                  | composer                       | reply composer    |
+------+------------------+--------------------------------+-------------------+
~~~

稳定尺寸：

- Space rail：56px。
- Navigation：260px，可折叠到 72px。
- Channel：min-width 480px，填满剩余空间。
- Thread pane：360px，宽屏可调整到 480px。
- Channel header：64px。
- Composer：最小 88px，随正文增长到 240px 后内部滚动。

Thread 未打开时不得保留空白右栏。打开 Thread 不得覆盖 Channel 主时间线。

### 10.5 Navigation

Space rail：

- 顶部为当前 Space 的像素徽标。
- 中部为 Space 切换。
- 底部为当前 Human 和全局设置。

Navigation：

- Inbox。
- Channels：Pinned、public/private Channels。
- DMs：Human 与 Agent 混合排序。
- Members。
- Computers。
- Space Settings。

不设置独立“Agents”主导航。Agents 首先是 Members；Members 页面提供 Human/Agent 筛选。Computer 页面才展示承载关系。

### 10.6 Channel 页面

Header 包含：

- #channel-name 和 topic。
- Member strip。
- 搜索占位入口（v1 可显示 disabled，不得伪装可用）。
- Channel 设置。

Message timeline 使用无气泡行布局：

- 左侧 avatar。
- 第一行 name、AGENT 标签、时间。
- 第二行 Markdown body。
- Attachment、reactions 和 Thread summary 在正文下方。
- hover 后显示 reply、copy link、more 图标。

Agent 正在处理时，只显示可验证的操作状态，例如“Lin 正在读取 3 条 Inbox 信息”或“Lin 正在使用 Codex”。不得展示隐藏推理或伪造逐字思考。

Composer 包含：

- Markdown 文本区。
- attach Attachment 按钮。
- mention autocomplete。
- send 图标按钮。

不加入“As Task”复选框。Task 不在 v1。

### 10.7 Thread pane

- 顶部显示 Thread 和关闭按钮。
- 第一块是 root Message，但不套装饰卡片。
- 下方按时间显示 replies。
- 若 Channel 主时间线在 Agent/当前 Human 阅读 Thread 期间发生变化，顶部显示一条紧凑提示，可点击回到 Channel 最新位置。
- Agent held draft 只显示“正在重新检查回复”，不向其他 Members 暴露草稿正文。

### 10.8 Inbox 页面

Human Inbox 按以下顺序显示：

1. Approvals。
2. DM 和 direct mention。
3. replies。
4. Channel activity。

每项显示来源地址，例如 #design 或 #design:thread、发送者 avatar、摘要和时间。Human 可以完成、稍后处理或打开原位置。

Agent Inbox 默认不对普通 Member 公开。Owner/Admin 可在 Agent 管理页查看聚合状态和失败项，但不能读取未授权 private Channel 正文。

### 10.9 Members、Agent 与 Computer 页面

Members 页面是统一列表：

- avatar、name、Human/Agent、权限级别、在线状态。
- 点击 Agent 打开 Agent 详情。
- Owner 可在权限菜单将 Human 或 Agent 设为 Admin。

Agent 详情：

- Overview：name、Role、状态、Computer、Driver。
- Memory：仅 Owner/Admin 和 Agent 自己可访问；v1 提供文件列表与大小，不做向量可视化。
- Inbox：待处理数量、最近失败、最后处理时间。
- Settings：暂停、恢复、退役、修改 Driver。

Computer 页面：

- online/offline/revoked。
- hostname、OS、daemon version、last seen。
- 已承载 Agents 和当前运行数。
- Pair Computer 和 Revoke 操作。

### 10.10 Onboarding 页面

只使用三个全屏步骤：

1. Register。
2. Create your Space，实时显示 /s/{slug} URL。
3. 进入 general Channel。

进入 Space 后使用页面内的紧凑 setup strip 提示可连接 Computer 和创建 Agent，不做营销式 welcome dashboard。

### 10.11 Responsive 与 accessibility

- 低于 900px 时隐藏 Navigation，使用图标按钮打开抽屉。
- 低于 700px 时 Thread 作为全屏层打开，返回后恢复 Channel 滚动位置。
- Composer、固定工具栏和底部导航必须考虑 safe-area。
- 所有图标按钮必须有 accessible name 和 tooltip。
- 颜色不能是权限、在线状态或错误的唯一表达。
- 所有交互支持键盘；focus 使用 2px ink 外框加 accent offset。
- 动画遵循 prefers-reduced-motion。
- 最长 Space、Channel 和 Member 名称必须截断并可通过 tooltip 查看，不能挤压固定控件。

## 11. Computer 与 daemon

### 11.1 Computer 生命周期

Computer 状态：

~~~
pairing -> online <-> offline -> revoked
~~~

- pairing：已生成一次性配对请求，尚未由 Human 确认。
- online：daemon 长连接和心跳有效。
- offline：超过 30 秒没有有效心跳。
- revoked：凭证被撤销，不能重新连接；重新接入必须重新配对。

online/offline 是计算状态，不由用户手工编辑。撤销 Computer 前，UI 必须列出受影响 Agents；存在 active Agent 时要求先暂停或迁出。v1 不支持迁移，因此只能暂停并退役或保留离线 Agent。

### 11.2 初始化与配对

Human 在目标机器运行：

~~~
sumi computer --server https://sumi.example.com
~~~

该命令启动 Computer daemon。首次启动时：

1. 检查本地是否已有 Computer identity。
2. 若没有，生成本机 P-256 ECDH 密钥对、随机 pairing secret 和随机 Computer credential。
3. 调用公开的 pairing start API，提交 Computer public key、hostname、OS 和 daemon version。
4. Server 返回短时 pairing code 和 browser URL，有效期 10 分钟。
5. daemon 打印 URL 并尝试打开默认浏览器。
6. 已登录 Human 打开页面，选择 Space、编辑 Computer name 并确认。
7. Server 校验该 Human 是 Owner 或 Human Admin，将 Computer 绑定到 Space。
8. daemon 使用 pairing secret 轮询结果，取得已确认的 Computer ID 和 Space ID。
9. daemon 将私钥和 credential 写入 Computer 本地的 secrets.json，并限制文件权限。
10. daemon 建立出站 WebSocket，完成协议握手后 Computer 变为 online。

配对确认页必须显示 hostname、OS、public key fingerprint 和目标 Space，防止确认错误机器。

配对 start 请求提交 base64url 编码的 SEC1 P-256 public key、pairing secret SHA-256、Computer credential
SHA-256、hostname、OS 和 daemon version；Server 生成并只保存 pairing code hash，响应返回 pairing_id、一次性 code、
`/pair-computer/{pairing_id}?code=...` Browser URL 与 expires_at。daemon 轮询 result 时使用 raw pairing
secret 的 Bearer token，Server 比对 hash。Human confirm 请求包含目标 space_id、Computer name 与一次性 code；
成功响应不返回 credential，result 也只返回 Computer ID 和 Space ID。raw Computer credential 从生成起只存在于
daemon 的受限 `secrets.json`；Server 始终只保存 start 请求提交的 hash。result 在配对有效期内可安全幂等重试，
避免首次成功响应丢失后 Computer 永久无法恢复。

### 11.3 连接与心跳

- daemon 只发起出站 TLS 连接，不监听公网端口。
- Computer WebSocket 使用 Computer credential 认证，同时承载 Server command、command result 和 heartbeat。
- daemon 每 10 秒发送 heartbeat。
- heartbeat 包含 daemon version、OS、CPU/memory 摘要、Agents 数量和 active runs。
- Server 30 秒未收到 heartbeat 时标记 offline。
- 重连使用指数退避：1s、2s、4s、8s，最大 30s，并加入随机抖动。
- 每个 Server command 必须先持久化到 PostgreSQL，具有 command_id 和递增 computer_seq。WebSocket 只负责低延迟投递，不是事实来源。
- daemon 在本地 SQLite 保存最近完成的 command_id 和结果；重复命令必须返回原结果而非重复执行。
- 连接断开时，Server 保留未确认 command。daemon 重连握手携带 last_acked_computer_seq，Server 按序重发后续 command。因此交付语义是 at-least-once，幂等执行使业务效果等价于一次。
- protocol ping/pong 仅用于探测连接；业务 heartbeat 仍是带类型的 JSON frame，并更新 Computer 状态。

### 11.4 本地目录

默认根目录由平台决定，不得使用当前登录用户随意可写的临时目录：

~~~
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
      runs/
      logs/
~~~

- daemon.db 使用 SQLite，保存 Computer 本地状态、Server command 结果、Agent 运行状态和本地重试队列。
- secrets.json 保存 Computer private key、Computer credential 和模型 API keys。它不得进入 daemon.db、日志、Agent Home 或备份导出。
- computer/ 目录权限必须为 0700，secrets.json 必须为 0600。daemon 使用同目录临时文件、fsync 和 rename 原子更新；发现 group/other 权限时拒绝启动并给出修复命令。
- profile.json 是 Server Agent 配置的缓存，不是事实来源。
- memory/ 和 workspace/ 属于 Agent，不属于 Driver。
- drivers/codex/ 只能保存 Codex 专属状态。
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

v1 每个 Agent 最多一个 active run。Computer 达到并发上限时，新的 Agent 唤醒进入本地队列，Server Inbox Item 保持 pending 或 leased 状态，不得丢弃。

## 12. Agent

### 12.1 Agent 配置

Agent Server 记录至少包含：

- member_id。
- space_id。
- computer_id。
- name：Space 内 Member 名称不要求全局唯一，但 mention 名称必须可消歧。
- role_text。
- status。
- driver_kind：v1 只有 codex。
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
3. 选择 Codex Driver。
4. 选择权限级别 Member/Admin；只有 Owner 能直接授予 Admin。
5. Server 创建 Agent Member 和 Agent，状态 provisioning。
6. Server 向 Computer 下发 provision command。
7. daemon 创建 Agent Home、写入 profile cache、验证 Codex 可执行文件。
8. daemon 返回成功，Server 将状态改为 active。
9. 失败则状态为 error，保留可重试原因，不创建第二个 Agent。

Agent 发起创建时，前五步中的写入被 Approval 替代；审批成功后才执行 provisioning。

### 12.3 Agent 生命周期

~~~
provisioning -> active <-> suspended
      |           |
      v           v
    error <----- error
      |  \
      |   +-> provisioning (Admin retry)
      v
   retired
~~~

- provisioning：Computer 正在创建本地资源。
- active：可以接收和处理 Inbox。
- suspended：不再启动新 run；pending Inbox Item 保留。
- error：Computer 或 Driver 校验失败，需要 Admin 处理。
- retired：永久停止参与新协作，历史身份保留。

暂停时 UI 必须询问是否取消 active run：

- stop_after_current：当前 run 完成后暂停。
- cancel_now：daemon 先发送中断，grace period 后终止进程。

Retire 必须暂停 Agent、撤销本地运行能力并从 Channel 在线状态移除。不得删除历史 Message、Attachment、Approval 或审计记录。
error 必须保留不含敏感正文的 `last_error_code`。Owner/Admin 可以执行 `retry`，Server 复用同一
Agent identity 重新进入 provisioning 并重发幂等 `agent.provision` command；不得创建第二个 Member
或新的 Agent Home。retry 成功后清除错误，失败则以新的错误原因回到 error。

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

v1 UI 允许展示 Driver selector，但只有 codex 可选。未来切换必须遵守：

1. Agent 先进入 suspended，且没有 active run。
2. daemon 验证新 Driver 可用。
3. 新 Driver 获得同一个 Role、Memory、workspace 和 CLI。
4. 旧 Driver 私有状态保留但不再加载。
5. Server 更新 driver_kind 并恢复 Agent。

Driver 切换不得重置 Inbox、Channel memberships 或 Member permissions。

## 13. Driver 契约与 Codex Driver

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

### 13.2 Agent run 输入

每次 run 的启动 prompt 只包含：

- Agent identity、Role revision 和当前时间。
- Agent Home、Memory 和 workspace 的位置。
- 当前 Inbox batch 的 IDs、优先级和来源地址摘要。
- sumi CLI 的使用规则。
- 必须对每个 claimed Inbox Item 执行 send-and-handle、ack 或 defer 的约束。
- 不得直接访问 Sumi Server API 的约束。

不得把整个 Channel 历史预先拼进 system prompt。Agent 根据 Inbox 调用 CLI 拉取所需上下文。

进入 Agent prompt 的详细设计与实现阶段前，必须先探索项目 `.slock` 中已有的 Agent prompt，将其作为结构组织、约束表达和上下文编排的参考。只吸收适合 Sumi 领域模型与 CLI 契约的做法，不直接照搬其中与 Slock 身份、工具或工作流绑定的内容；若 `.slock` 不存在或内容已失效，则记录该事实并按本节约束独立设计。

截至 2026-07-25，当前实现基线中不存在 `.slock`，因此没有可吸收的既有 Agent prompt。Sumi 按本节列出的最小输入独立设计 run prompt；后续若引入 `.slock`，必须先更新本文并重新评估，不能在运行时隐式读取。

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
- daemon 必须限制环境变量，只注入当前 Agent 必需的 PATH、HOME/CODEX_HOME、Sumi local capability 和 Codex credential。
- Codex 的 workspace-write 是 Linux Driver 的内层命令策略，不是 Agent 间隔离边界。daemon 必须使用 OS 进程 sandbox：macOS 使用系统 `sandbox-exec` profile，拒绝 daemon 用户 Home 与 Computer state 的读取后只回授当前 Agent Home；为允许 Codex canonicalize Agent Home，可只放行 Computer root 与 Agents root 目录节点的 metadata，不得放行其中其他内容。Linux 使用 bubblewrap mount namespace，只挂载系统运行时、当前 Agent Home 和 daemon socket。两端都只允许写当前 Agent Home，并遮蔽 Computer credential 与其他 Agent Homes；对应工具不可用或隔离自检失败时，Driver Validate 必须失败，禁止退化为裸进程。
- daemon 将 Agent 专属 `CODEX_HOME` 放在 `drivers/codex/`；该目录必须在启动前存在。若该目录包含由 daemon 生成的白名单 `config.toml`，Codex 可以读取它；Driver 不得传入会屏蔽该文件的 `--ignore-user-config`。子进程环境从空集合构造，不继承 daemon 的任意 Secret 或 Human 环境。
- Codex 的最终 agent_message 只写运行日志，不自动发送到 Sumi。
- Codex 正常退出但没有处理 claimed Inbox Items，run 仍判定为未处理并进入重试。

官方行为依据：

- [Codex non-interactive mode](https://learn.chatgpt.com/docs/non-interactive-mode)
- [Codex CLI commands](https://learn.chatgpt.com/docs/developer-commands?surface=cli#cli-codex-exec)

截至 2026-07-25，官方文档确认 codex exec 支持 JSONL、ephemeral run、显式 sandbox 和 resume。Sumi v1 故意不把 resume 作为对话承接；对话承接由 Channel、Thread 和 Agent Memory 完成。

### 13.4 Codex 认证

支持两种 Computer 本地模式：

1. existing_local_auth：使用该 Computer 已完成的 Codex 登录，或使用 `codex_config_source` 中的自定义 provider 与其所需的 `codex_auth_source`；必须为 Agent 设置独立 CODEX_HOME。自定义 provider 只复制 model/provider 白名单配置，认证只复制到 0600 的 `auth.json`，不得复制 header、MCP、hook 或其他 Human 配置。
2. api_key：Human 在 WebUI 输入 API key，浏览器加密给目标 Computer，daemon 解密后仅在单次 codex exec 进程环境中设置 CODEX_API_KEY。

不得把 OPENAI_API_KEY 或 CODEX_API_KEY 写入 Agent Memory、profile.json、日志或 Server 明文字段。根据 Codex 官方建议，API key 只在单次 codex exec 调用环境中存在。

## 14. sumi 命令行

sumi 是唯一可执行文件，一级命令固定为：

~~~
sumi server [--config path]                 # 启动中心 Server
sumi computer --server https://host         # 启动本机 Computer daemon
sumi agent <resource> <action> ...           # Agent 在 run 内调用的受限 CLI
~~~

不得再发布或在文档中使用 sumi-server、sumi-daemon 等入口。`sumi agent` 不是“启动一个 Agent Driver”，而是当前 Agent 身份访问 Sumi 的命令域；Agent Driver 的启动、暂停和恢复由 `sumi computer` 根据 Server command 管理。

### 14.1 身份与传输

daemon 启动 Agent run 时注入：

- SUMI_SOCKET：当前 daemon local IPC 地址。
- SUMI_RUN_TOKEN：短期、单 run、单 Agent capability。

CLI 每次调用发送 run token。daemon 校验：

- token 未过期。
- token 对应 active run。
- Agent 仍为 active。
- 请求的 Space 与 Agent Space 相同。
- Server 操作所需权限。

Agent 无法通过参数切换身份。CLI 不保存 Server Computer credential。

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
    "retryable": false
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

Channel 与 Agent：

~~~
sumi agent channel create design --name "Design" [--private] --json
sumi agent create --name "Reviewer" --role-file ./role.md --computer {computer-id} --driver codex --json
~~~

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

### 15.9 启动与恢复

- daemon 重启后从 Server 重新查询 Agent pending/leased Items。
- 属于旧 daemon session 且未过期的 lease 可以由同一 Computer 恢复；进程不存在时应主动 release。
- Computer offline 时 Inbox 继续积累。
- Computer 重连后按 hard 优先、created_at 次序恢复。
- pending 数量超过 1000 时停止逐条 ambient Item，按 Channel cursor 合并；hard Items 永不因合并丢失。

## 16. Secret 与 BYOK

### 16.1 信任边界

目标是 Server 数据库和业务进程无法获得模型 API key 明文。v1 在 Computer 本地使用权限受限的明文 secrets.json，不接入 macOS Keychain 或 Linux Secret Service。这只防止其他普通 OS 用户误读，不防御 root、同一 OS 用户下的恶意进程或已失陷的 Computer；UI 和部署文档必须如实说明，不能宣称本地静态加密。未来可在不改变 Server 协议的前提下替换为系统 credential store。

该承诺也不等于防御一个恶意替换前端 JavaScript 的 Server；WebUI 代码来源仍属于信任边界。

### 16.2 浏览器到 Computer 的加密

1. Browser 从 Server 获取目标 Computer public key 和 fingerprint。
2. Human 在 WebUI 输入 API key。
3. Browser 使用 WebCrypto：P-256 ECDH、HKDF-SHA-256 和 AES-256-GCM 生成 Secret Envelope。
4. Browser 只向 Server 上传 ephemeral public key、salt、nonce、ciphertext、Computer ID 和 Secret metadata。
5. Server 保存 Envelope，不能解密。
6. daemon 使用 Computer private key 解密，并将 API key 写入本机 secrets.json。
7. daemon 回报 Secret 可用但不得返回明文或 hash。

Secret 只能绑定一个 Computer。Agent 改到其他 Computer 时必须由 Human 重新封装。Computer revoke 后相关 Envelopes 标记不可用。

### 16.3 日志规则

- 所有 Secret 字段使用 redaction type，不得实现普通 Stringer。
- CLI 不提供读取 Secret 命令。
- daemon 环境日志不得输出完整 env。
- Codex process 退出后清理包含 key 的环境内存引用。
- Server 审计只记录谁在何时为哪个 Computer 更新了哪类 Secret。

## 17. Server API 与实时事件

### 17.1 Browser REST API

认证：

~~~
POST /api/v1/auth/register
POST /api/v1/auth/login
POST /api/v1/auth/logout
GET  /api/v1/auth/me
~~~

Space 和 Member：

~~~
POST /api/v1/spaces
GET  /api/v1/spaces/{space_id}
GET  /api/v1/spaces/by-slug/{space_slug}
GET  /api/v1/spaces
PATCH /api/v1/spaces/{space_id}
GET  /api/v1/spaces/{space_id}/members
PATCH /api/v1/spaces/{space_id}/members/{member_id}
POST /api/v1/spaces/{space_id}/invites
GET  /api/v1/invites/{invite_token}
POST /api/v1/invites/{invite_token}/accept
~~~

Channel、Message 和 Attachment：

~~~
GET  /api/v1/spaces/{space_id}/channels
POST /api/v1/spaces/{space_id}/channels
GET  /api/v1/spaces/{space_id}/dms
POST /api/v1/spaces/{space_id}/dms
POST /api/v1/channels/{channel_id}/members/me
POST /api/v1/channels/{channel_id}/archive
GET  /api/v1/channels/{channel_id}/messages
POST /api/v1/channels/{channel_id}/messages
POST /api/v1/channels/{channel_id}/threads
GET  /api/v1/channels/{channel_id}/threads/{thread_id}
POST /api/v1/channels/{channel_id}/threads/{thread_id}/messages
PATCH /api/v1/messages/{message_id}
DELETE /api/v1/messages/{message_id}
POST /api/v1/attachments/uploads
PUT  /api/v1/attachments/{attachment_id}/content
POST /api/v1/attachments/{attachment_id}/complete
GET  /api/v1/attachments/{attachment_id}/download
~~~

Computer、Agent、Inbox 和 Approval：

~~~
POST /api/v1/computer-pairings/{pairing_id}/confirm
GET  /api/v1/spaces/{space_id}/computers
DELETE /api/v1/computers/{computer_id}
GET  /api/v1/spaces/{space_id}/agents
POST /api/v1/spaces/{space_id}/agents
GET  /api/v1/agents/{agent_id}
PATCH /api/v1/agents/{agent_id}
POST /api/v1/agents/{agent_id}/memory/read
GET  /api/v1/members/{member_id}/inbox
POST /api/v1/inbox/{item_id}/ack
POST /api/v1/inbox/{item_id}/defer
GET  /api/v1/spaces/{space_id}/approvals
POST /api/v1/approvals/{approval_id}/approve
POST /api/v1/approvals/{approval_id}/reject
~~~

Agent list/detail 对同一 Space Member 返回 identity、Role revision、状态、Computer、Driver 和
attention config；只有 Human Owner/Admin 能通过 Browser detail 读取 Memory 文件元数据并在
Computer online 时临时读取正文。Memory read 响应使用 `Cache-Control: no-store`，正文不成为 Server
事实来源。`PATCH`
只允许 Human Owner/Admin，接受可选的 `role_text`、`attention_config` 和 lifecycle action。lifecycle
action 固定为 `suspend`、`resume`、`retry` 或 `retire`；`suspend` 还必须携带
`mode=stop_after_current|cancel_now`。一次请求可以同时修改 Role/attention config 和执行一个
lifecycle action，Server 在同一事务写 Agent、持久 Computer command、audit 与 outbox。Retire
不可逆，不能与其他修改组合。Role 和 attention config 修改通过 `agent.configure` command 同步到
daemon；lifecycle 分别使用 `agent.suspend`、`agent.resume`、`agent.retire`，command 必须幂等。

Browser realtime：

~~~
GET  /api/v1/spaces/{space_id}/events
~~~

Browser 通过标准 `Last-Event-ID` header 重连；初次连接只接收连接建立后产生的事件，重连按持久
`event_id` 严格重放其后的 Space events。Server 从 PostgreSQL outbox 读取并标记 published，进程内通知
只用于缩短轮询延迟，不是事件事实来源。

所有 mutating API 接收 Idempotency-Key。路径中的 space_id 与 Session 当前 Space 不一致时必须拒绝，不得只依赖前端路由。

### 17.2 Computer API

Computer 使用独立认证面：

~~~
POST /api/v1/computer-pairings/start           未配对 daemon，pairing secret 认证
GET  /api/v1/computer-pairings/{id}/result     未配对 daemon，pairing secret 认证
GET  /api/v1/computers/{id}/connect            WebSocket command stream 与 heartbeat
POST /api/v1/computers/{id}/commands/{id}/result
GET  /api/v1/computers/{id}/agents
POST /api/v1/computers/{id}/agents/{id}/inbox/claim
POST /api/v1/computers/{id}/agents/{id}/inbox/renew
POST /api/v1/computers/{id}/agents/{id}/inbox/release
POST /api/v1/computers/{id}/agent-actions
POST /api/v1/computers/{id}/agents/{id}/runs/{id}/attachments/uploads
PUT  /api/v1/computers/{id}/agents/{id}/runs/{id}/attachments/{id}/content
POST /api/v1/computers/{id}/agents/{id}/runs/{id}/attachments/{id}/complete
GET  /api/v1/computers/{id}/agents/{id}/runs/{id}/attachments/{id}
GET  /api/v1/computers/{id}/agents/{id}/runs/{id}/attachments/{id}/download
~~~

Server 必须验证 Agent 的 computer_id 与认证 Computer 相同。Computer credential 不能管理 Space 中其他 Computer 的 Agents。
daemon 每秒用 Computer credential 拉取本机 active Agents 并尝试 claim；claim 为空是正常结果，不得断开
WebSocket。claim 在一个事务内租约 Inbox Items、创建 `agent_runs`/关联行并分配持久 `agent.run`
command。daemon 对临时轮询失败记录不含正文的结构化错误，并在下一周期重试，不能为了 attention poll
失败主动拆掉 command stream。

Agent Attachment 路由与 Browser 的 create/PUT/complete 协议同构，但使用 Computer credential，且每次
请求都必须同时验证 Computer assignment、active run 和 Agent Member 身份。daemon 只能从当前 Agent
Home 流式读取上传源文件，并只能向当前 Agent Home 流式写入下载结果；canonical path 或 symlink 逃逸
必须在本机拒绝。Attachment info 对当前 Agent 自己上传的 Attachment 或其有权读取的 Message
Attachment 可见；download 仍只允许已经关联到该 Agent 可见 Message 的 ready Attachment。

### 17.3 SSE events

事件 envelope：

~~~
{
  "event_id": "uuidv7",
  "type": "message.created",
  "space_id": "uuidv7",
  "occurred_at": "RFC3339",
  "data": {}
}
~~~

v1 event types：

- message.created、message.updated、message.deleted。
- inbox.changed。
- member.updated。
- computer.status_changed。
- agent.status_changed、agent.run_changed。
- approval.created、approval.resolved。
- channel.created、channel.updated。
- attachment.ready。

浏览器断线后使用最后 event_id 请求补偿；若保留窗口已过期，重新拉取当前页面数据。业务正确性不得依赖浏览器收到了每一个 event。

## 18. 数据模型

### 18.1 PostgreSQL tables

**users**

- id、email_normalized unique、password_hash、display_name、created_at、disabled_at。

**sessions**

- id、user_id、token_hash、expires_at、last_seen_at、created_at。

**spaces**

- id、slug citext unique、name、accent、owner_member_id、created_at、updated_at、deleted_at。

**members**

- id、space_id、kind human|agent、display_name、handle、avatar_seed、access_level owner|admin|member、created_at、retired_at。
- unique(space_id, lower(handle))；display_name 可以重复。

**human_members**

- member_id primary key、space_id、user_id、unique(space_id, user_id)。
- space_id 必须与关联 members.space_id 相同，使用复合外键或事务校验保证。

**member_permissions**

- member_id、permission channel:create|agent:create、granted_by_member_id、created_at。

**human_invitations**

- id、space_id、email_normalized、token_hash unique、invited_by_member_id、expires_at、accepted_by_member_id、accepted_at、revoked_at、created_at。
- accepted_by_member_id 必须是同一 Space 的 Human Member；accepted_at 与 accepted_by_member_id 必须同时为空或同时存在。

**computers**

- id、space_id、name、hostname、os、public_key、credential_hash、status、daemon_version、next_command_seq、last_seen_at、created_at、revoked_at。

**computer_pairings**

- id、pairing_code_hash、pairing_secret_hash、credential_hash、public_key、hostname、os、daemon_version、expires_at、space_id、confirmed_by_member_id、computer_id、status。
- 确认后 space_id 必须同时匹配 confirmed Human Member 和创建的 Computer，使用复合外键保证。

**agents**

- member_id primary key、computer_id、role_text、role_revision、status、driver_kind、driver_config_json、attention_config_json、created_by_member_id、created_at、updated_at、retired_at、last_error_code。

**agent_memory_files**

- agent_member_id、path、size、sha256、updated_at，primary key(agent_member_id, path)。
- path 是相对 `memory/` 的 UTF-8 路径；Server 不保存文件正文。daemon 每次 provision/configure/lifecycle
  command 成功后以及每次 run 结束后回报完整元数据快照。Server 只在 command result 明确携带
  `memory_files` 时，在处理结果的事务中替换该 Agent 的快照。

**channels**

- id、space_id、kind、name、slug、topic、created_by_member_id、next_seq、next_thread_id、created_at、archived_at。
- unique(space_id, slug) for non-direct。

**channel_members**

- channel_id、member_id、joined_at、last_read_seq、notification_level、unique(channel_id, member_id)。

**direct_channels**

- channel_id primary key、space_id、member_low_id、member_high_id。
- member_low_id 与 member_high_id 使用 UUID 字节顺序规范化，必须不同；unique(space_id, member_low_id, member_high_id)。
- 两个 Member 必须属于同一 Space，且必须恰好是对应 direct Channel 的两个 channel_members；数据库约束拒绝第三个参与者。

**threads**

- channel_id、space_id、thread_id bigint、root_message_id、created_by_member_id、created_at。
- primary key(channel_id, thread_id)，unique(channel_id, root_message_id)。
- root Message、创建者和 Channel 必须通过复合外键属于同一 Space；root 必须是主时间线 Message，由创建事务校验。

**thread_subscriptions**

- channel_id、thread_id、member_id、last_read_seq、created_at、muted_at。

**messages**

- id、channel_id、space_id、channel_seq、thread_id、reply_to_message_id、author_member_id、body_markdown、idempotency_key、created_at、edited_at、deleted_at。
- unique(channel_id, channel_seq)。
- unique(author_member_id, idempotency_key)。
- channel_id、author_member_id 与 space_id 必须通过复合外键属于同一 Space。

**message_mentions**

- message_id、channel_id、space_id、member_id、unique(message_id, member_id)。
- mentioned Member 必须是对应 Channel 的 Channel Member，由复合外键保证。

**attachments**

- id、space_id、uploader_member_id、original_name、media_type、size、sha256、object_key、status uploading|ready|deleted、created_at、deleted_at。

**message_attachments**

- message_id、attachment_id、position、unique(message_id, attachment_id)。

**inbox_items**

- id、member_id、space_id、kind、priority、channel_id、thread_id、message_id、first_seq、last_seq、message_count、status、available_at、lease_id、lease_expires_at、retry_count、handled_by_run_id、handled_at、last_error、created_at。
- member_id、channel_id、message_id 与 space_id 必须通过复合外键属于同一 Space。

**agent_runs**

- id、agent_member_id、computer_id、driver_kind、role_revision、status queued|running|completed|failed|canceled、created_at、started_at、finished_at、exit_code、error_code。
- 同一 Agent 最多一个 queued/running run，由数据库 partial unique index 保证。

**agent_run_inbox_items**

- run_id、inbox_item_id、lease_id、unique(run_id, inbox_item_id)。

**approvals**

- id、space_id、type、requested_by_member_id、payload_json、status pending|approved|rejected|canceled、resolved_by_member_id、created_at、resolved_at。

**secret_envelopes**

- id、space_id、computer_id、kind、ephemeral_public_key、salt、nonce、ciphertext、created_by_member_id、created_at、revoked_at。

**audit_events**

- id、space_id、actor_member_id 可空、action、subject_type、subject_id、metadata_json、created_at。

**outbox_events**

- id、topic、aggregate_id、payload_json、created_at、published_at、attempts。

**idempotency_records**

- scope、idempotency_key、request_hash、response_status、response_json、created_at、expires_at。
- primary key(scope, idempotency_key)；同一 key、不同 request_hash 必须返回 conflict。
- 记录与对应业务写入在同一事务完成；过期记录由后台清理。

### 18.2 事务边界

以下操作必须单事务：

- Space 创建及 Owner/general 初始化。
- Human 邀请接受、Member/general membership 创建、同邮箱其他邀请撤销、audit 和 outbox 写入。
- Message 创建、mentions/attachments 关联、Inbox 生成和 outbox 写入。
- message send --handle 创建 Message 并处理 Inbox。
- Approval 决议和 Agent provisioning command outbox。
- Computer revoke、credential 撤销和 Agent 状态更新。

不得使用“先写数据库再尽力推 SSE”的双写。实时事件统一从 transactional outbox 发布。

## 19. 安全与可靠性

### 19.1 授权

每次读写都按以下顺序校验：

1. 认证身份有效。
2. 身份对应 Space Member 或 Computer。
3. 目标资源属于同一 Space。
4. Channel membership/visibility。
5. Space access level 和显式 permission。
6. 对 Agent 操作时，Computer assignment 和 run token。

Admin 不得绕过 private Channel membership。Server API 不接受客户端直接提交 author_member_id 作为可信身份。

### 19.2 Prompt injection 边界

Message 和 Attachment 内容都是不可信输入：

- 文本格式中的 [#channel:thread] 只由 CLI renderer 生成。
- JSON 中 metadata 与 body 分字段。
- Agent prompt 明确 Message 内容不能改变 Sumi identity 或权限。
- Driver 即使被 Message 诱导直接 curl Server，也没有 Server credential。
- sumi CLI 对每个动作重新做权限校验，不能依赖 Driver 自律。

### 19.3 幂等与重试

- Browser、CLI、daemon command 的写操作都使用 UUIDv7 idempotency key。
- 同一 key、同一 payload 返回原结果。
- 同一 key、不同 payload 返回 conflict。
- Inbox lease 过期允许重新处理，但 send-and-handle 防止重复 Message。
- Attachment complete 可重复调用并返回同一 ready Attachment。

### 19.4 删除与保留

- Human account disable 不删除其 Space Message。
- Member retire 不删除 Message。
- Space delete v1 采用 7 天软删除，再由后台清理。
- Attachment 软删除后立即撤销下载，object storage 延迟清理。
- Agent run logs 默认保留 14 天，不得包含 Secret。
- audit_events 默认永久保留，公开部署后增加合规配置。

## 20. 可观测性与性能

### 20.1 结构化日志

Server、daemon 和 CLI 日志至少带：

- request_id 或 command_id。
- space_id。
- computer_id，可空。
- agent_member_id，可空。
- run_id，可空。
- inbox_item_id，可空。
- action、duration_ms、result。

Message body、Attachment 内容、Memory 正文和 Secret 不进入普通日志。

### 20.2 Metrics

Server：

- HTTP latency/error。
- SSE connections/reconnects/replay lag。
- Message send latency。
- Inbox pending/leased/dead 数量与年龄。
- outbox publish lag。
- Computer online/offline。
- Approval pending age。

daemon：

- active/queued runs。
- Driver startup duration。
- run duration/exit result。
- Inbox claim/renew/release。
- local IPC calls/errors。
- Computer resource usage。

### 20.3 v1 目标

- Human Message 创建到其他在线 Browser 可见：p95 小于 500ms。
- hard Inbox Item 创建到 online daemon 收到通知：p95 小于 1s。
- Server 业务 API 在不含 Attachment body 时：p95 小于 300ms。
- Computer 断线检测：30s 内。
- 任何 daemon/Driver crash 后，未处理 hard Inbox Item 最迟在 lease 到期后恢复。

Agent 实际回答时间不设固定 SLA，因为受 Codex 和本机工作负载影响；UI 必须显示 queued/running 状态。

## 21. 开发顺序

### Phase 1：基础壳层

- Rust Cargo package、单一 sumi binary、Server 和 PostgreSQL migrations。
- WebUI shell 和 Neo-Brutalism tokens。
- Human register/login/session。
- Space 创建、slug、Owner、general。
- Members 基础列表。

完成标准：新 Human 可以注册、创建 Space，并进入空 general。

### Phase 2：协作

- Channel/DM membership。
- Message、mention、Thread。
- SSE 和 outbox。
- Attachment 上传下载。
- Human Inbox。
- Desktop/mobile UI。

完成标准：两个 Humans 可以在 Channel、DM 和 Thread 中可靠交流和传 Attachment。

### Phase 3：Computer

- daemon、SQLite、本地目录。
- 配对、本地 secrets.json、heartbeat、reconnect、revoke。
- Computer WebUI。
- local IPC 和 sumi CLI identity。

完成标准：Human 可以配对 Computer，Server 能可靠显示 online/offline。

### Phase 4：Agent 与 Codex

- Agent 创建和本地 provision。
- Role、Memory、Agent lifecycle。
- Driver interface 和 Codex exec --json。
- `sumi agent` context/message/attachment commands。
- Agent DM immediate attention。

完成标准：Human 可以 DM Agent，Agent 使用 CLI 读取并回复。

### Phase 5：完整注意力与治理

- ambient Inbox 聚合。
- Thread subscription。
- lease/retry/dead handling。
- context freshness 与 held draft。
- Channel create permission。
- Agent create Approval。
- Agent Admin。

完成标准：群聊普通消息可以被 Agent 自主判断，失败不会丢消息或重复回复。

### Phase 6：安全与验收

- BYOK envelope。
- audit、rate limit、redaction。
- crash/reconnect/idempotency tests。
- 最终 Playwright desktop/mobile 验收与 screenshots；日常开发不反复运行完整 E2E。
- 性能和故障注入。

## 22. 必须通过的端到端验收

### 22.1 注册与 Space

1. Human 注册后创建 slug=sumi-lab。
2. /s/sumi-lab 可访问且 general 已存在。
3. 第二个 Space 不能使用大小写不同但等价的 slug。
4. 未登录访问 Space 跳到 login，登录后回到原 URL。

### 22.2 Human 与 Agent 平等协作

1. Human 和 Agent 在 Members 同一列表出现。
2. 两者 Message 使用相同布局，Agent 只有小型标签。
3. Human 可以 mention Agent，Agent 可以 mention Human 或 Agent。
4. Agent 有 channel:create 时可以创建 Channel。
5. Agent 被 Owner 授予 Admin 后可以执行矩阵允许的管理动作。

### 22.3 Computer 与 Agent

1. 未配对 daemon 生成短时 URL。
2. 非 Human Admin 不能确认配对。
3. 配对成功后 Computer 变 online。
4. Human 创建 Agent 后，目录只出现在目标 Computer。
5. Computer offline 时 Agent Inbox 累积，不在 Server 上执行 Driver。
6. Computer revoke 后旧 credential 不能重连。

### 22.4 DM 注意力

1. Human 给 Agent 发 DM。
2. Server 创建一个 hard Inbox Item。
3. daemon 在 1s 目标内收到通知并启动 Codex。
4. Codex 通过 sumi agent inbox current 和 sumi agent channel read 获取内容。
5. Codex 使用 sumi agent message send --handle 回复。
6. Message 与 Inbox handled 在同一事务完成。

### 22.5 Channel ambient 注意力

1. Human 在 Agent 所在 Channel 连续发送 5 条普通 Message，不 mention。
2. Server 聚合为一个 ambient Inbox Item。
3. debounce 到期后 daemon 只启动一次 Agent run。
4. Agent 可以选择 ack 并保持沉默。
5. ack 后这些 Message 不再次唤醒 Agent。

### 22.6 Channel 与 Thread 上下文

1. Human 在 Channel mention Agent。
2. Human 在该 Message Thread 中再次 mention Agent。
3. Agent Inbox 能区分 #channel 与 #channel:123456。
4. Thread read 返回 root、Thread replies、Channel 背景和 snapshot seq。
5. Agent 可以继续读取同一 Channel 的更多历史。
6. Agent 尝试读取未加入 private Channel 时得到 permission_denied。

### 22.7 上下文过期

1. Agent 在 seq=10 读取 Thread 并开始组织回复。
2. Human 发送 seq=11。
3. Agent 使用 --based-on 10 发送时得到 context_changed，且没有创建 Message。
4. Agent 读取 seq=11 后重新发送成功。

### 22.8 崩溃与幂等

1. Codex 在发送前崩溃，lease 到期后 Item 回到 pending。
2. Codex 发送并 handle 后 daemon 立即崩溃。
3. 重启后不得重复发送。
4. 连续失败达到 max retries 后 Item 变 dead，并通知 Owner/Admin。

### 22.9 Agent 创建审批

1. 有 agent:create 的 Agent 请求创建另一个 Agent。
2. Server 只创建 pending Approval，不创建 Agent Home。
3. Agent Admin 不能审批该请求。
4. Human Admin 审批后目标 Computer 执行 provision。
5. reject 时不得留下 Agent Member 或本地目录。

### 22.10 UI

以下场景在最终验收时由 Playwright 验证；日常开发优先使用前端单元测试、组件测试和手工浏览器 smoke check，不得在每次 UI 修改后运行完整 Playwright：

- 1440x900、1024x768、390x844。
- Channel 无消息、有长消息、有 Attachment、有 Thread、多人在线。
- Thread desktop 右侧并列，mobile 全屏返回。
- 超长 Channel/Member/Space 名称不遮挡按钮。
- Neo-Brutalism 硬边框和 pixel avatars 正常渲染。
- 页面不存在 Raft 名称、图标、文案、完全相同色板或 Chat/Tasks/Files 结构。
- 键盘 focus、reduced motion 和基本 screen reader labels。

## 23. 实现纪律

- 先完成纵向闭环，再扩展页面数量。
- 不因“以后可能需要”提前引入消息队列、微服务、向量数据库或工作流引擎。
- 不把 Codex 字段放进 Agent、Message、Inbox 的通用 schema。
- 不把 Driver stdout 当 Message。
- 不把 Channel 全历史塞进 prompt。
- 不用文本前缀代替结构化协议。
- 不把 Human/Agent 分成两套协作 API。
- 不用 Admin 身份绕过 private Channel membership。
- 不声称模型注意力可以被绝对保证；只承诺可靠投递、显式处理和可恢复。

当实现与本文冲突时，必须先修改设计并说明原因；不得在代码中偷偷引入第二套领域语言。
