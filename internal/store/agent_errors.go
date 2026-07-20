package store

import "errors"

var (
	ErrAgentNotFound        = errors.New("agent not found")
	ErrAgentRequestConflict = errors.New("agent request payload conflict")
	ErrAgentNameExists      = errors.New("agent name already exists")
)
