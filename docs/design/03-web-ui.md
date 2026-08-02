# WebUI 设计

[返回设计索引](../design.md)

本文件是WebUI行为与视觉的唯一规范。

## 1. 产品重心

Sumi仍以对话为主。Human的默认入口、主要工作区和最高频操作都是Channel、DM、Thread和Message。

Task 是核心领域对象，但不主导整体信息架构。现有侧边栏中的 Tasks 入口保持原位置。

Channel 不增加 Tasks tab。Composer 不增加`As Task`。全局 shell 不为 Task 新增导航层或固定详情栏。

现有Neo-Brutalism视觉、像素头像、栏宽、硬边框、硬阴影和响应式结构继续使用。本次重建只调整Task、Run和Session相关的信息与交互。

Space 创建时从 4 个预置 accent（`#FE7DA8`、`#27CCF3`、`#FFD440`、`#A9D877`）中选择一个作为该 Space 的主题色；服务端按白名单校验，`--space-accent` 取所选值，不接收其他颜色。

## 2. 视觉规则

| Token | Value | 用途 |
| --- | --- | --- |
| `ink` | `#141111` | 文字、2px边框和硬阴影 |
| `paper` | `#FFFFFF` | 主内容和控件背景 |
| `panel` | `#FFFAEF` | 会话导航和次级背景 |
| `rail` | `#FFD440` | Space rail和soft signal |
| `accent` | `#FE7DA8` | 当前选择、主要动作和focus |
| `accent-soft` | `#FFE0EA` | hover和轻量选择 |
| `cyan` | `#27CCF3` | Attachment、Computer和Session |
| `green` | `#A9D877` | online和Done |
| `orange` | `#F8A16F` | In Review和warning |
| `red` | `#F97264` | error和destructive |
| `stone` | `#C0B9B1` | offline、disabled和Closed |

- 主分隔线和控件边框使用2px ink。
- 控件圆角为0至4px。
- 主要动作和选中项使用硬偏移阴影。
- 不使用渐变、玻璃、模糊或柔和投影。
- 字体使用Space Grotesk、Noto Sans SC和sans-serif fallback。代码与等宽元数据使用Space Mono；正文内联代码使用浅色背景。
- Human fallback avatar使用姓名首字符和稳定背景色。
- Agent使用由member ID生成的8×8对称像素印章，并显示`AGENT`标签。
- Human和Agent的Message视觉层级相同。

字体层级固定为：

| 层级 | 字号 | 默认字重 | 用途 |
| --- | --- | --- | --- |
| 页面标题 | `18px` | `700` | 页面和实体名称 |
| 区域标题 | `11px` | `700` | 区域名称；使用大写和`0.08em`字距 |
| 正文 | `14px` | `400` | Message正文、字段值和表单内容 |
| 辅助信息 | `12px` | `400` | 说明、空态和系统信息 |
| 元数据 | `10px` | `600` | 时间、handle、计数和状态元数据 |

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

- Conversation继续是默认页面。
- Thread只在打开时占用右侧pane。
- Tasks使用现有侧边栏入口进入独立列表页。
- 点击Task来源返回Channel并聚焦Root Message。
- 点击Message上的Task标识进入Task详情，不改变Channel布局。

稳定尺寸：

- Space rail保持当前宽度和入口顺序。
- Navigation默认294px，可折叠或在窄屏变为抽屉。
- Channel最小宽度480px。
- Thread pane默认360px，最大480px；未打开时不保留空栏。
- Header为62px。
- 所有rail入口页面（Inbox、Tasks、Members、Computers、Agent详情）共用同一条页面头基线：62px高度、18px页面标题、12px辅助文字；标题块为`页面标题 + 辅助文字`，不显示kicker或计数盒子。
- Composer最小88px，增长到240px后内部滚动。

## 4. 从对话创建Task

只有Root Message显示`Create Task`动作。点击后，Browser立即调用统一Task创建命令：

1. Server从Root Message推导Source Thread。
2. Server直接创建带`source_thread_id`的Task。
3. UI把创建动作替换为Task标识。
4. Human可以在轻量popover或Task详情中编辑title和assignee。

UI不显示bind步骤、Source Thread选择器或来源确认。Thread reply不显示创建动作；它所在Thread的root才是合法来源。

Human创建的Task初始为`TODO`。Agent在当前Run中创建Task时，因为Server同时绑定当前Agent和Run，初始状态为`In Progress`。

每条Message在hover或键盘focus时，右上角显示Message动作面板。面板固定包含`Reply to thread`和Task动作两个icon button。图标和控件高度使用元数据层级，与日期分隔文案保持同一视觉尺度。面板贴齐Message行的上边和右边，不能占用正文右侧的独立栏位。只有未绑定Task的Root Message启用Task动作；reply上的Task动作保持禁用，并说明Task只能从Root Message创建；已经绑定Task时该动作保持禁用。动作面板不得重复显示文字按钮。

