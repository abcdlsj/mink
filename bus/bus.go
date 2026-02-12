package bus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type MessageBus interface {
	Pub(m Msg) error
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
	pending  map[string][]Msg
	mu       sync.RWMutex
}

func New() *Bus {
	return &Bus{
		subs:     make(map[string][]chan Msg),
		handlers: make(map[string]Handler),
		agents:   make(map[string]*AgentConn),
		pending:  make(map[string][]Msg),
	}
}

func normalizeMsg(m Msg) Msg {
	if m.ID == "" {
		m.ID = uuid.New().String()[:8]
	}
	if m.Time.IsZero() {
		m.Time = time.Now()
	}
	return m
}

func validateMsg(op string, m Msg) error {
	if m.Type == "" {
		return ErrInvalidAddr(op, m.Type, m.From, m.To, "type is required")
	}
	if m.From == "" {
		return ErrInvalidAddr(op, m.Type, m.From, m.To, "from is required")
	}
	if !IsValidAddr(m.From) {
		return ErrInvalidAddr(op, m.Type, m.From, m.To, "invalid from address")
	}
	if m.To == "" {
		return ErrInvalidAddr(op, m.Type, m.From, m.To, "to is required")
	}
	if !IsValidAddr(m.To) {
		return ErrInvalidAddr(op, m.Type, m.From, m.To, "invalid to address")
	}
	return nil
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
			break
		}
	}
}

func (b *Bus) Pub(m Msg) error {
	m = normalizeMsg(m)
	if err := validateMsg("pub", m); err != nil {
		return err
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	var dropped int

	if m.To == AddrBroadcast {
		for _, ch := range b.subs[m.Type] {
			select {
			case ch <- m:
			default:
				dropped++
			}
		}
	} else {
		agent, ok := b.agents[m.To]
		if ok {
			select {
			case agent.Send <- m:
			default:
				dropped++
			}
		} else {
			for _, ch := range b.subs[m.Type] {
				select {
				case ch <- m:
				default:
					dropped++
				}
			}
		}
	}

	if dropped > 0 {
		return fmt.Errorf("bus: dropped %d messages for type %s", dropped, m.Type)
	}
	return nil
}

func (b *Bus) Req(ctx context.Context, m Msg) (Msg, error) {
	m = normalizeMsg(m)
	if err := validateMsg("req", m); err != nil {
		return Msg{}, err
	}
	if IsBroadcast(m.To) {
		return Msg{}, ErrInvalidAddr("req", m.Type, m.From, m.To, "broadcast target is not allowed for req")
	}

	b.mu.RLock()
	handler, ok := b.handlers[m.Type]
	b.mu.RUnlock()

	if ok {
		resp, err := handler(ctx, m)
		if err != nil {
			return Msg{}, err
		}
		resp = normalizeMsg(resp)
		if resp.ReplyTo == "" {
			resp.ReplyTo = m.ID
		}
		if resp.From == "" {
			resp.From = AddrSystemDispatch
		}
		if resp.To == "" {
			resp.To = m.From
		}
		if err := validateMsg("req:response", resp); err != nil {
			return Msg{}, err
		}
		return resp, nil
	}

	return b.reqToAgent(ctx, m)
}

func (b *Bus) popPending(agentID, reqID string) (Msg, bool) {
	arr := b.pending[agentID]
	for i, m := range arr {
		if m.ReplyTo == reqID {
			b.pending[agentID] = append(arr[:i], arr[i+1:]...)
			return m, true
		}
	}
	return Msg{}, false
}

func (b *Bus) pushPending(agentID string, m Msg) {
	b.pending[agentID] = append(b.pending[agentID], m)
}

func (b *Bus) reqToAgent(ctx context.Context, m Msg) (Msg, error) {
	b.mu.Lock()
	agent, ok := b.agents[m.To]
	if !ok {
		b.mu.Unlock()
		return Msg{}, fmt.Errorf("bus: request target not found: %s", m.To)
	}
	if p, ok := b.popPending(m.To, m.ID); ok {
		b.mu.Unlock()
		return p, nil
	}
	b.mu.Unlock()

	select {
	case agent.Send <- m:
	case <-ctx.Done():
		return Msg{}, ctx.Err()
	}

	for {
		select {
		case resp := <-agent.Recv:
			resp = normalizeMsg(resp)
			if resp.ReplyTo == m.ID {
				if resp.From == "" {
					resp.From = m.To
				}
				if resp.To == "" {
					resp.To = m.From
				}
				if err := validateMsg("req:agent-response", resp); err != nil {
					return Msg{}, err
				}
				return resp, nil
			}

			b.mu.Lock()
			b.pushPending(m.To, resp)
			b.mu.Unlock()
		case <-ctx.Done():
			return Msg{}, ctx.Err()
		}
	}
}

func (b *Bus) RegisterHandler(msgType string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[msgType] = h
}

func (b *Bus) RegisterAgent(id string, shareCtx bool) *AgentConn {
	if !IsValidAddr(id) || id == AddrBroadcast {
		panic(fmt.Sprintf("bus: invalid agent address: %s", id))
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if old, ok := b.agents[id]; ok {
		return old
	}

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
	if m == nil {
		return r
	}
	for k, v := range m {
		r[k] = v
	}
	return r
}
