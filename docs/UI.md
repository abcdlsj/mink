# Sumi WebUI 视觉与页面规格

- 状态：Approved direction
- 日期：2026-07-26
- 输入：用户提供的 Computers、Members/Agent detail、Channel 三张参考图
- 上位规格：[design.md](./design.md) 第 4、8、9、10、11、12、22 节
- 适用范围：Sumi v1 Browser WebUI

本文把参考图的视觉语言、信息密度和页面组织转成 Sumi 可执行的 UI 规格。参考图是风格与布局母版，不是功能清单；所有领域名词、权限、数据和交互以 `design.md` 与 `GLOSSARY.md` 为准。

## 1. 采用范围

### 1.1 直接采用

- 高信息密度的桌面控制室布局：窄 Space rail、上下文导航、主内容区，按需增加右侧 Thread pane。
- 黑色硬边框、方形控件、极小圆角、扁平色块、像素头像和明显选中态。
- 页面不依赖悬浮卡片；层级主要由分隔线、留白、字体和色块建立。
- 列表与详情联动：左侧选择实体，右侧原位展示详情，不额外跳入层层嵌套的卡片页。
- 标题栏、分段导航、详情 section、行式列表和底部 Composer 的紧凑节奏。
- 在线状态、权限、Driver、运行状态使用短标签与“图形/文字”双重表达。
- 主内容大面积保持安静的浅色背景，强调色只用于当前上下文、主要动作和关键状态。

### 1.2 按 Sumi 产品模型替换

| 参考图元素 | Sumi 实现 |
| --- | --- |
| 左上角 `S` 标志 | 当前 Space 的 Sumi 像素徽标 |
| 固定黄色 rail | `Space.accent`；默认值为 `sun`，因此默认观感仍接近参考图 |
| 粉色 Channel 选中行 | 当前 Space accent 的实体选中态；pink 仅用于 mention、重要 Inbox 等语义状态 |
| Agents/Humans 两套身份区 | 统一 Members 列表，可按 Human/Agent 筛选；二者不拆成两套产品层级 |
| Agent 的 Runtime/Model 标签 | Sumi 的 Driver、状态、Computer、Access Level 和 Role |
| 内联回复堆叠 | 主时间线只显示 Thread summary；Thread replies 在桌面右侧 pane、移动端全屏层展示 |
| 参考图中的品牌、图标顺序与完整配色 | 使用 Sumi 品牌、导航顺序和 tokens，不做逐像素复刻 |

### 1.3 明确排除

以下元素即使出现在参考图中，也不得实现、占位或伪装可用：

- Tasks 标签、Task 列表、`As Task`、任务/看板入口以及任何 Work/Task 领域 UI。
- Joint Channels、跨 Space Channel。
- `Chat / Tasks / Files` 标签组合。Channel 主时间线不再套一层 `Chat` 标签；Attachment 作为 Message 内容呈现，不建立 Files 产品域。
- Reminders、Apps、MCP 等 v1 未定义的 Agent 详情标签。
- 无后端能力的 Saved、Pinned、收藏和全局搜索。搜索若因布局验证临时出现，必须是明确 disabled 的开发占位，交付时移除。
- Pi、Kimi Code、Claude 等未实现 Driver 的可选按钮。能力探测可以显示不可用原因，但不能让用户误以为可创建。

## 2. 视觉语言

Sumi 的风格是安静、高密度的 Neo-Brutalist 协作控制室。它应该有工具感和人格，不应像营销落地页、传统企业后台或套壳聊天机器人。

### 2.1 色彩 tokens

使用 `design.md` 的唯一 palette，不另建一套近似色：

| Token | Value | 用途 |
| --- | --- | --- |
| `ink` | `#141111` | 主文字、图标、边框、focus ring |
| `paper` | `#FFFFFF` | 主内容背景、输入面 |
| `panel` | `#FFFAEF` | 上下文导航、次级区域、空状态底色 |
| `accent` | `#FE7DA8` | 当前选择、主要动作和 focus |
| `accent-soft` | `#FFE0EA` | 轻量选择、信息提示 |
| `cyan` | `#27CCF3` | Attachment、Computer 的技术语义 |
| `green` | `#A9D877` | online、成功、active |
| `yellow` | `#FFD440` | Agent busy（queued/running）和 rail |
| `red` | `#F97264` | error、destructive、dead |

