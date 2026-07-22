package store

import (
	"context"
	"database/sql"
	"fmt"
)

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
