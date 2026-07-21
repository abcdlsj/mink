package driverexec

import (
	"context"
	"testing"

	"github.com/abcdlsj/sumi/internal/driver"
)

func TestExecutorBuildsPromptAndMapsCompletion(t *testing.T) {
	executor, err := New(driver.Native{ExecuteFunc: func(_ context.Context, command driver.Command, _ driver.EventSink) (driver.TurnResult, error) {
		if command.Input == nil || command.Input.CurrentInput != "trigger" || command.Input.Target.SpaceID != "space-1" {
			t.Fatalf("input = %+v", command.Input)
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
