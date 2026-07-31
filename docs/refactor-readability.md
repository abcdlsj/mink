# 可读性与维护成本重构方案

- 状态：Proposed
- 日期：2026-07-31
- 适用范围：`src/server/` 与 `src/computer/` 的模块划分、领域字段封装和端口划分
- 相关规范：[代码组织与依赖边界](./design/11-code-organization.md)

## 1. 目标与验收标准

本次重构只降低阅读和修改成本，不改变产品行为、协议和数据库 schema。

完成标准：修改一项 Task 行为时，需要阅读的文件是 Task domain、Task application 和对应 adapter 三处，不需要浏览身份、附件和 Computer 代码。

本方案不做以下事情：

- 不引入 command bus、统一 `ServerCommand` 枚举或用例分发层。`server/application/` 当前有 58 个具名 unit struct 用例（形如 `CreateTaskFromRootMessage::execute(&mut transaction, input)`）已经表达入口，加一层枚举分发只增加间接层。
- 不引入独立的 persistence record 类型层。PostgreSQL adapter 直接构造领域对象在受检入口下是可接受的。
- 不为每个领域字段生成 getter。
- 不拆分 Cargo crate。该决定的依据见 [代码组织与依赖边界](./design/11-code-organization.md) 第 1 节。

## 2. 当前状态

以下数据由本方案编写时实测得到，作为拆分决策的依据。

### 2.1 文件规模

| 文件 | 行数 | 承担的职责 |
| --- | --- | --- |
| `src/server/adapters/runtime.rs` | 6322 | 路由组装（64 处 `.route`）、HTTP handler、29 处 DTO、错误转换、连接状态维护、WebSocket 与 SSE、测试模块 |
| `src/server/adapters/postgres.rs` | 4423 | 198 处 SQL、行锁、10 类领域对象重建 |
| `src/server/application/tests.rs` | 4311 | 用例流程与事务编排测试 |
| `src/server/application/ports.rs` | 801 | 7 个 trait，其中 `ServerTransaction` 单个 trait 92 个方法 |

`runtime.rs` 第 5380 行起是 `#[cfg(test)]` 模块，生产代码为前 5379 行。

### 2.2 HTTP 层越过 application 直接访问数据库

`runtime.rs` 生产区有 88 处查询调用（51 处 `sqlx::query`，37 处 `sqlx::query_scalar`）。其中 10 处是写操作，78 处只读。

写操作按表分布：

| 表 | 操作 | 处数 |
| --- | --- | --- |
| `computers` | `UPDATE` | 3 |
| `outbox_events` | `INSERT` | 2 |
| `inbox_items` | `UPDATE` | 1 |
| `agents` | `UPDATE` | 1 |
| `idempotency_records` | `INSERT` | 1 |
| `channel_members` | `INSERT` | 1 |
| `audit_events` | `INSERT` | 1 |

78 处只读查询不等于 78 处纯投影。`runtime.rs:3026` 的 `SELECT ... FOR UPDATE OF c` 取行锁后判断 `access_level` 与 `kind`，结果直接决定权限与状态转换，属于领域判断。阶段 1 需要逐处区分投影与判断。

`runtime.rs:3020-3105` 的 add channel members handler 在 HTTP 层完成了权限校验、幂等锁、`channel_members` 插入、`audit_events` 与 `outbox_events` 写入，没有对应的 application 用例。`runtime.rs:1310` 写 `inbox_items.last_error_code`、`runtime.rs:1599` 写 `agents.lifecycle` 属于同类。

这类代码违反 [代码组织与依赖边界](./design/11-code-organization.md) 第 4.3 节的"Adapter 不复制领域判断"。`tests/architecture_boundaries.rs:47` 的 `assert_handler_does_not_own_space_transaction` 只检查了 `create_space` 一个 handler。

### 2.3 领域对象字段可被适配器直接改写

`server/domain/` 的聚合字段全部标注 `pub(in crate::server)`，而 `server/adapters/` 也属于 `crate::server`，因此适配器可以绕过领域方法：

- `postgres.rs:1227` 用 `Thread { .. }` 字面量重建对象，绕过 `Channel::create` 一类工厂的校验。10 类聚合都有这种重建路径。
- `runtime.rs:2606` 直接赋值 `agent.memory_files`。
- `runtime.rs:4520` 直接赋值 `task.session_continuity`。

有状态转换的聚合字段数：

| 类型 | 字段数 | 是否需要收紧 |
| --- | --- | --- |
| `InboxItem` | 16 | 需要 |
| `Task` | 14 | 需要 |
| `Run` | 14 | 需要 |
| `LocalRun`（computer） | 14 | 需要 |
| `Attachment` | 11 | 需要 |
| `ProviderSession`（computer） | 9 | 需要 |
| `Channel` | 8 | 不需要 |
| `Message` | 8 | 不需要 |
| `Agent` | 8 | 不需要 |
| `Member` | 6 | 不需要 |
| `Thread` | 5 | 不需要 |
| `Computer` | 5 | 不需要 |

