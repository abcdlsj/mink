package runtime

import (
	"context"

	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
)

type Runtime interface {
	Start(ctx context.Context, cfg Config) error
	Send(ctx context.Context, input string) error
	SendSystem(ctx context.Context, input string) error
	Stop() error
	Status() Status
	Session() *session.Session
	TokenUsage() msg.TokenUsage
	Interrupt()
}

type Config struct {
	Source  string
	AgentID string
	Session *session.Session
}
