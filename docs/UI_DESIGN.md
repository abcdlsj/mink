# UI_DESIGN

本文件定义 WebUI 已采用的行为与视觉要求。产品要求见 `DESIGN.md`。

视觉候选与迭代过程保存在 `design-lab/`，与本文件分工不同：design lab 是探索工具，本文件只记录已采用的规范。候选方案不进入本文件。

## 视觉

- 视觉语言是物件化纸面工作台：控件是硬边物件，信号克制。不使用渐变、玻璃、模糊；硬偏移阴影只用于主要物件（按钮、像素铭牌、下拉浮层），轻量元素（附件、系统消息）不画边框。
- 颜色 token：ink `#241F1A`、paper `#FFFBF4`、panel `#F5EEE1`、surface-strong `#EFE5D5`、rail `#241F1A`（rail 前景 `#FFFBF4`）、accent `#F0602F`、accent-soft `#FDE3D5`、indigo `#3D5AA9`、green `#5B9253`、orange `#E08B2C`、red `#D33F2E`、stone `#B4AB9D`、muted `#8A8071`、line `#E8DECD`、line-strong `#C6B9A6`。
- Space accent 只取四个预置色（`#F0602F`、`#E8B42D`、`#3C9E8F`、`#6C5CE7`）之一；自定义主题色能力待定。
- 字体：Manrope、Noto Sans SC、sans-serif fallback；代码与等宽元数据使用 JetBrains Mono。
- 字号层级：页面标题 18px/700、区域标题 12px/700 大写、正文 14px/400、Message 正文 14px/400、辅助信息 13px/400、元数据 11px/600。
- 按钮基线：紧凑 30px、默认 34px、主要 CTA 40px、图标按钮 32px、发送 30px；字号 12-14px。按钮高度全局统一，不随场景放大。
- 自定义下拉控件（SumiSelect）：触发按钮 1px line-strong 边框 + 圆角 7px；浮层纸色底 + 2px ink 边框 + 3px 硬阴影；选中项加粗 + accent 勾。表单控件统一使用该组件，不渲染浏览器原生 select。
- 空间名像素字：5×7 粗体点阵，白底铭牌（2px ink 边框 + 3px 硬阴影），space 主题色；长名字按单词换行（最多两行）并保底像素单位。
- 附件以无边框浅底小条展示（panel 底 + 圆角 7px），hover 变 accent-soft；不画边框。
- 导航 active 项：surface-strong 浅底 + 3px accent 左边条，不使用整块 accent 背景。
- 状态必须同时使用文字和图形，颜色不是唯一线索；所有图形信号带 `aria-label` 和 `title`。
- Human 头像使用姓名首字符和稳定背景色；Agent 使用由 member ID 生成的 8×8 对称像素印章。消息正文不显示 `AGENT` 标签，成员列表等身份场景显示。

## 布局

- 桌面布局为 Space rail、Navigation、Channel、可选 Thread pane 四栏；Thread pane 只在打开时存在，展开时 Channel 自适应收缩，不预留空间。
- Navigation 默认 294px，可折叠或变为抽屉；Channel 最小宽度 480px；Thread pane 360 至 480px。
- Message 内容占满 Channel 可用宽度，不受行长限制。
- 1100px 及以上保持四栏；700 至 1099px Navigation 变抽屉、Thread 覆盖 Channel；低于 700px 使用单列，Thread 和 Task 详情为全屏路由。
- Header 高 62px；Composer 最小 88px，增长到 240px 后内部滚动。Composer 使用悬浮卡片（1px line-strong 边框、圆角 12px、柔和阴影），不画 2px 外框。
- 返回 Channel 时恢复上次退出的滚动位置（无保存位置时定位到最新 Message）和打开的 Thread；离开 Conversation 区域后，Conversation 入口与 Space 首页回到上次所在的 Channel/DM；Composer 未发送正文按对话保存在浏览器本地，发送成功后清除。
- Composer 只包含 Markdown、Attachment、mention 和 Send；DM 的 Composer 不提供 mention。

## 对话与 Task

