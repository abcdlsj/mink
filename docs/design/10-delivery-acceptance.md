# 交付与验收

[返回设计索引](../design.md)

## 1. 重建原则

实现以[AGENTS.md](../../AGENTS.md)、[领域词汇](../../GLOSSARY.md)和本设计为依据。当前代码只能作为实现材料。

发现旧代码与新边界冲突时：

1. 定位旧假设属于哪个模块。
2. 删除或重写冲突实现。
3. 只保留与新接口一致的通用代码。
4. 增加覆盖新不变量的测试。

不得为了减少 diff 加入兼容层。更少改动不是验收目标。

## 2. 实施顺序

目录、模块和依赖调整按[代码组织与依赖边界](./11-code-organization.md)执行。

各阶段在未接入运行时前，通过自身端口和定向测试独立验证。阶段之间不得建立临时桥接、兼容 adapter 或双写。

运行时入口只在全部新模块完成后切换。切换任务必须同时删除旧实现，再执行完整集成、故障和端到端验收。

### 阶段一：领域层

- 建立 Root Message、Thread、Task、TaskThread、Run 和 Inbox Item 类型。
- 实现 Task 状态和 Run 状态转换。
- 实现领域错误和事务命令。
- 使用内存 repository 验证不变量。

### 阶段二：新 schema

- 从空库创建 PostgreSQL 最终 schema。
- 从空目录创建 Computer SQLite 最终 schema。
- 实现 Task 来源、Thread 关联和 active Run 唯一性约束。
- 不创建从旧版本进入新基线的 migration。
- 新基线进入共享环境后，按数据库设计使用前向 migration。

### 阶段三：Server

- 实现 conversation、task、attention 和 execution 模块。
- 实现 Root Message 创建 Task 的单一领域入口。
- 实现 same-Focus attach 和 different-Focus notice。
- 实现 outbox、command 和 result receipt。

### 阶段四：Computer

- 实现 Session registry 和 fingerprint。
- 实现 Run supervisor、steer、yield 和恢复。
- 实现 Codex resume 和 Builtin Session adapter。
- 实现 sandbox 和本地 Secret 边界。

### 阶段五：Agent capability

- 注入 Run token 中的 Focus 和可选 Task。
- 实现一步 Task 创建、默认 Focus 发送、Task 完成和 yield。
- 删除 bind、settle 和重复上下文参数。

### 阶段六：WebUI

- 实现 Conversation 中的 Task marker 和一步创建。
- 实现 Task 列表、Task 详情和 Linked Threads。
- 实现 Agent current Focus、Run 和 Session continuity。
- 完成桌面、移动端、空态、错误和无障碍。

## 3. 单元验收

单元测试只验证业务流程、不变量和失败分支。测试对象包括状态转换、用例编排、幂等、重试、恢复、权限判断和 Session 生命周期。

以下实现不单独编写单元测试：

- getter、setter 和构造器。
- 字符串格式化、大小写转换和字段拼接。
- struct、DTO、row 和 frame 之间的逐字段搬运。
- derive、serde 和依赖库已经保证的行为。
- 没有分支、约束或安全含义的薄封装。

数据转换涉及协议必填字段、授权范围、安全过滤或稳定错误码时，使用 adapter contract test 或集成测试验证边界结果。测试不能逐字段复述实现。

- reply 不能创建 Task。
- Root Message 创建 Task 时 Source Thread 和 Task 同一行成功或失败。
- 并发创建只产生一个 Task。
- Source Thread 不可删除或更换。
- Thread 同时最多关联一个未结束 Task。
- 不兼容成员集合不能 link。
- Run 有 Task 时，Focus 必须属于 Task。
- 普通 Run 创建 Task 后，Run、Items 和 Source Thread 必须同事务绑定。
- 一个 Agent 同时最多一个 active Run。
- Done Task 必须有 Result Message。
- Closed Task 必须有 close reason。
- In Progress 可以直接进入 Done，也可以经过 In Review。
- In Review 可以由 assignee 之外、能读取 Task 的 Human 或 Agent 确认 Done 或退回 In Progress。
- Review 不检查 Permission，也不保存 reviewer 字段。
- Run 终态不自动完成 Task。
- assignee 的首个 Task Run 进入`working`时把`todo`推进为`in_progress`；其他 Agent 的 Run 和重复上报都不改变状态。
- Run 失败时释放未处理 Items 并增加 retry count，且该 Agent 随后可以创建新 Run。
- 释放保留 Agent 已上报的 disposition，重复上报不重复计数。
- retry count 超过上限的 Item 进入`dead`，并产生不含正文的 system Item。
- Agent 显式 release 的 Item 不增加 retry count。
- 订阅该 Thread 的 Agent 收到`thread_activity`，未订阅的收到`channel_activity`。
- `submit_review`、`done`和`close`只有一个事务入口，Browser 与 Agent CLI 共用它。
## 4. 集成验收

