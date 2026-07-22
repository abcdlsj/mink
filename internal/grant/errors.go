package grant

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/abcdlsj/sumi/internal/store"
)

func issueError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrPermissionDenied), errors.Is(err, store.ErrParentGrantInvalid), errors.Is(err, store.ErrGrantExpansion):
		return connect.NewError(connect.CodePermissionDenied, errors.New("grant issue denied"))
	case errors.Is(err, store.ErrGrantRequestConflict):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("request id already used for another grant"))
	case errors.Is(err, store.ErrGrantInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("grant is invalid"))
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func revokeError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrPermissionDenied), errors.Is(err, store.ErrLastOwner):
		return connect.NewError(connect.CodePermissionDenied, errors.New("grant revoke denied"))
	case errors.Is(err, store.ErrGrantNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("grant not found"))
	case errors.Is(err, store.ErrGrantRevokeConflict):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("request id already used for another revoke"))
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
