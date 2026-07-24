package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	collaborationdomain "github.com/abcdlsj/sumi/internal/collaboration/domain"
)

const (
	SpaceKindDM    = "dm"
	SpaceKindGroup = "group"

	MessageTargetSpace  = "space"
	MessageTargetThread = "thread"

	maxMessageListLimit = 200
)

type Space = collaborationapp.Space

type Membership = collaborationapp.Membership

type Thread = collaborationapp.Thread

type MessageTarget = collaborationapp.MessageTarget

type Message = collaborationapp.Message

type MutationReceipt = collaborationapp.MutationReceipt

type collaborationReceipt struct {
	Operation   string
	ResultID    string
	CommittedAt time.Time
}

func collaborationFingerprint(value any) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode collaboration request: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func readCollaborationReceipt(ctx context.Context, tx *sql.Tx, requestID, operation string, fp [sha256.Size]byte) (collaborationReceipt, bool, error) {
	var receipt collaborationReceipt
	var sfp []byte
	var committedAt int64
	err := tx.QueryRowContext(ctx, `
		SELECT operation, result_id, payload_fingerprint, committed_at
		FROM collaboration_requests
		WHERE request_id = ?
	`, requestID).Scan(&receipt.Operation, &receipt.ResultID, &sfp, &committedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return collaborationReceipt{}, false, nil
	}
	if err != nil {
		return collaborationReceipt{}, false, fmt.Errorf("read collaboration request receipt: %w", err)
	}
	if receipt.Operation != operation || !bytes.Equal(sfp, fp[:]) {
		return collaborationReceipt{}, false, ErrCollaborationRequestConflict
	}
	receipt.CommittedAt = timeFromUnixNano(committedAt)
	return receipt, true, nil
}

func persistCollaborationReceipt(ctx context.Context, tx *sql.Tx, requestID, operation string, fp [sha256.Size]byte, resultID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO collaboration_requests(request_id, operation, payload_fingerprint, result_id, committed_at)
		VALUES(?, ?, ?, ?, ?)
	`, requestID, operation, fp[:], resultID, unixNano(now)); err != nil {
		return fmt.Errorf("persist collaboration request receipt: %w", err)
	}
	return nil
}

func commitMutationReplay(tx *sql.Tx, requestID string, receipt collaborationReceipt) (MutationReceipt, error) {
	if err := tx.Commit(); err != nil {
		return MutationReceipt{}, fmt.Errorf("commit collaboration request replay: %w", err)
	}
	return MutationReceipt{RequestID: requestID, CommittedAt: receipt.CommittedAt}, nil
}

func denyCollaboration(ctx context.Context, tx *sql.Tx, actor Principal, action, targetKind, targetID, requestID, reason string, now time.Time, result error) error {
	return denyCollaborationWithContext(ctx, tx, actor, action, targetKind, targetID, "", "", requestID, reason, now, result)
}

func denyCollaborationWithContext(ctx context.Context, tx *sql.Tx, actor Principal, action, targetKind, targetID, contextKind, contextID, requestID, reason string, now time.Time, result error) error {
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: actor.OrganizationID,
		Actor:          actor,
		Action:         action,
		TargetKind:     targetKind,
		TargetID:       targetID,
		ContextKind:    contextKind,
		ContextID:      contextID,
		RequestID:      requestID,
		Outcome:        "denied",
		ReasonCode:     reason,
		Now:            now,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit denied collaboration action: %w", err)
	}
	return result
}

func requireCollaborationGrant(ctx context.Context, tx *sql.Tx, actor Principal, capability Capability, scope Scope, action, targetKind, targetID, requestID string, now time.Time) error {
	return requireCollaborationGrantWithContext(ctx, tx, actor, capability, scope, action, targetKind, targetID, "", "", requestID, now)
}

func requireCollaborationGrantWithContext(ctx context.Context, tx *sql.Tx, actor Principal, capability Capability, scope Scope, action, targetKind, targetID, contextKind, contextID, requestID string, now time.Time) error {
	reason, err := requireGrant(ctx, tx, actor, capability, scope, now, "")
	if err != nil {
		return err
	}
	if reason == "" {
		return nil
	}
	return denyCollaborationWithContext(ctx, tx, actor, action, targetKind, targetID, contextKind, contextID, requestID, reason, now, ErrPermissionDenied)
}

func validatePrincipalInOrganization(ctx context.Context, tx *sql.Tx, principal Principal, organizationID string) error {
	return validatePrincipalStateInOrganization(ctx, tx, principal, organizationID, true)
}

func validatePrincipalExistsInOrganization(ctx context.Context, tx *sql.Tx, principal Principal, organizationID string) error {
	return validatePrincipalStateInOrganization(ctx, tx, principal, organizationID, false)
}

func validatePrincipalStateInOrganization(ctx context.Context, tx *sql.Tx, principal Principal, organizationID string, requireActive bool) error {
	switch principal.Kind {
	case "human":
		var actualOrganizationID, status string
		err := tx.QueryRowContext(ctx, `SELECT organization_id, status FROM humans WHERE id = ?`, principal.ID).Scan(&actualOrganizationID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPrincipalNotFound
		}
		if err != nil {
			return fmt.Errorf("read human principal: %w", err)
		}
		if actualOrganizationID != organizationID || (requireActive && status != "active") {
			return ErrPermissionDenied
		}
		return nil
	case "agent":
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agents WHERE id = ?)`, principal.ID).Scan(&exists); err != nil {
			return fmt.Errorf("read agent principal: %w", err)
		}
		if !exists {
			return ErrPrincipalNotFound
		}
		return nil
	default:
		return ErrInvalidPrincipal
	}
}

