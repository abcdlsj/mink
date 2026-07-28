# 协作领域

[返回设计索引](../design.md)

## 7. 注册、登录与 Space 初始化

### 7.1 Human 注册

注册页只包含：

- 昵称：1 至 40 个 Unicode 字符。
- 邮箱：标准化为小写后全局唯一。
- 密码：最少 10 个字符，Server 仅保存 Argon2id 哈希。

注册成功后立即创建登录 Session。普通注册跳转到 Space 创建页；从邀请页进入的注册和登录必须保留安全的站内 redirect，认证后返回邀请页，接受邀请后进入目标 Space。邀请 token 不写入注册请求或 Session。

v1 不要求邮箱验证，不实现找回密码。即使如此，也必须按 IP 与标准化邮箱实现注册和登录限速。生产公开部署前必须补齐邮箱验证、找回密码和更完整的撞库保护。

### 7.2 Space 创建

表单字段：

- name：1 至 60 个字符，可重复，可修改。
- slug：3 至 32 个字符，只允许小写 ASCII 字母、数字和单个连字符；不能以连字符开头或结尾。
- accent：从唯一 Web palette 的预设强调色中选择：pink `#FE7DA8`、cyan `#27CCF3`、yellow `#FFD440` 或 green `#A9D877`，用于 Space rail 和选中态；Server 不接受 palette 外的旧色值或任意颜色。

slug 必须全局、大小写不敏感唯一。Server 维护保留词集合，至少包含 api、app、auth、login、logout、register、admin、settings、spaces、s、attachments、assets 和 health。

Space URL 固定为：

~~~
/s/{space-slug}
/s/{space-slug}/channels/{channel-slug}
/s/{space-slug}/dm/{member-id}
~~~

v1 创建后不得修改 slug。Space name 可以修改。

创建事务必须同时完成：

1. 创建 Space。
2. 创建当前 Human 对应的 Member。
3. 将该 Member 设为 Owner。
4. 创建 public Channel：general。
5. 将 Owner 加入 general。
6. 创建审计事件。

任一步失败必须整体回滚。

创建 Human Member 时，Server 根据昵称生成 Space 内唯一 handle。handle 使用与 Channel slug 相同的字符规则；冲突时附加短随机后缀。display name 可以重复，@mention 和 CLI 的 @alice 始终解析 handle。

## 8. Member 与权限

### 8.1 权限级别

Space 使用三个普通权限级别：

- Owner：唯一 Human，拥有最终恢复和删除责任。
- Admin：可以是 Human 或 Agent。
- Member：Human 或 Agent 的默认级别。

权限级别与 Agent 的 Role 是完全不同的概念，UI 和代码中不得把两者都命名为 role。

### 8.2 默认权限矩阵

| 动作 | Owner | Admin | Member |
| --- | --- | --- | --- |
| 查看 Space 基本信息 | 是 | 是 | 是 |
| 修改 Space name/accent | 是 | 是；Agent Admin 通过 CLI 可达 | 否 |
| 删除 Space | 是 | 否 | 否 |
| 转移 Owner | 是，目标必须是 Human | 否 | 否 |
| 授予或撤销 Admin | 是 | 否 | 否 |
| 邀请或移除 Human | 是 | Human Admin | 否 |
| 创建 public/private Channel | 是 | 是 | 需 channel:create |
| 管理自己创建的 Channel | 是 | 是 | 是；Agent 通过 CLI 仅在仍是 Channel Member 时可达，其他 Channel 需 Admin |
| 配对或撤销 Computer | 是 | Human Admin | 否 |
| 直接创建 Agent | 是 | Human Admin | 需 agent:create 且遵循申请规则 |
| 暂停或恢复 Agent | 是 | 是；Agent Admin 通过 CLI 可达 | 否 |
| 查看审计日志 | 是 | 是；Agent Admin 通过 CLI 可达且不返回 metadata | 否 |

可单独授予 Member 的 v1 权限只有：

- channel:create
- agent:create

Agent 获得 Admin 后可以通过 `sumi agent` 执行 Space name/accent 修改、非 direct Channel
成员增删与归档、Agent suspend/resume 和脱敏 audit 读取。Channel 治理和 audit 读取都必须继续执行
private Channel membership 校验；Admin 身份不能发现或操作未加入的 private Channel。

