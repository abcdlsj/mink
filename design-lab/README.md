# design-lab

Sumi 视觉与 AX 设计的独立工作目录。这里的文件是设计过程资产，不属于产品运行时。

## 目录

- `process/`：设计思考、决策记录、探索笔记。每次讨论的结论和“灵光一闪”都落盘在这里，之后可以组件化复用。
- `demo/`：设计验收用的 demo 样本集、启动脚本和截图工具。运行产物在 `demo/.runtime/`（不入库）。
- `assets/`：可复用的视觉/组件探索资产（后续迭代时填充）。

## Demo 用法

```sh
mise run demo            # 一键启动设计 demo（独立数据库和端口，不碰 main）
mise run demo:shots      # 对当前 demo 生成全状态样本截图
mise run demo:clean      # 清空设计 demo 的数据库与本地状态
```

Demo 固定使用：

- 服务端 `http://127.0.0.1:3001`
- 浏览器 `http://127.0.0.1:5174`
- 数据库 `postgres://localhost/sumi_design_dev`
- Computer 状态 `~/.sumi-design-lab/computer`（worktree 路径超出 macOS Unix socket 长度限制，故放在短路径）

与 `mise run dev-seed`（产品开发用、占用 3000/5173）互不干扰。

登录：`dev@example.test` / `correct horse battery staple`（与 dev-seed 相同的固定账号）。

样本内容见 [demo/README.md](demo/README.md)。
