package command

import (
	"context"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/tool"
)

type GuardMux struct {
	guards map[string]tool.Guard
}

func NewGuardMux() *GuardMux {
	return &GuardMux{guards: make(map[string]tool.Guard)}
}

func (m *GuardMux) Register(prefix string, g tool.Guard) {
	m.guards[prefix] = g
}

func (m *GuardMux) Allow(ctx context.Context, cmd string) (bool, error) {
	src := bus.SourceFrom(ctx)
	for prefix, g := range m.guards {
		if strings.HasPrefix(src, prefix) {
			return g.Allow(ctx, cmd)
		}
	}
	return true, nil
}
