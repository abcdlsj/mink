package store

import placementapp "github.com/abcdlsj/sumi/internal/placement/application"

var (
	ErrPlacementNotFound        = placementapp.ErrNotFound
	ErrPlacementStale           = placementapp.ErrStale
	ErrPlacementConflict        = placementapp.ErrConflict
	ErrPlacementRequestConflict = placementapp.ErrRequestConflict
)
