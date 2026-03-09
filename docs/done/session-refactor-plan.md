# Session Refactor Plan

## Goal

把 `session` 从“消息数组 + 零散分配逻辑”重构成系统内稳定的上下文状态单元。

这次重构的目标不是只优化存储细节，而是统一以下语义：

- `session` 是什么
- `source` 和 `session` 的关系是什么
- `compact` / `fork` / `resume` 的本质是什么
- Agent 每次真正读取的上下文是什么

该计划默认 `bus` 已经具备显式生命周期和全量可观察性。

## What I Learned

近期参考了 [tape.systems](https://tape.systems/) 的抽象，收获最大的不是某个具体 API，而是它对上下文系统的几个判断：

### 1. Context should be assembled, not copied wholesale

当前很多 Agent 系统会把“当前上下文”直接等同于“完整消息历史”。

`tape.systems` 给出的更好视角是：

- 原始事实应当追加保存
- 当前输入窗口应当按策略组装
- 派生物不能替代原始事实

这意味着我们不应该长期把 `session.Messages()` 直接当成 LLM 输入，也不应该把 `fork` 简化成整个 `[]Message` 的复制。

### 2. Compact is an anchor, not history deletion

现在常见实现会把 compact 理解成“把旧历史压成一条 system message，然后替换原历史”。

更优雅的理解是：

- 历史仍然存在
- compact 只是新增一个更便于恢复上下文的锚点（anchor）
- 后续默认 view 读取 anchor + recent window，而不是重写历史

### 3. Session is closer to a tape than to a request cache

`session` 更像一条按时间追加的事实带（tape），而不是某个请求生命周期的临时缓存。

因此：

- `session` 不是 transport 层
- `session` 不是 UI source 的别名
- `session` 不是 Agent worker 的内部变量
- `session` 应该是可持久化、可恢复、可派生 view 的状态单元

### 4. View is a first-class concept

Agent 真正应该消费的是一个“上下文视图（view）”，而不是底层全量原始记录。

这个 view 可能由以下内容组成：

- system prompt
- active summary / anchor
- recent user/assistant/tool exchange
- 当前回合的输入
- 某些必要的持久约束

这层目前在仓库里并不存在，但它应该被明确建出来。

## Current Problems In This Repo

当前 `session` 设计最大的问题不是代码多少，而是职责分散且语义交叉。

### 1. Session allocation is scattered

现在会话分配逻辑分散在多个地方：

- `Dispatcher` 决定某个 source 用哪个 session
- `Supervisor` 决定 sub-agent 如何复制 parent session
- `App` / daemon 会参与 resume 映射
- command 层会直接创建 session

结果是：

- `session.Manager` 反而不是会话协调中心
- `source -> current session` 不是系统里的第一等关系
- reset / resume / fork 的逻辑没有统一入口

### 2. Session mixes identity and storage, but not coordination

当前 `Session` 基本只是：

- `id`
- `[]msg.Message`
- `dirty`
- `Flush()`

这对存储足够，但对上下文系统不够。

缺少的恰恰是：

- source binding
- active session lookup
- fork semantics
- compact anchor semantics
- current view construction

### 3. Compact currently rewrites history too early

当前 compact 逻辑本质上是用 summary 替换旧消息片段。这个实现可以工作，但有两个代价：

- 原始历史被提前折叠
- summary 成为了历史替代物，而不是派生锚点

这会让后续做 replay、fork、compare、resume 都变得更困难。

### 4. Fork is implemented as message copying

当前 sub-agent 或会话分支更接近“复制完整消息数组”。

这不够优雅，因为：

- provenance 丢失
- fork 点不明确
- merge 将来很难做
- compact 之后再 fork 的语义也会含糊

## Target Model

目标不是一步到位复制 `tape.systems`，而是吸收它最有价值的抽象，形成适合 Mink 的模型。

### Core Concepts

#### Session

`Session` 表示一条上下文时间线。

建议它至少包含：

- `ID`
- `Entries`
- `Anchors`
- metadata（创建时间、更新时间、标签等）

#### Entry

`Entry` 表示追加到 session 上的一条事实记录。

建议类型包括：

- user input
- assistant output
- tool call
- tool result
- system note
- compact summary
- handoff / resume marker

注意：`Entry` 是事实，不是 view。

#### Anchor

`Anchor` 表示上下文恢复锚点，是一种专门为了后续 view 组装服务的派生结构。

建议场景：

- compact 产物
- long-running task checkpoint
- self-update handoff
- branch/fork checkpoint

#### View

`View` 是真正喂给 Agent 的上下文窗口。

它不是持久化真相，而是 runtime assembled context。

### Responsibility Boundaries

#### Session

负责：

- 维护时间线数据
- 追加 entry
- 维护 anchor
- 提供基础读取接口

不负责：

- source 路由
- Agent 调度
- UI 展示
- bus 消息投递

#### SessionManager

负责：

- create / get / restore
- `source -> current session`
- reset / fork / resume
- flush / delete / list

不负责：

- LLM context assembly
- compact 内容生成
- tool execution

#### Context Builder

建议新增一个显式组件，例如：

- `session/view.go`
- `session/context_builder.go`

职责：

- 根据 session entries / anchors 组装当前 prompt view
- 控制 recent window 大小
- 决定 compact 后默认读取哪些锚点
- 为 future features 预留策略层

## Proposed Refactor Phases

### Phase 0: Stabilize manager as the only coordination point

目标：先让 `SessionManager` 成为唯一会话协调中心。

建议动作：

- 把 `source -> current session` 绑定关系移入 `SessionManager`
- 提供统一接口：
  - `Current(source)`
  - `ResetSource(source)`
  - `RestoreSource(source, sessionID)`
  - `Fork(parent)`
- 删除 `Dispatcher` / `Supervisor` / command 层中的会话分配细节

完成标准：

- 代码里不再出现多个模块各自决定 session 分配策略
- `source` 绑定关系有唯一权威来源

### Phase 1: Introduce explicit session timeline types

目标：把 `[]msg.Message` 升级成更稳定的 timeline 结构。

建议动作：

- 在 `session/` 下引入：
  - `Entry`
  - `EntryKind`
  - `Anchor`
- 保留兼容层，让旧的 `msg.Message` 可以暂时映射成 entry
- 先不要大范围改 agent，只做数据模型准备

完成标准：

- session 存储不再只有裸 `[]msg.Message`
- compact / fork / resume 可以围绕 entry / anchor 建模

### Phase 2: Separate storage truth from runtime view

目标：引入显式 `View` 组装层。

建议动作：

- 新增 `ContextBuilder` 或 `ViewBuilder`
- Agent 不再直接调用 `session.Messages()` 作为完整输入
- 改为：
  - `session` 提供 timeline
  - `builder` 产出当前 prompt view

完成标准：

- Agent 构造输入的逻辑和 session 存储逻辑解耦
- 以后可单独调整 context window 策略而不碰 session store

### Phase 3: Rework compact into anchor creation

目标：compact 从“替换历史”改成“追加锚点”。

建议动作：

- compact 生成 `summary anchor`
- 默认 view 读取：
  - latest anchor
  - recent entries
  - current input
- 原始 entries 不被直接抹掉

完成标准：

- compact 不再通过重写历史完成
- summary 成为一等对象，而非覆盖性的 system message

### Phase 4: Rework fork semantics

目标：让 fork 成为时间线派生，而不是消息数组复制。

建议动作：

- fork 时记录：
  - parent session id
  - fork point
  - inherited anchor / entry reference
- 子 session 的初始 view 来自 parent 的某个可追溯状态
- 如果短期内不做 merge，也应先保留 provenance

完成标准：

- fork 具备可解释性
- 后续做 merge / compare / replay 不会被当前实现卡死

### Phase 5: Align commands and platform semantics

目标：让用户可见行为和内部模型一致。

建议动作：

- `!session new` 改成切换到新的 current session，而不是只是裸创建
- 后续可增加：
  - `!session current`
  - `!session switch <id>`
  - `!session fork`
- `session:new` bus event 应当表达“source 当前会话已切换”

完成标准：

- UI 命令语义与内部 source binding 一致
- daemon resume / self-update 恢复逻辑直接复用 `SessionManager`

## File-Level Suggestions

建议让后续 Codex 按以下方向落文件：

- `session/session.go`
  - 保留 `Session`，但逐步引入 entries / anchors
- `session/manager.go`
  - 成为唯一会话协调器
- `session/store.go`
  - 后续拆成 `Store`（timeline）与可选的 `BindingStore`（source mapping）
- `session/view.go` 或 `session/context_builder.go`
  - 新增 view 组装器
- `agent/agent.go`
  - 改为消费 `View`
- `agent/dispatcher.go`
  - 去掉会话分配逻辑，只保留 source worker / agent execution
- `agent/supervisor.go`
  - 去掉复制消息数组的 fork 逻辑，改为通过 manager + provenance 派生
- `command/builtin.go`
  - `!session` 系列命令统一走 manager

## Non-Goals For The First Pass

第一轮不要做太多，以免范围失控。

暂时不建议同时做：

- merge 算法
- entry 级别回放 UI
- 多存储后端抽象
- 太激进的磁盘格式迁移

第一轮最重要的是：

- 职责收口
- source binding 正名
- view 概念落地
- compact / fork 语义理顺

## Acceptance Criteria

当以下几点成立时，可以认为 `session` 重构进入了正确轨道：

- `SessionManager` 成为唯一会话协调入口
- `Dispatcher` / `Supervisor` 不再自己决定 session 分配规则
- Agent 输入来自显式 `View`，而不是直接整包历史
- compact 生成 anchor，而不是覆盖原历史
- fork 具备明确的 parent / fork point / provenance
- source 切换会话的语义在 CLI / TG / daemon 恢复路径里一致

## Recommended Order For Another Codex

建议另一个 Codex 按以下顺序实施：

1. 完成 `SessionManager` 的 source binding 收口
2. 把 `!session new` / resume / reset 全部改走 manager
3. 引入 `Entry` / `Anchor` 类型，但先保留兼容层
4. 新增 `ViewBuilder`
5. 让 `Agent` 消费 `View`
6. 重写 compact 为 anchor 生成
7. 最后再升级 fork/provenance

这个顺序的优点是：

- 每一步都能单独验证
- 不需要一次性推翻当前数据结构
- 可以持续保持系统可运行
