package store

import "errors"

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
	ErrInvalidMention         = errors.New("invalid message mention")
	ErrHeldDraftNotFound      = errors.New("held draft not found")
	ErrHeldDraftNotHeld       = errors.New("held draft is not held")
	ErrInvalidDraftResolution = errors.New("invalid held draft resolution")
	ErrInboxIntegrity         = errors.New("inbox data integrity failure")
)
