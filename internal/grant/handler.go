package grant

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	"github.com/abcdlsj/sumi/internal/authority"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
	"github.com/abcdlsj/sumi/internal/servicesvc"
	"github.com/abcdlsj/sumi/internal/transport/id"
)

func (s *Service) IssueGrant(ctx context.Context, req *connect.Request[grantv1.IssueGrantRequest]) (*connect.Response[grantv1.IssueGrantResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := id.CanonicalID(req.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	subject, err := parsePrincipal(req.Msg.GetSubject(), actor.OrganizationID, false)
	if err != nil {
		return nil, err
	}
	capability, ok := capName(req.Msg.GetCapability())
	if !ok {
		return nil, servicesvc.InvalArg("grant capability is invalid")
	}
	scope, err := parseScope(req.Msg.GetScope(), actor.OrganizationID)
	if err != nil {
		return nil, err
	}
	parentID, err := id.CanonicalID(req.Msg.GetParentGrantId(), "parent grant id")
	if err != nil {
		return nil, err
	}
	var expiresAt *time.Time
	if req.Msg.GetExpiresAt() != nil {
		if err := req.Msg.GetExpiresAt().CheckValid(); err != nil {
			return nil, servicesvc.InvalArg("grant expiry is invalid")
		}
		v := req.Msg.GetExpiresAt().AsTime()
		expiresAt = &v
	}
	issued, err := s.store.IssueGrant(ctx, grantapp.IssueCommand{
		RequestID: requestID, Actor: actor, Subject: subject, Capability: capability,
		Scope: scope, ParentGrantID: parentID, ExpiresAt: expiresAt, Now: s.now(),
	})
	if err := issueErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&grantv1.IssueGrantResponse{Grant: grantToProto(issued)}), nil
}

func (s *Service) RevokeGrant(ctx context.Context, req *connect.Request[grantv1.RevokeGrantRequest]) (*connect.Response[grantv1.RevokeGrantResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := id.CanonicalID(req.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	grantID, err := id.CanonicalID(req.Msg.GetGrantId(), "grant id")
	if err != nil {
		return nil, err
	}
	revoked, err := s.store.RevokeGrant(ctx, grantapp.RevokeCommand{
		RequestID: requestID, Actor: actor, GrantID: grantID, Now: s.now(),
	})
	if err := revokeErr(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&grantv1.RevokeGrantResponse{Grant: grantToProto(revoked)}), nil
}

func (s *Service) GetGrant(ctx context.Context, req *connect.Request[grantv1.GetGrantRequest]) (*connect.Response[grantv1.GetGrantResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireAdmin(ctx, actor); err != nil {
		return nil, err
	}
	id, err := id.CanonicalID(req.Msg.GetGrantId(), "grant id")
	if err != nil {
		return nil, err
	}
	v, err := s.store.GetGrant(ctx, grantapp.GetQuery{GrantID: id})
	switch {
	case errors.Is(err, grantapp.ErrNotFound):
		return nil, connect.NewError(connect.CodeNotFound, errors.New("grant not found"))
	case err != nil:
		return nil, servicesvc.ErrInternal
	}
	if v.OrganizationID != actor.OrganizationID {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("grant access denied"))
	}
	return connect.NewResponse(&grantv1.GetGrantResponse{Grant: grantToProto(v)}), nil
}

func (s *Service) ListGrants(ctx context.Context, _ *connect.Request[grantv1.ListGrantsRequest]) (*connect.Response[grantv1.ListGrantsResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireAdmin(ctx, actor); err != nil {
		return nil, err
	}
	values, err := s.store.ListGrants(ctx, grantapp.ListQuery{OrganizationID: actor.OrganizationID})
	if err != nil {
		return nil, servicesvc.ErrInternal
	}
	resp := &grantv1.ListGrantsResponse{Grants: make([]*grantv1.Grant, 0, len(values))}
	for _, v := range values {
		resp.Grants = append(resp.Grants, grantToProto(v))
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) CheckPermission(ctx context.Context, req *connect.Request[grantv1.CheckPermissionRequest]) (*connect.Response[grantv1.CheckPermissionResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireAdmin(ctx, actor); err != nil {
		return nil, err
	}
	subject, err := parsePrincipal(req.Msg.GetSubject(), actor.OrganizationID, false)
	if err != nil {
		return nil, err
	}
	capability, ok := capName(req.Msg.GetCapability())
	if !ok {
		return nil, servicesvc.InvalArg("permission capability is invalid")
	}
	scope, err := parseScope(req.Msg.GetScope(), actor.OrganizationID)
	if err != nil {
		return nil, err
	}
	allowed, err := s.store.CheckPermission(ctx, grantapp.PermissionQuery{
		Subject: subject, Capability: capability, Scope: scope, Now: s.now(),
	})
	if err != nil {
		return nil, servicesvc.ErrInternal
	}
	return connect.NewResponse(&grantv1.CheckPermissionResponse{Allowed: allowed}), nil
}

func (s *Service) requireAdmin(ctx context.Context, actor authoritydomain.Principal) error {
	allowed, err := s.store.CheckPermission(ctx, grantapp.PermissionQuery{
		Subject: actor, Capability: authoritydomain.CapabilityOrganizationAdmin,
		Scope: authoritydomain.Scope{Kind: authoritydomain.ScopeOrganization, ID: actor.OrganizationID},
		Now: s.now(),
	})
	if err != nil {
		return servicesvc.ErrInternal
	}
	if !allowed {
		return connect.NewError(connect.CodePermissionDenied, errors.New("organization authority required"))
	}
	return nil
}
