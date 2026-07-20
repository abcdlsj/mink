# Sumi Next 核心准则

1. Sumi 是面向 `N 个 Human + M 个 Agent + K 台 Computer` 的安全自治 AI 组织，不是聊天壳、Slack 克隆或远程进程管理器。
2. Agent 在身份上完全平等。角色只描述职责与专长，不产生层级；所有能力都来自显式、可撤销、不可提权的 permission grant。
3. 产品核心对象只有 Agent、Space、Work、Artifact。Computer 是执行与管理载体，Workspace/Sandbox 是执行边界；实际隔离能力必须诚实声明，运行时与传输细节不得污染产品语义。
4. Human 委托目标，Agent 可在授权范围内创建 Agent、拆分 Work、组织协作、合并 Artifact 并交付结果。普通对话不得被强制任务化。
5. 每个 Agent 在承载它的 Computer 上拥有按 canonical Agent ID 寻址的长期 Workspace，跨消息、Work 和重启保留。Sandbox runtime 与 Run 临时状态有独立生命周期；trusted local provider 可直接使用 Workspace，但不能冒充强 Sandbox。跨 Agent/Computer 成果只通过显式 Artifact 交换。
6. Agent 身份、Space、Work、Artifact 和权限是稳定事实，不依赖模型、进程、External Driver 或 Computer。Native 与 External Agent 必须共享同一套产品语义。
7. 记忆必须长期连续但按需加载。历史以 Space、Work、Artifact 为事实源，跨上下文检索受权限约束并保留来源；禁止把全部历史灌入 Prompt。
8. 本地 Desktop、Web 和多 Computer 使用同一套架构与事实模型，不得出现本地/远程双轨实现。
9. 先实现当前完整版本，不为未来 HA、Windows 或未知规模预埋复杂度。SQLite、macOS 与 Linux 是当前基线，Windows 后续按需求兼容。
10. 模块化只服务真实替换点。默认使用具体类型、小组件和使用方定义的小接口；允许切换实现时迁移代码，不追求无缝替换，不建通用插件框架。
11. 代码不写注释。用准确命名、短函数、显式数据流、类型、测试和 `DESIGN.md` 表达意图；保持 Rob Pike 式简单、直接、精悍，拒绝 code golf 与架构表演。
12. `DESIGN.md` 是持续演进的产品设计真相。改变核心语义前先更新设计；不得为了复用旧代码而恢复旧概念或兼容路径。
