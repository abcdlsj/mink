# Sumi 结构优化开发目标

## 目标

在不改变 `docs/design.md` 规定的产品模型和验收行为的前提下，消除 Message、Computer 协议和 Web API 的重复事实来源，减少数据库往返，并按已有职责边界拆分过大的运行时模块。

## 执行规则

1. 每次只实现一个任务，开始前重新检查工作区并阅读该任务涉及的设计文档。
2. 保留用户和其他 Agent 的未提交修改；当前 `web/package-lock.json` 不属于本目标，不得提交。
3. 每个任务完成后运行定向测试、`cargo fmt --all -- --check` 和 `cargo clippy --all-targets --all-features -- -D warnings`；涉及 Web 时还要运行 Web lint 和定向测试。
4. 每个任务单独提交，不把下一任务的实现混入当前提交。
5. 提交后在 `tmp/handoff/` 写非空交接文档并提交，通过当前 Superset workspace 创建新的 Codex session；新 session 从下一个未完成任务继续。
6. 行为、协议或 wire schema 改变时先核对设计；现有设计未覆盖时先更新对应文档。
7. 结构优化以减少状态、入口、转换、依赖和公开 API 为准；拆分文件或减少行数不能单独作为完成依据，也不设置代码行数指标。

## 任务

### 1. Agent 结构化 mention

- 状态：已完成
- CLI 从 Message 正文解析 `@handle`，通过本地协议提交结构化 handle。
- Server 只解析当前 Channel Member，写入 `message_mentions`，并创建 hard Inbox。
- Thread mention、ambient 排除、幂等响应和未知 handle 行为必须有测试。

验收：mention 解析单测、Computer/Agent 安全边界集成测试、Rust fmt 和 Clippy 通过。

### 2. 统一 Message 发布事务

- 状态：已完成
- 提取事务级 Message 发布应用服务，统一主时间线和 Thread 的 Message、mention、Attachment、Inbox、subscription 与 outbox 写入。
- Human handler 和 Agent gateway 只保留认证、目标解析、上下文 freshness、Inbox handle 和响应转换。
- 不增加按表划分的 repository trait，不改变现有 HTTP、CLI 和数据库 schema。

验收：协作、Thread、Agent DM/Channel、幂等和事务回滚测试通过；三条入口不再各自执行 Message 核心 INSERT 流程。

### 3. Browser API 类型生成

- 状态：已完成
- 使用 Rust API schema 生成 OpenAPI，再生成 `web/src/api/types.ts`。
- `web/src/api/client.ts` 只保留传输 helper 和领域命名函数。
- 增加可重复的生成命令和生成结果一致性检查。

验收：Rust schema、生成文件、TypeScript build 和 lint 通过；手工 wire interface 不再作为并行事实来源。

### 4. Computer 共享强类型协议

- 状态：待开始
- 将 Server 与 daemon 的镜像 Frame 移入共享 protocol 模块。
- 统一 `run_attempt` 等字段类型。
- 将已稳定的 command payload/result 从 `kind + serde_json::Value` 改为 tagged enum 和 struct。

验收：重连、command replay、run started/result、lifecycle 和崩溃恢复测试通过；两端不再重复声明 Frame。

### 5. Message hydration 批量查询

- 状态：待开始
- 使用 Message ID 集合批量读取 Attachment、mention 和 Task summary。
- Browser Channel、Thread read 和 Agent read 复用批量 hydration。

验收：返回结构和排序不变；列表 hydration 的查询次数不随 Message 数量线性增长；相关读取测试通过。

### 6. 代码简化与架构整理

- 状态：待开始
- 只保留唯一的最新版本实现，删除历史兼容、旧入口和旧逻辑。
- 合并重复实现，删除废弃配置、无效分支、未使用依赖和不再需要的代码。
- 删除不承载业务边界或不变量的单实现 trait、透传层、wrapper、factory、一次性 helper 和预留扩展点。
- 删除可从其他事实推导的重复状态及其同步代码；合并语义相同的状态、错误分支和数据转换。
- 每项核心状态、协议和业务规则只保留一个事实来源和一个修改入口。
- 按变化原因划分模块，分离流程编排与具体执行；数据转换只发生在 HTTP、WebSocket、数据库和领域对象的边界。
- 保持依赖单向和模块 API 最小化，默认使用私有可见性；共享代码必须表达共同业务规则，而非只消除语法重复。
- 前端删除可由 URL、查询缓存或现有状态推导的副本；删除失效或重复的 CSS 规则及覆盖链。
- 在不影响主要功能和既有验收行为的前提下统一命名、结构和格式。

验收：不存在历史兼容、旧入口、旧逻辑、重复事实来源、重复状态或无业务边界价值的抽象；核心状态、协议和业务规则均有唯一所有者和修改入口；模块职责、依赖方向、公开 API 和数据流可明确说明；主要功能及既有测试保持通过；Rust fmt 和 Clippy 通过，涉及 Web 时 Web build、lint 和定向测试通过。

### 7. Computer 与前端职责拆分

- 状态：待开始
- 按 pairing/credentials、connection、command executor、attention scheduler、lease/recovery 拆分 `src/computer.rs`。
- 从 `ChannelPage.tsx` 提取 Message composer 状态与 Timeline、ThreadPane、Composer 组件。
- CSS 只在组件边界稳定后按 feature 拆分。

验收：运行时职责和依赖方向符合设计文档；Rust 全量测试、Web 单元测试、build 和 lint 通过。

## 完成条件

七个任务均完成并分别提交；最终运行 `mise run lint` 和 `mise run test`。需要真实 provider 的手工 smoke test可以继续保持 ignored，但必须记录原因。