### 2.4 可见性标注不在本次范围内

`src/` 有 1075 处 `pub(in crate::server)` 或 `pub(in crate::computer)`，325 处 `pub(crate)`，456 处 `pub(super)`。

这些标注不减少可读性负担时也不得改成裸 `pub`。原因：`tests/architecture_boundaries.rs:89` 的 `assert_source_has_scoped_visibility` 拒绝任何以 `pub ` 开头的行，[代码组织与依赖边界](./design/11-code-organization.md) 第 269 行也规定当前 binary crate 禁止无范围限制的 `pub`。本方案实测过全量替换：编译通过，架构测试失败。

因此本方案不改动可见性写法，只在第 5 阶段把有状态实体的字段改为私有。

### 2.5 其他

`src/` 有 60 处 `crate::server::domain::模块::类型` 形式的全限定路径出现在表达式位置。

## 3. 执行前提

开始第 1 阶段前必须先处理工作区的未提交改动。本方案编写时工作区存在以下改动，均不属于本次重构：

- `src/computer/adapters/sqlite.rs:787` 有一行 `tracing::error!(%error, "DIAG sqlite error")` 临时诊断日志。该日志必须删除或单独提交，不得混入重构提交。
- `docs/design/10-delivery-acceptance.md` 与 `docs/design/11-code-organization.md` 有关于 `tests/failure_recovery.rs` 的改动。
- `tests/failure_recovery.rs` 是未跟踪的新文件，`tests/support/mod.rs` 有配套改动。

每个阶段独立提交，独立验证，可独立回退。

## 4. 阶段 1：盘点 runtime.rs 的数据库访问

不修改代码。产出一张分类清单。

对 `runtime.rs` 前 5379 行的 88 处 `sqlx::query` 逐处判定归属：

| 归属 | 判定依据 | 后续处理 |
| --- | --- | --- |
| 只读投影 | 只做 `SELECT`，结果直接转 HTTP 响应，不参与状态判断 | 阶段 2 移入 HTTP 子模块或独立查询模块 |
| 缺失的用例 | 有写操作，或读取结果用于领域判断（权限、状态转换、幂等） | 阶段 6 补 application 用例 |
| 适配器自有职责 | 维护连接与传输状态，不表达领域事实 | 保留在 adapter |

`computers.connection_status`、`computers.last_seen_at` 的更新属于第三类：这是 WebSocket 连接生命周期的一部分，不是领域状态转换。

`channel_members` 插入、`agents.lifecycle` 更新、`inbox_items.last_error_code` 更新、`audit_events` 与 `outbox_events` 写入属于第二类。

**为什么先盘点：** 直接进入阶段 2 会把这些越层 handler 原样搬进 `http/conversation.rs` 一类文件，使它们在新目录里看起来与正规 handler 无异，后续更难识别。先分类，阶段 2 的拆分线才是按职责划的。

**产出：** 一份清单，每处 `sqlx::query` 标注行号、归属类别和判定依据。清单可写在阶段 1 的提交说明里，或临时文件中，不进入 `docs/`。

**验证：** 清单覆盖全部 88 处，无遗漏。

## 5. 阶段 2：拆分 runtime.rs

按阶段 1 的清单拆分，不改变行为。

```text
src/server/adapters/http/
  mod.rs            Router 组装与共享 state
  identity.rs
  conversation.rs
  task.rs
  execution.rs
  computer.rs
  attachment.rs
  dto.rs            请求与响应 DTO
  error.rs          ApplicationError 到 HTTP 状态码的映射
```

现有 `src/server/adapters/http.rs`（322 行）承担认证输入与 `WriteContext`，并入 `http/mod.rs` 或按职责分入 `identity.rs` 与 `error.rs`。

WebSocket 与 SSE 相关代码移入现有 `adapters/websocket.rs` 与 `adapters/realtime.rs`。运行时装配（`PgPoolOptions`、`ServeDir`、`TraceLayer`）保留在一个装配入口。

`runtime.rs` 第 5380 行起的测试模块随被测代码分散到对应文件的 `#[cfg(test)] mod tests`，符合 [代码组织与依赖边界](./design/11-code-organization.md) 第 9 节。

**拆分标准：** 修改 Task 的 HTTP 行为时不需要打开身份、附件或 Computer 文件。不按行数均分。

**约束：** 本阶段不改任何 SQL、不补用例、不改可见性写法。阶段 1 判定为"缺失的用例"的代码原样移动，在提交说明中列出它们的新位置，供阶段 6 使用。

