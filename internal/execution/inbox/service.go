package inbox

import (
	"context"
	"errors"
	"math"
	"time"

	"connectrpc.com/connect"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	runtimeauth "github.com/abcdlsj/sumi/internal/authority/runtime"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
	"github.com/abcdlsj/sumi/internal/transport/messagecodec"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

type inboxStore interface {
	GetInboxNotice(context.Context, store.InboxNoticeParams) (bool, error)
	ListInboxItems(context.Context, store.ListInboxItemsParams) ([]store.InboxItem, error)
	ClaimInboxItem(context.Context, store.ClaimInboxItemParams) (store.InboxItem, error)
	ObserveTarget(context.Context, store.ObserveTargetParams) (store.ObserveTargetResult, error)
	CompleteInboxItem(context.Context, store.CompleteInboxItemParams) (store.InboxItem, error)
	SetSpaceMute(context.Context, store.SetSpaceMuteParams) (store.InboxPreferenceResult, error)
	SetThreadFollow(context.Context, store.SetThreadFollowParams) (store.InboxPreferenceResult, error)
	SendInboxReply(context.Context, store.SendInboxReplyParams) (store.SendInboxReplyResult, error)
	ListHeldDrafts(context.Context, store.ListHeldDraftsParams) (store.ListHeldDraftsResult, error)
	ResolveHeldDraft(context.Context, store.ResolveHeldDraftParams) (store.ResolveHeldDraftResult, error)
}

type Service struct {
	store inboxStore
	now   func() time.Time
}

var _ inboxv1connect.InboxServiceHandler = (*Service)(nil)

func New(database inboxStore) *Service {
	return &Service{store: database, now: time.Now}
}

func Procedures() []string {
	return []string{
		inboxv1connect.InboxServiceGetInboxNoticeProcedure,
		inboxv1connect.InboxServiceListInboxItemsProcedure,
		inboxv1connect.InboxServiceClaimInboxItemProcedure,
		inboxv1connect.InboxServiceObserveTargetProcedure,
		inboxv1connect.InboxServiceCompleteInboxItemProcedure,
		inboxv1connect.InboxServiceSetSpaceMuteProcedure,
		inboxv1connect.InboxServiceSetThreadFollowProcedure,
		inboxv1connect.InboxServiceSendInboxReplyProcedure,
		inboxv1connect.InboxServiceListHeldDraftsProcedure,
		inboxv1connect.InboxServiceResolveHeldDraftProcedure,
	}
}

func (s *Service) GetInboxNotice(ctx context.Context, _ *connect.Request[inboxv1.GetInboxNoticeRequest]) (*connect.Response[inboxv1.GetInboxNoticeResponse], error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return nil, err
	}
	hasUnread, err := s.store.GetInboxNotice(ctx, store.InboxNoticeParams{Authentication: authentication, Now: s.now()})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&inboxv1.GetInboxNoticeResponse{HasUnread: hasUnread}), nil
}

