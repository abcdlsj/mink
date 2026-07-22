package delivery

import (
	"context"

	"connectrpc.com/connect"
	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	"github.com/abcdlsj/sumi/internal/agentmessage"
	"github.com/abcdlsj/sumi/internal/connectapi"
)

func (s *Service) ListDeliveries(ctx context.Context, request *connect.Request[deliveryv1.ListDeliveriesRequest]) (*connect.Response[deliveryv1.ListDeliveriesResponse], error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return nil, err
	}
	after, limit, err := listParams(request.Msg.GetAfterSequence(), request.Msg.GetLimit())
	if err != nil {
		return nil, err
	}
	result, err := s.listDeliveries(ctx, ListDeliveriesCommand{
		Authentication: authentication, AfterSequence: after, Limit: limit, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	if err := validateActiveFacts(result); err != nil {
		return nil, err
	}
	response := &deliveryv1.ListDeliveriesResponse{
		Deliveries:   make([]*deliveryv1.Delivery, 0, len(result.Deliveries)),
		NextSequence: result.NextSequence,
	}
	for _, delivery := range result.Deliveries {
		message, err := deliveryMessage(delivery)
		if err != nil {
			return nil, err
		}
		response.Deliveries = append(response.Deliveries, message)
	}
	if result.ActiveDelivery != nil {
		response.ActiveDelivery, err = deliveryMessage(*result.ActiveDelivery)
		if err != nil {
			return nil, err
		}
	}
	if result.ActiveRun != nil {
		response.ActiveRun, err = runMessage(*result.ActiveRun)
		if err != nil {
			return nil, err
		}
	}
	if result.ActiveLaunch != nil {
		response.ActiveLaunch, err = runLaunchMessage(*result.ActiveLaunch)
		if err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(response), nil
}

func (s *Service) AcceptDelivery(ctx context.Context, request *connect.Request[deliveryv1.AcceptDeliveryRequest]) (*connect.Response[deliveryv1.AcceptDeliveryResponse], error) {
	authentication, requestID, deliveryID, err := mutationIDs(ctx, request.Msg.GetRequestId(), request.Msg.GetDeliveryId(), "delivery id")
	if err != nil {
		return nil, err
	}
	run, err := s.acceptDelivery(ctx, AcceptDeliveryCommand{
		RequestID: requestID, Authentication: authentication, DeliveryID: deliveryID, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	message, err := runMessage(run)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&deliveryv1.AcceptDeliveryResponse{Run: message}), nil
}

func (s *Service) GetRun(ctx context.Context, request *connect.Request[deliveryv1.GetRunRequest]) (*connect.Response[deliveryv1.GetRunResponse], error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return nil, err
	}
	runID, err := connectapi.CanonicalID(request.Msg.GetRunId(), "run id")
	if err != nil {
		return nil, err
	}
	run, err := s.getRun(ctx, GetRunCommand{Authentication: authentication, RunID: runID, Now: s.now()})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	message, err := runMessage(run)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&deliveryv1.GetRunResponse{Run: message}), nil
}

func (s *Service) ClaimRun(ctx context.Context, request *connect.Request[deliveryv1.ClaimRunRequest]) (*connect.Response[deliveryv1.ClaimRunResponse], error) {
	authentication, requestID, runID, err := mutationIDs(ctx, request.Msg.GetRequestId(), request.Msg.GetRunId(), "run id")
	if err != nil {
		return nil, err
	}
	launch, err := s.claimRun(ctx, ClaimRunCommand{
		RequestID: requestID, Authentication: authentication, RunID: runID, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	message, err := runLaunchMessage(launch)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&deliveryv1.ClaimRunResponse{Launch: message}), nil
}

func (s *Service) RenewRun(ctx context.Context, request *connect.Request[deliveryv1.RenewRunRequest]) (*connect.Response[deliveryv1.RenewRunResponse], error) {
	authentication, requestID, runID, err := mutationIDs(ctx, request.Msg.GetRequestId(), request.Msg.GetRunId(), "run id")
	if err != nil {
		return nil, err
	}
	launchID, err := connectapi.CanonicalID(request.Msg.GetLaunchId(), "launch id")
	if err != nil {
		return nil, err
	}
	fence, err := fenceParam(request.Msg.GetFence())
	if err != nil {
		return nil, err
	}
	launch, err := s.renewRun(ctx, RenewRunCommand{
		RequestID: requestID, Authentication: authentication, RunID: runID,
		LaunchID: launchID, Fence: fence, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	message, err := runLaunchMessage(launch)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&deliveryv1.RenewRunResponse{Launch: message}), nil
}

func (s *Service) CompleteRun(ctx context.Context, request *connect.Request[deliveryv1.CompleteRunRequest]) (*connect.Response[deliveryv1.CompleteRunResponse], error) {
	authentication, requestID, runID, err := mutationIDs(ctx, request.Msg.GetRequestId(), request.Msg.GetRunId(), "run id")
	if err != nil {
		return nil, err
	}
	outboxEventID, err := connectapi.CanonicalID(request.Msg.GetOutboxEventId(), "outbox event id")
	if err != nil {
		return nil, err
	}
	launchID, err := connectapi.CanonicalID(request.Msg.GetLaunchId(), "launch id")
	if err != nil {
		return nil, err
	}
	fence, err := fenceParam(request.Msg.GetFence())
	if err != nil {
		return nil, err
	}
	outcome, err := outcomeParam(request.Msg.GetOutcome())
	if err != nil {
		return nil, err
	}
	if err := agentmessage.ValidateBody(request.Msg.GetBody()); err != nil {
		return nil, err
	}
	mentions, err := agentmessage.MentionedAgentIDs(request.Msg.GetMentionedAgentIds())
	if err != nil {
		return nil, err
	}
	result, err := s.completeRun(ctx, CompleteRunCommand{
		RequestID: requestID, OutboxEventID: outboxEventID, Authentication: authentication,
		RunID: runID, LaunchID: launchID, Fence: fence, Outcome: outcome,
		Body: request.Msg.GetBody(), MentionedAgentIDs: mentions, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	response, err := completeRunResponse(result)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}
