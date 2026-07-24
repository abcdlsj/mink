package application

import "errors"

var (
	ErrNotFound                   = errors.New("computer not found")
	ErrRegistrationKeyMismatch    = errors.New("registration key mismatch")
	ErrPairingInvalid             = errors.New("computer pairing invalid")
	ErrPairingConflict            = errors.New("computer pairing conflict")
	ErrCapabilityInventoryInvalid = errors.New("capability inventory invalid")
	ErrCredentialDeliveryInvalid  = errors.New("credential delivery invalid")
	ErrCredentialDeliveryConflict = errors.New("credential delivery conflict")
	ErrCredentialDeliveryDenied   = errors.New("credential delivery denied")
)
