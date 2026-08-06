# UI_DESIGN

本文件定义 WebUI 的行为与视觉要求。产品要求见 `DESIGN.md`。

## 视觉

- 使用 Neo-Brutalism：2px ink 边框、硬偏移阴影；不使用渐变、玻璃、模糊或柔和投影。
- 颜色 token：ink `#241F1A`、paper `#FFFBF4`、panel `#F5EEE1`、rail `#241F1A`（rail 前景 `#FFFBF4`）、accent `#F0602F`、accent-soft `#FDE3D5`、indigo `#3D5AA9`、green `#5B9253`、orange `#E08B2C`、red `#D33F2E`、stone `#B4AB9D`、muted `#8A8071`。
- Space accent 只取四个预置色（`#F0602F`、`#E8B42D`、`#3C9E8F`、`#6C5CE7`）之一；自定义主题色能力待定。
- 字体：Manrope、Noto Sans SC、sans-serif fallback；代码与等宽元数据使用 JetBrains Mono。
- 字号层级：页面标题 18px/700、区域标题 11px/700 大写、正文 14px/400、Message 正文 13px/400、辅助信息 12px/400、元数据 10px/600。
- 状态必须同时使用文字和图形，颜色不是唯一线索；所有图形信号带 `aria-label` 和 `title`。
- Human 头像使用姓名首字符和稳定背景色；Agent 使用由 member ID 生成的 8×8 对称像素印章并显示 `AGENT` 标签。

## 布局

- 桌面布局为 Space rail、Navigation、Channel、可选 Thread pane 四栏；Thread pane 只在打开时存在。
- Navigation 默认 294px，可折叠或变为抽屉；Channel 最小宽度 480px；Thread pane 360 至 480px。
- 1100px 及以上保持四栏；700 至 1099px Navigation 变抽屉、Thread 覆盖 Channel；低于 700px 使用单列，Thread 和 Task 详情为全屏路由。
- Header 高 62px；Composer 最小 88px，增长到 240px 后内部滚动。
- 返回 Channel 时恢复滚动位置、打开的 Thread 和 Composer draft。
- Composer 只包含 Markdown、Attachment、mention 和 Send；DM 的 Composer 不提供 mention。

## 对话与 Task

- 只有 Root Message 提供 `Create Task` 动作；创建后立即替换为 Task 标识，不显示 bind、Source Thread 选择或来源确认步骤。
- Thread reply 不显示创建动作；reply 上的 Task 动作保持禁用并说明只能从 Root Message 创建。
- 每条 Message 在 hover 或键盘 focus 时显示动作面板，固定包含 `Reply to thread` 和 Task 动作；面板贴齐 Message 行右上角，不占正文栏位。
- Root Message 的 Task 标识使用元数据行：`!<seq>`、状态文字和状态图形；title、assignee 不显示在消息上，hover 显示 title；当前 Run 在其他 Linked Thread 时显示 `· elsewhere`。只有 Root Message 显示。
- Task 状态标签：TODO paper 背景、In Progress accent-soft、In Review orange、Done green、Closed stone；全部带状态图形。
- Action Message 使用紧凑结构化行，显示 actor、动作和资源链接，不显示原始 JSON、命令参数或内部 ID；普通 composer 不能选择 action kind。
- System Notice 渲染为居中系统信息行，不显示分割线、作者或回复操作；同一日期内连续多条默认折叠。
- Agent attention 失败在来源 Message 下方显示 inline notice，包含目标 Agent、稳定错误码和重试状态，不显示数据库错误或正文副本。
- Message 正文支持常用 Markdown；正文中的 mention 和 Task 引用只渲染 Server 返回的结构化结果，不根据符号推断。
- 普通 Message 正文超过 8 个视觉行时默认折叠，按实际排版高度判断，不按字符数推断。

## Task 页面

- Tasks 页面使用 list/detail 布局，是对话的辅助入口，不是项目管理页面；入口保留在侧边栏。
- 筛选固定为 All open、TODO、In Progress、In Review、Done、Closed、Assigned to me；默认先显示 In Review，再显示 In Progress 和 TODO，各组按 updated_at 倒序。
- Task 详情显示 title、status、assignee、Source Thread、Related Threads、Result（仅 Done）、Close reason（仅 Closed）、current Run 和 Focus、最近 Run outcomes、Session continuity。
- Task 创建后 title 与 assignee 不可修改；对 Task 的操作只有状态流转（TODO → In Progress → In Review → Done / Closed）与 Close（记录原因）。
- Session continuity 位于详情底部，取值 Warm / Cold / Reset required / Unavailable，只说明下次执行效率。
- Working、Waiting、Failed 是 Run 或 attention 状态，不进入 Task 状态选择器。

## Inbox、Members、Computers、Agent

- Human Inbox 按 DM 和 Thread 聚合，同一组只显示最新来源 Message 的预览、时间和组内 Item 数；打开聚合行即标记已读。
- Agent Inbox 不向普通 Member 公开；Owner/Admin 只能读取来源摘要和错误代码。
- Agent 详情默认选中 Activity tab；feed 展示 Agent 自身的写交互（kind、语义参数、时间、目标链接和有界 preview），不显示 Attachment、Memory、workspace 文件、Provider transcript 或隐藏推理。
- Members 页面按 Agents、Humans 分组显示扁平成员行；Permission 在 Agent 详情逐项管理，不提供 Role 套餐。
- Computers 页面左侧为已配对列表和配对入口；未选择 Computer 时显示配对 onboarding。
- Agent 详情的消息按钮直接创建或复用与目标 Agent 的 DM 并跳转，不显示中间空对话。
- 详情分区使用扁平布局：不绘制卡片边框、底色或阴影，用 2px ink 分隔线组织。

## 响应式与无障碍

- 所有操作支持键盘，focus 可见；icon button 具有 accessible name。
- 动画遵循 `prefers-reduced-motion`。
- 390px 和 1440px 视口都能完成核心流程。
