# Mink 项目文档

## 项目概述

**Mink** 是一个用 Go 编写的极简主义 AI 编码代理，设计哲学强调美学、效率和可扩展性。

### 核心特性

- **🦦 极简核心**: 仅4个基础工具 (read, write, edit, bash)
- **🌳 树形会话**: 支持分支和压缩的会话管理
- **🤖 多代理协作**: 内置 spawn 和 background 工具
- **💬 多平台支持**: CLI TUI 和 Telegram Bot
- **📊 Token 估算**: 精确的用量统计和自动压缩
- **⚡ 流式响应**: 实时显示 AI 回复

---

## 架构设计

### 整体架构

```
┌─────────────────────────────────────────────────────────────┐
│                         Main                                │
└──────────────────────┬──────────────────────────────────────┘
                       │
       ┌───────────────┼───────────────┐
       ▼               ▼               ▼
┌────────────┐  ┌────────────┐  ┌────────────┐
│   CLI      │  │  Telegram  │  │  Hook      │
│ Platform   │  │  Platform  │  │  Manager   │
└─────┬──────┘  └─────┬──────┘  └─────┬──────┘
      │               │               │
      └───────────────┼───────────────┘
                      ▼
              ┌──────────────┐
              │  Event Bus   │
              │   (bus/)     │
              └──────┬───────┘
                     │
        ┌────────────┼────────────┐
        ▼            ▼            ▼
   ┌─────────┐  ┌─────────┐  ┌─────────┐
   │Dispatcher│  │Supervisor│  │  Agent  │
   └────┬────┘  └────┬────┘  └────┬────┘
        │            │            │
        └────────────┼────────────┘
                     ▼
              ┌──────────────┐
              │  ReAct Loop  │
              └──────┬───────┘
                     │
        ┌────────────┼────────────┐
        ▼            ▼            ▼
   ┌─────────┐  ┌─────────┐  ┌─────────┐
   │   LLM   │  │  Tools  │  │ Session │
   │Provider │  │Registry │  │ Manager │
   └─────────┘  └─────────┘  └─────────┘
```

### 核心组件

#### 1. Agent 模块 (`agent/`)

负责 ReAct (Reasoning + Acting) 循环的实现。

| 文件 | 职责 |
|------|------|
| `agent.go` | Agent 核心结构、运行控制、Token 管理 |
| `react.go` | ReAct 循环实现、流式处理、命令解析 |
| `dispatcher.go` | 消息分发、Agent 生命周期管理 |
| `supervisor.go` | 监督模式、特殊 Agent 管理 |
| `token_estimator.go` | Token 用量估算 |

**ReAct 循环流程**:
```
User Input → LLM Call → (Response | Tool Call)
                 ↑___________|
                          (Execute Tool → Result)
```

#### 2. 消息总线 (`bus/`)

基于发布-订阅模式的异步消息系统。

```go
// 核心接口
type MessageBus interface {
    Pub(m Msg) error                    // 发布消息
    Subscribe(msgType string, ch chan Msg)   // 订阅
    Req(ctx context.Context, m Msg) (Msg, error)  // 请求-响应
    RegisterAgent(id string, shareCtx bool) *AgentConn  // 注册Agent
}
```

**消息类型**:
- `TypeUser` - 用户输入
- `TypeAssistant` - AI 回复
- `TypeToolCall` - 工具调用
- `TypeToolResult` - 工具结果
- `TypeStreamChunk` - 流式片段
- `TypeCommand` - 内部命令

#### 3. 工具系统 (`tool/`)

| 工具 | 功能 | 使用场景 |
|------|------|----------|
| `read` | 读取文件内容 | 查看代码、配置文件 |
| `write` | 写入/覆盖文件 | 创建新文件 |
| `edit` | 搜索替换编辑 | 修改现有代码 |
| `bash` | 执行 shell 命令 | 运行命令、构建 |
| `background` | 后台执行命令 | 长时间任务 |
| `spawn` | 创建子 Agent | 多任务并行 |

#### 4. LLM 提供商 (`llm/`)

统一接口支持多提供商:
- **Anthropic** (Claude)
- **OpenAI** (GPT)

```go
type Provider interface {
    Chat(ctx context.Context, msgs []msg.Message, tools []Tool) (*Response, error)
    ChatStream(ctx context.Context, msgs []msg.Message, tools []Tool) (<-chan Chunk, error)
}
```

#### 5. 平台适配器 (`platform/`)

- **CLI**: Bubble Tea TUI 实现
- **Telegram**: Bot API 实现

#### 6. 会话管理 (`session/`)

- 持久化存储 (JSONL 格式)
- 树形结构支持分支
- 自动/手动压缩

---

## 配置详解

### 配置文件结构

```toml
# 基础配置
provider = "anthropic"                    # LLM 提供商
model = "claude-sonnet-4-20250514"       # 模型名称
api_key = "sk-..."                        # API 密钥
base_url = ""                             # 自定义端点 (可选)
telegram_token = ""                       # Telegram Bot Token (可选)

# 自动压缩配置
[compact]
auto = true                               # 启用自动压缩
trigger_tokens = 12000                    # Token 阈值
trigger_messages = 80                     # 消息数阈值
keep_recent_messages = 20                 # 保留最近消息数

# 超时配置 (秒)
[timeout]
agent = 600                               # Agent 运行超时
llm = 120                                 # LLM 调用超时
tool = 60                                 # 工具执行超时
background = 300                          # 后台任务超时

# 请求头
[headers]
User-Agent = "mink-agent"
```

