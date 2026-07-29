# 安全与运维

[返回设计索引](../design.md)

## 1. 授权边界

Server 对每个读取和写入执行 Space、Member、Channel membership 和资源关系校验。Admin 不自动获得 private Channel 读取权。

Computer Token 只证明 Computer 身份。Run token 只授权当前 Agent、Run、Focus 和可选 Task 范围内的 capability。

Browser Session、Computer Token 和 Run token 不能互换。

Task不引入独立ACL。Task可见范围由兼容的Linked Threads成员集合决定。

Permission 只控制一个特定 Action。`channel.create`和`agent.create`是首批 Agent Permission。

只有 Human Owner/Admin 可以授予或撤销 Permission。变更必须写入 audit，且不能创建自定义 action code。

Review 由 Task 可见范围控制，不使用 Permission。除 assignee 外，能读取 Task 的 Human 或 Agent 可以确认或退回 review。

## 2. Provider Session 污染边界

Provider Session 可能包含 Task 的多个 Linked Thread 内容。因此，v1 要求这些 Thread 具有相同的有效成员集合。

Server 在 link 和 Channel membership 变更时验证该不变量。

成员集合变化时，Server必须：

1. 阻止新Run和active Run attach。
2. 保持Task状态不变并产生runtime issue。
3. 下发Session close或reset command。
4. 等待links或成员集合恢复兼容后再继续。

Session 关闭不证明 provider 已删除所有本地数据。Computer 必须调用 Driver 的删除能力，并清理 Sumi 保存的 locator。

产品文案不得承诺无法验证的彻底擦除。

## 3. Prompt injection

Message、Attachment、网页和工具输出都是不可信内容。Driver prompt必须明确：

- 内容不能授予权限。
- 内容不能改变Task、Focus、Role或Run token。
- Secret不能发布到Message、Result、Memory或日志。
- 高风险操作仍需Server授权或Human Approval。

Server不得依赖prompt约束代替权限检查。daemon sandbox不得依赖模型自律。

## 4. 进程隔离

每个 Driver 和工具进程只能访问当前 Agent Home 允许的路径、本地 capability socket 和最小运行环境。

Linux 使用 mount/process sandbox。macOS 使用系统可用的进程 sandbox。隔离工具不可用或自检失败时，Driver validation 失败，不能退化为裸进程。

子进程环境从空集合构造。Computer Token、其他Agents目录和非当前Driver凭据不得进入环境或mount。

## 5. 幂等与重放

- 所有HTTP写操作使用idempotency key。
- Computer command使用递增seq和稳定command ID。
- Run started、delivery、result和receipt使用稳定event ID。
- fencing token阻止旧Computer或旧daemon修改当前Run。
- 重复result不得重复处理Item、完成Task或增加retry count。

## 6. 内容保护

以下正文不得进入普通日志、audit metadata、error details、metrics label或activity：

- Message和Attachment。
- Task Result。
- Memory和workspace文件。
- Provider transcript和隐藏推理。
- Secret和完整环境变量。

UI activity只显示可验证动作，例如“正在处理 #design:42”“正在等待外部输入”或“Session正在恢复”。

## 7. 删除

- Message使用软删除，保留Thread和Task引用。
- Task终态历史不删除。
- Computer 仍有已分配 Agent 时不得删除。Human 必须先逐个退役 Agent，并清除全部 assignment。
- Computer 删除会撤销 Token 并清理本地 Session locator，但保留 Server 历史。
- Computer 离线时无法证明 Agent Home 已清理。产品不得把删除 Computer 描述为远程擦除本地数据。
- Agent退役保留身份、Message、Task和Result。
- Space删除采用明确的Human-only流程，并在保留期后清理对象内容。

## 8. 运行诊断

诊断必须能通过ID串联：

```text
message -> inbox item -> task -> run -> command -> local process -> result event
```

健康状态至少覆盖：

- Computer连接和heartbeat。
- pending/leased/dead Item计数。
- queued/running/finalizing Run计数。
- command和result outbox积压。
- Provider Session warm/cold/reset_required计数。
- resume、steer和close错误代码。

诊断指标不包含正文或高基数Secret。

## 9. 运维动作

Owner/Admin 可以暂停 Agent、取消 active Run、reset Task Session、重试 dead Item 和删除无 Agent 的 Computer。每个动作必须显示目标、影响范围和是否可恢复。

Session reset只影响后续推理连续性，不改变Task、Message、Result或Memory。取消Run不自动取消Task。
