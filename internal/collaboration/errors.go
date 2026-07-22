package collaboration

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/abcdlsj/sumi/internal/store"
)

func collaborationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("collaboration action denied"))
	case errors.Is(err, store.ErrSpaceNotFound), errors.Is(err, store.ErrMessageNotFound), errors.Is(err, store.ErrThreadNotFound),
		errors.Is(err, store.ErrMembershipNotFound), errors.Is(err, store.ErrPrincipalNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrCollaborationRequestConflict), errors.Is(err, store.ErrMembershipExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, store.ErrSpaceArchived), errors.Is(err, store.ErrDMImmutable), errors.Is(err, store.ErrLastActiveHumanMember),
		errors.Is(err, store.ErrInvalidMessageTarget):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, store.ErrDMRequiresDistinctPrincipals), errors.Is(err, store.ErrInvalidSpaceName),
		errors.Is(err, store.ErrInvalidPrincipal), errors.Is(err, store.ErrInvalidMessageBody), errors.Is(err, store.ErrInvalidMessageLimit),
		errors.Is(err, store.ErrInvalidMention):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
