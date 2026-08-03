# WebUI 设计

[返回设计索引](../design.md)

本文件是 WebUI 行为与视觉的唯一规范。

## 1. 产品重心

Sumi 仍以对话为主。Human 的默认入口、主要工作区和最高频操作都是 Channel、DM、Thread 和 Message。

Task 是核心领域对象，但不主导整体信息架构。现有侧边栏中的 Tasks 入口保持原位置。

Channel 不增加 Tasks tab。Composer 不增加`As Task`。全局 shell 不为 Task 新增导航层或固定详情栏。

现有 Neo-Brutalism 视觉、像素头像、栏宽、硬边框、硬阴影和响应式结构继续使用。本次重建只调整 Task、Run 和 Session 相关的信息与交互。

Space 创建时从 4 个预置 accent（`#FE7DA8`、`#27CCF3`、`#FFD440`、`#A9D877`）中选择一个作为该 Space 的主题色；服务端按白名单校验，`--space-accent` 取所选值，不接收其他颜色。

## 2. 视觉规则

| Token | Value | 用途 |
| --- | --- | --- |
| `ink` | `#141111` | 文字、2px 边框和硬阴影 |
| `paper` | `#FFFFFF` | 主内容和控件背景 |
| `panel` | `#FFFAEF` | 会话导航和次级背景 |
| `rail` | `#FFD440` | Space rail 和 soft signal |
| `accent` | `#FE7DA8` | 当前选择、主要动作和 focus |
| `accent-soft` | `#FFE0EA` | hover 和轻量选择 |
| `cyan` | `#27CCF3` | Attachment、Computer 和 Session |
| `green` | `#A9D877` | online 和 Done |
| `orange` | `#F8A16F` | In Review 和 warning |
| `red` | `#F97264` | error 和 destructive |
| `stone` | `#C0B9B1` | offline、disabled 和 Closed |

- 主分隔线和控件边框使用 2px ink。
- 控件圆角为 0 至 4px。
- 主要动作和选中项使用硬偏移阴影。
- 不使用渐变、玻璃、模糊或柔和投影。
- 字体使用 Space Grotesk、Noto Sans SC 和 sans-serif fallback。代码与等宽元数据使用 Space Mono；正文内联代码使用浅色背景。
- Human fallback avatar 使用姓名首字符和稳定背景色。
- Agent 使用由 member ID 生成的 8×8 对称像素印章，并显示`AGENT`标签。
- Human 和 Agent 的 Message 视觉层级相同。

字体层级固定为：

| 层级 | 字号 | 默认字重 | 用途 |
| --- | --- | --- | --- |
| 页面标题 | `18px` | `700` | 页面和实体名称 |
| 区域标题 | `11px` | `700` | 区域名称；使用大写和`0.08em`字距 |
| 正文 | `14px` | `400` | 字段值和表单内容 |
| Message 正文 | `13px` | `400` | Channel 和 Thread 中的普通 Message 正文 |
| 辅助信息 | `12px` | `400` | 说明、空态和系统信息 |
| 元数据 | `10px` | `600` | 时间、display name、计数和状态元数据 |

系统信息统一使用辅助信息字号。普通系统信息使用`muted`颜色和`400`字重；警告使用`orange`相关背景和`600`字重；错误使用`red`相关背景和`600`字重。页面不得通过放大空态文字表达重要性。

## 3. 现有页面结构

```text
+------+------------------+--------------------------------+-------------------+
|Space | Conversation nav | Channel                        | Thread            |
|rail  | Inbox            | header + Member strip          | root + replies    |
|Tools | Channels         | Message timeline               |                   |
|      | DMs              | composer                       | reply composer    |
+------+------------------+--------------------------------+-------------------+
```

- Conversation 继续是默认页面。
- Thread 只在打开时占用右侧 pane。
- Tasks 使用现有侧边栏入口进入独立列表页。
- 点击 Task 来源返回 Channel 并聚焦 Root Message。
- 点击 Message 上的 Task 标识进入 Task 详情，不改变 Channel 布局。

稳定尺寸：

