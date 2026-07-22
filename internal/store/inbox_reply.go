package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func sendOrHoldInboxReplyTx(ctx context.Context, tx *sql.Tx, principal Principal, item InboxItem, predecessorDraftID string, target MessageTarget, basis uint64, body string, mentions []string, requestID string, fingerprint [sha256.Size]byte, now time.Time) (SendInboxReplyResult, error) {
	head, err := targetHead(ctx, tx, target)
	if err != nil {
		return SendInboxReplyResult{}, err
	}
	if head < basis {
		return SendInboxReplyResult{}, ErrInboxBasisMismatch
	}
	if head > basis {
		draft, err := createHeldDraft(ctx, tx, item, predecessorDraftID, target, basis, body, mentions, now)
		if err != nil {
			return SendInboxReplyResult{}, err
		}
		return SendInboxReplyResult{Kind: InboxResultHeldDraft, HeldDraft: &draft, CommittedAt: now.UTC()}, nil
	}
	message, _, err := publishMessageTx(ctx, tx, principal, target, body, mentions, requestID, fingerprint, now)
	if err != nil {
		return SendInboxReplyResult{}, err
	}
	if err := finishInboxItem(ctx, tx, item.ID, InboxCompletionSent, now); err != nil {
		return SendInboxReplyResult{}, err
	}
	return SendInboxReplyResult{Kind: InboxResultMessage, Message: &message, CommittedAt: now.UTC()}, nil
}

func createHeldDraft(ctx context.Context, tx *sql.Tx, item InboxItem, predecessorDraftID string, target MessageTarget, basis uint64, body string, mentions []string, now time.Time) (HeldDraft, error) {
	spaceID, err := resolveReadableTargetSpace(ctx, tx, target)
	if err != nil {
		return HeldDraft{}, err
	}
	draftID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_held_drafts(
			id, agent_id, inbox_item_id, predecessor_draft_id, space_id, target_kind, target_id,
			basis_target_sequence, body, held_reason, state, created_at, updated_at
		)
		VALUES(?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, 'target_advanced', 'held', ?, ?)
	`, draftID, item.AgentID, item.ID, predecessorDraftID, spaceID, target.Kind, target.ID, basis, body, unixNano(now), unixNano(now)); err != nil {
		return HeldDraft{}, fmt.Errorf("persist held draft: %w", err)
	}
	for ordinal, agentID := range mentions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_held_draft_mentions(draft_id, agent_id, ordinal) VALUES(?, ?, ?)`, draftID, agentID, ordinal); err != nil {
			return HeldDraft{}, fmt.Errorf("persist held draft mention: %w", err)
		}
	}
	draft, err := scanHeldDraft(tx.QueryRowContext(ctx, heldDraftSelect+` WHERE id = ?`, draftID))
	if err != nil {
		return HeldDraft{}, fmt.Errorf("read held draft: %w", err)
	}
	draft.MentionedAgentIDs = append([]string(nil), mentions...)
	return draft, nil
}

func canonicalMentionIDs(agentIDs []string) ([]string, error) {
	if len(agentIDs) > maxMentionCount {
		return nil, ErrInvalidMention
	}
	seen := make(map[string]struct{}, len(agentIDs))
	canonical := make([]string, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		parsed, err := uuid.Parse(agentID)
		if err != nil || parsed.String() != agentID {
			return nil, ErrInvalidMention
		}
		if _, exists := seen[agentID]; exists {
			return nil, ErrInvalidMention
		}
		seen[agentID] = struct{}{}
		canonical = append(canonical, agentID)
	}
	return canonical, nil
}

func validateMentionMembers(ctx context.Context, tx *sql.Tx, spaceID string, agentIDs []string) error {
	for _, agentID := range agentIDs {
		var member bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM space_memberships
				WHERE space_id = ? AND principal_kind = 'agent' AND principal_id = ?
			)
		`, spaceID, agentID).Scan(&member); err != nil {
			return fmt.Errorf("check mentioned agent membership: %w", err)
		}
		if !member {
			return ErrInvalidMention
		}
	}
	return nil
}
