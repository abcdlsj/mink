package delivery

import (
	"context"
	"errors"
	"math"
	"time"

	"connectrpc.com/connect"
	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1/deliveryv1connect"
	"github.com/abcdlsj/sumi/internal/agentmessage"
	"github.com/abcdlsj/sumi/internal/connectapi"
	"github.com/abcdlsj/sumi/internal/runtimeauth"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

type deliveryStore interface {
	ListDeliveries(context.Context, store.ListDeliveriesParams) (store.ListDeliveriesResult, error)
	AcceptDelivery(context.Context, store.AcceptDeliveryParams) (store.Run, error)
	GetRun(context.Context, store.AgentRuntimeAuthentication, string, time.Time) (store.Run, error)
	ClaimRun(context.Context, store.ClaimRunParams) (store.RunLaunch, error)
	RenewRun(context.Context, store.RenewRunParams) (store.RunLaunch, error)
	CompleteRun(context.Context, store.CompleteRunParams) (store.CompleteRunResult, error)
}

type Service struct {
	store deliveryStore
	now   func() time.Time
}

var _ deliveryv1connect.DeliveryServiceHandler = (*Service)(nil)

func New(database deliveryStore) *Service {
	return &Service{store: database, now: time.Now}
}

func Procedures() []string {
	return []string{
		deliveryv1connect.DeliveryServiceListDeliveriesProcedure,
		deliveryv1connect.DeliveryServiceAcceptDeliveryProcedure,
		deliveryv1connect.DeliveryServiceGetRunProcedure,
		deliveryv1connect.DeliveryServiceClaimRunProcedure,
		deliveryv1connect.DeliveryServiceRenewRunProcedure,
		deliveryv1connect.DeliveryServiceCompleteRunProcedure,
	}
}

func (s *Service) ListDeliveries(ctx context.Context, request *connect.Request[deliveryv1.ListDeliveriesRequest]) (*connect.Response[deliveryv1.ListDeliveriesResponse], error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return nil, err
	}
	after, limit, err := listParams(request.Msg.GetAfterSequence(), request.Msg.GetLimit())
	if err != nil {
		return nil, err
	}
	result, err := s.store.ListDeliveries(ctx, store.ListDeliveriesParams{
		Authentication: authentication, AfterSequence: after, Limit: limit, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
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
	if result.ActiveRun == nil && result.ActiveLaunch != nil {
		return nil, internalError()
	}
	if (result.ActiveRun == nil) != (result.ActiveDelivery == nil) {
		return nil, internalError()
	}
	if result.ActiveRun != nil {
		if result.ActiveDelivery.ID != result.ActiveRun.DeliveryID || result.ActiveDelivery.AgentID != result.ActiveRun.AgentID ||
			result.ActiveDelivery.State != store.DeliveryStateAccepted {
			return nil, internalError()
		}
		response.ActiveDelivery, err = deliveryMessage(*result.ActiveDelivery)
		if err != nil {
			return nil, err
		}
		if (result.ActiveRun.State == store.RunStateAccepted && result.ActiveLaunch != nil) ||
			(result.ActiveRun.State == store.RunStateRunning && result.ActiveLaunch == nil) ||
			result.ActiveRun.State == store.RunStateCompleted {
			return nil, internalError()
		}
		response.ActiveRun, err = runMessage(*result.ActiveRun)
		if err != nil {
			return nil, err
		}
	}
	if result.ActiveLaunch != nil {
		if result.ActiveLaunch.RunID != result.ActiveRun.ID || result.ActiveLaunch.AgentID != result.ActiveRun.AgentID ||
			result.ActiveLaunch.ClosedAt != nil || result.ActiveLaunch.CloseReason != "" {
			return nil, internalError()
		}
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
	run, err := s.store.AcceptDelivery(ctx, store.AcceptDeliveryParams{
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
	run, err := s.store.GetRun(ctx, authentication, runID, s.now())
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
	launch, err := s.store.ClaimRun(ctx, store.ClaimRunParams{
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
	launch, err := s.store.RenewRun(ctx, store.RenewRunParams{
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
	result, err := s.store.CompleteRun(ctx, store.CompleteRunParams{
		RequestID: requestID, OutboxEventID: outboxEventID, Authentication: authentication,
		RunID: runID, LaunchID: launchID, Fence: fence, Outcome: outcome,
		Body: request.Msg.GetBody(), MentionedAgentIDs: mentions, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	run, err := runMessage(result.Run)
	if err != nil {
		return nil, err
	}
	response := &deliveryv1.CompleteRunResponse{Run: run, CommittedAt: timestamppb.New(result.CommittedAt)}
	switch result.Kind {
	case store.InboxResultMessage:
		if result.Message == nil || result.HeldDraft != nil || result.Run.ResultKind != store.InboxResultMessage || result.Run.ResultID != result.Message.ID {
			return nil, internalError()
		}
		message, err := agentmessage.Message(*result.Message)
		if err != nil {
			return nil, err
		}
		response.Result = &deliveryv1.CompleteRunResponse_Message{Message: message}
	case store.InboxResultHeldDraft:
		if result.Message != nil || result.HeldDraft == nil || result.Run.ResultKind != store.InboxResultHeldDraft || result.Run.ResultID != result.HeldDraft.ID {
			return nil, internalError()
		}
		draft, err := agentmessage.HeldDraft(*result.HeldDraft)
		if err != nil {
			return nil, err
		}
		response.Result = &deliveryv1.CompleteRunResponse_HeldDraft{HeldDraft: draft}
	default:
		return nil, internalError()
	}
	return connect.NewResponse(response), nil
}

func authentication(ctx context.Context) (store.AgentRuntimeAuthentication, error) {
	principal, proof, err := runtimeauth.Subject(ctx)
	if err != nil {
		return store.AgentRuntimeAuthentication{}, err
	}
	return store.AgentRuntimeAuthentication{Principal: principal, Proof: proof}, nil
}

func mutationIDs(ctx context.Context, requestIDValue, factIDValue, factName string) (store.AgentRuntimeAuthentication, string, string, error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return store.AgentRuntimeAuthentication{}, "", "", err
	}
	requestID, err := connectapi.CanonicalID(requestIDValue, "request id")
	if err != nil {
		return store.AgentRuntimeAuthentication{}, "", "", err
	}
	factID, err := connectapi.CanonicalID(factIDValue, factName)
	if err != nil {
		return store.AgentRuntimeAuthentication{}, "", "", err
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

func outcomeParam(value deliveryv1.RunOutcome) (string, error) {
	switch value {
	case deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED:
		return store.RunOutcomeSucceeded, nil
	case deliveryv1.RunOutcome_RUN_OUTCOME_FAILED:
		return store.RunOutcomeFailed, nil
	default:
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("run outcome is invalid"))
	}
}

func serviceError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrAgentRuntimeUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("agent runtime authentication invalid"))
	case errors.Is(err, store.ErrPermissionDenied), errors.Is(err, store.ErrInboxAccessLost):
		return connect.NewError(connect.CodePermissionDenied, errors.New("agent delivery action denied"))
	case errors.Is(err, store.ErrDeliveryNotFound), errors.Is(err, store.ErrRunNotFound),
		errors.Is(err, store.ErrSpaceNotFound), errors.Is(err, store.ErrThreadNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrInboxRequestConflict), errors.Is(err, store.ErrRunCompletionConflict):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, store.ErrDeliveryNotAvailable), errors.Is(err, store.ErrDeliveryCursorUnavailable),
		errors.Is(err, store.ErrRunNotAccepted), errors.Is(err, store.ErrRunNotRunning),
		errors.Is(err, store.ErrRunLaunchActive), errors.Is(err, store.ErrRunLaunchStale),
		errors.Is(err, store.ErrRunLaunchExpired), errors.Is(err, store.ErrInboxBasisMismatch),
		errors.Is(err, store.ErrSpaceArchived):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, store.ErrRunAlreadyActive):
		return connect.NewError(connect.CodeResourceExhausted, err)
	case errors.Is(err, store.ErrInvalidDeliveryLimit), errors.Is(err, store.ErrRunInvalidOutcome),
		errors.Is(err, store.ErrInvalidMessageBody), errors.Is(err, store.ErrInvalidMention):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return internalError()
	}
}

func deliveryMessage(value store.Delivery) (*deliveryv1.Delivery, error) {
	state := deliveryv1.DeliveryState_DELIVERY_STATE_UNSPECIFIED
	switch value.State {
	case store.DeliveryStateAvailable:
		if value.AcceptedAt != nil || value.CompletedAt != nil {
			return nil, internalError()
		}
		state = deliveryv1.DeliveryState_DELIVERY_STATE_AVAILABLE
	case store.DeliveryStateAccepted:
		if value.AcceptedAt == nil || value.CompletedAt != nil {
			return nil, internalError()
		}
		state = deliveryv1.DeliveryState_DELIVERY_STATE_ACCEPTED
	case store.DeliveryStateCompleted:
		if value.AcceptedAt == nil || value.CompletedAt == nil {
			return nil, internalError()
		}
		state = deliveryv1.DeliveryState_DELIVERY_STATE_COMPLETED
	default:
		return nil, internalError()
	}
	target, err := agentmessage.Target(value.Target)
	if err != nil {
		return nil, err
	}
	message := &deliveryv1.Delivery{
		Sequence: value.Sequence, Id: value.ID, AgentId: value.AgentID,
		InboxItemId: value.InboxItemID, TriggerMessageId: value.TriggerMessageID,
		SpaceId: value.SpaceID, Target: target,
		TriggerTargetSequence: value.TriggerTargetSequence, State: state,
		CreatedAt: timestamppb.New(value.CreatedAt),
	}
	if value.AcceptedAt != nil {
		message.AcceptedAt = timestamppb.New(*value.AcceptedAt)
	}
	if value.CompletedAt != nil {
		message.CompletedAt = timestamppb.New(*value.CompletedAt)
	}
	return message, nil
}

func runMessage(value store.Run) (*deliveryv1.Run, error) {
	state := deliveryv1.RunState_RUN_STATE_UNSPECIFIED
	switch value.State {
	case store.RunStateAccepted:
		if value.Outcome != "" || value.ResultKind != "" || value.ResultID != "" || value.StartedAt != nil || value.CompletedAt != nil {
			return nil, internalError()
		}
		state = deliveryv1.RunState_RUN_STATE_ACCEPTED
	case store.RunStateRunning:
		if value.Outcome != "" || value.ResultKind != "" || value.ResultID != "" || value.StartedAt == nil || value.CompletedAt != nil {
			return nil, internalError()
		}
		state = deliveryv1.RunState_RUN_STATE_RUNNING
	case store.RunStateCompleted:
		if value.Outcome == "" || value.ResultKind == "" || value.ResultID == "" || value.StartedAt == nil || value.CompletedAt == nil {
			return nil, internalError()
		}
		state = deliveryv1.RunState_RUN_STATE_COMPLETED
	default:
		return nil, internalError()
	}
	outcome := deliveryv1.RunOutcome_RUN_OUTCOME_UNSPECIFIED
	switch value.Outcome {
	case "":
	case store.RunOutcomeSucceeded:
		outcome = deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED
	case store.RunOutcomeFailed:
		outcome = deliveryv1.RunOutcome_RUN_OUTCOME_FAILED
	default:
		return nil, internalError()
	}
	message := &deliveryv1.Run{
		Id: value.ID, DeliveryId: value.DeliveryID, AgentId: value.AgentID,
		BasisTargetSequence: value.BasisTargetSequence, State: state, Outcome: outcome,
		AcceptedAt: timestamppb.New(value.AcceptedAt),
	}
	switch value.ResultKind {
	case "":
		if value.ResultID != "" {
			return nil, internalError()
		}
	case store.InboxResultMessage:
		message.ResultRef = &deliveryv1.Run_ResultMessageId{ResultMessageId: value.ResultID}
	case store.InboxResultHeldDraft:
		message.ResultRef = &deliveryv1.Run_ResultHeldDraftId{ResultHeldDraftId: value.ResultID}
	default:
		return nil, internalError()
	}
	if value.StartedAt != nil {
		message.StartedAt = timestamppb.New(*value.StartedAt)
	}
	if value.CompletedAt != nil {
		message.CompletedAt = timestamppb.New(*value.CompletedAt)
	}
	return message, nil
}

func runLaunchMessage(value store.RunLaunch) (*deliveryv1.RunLaunch, error) {
	reason := deliveryv1.RunLaunchCloseReason_RUN_LAUNCH_CLOSE_REASON_UNSPECIFIED
	switch value.CloseReason {
	case "":
		if value.ClosedAt != nil {
			return nil, internalError()
		}
	case store.RunLaunchCloseReplaced:
		if value.ClosedAt == nil || value.ClosedAt.Before(value.ExpiresAt) {
			return nil, internalError()
		}
		reason = deliveryv1.RunLaunchCloseReason_RUN_LAUNCH_CLOSE_REASON_REPLACED
	case store.RunLaunchCloseCompleted:
		if value.ClosedAt == nil || value.ClosedAt.Before(value.ClaimedAt) {
			return nil, internalError()
		}
		reason = deliveryv1.RunLaunchCloseReason_RUN_LAUNCH_CLOSE_REASON_COMPLETED
	default:
		return nil, internalError()
	}
	if value.Fence == 0 || value.HolderComputerID == "" || value.HolderPlacementGeneration == 0 || !value.ExpiresAt.After(value.ClaimedAt) {
		return nil, internalError()
	}
	message := &deliveryv1.RunLaunch{
		Id: value.ID, RunId: value.RunID, AgentId: value.AgentID,
		HolderComputerId:          value.HolderComputerID,
		HolderPlacementGeneration: value.HolderPlacementGeneration, Fence: value.Fence,
		ClaimedAt: timestamppb.New(value.ClaimedAt), ExpiresAt: timestamppb.New(value.ExpiresAt),
		CloseReason: reason,
	}
	if value.ClosedAt != nil {
		message.ClosedAt = timestamppb.New(*value.ClosedAt)
	}
	return message, nil
}

func internalError() error {
	return connect.NewError(connect.CodeInternal, errors.New("agent delivery operation failed"))
}