- Space rail 保持当前宽度和入口顺序。
- Navigation 默认 294px，可折叠或在窄屏变为抽屉。
- Channel 最小宽度 480px。
- Thread pane 默认 360px，最大 480px；未打开时不保留空栏。
- Header 为 62px。
- 所有 rail 入口页面（Inbox、Tasks、Members、Computers、Agent 详情）共用同一条页面头基线：62px 高度、18px 页面标题、12px 辅助文字；标题块为`页面标题 + 辅助文字`，不显示 kicker 或计数盒子。
- Composer 最小 88px，增长到 240px 后内部滚动。

## 4. 从对话创建 Task

只有 Root Message 显示`Create Task`动作。点击后，Browser 立即调用统一 Task 创建命令：

1. Server 从 Root Message 推导 Source Thread。
2. Server 直接创建带`source_thread_id`的 Task。
3. UI 把创建动作替换为 Task 标识。
4. Human 可以在轻量 popover 或 Task 详情中编辑 title 和 assignee。

UI 不显示 bind 步骤、Source Thread 选择器或来源确认。Thread reply 不显示创建动作；它所在 Thread 的 root 才是合法来源。

Human 创建的 Task 初始为`TODO`。Agent 在当前 Run 中创建 Task 时，因为 Server 同时绑定当前 Agent 和 Run，初始状态为`In Progress`。

每条 Message 在 hover 或键盘 focus 时，右上角显示 Message 动作面板。面板固定包含`Reply to thread`和 Task 动作两个 icon button。图标和控件高度使用元数据层级，与日期分隔文案保持同一视觉尺度。面板贴齐 Message 行的上边和右边，不能占用正文右侧的独立栏位。只有未绑定 Task 的 Root Message 启用 Task 动作；reply 上的 Task 动作保持禁用，并说明 Task 只能从 Root Message 创建；已经绑定 Task 时该动作保持禁用。动作面板不得重复显示文字按钮。

普通 Message 正文超过 8 个视觉行时默认折叠到 8 行，并显示`Show more`。展开后显示`Show less`。Browser 必须按实际排版高度判断是否超过 8 行，不能按字符数量推断。

Message 投递或 attention 失败使用与日期分隔相同的居中系统信息行。错误文字使用低饱和红色和元数据字号，不使用红色面板、粗边框或错误图标。一个 Message 只有一项失败时直接显示失败事实；有多项失败时合并为一行数量摘要，默认收起，点击后展开每项失败。错误代码只在展开内容或单项失败中显示。

## 5. Message 投影

Message 正文支持常见 Markdown：

