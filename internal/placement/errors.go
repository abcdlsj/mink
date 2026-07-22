package placement

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/abcdlsj/sumi/internal/store"
)

func placementError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrAgentNotFound), errors.Is(err, store.ErrComputerNotFound), errors.Is(err, store.ErrPlacementNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrRegistrationKeyMismatch):
		return connect.NewError(connect.CodePermissionDenied, errors.New("computer credentials do not match"))
	case errors.Is(err, store.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("agent placement denied"))
	case errors.Is(err, store.ErrPlacementRequestConflict):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("request id already exists with different placement data"))
	case errors.Is(err, store.ErrPlacementStale), errors.Is(err, store.ErrPlacementConflict):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
