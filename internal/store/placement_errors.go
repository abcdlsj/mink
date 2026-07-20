package store

import "errors"

var (
	ErrPlacementNotFound = errors.New("agent placement not found")
	ErrPlacementStale    = errors.New("agent placement acknowledgement is stale")
	ErrPlacementConflict = errors.New("agent placement acknowledgement conflicts with current state")
)
