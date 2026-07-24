package application

import (
	"errors"

	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
)

var (
	ErrNotFound                 = errors.New("agent placement not found")
	ErrAgentNotFound            = errors.New("agent not found")
	ErrStale                    = errors.New("agent placement acknowledgement is stale")
	ErrConflict                 = errors.New("agent placement acknowledgement conflicts with current state")
	ErrRequestConflict          = errors.New("agent placement request conflict")
	ErrRuntimeSpecMissing       = agentapp.ErrRuntimeSpecMissing
	ErrCredentialBindingInvalid = errors.New("agent runtime credential binding does not match placement")
)
