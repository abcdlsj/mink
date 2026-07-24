package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func sendOrHoldInboxReplyTx(ctx context.Context, tx *sql.Tx, principal Principal, item InboxItem, predecessorDraftID string, target MessageTarget, basis uint64, body string, mentions []Principal, requestID string, fp [sha256.Size]byte, now time.Time) (SendInboxReplyResult, error) {
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
	message, _, err := publishMessageTx(ctx, tx, principal, target, body, mentions, requestID, fp, now)
	if err != nil {
		return SendInboxReplyResult{}, err
	}
	if err := finishInboxItem(ctx, tx, item.ID, InboxCompletionSent, now); err != nil {
		return SendInboxReplyResult{}, err
	}
	return SendInboxReplyResult{Kind: InboxResultMessage, Message: &message, CommittedAt: now.UTC()}, nil
}

func createHeldDraft(ctx context.Context, tx *sql.Tx, item InboxItem, predecessorDraftID string, target MessageTarget, basis uint64, body string, mentions []Principal, now time.Time) (HeldDraft, error) {
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
	`, draftID, item.Recipient.ID, item.ID, predecessorDraftID, spaceID, target.Kind, target.ID, basis, body, unixNano(now), unixNano(now)); err != nil {
		return HeldDraft{}, fmt.Errorf("persist held draft: %w", err)
	}
	for ordinal, mention := range mentions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_held_draft_mentions(draft_id, principal_kind, principal_id, ordinal) VALUES(?, ?, ?, ?)`, draftID, mention.Kind, mention.ID, ordinal); err != nil {
			return HeldDraft{}, fmt.Errorf("persist held draft mention: %w", err)
		}
	}
	draft, err := scanHeldDraft(tx.QueryRowContext(ctx, heldDraftSelect+` WHERE id = ?`, draftID))
	if err != nil {
		return HeldDraft{}, fmt.Errorf("read held draft: %w", err)
	}
	draft.MentionedPrincipals = append([]Principal(nil), mentions...)
	return draft, nil
}

func canonicalMentionPrincipals(principals []Principal) ([]Principal, error) {
	if len(principals) > maxMentionCount {
		return nil, ErrInvalidMention
	}
	seen := make(map[Principal]struct{}, len(principals))
	canonical := make([]Principal, 0, len(principals))
	for _, principal := range principals {
		parsed, err := uuid.Parse(principal.ID)
		if err != nil || parsed.String() != principal.ID ||
			(principal.Kind != PrincipalHuman && principal.Kind != PrincipalAgent) {
			return nil, ErrInvalidMention
		}
		principal.OrganizationID = ""
		if _, exists := seen[principal]; exists {
			return nil, ErrInvalidMention
		}
		seen[principal] = struct{}{}
		canonical = append(canonical, principal)
	}
	return canonical, nil
}

func validateMentionMembers(ctx context.Context, tx *sql.Tx, spaceID string, principals []Principal) error {
	for _, principal := range principals {
		var member bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM space_memberships
				WHERE space_id = ? AND principal_kind = ? AND principal_id = ?
			)
		`, spaceID, principal.Kind, principal.ID).Scan(&member); err != nil {
			return fmt.Errorf("check mentioned principal membership: %w", err)
		}
		if !member {
			return ErrInvalidMention
		}
	}
	return nil
}
