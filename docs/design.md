# Sumi v1 设计索引

- 状态：Draft
- 日期：2026-07-28
- 目标读者：产品、设计、前端、Server、daemon、CLI 与 Driver 实现者
- 领域词汇：[GLOSSARY.md](../GLOSSARY.md)

`docs/design.md` 只提供导航。规范正文位于 `docs/design/`。文中的“必须”和“不得”是验收要求，“应该”是默认实现，“可以”表示允许实现。行为或技术边界改变时，更新对应主题文档。

## 产品基础与系统结构

- [§1 至 §6 产品定义、决策、技术栈、范围、系统结构与核心约束](./design/01-foundations.md)

## 协作领域

- [§7 至 §9 注册、Space、Member、权限、Channel、DM、Thread、Message、Attachment 与 Task](./design/02-collaboration.md)

## WebUI

- [§10 视觉语言、Pixel Avatar、页面结构、响应式与无障碍](./design/03-web-ui.md)

## Computer 与 Agent

- [§11 至 §12 Computer、daemon、Agent 配置、生命周期、Role 与 Memory](./design/04-computer-agent.md)
- [Agent 生命周期可靠性：状态事实、租约、结果回执、恢复、取消与调度](./design/04-agent-lifecycle-reliability.md)

## Driver 与 CLI

- [§13 至 §14 Driver 契约、Codex、Builtin 与 `sumi` 命令行](./design/05-driver-cli.md)

## Inbox 与本地凭据

- [§15 至 §16 Inbox 注意力、上下文变化、恢复与 Computer 本地凭据](./design/06-inbox-credentials.md)

## API 与数据

- [§17 至 §18 Browser/Computer API、SSE、PostgreSQL 数据模型与事务边界](./design/07-api-data.md)

## 安全、可靠性与运维

- [§19 至 §20 授权、Prompt injection、幂等、删除、诊断与运行状态](./design/08-security-operations.md)

## 交付与验收

- [§21 至 §23 开发顺序、端到端验收与实现纪律](./design/09-delivery-acceptance.md)

## 外部参考

- [Raft Blog 快照](../references/raft-blog/README.md)
- [Raft Computer 生命周期实现参考](../references/raft-computer-lifecycle.md)
