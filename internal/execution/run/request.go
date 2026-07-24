package run

import (
	"context"
	"math"

	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	runtimeauth "github.com/abcdlsj/sumi/internal/authority/runtime"
	executiondomain "github.com/abcdlsj/sumi/internal/execution/domain"
	"github.com/abcdlsj/sumi/internal/servicesvc"
	"github.com/abcdlsj/sumi/internal/transport/id"
)

func auth(ctx context.Context) (authorityapp.RuntimeAuthentication, error) {
	principal, proof, err := runtimeauth.Subject(ctx)
	if err != nil {
		return authorityapp.RuntimeAuthentication{}, err
	}
	return authorityapp.RuntimeAuthentication{Principal: principal, Proof: proof}, nil
}

func mutationIDs(ctx context.Context, requestIDValue, runIDValue string) (authorityapp.RuntimeAuthentication, string, string, error) {
	a, err := auth(ctx)
	if err != nil {
		return authorityapp.RuntimeAuthentication{}, "", "", err
	}
	requestID, err := id.CanonicalID(requestIDValue, "request id")
	if err != nil {
		return authorityapp.RuntimeAuthentication{}, "", "", err
	}
	runID, err := id.CanonicalID(runIDValue, "run id")
	if err != nil {
		return authorityapp.RuntimeAuthentication{}, "", "", err
	}
	return a, requestID, runID, nil
}

func listParams(after uint64, requestedLimit uint32) (uint64, uint32, error) {
	if after > math.MaxInt64 {
		return 0, 0, servicesvc.InvalArg("after sequence is too large")
	}
	limit := requestedLimit
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		return 0, 0, servicesvc.InvalArg("limit must be at most 200")
	}
	return after, limit, nil
}

func attemptFence(attempt, fence uint64, allowZero bool) (uint64, uint64, error) {
	if allowZero && attempt == 0 && fence == 0 {
		return 0, 0, nil
	}
	if attempt == 0 || attempt > math.MaxInt64 || fence == 0 || fence > math.MaxInt64 {
		return 0, 0, servicesvc.InvalArg("run attempt and fence are invalid")
	}
	return attempt, fence, nil
}

func parseOutcome(v runv1.RunOutcome) (executiondomain.Outcome, error) {
	switch v {
	case runv1.RunOutcome_RUN_OUTCOME_SUCCEEDED:
		return executiondomain.OutcomeSucceeded, nil
	case runv1.RunOutcome_RUN_OUTCOME_FAILED:
		return executiondomain.OutcomeFailed, nil
	default:
		return "", servicesvc.InvalArg("run outcome is invalid")
	}
}
