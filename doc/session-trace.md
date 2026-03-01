# Session Trace / Execution Flow 设计文档

## 背景与问题

目前 Mink 的调试能力存在明显短板：

1. **日志碎片化** - 只有 `_req.jsonl` 和 `_resp.jsonl`，中间过程完全黑盒
2. **缺少因果链** - 无法还原 "为什么调用了这个工具"、"LLM 是怎么思考的"
3. **难以自动化分析** - 现有格式不统一，工具调用和响应分散在不同地方
4. **Telegram 场景 debug 困难** - 异步、流式输出，无法追踪完整执行路径

## 设计目标

构建一套 **Session Trace** 机制，实现：

1. **完整可还原** - 一次会话的所有中间操作都可记录、可回放
2. **结构化统一** - 所有事件统一格式，便于程序化解析
3. **低开销** - 不影响正常执行性能
4. **按需启用** - 可配置粒度（production 可关闭详细 trace）

## 核心概念

### Trace 事件类型

```go
type TraceEventType string

const (
    // 生命周期事件
    TraceSessionStart   TraceEventType = "session_start"
    TraceSessionEnd     TraceEventType = "session_end"
    TraceStepStart      TraceEventType = "step_start"
    TraceStepEnd        TraceEventType = "step_end"
    
    // LLM 交互事件
    TraceLLMRequest     TraceEventType = "llm_request"
    TraceLLMResponse    TraceEventType = "llm_response"
    TraceLLMStreamChunk TraceEventType = "llm_stream_chunk"
    TraceLLMThinking    TraceEventType = "llm_thinking"
    
    // 工具执行事件
    TraceToolCall       TraceEventType = "tool_call"
    TraceToolStart      TraceEventType = "tool_start"
    TraceToolEnd        TraceEventType = "tool_end"
    TraceToolError      TraceEventType = "tool_error"
    
    // 内部逻辑事件
    TracePromptBuild    TraceEventType = "prompt_build"
    TraceHookTrigger    TraceEventType = "hook_trigger"
    TraceContextUpdate  TraceEventType = "context_update"
    TraceTokenCalc      TraceEventType = "token_calc"
    
    // 系统事件
    TraceInterrupt      TraceEventType = "interrupt"
    TraceTimeout        TraceEventType = "timeout"
    TraceCompact        TraceEventType = "compact"
    
    // 子代理事件
    TraceSpawnStart     TraceEventType = "spawn_start"
    TraceSpawnEnd       TraceEventType = "spawn_end"
    
    // 后台任务事件
    TraceBgStart        TraceEventType = "bg_start"
    TraceBgEnd          TraceEventType = "bg_end"
)
```

### Trace 事件结构

```go
type TraceEvent struct {
    ID            string         `json:"id"`
    Type          TraceEventType `json:"type"`
    Timestamp     time.Time      `json:"timestamp"`
    SessionID     string         `json:"session_id"`
    StepNum       int            `json:"step_num"`
    ParentID      string         `json:"parent_id,omitempty"`
    CorrelationID string         `json:"correlation_id,omitempty"`
    Data          json.RawMessage `json:"data"`
    DurationMs    int64          `json:"duration_ms,omitempty"`
    Source        string         `json:"source,omitempty"`
    AgentID       string         `json:"agent_id,omitempty"`
}
```

### 各事件类型的数据结构

#### LLM 请求/响应

```go
type TraceLLMRequestData struct {
    Messages []msg.Message `json:"messages"`
    System   string        `json:"system"`
    Tools    []string      `json:"tools"`
    Stream   bool          `json:"stream"`
    Model    string        `json:"model"`
}

type TraceLLMResponseData struct {
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
type TraceToolCallData struct {
    ToolCallID string          `json:"tool_call_id"`
    Name       string          `json:"name"`
    Args       json.RawMessage `json:"args"`
}

type TraceToolStartData struct {
    ToolCallID string `json:"tool_call_id"`
    Name       string `json:"name"`
    Args       string `json:"args"`
}

type TraceToolEndData struct {
    ToolCallID string `json:"tool_call_id"`
    Name       string `json:"name"`
    Output     string `json:"output"`
    OutputSize int    `json:"output_size"`
    Error      string `json:"error,omitempty"`
}
```

#### Prompt 构建

