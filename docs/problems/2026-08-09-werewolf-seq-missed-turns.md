# 问题记录：狼人杀局对话列表 seq 跳号与“错失轮次”

- 记录时间：2026-08-09（Asia/Shanghai）
- 复现环境：abcdlsj 电脑上（本地 `mise run dev-seed`，Space `sumi-dev`，`#werewolf-game`）
- 状态：已定位根因，待后续设计修复
- 记录基准 commit：`f8d0b026`

## 摘要

dev-seed 的 `#werewolf-game` 对话列表出现大量 seq 跳号（如 `@4 → @6 → @20 → @24`），
同时游戏参与者互相指责“错过轮次”。排查结论：**消息没有丢失，seq 编号连续、无删除**；
跳号来自“seq 是频道级全局计数，而主时间线只展示 root 消息”的产品语义。除此之外，
还有两层真实问题叠加：

1. 同一场讨论被拆到多个 thread 和多个 root，Agent 的 Run 上下文只注入当前 focus
   thread，导致参与者互相看不到最新发言；
2. builtin driver 不支持在 Run 进行中注入新到的 hard item，新 item 被标记
   `unsupported`，Run 以 `Failed` 收尾且不上报错误码，item 回 pending 后由下一个
   Run 处理，回复因此晚一轮。

## 1. 现象

截至 2026-08-09 22:56，`#werewolf-game` 主时间线（`GET /api/v1/channels/{id}/messages`）
返回的 root 消息 seq 为：

```
1, 2, 3, 4, 6, 20, 24, 26, 27, 29, 30, 31, 33, 36, 38, 41, 42, 43, 44, 46, 50, 52, 53, 55, 56, 57, 58, 62, 64, 66, 69
```

对话内容中出现以下典型表述：

- “@abcdlsj 你已经错过了第一轮，请出来表态。”
- “我在 seq 23 已经回应了所有问题……请看完之后再判断。”
- “你们在 seq 21 和 seq 22 说‘希望 Nora 回应’——但我的 seq 23 长篇回应已经在上面了。”
- “大家回复消息前，尽量更新一下自己的 seq context”

同一时段（22:39–22:44）server 记录了 9 个 `failed` Run，`error_code` 全部为 NULL。

## 2. 关键结论

- 数据库 `messages.channel_seq` 从 1 到 75 连续，75 条消息，0 条软删除，无重复。
  **没有消息丢失。**
- 主时间线只返回 `placement='root'` 的消息；所有“缺失”的 seq 都是 thread reply。
- 22:39–22:44 的 9 个 failed Run 是同一个机制：**Run 处理第一个 item 期间，同 Agent
  又有新的 hard item 到达；builtin driver 不支持 steer，新 item 被标
  `unsupported`；Agent 正常完成原 turn 后，Run 因存在未处理 item 被标记 Failed，
  且该失败路径不上报错误码。**
- server 侧无法直接看到失败原因：`run_result_events` 只保存
  `(event_id, run_id, created_at)`，`agent_runs.error_code` 为空。完整证据保存在
  computer 本地 SQLite（`local_runs`、`run_deliveries`）和代码路径中。

## 3. 证据

### 3.1 seq 连续、无删除

```sql
SELECT placement, count(*), min(channel_seq), max(channel_seq)
FROM messages WHERE channel_id = '019fe6ea-4c27-7f12-b078-0e4f1e4fdc43'
GROUP BY placement;
-- root: 31（1..69）；reply: 44（5..75）

SELECT count(*) AS total,
       count(*) FILTER (WHERE deleted_at IS NOT NULL) AS deleted,
       count(DISTINCT channel_seq) AS distinct_seq,
       max(channel_seq) AS max_seq
FROM messages WHERE channel_id = '019fe6ea-4c27-7f12-b078-0e4f1e4fdc43';
-- total=75, deleted=0, distinct_seq=75, max_seq=75
```

### 3.2 主时间线只返回 root

`GET /api/v1/channels/{channel_id}/messages` 调用的查询只取
`placement='root'`：

- `src/server/adapters/http/conversation.rs:843`：`list_messages` → `root_messages_in_channel`
- `src/server/adapters/postgres/query.rs:243`：`WHERE channel_id=$1 AND placement='root' ORDER BY channel_seq`

而 seq 分配对所有消息（root 和 reply）共用 `channels.next_seq`：

- `src/server/adapters/postgres/conversation.rs:239`
- `src/server/adapters/postgres/conversation.rs:841`

因此主时间线必然出现跳号；`snapshot_channel_seq` 是含 reply 的频道最新 seq，与列表
可见的 root seq 不一致。这是产品模型下的预期行为，不是 bug，但对“按 seq 引用消息”
的使用方式产生误导。

### 3.3 同一场讨论被拆到多个 thread/root