func (s *Service) ListInboxItems(ctx context.Context, request *connect.Request[inboxv1.ListInboxItemsRequest]) (*connect.Response[inboxv1.ListInboxItemsResponse], error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return nil, err
	}
	after, limit, err := listParams(request.Msg.GetAfterSequence(), request.Msg.GetLimit())
	if err != nil {
		return nil, err
	}
	items, err := s.store.ListInboxItems(ctx, store.ListInboxItemsParams{
		Authentication: authentication, AfterSequence: after, Limit: limit, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	response := &inboxv1.ListInboxItemsResponse{Items: make([]*inboxv1.InboxItem, 0, len(items))}
	for _, item := range items {
		message, err := inboxItemMessage(item)
		if err != nil {
			return nil, err
		}
		response.Items = append(response.Items, message)
	}
	return connect.NewResponse(response), nil
}

func (s *Service) ClaimInboxItem(ctx context.Context, request *connect.Request[inboxv1.ClaimInboxItemRequest]) (*connect.Response[inboxv1.ClaimInboxItemResponse], error) {
	authentication, requestID, itemID, err := itemMutationParams(ctx, request.Msg.GetRequestId(), request.Msg.GetInboxItemId())
	if err != nil {
		return nil, err
	}
	item, err := s.store.ClaimInboxItem(ctx, store.ClaimInboxItemParams{
		RequestID: requestID, Authentication: authentication, InboxItemID: itemID, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	message, err := inboxItemMessage(item)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&inboxv1.ClaimInboxItemResponse{Item: message}), nil
}

func (s *Service) ObserveTarget(ctx context.Context, request *connect.Request[inboxv1.ObserveTargetRequest]) (*connect.Response[inboxv1.ObserveTargetResponse], error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return nil, err
	}
	target, err := messagecodec.ParseTarget(request.Msg.GetTarget())
	if err != nil {
		return nil, err
	}
	_, limit, err := listParams(0, request.Msg.GetLimit())
	if err != nil {
		return nil, err
	}
	result, err := s.store.ObserveTarget(ctx, store.ObserveTargetParams{
		Authentication: authentication, Target: target, Limit: limit, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	targetMessage, err := messagecodec.Target(result.Target)
	if err != nil {
		return nil, err
	}
	response := &inboxv1.ObserveTargetResponse{
		Target: targetMessage, HeadSequence: result.Head,
		Messages: make([]*spacev1.Message, 0, len(result.Messages)), ObservedAt: timestamppb.New(result.ObservedAt),
	}
	for _, message := range result.Messages {
		message, err := messagecodec.Message(message)
		if err != nil {
			return nil, err
		}
		response.Messages = append(response.Messages, message)
	}
	return connect.NewResponse(response), nil
}

func (s *Service) CompleteInboxItem(ctx context.Context, request *connect.Request[inboxv1.CompleteInboxItemRequest]) (*connect.Response[inboxv1.CompleteInboxItemResponse], error) {
	authentication, requestID, itemID, err := itemMutationParams(ctx, request.Msg.GetRequestId(), request.Msg.GetInboxItemId())
	if err != nil {
		return nil, err
	}
	item, err := s.store.CompleteInboxItem(ctx, store.CompleteInboxItemParams{
		RequestID: requestID, Authentication: authentication, InboxItemID: itemID, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	message, err := inboxItemMessage(item)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&inboxv1.CompleteInboxItemResponse{Item: message}), nil
}

func (s *Service) SetSpaceMute(ctx context.Context, request *connect.Request[inboxv1.SetSpaceMuteRequest]) (*connect.Response[inboxv1.SetSpaceMuteResponse], error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectid.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	spaceID, err := connectid.CanonicalID(request.Msg.GetSpaceId(), "space id")
	if err != nil {
		return nil, err
	}
	result, err := s.store.SetSpaceMute(ctx, store.SetSpaceMuteParams{
		RequestID: requestID, Authentication: authentication, SpaceID: spaceID, Muted: request.Msg.GetMuted(), Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&inboxv1.SetSpaceMuteResponse{
		Muted: result.Enabled, CommittedAt: timestamppb.New(result.CommittedAt),
	}), nil
}

func (s *Service) SetThreadFollow(ctx context.Context, request *connect.Request[inboxv1.SetThreadFollowRequest]) (*connect.Response[inboxv1.SetThreadFollowResponse], error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectid.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	threadID, err := connectid.CanonicalID(request.Msg.GetThreadRootMessageId(), "thread root message id")
	if err != nil {
		return nil, err
	}
	result, err := s.store.SetThreadFollow(ctx, store.SetThreadFollowParams{
		RequestID: requestID, Authentication: authentication, ThreadID: threadID, Followed: request.Msg.GetFollowed(), Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&inboxv1.SetThreadFollowResponse{
		Followed: result.Enabled, CommittedAt: timestamppb.New(result.CommittedAt),
	}), nil
}

func (s *Service) SendInboxReply(ctx context.Context, request *connect.Request[inboxv1.SendInboxReplyRequest]) (*connect.Response[inboxv1.SendInboxReplyResponse], error) {
	authentication, requestID, itemID, err := itemMutationParams(ctx, request.Msg.GetRequestId(), request.Msg.GetInboxItemId())
	if err != nil {
		return nil, err
	}
	if request.Msg.GetBasisTargetSequence() > math.MaxInt64 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("basis target sequence is too large"))
	}
	if err := messagecodec.ValidateBody(request.Msg.GetBody()); err != nil {
		return nil, err
	}
	mentions, err := messagecodec.MentionedAgentIDs(request.Msg.GetMentionedAgentIds())
	if err != nil {
		return nil, err
	}
	result, err := s.store.SendInboxReply(ctx, store.SendInboxReplyParams{
		RequestID: requestID, Authentication: authentication, InboxItemID: itemID,
		BasisTargetSequence: request.Msg.GetBasisTargetSequence(), Body: request.Msg.GetBody(),
		MentionedAgentIDs: mentions, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	response := &inboxv1.SendInboxReplyResponse{CommittedAt: timestamppb.New(result.CommittedAt)}
	switch result.Kind {
	case store.InboxResultMessage:
		if result.Message == nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("send inbox reply result invalid"))
		}
		message, err := messagecodec.Message(*result.Message)
		if err != nil {
			return nil, err
		}
		response.Result = &inboxv1.SendInboxReplyResponse_Message{Message: message}
	case store.InboxResultHeldDraft:
		if result.HeldDraft == nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("send inbox reply result invalid"))
		}
		draft, err := messagecodec.HeldDraft(*result.HeldDraft)
		if err != nil {
			return nil, err
		}
		response.Result = &inboxv1.SendInboxReplyResponse_HeldDraft{HeldDraft: draft}
	default:
		return nil, connect.NewError(connect.CodeInternal, errors.New("send inbox reply result invalid"))
	}
	return connect.NewResponse(response), nil
}

func (s *Service) ListHeldDrafts(ctx context.Context, request *connect.Request[inboxv1.ListHeldDraftsRequest]) (*connect.Response[inboxv1.ListHeldDraftsResponse], error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return nil, err
	}
	after, limit, err := requiredListParams(request.Msg.GetAfterSequence(), request.Msg.GetLimit())
	if err != nil {
		return nil, err
	}
	result, err := s.store.ListHeldDrafts(ctx, store.ListHeldDraftsParams{
		Authentication: authentication, AfterSequence: after, Limit: limit, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	response := &inboxv1.ListHeldDraftsResponse{
		Drafts: make([]*inboxv1.HeldDraft, 0, len(result.Drafts)), NextSequence: result.NextSequence,
	}
	for _, draft := range result.Drafts {
		draft, err := messagecodec.HeldDraft(draft)
		if err != nil {
			return nil, err
		}
		response.Drafts = append(response.Drafts, draft)
	}
	return connect.NewResponse(response), nil
}

func (s *Service) ResolveHeldDraft(ctx context.Context, request *connect.Request[inboxv1.ResolveHeldDraftRequest]) (*connect.Response[inboxv1.ResolveHeldDraftResponse], error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectid.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	draftID, err := connectid.CanonicalID(request.Msg.GetHeldDraftId(), "held draft id")
	if err != nil {
		return nil, err
	}
	action, err := resolutionAction(request.Msg.GetAction())
	if err != nil {
		return nil, err
	}
	target := store.MessageTarget{}
	if action == store.DraftResolutionRetarget {
		target, err = messagecodec.ParseTarget(request.Msg.GetTarget())
		if err != nil {
			return nil, err
		}
	}
	if action != store.DraftResolutionCancel && request.Msg.GetBasisTargetSequence() > math.MaxInt64 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("basis target sequence is too large"))
	}
	result, err := s.store.ResolveHeldDraft(ctx, store.ResolveHeldDraftParams{
		RequestID: requestID, Authentication: authentication, HeldDraftID: draftID,
		Action: action, Target: target, BasisTargetSequence: request.Msg.GetBasisTargetSequence(), Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	item, err := inboxItemMessage(result.InboxItem)
	if err != nil {
		return nil, err
	}
	response := &inboxv1.ResolveHeldDraftResponse{
		Action: messagecodec.DraftResolutionAction(result.Action), Item: item,
		CommittedAt: timestamppb.New(result.CommittedAt),
	}
	switch result.Kind {
	case "":
	case store.InboxResultMessage:
		if result.Message == nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("resolve held draft result invalid"))
		}
		message, err := messagecodec.Message(*result.Message)
		if err != nil {
			return nil, err
		}
		response.Result = &inboxv1.ResolveHeldDraftResponse_Message{Message: message}
	case store.InboxResultHeldDraft:
		if result.HeldDraft == nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("resolve held draft result invalid"))
		}
		draft, err := messagecodec.HeldDraft(*result.HeldDraft)
		if err != nil {
			return nil, err
		}
		response.Result = &inboxv1.ResolveHeldDraftResponse_HeldDraft{HeldDraft: draft}
	default:
		return nil, connect.NewError(connect.CodeInternal, errors.New("resolve held draft result invalid"))
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

func itemMutationParams(ctx context.Context, requestIDValue, itemIDValue string) (store.AgentRuntimeAuthentication, string, string, error) {
	authentication, err := authentication(ctx)
	if err != nil {
		return store.AgentRuntimeAuthentication{}, "", "", err
	}
	requestID, err := connectid.CanonicalID(requestIDValue, "request id")
	if err != nil {
		return store.AgentRuntimeAuthentication{}, "", "", err
	}
	itemID, err := connectid.CanonicalID(itemIDValue, "inbox item id")
	if err != nil {
		return store.AgentRuntimeAuthentication{}, "", "", err
	}
	return authentication, requestID, itemID, nil
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

func requiredListParams(after uint64, limit uint32) (uint64, uint32, error) {
	if limit == 0 {
		return 0, 0, connect.NewError(connect.CodeInvalidArgument, errors.New("limit must be between 1 and 200"))
	}
	return listParams(after, limit)
}

func resolutionAction(value inboxv1.DraftResolutionAction) (string, error) {
	switch value {
	case inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_RETRY:
		return store.DraftResolutionRetry, nil
	case inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_CANCEL:
		return store.DraftResolutionCancel, nil
	case inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_RETARGET:
		return store.DraftResolutionRetarget, nil
	default:
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("draft resolution action is invalid"))
	}
}

func serviceError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrAgentRuntimeUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("agent runtime authentication invalid"))
	case errors.Is(err, store.ErrPermissionDenied), errors.Is(err, store.ErrInboxAccessLost):
		return connect.NewError(connect.CodePermissionDenied, errors.New("agent inbox action denied"))
	case errors.Is(err, store.ErrInboxItemNotFound), errors.Is(err, store.ErrHeldDraftNotFound),
		errors.Is(err, store.ErrSpaceNotFound), errors.Is(err, store.ErrThreadNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrInboxRequestConflict):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, store.ErrInboxItemNotUnread), errors.Is(err, store.ErrInboxItemNotClaimed),
		errors.Is(err, store.ErrInboxItemHasHeldDraft), errors.Is(err, store.ErrInboxBasisMismatch),
		errors.Is(err, store.ErrHeldDraftNotHeld), errors.Is(err, store.ErrSpaceArchived):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, store.ErrInvalidInboxLimit), errors.Is(err, store.ErrInvalidMessageLimit),
		errors.Is(err, store.ErrInvalidMessageTarget), errors.Is(err, store.ErrInvalidMessageBody),
		errors.Is(err, store.ErrInvalidMention), errors.Is(err, store.ErrInvalidDraftResolution):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, errors.New("agent inbox operation failed"))
	}
}

