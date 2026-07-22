package domain

import (
	"errors"
	"time"
)

type DeliveryState string

const (
	DeliveryAvailable DeliveryState = "available"
	DeliveryAccepted  DeliveryState = "accepted"
	DeliveryCompleted DeliveryState = "completed"
)

type InboxState string

const (
	InboxUnread  InboxState = "unread"
	InboxClaimed InboxState = "claimed"
)

type RunState string

const (
	RunAccepted  RunState = "accepted"
	RunRunning   RunState = "running"
	RunCompleted RunState = "completed"
)

type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
)

type ResultKind string

const (
	ResultMessage   ResultKind = "message"
	ResultHeldDraft ResultKind = "held_draft"
)

type CloseReason string

const (
	CloseReplaced  CloseReason = "replaced"
	CloseCompleted CloseReason = "completed"
)

type Delivery struct {
	ID                    string
	AgentID               string
	TargetKind            string
	TargetID              string
	TriggerTargetSequence uint64
	State                 DeliveryState
	AcceptedAt            *time.Time
	CompletedAt           *time.Time
}

type Run struct {
	ID                  string
	DeliveryID          string
	AgentID             string
	BasisTargetSequence uint64
	State               RunState
	Outcome             Outcome
	ResultKind          ResultKind
	ResultID            string
	AcceptedAt          time.Time
	StartedAt           *time.Time
	CompletedAt         *time.Time
}

type Launch struct {
	ID                        string
	RunID                     string
	AgentID                   string
	HolderComputerID          string
	HolderPlacementGeneration uint64
	Fence                     uint64
	ClaimedAt                 time.Time
	ExpiresAt                 time.Time
	ClosedAt                  *time.Time
	CloseReason               CloseReason
}

type ActiveFacts struct {
	Delivery *Delivery
	Run      *Run
	Launch   *Launch
}

type ClaimDecision struct {
	ReplacedLaunchID string
	StartRun         bool
}

var (
	ErrDeliveryNotAvailable      = errors.New("delivery is not available")
	ErrDeliveryCursorUnavailable = errors.New("delivery cursor unavailable")
	ErrRunAlreadyActive          = errors.New("agent already has an active run")
	ErrRunNotAccepted            = errors.New("run is not accepted")
	ErrRunNotRunning             = errors.New("run is not running")
	ErrRunLaunchActive           = errors.New("run launch is active")
	ErrRunLaunchStale            = errors.New("run launch is stale")
	ErrRunLaunchExpired          = errors.New("run launch is expired")
	ErrRunInvalidOutcome         = errors.New("invalid run outcome")
	ErrRunIntegrity              = errors.New("run data integrity failure")
)

func ValidateActiveFacts(facts ActiveFacts) error {
	if facts.Run == nil {
		if facts.Delivery != nil || facts.Launch != nil {
			return ErrRunIntegrity
		}
		return nil
	}
	if facts.Delivery == nil || facts.Delivery.ID != facts.Run.DeliveryID || facts.Delivery.AgentID != facts.Run.AgentID ||
		facts.Delivery.State != DeliveryAccepted {
		return ErrRunIntegrity
	}
	if err := ValidateDelivery(*facts.Delivery); err != nil {
		return err
	}
	if err := ValidateRun(*facts.Run); err != nil {
		return err
	}
	switch facts.Run.State {
	case RunAccepted:
		if facts.Launch != nil {
			return ErrRunIntegrity
		}
	case RunRunning:
		if facts.Launch == nil {
			return ErrRunIntegrity
		}
	case RunCompleted:
		return ErrRunIntegrity
	default:
		return ErrRunIntegrity
	}
	if facts.Launch != nil && (facts.Launch.RunID != facts.Run.ID || facts.Launch.AgentID != facts.Run.AgentID ||
		facts.Launch.ClosedAt != nil || facts.Launch.CloseReason != "") {
		return ErrRunIntegrity
	}
	if facts.Launch != nil {
		return ValidateLaunch(*facts.Launch)
	}
	return nil
}

func ValidateDelivery(delivery Delivery) error {
	switch delivery.State {
	case DeliveryAvailable:
		if delivery.AcceptedAt != nil || delivery.CompletedAt != nil {
			return ErrRunIntegrity
		}
	case DeliveryAccepted:
		if delivery.AcceptedAt == nil || delivery.CompletedAt != nil {
			return ErrRunIntegrity
		}
	case DeliveryCompleted:
		if delivery.AcceptedAt == nil || delivery.CompletedAt == nil {
			return ErrRunIntegrity
		}
	default:
		return ErrRunIntegrity
	}
	return nil
}

