package application

import "errors"

var (
	ErrHumanNotFound         = errors.New("human not found")
	ErrHumanNameExists       = errors.New("human name exists")
	ErrHumanCredentialExists = errors.New("human credential exists")
	ErrHumanRequestConflict  = errors.New("human request conflict")
	ErrHumanStatusConflict   = errors.New("human status request conflict")
)
