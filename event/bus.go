package event

import "sync"

type Event struct {
	Type string
	Data any
}

const (
	AgentStart   = "agent:start"
	AgentEnd     = "agent:end"
	AgentError   = "agent:error"
	UserMessage  = "msg:user"
	AssistantMsg = "msg:assistant"
	SystemMsg    = "msg:system"
	ToolStart    = "tool:start"
	ToolEnd      = "tool:end"
	ToolError    = "tool:error"
	StreamStart  = "stream:start"
	StreamChunk  = "stream:chunk"
	StreamEnd    = "stream:end"
	SessionNew   = "session:new"
	SessionSwitch = "session:switch"
	SessionBranch = "session:branch"
	SessionCompact = "session:compact"
)

type Handler func(e Event)

type Bus struct {
	handlers map[string][]Handler
	mu       sync.RWMutex
}

func NewBus() *Bus {
	return &Bus{handlers: make(map[string][]Handler)}
}

func (b *Bus) Subscribe(t string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[t] = append(b.handlers[t], h)
}

func (b *Bus) Pub(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, h := range b.handlers[e.Type] {
		go h(e)
	}
}

func (b *Bus) PubSync(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, h := range b.handlers[e.Type] {
		h(e)
	}
}