### 命令行参数

```bash
./mink [tg] [flags]

# 子命令
./mink tg           # 仅启动 Telegram Bot 模式

# Flags
-p, --provider    # 覆盖提供商
-m, --model       # 覆盖模型
-k, --api-key     # 覆盖 API Key
-u, --base-url    # 覆盖基础 URL
-tg, --telegram   # 覆盖 Telegram Token
-c, --compact     # 强制压缩当前会话
-t, --tokens      # 显示 Token 用量
-s, --session     # 会话管理
-h, --help        # 帮助信息
```

---

## 交互命令

### 会话管理命令

| 命令 | 功能 | 示例 |
|------|------|------|
| `/new` | 创建新会话 | `/new` |
| `/branch <name>` | 创建分支 | `/branch feature-x` |
| `/switch <id>` | 切换会话 | `/switch abc123` |
| `/compact [note]` | 手动压缩 | `/compact 总结需求` |
| `/tokens` | 查看用量 | `/tokens` |
| `/help` | 显示帮助 | `/help` |

### 代码块命令 (AI 执行)

在 AI 回复的代码块中使用 `!` 前缀:

```bash
!ls -la                    # 执行命令
!git status               # Git 操作
!compact                  # 手动压缩
!tokens                   # 查看用量
```

---

## 多代理协作

### Spawn 工具

用于委派独立子任务:

```json
{
  "task": "分析 cmd/ 目录的错误处理",
  "share_context": false
}
```

**适用场景**:
- 并行工作：同时处理多个独立任务
- 复杂任务：拆解为子任务
- 专注工作：每个 Agent 专注单一职责

### Background 工具

用于长时间运行的任务:

```json
{
  "cmd": "go build ./...",
  "cwd": "/path/to/project"
}
```

**适用场景**:
- 下载大文件
- 编译项目
- 运行测试套件
- 任何耗时操作

---

## Token 管理

### 估算策略

1. **Provider 报告**: 优先使用 LLM API 返回的实际用量
2. **Tiktoken 回退**: 使用 tiktoken-go 进行估算
3. **混合模式**: 基准值 + 增量估算

### 自动压缩触发条件

```go
// 任一条件满足即触发
Token 数 >= 12000 || 消息数 >= 80
```

### 压缩过程

1. 保留最近 N 条消息 (默认 20)
2. 对历史消息生成摘要
3. 替换为系统消息形式的摘要
4. 重置 Token 基线

---

## 开发指南

### 代码风格

遵循 Rob Pike 风格，强调:
- 短命名: `i`, `s *Session`
- 小函数: 单屏可显示
- 组合优于继承
- 立即错误处理
- 避免过度抽象

### 添加新工具

```go
type MyTool struct{}

func (t *MyTool) Name() string { return "mytool" }
func (t *MyTool) Desc() string { return "My tool description" }
func (t *MyTool) Schema() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "arg": map[string]string{"type": "string"},
        },
        "required": []string{"arg"},
    }
}
func (t *MyTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
    // 实现逻辑
    return "result", nil
}
```

### 添加新 LLM 提供商

```go
type myProvider struct {
    apiKey string
    model  string
}

func (p *myProvider) Chat(ctx context.Context, msgs []msg.Message, tools []Tool) (*Response, error) {
    // 实现调用
}

func (p *myProvider) ChatStream(ctx context.Context, msgs []msg.Message, tools []Tool) (<-chan Chunk, error) {
    // 实现流式调用
}
```

---

## 扩展机制

### 扩展目录

```
~/.mink/
├── config.toml      # 配置文件
├── sessions/        # 会话存储
├── ext/             # 可执行扩展
└── skills/          # 技能脚本
```

### 扩展加载

- 自动监视目录变化
- 热重载无需重启
- 扩展通过标准输入/输出与主程序通信

---

## 部署模式

### 1. 本地 CLI 模式

```bash
./mink
```
- 启动交互式 TUI
- 支持 CLI 和 Telegram 双平台 (如配置了 token)

### 2. Telegram Bot 模式

```bash
./mink tg
```
- 仅启动 Telegram Bot
- 适合服务器部署

### 3. 混合模式

```bash
./mink -tg <token>
```
- CLI 为主，Telegram 同时运行

---

## 故障排查

### 常见问题

| 问题 | 原因 | 解决 |
|------|------|------|
| API Key 错误 | 未配置或无效 | 检查 config.toml 或 -k 参数 |
| Token 超限 | 对话过长 | 使用 `/compact` 或自动压缩 |
| 工具超时 | 命令执行时间过长 | 使用 `background` 工具 |
| Telegram 无法连接 | 网络或 Token 问题 | 检查网络和 token |

### 日志查看

```bash
# 会话文件
ls ~/.mink/sessions/
cat ~/.mink/sessions/<session-id>.jsonl
```

---

## 许可证

MIT License - 详见 LICENSE 文件