func inboxItemMessage(item store.InboxItem) (*inboxv1.InboxItem, error) {
	target, err := messagecodec.Target(item.Target)
	if err != nil {
		return nil, err
	}
	result := &inboxv1.InboxItem{
		Sequence: item.Sequence, Id: item.ID, AgentId: item.AgentID, SpaceId: item.SpaceID,
		Target: target, TriggerMessageId: item.TriggerMessageID,
		TriggerTargetSequence: item.TriggerTargetSequence, Reason: inboxReasonMessage(item.Reason),
		State: inboxStateMessage(item.State), Completion: inboxCompletionMessage(item.Completion),
		CreatedAt: timestamppb.New(item.CreatedAt),
	}
	if item.ClaimedAt != nil {
		result.ClaimedAt = timestamppb.New(*item.ClaimedAt)
	}
	if item.DoneAt != nil {
		result.DoneAt = timestamppb.New(*item.DoneAt)
	}
	return result, nil
}

func inboxReasonMessage(reason string) inboxv1.InboxReason {
	switch reason {
	case store.InboxReasonDM:
		return inboxv1.InboxReason_INBOX_REASON_DM
	case store.InboxReasonMention:
		return inboxv1.InboxReason_INBOX_REASON_MENTION
	case store.InboxReasonThreadFollow:
		return inboxv1.InboxReason_INBOX_REASON_THREAD_FOLLOW
	default:
		return inboxv1.InboxReason_INBOX_REASON_UNSPECIFIED
	}
}

func inboxStateMessage(state string) inboxv1.InboxState {
	switch state {
	case store.InboxStateUnread:
		return inboxv1.InboxState_INBOX_STATE_UNREAD
	case store.InboxStateClaimed:
		return inboxv1.InboxState_INBOX_STATE_CLAIMED
	case store.InboxStateDone:
		return inboxv1.InboxState_INBOX_STATE_DONE
	default:
		return inboxv1.InboxState_INBOX_STATE_UNSPECIFIED
	}
}

func inboxCompletionMessage(completion string) inboxv1.InboxCompletion {
	switch completion {
	case store.InboxCompletionSent:
		return inboxv1.InboxCompletion_INBOX_COMPLETION_SENT
	case store.InboxCompletionCancelled:
		return inboxv1.InboxCompletion_INBOX_COMPLETION_CANCELLED
	case store.InboxCompletionSilent:
		return inboxv1.InboxCompletion_INBOX_COMPLETION_SILENT
	case store.InboxCompletionAccessLost:
		return inboxv1.InboxCompletion_INBOX_COMPLETION_ACCESS_LOST
	default:
		return inboxv1.InboxCompletion_INBOX_COMPLETION_UNSPECIFIED
	}
}
