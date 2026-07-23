package executor

import (
	"context"
	"testing"

	"github.com/abcdlsj/sumi/internal/driver"
)

type testEngine struct {
	execute func(context.Context, driver.Command, driver.EventSink) (driver.TurnResult, error)
}

func (e testEngine) Execute(ctx context.Context, command driver.Command, events driver.EventSink) (driver.TurnResult, error) {
	return e.execute(ctx, command, events)
}

func TestExecutorBuildsPromptAndMapsCompletion(t *testing.T) {
	executor, err := New(driver.KindCodex, testEngine{execute: func(_ context.Context, command driver.Command, _ driver.EventSink) (driver.TurnResult, error) {
		if command.Input == nil || command.Input.CurrentInput != "trigger" || command.Input.Target.SpaceID != "space-1" {
			t.Fatalf("input = %+v", command.Input)
		}
		want, err := driver.Capabilities(driver.KindCodex)
		if err != nil {
			t.Fatal(err)
		}
		if command.Input.Capabilities != want {
			t.Fatalf("capabilities = %+v, want %+v", command.Input.Capabilities, want)
		}
		return driver.TurnResult{Outcome: driver.OutcomeSucceeded, Body: "done"}, nil
	}}, "host policy")
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Close()
	completion, err := executor.Execute(context.Background(), Execution{
		AgentID: "agent-1", ComputerID: "computer-1", DeliveryID: "delivery-1", RunID: "run-1", LaunchID: "launch-1",
		Fence: 1, PlacementGeneration: 1, Workspace: "/tmp/agent", SpaceID: "space-1", BasisTargetSequence: 1, CurrentInput: "trigger",
	})
	if err != nil || completion.Outcome != driver.OutcomeSucceeded || completion.Body != "done" {
		t.Fatalf("completion = %+v, %v", completion, err)
	}
}
