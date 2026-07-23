package executor

import (
	"context"
	"errors"
	"testing"

	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	computerhost "github.com/abcdlsj/sumi/internal/computer/host"
	"github.com/abcdlsj/sumi/internal/driver"
)

func TestComputerExecutorFiltersDriverAndMapsCompletion(t *testing.T) {
	executor, err := NewComputerExecutor(driver.KindCodex, testEngine{execute: func(_ context.Context, command driver.Command, _ driver.EventSink) (driver.TurnResult, error) {
		if command.Input == nil || command.Input.CurrentInput != "trigger" || command.Input.Target.SpaceID != "space-1" {
			t.Fatalf("input = %+v", command.Input)
		}
		return driver.TurnResult{Outcome: driver.OutcomeSucceeded, Body: "done"}, nil
	}}, "host policy", func(_ context.Context, agentID string) (driver.Kind, error) {
		if agentID == "bad-agent" {
			return "", errors.New("agent lookup failed")
		}
		if agentID == "native-agent" {
			return driver.KindNative, nil
		}
		return driver.KindCodex, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Close()
	for agentID, want := range map[string]bool{"codex-agent": true, "native-agent": false} {
		eligible, err := executor.Eligible(context.Background(), agentID)
		if err != nil || eligible != want {
			t.Fatalf("eligible(%q) = %t, %v; want %t", agentID, eligible, err, want)
		}
	}
	if _, err := executor.Eligible(context.Background(), "bad-agent"); err == nil {
		t.Fatal("agent lookup error was accepted")
	}
	completion, err := executor.Execute(context.Background(), computerhost.Execution{
		AgentID: "codex-agent", ComputerID: "computer-1", DeliveryID: "delivery-1", RunID: "run-1", LaunchID: "launch-1",
		Fence: 1, PlacementGeneration: 1, Workspace: "/tmp/agent", SpaceID: "space-1", BasisTargetSequence: 1, CurrentInput: "trigger",
	})
	if err != nil || completion.Outcome != deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED || completion.Body != "done" {
		t.Fatalf("completion = %+v, %v", completion, err)
	}
}

func TestComputerExecutorRejectsNativeConfiguration(t *testing.T) {
	_, err := NewComputerExecutor(driver.KindNative, nil, "host policy", func(context.Context, string) (driver.Kind, error) {
		return driver.KindNative, nil
	})
	if err == nil {
		t.Fatal("native external configuration was accepted")
	}
}
