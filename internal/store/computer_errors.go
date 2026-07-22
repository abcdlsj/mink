package store

import computerapp "github.com/abcdlsj/sumi/internal/computer/application"

var (
	ErrComputerNotFound         = computerapp.ErrNotFound
	ErrRegistrationKeyMismatch  = computerapp.ErrRegistrationKeyMismatch
	ErrComputerPairingInvalid   = computerapp.ErrPairingInvalid
	ErrComputerPairingConflict  = computerapp.ErrPairingConflict
	ErrSandboxCapabilityInvalid = computerapp.ErrSandboxCapabilityInvalid
)