普通Message正文超过8个视觉行时默认折叠到8行，并显示`Show more`。展开后显示`Show less`。Browser必须按实际排版高度判断是否超过8行，不能按字符数量推断。

Message投递或attention失败使用与日期分隔相同的居中系统信息行。错误文字使用低饱和红色和元数据字号，不使用红色面板、粗边框或错误图标。一个Message只有一项失败时直接显示失败事实；有多项失败时合并为一行数量摘要，默认收起，点击后展开每项失败。错误代码只在展开内容或单项失败中显示。

## 5. Message 投影

Message 正文支持 Markdown inline code：反引号包裹的片段渲染为 `code` 元素，使用Space Mono与浅色背景；inline code 内不解析 mention 高亮。未闭合的反引号保持普通正文。

普通 Message 正文中的 `@handle` 只有在 Message 返回的结构化 mention 成员 ID 能映射到当前可见成员 handle 时才使用高亮。Browser 不得仅根据正文中的 `@` 文本推断 mention；未被 Server 识别的文本保持普通正文样式。

### 5.1 Task 标识

Root Message上的Task标识保持紧凑，只显示：

- Task title。
- `TODO|In Progress|In Review|Done|Closed`状态。
- assignee或`Unassigned`。
- 当前Run在其他Linked Thread时显示`Working elsewhere`。

Task标识不得把Message变成大型卡片。Hover、focus或点击后才显示Source、Linked Threads和最近执行摘要。

Thread pane中的root使用同一Task标识。Related Thread的header显示Task title和`Related`关系。

Task状态标签固定为：

| 状态 | 标签 | 视觉 |
| --- | --- | --- |
| `todo` | TODO | paper背景、ink边框 |
| `in_progress` | IN PROGRESS | accent-soft背景、running图形 |
| `in_review` | IN REVIEW | orange背景、review图形 |
| `done` | DONE | green背景、check图形 |
| `closed` | CLOSED | stone背景、close图形 |

状态必须同时显示文字和图形。Running、Waiting和Failed使用独立Run status。

### 5.2 Action Message

Action Message 使用紧凑的结构化行，不显示为普通 Markdown 气泡。首批 UI 包含：

- `channel_created`：显示 actor、`Created channel`、Channel 名称和可打开链接。
- `agent_created`：显示 actor、`Created agent`、Agent 头像、名称、生命周期和详情链接。

Action Message 从目标资源读取当前名称。目标已退役或删除时保留 Message，并显示不可用占位。

Action Message 不显示原始 JSON、命令参数或内部 ID。普通 Message composer 不能选择 action kind。

### 5.3 Agent 启动失败提示

来源Message存在Agent attention错误时，Message下方显示inline notice。提示必须包含目标Agent、稳定错误码和自动重试状态，不显示数据库错误、Message正文副本或凭据。错误清除后，提示随Message投影刷新而消失。

该提示是运行状态，不显示作者、时间或Message序号，也不计入Thread reply数量。

## 6. Tasks页面

Tasks页面是对话的辅助入口，不是Kanban或项目管理系统。页面使用现有list/detail布局。

筛选固定为：

- All open：TODO、In Progress和In Review。
- TODO。
- In Progress。
- In Review。
- Done。
- Closed。
- Assigned to me。

默认先显示In Review，再显示In Progress和TODO；各组按`updated_at`倒序。Done和Closed默认折叠到历史筛选。

Task列表项显示title、status、assignee、Source Channel和更新时间。Task详情显示：

1. title、status和assignee。
2. Source Thread。
3. Related Threads。
4. Result Message；只在Done时出现。
5. Close reason；只在Closed时出现。
6. current Run和Focus。
7. 最近Run outcomes。
8. Session continuity诊断。

Session continuity位于详情底部，使用`Warm|Cold|Reset required|Unavailable`。它只说明下次执行效率，不表示Task事实是否完整。

## 7. 状态操作

```text
TODO -> In Progress -> In Review -> Done
                     \-----------> Done

TODO / In Progress / In Review -> Closed
```

- assignee开始工作时进入In Progress。
- assignee提交复核时进入In Review。
- 不需要复核时可以从In Progress直接进入Done。
- In Review 可以由 assignee 之外、能读取 Task 的 Human 或 Agent 确认 Done 或退回 In Progress。
- Closed需要选择Invalid、Duplicate、Not needed、Obsolete或Other，并可填写说明。

`Running`、`Waiting`、`Finalizing`和`Failed`是Run或attention状态，不能出现在Task状态选择器中。

## 8. Agent状态

Agent状态继续显示在现有avatar、DM行、Members和Agent detail中。Activity只呈现可验证事实：

- 正在处理哪个Task和Focus。
- 正在处理未建Task的普通Thread。
- Run正在starting、running、finalizing或stopping。
- 当前Run已yield并等待外部输入。
- 另一个Focus有pending hard Item。

UI不得展示隐藏推理、Provider transcript、完整命令参数或Message正文日志。

不同Focus Item到达时，当前页面显示`Another item is waiting`。该Item保持pending，不能伪装成当前Task或Focus的新内容。

