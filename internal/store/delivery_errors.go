package store

import (
	"errors"

	execution "github.com/abcdlsj/sumi/internal/execution/domain"
)

var (
	ErrDeliveryNotFound          = errors.New("delivery not found")
	ErrDeliveryNotAvailable      = execution.ErrDeliveryNotAvailable
	ErrDeliveryCursorUnavailable = execution.ErrDeliveryCursorUnavailable
	ErrInvalidDeliveryLimit      = errors.New("invalid delivery limit")
	ErrRunAlreadyActive          = execution.ErrRunAlreadyActive
	ErrRunNotFound               = errors.New("run not found")
	ErrRunNotAccepted            = execution.ErrRunNotAccepted
	ErrRunNotRunning             = execution.ErrRunNotRunning
	ErrRunLaunchActive           = execution.ErrRunLaunchActive
	ErrRunLaunchStale            = execution.ErrRunLaunchStale
	ErrRunLaunchExpired          = execution.ErrRunLaunchExpired
	ErrRunCompletionConflict     = errors.New("run completion conflict")
	ErrRunInvalidOutcome         = execution.ErrRunInvalidOutcome
	ErrRunIntegrity              = execution.ErrRunIntegrity
)
