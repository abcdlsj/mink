package driverexec

import (
	"context"
	"errors"

	"github.com/abcdlsj/sumi/internal/driver"
)

type Execution struct {
	AgentID             string
	ComputerID          string
	DeliveryID          string
	RunID               string
	LaunchID            string
	Fence               uint64
	PlacementGeneration uint64
	Workspace           string
	SpaceID             string
	ThreadRootMessageID string
	BasisTargetSequence uint64
	CurrentInput        string
}

type Completion struct {
	Outcome           driver.Outcome
	Body              string
	MentionedAgentIDs []string
}

type Executor struct {
	owner      *driver.Owner
	hostPolicy string
}

func New(engine driver.Engine, hostPolicy string) (*Executor, error) {
	if hostPolicy == "" {
		return nil, errors.New("host policy is required")
	}
	owner, err := driver.NewOwner(engine, 1)
	if err != nil {
		return nil, err
	}
	return &Executor{owner: owner, hostPolicy: hostPolicy}, nil
}

func (e *Executor) Close() error {
	return e.owner.Close()
}

func (e *Executor) Execute(ctx context.Context, execution Execution) (Completion, error) {
	input := driver.RunInput{
		AgentID: execution.AgentID, ComputerID: execution.ComputerID, Generation: execution.PlacementGeneration,
		DeliveryID: execution.DeliveryID, RunID: execution.RunID, LaunchID: execution.LaunchID, Fence: execution.Fence,
		Workspace: execution.Workspace, Capabilities: driver.Capability{Streaming: true, Tools: true, Cancel: true},
		Target:       driver.Target{SpaceID: execution.SpaceID, ThreadID: execution.ThreadRootMessageID, HeadSequence: execution.BasisTargetSequence},
		CurrentInput: execution.CurrentInput, HostPolicy: e.hostPolicy,
	}
	result, err := e.owner.Submit(ctx, driver.Command{Kind: driver.CommandPrompt, Input: &input})
	if err != nil {
		return Completion{}, err
	}
	if result.Body == "" {
		return Completion{}, errors.New("driver result body is required")
	}
	return Completion{Outcome: result.Outcome, Body: result.Body, MentionedAgentIDs: result.MentionedAgentIDs}, nil
}
