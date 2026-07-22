package placement

import (
	"errors"

	"connectrpc.com/connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	placementdomain "github.com/abcdlsj/sumi/internal/placement/domain"
)

func registrationKeyValid(key string) error {
	if key == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("registration key is required"))
	}
	if len(key) > 256 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("registration key is too long"))
	}
	return nil
}

func acknowledgement(result placementv1.AcknowledgementResult, errorCode string) (placementdomain.State, string, error) {
	var state placementdomain.State
	switch result {
	case placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE:
		state = placementdomain.StateActive
	case placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_FAILED:
		state = placementdomain.StateFailed
	default:
		return "", "", connect.NewError(connect.CodeInvalidArgument, errors.New("acknowledgement result must be active or failed"))
	}
	value, err := placementdomain.NewAcknowledgement(state, errorCode)
	if errors.Is(err, placementdomain.ErrActiveWithErrorCode) {
		return "", "", connect.NewError(connect.CodeInvalidArgument, errors.New("active acknowledgement cannot include an error code"))
	}
	if errors.Is(err, placementdomain.ErrFailureCodeInvalid) {
		return "", "", connect.NewError(connect.CodeInvalidArgument, errors.New("failed acknowledgement requires a known error code"))
	}
	if err != nil {
		return "", "", connect.NewError(connect.CodeInvalidArgument, errors.New("acknowledgement result must be active or failed"))
	}
	return value.State, value.ErrorCode, nil
}
