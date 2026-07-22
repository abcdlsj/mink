package application

import (
	"errors"

	collaborationdomain "github.com/abcdlsj/sumi/internal/collaboration/domain"
)

var (
	ErrSpaceNotFound                = errors.New("space not found")
	ErrSpaceArchived                = collaborationdomain.ErrSpaceArchived
	ErrDMImmutable                  = collaborationdomain.ErrDMImmutable
	ErrDMRequiresDistinctPrincipals = collaborationdomain.ErrDMRequiresDistinctPrincipals
	ErrInvalidSpaceName             = collaborationdomain.ErrInvalidSpaceName
	ErrInvalidPrincipal             = collaborationdomain.ErrInvalidPrincipal
	ErrMembershipExists             = collaborationdomain.ErrMembershipExists
	ErrMembershipNotFound           = collaborationdomain.ErrMembershipNotFound
	ErrLastActiveHumanMember        = collaborationdomain.ErrLastActiveHumanMember
	ErrRequestConflict              = errors.New("collaboration request conflict")
	ErrMessageNotFound              = errors.New("message not found")
	ErrThreadNotFound               = errors.New("thread not found")
	ErrInvalidMessageTarget         = collaborationdomain.ErrInvalidMessageTarget
	ErrInvalidMessageBody           = collaborationdomain.ErrInvalidMessageBody
	ErrInvalidMention               = errors.New("invalid message mention")
	ErrInvalidMessageLimit          = errors.New("invalid message limit")
	ErrIntegrity                    = errors.New("collaboration data integrity failure")
)
