package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

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
		trigger := EligibleInboxTrigger{Item: item, Message: message}
		if _, err := ensureDeliveryTx(ctx, tx, trigger); err != nil {
			return nil, err
		}
		created = append(created, trigger)
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
