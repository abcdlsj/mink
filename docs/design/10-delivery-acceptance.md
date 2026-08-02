# 交付与验收

[返回设计索引](../design.md)

## 1. 重建原则

实现以[GOAL](../../GOAL.md)、[领域词汇](../../GLOSSARY.md)和本设计为依据。当前代码只能作为实现材料。

发现旧代码与新边界冲突时：

1. 定位旧假设属于哪个模块。
2. 删除或重写冲突实现。
3. 只保留与新接口一致的通用代码。
4. 增加覆盖新不变量的测试。

不得为了减少diff加入兼容层。更少改动不是验收目标。

## 2. 实施顺序

目录、模块和依赖调整按[代码组织与依赖边界](./11-code-organization.md)执行。

各阶段在未接入运行时前，通过自身端口和定向测试独立验证。阶段之间不得建立临时桥接、兼容 adapter 或双写。

运行时入口只在全部新模块完成后切换。切换任务必须同时删除旧实现，再执行完整集成、故障和端到端验收。

### 阶段一：领域层

- 建立Root Message、Thread、Task、TaskThread、Run和Inbox Item类型。
- 实现Task状态和Run状态转换。
- 实现领域错误和事务命令。
- 使用内存repository验证不变量。

### 阶段二：新schema

- 从空库创建PostgreSQL最终schema。
- 从空目录创建Computer SQLite最终schema。
- 实现Task来源、Thread关联、active Run和fencing约束。
- 不创建从旧版本进入新基线的 migration。
- 新基线进入共享环境后，按数据库设计使用前向 migration。

### 阶段三：Server

- 实现conversation、task、attention和execution模块。
- 实现Root Message创建Task的单一领域入口。
- 实现same-Focus attach和different-Focus notice。
- 实现outbox、command和result receipt。

### 阶段四：Computer

- 实现Session registry和fingerprint。
- 实现Run supervisor、steer、yield、finalizing和恢复。
- 实现Codex resume和Builtin Session adapter。
- 实现sandbox和本地Secret边界。

### 阶段五：Agent capability

- 注入Run token中的Focus和可选Task。
- 实现一步Task创建、默认Focus发送、Task完成和yield。
- 删除bind、settle和重复上下文参数。

### 阶段六：WebUI

- 实现Conversation中的Task marker和一步创建。
- 实现Task列表、Task详情和Linked Threads。
- 实现Agent current Focus、Run和Session continuity。
- 完成桌面、移动端、空态、错误和无障碍。

## 3. 单元验收

单元测试只验证业务流程、不变量和失败分支。测试对象包括状态转换、用例编排、幂等、租约、重试、恢复、权限判断和 Session 生命周期。

以下实现不单独编写单元测试：

- getter、setter 和构造器。
- 字符串格式化、大小写转换和字段拼接。
- struct、DTO、row 和 frame 之间的逐字段搬运。
- derive、serde 和依赖库已经保证的行为。
- 没有分支、约束或安全含义的薄封装。

数据转换涉及协议必填字段、授权范围、安全过滤或稳定错误码时，使用 adapter contract test 或集成测试验证边界结果。测试不能逐字段复述实现。

- reply不能创建Task。
- Root Message创建Task时Source Thread和Task同一行成功或失败。
- 并发创建只产生一个Task。
- Source Thread不可删除或更换。
- Thread同时最多关联一个未结束Task。
- 不兼容成员集合不能link。
- Run有Task时，Focus必须属于Task。
- 普通Run创建Task后，Run、Items和Source Thread必须同事务绑定。
- 一个Agent同时最多一个active Run。
- Done Task必须有Result Message。
- Closed Task必须有close reason。
- In Progress可以直接进入Done，也可以经过In Review。
- In Review 可以由 assignee 之外、能读取 Task 的 Human 或 Agent 确认 Done 或退回 In Progress。
- Review 不检查 Permission，也不保存 reviewer 字段。
- Run终态不自动完成Task。
- assignee 的首个 Task Run 进入`running`时把`todo`推进为`in_progress`；其他 Agent 的 Run 和重复上报都不改变状态。
- lease 过期回收释放 Items 并增加 retry count，把 Run 置为`failed`，且该 Agent 随后可以创建新 Run。
- 回收保留 Agent 已上报的 disposition，重复扫描不重复计数。
- retry count 超过上限的 Item 进入`dead`，并产生不含正文的 system Item。
- Agent 显式 release 的 Item 不增加 retry count。
- 订阅该 Thread 的 Agent 收到`thread_activity`，未订阅的收到`channel_activity`。
- `submit_review`、`done`和`close`只有一个事务入口，Browser 与 Agent CLI 共用它。
- Context Citation 只接受当前 Run 的 Focus 快照或 claimed Item 来源，并拒绝空原文、不唯一原文和越界字符范围。