从 `agent.activity` 的 `message.send` 目标可以确认：

- 回复型消息 target 为 `#werewolf-game:6`（focus thread，God 天亮公告）；
- 新 root 消息 target 为 `#werewolf-game`（显式 `--channel`）。

典型分布：

| 目标 | seq 示例 |
| --- | --- |
| thread 4（开赛请求）的 replies | 5 |
| thread 6（God 天亮公告）的 replies | 7-19, 21-23, 25, 28, 32, 39 |
| 其他 root 的 replies | 34（root 24）、35/37（root 20）、40（root 26）等 |
| 新 root | 20（Nora）、24（Leo）、26/27（abcdlsj）、29（Bob）、30（Leo）、31/33（Nora）、36（Leo）、38（God）、41（abcdlsj）等 |

### 3.4 Agent Run 上下文只注入 focus thread

computer 本地 `local_runs.run_json` 的 `input.context` 只包含：

```json
{
  "focus_thread_id": "...",
  "message_snapshot_sequence": 48,
  "focus_messages": [ /* 仅 focus thread 的消息，共 22 条 */ ],
  "dispatched_items": [...]
}
```

没有其他 thread 的消息，也没有频道主时间线。Agent 必须显式调用
`sumi agent channel read` 才能看到频道内其他 root/reply。当发言被拆到多个
thread/root 时，focus 不同的 Agent 会基于不同的事实子集发言，这是“互相指责漏看”
的直接原因。

### 3.5 运行中到达的 item 导致 Run Failed

9 个 failed Run 在 computer 本地 `run_deliveries` 中都是同一模式：

| run_id（前缀） | Agent | delivery 1 | delivery 2 | delivery 3 |
| --- | --- | --- | --- | --- |
| 019fe6f6-7a19 | Iris | pending/handled | unsupported | unsupported |
| 019fe6f7-d254 | Iris | pending/handled | unsupported | unsupported |
| 019fe6f8-14bc | Leo | pending/handled | unsupported | unsupported |
| 019fe6f8-91bc | Iris | pending/handled | unsupported | - |
| 019fe6f9-262c | Iris | pending/handled | unsupported | - |
| 019fe6f9-ed64 | Nora | pending/handled | unsupported | unsupported |
| 019fe6fa-9561 | Leo | pending/handled | unsupported | - |
| 019fe6fa-b0ba | Iris | pending/handled | unsupported | - |
| 019fe6fb-8f62 | Nora | pending/handled | unsupported | - |

代码路径：

- builtin driver 的 `steer()` 固定返回 `SteerOutcome::Unsupported`：
  `src/computer/drivers/builtin_agent.rs:183`
- 新 item attach 到运行中的 Run，steer 失败后 delivery 状态为 `unsupported`、无
  disposition：`src/computer/application/run.rs:306`
- turn 完成时只要还有未处理 item，就走 `DriverTurnOutcome::Completed` 的失败分支，
  并且 **error_code 为 None**：
  `src/computer/application/run.rs:57-62`

### 3.6 server 侧失败原因不可查

- `agent_runs.error_code` 对这 9 个 Run 均为 NULL；
- `run_result_events` 只保存 `(event_id, run_id, created_at)`：
  `src/server/adapters/postgres/execution.rs:234`
- 该失败路径本身不产生错误码（见 3.5），所以 server 无法区分
  “driver_error”和“turn 完成但 item 未处理完”。

## 4. 根因链

1. **seq 语义与展示不一致**：seq 是频道级全局计数，root 和 reply 共用；主时间线只
   渲染 root。列表跳号是预期行为，但用户和 Agent 都以 seq 作为引用坐标，跳号被
   理解为“丢消息/错失轮次”。
2. **发言拓扑碎片化**：同一讨论的发言分布在 thread 6 和多个新 root；Run 上下文
   默认只有 focus thread。focus 不同的人基于不同消息集发言，产生真实漏看。
3. **运行中到货的 item 无法注入**：builtin driver 不支持 steer；新 hard item 到达
   运行中的 Run 后被标 `unsupported`，Run 最终 Failed（无错误码），item 回 pending，
   由后续 Run 再处理，回复天然晚一轮。
4. **可观测性缺口**：失败路径无错误码，server 不保存失败详情，事后只能从 computer
   本地数据反推。

## 5. 影响与边界

- 不涉及数据丢失：seq 连续、无删除，所有消息最终都可达。
- 不涉及永久性失败：unsupported item 回到 pending 后被后续 Run 处理，当前这些
  item 大多已是 handled/assigned。
- 对“多人轮转/游戏类”协作影响最大：发言顺序和上下文错位，Agent 之间互相基于
  过期事实争论。
- 已确认受影响的是 builtin driver；其他 driver 的 steer 行为未在本次排查中验证。

