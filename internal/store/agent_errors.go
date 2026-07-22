package store

import agentapp "github.com/abcdlsj/sumi/internal/agent/application"

var (
	ErrAgentNotFound        = agentapp.ErrNotFound
	ErrAgentRequestConflict = agentapp.ErrRequestConflict
	ErrAgentNameExists      = agentapp.ErrNameExists
)
