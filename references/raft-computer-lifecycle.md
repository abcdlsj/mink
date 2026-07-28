# Raft Computer 生命周期实现参考

本参考来自本机 `/Users/lisongjian/.local/bin/raft-computer` 的只读分析。样本是 macOS arm64 Mach-O 和 Raft Computer v1.0.14。应用代码位于 `NODE_SEA/__NODE_SEA_BLOB`。bundle 标记为 `/* raft-computer SEA bundle v1.0.14 */`，构建路径为 `raft-computer-sea/darwin-arm64/computer-bundle.cjs`。

本文记录设计锚点和可迁移原则。完整 bundle 不进入仓库。版本升级可能改变符号和行为，这些内部实现不属于稳定 API。

## Agent 进程生命周期

在解包后的 `computer-bundle.cjs` 中检索以下符号：

| 源码位置 | 可借鉴内容 |
| --- | --- |
| `AgentProcessManager` | Agent 进程、消息投递、重启、停止和活动上报的主协调器 |
| `AgentProcessManager.stopAgent()` | stop epoch、SIGTERM、等待退出、超时 SIGKILL 和清理顺序 |
| `RuntimeProcessBindingFence` | 异步回调必须确认仍绑定当前 runtime/process instance |
| `AgentLifecycleRecords` | launch/session/restart/stop epoch 的集中生命周期记录 |
| `AgentNoProcessResidency` | 没有进程时也显式保存 idle、cooldown、terminal failure 等驻留状态 |
| `AgentNoProcessResidency.assertInvariant()` | 防止 cooldown、restart config、terminal failure 等互斥状态并存 |
| `AgentStartCoordinator` | 全局 Agent 启动并发限制与启动节流 |
| `AgentStartPendingDeliveryBuffer` | runtime starting 时暂存消息，ready 后再投递 |
| `AgentVisibleDeliveryLedger` | 将“已经对 Agent 可见”建模为显式事实，而不是 socket write 成功 |
| `RuntimeProgressState` | runtime progress 与 stale 判断，不只检查 PID |

Sumi 采用以下原则：

- 使用 `launchId/process_instance_id` fencing 旧进程回调。
- stop 会递增 epoch，延迟重启必须检查 stop 后是否仍获授权。
- Agent 没有进程时，仍需记录 idle backoff、terminal failure 或等待唤醒状态。
- runtime ready 和消息已交付分别有证据，不能由进程存在推导。
- 停止流程包含发信号、等待退出、强制停止和 reap。

## 卡死恢复与退出

可检索以下日志和常量定位对应实现：

- `DEFAULT_STALLED_RECOVERY_SIGTERM_TIMEOUT_MS`
- `stalled_recovery_sigterm_timeout`
- `kill_signal_sequence: "SIGTERM,SIGKILL"`
- `Process unavailable; restart required`
- `Turn completed; restarting immediately for queued message`

Raft 在 runtime stalled 时发送 SIGTERM，等待固定期限，再发送 SIGKILL。trace 记录 signal sequence 和 process instance。Sumi 需要记录相同类型的 timeout、reap 和诊断信息，具体期限由 Driver 契约定义。

## Runner supervisor 与熔断

Computer 外层 server runner 还提供另一组参考：

| 源码位置 | 可借鉴内容 |
| --- | --- |
| `nextRunnerStateOnExit()` | 按 graceful、config-error、unlinked、crash 分类退出 |
| `rehydrateRunnerRecord()` | 服务重启后从持久证据恢复 runner record |
| `handleRunnerExitForSupervisor()` | 统一处理退出、重启预算和状态迁移 |
| `resetRunnerHealth()` | 人工修复后清理 degraded 状态 |
| `degraded_backoff` / repeated crashes | 超出 crash budget 后暂停自动重启，避免无限 crash loop |

Sumi 保留现有 daemon 和 Driver 层次，并实现退出分类、重启预算和 degraded 或 orphaned 状态。重试次数必须有上限，防止同一 Run 持续占用资源和续租 Inbox。

## 适用范围

- Sumi v1 不实现 Raft bundle 中的多 runtime、迁移、桌面应用和多 Server attachment。
- Sumi 保留 Inbox lease 和 bounded Run 模型，只采用 Raft 的 fencing、状态记录、启动协调、停止、reap 和恢复检查。
- Sumi 的测试验证自身协议，不依赖 Raft 内部符号。