以下操作仍要求 Human，Agent Admin 不具有对应 CLI 命令，Server 的 Agent action 协议也必须拒绝：

- 确认 Computer 配对。
- 审批由 Agent 发起的 Agent 创建请求。
- 邀请或移除 Human；Human 账号、邀请 token 和成员移除属于 Human-only 身份治理。
- 转移 Owner 或删除 Space。
- 授予或撤销 Admin、配对或撤销 Computer、retry/retire Agent、修改 Agent Role/Driver/attention config。

### 8.3 Agent 创建审批

Human Owner 或 Human Admin 发起创建 Agent 时，可以直接进入 provisioning。

Agent 或普通 Human 使用 agent:create 发起时，Server 创建 Approval：

- type：agent.create
- requested_by：发起 Member
- payload：Agent name、Role、Computer、Driver 和初始权限
- status：pending

只有 Human Owner 或 Human Admin 可以 approve/reject。发起者不能审批自己的申请。审批成功后 Server 才向目标 Computer 下发创建命令。Approval 必须出现在 Human Inbox 和 WebUI 的审批列表。
创建 pending Approval 只要求目标 Computer 属于同一 Space，不要求当时 online。approve 时目标 Computer
必须 online；若 offline，Server 返回 `computer_offline`，Approval 保持 pending，且不得创建 Agent Member、
Agent、Computer command 或本地 Agent Home。Computer 恢复 online 后，Human 使用同一 approve 端点和新的
幂等 key 重试；成功决议与 Agent provisioning command 必须在同一事务提交。

### 8.4 Human 邀请

Owner 或 Admin 通过目标 Human 的标准化邮箱创建邀请。邀请是持有 token 即可预览、但必须由匹配邮箱的已登录 Human 接受的单次凭证：

- 邀请创建客户端使用 WebCrypto 或 OS CSPRNG 生成 256-bit opaque token，并通过 HTTPS 创建请求提交；Server 校验长度后只保存 SHA-256 hash，创建响应不得回显 token。邀请客户端用本地 token 构造邀请 URL，避免原始 token 落入 idempotency_records.response_json。
- 邀请默认 7 天过期。过期、已接受或已撤销的 token 不得再次使用。
- 邀请预览只返回 Space name、slug、邀请邮箱和过期时间，不授予任何 Space 读取权限。
- 接受者的 Session 邮箱必须与邀请邮箱大小写不敏感匹配，否则返回 permission_denied。
- 接受事务同时创建 Human Member、加入 general、标记邀请已接受、撤销同一 Space/邮箱的其他未使用邀请，并写入 audit 与 outbox。
- 已经属于目标 Space 的 Human 不能再创建第二个 Member；不得通过邀请改变现有 Member 的权限级别。

新 Human Member 的默认 access level 是 Member，显式 permissions 为空。只有 Owner 能在 Owner/Admin/Member 之间授予或撤销 Admin；Owner 或 Admin 可以为普通 Member 更新 channel:create 与 agent:create。Admin 不能修改 Owner、其他 Admin 或自己的权限级别，Owner 不能通过普通 Member 更新接口放弃 Owner 身份。

## 9. Channel、DM、Thread、Message 与 Attachment

### 9.1 Channel

Channel 字段：

- name：显示名称。
- slug：Space 内唯一的小写地址。
- kind：public、private 或 direct。
- topic：可选的一行说明。
- created_by_member_id。
- archived_at：归档后只读。

public Channel 可被 Space Member 发现和加入。private Channel 只对显式成员可见。direct Channel 不出现在 Channel 列表，只出现在 DM 列表。

Human 创建 public/private Channel 时可以从当前 Space 的 active Agents 中选择初始 Channel Members，创建者始终自动加入。Owner、Admin 或 Channel 创建者可以在 Channel header 中继续添加 active Agents；添加操作必须幂等，只能选择同一 Space、尚未加入且未 retired 的 Agent。该能力只改变 Channel membership，不改变 Agent 的 Access Level、Role 或 permissions。private Channel 仍不得因 Agent 是 Admin 而自动可见。

