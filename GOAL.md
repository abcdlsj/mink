# Sumi 新版本交付目标

- 状态：Accepted
- 日期：2026-07-29

## 1. 目标

重建 Sumi，使 Agent 成为持续在线、可以被新消息打断或转向的协作者。

系统必须让 Agent 把执行步骤用于理解问题、完成工作和参与对话。系统能从认证、Run 和资源关系推导的信息，不得要求 Agent 重复建模或提交。

新实现以[设计索引](./docs/design.md)列出的主题文件为唯一产品与技术事实。当前代码只提供可验证的实现材料，不能证明产品行为、领域关系、API 或数据结构。

## 2. 实现原则

1. 新版本只实现当前设计，不迁移旧数据，也不保留旧 API、兼容读取、双写或 deprecated 字段。
2. Server 和 Computer 使用明确的运行时边界。两端只通过版本化协议交换命令、快照和回执。
3. 领域规则、流程编排和外部适配分别归属一个模块。模块公开接口必须表达业务行为。
4. 依赖必须从外部适配器单向指向 application，再指向 domain 或 core。禁止用共享状态、动态 JSON 或全局 service locator 绕过边界。
5. 每项事实、状态转换和写入行为只有一个所有者和一个修改入口。
6. 代码默认私有。只有跨边界契约、运行时入口和必要测试端口可以公开。
7. UI 继续以 Conversation 为主。Task 使用独立侧边栏入口，不改变产品的对话中心结构。
8. 新实现从空 PostgreSQL 和空 Computer 目录建立。共享环境建立基线后，只使用前向 migration。

## 3. 执行任务

- [x] 建立标识、协议、Server facade 和 Computer facade。
- [x] 重建 Server domain 和 application，并通过模块测试。
- [x] 实现 PostgreSQL、对象存储和 Server transport adapters，并通过模块测试。
- [x] 重建 Computer core 和 application，并通过模块测试。
- [x] 实现 SQLite、Driver、连接和本地 IPC adapters，并通过模块测试。
- [x] 接入 Agent CLI，并通过本地能力流程测试。
- [x] 重建 WebUI，并保持现有信息架构和视觉基线。
- [x] 一次性切换运行时入口，删除全部旧实现，并按[交付与验收](./docs/design/10-delivery-acceptance.md)完成整体验证。
  - [x] 将 Server、Computer 和 Agent CLI 入口切换到新运行时。
  - [x] 接通 Run result、Item disposition、lease renewal 和 Agent `run yield`，并验证本地 outbox 与 Driver completion 竞态。
  - [x] 补齐 Task 终态、Attachment、Memory 和剩余 Agent capability。
  - [x] 接通 same-Focus attach、different-Focus notice、Browser SSE、Agent 退役和 Computer 删除。
  - [x] 实现 Builtin Driver，删除旧实现、旧 schema 和冲突测试。
  - [x] 完成 Rust、Web、协议、数据库、并发、安全、故障和端到端验收。
    - [x] 实现 ambient 聚合：按 Agent 和 Thread 聚合 ambient activity，保存首尾 Message 序号、数量、available time 和 force time，并保证新 Message 不能无限推迟 force time。见 [Inbox 与本地凭据](./docs/design/06-inbox-credentials.md) 第 6 节。
    - [x] 实现重新排队 dead Item 的运维入口，并限定 Owner/Admin 可执行。见 [安全与运维](./docs/design/09-security-operations.md) 第 9 节。
    - [x] 补齐故障验收测试：Server 在 Run 期间重启、Computer 离线直到 lease 过期、workspace 丢失与 Provider locator 损坏。见 [交付与验收](./docs/design/10-delivery-acceptance.md) 第 6 节。
    - [x] 执行端到端验收：在运行中的 Server 上跑 Playwright 核心流程，并记录可复现命令与结果。见 [交付与验收](./docs/design/10-delivery-acceptance.md) 第 9 节。

Rust、Web、协议、数据库、并发和安全验收当前通过。lease 过期回收、`retry_count`递增与`dead`、`thread_activity`路由已经实现并有测试覆盖。

上列四个子任务是最后一个子任务的全部剩余工作。四者之间没有依赖，可以按任意顺序执行，但每项都必须独立提交并通过自己的验收。

ambient 聚合需要新的聚合行与 debounce 状态，因此它是唯一需要改动 schema 的一项：新增表或列必须同时更新[数据库设计](./docs/design/08-database.md)，并在基线阶段直接改`schema/postgres.sql`，不新增 migration。

任务按列表顺序执行。任务通过对应验收后，把`[ ]`改为`[x]`，并与该任务的实现一起提交。

前七个任务只运行本模块的定向测试。最后一个任务负责运行完整集成、故障和端到端验收。

新模块不能通过临时桥接、兼容 adapter 或双写接入旧实现。最终切换前，旧运行时继续独立工作；新模块通过自身端口和测试独立验证。

每个任务完成实现和定向测试后，必须按该任务涉及的主题设计文档 review 实现、测试和依赖边界。发现偏差时先修正并重新验证。Review 未通过时不得勾选任务或提交。

不得为字段转换、逐字段搬运或不承载业务、协议或安全约束的字段检查编写单元测试。调试期间临时添加的此类测试或脚手架必须在每次任务交接前删除。

## 4. Handoff 规则

1. 每个实现任务单独提交。下一任务的实现不能混入当前提交。
2. 当前任务提交后，如果仍有未完成任务，在`/tmp/handoff.md`写非空交接文档。
3. 交接文档必须记录已完成内容、验证结果、当前提交、剩余任务和已知风险。
4. 交接文档是一次性文件，只能存放在`/tmp`，不得写入或提交到仓库。
5. 通过当前 Superset workspace 创建新的 Codex session。创建命令必须使用`--attachment /tmp/handoff.md`上传交接文档，并使用`--json`校验结果。
6. `--prompt`只要求新 session 读取附件、验证仓库现状并继续任务。完整交接内容不能复制到 prompt。
7. 新 session 必须先读取项目约定、领域词汇和设计索引，再从下一个未完成任务继续。
8. 用户指定目标 Agent 时使用该 Agent。用户未指定时沿用当前 Agent 类型。
9. 新 session 创建成功后保留`/tmp/handoff.md`。下一次 handoff 生成时覆盖该文件。

## 5. 完成定义

- 设计索引中的主题文件是仓库内唯一现行规范。
- 每项产品事实只有一个主题文件和一个代码所有者。
- Server、Computer、协议、Driver 和 Agent CLI 的依赖符合[代码组织与依赖边界](./docs/design/11-code-organization.md)。
- 新数据库和新 Computer Home 可以从空状态建立并完成核心流程。
- 旧实现、旧入口、旧 schema 和无业务边界价值的抽象已经删除。
- Rust、Web、协议、数据库、并发、安全和故障验收全部通过。
- 最终提交包含可复现的验证记录。
