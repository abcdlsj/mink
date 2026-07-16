package cli

import (
	"context"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
)

type shellApp interface {
	HandleInput(context.Context, string, string) (string, error)
	HandleInputWithAttachments(context.Context, string, string, []msg.Attachment) (string, error)
	Config() config.Config
	CurrentModel() string
	Commands() []command.Command
	Workspace() string
	Personas() *persona.Registry
	CurrentSession(string) (*session.Session, error)
	DraftSession(string) *session.Session
	Spaces() *space.Manager
}
