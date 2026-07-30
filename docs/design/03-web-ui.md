# WebUI 设计

[返回设计索引](../design.md)

本文件是WebUI行为与视觉的唯一规范。

## 1. 产品重心

Sumi仍以对话为主。Human的默认入口、主要工作区和最高频操作都是Channel、DM、Thread和Message。

Task 是核心领域对象，但不主导整体信息架构。现有侧边栏中的 Tasks 入口保持原位置。

Channel 不增加 Tasks tab。Composer 不增加`As Task`。全局 shell 不为 Task 新增导航层或固定详情栏。

现有Neo-Brutalism视觉、像素头像、栏宽、硬边框、硬阴影和响应式结构继续使用。本次重建只调整Task、Run和Session相关的信息与交互。

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
- 字体使用Space Grotesk、Noto Sans SC和sans-serif fallback。
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

Human Inbox继续按Approvals、DM/mention、replies、Task updates、Channel activity和system issues分组。Inbox不是Message历史。

Agent Inbox默认不向普通Member公开。Owner/Admin只能读取自己有权访问的来源摘要和错误代码。

Agent 详情显示 action permissions。Human Owner/Admin 可以逐项授予或撤销`channel.create`和`agent.create`。UI 不提供 Role 形式的 Permission 套餐。

## 10. 通用组件

- Button保留Primary、Secondary、Quiet和Destructive四类。
- Entity row使用扁平列表，不增加卡片层级。
- 持久阻断使用inline notice，短期结果使用toast。
- 空态只写事实和至多一个下一步动作，不使用大插画。
- 空态和`No DMs yet`等系统信息使用辅助信息字号。
- Composer只包含Markdown、Attachment、mention和Send，不包含Task或Session控制。

## 11. 响应式与无障碍

- 1100px及以上保持Space rail、Navigation、Channel和可选Thread pane。
- 700至1099px将Navigation变为抽屉，Thread覆盖Channel。
- 低于700px使用单列，Thread和Task详情为全屏路由。
- 返回Channel时恢复滚动位置、打开的Thread和Composer draft。
- 所有操作支持键盘，focus可见。
- 状态使用文字和图形，颜色不是唯一线索。
- icon button具有accessible name。
- 动画遵循`prefers-reduced-motion`。
