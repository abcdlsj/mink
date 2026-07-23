package collaboration

import (
	"errors"

	"connectrpc.com/connect"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
)

func collaborationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authorityapp.ErrRuntimeUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("agent runtime authentication invalid"))
	case errors.Is(err, authoritydomain.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("collaboration action denied"))
	case errors.Is(err, collaborationapp.ErrSpaceNotFound), errors.Is(err, collaborationapp.ErrMessageNotFound), errors.Is(err, collaborationapp.ErrThreadNotFound),
		errors.Is(err, collaborationapp.ErrMembershipNotFound), errors.Is(err, authoritydomain.ErrPrincipalNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, collaborationapp.ErrRequestConflict), errors.Is(err, collaborationapp.ErrMembershipExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, collaborationapp.ErrSpaceArchived), errors.Is(err, collaborationapp.ErrDMImmutable), errors.Is(err, collaborationapp.ErrLastActiveHumanMember),
		errors.Is(err, collaborationapp.ErrInvalidMessageTarget):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, collaborationapp.ErrDMRequiresDistinctPrincipals), errors.Is(err, collaborationapp.ErrInvalidSpaceName),
		errors.Is(err, collaborationapp.ErrInvalidPrincipal), errors.Is(err, collaborationapp.ErrInvalidMessageBody), errors.Is(err, collaborationapp.ErrInvalidMessageLimit),
		errors.Is(err, collaborationapp.ErrInvalidMention):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
