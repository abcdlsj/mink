package placement

import (
	"errors"

	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	placementdomain "github.com/abcdlsj/sumi/internal/placement/domain"
	"github.com/abcdlsj/sumi/internal/servicesvc"
)

func regKeyValid(key string) error {
	if key == "" {
		return servicesvc.InvalArg("registration key is required")
	}
	if len(key) > 256 {
		return servicesvc.InvalArg("registration key is too long")
	}
	return nil
}

func parseAckResult(result placementv1.AcknowledgementResult, errCode string) (placementdomain.State, string, error) {
	var state placementdomain.State
	switch result {
	case placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_READY:
		state = placementdomain.StateReady
	case placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_FAILED:
		state = placementdomain.StateFailed
	default:
		return "", "", servicesvc.InvalArg("acknowledgement result must be ready or failed")
	}
	value, err := placementdomain.NewAcknowledgement(state, errCode)
	switch {
	case errors.Is(err, placementdomain.ErrReadyWithErrorCode):
		return "", "", servicesvc.InvalArg("ready acknowledgement cannot include an error code")
	case errors.Is(err, placementdomain.ErrFailureCodeInvalid):
		return "", "", servicesvc.InvalArg("failed acknowledgement requires a known error code")
	case err != nil:
		return "", "", servicesvc.InvalArg("acknowledgement result must be ready or failed")
	}
	return value.State, value.ErrorCode, nil
}
