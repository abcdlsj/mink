package delivery

import (
	"context"
	"errors"
	"math"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1/deliveryv1connect"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/internal/connectapi"
	"github.com/abcdlsj/sumi/internal/runtimeauth"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
	maxMessageRunes  = 400_000
	maxMentions      = 64
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
	if result.ActiveRun != nil {
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
	if err := messageBodyValid(request.Msg.GetBody()); err != nil {
		return nil, err
	}
	mentions, err := mentionedAgentIDs(request.Msg.GetMentionedAgentIds())
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
		message, err := messageMessage(*result.Message)
		if err != nil {
			return nil, err
		}
		response.Result = &deliveryv1.CompleteRunResponse_Message{Message: message}
	case store.InboxResultHeldDraft:
		if result.Message != nil || result.HeldDraft == nil || result.Run.ResultKind != store.InboxResultHeldDraft || result.Run.ResultID != result.HeldDraft.ID {
			return nil, internalError()
		}
		draft, err := heldDraftMessage(*result.HeldDraft)
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

func messageBodyValid(body string) error {
	if !utf8.ValidString(body) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("message body must be valid UTF-8"))
	}
	size := utf8.RuneCountInString(body)
	if size < 1 || size > maxMessageRunes {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("message body must contain 1 to 400000 characters"))
	}
	return nil
}

func mentionedAgentIDs(values []string) ([]string, error) {
	if len(values) > maxMentions {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("mentioned agent count must be at most 64"))
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		id, err := connectapi.CanonicalID(value, "mentioned agent id")
		if err != nil {
			return nil, err
		}
		if _, ok := seen[id]; ok {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("mentioned agent ids must be unique"))
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
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
	target, err := targetMessage(value.Target)
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

func heldDraftMessage(value store.HeldDraft) (*inboxv1.HeldDraft, error) {
	state := heldDraftStateMessage(value.State)
	if state == inboxv1.HeldDraftState_HELD_DRAFT_STATE_UNSPECIFIED {
		return nil, internalError()
	}
	action := resolutionActionMessage(value.ResolutionAction)
	if value.ResolutionAction != "" && action == inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_UNSPECIFIED {
		return nil, internalError()
	}
	switch value.State {
	case store.HeldDraftStateHeld:
		if value.ResolutionAction != "" || value.ResultKind != "" || value.ResultID != "" {
			return nil, internalError()
		}
	case store.HeldDraftStateCancelled:
		if value.ResolutionAction != store.DraftResolutionCancel || value.ResultKind != "" || value.ResultID != "" {
			return nil, internalError()
		}
	case store.HeldDraftStateSent:
		if value.ResolutionAction != store.DraftResolutionRetry || value.ResultKind != store.InboxResultMessage || value.ResultID == "" {
			return nil, internalError()
		}
	case store.HeldDraftStateSuperseded:
		if value.ResolutionAction != store.DraftResolutionRetry || value.ResultKind != store.InboxResultHeldDraft || value.ResultID == "" {
			return nil, internalError()
		}
	case store.HeldDraftStateRetargeted:
		if value.ResolutionAction != store.DraftResolutionRetarget ||
			(value.ResultKind != store.InboxResultMessage && value.ResultKind != store.InboxResultHeldDraft) || value.ResultID == "" {
			return nil, internalError()
		}
	default:
		return nil, internalError()
	}
	target, err := targetMessage(value.Target)
	if err != nil {
		return nil, err
	}
	message := &inboxv1.HeldDraft{
		Sequence: value.Sequence, Id: value.ID, InboxItemId: value.InboxItemID,
		PredecessorDraftId: value.PredecessorDraftID, SpaceId: value.SpaceID,
		Target: target, BasisTargetSequence: value.BasisTargetSequence,
		Body: value.Body, MentionedAgentIds: value.MentionedAgentIDs,
		State: state, ResolutionAction: action,
		CreatedAt: timestamppb.New(value.CreatedAt), UpdatedAt: timestamppb.New(value.UpdatedAt),
	}
	if value.ResultKind == store.InboxResultMessage {
		message.ResultRef = &inboxv1.HeldDraft_ResultMessageId{ResultMessageId: value.ResultID}
	} else if value.ResultKind == store.InboxResultHeldDraft {
		message.ResultRef = &inboxv1.HeldDraft_ResultHeldDraftId{ResultHeldDraftId: value.ResultID}
	} else if value.ResultKind != "" || value.ResultID != "" {
		return nil, internalError()
	}
	return message, nil
}

func messageMessage(value store.Message) (*spacev1.Message, error) {
	author := principalMessage(value.Author)
	if author.Kind == spacev1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED {
		return nil, internalError()
	}
	if !canonicalStoreID(value.SpaceID) {
		return nil, internalError()
	}
	switch value.Target.Kind {
	case store.MessageTargetSpace:
		if value.Target.ID != value.SpaceID {
			return nil, internalError()
		}
	case store.MessageTargetThread:
		if !canonicalStoreID(value.Target.ID) {
			return nil, internalError()
		}
	default:
		return nil, internalError()
	}
	message := &spacev1.Message{
		Id: value.ID, RequestId: value.RequestID, SpaceId: value.SpaceID,
		TargetSequence: value.TargetSequence, Author: author,
		Body: value.Body, MentionedAgentIds: value.MentionedAgentIDs,
		CreatedAt: timestamppb.New(value.CreatedAt),
	}
	if value.Target.Kind == store.MessageTargetThread {
		message.ThreadRootMessageId = value.Target.ID
	}
	return message, nil
}

func targetMessage(value store.MessageTarget) (*spacev1.MessageTarget, error) {
	if !canonicalStoreID(value.ID) {
		return nil, internalError()
	}
	message := &spacev1.MessageTarget{}
	if value.Kind == store.MessageTargetSpace {
		message.Target = &spacev1.MessageTarget_SpaceId{SpaceId: value.ID}
	} else if value.Kind == store.MessageTargetThread {
		message.Target = &spacev1.MessageTarget_ThreadRootMessageId{ThreadRootMessageId: value.ID}
	} else {
		return nil, internalError()
	}
	return message, nil
}

func canonicalStoreID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func principalMessage(value store.Principal) *spacev1.Principal {
	kind := spacev1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED
	if value.Kind == "human" {
		kind = spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN
	} else if value.Kind == "agent" {
		kind = spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT
	}
	return &spacev1.Principal{Kind: kind, Id: value.ID}
}

func heldDraftStateMessage(value string) inboxv1.HeldDraftState {
	switch value {
	case store.HeldDraftStateHeld:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_HELD
	case store.HeldDraftStateSent:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_SENT
	case store.HeldDraftStateCancelled:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_CANCELLED
	case store.HeldDraftStateSuperseded:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_SUPERSEDED
	case store.HeldDraftStateRetargeted:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_RETARGETED
	default:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_UNSPECIFIED
	}
}

func resolutionActionMessage(value string) inboxv1.DraftResolutionAction {
	switch value {
	case store.DraftResolutionRetry:
		return inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_RETRY
	case store.DraftResolutionCancel:
		return inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_CANCEL
	case store.DraftResolutionRetarget:
		return inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_RETARGET
	default:
		return inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_UNSPECIFIED
	}
}

func internalError() error {
	return connect.NewError(connect.CodeInternal, errors.New("agent delivery operation failed"))
}
