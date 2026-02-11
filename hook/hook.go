package hook

import (
	"context"
	"sync"
)

type Type string

const (
	BeforeInput  Type = "before:input"
	AfterInput   Type = "after:input"
	BeforeTool   Type = "before:tool"
	AfterTool    Type = "after:tool"
	BeforeAssist Type = "before:assist"
	AfterAssist  Type = "after:assist"
)

type Hook struct {
	Type    Type
	Handler func(ctx context.Context, data any) error
}

type Manager struct {
	hooks map[Type][]Hook
	mu    sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{hooks: make(map[Type][]Hook)}
}

func (m *Manager) Register(h Hook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks[h.Type] = append(m.hooks[h.Type], h)
}

func (m *Manager) Trigger(ctx context.Context, t Type, data any) error {
	m.mu.RLock()
	hs := m.hooks[t]
	m.mu.RUnlock()

	for _, h := range hs {
		if err := h.Handler(ctx, data); err != nil {
			return err
		}
	}
	return nil
}
