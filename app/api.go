package app

import (
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/session"
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
