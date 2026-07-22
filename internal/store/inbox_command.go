package store

import (
	"context"
	"fmt"
)

func (s *Store) ClaimInboxItem(ctx context.Context, params ClaimInboxItemParams) (InboxItem, error) {
	fingerprint, err := inboxFingerprint(struct {
		InboxItemID string `json:"inbox_item_id"`
	}{params.InboxItemID})
	if err != nil {
		return InboxItem{}, err
	}
	tx, authentication, err := s.beginInboxTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return InboxItem{}, err
	}
	defer tx.Rollback()
	item, err := requireOwnedInboxItem(ctx, tx, authentication.Principal.ID, params.InboxItemID)
	if err != nil {
		return InboxItem{}, err
	}
	if err := requireInboxItemAccess(ctx, tx, authentication.Principal, item, params.Now); err != nil {
		return InboxItem{}, err
	}
	if replay, found, err := replayInboxItemRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationClaimInboxItem, fingerprint, item); err != nil {
		return InboxItem{}, err
	} else if found {
		return commitInboxReplay(tx, replay)
	}
	if item.State != InboxStateUnread {
		return InboxItem{}, ErrInboxItemNotUnread
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_inbox_items SET state = 'claimed', claimed_at = ? WHERE id = ?`, unixNano(params.Now), item.ID); err != nil {
		return InboxItem{}, fmt.Errorf("claim inbox item: %w", err)
	}
	item, err = inboxItemByID(ctx, tx, item.ID)
	if err != nil {
		return InboxItem{}, err
	}
	if err := persistInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationClaimInboxItem, fingerprint, inboxItemRequestReceipt{Item: item}, params.Now); err != nil {
		return InboxItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return InboxItem{}, fmt.Errorf("commit inbox claim: %w", err)
	}
	return item, nil
}

func (s *Store) CompleteInboxItem(ctx context.Context, params CompleteInboxItemParams) (InboxItem, error) {
	fingerprint, err := inboxFingerprint(struct {
		InboxItemID string `json:"inbox_item_id"`
	}{params.InboxItemID})
	if err != nil {
		return InboxItem{}, err
	}
	tx, authentication, err := s.beginInboxTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return InboxItem{}, err
	}
	defer tx.Rollback()
	item, err := requireOwnedInboxItem(ctx, tx, authentication.Principal.ID, params.InboxItemID)
	if err != nil {
		return InboxItem{}, err
	}
	if err := requireInboxItemAccess(ctx, tx, authentication.Principal, item, params.Now); err != nil {
		return InboxItem{}, err
	}
	if replay, found, err := replayInboxItemRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationCompleteInboxItem, fingerprint, item); err != nil {
		return InboxItem{}, err
	} else if found {
		return commitInboxReplay(tx, replay)
	}
	if item.State != InboxStateClaimed {
		return InboxItem{}, ErrInboxItemNotClaimed
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_held_drafts
		SET state = 'cancelled', resolution_action = 'cancel', updated_at = ?
		WHERE inbox_item_id = ? AND state = 'held'
	`, unixNano(params.Now), item.ID); err != nil {
		return InboxItem{}, fmt.Errorf("cancel held drafts on inbox completion: %w", err)
	}
	if err := finishInboxItem(ctx, tx, item.ID, InboxCompletionSilent, params.Now); err != nil {
		return InboxItem{}, err
	}
	item, err = inboxItemByID(ctx, tx, item.ID)
	if err != nil {
		return InboxItem{}, err
	}
	if err := persistInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationCompleteInboxItem, fingerprint, inboxItemRequestReceipt{Item: item}, params.Now); err != nil {
		return InboxItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return InboxItem{}, fmt.Errorf("commit inbox completion: %w", err)
	}
	return item, nil
}

