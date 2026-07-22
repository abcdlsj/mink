package delivery

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/abcdlsj/sumi/internal/store"
)

func serviceError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrAgentRuntimeUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("agent runtime authentication invalid"))
	case errors.Is(err, store.ErrPermissionDenied), errors.Is(err, store.ErrInboxAccessLost):
		return connect.NewError(connect.CodePermissionDenied, errors.New("agent delivery action denied"))
	case errors.Is(err, store.ErrDeliveryNotFound), errors.Is(err, store.ErrRunNotFound),
		errors.Is(err, store.ErrSpaceNotFound), errors.Is(err, store.ErrThreadNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrInboxRequestConflict), errors.Is(err, store.ErrRunCompletionConflict):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, store.ErrDeliveryNotAvailable), errors.Is(err, store.ErrDeliveryCursorUnavailable),
		errors.Is(err, store.ErrRunNotAccepted), errors.Is(err, store.ErrRunNotRunning),
		errors.Is(err, store.ErrRunLaunchActive), errors.Is(err, store.ErrRunLaunchStale),
		errors.Is(err, store.ErrRunLaunchExpired), errors.Is(err, store.ErrInboxBasisMismatch),
		errors.Is(err, store.ErrSpaceArchived):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, store.ErrRunAlreadyActive):
		return connect.NewError(connect.CodeResourceExhausted, err)
	case errors.Is(err, store.ErrInvalidDeliveryLimit), errors.Is(err, store.ErrRunInvalidOutcome),
		errors.Is(err, store.ErrInvalidMessageBody), errors.Is(err, store.ErrInvalidMention):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return internalError()
	}
}

func internalError() error {
	return connect.NewError(connect.CodeInternal, errors.New("agent delivery operation failed"))
}
