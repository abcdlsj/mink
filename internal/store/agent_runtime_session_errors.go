package store

import "errors"

var (
	ErrAgentRuntimeBinding         = errors.New("agent runtime binding unavailable")
	ErrAgentRuntimeInvalid         = errors.New("agent runtime session parameters invalid")
	ErrAgentRuntimeUnauthenticated = errors.New("agent runtime session unauthenticated")
)
