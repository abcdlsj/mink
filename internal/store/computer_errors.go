package store

import "errors"

var (
	ErrComputerNotFound        = errors.New("computer not found")
	ErrRegistrationKeyMismatch = errors.New("registration key mismatch")
)
