package computer

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/abcdlsj/sumi/internal/store"
)

func pairingError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrComputerPairingInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("computer pairing is invalid or expired"))
	case errors.Is(err, store.ErrComputerPairingConflict):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("computer pairing request conflicts with existing data"))
	case errors.Is(err, store.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("computer pairing denied"))
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
