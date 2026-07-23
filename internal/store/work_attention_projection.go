package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const workAttentionLimit = 100

// WorkAttentionItem is deliberately metadata-only. Bodies, message targets,
// runtime credentials, and replay bases remain private to the Agent runtime.
type WorkAttentionItem struct {
	WorkID     string
	SpaceID    string
	AgentID    string
	Kind       string
	Status     string
	ReasonCode string
	UpdatedAt  time.Time
}

type WorkAttentionQuery struct {
	Human Principal
	Limit uint32
	Now   time.Time
}

func (s *Store) ListWorkAttentionItems(ctx context.Context, params WorkAttentionQuery) ([]WorkAttentionItem, error) {
	if params.Limit > workAttentionLimit || params.Now.IsZero() || params.Human.Kind != "human" || !params.Human.Valid() {
		return nil, ErrPermissionDenied
	}
	limit := params.Limit
	if limit == 0 {
		limit = 50
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin Work Attention projection: %w", err)
	}
	defer tx.Rollback()
	if err := validatePrincipalInOrganization(ctx, tx, params.Human, params.Human.OrganizationID); err != nil {
		return nil, ErrPermissionDenied
	}
	items := make([]WorkAttentionItem, 0, limit)
	if err := appendHumanWorkAttention(ctx, tx, params, &items, limit); err != nil {
		return nil, err
	}
	if len(items) < int(limit) {
		if err := appendHumanAgentExceptions(ctx, tx, params, &items, limit); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit Work Attention projection: %w", err)
	}
	return items, nil
}

func appendHumanWorkAttention(ctx context.Context, tx *sql.Tx, params WorkAttentionQuery, items *[]WorkAttentionItem, limit uint32) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT w.id, w.source_space_id, COALESCE((
			SELECT wa.agent_id FROM work_assignments wa
			WHERE wa.work_id = w.id AND wa.ended_at IS NULL
			ORDER BY wa.assigned_at, wa.id LIMIT 1
		), ''), a.status, a.requested_at
		FROM works w JOIN work_approvals a ON a.work_id = w.id
		WHERE a.status = 'pending' ORDER BY a.requested_at DESC, a.id DESC
	`)
	if err != nil {
		return fmt.Errorf("list Human work attention: %w", err)
	}
	defer rows.Close()
	for rows.Next() && len(*items) < int(limit) {
		var item WorkAttentionItem
		var updated int64
		if err := rows.Scan(&item.WorkID, &item.SpaceID, &item.AgentID, &item.Status, &updated); err != nil {
			return fmt.Errorf("scan Human work attention: %w", err)
		}
		if !workAttentionReadable(ctx, tx, params.Human, item.WorkID, item.SpaceID, params.Now) {
			continue
		}
		item.Kind, item.ReasonCode, item.UpdatedAt = "work_approval", "pending_approval", timeFromUnixNano(updated)
		*items = append(*items, item)
	}
	return rows.Err()
}

func appendHumanAgentExceptions(ctx context.Context, tx *sql.Tx, params WorkAttentionQuery, items *[]WorkAttentionItem, limit uint32) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT w.id, w.source_space_id, i.recipient_id, i.state,
			CASE WHEN d.id IS NOT NULL THEN 'held_draft' ELSE 'claimed' END,
			COALESCE(d.updated_at, i.claimed_at, i.created_at)
		FROM inbox_items i
		JOIN work_assignments wa ON wa.agent_id = i.recipient_id AND wa.ended_at IS NULL
		JOIN works w ON w.id = wa.work_id AND w.source_space_id = i.space_id
		LEFT JOIN agent_held_drafts d ON d.inbox_item_id = i.id AND d.state = 'held'
		WHERE i.recipient_kind = 'agent' AND (i.state = 'claimed' OR d.id IS NOT NULL)
		ORDER BY COALESCE(d.updated_at, i.claimed_at, i.created_at) DESC, i.id DESC
	`)
	if err != nil {
		return fmt.Errorf("list Human Agent exceptions: %w", err)
	}
	defer rows.Close()
	for rows.Next() && len(*items) < int(limit) {
		var item WorkAttentionItem
		var updated int64
		if err := rows.Scan(&item.WorkID, &item.SpaceID, &item.AgentID, &item.Status, &item.ReasonCode, &updated); err != nil {
			return fmt.Errorf("scan Human Agent exception: %w", err)
		}
		if !workAttentionReadable(ctx, tx, params.Human, item.WorkID, item.SpaceID, params.Now) {
			continue
		}
		item.Kind, item.UpdatedAt = "agent_exception", timeFromUnixNano(updated)
		*items = append(*items, item)
	}
	return rows.Err()
}

func workAttentionReadable(ctx context.Context, tx *sql.Tx, human Principal, workID, spaceID string, now time.Time) bool {
	if workID == "" || spaceID == "" {
		return false
	}
	if reason, err := requireGrant(ctx, tx, human, CapabilityWorkRead, Scope{Kind: "work", ID: workID}, now, ""); err != nil || reason != "" {
		return false
	}
	_, err := requireReadableSpace(ctx, tx, human, spaceID, now)
	return err == nil
}
