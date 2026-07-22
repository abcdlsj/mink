package store

import (
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	collaborationdomain "github.com/abcdlsj/sumi/internal/collaboration/domain"
)

var (
	ErrSpaceNotFound                = collaborationapp.ErrSpaceNotFound
	ErrSpaceArchived                = collaborationdomain.ErrSpaceArchived
	ErrDMImmutable                  = collaborationdomain.ErrDMImmutable
	ErrDMRequiresDistinctPrincipals = collaborationdomain.ErrDMRequiresDistinctPrincipals
	ErrInvalidSpaceName             = collaborationdomain.ErrInvalidSpaceName
	ErrInvalidPrincipal             = collaborationdomain.ErrInvalidPrincipal
	ErrMembershipExists             = collaborationdomain.ErrMembershipExists
	ErrMembershipNotFound           = collaborationdomain.ErrMembershipNotFound
	ErrLastActiveHumanMember        = collaborationdomain.ErrLastActiveHumanMember
	ErrCollaborationRequestConflict = collaborationapp.ErrRequestConflict
	ErrMessageNotFound              = collaborationapp.ErrMessageNotFound
	ErrThreadNotFound               = collaborationapp.ErrThreadNotFound
	ErrInvalidMessageTarget         = collaborationdomain.ErrInvalidMessageTarget
	ErrInvalidMessageBody           = collaborationdomain.ErrInvalidMessageBody
	ErrInvalidMessageLimit          = collaborationapp.ErrInvalidMessageLimit
	ErrCollaborationIntegrity       = collaborationapp.ErrIntegrity
)
