package run

import (
	"context"
	"math"

	"connectrpc.com/connect"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
	"github.com/abcdlsj/sumi/internal/transport/messagecodec"
)

func (s *Service) ListRuns(ctx context.Context, request *connect.Request[runv1.ListRunsRequest]) (*connect.Response[runv1.ListRunsResponse], error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return nil, err
	}
	after, limit, err := listParams(request.Msg.GetAfterSequence(), request.Msg.GetLimit())
	if err != nil {
		return nil, err
	}
	result, err := s.store.ListRuns(ctx, executionapp.ListRunsQuery{Authentication: authentication, AfterSequence: after, Limit: limit, Now: s.now()})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	response := &runv1.ListRunsResponse{Runs: make([]*runv1.Run, 0, len(result.Runs)), NextSequence: result.NextSequence}
	for _, value := range result.Runs {
		message, err := runMessage(value)
		if err != nil {
			return nil, err
		}
		response.Runs = append(response.Runs, message)
	}
	return connect.NewResponse(response), nil
}

func (s *Service) GetRun(ctx context.Context, request *connect.Request[runv1.GetRunRequest]) (*connect.Response[runv1.GetRunResponse], error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return nil, err
	}
	runID, err := connectid.CanonicalID(request.Msg.GetRunId(), "run id")
	if err != nil {
		return nil, err
	}
	value, err := s.store.GetRun(ctx, executionapp.GetRunQuery{Authentication: authentication, RunID: runID, Now: s.now()})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	message, err := runMessage(value)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runv1.GetRunResponse{Run: message}), nil
}

func (s *Service) ClaimRun(ctx context.Context, request *connect.Request[runv1.ClaimRunRequest]) (*connect.Response[runv1.ClaimRunResponse], error) {
	authentication, requestID, runID, err := mutationIDs(ctx, request.Msg.GetRequestId(), request.Msg.GetRunId())
	if err != nil {
		return nil, err
	}
	value, err := s.store.ClaimRun(ctx, executionapp.ClaimRunCommand{RequestID: requestID, Authentication: authentication, RunID: runID, Now: s.now()})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	message, err := runMessage(value)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runv1.ClaimRunResponse{Run: message}), nil
}

func (s *Service) RenewRun(ctx context.Context, request *connect.Request[runv1.RenewRunRequest]) (*connect.Response[runv1.RenewRunResponse], error) {
	authentication, requestID, runID, err := mutationIDs(ctx, request.Msg.GetRequestId(), request.Msg.GetRunId())
	if err != nil {
		return nil, err
	}
	attempt, fence, err := attemptFence(request.Msg.GetAttempt(), request.Msg.GetFence(), false)
	if err != nil {
		return nil, err
	}
	value, err := s.store.RenewRun(ctx, executionapp.RenewRunCommand{RequestID: requestID, Authentication: authentication, RunID: runID, Attempt: attempt, Fence: fence, Now: s.now()})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	message, err := runMessage(value)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runv1.RenewRunResponse{Run: message}), nil
}

func (s *Service) CancelRun(ctx context.Context, request *connect.Request[runv1.CancelRunRequest]) (*connect.Response[runv1.CancelRunResponse], error) {
	authentication, requestID, runID, err := mutationIDs(ctx, request.Msg.GetRequestId(), request.Msg.GetRunId())
	if err != nil {
		return nil, err
	}
	attempt, fence, err := attemptFence(request.Msg.GetAttempt(), request.Msg.GetFence(), true)
	if err != nil {
		return nil, err
	}
	value, err := s.store.CancelRun(ctx, executionapp.CancelRunCommand{RequestID: requestID, Authentication: authentication, RunID: runID, Attempt: attempt, Fence: fence, Now: s.now()})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	message, err := runMessage(value)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runv1.CancelRunResponse{Run: message}), nil
}

func (s *Service) CompleteRun(ctx context.Context, request *connect.Request[runv1.CompleteRunRequest]) (*connect.Response[runv1.CompleteRunResponse], error) {
	authentication, requestID, runID, err := mutationIDs(ctx, request.Msg.GetRequestId(), request.Msg.GetRunId())
	if err != nil {
		return nil, err
	}
	outboxEventID, err := connectid.CanonicalID(request.Msg.GetOutboxEventId(), "outbox event id")
	if err != nil {
		return nil, err
	}
	attempt, fence, err := attemptFence(request.Msg.GetAttempt(), request.Msg.GetFence(), false)
	if err != nil {
		return nil, err
	}
	outcome, err := outcome(request.Msg.GetOutcome())
	if err != nil {
		return nil, err
	}
	if err := messagecodec.ValidateBody(request.Msg.GetBody()); err != nil {
		return nil, err
	}
	mentions, err := messagecodec.MentionedPrincipals(request.Msg.GetMentionedPrincipals())
	if err != nil {
		return nil, err
	}
	usage := request.Msg.GetUsage()
	if usage == nil || usage.GetInputUnits() > math.MaxInt64 || usage.GetOutputUnits() > math.MaxInt64 {
		return nil, connect.NewError(connect.CodeInvalidArgument, internalError())
	}
	result, err := s.store.CompleteRun(ctx, executionapp.CompleteRunCommand{
		RequestID: requestID, OutboxEventID: outboxEventID, Authentication: authentication,
		RunID: runID, Attempt: attempt, Fence: fence, Outcome: outcome, ErrorCode: request.Msg.GetErrorCode(),
		Body: request.Msg.GetBody(), MentionedPrincipals: mentions,
		Usage: executionapp.RunUsage{InputUnits: usage.GetInputUnits(), OutputUnits: usage.GetOutputUnits()}, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	response, err := completeResponse(result)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}
