package run

import (
	"errors"

	"connectrpc.com/connect"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
)

func serviceError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authorityapp.ErrRuntimeUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("agent runtime authentication invalid"))
	case errors.Is(err, authoritydomain.ErrPermissionDenied), errors.Is(err, executionapp.ErrInboxAccessLost):
		return connect.NewError(connect.CodePermissionDenied, errors.New("agent run action denied"))
	case errors.Is(err, executionapp.ErrRunNotFound), errors.Is(err, collaborationapp.ErrSpaceNotFound),
		errors.Is(err, collaborationapp.ErrThreadNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, executionapp.ErrRunRequestConflict), errors.Is(err, executionapp.ErrRunCompletionConflict):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, executionapp.ErrRunNotQueued), errors.Is(err, executionapp.ErrRunNotRunning),
		errors.Is(err, executionapp.ErrRunLeaseActive), errors.Is(err, executionapp.ErrRunLeaseStale),
		errors.Is(err, executionapp.ErrRunLeaseExpired), errors.Is(err, executionapp.ErrInboxBasisMismatch),
		errors.Is(err, executionapp.ErrInboxItemNotClaimed), errors.Is(err, collaborationapp.ErrSpaceArchived):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, executionapp.ErrRunAlreadyActive):
		return connect.NewError(connect.CodeResourceExhausted, err)
	case errors.Is(err, executionapp.ErrInvalidRunLimit), errors.Is(err, executionapp.ErrRunInvalidOutcome),
		errors.Is(err, collaborationapp.ErrInvalidMessageBody), errors.Is(err, collaborationapp.ErrInvalidMention):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, errors.New("agent run operation failed"))
	}
}
