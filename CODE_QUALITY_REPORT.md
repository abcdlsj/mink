# Mink 项目代码质量报告

## 执行时间: 2024
## 项目语言: Go
## 代码文件数: 30+

---

## 一、代码格式化问题

### ⚠️ 问题 1: 文件末尾多余空行
**位置**: `bus/address.go`, `bus/errors.go`

**问题描述**: 
```bash
diff bus/address.go.orig bus/address.go
- 最后一行存在多余换行
```

**修复建议**:
```bash
gofmt -w bus/address.go bus/errors.go
```

**严重程度**: 🔵 低

---

## 二、并发安全问题

### ⚠️ 问题 2: 锁顺序不一致可能导致死锁
**位置**: `agent/dispatcher.go:100-120`

**问题描述**: 
```go
// resetAgent 方法
func (d *Dispatcher) resetAgent(src string) {
    var cancel context.CancelFunc

    d.mu.Lock()
    delete(d.agents, src)
    // ...
    d.mu.Unlock()

    if cancel != nil {
        cancel()  // 在锁外执行
    }
    
    if isTelegramSource(src) {
        _ = d.sm.Delete(src)  // 调用其他包的锁操作
    }
}
```

**风险**: 与 `session/manager.go` 的锁交互时可能存在潜在死锁风险

**修复建议**: 统一锁获取顺序，或使用 try-lock 模式

**严重程度**: 🟡 中

### ⚠️ 问题 3: channel 关闭后写入风险
**位置**: `bus/bus.go:241-250`

**问题描述**:
```go
func (b *Bus) UnregisterAgent(id string) {
    b.mu.Lock()
    defer b.mu.Unlock()

    if agent, ok := b.agents[id]; ok {
        close(agent.Send)  // 关闭 channel
        close(agent.Recv)
        delete(b.agents, id)
    }
}
```

**风险**: 如果在 channel 关闭后仍有 goroutine 尝试写入，会导致 panic

**修复建议**:
```go
// 使用 select + 检查关闭状态
select {
case agent.Send <- m:
case <-agent.closed:
    return ErrAgentClosed
}
```

**严重程度**: 🟡 中

### ⚠️ 问题 4: 共享 map 的非深度拷贝
**位置**: `bus/bus.go:286-297`

**问题描述**:
```go
func (b *Bus) ForkContext(parentID, childID string) MsgContext {
    // ...
    if ok && parent.ShareCtx {
        ctx.SessionID = parent.Context.SessionID
        ctx.Data = copyMap(parent.Context.Data)  // 浅拷贝
    }
    // ...
}
```

**风险**: `copyMap` 只做了浅拷贝，如果 `Data` 中的值是引用类型，父子 agent 会共享状态

**严重程度**: 🟡 中

---

## 三、错误处理问题

### ⚠️ 问题 5: 忽略关键错误
**位置**: `main.go:109-113`

**问题描述**:
```go
if cfg.Telegram != "" {
    tg := platform.NewTelegram(cfg.Telegram, b)
    if err := tg.Start(ctx); err != nil {
        // telegram 错误不阻止启动 - 注释说忽略，但没有日志
    } else {
```

**风险**: Telegram 启动失败被静默忽略，用户无法得知

**修复建议**:
```go
if err := tg.Start(ctx); err != nil {
    log.Printf("[Warning] Telegram start failed: %v", err)
}
```

**严重程度**: 🟡 中

### ⚠️ 问题 6: JSON 解码错误被忽略
**位置**: `llm/anthropic.go:130-137`

**问题描述**:
```go
for i, t := range tools {
    schemaBytes, _ := json.Marshal(t.Function.Parameters)  // 忽略错误
    var inputSchema anthropic.ToolInputSchemaParam
    json.Unmarshal(schemaBytes, &inputSchema)  // 忽略错误
```

**风险**: JSON 处理错误被忽略，可能导致数据不一致

**严重程度**: 🟡 中

### ⚠️ 问题 7: 资源未正确关闭
**位置**: `agent/dispatcher.go:47-51`

