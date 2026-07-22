package store

import authorityapp "github.com/abcdlsj/sumi/internal/authority/application"

var (
	ErrAgentRuntimeBinding         = authorityapp.ErrRuntimeBinding
	ErrAgentRuntimeInvalid         = authorityapp.ErrRuntimeInvalid
	ErrAgentRuntimeUnauthenticated = authorityapp.ErrRuntimeUnauthenticated
)
