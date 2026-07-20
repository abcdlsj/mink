package audit

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	auditv1 "github.com/abcdlsj/sumi/gen/go/sumi/audit/v1"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	"github.com/abcdlsj/sumi/internal/authority"
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

func (s *Service) ListAuditEvents(ctx context.Context, request *connect.Request[auditv1.ListAuditEventsRequest]) (*connect.Response[auditv1.ListAuditEventsResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	allowed, err := s.store.CheckPermission(ctx, actor, store.CapabilityAuditRead, store.Scope{Kind: "organization", ID: actor.OrganizationID}, s.now())
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
	events, err := s.store.ListAuditEvents(ctx, actor.OrganizationID, request.Msg.GetAfterSequence(), limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	response := &auditv1.ListAuditEventsResponse{Events: make([]*auditv1.AuditEvent, 0, len(events))}
	for _, event := range events {
		response.Events = append(response.Events, eventMessage(event))
	}
	return connect.NewResponse(response), nil
}

func eventMessage(event store.AuditEvent) *auditv1.AuditEvent {
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

func actionValue(value string) auditv1.AuditAction {
	values := map[string]auditv1.AuditAction{
		store.AuditOrganizationBootstrap: auditv1.AuditAction_AUDIT_ACTION_ORGANIZATION_BOOTSTRAP,
		store.AuditHumanCreate:           auditv1.AuditAction_AUDIT_ACTION_HUMAN_CREATE,
		store.AuditHumanStatusSet:        auditv1.AuditAction_AUDIT_ACTION_HUMAN_STATUS_SET,
		store.AuditGrantIssue:            auditv1.AuditAction_AUDIT_ACTION_GRANT_ISSUE,
		store.AuditGrantRevoke:           auditv1.AuditAction_AUDIT_ACTION_GRANT_REVOKE,
		store.AuditAgentCreate:           auditv1.AuditAction_AUDIT_ACTION_AGENT_CREATE,
		store.AuditAgentPlace:            auditv1.AuditAction_AUDIT_ACTION_AGENT_PLACE,
		store.AuditSpaceCreate:           auditv1.AuditAction_AUDIT_ACTION_SPACE_CREATE,
		store.AuditSpaceMemberAdd:        auditv1.AuditAction_AUDIT_ACTION_SPACE_MEMBER_ADD,
		store.AuditSpaceMemberRemove:     auditv1.AuditAction_AUDIT_ACTION_SPACE_MEMBER_REMOVE,
		store.AuditSpaceArchive:          auditv1.AuditAction_AUDIT_ACTION_SPACE_ARCHIVE,
		store.AuditSpaceUnarchive:        auditv1.AuditAction_AUDIT_ACTION_SPACE_UNARCHIVE,
		store.AuditThreadCreate:          auditv1.AuditAction_AUDIT_ACTION_THREAD_CREATE,
		store.AuditMessageSend:           auditv1.AuditAction_AUDIT_ACTION_MESSAGE_SEND,
	}
	return values[value]
}

func targetValue(value string) auditv1.AuditTargetKind {
	values := map[string]auditv1.AuditTargetKind{
		"organization": auditv1.AuditTargetKind_AUDIT_TARGET_KIND_ORGANIZATION,
		"human":        auditv1.AuditTargetKind_AUDIT_TARGET_KIND_HUMAN,
		"agent":        auditv1.AuditTargetKind_AUDIT_TARGET_KIND_AGENT,
		"grant":        auditv1.AuditTargetKind_AUDIT_TARGET_KIND_GRANT,
		"space":        auditv1.AuditTargetKind_AUDIT_TARGET_KIND_SPACE,
		"thread":       auditv1.AuditTargetKind_AUDIT_TARGET_KIND_THREAD,
		"message":      auditv1.AuditTargetKind_AUDIT_TARGET_KIND_MESSAGE,
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
