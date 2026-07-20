package collaboration

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/connectapi"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultMessageLimit = 50
	maxMessageLimit     = 200
	maxMessageBodyRunes = 400_000
)

type Service struct {
	store *store.Store
	now   func() time.Time
}

var _ spacev1connect.CollaborationServiceHandler = (*Service)(nil)

func New(database *store.Store) *Service {
	return &Service{store: database, now: time.Now}
}

func (s *Service) CreateDM(ctx context.Context, request *connect.Request[spacev1.CreateDMRequest]) (*connect.Response[spacev1.CreateDMResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectapi.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	peer, err := principalParams(request.Msg.GetPeer(), actor.OrganizationID)
	if err != nil {
		return nil, err
	}
	space, err := s.store.CreateDM(ctx, store.CreateDMParams{RequestID: requestID, Actor: actor, Peer: peer, Now: s.now()})
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.CreateDMResponse{Space: spaceMessage(space)}), nil
}

func (s *Service) CreateGroup(ctx context.Context, request *connect.Request[spacev1.CreateGroupRequest]) (*connect.Response[spacev1.CreateGroupResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectapi.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	if err := groupNameValid(request.Msg.GetName()); err != nil {
		return nil, err
	}
	space, err := s.store.CreateGroup(ctx, store.CreateGroupParams{RequestID: requestID, Actor: actor, Name: request.Msg.GetName(), Now: s.now()})
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.CreateGroupResponse{Space: spaceMessage(space)}), nil
}

func (s *Service) GetSpace(ctx context.Context, request *connect.Request[spacev1.GetSpaceRequest]) (*connect.Response[spacev1.GetSpaceResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	spaceID, err := connectapi.CanonicalID(request.Msg.GetSpaceId(), "space id")
	if err != nil {
		return nil, err
	}
	space, err := s.store.GetSpace(ctx, store.SpaceReadParams{Actor: actor, SpaceID: spaceID, Now: s.now()})
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.GetSpaceResponse{Space: spaceMessage(space)}), nil
}

func (s *Service) ListSpaces(ctx context.Context, _ *connect.Request[spacev1.ListSpacesRequest]) (*connect.Response[spacev1.ListSpacesResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	spaces, err := s.store.ListSpaces(ctx, store.ListSpacesParams{Actor: actor, Now: s.now()})
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	response := &spacev1.ListSpacesResponse{Spaces: make([]*spacev1.Space, 0, len(spaces))}
	for _, space := range spaces {
		response.Spaces = append(response.Spaces, spaceMessage(space))
	}
	return connect.NewResponse(response), nil
}

func (s *Service) AddMember(ctx context.Context, request *connect.Request[spacev1.AddMemberRequest]) (*connect.Response[spacev1.AddMemberResponse], error) {
	params, err := s.memberParams(ctx, request.Msg.GetRequestId(), request.Msg.GetSpaceId(), request.Msg.GetMember())
	if err != nil {
		return nil, err
	}
	receipt, err := s.store.AddMember(ctx, params)
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.AddMemberResponse{Receipt: receiptMessage(receipt)}), nil
}

func (s *Service) RemoveMember(ctx context.Context, request *connect.Request[spacev1.RemoveMemberRequest]) (*connect.Response[spacev1.RemoveMemberResponse], error) {
	params, err := s.memberParams(ctx, request.Msg.GetRequestId(), request.Msg.GetSpaceId(), request.Msg.GetMember())
	if err != nil {
		return nil, err
	}
	receipt, err := s.store.RemoveMember(ctx, params)
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.RemoveMemberResponse{Receipt: receiptMessage(receipt)}), nil
}

func (s *Service) ListMembers(ctx context.Context, request *connect.Request[spacev1.ListMembersRequest]) (*connect.Response[spacev1.ListMembersResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	spaceID, err := connectapi.CanonicalID(request.Msg.GetSpaceId(), "space id")
	if err != nil {
		return nil, err
	}
	memberships, err := s.store.ListMembers(ctx, store.SpaceReadParams{Actor: actor, SpaceID: spaceID, Now: s.now()})
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	response := &spacev1.ListMembersResponse{Memberships: make([]*spacev1.Membership, 0, len(memberships))}
	for _, membership := range memberships {
		response.Memberships = append(response.Memberships, membershipMessage(membership))
	}
	return connect.NewResponse(response), nil
}

func (s *Service) ArchiveSpace(ctx context.Context, request *connect.Request[spacev1.ArchiveSpaceRequest]) (*connect.Response[spacev1.ArchiveSpaceResponse], error) {
	params, err := s.archiveParams(ctx, request.Msg.GetRequestId(), request.Msg.GetSpaceId())
	if err != nil {
		return nil, err
	}
	receipt, err := s.store.ArchiveSpace(ctx, params)
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.ArchiveSpaceResponse{Receipt: receiptMessage(receipt)}), nil
}