- Message 发送事务同时创建 Root Thread 和 Inbox Items。
- Run 已进入终态后到达的 same-Focus hard Item 保持 pending，不附加到该 Run。
- different-Focus Item 保持 pending，notice 不泄露正文。
- 重复 command、started、delivery 和 result 只应用一次。
- 上一 daemon session 的残留帧不能修改状态。
- Task 完成事务失败时 Message 和 Result 都不产生部分写入。
- Session close 失败不回滚已完成 Task。
- Task 表不重复保存 Root Message、Result 正文或 Source link。
- different-Focus notice 和 Session continuity 不在 Server 建立事实表。
- 发送 Message 的事务同时产生`inbox.changed`；reply 另外产生`thread.updated`。
- SSE 不向读不到该 Channel 的调用方投递事件，也不把一个 Member 的`inbox.changed`投递给他人。
- 空 PostgreSQL 可以直接建立完整基线。
- 空 Computer 目录可以直接建立完整 SQLite schema。
- 数据库约束拒绝非法 Task 终态和并行 active Run。
- 已应用 migration 不允许修改。
- backfill 中断后可以从稳定游标继续。
- 创建 Channel 或 Agent 时，目标资源和 Action Message 同时成功或失败。
- Agent 缺少对应 Permission 时返回`permission_denied`，且不创建目标资源或 Action Message。
- 普通 Message API 不能创建 Action Message。
- Computer 删除事务锁定 assigned Agents，并在仍有 assignment 时拒绝删除。
- Agent 退役清除 assignment 后，Computer 才能删除。
- 首次 provision Agent Home 时创建包含 Role 和默认结构的`MEMORY.md`，重复 provision 保留 Agent 已写入的内容。

## 5. Driver 验收

- 同一 Task 的第二个 Run resume 同一 Provider Session。
- 不同 Task 不会复用 Session。
- 同一 Run 只使用一个 Session 和一个 Focus。
- Session resume 失败会创建新 generation 并恢复 Task 事实。
- token 量、Run 数量和经过时间不会单独换新 Session。
- Role、Driver、workspace 或 audience 不兼容变化会换新 Session。
- Codex steer unsupported 时 Item 保持 pending。
- Driver output 不会自动创建 Message 或 Result。
- Driver contract 要求 Agent 在每个 Run 开始时读取`MEMORY.md`，并在相关对外动作前写入本 Run 新增的持久知识。
- Driver contract 要求 Agent 在同一个 tool-call batch 中发出相互独立的 Sumi CLI 调用，并为数据依赖、写入冲突和可见顺序保留屏障。
- Agent CLI 的参数、权限、冲突、IPC 和 Server 错误都返回统一 JSON error envelope。
- JSON error stdout 只包含一个文档，且不泄露正文或 Secret。

## 6. 故障验收

- Server 在 Run 期间重启。
- WebSocket 在 start ACK 前后断开。
- daemon 在 Driver 运行和 result 上报期间重启。
- receipt 丢失并重复上报。
- Computer 离线任意时长后重连。
- Driver 进程在 daemon 存活期间消失。
- Task 完成后 Computer 离线，Session 稍后关闭。
- workspace 丢失和 Provider locator 损坏。
- active Run 收到 Human 明确转向并 yield。

每个场景必须证明 Message、Task、Inbox 和 Run 事实一致。

需要真实进程与数据库的场景在`tests/failure_recovery.rs`验证：Server 重启、Computer 离线后重连、workspace 丢失与 Provider locator 损坏。daemon 重启、Driver 进程消失、重复上报和 yield 是 Computer 内部的状态转换，在`src/computer/application/tests.rs`验证，见 [代码组织与依赖边界](11-code-organization.md) 的测试组织。

