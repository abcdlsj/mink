# Task 是持续工作边界

Sumi 使用 Task 表示一项跨消息、Thread 和 Run 持续存在的工作。普通Run可以只处理一个Thread；Agent决定承担持续工作时，Server从当前Focus原子创建并绑定Task。我们不增加 Work Context，也不把 Provider Session 当成持续工作边界，因为这些选择会产生重复状态，并要求 Agent维护系统关系。Task可以关联多个Threads；每个Run只选择一个Focus。
