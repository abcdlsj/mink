package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

const (
	InboxReasonDM           = "dm"
	InboxReasonMention      = "mention"
	InboxReasonThreadFollow = "thread_follow"

	InboxStateUnread  = "unread"
	InboxStateClaimed = "claimed"
	InboxStateDone    = "done"

	InboxCompletionSent       = "sent"
	InboxCompletionCancelled  = "cancelled"
	InboxCompletionSilent     = "silent"
	InboxCompletionAccessLost = "access_lost"

	HeldDraftStateHeld       = "held"
	HeldDraftStateSent       = "sent"
	HeldDraftStateCancelled  = "cancelled"
	HeldDraftStateSuperseded = "superseded"
	HeldDraftStateRetargeted = "retargeted"

	DraftResolutionRetry    = "retry"
	DraftResolutionCancel   = "cancel"
	DraftResolutionRetarget = "retarget"

	InboxResultMessage   = "message"
	InboxResultHeldDraft = "held_draft"

	operationClaimInboxItem    = "claim"
	operationCompleteInboxItem = "complete"
	operationSetSpaceMute      = "space_mute.set"
	operationSetThreadFollow   = "thread_follow.set"
	operationSendInboxReply    = "reply.send"
	operationResolveHeldDraft  = "draft.resolve"

	maxInboxListLimit = 200
	maxMentionCount   = 64
)

type InboxItem struct {
	Sequence              uint64
	ID                    string
	AgentID               string
	SpaceID               string
	Target                MessageTarget
	TriggerMessageID      string
	TriggerTargetSequence uint64
	Reason                string
	State                 string
	ClaimedAt             *time.Time
	DoneAt                *time.Time
	Completion            string
	CreatedAt             time.Time
}

type EligibleInboxTrigger struct {
	Item    InboxItem
	Message Message
}

