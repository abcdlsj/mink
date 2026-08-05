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

## 脚本

- `seed-samples.mjs`：通过 HTTP API 幂等创建样本（可重复运行）。
- `start.sh`：一键起 server + Vite + dev-seed + 样本集。
- `shots.mjs`：对关键页面和视口截图到 `demo/.runtime/shots/`。

## 运行产物

`demo/.runtime/` 存放数据库之外的全部本地状态（Computer 状态、截图、日志），已 gitignore。
