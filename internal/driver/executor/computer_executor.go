package executor

import (
	"context"
	"errors"

	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	computerhost "github.com/abcdlsj/sumi/internal/computer/host"
	"github.com/abcdlsj/sumi/internal/driver"
)

type AgentDriverResolver func(context.Context, string) (driver.Kind, error)

type ComputerExecutor struct {
	kind     driver.Kind
	resolve  AgentDriverResolver
	executor *Executor
}

func NewComputerExecutor(kind driver.Kind, engine driver.Engine, hostPolicy string, resolve AgentDriverResolver) (*ComputerExecutor, error) {
	if kind != driver.KindCodex && kind != driver.KindClaude {
		return nil, errors.New("computer external driver kind is invalid")
	}
	if resolve == nil {
		return nil, errors.New("agent driver resolver is required")
	}
	executor, err := New(kind, engine, hostPolicy)
	if err != nil {
		return nil, err
	}
	return &ComputerExecutor{kind: kind, resolve: resolve, executor: executor}, nil
}

func (e *ComputerExecutor) Close() error {
	return e.executor.Close()
}

func (e *ComputerExecutor) Eligible(ctx context.Context, agentID string) (bool, error) {
	kind, err := e.resolve(ctx, agentID)
	if err != nil {
		return false, err
	}
	return kind == e.kind, nil
}

func (e *ComputerExecutor) Execute(ctx context.Context, execution computerhost.Execution) (computerhost.Completion, error) {
	completion, err := e.executor.Execute(ctx, Execution{
		AgentID: execution.AgentID, ComputerID: execution.ComputerID, DeliveryID: execution.DeliveryID,
		RunID: execution.RunID, LaunchID: execution.LaunchID, Fence: execution.Fence,
		PlacementGeneration: execution.PlacementGeneration, Workspace: execution.Workspace,
		SpaceID: execution.SpaceID, ThreadRootMessageID: execution.ThreadRootMessageID,
		BasisTargetSequence: execution.BasisTargetSequence, CurrentInput: execution.CurrentInput,
	})
	if err != nil {
		return computerhost.Completion{}, err
	}
	outcome := deliveryv1.RunOutcome_RUN_OUTCOME_UNSPECIFIED
	if completion.Outcome == driver.OutcomeSucceeded {
		outcome = deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED
	} else if completion.Outcome == driver.OutcomeFailed {
		outcome = deliveryv1.RunOutcome_RUN_OUTCOME_FAILED
	}
	return computerhost.Completion{
		Outcome: outcome, Body: completion.Body, MentionedAgentIDs: completion.MentionedAgentIDs,
	}, nil
}

var _ computerhost.Executor = (*ComputerExecutor)(nil)
var _ computerhost.EligibleExecutor = (*ComputerExecutor)(nil)