type HeldDraft struct {
	Sequence            uint64
	ID                  string
	AgentID             string
	InboxItemID         string
	PredecessorDraftID  string
	SpaceID             string
	Target              MessageTarget
	BasisTargetSequence uint64
	Body                string
	MentionedAgentIDs   []string
	HeldReason          string
	State               string
	ResolutionAction    string
	ResultKind          string
	ResultID            string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type InboxNoticeParams struct {
	Authentication AgentRuntimeAuthentication
	Now            time.Time
}

type ListInboxItemsParams struct {
	Authentication AgentRuntimeAuthentication
	AfterSequence  uint64
	Limit          uint32
	Now            time.Time
}

type ClaimInboxItemParams struct {
	RequestID      string
	Authentication AgentRuntimeAuthentication
	InboxItemID    string
	Now            time.Time
}

type ObserveTargetParams struct {
	Authentication AgentRuntimeAuthentication
	Target         MessageTarget
	Limit          uint32
	Now            time.Time
}

type ObserveTargetResult struct {
	Target     MessageTarget
	Head       uint64
	Messages   []Message
	ObservedAt time.Time
}

type CompleteInboxItemParams struct {
	RequestID      string
	Authentication AgentRuntimeAuthentication
	InboxItemID    string
	Now            time.Time
}

type SetSpaceMuteParams struct {
	RequestID      string
	Authentication AgentRuntimeAuthentication
	SpaceID        string
	Muted          bool
	Now            time.Time
}

type SetThreadFollowParams struct {
	RequestID      string
	Authentication AgentRuntimeAuthentication
	ThreadID       string
	Followed       bool
	Now            time.Time
}

type InboxPreferenceResult struct {
	Enabled     bool
	CommittedAt time.Time
}

type SendInboxReplyParams struct {
	RequestID           string
	Authentication      AgentRuntimeAuthentication
	InboxItemID         string
	BasisTargetSequence uint64
	Body                string
	MentionedAgentIDs   []string
	Now                 time.Time
}

type SendInboxReplyResult struct {
	Kind        string
	Message     *Message
	HeldDraft   *HeldDraft
	CommittedAt time.Time
}

type ListHeldDraftsParams struct {
	Authentication AgentRuntimeAuthentication
	AfterSequence  uint64
	Limit          uint32
	Now            time.Time
}

type ListHeldDraftsResult struct {
	Drafts       []HeldDraft
	NextSequence uint64
}

type ResolveHeldDraftParams struct {
	RequestID           string
	Authentication      AgentRuntimeAuthentication
	HeldDraftID         string
	Action              string
	Target              MessageTarget
	BasisTargetSequence uint64
	Now                 time.Time
}

type ResolveHeldDraftResult struct {
	Action      string
	Kind        string
	Message     *Message
	HeldDraft   *HeldDraft
	InboxItem   InboxItem
	CommittedAt time.Time
}

type inboxReceipt struct {
	CommittedAt time.Time
	Snapshot    []byte
}

func (s *Store) GetInboxNotice(ctx context.Context, params InboxNoticeParams) (bool, error) {
	tx, authentication, err := s.beginInboxTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	after := uint64(0)
	for {
		items, err := listPendingInboxItems(ctx, tx, authentication.Principal.ID, after, maxInboxListLimit)
		if err != nil {
			return false, err
		}
		if len(items) == 0 {
			if err := tx.Commit(); err != nil {
				return false, fmt.Errorf("commit inbox notice: %w", err)
			}
			return false, nil
		}
		for _, item := range items {
			after = item.Sequence
			allowed, err := inboxItemReadable(ctx, tx, authentication.Principal, item, params.Now)
			if err != nil {
				return false, err
			}
			if !allowed {
				if err := closeInboxItemAccessLost(ctx, tx, item.ID, params.Now); err != nil {
					return false, err
				}
				continue
			}
			if item.State == InboxStateUnread {
				if err := tx.Commit(); err != nil {
					return false, fmt.Errorf("commit inbox notice: %w", err)
				}
				return true, nil
			}
		}
	}
}

func (s *Store) ListInboxItems(ctx context.Context, params ListInboxItemsParams) ([]InboxItem, error) {
	if params.Limit == 0 || params.Limit > maxInboxListLimit {
		return nil, ErrInvalidInboxLimit
	}
	tx, authentication, err := s.beginInboxTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	items := make([]InboxItem, 0, params.Limit)
	after := params.AfterSequence
	for len(items) < int(params.Limit) {
		candidates, err := listPendingInboxItems(ctx, tx, authentication.Principal.ID, after, params.Limit)
		if err != nil {
			return nil, err
		}
		if len(candidates) == 0 {
			break
		}
		for _, item := range candidates {
			after = item.Sequence
			allowed, err := inboxItemReadable(ctx, tx, authentication.Principal, item, params.Now)
			if err != nil {
				return nil, err
			}
			if !allowed {
				if err := closeInboxItemAccessLost(ctx, tx, item.ID, params.Now); err != nil {
					return nil, err
				}
				continue
			}
			items = append(items, item)
			if len(items) == int(params.Limit) {
				break
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit inbox list: %w", err)
	}
	return items, nil
}

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
	var replay InboxItem
	if found, err := replayInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationClaimInboxItem, fingerprint, &replay); err != nil {
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
	if err := persistInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationClaimInboxItem, fingerprint, item, params.Now); err != nil {
		return InboxItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return InboxItem{}, fmt.Errorf("commit inbox claim: %w", err)
	}
	return item, nil
}

func (s *Store) ObserveTarget(ctx context.Context, params ObserveTargetParams) (ObserveTargetResult, error) {
	if params.Limit == 0 || params.Limit > maxMessageListLimit {
		return ObserveTargetResult{}, ErrInvalidMessageLimit
	}
	tx, authentication, err := s.beginInboxTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return ObserveTargetResult{}, err
	}
	defer tx.Rollback()
	space, err := requireAgentReadableTarget(ctx, tx, authentication.Principal, params.Target, params.Now)
	if err != nil {
		return ObserveTargetResult{}, err
	}
	head, err := targetHead(ctx, tx, params.Target)
	if err != nil {
		return ObserveTargetResult{}, err
	}
	rows, err := tx.QueryContext(ctx, messageSelect+`
		WHERE target_kind = ? AND target_id = ? AND target_sequence <= ?
		ORDER BY target_sequence DESC
		LIMIT ?
	`, params.Target.Kind, params.Target.ID, head, params.Limit)
	if err != nil {
		return ObserveTargetResult{}, fmt.Errorf("observe target messages: %w", err)
	}
	var messages []Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			rows.Close()
			return ObserveTargetResult{}, fmt.Errorf("scan observed message: %w", err)
		}
		message.Author.OrganizationID = space.OrganizationID
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ObserveTargetResult{}, fmt.Errorf("iterate observed messages: %w", err)
	}
	if err := rows.Close(); err != nil {
		return ObserveTargetResult{}, fmt.Errorf("close observed messages: %w", err)
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	if err := loadMessageMentions(ctx, tx, messages); err != nil {
		return ObserveTargetResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_target_cursors(agent_id, space_id, target_kind, target_id, seen_up_to_target_sequence, observed_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id, target_kind, target_id) DO UPDATE SET
			seen_up_to_target_sequence = max(seen_up_to_target_sequence, excluded.seen_up_to_target_sequence),
			observed_at = CASE WHEN excluded.seen_up_to_target_sequence >= seen_up_to_target_sequence THEN excluded.observed_at ELSE observed_at END
	`, authentication.Principal.ID, space.ID, params.Target.Kind, params.Target.ID, head, unixNano(params.Now)); err != nil {
		return ObserveTargetResult{}, fmt.Errorf("persist target cursor: %w", err)
	}
	result := ObserveTargetResult{Target: params.Target, Head: head, Messages: messages, ObservedAt: params.Now.UTC()}
	if err := tx.Commit(); err != nil {
		return ObserveTargetResult{}, fmt.Errorf("commit target observation: %w", err)
	}
	return result, nil
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
	var replay InboxItem
	if found, err := replayInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationCompleteInboxItem, fingerprint, &replay); err != nil {
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
	if err := persistInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationCompleteInboxItem, fingerprint, item, params.Now); err != nil {
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
	var replay InboxPreferenceResult
	if found, err := replayInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationSetSpaceMute, fingerprint, &replay); err != nil {
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
	if err := persistInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationSetSpaceMute, fingerprint, result, params.Now); err != nil {
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
	var replay InboxPreferenceResult
	if found, err := replayInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationSetThreadFollow, fingerprint, &replay); err != nil {
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
	if err := persistInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationSetThreadFollow, fingerprint, result, params.Now); err != nil {
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
	var replay SendInboxReplyResult
	if found, err := replayInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationSendInboxReply, fingerprint, &replay); err != nil {
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
	if err := persistInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationSendInboxReply, fingerprint, result, params.Now); err != nil {
		return SendInboxReplyResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SendInboxReplyResult{}, fmt.Errorf("commit inbox reply: %w", err)
	}
	return result, nil
}

func (s *Store) ListHeldDrafts(ctx context.Context, params ListHeldDraftsParams) (ListHeldDraftsResult, error) {
	if params.Limit == 0 || params.Limit > maxInboxListLimit {
		return ListHeldDraftsResult{}, ErrInvalidInboxLimit
	}
	tx, authentication, err := s.beginInboxTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return ListHeldDraftsResult{}, err
	}
	defer tx.Rollback()
	result := ListHeldDraftsResult{Drafts: make([]HeldDraft, 0, params.Limit), NextSequence: params.AfterSequence}
	for len(result.Drafts) < int(params.Limit) {
		drafts, err := listHeldDraftCandidates(ctx, tx, authentication.Principal.ID, result.NextSequence, params.Limit)
		if err != nil {
			return ListHeldDraftsResult{}, err
		}
		if len(drafts) == 0 {
			break
		}
		for index := range drafts {
			result.NextSequence = drafts[index].Sequence
			item, err := requireOwnedInboxItem(ctx, tx, authentication.Principal.ID, drafts[index].InboxItemID)
			if err != nil {
				return ListHeldDraftsResult{}, err
			}
			if item.State != InboxStateClaimed {
				continue
			}
			allowed, err := inboxItemReadable(ctx, tx, authentication.Principal, item, params.Now)
			if err != nil {
				return ListHeldDraftsResult{}, err
			}
			if !allowed {
				if err := closeInboxItemAccessLost(ctx, tx, item.ID, params.Now); err != nil {
					return ListHeldDraftsResult{}, err
				}
				continue
			}
			mentions, err := heldDraftMentions(ctx, tx, drafts[index].ID)
			if err != nil {
				return ListHeldDraftsResult{}, err
			}
			drafts[index].MentionedAgentIDs = mentions
			result.Drafts = append(result.Drafts, drafts[index])
			if len(result.Drafts) == int(params.Limit) {
				break
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return ListHeldDraftsResult{}, fmt.Errorf("commit held draft list: %w", err)
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
	var replay ResolveHeldDraftResult
	if found, err := replayInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationResolveHeldDraft, fingerprint, &replay); err != nil {
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
	result := ResolveHeldDraftResult{Action: params.Action, CommittedAt: params.Now.UTC()}
	if params.Action == DraftResolutionCancel {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_held_drafts SET state = 'cancelled', resolution_action = 'cancel', updated_at = ? WHERE id = ?`, unixNano(params.Now), draft.ID); err != nil {
			return ResolveHeldDraftResult{}, fmt.Errorf("cancel held draft: %w", err)
		}
		if err := finishInboxItem(ctx, tx, item.ID, InboxCompletionCancelled, params.Now); err != nil {
			return ResolveHeldDraftResult{}, err
		}
		item, err = inboxItemByID(ctx, tx, item.ID)
		if err != nil {
			return ResolveHeldDraftResult{}, err
		}
		result.InboxItem = item
	} else {
		if targetSpace.ArchivedAt != nil {
			return ResolveHeldDraftResult{}, ErrSpaceArchived
		}
		if err := requireObservedBasis(ctx, tx, authentication.Principal.ID, target, params.BasisTargetSequence); err != nil {
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
		result.Kind = sendResult.Kind
		result.Message = sendResult.Message
		result.HeldDraft = sendResult.HeldDraft
		var state, resultID string
		switch {
		case params.Action == DraftResolutionRetry && sendResult.Kind == InboxResultMessage && sendResult.Message != nil:
			state = HeldDraftStateSent
			resultID = sendResult.Message.ID
		case params.Action == DraftResolutionRetry && sendResult.Kind == InboxResultHeldDraft && sendResult.HeldDraft != nil:
			state = HeldDraftStateSuperseded
			resultID = sendResult.HeldDraft.ID
		case params.Action == DraftResolutionRetarget && sendResult.Kind == InboxResultMessage && sendResult.Message != nil:
			state = HeldDraftStateRetargeted
			resultID = sendResult.Message.ID
		case params.Action == DraftResolutionRetarget && sendResult.Kind == InboxResultHeldDraft && sendResult.HeldDraft != nil:
			state = HeldDraftStateRetargeted
			resultID = sendResult.HeldDraft.ID
		default:
			return ResolveHeldDraftResult{}, ErrInboxIntegrity
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
		result.InboxItem = item
	}
	if err := persistInboxRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationResolveHeldDraft, fingerprint, result, params.Now); err != nil {
		return ResolveHeldDraftResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ResolveHeldDraftResult{}, fmt.Errorf("commit held draft resolution: %w", err)
	}
	return result, nil
}

