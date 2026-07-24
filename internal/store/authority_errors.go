package store

import (
	"errors"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
	organizationapp "github.com/abcdlsj/sumi/internal/organization/application"
	workapp "github.com/abcdlsj/sumi/internal/work/application"
)

var (
	ErrAuthorityNotBootstrapped = errors.New("authority not bootstrapped")
	ErrAuthorityMismatch        = errors.New("authority bootstrap mismatch")
	ErrHumanNotFound            = organizationapp.ErrHumanNotFound
	ErrHumanNameExists          = organizationapp.ErrHumanNameExists
	ErrHumanAccountExists       = organizationapp.ErrHumanAccountExists
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
	ErrBrowserSessionInvalid    = authorityapp.ErrBrowserSessionInvalid
	ErrLocalAccountInvalid      = authorityapp.ErrLocalAccountInvalid
	ErrRegistrationClosed       = authorityapp.ErrRegistrationClosed
	ErrWorkNotFound             = workapp.ErrNotFound
	ErrWorkRequestConflict      = workapp.ErrRequestConflict
	ErrWorkInvalid              = workapp.ErrInvalid
	ErrWorkTransitionInvalid    = workapp.ErrTransitionInvalid
	ErrWorkTerminal             = workapp.ErrTerminal
	ErrWorkAcceptanceIncomplete = workapp.ErrAcceptanceIncomplete
	ErrWorkApprovalNotFound     = workapp.ErrApprovalNotFound
	ErrWorkApprovalConflict     = workapp.ErrApprovalConflict
	ErrWorkAssignmentConflict   = workapp.ErrAssignmentConflict
	ErrWorkPlacementInvalid     = workapp.ErrPlacementInvalid
)
