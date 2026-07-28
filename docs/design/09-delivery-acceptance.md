# 交付与验收

[返回设计索引](../design.md)

## 21. 开发顺序

### Phase 1：基础壳层

- Rust Cargo package、单一 sumi binary、Server 和 PostgreSQL migrations。
- WebUI shell 和 Neo-Brutalism tokens。
- Human register/login/session。
- Space 创建、slug、Owner、general。
- Members 基础列表。

完成标准：新 Human 可以注册、创建 Space，并进入空 general。

### Phase 2：协作

- Channel/DM membership。
- Message、mention、Thread。
- SSE 和 outbox。
- Attachment 上传下载。
- Human Inbox。
- Desktop/mobile UI。

完成标准：两个 Humans 可以在 Channel、DM 和 Thread 中可靠交流和传 Attachment。

### Phase 3：Computer

- daemon、SQLite、本地目录。
- 配对、本地 secrets.json、heartbeat、reconnect、Delete Computer 与 daemon 退出。
- Computer WebUI。
- local IPC 和 sumi CLI identity。

完成标准：Human 可以配对 Computer，Server 能可靠显示 online/offline。

### Phase 4：Agent 对话流程

- Agent 创建和本地 provision。
- Role、Memory、Agent lifecycle。
- Driver interface、Codex exec --json 和 Builtin provider/tool loop。
- `sumi agent` context/message/attachment commands。
- Agent DM immediate attention。

完成标准：Human 可以在 DM、Channel 和 Thread 与真实运行的 Agent 对话，Agent 使用 CLI 读取、回复并原子处理 Inbox。

### Phase 5：完整注意力与治理

- ambient Inbox 聚合。
- Thread subscription。
- lease/retry/dead handling。
- context freshness 与 held draft。
- Channel create permission。
- Agent create Approval。
- Agent Admin。

完成标准：群聊普通消息可以被 Agent 自主判断，失败不会丢消息或重复回复。

### Phase 6：完整验收

- audit、rate limit、redaction。
- crash/reconnect/idempotency tests。
- 最终 Playwright desktop/mobile 验收与 screenshots；日常开发不反复运行完整 E2E。
- macOS/Linux 构建、sandbox 与故障恢复验收。

## 22. 必须通过的端到端验收

### 22.1 注册与 Space

1. Human 注册后创建 slug=sumi-lab。
2. /s/sumi-lab 可访问且 general 已存在。
3. 第二个 Space 不能使用大小写不同但等价的 slug。
4. 未登录访问 Space 跳到 login，登录后回到原 URL。

### 22.2 Human 与 Agent 平等协作

1. Human 和 Agent 在 Members 同一列表出现。
2. 两者 Message 使用相同布局，Agent 只有小型标签。
3. Human 可以 mention Agent，Agent 可以 mention Human 或 Agent。
4. Agent 有 channel:create 时可以创建 Channel。
5. Agent 被 Owner 授予 Admin 后，通过真实 Builtin run 与 `sumi agent` 修改 Space name/accent、管理其已加入的非 direct Channel 成员与归档、暂停/恢复 Agent，并读取脱敏 audit。
6. Agent Admin 不能邀请/移除 Human、决议 Approval、变更 Access Level、管理 Computer、retry/retire Agent 或修改 Agent 配置。
7. Agent Admin 不能读取、从 audit 发现或治理自己未加入的 private Channel。

### 22.3 Computer 与 Agent

1. 未配对 daemon 生成短时 URL。
2. 非 Human Admin 不能确认配对。
3. 配对成功后 Computer Token 只保存在本机，Server 只保存 hash，Computer 变 online。
4. daemon 退出或断网后 Computer 只变 offline；使用同一 Token 重连后恢复 online，不重新 Pair。
5. Human 创建 Agent 后，目录只出现在目标 Computer。
6. Computer offline 时 Agent Inbox 累积，不在 Server 上执行 Driver。
7. Computer 删除后旧 Token 不能重连，在线或再次启动的 daemon 必须退出。
8. Run 在 WebSocket 断线期间完成后，daemon 重连并补报结果，Server 只应用一次。
9. Computer 永久离线后，ownership lease 过期，Server 终结 Run 并释放 Inbox。
10. command ACK 后等待本地执行槽的 Run 保持 queued 或 starting；Driver 启动后才进入 running。

