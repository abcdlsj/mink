package store

import "errors"

var (
	ErrSpaceNotFound                = errors.New("space not found")
	ErrSpaceArchived                = errors.New("space is archived")
	ErrDMImmutable                  = errors.New("dm membership and lifecycle are immutable")
	ErrDMRequiresDistinctPrincipals = errors.New("dm requires two distinct principals")
	ErrInvalidSpaceName             = errors.New("invalid space name")
	ErrInvalidPrincipal             = errors.New("invalid principal")
	ErrMembershipExists             = errors.New("membership already exists")
	ErrMembershipNotFound           = errors.New("membership not found")
	ErrLastActiveHumanMember        = errors.New("cannot remove last active human member")
	ErrCollaborationRequestConflict = errors.New("collaboration request conflict")
	ErrMessageNotFound              = errors.New("message not found")
	ErrThreadNotFound               = errors.New("thread not found")
	ErrInvalidMessageTarget         = errors.New("invalid message target")
	ErrInvalidMessageBody           = errors.New("invalid message body")
	ErrInvalidMessageLimit          = errors.New("invalid message limit")
	ErrCollaborationIntegrity       = errors.New("collaboration data integrity failure")
)
