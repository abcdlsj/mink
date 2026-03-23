package command

import (
	"context"
	"strings"
	"sync"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/tool"
)

type GuardMux struct {
	guards map[string]tool.InteractiveGuard
	perms  *tool.Permissions
	mu     sync.RWMutex
}

func NewGuardMux(perms *tool.Permissions) *GuardMux {
	return &GuardMux{
		guards: make(map[string]tool.InteractiveGuard),
		perms:  perms,
	}
}

func (m *GuardMux) Register(prefix string, g tool.InteractiveGuard) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.guards[prefix] = g
}

func (m *GuardMux) Unregister(prefix string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.guards, prefix)
}

func (m *GuardMux) Allow(ctx context.Context, cmd string) (bool, error) {
	if m.perms != nil && m.perms.Check(cmd) {
		return true, nil
	}

	src := bus.SourceFrom(ctx)
	type guardPair struct {
		prefix string
		guard  tool.InteractiveGuard
	}
	m.mu.RLock()
	pairs := make([]guardPair, 0, len(m.guards))
	for prefix, g := range m.guards {
		pairs = append(pairs, guardPair{prefix: prefix, guard: g})
	}
	m.mu.RUnlock()
	for _, pair := range pairs {
		prefix := pair.prefix
		g := pair.guard
		if strings.HasPrefix(src, prefix) {
			approval, err := g.Approve(ctx, cmd)
			if err != nil {
				return false, err
			}
			switch approval {
			case tool.AllowOnce:
				if m.perms != nil {
					m.perms.AllowSession(cmd)
				}
				return true, nil
			case tool.AllowAlways:
				if m.perms != nil {
					_ = m.perms.AllowPersist(tool.PatternFor(cmd))
				}
				return true, nil
			default:
				return false, nil
			}
		}
	}
	return true, nil
}
