package application

import (
	"errors"

	executiondomain "github.com/abcdlsj/sumi/internal/execution/domain"
)

var (
	ErrInboxItemNotFound      = errors.New("inbox item not found")
	ErrInboxItemNotUnread     = errors.New("inbox item is not unread")
	ErrInboxItemNotClaimed    = errors.New("inbox item is not claimed")
	ErrInboxItemHasHeldDraft  = errors.New("inbox item already has a held draft")
	ErrInboxRequestConflict   = errors.New("inbox request conflict")
	ErrInboxAccessLost        = errors.New("inbox item access lost")
	ErrInboxBasisMismatch     = errors.New("inbox target basis does not match observed cursor")
	ErrInboxTargetAdvanced    = errors.New("inbox target advanced")
	ErrInvalidInboxLimit      = errors.New("invalid inbox list limit")
	ErrHeldDraftNotFound      = errors.New("held draft not found")
	ErrHeldDraftNotHeld       = errors.New("held draft is not held")
	ErrInvalidDraftResolution = errors.New("invalid held draft resolution")
	ErrInboxIntegrity         = errors.New("inbox data integrity failure")
	ErrInvalidRunLimit        = errors.New("invalid run limit")
	ErrRunAlreadyActive       = executiondomain.ErrRunAlreadyActive
	ErrRunNotFound            = errors.New("run not found")
	ErrRunNotQueued           = executiondomain.ErrRunNotQueued
	ErrRunNotRunning          = executiondomain.ErrRunNotRunning
	ErrRunLeaseActive         = executiondomain.ErrRunLeaseActive
	ErrRunLeaseStale          = executiondomain.ErrRunLeaseStale
	ErrRunLeaseExpired        = executiondomain.ErrRunLeaseExpired
	ErrRunRequestConflict     = errors.New("run request conflict")
	ErrRunCompletionConflict  = errors.New("run completion conflict")
	ErrRunInvalidOutcome      = executiondomain.ErrRunInvalidOutcome
	ErrRunIntegrity           = executiondomain.ErrRunIntegrity
)