func requireActiveMembership(ctx context.Context, tx *sql.Tx, spaceID string, principal Principal) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM space_memberships
			WHERE space_id = ? AND principal_kind = ? AND principal_id = ?
		)
	`, spaceID, principal.Kind, principal.ID).Scan(&exists); err != nil {
		return fmt.Errorf("check space membership: %w", err)
	}
	if !exists {
		return ErrPermissionDenied
	}
	return nil
}

func canonicalDMKey(first, second Principal) (string, error) {
	return collaborationdomain.CanonicalDMKey(
		collaborationdomain.Principal{Kind: collaborationdomain.PrincipalKind(first.Kind), ID: first.ID},
		collaborationdomain.Principal{Kind: collaborationdomain.PrincipalKind(second.Kind), ID: second.ID},
	)
}

func validateSpaceName(name string) error {
	return collaborationdomain.ValidateSpaceName(name)
}

func validateMessageBody(body string) error {
	return collaborationdomain.ValidateMessageBody(body)
}

func collaborationSpace(space Space) collaborationdomain.Space {
	return collaborationdomain.Space{
		Kind:     collaborationdomain.SpaceKind(space.Kind),
		Archived: space.ArchivedAt != nil,
	}
}

func collaborationPrincipal(principal Principal) collaborationdomain.Principal {
	return collaborationdomain.Principal{
		Kind: collaborationdomain.PrincipalKind(principal.Kind),
		ID:   principal.ID,
	}
}

func collaborationMembershipChange(change membershipChange) collaborationdomain.MembershipChange {
	if change == membershipAdd {
		return collaborationdomain.MembershipAdd
	}
	return collaborationdomain.MembershipRemove
}

func validateMessageTarget(target MessageTarget) error {
	return collaborationdomain.ValidateMessageTarget(collaborationdomain.MessageTargetKind(target.Kind))
}

func scanSpace(row scanner) (Space, error) {
	var space Space
	var createdAt, updatedAt int64
	var archivedAt sql.NullInt64
	if err := row.Scan(&space.ID, &space.OrganizationID, &space.Kind, &space.Name, &createdAt, &updatedAt, &archivedAt); err != nil {
		return Space{}, err
	}
	space.CreatedAt = timeFromUnixNano(createdAt)
	space.UpdatedAt = timeFromUnixNano(updatedAt)
	if archivedAt.Valid {
		value := timeFromUnixNano(archivedAt.Int64)
		space.ArchivedAt = &value
	}
	return space, nil
}

const spaceSelect = `SELECT id, organization_id, kind, name, created_at, updated_at, archived_at FROM spaces`

func scanMembership(row scanner) (Membership, error) {
	var membership Membership
	var joinedAt int64
	if err := row.Scan(&membership.SpaceID, &membership.Principal.Kind, &membership.Principal.ID, &joinedAt); err != nil {
		return Membership{}, err
	}
	membership.JoinedAt = timeFromUnixNano(joinedAt)
	return membership, nil
}

func scanMessage(row scanner) (Message, error) {
	var message Message
	var createdAt int64
	if err := row.Scan(&message.ID, &message.RequestID, &message.SpaceID, &message.Target.Kind, &message.Target.ID,
		&message.TargetSequence, &message.Author.Kind, &message.Author.ID, &message.Body, &createdAt); err != nil {
		return Message{}, err
	}
	message.CreatedAt = timeFromUnixNano(createdAt)
	return message, nil
}

const messageSelect = `SELECT id, request_id, space_id, target_kind, target_id, target_sequence, author_kind, author_id, body, created_at FROM messages`
