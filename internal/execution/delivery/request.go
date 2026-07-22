package delivery

import (
	"context"
	"errors"
	"math"

	"connectrpc.com/connect"
	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	runtimeauth "github.com/abcdlsj/sumi/internal/authority/runtime"
	execution "github.com/abcdlsj/sumi/internal/execution/domain"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
)

func authentication(ctx context.Context) (authorityapp.RuntimeAuthentication, error) {
	principal, proof, err := runtimeauth.Subject(ctx)
	if err != nil {
		return authorityapp.RuntimeAuthentication{}, err
	}
	return authorityapp.RuntimeAuthentication{Principal: principal, Proof: proof}, nil
}

func mutationIDs(ctx context.Context, requestIDValue, factIDValue, factName string) (authorityapp.RuntimeAuthentication, string, string, error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return authorityapp.RuntimeAuthentication{}, "", "", err
	}
	requestID, err := connectid.CanonicalID(requestIDValue, "request id")
	if err != nil {
		return authorityapp.RuntimeAuthentication{}, "", "", err
	}
	factID, err := connectid.CanonicalID(factIDValue, factName)
	if err != nil {
		return authorityapp.RuntimeAuthentication{}, "", "", err
	}
	return authentication, requestID, factID, nil
}

func listParams(after uint64, requestedLimit uint32) (uint64, uint32, error) {
	if after > math.MaxInt64 {
		return 0, 0, connect.NewError(connect.CodeInvalidArgument, errors.New("after sequence is too large"))
	}
	limit := requestedLimit
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		return 0, 0, connect.NewError(connect.CodeInvalidArgument, errors.New("limit must be at most 200"))
	}
	return after, limit, nil
}

func fenceParam(value uint64) (uint64, error) {
	if value == 0 || value > math.MaxInt64 {
		return 0, connect.NewError(connect.CodeInvalidArgument, errors.New("fence must be between 1 and 9223372036854775807"))
	}
	return value, nil
}

func outcomeParam(value deliveryv1.RunOutcome) (execution.Outcome, error) {
	switch value {
	case deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED:
		return execution.OutcomeSucceeded, nil
	case deliveryv1.RunOutcome_RUN_OUTCOME_FAILED:
		return execution.OutcomeFailed, nil
	default:
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("run outcome is invalid"))
	}
}
