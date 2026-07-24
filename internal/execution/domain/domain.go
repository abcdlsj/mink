package domain

import (
	"errors"
	"time"
)

type RunState string

const (
	RunQueued    RunState = "queued"
	RunRunning   RunState = "running"
	RunSucceeded RunState = "succeeded"
	RunFailed    RunState = "failed"
	RunCancelled RunState = "cancelled"
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

type Run struct {
	State                    RunState
	InputBasisTargetSequence uint64
	Attempt                  uint64
	LeaseHolderComputerID    string
	LeaseExpiresAt           *time.Time
	Fence                    uint64
	PlacementDesiredRevision uint64
	ResultKind               ResultKind
	ResultID                 string
	ErrorCode                string
	StartedAt                *time.Time
	CompletedAt              *time.Time
	CancelledAt              *time.Time
}

var (
	ErrRunAlreadyActive  = errors.New("agent already has an active run")
	ErrRunNotQueued      = errors.New("run is not queued")
	ErrRunNotRunning     = errors.New("run is not running")
	ErrRunLeaseActive    = errors.New("run lease is active")
	ErrRunLeaseStale     = errors.New("run lease is stale")
	ErrRunLeaseExpired   = errors.New("run lease is expired")
	ErrRunInvalidOutcome = errors.New("invalid run outcome")
	ErrRunIntegrity      = errors.New("run data integrity failure")
)

func ValidateRun(run Run) error {
	switch run.State {
	case RunQueued:
		if run.InputBasisTargetSequence != 0 || run.Attempt != 0 || run.LeaseHolderComputerID != "" ||
			run.LeaseExpiresAt != nil || run.Fence != 0 || run.PlacementDesiredRevision != 0 ||
			run.ResultKind != "" || run.ResultID != "" || run.ErrorCode != "" || run.StartedAt != nil ||
			run.CompletedAt != nil || run.CancelledAt != nil {
			return ErrRunIntegrity
		}
	case RunRunning:
		if run.InputBasisTargetSequence == 0 || run.Attempt == 0 || run.LeaseHolderComputerID == "" ||
			run.LeaseExpiresAt == nil || run.Fence == 0 || run.PlacementDesiredRevision == 0 ||
			run.ResultKind != "" || run.ResultID != "" || run.ErrorCode != "" || run.StartedAt == nil ||
			run.CompletedAt != nil || run.CancelledAt != nil {
			return ErrRunIntegrity
		}
	case RunSucceeded, RunFailed:
		if run.InputBasisTargetSequence == 0 || run.Attempt == 0 || run.LeaseHolderComputerID == "" ||
			run.LeaseExpiresAt == nil || run.Fence == 0 || run.PlacementDesiredRevision == 0 ||
			run.ResultKind == "" || run.ResultID == "" || run.StartedAt == nil || run.CompletedAt == nil ||
			run.CancelledAt != nil {
			return ErrRunIntegrity
		}
		if run.State == RunSucceeded && run.ErrorCode != "" {
			return ErrRunIntegrity
		}
		if run.State == RunFailed && run.ErrorCode == "" {
			return ErrRunIntegrity
		}
	case RunCancelled:
		unclaimed := run.Attempt == 0 && run.Fence == 0 && run.PlacementDesiredRevision == 0 &&
			run.LeaseHolderComputerID == "" && run.LeaseExpiresAt == nil && run.StartedAt == nil
		claimed := run.Attempt > 0 && run.Fence > 0 && run.PlacementDesiredRevision > 0 &&
			run.LeaseHolderComputerID != "" && run.LeaseExpiresAt != nil && run.StartedAt != nil
		if (!unclaimed && !claimed) || run.CancelledAt == nil || run.ResultKind != "" || run.ResultID != "" ||
			run.ErrorCode != "" || run.CompletedAt != nil {
			return ErrRunIntegrity
		}
	default:
		return ErrRunIntegrity
	}
	if run.ResultKind != "" && run.ResultKind != ResultMessage && run.ResultKind != ResultHeldDraft {
		return ErrRunIntegrity
	}
	return nil
}

func CanClaim(run Run, now time.Time) error {
	if err := ValidateRun(run); err != nil {
		return err
	}
	switch run.State {
	case RunQueued:
		return nil
	case RunRunning:
		if run.LeaseExpiresAt.After(now) {
			return ErrRunLeaseActive
		}
		return nil
	default:
		return ErrRunNotQueued
	}
}

func ValidateLease(run Run, computerID string, rev, attempt, fence uint64, now time.Time) error {
	if err := ValidateRun(run); err != nil {
		return err
	}
	if run.State != RunRunning {
		return ErrRunNotRunning
	}
	if run.LeaseHolderComputerID != computerID || run.PlacementDesiredRevision != rev ||
		run.Attempt != attempt || run.Fence != fence {
		return ErrRunLeaseStale
	}
	if !run.LeaseExpiresAt.After(now) {
		return ErrRunLeaseExpired
	}
	return nil
}

func ValidateOutcome(outcome Outcome) error {
	if outcome != OutcomeSucceeded && outcome != OutcomeFailed {
		return ErrRunInvalidOutcome
	}
	return nil
}
