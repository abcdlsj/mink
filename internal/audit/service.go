package audit

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	auditv1 "github.com/abcdlsj/sumi/gen/go/sumi/audit/v1"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	auditapp "github.com/abcdlsj/sumi/internal/audit/application"
	"github.com/abcdlsj/sumi/internal/authority"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
	"github.com/abcdlsj/sumi/internal/servicesvc"
)

type Service struct {
	store auditStore
	now   func() time.Time
}

func New(db auditStore) *Service {
	return &Service{store: db, now: time.Now}
}

func (s *Service) ListAuditEvents(ctx context.Context, req *connect.Request[auditv1.ListAuditEventsRequest]) (*connect.Response[auditv1.ListAuditEventsResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	allowed, err := s.store.CheckPermission(ctx, grantapp.PermissionQuery{
		Subject: actor, Capability: authoritydomain.CapabilityAuditRead,
		Scope: authoritydomain.Scope{Kind: authoritydomain.ScopeOrganization, ID: actor.OrganizationID},
		Now: s.now(),
	})
	if err != nil {
		return nil, servicesvc.ErrInternal
	}
	if !allowed {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("audit access denied"))
	}
	limit := req.Msg.GetLimit()
	if limit == 0 {
		limit = 100
	}
	if limit > 500 {
		return nil, servicesvc.InvalArg("audit limit must not exceed 500")
	}
	events, err := s.store.ListAuditEvents(ctx, auditapp.ListQuery{
		OrganizationID: actor.OrganizationID,
		AfterSequence:  req.Msg.GetAfterSequence(),
		Limit:          limit,
	})
	if err != nil {
		return nil, servicesvc.ErrInternal
	}
	resp := &auditv1.ListAuditEventsResponse{
		Events: make([]*auditv1.AuditEvent, 0, len(events)),
	}
	for _, e := range events {
		resp.Events = append(resp.Events, eventToProto(e))
	}
	return connect.NewResponse(resp), nil
}

func eventToProto(e auditapp.Event) *auditv1.AuditEvent {
	return &auditv1.AuditEvent{
		Sequence: e.Sequence, Id: e.ID, OrganizationId: e.OrganizationID,
		Actor:       principalToProto(e.Actor),
		Action:      servicesvc.AuditActionToProto(e.Action),
		TargetKind:  servicesvc.AuditTargetToProto(e.TargetKind),
		TargetId:    e.TargetID,
		RequestId:   e.RequestID,
		Outcome:     servicesvc.AuditOutcomeToProto(e.Outcome),
		ReasonCode:  e.ReasonCode,
		OccurredAt:  servicesvc.Ts(e.OccurredAt),
		ContextKind: servicesvc.AuditCtxToProto(e.ContextKind),
		ContextId:   e.ContextID,
	}
}

func principalToProto(p authoritydomain.Principal) *grantv1.Principal {
	kind := grantv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED
	switch p.Kind {
	case authoritydomain.PrincipalSystem:
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_SYSTEM
	case authoritydomain.PrincipalHuman:
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_HUMAN
	case authoritydomain.PrincipalAgent:
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT
	}
	return &grantv1.Principal{Kind: kind, Id: p.ID}
}
