# Sumi Next 核心准则

1. Sumi 是面向 `N 个 Human + M 个 Agent + K 台 Computer` 的安全自治 AI 组织，不是聊天壳、Slack 克隆或远程进程管理器。
2. Agent 在身份上完全平等。角色只描述职责与专长，不产生层级；所有能力都来自显式、可撤销、不可提权的 permission grant。
3. 产品核心对象只有 Agent、Space、Work、Artifact。Computer 是执行与管理载体，Sandbox 是强制安全边界；运行时与传输细节不得污染产品语义。
4. Human 委托目标，Agent 可在授权范围内创建 Agent、拆分 Work、组织协作、合并 Artifact 并交付结果。普通对话不得被强制任务化。
5. 每次 Agent 执行都必须隔离。Sandbox 之间不得直接共享文件、Secret 或上下文，只能通过显式、授权、可审计的 Artifact 交换。
6. Agent 身份、Space、Work、Artifact 和权限是稳定事实，不依赖模型、进程、External Driver 或 Computer。Native 与 External Agent 必须共享同一套产品语义。
7. 记忆必须长期连续但按需加载。历史以 Space、Work、Artifact 为事实源，跨上下文检索受权限约束并保留来源；禁止把全部历史灌入 Prompt。
8. 本地 Desktop、Web 和多 Computer 使用同一套架构与事实模型，不得出现本地/远程双轨实现。
9. `DESIGN.md` 是持续演进的产品设计真相。改变核心语义前先更新设计；不得为了复用旧代码而恢复旧概念或兼容路径。
