# Sumi v1 剩余目标

## 目标

完成 Sumi v1 的安全、可靠性和最终验收，使当前实现可以在 macOS 与 Linux 上安装、运行并完整测试，且真实通过 `docs/design.md` 第 22 节全部端到端场景。

Phase 0–5 已作为当前代码基线完成，本文件不再重复记录已实现功能。后续开发只处理下方未完成项；开始前必须以当前代码和实际测试核实现状，不得根据旧进度重新实现已有能力。

## 开始和续跑规则

1. 完整阅读 `AGENTS.md`、本文件、`GLOSSARY.md` 和 `docs/design.md`；具体开发前重读对应设计章节。
2. 当前工作区文件是唯一实现基线，禁止读取、恢复或推断 Git 历史中的旧实现。
3. 从下方最早的未完成项开始，先审计当前实现与规范的差距，再完成一条可运行的纵向路径。
4. 行为、协议、数据模型或领域名词需要改变时，先更新 `docs/design.md` 或 `GLOSSARY.md`。
5. 普通编译错误、测试失败和可自行验证的不确定性不是阻塞；持续执行“检查 -> 实现 -> 定向测试 -> 修复 -> 更新进度”。
6. 只有真实通过对应验收后才能勾选；勾选时在“验证记录”追加简短、可复现的命令或证据。

## 剩余工作

### 1. Secret 与安全闭环

- [ ] 完成 BYOK Secret Envelope 纵向路径：Browser WebCrypto 封装、Server 仅保存密文、目标 Computer 解密和受限本地保存、可用状态回报、Computer revoke 后失效。
- [ ] 完成 Secret、Message、Attachment、Memory 的日志 redaction 审计和测试，确保错误、审计、command result、outbox、幂等记录及测试失败输出不泄露正文或凭证。
- [ ] 补齐治理与敏感操作的 audit，覆盖操作者、目标、结果和不含敏感正文的 metadata。
- [ ] 审计并补齐注册、登录及高风险写操作的 rate limit；验证限流键、恢复时间和绕过边界。
- [ ] 完成 `docs/design.md` 第 19.2 节 prompt injection 边界测试，验证不可信 Message/Attachment 无法改变身份、权限或绕过 `sumi agent` CLI。
- [ ] 完成删除与保留策略：Space 软删除、Attachment 下载撤销与延迟清理、Agent run log 保留、Member/Agent 历史身份保留。

### 2. 故障与数据不变量

- [ ] 补齐并发故障测试：同 Agent 单 active run、Computer 并发上限、并发 Thread/Message sequence、Inbox claim/lease 竞争。
- [ ] 补齐崩溃与断线测试：Driver 发送前崩溃、send-and-handle 后 daemon 崩溃、Server/Computer 重连、lease 到期恢复和 dead 通知。
- [ ] 补齐重复交付测试：重复 Computer command、重复 Message/Attachment 写入、幂等 key 同 payload 重放及不同 payload 冲突。
- [ ] 补齐权限越界测试：跨 Space、其他 Computer、其他 Agent Home、private Channel、Human-only Approval 与 Secret 操作。
- [ ] 使用真实 PostgreSQL integration tests 验证 schema、复合外键、唯一约束、事务回滚和 transactional outbox 关键不变量。

### 3. 可观测性、性能与平台

- [ ] 补齐 `docs/design.md` 第 20 节要求的 Server/daemon 结构化日志与核心 metrics，并验证敏感正文不会进入观测数据。
- [ ] 对 Message/SSE、hard Inbox 通知、业务 API、Computer 离线判定和 crash 恢复执行可复现性能测试；达到目标或在设计文档记录证据与已接受偏差。
- [ ] 在 Linux 上完成构建、测试和 sandbox 验收，确认 bubblewrap、目录权限、Unix socket 与 macOS 行为一致。

### 4. 最终产品验收

- [ ] 按 `docs/design.md` 第 22.1–22.9 节逐项运行真实端到端验收，不能用 handler 存在、mock 或单元测试替代完整闭环。
- [ ] 使用 Playwright 验收 1440x900、1024x768、390x844 三个 viewport，覆盖 Channel、Attachment、Thread、长内容、响应式、键盘、reduced motion 和 screen reader labels。
- [ ] 核对 WebUI 的 Neo-Brutalism、pixel art avatar 和 Human/Agent 平等布局，不存在 Raft 复刻或伪装可用的入口。
- [ ] 执行统一 format、clippy `-D warnings`、typecheck、lint、Rust unit/integration、真实 PostgreSQL、CLI、Web component、production build 和 E2E 测试。
- [ ] 清除占位实现、无主 TODO、过期入口和文档偏差；最终 `git diff --check` 通过。

## 完成定义

只有同时满足以下条件，Sumi v1 才算完成：

- 上述所有项目均已勾选并附有可复现证据。
- `docs/design.md` 第 22 节所有场景真实通过。
- macOS 与 Linux 的最终统一门禁成功。
- 文档、数据库 schema、Server、daemon、CLI 和 WebUI 行为一致。
- 日志、错误和持久化数据中不存在 Secret、Message、Attachment 或 Memory 正文泄露。

## 测试节奏

- 日常循环先运行最小相关测试；每个纵向项完成后运行对应 Rust、PostgreSQL、CLI 或 Web 测试。
- PostgreSQL repository/integration tests 必须连接本机临时 database 或 schema，不能用内存数据库替代真实 SQL。
- Playwright 只用于明确浏览器回归和最终验收；调试时只跑相关 spec 与单一 viewport，最终再运行完整矩阵。

## 明确不做

遵守 `docs/design.md` 第 4.2 节。尤其不实现 Work/Task、其他具体 Driver、微服务、工作流/DAG、向量搜索、Agent marketplace、Windows 支持或 Agent 热迁移。

## 验证记录

格式：`YYYY-MM-DD | 项目 | 命令或证据 | 结果`。
