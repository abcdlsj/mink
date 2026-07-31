# 代码组织与依赖边界

[返回设计索引](../design.md)

## 1. 组织目标

新版本继续编译为一个 `sumi` 可执行文件。Server、Computer 和 Agent CLI 使用同一个 Cargo package。

代码必须通过模块可见性建立边界。目录名称不能代替依赖约束。

组织方式必须满足以下要求：

- Server 业务规则不能被 Computer 直接调用。
- Computer 本地状态不能被 Server 直接读取。
- 两端只能通过版本化协议交换命令、快照和回执。
- 领域规则、流程编排和外部适配器分别归属不同模块。
- 每个模块只公开上层完成用例所需的接口。
- 共享代码只保存确实由两端共同解释的协议或标识。

暂不拆分多个 Cargo crate。Server 和 Computer 使用同一个发布周期，Rust 限定可见性可以阻止跨边界调用。拆分 crate 会增加 manifest 和依赖管理，但当前不会增加运行时隔离。出现独立发布、独立依赖或模块可见性无法约束的需求时，再通过 ADR 决定是否拆分。

## 2. 运行时边界

### 2.1 Server

Server 持有以下职责：

- Space、Member、权限和 Agent assignment。
- Channel、Message、Thread、Attachment、Task、Inbox Item 和 Run 事实。
- 领域状态转换、并发约束和事务。
- PostgreSQL、对象存储和事务 outbox。
- Browser API、Computer API、SSE 和 WebSocket command 投递。
- Computer command 的持久化、排序、重试和回执验证。

Server 不得持有以下状态或能力：

- Driver 进程和 Provider Session locator。
- Provider transcript 和隐藏推理。
- Agent workspace、Memory 正文和模型凭据。
- Computer 的本地重试、进程句柄和 sandbox 状态。
- 根据 Message 正文推断 Task 或 Thread 关系的逻辑。

### 2.2 Computer

Computer 持有以下职责：

- 到 Server 的出站连接和断线重连。
- Computer SQLite、本地 command 幂等和 result outbox。
- 本机执行槽、Run supervisor 和 Driver 进程。
- Provider Session registry、resume、reset 和失效处理。
- Agent Home、workspace、Memory、凭据和 sandbox。
- Agent CLI 的本地认证、Run 上下文注入和能力转发。

Computer 不得持有以下事实或决定：

- Task、Message、Thread、Inbox Item 或 Run 的正式状态。
- Space 权限、Task 状态转换和 Result 成立条件。
- Thread 是否属于 Task 的判断。
- 根据正文创建、合并或关联 Task 的路由规则。
- Server 事务的本地镜像。

Computer 可以缓存 Server 下发的 Run 快照。该快照只用于执行，不能作为断线期间修改 Server 事实的依据。

### 2.3 Agent CLI

Agent CLI 是当前 Run 的本地客户端。它只向 Computer 请求读取上下文或执行能力。

CLI 不保存领域状态。CLI 不要求 Agent 提交可由 Run token、Focus 或资源关系推导的字段。

### 2.4 Driver

Driver 是 Computer 内的执行适配器。Driver 只实现启动、resume、steer、interrupt 和结果收集契约。

Driver 不访问 PostgreSQL、Server repository 或 Browser API。Driver output 不能直接修改 Message、Task、Run 或 Result。

## 3. 顶层目录

```text
src/
  main.rs
  cli.rs
  config.rs
  ids.rs

  protocol/
    mod.rs
    version.rs
    computer.rs
    capability.rs

  server/
    mod.rs
    domain/
      mod.rs
      access.rs
      identity.rs
      conversation.rs
      attachment.rs
      task.rs
      attention.rs
      execution.rs
      invitation.rs
      pairing.rs
    application/
      mod.rs
      identity.rs
      conversation.rs
      attachment.rs
      task.rs
      attention.rs
      execution.rs
      computer.rs
      invitation.rs
      ports.rs
    adapters/
      mod.rs
      http.rs
      websocket.rs
      postgres.rs
      object_storage.rs
      realtime.rs
      credential.rs
      openapi.rs
      query.rs
      runtime.rs

  computer/
    mod.rs
    core/
      mod.rs
      scheduler.rs
      supervisor.rs
      session.rs
      home.rs
      input.rs
    application/
      mod.rs
      command.rs
      query.rs
      run.rs
      recovery.rs
      scheduler.rs
      capability.rs
      ports.rs
    adapters/
      mod.rs
      server_connection.rs
      sqlite.rs
      local_ipc.rs
      filesystem.rs
      sandbox.rs
    drivers/
      mod.rs
      contract.rs
      runtime.rs
      codex.rs
      builtin.rs
      builtin_runtime/

  agent_cli/
    mod.rs
    client.rs
    commands.rs
```

