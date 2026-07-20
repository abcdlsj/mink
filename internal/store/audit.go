package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	AuditOrganizationBootstrap = "organization.bootstrap"
	AuditHumanCreate           = "human.create"
	AuditHumanStatusSet        = "human.status.set"
	AuditGrantIssue            = "grant.issue"
	AuditGrantRevoke           = "grant.revoke"
	AuditSpaceCreate           = "space.create"
	AuditSpaceMemberAdd        = "space.member.add"
	AuditSpaceMemberRemove     = "space.member.remove"
	AuditSpaceArchive          = "space.archive"
	AuditSpaceUnarchive        = "space.unarchive"
	AuditThreadCreate          = "thread.create"
	AuditMessageSend           = "message.send"
)

type AuditEvent struct {
	Sequence       uint64
	ID             string
	OrganizationID string
	Actor          Principal
	Action         string
	TargetKind     string
	TargetID       string
	ContextKind    string
	ContextID      string
	RequestID      string
	Outcome        string
	ReasonCode     string
	OccurredAt     time.Time
}

type AppendAuditParams struct {
	OrganizationID string
	Actor          Principal
	Action         string
	TargetKind     string
	TargetID       string
	ContextKind    string
	ContextID      string
	RequestID      string
	Outcome        string
	ReasonCode     string
	Now            time.Time
}

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
	if (kind != "space" && kind != "thread") || id == "" {
		return fmt.Errorf("invalid audit context")
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return fmt.Errorf("invalid audit context id")
	}
	return nil
}

func (s *Store) ListAuditEvents(ctx context.Context, organizationID string, after uint64, limit uint32) ([]AuditEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sequence, id, organization_id, actor_kind, actor_id, action,
		       target_kind, target_id, request_id, outcome, reason_code, occurred_at,
		       context_kind, context_id
		FROM audit_events
		WHERE organization_id = ? AND sequence > ?
		ORDER BY sequence
		LIMIT ?
	`, organizationID, after, limit)
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
