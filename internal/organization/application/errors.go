package application

import "errors"

var (
	ErrHumanNotFound        = errors.New("human not found")
	ErrHumanNameExists      = errors.New("human name exists")
	ErrHumanAccountExists   = errors.New("human account exists")
	ErrHumanRequestConflict = errors.New("human request conflict")
	ErrHumanStatusConflict  = errors.New("human status request conflict")
)
