package application

import "errors"

var (
	ErrNotFound                    = errors.New("agent not found")
	ErrRequestConflict             = errors.New("agent request payload conflict")
	ErrHandleExists                = errors.New("agent handle already exists")
	ErrRevisionConflict            = errors.New("agent profile revision conflict")
	ErrRuntimeSpecMissing          = errors.New("agent runtime spec missing")
	ErrRuntimeSpecRevisionConflict = errors.New("agent runtime spec revision conflict")
)
