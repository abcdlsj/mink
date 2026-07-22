package application

import "errors"

var (
	ErrNotFound        = errors.New("agent not found")
	ErrRequestConflict = errors.New("agent request payload conflict")
	ErrNameExists      = errors.New("agent name already exists")
)
