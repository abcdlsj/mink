package collaboration

import (
	"context"
	"math"
	"net/http"

	"connectrpc.com/connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/internal/authority"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	"github.com/abcdlsj/sumi/internal/servicesvc"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
)

// ── Space ────────────────────────────────────────────────────

func (s *Service) CreateDM(ctx context.Context, req *connect.Request[spacev1.CreateDMRequest]) (*connect.Response[spacev1.CreateDMResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectid.CanonicalID(req.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	peer, err := parsePrincipal(req.Msg.GetPeer(), actor.OrganizationID)
	if err != nil {
		return nil, err
	}
	space, err := s.createDM(ctx, CreateDMCommand{
		RequestID: requestID, Actor: actor, Peer: peer, Now: s.now(),
	})
	if err := collabErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.CreateDMResponse{Space: spaceToProto(space)}), nil
}

func (s *Service) CreateGroup(ctx context.Context, req *connect.Request[spacev1.CreateGroupRequest]) (*connect.Response[spacev1.CreateGroupResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectid.CanonicalID(req.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	if err := validateGroupName(req.Msg.GetName()); err != nil {
		return nil, err
	}
	space, err := s.createGroup(ctx, CreateGroupCommand{
		RequestID: requestID, Actor: actor, Name: req.Msg.GetName(), Now: s.now(),
	})
	if err := collabErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.CreateGroupResponse{Space: spaceToProto(space)}), nil
}

func (s *Service) GetSpace(ctx context.Context, req *connect.Request[spacev1.GetSpaceRequest]) (*connect.Response[spacev1.GetSpaceResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	spaceID, err := connectid.CanonicalID(req.Msg.GetSpaceId(), "space id")
	if err != nil {
		return nil, err
	}
	space, err := s.store.GetSpace(ctx, collaborationapp.SpaceReadQuery{
		Actor: actor, SpaceID: spaceID, Now: s.now(),
	})
	if err := collabErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.GetSpaceResponse{Space: spaceToProto(space)}), nil
}

func (s *Service) ListSpaces(ctx context.Context, _ *connect.Request[spacev1.ListSpacesRequest]) (*connect.Response[spacev1.ListSpacesResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	spaces, err := s.store.ListSpaces(ctx, collaborationapp.ListSpacesQuery{
		Actor: actor, Now: s.now(),
	})
	if err := collabErr(err); err != nil {
		return nil, err
	}
	resp := &spacev1.ListSpacesResponse{Spaces: make([]*spacev1.Space, 0, len(spaces))}
	for _, sp := range spaces {
		resp.Spaces = append(resp.Spaces, spaceToProto(sp))
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) AddMember(ctx context.Context, req *connect.Request[spacev1.AddMemberRequest]) (*connect.Response[spacev1.AddMemberResponse], error) {
	params, err := s.buildMemberParams(ctx, req.Msg.GetRequestId(), req.Msg.GetSpaceId(), req.Msg.GetMember())
	if err != nil {
		return nil, err
	}
	receipt, err := s.addMember(ctx, params)
	if err := collabErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.AddMemberResponse{Receipt: receiptToProto(receipt)}), nil
}

func (s *Service) RemoveMember(ctx context.Context, req *connect.Request[spacev1.RemoveMemberRequest]) (*connect.Response[spacev1.RemoveMemberResponse], error) {
	params, err := s.buildMemberParams(ctx, req.Msg.GetRequestId(), req.Msg.GetSpaceId(), req.Msg.GetMember())
	if err != nil {
		return nil, err
	}
	receipt, err := s.removeMember(ctx, params)
	if err := collabErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.RemoveMemberResponse{Receipt: receiptToProto(receipt)}), nil
}

func (s *Service) ListMembers(ctx context.Context, req *connect.Request[spacev1.ListMembersRequest]) (*connect.Response[spacev1.ListMembersResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	spaceID, err := connectid.CanonicalID(req.Msg.GetSpaceId(), "space id")
	if err != nil {
		return nil, err
	}
	memberships, err := s.store.ListMembers(ctx, collaborationapp.SpaceReadQuery{
		Actor: actor, SpaceID: spaceID, Now: s.now(),
	})
	if err := collabErr(err); err != nil {
		return nil, err
	}
	resp := &spacev1.ListMembersResponse{Memberships: make([]*spacev1.Membership, 0, len(memberships))}
	for _, m := range memberships {
		resp.Memberships = append(resp.Memberships, membershipToProto(m))
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) ArchiveSpace(ctx context.Context, req *connect.Request[spacev1.ArchiveSpaceRequest]) (*connect.Response[spacev1.ArchiveSpaceResponse], error) {
	params, err := s.buildArchiveParams(ctx, req.Msg.GetRequestId(), req.Msg.GetSpaceId())
	if err != nil {
		return nil, err
	}
	receipt, err := s.archiveSpace(ctx, params)
	if err := collabErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.ArchiveSpaceResponse{Receipt: receiptToProto(receipt)}), nil
}

func (s *Service) UnarchiveSpace(ctx context.Context, req *connect.Request[spacev1.UnarchiveSpaceRequest]) (*connect.Response[spacev1.UnarchiveSpaceResponse], error) {
	params, err := s.buildArchiveParams(ctx, req.Msg.GetRequestId(), req.Msg.GetSpaceId())
	if err != nil {
		return nil, err
	}
	receipt, err := s.unarchiveSpace(ctx, params)
	if err := collabErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.UnarchiveSpaceResponse{Receipt: receiptToProto(receipt)}), nil
}

// ── Messages ─────────────────────────────────────────────────

func (s *Service) SendMessage(ctx context.Context, req *connect.Request[spacev1.SendMessageRequest]) (*connect.Response[spacev1.SendMessageResponse], error) {
	auth, err := s.authenticateMessage(ctx, http.Header(req.Header()), true)
	if err != nil {
		return nil, err
	}
	actor := auth.actor
	requestID, err := connectid.CanonicalID(req.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	target, err := parseTarget(req.Msg.GetTarget())
	if err != nil {
		return nil, err
	}
	if err := validateBody(req.Msg.GetBody()); err != nil {
		return nil, err
	}
	mentions, err := parsePrincipalList(req.Msg.GetMentionedPrincipals(), actor.OrganizationID)
	if err != nil {
		return nil, err
	}
	run, err := buildMsgRunProof(req.Msg.GetRunProof())
	if err != nil {
		return nil, err
	}
	msg, err := s.sendMessage(ctx, SendMessageCommand{
		RequestID: requestID, Actor: actor, Runtime: auth.runtime, Run: run,
		Target: target, Body: req.Msg.GetBody(),
		MentionedPrincipals: mentions, Now: s.now(),
	})
	if err := collabErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.SendMessageResponse{Message: msgToProto(msg)}), nil
}

func (s *Service) GetMessage(ctx context.Context, req *connect.Request[spacev1.GetMessageRequest]) (*connect.Response[spacev1.GetMessageResponse], error) {
	auth, err := s.authenticateMessage(ctx, http.Header(req.Header()), false)
	if err != nil {
		return nil, err
	}
	messageID, err := connectid.CanonicalID(req.Msg.GetMessageId(), "message id")
	if err != nil {
		return nil, err
	}
	msg, err := s.store.GetMessage(ctx, collaborationapp.GetMessageQuery{
		Actor: auth.actor, Runtime: auth.runtime, MessageID: messageID, Now: s.now(),
	})
	if err := collabErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.GetMessageResponse{Message: msgToProto(msg)}), nil
}

func (s *Service) GetThread(ctx context.Context, req *connect.Request[spacev1.GetThreadRequest]) (*connect.Response[spacev1.GetThreadResponse], error) {
	auth, err := s.authenticateMessage(ctx, http.Header(req.Header()), false)
	if err != nil {
		return nil, err
	}
	threadID, err := connectid.CanonicalID(req.Msg.GetThreadRootMessageId(), "thread root message id")
	if err != nil {
		return nil, err
	}
	thread, err := s.store.GetThread(ctx, collaborationapp.GetThreadQuery{
		Actor: auth.actor, Runtime: auth.runtime, ThreadID: threadID, Now: s.now(),
	})
	if err := collabErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&spacev1.GetThreadResponse{Thread: threadToProto(thread)}), nil
}

