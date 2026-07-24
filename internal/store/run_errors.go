package store

import executionapp "github.com/abcdlsj/sumi/internal/execution/application"

var (
	ErrInvalidRunLimit       = executionapp.ErrInvalidRunLimit
	ErrRunAlreadyActive      = executionapp.ErrRunAlreadyActive
	ErrRunNotFound           = executionapp.ErrRunNotFound
	ErrRunNotQueued          = executionapp.ErrRunNotQueued
	ErrRunNotRunning         = executionapp.ErrRunNotRunning
	ErrRunLeaseActive        = executionapp.ErrRunLeaseActive
	ErrRunLeaseStale         = executionapp.ErrRunLeaseStale
	ErrRunLeaseExpired       = executionapp.ErrRunLeaseExpired
	ErrRunRequestConflict    = executionapp.ErrRunRequestConflict
	ErrRunCompletionConflict = executionapp.ErrRunCompletionConflict
	ErrRunInvalidOutcome     = executionapp.ErrRunInvalidOutcome
	ErrRunIntegrity          = executionapp.ErrRunIntegrity
)
