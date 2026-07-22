package application

import "errors"

var (
	ErrRuntimeBinding         = errors.New("agent runtime binding unavailable")
	ErrRuntimeInvalid         = errors.New("agent runtime session parameters invalid")
	ErrRuntimeUnauthenticated = errors.New("agent runtime session unauthenticated")
	ErrBrowserHandoffInvalid  = errors.New("browser handoff invalid")
	ErrBrowserSessionInvalid  = errors.New("browser session invalid")
)