func (s *Service) ListMessages(ctx context.Context, req *connect.Request[spacev1.ListMessagesRequest]) (*connect.Response[spacev1.ListMessagesResponse], error) {
	auth, err := s.authenticateMessage(ctx, http.Header(req.Header()), false)
	if err != nil {
		return nil, err
	}
	target, err := parseTarget(req.Msg.GetTarget())
	if err != nil {
		return nil, err
	}
	if req.Msg.GetAfterSequence() > math.MaxInt64 {
		return nil, servicesvc.InvalArg("after sequence is too large")
	}
	limit := req.Msg.GetLimit()
	if limit == 0 {
		limit = defaultMessageLimit
	}
	if limit > maxMessageLimit {
		return nil, servicesvc.InvalArg("message limit must be at most 200")
	}
	messages, err := s.store.ListMessages(ctx, collaborationapp.ListMessagesQuery{
		Actor: auth.actor, Runtime: auth.runtime, Target: target,
		AfterSequence: req.Msg.GetAfterSequence(), Limit: limit, Now: s.now(),
	})
	if err := collabErr(err); err != nil {
		return nil, err
	}
	resp := &spacev1.ListMessagesResponse{Messages: make([]*spacev1.Message, 0, len(messages))}
	for _, m := range messages {
		resp.Messages = append(resp.Messages, msgToProto(m))
	}
	return connect.NewResponse(resp), nil
}

func buildMsgRunProof(p *grantv1.RunProof) (*authorityapp.RunProof, error) {
	if p == nil {
		return nil, nil
	}
	id, err := connectid.CanonicalID(p.GetRunId(), "run id")
	if err != nil || p.GetAttempt() == 0 || p.GetFence() == 0 {
		return nil, servicesvc.InvalArg("run proof is invalid")
	}
	return &authorityapp.RunProof{RunID: id, Attempt: p.GetAttempt(), Fence: p.GetFence()}, nil
}