每个模块用一个文件表达，规则数量超出单文件可读范围时才拆为目录；`builtin_runtime/`是当前唯一需要拆分的模块。文件与目录的选择不改变依赖方向。

`main.rs`、`cli.rs`和`config.rs`是运行时入口与配置装配，只负责解析参数、读取配置并调用两个 facade。

`ids.rs`只定义跨边界使用的不透明标识。该文件不得加入状态、领域规则、DTO 或通用 helper。

`protocol/`只定义两端必须共同解释的 wire 类型、错误码和版本。Browser OpenAPI schema 归属 Server HTTP adapter，不进入该目录。

`server/domain/`是 Server 业务模型。Computer、Driver、protocol 和 Agent CLI 都不能依赖该目录。

`computer/core/`只表达本地执行规则。它不能引用 Server 领域对象或传输 frame。

## 4. 模块职责

### 4.1 Domain 和 Core

`server/domain/`保存聚合、值对象、状态转换和领域错误。它不执行 SQL，不解析 HTTP，也不发送事件。

`computer/core/`保存本地 scheduler、Run、Session 和 Agent Home 的不变量。它不连接 Server，不读写 SQLite，也不调用 Driver SDK。

### 4.2 Application

Application 模块编排一个完整用例。它负责调用领域对象、端口和事务边界。

Server transaction service 归属 `server/application/`。每个写用例只能有一个 transaction service。

Computer application service 负责 command 执行、Run 编排和本地恢复。它不能确认 Server 领域状态已经改变，只能生成带幂等标识的回执或结果请求。

`ports.rs`定义 application 需要的最小外部能力。端口按业务动作命名，不按数据库表命名。

### 4.3 Adapters

Adapter 把外部格式转换为 application input，并实现 application port。

- HTTP adapter 负责认证输入、DTO、状态码和 OpenAPI。
- WebSocket adapter 负责协议 frame、连接和 ACK 运输。
- PostgreSQL adapter 负责 SQL、行锁和行到领域对象的转换。
- SQLite adapter 负责 Computer 本地恢复状态。
- Driver adapter 负责 provider SDK 和进程协议。

Adapter 不复制领域判断。多个 adapter 触发同一行为时，必须调用同一个 application service。

## 5. 依赖方向

允许的内部依赖如下：

```text
调用方 -> 被依赖方

main/cli -> server public facade
main/cli -> computer public facade
protocol -> ids
server/domain -> ids
server/application -> server/domain
server/adapters -> server/application
server/adapters -> protocol
computer/core -> ids
computer/application -> computer/core
computer/adapters -> computer/application
computer/adapters -> protocol
computer/drivers -> computer/application
computer/drivers -> computer/core
agent_cli -> protocol/capability
```

Adapter 依赖 application，application 依赖 domain 或 core。内层不能反向依赖外层。

禁止以下依赖：

- `server/domain -> protocol | adapters | computer`
- `server/application -> computer | Driver SDK`
- `computer/core -> protocol | server | Driver SDK`
- `computer/application -> server/domain`
- `protocol -> server | computer | Driver SDK`
- `computer/drivers -> server | PostgreSQL`
- `agent_cli -> server/domain | computer/core | SQLite`

禁止使用 `common`、`utils` 或 `shared` 目录绕过以上边界。复用代码必须归入拥有该规则的模块，或证明它是协议的一部分。

## 6. 数据转换边界

每类数据只允许在边界转换一次：

