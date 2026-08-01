# 产品基础与系统结构

[返回设计索引](../design.md)

## 1. 产品定义

Sumi 让 Human 与 Agent 在同一个 Space 中持续协作。

Agent 是具有身份、Role、Memory 和工作连续性的 Member。Agent 可以接收新消息、继续已有 Task、被打断，并在不同工作之间明确转向。

Sumi 不把 Agent 等同于一次模型调用。Run 可以结束，Provider Session 可以丢失，Computer 可以离线。

Agent 身份、Message、Task 和 Result 必须继续存在。

## 2. 事实来源

新版本只使用以下事实来源：

| 事实 | 唯一持有者 |
| --- | --- |
| Space、Member、权限 | Server PostgreSQL |
| Channel、Message、Thread、Attachment | Server PostgreSQL 和对象存储 |
| Task、Linked Thread、Result | Server PostgreSQL |
| Inbox Item、Run、租约和结果回执 | Server PostgreSQL |
| Agent Role 与 Computer assignment | Server PostgreSQL |
| Provider Session、workspace、Memory | Computer 本地状态 |
| Driver 进程、当前 turn、本地 outbox | Computer 本地状态 |

WebSocket、SSE、进程内 channel、Provider Session 和 Browser cache 都不是事实来源。

当前代码不是产品事实来源。实现与本文档冲突时，必须修改或删除实现。不得通过兼容分支同时保留两套行为。

## 3. 产品不变量

1. Human 和 Agent 使用相同的 Space、Channel、DM、Thread、Message 和 Attachment 模型。
2. Task 必须从一条 Root Message 原子创建。创建成功时，Source Thread 绑定已经存在。
3. Thread reply 不能成为 Task source。
4. 一个 Task 可以关联多个 Threads。一个 Run 只处理一个 Focus，并可以暂时不属于 Task。
5. 一个 Agent 同时最多有一个 active Run。
6. Run 必须有界。新消息可以影响 active Run，但不能让同一个 Run 永远存活。
7. Provider Session 可以跨 Runs 复用。复用不按 token、Run 数量或固定时间决定。
8. Provider Session 丢失不得改变 Task、Message、Result 或 Inbox 状态。
9. Server 和 daemon 不读取正文来猜测 Task 归属。
10. Agent 不负责补写可由系统从请求上下文确定的关系。

## 4. v1 范围

### 4.1 必须实现

- Human 注册、登录、Space、Member 和权限治理。
- Channel、DM、Message、Thread、mention 和 Attachment。
- Computer 配对、在线状态、撤销和本地 Agent Home。
- Agent 创建、Role、Memory、暂停、恢复和退役。
- Codex Driver 与 Builtin Driver。
- Human 和 Agent Inbox。
- Root Message 到 Task 的一步创建。
- Task 状态、assignee、Linked Threads 和 Result。
- Run、Focus、active Run steering、yield 和可靠恢复。
- 同一 Task 跨 Runs 的 Provider Session resume。
- Browser HTTP API、Computer WebSocket 和 Browser SSE。
- Task、Thread、Agent 和 Computer 的 WebUI。

### 4.2 明确不做

- 子任务、Task 依赖、工时、截止时间、优先级和审批流。
- 根据正文自动创建、合并或绑定 Task。
- 一个 Run 同时执行多个 Focus。
- Agent 并行运行多个 Runs。
- Provider Session 跨 Agent、跨 Task或跨无关Threads复用。
- Server 保存 Provider Session 正文。
- Browser 输入模型 API key。
- 旧数据迁移和旧 API 兼容。
- Windows 运行支持。

## 5. 系统结构

```text
Browser
  | HTTPS + SSE
  v
Server
  | PostgreSQL + object storage
  | HTTPS + outbound Computer WebSocket
  v
Computer daemon
  | local IPC
  +-- Agent CLI
  +-- Run supervisor
  +-- Provider Session registry
  +-- Driver adapter
  +-- Agent Home
```

