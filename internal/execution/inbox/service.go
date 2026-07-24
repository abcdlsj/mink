package inbox

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"

	"connectrpc.com/connect"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	sharedauthentication "github.com/abcdlsj/sumi/internal/authentication"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
	"github.com/abcdlsj/sumi/internal/transport/messagecodec"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

type inboxStore interface {
	sharedauthentication.Authenticator
	GetInboxNotice(context.Context, executionapp.InboxNoticeQuery) (bool, error)
	ListInboxItems(context.Context, executionapp.ListInboxItemsQuery) ([]executionapp.InboxItem, error)
	ClaimInboxItem(context.Context, executionapp.ClaimInboxItemCommand) (executionapp.InboxItem, error)
	ObserveTarget(context.Context, executionapp.ObserveTargetQuery) (executionapp.ObserveTargetResult, error)
	CompleteInboxItem(context.Context, executionapp.CompleteInboxItemCommand) (executionapp.InboxItem, error)
	SetSpaceMute(context.Context, executionapp.SetSpaceMuteCommand) (executionapp.InboxPreferenceResult, error)
	SetThreadFollow(context.Context, executionapp.SetThreadFollowCommand) (executionapp.InboxPreferenceResult, error)
	SendInboxReply(context.Context, executionapp.SendInboxReplyCommand) (executionapp.SendInboxReplyResult, error)
	ListHeldDrafts(context.Context, executionapp.ListHeldDraftsQuery) (executionapp.ListHeldDraftsResult, error)
	ResolveHeldDraft(context.Context, executionapp.ResolveHeldDraftCommand) (executionapp.ResolveHeldDraftResult, error)
}

type Service struct {
	store  inboxStore
	origin string
	now    func() time.Time
}

var _ inboxv1connect.InboxServiceHandler = (*Service)(nil)

func New(db inboxStore, browserOrigin string) *Service {
	return &Service{store: db, origin: browserOrigin, now: time.Now}
}

func (s *Service) GetInboxNotice(ctx context.Context, request *connect.Request[inboxv1.GetInboxNoticeRequest]) (*connect.Response[inboxv1.GetInboxNoticeResponse], error) {
	authentication, err := s.authentication(ctx, http.Header(request.Header()), false)
	if err != nil {
		return nil, err
	}
	hasUnread, err := s.store.GetInboxNotice(ctx, executionapp.InboxNoticeQuery{Authentication: authentication, Now: s.now()})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&inboxv1.GetInboxNoticeResponse{HasUnread: hasUnread}), nil
}

