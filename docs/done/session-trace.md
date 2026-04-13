# Session Operation Log 设计文档

## 背景与问题

目前 Mink 的调试能力存在明显短板：

1. **日志碎片化** - 只有 `_req.jsonl` 和 `_resp.jsonl`，中间过程完全黑盒
2. **缺少因果链** - 无法还原 "为什么调用了这个工具"、"LLM 是怎么思考的"
3. **难以自动化分析** - 现有格式不统一，工具调用和响应分散在不同地方
4. **Telegram 场景 debug 困难** - 异步、流式输出，无法追踪完整执行路径
5. **无法回放** - 现有日志不完整，无法基于日志还原完整执行过程

## 设计目标

构建一套 **Operation Log（操作日志）** 机制，核心是 **全集不丢，可回放**：

1. **完整可还原** - 一次会话的所有操作100%记录，毫秒级时间线，可完整回放
2. **结构化统一** - 所有事件统一 NDJSON 格式，便于程序化解析
3. **低开销** - 不影响正常执行性能，但绝不采样/丢弃
4. **持久化存储** - 按 session 存储，支持长期保留

> **核心理念**：这是 Operation Log，不是传统 Trace。传统 Trace 强调采样和因果链，这里强调全集和回放。

## 核心概念

### Event 事件类型

```go
type EventType string

const (
    // 生命周期事件
    EventSessionStart   EventType = "session_start"
    EventSessionEnd     EventType = "session_end"
    EventStepStart      EventType = "step_start"
    EventStepEnd        EventType = "step_end"

    // LLM 交互事件
    EventLLMRequest     EventType = "llm_request"
    EventLLMResponse    EventType = "llm_response"
    EventLLMStreamChunk EventType = "llm_stream_chunk"
    EventLLMThinking    EventType = "llm_thinking"

    // 工具执行事件
    EventToolCall       EventType = "tool_call"
    EventToolStart      EventType = "tool_start"
    EventToolEnd        EventType = "tool_end"
    EventToolError      EventType = "tool_error"

    // 内部逻辑事件
    EventPromptBuild    EventType = "prompt_build"
    EventHookTrigger    EventType = "hook_trigger"
    EventContextUpdate  EventType = "context_update"
    EventTokenCalc      EventType = "token_calc"

    // 系统事件
    EventInterrupt      EventType = "interrupt"
    EventTimeout        EventType = "timeout"
    EventCompact        EventType = "compact"

    // 子代理事件
    EventSpawnStart     EventType = "spawn_start"
    EventSpawnEnd       EventType = "spawn_end"

    // 后台任务事件
    EventBgStart        EventType = "bg_start"
    EventBgEnd          EventType = "bg_end"

    // 输入输出事件
    EventUserInput      EventType = "user_input"
    EventAgentOutput    EventType = "agent_output"
)
```

### Event 事件结构

```go
type Event struct {
    ID            string         `json:"id"`
    Type          EventType      `json:"type"`
    Timestamp     time.Time      `json:"timestamp"`
    SessionID     string         `json:"session_id"`
    StepNum       int            `json:"step_num,omitempty"`
    ParentID      string         `json:"parent_id,omitempty"`
    CorrelationID string         `json:"correlation_id,omitempty"`
    Data          json.RawMessage `json:"data"`
    DurationMs    int64          `json:"duration_ms,omitempty"`
    Source        string         `json:"source,omitempty"`
    AgentID       string         `json:"agent_id,omitempty"`
    Level         EventLevel     `json:"level,omitempty"`
}

type EventLevel string

const (
    LevelDebug EventLevel = "debug"
    LevelInfo  EventLevel = "info"
    LevelWarn  EventLevel = "warn"
    LevelError EventLevel = "error"
)
```

### CorrelationID 规范

每个需要配对的 Event（request/response, start/end）使用相同的 `correlation_id` 关联：

```
correlation_id 生成规则：
1. LLM 请求：以 "req_" 前缀 + UUID:  req_abc123
2. 工具调用：以 "tool_" 前缀 + UUID:  tool_xyz789
3. 子代理：以 "spawn_" 前缀 + UUID:  spawn_aaa111
4. 后台任务：以 "bg_" 前缀 + UUID:   bg_bbb222
```

### 各事件类型的数据结构

#### LLM 请求/响应

```go
type LLMRequestData struct {
    Messages     []msg.Message   `json:"messages"`
    System       string          `json:"system"`
    Tools        []string        `json:"tools"`
    Stream       bool            `json:"stream"`
    Model        string          `json:"model"`
}

type LLMResponseData struct {
    Content            string          `json:"content"`
    Reasoning          string          `json:"reasoning,omitempty"`
    ReasoningSignature string          `json:"reasoning_signature,omitempty"`
    ToolCalls          []msg.ToolCall  `json:"tool_calls,omitempty"`
    Usage              *llm.TokenUsage `json:"usage,omitempty"`
    FinishReason       string          `json:"finish_reason"`
}
```

