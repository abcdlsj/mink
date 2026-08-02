# Computer 与 Agent

[返回设计索引](../design.md)

## 1. Computer 职责

Computer daemon 承载本机 Agents，并负责以下本地能力：

- 维护 Server 出站连接和 heartbeat。
- 保存 Agent Home、Memory、workspace 和 Driver 私有状态。
- 调度本机执行槽。
- 启动、steer、interrupt 和回收 Driver。
- 创建、恢复、重置和关闭 Provider Session。
- 保存 command、Run 和 result 的本地 outbox。
- 保护 Computer Token 和模型凭据。

Computer 不创建 Task 关系，不根据 Message 正文路由工作，也不成为 Server 事实的权威副本。

## 2. Computer 生命周期

```text
pairing -> online <-> offline -> deleted
```

- `pairing`：Human 尚未确认本机身份。
- `online`：daemon 长连接和 heartbeat 有效。
- `offline`：连接失效，但配对关系仍存在。
- `deleted`：Computer Token 已撤销，状态不可恢复。

daemon 退出或网络断开只导致 offline。使用同一 Computer Token 重连后恢复 online。

存在已分配 Agent 时，Server 必须拒绝删除 Computer。Human 必须先逐个退役这些 Agent，并清除全部 Computer assignment。

Computer 没有已分配 Agent 后，删除操作撤销 Computer Token，并把 Computer 标记为`deleted`。系统不得在 Computer 删除事务中自动退役或重新分配 Agent。

## 3. Agent Home

```text
~/.sumi/
  computer/{space-id}/{computer-id}/
    daemon.db
    secrets.json
    agents/{agent-id}/
      profile.json
      memory/
      workspace/
      drivers/{driver-kind}/
      sessions/
      runs/
      logs/
  runtime/{computer-id}/daemon.sock
```

- `daemon.db` 保存本地 command、Run、Session registry 和 outbox。
- `secrets.json` 保存 Computer Token 和本地 Driver 认证。
- `profile.json` 是 Server Agent 配置的缓存。
- `memory/` 和 `workspace/` 属于 Agent，不属于 Driver 或 Task。
- `sessions/` 只保存 Provider Session locator、generation 和恢复元数据，不保存为 Server 事实。
- `runs/` 保存有界执行的临时文件。

目录权限必须限制为 daemon OS 用户。OS sandbox 必须限制 Driver 进程的访问路径。

Driver 只能访问当前 Agent Home 中明确允许的路径。不同 Agent 不能读取彼此的 Home。

### 3.1 重建期本地基线

当前开发机上已有的`~/.sumi`属于旧版本数据。本次重建可以直接删除该目录，并按本文件定义的新结构重新创建。

删除前必须把目标解析为当前用户`.sumi`目录的绝对路径，并验证目标不是用户主目录或其他父目录。不得使用未解析的`~`、`$HOME`或通配符执行递归删除。

旧目录不需要备份、迁移或兼容读取。首次建立新版本 Agent Home 后，本规则失效；后续删除必须遵守正式的 Computer 和 Agent 生命周期。

## 4. Agent 持续性

Agent 的持续身份由 Server 的 Member/Agent 记录和本地 Agent Home 共同实现：

- Server 保存 identity、Role、权限、Computer assignment 和生命周期意图。
- Computer 保存 Memory、workspace 和 Driver 私有状态。
- Task 保存正式工作连续性。
- Provider Session 只优化同一 Task 的推理连续性。

切换 Driver、关闭 Session 或结束 Run 都不创建新 Agent。

Agent 生命周期：

```text
provisioning -> active <-> suspended -> retired
       \----------> error
```

一个 Agent 同时最多有一个 active Run。暂停可以等待当前 Run 完成，也可以立即请求取消。

退役不可恢复。历史 Message、Task、Run 和 Result 保留。

## 5. Provider Session registry

Computer 为每个 Session 保存：

- `agent_id`
- `scope_kind=thread|task`
- `scope_id`
- `driver_kind`
- `generation`
- `provider_session_locator`
- `workspace_fingerprint`
- `role_revision`
- `audience_fingerprint`
- `state=ready|in_use|closing|closed|lost`
- `created_at`、`last_resumed_at`、`closed_at`

唯一复用键是 `(agent_id, scope_kind, scope_id, generation)`。Thread Session 只服务一个 Thread；Task Session 只服务一个 Task。

`provider_session_locator`只存在于 Computer。Server 不保存 locator、会话正文、provider transcript 或 continuity 投影。

Browser 需要 continuity 时，Server 向在线 Computer 查询，见 [API 与事件](07-api.md) 的 WebSocket query。Computer 离线时返回`unavailable`。

Computer 按 scope 下最高 generation 的 Session 状态回答：`ready`和`in_use`是`warm`，`closing`和`closed`是`cold`，`lost`是`reset_required`。scope 下没有 Session 时是`cold`。`warm`只说明 Session 还在，实际能否 resume 仍取决于 Run 启动时的 fingerprint 比较。