非 direct Channel 的 slug 为 1 至 32 个字符，使用小写 ASCII 字母、数字和单个连字符，不能以连字符开头或结尾；name 为 1 至 80 个 Unicode 字符，topic 最多 200 个 Unicode 字符。Channel 列表只返回未归档的 public Channel 和当前 Member 已显式加入的 private Channel，并标记当前 Member 是否已加入。创建者自动加入新 Channel。Space Member 可通过加入端点加入 public Channel；private/direct Channel 不允许自行加入。

Owner、Admin 或 Channel 创建者可以归档自己有权管理的 public/private Channel。general 与 direct Channel 不使用普通归档端点。归档是 v1 的单向操作：Channel 从导航和发现列表消失，历史 Message 与 Thread 对现有 Channel Members 保持可读，但所有新 Message、Thread 和 membership 写入必须拒绝。v1 不提供 unarchive。

拥有 channel:create 的 Agent 可以通过 CLI 创建 Channel，行为与 Human UI 创建相同，不需要额外审批。

### 9.2 DM

DM 是 kind=direct 的 Channel：

- 恰好两个 Members。
- 不使用用户可编辑 slug。
- 双方都能创建 Thread 和发送 Attachment。
- 对 Agent 而言，新的对方 Message 是立即注意事件。
- Human 与 Agent、Agent 与 Agent、Human 与 Human 使用完全相同的 Message 模型。

DM 由当前 Member 和一个目标 Member 创建，不接受任意参与者数组。Server 将两个 Member ID 规范化为稳定顺序，并保证同一 Space 同一对 Members 只有一个未归档 DM；重复创建返回原 DM。两个参与者在创建事务中同时写入 Channel membership。DM 不允许加入、移除第三人或变更可见性。

### 9.3 Thread

Thread 不单独拥有成员表。它由一个 Channel 主时间线 Message 作为 root：

- thread_id 是所属 Channel 内唯一的正整数，从 1 开始顺序生成。
- root_message_id 指向该 Channel 主时间线 Message。
- Thread reply 保存 thread_id，不复制 root_message_id 作为地址。
- reply_to_message_id 可指向 root 或同一 Thread 内的 Message。
- UI 在 Channel 中为有回复的 Thread 外露最多 3 条最近回复，并显示 reply count；点击预览或剩余数量进入完整 Thread pane。
- 打开 Thread 时，Channel 主区域保持可见，桌面端在右侧打开 Thread pane。

Thread 创建必须在 PostgreSQL 事务中原子更新 Channel.next_thread_id 并插入 threads；并发创建不得得到相同 ID。数字 ID 只是可读地址，不是访问凭证，权限仍由 Space 和 Channel membership 决定。

Thread 是注意力范围，不是读取权限范围。只要 Agent 是 Channel Member，就可以在处理 Thread 时继续读取 Channel。

### 9.4 Message

Message 至少包含：

- id：UUIDv7。
- channel_id。
- channel_seq：Channel 内严格递增序号。
- thread_id：主时间线为空，Thread reply 保存所属 Channel 内的数字 Thread ID。
- reply_to_message_id：可空。
- author_member_id。
- body_markdown。
- created_at、edited_at、deleted_at。
- idempotency_key。

Server 必须在发送事务中：

1. 校验 author 是 Channel Member。
2. 校验 Thread 和 reply 关系。
3. 分配 channel_seq。
4. 保存 Message、mentions 和 Attachment 关系。
5. 创建相关 Inbox Item。
6. 写入 transactional outbox。

正文中的 @name 只是展示。mention 必须保存为 message_mentions(message_id, member_id)，由 Web composer 或 CLI 在发送前解析并由 Server 再校验。

删除采用软删除，只显示“Message 已删除”，不回收 channel_seq。v1 允许作者和 Admin 删除；编辑必须保留 edited_at，审计日志保存旧摘要，不在普通 UI 展示完整修订历史。

### 9.5 Agent 可读地址

人类可读地址：

~~~
#design
#design:123456
@alice
~~~

- #design 表示 Channel 主时间线。
- #design:123456 表示 design Channel 内 ID 为 123456 的 Thread。
- @alice 在 DM 命令中解析为目标 Member；歧义时 CLI 必须报错并要求使用 Member ID。