- Channel 与 Thread 收到新消息时，距底部不超过 3/4 可视屏高则自动定位到最新 Message；超过 3/4 屏时不自动定位，显示 `To bottom`，点击后回到最新 Message；发送成功后同样定位到新 Message。Channel 在恢复的旧位置或离开期间产生的新消息，以数量展示在 `To bottom` 按钮上，点击回到最新 Message 后清除。
- 只有 Root Message 提供 `Create Task` 动作；创建后立即替换为 Task 标识，不显示 bind、Source Thread 选择或来源确认步骤。
- Thread reply 不显示创建动作；reply 上的 Task 动作保持禁用并说明只能从 Root Message 创建。
- 每条 Message 在 hover 或键盘 focus 时显示动作面板：极淡、单 icon（Reply to thread 与 Task 动作），无边框无背景，贴齐 Message 行右上角；hover 消息才出现。
- Channel 消息与 Thread reply 中的 Agent 头像可点击，直接打开与该 Agent 的 DM；DM 消息中的头像不可点击。
- Root Message 的 Task 标识位于消息正文左下角：`!<seq>`、状态文字与状态图形；title、assignee 不显示在消息上，hover 显示 title；当前 Run 在其他 Linked Thread 时显示 `· elsewhere`。只有 Root Message 显示。
- Task 状态标签：TODO paper 背景、In Progress accent-soft、In Review orange、Done green、Closed stone；全部带状态图形。
- Action Message 使用紧凑结构化行，显示 actor、动作和资源链接，不显示原始 JSON、命令参数或内部 ID；普通 composer 不能选择 action kind。
- System Notice 渲染为居中系统信息行：成员名用 accent 高亮，正文 muted；不画横线、无边框；同一日期内连续多条默认折叠，折叠按钮为纯文字。
- Agent attention 失败在来源 Message 下方显示 inline notice，包含目标 Agent、稳定错误码和重试状态，不显示数据库错误或正文副本。
- Message 正文支持常用 Markdown（含 `- [x]` / `- [ ]` 任务列表渲染）；正文中的 mention 和 Task 引用只渲染 Server 返回的结构化结果，不根据符号推断。
- 普通 Message 正文超过 8 个视觉行时默认折叠，按实际排版高度判断，不按字符数推断。

## Task 页面

- Tasks 页面使用 list/detail 布局，是对话的辅助入口，不是项目管理页面；入口保留在侧边栏。
- 筛选固定为 All open、TODO、In Progress、In Review、Done、Closed、Assigned to me；默认先显示 In Review，再显示 In Progress 和 TODO，各组按 updated_at 倒序。
- Task 详情依次显示：Task facts（Created by、Updated；TODO 未指派时可选择 Agent 开始）、Actions、Source Thread、Result（仅 Done）、Close reason（仅 Closed）、侧栏（Current Run and Focus、Recent Run outcomes、Session continuity）。
- Actions 只有两个操作：Done（直接完成，不需要理由）与 Closed（弹窗选择理由 + 可选备注）。不提供其它状态操作按钮。
- Task 创建后不可修改（见 `DESIGN.md`）；不显示 Related Threads 区块，不提供手动 Link/Unlink。
- Recent Run outcomes 在独立滚动容器内展示，每条 Run 聚合为一行（状态、Agent、Focus、错误码、起止时间）。
- Session continuity 取值 Warm / Cold / Reset required / Unavailable，只说明下次执行效率。
- Working、Waiting、Failed 是 Run 或 attention 状态，不进入 Task 状态选择器。

## Company 页面

