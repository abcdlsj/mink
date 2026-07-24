package grant

import (
	"errors"

	"connectrpc.com/connect"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
	"github.com/abcdlsj/sumi/internal/servicesvc"
)

func issueErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authoritydomain.ErrPermissionDenied),
		errors.Is(err, grantapp.ErrParentInvalid),
		errors.Is(err, grantapp.ErrExpansion):
		return connect.NewError(connect.CodePermissionDenied, errors.New("grant issue denied"))
	case errors.Is(err, grantapp.ErrIssueConflict):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("request id already used for another grant"))
	case errors.Is(err, grantapp.ErrInvalid):
		return servicesvc.InvalArg("grant is invalid")
	default:
		return servicesvc.ErrInternal
	}
}

func revokeErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authoritydomain.ErrPermissionDenied),
		errors.Is(err, grantapp.ErrLastOwner):
		return connect.NewError(connect.CodePermissionDenied, errors.New("grant revoke denied"))
	case errors.Is(err, grantapp.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("grant not found"))
	case errors.Is(err, grantapp.ErrRevokeConflict):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("request id already used for another revoke"))
	default:
		return servicesvc.ErrInternal
	}
}
