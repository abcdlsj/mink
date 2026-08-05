# Demo 样本集

设计验收用 demo：固定账号、固定空间、固定样本，覆盖所有关键视觉状态。

## 样本内容

- `#general`：普通消息、Markdown 全形态、mention、Task 引用、附件、System Notice。
- `#design-lab`：为设计评审准备的讨论样本（Root + Thread + Task 标识）。
- `#empty-lab`：空态样本。
- Tasks 页面：TODO / In Progress / In Review / Done / Closed 各一条，含 Source Thread、Related Thread、Result、Close reason。
- Inbox：第二个 Human（Mara）制造的 DM、mention、reply 未读聚合。
- Members / Agents / Computers：Agent 状态、权限、生命周期样本。
- Action Message：channel_created、agent_created。
- Thread pane：root + replies + Task 标识。
- Run 与 Activity：In Review / Done 两个样本由真实 Agent Run 驱动（Agent 在 Source Thread 收到 mention 后用 CLI 提交复核），所以 Tasks 详情和 Agent Activity 里有真实 run 历史。

## 脚本

- `seed-samples.mjs`：通过 HTTP API 幂等创建样本（可重复运行）。
- `start.sh`：一键起 server + Vite + dev-seed + 样本集。
- `shots.mjs`：对关键页面和视口截图到 `demo/.runtime/shots/`。

## 运行说明

- 首次启动的样本集包含真实 Agent Run（提交复核），需要 Computer daemon 在线，等待约 2-8 分钟；后续重复运行是幂等的，只补齐缺失样本。
- 样本脚本会在 Agent 驱动前取消残留的 active Run，避免旧 Run 占用 Agent。
- `#general` 的 agent 提及样本是纯文本，不触发 Run，避免启动时抢占 Agent。
- 已知现象：builtin Agent 摸索 CLI 会多次重试并产生多个 Run；这是真实产品行为，不是样本集缺陷。

## 运行产物

`demo/.runtime/` 存放数据库之外的全部本地状态（Computer 状态、截图、日志），已 gitignore。
