package run

import (
	"context"
	"math"

	"connectrpc.com/connect"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
	"github.com/abcdlsj/sumi/internal/servicesvc"
	"github.com/abcdlsj/sumi/internal/transport/id"
	"github.com/abcdlsj/sumi/internal/transport/msgcodec"
)

func (s *Service) ListRuns(ctx context.Context, req *connect.Request[runv1.ListRunsRequest]) (*connect.Response[runv1.ListRunsResponse], error) {
	auth, err := auth(ctx)
	if err != nil {
		return nil, err
	}
	after, limit, err := listParams(req.Msg.GetAfterSequence(), req.Msg.GetLimit())
	if err != nil {
		return nil, err
	}
	result, err := s.store.ListRuns(ctx, executionapp.ListRunsQuery{
		Authentication: auth, AfterSequence: after, Limit: limit, Now: s.now(),
	})
	if err := runErr(err); err != nil {
		return nil, err
	}
	resp := &runv1.ListRunsResponse{
		Runs: make([]*runv1.Run, 0, len(result.Runs)), NextSequence: result.NextSequence,
	}
	for _, v := range result.Runs {
		msg, err := runToProto(v)
		if err != nil {
			return nil, err
		}
		resp.Runs = append(resp.Runs, msg)
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) GetRun(ctx context.Context, req *connect.Request[runv1.GetRunRequest]) (*connect.Response[runv1.GetRunResponse], error) {
	auth, err := auth(ctx)
	if err != nil {
		return nil, err
	}
	runID, err := id.CanonicalID(req.Msg.GetRunId(), "run id")
	if err != nil {
		return nil, err
	}
	v, err := s.store.GetRun(ctx, executionapp.GetRunQuery{
		Authentication: auth, RunID: runID, Now: s.now(),
	})
	if err := runErr(err); err != nil {
		return nil, err
	}
	msg, err := runToProto(v)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runv1.GetRunResponse{Run: msg}), nil
}

func (s *Service) ClaimRun(ctx context.Context, req *connect.Request[runv1.ClaimRunRequest]) (*connect.Response[runv1.ClaimRunResponse], error) {
	auth, requestID, runID, err := mutationIDs(ctx, req.Msg.GetRequestId(), req.Msg.GetRunId())
	if err != nil {
		return nil, err
	}
	v, err := s.store.ClaimRun(ctx, executionapp.ClaimRunCommand{
		RequestID: requestID, Authentication: auth, RunID: runID, Now: s.now(),
	})
	if err := runErr(err); err != nil {
		return nil, err
	}
	msg, err := runToProto(v)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runv1.ClaimRunResponse{Run: msg}), nil
}

func (s *Service) RenewRun(ctx context.Context, req *connect.Request[runv1.RenewRunRequest]) (*connect.Response[runv1.RenewRunResponse], error) {
	auth, requestID, runID, err := mutationIDs(ctx, req.Msg.GetRequestId(), req.Msg.GetRunId())
	if err != nil {
		return nil, err
	}
	rp := req.Msg.GetRunProof()
	attempt, fence, err := attemptFence(rp.GetAttempt(), rp.GetFence(), false)
	if err != nil {
		return nil, err
	}
	v, err := s.store.RenewRun(ctx, executionapp.RenewRunCommand{
		RequestID: requestID, Authentication: auth, RunID: runID,
		Attempt: attempt, Fence: fence, Now: s.now(),
	})
	if err := runErr(err); err != nil {
		return nil, err
	}
	msg, err := runToProto(v)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runv1.RenewRunResponse{Run: msg}), nil
}

func (s *Service) CancelRun(ctx context.Context, req *connect.Request[runv1.CancelRunRequest]) (*connect.Response[runv1.CancelRunResponse], error) {
	auth, requestID, runID, err := mutationIDs(ctx, req.Msg.GetRequestId(), req.Msg.GetRunId())
	if err != nil {
		return nil, err
	}
	rp := req.Msg.GetRunProof()
	attempt, fence, err := attemptFence(rp.GetAttempt(), rp.GetFence(), true)
	if err != nil {
		return nil, err
	}
	v, err := s.store.CancelRun(ctx, executionapp.CancelRunCommand{
		RequestID: requestID, Authentication: auth, RunID: runID,
		Attempt: attempt, Fence: fence, Now: s.now(),
	})
	if err := runErr(err); err != nil {
		return nil, err
	}
	msg, err := runToProto(v)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runv1.CancelRunResponse{Run: msg}), nil
}

func (s *Service) CompleteRun(ctx context.Context, req *connect.Request[runv1.CompleteRunRequest]) (*connect.Response[runv1.CompleteRunResponse], error) {
	auth, requestID, runID, err := mutationIDs(ctx, req.Msg.GetRequestId(), req.Msg.GetRunId())
	if err != nil {
		return nil, err
	}
	outboxEventID, err := id.CanonicalID(req.Msg.GetOutboxEventId(), "outbox event id")
	if err != nil {
		return nil, err
	}
	rp := req.Msg.GetRunProof()
	attempt, fence, err := attemptFence(rp.GetAttempt(), rp.GetFence(), false)
	if err != nil {
		return nil, err
	}
	outcome, err := parseOutcome(req.Msg.GetOutcome())
	if err != nil {
		return nil, err
	}
	if err := msgcodec.ValidateBody(req.Msg.GetBody()); err != nil {
		return nil, err
	}
	mentions, err := msgcodec.MentionedPrincipals(req.Msg.GetMentionedPrincipals())
	if err != nil {
		return nil, err
	}
	usage := req.Msg.GetUsage()
	if usage == nil || usage.GetInputUnits() > math.MaxInt64 || usage.GetOutputUnits() > math.MaxInt64 {
		return nil, servicesvc.InvalArg("run usage is invalid")
	}
	result, err := s.store.CompleteRun(ctx, executionapp.CompleteRunCommand{
		RequestID: requestID, OutboxEventID: outboxEventID, Authentication: auth,
		RunID: runID, Attempt: attempt, Fence: fence, Outcome: outcome,
		ErrorCode: req.Msg.GetErrorCode(), Body: req.Msg.GetBody(),
		MentionedPrincipals: mentions,
		Usage:               executionapp.RunUsage{InputUnits: usage.GetInputUnits(), OutputUnits: usage.GetOutputUnits()},
		Now: s.now(),
	})
	if err := runErr(err); err != nil {
		return nil, err
	}
	resp, err := completeResponse(result)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}
