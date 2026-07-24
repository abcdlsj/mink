package store

import (
	"context"
	"database/sql"
	"fmt"

	auditapp "github.com/abcdlsj/sumi/internal/audit/application"
	"github.com/google/uuid"
)

const (
	AuditOrganizationBootstrap = auditapp.ActionOrganizationBootstrap
	AuditHumanCreate           = auditapp.ActionHumanCreate
	AuditHumanStatusSet        = auditapp.ActionHumanStatusSet
	AuditGrantIssue            = auditapp.ActionGrantIssue
	AuditGrantRevoke           = auditapp.ActionGrantRevoke
	AuditAgentCreate           = auditapp.ActionAgentCreate
	AuditAgentProfileUpdate    = auditapp.ActionAgentProfileUpdate
	AuditAgentRuntimeConfigure = auditapp.ActionAgentRuntimeConfigure
	AuditAgentPlace            = auditapp.ActionAgentPlace
	AuditSpaceCreate           = auditapp.ActionSpaceCreate
	AuditSpaceMemberAdd        = auditapp.ActionSpaceMemberAdd
	AuditSpaceMemberRemove     = auditapp.ActionSpaceMemberRemove
	AuditSpaceArchive          = auditapp.ActionSpaceArchive
	AuditSpaceUnarchive        = auditapp.ActionSpaceUnarchive
	AuditThreadCreate          = auditapp.ActionThreadCreate
	AuditMessageSend           = auditapp.ActionMessageSend
	AuditRunClaim              = auditapp.ActionRunClaim
	AuditRunRenew              = auditapp.ActionRunRenew
	AuditRunCancel             = auditapp.ActionRunCancel
	AuditRunComplete           = auditapp.ActionRunComplete
	AuditComputerPairPrepare   = auditapp.ActionComputerPairPrepare
	AuditComputerPair          = auditapp.ActionComputerPair
	AuditWorkCreate            = auditapp.ActionWorkCreate
	AuditWorkAssign            = auditapp.ActionWorkAssign
	AuditWorkTransition        = auditapp.ActionWorkTransition
	AuditWorkApprovalRequest   = auditapp.ActionWorkApprovalRequest
	AuditWorkApprovalResolve   = auditapp.ActionWorkApprovalResolve
	AuditAuthIdentityBind      = auditapp.ActionAuthIdentityBind
)

type AuditEvent = auditapp.Event

type AppendAuditParams = auditapp.AppendCommand

type ListAuditEventsParams = auditapp.ListQuery

func appendAuditEvent(ctx context.Context, tx *sql.Tx, params AppendAuditParams) error {
	if err := validateAuditContext(params.ContextKind, params.ContextID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events(
			id, organization_id, actor_kind, actor_id, action, target_kind,
			target_id, request_id, outcome, reason_code, occurred_at, context_kind, context_id
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, uuid.NewString(), params.OrganizationID, params.Actor.Kind, params.Actor.ID, params.Action,
		params.TargetKind, params.TargetID, params.RequestID, params.Outcome, params.ReasonCode,
		unixNano(params.Now), params.ContextKind, params.ContextID); err != nil {
		return fmt.Errorf("append audit event: %w", err)
	}
	return nil
}

func validateAuditContext(kind, id string) error {
	if kind == "" && id == "" {
		return nil
	}
	if (kind != "space" && kind != "thread" && kind != "computer") || id == "" {
		return fmt.Errorf("invalid audit context")
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return fmt.Errorf("invalid audit context id")
	}
	return nil
}

func (s *Store) ListAuditEvents(ctx context.Context, params ListAuditEventsParams) ([]AuditEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sequence, id, organization_id, actor_kind, actor_id, action,
		       target_kind, target_id, request_id, outcome, reason_code, occurred_at,
		       context_kind, context_id
		FROM audit_events
		WHERE organization_id = ? AND sequence > ?
		ORDER BY sequence
		LIMIT ?
	`, params.OrganizationID, params.AfterSequence, params.Limit)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	var events []AuditEvent
	for rows.Next() {
		var event AuditEvent
		var occurredAt int64
		if err := rows.Scan(&event.Sequence, &event.ID, &event.OrganizationID, &event.Actor.Kind,
			&event.Actor.ID, &event.Action, &event.TargetKind, &event.TargetID, &event.RequestID,
			&event.Outcome, &event.ReasonCode, &occurredAt, &event.ContextKind, &event.ContextID); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		event.Actor.OrganizationID = event.OrganizationID
		event.OccurredAt = timeFromUnixNano(occurredAt)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	return events, nil
}