视觉收敛为白色纸面、cream panel、深墨色和受控 palette。Space accent 使用 pink、cyan、yellow、green 四个预设之一，并真实作用于 Space rail、选中态与主要动作；同一视图中大面积 accent 面不超过两个。不可用状态使用 `panel`、低对比文字和明确文案，不只靠降低透明度。

禁止渐变、玻璃拟态、模糊、柔和投影、bokeh 和纯装饰纹理。

### 2.2 边框、阴影与圆角

- 页面主分隔线：`2px solid ink`。
- 控件和实体边界：`2px solid ink`。
- 次级列表分隔线：`1px solid color-mix(in srgb, ink 18%, transparent)`；不允许每行都套卡片。
- 圆角：默认 `0`，输入框与长文本区最多 `4px`。
- 主要按钮、当前 rail 图标和当前列表项可使用 `3px 3px 0 ink` 的硬偏移阴影。
- pressed 状态向右下移动 `2px` 并收回阴影；disabled 状态不响应位移。
- 不使用柔和 box-shadow。

### 2.3 排版

- UI 与正文：`Space Grotesk, Noto Sans SC, sans-serif`。
- 地址、时间、版本、计数和技术元数据继续使用 Space Grotesk，并启用 tabular numbers。
- 正文绝不使用像素字体；像素感来自头像、徽标和硬边界。
- 页面标题：18px / 700。
- 实体标题：16–17px / 700。
- Message 正文：14px / 1.55。
- 普通 UI：13–14px / 1.4。
- section eyebrow：11px / 700 / uppercase，允许 `0.06em` 字距。
- 辅助文字：12px；不得低于 11px。
- 中英文混排不强制全大写；领域名词保持 `GLOSSARY.md` 拼写。

### 2.4 图标与像素头像

- 使用统一的 outline 图标集，视觉尺寸 18–20px，描边约 2px。
- 图标按钮通常为 36x36px；窄 rail 中为 40x40px；命中区不得小于 40x40px。
- 所有图标按钮必须有 accessible name；hover/focus 显示 tooltip。
- Human avatar 使用 display name 首个 Unicode 字符，member ID 决定受控背景色；同首字母的 Humans 通过颜色区分。
- Agent avatar 使用 8x8 对称像素印章，不出现脸、人物或机器人轮廓；member ID 从少量受控骨架与双色 palette 中稳定选择，不生成散乱噪点。
- Agent 与 Human 使用相同尺寸和硬边外框；Agent 名字旁仍增加紧凑 `AGENT` 标签。
- Agent 头像右下角允许叠加 10px activity 点：busy yellow、idle green、offline gray、error red；点具有 2px ink 外框，必须同时提供可见文字或 accessible name。busy 只做低幅阶梯脉冲，`prefers-reduced-motion` 下完全静止。
- Computer 使用显示器像素图标，不与 Member avatar 混用。

## 3. 全局 Shell

### 3.1 桌面结构

```text
+--------+----------------------+--------------------------------+------------------+
| Space  | contextual nav       | primary content                | Thread (optional)|
| rail   | Channels / Members / | page header                    |                  |
|        | Computers / Inbox    | body                           | root + replies   |
|        |                      | composer when applicable       | reply composer   |
+--------+----------------------+--------------------------------+------------------+
```

稳定尺寸：

- Space rail：56px。
- contextual nav：260px；可折叠为 72px。
- primary content：`min-width: 480px`，占据剩余空间。
- Thread pane：默认 360px，可调整到 480px；未打开时完全不存在。
- 顶部 page header：64px。
- 左侧 contextual nav header：64px，并与主 header 共用水平基线。
- Composer：最小 88px，最大 240px 后内部滚动。

所有主列之间使用连续 2px 竖向边框。滚动责任必须清楚：rail 固定，contextual nav 独立滚动，主内容独立滚动，Thread pane 独立滚动；Composer 与所在 pane 的 header 固定。