func (s *Service) UnarchiveSpace(ctx context.Context, request *connect.Request[spacev1.UnarchiveSpaceRequest]) (*connect.Response[spacev1.UnarchiveSpaceResponse], error) {
	params, err := s.archiveParams(ctx, request.Msg.GetRequestId(), request.Msg.GetSpaceId())
	if err != nil {
		return nil, err
	}
	receipt, err := s.store.UnarchiveSpace(ctx, params)
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.UnarchiveSpaceResponse{Receipt: receiptMessage(receipt)}), nil
}

func (s *Service) SendMessage(ctx context.Context, request *connect.Request[spacev1.SendMessageRequest]) (*connect.Response[spacev1.SendMessageResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectapi.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	target, err := targetParams(request.Msg.GetTarget())
	if err != nil {
		return nil, err
	}
	if err := messageBodyValid(request.Msg.GetBody()); err != nil {
		return nil, err
	}
	message, err := s.store.SendMessage(ctx, store.SendMessageParams{
		RequestID: requestID, Actor: actor, Target: target, Body: request.Msg.GetBody(), Now: s.now(),
	})
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.SendMessageResponse{Message: messageMessage(message)}), nil
}

func (s *Service) GetMessage(ctx context.Context, request *connect.Request[spacev1.GetMessageRequest]) (*connect.Response[spacev1.GetMessageResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	messageID, err := connectapi.CanonicalID(request.Msg.GetMessageId(), "message id")
	if err != nil {
		return nil, err
	}
	message, err := s.store.GetMessage(ctx, store.GetMessageParams{Actor: actor, MessageID: messageID, Now: s.now()})
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.GetMessageResponse{Message: messageMessage(message)}), nil
}

func (s *Service) GetThread(ctx context.Context, request *connect.Request[spacev1.GetThreadRequest]) (*connect.Response[spacev1.GetThreadResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	threadID, err := connectapi.CanonicalID(request.Msg.GetThreadRootMessageId(), "thread root message id")
	if err != nil {
		return nil, err
	}
	thread, err := s.store.GetThread(ctx, store.GetThreadParams{Actor: actor, ThreadID: threadID, Now: s.now()})
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.GetThreadResponse{Thread: threadMessage(thread)}), nil
}

func (s *Service) ListMessages(ctx context.Context, request *connect.Request[spacev1.ListMessagesRequest]) (*connect.Response[spacev1.ListMessagesResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	target, err := targetParams(request.Msg.GetTarget())
	if err != nil {
		return nil, err
	}
	if request.Msg.GetAfterSequence() > math.MaxInt64 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("after sequence is too large"))
	}
	limit := request.Msg.GetLimit()
	if limit == 0 {
		limit = defaultMessageLimit
	}
	if limit > maxMessageLimit {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("message limit must be at most 200"))
	}
	messages, err := s.store.ListMessages(ctx, store.ListMessagesParams{
		Actor: actor, Target: target, AfterSequence: request.Msg.GetAfterSequence(), Limit: limit, Now: s.now(),
	})
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	response := &spacev1.ListMessagesResponse{Messages: make([]*spacev1.Message, 0, len(messages))}
	for _, message := range messages {
		response.Messages = append(response.Messages, messageMessage(message))
	}
	return connect.NewResponse(response), nil
}

func (s *Service) memberParams(ctx context.Context, requestIDValue, spaceIDValue string, memberValue *spacev1.Principal) (store.ChangeMemberParams, error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return store.ChangeMemberParams{}, err
	}
	requestID, err := connectapi.CanonicalID(requestIDValue, "request id")
	if err != nil {
		return store.ChangeMemberParams{}, err
	}
	spaceID, err := connectapi.CanonicalID(spaceIDValue, "space id")
	if err != nil {
		return store.ChangeMemberParams{}, err
	}
	member, err := principalParams(memberValue, actor.OrganizationID)
	if err != nil {
		return store.ChangeMemberParams{}, err
	}
	return store.ChangeMemberParams{RequestID: requestID, Actor: actor, SpaceID: spaceID, Member: member, Now: s.now()}, nil
}

func (s *Service) archiveParams(ctx context.Context, requestIDValue, spaceIDValue string) (store.ChangeSpaceArchiveParams, error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return store.ChangeSpaceArchiveParams{}, err
	}
	requestID, err := connectapi.CanonicalID(requestIDValue, "request id")
	if err != nil {
		return store.ChangeSpaceArchiveParams{}, err
	}
	spaceID, err := connectapi.CanonicalID(spaceIDValue, "space id")
	if err != nil {
		return store.ChangeSpaceArchiveParams{}, err
	}
	return store.ChangeSpaceArchiveParams{RequestID: requestID, Actor: actor, SpaceID: spaceID, Now: s.now()}, nil
}

func principalParams(value *spacev1.Principal, organizationID string) (store.Principal, error) {
	if value == nil {
		return store.Principal{}, connect.NewError(connect.CodeInvalidArgument, errors.New("principal is required"))
	}
	kind := ""
	switch value.GetKind() {
	case spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN:
		kind = "human"
	case spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT:
		kind = "agent"
	}
	if kind == "" {
		return store.Principal{}, connect.NewError(connect.CodeInvalidArgument, errors.New("principal kind is invalid"))
	}
	id, err := connectapi.CanonicalID(value.GetId(), "principal id")
	if err != nil {
		return store.Principal{}, err
	}
	return store.Principal{Kind: kind, ID: id, OrganizationID: organizationID}, nil
}

