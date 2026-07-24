package collaboration

import (
	"context"
	"errors"
	"math"
	"net/http"

	"connectrpc.com/connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/internal/authority"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
)

func (s *Service) CreateDM(ctx context.Context, request *connect.Request[spacev1.CreateDMRequest]) (*connect.Response[spacev1.CreateDMResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectid.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	peer, err := principalParams(request.Msg.GetPeer(), actor.OrganizationID)
	if err != nil {
		return nil, err
	}
	space, err := s.createDM(ctx, CreateDMCommand{
		RequestID: requestID,
		Actor:     actor,
		Peer:      peer,
		Now:       s.now(),
	})
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
	requestID, err := connectid.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	if err := groupNameValid(request.Msg.GetName()); err != nil {
		return nil, err
	}
	space, err := s.createGroup(ctx, CreateGroupCommand{
		RequestID: requestID,
		Actor:     actor,
		Name:      request.Msg.GetName(),
		Now:       s.now(),
	})
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
	spaceID, err := connectid.CanonicalID(request.Msg.GetSpaceId(), "space id")
	if err != nil {
		return nil, err
	}
	space, err := s.store.GetSpace(ctx, collaborationapp.SpaceReadQuery{Actor: actor, SpaceID: spaceID, Now: s.now()})
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
	spaces, err := s.store.ListSpaces(ctx, collaborationapp.ListSpacesQuery{Actor: actor, Now: s.now()})
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
	receipt, err := s.addMember(ctx, params)
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.AddMemberResponse{Receipt: receiptSnapshotMessage(receipt)}), nil
}

func (s *Service) RemoveMember(ctx context.Context, request *connect.Request[spacev1.RemoveMemberRequest]) (*connect.Response[spacev1.RemoveMemberResponse], error) {
	params, err := s.memberParams(ctx, request.Msg.GetRequestId(), request.Msg.GetSpaceId(), request.Msg.GetMember())
	if err != nil {
		return nil, err
	}
	receipt, err := s.removeMember(ctx, params)
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.RemoveMemberResponse{Receipt: receiptSnapshotMessage(receipt)}), nil
}

func (s *Service) ListMembers(ctx context.Context, request *connect.Request[spacev1.ListMembersRequest]) (*connect.Response[spacev1.ListMembersResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	spaceID, err := connectid.CanonicalID(request.Msg.GetSpaceId(), "space id")
	if err != nil {
		return nil, err
	}
	memberships, err := s.store.ListMembers(ctx, collaborationapp.SpaceReadQuery{Actor: actor, SpaceID: spaceID, Now: s.now()})
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
	receipt, err := s.archiveSpace(ctx, params)
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.ArchiveSpaceResponse{Receipt: receiptSnapshotMessage(receipt)}), nil
}

func (s *Service) UnarchiveSpace(ctx context.Context, request *connect.Request[spacev1.UnarchiveSpaceRequest]) (*connect.Response[spacev1.UnarchiveSpaceResponse], error) {
	params, err := s.archiveParams(ctx, request.Msg.GetRequestId(), request.Msg.GetSpaceId())
	if err != nil {
		return nil, err
	}
	receipt, err := s.unarchiveSpace(ctx, params)
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.UnarchiveSpaceResponse{Receipt: receiptSnapshotMessage(receipt)}), nil
}

func (s *Service) SendMessage(ctx context.Context, request *connect.Request[spacev1.SendMessageRequest]) (*connect.Response[spacev1.SendMessageResponse], error) {
	authentication, err := s.authenticateMessage(ctx, http.Header(request.Header()), true)
	if err != nil {
		return nil, err
	}
	actor := authentication.actor
	requestID, err := connectid.CanonicalID(request.Msg.GetRequestId(), "request id")
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
	mentions, err := principalListParams(request.Msg.GetMentionedPrincipals(), actor.OrganizationID)
	if err != nil {
		return nil, err
	}
	run, err := messageRunProof(request.Msg.GetRunId(), request.Msg.GetRunAttempt(), request.Msg.GetRunFence())
	if err != nil {
		return nil, err
	}
	message, err := s.sendMessage(ctx, SendMessageCommand{
		RequestID: requestID,
		Actor:     actor, Runtime: authentication.runtime, Run: run,
		Target: target, Body: request.Msg.GetBody(), MentionedPrincipals: mentions, Now: s.now(),
	})
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.SendMessageResponse{Message: messageMessage(message)}), nil
}

func messageRunProof(runID string, attempt, fence uint64) (*authorityapp.RunProof, error) {
	if runID == "" && attempt == 0 && fence == 0 {
		return nil, nil
	}
	id, err := connectid.CanonicalID(runID, "run id")
	if err != nil || attempt == 0 || fence == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("run proof is invalid"))
	}
	return &authorityapp.RunProof{RunID: id, Attempt: attempt, Fence: fence}, nil
}

func (s *Service) GetMessage(ctx context.Context, request *connect.Request[spacev1.GetMessageRequest]) (*connect.Response[spacev1.GetMessageResponse], error) {
	authentication, err := s.authenticateMessage(ctx, http.Header(request.Header()), false)
	if err != nil {
		return nil, err
	}
	messageID, err := connectid.CanonicalID(request.Msg.GetMessageId(), "message id")
	if err != nil {
		return nil, err
	}
	message, err := s.store.GetMessage(ctx, collaborationapp.GetMessageQuery{Actor: authentication.actor, Runtime: authentication.runtime, MessageID: messageID, Now: s.now()})
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.GetMessageResponse{Message: messageMessage(message)}), nil
}

func (s *Service) GetThread(ctx context.Context, request *connect.Request[spacev1.GetThreadRequest]) (*connect.Response[spacev1.GetThreadResponse], error) {
	authentication, err := s.authenticateMessage(ctx, http.Header(request.Header()), false)
	if err != nil {
		return nil, err
	}
	threadID, err := connectid.CanonicalID(request.Msg.GetThreadRootMessageId(), "thread root message id")
	if err != nil {
		return nil, err
	}
	thread, err := s.store.GetThread(ctx, collaborationapp.GetThreadQuery{Actor: authentication.actor, Runtime: authentication.runtime, ThreadID: threadID, Now: s.now()})
	if err := collaborationError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.GetThreadResponse{Thread: threadMessage(thread)}), nil
}

func (s *Service) ListMessages(ctx context.Context, request *connect.Request[spacev1.ListMessagesRequest]) (*connect.Response[spacev1.ListMessagesResponse], error) {
	authentication, err := s.authenticateMessage(ctx, http.Header(request.Header()), false)
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
	messages, err := s.store.ListMessages(ctx, collaborationapp.ListMessagesQuery{
		Actor: authentication.actor, Runtime: authentication.runtime, Target: target,
		AfterSequence: request.Msg.GetAfterSequence(), Limit: limit, Now: s.now(),
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