### 3.2 Space rail

- 顶部为当前 Space 像素徽标，使用 `Space.accent` 填充；选中态带硬偏移阴影。
- 中部只放 Space 级入口：Conversation、Inbox、Members、Computers。
- 底部放当前 Human 入口和 Space Settings。
- 图标顺序固定，不因某页面是否有数据而跳动。
- 未读以带数字的 badge 或图标旁实心标记表达，不能只用红点。
- 不放 Task、活动流、全局搜索等范围外入口。

### 3.3 Contextual navigation

左栏随 rail 当前入口改变内容，但尺寸、标题基线和选中态保持一致：

- Conversation：Inbox、Channels、DMs 和创建/发现 Channel 操作。
- Members：统一 Members 列表、Human/Agent 筛选、Invite Human 与 Create Agent。
- Computers：Computer 列表和 Pair Computer。
- Inbox：Approvals、DM/mention、replies、Channel activity 分组。

列表规则：

- section header 使用 disclosure icon、uppercase 名称、数量和右侧动作。
- 行高 44–56px；带描述的 Member/Computer 行可到 64px。
- 当前项使用 accent 填充、2px 边框和 3px 硬阴影；不使用参考图固定粉色。
- hover 只改变浅背景或显示行内操作，不能造成布局位移。
- 超长名称单行截断，完整值通过 tooltip/accessibility name 提供。
- online/offline 必须同时显示状态点和文字；列表窄时允许仅保留状态点，但 accessible name 仍包含文字。

### 3.4 Page header

- 左侧为页面图标色块、主标题和可选的一行摘要/状态。
- 右侧只放当前页面真实可用的全局动作。
- icon tile 为 40–48px 方形、2px 边框；其填充优先使用 accent，Computer 可使用 cyan。
- 主标题与 detail body 的实体名称一致，不制造第二种命名。
- header 底部使用连续 2px 分隔线。

## 4. 通用组件

### 4.1 Button

| Variant | 表现 |
| --- | --- |
| Primary | accent 填充、2px ink 边框、3px 硬阴影、700 字重 |
| Secondary | paper 填充、2px ink 边框、2px 硬阴影 |
| Quiet | 无外框，hover/focus 时出现 panel 背景；只用于低风险行内动作 |
| Destructive | red 填充、2px ink 边框；确认文案必须写明对象和后果 |
| Disabled | panel 填充、低对比文字、无阴影，并带不可用原因 |

按钮只使用三档视觉高度：紧凑 `30px`、默认 `36px`、主要表单动作 `42px`；同一工具栏不得混用高度。水平 padding 分别为 `8px / 12px / 16px`，图标为 `14px / 16px / 18px`。移动端命中区至少 44px，可通过伪元素扩展而不改变视觉高度。按钮必须使用动词，如 `Pair Computer`、`Create Agent`、`Send`，避免只有含糊图标。

Member 权限使用 36px 高的矩形 toggle：未选中为 paper，选中为 accent-soft 并显示 check；disabled 保留边框和明确文字，不使用浏览器默认 checkbox 的灰化小方块。Access Level select 与相邻 toggle 同高。

### 4.2 Tag 与状态

- Tag 为 24–28px 高的矩形色块，1–2px ink 边框，字体 12–13px / 700。
- `AGENT`、Access Level、Driver 是分类标签；online/offline/running 是状态，不得混成同一颜色语义。
- idle/online：green 点 + `Idle`/`Online`；busy：yellow 点 + `Busy`；offline：灰点 + `Offline`；error：red 点/图形 + `Error`。
- 颜色不能单独承载含义。

### 4.3 Section 与详情字段

- 详情页面按横向 section 分割，不把每个字段做成卡片。
- section 上下 padding 24–32px，左右与 page header 对齐，默认 28px。
- section title 使用 eyebrow 样式；可编辑时紧跟 pencil icon button。
- label 使用 13px 次级色；value 使用 15–16px，技术值使用 mono。
- 空值使用明确的斜体辅助文案，如 `No description`；不显示 `--`。
- 多个短字段优先水平排列，窄屏再换行。