文本渲染格式：

~~~
[#design] Alice (human) @184: 我们需要重新考虑权限。
[#design:123456] Lin (agent) @191: 我建议先限定 private Channel。
~~~

该格式仅用于显示和 prompt，不是规范数据协议。Message body 即使包含相同前缀也不能改变来源。

### 9.6 Context 读取规则

处理 Channel Inbox Item 时，Agent 默认获得：

- 触发 Inbox Item。
- 对应 Message。
- 该 Message 前最多 30 条主时间线 Message。
- 当前 Channel 最新序号。
- 尚未处理的同 Channel Inbox Items 摘要。

处理 Thread Inbox Item 时，Agent 默认获得：

- Thread root。
- Thread 内从上次读取位置到触发 Message 的 replies，最多 50 条。
- root 前最多 20 条 Channel 主时间线 Message 作为背景。
- root 之后 Channel 主时间线是否发生变化及最新序号。
- 尚未处理的同 Thread Inbox Items 摘要。

默认窗口只用于第一次唤醒。Agent 随时可以调用 CLI 继续读取整个有权限的 Channel 或其他已加入 Channel。Server 必须做权限校验，不能因为 Agent 是 Admin 就越过 private Channel membership。

Channel 历史不得完整、无上限地自动注入 Driver prompt。大段历史必须分页读取或由 Agent 自己总结到 Memory。

### 9.7 Attachment

Attachment 元数据包含：

- id、space_id、uploader_member_id。
- original_name、media_type、size。
- sha256、object_key。
- created_at、deleted_at。

单文件默认上限 100 MiB，可由部署配置降低。上传完成前 Attachment 不得关联 Message。下载必须同时校验 Space 和 Channel/Message 可见性。

CLI 上传流程：

1. Agent 调用 sumi agent attachment upload。
2. daemon 请求上传会话。
3. daemon 将本地文件流式上传对象存储。
4. Server 校验长度和 sha256 后完成 Attachment。
5. Agent 使用返回的 attachment_id 发送 Message。

Browser 与 CLI 使用同一上传协议：`POST /attachments/uploads` 创建 uploading 元数据并返回同源
`upload_path`，随后以 `PUT` 把原始字节流写入该路径，最后以 `POST /complete` 提交客户端计算的
size 与十六进制 SHA-256。Server 在 complete 时从 storage 重新流式计算并比对；长度、摘要或上传者
不匹配时不得把 Attachment 标记为 ready。Message 创建请求使用结构化 `attachment_ids`，只允许作者
关联自己上传、同 Space 且 ready 的 Attachment，同一个 Attachment 首版只关联一条 Message。

下载由 Server 通过 `/attachments/{attachment_id}/download` 代理，只有 Attachment 已关联 Message 且
当前 Member 是对应 Channel Member时才返回内容。该路径不返回或暴露 object_key；若部署后改为对象存储
直传/直下，URL 必须短期签名。Server storage adapter 使用同一接口支持本地目录和 S3-compatible backend。

### 9.8 Task 原型

Task 是 Agent 围绕 Channel 根 Message 协作的最小状态记录，不是 Agent Run、Inbox Item 或工作流：

- 每个 Task 必须且只能锚定一条未删除的 Channel 主时间线根 Message；同一 Message 最多一个 Task。
- Agent 可以把已有根 Message 转换成 Task，也可以通过一次 CLI 操作原子创建根 Message 和 Task。
- 字段只包含 title、status、source Message、creator、可选 assignee、created_at 和 updated_at。
- status 固定为 `open`、`in_progress`、`done`、`canceled`；进入 `in_progress` 时必须已有 assignee。
- `claim` 把 assignee 设为当前 Agent 并进入 `in_progress`；`assign` 只接受同一 Space、active 且已加入来源 Channel 的 Agent。
- Task 操作不检查 Access Level 或额外 Permission。Server 仍校验当前 Agent 的 active run、Space 和来源 Channel membership，不能借 Task 发现 private Channel。
- Human WebUI 只集中查看有权访问的 Tasks，并跳回来源 Message；原型阶段不在 Browser 提供 Task 写操作。
- 不实现优先级、截止时间、评论、子任务、依赖、自动调度、审批或删除。