- Company 是 Space 的公司形态入口，默认进入 Company Office；Office 入口固定在中间 Navigation 面板。
- Company Office 是像素化办公区：每个 Agent 一个工位，按 activity 状态显示坐姿、打字或空闲；不同 Agent 使用不同配色区分，名字固定在头顶。Agent 空闲一段时间后会随机游走、去茶水角喝水/喝咖啡或站到游戏桌，移动速度缓慢；一旦转为工作状态就走回工位（不瞬移）；Agent 参与 DM 活跃时，发起方走到对方工位讨论；群组频道在数分钟窗口内消息达到阈值时，频道内 Agent 聚集到会议区。动画遵循 `prefers-reduced-motion`；静止模式下停止随机游走与定时位置更换，工作、DM 和群组事件直接呈现目标位置。
- Office 场景与小人使用 Arlan_TR 的 [Free office pixel art](https://arlantr.itch.io/free-office-pixel-art)（itch.io 免费素材包）：场景由包内独立物件拼装（隔断、工位、电脑、椅子、绿植、水冷机等），地板为平铺像素砖；Agent 使用包内 Julia 的 idle / walk 动画，工作状态使用 Julia 的背面帧并对应电脑亮屏；页面底部展示作者署名。
- 办公室画布随 Agent 数量成长：0 人显示空办公室、1-3 人 480×270 单排、4-6 人 640×360 两排、7-9 人 800×450 三排、10-12 人 960×540 三排四列；超过 12 人后保持四列并通过增加行数向下扩展画布。只给实际 Agent 生成工位与电脑，每个 Agent 使用独立工位；人少时桌子挨在一起，画布也更小。
## Inbox、Members、Computers、Agent

- Channel header 的 Member strip 使用与 Agent 头像相同尺寸的加号按钮，并在 Agent 选择浮层中显示 Display Name 与 Role。
- Human Inbox 按 DM 和 Thread 聚合，同一组只显示最新来源 Message 的预览、时间和组内 Item 数；打开聚合行即标记已读。
- Agent Inbox 不向普通 Member 公开；Owner/Admin 只能读取来源摘要和错误代码。
- Members 页面按 Agents、Humans 分组显示扁平列表行：身份（头像 + 名字 + 行内状态点）、Access 控制（自定义下拉）、30px 轻量消息图标。Permission 在 Agent 详情逐项管理，当前动作包括 channel.create、channel.invite、channel.remove 和 agent.create，不提供 Role 套餐。
- Computers 页面左侧为已配对列表和配对入口；未选择 Computer 时显示配对 onboarding。
- Agent 详情头部只显示安静信号（Activity、Computer 可达性、错误码），不提供 DM 按钮；facts 值使用 13px 并截断超长文本。
- Agent 详情 Activity feed 按时间倒序；参数压缩为单行 `name = value`（名称 muted 等宽、等号 muted、值 ink-soft 的 code 块，最多 3 个），message preview 为核心 codeblock（accent-soft 底 + accent 左边条 + truncated），时间右对齐；字号统一。
- 详情分区使用扁平布局：不绘制卡片边框、底色或阴影，用 2px ink 分隔线组织。

## Agent insights

- `Agent insights` 是默认关闭的实验功能：只有 Settings 开启后，Space rail（Network 图标）与移动端 Space tools 导航才显示入口；路由为 `/s/$spaceSlug/insights`，未开启时直达页面显示禁用说明与 Settings 链接。
- insights 使用三栏布局：中栏是 `Statistics` 与 `Graph` 两个条目（`/insights/stats`、`/insights/graph`），右侧展示对应内容。
- Statistics 页按 Agent 聚合 LLM 消耗：左列是可选中 Agent 列表（像素印章 + input/output 摘要），右侧显示该 Agent 的请求数、input/output/cached 统计卡、SVG 曲线和按 model 的分组表；支持 24h/7d/30d。部分 Computer 查询失败时显示部分数据警告，全部失败时显示错误与 Retry，不显示为无 usage。
- Graph 页为力导向关系图 + 右侧详情面板。节点是 Agent 像素印章头像（与 PixelIdentity 同源算法，SVG 内联渲染，不依赖 HTML foreignObject）+ display name；边是 Agent 之间的互动关系，使用 1px ink 细线，hover/focus 显示总数，选中态只通过头像描边与整体明暗区分。
- 支持拖拽节点、拖拽空白平移、滚轮缩放和屏幕上的 zoom in/out/reset 按钮；`prefers-reduced-motion` 下不做动画。
- 点击节点高亮其邻居并在面板列出相邻关系；点击边显示统计明细（DM、mention、reply 分向计数）和 Communication chain（最近 5 条可读消息的 author、kind、时间、正文预览）。
- 空态提示创建 Agent；加载失败显示 Retry。所有图形节点可键盘聚焦（Enter/Space 选中，Esc 清除），图形信号同时有文字与 `aria-label`。

## Settings

- Space rail 最底部提供 Settings 入口（齿轮图标），路由为 `/s/$spaceSlug/settings`；移动端 Space tools 导航提供同入口。
- Settings 使用列表/详情布局：中栏是已注册 feature 的名称列表（含类型与 On/Off 状态），点击后在右侧展开该 feature 的详情；未选中时右侧显示占位说明。
- feature 通过注册表（`featureRegistry`）声明 id、名称、类型、描述、storageKey 和可选配置项；后续新增 feature 只改注册表，无需改页面结构。
- 实验类型（experimental）的详情默认包含 `Enabled` 开关；所有状态只持久化在浏览器 localStorage（`sumi.feature.<id>`），不上传 Server。开启后左 rail 才显示对应实验入口（当前为 Agent insights 与 Company office；Company office 未开启时直达 `/company/office` 显示禁用说明）。

## Computers 的 LLM usage 面板

- Computer 详情页为 Owner/Admin 显示 `LLM usage` 面板；数据只来自该 Computer 本地存储，Server 不保存。
- 面板包含请求数、input/output tokens、cache hit rate 四张统计卡；SVG 曲线展示 input/cached input/output 随时间的走势（24h 按小时、7d/30d 按天）；`By model` 与 `By agent` 两张分组表。
- 提供 24h / 7d / 30d 周期切换；Computer 离线显示离线说明，无数据显示空态，daemon 不可用显示重试提示。图表有 aria-label 与图例，控件可键盘操作。

## 响应式与无障碍

- 所有操作支持键盘，focus 可见；icon button 具有 accessible name。
- 自定义下拉、折叠分组等交互支持完整键盘操作（方向键、Enter、Esc）。
- 动画遵循 `prefers-reduced-motion`。
- 390px 和 1440px 视口都能完成核心流程。
