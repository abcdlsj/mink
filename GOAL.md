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

## 3. 实施阶段

1. 建立标识、协议、Server facade 和 Computer facade。
2. 重建 Server domain、application 和 PostgreSQL adapter。
3. 重建 Computer core、application、SQLite adapter 和 Driver adapter。
4. 接入 Agent CLI、Computer 连接和 Server transport。
5. 切换 WebUI 到新 API，并保持现有信息架构和视觉基线。
6. 删除旧模块、旧协议、旧 schema 和临时桥接。
7. 按[交付与验收](./docs/design/10-delivery-acceptance.md)完成测试和故障验证。

阶段顺序只表示依赖关系。每个实现任务必须保持仓库可验证，并删除该任务引入的临时兼容路径。

## 4. Handoff 规则

1. 每个实现任务单独提交。下一任务的实现不能混入当前提交。
2. 每个任务提交后，在`tmp/handoff/`写非空交接文档并提交。
3. 交接文档必须记录已完成内容、验证结果、当前提交、剩余任务和已知风险。
4. 通过当前 Superset workspace 创建新的 Codex session。创建命令必须使用`--attachment`上传交接文档，并使用`--json`校验结果。
5. `--prompt`只要求新 session 读取附件、验证仓库现状并继续任务。完整交接内容不能复制到 prompt。
6. 新 session 必须先读取项目约定、领域词汇和设计索引，再从下一个未完成任务继续。
7. 用户指定目标 Agent 时使用该 Agent。用户未指定时沿用当前 Agent 类型。

## 5. 完成定义

- 设计索引中的主题文件是仓库内唯一现行规范。
- 每项产品事实只有一个主题文件和一个代码所有者。
- Server、Computer、协议、Driver 和 Agent CLI 的依赖符合[代码组织与依赖边界](./docs/design/11-code-organization.md)。
- 新数据库和新 Computer Home 可以从空状态建立并完成核心流程。
- 旧实现、旧入口、旧 schema 和无业务边界价值的抽象已经删除。
- Rust、Web、协议、数据库、并发、安全和故障验收全部通过。
- 最终提交包含可复现的验证记录和下一阶段所需的 handoff 文档。