#### 工具执行

```go
type ToolCallData struct {
    ToolCallID string          `json:"tool_call_id"`
    Name       string          `json:"name"`
    Args       json.RawMessage `json:"args"`
}

type ToolStartData struct {
    ToolCallID string `json:"tool_call_id"`
    Name       string `json:"name"`
    Args       string `json:"args"`
}

type ToolEndData struct {
    ToolCallID string `json:"tool_call_id"`
    Name       string `json:"name"`
    Output     string `json:"output"`
    OutputSize int    `json:"output_size"`
    Error      string `json:"error,omitempty"`
    ExitCode   int    `json:"exit_code,omitempty"`
}
```

#### Prompt 构建

```go
type PromptBuildData struct {
    Source    string          `json:"source"`
    Sections  []PromptSection `json:"sections"`
    FinalSize int             `json:"final_size"`
}

type PromptSection struct {
    Name string `json:"name"`
    Size int    `json:"size"`
    Hash string `json:"hash"`
}
```

#### 用户输入/代理输出

```go
type UserInputData struct {
    Role  string `json:"role"`
    Input string `json:"input"`
}

type AgentOutputData struct {
    Content string `json:"content"`
    Stream  bool   `json:"stream"`
}
```

> **状态更新**：这份设计稿里的 `session.jsonl` / `*.log.jsonl` 方案已经废弃。当前 Mink 已改为 `SQLite` 持久化：session 元数据在 `sessions`，消息在 `session_entries`，活动回放在 `events` / `runs`。下面的 JSONL 结构仅作为历史草稿保留，不再作为实现依据。

## 存储设计

### 文件结构

```
~/.mink/sessions/
├── <session_id>.jsonl          # 消息历史（已有）
├── <session_id>_req.jsonl      # 请求日志（已有，废弃）
├── <session_id>_resp.jsonl     # 响应日志（已有，废弃）
└── <session_id>.log.jsonl     # 完整操作日志（全集不丢）
```

### 存储格式

每行一个 JSON 对象（NDJSON），确保追加原子性（每条 flush）。

示例：

```
{"id":"evt_001","type":"session_start","timestamp":"2024-01-15T10:00:00Z","session_id":"sess_abc","level":"info","data":{"source":"telegram"}}
{"id":"evt_002","type":"user_input","timestamp":"2024-01-15T10:00:00Z","session_id":"sess_abc","parent_id":"evt_001","level":"info","data":{"role":"user","input":"帮我写个函数"}}
{"id":"evt_003","type":"step_start","timestamp":"2024-01-15T10:00:00Z","session_id":"sess_abc","step_num":0,"level":"info","data":{}}
{"id":"evt_004","type":"llm_request","timestamp":"2024-01-15T10:00:00Z","session_id":"sess_abc","step_num":0,"correlation_id":"req_001","level":"debug","data":{"model":"claude-3","messages":[...]}}
{"id":"evt_005","type":"llm_response","timestamp":"2024-01-15T10:00:01Z","session_id":"sess_abc","step_num":0","correlation_id":"req_001","duration_ms":1000,"level":"debug","data":{"content":"","tool_calls":[...]}}
{"id":"evt_006","type":"tool_call","timestamp":"2024-01-15T10:00:01Z","session_id":"sess_abc","step_num":0","correlation_id":"req_001","parent_id":"evt_005","level":"info","data":{"name":"bash","args":{...}}}
{"id":"evt_007","type":"tool_start","timestamp":"2024-01-15T10:00:01Z","session_id":"sess_abc","step_num":0","correlation_id":"tool_001","parent_id":"evt_006","level":"info","data":{"name":"bash","args":"..."}}
{"id":"evt_008","type":"tool_end","timestamp":"2024-01-15T10:00:02Z","session_id":"sess_abc","step_num":0","correlation_id":"tool_001","parent_id":"evt_007","duration_ms":1000,"level":"info","data":{"output":"...","output_size":1234}}
{"id":"evt_009","type":"step_end","timestamp":"2024-01-15T10:00:02Z","session_id":"sess_abc","step_num":0","duration_ms":2000,"level":"info","data":{}}
```

### 配置

```go
type LogConfig struct {
    Enabled         bool  // 是否启用（默认 true）
    MaxFileSize     int64 // 单文件大小软限制（默认 200MB，超过则 rotate）
    MaxFiles        int   // 保留文件数（默认 10）
    LogStreamChunks bool  // 是否记录流式 chunks（默认 true）
}
```