## 4. 集成验收

- Message发送事务同时创建Root Thread和Inbox Items。
- same-Focus hard Item与finalizing并发时不会丢失或重复处理。
- different-Focus Item保持pending，notice不泄露正文。
- 重复command、started、delivery和result只应用一次。
- lease过期后旧fencing token不能修改状态。
- Task完成事务失败时Message和Result都不产生部分写入。
- Session close失败不回滚已完成Task。
- Task表不重复保存Root Message、Result正文或Source link。
- different-Focus notice和Session continuity不在Server建立事实表。
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
- Agent Message 与 Context Citations 同时成功或失败；重放相同 idempotency key 不重复创建引用。
- Message 投影不会向失去来源 Channel 权限的调用方返回 Context Citation 来源正文。

## 5. Driver 验收

- 同一Task的第二个Run resume同一Provider Session。
- 不同Task不会复用Session。
- 同一Run只使用一个Session和一个Focus。
- Session resume失败会创建新generation并恢复Task事实。
- token量、Run数量和经过时间不会单独换新Session。
- Role、Driver、workspace或audience不兼容变化会换新Session。
- Codex steer unsupported时Item保持pending。
- Driver output不会自动创建Message或Result。
- Agent CLI 的参数、权限、冲突、IPC 和 Server 错误都返回统一 JSON error envelope。
- JSON error stdout 只包含一个文档，且不泄露正文或 Secret。

## 6. 故障验收

- Server在Run期间重启。
- WebSocket在start ACK前后断开。
- daemon在Driver运行和result上报期间重启。
- receipt丢失并重复上报。
- Computer离线直到lease过期。
- Task完成后Computer离线，Session稍后关闭。
- workspace丢失和Provider locator损坏。
- active Run收到Human明确转向并yield。

每个场景必须证明Message、Task、Inbox和Run事实一致。

需要真实进程与数据库的场景在`tests/failure_recovery.rs`验证：Server 重启、Computer 离线直到 lease 过期、workspace 丢失与 Provider locator 损坏。daemon 重启、重复上报和 yield 是 Computer 内部的状态转换，在`src/computer/application/tests.rs`验证，见 [代码组织与依赖边界](11-code-organization.md) 的测试组织。

workspace 场景需要运行中的 daemon 把 Agent 带到`active`，因此依赖本机`codex`可执行文件。缺少该文件时该场景跳过并说明原因，不得记为通过。

workspace 丢失后 Computer 当前无法重新完成握手：重连会重放 provision command，而 Driver 校验要求 workspace 目录存在，因此该 Computer 保持 offline 并持续重试。此时 Server 端事实仍然一致，Item 留在队列中等待，损坏的 Provider Session 不被 resume。自动重建 workspace 尚未实现。

## 7. UI 验收

- Root Message的一步创建不显示bind或Source Thread字段。
- reply上的Task动作解释只能从Root Message创建。
- Task详情显示Source、Linked Threads、assignee、status、Result和Runs。
- Agent详情区分Task、Focus、Run状态和Session continuity。
- different-Focus notice不会显示成当前Task的新Message。
- UI不展示Provider transcript、隐藏推理或Secret。
- hover和键盘focus回答引用时显示来源，并高亮当前视图中的来源Message。
- 一个回答片段的多条来源和移动端底部浮层都可操作。
- Channel 和 Agent 创建 Action Message 使用专用 UI，不显示原始 JSON 或命令参数。
- 390px和1440px视口能完成核心流程。
- 所有状态使用文字和图形，不只使用颜色。

## 8. 代码质量验收

- 领域模块不引用HTTP DTO、WebSocket frame或Driver SDK。
- Server领域模块不能被Computer、Driver或Agent CLI引用。
- Computer core不能引用Server领域模块、wire frame或Driver SDK。
- Server与Computer只能通过`protocol`中的版本化wire类型通信。
- `protocol`不能包含领域行为、SQL row或运行时实现。
- Adapter只能依赖application公开用例，不能复制领域判断。
- 架构依赖检查拒绝设计文件列出的禁止依赖。
- 每个模块有单一公开职责和领域命名接口。
- Task创建只有一个transaction service。
- Session lifecycle只存在于Computer session模块。
- 注释只说明不变量、并发和安全原因。
- 日志测试证明正文和Secret被排除。
- 仓库搜索不到旧schema兼容、双写、deprecated入口和Agent bind步骤。

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
