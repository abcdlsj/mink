package bus

import (
	"context"
	"sync"
	"time"
)

// Msg 统一消息格式
type Msg struct {
	ID      string      // 消息唯一ID
	From    string      // 发送者 (agent-id, user, telegram, etc)
	To      string      // 接收者 ("*"=广播, "agent-xxx"=特定agent)
	Type    string      // 消息类型
	Payload any         // 载荷
	Context MsgContext  // 上下文
	Time    time.Time
	ReplyTo string      // 回复哪条消息
}

// MsgContext 消息上下文
type MsgContext struct {
	SessionID string            // 所属会话
	AgentID   string            // 所属Agent
	BranchID  string            // 分支ID
	ParentID  string            // 父上下文ID
	Data      map[string]any    // 扩展数据
}

// Handler 消息处理器
type Handler func(ctx context.Context, m Msg) (Msg, error)

// Bus 消息总线
type Bus struct {
	subs     map[string][]chan Msg      // 订阅表
	handlers map[string]Handler         // 请求处理器
	agents   map[string]*AgentConn      // Agent连接表
	mu       sync.RWMutex
}

// AgentConn Agent连接
type AgentConn struct {
	ID       string
	Send     chan Msg
	Recv     chan Msg
	Context  MsgContext
	ShareCtx bool   // 是否共享上下文
}

func New() *Bus {
	return &Bus{
		subs:     make(map[string][]chan Msg),
		handlers: make(map[string]Handler),
		agents:   make(map[string]*AgentConn),
	}
}

// Subscribe 订阅某类消息
func (b *Bus) Subscribe(msgType string, ch chan Msg) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[msgType] = append(b.subs[msgType], ch)
}

// Unsubscribe 取消订阅
func (b *Bus) Unsubscribe(msgType string, ch chan Msg) {
	b.mu.Lock()
	defer b.mu.Unlock()
	arr := b.subs[msgType]
	for i, c := range arr {
		if c == ch {
			b.subs[msgType] = append(arr[:i], arr[i+1:]...)
			close(ch)
			break
		}
	}
}

// Pub 发布消息（广播）
func (b *Bus) Pub(m Msg) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	// 发给订阅者
	for _, ch := range b.subs[m.Type] {
		select {
		case ch <- m:
		default:
		}
	}
	
	// 发给特定Agent
	if m.To != "" && m.To != "*" {
		if agent, ok := b.agents[m.To]; ok {
			select {
			case agent.Send <- m:
			default:
			}
		}
	}
}

// Req 请求-响应模式
func (b *Bus) Req(ctx context.Context, m Msg) (Msg, error) {
	// 找处理器
	b.mu.RLock()
	handler, ok := b.handlers[m.Type]
	b.mu.RUnlock()
	
	if !ok {
		// 转发给特定Agent
		if m.To != "" && m.To != "*" {
			return b.reqToAgent(ctx, m)
		}
		return Msg{}, nil
	}
	
	return handler(ctx, m)
}

// reqToAgent 向特定Agent发送请求
func (b *Bus) reqToAgent(ctx context.Context, m Msg) (Msg, error) {
	b.mu.RLock()
	agent, ok := b.agents[m.To]
	b.mu.RUnlock()
	
	if !ok {
		return Msg{}, nil
	}
	
	// 发送请求
	select {
	case agent.Send <- m:
	case <-ctx.Done():
		return Msg{}, ctx.Err()
	}
	
	// 等待响应
	select {
	case resp := <-agent.Recv:
		return resp, nil
	case <-ctx.Done():
		return Msg{}, ctx.Err()
	}
}

// RegisterHandler 注册请求处理器
func (b *Bus) RegisterHandler(msgType string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[msgType] = h
}

// RegisterAgent 注册Agent
func (b *Bus) RegisterAgent(id string, shareCtx bool) *AgentConn {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	conn := &AgentConn{
		ID:       id,
		Send:     make(chan Msg, 64),
		Recv:     make(chan Msg, 64),
		ShareCtx: shareCtx,
		Context: MsgContext{
			AgentID: id,
		},
	}
	b.agents[id] = conn
	return conn
}

// UnregisterAgent 注销Agent
func (b *Bus) UnregisterAgent(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	if agent, ok := b.agents[id]; ok {
		close(agent.Send)
		close(agent.Recv)
		delete(b.agents, id)
	}
}

// GetAgent 获取Agent连接
func (b *Bus) GetAgent(id string) (*AgentConn, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	c, ok := b.agents[id]
	return c, ok
}

// ForkContext 分叉上下文（创建子Agent）
func (b *Bus) ForkContext(parentID string, childID string) MsgContext {
	b.mu.RLock()
	parent, ok := b.agents[parentID]
	b.mu.RUnlock()
	
	ctx := MsgContext{
		AgentID:  childID,
		ParentID: parentID,
	}
	
	if ok && parent.ShareCtx {
		ctx.SessionID = parent.Context.SessionID
		ctx.Data = copyMap(parent.Context.Data)
	}
	
	return ctx
}

func copyMap(m map[string]any) map[string]any {
	r := make(map[string]any)
	for k, v := range m {
		r[k] = v
	}
	return r
}

// Types
const (
	TypeUserInput    = "user:input"
	TypeAssistant    = "assistant:output"
	TypeToolCall     = "tool:call"
	TypeToolResult   = "tool:result"
	TypeToolError    = "tool:error"
	TypeSessionNew   = "session:new"
	TypeSessionFork  = "session:fork"
	TypeAgentSpawn   = "agent:spawn"
	TypeAgentDie     = "agent:die"
	TypeContextShare = "context:share"
)
