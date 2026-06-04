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

func (a *App) BarkURL() string {
	if a == nil {
		return ""
	}
	return a.cfg.Notify.BarkURL
}

func (a *App) Commands() []command.Command {
	if a == nil || a.cmds == nil {
		return nil
	}
	return a.cmds.All()
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

func (a *App) ListSessions() ([]*session.Session, error) {
	return a.sessions.List()
}

func (a *App) ListSessionsBySource(source string) ([]*session.Session, error) {
	return a.sessions.ListBySource(source)
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