> **注意**：Log 与 Session 消息历史独立存储，不受 compact 影响。Compact 压缩的是 `session.jsonl`，`session.log.jsonl` 永远保留完整操作记录。

## 关键实现点

### 1. Logger 接口

```go
type Logger interface {
    Log(event Event)

    SessionStart(source string)
    SessionEnd()

    StepStart(stepNum int)
    StepEnd(stepNum int, duration time.Duration)

    UserInput(role, input string)
    AgentOutput(content string, stream bool)

    LLMRequest(req LLMRequestData) string // 返回 correlation_id
    LLMResponse(correlationID string, resp LLMResponseData, duration time.Duration)

    ToolCall(tc msg.ToolCall) string // 返回 correlation_id
    ToolStart(correlationID string, name string, args string)
    ToolEnd(correlationID string, output string, exitCode int, err error, duration time.Duration)

    PromptBuild(data PromptBuildData)
    HookTrigger(name string, payload interface{})

    Close() error
}
```

### 2. 与现有代码集成

本系统通过 Bus 消息总线收集事件，两种方式结合：

#### 方式一：Bus 订阅（推荐）

Logger 订阅 Bus 消息，自动记录事件：

```go
type Logger struct {
    bus  *bus.Bus
    sess *session.Session
    // ...
}

func NewLogger(bus *bus.Bus, sess *session.Session) *Logger {
    l := &Logger{bus: bus, sess: sess}
    
    // 订阅 Bus 消息类型
    bus.Subscribe(bus.TypeUserInput, l.onUserInput)
    bus.Subscribe(bus.TypeAssistant, l.onAssistant)
    bus.Subscribe(bus.TypeToolCall, l.onToolCall)
    bus.Subscribe(bus.TypeToolResult, l.onToolResult)
    bus.Subscribe(bus.TypeToolError, l.onToolError)
    bus.Subscribe(bus.TypeSessionCompact, l.onCompact)
    bus.Subscribe(bus.TypeStreamChunk, l.onStreamChunk)
    bus.Subscribe(bus.TypeThinkingChunk, l.onThinking)
    
    return l
}
```

#### 方式二：Agent 显式调用

Agent 在关键节点显式调用 Logger：

```go
// agent/agent.go
func (a *Agent) run(ctx context.Context, src, role, input string) error {
    log := a.logger.WithContext(ctx)
    log.SessionStart(src)

    if input != "" {
        log.UserInput(role, input)
    }

    defer log.SessionEnd()

    for i := 0; i < maxSteps; i++ {
        log.StepStart(i)
        done, err := a.step(ctx, src, log)
        log.StepEnd(i, time.Since(stepStart))
        // ...
    }
}

// agent/react.go
func (a *Agent) step(ctx context.Context, src string, log Logger) (bool, error) {
    log.PromptBuild(PromptBuildData{...})

    corrID := log.LLMRequest(LLMRequestData{...})
    start := time.Now()
    resp, err := a.p.Chat(...)
    log.LLMResponse(corrID, LLMResponseData{...}, time.Since(start))

    for _, tc := range resp.ToolCalls {
        toolCorrID := log.ToolCall(tc)
        log.ToolStart(toolCorrID, tc.Name, string(tc.Args))
        toolStart := time.Now()
        out, code, err := a.execTool(ctx, tc)
        log.ToolEnd(toolCorrID, out, code, err, time.Since(toolStart))
    }
}
```

### 3. 回放器接口

```go
type Replayer interface {
    Load(path string) error
    Events() <-chan Event
    Replay(opts ReplayOptions) error
}

type ReplayOptions struct {
    Speed      float64 // 1.0 = real time, 2.0 = 2x speed
    FromEvent  string  // 从指定 event ID 开始
    ToEvent    string  // 到指定 event ID 结束
    SkipOutput bool    // 跳过 agent output 事件
}
```

### 4. Bus 消息类型映射

现有 Bus 消息类型与 Event 的映射关系：

| Bus 消息类型 | Event 类型 | 说明 |
|-------------|-----------|------|
| `user:input` | `user_input` | 用户输入 |
| `assistant:output` | `agent_output` | 代理输出 |
| `tool:call` | `tool_call` | 工具调用 |
| `tool:result` | `tool_end` | 工具执行完成 |
| `tool:error` | `tool_error` | 工具执行错误 |
| `session:compact` | `compact` | 会话压缩 |
| `stream:chunk` | `llm_stream_chunk` | 流式输出片段 |
| `thinking:chunk` | `llm_thinking` | thinking 内容 |
| `agent:spawn` | `spawn_start` | 子代理启动 |
| `agent:done` | `spawn_end` | 子代理完成 |
| `agent:interrupt` | `interrupt` | 用户中断 |
| `cron:trigger` | - | 定时任务触发（单独记录） |