- Browser 在 Markdown 分块前把正文中的字面量 `\n`（反斜杠加 `n`）转换为换行，以兼容以转义文本提交的多行内容。
- 行内：`**bold**`、`*italic*`、`_italic_`、`~~deleted~~`、`` `inline code` ``、`[label](url)`。链接只允许 http/https 与站内路径，其他协议保持普通正文。inline code 内不解析 mention 高亮。
- 块级：`#`至`######`标题、无序列表（`-`/`*`）、有序列表（`1.`）、引用（`>`）、围栏代码块（```` ``` ````）和分隔线（`---`）。
- 未闭合的反引号保持普通正文；普通 Message 正文使用 13px、1.4 行高，消息内标题使用 14 至 16px、700 字重，块间距压缩为 0.25em。

普通 Message 正文中的 `@display_name` 只有在 Message 返回的结构化 mention 成员 ID 能映射到当前可见成员 display name 时才使用高亮。`!<seq>`只有在 Message 返回的结构化 task refs 包含该序号时才渲染为 Task 引用。Browser 不得仅根据正文中的符号推断 mention 或 Task 引用；未被 Server 识别的文本保持普通正文样式。Browser 不显示 member handle，成员身份只以 display name 呈现。

结构化 mention 指向 Agent 时，`@display_name` 渲染为站内链接，点击进入该 Agent 的管理页；Human mention 继续使用高亮文本。链接只使用当前 Space 中可见且由 Server 识别的 Agent。

### 5.1 Task 标识

Root Message 上的 Task 标识保持紧凑，只显示：

- `!<seq>`引用和 Task title。
- `TODO|In Progress|In Review|Done|Closed`状态。
- assignee 或`Unassigned`。
- 当前 Run 在其他 Linked Thread 时显示`Working elsewhere`。

Task 标识与 AGENT 标识同一尺度：10px 元数据字号、单行、状态图形加短文字，不带大型卡片或 Tooltip。标识固定在 Message 行右上角，点击回跳 Task 详情页。只有 Root Message 显示 Task 标识；reply 即使属于绑定 Task 的 Thread 也不显示。Hover 出现的 Message 动作面板必须避开该标识。Thread pane 中的 root 使用同一标识。

Thread pane 中的 root 使用同一 Task 标识。Related Thread 的 header 显示 Task title 和`Related`关系。

正文中的 Task Reference 渲染为链接：文字保持`!<seq>`，点击回跳 Task 详情页，视觉与 mention 同尺度但使用 Task 状态色。引用只来自 Message 返回的结构化 task refs，不得从正文推断。

Task 状态标签固定为：

| 状态 | 标签 | 视觉 |
| --- | --- | --- |
| `todo` | TODO | paper 背景、ink 边框 |
| `in_progress` | IN PROGRESS | accent-soft 背景、running 图形 |
| `in_review` | IN REVIEW | orange 背景、review 图形 |
| `done` | DONE | green 背景、check 图形 |
| `closed` | CLOSED | stone 背景、close 图形 |

状态必须同时显示文字和图形。Working、Waiting 和 Failed 使用独立 Run status。

### 5.2 Action Message

Action Message 使用紧凑的结构化行，不显示为普通 Markdown 气泡。首批 UI 包含：

- `channel_created`：显示 actor、`Created channel`、Channel 名称和可打开链接。
- `agent_created`：显示 actor、`Created agent`、Agent 头像、名称、生命周期和详情链接。

Action Message 从目标资源读取当前名称。目标已退役或删除时保留 Message，并显示不可用占位。

Action Message 不显示原始 JSON、命令参数或内部 ID。普通 Message composer 不能选择 action kind。

Channel 成员加入或离开时，Server 在该 Channel 时间线写入 `system_notice`。Browser 将单条 System Notice 渲染为居中的系统信息行。该行只显示正文，不显示分割线、图标、作者、回复或 Task 操作。

同一日期内连续出现多条 System Notice 时，Browser 将其合并为一组并默认折叠。折叠行显示组内数量，并允许展开或收起全部正文。普通 Message 或日期变化会结束当前分组。

SSE 的 `message.created` 事件按普通 Channel 活动设置导航未读红点。

### 5.3 Agent 启动失败提示

来源 Message 存在 Agent attention 错误时，Message 下方显示 inline notice。提示必须包含目标 Agent、稳定错误码和自动重试状态，不显示数据库错误、Message 正文副本或凭据。错误清除后，提示随 Message 投影刷新而消失。

该提示是运行状态，不显示作者、时间或 Message 序号，也不计入 Thread reply 数量。

## 6. Tasks 页面

Tasks 页面是对话的辅助入口，不是 Kanban 或项目管理系统。页面使用现有 list/detail 布局。

筛选固定为：

- All open：TODO、In Progress 和 In Review。
- TODO。
- In Progress。
- In Review。
- Done。
- Closed。
- Assigned to me。

默认先显示 In Review，再显示 In Progress 和 TODO；各组按`updated_at`倒序。Done 和 Closed 默认折叠到历史筛选。

Task 列表项显示 title、status、assignee、Source Channel 和更新时间。Task 详情显示：

1. title、status 和 assignee。
2. Source Thread。
3. Related Threads。
4. Result Message；只在 Done 时出现。
5. Close reason；只在 Closed 时出现。
6. current Run 和 Focus。
7. 最近 Run outcomes。
8. Session continuity 诊断。

Session continuity 位于详情底部，使用`Warm|Cold|Reset required|Unavailable`。它只说明下次执行效率，不表示 Task 事实是否完整。

## 7. 状态操作

```text
TODO -> In Progress -> In Review -> Done
                     \-----------> Done

