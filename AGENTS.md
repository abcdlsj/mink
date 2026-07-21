# Sumi Next 核心准则

1. Sumi 面向 `N 个 Human + M 个 Agent + K 台 Computer`，产品核心对象是 Agent、Space、Work、Artifact。它不是聊天壳、Slack 克隆或远程进程管理器。
2. Agent 身份平等。角色只描述职责与专长；能力只能来自显式、可撤销、不可提权的 permission grant。
3. Agent、Space、Work、Artifact 和权限是稳定事实，不依赖模型、进程、External Driver 或 Computer。Native 与 External Agent 必须共享同一套产品语义。
4. Computer 是执行载体；Workspace、Sandbox、Secret、Run 是不同边界。必须诚实声明 trusted-local 的隔离能力，不能把它伪装成强 Sandbox。跨 Agent 或 Computer 的成果必须通过显式 Artifact 交换。
5. 记忆按需加载。历史以 Space、Work、Artifact 为事实源，检索必须受权限约束并保留来源，禁止把全部历史灌入 Prompt。
6. Web、Desktop 和多 Computer 使用同一套事实模型。当前基线是 SQLite、macOS 和 Linux；不要为未来 HA、Windows 或未知规模预埋复杂度。
7. `DESIGN.md` 是产品设计真相：开始需求前先读 `DESIGN.md`；改变核心语义前先更新它；每个需求完成后必须同步结果、边界和遗留风险。不得为了复用旧代码恢复旧概念或兼容路径。
8. 每次开发前先读根目录 `AGENTS.md`、`CLAUDE.md` 和相关 `DESIGN.md`，再读代码。`CLAUDE.md` 必须是指向 `AGENTS.md` 的软链接，不维护第二份入口正文。
9. Go 遵循 Google Go Style Guide 与 Best Practices：命名准确，函数短小，主路径直接，错误尽早返回，数据流显式。只在真实重复或替换边界出现时抽象，禁止为了通用性牺牲可读性和类型安全。
10. 代码不写解释性注释；用命名、类型、测试和 DESIGN 表达意图。完成前必须运行与改动匹配的测试、lint、format、generate/build，并报告实际证据、未覆盖风险和工作区状态。