### 22.4 DM 注意力

1. Human 给 Agent 发 DM。
2. Server 创建一个 hard Inbox Item。
3. daemon 收到通知并启动 Agent 当前 Driver；Builtin 必须作为 v1 验收 Driver 跑通。
4. Agent 通过 sumi agent inbox current 和 sumi agent channel read 获取内容。
5. Agent 使用 sumi agent message send --handle 回复。
6. Message 与 Inbox handled 在同一事务完成。

### 22.5 Channel ambient 注意力

1. Human 在 Agent 所在 Channel 连续发送 5 条普通 Message，不 mention。
2. Server 聚合为一个 ambient Inbox Item。
3. debounce 到期后 daemon 只启动一次 Agent run。
4. Agent 可以选择 ack 并保持沉默。
5. ack 后这些 Message 不再次唤醒 Agent。

### 22.6 Channel 与 Thread 上下文

1. Human 在 Channel mention Agent。
2. Human 在该 Message Thread 中再次 mention Agent。
3. Agent Inbox 能区分 #channel 与 #channel:123456。
4. Thread read 返回 root、Thread replies、Channel 背景和 snapshot seq。
5. Agent 可以继续读取同一 Channel 的更多历史。
6. Agent 尝试读取未加入 private Channel 时得到 permission_denied。

### 22.7 上下文过期

1. Agent 在 seq=10 读取 Thread 并开始组织回复。
2. Human 发送 seq=11。
3. Agent 使用 --based-on 10 发送时得到 context_changed，且没有创建 Message。
4. Agent 读取 seq=11 后重新发送成功。

### 22.8 崩溃与幂等

1. Driver 在发送前崩溃，lease 到期后 Item 回到 pending。
2. Driver 发送并 handle 后 daemon 立即崩溃。
3. 重启后不得重复发送。
4. 连续失败达到 max retries 后 Item 变 dead，并通知 Owner/Admin。

### 22.9 Agent 创建审批

1. 有 agent:create 的 Agent 请求创建另一个 Agent。
2. Server 只创建 pending Approval，不创建 Agent Home。
3. Agent Admin 不能审批该请求。
4. Human Admin 审批后目标 Computer 执行 provision。
5. reject 时不得留下 Agent Member 或本地目录。

### 22.10 UI

以下场景在最终验收时由 Playwright 验证；日常开发优先使用前端单元测试、组件测试和手工浏览器 smoke check，不得在每次 UI 修改后运行完整 Playwright：

- 1440x900、1024x768、390x844。
- Channel 无消息、有长消息、有 Attachment、有 Thread、多人在线。
- Thread desktop 右侧并列，mobile 全屏返回。
- 超长 Channel/Member/Space 名称不遮挡按钮。
- Neo-Brutalism 硬边框和 pixel avatars 正常渲染。
- 页面不存在参考产品的品牌名称、专属图标、文案或 Files 等范围外结构；Tasks 只使用 Sumi 自己的轻量模型。允许并要求视觉 palette、排版密度和交互手感贴近 `reference_style.md`。
- 键盘 focus、reduced motion 和基本 screen reader labels。

## 23. 实现纪律

- 完成一个端到端用户流程并通过测试后，再增加页面。
- 不因“以后可能需要”提前引入消息队列、微服务、向量数据库或工作流引擎。
- 不把 Codex 字段放进 Agent、Message、Inbox 的通用 schema。
- 不把 Driver stdout 当 Message。
- 不把 Channel 全历史塞进 prompt。
- 不用文本前缀代替结构化协议。
- 不把 Human/Agent 分成两套协作 API。
- 不用 Admin 身份绕过 private Channel membership。
- 不声称模型注意力可以被绝对保证；只承诺可靠投递、显式处理和可恢复。
- 测试必须验证产品行为、事务不变量、安全边界或自有协议逻辑；不得为 serde derive、简单 getter 或依赖库已保证的机械行为保留测试。
- helper 名称描述业务效果，例如 publish、require、claim；不得用含糊的 insert_event、process_data 等名称隐藏职责。

当实现与本文冲突时，必须先修改设计并说明原因；不得在代码中偷偷引入第二套领域语言。