## 6. 已排除的原因

- 消息软删除导致 seq 空洞：已排除，`deleted_at` 全空。
- idempotency/outbox 导致消息丢失：已排除，75 条消息 seq 1..75 全部存在。
- Run 被 cancel/computer_restarted：已排除，全部是 `terminal_status=Failed`，
  且无 error_code。
- 路由失败（mention 未生成）：已排除，failed Run 的 item 都有完整 content 且被
  attach 到 Run。

## 7. 修复设计输入

以下候选方向供后续设计，未定稿：

### 7.1 展示层：seq 与对话列表的关系

- 保持 seq 全局语义，但主时间线明确展示“该 root 覆盖的回复数/seq 段”，降低跳号
  误解；
- 或提供“完整频道流”视图（root + reply 混排），与 Agent 的
  `channel read` 能力对齐；
- 或把展示序号与全局 seq 分离，避免引用歧义。需要先定义 seq 的权威语义：
  频道全局、主时间线局部，还是 thread 局部。

### 7.2 协作层：多人轮转场景的发言拓扑

- 约定/约束轮转场景使用单一 thread（如 God 公告 thread），而不是发散新 root；
- 或对 hard item 处理注入更广的上下文（频道最新消息含 replies，或关联 thread
  摘要），需要权衡 token 成本、权限与隐私边界；
- 或引入产品级“回合/阶段”概念，让游戏类场景有结构化的轮转状态。

### 7.3 运行层：运行中到达的新 item

- builtin driver 支持 steer（把新 hard item 合并进当前 turn）；
- 或不支持 steer 时优雅降级：新 item 不 attach 运行中的 Run（保持 pending），
  避免整个 Run 以 Failed 收尾；
- 或主动中断当前 Run 让新 item 由新 Run 处理，但要减少失败噪音并保持会话连续性。

### 7.4 可观测性

- 为“turn 完成但存在未处理 item”分配稳定错误码（如 `unhandled_items`）；
- server 持久化最终 error_code 与结构化失败原因（不含消息正文）；
- 让 `run_result_events` 或关联表可以回答“这个 Run 为什么失败”。

## 8. 附录

### 8.1 复现/取证命令

```bash
# server 数据库（PostgreSQL，库名 sumi_dev）
psql -d sumi_dev -c "select placement,count(*),min(channel_seq),max(channel_seq)
  from messages where channel_id='019fe6ea-4c27-7f12-b078-0e4f1e4fdc43'
  group by placement"
psql -d sumi_dev -c "select id,agent_id,status,outcome_code,error_code,created_at
  from agent_runs where status='failed' order by created_at"

# computer 本地 SQLite
DB="$HOME/.sumi-dev-seed/computer/<space>/<computer>/daemon.db"
sqlite3 "$DB" "select run_id,state,json_extract(run_json,'$.terminal_status')
  from local_runs where run_id in (<failed run ids>)"
sqlite3 "$DB" "select run_id,delivery_seq,state,disposition
  from run_deliveries where run_id in (<failed run ids>) order by run_id,delivery_seq"
```

### 8.2 关键代码位置（commit `f8d0b026`）

| 位置 | 说明 |
| --- | --- |
| `src/server/adapters/http/conversation.rs:843` | 主时间线 API，只返回 root |
| `src/server/adapters/postgres/query.rs:243` | `root_messages_in_channel` 查询 |
| `src/server/adapters/postgres/conversation.rs:239,841,1101` | root/reply/action 共用 `channels.next_seq` |
| `src/server/adapters/http/execution.rs:1677` | Agent `channel read`（返回全部 root+reply） |
| `src/computer/drivers/builtin_agent.rs:183` | builtin `steer()` 返回 Unsupported |
| `src/computer/application/run.rs:57-62` | Completed + 未处理 item → Failed，error_code=None |
| `src/computer/application/run.rs:303-306` | attach 新 item，steer Unsupported → delivery unsupported |
| `src/server/adapters/postgres/execution.rs:234` | `run_result_events` 只存 event_id/run_id/created_at |

### 8.3 9 个 failed Run ID

```text
019fe6f6-7a19-7081-8516-48aedd32ac9a  Iris
019fe6f7-d254-7030-8bcf-3743cb00669c  Iris
019fe6f8-14bc-73d3-aa91-af01eb664d56  Leo
019fe6f8-91bc-7592-b67c-e56e225ac224  Iris
019fe6f9-262c-7523-b23e-3c4d05b89ff1  Iris
019fe6f9-ed64-7f43-a422-1965646c3a50  Nora
019fe6fa-9561-7e93-ac9e-5c920471651b  Leo
019fe6fa-b0ba-78b0-b619-f8325377f808  Iris
019fe6fb-8f62-7753-90b7-9ddd963a1247  Nora
```
