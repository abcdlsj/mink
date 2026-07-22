package application

import "errors"

var (
	ErrNotFound                 = errors.New("computer not found")
	ErrRegistrationKeyMismatch  = errors.New("registration key mismatch")
	ErrPairingInvalid           = errors.New("computer pairing invalid")
	ErrPairingConflict          = errors.New("computer pairing conflict")
	ErrSandboxCapabilityInvalid = errors.New("sandbox capability invalid")
)
