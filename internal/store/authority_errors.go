package store

import (
	"errors"

	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
)

var (
	ErrAuthorityNotBootstrapped = errors.New("authority not bootstrapped")
	ErrAuthorityMismatch        = errors.New("authority bootstrap mismatch")
	ErrHumanNotFound            = errors.New("human not found")
	ErrHumanNameExists          = errors.New("human name exists")
	ErrHumanCredentialExists    = errors.New("human credential exists")
	ErrHumanRequestConflict     = errors.New("human request conflict")
	ErrHumanStatusConflict      = errors.New("human status request conflict")
	ErrGrantNotFound            = grantapp.ErrNotFound
	ErrGrantRequestConflict     = grantapp.ErrIssueConflict
	ErrGrantRevokeConflict      = grantapp.ErrRevokeConflict
	ErrGrantInvalid             = grantapp.ErrInvalid
	ErrPermissionDenied         = authoritydomain.ErrPermissionDenied
	ErrPrincipalNotFound        = errors.New("principal not found")
	ErrScopeNotFound            = errors.New("scope not found")
	ErrParentGrantInvalid       = grantapp.ErrParentInvalid
	ErrGrantExpansion           = grantapp.ErrExpansion
	ErrLastOwner                = grantapp.ErrLastOwner
	ErrBrowserHandoffInvalid    = errors.New("browser handoff invalid")
	ErrBrowserSessionInvalid    = errors.New("browser session invalid")
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
