package bus

import (
	"sync"
	"time"
)

const (
	TurnStarted      = "turn.started"
	TurnChunk        = "turn.chunk"
	TurnFinished     = "turn.finished"
	TurnError        = "turn.error"
	ToolCallStarted  = "tool.call.started"
	ToolCallFinished = "tool.call.finished"
	ToolCallFailed   = "tool.call.failed"
	SessionUpdated   = "session.updated"
	SessionCompacted = "session.compacted"
	CommandHandled   = "command.handled"
	ModelChanged     = "model.changed"
	ServiceNotice    = "service.notice"
)

type Event struct {
	Type       string
	Source     string
	SessionID  string
	TaskID     string
	ToolCallID string
	Text       string
	Tool       string
	Input      string
	Output     string
	Err        string
	Time       time.Time
}

type Bus struct {
	mu   sync.RWMutex
	next int
	subs map[int]chan Event
	taps map[int]func(Event)
}

func New() *Bus {
	return &Bus{
		subs: map[int]chan Event{},
		taps: map[int]func(Event){},
	}
}

func (b *Bus) Subscribe(size int) (<-chan Event, func()) {
	if size <= 0 {
		size = 64
	}
	ch := make(chan Event, size)
	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = ch
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

func (b *Bus) Publish(ev Event) {
	if b == nil {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	b.mu.RLock()
	subs := make([]chan Event, 0, len(b.subs))
	for _, ch := range b.subs {
		subs = append(subs, ch)
	}
	taps := make([]func(Event), 0, len(b.taps))
	for _, tap := range b.taps {
		taps = append(taps, tap)
	}
	b.mu.RUnlock()
	for _, tap := range taps {
		tap(ev)
	}
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (b *Bus) OnPublish(fn func(Event)) func() {
	if b == nil || fn == nil {
		return func() {}
	}
	b.mu.Lock()
	id := b.next
	b.next++
	b.taps[id] = fn
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.taps, id)
		b.mu.Unlock()
	}
}