func (s *Store) beginInboxTransaction(ctx context.Context, authentication AgentRuntimeAuthentication, now time.Time) (*sql.Tx, AgentRuntimeAuthentication, error) {
	if !authentication.Valid() {
		return nil, AgentRuntimeAuthentication{}, ErrAgentRuntimeUnauthenticated
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, AgentRuntimeAuthentication{}, fmt.Errorf("begin agent inbox transaction: %w", err)
	}
	current, err := requireAgentRuntimeSession(ctx, tx, authentication.Proof, now)
	if err != nil {
		tx.Rollback()
		return nil, AgentRuntimeAuthentication{}, err
	}
	if current.Principal != authentication.Principal {
		tx.Rollback()
		return nil, AgentRuntimeAuthentication{}, ErrAgentRuntimeUnauthenticated
	}
	return tx, current, nil
}

func inboxFingerprint(value any) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode inbox request: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func replayInboxRequest(ctx context.Context, tx *sql.Tx, requestID, agentID, operation string, fingerprint [sha256.Size]byte, response any) (bool, error) {
	var storedAgentID, storedOperation string
	var storedFingerprint, snapshot []byte
	err := tx.QueryRowContext(ctx, `
		SELECT agent_id, operation, payload_fingerprint, response_snapshot
		FROM agent_requests WHERE request_id = ?
	`, requestID).Scan(&storedAgentID, &storedOperation, &storedFingerprint, &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read inbox request receipt: %w", err)
	}
	if storedAgentID != agentID || storedOperation != operation || !bytes.Equal(storedFingerprint, fingerprint[:]) {
		return false, ErrInboxRequestConflict
	}
	if err := json.Unmarshal(snapshot, response); err != nil {
		return false, ErrInboxIntegrity
	}
	return true, nil
}

