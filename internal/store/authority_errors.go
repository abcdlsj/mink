package store

import (
	"errors"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
	organizationapp "github.com/abcdlsj/sumi/internal/organization/application"
)

var (
	ErrAuthorityNotBootstrapped = errors.New("authority not bootstrapped")
	ErrAuthorityMismatch        = errors.New("authority bootstrap mismatch")
	ErrHumanNotFound            = organizationapp.ErrHumanNotFound
	ErrHumanNameExists          = organizationapp.ErrHumanNameExists
	ErrHumanCredentialExists    = organizationapp.ErrHumanCredentialExists
	ErrHumanRequestConflict     = organizationapp.ErrHumanRequestConflict
	ErrHumanStatusConflict      = organizationapp.ErrHumanStatusConflict
	ErrGrantNotFound            = grantapp.ErrNotFound
	ErrGrantRequestConflict     = grantapp.ErrIssueConflict
	ErrGrantRevokeConflict      = grantapp.ErrRevokeConflict
	ErrGrantInvalid             = grantapp.ErrInvalid
	ErrPermissionDenied         = authoritydomain.ErrPermissionDenied
	ErrPrincipalNotFound        = authoritydomain.ErrPrincipalNotFound
	ErrScopeNotFound            = errors.New("scope not found")
	ErrParentGrantInvalid       = grantapp.ErrParentInvalid
	ErrGrantExpansion           = grantapp.ErrExpansion
	ErrLastOwner                = grantapp.ErrLastOwner
	ErrBrowserHandoffInvalid    = authorityapp.ErrBrowserHandoffInvalid
	ErrBrowserSessionInvalid    = authorityapp.ErrBrowserSessionInvalid
	ErrWorkNotFound             = errors.New("work not found")
	ErrWorkRequestConflict      = errors.New("work request conflict")
	ErrWorkInvalid              = errors.New("work invalid")
	ErrWorkTransitionInvalid    = errors.New("invalid work transition")
	ErrWorkTerminal             = errors.New("work is terminal")
	ErrWorkAcceptanceIncomplete = errors.New("work acceptance criteria incomplete")
	ErrWorkApprovalNotFound     = errors.New("work approval not found")
	ErrWorkApprovalConflict     = errors.New("work approval conflict")
	ErrWorkAssignmentConflict   = errors.New("work assignment conflict")
	ErrWorkPlacementInvalid     = errors.New("work assignment placement invalid")
)
