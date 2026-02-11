package bus

import (
	"context"
	"sync"
)

type MessageBus interface {
	Pub(m Msg)
	Subscribe(msgType string, ch chan Msg)
	Unsubscribe(msgType string, ch chan Msg)
	Req(ctx context.Context, m Msg) (Msg, error)
	RegisterHandler(msgType string, h Handler)
	RegisterAgent(id string, shareCtx bool) *AgentConn
	UnregisterAgent(id string)
}

type AgentConn struct {
	ID       string
	Send     chan Msg
	Recv     chan Msg
	Context  MsgContext
	ShareCtx bool
}

type Bus struct {
	subs     map[string][]chan Msg
	handlers map[string]Handler
	agents   map[string]*AgentConn
	mu       sync.RWMutex
}

func New() *Bus {
	return &Bus{
		subs:     make(map[string][]chan Msg),
		handlers: make(map[string]Handler),
		agents:   make(map[string]*AgentConn),
	}
}

func (b *Bus) Subscribe(msgType string, ch chan Msg) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[msgType] = append(b.subs[msgType], ch)
}

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

func (b *Bus) Pub(m Msg) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, ch := range b.subs[m.Type] {
		select {
		case ch <- m:
		default:
		}
	}

	if m.To != "" && m.To != "*" {
		if agent, ok := b.agents[m.To]; ok {
			select {
			case agent.Send <- m:
			default:
			}
		}
	}
}

func (b *Bus) Req(ctx context.Context, m Msg) (Msg, error) {
	b.mu.RLock()
	handler, ok := b.handlers[m.Type]
	b.mu.RUnlock()

	if !ok {
		if m.To != "" && m.To != "*" {
			return b.reqToAgent(ctx, m)
		}
		return Msg{}, nil
	}

	return handler(ctx, m)
}

func (b *Bus) reqToAgent(ctx context.Context, m Msg) (Msg, error) {
	b.mu.RLock()
	agent, ok := b.agents[m.To]
	b.mu.RUnlock()

	if !ok {
		return Msg{}, nil
	}

	select {
	case agent.Send <- m:
	case <-ctx.Done():
		return Msg{}, ctx.Err()
	}

	select {
	case resp := <-agent.Recv:
		return resp, nil
	case <-ctx.Done():
		return Msg{}, ctx.Err()
	}
}

func (b *Bus) RegisterHandler(msgType string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[msgType] = h
}

func (b *Bus) RegisterAgent(id string, shareCtx bool) *AgentConn {
	b.mu.Lock()
	defer b.mu.Unlock()

	conn := &AgentConn{
		ID:       id,
		Send:     make(chan Msg, 64),
		Recv:     make(chan Msg, 64),
		ShareCtx: shareCtx,
		Context:  MsgContext{AgentID: id},
	}
	b.agents[id] = conn
	return conn
}

func (b *Bus) UnregisterAgent(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if agent, ok := b.agents[id]; ok {
		close(agent.Send)
		close(agent.Recv)
		delete(b.agents, id)
	}
}

func (b *Bus) GetAgent(id string) (*AgentConn, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	c, ok := b.agents[id]
	return c, ok
}

func (b *Bus) ForkContext(parentID, childID string) MsgContext {
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
