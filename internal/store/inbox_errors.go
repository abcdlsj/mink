package store

import (
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
)

var (
	ErrInboxItemNotFound      = executionapp.ErrInboxItemNotFound
	ErrInboxItemNotUnread     = executionapp.ErrInboxItemNotUnread
	ErrInboxItemNotClaimed    = executionapp.ErrInboxItemNotClaimed
	ErrInboxItemHasHeldDraft  = executionapp.ErrInboxItemHasHeldDraft
	ErrInboxRequestConflict   = executionapp.ErrInboxRequestConflict
	ErrInboxAccessLost        = executionapp.ErrInboxAccessLost
	ErrInboxBasisMismatch     = executionapp.ErrInboxBasisMismatch
	ErrInboxTargetAdvanced    = executionapp.ErrInboxTargetAdvanced
	ErrInvalidInboxLimit      = executionapp.ErrInvalidInboxLimit
	ErrInvalidMention         = collaborationapp.ErrInvalidMention
	ErrHeldDraftNotFound      = executionapp.ErrHeldDraftNotFound
	ErrHeldDraftNotHeld       = executionapp.ErrHeldDraftNotHeld
	ErrInvalidDraftResolution = executionapp.ErrInvalidDraftResolution
	ErrInboxIntegrity         = executionapp.ErrInboxIntegrity
)