**验证：**

```bash
cargo fmt --all -- --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test
```

`tests/architecture_boundaries.rs:48` 硬编码了 `src/server/adapters/runtime.rs` 路径和 `async fn create_space(` 的位置。本阶段必须同步更新该测试指向新文件，不得因为路径变化而删除该断言。

## 6. 阶段 3：拆分 postgres.rs

```text
src/server/adapters/postgres/
  mod.rs            PostgresAdapter、事务入口、ServerTransaction 实现装配
  identity.rs
  conversation.rs
  task.rs
  attention.rs
  execution.rs
  attachment.rs
  rows.rs           行到领域对象的转换
```

`rows.rs` 集中当前分散的 10 处领域对象重建代码。阶段 5 会给这些重建加受检入口，集中放置便于一次改完。

**约束：** 不改 SQL 语句内容，不改事务边界。

**验证：** 同阶段 2。SQL 约束相关测试必须全部通过。

## 7. 阶段 4：清理全限定路径

把 60 处表达式位置的 `crate::server::domain::模块::类型` 改为文件顶部 `use`，表达式保留 `类型::变体` 形式：

```rust
use crate::server::domain::attention::InboxItemDisposition;

// 表达式位置
InboxItemDisposition::Handled
```

`mod.rs` 可以导出稳定概念，不得把所有类型平铺成单一 prelude。读者看到导入路径时应能判断类型归属。

**约束：** 不改可见性写法。原因见第 2.4 节。

**验证：** 同阶段 2。本阶段是纯机械改动。

## 8. 阶段 5：私有化有状态实体的字段

只处理第 2.3 节标记为"需要"的六个类型：`InboxItem`、`Task`、`Run`、`Attachment`、`LocalRun`、`ProviderSession`。

其余类型（`Thread`、`Channel`、`Message`、`Agent`、`Member`、`Computer`）基本是数据载体，字段公开不产生绕过状态转换的风险，不动。

### 8.1 字段可见性

```rust
pub(in crate::server) struct Task {
    id: TaskId,
    status: TaskStatus,
    result_message_id: Option<MessageId>,
}
```

字段声明去掉 `pub(in crate::server)`，struct 本身保留。

### 8.2 读取接口的判断标准

不为每个字段生成 getter。`Task`、`Run`、`InboxItem` 各有 14 到 16 个字段，机械生成会给 `domain/` 增加 400 行以上噪音，与本方案目标相反。

判断标准：

- **加 getter**：字段被跨聚合的决策读取。例如 application 需要读 `Task::status` 来选择分支。
- **加只读 view**：一批字段被同一处消费，通常是响应构造。用 `TaskView<'a>` 一次返回。
- **不加**：只在聚合内部使用。

```rust
pub(in crate::server) struct TaskView<'a> {
    pub(in crate::server) id: TaskId,
    pub(in crate::server) status: TaskStatus,
    pub(in crate::server) title: &'a str,
}
```

view 是投影，不是第二套字段定义。view 只允许由领域方法构造，不允许 adapter 用 view 反向写回聚合。同一聚合的 view 不超过两个；超过说明消费方需要的是 getter。

### 8.3 三类结构的区分

| 类型 | 判定 | 可见性策略 |
| --- | --- | --- |
| 领域实体 | 维护状态转换或生命周期不变量 | 字段私有，通过领域方法修改 |
| application input 与 output | 只是用例的入参与出参 | 字段 `pub(in crate::server)`，供 adapter 构造和读取 |
| 查询投影、SQL row、wire DTO | 只承载数据 | 普通数据结构，不封装 |

`CreateTaskInput`、`RecordTaskOutcomeInput`、`UpdateTaskInput` 一类属于第二类，字段保持公开。本阶段不动它们。

**验证：** 同阶段 2。领域状态转换测试必须全部通过。

## 9. 阶段 6：为数据库重建与缺失用例补受检入口

### 9.1 领域对象重建

阶段 5 之后 `postgres/rows.rs` 无法再用字面量构造私有字段的聚合。为这六个类型各加一个重建入口：

```rust
impl Task {
    pub(in crate::server) fn rehydrate(snapshot: TaskSnapshot) -> Result<Self, DomainError> {
        // 校验从数据库读出的组合是否是合法状态
    }
}
```

`rehydrate` 与 `create` 的区别：`create` 校验业务前置条件并生成初始状态，`rehydrate` 只校验字段组合本身合法（例如 `status` 为终态时 `result_message_id` 必须存在）。`rehydrate` 不重复执行 `create` 的前置校验。

`rehydrate` 只允许 `postgres/rows.rs` 调用。

