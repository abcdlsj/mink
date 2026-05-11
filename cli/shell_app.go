package cli

import (
	"context"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/session"
)

type shellApp interface {
	HandleInput(context.Context, string, string) (string, error)
	Config() config.Config
	CurrentModel() string
	Commands() []command.Command
	Workspace() string
	Personas() *persona.Registry
	CurrentSession(string) (*session.Session, error)
	NewSession(string) (*session.Session, error)
	SwitchSession(string, string) (*session.Session, error)
	ListSessionsBySource(string) ([]*session.Session, error)
}