workspace 场景需要运行中的 daemon 把 Agent 带到`active`，因此依赖本机`codex`可执行文件。缺少该文件时该场景跳过并说明原因，不得记为通过。

workspace 丢失后 Computer 当前无法重新完成握手：重连会重放 provision command，而 Driver 校验要求 workspace 目录存在，因此该 Computer 保持 offline 并持续重试。此时 Server 端事实仍然一致，Item 留在队列中等待，损坏的 Provider Session 不被 resume。自动重建 workspace 尚未实现。

## 7. UI 验收

- Root Message 的一步创建不显示 bind 或 Source Thread 字段。
- reply 上的 Task 动作解释只能从 Root Message 创建。
- Task 详情显示 Source、Linked Threads、assignee、status、Result 和 Runs。
- Agent 详情区分 Task、Focus、Run 状态和 Session continuity。
- different-Focus notice 不会显示成当前 Task 的新 Message。
- UI 不展示 Provider transcript、隐藏推理或 Secret。
- hover 和键盘 focus 回答引用时显示来源，并高亮当前视图中的来源 Message。
- 一个回答片段的多条来源和移动端底部浮层都可操作。
- Channel 和 Agent 创建 Action Message 使用专用 UI，不显示原始 JSON 或命令参数。
- Channel 成员加入或离开会在主时间线显示 `system_notice`，并通过 `message.created` 产生 Channel 未读状态。
- 390px 和 1440px 视口能完成核心流程。
- 所有状态使用文字和图形，不只使用颜色。

## 8. 代码质量验收

- 领域模块不引用 HTTP DTO、WebSocket frame 或 Driver SDK。
- Server 领域模块不能被 Computer、Driver 或 Agent CLI 引用。
- Computer core 不能引用 Server 领域模块、wire frame 或 Driver SDK。
- Server 与 Computer 只能通过`protocol`中的版本化 wire 类型通信。
- `protocol`不能包含领域行为、SQL row 或运行时实现。
- Adapter 只能依赖 application 公开用例，不能复制领域判断。
- 架构依赖检查拒绝设计文件列出的禁止依赖。
- 每个模块有单一公开职责和领域命名接口。
- Task 创建只有一个 transaction service。
- Session lifecycle 只存在于 Computer session 模块。
- 注释只说明不变量、并发和安全原因。
- 日志测试证明正文和 Secret 被排除。
- 仓库搜索不到旧 schema 兼容、双写、deprecated 入口和 Agent bind 步骤。

## 9. 提交前验证

文档任务运行：

```text
git diff --check
Markdown相对链接检查
旧模型冲突词定向搜索
```

Rust 模块任务运行该模块的定向测试，并执行：

```text
cargo fmt --all -- --check
cargo clippy --all-targets --all-features -- -D warnings
```

Web 模块任务运行类型检查、lint 和相关流程测试。

最终切换任务运行：

```text
cargo test --all-targets --all-features
cargo fmt --all -- --check
cargo clippy --all-targets --all-features -- -D warnings
Web 类型检查、lint、流程测试和 Playwright 核心流程
```

Playwright 需要一个提供已构建 WebUI 的运行中 Server。`playwright.config.ts`的 baseURL 默认`http://127.0.0.1:3000`，用`PLAYWRIGHT_BASE_URL`指向其他端口：

```text
pnpm --dir web build
createdb sumi_e2e
sumi server --config <指向该库与 web/dist 的配置>
PLAYWRIGHT_BASE_URL=http://127.0.0.1:<port> pnpm --dir web test:e2e
```

两个 spec 各自注册 Human 并创建 Space，因此要求空数据库且没有已配对 Computer：`collaboration-smoke.spec.ts`断言「No Computers paired」。不要用`mise run dev-seed`准备该环境，它会预先创建 Space 与 Computer 并使这些断言失败。

三个 viewport project 都必须通过。Message 动作面板在窄断点以上依赖 hover 显示，因此测试必须先 hover 该 Message 行再点击面板按钮，见 [WebUI](03-web-ui.md) 的 Message 动作面板。

最终整体验收通过后，才能声明新版本完成。