### 4.4 Entity row

- avatar/icon、主名称、次要描述和尾部状态沿同一基线组织。
- 行本身保持扁平；仅当前选择项和需要整行操作的对象加完整边框。
- 可点击行具有完整键盘 focus，内部次级按钮不劫持整行 click。

### 4.5 空态、错误与 loading

- 空态直接出现在其所属 section 中，以一句原因和至多一个下一步动作结束，不使用大插画。
- loading 使用固定尺寸 skeleton，避免列宽和 header 跳动。
- 页面级错误保留已有导航和实体上下文，错误文案下面提供 `Retry`。
- Computer offline 等阻断状态用整宽 inline notice，写明原因与受影响能力；不得只弹 toast。
- toast 只反馈短期操作结果，不能承载唯一错误解释。

## 5. Channel 与 DM

### 5.1 Conversation navigation

只包含：

1. Inbox。
2. Channels：public/private Channel；header 提供排序/发现和创建操作。
3. DMs：Human 与 Agent 混合排列。

不显示 Saved、Pinned、Joint Channels。Channel 行以 `#` 开头，private Channel 使用 lock 图标；DM 行必须外露 Member pixel avatar、名称、`@handle` 与状态，Agent avatar 叠加 activity 点。当前 Channel/DM 使用统一 accent 选中态。

### 5.2 Channel header

- 左侧：`#channel-slug`、topic。
- 中部或标题下沿：Member strip，显示当前在线 Members 的像素头像和简短状态；溢出显示 `+N`。
- Member strip 中头像不得相互遮挡；Agent activity 点必须完整外露，不能被相邻头像覆盖。
- 右侧：真实可用的 Channel members、设置等操作。
- 有管理权限时右侧提供紧凑的 `+`，打开单层 popup，把尚未加入的 active Agents 添加到当前 Channel；popup 中显示 avatar、name、handle 和状态，不跳转到独立管理页。
- v1 没有可用搜索时不显示搜索按钮；不要摆一个看起来能点的假入口。
- header 下方直接进入 Message timeline，不添加 `Chat / Tasks / Files` tabs。

### 5.3 Message timeline

Message 使用无气泡行布局：

```text
[avatar]  Display Name  [AGENT]  09:08
          Markdown body with links and @mentions
          [Attachment rows]
          [up to 3 compact recent replies]
          12 replies · open full Thread
```

- avatar 列固定 40px；正文列保持可读宽度，但不强制居中成窄文章栏。
- 连续同作者且间隔较短的 Message 可以压缩重复头像与姓名，但键盘定位和 screen reader label 仍逐条完整。
- 日期分隔为两侧 1px 线和居中 mono eyebrow 文本。
- Markdown 标题、列表、引用、代码块不得打破时间线宽度；长 URL 和代码允许换行或内部横向滚动。
- mention 使用 pink 的浅色底/下划线语义，不把整条 Message 染粉。
- Attachment 使用 cyan accent、文件名、大小、媒体类型和下载动作。
- hover/focus 后显示 reply、copy link、more；触屏设备通过显式 more 按钮访问。
- 有回复的 Thread 在主时间线内嵌最多 3 条最近 reply，使用紧凑头像、作者、时间和单行/双行摘要；预览整体和剩余 reply 数量都可打开完整 Thread pane。无回复时仅在 hover/focus 显示 `Reply in Thread`。
- Agent 处理状态只呈现可验证动作，例如 `Lin is reading 3 Inbox items` 或 `Lin is using Codex`，不得展示或伪造思维链。

### 5.4 Composer

- 底部固定的 2px 边框区域，包含 Markdown textarea、Attachment 按钮、mention autocomplete 和 Send。
- placeholder 使用 `Message #channel` 或 `Message @member`。
- textarea 从一行自然增长至 240px，之后内部滚动；`Shift+Enter` 换行，发送快捷键必须在 tooltip/help 中说明。
- Attachment 上传中、失败、ready 状态在输入区内以行式 chip 呈现。
- 输入 `@` 后在 Composer 上方显示紧凑 autocomplete popup，只列当前 Channel Members；支持键盘选择并在候选中明确标记 `AGENT`。
- Send 是可辨识按钮，不只显示低对比纸飞机。
- 不显示 GIF、`As Task` 或其它未实现能力。