func (s *Store) SetSpaceMute(ctx context.Context, params SetSpaceMuteParams) (InboxPreferenceResult, error) {
	fingerprint, err := inboxFingerprint(struct {
		SpaceID string `json:"space_id"`
		Muted   bool   `json:"muted"`
	}{params.SpaceID, params.Muted})
	if err != nil {
		return InboxPreferenceResult{}, err
	}
	tx, authentication, err := s.beginInboxTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return InboxPreferenceResult{}, err
	}
	defer tx.Rollback()
	if _, err := requireAgentReadableTarget(ctx, tx, authentication.Principal, MessageTarget{Kind: MessageTargetSpace, ID: params.SpaceID}, params.Now); err != nil {
		return InboxPreferenceResult{}, err
	}
	if replay, found, err := replayInboxPreferenceRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationSetSpaceMute, fingerprint); err != nil {
		return InboxPreferenceResult{}, err
	} else if found {
		return commitInboxReplay(tx, replay)
	}
	if params.Muted {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO agent_space_mutes(agent_id, space_id, muted_at) VALUES(?, ?, ?)
			ON CONFLICT(agent_id, space_id) DO UPDATE SET muted_at = excluded.muted_at
		`, authentication.Principal.ID, params.SpaceID, unixNano(params.Now))
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM agent_space_mutes WHERE agent_id = ? AND space_id = ?`, authentication.Principal.ID, params.SpaceID)
	}
	if err != nil {
		return InboxPreferenceResult{}, fmt.Errorf("persist space mute: %w", err)
	}
	result := InboxPreferenceResult{Enabled: params.Muted, CommittedAt: params.Now.UTC()}
	if err := persistInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationSetSpaceMute, fingerprint, inboxPreferenceRequestReceipt(result), params.Now); err != nil {
		return InboxPreferenceResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return InboxPreferenceResult{}, fmt.Errorf("commit space mute: %w", err)
	}
	return result, nil
}

func (s *Store) SetThreadFollow(ctx context.Context, params SetThreadFollowParams) (InboxPreferenceResult, error) {
	fingerprint, err := inboxFingerprint(struct {
		ThreadID string `json:"thread_id"`
		Followed bool   `json:"followed"`
	}{params.ThreadID, params.Followed})
	if err != nil {
		return InboxPreferenceResult{}, err
	}
	tx, authentication, err := s.beginInboxTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return InboxPreferenceResult{}, err
	}
	defer tx.Rollback()
	target := MessageTarget{Kind: MessageTargetThread, ID: params.ThreadID}
	space, err := requireAgentReadableTarget(ctx, tx, authentication.Principal, target, params.Now)
	if err != nil {
		return InboxPreferenceResult{}, err
	}
	if replay, found, err := replayInboxPreferenceRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationSetThreadFollow, fingerprint); err != nil {
		return InboxPreferenceResult{}, err
	} else if found {
		return commitInboxReplay(tx, replay)
	}
	if params.Followed {
		err = upsertThreadFollow(ctx, tx, authentication.Principal.ID, space.ID, params.ThreadID, "explicit", params.Now)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM agent_thread_follows WHERE agent_id = ? AND thread_root_message_id = ?`, authentication.Principal.ID, params.ThreadID)
	}
	if err != nil {
		return InboxPreferenceResult{}, fmt.Errorf("persist thread follow: %w", err)
	}
	result := InboxPreferenceResult{Enabled: params.Followed, CommittedAt: params.Now.UTC()}
	if err := persistInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationSetThreadFollow, fingerprint, inboxPreferenceRequestReceipt(result), params.Now); err != nil {
		return InboxPreferenceResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return InboxPreferenceResult{}, fmt.Errorf("commit thread follow: %w", err)
	}
	return result, nil
}

func (s *Store) SendInboxReply(ctx context.Context, params SendInboxReplyParams) (SendInboxReplyResult, error) {
	mentions, err := canonicalMentionIDs(params.MentionedAgentIDs)
	if err != nil {
		return SendInboxReplyResult{}, err
	}
	if err := validateMessageBody(params.Body); err != nil {
		return SendInboxReplyResult{}, err
	}
	fingerprint, err := inboxFingerprint(struct {
		InboxItemID string   `json:"inbox_item_id"`
		Basis       uint64   `json:"basis_target_sequence"`
		Body        string   `json:"body"`
		Mentions    []string `json:"mentioned_agent_ids,omitempty"`
	}{params.InboxItemID, params.BasisTargetSequence, params.Body, mentions})
	if err != nil {
		return SendInboxReplyResult{}, err
	}
	tx, authentication, err := s.beginInboxTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return SendInboxReplyResult{}, err
	}
	defer tx.Rollback()
	item, err := requireOwnedInboxItem(ctx, tx, authentication.Principal.ID, params.InboxItemID)
	if err != nil {
		return SendInboxReplyResult{}, err
	}
	space, err := requireInboxReplyGrant(ctx, tx, authentication.Principal, item.Target, item, params.Now)
	if err != nil {
		return SendInboxReplyResult{}, err
	}
	if replay, found, err := replayInboxSendRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationSendInboxReply, fingerprint, item); err != nil {
		return SendInboxReplyResult{}, err
	} else if found {
		return commitInboxReplay(tx, replay)
	}
	if item.State != InboxStateClaimed {
		return SendInboxReplyResult{}, ErrInboxItemNotClaimed
	}
	var activeDraft bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM agent_held_drafts WHERE inbox_item_id = ? AND state = 'held')
	`, item.ID).Scan(&activeDraft); err != nil {
		return SendInboxReplyResult{}, fmt.Errorf("check active held draft: %w", err)
	}
	if activeDraft {
		return SendInboxReplyResult{}, ErrInboxItemHasHeldDraft
	}
	if space.ArchivedAt != nil {
		return SendInboxReplyResult{}, ErrSpaceArchived
	}
	if err := validateMentionMembers(ctx, tx, space.ID, mentions); err != nil {
		return SendInboxReplyResult{}, err
	}
	if err := requireObservedBasis(ctx, tx, authentication.Principal.ID, item.Target, params.BasisTargetSequence); err != nil {
		return SendInboxReplyResult{}, err
	}
	result, err := sendOrHoldInboxReplyTx(ctx, tx, authentication.Principal, item, "", item.Target, params.BasisTargetSequence, params.Body, mentions, params.RequestID, fingerprint, params.Now)
	if err != nil {
		return SendInboxReplyResult{}, err
	}
	receipt, err := newInboxSendRequestReceipt(item.ID, result)
	if err != nil {
		return SendInboxReplyResult{}, err
	}
	if err := persistInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationSendInboxReply, fingerprint, receipt, params.Now); err != nil {
		return SendInboxReplyResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SendInboxReplyResult{}, fmt.Errorf("commit inbox reply: %w", err)
	}
	return result, nil
}