1. HTTP DTO 在 Server HTTP adapter 转为 application input。
2. PostgreSQL row 在 PostgreSQL adapter 转为 Server domain object。
3. Server domain event 在事务提交前转为 outbox record。
4. Computer command 在 Server WebSocket adapter 转为 protocol frame。
5. protocol frame 在 Computer connection adapter 转为 Computer application input。
6. Driver SDK 类型在 Driver adapter 转为 Computer core result。
7. Computer result 在 connection adapter 转为 protocol receipt。

Application 和内层模块不得接受 `serde_json::Value`、HTTP request、SQL row、WebSocket frame 或 Provider SDK 类型。

协议 payload 必须使用 tagged enum 和明确 struct。只有明确为开放扩展数据的字段可以使用 JSON object。

## 7. 可见性规则

- `server::run`和`computer::run`是两个运行时的 crate 内入口。
- 顶层模块默认私有。
- Server 内部跨模块接口使用`pub(in crate::server)`。
- Computer 内部跨模块接口使用`pub(in crate::computer)`。
- 单个父模块内的接口使用`pub(super)`。
- 跨顶层边界的 facade 和 wire 类型使用`pub(crate)`。
- 当前 binary crate 禁止使用无范围限制的`pub`。
- `mod.rs`导出模块能力，不导出内部文件结构。
- 测试不得通过扩大生产 API 可见性访问内部状态。

编译期边界优先于代码审查约定。发现循环依赖时，必须重新确定规则所有者，不能通过全局 service locator、动态 JSON 或 callback registry 绕开。

## 8. 协议版本

Computer 与 Server 的协议定义归属`protocol/`。协议版本适用于完整连接，不为单个 command 建立隐式版本。

握手必须交换以下信息：

- Server 支持的协议版本范围。
- Computer 支持的协议版本范围。
- daemon 版本和能力集合。

双方没有共同版本时拒绝连接，并返回稳定错误码。新版本基线不兼容旧协议。

协议字段变化必须先修改[API 与事件](./07-api.md)，再修改 wire 类型和两端 adapter。领域模块不能为兼容旧 frame 增加分支。

## 9. 测试组织

单元测试与被测模块同文件，使用`#[cfg(test)] mod tests`；模块的测试量超出单文件可读范围时，拆出同级`tests.rs`并由`mod.rs`挂载。

```text
src/server/domain/*.rs              状态转换和领域不变量
src/server/application/tests.rs     用例流程和事务编排
src/computer/core/*.rs              调度、Run 和 Session 流程
src/computer/application/tests.rs   command、恢复和幂等流程
src/server/adapters/*.rs            SQL 约束、DTO 边界和事件过滤
src/computer/drivers/*.rs           Driver 契约与进程协议
tests/architecture_boundaries.rs    模块依赖与可见性
tests/registration_space.rs         注册、Space 与治理路由
tests/governance_routes.rs          治理动作的授权与状态规则
tests/space_invitation.rs           Invitation 全流程
tests/inbox_direct_message.rs       Inbox 投影与 DM
tests/attachment_flow.rs            Attachment 上传与下载
tests/cli.rs                        命令行入口
```

内层单元测试使用内存端口。SQL 约束、进程生命周期和网络重连只在对应 adapter 或集成测试中验证。

集成测试按流程命名，不按被测层命名。新增流程时增加文件，不把断言塞进既有文件。

单元测试的取舍规则由[交付与验收](./10-delivery-acceptance.md)定义。测试目录不能按函数或数据类型机械生成。

架构测试必须扫描 Rust 模块依赖，拒绝本文件列出的禁止依赖。`cargo clippy`不能替代该检查。

## 10. 重组顺序

代码重组与行为重建使用同一条组装路径：

1. 建立`ids`、`protocol`和两个运行时 facade。
2. 建立新的 Server domain 和 application，并使用内存端口独立测试。
3. 建立新的 Server adapters，并使用 adapter contract test 独立测试。
4. 建立 Computer core、application 和本地 ports。
5. 将 Driver、SQLite、连接和 Agent CLI 接入新端口，并测试 Computer 内部流程。
6. 完成 WebUI 后，一次性切换运行时入口和 Browser API 到新实现。
7. 在同一切换任务中删除旧根目录模块、旧协议和旧 schema。
8. 运行完整集成、故障和端到端验收。

每一步必须保持依赖单向。新旧实现之间不得增加 bridge、兼容 adapter、调用转发或双写。
