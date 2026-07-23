package store

import (
	"context"
	"fmt"
)

func (s *Store) GetInboxNotice(ctx context.Context, params InboxNoticeParams) (bool, error) {
	tx, authentication, err := s.beginInboxTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	after := uint64(0)
	for {
		items, err := listPendingInboxItems(ctx, tx, authentication.Principal, after, maxInboxListLimit)
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
		candidates, err := listPendingInboxItems(ctx, tx, authentication.Principal, after, params.Limit)
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

func (s *Store) ObserveTarget(ctx context.Context, params ObserveTargetParams) (ObserveTargetResult, error) {
	if params.Limit == 0 || params.Limit > maxMessageListLimit {
		return ObserveTargetResult{}, ErrInvalidMessageLimit
	}
	tx, authentication, err := s.beginInboxTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return ObserveTargetResult{}, err
	}
	defer tx.Rollback()
	space, err := requireInboxReadableTarget(ctx, tx, authentication.Principal, params.Target, params.Now)
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
		INSERT INTO principal_target_cursors(principal_kind, principal_id, space_id, target_kind, target_id, seen_up_to_target_sequence, observed_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(principal_kind, principal_id, target_kind, target_id) DO UPDATE SET
			seen_up_to_target_sequence = max(seen_up_to_target_sequence, excluded.seen_up_to_target_sequence),
			observed_at = CASE WHEN excluded.seen_up_to_target_sequence >= seen_up_to_target_sequence THEN excluded.observed_at ELSE observed_at END
	`, authentication.Principal.Kind, authentication.Principal.ID, space.ID, params.Target.Kind, params.Target.ID, head, unixNano(params.Now)); err != nil {
		return ObserveTargetResult{}, fmt.Errorf("persist target cursor: %w", err)
	}
	result := ObserveTargetResult{Target: params.Target, Head: head, Messages: messages, ObservedAt: params.Now.UTC()}
	if err := tx.Commit(); err != nil {
		return ObserveTargetResult{}, fmt.Errorf("commit target observation: %w", err)
	}
	return result, nil
}

func (s *Store) ListHeldDrafts(ctx context.Context, params ListHeldDraftsParams) (ListHeldDraftsResult, error) {
	if params.Limit == 0 || params.Limit > maxInboxListLimit {
		return ListHeldDraftsResult{}, ErrInvalidInboxLimit
	}
	tx, authentication, err := s.beginAgentInboxTransaction(ctx, params.Authentication, params.Now)
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
			item, err := requireOwnedInboxItem(ctx, tx, authentication.Principal, drafts[index].InboxItemID)
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
			drafts[index].MentionedPrincipals = mentions
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