### 9.2 补齐缺失的用例

按阶段 1 清单中判定为"缺失的用例"的条目，逐条在 `server/application/` 补 use case，把 HTTP handler 里的领域判断、幂等锁、`audit_events` 与 `outbox_events` 写入移入 application 与 domain。

用例形式沿用现有约定：

```rust
pub(in crate::server) struct AddChannelMembers;

impl AddChannelMembers {
    pub(in crate::server) async fn execute(
        transaction: &mut impl TransactionPort,
        input: AddChannelMembersInput,
    ) -> Result<ChannelMembersOutput, ApplicationError> {
    }
}
```

不需要持有依赖时可以用普通函数代替 unit struct。两种形式在同一模块内保持一致，不混用。

**风险：** 本阶段改动幂等记录与 outbox 写入的位置，是全方案风险最高的一步。逐个用例提交，每个用例单独验证。

**为什么放在最后：** 阶段 2 到 5 是结构移动，出问题容易定位。本阶段涉及事务内写入顺序，需要在结构已经清晰之后再做。

**验证：** 同阶段 2，加上：

```bash
cargo test --test governance_routes
cargo test --test registration_space
cargo test --test inbox_direct_message
```

每个补齐的用例必须有 application 层测试覆盖其幂等行为。完成后把 `tests/architecture_boundaries.rs` 的 `assert_handler_does_not_own_space_transaction` 扩展为覆盖全部 HTTP handler 的检查，而不是只检查 `create_space`。

## 10. 阶段 7：拆分 ports.rs

放在最后，因为拆分依据是阶段 2 到 6 暴露出的实际修改频率。

```text
src/server/application/ports/
  mod.rs
  transaction.rs    事务生命周期
  identity.rs
  collaboration.rs
  task.rs
  execution.rs
  attachment.rs
  effects.rs        outbox、审计、通知
```

`ServerTransaction` 当前 92 个方法（`ports.rs:386-794`）按能力拆成组合 trait：

```rust
pub(in crate::server) trait TaskTransaction {
    async fn task(&mut self, id: TaskId) -> Result<Task, ApplicationError>;
    async fn save_task(&mut self, task: Task) -> Result<(), ApplicationError>;
}

pub(in crate::server) trait RecordTaskOutcomeTransaction:
    TaskTransaction + MessageTransaction + RunTransaction + EffectSink
{
}
```

不拆成每张表一个 repository。事务对象保持统一，只是按能力分组接口。

**退出条件：** 如果泛型约束开始占据大量代码，或编译错误变得难以定位，退回单个 `ServerTransaction`。可读性优先于接口最小化。这一步允许不做完。

**验证：** 同阶段 2。

## 11. 阶段依赖关系

```text
阶段 1 盘点  ──→ 阶段 2 拆 http  ──→ 阶段 6 补用例
                        │                  ↑
                        ├──→ 阶段 3 拆 postgres ──→ 阶段 5 私有化字段
                        │                              │
                        └──→ 阶段 4 清理路径            └──→ 阶段 6.1 rehydrate
                                                              │
                                                              └──→ 阶段 7 拆 ports
```

阶段 3 与阶段 4 之间没有依赖，可以任意顺序。阶段 5 依赖阶段 3，因为 `rows.rs` 需要先集中。阶段 6 依赖阶段 1 的清单和阶段 5 的 `rehydrate`。

## 12. 每阶段的完成条件

所有阶段共同要求：

```bash
cargo fmt --all -- --check
cargo clippy --all-targets --all-features -- -D warnings
cargo test
```

阶段 2、3、4 不得改变任何可观测行为。判断方式：diff 中不应出现 SQL 语句内容、条件判断或状态转换的改动，只有代码位置和 `use` 的变化。

阶段 5、6 改变内部结构，必须有对应测试覆盖。

`tests/architecture_boundaries.rs` 在阶段 2 和阶段 6 需要同步修改。修改只能是扩大检查范围或更新路径，不得削弱断言。

## 13. 需要同步更新的设计文档

本方案完成后，[代码组织与依赖边界](./design/11-code-organization.md) 的以下内容与代码不再一致，必须更新：

- 第 3 节顶层目录：`adapters/http.rs`、`adapters/postgres.rs`、`application/ports.rs` 变为目录。
- 第 166 行"`builtin_runtime/` 是当前唯一需要拆分的模块"这一句不再成立。
- 第 9 节测试组织：`src/server/adapters/*.rs` 需要改为覆盖 `adapters/http/` 与 `adapters/postgres/` 子目录。

按 `CLAUDE.md` 的文档职责规则，行为与结构改变时先更新设计文档。本方案是执行计划，不是事实定义，完成后从 `docs/` 移除或归档，不留作第二套规范。
