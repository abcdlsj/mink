package domain

import (
	"errors"
	"testing"
	"time"
)

func TestRunLifecycleValidation(t *testing.T) {
	now := time.Now().UTC()
	later := now.Add(time.Minute)
	tests := []struct {
		name string
		run  Run
		want error
	}{
		{name: "queued", run: Run{State: RunQueued}},
		{name: "running", run: Run{State: RunRunning, InputBasisTargetSequence: 1, Attempt: 1, LeaseHolderComputerID: "computer", LeaseExpiresAt: &later, Fence: 1, PlacementDesiredRevision: 1, StartedAt: &now}},
		{name: "succeeded", run: Run{State: RunSucceeded, InputBasisTargetSequence: 1, Attempt: 1, LeaseHolderComputerID: "computer", LeaseExpiresAt: &later, Fence: 1, PlacementDesiredRevision: 1, ResultKind: ResultMessage, ResultID: "message", StartedAt: &now, CompletedAt: &later}},
		{name: "failed", run: Run{State: RunFailed, InputBasisTargetSequence: 1, Attempt: 1, LeaseHolderComputerID: "computer", LeaseExpiresAt: &later, Fence: 1, PlacementDesiredRevision: 1, ResultKind: ResultHeldDraft, ResultID: "draft", ErrorCode: "provider_unavailable", StartedAt: &now, CompletedAt: &later}},
		{name: "unclaimed cancelled", run: Run{State: RunCancelled, CancelledAt: &now}},
		{name: "claimed cancelled", run: Run{State: RunCancelled, InputBasisTargetSequence: 1, Attempt: 1, LeaseHolderComputerID: "computer", LeaseExpiresAt: &later, Fence: 1, PlacementDesiredRevision: 1, StartedAt: &now, CancelledAt: &later}},
		{name: "failed without code", run: Run{State: RunFailed, InputBasisTargetSequence: 1, Attempt: 1, LeaseHolderComputerID: "computer", LeaseExpiresAt: &later, Fence: 1, PlacementDesiredRevision: 1, ResultKind: ResultMessage, ResultID: "message", StartedAt: &now, CompletedAt: &later}, want: ErrRunIntegrity},
		{name: "unknown result", run: Run{State: RunSucceeded, InputBasisTargetSequence: 1, Attempt: 1, LeaseHolderComputerID: "computer", LeaseExpiresAt: &later, Fence: 1, PlacementDesiredRevision: 1, ResultKind: "opaque", ResultID: "result", StartedAt: &now, CompletedAt: &later}, want: ErrRunIntegrity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateRun(test.run); !errors.Is(err, test.want) {
				t.Fatalf("ValidateRun() = %v, want %v", err, test.want)
			}
		})
	}
}

func TestRunLeaseRequiresExactHolderRevisionAttemptAndFence(t *testing.T) {
	now := time.Now().UTC()
	expires := now.Add(time.Minute)
	run := Run{State: RunRunning, InputBasisTargetSequence: 1, Attempt: 2, LeaseHolderComputerID: "computer", LeaseExpiresAt: &expires, Fence: 7, PlacementDesiredRevision: 3, StartedAt: &now}
	if err := ValidateLease(run, "computer", 3, 2, 7, now); err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct {
		computer string
		revision uint64
		attempt  uint64
		fence    uint64
	}{
		{computer: "other", revision: 3, attempt: 2, fence: 7},
		{computer: "computer", revision: 4, attempt: 2, fence: 7},
		{computer: "computer", revision: 3, attempt: 1, fence: 7},
		{computer: "computer", revision: 3, attempt: 2, fence: 8},
	} {
		if err := ValidateLease(run, call.computer, call.revision, call.attempt, call.fence, now); !errors.Is(err, ErrRunLeaseStale) {
			t.Fatalf("ValidateLease(%+v) = %v", call, err)
		}
	}
	if err := ValidateLease(run, "computer", 3, 2, 7, expires); !errors.Is(err, ErrRunLeaseExpired) {
		t.Fatalf("expired lease error = %v", err)
	}
}
