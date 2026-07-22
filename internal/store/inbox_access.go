package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func requireOwnedInboxItem(ctx context.Context, tx *sql.Tx, agentID, itemID string) (InboxItem, error) {
	item, err := inboxItemByID(ctx, tx, itemID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && item.AgentID != agentID) {
		return InboxItem{}, ErrInboxItemNotFound
	}
	if err != nil {
		return InboxItem{}, err
	}
	return item, nil
}

func requireOwnedHeldDraft(ctx context.Context, tx *sql.Tx, agentID, draftID string) (HeldDraft, error) {
	draft, err := scanHeldDraft(tx.QueryRowContext(ctx, heldDraftSelect+` WHERE id = ?`, draftID))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && draft.AgentID != agentID) {
		return HeldDraft{}, ErrHeldDraftNotFound
	}
	if err != nil {
		return HeldDraft{}, fmt.Errorf("read held draft: %w", err)
	}
	return draft, nil
}

func requireInboxItemAccess(ctx context.Context, tx *sql.Tx, principal Principal, item InboxItem, now time.Time) error {
	allowed, err := inboxItemReadable(ctx, tx, principal, item, now)
	if err != nil {
		return err
	}
	if allowed {
		return nil
	}
	if item.State != InboxStateDone {
		if err := closeInboxItemAccessLost(ctx, tx, item.ID, now); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit inbox access loss: %w", err)
		}
	}
	return ErrInboxAccessLost
}

func inboxItemReadable(ctx context.Context, tx *sql.Tx, principal Principal, item InboxItem, now time.Time) (bool, error) {
	reason, err := requireGrant(ctx, tx, principal, CapabilitySpaceRead, Scope{Kind: "space", ID: item.SpaceID}, now, "")
	if err != nil {
		return false, err
	}
	if reason != "" {
		return false, nil
	}
	var valid bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM spaces s
			JOIN space_memberships m ON m.space_id = s.id
			WHERE s.id = ? AND s.organization_id = ?
			  AND m.principal_kind = 'agent' AND m.principal_id = ?
		)
	`, item.SpaceID, principal.OrganizationID, principal.ID).Scan(&valid); err != nil {
		return false, fmt.Errorf("check inbox item access: %w", err)
	}
	return valid, nil
}

func closeInboxItemAccessLost(ctx context.Context, tx *sql.Tx, itemID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_inbox_items
		SET state = 'done', done_at = ?, completion = 'access_lost'
		WHERE id = ? AND state != 'done'
	`, unixNano(now), itemID); err != nil {
		return fmt.Errorf("close inaccessible inbox item: %w", err)
	}
	return nil
}

func finishInboxItem(ctx context.Context, tx *sql.Tx, itemID, completion string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_inbox_items SET state = 'done', done_at = ?, completion = ?
		WHERE id = ? AND state = 'claimed'
	`, unixNano(now), completion, itemID); err != nil {
		return fmt.Errorf("finish inbox item: %w", err)
	}
	return nil
}

func requireAgentReadableTarget(ctx context.Context, tx *sql.Tx, principal Principal, target MessageTarget, now time.Time) (Space, error) {
	spaceID, err := resolveReadableTargetSpace(ctx, tx, target)
	if err != nil {
		return Space{}, err
	}
	return requireReadableSpace(ctx, tx, principal, spaceID, now)
}

func requireInboxReplyGrant(ctx context.Context, tx *sql.Tx, principal Principal, target MessageTarget, item InboxItem, now time.Time) (Space, error) {
	if err := requireInboxItemAccess(ctx, tx, principal, item, now); err != nil {
		return Space{}, err
	}
	spaceID, err := resolveReadableTargetSpace(ctx, tx, target)
	if err != nil {
		return Space{}, err
	}
	reason, err := requireGrant(ctx, tx, principal, CapabilityMessageSend, Scope{Kind: "space", ID: spaceID}, now, "")
	if err != nil {
		return Space{}, err
	}
	if reason != "" {
		return Space{}, ErrPermissionDenied
	}
	space, err := loadMutationSpace(ctx, tx, principal, spaceID)
	if err != nil {
		return Space{}, err
	}
	return space, nil
}

func requireObservedBasis(ctx context.Context, tx *sql.Tx, agentID string, target MessageTarget, basis uint64) error {
	var seen uint64
	err := tx.QueryRowContext(ctx, `
		SELECT seen_up_to_target_sequence FROM agent_target_cursors
		WHERE agent_id = ? AND target_kind = ? AND target_id = ?
	`, agentID, target.Kind, target.ID).Scan(&seen)
	if errors.Is(err, sql.ErrNoRows) {
		seen = 0
	} else if err != nil {
		return fmt.Errorf("read target cursor: %w", err)
	}
	if seen != basis {
		return ErrInboxBasisMismatch
	}
	return nil
}

func targetHead(ctx context.Context, tx *sql.Tx, target MessageTarget) (uint64, error) {
	var head uint64
	err := tx.QueryRowContext(ctx, `
		SELECT next_sequence - 1 FROM message_target_sequences
		WHERE target_kind = ? AND target_id = ?
	`, target.Kind, target.ID).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read target head: %w", err)
	}
	return head, nil
}
