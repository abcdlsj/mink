package store

import agentapp "github.com/abcdlsj/sumi/internal/agent/application"

var (
	ErrAgentNotFound                    = agentapp.ErrNotFound
	ErrAgentRequestConflict             = agentapp.ErrRequestConflict
	ErrAgentHandleExists                = agentapp.ErrHandleExists
	ErrAgentRevisionConflict            = agentapp.ErrRevisionConflict
	ErrAgentRuntimeSpecMissing          = agentapp.ErrRuntimeSpecMissing
	ErrAgentRuntimeSpecRevisionConflict = agentapp.ErrRuntimeSpecRevisionConflict
)
