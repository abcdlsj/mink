package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	SpaceKindDM    = "dm"
	SpaceKindGroup = "group"

	MessageTargetSpace  = "space"
	MessageTargetThread = "thread"

	maxSpaceNameRunes   = 100
	maxMessageBodyRunes = 400_000
	maxMessageListLimit = 200
)

type Space struct {
	ID             string
	OrganizationID string
	Kind           string
	Name           string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ArchivedAt     *time.Time
}

type Membership struct {
	SpaceID   string
	Principal Principal
	JoinedAt  time.Time
}

type Thread struct {
	ID        string
	SpaceID   string
	CreatedAt time.Time
}

type MessageTarget struct {
	Kind string
	ID   string
}

type Message struct {
	ID             string
	RequestID      string
	SpaceID        string
	Target         MessageTarget
	TargetSequence uint64
	Author         Principal
	Body           string
	CreatedAt      time.Time
}

type MutationReceipt struct {
	RequestID   string
	CommittedAt time.Time
}

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

func readCollaborationReceipt(ctx context.Context, tx *sql.Tx, requestID, operation string, fingerprint [sha256.Size]byte) (collaborationReceipt, bool, error) {
	var receipt collaborationReceipt
	var storedFingerprint []byte
	var committedAt int64
	err := tx.QueryRowContext(ctx, `
		SELECT operation, result_id, payload_fingerprint, committed_at
		FROM collaboration_requests
		WHERE request_id = ?
	`, requestID).Scan(&receipt.Operation, &receipt.ResultID, &storedFingerprint, &committedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return collaborationReceipt{}, false, nil
	}
	if err != nil {
		return collaborationReceipt{}, false, fmt.Errorf("read collaboration request receipt: %w", err)
	}
	if receipt.Operation != operation || !bytes.Equal(storedFingerprint, fingerprint[:]) {
		return collaborationReceipt{}, false, ErrCollaborationRequestConflict
	}
	receipt.CommittedAt = timeFromUnixNano(committedAt)
	return receipt, true, nil
}

func persistCollaborationReceipt(ctx context.Context, tx *sql.Tx, requestID, operation string, fingerprint [sha256.Size]byte, resultID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO collaboration_requests(request_id, operation, payload_fingerprint, result_id, committed_at)
		VALUES(?, ?, ?, ?, ?)
	`, requestID, operation, fingerprint[:], resultID, unixNano(now)); err != nil {
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
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: actor.OrganizationID,
		Actor:          actor,
		Action:         action,
		TargetKind:     targetKind,
		TargetID:       targetID,
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

func requireCollaborationGrant(ctx context.Context, tx *sql.Tx, actor Principal, capability string, scope Scope, action, targetKind, targetID, requestID string, now time.Time) error {
	reason, err := requireGrant(ctx, tx, actor, capability, scope, now, "")
	if err != nil {
		return err
	}
	if reason == "" {
		return nil
	}
	return denyCollaboration(ctx, tx, actor, action, targetKind, targetID, requestID, reason, now, ErrPermissionDenied)
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
	if first.Kind == second.Kind && first.ID == second.ID {
		return "", ErrDMRequiresDistinctPrincipals
	}
	parts := []string{first.Kind + ":" + first.ID, second.Kind + ":" + second.ID}
	sort.Strings(parts)
	digest := sha256.Sum256([]byte(parts[0] + "\x00" + parts[1]))
	return hex.EncodeToString(digest[:]), nil
}

func validateSpaceName(name string) error {
	if !utf8.ValidString(name) {
		return ErrInvalidSpaceName
	}
	count := utf8.RuneCountInString(name)
	if count < 1 || count > maxSpaceNameRunes || strings.TrimSpace(name) == "" {
		return ErrInvalidSpaceName
	}
	return nil
}

func validateMessageBody(body string) error {
	if !utf8.ValidString(body) {
		return ErrInvalidMessageBody
	}
	count := utf8.RuneCountInString(body)
	if count < 1 || count > maxMessageBodyRunes {
		return ErrInvalidMessageBody
	}
	return nil
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
