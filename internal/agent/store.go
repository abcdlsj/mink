package agent

import (
	"context"

	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
)

type agentStore interface {
	CreateAgent(context.Context, agentapp.CreateCommand) (agentapp.Agent, error)
	UpdateAgentProfile(context.Context, agentapp.UpdateProfileCommand) (agentapp.Agent, error)
	UpdateAgentRuntimeSpec(context.Context, agentapp.UpdateRuntimeSpecCommand) (agentapp.RuntimeSpec, error)
	GetAgentRuntimeSpec(context.Context, string) (agentapp.RuntimeSpec, error)
	GetAgent(context.Context, string) (agentapp.Agent, error)
	ListAgents(context.Context) ([]agentapp.Agent, error)
}
