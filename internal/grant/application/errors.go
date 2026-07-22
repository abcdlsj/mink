package application

import "errors"

var (
	ErrNotFound       = errors.New("grant not found")
	ErrIssueConflict  = errors.New("grant request conflict")
	ErrRevokeConflict = errors.New("grant revoke request conflict")
	ErrInvalid        = errors.New("grant invalid")
	ErrParentInvalid  = errors.New("parent grant invalid")
	ErrExpansion      = errors.New("grant expands parent authority")
	ErrLastOwner      = errors.New("cannot remove last recoverable owner")
)
