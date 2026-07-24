package application

import "errors"

var (
	ErrRuntimeBinding         = errors.New("agent runtime binding unavailable")
	ErrRuntimeInvalid         = errors.New("agent runtime session parameters invalid")
	ErrRuntimeUnauthenticated = errors.New("agent runtime session unauthenticated")
	ErrBrowserSessionInvalid  = errors.New("browser session invalid")
	ErrLocalAccountInvalid    = errors.New("local account invalid")
	ErrRegistrationClosed     = errors.New("first owner registration closed")
)