### 5.5 Thread pane

- 桌面端从右侧加入 360–480px pane，不覆盖、不压成不可读的 Channel 主区。
- pane header 显示 `Thread`、地址 `#channel:id`、follow 状态和关闭按钮。
- root Message 平铺显示；下方由分隔线进入 replies，底部使用独立 reply Composer。
- 阅读期间主时间线有新 Message 时，显示紧凑的 `Channel has new messages` 返回入口。
- 低于 700px 时 Thread 为全屏层，返回后恢复 Channel 的选择与滚动位置。

## 6. Members 与 Agent detail

### 6.1 Members navigation

- 标题为 `Members`，header 提供 Graph/列表切换的前提是 Graph 真实实现；v1 默认只提供列表。
- 所有 Human 与 Agent 位于同一 `MEMBERS` section，可通过 `All / Human / Agent` 筛选。
- 行包含 pixel avatar、display name、Agent 的 `AGENT` 标签、Role/描述摘要、在线状态。
- 不按 `AGENTS` / `HUMANS` 拆成身份等级，也不把 Agent 藏在 Computer 下。
- Computer 只作为 Agent 的次级 metadata 或过滤条件。
- 有权限的 Human 在左栏 header 获得紧凑的 `Invite` 和 `+ Agent` 操作，accessible name 使用完整的 `Invite Human` 和 `Create Agent`；不能只在 Computer 详情里创建 Agent。

### 6.2 Agent detail header

沿用参考图的紧凑 identity header：

- 48px pixel avatar。
- display name、`AGENT` 标签、online/offline/queued/running 状态。
- `@handle` 和 Role 摘要。
- 右侧只显示真实可用的 Message/DM、暂停/恢复和 more 操作。

### 6.3 Agent detail tabs

只使用 v1 已定义的内容：

| Tab | 内容 |
| --- | --- |
| Overview | identity、description、Access Level、Computer、Driver、创建信息、运行状态 |
| Memory | 文件名、大小、更新时间；权限和 Computer online 条件按 `design.md` 执行 |
| Inbox | pending 数量、最近失败、最后处理时间；正文可见性按授权限制 |
| Settings | Role、attention config、Driver 切换、暂停、恢复、重试、退役 |

Activity 只有在真实运行/审计数据形成产品闭环后才加入。不得照搬 Workspace、Reminders、Chat、Apps、MCP 标签。

### 6.4 Agent Overview

详情 section 顺序：

1. `IDENTITY`：Display Name、handle、description。
2. `ACCESS`：Access Level 与显式 permissions。Role 不得显示成 Admin/Member。
3. `RUNTIME`：Computer、Computer 状态、Driver、Agent 状态、Role revision。
4. `CREATED BY`：creator avatar/name 与 created time。
5. `CREATED AGENTS`：仅当后端能返回由该 Agent 发起并已审批的 Agents 时展示，否则整节不出现。

Computer offline 导致 Memory/操作不可用时，在相关内容区显示整宽 notice：`Computer is offline. Memory and runtime actions are unavailable.`，并提供真实可用的 Retry；不在整个页面底部留巨大空白。

## 7. Computers 与 Computer detail

### 7.1 Computers navigation

- 桌面端固定使用 Space rail、Computer list、Computer detail 三栏；Computer detail header 与左侧 `Computers` header 共享同一水平基线，主区不得再叠一层重复的页面标题。
- 标题为 `Computers`，section header 显示数量和 `Pair Computer` 动作。
- 行包含方形 Computer icon、name、online/offline 状态和一行关键 metadata；deleted tombstone 不出现在普通列表。
- online 行可显示 daemon version；offline 行显示 `Last seen …` 或明确 `Computer offline`。
- 当前项使用 accent 填充；状态点保留自己的 green/gray/red 语义，不跟随选中背景改变。

