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
	SessionNew   = "session:new"
	SessionSwitch = "session:switch"
	SessionBranch = "session:branch"
	SessionCompact = "session:compact"
)

type Handler func(e Event)

type Bus struct {
	h map[string][]Handler
	m sync.RWMutex
}

func NewBus() *Bus {
	return &Bus{h: make(map[string][]Handler)}
}

func (b *Bus) Subscribe(t string, fn Handler) {
	b.m.Lock()
	defer b.m.Unlock()
	b.h[t] = append(b.h[t], fn)
}

func (b *Bus) Pub(e Event) {
	b.m.RLock()
	defer b.m.RUnlock()
	for _, fn := range b.h[e.Type] {
		go fn(e)
	}
}

func (b *Bus) PubSync(e Event) {
	b.m.RLock()
	defer b.m.RUnlock()
	for _, fn := range b.h[e.Type] {
		fn(e)
	}
}
