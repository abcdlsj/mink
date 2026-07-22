package agent

import (
	"context"

	"github.com/abcdlsj/sumi/internal/store"
)

type agentStore interface {
	CreateAgent(context.Context, store.CreateAgentParams) (store.Agent, error)
	GetAgent(context.Context, string) (store.Agent, error)
	ListAgents(context.Context) ([]store.Agent, error)
}
