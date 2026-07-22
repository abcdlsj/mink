package application

import "errors"

var (
	ErrNotFound        = errors.New("agent placement not found")
	ErrAgentNotFound   = errors.New("agent not found")
	ErrStale           = errors.New("agent placement acknowledgement is stale")
	ErrConflict        = errors.New("agent placement acknowledgement conflicts with current state")
	ErrRequestConflict = errors.New("agent placement request conflict")
)
