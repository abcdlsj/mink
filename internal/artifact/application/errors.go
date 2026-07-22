package application

import "errors"

var (
	ErrInvalid           = errors.New("artifact input is invalid")
	ErrNotFound          = errors.New("artifact not found")
	ErrVersionNotFound   = errors.New("artifact version not found")
	ErrGrantNotFound     = errors.New("artifact grant not found")
	ErrRequestConflict   = errors.New("artifact request conflicts with committed request")
	ErrCursorUnavailable = errors.New("artifact cursor is unavailable")
	ErrIntegrity         = errors.New("artifact content integrity failure")
	ErrBlobUnavailable   = errors.New("artifact blob store is unavailable")
)