### 5.1 Server

Server 是模块化单体。Server 负责身份、权限、协作事实、Task、Inbox、Run 调度、事务和实时事件。

Server 不执行模型，不访问 Agent workspace，也不保存 Provider Session 或 Memory 正文。

### 5.2 Computer

Computer daemon 是本地执行控制面。它负责 Driver 进程、Provider Session、Agent Home、sandbox、本地 outbox、断线恢复和凭据。

Computer 不决定 Task 关系，也不修改 Server 事实的含义。

### 5.3 Agent CLI

Agent CLI 是 Run 内的受限能力接口。CLI 从当前 Run 获得 Agent、Focus和可选Task，不要求 Agent重复提交这些可推导字段。

### 5.4 Browser

Browser 只通过 Server API 修改事实。SSE 用于刷新投影，不承担交付保证。

## 6. 模块边界

Server、Computer、Agent CLI 和 Driver 的代码职责、目录与依赖方向由[代码组织与依赖边界](./11-code-organization.md)定义。

以下行为必须各自只有一个入口：

| 行为 | 唯一入口 |
| --- | --- |
| 从 Root Message 创建 Task | `server::application::task::CreateTaskFromRootMessage` |
| 关联 Related Thread | `server::application::task::LinkThreadToTask` |
| Task 终态与 Result | `server::application::task::RecordTaskOutcome` |
| 领取并创建 Run | `server::application::execution::ClaimRun` |
| 路由 hard Item 到 active Run | `server::application::attention::RouteHardItem` |
| 完成 Run | `server::application::execution::CompleteRun` |
| 解析 Provider Session | `computer::core::session::resolve` |

`RecordTaskOutcome`同时服务 Browser 与 Agent CLI：Agent 调用时附带 Run 与 fencing token 并在同一事务完成 Run，Browser 调用时不持有 Run。`submit_review`、`done`和`close`不得再有第二个事务入口。

## 7. 技术基线

- Server、Computer 和 Agent CLI 使用 Rust，并编译为一个 `sumi` 可执行文件。
- Server 使用 axum、tokio、sqlx 和 PostgreSQL。
- Server 未配置数据库地址时连接`postgres://localhost/sumi_prod`。本地开发任务显式连接可重建的`postgres://localhost/sumi_dev`。
- Server 和 Computer 默认读取`$HOME/.sumi/config.toml`；文件不存在时使用代码默认值。显式`--config`路径覆盖该默认文件。
- `dev-seed`从该默认文件生成隔离的 Computer 配置，保留 Driver 配置，并覆盖本地 Server URL、Computer 状态根目录和配对浏览器行为。
- Computer 本地状态使用 SQLite。
- Browser 使用 React、TypeScript、Vite、TanStack Router 和 TanStack Query。
- Browser API 类型从 Rust OpenAPI 生成。
- Attachment 内容使用 S3-compatible storage；本地测试使用临时目录 adapter。
- Computer 只建立出站连接。Browser 使用 HTTPS，Computer 使用 HTTPS 和 WebSocket。
- Rust 和 Web 依赖使用锁文件固定。

技术选型不能反向改变领域边界。ORM、消息总线或 Driver SDK 不得生成第二套 Task、Run 或 Session 模型。

## 8. 数据版本策略

新版本从空 PostgreSQL 和空 Computer SQLite 创建基线 schema。基线冻结前，可以重建本地数据库。

基线进入共享环境后的变更规则见[数据库设计](./08-database.md)。

不得实现以下内容：

- 旧表迁移脚本。
- 兼容读取或回退查询。
- 新旧字段双写。
- deprecated DTO。
- 旧 CLI alias。
- 根据旧数据形状推断新关系。

如果旧实现包含可复用的安全、传输或存储代码，必须先证明该代码不携带旧领域假设，再将其接入新模块。