```go
type TracePromptBuildData struct {
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

## 存储设计

### 文件结构

```
~/.mink/sessions/
├── <session_id>.jsonl          # 消息历史（已有）
├── <session_id>_req.jsonl      # 请求日志（已有，可废弃）
├── <session_id>_resp.jsonl     # 响应日志（已有，可废弃）
└── <session_id>.trace.jsonl    # 新：完整 trace（替代上面两个）
```

### 存储格式

每行一个 JSON 对象（NDJSON），便于追加和流式读取。

示例：

```
{"id":"evt_001","type":"session_start","timestamp":"...","session_id":"...","data":{...}}
{"id":"evt_002","type":"step_start","timestamp":"...","session_id":"...","step_num":0}
{"id":"evt_003","type":"llm_request","timestamp":"...","correlation_id":"req_001","data":{...}}
{"id":"evt_004","type":"llm_response","timestamp":"...","correlation_id":"req_001","duration_ms":2733,"data":{...}}
{"id":"evt_005","type":"tool_call","timestamp":"...","data":{"name":"bash","args":{...}}}
{"id":"evt_006","type":"tool_end","timestamp":"...","duration_ms":100,"data":{"output":"..."}}
```

### 配置

```go
type TraceConfig struct {
    Enabled         bool  // 是否启用 trace
    MaxFileSize     int64 // 单文件大小限制（默认 100MB）
    MaxFiles        int   // 保留文件数（默认 5）
    LogStreamChunks bool  // 是否记录流式 chunks
    LogFullPrompt   bool  // 是否记录完整 prompt
    ToolOutputLimit int   // 工具输出截断长度（默认 10KB）
}
```

## 关键实现点

### 1. TraceLogger 接口

```go
type TraceLogger interface {
    Log(event TraceEvent)
    
    SessionStart(source string)
    SessionEnd()
    StepStart(stepNum int)
    StepEnd(stepNum int, duration time.Duration)
    
    LLMRequest(req TraceLLMRequestData) string
    LLMResponse(correlationID string, resp TraceLLMResponseData, duration time.Duration)
    
    ToolCall(tc msg.ToolCall) string
    ToolStart(correlationID string, name string, args string)
    ToolEnd(correlationID string, output string, err error, duration time.Duration)
    
    PromptBuild(data TracePromptBuildData)
    HookTrigger(name string, payload interface{})
    
    Close() error
}
```

### 2. 与现有代码集成

```go
// agent/agent.go
func (a *Agent) run(ctx context.Context, src, role, input string) error {
    trace := a.tracer.WithContext(ctx)
    trace.SessionStart(src)
    defer trace.SessionEnd()
    
    for i := 0; i < maxSteps; i++ {
        trace.StepStart(i)
        done, err := a.step(ctx, src, trace)
        trace.StepEnd(i, time.Since(stepStart))
        // ...
    }
}

// agent/react.go
func (a *Agent) step(ctx context.Context, src string, trace TraceLogger) (bool, error) {
    trace.PromptBuild(TracePromptBuildData{...})
    
    corrID := trace.LLMRequest(TraceLLMRequestData{...})
    start := time.Now()
    resp, err := a.p.Chat(...)
    trace.LLMResponse(corrID, TraceLLMResponseData{...}, time.Since(start))
    
    for _, tc := range resp.ToolCalls {
        toolCorrID := trace.ToolCall(tc)
        trace.ToolStart(toolCorrID, tc.Name, string(tc.Args))
        toolStart := time.Now()
        out, err := a.execTool(ctx, tc)
        trace.ToolEnd(toolCorrID, out, err, time.Since(toolStart))
    }
}
```

## 使用场景

### 1. Debug 问题会话

```bash
# 查看某次会话的完整执行流
mink trace show <session_id>

# 只看工具调用链
mink trace show <session_id> --filter=tool_call,tool_start,tool_end

# 时间线视图
mink trace timeline <session_id>
```

### 2. 自动化分析

```python
# 分析工具使用模式
# 分析 LLM 响应时间
# 检测异常循环（如重复调用同一工具）
```

### 3. 会话回放

```bash
# 基于 trace 回放一次完整会话
mink trace replay <session_id>
```

## 实现阶段

### Phase 1: 基础事件（MVP）

- TraceEvent 数据结构定义
- TraceLogger 接口与文件实现
- 集成到 agent.run() - session_start/end, step_start/end
- 集成到 agent.step() - llm_request/response
- 集成工具调用 - tool_call/start/end

### Phase 2: 增强事件

- Prompt 构建详情
- Hook 触发记录
- Token 计算详情
- 流式 chunks（可选）

### Phase 3: 工具链

- `mink trace list` - 列出可追踪的会话
- `mink trace show` - 查看 trace 详情
- `mink trace export` - 导出为其他格式
- `mink trace analyze` - 简单统计分析

### Phase 4: 高级功能

- 实时 trace 流（WebSocket）
- Trace 可视化界面
- 基于 trace 的自动化测试

## 设计决策记录

### 为什么用 NDJSON 而不是结构化存储？

1. **追加友好** - session 是长时间运行的，需要频繁追加
2. **人类可读** - 可以直接 tail -f 查看
3. **工具友好** - jq, grep 等标准工具直接可用
4. **无依赖** - 不需要 SQLite 等额外依赖

### 为什么每个事件都有独立 ID？

- 支持精确的因果链（parent_id）
- 支持分布式追踪（未来扩展）
- 便于精确定位特定事件

### 为什么 correlation_id 而不是嵌套结构？

- request/response 可能跨多个 step（如 background 任务）
- 扁平结构更易查询和分析
- 避免深层嵌套的 JSON

## 与现有系统的对比

| 能力 | 当前系统 | Trace 系统 |
|------|----------|------------|
| 请求记录 | _req.jsonl（仅系统prompt） | 完整 messages + tools |
| 响应记录 | _resp.jsonl（仅content） | 完整响应 + tool_calls + usage |
| 工具调用 | 只能在 messages 中查看 | 独立事件，带耗时 |
| 执行顺序 | 从 messages 推断 | 精确时间戳 + parent_id |
| 调试难度 | 高（需人工关联） | 低（因果关系清晰） |
| 自动化分析 | 困难 | 容易（统一格式） |