### 7.2 Computer detail header

- Computer icon tile、name、状态文字、hostname。
- online 使用 green 点 + `Connected`/`Online`；offline 使用灰点 + `Offline`。
- 顶部只放日常状态与低风险动作；`Delete Computer` 位于底部 Danger Zone，危险动作不得只用无文案图标。

### 7.3 Computer detail body

section 顺序：

1. `NAME`：可编辑 Computer name。
2. `DESCRIPTION`：若产品尚无该字段则整节不显示，不能做一个保存不了的铅笔按钮。
3. `INFO`：OS/architecture、daemon version、hostname、last seen、created time。
4. `CAPABILITIES`：只显示 daemon 实际验证过的 Driver 和隔离能力；可用项用 cyan/green tag，不可用项以文字说明原因，不能伪装可选。
5. `AGENTS ON THIS COMPUTER`：Agent rows，包含 avatar、name、Driver 和状态；提供有权限时的 `Create Agent`。
6. `AGENT HOMES`：仅展示 Server 持有的 Memory/workspace 元数据与可验证状态，不把本地路径或目录正文泄露给无权限 Member。

Agent rows 使用 2px 外框的扁平列表，与参考图相同；尾部状态右对齐。列表选择模式只有在存在真实批量操作时才出现，不能长期摆一个无作用的 `Select`。

### 7.4 Delete 交互

- 点击 `Delete Computer` 后先展示受影响 Agents，并要求显式确认这些 Agents 将被退役。
- 确认按钮使用 destructive variant，并写明 Computer name。
- 删除事务取消 active runs、退役承载的 Agents 并撤销 Computer Token；历史协作身份与内容保留。
- 成功后 Computer 从普通列表和 detail 消失，不提供恢复入口；在线 daemon graceful shutdown，离线 daemon 下次连接后退出。

## 8. Inbox 与治理页

- 沿用相同 list/detail shell，不建立另一套 Dashboard 视觉。
- Human Inbox 顺序固定为 Approvals、DM/mention、replies、Channel activity。
- Inbox 行包含来源地址、sender avatar、摘要、时间和 priority 文案。
- Approvals 详情清楚区分申请者、目标 Computer、Driver、权限和 approve/reject 后果。
- error/dead 项不得展示 private Channel 正文或敏感内容。
- 空 Inbox 使用扁平解释区：`Nothing needs your attention`、一句说明和四条 `0` 计数分组（Approvals、DM & mentions、Replies、Channel activity）。它必须说明 Inbox 是显式待处理入口而非消息历史，不制造示例数据或营销插画。
- Settings 与配对确认等治理表单继续使用 section、label/value、硬边框控件，不改成浮动卡片页。

## 9. 表单与编辑

- 简短字段可在 detail section 原位编辑；复杂配置进入同一主内容区的专用表单。
- Create Agent 使用单层 modal：桌面宽 520px、字段高 36px、Role 初始高 88px、底部固定动作区；从 Computer detail 打开时预选当前 Computer。其它 modal 只用于必须阻断的确认，例如 retire、删除和 cancel active run。
- Create Channel 使用单层 modal：桌面宽 520px，包含 name、slug、visibility、topic 与可多选的初始 Agents；左栏 `+` 只负责打开 modal，不在导航内展开表单。
- label 始终可见，不以 placeholder 代替。
- 输入校验靠近字段显示，并在提交失败时保留用户输入。
- WebUI 不提供模型 API key 或其他 Driver Secret 输入；Driver 认证只在 Computer 本地配置。
- 异步提交期间按钮显示动词进行态，如 `Pairing…`、`Creating…`，并防止重复提交。

## 10. 响应式

### 10.1 1100px 及以上

- 完整 Space rail、contextual nav 和主内容。
- Thread 打开时并列显示；空间不足时先收窄/折叠 contextual nav，再保证主内容至少 480px。

### 10.2 700–1099px

