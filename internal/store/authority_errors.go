package store

import "errors"

var (
	ErrAuthorityNotBootstrapped = errors.New("authority not bootstrapped")
	ErrAuthorityMismatch        = errors.New("authority bootstrap mismatch")
	ErrHumanNotFound            = errors.New("human not found")
	ErrHumanNameExists          = errors.New("human name exists")
	ErrHumanCredentialExists    = errors.New("human credential exists")
	ErrHumanRequestConflict     = errors.New("human request conflict")
	ErrHumanStatusConflict      = errors.New("human status request conflict")
	ErrGrantNotFound            = errors.New("grant not found")
	ErrGrantRequestConflict     = errors.New("grant request conflict")
	ErrGrantRevokeConflict      = errors.New("grant revoke request conflict")
	ErrPermissionDenied         = errors.New("permission denied")
	ErrPrincipalNotFound        = errors.New("principal not found")
	ErrScopeNotFound            = errors.New("scope not found")
	ErrParentGrantInvalid       = errors.New("parent grant invalid")
	ErrGrantExpansion           = errors.New("grant expands parent authority")
	ErrLastOwner                = errors.New("cannot remove last recoverable owner")
)
