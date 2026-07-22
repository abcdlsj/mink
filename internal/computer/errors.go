package computer

import (
	"errors"

	"connectrpc.com/connect"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
)

func pairingError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, computerapp.ErrPairingInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("computer pairing is invalid or expired"))
	case errors.Is(err, computerapp.ErrPairingConflict):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("computer pairing request conflicts with existing data"))
	case errors.Is(err, authoritydomain.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("computer pairing denied"))
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
