package cli

import (
	"context"

	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/session"
)

type shellApp interface {
	HandleInput(context.Context, string, string) (string, error)
	Config() config.Config
	CurrentModel() string
	Commands() []command.Command
	Workspace() string
	CurrentSession(string) (*session.Session, error)
	NewSession(string) (*session.Session, error)
	SwitchSession(string, string) (*session.Session, error)
	ListSessionsBySource(string) ([]*session.Session, error)
}