**问题描述**:
```go
d := &Dispatcher{
    bus:     b,
    sm:      sm,
    p:       p,
    agents:  make(map[string]*Agent),
    workers: make(map[string]*workerState),
}
// hooks 和 router 未初始化，但后续会用到
```

**严重程度**: 🔵 低

---

## 四、资源管理问题

### ⚠️ 问题 8: goroutine 泄漏风险
**位置**: `tool/spawn.go:76-92`

**问题描述**:
```go
timeout := time.After(10 * time.Minute)
for {
    select {
    case m := <-ch:
        if m.From != childID {
            continue  // 不匹配时继续等待，但没有超时检查
        }
        // ...
    case <-timeout:
        return "", fmt.Errorf("agent %s timeout", childID)
    case <-ctx.Done():
        return "", ctx.Err()
    }
}
```

**风险**: 如果子 agent 的 `AgentDone` 消息丢失，此 goroutine 会等待 10 分钟

**严重程度**: 🟡 中

### ⚠️ 问题 9: 内存泄漏风险 - pending 消息
**位置**: `bus/bus.go:168-173`

**问题描述**:
```go
func (b *Bus) pushPending(agentID string, m Msg) {
    b.pending[agentID] = append(b.pending[agentID], m)
}
```

**风险**: `pending` map 中的消息永远不会被清理，可能导致内存泄漏

**修复建议**: 添加清理机制或限制大小

**严重程度**: 🟠 高

### ⚠️ 问题 10: session 文件未清理
**位置**: `session/manager.go`

**问题描述**: 没有自动清理过期 session 文件的机制

**严重程度**: 🟡 中

---

## 五、安全问题

### ⚠️ 问题 11: 命令注入风险
**位置**: `cmd/router.go:47-53`

**问题描述**:
```go
func (r *Router) shell(ctx context.Context, raw string) (string, bool, error) {
    // ...
    cmd := exec.CommandContext(ctx, "bash", "-c", raw)
    out, err := cmd.CombinedOutput()
```

**风险**: 用户输入直接传入 shell，存在命令注入风险

**缓解措施**: 已有 `IsDangerous` 检查，但防护不完整

**修复建议**: 
- 使用白名单而非黑名单
- 参数化命令而非直接拼接

**严重程度**: 🟠 高

### ⚠️ 问题 12: 路径遍历风险
**位置**: `tool/core.go:78-86` (Read 工具)

**问题描述**:
```go
func (r *Read) Run(ctx context.Context, args json.RawMessage) (string, error) {
    // ...
    data, err := os.ReadFile(params.Path)  // 直接使用用户输入路径
```

**风险**: 可能读取任意文件（如 `/etc/passwd`）

**修复建议**:
```go
// 验证路径在允许的目录内
absPath, _ := filepath.Abs(params.Path)
if !strings.HasPrefix(absPath, allowedBasePath) {
    return "", fmt.Errorf("path not allowed: %s", params.Path)
}
```

**严重程度**: 🟠 高

### ⚠️ 问题 13: panic 使用不当
**位置**: `bus/bus.go:243`

**问题描述**:
```go
func (b *Bus) RegisterAgent(id string, shareCtx bool) *AgentConn {
    if !IsValidAddr(id) || id == AddrBroadcast {
        panic(fmt.Sprintf("bus: invalid agent address: %s", id))
    }
```

**风险**: 库代码使用 panic，可能导致整个程序崩溃

**修复建议**: 返回错误而非 panic

**严重程度**: 🟡 中

---

## 六、代码风格问题

### ⚠️ 问题 14: 魔法数字
**位置**: 多个文件

**问题列表**:
- `agent/react.go`: `1200` (内容截断长度)
- `agent/supervisor.go`: `maxActiveSubAgents = 2`
- `agent/dispatcher.go`: `workerIdleTTL = 5 * time.Minute`
- `platform/telegram.go`: `telegramMsgLimit = 3800`

**修复建议**: 定义为有意义的常量

**严重程度**: 🔵 低