func ValidateRun(run Run) error {
	switch run.State {
	case RunAccepted:
		if run.Outcome != "" || run.ResultKind != "" || run.ResultID != "" || run.StartedAt != nil || run.CompletedAt != nil {
			return ErrRunIntegrity
		}
	case RunRunning:
		if run.Outcome != "" || run.ResultKind != "" || run.ResultID != "" || run.StartedAt == nil || run.CompletedAt != nil {
			return ErrRunIntegrity
		}
	case RunCompleted:
		if run.Outcome == "" || run.ResultKind == "" || run.ResultID == "" || run.StartedAt == nil || run.CompletedAt == nil {
			return ErrRunIntegrity
		}
	default:
		return ErrRunIntegrity
	}
	if run.Outcome != "" && run.Outcome != OutcomeSucceeded && run.Outcome != OutcomeFailed {
		return ErrRunIntegrity
	}
	if run.ResultKind != "" && run.ResultKind != ResultMessage && run.ResultKind != ResultHeldDraft {
		return ErrRunIntegrity
	}
	if run.ResultKind == "" && run.ResultID != "" {
		return ErrRunIntegrity
	}
	return nil
}

func ValidateLaunch(launch Launch) error {
	switch launch.CloseReason {
	case "":
		if launch.ClosedAt != nil {
			return ErrRunIntegrity
		}
	case CloseReplaced:
		if launch.ClosedAt == nil || launch.ClosedAt.Before(launch.ExpiresAt) {
			return ErrRunIntegrity
		}
	case CloseCompleted:
		if launch.ClosedAt == nil || launch.ClosedAt.Before(launch.ClaimedAt) {
			return ErrRunIntegrity
		}
	default:
		return ErrRunIntegrity
	}
	if launch.Fence == 0 || launch.HolderComputerID == "" || launch.HolderPlacementGeneration == 0 || !launch.ExpiresAt.After(launch.ClaimedAt) {
		return ErrRunIntegrity
	}
	return nil
}

func ValidateResult(run Run, kind ResultKind, resultID string, messageID, heldDraftID string) error {
	if err := ValidateRun(run); err != nil {
		return err
	}
	if run.State != RunCompleted || run.ResultKind != kind || run.ResultID != resultID {
		return ErrRunIntegrity
	}
	switch kind {
	case ResultMessage:
		if messageID == "" || heldDraftID != "" || resultID != messageID {
			return ErrRunIntegrity
		}
	case ResultHeldDraft:
		if heldDraftID == "" || messageID != "" || resultID != heldDraftID {
			return ErrRunIntegrity
		}
	default:
		return ErrRunIntegrity
	}
	return nil
}

func CanAccept(deliveryState DeliveryState, inboxState InboxState, cursorAvailable, activeRun bool) error {
	if deliveryState != DeliveryAvailable || (inboxState != InboxUnread && inboxState != InboxClaimed) {
		return ErrDeliveryNotAvailable
	}
	if !cursorAvailable {
		return ErrDeliveryCursorUnavailable
	}
	if activeRun {
		return ErrRunAlreadyActive
	}
	return nil
}

func CanClaim(run Run, currentLaunch *Launch, now time.Time) (ClaimDecision, error) {
	if err := ValidateRun(run); err != nil {
		return ClaimDecision{}, err
	}
	if currentLaunch != nil {
		if err := ValidateLaunch(*currentLaunch); err != nil {
			return ClaimDecision{}, err
		}
	}
	switch run.State {
	case RunAccepted:
		if currentLaunch != nil {
			return ClaimDecision{}, ErrRunIntegrity
		}
		return ClaimDecision{StartRun: true}, nil
	case RunRunning:
		if currentLaunch == nil {
			return ClaimDecision{}, ErrRunIntegrity
		}
		if currentLaunch.ExpiresAt.After(now) {
			return ClaimDecision{}, ErrRunLaunchActive
		}
		return ClaimDecision{ReplacedLaunchID: currentLaunch.ID}, nil
	default:
		return ClaimDecision{}, ErrRunNotAccepted
	}
}

func CanRenew(run Run, launch Launch, launchID string, fence uint64, heldByCurrentRuntime bool, now time.Time) error {
	if err := ValidateRun(run); err != nil {
		return err
	}
	if err := ValidateLaunch(launch); err != nil {
		return err
	}
	if run.State != RunRunning {
		return ErrRunNotRunning
	}
	if launch.ID != launchID || launch.Fence != fence || !heldByCurrentRuntime {
		return ErrRunLaunchStale
	}
	if !launch.ExpiresAt.After(now) {
		return ErrRunLaunchExpired
	}
	return nil
}

func CanComplete(facts ActiveFacts, launchID string, fence uint64, heldByCurrentRuntime bool, now time.Time) error {
	if err := ValidateActiveFacts(facts); err != nil {
		return err
	}
	if facts.Run == nil || facts.Delivery == nil || facts.Launch == nil || facts.Run.State != RunRunning || facts.Delivery.State != DeliveryAccepted {
		return ErrRunNotRunning
	}
	if facts.Launch.ID != launchID || facts.Launch.Fence != fence || !heldByCurrentRuntime {
		return ErrRunLaunchStale
	}
	if !facts.Launch.ExpiresAt.After(now) {
		return ErrRunLaunchExpired
	}
	return nil
}

func ValidateOutcome(outcome Outcome) error {
	if outcome != OutcomeSucceeded && outcome != OutcomeFailed {
		return ErrRunInvalidOutcome
	}
	return nil
}
