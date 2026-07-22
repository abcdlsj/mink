package agent

import (
	"context"

	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
)

type agentStore interface {
	CreateAgent(context.Context, agentapp.CreateCommand) (agentapp.Agent, error)
	GetAgent(context.Context, string) (agentapp.Agent, error)
	ListAgents(context.Context) ([]agentapp.Agent, error)
}
