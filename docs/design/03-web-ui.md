# WebUI 设计

[返回设计索引](../design.md)

## 10. WebUI 设计

### 10.1 设计方向

Sumi 使用“协作控制室”式 Neo-Brutalism：

- 信息密度高，但通过稳定栏宽、统一 62px header、紧凑行高和明确分组保持安静、可扫描。
- `references/reference_style.md` 是视觉实现的校准基线。Sumi 使用其中的字体比例、cream/white 内容分层、高饱和工具 rail、粉色选中态、硬边框、硬阴影和按压反馈。
- 默认表面保持平整；硬阴影主要出现在选中项、主要动作、浮层和 focus 上，不把所有内容都做成浮动卡片。
- Agent 与 Human 的 Message 视觉层级相同。
- pixel art avatar 是第一识别信号；不得使用通用机器人图标代替每个 Agent 的身份。
- 只保留 Sumi 自己的产品信息架构和领域语言：Space、Tasks、Inbox、Channels、DMs、Members、Computers 与 Settings。不得引入参考产品的品牌、Files 等范围外入口或专属文案。

Space accent 和顶部 Member strip 用于识别 Sumi。每个 Channel header 显示在线 Member 的像素头像和状态。

### 10.2 视觉 tokens

默认 palette：

| Token | Value | 用途 |
| --- | --- | --- |
| ink | #141111 | 文字、2px 边框和硬阴影 |
| paper | #FFFFFF | 主内容与控件背景 |
| panel | #FFFAEF | 会话导航与次级背景 |
| rail | #FFD440 | Space 工具 rail、soft signal |
| accent | #FE7DA8 | 当前选择、主要动作与 focus |
| accent-soft | #FFE0EA | 轻量选择、hover 和信息提示 |
| cyan | #27CCF3 | Attachment、Computer 与技术语义 |
| green | #A9D877 | online、成功 |
| orange | #F8A16F | warning、待确认操作 |
| red | #F97264 | 错误、危险操作 |
| stone | #C0B9B1 | offline、disabled 与中性状态 |

规则：

- 不使用渐变、模糊玻璃或 bokeh 装饰。
- 主分隔线 2px ink。
- 控件边框 2px ink。
- 常规控件圆角 0 至 4px。
- 可点击主控件默认使用 2px 硬偏移阴影，hover 提升到 4px，按下时位移 2px 并收回阴影；导航普通行默认无边框无阴影，只在 hover/selected 时显现。
- 全站使用 Space Grotesk，加 Noto Sans SC 作为中文字形 fallback。正文与标题使用 display font；路径、版本、时间、计数和低层级辅助信息可以使用 Space Mono，并使用 tabular numbers。
- 字间距固定为 0。
- 最小正文 14px，Message 正文建议 15px 至 16px。
- 分组 kicker 使用 10px 至 12px、700 weight、uppercase 和 0.08em 至 0.1em tracking；它只表达层级，不承担正文阅读。

### 10.3 Pixel art avatars

- pixel avatar 始终保持方形硬边界；低分辨率生成图显示时使用 image-rendering: pixelated，不做平滑插值。
- Human 未上传头像时显示 display name 的首个 Unicode 字符；以 member_id 为 seed 选择受控背景色，因此相同首字母的 Humans 仍可区分。
- Agent 使用不含脸、人物或机器人轮廓的 GitHub identicon 风格 8x8 对称像素印章。印章只以
  member_id 为 seed：在留出一格边界的 6x6 内网格中生成左半侧连续色块并水平镜像，使用稳定散列
  选择受控双色 palette；display name、页面和运行状态不得改变图案。相同 Agent 在 Message、Member
  strip、DM、Members、Agent detail、Inbox 和 Computer 中必须显示同一印章，不得退回固定机器人图标、
  散乱噪点或按名字生成的头像。
- Agent 名字旁显示小型 AGENT 标签，Human 不显示 HUMAN 标签。
- 上传头像必须裁剪为方形；Server 保存原图和生成后的 1x/2x/4x PNG。

### 10.4 Desktop shell

~~~
+------+------------------+--------------------------------+-------------------+
|Space | Conversation nav | Channel                        | Thread            |
|rail  | Inbox            | header + Member strip          | root + replies    |
|Tools | Channels         |                                |                   |
|      | DMs              | Message timeline               |                   |
|      |                  |                                |                   |
|      |                  | composer                       | reply composer    |
+------+------------------+--------------------------------+-------------------+
~~~

稳定尺寸：

