package domain

import (
	"errors"
	"testing"
	"time"
)

func TestValidateActiveFacts(t *testing.T) {
	now := time.Unix(100, 0)
	baseRun := &Run{ID: "run", DeliveryID: "delivery", AgentID: "agent", State: RunRunning, AcceptedAt: now, StartedAt: timePtr(now)}
	baseDelivery := &Delivery{ID: "delivery", AgentID: "agent", State: DeliveryAccepted, AcceptedAt: timePtr(now)}
	baseLaunch := &Launch{
		ID: "launch", RunID: "run", AgentID: "agent", HolderComputerID: "computer", HolderPlacementGeneration: 1,
		Fence: 1, ClaimedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	tests := []struct {
		name  string
		facts ActiveFacts
		want  error
	}{
		{name: "running with launch", facts: ActiveFacts{Delivery: baseDelivery, Run: baseRun, Launch: baseLaunch}},
		{name: "accepted without launch", facts: ActiveFacts{Delivery: baseDelivery, Run: &Run{ID: "run", DeliveryID: "delivery", AgentID: "agent", State: RunAccepted, AcceptedAt: now}}},
		{name: "missing delivery", facts: ActiveFacts{Run: baseRun, Launch: baseLaunch}, want: ErrRunIntegrity},
		{name: "running without launch", facts: ActiveFacts{Delivery: baseDelivery, Run: baseRun}, want: ErrRunIntegrity},
		{name: "malformed running run", facts: ActiveFacts{Delivery: baseDelivery, Run: &Run{ID: "run", DeliveryID: "delivery", AgentID: "agent", State: RunRunning, AcceptedAt: now}, Launch: baseLaunch}, want: ErrRunIntegrity},
		{name: "launch belongs to another run", facts: ActiveFacts{Delivery: baseDelivery, Run: baseRun, Launch: &Launch{ID: "launch", RunID: "other", AgentID: "agent", HolderComputerID: "computer", HolderPlacementGeneration: 1, Fence: 1, ClaimedAt: now, ExpiresAt: now.Add(time.Minute)}}, want: ErrRunIntegrity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateActiveFacts(test.facts)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestRunTransitions(t *testing.T) {
	now := time.Unix(100, 0)
	runAccepted := Run{ID: "run", DeliveryID: "delivery", AgentID: "agent", State: RunAccepted, AcceptedAt: now}
	runRunning := Run{ID: "run", DeliveryID: "delivery", AgentID: "agent", State: RunRunning, AcceptedAt: now, StartedAt: timePtr(now)}
	launch := Launch{ID: "launch", RunID: "run", AgentID: "agent", HolderComputerID: "computer", HolderPlacementGeneration: 1, Fence: 1, ClaimedAt: now, ExpiresAt: now.Add(time.Minute)}

	claim, err := CanClaim(runAccepted, nil, now)
	if err != nil || !claim.StartRun || claim.ReplacedLaunchID != "" {
		t.Fatalf("accepted claim = %+v, %v", claim, err)
	}
	if _, err := CanClaim(runRunning, &launch, now); !errors.Is(err, ErrRunLaunchActive) {
		t.Fatalf("active claim error = %v", err)
	}
	claim, err = CanClaim(runRunning, &Launch{ID: "old", RunID: "run", AgentID: "agent", HolderComputerID: "computer", HolderPlacementGeneration: 1, Fence: 1, ClaimedAt: now.Add(-2 * time.Minute), ExpiresAt: now.Add(-time.Second)}, now)
	if err != nil || claim.ReplacedLaunchID != "old" || claim.StartRun {
		t.Fatalf("expired claim = %+v, %v", claim, err)
	}
	if err := CanRenew(runRunning, launch, "launch", 1, true, now); err != nil {
		t.Fatalf("renew error = %v", err)
	}
	if err := CanRenew(runRunning, launch, "other", 1, true, now); !errors.Is(err, ErrRunLaunchStale) {
		t.Fatalf("stale renew error = %v", err)
	}
	if err := CanComplete(ActiveFacts{Delivery: &Delivery{ID: "delivery", AgentID: "agent", State: DeliveryAccepted, AcceptedAt: timePtr(now)}, Run: &runRunning, Launch: &launch}, "launch", 1, true, now); err != nil {
		t.Fatalf("complete error = %v", err)
	}
}

func TestRenewRunTransitionFailures(t *testing.T) {
	now := time.Unix(100, 0)
	run := Run{ID: "run", DeliveryID: "delivery", AgentID: "agent", State: RunRunning, AcceptedAt: now, StartedAt: timePtr(now)}
	launch := Launch{ID: "launch", RunID: "run", AgentID: "agent", HolderComputerID: "computer", HolderPlacementGeneration: 1, Fence: 1, ClaimedAt: now, ExpiresAt: now.Add(time.Minute)}
	tests := []struct {
		name     string
		run      Run
		launch   Launch
		launchID string
		fence    uint64
		held     bool
		at       time.Time
		want     error
	}{
		{name: "accepted run", run: Run{ID: "run", DeliveryID: "delivery", AgentID: "agent", State: RunAccepted, AcceptedAt: now}, launch: launch, launchID: "launch", fence: 1, held: true, at: now, want: ErrRunNotRunning},
		{name: "wrong launch", run: run, launch: launch, launchID: "other", fence: 1, held: true, at: now, want: ErrRunLaunchStale},
		{name: "wrong fence", run: run, launch: launch, launchID: "launch", fence: 2, held: true, at: now, want: ErrRunLaunchStale},
		{name: "wrong holder", run: run, launch: launch, launchID: "launch", fence: 1, at: now, want: ErrRunLaunchStale},
		{name: "expired", run: run, launch: launch, launchID: "launch", fence: 1, held: true, at: launch.ExpiresAt, want: ErrRunLaunchExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := CanRenew(test.run, test.launch, test.launchID, test.fence, test.held, test.at)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateResult(t *testing.T) {
	now := time.Unix(100, 0)
	run := Run{
		ID: "run", DeliveryID: "delivery", AgentID: "agent", State: RunCompleted, Outcome: OutcomeSucceeded,
		ResultKind: ResultMessage, ResultID: "message", AcceptedAt: now, StartedAt: timePtr(now), CompletedAt: timePtr(now.Add(time.Second)),
	}
	if err := ValidateResult(run, ResultMessage, "message", "message", ""); err != nil {
		t.Fatalf("message result = %v", err)
	}
	if err := ValidateResult(run, ResultHeldDraft, "draft", "", "draft"); !errors.Is(err, ErrRunIntegrity) {
		t.Fatalf("mismatched result = %v", err)
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}
