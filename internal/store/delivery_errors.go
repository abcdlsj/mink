package store

import "errors"

var (
	ErrDeliveryNotFound          = errors.New("delivery not found")
	ErrDeliveryNotAvailable      = errors.New("delivery is not available")
	ErrDeliveryCursorUnavailable = errors.New("delivery cursor unavailable")
	ErrInvalidDeliveryLimit      = errors.New("invalid delivery limit")
	ErrRunAlreadyActive          = errors.New("agent already has an active run")
	ErrRunNotFound               = errors.New("run not found")
	ErrRunNotAccepted            = errors.New("run is not accepted")
	ErrRunNotRunning             = errors.New("run is not running")
	ErrRunLaunchActive           = errors.New("run launch is active")
	ErrRunLaunchStale            = errors.New("run launch is stale")
	ErrRunLaunchExpired          = errors.New("run launch is expired")
	ErrRunCompletionConflict     = errors.New("run completion conflict")
	ErrRunInvalidOutcome         = errors.New("invalid run outcome")
	ErrRunIntegrity              = errors.New("run data integrity failure")
)