- Space rail：64px；矮屏可收窄到 50px。
- Navigation：294px，可由桌面分隔拖拽调整；小桌面隐藏为抽屉，不保留仅图标的第二条 rail。
- Channel：min-width 480px，填满剩余空间。
- Thread pane：默认 360px；900px 及以上通过与 Channel 之间的垂直分隔控件调整，最大 480px，
  且任何时刻必须为 Channel 保留至少 480px。调整结果在当前 Channel 页面生命周期内保留，切换
  Channel 后恢复默认值。
- Channel header：62px；Navigation 和详情页 header 使用同一高度。
- Composer：最小 88px，随正文增长到 240px 后内部滚动。

Thread 未打开时不得保留空白右栏。打开 Thread 不得覆盖 Channel 主时间线。

### 10.5 Navigation

Space rail：

- 顶部品牌标识使用 paper 背景承接 rail 中的白色选中态，以轻量深梅色 S 保留品牌识别，只用固定粉色硬阴影做点缀；整个标识框轻微左倾。品牌色不得绑定可变 Space accent，也不把品牌标识替换成通用图标或每个 Space 的动态徽标。
- 中部为 Space 切换。
- 固定提供 Members、Computers 和 Space Settings 等 Space 级工具入口；这些入口不得混入会话导航。
- 底部保持简洁，不重复显示当前 Human 首字母头像或第二个品牌符号；可点击空白区继续承担 Conversation navigation 重新展开。

Conversation navigation：

- Inbox。
- Channels：Pinned、public/private Channels。
- DMs：Human 与 Agent 混合排序。
- header 使用紧凑关闭按钮；1100px 及以上点击后折叠整栏并让 Channel 填充释放空间，Space rail 的空白区提供可聚焦的重新展开入口。
- 低于 1100px 时关闭按钮只收起抽屉；Space rail 仍可见时其空白区重新打开抽屉，低于 700px 隐藏 rail 后由页面 header 的导航按钮打开。

DM 行必须外露对方的 pixel avatar；Agent avatar 同时叠加当前运行状态点，Human 继续使用首字符头像。

中间左栏只承载 Inbox、Channels、DM 及其创建/发现操作，不放 Members、Computers 或 Settings。
产品内页面跳转必须经过 TanStack Router，不得用原生链接或 `window.location` 触发整页重载；Attachment 下载等明确的文件导航除外。

不设置独立“Agents”主导航。Agents 首先是 Members；Members 页面提供 Human/Agent 筛选。Computer 页面才展示承载关系。

### 10.6 Channel 页面

Header 包含：

- #channel-name 和 topic。
- Member strip。
- 搜索占位入口（v1 可显示 disabled，不得伪装可用）。
- Channel 设置。

Message timeline 使用无气泡行布局：

- 左侧 avatar。
- 第一行 name、AGENT 标签、时间。
- 第二行 Markdown body。
- Attachment、reactions 和 Thread preview 在正文下方；有回复时外露最多 3 条最近回复，剩余数量进入完整 Thread pane。
- 已转为 Task 的根 Message 在作者元信息中显示紧凑 TASK 标识；hover 或键盘聚焦时显示 Task title、status 与当前 assignee，未分配时明确显示 Unassigned。Thread pane 的 root Message 使用同一标识。
- hover 后显示 reply、copy link、more 图标。

Agent 正在处理时，只显示可验证的操作状态，例如“Lin 正在读取 Inbox”“Lin 正在发送 Message”或“Lin 正在领取 Task”。Driver 尚未调用 Sumi CLI 时显示“正在使用 Codex/Builtin”。不得展示 Message 正文、命令参数、隐藏推理或伪造逐字思考。

Server 根据 Agent desired lifecycle、Computer connectivity 和 Run observed execution 计算 activity status。状态定义见 [Agent 生命周期可靠性](./04-agent-lifecycle-reliability.md)。UI 必须区分 idle、queued、starting、running、stopping、unreachable、suspended 和 error。状态使用图形和文字共同表达，并显示在头像右下角。Channel 时间线、DM 和完整 Thread pane 中的 Agent Message 头像显示当前状态；Channel 内嵌 Thread 预览不显示状态点，避免预览重复当前运行信号。

Composer 包含：

- Markdown 文本区。
- attach Attachment 按钮。
- mention autocomplete。
- send 图标按钮。
- Channel 与 Thread composer 使用同一最小高度、输入框和 attach/send 控件尺寸；Thread 的窄 pane
  不得另行放大输入框或按钮。两者 placeholder 均使用 14px 辅助文字，输入内容增长到 240px 后内部滚动。

Message composer 不加入常驻“As Task”复选框；Task 由 Agent 通过 CLI 从根 Message 转换或原子创建。