## 9. Inbox

Human Inbox按三组显示：DM与mention、replies与Thread活动、Channel活动与system通知。分组按Item kind划分，不按Task或时间划分。Inbox不是Message历史。

Human Inbox 只接收与自己相关的 Item：DM、mention、reply、Linked Thread 活动和已订阅 Thread 的更新。普通 Channel Message 不进入 Human Inbox；`channel_activity`和`system`只属于 Agent，因此 Human 的第三组保持为空。

打开 Item 的来源时，Browser 调用`read`端点把该 Item 标记为已读，见 [API 与事件](07-api.md)。Item 不显示完成或延后控件：Agent Item 的终态由领取它的 Run 决定，见 [Inbox 与凭据](06-inbox-credentials.md)。

Agent Inbox默认不向普通Member公开。Owner/Admin只能读取自己有权访问的来源摘要和错误代码。

Agent 详情显示 action permissions。Human Owner/Admin 可以逐项授予或撤销`channel.create`和`agent.create`。UI 不提供 Role 形式的 Permission 套餐。

## 9.1 Members、Computers 与 Agent

Computer 与 Agent 详情沿用三栏 shell 和同一条标题基线。详情主体使用扁平分区和紧凑字段网格：分区不绘制卡片边框、底色或阴影，以留白和2px ink分隔线组织；区域标题使用ink色大写，事实数字使用大号粗体，状态只使用图形信号加短标签，不带边框。页面头与Inbox/Channel共用62px基线，标题、辅助文字、字段标签和字段值分别使用页面标题、辅助信息、元数据和正文层级，并在同一行按baseline对齐。

- 生命周期、运行和连接状态使用图形信号加短标签。信号必须带 `aria-label` 与 `title`，颜色不能是唯一线索；状态不使用占用整行的彩色卡片。
- 标题、辅助文本、字段标签和字段值分别使用页面标题、辅助信息、元数据和正文层级，并在同一行按 baseline 对齐。字段网格在 1024px 以下收为单列，在 390px 仍保持可读间距。
- Agent action permissions 直接显示为 checkbox 列表。每行只显示 action code 和一条最短说明；Owner/Admin 可以逐项勾选或取消，普通 Member 看到禁用状态。Permission 不显示 Role 套餐或重复解释。
- Computer Hosted Agents 列表和 Agent Runtime 区域共用状态信号、字段间距和按钮高度。三栏分布、Computer/Agent 路由及 API 行为保持不变。
- Members 主页按 Agents、Humans 分组显示扁平成员行；不显示表头行、不使用斑马纹，行间以细分割线分隔。每行只显示身份、Access Level（Member/Admin 可直接设置）和消息动作，不展示 action permission；权限逐项管理在 Agent 详情。Members 页头部提供 kind 筛选（All、Human、Agent）以及 Invite Human 与 Create Agent 操作，行内容不因筛选和头部操作而增加。
- Agent 详情内容区与页面头、标签页共用左缘基线，不居中。详情内字体层级固定为：分区标题13px大写、字段值16px/600、字段标签10px大写，页面标题保持18px。
- Agent 详情 Overview 不重复显示 Role（页面头已显示）和 Role revision 等内部计数。Computer 字段链接到对应 Computer 详情。
- Agent 详情的消息按钮点击后自动创建或复用与目标 Agent 的 DM，再跳转到该 DM，不在中间显示空对话。
- Owner/Admin 打开 `/computers` 且未选择 Computer 时，中间显示新增 Computer onboarding，左侧保留已配对列表；点击 Computer 行后才进入详情。`pair-computer` hash 兼容现有入口并显示同一 onboarding，不再叠加重复 modal。无配对 Computer 时 onboarding 仍是 Owner/Admin 的主内容。普通 Member 不显示新增入口或配对命令。
- Computers 左侧导航只提供已配对列表和 `+` 配对入口，不显示重复的 Add Computer 项。

## 10. 通用组件

- Button保留Primary、Secondary、Quiet和Destructive四类。
- Entity row使用扁平列表，不增加卡片层级。
- 持久阻断使用inline notice，短期结果使用toast。
- 空态只写事实和至多一个下一步动作，不使用大插画。
- 空态和`No DMs yet`等系统信息使用辅助信息字号。
- Composer只包含Markdown、Attachment、mention和Send，不包含Task或Session控制。Channel与Thread复用同一Composer结构和高度；Composer外框是唯一输入边框，Attachment位于左下角，Send位于右下角，宽度随所在区域变化。

## 11. 响应式与无障碍

- 1100px及以上保持Space rail、Navigation、Channel和可选Thread pane。
- 700至1099px将Navigation变为抽屉，Thread覆盖Channel。
- 低于700px使用单列，Thread和Task详情为全屏路由。
- 返回Channel时恢复滚动位置、打开的Thread和Composer draft。
- 所有操作支持键盘，focus可见。
- 状态使用文字和图形，颜色不是唯一线索。
- icon button具有accessible name。
- 动画遵循`prefers-reduced-motion`。
