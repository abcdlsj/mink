package app

import (
	"strings"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
	"github.com/abcdlsj/sumi/store"
	"github.com/abcdlsj/sumi/task"
	"github.com/abcdlsj/sumi/tool"
)

func (a *App) Spaces() *space.Manager { return a.spaces }

func (a *App) Tasks() *task.Manager { return a.tasks }

func (a *App) Workspace() string {
	return a.cfg.Workspace
}

func (a *App) Personas() *persona.Registry {
	return a.personas
}

func (a *App) CurrentModel() string {
	return a.currentModel()
}

func (a *App) ChildEnv() []string {
	if a == nil {
		return nil
	}
	return a.cfg.ChildEnv()
}

func (a *App) Commands() []command.Command {
	if a == nil || a.cmds == nil {
		return nil
	}
	return a.cmds.All()
}

func (a *App) IsRegisteredCommandInput(input string) bool {
	if a == nil || a.cmds == nil {
		return false
	}
	raw := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(input), "!/"))
	if raw == "" {
		return false
	}
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return false
	}
	return a.cmds.Get(parts[0]) != nil
}

func (a *App) Tools() []tool.Tool {
	if a == nil || a.tools == nil {
		return nil
	}
	return a.tools.All()
}

func (a *App) CurrentSession(source string) (*session.Session, error) {
	return a.sessions.Current(source)
}

func (a *App) NewSession(source string) (*session.Session, error) {
	return a.sessions.New(source)
}

func (a *App) DraftSession(source string) *session.Session {
	return a.sessions.Draft(source)
}

func (a *App) SwitchSession(source, id string) (*session.Session, error) {
	return a.sessions.Switch(source, id)
}

func (a *App) SaveSession(s *session.Session) error {
	return a.sessions.Save(s)
}

// ManualCompactSpaceBacked reports whether a manual compact command from this
// source must be refused because the conversation is a persisted Space (context
// is rebuilt from Space each turn and overflow is compacted automatically).
// Plugin compact commands call this and return ErrManualCompactSpaceBacked when
// it is true rather than performing an in-place compact the next projection
// would silently discard.
func (a *App) ManualCompactSpaceBacked(source string) bool {
	return a.manualCompactSpaceBacked(source)
}

func (a *App) ListSessions() ([]*session.Session, error) {
	return a.sessions.List()
}

func (a *App) ListSessionsBySource(source string) ([]*session.Session, error) {
	return a.sessions.ListBySource(source)
}

func (a *App) DeleteSessionsMatching(match func(*session.Session) bool) (int, error) {
	if a == nil || a.sessions == nil {
		return 0, nil
	}
	return a.sessions.DeleteMatching(match)
}

func (a *App) SessionIndex() ([]store.SessionMeta, error) {
	if a == nil || a.store == nil {
		return nil, nil
	}
	return a.store.SessionIndex()
}

func (a *App) SessionTurnDepth(id string) int {
	if a == nil || a.sessions == nil {
		return 0
	}
	return a.sessions.TurnDepth(id)
}

func (a *App) PublishNotice(source, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	a.bus.Publish(bus.Event{
		Type:   bus.ServiceNotice,
		Source: source,
		Text:   text,
	})
}

func (a *App) ReplaySession(id string, limit int) ([]bus.Event, error) {
	if a == nil || a.store == nil {
		return nil, nil
	}
	return a.store.ReplaySession(id, limit)
}

func (a *App) ReplayTask(id string, limit int) ([]bus.Event, error) {
	if a == nil || a.store == nil {
		return nil, nil
	}
	return a.store.ReplayTask(id, limit)
}

func (a *App) SetToolGuard(g tool.Guard) {
	if a == nil || a.tools == nil {
		return
	}
	a.tools.SetGuard(g)
}

func (a *App) SetToolApprover(v tool.Approver) {
	if a == nil || a.tools == nil {
		return
	}
	type approverSetter interface {
		SetApprover(tool.Approver)
	}
	if setter, ok := any(a.tools.Guard()).(approverSetter); ok {
		setter.SetApprover(v)
	}
}
