# Sumi 新版本设计索引

- 状态：Draft
- 日期：2026-07-29
- 目标：[GOAL.md](../GOAL.md)
- 领域词汇：[GLOSSARY.md](../GLOSSARY.md)

本文档集是新版本的产品和技术规范。当前代码、旧数据库和外部参考都不能覆盖本规范。

“必须”和“不得”表示验收要求。“应该”表示默认实现。“可以”表示允许实现。

## 唯一事实规则

- 本文件只提供索引，不定义具体行为。
- 每项事实只由下列一个主题文件负责。
- 主题文件需要使用其他主题的事实时，只链接原定义。
- `GLOSSARY.md`只定义词义，`GOAL.md`只定义交付目标，ADR只记录决策原因。
- 文件已从本索引移除时，该文件必须删除，不能作为“参考版本”留在仓库。

## 主题文档

1. [产品基础与系统结构](./design/01-foundations.md)：事实来源、范围、职责边界、技术结构和实现原则。
2. [协作、Task 与 Thread](./design/02-collaboration.md)：Message、Thread、Task、来源绑定、关联和 Result。
3. [WebUI](./design/03-web-ui.md)：视觉、页面、交互、状态和响应式行为。
4. [Computer 与 Agent](./design/04-computer-agent.md)：本地状态、Provider Session、workspace、Memory 和 Driver。
5. [Agent Run 可靠性](./design/04-agent-lifecycle-reliability.md)：Run、Focus、打断、yield、结果回执和恢复。
6. [Driver 与 Agent CLI](./design/05-driver-cli.md)：Driver 契约、Codex resume 和最小 Agent 操作面。
7. [Inbox 与凭据](./design/06-inbox-credentials.md)：注意力路由、active Run 交付和本地 Secret。
8. [API 与事件](./design/07-api.md)：Browser、Computer、WebSocket 和 SSE 契约。
9. [数据库设计](./design/08-database.md)：原子写模型、约束、事务和持续优化。
10. [安全与运维](./design/09-security-operations.md)：授权、可见范围、Session 污染和诊断。
11. [交付与验收](./design/10-delivery-acceptance.md)：重建顺序、测试矩阵和完成条件。
12. [代码组织与依赖边界](./design/11-code-organization.md)：Server、Computer、协议、目录、依赖方向和可见性。

## 架构决策

- [Task 是持续工作边界](./adr/0001-task-is-the-continuity-boundary.md)
- [Provider Session 是 Computer 本地缓存](./adr/0002-provider-session-is-local-cache.md)
- [新版本采用断代 schema](./adr/0003-clean-break-schema.md)