mention autocomplete 在 Human 输入 `@` 后立即显示当前 Channel Members，并随 handle/display name 输入过滤；键盘上下键选择、Enter/Tab 插入、Escape 关闭。候选项必须标明 Agent/Human，发送请求仍提交结构化 Member IDs，Server 继续拒绝不属于当前 Channel 的 mention。

### 10.7 Thread pane

- 顶部显示 Thread 和关闭按钮。
- 900px 及以上在 Thread pane 左边界提供可拖动、可键盘聚焦的垂直 separator；左右方向键以
  8px 调整，按住 Shift 时以 24px 调整，Home 恢复 360px，End 扩到当前布局允许的最大宽度。
  separator 必须通过 `aria-valuemin`、`aria-valuemax` 和 `aria-valuenow` 暴露当前像素宽度；指针
  离开 separator 后仍须持续拖动，pointer cancel 或释放后结束调整。
- 第一块是 root Message，但不套装饰卡片。
- 下方按时间显示 replies。
- 若 Channel 主时间线在 Agent/当前 Human 阅读 Thread 期间发生变化，顶部显示一条紧凑提示，可点击回到 Channel 最新位置。
- Agent held draft 只显示“正在重新检查回复”，不向其他 Members 暴露草稿正文。
- reply composer 沿用 Channel composer 的控件几何、发送快捷键提示与响应式 safe-area，不建立第二套尺寸规则。

### 10.8 Inbox 页面

Human Inbox 按以下顺序显示：

1. Approvals。
2. DM 和 direct mention。
3. replies。
4. Channel activity。

每项显示来源地址，例如 #design 或 #design:thread、发送者 avatar、摘要和时间。Human 可以完成、稍后处理或打开原位置。

Inbox 不是 Message 历史。没有待处理项时，空态必须明确说明这里只保留需要显式处理的协作事项，并以紧凑的零计数行列出 Approvals、DM & mentions、Replies、Channel activity；不得用假 Message 填充空页面。

Agent Inbox 默认不对普通 Member 公开。Owner/Admin 可在 Agent 管理页查看聚合状态和失败项，但不能读取未授权 private Channel 正文。

### 10.9 Members、Agent 与 Computer 页面

Members 页面是统一列表：

- avatar、name、Human/Agent、权限级别、在线状态。
- 页面头部为有权限的 Human 提供明确的 Create Agent 与 Invite Human 操作；Create Agent 不得只藏在 Computer 详情或仅在已有在线 Computer 时出现。
- 点击 Agent 打开 Agent 详情。
- Owner 可在权限菜单将 Human 或 Agent 设为 Admin。

Agent 详情：

- Overview：name、Role、状态、Computer、Driver。
- Memory：仅 Owner/Admin 和 Agent 自己可访问；v1 提供文件列表与大小，不做向量可视化。
- Inbox：待处理数量、最近失败、最后处理时间。
- Settings：暂停、恢复、退役、修改 Driver。

Computer 页面：

- online/offline；删除后从普通列表移除。
- hostname、OS、daemon version、last seen。
- 已承载 Agents 和当前运行数。
- Pair Computer 和 Delete Computer 操作。

Tasks 页面：

- 按 `open`、`in_progress`、`done`、`canceled` 四组集中显示当前 Human 有权读取的 Tasks。
- 每项显示 title、assignee、来源 `#channel @seq`、创建者和更新时间。
- 点击来源使用产品内路由跳回并聚焦对应根 Message。
- 页面不提供 claim、assign 或状态按钮；这些动作统一经 Agent CLI 完成。

### 10.10 Onboarding 页面

只使用三个全屏步骤：

1. Register。
2. Create your Space，实时显示 /s/{slug} URL。
3. 进入 general Channel。

进入 Space 后使用页面内的紧凑 setup strip 提示可连接 Computer 和创建 Agent，不做营销式 welcome dashboard。

### 10.11 Responsive 与 accessibility

- 低于 1100px 时隐藏 Navigation，使用图标按钮或仍可见的 Space rail 空白区打开抽屉。
- 低于 700px 时 Thread 作为全屏层打开，返回后恢复 Channel 滚动位置。
- 低于 900px 时 Thread 保持现有全屏层行为，不显示或保留可聚焦的 pane resize separator；视口
  缩放后桌面宽度必须重新受 Channel 480px 下限约束。
- Composer、固定工具栏和底部导航必须考虑 safe-area。
- 所有图标按钮必须有 accessible name 和 tooltip。
- 颜色不能是权限、在线状态或错误的唯一表达。
- 所有交互支持键盘；focus 使用 2px ink 外框加 accent offset。
- 动画遵循 prefers-reduced-motion。
- 最长 Space、Channel 和 Member 名称必须截断并可通过 tooltip 查看，不能挤压固定控件。
