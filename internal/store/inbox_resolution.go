package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
)

func cancelHeldDraft(ctx context.Context, tx *sql.Tx, draft HeldDraft, item InboxItem, params ResolveHeldDraftParams) (ResolveHeldDraftResult, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE agent_held_drafts SET state = 'cancelled', resolution_action = 'cancel', updated_at = ? WHERE id = ?`, unixNano(params.Now), draft.ID); err != nil {
		return ResolveHeldDraftResult{}, fmt.Errorf("cancel held draft: %w", err)
	}
	if err := finishInboxItem(ctx, tx, item.ID, InboxCompletionCancelled, params.Now); err != nil {
		return ResolveHeldDraftResult{}, err
	}
	item, err := inboxItemByID(ctx, tx, item.ID)
	if err != nil {
		return ResolveHeldDraftResult{}, err
	}
	return ResolveHeldDraftResult{
		Action:      params.Action,
		InboxItem:   item,
		CommittedAt: params.Now.UTC(),
	}, nil
}

func sendHeldDraft(ctx context.Context, tx *sql.Tx, authentication AgentRuntimeAuthentication, draft HeldDraft, item InboxItem, targetSpace Space, target MessageTarget, fingerprint [sha256.Size]byte, params ResolveHeldDraftParams) (ResolveHeldDraftResult, error) {
	if targetSpace.ArchivedAt != nil {
		return ResolveHeldDraftResult{}, ErrSpaceArchived
	}
	if err := requireObservedBasis(ctx, tx, authentication.Principal, target, params.BasisTargetSequence); err != nil {
		return ResolveHeldDraftResult{}, err
	}
	mentions, err := heldDraftMentions(ctx, tx, draft.ID)
	if err != nil {
		return ResolveHeldDraftResult{}, err
	}
	space, err := resolveReadableTargetSpace(ctx, tx, target)
	if err != nil {
		return ResolveHeldDraftResult{}, err
	}
	if err := validateMentionMembers(ctx, tx, space, mentions); err != nil {
		return ResolveHeldDraftResult{}, err
	}
	sendResult, err := sendOrHoldInboxReplyTx(ctx, tx, authentication.Principal, item, draft.ID, target, params.BasisTargetSequence, draft.Body, mentions, params.RequestID, fingerprint, params.Now)
	if err != nil {
		return ResolveHeldDraftResult{}, err
	}
	state, resultID, err := heldDraftResolutionState(params.Action, sendResult)
	if err != nil {
		return ResolveHeldDraftResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_held_drafts
		SET state = ?, resolution_action = ?, result_kind = ?, result_id = ?, updated_at = ?
		WHERE id = ?
	`, state, params.Action, sendResult.Kind, resultID, unixNano(params.Now), draft.ID); err != nil {
		return ResolveHeldDraftResult{}, fmt.Errorf("resolve held draft: %w", err)
	}
	item, err = inboxItemByID(ctx, tx, item.ID)
	if err != nil {
		return ResolveHeldDraftResult{}, err
	}
	return ResolveHeldDraftResult{
		Action:      params.Action,
		Kind:        sendResult.Kind,
		Message:     sendResult.Message,
		HeldDraft:   sendResult.HeldDraft,
		InboxItem:   item,
		CommittedAt: params.Now.UTC(),
	}, nil
}

func heldDraftResolutionState(action string, result SendInboxReplyResult) (string, string, error) {
	if result.Kind == InboxResultMessage && result.Message != nil && result.HeldDraft == nil {
		if action == DraftResolutionRetry {
			return HeldDraftStateSent, result.Message.ID, nil
		}
		if action == DraftResolutionRetarget {
			return HeldDraftStateRetargeted, result.Message.ID, nil
		}
	}
	if result.Kind == InboxResultHeldDraft && result.Message == nil && result.HeldDraft != nil {
		if action == DraftResolutionRetry {
			return HeldDraftStateSuperseded, result.HeldDraft.ID, nil
		}
		if action == DraftResolutionRetarget {
			return HeldDraftStateRetargeted, result.HeldDraft.ID, nil
		}
	}
	return "", "", ErrInboxIntegrity
}