func targetParams(value *spacev1.MessageTarget) (store.MessageTarget, error) {
	if value == nil {
		return store.MessageTarget{}, connect.NewError(connect.CodeInvalidArgument, errors.New("message target is required"))
	}
	switch target := value.GetTarget().(type) {
	case *spacev1.MessageTarget_SpaceId:
		id, err := connectapi.CanonicalID(target.SpaceId, "space id")
		if err != nil {
			return store.MessageTarget{}, err
		}
		return store.MessageTarget{Kind: store.MessageTargetSpace, ID: id}, nil
	case *spacev1.MessageTarget_ThreadRootMessageId:
		id, err := connectapi.CanonicalID(target.ThreadRootMessageId, "thread root message id")
		if err != nil {
			return store.MessageTarget{}, err
		}
		return store.MessageTarget{Kind: store.MessageTargetThread, ID: id}, nil
	default:
		return store.MessageTarget{}, connect.NewError(connect.CodeInvalidArgument, errors.New("message target is invalid"))
	}
}

func groupNameValid(name string) error {
	if !utf8.ValidString(name) || name != strings.TrimSpace(name) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("group name must not contain surrounding whitespace"))
	}
	size := utf8.RuneCountInString(name)
	if size < 1 || size > 100 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("group name must contain 1 to 100 characters"))
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("group name cannot contain control characters"))
		}
	}
	return nil
}

func messageBodyValid(body string) error {
	if !utf8.ValidString(body) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("message body must be valid UTF-8"))
	}
	size := utf8.RuneCountInString(body)
	if size < 1 || size > maxMessageBodyRunes {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("message body must contain 1 to 400000 characters"))
	}
	return nil
}

func collaborationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("collaboration action denied"))
	case errors.Is(err, store.ErrSpaceNotFound), errors.Is(err, store.ErrMessageNotFound), errors.Is(err, store.ErrThreadNotFound),
		errors.Is(err, store.ErrMembershipNotFound), errors.Is(err, store.ErrPrincipalNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrCollaborationRequestConflict), errors.Is(err, store.ErrMembershipExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, store.ErrSpaceArchived), errors.Is(err, store.ErrDMImmutable), errors.Is(err, store.ErrLastActiveHumanMember),
		errors.Is(err, store.ErrInvalidMessageTarget):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, store.ErrDMRequiresDistinctPrincipals), errors.Is(err, store.ErrInvalidSpaceName),
		errors.Is(err, store.ErrInvalidPrincipal), errors.Is(err, store.ErrInvalidMessageBody), errors.Is(err, store.ErrInvalidMessageLimit):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func principalMessage(principal store.Principal) *spacev1.Principal {
	kind := spacev1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED
	if principal.Kind == "human" {
		kind = spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN
	} else if principal.Kind == "agent" {
		kind = spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT
	}
	return &spacev1.Principal{Kind: kind, Id: principal.ID}
}

func spaceMessage(space store.Space) *spacev1.Space {
	kind := spacev1.SpaceKind_SPACE_KIND_UNSPECIFIED
	if space.Kind == store.SpaceKindDM {
		kind = spacev1.SpaceKind_SPACE_KIND_DM
	} else if space.Kind == store.SpaceKindGroup {
		kind = spacev1.SpaceKind_SPACE_KIND_GROUP
	}
	message := &spacev1.Space{
		Id: space.ID, OrganizationId: space.OrganizationID, Kind: kind, Name: space.Name,
		CreatedAt: timestamppb.New(space.CreatedAt), UpdatedAt: timestamppb.New(space.UpdatedAt),
	}
	if space.ArchivedAt != nil {
		message.ArchivedAt = timestamppb.New(*space.ArchivedAt)
	}
	return message
}

func membershipMessage(membership store.Membership) *spacev1.Membership {
	return &spacev1.Membership{
		SpaceId: membership.SpaceID, Principal: principalMessage(membership.Principal), JoinedAt: timestamppb.New(membership.JoinedAt),
	}
}

func threadMessage(thread store.Thread) *spacev1.Thread {
	return &spacev1.Thread{Id: thread.ID, SpaceId: thread.SpaceID, CreatedAt: timestamppb.New(thread.CreatedAt)}
}

func messageMessage(message store.Message) *spacev1.Message {
	result := &spacev1.Message{
		Id: message.ID, RequestId: message.RequestID, SpaceId: message.SpaceID,
		TargetSequence: message.TargetSequence, Author: principalMessage(message.Author), Body: message.Body,
		CreatedAt: timestamppb.New(message.CreatedAt),
	}
	if message.Target.Kind == store.MessageTargetThread {
		result.ThreadRootMessageId = message.Target.ID
	}
	return result
}

func receiptMessage(receipt store.MutationReceipt) *spacev1.MutationReceipt {
	return &spacev1.MutationReceipt{RequestId: receipt.RequestID, CommittedAt: timestamppb.New(receipt.CommittedAt)}
}