func persistInboxRequest(ctx context.Context, tx *sql.Tx, requestID, agentID, operation string, fingerprint [sha256.Size]byte, response any, now time.Time) error {
	snapshot, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode inbox response snapshot: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_requests(request_id, agent_id, operation, payload_fingerprint, response_snapshot, committed_at)
		VALUES(?, ?, ?, ?, ?, ?)
	`, requestID, agentID, operation, fingerprint[:], snapshot, unixNano(now)); err != nil {
		return fmt.Errorf("persist inbox request receipt: %w", err)
	}
	return nil
}

func commitInboxReplay[T any](tx *sql.Tx, value T) (T, error) {
	if err := tx.Commit(); err != nil {
		var zero T
		return zero, fmt.Errorf("commit inbox request replay: %w", err)
	}
	return value, nil
}

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

func projectMessageAttention(ctx context.Context, tx *sql.Tx, message Message, mentions []string, now time.Time) ([]EligibleInboxTrigger, error) {
	if message.Target.Kind == MessageTargetThread && message.Author.Kind == "agent" {
		if err := upsertThreadFollow(ctx, tx, message.Author.ID, message.SpaceID, message.Target.ID, "reply", now); err != nil {
			return nil, err
		}
	}
	eligibleMentions := make(map[string]bool, len(mentions))
	for _, agentID := range mentions {
		principal := Principal{Kind: "agent", ID: agentID, OrganizationID: message.Author.OrganizationID}
		reason, err := requireGrant(ctx, tx, principal, CapabilitySpaceRead, Scope{Kind: "space", ID: message.SpaceID}, now, "")
		if err != nil {
			return nil, err
		}
		eligibleMentions[agentID] = reason == ""
		if reason == "" && message.Target.Kind == MessageTargetThread {
			if err := upsertThreadFollow(ctx, tx, agentID, message.SpaceID, message.Target.ID, "mention", now); err != nil {
				return nil, err
			}
		}
	}
	reasons := make(map[string]string)
	if message.Target.Kind == MessageTargetSpace {
		var kind string
		if err := tx.QueryRowContext(ctx, `SELECT kind FROM spaces WHERE id = ?`, message.SpaceID).Scan(&kind); err != nil {
			return nil, fmt.Errorf("read attention space kind: %w", err)
		}
		if kind == SpaceKindDM {
			rows, err := tx.QueryContext(ctx, `
				SELECT principal_id FROM space_memberships
				WHERE space_id = ? AND principal_kind = 'agent'
			`, message.SpaceID)
			if err != nil {
				return nil, fmt.Errorf("list dm agent recipients: %w", err)
			}
			for rows.Next() {
				var agentID string
				if err := rows.Scan(&agentID); err != nil {
					rows.Close()
					return nil, fmt.Errorf("scan dm agent recipient: %w", err)
				}
				if message.Author.Kind != "agent" || message.Author.ID != agentID {
					reasons[agentID] = InboxReasonDM
				}
			}
			if err := rows.Close(); err != nil {
				return nil, fmt.Errorf("close dm agent recipients: %w", err)
			}
			if err := rows.Err(); err != nil {
				return nil, fmt.Errorf("iterate dm agent recipients: %w", err)
			}
		}
	} else {
		rows, err := tx.QueryContext(ctx, `
			SELECT follows.agent_id
			FROM agent_thread_follows follows
			WHERE follows.thread_root_message_id = ?
			  AND NOT EXISTS(
				SELECT 1 FROM agent_space_mutes mutes
				WHERE mutes.agent_id = follows.agent_id AND mutes.space_id = follows.space_id
			  )
		`, message.Target.ID)
		if err != nil {
			return nil, fmt.Errorf("list thread followers: %w", err)
		}
		for rows.Next() {
			var agentID string
			if err := rows.Scan(&agentID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan thread follower: %w", err)
			}
			if message.Author.Kind != "agent" || message.Author.ID != agentID {
				reasons[agentID] = InboxReasonThreadFollow
			}
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close thread followers: %w", err)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate thread followers: %w", err)
		}
	}
	for _, agentID := range mentions {
		if !eligibleMentions[agentID] {
			continue
		}
		if message.Author.Kind == "agent" && message.Author.ID == agentID {
			continue
		}
		if reasons[agentID] != InboxReasonDM {
			reasons[agentID] = InboxReasonMention
		}
	}
	agentIDs := make([]string, 0, len(reasons))
	for agentID := range reasons {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)
	created := make([]EligibleInboxTrigger, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		principal := Principal{Kind: "agent", ID: agentID, OrganizationID: message.Author.OrganizationID}
		reason, err := requireGrant(ctx, tx, principal, CapabilitySpaceRead, Scope{Kind: "space", ID: message.SpaceID}, now, "")
		if err != nil {
			return nil, err
		}
		if reason != "" {
			continue
		}
		itemID := uuid.NewString()
		row := tx.QueryRowContext(ctx, `
			INSERT INTO agent_inbox_items(
				id, agent_id, space_id, target_kind, target_id, trigger_message_id,
				trigger_target_sequence, reason, state, created_at
			)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, 'unread', ?)
			ON CONFLICT(agent_id, trigger_message_id) DO NOTHING
			RETURNING sequence, id, agent_id, space_id, target_kind, target_id, trigger_message_id,
			          trigger_target_sequence, reason, state, claimed_at, done_at, completion, created_at
		`, itemID, agentID, message.SpaceID, message.Target.Kind, message.Target.ID, message.ID, message.TargetSequence, reasons[agentID], unixNano(now))
		item, err := scanInboxItem(row)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("project inbox attention: %w", err)
		}
		created = append(created, EligibleInboxTrigger{Item: item, Message: message})
	}
	return created, nil
}

func upsertThreadFollow(ctx context.Context, tx *sql.Tx, agentID, spaceID, threadID, source string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_thread_follows(agent_id, space_id, thread_root_message_id, followed_at, source)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(agent_id, thread_root_message_id) DO UPDATE SET
			followed_at = excluded.followed_at,
			source = excluded.source
	`, agentID, spaceID, threadID, unixNano(now), source); err != nil {
		return fmt.Errorf("persist thread follow: %w", err)
	}
	return nil
}

