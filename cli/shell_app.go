package cli

import (
	"context"

	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/session"
)

type shellApp interface {
	HandleInput(context.Context, string, string) (string, error)
	Config() config.Config
	CurrentModel() string
	Workspace() string
	CurrentSession(string) (*session.Session, error)
}
