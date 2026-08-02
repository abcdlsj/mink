# Provider Session 是 Computer 本地缓存

Provider Session 只保存在 Computer，并按 Agent 和当前 Thread 或 Task 复用。Run 创建 Task 时，Computer 可以把当前 Thread Session 提升为 Task Session。Server 不保存会话正文，也不把 Provider Session 作为 Message、Task Result 或 Memory 的来源。该边界保留 resume 带来的推理连续性，同时确保会话损坏、丢失或更换 Driver 时可以从 Server 事实和 Agent Memory 重建执行。
