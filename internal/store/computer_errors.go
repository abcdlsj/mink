package store

import "errors"

var (
	ErrComputerNotFound        = errors.New("computer not found")
	ErrRegistrationKeyMismatch = errors.New("registration key mismatch")
	ErrComputerPairingInvalid  = errors.New("computer pairing invalid")
	ErrComputerPairingConflict = errors.New("computer pairing conflict")
)
