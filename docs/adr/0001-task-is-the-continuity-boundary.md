# Task 是持续工作边界

Sumi 使用 Task 表示一项跨消息、Thread 和 Run 持续存在的工作。普通 Run 可以只处理一个 Thread；Agent 决定承担持续工作时，Server 从当前 Focus 原子创建并绑定 Task。我们不增加 Work Context，也不把 Provider Session 当成持续工作边界，因为这些选择会产生重复状态，并要求 Agent 维护系统关系。Task 可以关联多个 Threads；每个 Run 只选择一个 Focus。
