package command

import (
	"context"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/tool"
)

type GuardMux struct {
	guards map[string]tool.InteractiveGuard
	perms  *tool.Permissions
}

func NewGuardMux(perms *tool.Permissions) *GuardMux {
	return &GuardMux{
		guards: make(map[string]tool.InteractiveGuard),
		perms:  perms,
	}
}

func (m *GuardMux) Register(prefix string, g tool.InteractiveGuard) {
	m.guards[prefix] = g
}

func (m *GuardMux) Unregister(prefix string) {
	delete(m.guards, prefix)
}

func (m *GuardMux) Allow(ctx context.Context, cmd string) (bool, error) {
	if m.perms != nil && m.perms.Check(cmd) {
		return true, nil
	}

	src := bus.SourceFrom(ctx)
	for prefix, g := range m.guards {
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
