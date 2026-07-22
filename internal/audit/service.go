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
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Service struct {
	store auditStore
	now   func() time.Time
}

func New(database auditStore) *Service {
	return &Service{store: database, now: time.Now}
}

func (s *Service) ListAuditEvents(ctx context.Context, request *connect.Request[auditv1.ListAuditEventsRequest]) (*connect.Response[auditv1.ListAuditEventsResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	allowed, err := s.store.CheckPermission(ctx, grantapp.PermissionQuery{
		Subject: actor, Capability: authoritydomain.CapabilityAuditRead,
		Scope: authoritydomain.Scope{Kind: authoritydomain.ScopeOrganization, ID: actor.OrganizationID}, Now: s.now(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if !allowed {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("audit access denied"))
	}
	limit := request.Msg.GetLimit()
	if limit == 0 {
		limit = 100
	}
	if limit > 500 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("audit limit must not exceed 500"))
	}
	events, err := s.store.ListAuditEvents(ctx, auditapp.ListQuery{
		OrganizationID: actor.OrganizationID, AfterSequence: request.Msg.GetAfterSequence(), Limit: limit,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	response := &auditv1.ListAuditEventsResponse{Events: make([]*auditv1.AuditEvent, 0, len(events))}
	for _, event := range events {
		response.Events = append(response.Events, eventMessage(event))
	}
	return connect.NewResponse(response), nil
}

func eventMessage(event auditapp.Event) *auditv1.AuditEvent {
	return &auditv1.AuditEvent{
		Sequence: event.Sequence, Id: event.ID, OrganizationId: event.OrganizationID,
		Actor: principalMessage(event.Actor), Action: actionValue(event.Action), TargetKind: targetValue(event.TargetKind),
		TargetId: event.TargetID, RequestId: event.RequestID, Outcome: outcomeValue(event.Outcome), ReasonCode: event.ReasonCode,
		OccurredAt: timestamppb.New(event.OccurredAt), ContextKind: contextValue(event.ContextKind), ContextId: event.ContextID,
	}
}

func contextValue(value string) auditv1.AuditContextKind {
	if value == "space" {
		return auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_SPACE
	}
	if value == "thread" {
		return auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_THREAD
	}
	if value == "computer" {
		return auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_COMPUTER
	}
	return auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_UNSPECIFIED
}

func principalMessage(value authoritydomain.Principal) *grantv1.Principal {
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

func actionValue(value auditapp.Action) auditv1.AuditAction {
	values := map[string]auditv1.AuditAction{
		auditapp.ActionOrganizationBootstrap: auditv1.AuditAction_AUDIT_ACTION_ORGANIZATION_BOOTSTRAP,
		auditapp.ActionHumanCreate:           auditv1.AuditAction_AUDIT_ACTION_HUMAN_CREATE,
		auditapp.ActionHumanStatusSet:        auditv1.AuditAction_AUDIT_ACTION_HUMAN_STATUS_SET,
		auditapp.ActionGrantIssue:            auditv1.AuditAction_AUDIT_ACTION_GRANT_ISSUE,
		auditapp.ActionGrantRevoke:           auditv1.AuditAction_AUDIT_ACTION_GRANT_REVOKE,
		auditapp.ActionAgentCreate:           auditv1.AuditAction_AUDIT_ACTION_AGENT_CREATE,
		auditapp.ActionAgentPlace:            auditv1.AuditAction_AUDIT_ACTION_AGENT_PLACE,
		auditapp.ActionSpaceCreate:           auditv1.AuditAction_AUDIT_ACTION_SPACE_CREATE,
		auditapp.ActionSpaceMemberAdd:        auditv1.AuditAction_AUDIT_ACTION_SPACE_MEMBER_ADD,
		auditapp.ActionSpaceMemberRemove:     auditv1.AuditAction_AUDIT_ACTION_SPACE_MEMBER_REMOVE,
		auditapp.ActionSpaceArchive:          auditv1.AuditAction_AUDIT_ACTION_SPACE_ARCHIVE,
		auditapp.ActionSpaceUnarchive:        auditv1.AuditAction_AUDIT_ACTION_SPACE_UNARCHIVE,
		auditapp.ActionThreadCreate:          auditv1.AuditAction_AUDIT_ACTION_THREAD_CREATE,
		auditapp.ActionMessageSend:           auditv1.AuditAction_AUDIT_ACTION_MESSAGE_SEND,
		auditapp.ActionComputerPairPrepare:   auditv1.AuditAction_AUDIT_ACTION_COMPUTER_PAIR_PREPARE,
		auditapp.ActionComputerPair:          auditv1.AuditAction_AUDIT_ACTION_COMPUTER_PAIR,
	}
	return values[value]
}

func targetValue(value string) auditv1.AuditTargetKind {
	values := map[string]auditv1.AuditTargetKind{
		"organization":     auditv1.AuditTargetKind_AUDIT_TARGET_KIND_ORGANIZATION,
		"human":            auditv1.AuditTargetKind_AUDIT_TARGET_KIND_HUMAN,
		"agent":            auditv1.AuditTargetKind_AUDIT_TARGET_KIND_AGENT,
		"grant":            auditv1.AuditTargetKind_AUDIT_TARGET_KIND_GRANT,
		"space":            auditv1.AuditTargetKind_AUDIT_TARGET_KIND_SPACE,
		"thread":           auditv1.AuditTargetKind_AUDIT_TARGET_KIND_THREAD,
		"message":          auditv1.AuditTargetKind_AUDIT_TARGET_KIND_MESSAGE,
		"computer_pairing": auditv1.AuditTargetKind_AUDIT_TARGET_KIND_COMPUTER_PAIRING,
		"computer":         auditv1.AuditTargetKind_AUDIT_TARGET_KIND_COMPUTER,
	}
	return values[value]
}

func outcomeValue(value string) auditv1.AuditOutcome {
	if value == "committed" {
		return auditv1.AuditOutcome_AUDIT_OUTCOME_COMMITTED
	}
	if value == "denied" {
		return auditv1.AuditOutcome_AUDIT_OUTCOME_DENIED
	}
	return auditv1.AuditOutcome_AUDIT_OUTCOME_UNSPECIFIED
}
