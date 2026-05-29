package task

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrSpaceIDRequired   = errors.New("task: space_id required")
	ErrInitiatorRequired = errors.New("task: initiator_id required")
	ErrWorkerRequired    = errors.New("task: worker_id required")
	ErrTitleRequired     = errors.New("task: title required")
	ErrTitleTooLong      = errors.New("task: title exceeds limit")
	ErrOutcomeTooLong    = errors.New("task: outcome exceeds limit")
	ErrKeyStepKind       = errors.New("task: key_step kind not allowed")
	ErrKeyStepTitle      = errors.New("task: key_step title invalid")
	ErrKeyStepOverflow   = errors.New("task: too many key_steps")
)

func NewID() string {
	return "task-" + uuid.NewString()[:8]
}

func NewRunID() string {
	return "run-" + uuid.NewString()[:8]
}

func ValidateTask(t Task) error {
	if strings.TrimSpace(t.SpaceID) == "" {
		return ErrSpaceIDRequired
	}
	if strings.TrimSpace(t.InitiatorID) == "" {
		return ErrInitiatorRequired
	}
	if strings.TrimSpace(t.WorkerID) == "" {
		return ErrWorkerRequired
	}
	title := strings.TrimSpace(t.Title)
	if title == "" {
		return ErrTitleRequired
	}
	if runeLen(title) > MaxTitleLen {
		return ErrTitleTooLong
	}
	if runeLen(strings.TrimSpace(t.Outcome)) > MaxOutcomeLen {
		return ErrOutcomeTooLong
	}
	return nil
}

func ValidateKeyStep(s KeyStep) error {
	switch s.Kind {
	case KindRead, KindWrite, KindRun, KindSubtask, KindSummary, KindError:
	default:
		return ErrKeyStepKind
	}
	title := strings.TrimSpace(s.Title)
	if title == "" {
		return ErrKeyStepTitle
	}
	if runeLen(title) > MaxTitleLen {
		return ErrKeyStepTitle
	}
	return nil
}

func ValidateKeySteps(steps []KeyStep) error {
	if len(steps) > MaxKeySteps {
		return ErrKeyStepOverflow
	}
	for _, s := range steps {
		if err := ValidateKeyStep(s); err != nil {
			return err
		}
	}
	return nil
}

func runeLen(s string) int {
	return len([]rune(s))
}
