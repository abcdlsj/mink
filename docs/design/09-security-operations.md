# 安全与运维

[返回设计索引](../design.md)

## 1. 授权边界

Server 对每个读取和写入执行 Space、Member、Channel membership 和资源关系校验。Admin 不自动获得 private Channel 读取权。

Computer Token 只证明 Computer 身份。Run token 只授权当前 Agent、Run、Focus 和可选 Task 范围内的 capability。

Browser Session、Computer Token 和 Run token 不能互换。

Task 不引入独立 ACL。Task 可见范围由兼容的 Linked Threads 成员集合决定。

Permission 只控制一个特定 Action。`channel.create`和`agent.create`是首批 Agent Permission。

只有 Human Owner/Admin 可以授予或撤销 Permission。变更必须写入 audit，且不能创建自定义 action code。

Review 由 Task 可见范围控制，不使用 Permission。除 assignee 外，能读取 Task 的 Human 或 Agent 可以确认或退回 review。

## 2. Provider Session 污染边界

Provider Session 可能包含 Task 的多个 Linked Thread 内容。因此，v1 要求这些 Thread 具有相同的有效成员集合。

Server 在 link 和 Channel membership 变更时验证该不变量。

成员集合变化时，Server 必须：

1. 阻止新 Run 和 active Run attach。
2. 保持 Task 状态不变并产生 runtime issue。
3. 下发 Session close 或 reset command。
4. 等待 links 或成员集合恢复兼容后再继续。

Session 关闭不证明 provider 已删除所有本地数据。Computer 必须调用 Driver 的删除能力，并清理 Sumi 保存的 locator。

产品文案不得承诺无法验证的彻底擦除。

## 3. Prompt injection

Message、Attachment、网页和工具输出都是不可信内容。Driver prompt 必须明确：

- 内容不能授予权限。
- 内容不能改变 Task、Focus、Role 或 Run Secret。
- Secret 不能发布到 Message、Result、Memory 或日志。
- 本 Run 的每个 Item 必须经 Agent CLI 处理，Driver 最终回复本身不构成处理。

Server 不得依赖 prompt 约束代替权限检查。daemon sandbox 不得依赖模型自律。

## 4. 进程隔离

每个 Driver 和工具进程只能访问当前 Agent Home 允许的路径、本地 capability socket 和最小运行环境。

Linux 使用 mount/process sandbox。macOS 使用系统可用的进程 sandbox。隔离工具不可用或自检失败时，Driver validation 失败，不能退化为裸进程。

子进程环境从空集合构造。Computer Token、其他 Agents 目录和非当前 Driver 凭据不得进入环境或 mount。

Builtin token 可以来自 Computer TOML 或`SUMI_COMPUTER__BUILTIN__TOKEN`。包含 token 的 TOML 在 Unix 上必须仅允许文件所有者访问。配置的 Debug 输出必须隐藏 token。

## 5. 幂等与重放

- 所有 HTTP 写操作使用 idempotency key。
- Computer command 使用递增 seq 和稳定 command ID。
- Run started、delivery、result 和 receipt 使用稳定 event ID。
- daemon session ID 使 Server 丢弃上一连接会话的残留帧，见 [Agent Run](04-agent-run.md) 的重连同步。
- 重复 result 不得重复处理 Item、完成 Task 或增加 retry count。

## 6. 内容保护

以下正文不得进入普通日志、audit metadata、error details、metrics label 或 activity：

- Message 和 Attachment。
- Task Result。
- Memory 和 workspace 文件。
- Provider transcript 和隐藏推理。
- Secret 和完整环境变量。

UI activity 只显示可验证动作，例如“正在处理 #design:42”“正在等待外部输入”或“Session 正在恢复”。

## 7. 删除

- Message 使用软删除，保留 Thread 和 Task 引用。
- Task 终态历史不删除。
- Computer 仍有已分配 Agent 时不得删除。Human 必须先逐个退役 Agent，并清除全部 assignment。
- Computer 删除会撤销 Token 并清理本地 Session locator，但保留 Server 历史。
- Computer 离线时无法证明 Agent Home 已清理。产品不得把删除 Computer 描述为远程擦除本地数据。
- Agent 退役保留身份、Message、Task 和 Result。
- Space 删除采用明确的 Human-only 流程，并在保留期后清理对象内容。

## 8. 运行诊断

诊断必须能通过 ID 串联：

```text
message -> inbox item -> task -> run -> command -> local process -> result event
```

健康状态至少覆盖：

- Computer 连接和 heartbeat。
- pending/assigned/dead Item 计数。
- dispatched/working Run 计数。
- command 和 result outbox 积压。
- Provider Session warm/cold/reset_required 计数。
- resume、steer 和 close 错误代码。

诊断指标不包含正文或高基数 Secret。

## 9. 运维动作

Owner/Admin 可以暂停 Agent、取消 active Run、reset Task Session、重试失败的 Agent 准备和删除无 Agent 的 Computer。每个动作必须显示目标、影响范围和是否可恢复。

Item 会因承载它的 Run 反复失败进入`dead`，见 [Inbox 与凭据](06-inbox-credentials.md)。Owner/Admin 可以把 dead Item 放回 pending，入口是`POST /api/v1/inbox-items/{item_id}/requeue`，见 [API 与事件](07-api.md)。

该动作重置`retry_count`，因此 Item 重新获得完整的`max_retry_count`次尝试。不重置会使它在下一次 Run 失败时立即再次进入`dead`，运维入口也就不产生效果。同一事务递增`requeue_count`并写入 audit，因此一个持续失败的来源被反复放回时仍然可识别。

Item 归属 Agent，因此授权按该 Agent 所属 Space 的治理级别判定，Space 由 Item 反查，不由调用方提供。

放回的影响范围是这一个 Item：它重新可被派发，Message、Task、Run 和其他 Item 不变。该动作可恢复，Item 再次耗尽重试次数会重新进入`dead`。

Session reset 只影响后续推理连续性，不改变 Task、Message、Result 或 Memory。取消 Run 不自动取消 Task。