> 说明：Bus 消息类型已经定义了大部分运行时事件，Logger 通过订阅这些消息即可自动记录，无需在 Agent 代码中显式调用。对于 Bus 未覆盖的事件（如 LLM 请求/响应、step 边界），仍需在 Agent 中显式记录。

## 使用场景

### 1. Debug 问题会话

```bash
# 查看某次会话的完整执行流
mink log show <session_id>

# 只看工具调用链
mink log show <session_id> --filter=tool_call,tool_start,tool_end

# 时间线视图
mink log timeline <session_id>

# 查看最近 N 条
mink log tail <session_id> -n 100
```

### 2. 会话回放

```bash
# 基于日志完整回放一次会话
mink log replay <session_id>

# 2倍速回放
mink log replay <session_id> --speed 2.0

# 从指定步骤开始回放
mink log replay <session_id> --from-event evt_010
```

### 3. 自动化分析

```bash
# 导出为 JSON 便于分析
mink log export <session_id> --format=json

# 统计工具使用
mink log analyze <session_id> --stat=tools

# 统计 token 消耗
mink log analyze <session_id> --stat=token
```

## 实现阶段

### Phase 1: 基础事件（MVP）

- Event 数据结构定义
- Logger 接口与文件实现
- 集成到 agent.run() - session_start/end, step_start/end
- 集成到 agent.step() - llm_request/response
- 集成工具调用 - tool_call/start/end
- 基本 CLI：log show, log tail

### Phase 2: 增强事件

- UserInput / AgentOutput 事件
- Prompt 构建详情
- Hook 触发记录
- 流式 chunks 记录
- Log rotate（文件大小限制）

### Phase 3: 回放功能

- Replayer 接口实现
- `mink log replay` 命令
- 回放控制（speed, from/to）

### Phase 4: 分析工具

- `mink log analyze` - 统计分析
- `mink log export` - 导出功能
- `mink log timeline` - 可视化时间线

## 设计决策记录

### 为什么是 Operation Log 而不是 Trace？

传统 Trace（如 OpenTelemetry）核心是：
- 采样（高流量时丢弃数据）
- 聚焦因果链（parent/child span）
- 目标：调试 + 性能分析

本系统核心是：
- **全集不丢**（高流量时宁可撑爆磁盘也不丢数据）
- **完整时间线**（精确到毫秒的执行顺序）
- **目标：可回放 + 审计**

本质是 **Audit Log / Operation Log**，借用 trace 的事件结构是为了复用概念。

### 为什么用 NDJSON 而不是结构化存储？

1. **追加友好** - session 是长时间运行的，需要频繁追加
2. **人类可读** - 可以直接 tail -f 查看
3. **工具友好** - jq, grep 等标准工具直接可用
4. **无依赖** - 不需要 SQLite 等额外依赖

### 为什么每个事件都有独立 ID？

- 支持精确的因果链（parent_id）
- 支持分布式追踪（未来扩展）
- 便于精确定位特定事件
- 便于回放时从任意断点恢复

### 为什么 correlation_id 而不是嵌套结构？

- request/response 可能跨多个 step（如 background 任务）
- 扁平结构更易查询和分析
- 避免深层嵌套的 JSON

### 为什么需要 Log 与 Session 分离？

Log 是操作日志，记录完整执行过程；Session 是消息历史，用于 LLM 上下文。两者独立：
- Session 会 compact 压缩历史
- Log 不受 compact 影响，永远保留完整操作记录
- 即使 Session 被 compact，Log 仍可完整回放

## 与现有系统的对比

| 能力 | 当前系统 | Operation Log |
|------|----------|----------------|
| 请求记录 | _req.jsonl（仅系统prompt） | 完整 messages + tools |
| 响应记录 | _resp.jsonl（仅content） | 完整响应 + tool_calls + usage |
| 工具调用 | 只能在 messages 中查看 | 独立事件，带耗时 + exit code |
| 工具输出 | 无 | 完整输出（不截断） |
| 执行顺序 | 从 messages 推断 | 精确时间戳 + parent_id |
| 调试难度 | 高（需人工关联） | 低（因果关系清晰） |
| 自动化分析 | 困难 | 容易（统一格式） |
| 会话回放 | 不可行 | 完全支持 |
| 与 compact 关系 | compact 影响消息历史 | 独立存储，不受 compact 影响 |
