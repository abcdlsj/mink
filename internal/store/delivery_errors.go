package store

import (
	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
)

var (
	ErrDeliveryNotFound          = executionapp.ErrDeliveryNotFound
	ErrDeliveryNotAvailable      = executionapp.ErrDeliveryNotAvailable
	ErrDeliveryCursorUnavailable = executionapp.ErrDeliveryCursorUnavailable
	ErrInvalidDeliveryLimit      = executionapp.ErrInvalidDeliveryLimit
	ErrRunAlreadyActive          = executionapp.ErrRunAlreadyActive
	ErrRunNotFound               = executionapp.ErrRunNotFound
	ErrRunNotAccepted            = executionapp.ErrRunNotAccepted
	ErrRunNotRunning             = executionapp.ErrRunNotRunning
	ErrRunLaunchActive           = executionapp.ErrRunLaunchActive
	ErrRunLaunchStale            = executionapp.ErrRunLaunchStale
	ErrRunLaunchExpired          = executionapp.ErrRunLaunchExpired
	ErrRunCompletionConflict     = executionapp.ErrRunCompletionConflict
	ErrRunInvalidOutcome         = executionapp.ErrRunInvalidOutcome
	ErrRunIntegrity              = executionapp.ErrRunIntegrity
)
