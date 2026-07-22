package placement

import (
	"errors"

	"connectrpc.com/connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	placementfailure "github.com/abcdlsj/sumi/internal/placement/failure"
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

func acknowledgement(result placementv1.AcknowledgementResult, errorCode string) (string, string, error) {
	switch result {
	case placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE:
		if errorCode != "" {
			return "", "", connect.NewError(connect.CodeInvalidArgument, errors.New("active acknowledgement cannot include an error code"))
		}
		return "active", "", nil
	case placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_FAILED:
		if !placementfailure.Valid(errorCode) {
			return "", "", connect.NewError(connect.CodeInvalidArgument, errors.New("failed acknowledgement requires a known error code"))
		}
		return "failed", errorCode, nil
	default:
		return "", "", connect.NewError(connect.CodeInvalidArgument, errors.New("acknowledgement result must be active or failed"))
	}
}
