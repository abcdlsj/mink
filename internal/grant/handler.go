package grant

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/connectapi"
	"github.com/abcdlsj/sumi/internal/store"
)

func (s *Service) IssueGrant(ctx context.Context, request *connect.Request[grantv1.IssueGrantRequest]) (*connect.Response[grantv1.IssueGrantResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectapi.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	subject, err := principalParams(request.Msg.GetSubject(), actor.OrganizationID, false)
	if err != nil {
		return nil, err
	}
	capability, ok := capabilityName(request.Msg.GetCapability())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("grant capability is invalid"))
	}
	scope, err := scopeParams(request.Msg.GetScope(), actor.OrganizationID)
	if err != nil {
		return nil, err
	}
	parentID, err := connectapi.CanonicalID(request.Msg.GetParentGrantId(), "parent grant id")
	if err != nil {
		return nil, err
	}
	var expiresAt *time.Time
	if request.Msg.GetExpiresAt() != nil {
		if err := request.Msg.GetExpiresAt().CheckValid(); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("grant expiry is invalid"))
		}
		value := request.Msg.GetExpiresAt().AsTime()
		expiresAt = &value
	}
	issued, err := s.store.IssueGrant(ctx, store.IssueGrantParams{
		RequestID: requestID, Actor: actor, Subject: subject, Capability: capability, Scope: scope,
		ParentGrantID: parentID, ExpiresAt: expiresAt, Now: s.now(),
	})
	if err := issueError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&grantv1.IssueGrantResponse{Grant: grantMessage(issued)}), nil
}

func (s *Service) RevokeGrant(ctx context.Context, request *connect.Request[grantv1.RevokeGrantRequest]) (*connect.Response[grantv1.RevokeGrantResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectapi.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	grantID, err := connectapi.CanonicalID(request.Msg.GetGrantId(), "grant id")
	if err != nil {
		return nil, err
	}
	revoked, err := s.store.RevokeGrant(ctx, store.RevokeGrantParams{RequestID: requestID, Actor: actor, GrantID: grantID, Now: s.now()})
	if err := revokeError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&grantv1.RevokeGrantResponse{Grant: grantMessage(revoked)}), nil
}

func (s *Service) GetGrant(ctx context.Context, request *connect.Request[grantv1.GetGrantRequest]) (*connect.Response[grantv1.GetGrantResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireAdmin(ctx, actor); err != nil {
		return nil, err
	}
	id, err := connectapi.CanonicalID(request.Msg.GetGrantId(), "grant id")
	if err != nil {
		return nil, err
	}
	value, err := s.store.GetGrant(ctx, id)
	if errors.Is(err, store.ErrGrantNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("grant not found"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if value.OrganizationID != actor.OrganizationID {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("grant access denied"))
	}
	return connect.NewResponse(&grantv1.GetGrantResponse{Grant: grantMessage(value)}), nil
}

func (s *Service) ListGrants(ctx context.Context, _ *connect.Request[grantv1.ListGrantsRequest]) (*connect.Response[grantv1.ListGrantsResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireAdmin(ctx, actor); err != nil {
		return nil, err
	}
	values, err := s.store.ListGrants(ctx, actor.OrganizationID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	response := &grantv1.ListGrantsResponse{Grants: make([]*grantv1.Grant, 0, len(values))}
	for _, value := range values {
		response.Grants = append(response.Grants, grantMessage(value))
	}
	return connect.NewResponse(response), nil
}

func (s *Service) CheckPermission(ctx context.Context, request *connect.Request[grantv1.CheckPermissionRequest]) (*connect.Response[grantv1.CheckPermissionResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireAdmin(ctx, actor); err != nil {
		return nil, err
	}
	subject, err := principalParams(request.Msg.GetSubject(), actor.OrganizationID, false)
	if err != nil {
		return nil, err
	}
	capability, ok := capabilityName(request.Msg.GetCapability())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("permission capability is invalid"))
	}
	scope, err := scopeParams(request.Msg.GetScope(), actor.OrganizationID)
	if err != nil {
		return nil, err
	}
	allowed, err := s.store.CheckPermission(ctx, store.CheckPermissionParams{
		Subject: subject, Capability: capability, Scope: scope, Now: s.now(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&grantv1.CheckPermissionResponse{Allowed: allowed}), nil
}

func (s *Service) requireAdmin(ctx context.Context, actor store.Principal) error {
	allowed, err := s.store.CheckPermission(ctx, store.CheckPermissionParams{
		Subject: actor, Capability: store.CapabilityOrganizationAdmin,
		Scope: store.Scope{Kind: "organization", ID: actor.OrganizationID}, Now: s.now(),
	})
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if !allowed {
		return connect.NewError(connect.CodePermissionDenied, errors.New("organization authority required"))
	}
	return nil
}
