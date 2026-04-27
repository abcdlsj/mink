package app

import (
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/store"
	"github.com/abcdlsj/mink/tool"
)

func (a *App) Workspace() string {
	return a.cfg.Workspace
}

func (a *App) CurrentModel() string {
	return a.currentModel()
}

func (a *App) CurrentSession(source string) (*session.Session, error) {
	return a.sessions.Current(source)
}

func (a *App) NewSession(source string) (*session.Session, error) {
	return a.sessions.New(source)
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
