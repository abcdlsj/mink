package placement

import (
	"errors"

	"connectrpc.com/connect"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
	placementapp "github.com/abcdlsj/sumi/internal/placement/application"
	placementdomain "github.com/abcdlsj/sumi/internal/placement/domain"
)

func placementError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, placementapp.ErrAgentNotFound), errors.Is(err, computerapp.ErrNotFound), errors.Is(err, placementapp.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, computerapp.ErrRegistrationKeyMismatch):
		return connect.NewError(connect.CodePermissionDenied, errors.New("computer credentials do not match"))
	case errors.Is(err, authoritydomain.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("agent placement denied"))
	case errors.Is(err, placementapp.ErrRequestConflict):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("request id already exists with different placement data"))
	case errors.Is(err, placementapp.ErrStale), errors.Is(err, placementapp.ErrConflict):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, placementdomain.ErrActiveWithErrorCode):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("active acknowledgement cannot include an error code"))
	case errors.Is(err, placementdomain.ErrFailureCodeInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("failed acknowledgement requires a known error code"))
	case errors.Is(err, placementdomain.ErrAcknowledgementStateInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("acknowledgement result must be active or failed"))
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
