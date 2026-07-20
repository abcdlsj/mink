package app

import (
	"context"

	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/task"
)

type AsyncTurnRequest struct {
	Task            *task.Task
	Worker          *persona.Persona
	SpaceID         string
	ParentMessageID string
	AgentID         string
	StreamID        string
	DeliveryID      string
	ResultMessageID string
}

type AsyncTurnResult struct {
	Content     string
	Reasoning   string
	Mentions    []string
	Usage       *msg.TokenUsage
	RuntimeMeta map[string]string
	Steps       []task.KeyStep
	EmptyOutput bool
	Err         error
}

type AsyncTurnExecutor func(ctx context.Context, req AsyncTurnRequest) AsyncTurnResult

func (a *App) RegisterAsyncTurnExecutor(exec AsyncTurnExecutor) {
	if a == nil {
		return
	}
	a.asyncTurnExecutor = exec
}