func closeRemovedAgentInbox(ctx context.Context, tx *sql.Tx, agentID, spaceID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_inbox_items
		SET state = 'done', done_at = ?, completion = 'access_lost'
		WHERE agent_id = ? AND space_id = ? AND state != 'done'
	`, unixNano(now), agentID, spaceID); err != nil {
		return fmt.Errorf("close removed agent inbox items: %w", err)
	}
	for _, statement := range []string{
		`DELETE FROM agent_space_mutes WHERE agent_id = ? AND space_id = ?`,
		`DELETE FROM agent_thread_follows WHERE agent_id = ? AND space_id = ?`,
		`DELETE FROM agent_target_cursors WHERE agent_id = ? AND space_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, agentID, spaceID); err != nil {
			return fmt.Errorf("remove agent inbox projection: %w", err)
		}
	}
	return nil
}

func listPendingInboxItems(ctx context.Context, tx *sql.Tx, agentID string, after uint64, limit uint32) ([]InboxItem, error) {
	rows, err := tx.QueryContext(ctx, inboxItemSelect+`
		WHERE agent_id = ? AND state != 'done' AND sequence > ?
		ORDER BY sequence
		LIMIT ?
	`, agentID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending inbox items: %w", err)
	}
	var items []InboxItem
	for rows.Next() {
		item, err := scanInboxItem(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan pending inbox item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate pending inbox items: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close pending inbox items: %w", err)
	}
	return items, nil
}

func listHeldDraftCandidates(ctx context.Context, tx *sql.Tx, agentID string, after uint64, limit uint32) ([]HeldDraft, error) {
	rows, err := tx.QueryContext(ctx, heldDraftSelect+`
		WHERE agent_id = ? AND state = 'held' AND sequence > ?
		ORDER BY sequence
		LIMIT ?
	`, agentID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list held draft candidates: %w", err)
	}
	var drafts []HeldDraft
	for rows.Next() {
		draft, err := scanHeldDraft(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan held draft candidate: %w", err)
		}
		drafts = append(drafts, draft)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate held draft candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close held draft candidates: %w", err)
	}
	return drafts, nil
}

func inboxItemByID(ctx context.Context, tx *sql.Tx, itemID string) (InboxItem, error) {
	item, err := scanInboxItem(tx.QueryRowContext(ctx, inboxItemSelect+` WHERE id = ?`, itemID))
	if err != nil {
		return InboxItem{}, err
	}
	return item, nil
}

func scanInboxItem(row scanner) (InboxItem, error) {
	var item InboxItem
	var claimedAt, doneAt sql.NullInt64
	var createdAt int64
	if err := row.Scan(&item.Sequence, &item.ID, &item.AgentID, &item.SpaceID, &item.Target.Kind, &item.Target.ID,
		&item.TriggerMessageID, &item.TriggerTargetSequence, &item.Reason, &item.State, &claimedAt, &doneAt,
		&item.Completion, &createdAt); err != nil {
		return InboxItem{}, err
	}
	if claimedAt.Valid {
		value := timeFromUnixNano(claimedAt.Int64)
		item.ClaimedAt = &value
	}
	if doneAt.Valid {
		value := timeFromUnixNano(doneAt.Int64)
		item.DoneAt = &value
	}
	item.CreatedAt = timeFromUnixNano(createdAt)
	return item, nil
}

func scanHeldDraft(row scanner) (HeldDraft, error) {
	var draft HeldDraft
	var predecessor sql.NullString
	var createdAt, updatedAt int64
	if err := row.Scan(&draft.Sequence, &draft.ID, &draft.AgentID, &draft.InboxItemID, &predecessor, &draft.SpaceID,
		&draft.Target.Kind, &draft.Target.ID, &draft.BasisTargetSequence, &draft.Body, &draft.HeldReason,
		&draft.State, &draft.ResolutionAction, &draft.ResultKind, &draft.ResultID, &createdAt, &updatedAt); err != nil {
		return HeldDraft{}, err
	}
	if predecessor.Valid {
		draft.PredecessorDraftID = predecessor.String
	}
	draft.CreatedAt = timeFromUnixNano(createdAt)
	draft.UpdatedAt = timeFromUnixNano(updatedAt)
	return draft, nil
}

func heldDraftMentions(ctx context.Context, tx *sql.Tx, draftID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT agent_id FROM agent_held_draft_mentions WHERE draft_id = ? ORDER BY ordinal`, draftID)
	if err != nil {
		return nil, fmt.Errorf("list held draft mentions: %w", err)
	}
	defer rows.Close()
	var mentions []string
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			return nil, fmt.Errorf("scan held draft mention: %w", err)
		}
		mentions = append(mentions, agentID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate held draft mentions: %w", err)
	}
	return mentions, nil
}

const inboxItemSelect = `
	SELECT sequence, id, agent_id, space_id, target_kind, target_id, trigger_message_id,
	       trigger_target_sequence, reason, state, claimed_at, done_at, completion, created_at
	FROM agent_inbox_items`

const heldDraftSelect = `
	SELECT sequence, id, agent_id, inbox_item_id, predecessor_draft_id, space_id, target_kind, target_id,
	       basis_target_sequence, body, held_reason, state, resolution_action, result_kind, result_id,
	       created_at, updated_at
	FROM agent_held_drafts`