- Space rail 保留；contextual nav 变为由 header 按钮打开的抽屉。
- 抽屉宽度不超过 viewport 的 80%，打开时 focus trap，关闭后焦点回到触发按钮。
- Thread 覆盖主内容并保留 Space rail，使用明确的返回 Channel 动作；不允许把 Channel 挤成窄条。

### 10.3 低于 700px

- 单主列。Space rail 与 contextual nav 收入导航抽屉；顶部保留当前上下文和菜单按钮。
- Thread、Member detail、Computer detail 均为带明确 Back 的全屏路由/层。
- Composer、底部动作与抽屉考虑 `env(safe-area-inset-*)`。
- 主要操作命中区至少 44x44px。
- 返回后恢复列表选择、滚动位置和未提交 Composer 草稿。

## 11. Accessibility 与交互状态

- 所有功能可由键盘完成；focus 使用 `2px ink` 外框加 accent offset，不移除浏览器 focus 而不给替代。
- 颜色不是权限、状态、错误或选择的唯一表达；使用图形、文字、边框中的至少两种。
- 页面、nav、list、tab、dialog、status 使用正确语义；不要给普通 `div` 堆 ARIA 模拟控件。
- 动态 online/run/上传状态在必要时使用低打扰 live region，不逐条朗读 SSE 噪声。
- `prefers-reduced-motion` 下取消位移动画，只保留即时状态变化。
- tooltip 不能承载完成任务所必需的唯一信息。
- 头像 `alt` 不重复相邻可见姓名；装饰图标对 screen reader 隐藏。
- 超长 Space、Channel、Member、Computer 名称不能遮挡固定操作。

## 12. 验收清单

### 12.1 视觉

- [ ] 1440x900 下 Space rail、contextual nav、主内容和可选 Thread pane 边界清楚。
- [ ] 使用 `ink/paper/panel/Space.accent` 建立层级，无渐变、玻璃、软阴影或卡片海。
- [ ] current rail item、current entity、primary action 不会同时大面积争抢注意力。
- [ ] Human 与 Agent 使用相同 Message 和 Member 行布局，Agent 仅增加小标签。
- [ ] pixel art avatar 在 1x/2x/高 DPR 下保持清晰硬边。
- [ ] 页面没有参考产品品牌、固定黄色+固定粉色的完整色板或相同导航顺序。

### 12.2 功能呈现

- [ ] Channel 页面没有 `Chat / Tasks / Files` tabs、`As Task` 或 Task 入口。
- [ ] Members 是统一列表，Create Agent 与 Invite Human 对有权限 Human 可发现。
- [ ] Computer detail 清晰展示状态、信息、能力和承载 Agents。
- [ ] Agent detail 只出现 Overview、Memory、Inbox、Settings 等已实现内容。
- [ ] offline/error/loading/empty 状态都能解释原因和下一步，deleted Computer 不残留在普通页面。
- [ ] 不可用能力不会以可点击控件伪装。

### 12.3 响应式与无障碍

- [ ] 1024x768 下主内容不低于 480px，导航抽屉和 Thread 策略正确。
- [ ] 390x844 下单列可完成 Channel、Thread、Member、Computer 和 Composer 主路径。
- [ ] 键盘可以访问全部交互，focus 可见，icon buttons 有 accessible name。
- [ ] reduced motion、safe-area、长名称、长 Message、长 URL 和 Attachment 不破版。
- [ ] screen reader 能获得 Member kind、online 状态、Agent run 状态和表单错误。

## 13. 实现纪律

- 本文件定义视觉与页面呈现，不改变 `design.md` 的领域行为、权限和 API。
- 组件抽象以重复交互为依据：Button、Tag、Status、Avatar、EntityRow、Section、Composer、SplitShell；不为了“设计系统完整”提前造大而全组件库。
- 页面不得硬编码参考截图里的名称、数量、日期、Driver 或状态。
- 后端没有的数据不制造静态假数据；后端没有的能力不制造可点击入口。
- 每个页面先完成 loading、empty、error、offline 和 permission denied，再视为视觉完成。
- UI 里统一使用 Member、Human、Agent、Space、Channel、Thread、Message、Attachment、Computer、Driver、Role、Access Level 等规范词汇。