### ⚠️ 问题 15: 重复代码
**位置**: `tool/background.go` 和 `tool/core.go:Bash`

**问题描述**: 两处都有执行 bash 命令的代码，逻辑重复

**修复建议**: 抽象为公共函数

**严重程度**: 🔵 低

---

## 七、性能问题

### ⚠️ 问题 16: 频繁的字符串拼接
**位置**: `agent/agent.go:95-121` (Compact 方法)

**问题描述**:
```go
var hist strings.Builder
hist.WriteString("Summarize the conversation history below...")
// ...多次 WriteString
```

**评估**: 实际上使用了 `strings.Builder`，这是正确的做法

**严重程度**: ✅ 无问题

### ⚠️ 问题 17: 频繁的锁竞争
**位置**: `bus/bus.go`

**问题描述**: `Pub` 方法使用 `RLock`，但内部有 channel 操作可能阻塞

**严重程度**: 🟡 中

---

## 八、测试覆盖

### ⚠️ 问题 18: 缺少单元测试
**位置**: 整个项目

**问题描述**: 未找到 `*_test.go` 文件

**严重程度**: 🟠 高

---

## 九、文档问题

### ⚠️ 问题 19: 缺少包文档
**位置**: 多个包

**问题描述**: 大部分包缺少 `package` 级别的文档注释

**严重程度**: 🔵 低

### ⚠️ 问题 20: 导出函数缺少文档
**位置**: 多个文件

**问题描述**: 许多导出函数缺少文档注释（以函数名开头的注释）

**严重程度**: 🔵 低

---

## 十、架构问题

### ⚠️ 问题 21: 循环依赖风险
**位置**: `agent` 和 `tool` 包

**问题描述**:
```go
// agent/agent.go
"github.com/abcdlsj/mink/tool"

// tool/spawn.go
"github.com/abcdlsj/mink/bus"
```

**评估**: 目前未形成循环，但架构上存在风险

**严重程度**: 🔵 低

### ⚠️ 问题 22: 全局配置传递
**位置**: 多处

**问题描述**: `config.Config` 被多处传递，可能导致配置不一致

**修复建议**: 使用依赖注入或配置中心模式

**严重程度**: 🟡 中

---

## 优先级排序的修复建议

### 🔴 立即修复 (高优先级)

1. **添加路径验证** (`tool/core.go`) - 安全风险
2. **修复命令注入防护** (`cmd/router.go`) - 安全风险
3. **修复内存泄漏** (`bus/bus.go:pending`) - 稳定性
4. **添加单元测试** - 代码质量保证

### 🟡 近期修复 (中优先级)

5. **添加错误日志** (`main.go:telegram` 错误)
6. **移除 panic** (`bus/bus.go:RegisterAgent`)
7. **修复 goroutine 泄漏** (`tool/spawn.go`)
8. **统一锁获取顺序**
9. **处理 JSON 错误** (`llm/anthropic.go`)

### 🔵 长期改进 (低优先级)

10. **修复代码格式** (`gofmt`)
11. **添加包文档**
12. **提取魔法数字为常量**
13. **消除重复代码**

---

## 代码统计

```
语言: Go
代码文件: 30+
总代码行数: ~3000+ 行
包数量: 10
依赖库: 15+ (外部)

外部依赖:
- github.com/BurntSushi/toml
- github.com/anthropics/anthropic-sdk-go
- github.com/charmbracelet/bubbles
- github.com/charmbracelet/bubbletea
- github.com/google/uuid
- github.com/pkoukk/tiktoken-go
- github.com/sashabaranov/go-openai
- gopkg.in/telebot.v4
```

---

## 总结

该项目整体架构清晰，采用了良好的模块划分（bus/agent/tool/platform 等）。主要问题集中在：

1. **安全问题**: 路径遍历和命令注入风险需要立即修复
2. **稳定性问题**: 内存泄漏和 goroutine 泄漏需要关注
3. **质量保证**: 缺少测试覆盖

**总体评分**: 7/10

**建议**: 优先处理安全和稳定性问题，然后补充测试覆盖。