func (s *Service) ListInboxItems(ctx context.Context, request *connect.Request[inboxv1.ListInboxItemsRequest]) (*connect.Response[inboxv1.ListInboxItemsResponse], error) {
	authentication, err := s.authentication(ctx, http.Header(request.Header()), false)
	if err != nil {
		return nil, err
	}
	after, limit, err := listParams(request.Msg.GetAfterSequence(), request.Msg.GetLimit())
	if err != nil {
		return nil, err
	}
	items, err := s.store.ListInboxItems(ctx, executionapp.ListInboxItemsQuery{
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
	authentication, err := s.authentication(ctx, http.Header(request.Header()), true)
	if err != nil {
		return nil, err
	}
	requestID, itemID, err := itemMutationParams(request.Msg.GetRequestId(), request.Msg.GetInboxItemId())
	if err != nil {
		return nil, err
	}
	item, err := s.store.ClaimInboxItem(ctx, executionapp.ClaimInboxItemCommand{
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
	authentication, err := s.authentication(ctx, http.Header(request.Header()), true)
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
	result, err := s.store.ObserveTarget(ctx, executionapp.ObserveTargetQuery{
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
	authentication, err := s.authentication(ctx, http.Header(request.Header()), true)
	if err != nil {
		return nil, err
	}
	requestID, itemID, err := itemMutationParams(request.Msg.GetRequestId(), request.Msg.GetInboxItemId())
	if err != nil {
		return nil, err
	}
	item, err := s.store.CompleteInboxItem(ctx, executionapp.CompleteInboxItemCommand{
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
	authentication, err := s.authentication(ctx, http.Header(request.Header()), true)
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
	result, err := s.store.SetSpaceMute(ctx, executionapp.SetSpaceMuteCommand{
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
	authentication, err := s.authentication(ctx, http.Header(request.Header()), true)
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
	result, err := s.store.SetThreadFollow(ctx, executionapp.SetThreadFollowCommand{
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
	authentication, err := s.agentAuthentication(ctx, http.Header(request.Header()), true)
	if err != nil {
		return nil, err
	}
	requestID, itemID, err := itemMutationParams(request.Msg.GetRequestId(), request.Msg.GetInboxItemId())
	if err != nil {
		return nil, err
	}
	if request.Msg.GetBasisTargetSequence() > math.MaxInt64 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("basis target sequence is too large"))
	}
	if err := messagecodec.ValidateBody(request.Msg.GetBody()); err != nil {
		return nil, err
	}
	mentions, err := messagecodec.MentionedPrincipals(request.Msg.GetMentionedPrincipals())
	if err != nil {
		return nil, err
	}
	result, err := s.store.SendInboxReply(ctx, executionapp.SendInboxReplyCommand{
		RequestID: requestID, Authentication: authentication, InboxItemID: itemID,
		BasisTargetSequence: request.Msg.GetBasisTargetSequence(), Body: request.Msg.GetBody(),
		MentionedPrincipals: mentions, Now: s.now(),
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	response := &inboxv1.SendInboxReplyResponse{CommittedAt: timestamppb.New(result.CommittedAt)}
	switch result.Kind {
	case executionapp.ResultMessage:
		if result.Message == nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("send inbox reply result invalid"))
		}
		message, err := messagecodec.Message(*result.Message)
		if err != nil {
			return nil, err
		}
		response.Result = &inboxv1.SendInboxReplyResponse_Message{Message: message}
	case executionapp.ResultHeldDraft:
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
	authentication, err := s.agentAuthentication(ctx, http.Header(request.Header()), false)
	if err != nil {
		return nil, err
	}
	after, limit, err := requiredListParams(request.Msg.GetAfterSequence(), request.Msg.GetLimit())
	if err != nil {
		return nil, err
	}
	result, err := s.store.ListHeldDrafts(ctx, executionapp.ListHeldDraftsQuery{
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
	authentication, err := s.agentAuthentication(ctx, http.Header(request.Header()), true)
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
	target := collaborationapp.MessageTarget{}
	if action == executionapp.DraftResolutionRetarget {
		target, err = messagecodec.ParseTarget(request.Msg.GetTarget())
		if err != nil {
			return nil, err
		}
	}
	if action != executionapp.DraftResolutionCancel && request.Msg.GetBasisTargetSequence() > math.MaxInt64 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("basis target sequence is too large"))
	}
	result, err := s.store.ResolveHeldDraft(ctx, executionapp.ResolveHeldDraftCommand{
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
	case executionapp.ResultMessage:
		if result.Message == nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("resolve held draft result invalid"))
		}
		message, err := messagecodec.Message(*result.Message)
		if err != nil {
			return nil, err
		}
		response.Result = &inboxv1.ResolveHeldDraftResponse_Message{Message: message}
	case executionapp.ResultHeldDraft:
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

func (s *Service) authentication(ctx context.Context, header http.Header, mutation bool) (executionapp.InboxAuthentication, error) {
	resolved, err := sharedauthentication.Resolve(ctx, s.store, header, mutation, s.origin, s.now())
	if err != nil {
		return executionapp.InboxAuthentication{}, authenticationError(err)
	}
	if human, ok := resolved.Human(); ok {
		return executionapp.HumanInboxAuthentication(human), nil
	}
	if agent, ok := resolved.Agent(); ok {
		return executionapp.AgentInboxAuthentication(agent), nil
	}
	return executionapp.InboxAuthentication{}, connect.NewError(connect.CodeUnauthenticated, errors.New("inbox actor authentication invalid"))
}

func (s *Service) agentAuthentication(ctx context.Context, header http.Header, mutation bool) (authorityapp.RuntimeAuthentication, error) {
	resolved, err := sharedauthentication.Resolve(ctx, s.store, header, mutation, s.origin, s.now())
	if err != nil {
		return authorityapp.RuntimeAuthentication{}, authenticationError(err)
	}
	agent, ok := resolved.Agent()
	if !ok {
		return authorityapp.RuntimeAuthentication{}, connect.NewError(connect.CodeUnauthenticated, errors.New("agent runtime authentication required"))
	}
	return agent, nil
}

func authenticationError(err error) error {
	switch {
	case errors.Is(err, sharedauthentication.ErrSameOrigin):
		return connect.NewError(connect.CodePermissionDenied, errors.New("same-origin browser request required"))
	case errors.Is(err, sharedauthentication.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("inbox authentication is unavailable"))
	default:
		return connect.NewError(connect.CodeUnauthenticated, errors.New("inbox actor authentication invalid"))
	}
}

func itemMutationParams(requestIDValue, itemIDValue string) (string, string, error) {
	requestID, err := connectid.CanonicalID(requestIDValue, "request id")
	if err != nil {
		return "", "", err
	}
	itemID, err := connectid.CanonicalID(itemIDValue, "inbox item id")
	if err != nil {
		return "", "", err
	}
	return requestID, itemID, nil
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
		return executionapp.DraftResolutionRetry, nil
	case inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_CANCEL:
		return executionapp.DraftResolutionCancel, nil
	case inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_RETARGET:
		return executionapp.DraftResolutionRetarget, nil
	default:
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("draft resolution action is invalid"))
	}
}

func serviceError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authorityapp.ErrRuntimeUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("agent runtime authentication invalid"))
	case errors.Is(err, authoritydomain.ErrPermissionDenied), errors.Is(err, executionapp.ErrInboxAccessLost):
		return connect.NewError(connect.CodePermissionDenied, errors.New("inbox action denied"))
	case errors.Is(err, executionapp.ErrInboxItemNotFound), errors.Is(err, executionapp.ErrHeldDraftNotFound),
		errors.Is(err, collaborationapp.ErrSpaceNotFound), errors.Is(err, collaborationapp.ErrThreadNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, executionapp.ErrInboxRequestConflict):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, executionapp.ErrInboxItemNotUnread), errors.Is(err, executionapp.ErrInboxItemNotClaimed),
		errors.Is(err, executionapp.ErrInboxItemHasHeldDraft), errors.Is(err, executionapp.ErrInboxBasisMismatch),
		errors.Is(err, executionapp.ErrHeldDraftNotHeld), errors.Is(err, collaborationapp.ErrSpaceArchived):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, executionapp.ErrInvalidInboxLimit), errors.Is(err, collaborationapp.ErrInvalidMessageLimit),
		errors.Is(err, collaborationapp.ErrInvalidMessageTarget), errors.Is(err, collaborationapp.ErrInvalidMessageBody),
		errors.Is(err, collaborationapp.ErrInvalidMention), errors.Is(err, executionapp.ErrInvalidDraftResolution):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, errors.New("inbox operation failed"))
	}
}

func inboxItemMessage(item executionapp.InboxItem) (*inboxv1.InboxItem, error) {
	target, err := messagecodec.Target(item.Target)
	if err != nil {
		return nil, err
	}
	recipient, err := messagecodec.Principal(item.Recipient)
	if err != nil {
		return nil, err
	}
	result := &inboxv1.InboxItem{
		Sequence: item.Sequence, Id: item.ID, Recipient: recipient, SpaceId: item.SpaceID,
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
	case executionapp.InboxReasonDM:
		return inboxv1.InboxReason_INBOX_REASON_DM
	case executionapp.InboxReasonMention:
		return inboxv1.InboxReason_INBOX_REASON_MENTION
	case executionapp.InboxReasonThreadFollow:
		return inboxv1.InboxReason_INBOX_REASON_THREAD_FOLLOW
	default:
		return inboxv1.InboxReason_INBOX_REASON_UNSPECIFIED
	}
}

func inboxStateMessage(state string) inboxv1.InboxState {
	switch state {
	case executionapp.InboxStateUnread:
		return inboxv1.InboxState_INBOX_STATE_UNREAD
	case executionapp.InboxStateClaimed:
		return inboxv1.InboxState_INBOX_STATE_CLAIMED
	case executionapp.InboxStateDone:
		return inboxv1.InboxState_INBOX_STATE_DONE
	default:
		return inboxv1.InboxState_INBOX_STATE_UNSPECIFIED
	}
}

func inboxCompletionMessage(completion string) inboxv1.InboxCompletion {
	switch completion {
	case executionapp.InboxCompletionSent:
		return inboxv1.InboxCompletion_INBOX_COMPLETION_SENT
	case executionapp.InboxCompletionCancelled:
		return inboxv1.InboxCompletion_INBOX_COMPLETION_CANCELLED
	case executionapp.InboxCompletionSilent:
		return inboxv1.InboxCompletion_INBOX_COMPLETION_SILENT
	case executionapp.InboxCompletionAccessLost:
		return inboxv1.InboxCompletion_INBOX_COMPLETION_ACCESS_LOST
	default:
		return inboxv1.InboxCompletion_INBOX_COMPLETION_UNSPECIFIED
	}
}
