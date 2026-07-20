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
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Service struct {
	store *store.Store
	now   func() time.Time
}

func New(database *store.Store) *Service {
	return &Service{store: database, now: time.Now}
}

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
	issued, err := s.store.IssueGrant(ctx, store.IssueGrantParams{RequestID: requestID, Actor: actor, Subject: subject, Capability: capability, Scope: scope, ParentGrantID: parentID, ExpiresAt: expiresAt, Now: s.now()})
	if errors.Is(err, store.ErrPermissionDenied) || errors.Is(err, store.ErrParentGrantInvalid) || errors.Is(err, store.ErrGrantExpansion) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("grant issue denied"))
	}
	if errors.Is(err, store.ErrGrantRequestConflict) {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("request id already used for another grant"))
	}
	if errors.Is(err, store.ErrGrantInvalid) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("grant is invalid"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
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
	if errors.Is(err, store.ErrPermissionDenied) || errors.Is(err, store.ErrLastOwner) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("grant revoke denied"))
	}
	if errors.Is(err, store.ErrGrantNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("grant not found"))
	}
	if errors.Is(err, store.ErrGrantRevokeConflict) {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("request id already used for another revoke"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
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
	allowed, err := s.store.CheckPermission(ctx, subject, capability, scope, s.now())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&grantv1.CheckPermissionResponse{Allowed: allowed}), nil
}

func (s *Service) requireAdmin(ctx context.Context, actor store.Principal) error {
	allowed, err := s.store.CheckPermission(ctx, actor, store.CapabilityOrganizationAdmin, store.Scope{Kind: "organization", ID: actor.OrganizationID}, s.now())
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if !allowed {
		return connect.NewError(connect.CodePermissionDenied, errors.New("organization authority required"))
	}
	return nil
}

func principalParams(principal *grantv1.Principal, organizationID string, allowSystem bool) (store.Principal, error) {
	if principal == nil {
		return store.Principal{}, connect.NewError(connect.CodeInvalidArgument, errors.New("principal is required"))
	}
	kind := ""
	if principal.GetKind() == grantv1.PrincipalKind_PRINCIPAL_KIND_HUMAN {
		kind = "human"
	} else if principal.GetKind() == grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT {
		kind = "agent"
	} else if allowSystem && principal.GetKind() == grantv1.PrincipalKind_PRINCIPAL_KIND_SYSTEM {
		kind = "system"
	}
	if kind == "" {
		return store.Principal{}, connect.NewError(connect.CodeInvalidArgument, errors.New("principal kind is invalid"))
	}
	id, err := connectapi.CanonicalID(principal.GetId(), "principal id")
	if err != nil {
		return store.Principal{}, err
	}
	return store.Principal{Kind: kind, ID: id, OrganizationID: organizationID}, nil
}

func scopeParams(scope *grantv1.Scope, organizationID string) (store.Scope, error) {
	if scope == nil {
		return store.Scope{}, connect.NewError(connect.CodeInvalidArgument, errors.New("scope is required"))
	}
	kind := ""
	switch scope.GetKind() {
	case grantv1.ScopeKind_SCOPE_KIND_ORGANIZATION:
		kind = "organization"
	case grantv1.ScopeKind_SCOPE_KIND_AGENT:
		kind = "agent"
	case grantv1.ScopeKind_SCOPE_KIND_COMPUTER:
		kind = "computer"
	case grantv1.ScopeKind_SCOPE_KIND_SPACE:
		kind = "space"
	}
	if kind == "" {
		return store.Scope{}, connect.NewError(connect.CodeInvalidArgument, errors.New("scope kind is invalid"))
	}
	id, err := connectapi.CanonicalID(scope.GetId(), "scope id")
	if err != nil {
		return store.Scope{}, err
	}
	if kind == "organization" && id != organizationID {
		return store.Scope{}, connect.NewError(connect.CodePermissionDenied, errors.New("cross-organization scope denied"))
	}
	return store.Scope{Kind: kind, ID: id}, nil
}

func grantMessage(value store.Grant) *grantv1.Grant {
	message := &grantv1.Grant{
		Id: value.ID, OrganizationId: value.OrganizationID, Subject: principalMessage(value.Subject), Issuer: principalMessage(value.Issuer),
		Capability: capabilityValue(value.Capability), Scope: scopeMessage(value.Scope), ParentGrantId: value.ParentGrantID,
		CreatedAt: timestamppb.New(value.CreatedAt), UpdatedAt: timestamppb.New(value.UpdatedAt),
	}
	if value.ExpiresAt != nil {
		message.ExpiresAt = timestamppb.New(*value.ExpiresAt)
	}
	if value.RevokedAt != nil {
		message.RevokedAt = timestamppb.New(*value.RevokedAt)
	}
	return message
}