func (s *Store) ResolveHeldDraft(ctx context.Context, params ResolveHeldDraftParams) (ResolveHeldDraftResult, error) {
	if params.Action != DraftResolutionRetry && params.Action != DraftResolutionCancel && params.Action != DraftResolutionRetarget {
		return ResolveHeldDraftResult{}, ErrInvalidDraftResolution
	}
	fingerprintTarget := MessageTarget{}
	fingerprintBasis := uint64(0)
	if params.Action == DraftResolutionRetarget {
		fingerprintTarget = params.Target
	}
	if params.Action != DraftResolutionCancel {
		fingerprintBasis = params.BasisTargetSequence
	}
	fingerprint, err := inboxFingerprint(struct {
		HeldDraftID string        `json:"held_draft_id"`
		Action      string        `json:"action"`
		Target      MessageTarget `json:"target,omitempty"`
		Basis       uint64        `json:"basis_target_sequence,omitempty"`
	}{params.HeldDraftID, params.Action, fingerprintTarget, fingerprintBasis})
	if err != nil {
		return ResolveHeldDraftResult{}, err
	}
	tx, authentication, err := s.beginInboxTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return ResolveHeldDraftResult{}, err
	}
	defer tx.Rollback()
	draft, err := requireOwnedHeldDraft(ctx, tx, authentication.Principal.ID, params.HeldDraftID)
	if err != nil {
		return ResolveHeldDraftResult{}, err
	}
	item, err := requireOwnedInboxItem(ctx, tx, authentication.Principal.ID, draft.InboxItemID)
	if err != nil {
		return ResolveHeldDraftResult{}, err
	}
	if err := requireInboxItemAccess(ctx, tx, authentication.Principal, item, params.Now); err != nil {
		return ResolveHeldDraftResult{}, err
	}
	target := draft.Target
	if params.Action == DraftResolutionRetarget {
		target = params.Target
	}
	var targetSpace Space
	if params.Action != DraftResolutionCancel {
		targetSpace, err = requireInboxReplyGrant(ctx, tx, authentication.Principal, target, item, params.Now)
		if err != nil {
			return ResolveHeldDraftResult{}, err
		}
	}
	if replay, found, err := replayInboxResolveRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationResolveHeldDraft, fingerprint, draft, item); err != nil {
		return ResolveHeldDraftResult{}, err
	} else if found {
		return commitInboxReplay(tx, replay)
	}
	if draft.State != HeldDraftStateHeld {
		return ResolveHeldDraftResult{}, ErrHeldDraftNotHeld
	}
	if item.State != InboxStateClaimed {
		return ResolveHeldDraftResult{}, ErrInboxItemNotClaimed
	}
	var result ResolveHeldDraftResult
	if params.Action == DraftResolutionCancel {
		result, err = cancelHeldDraft(ctx, tx, draft, item, params)
	} else {
		result, err = sendHeldDraft(ctx, tx, authentication, draft, item, targetSpace, target, fingerprint, params)
	}
	if err != nil {
		return ResolveHeldDraftResult{}, err
	}
	receipt, err := newInboxResolveRequestReceipt(draft.ID, result)
	if err != nil {
		return ResolveHeldDraftResult{}, err
	}
	if err := persistInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationResolveHeldDraft, fingerprint, receipt, params.Now); err != nil {
		return ResolveHeldDraftResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ResolveHeldDraftResult{}, fmt.Errorf("commit held draft resolution: %w", err)
	}
	return result, nil
}