## 6. Session 解析

Run 启动时，Computer 的 Session resolver 按以下顺序执行：

1. Run 有 Task 时查找 Task Session，否则查找 Focus Thread Session。
2. 比较 Driver、workspace、Role 和 audience fingerprint。
3. 条件兼容时 resume 现有 Session。
4. Session 不存在或无法恢复时创建新 generation。
5. 将 Run 事实、可选 Task 摘要、Focus 和未处理 Items 注入新 turn。

Session resume 的收益是保留同一项工作的推理线索、工具上下文和 provider cache。以下场景最适合复用：

- 同一 Task 因新 reply 启动后续 Run。
- Agent yield 后继续同一 Task。
- Task 等待外部结果后重新收到相关 Message。
- Run 因网络或 Computer 调度结束，但工作边界未改变。

同一 Task 的多个 Runs 或同一普通 Thread 的多个 Runs 可以复用一个 Session。一个 Run 不能同时使用多个 Sessions。

## 7. Thread Session 提升

Agent 在普通 Run 中创建 Task 时，Server 把当前 Run 绑定到新 Task，并下发`run.task_bound` command。

Computer 将当前 Thread Session 的 scope 改为 Task。generation 保持不变。

提升要求 Session 属于当前 Agent 和 Focus Thread。audience、Driver、Role 和 workspace fingerprint 必须兼容。

提升失败不回滚 Server Task。当前 Run 可以继续使用已打开的 Session。后续 Run 为 Task 创建 cold generation。

## 8. Session 失效

以下事件必须关闭当前 generation，并在需要继续 Task 时创建新 generation：

- Task 进入`done`或`closed`。
- Linked Threads 的有效成员集合发生不兼容变化。
- Agent 更换 Driver。
- Role 变化会改变安全或授权边界。
- workspace 被替换、重建或发生不兼容切换。
- provider session locator 丢失、损坏或 resume 失败。
- Human 或 Agent 在授权范围内显式 reset。

以下事件不得单独触发换新：

- token 数达到阈值。
- Run 数达到阈值。
- 固定时间经过。
- Server 或 daemon 单次重启。
- Task 当前 Run 因等待外部输入而 yield。

Provider 压缩上下文时，Computer 记录诊断事件，但不自动创建新的 Sumi Session generation。

Sumi 通过 Task、Message、Result 和 Memory 保证事实可重建。系统不依赖无限上下文。

## 9. Session 丢失后的恢复

Session 丢失不等于 Task 丢失。Computer 创建新 generation，并注入：

- Agent identity、Role revision 和 Memory 投影。
- Task title、status、Result 草稿元数据和 Linked Threads。
- Focus Root Message 和必要 replies。
- 未处理 Inbox Items。
- 最近 Runs 的结构化结果和错误，不包含隐藏推理。

Agent 可以按需读取更多 Message 和 Memory。Server 不保存或重放 provider transcript。Memory 投影的注入规则见 [Driver 与 Agent CLI](05-driver-cli.md)。

## 10. Memory 与 workspace

Memory 属于 Agent，跨所有 Tasks 持续存在。Agent 只有主动总结可复用知识时才写入 Memory。Message 历史和 Provider Session 不自动复制到 Memory。

workspace 也属于 Agent。Task 可以在 workspace 中使用分支、目录或项目状态，但 Task 不拥有独立 workspace 实体。

Computer 用 fingerprint 判断当前 Session 是否还能安全复用。

Memory 正文和 workspace 文件不上传 Server。Server 不保存 Memory 投影：文件名、大小、SHA-256 和更新时间在每次读取时向在线 Computer 查询。

UI 读取正文时必须经在线 Computer 临时转发，并设置`no-store`。

Computer 生成投影时递归遍历 Memory 目录，路径相对 Memory 根。symlink 可能指向 Memory 根之外，投影和正文读取都不跟随它。

## 11. Computer 删除与 Agent 迁移

Computer 删除和 Agent 迁移是两个独立流程。迁移不能作为删除 Computer 的隐式副作用。

v1 不支持 active Agent 热迁移。迁移需要：

1. 暂停 Agent 并结束 active Run。
2. 关闭现有 Provider Sessions。
3. 转移或重新建立 Agent Home。
4. 更新 Server assignment。
5. 在目标 Computer 通过 Driver 和 sandbox 校验。
6. 恢复 Agent。

如果 Agent Home 不可恢复，Message、Task 和 Result 仍保留。Memory、workspace 和 warm Session continuity 可能丢失，UI 必须明确显示该限制。

Agent 退役事务必须结束 active Run、关闭 Sessions，并清除 Computer assignment。Computer 在线时，daemon 同时清理 Agent Home；Computer 离线时，Server 记录本地清理未确认。

本地清理未确认不阻止 Computer 删除。删除 Computer 会撤销 Token，但不能证明离线设备上的 Agent Home 已被擦除。