func principalMessage(value store.Principal) *grantv1.Principal {
	kind := grantv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED
	if value.Kind == "system" {
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_SYSTEM
	} else if value.Kind == "human" {
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_HUMAN
	} else if value.Kind == "agent" {
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT
	}
	return &grantv1.Principal{Kind: kind, Id: value.ID}
}

func scopeMessage(value store.Scope) *grantv1.Scope {
	kind := grantv1.ScopeKind_SCOPE_KIND_UNSPECIFIED
	if value.Kind == "organization" {
		kind = grantv1.ScopeKind_SCOPE_KIND_ORGANIZATION
	} else if value.Kind == "agent" {
		kind = grantv1.ScopeKind_SCOPE_KIND_AGENT
	} else if value.Kind == "computer" {
		kind = grantv1.ScopeKind_SCOPE_KIND_COMPUTER
	} else if value.Kind == "space" {
		kind = grantv1.ScopeKind_SCOPE_KIND_SPACE
	}
	return &grantv1.Scope{Kind: kind, Id: value.ID}
}

func capabilityName(value grantv1.Capability) (string, bool) {
	names := map[grantv1.Capability]string{
		grantv1.Capability_CAPABILITY_ORGANIZATION_ADMIN:   store.CapabilityOrganizationAdmin,
		grantv1.Capability_CAPABILITY_HUMAN_CREATE:         store.CapabilityHumanCreate,
		grantv1.Capability_CAPABILITY_GRANT_ISSUE:          store.CapabilityGrantIssue,
		grantv1.Capability_CAPABILITY_GRANT_REVOKE:         store.CapabilityGrantRevoke,
		grantv1.Capability_CAPABILITY_AUDIT_READ:           store.CapabilityAuditRead,
		grantv1.Capability_CAPABILITY_AGENT_CREATE:         store.CapabilityAgentCreate,
		grantv1.Capability_CAPABILITY_AGENT_PLACE:          store.CapabilityAgentPlace,
		grantv1.Capability_CAPABILITY_SPACE_CREATE:         store.CapabilitySpaceCreate,
		grantv1.Capability_CAPABILITY_SPACE_READ:           store.CapabilitySpaceRead,
		grantv1.Capability_CAPABILITY_SPACE_MEMBERS_MANAGE: store.CapabilitySpaceMembers,
		grantv1.Capability_CAPABILITY_SPACE_ARCHIVE:        store.CapabilitySpaceArchive,
		grantv1.Capability_CAPABILITY_MESSAGE_SEND:         store.CapabilityMessageSend,
	}
	name, ok := names[value]
	return name, ok
}

func capabilityValue(value string) grantv1.Capability {
	for enum, name := range map[grantv1.Capability]string{
		grantv1.Capability_CAPABILITY_ORGANIZATION_ADMIN:   store.CapabilityOrganizationAdmin,
		grantv1.Capability_CAPABILITY_HUMAN_CREATE:         store.CapabilityHumanCreate,
		grantv1.Capability_CAPABILITY_GRANT_ISSUE:          store.CapabilityGrantIssue,
		grantv1.Capability_CAPABILITY_GRANT_REVOKE:         store.CapabilityGrantRevoke,
		grantv1.Capability_CAPABILITY_AUDIT_READ:           store.CapabilityAuditRead,
		grantv1.Capability_CAPABILITY_AGENT_CREATE:         store.CapabilityAgentCreate,
		grantv1.Capability_CAPABILITY_AGENT_PLACE:          store.CapabilityAgentPlace,
		grantv1.Capability_CAPABILITY_SPACE_CREATE:         store.CapabilitySpaceCreate,
		grantv1.Capability_CAPABILITY_SPACE_READ:           store.CapabilitySpaceRead,
		grantv1.Capability_CAPABILITY_SPACE_MEMBERS_MANAGE: store.CapabilitySpaceMembers,
		grantv1.Capability_CAPABILITY_SPACE_ARCHIVE:        store.CapabilitySpaceArchive,
		grantv1.Capability_CAPABILITY_MESSAGE_SEND:         store.CapabilityMessageSend,
	} {
		if name == value {
			return enum
		}
	}
	return grantv1.Capability_CAPABILITY_UNSPECIFIED
}