TODO / In Progress / In Review -> Closed
```

- assignee 开始工作时进入 In Progress。
- assignee 提交复核时进入 In Review。
- 不需要复核时可以从 In Progress 直接进入 Done。
- In Review 可以由 assignee 之外、能读取 Task 的 Human 或 Agent 确认 Done 或退回 In Progress。
- Closed 需要选择 Invalid、Duplicate、Not needed、Obsolete 或 Other，并可填写说明。

`Working`、`Waiting`和`Failed`是 Run 或 attention 状态，不能出现在 Task 状态选择器中。

## 8. Agent 状态

Agent 状态继续显示在现有 avatar、DM 行、Members 和 Agent detail 中。Activity 只呈现可验证事实：

- 正在处理哪个 Task 和 Focus。
- 正在处理未建 Task 的普通 Thread。
- Run 正在 dispatched 或 working。
- 当前 Run 已 yield 并等待外部输入。
- 另一个 Focus 有 pending hard Item。
- 承载该 Agent 的 Computer 当前是否可达。该事实与 Run 状态分开展示：Computer 联系不上时，正在进行的 Run 仍显示为 working，见 [Agent Run](04-agent-run.md)。

UI 不得展示隐藏推理、Provider transcript、完整命令参数或 Message 正文日志。

不同 Focus Item 到达时，当前页面显示`Another item is waiting`。该 Item 保持 pending，不能伪装成当前 Task 或 Focus 的新内容。

## 9. Inbox

Human Inbox 按来源对象聚合为 DM 和 Thread 两组。DM 组以`space_id + channel_id`为键，Thread 组以`space_id + thread_id`为键；同一组只显示最新来源 Message 的预览和时间，并显示组内待处理 Item 数。Inbox 不是 Message 历史。

Human Inbox 只接收与自己相关的 hard Item：DM、mention、reply、Linked Thread 活动和已订阅 Thread 的更新。普通 Channel Message 不进入 Human Inbox；`channel_activity`和执行错误`system`只属于 Agent。

打开一个聚合行时，Browser 为该行的全部 Item 调用`read`端点，再打开 DM 或 Thread 来源；其中任一 Item 已处理时，重复读取必须幂等，见 [API 与事件](07-api.md)。聚合行不显示完成或延后控件：Human 打开来源就是已读，Agent Item 的终态仍由处理它的 Run 决定，见 [Inbox 与凭据](06-inbox-credentials.md)。

来源 Message 可见时，聚合行在发送者和来源地址下显示最新 Message 的有长度上限正文预览。预览按一行视觉高度显示并在超出容器时省略；点击聚合行仍打开来源以读取完整 Thread 或 DM。

Agent Inbox 默认不向普通 Member 公开。Owner/Admin 只能读取自己有权访问的来源摘要和错误代码。

Agent 详情显示 action permissions。Human Owner/Admin 可以逐项授予或撤销`channel.create`和`agent.create`。UI 不提供 Role 形式的 Permission 套餐。

## 9.1 Members、Computers 与 Agent

Computer 与 Agent 详情沿用三栏 shell 和同一条标题基线。详情主体使用扁平分区和紧凑字段网格：分区不绘制卡片边框、底色或阴影，以留白和 2px ink 分隔线组织；区域标题使用 ink 色大写，事实数字使用大号粗体，状态只使用图形信号加短标签，不带边框。页面头与 Inbox/Channel 共用 62px 基线，标题、辅助文字、字段标签和字段值分别使用页面标题、辅助信息、元数据和正文层级，并在同一行按 baseline 对齐。

- 生命周期、运行和连接状态使用图形信号加短标签。信号必须带 `aria-label` 与 `title`，颜色不能是唯一线索；状态不使用占用整行的彩色卡片。
- 标题、辅助文本、字段标签和字段值分别使用页面标题、辅助信息、元数据和正文层级，并在同一行按 baseline 对齐。字段网格在 1024px 以下收为单列，在 390px 仍保持可读间距。
- Agent action permissions 直接显示为 checkbox 列表。每行只显示 action code 和一条最短说明；Owner/Admin 可以逐项勾选或取消，普通 Member 看到禁用状态。Permission 不显示 Role 套餐或重复解释。
- Computer Hosted Agents 列表和 Agent Runtime 区域共用状态信号、字段间距和按钮高度。三栏分布、Computer/Agent 路由及 API 行为保持不变。
- Members 主页按 Agents、Humans 分组显示扁平成员行；不显示表头行、不使用斑马纹，行间以细分割线分隔。每行只显示身份、Access Level（Member/Admin 可直接设置）和消息动作，不展示 action permission；权限逐项管理在 Agent 详情。Members 页头部提供 kind 筛选（All、Human、Agent）以及 Invite Human 与 Create Agent 操作，行内容不因筛选和头部操作而增加。
- `/s/:spaceSlug/agents` 是 Agent 目录入口，显示 Agent 数量、Working、在线 Computer 和需要处理的错误摘要；列表复用 Agent 成员行并提供进入单个 Agent 管理页的链接。
- Agent 详情内容区与页面头、标签页共用左缘基线，不居中。详情内字体层级固定为：分区标题 13px 大写、字段值 16px/600、字段标签 10px 大写，页面标题保持 18px。
- Agent 详情 Overview 不重复显示 Role（页面头已显示）和 Role revision 等内部计数。Computer 字段链接到对应 Computer 详情。
- Agent 详情的消息按钮点击后自动创建或复用与目标 Agent 的 DM，再跳转到该 DM，不在中间显示空对话。
- Owner/Admin 在 Agent 详情 Overview 的 `Agent DMs` 分区查看该 Agent 与其他 Agent 的 DM 元数据；该分区只显示对端 Agent 和创建时间，不提供正文或普通 DMs 导航入口。
- Agent 详情提供独立的 Activity tab，并默认选中该 tab。面板展示该 Agent 自身的写交互 feed：`message.send`、`task.create`、`task.update`、`task.link_thread`、`task.unlink_thread`、`task.submit_review`、`task.done`、`task.close`、`channel.create`、`agent.create`、`inbox.ack`、`inbox.defer` 和 `run.yield`。
- Activity 每条记录显示 action kind、允许的资源 ID 参数、发生时间和目标资源链接。面板不显示 Message 正文、Task Result、Memory、workspace 文件、Provider transcript 或隐藏推理；参数只来自 `agent.activity` payload 的资源 ID 字段，见[安全与运维](09-security-operations.md)。Activity 不是 Run activity 状态：后者继续由 header 状态信号表达。
- Activity 是临时视图，不提供历史查询。Browser 通过 SSE 接收 `agent.activity` 事件并把当前 feed 保存在页面内，见[API 与事件](07-api.md)。重连或刷新只恢复保留窗口内的记录；页面内超过上限时丢弃最旧记录。Activity 列表使用独立的纵向滚动容器。
- Activity 空态只写事实：该 Agent 尚无可见交互。窄屏下 Activity 内容收为单列。
- Owner/Admin 打开 `/computers` 且未选择 Computer 时，中间显示新增 Computer onboarding，左侧保留已配对列表；点击 Computer 行后才进入详情。`pair-computer` hash 兼容现有入口并显示同一 onboarding，不再叠加重复 modal。无配对 Computer 时 onboarding 仍是 Owner/Admin 的主内容。普通 Member 不显示新增入口或配对命令。
- Computers 左侧导航只提供已配对列表和 `+` 配对入口，不显示重复的 Add Computer 项。

## 10. 通用组件

- Button 保留 Primary、Secondary、Quiet 和 Destructive 四类。
- Entity row 使用扁平列表，不增加卡片层级。
- 持久阻断使用 inline notice，短期结果使用 toast。
- 空态只写事实和至多一个下一步动作，不使用大插画。
- 空态和`No DMs yet`等系统信息使用辅助信息字号。
- Composer 只包含 Markdown、Attachment、mention 和 Send，不包含 Task 或 Session 控制。DM 的 Composer 不提供 mention，因为 DM 恰好只有两个 Member。Channel 与 Thread 复用同一 Composer 结构和高度；Composer 外框是唯一输入边框，Attachment 位于左下角，Send 位于右下角，宽度随所在区域变化。

## 11. 响应式与无障碍

- 1100px 及以上保持 Space rail、Navigation、Channel 和可选 Thread pane。
- 700 至 1099px 将 Navigation 变为抽屉，Thread 覆盖 Channel。
- 低于 700px 使用单列，Thread 和 Task 详情为全屏路由。
- 返回 Channel 时恢复滚动位置、打开的 Thread 和 Composer draft。
- 所有操作支持键盘，focus 可见。
- 状态使用文字和图形，颜色不是唯一线索。
- icon button 具有 accessible name。
- 动画遵循`prefers-reduced-motion`。
