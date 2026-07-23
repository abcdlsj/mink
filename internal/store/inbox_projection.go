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

func projectMessageAttention(ctx context.Context, tx *sql.Tx, message Message, mentions []Principal, now time.Time) ([]EligibleInboxTrigger, error) {
	if message.Target.Kind == MessageTargetThread {
		if err := upsertThreadFollow(ctx, tx, message.Author, message.SpaceID, message.Target.ID, "reply", now); err != nil {
			return nil, err
		}
	}
	eligibleMentions := make(map[Principal]bool, len(mentions))
	for _, mention := range mentions {
		mention.OrganizationID = message.Author.OrganizationID
		reason, err := requireGrant(ctx, tx, mention, CapabilitySpaceRead, Scope{Kind: "space", ID: message.SpaceID}, now, "")
		if err != nil {
			return nil, err
		}
		eligibleMentions[mention] = reason == ""
		if reason == "" && message.Target.Kind == MessageTargetThread {
			if err := upsertThreadFollow(ctx, tx, mention, message.SpaceID, message.Target.ID, "mention", now); err != nil {
				return nil, err
			}
		}
	}
	reasons := make(map[Principal]string)
	if message.Target.Kind == MessageTargetSpace {
		var kind string
		if err := tx.QueryRowContext(ctx, `SELECT kind FROM spaces WHERE id = ?`, message.SpaceID).Scan(&kind); err != nil {
			return nil, fmt.Errorf("read attention space kind: %w", err)
		}
		if kind == SpaceKindDM {
			rows, err := tx.QueryContext(ctx, `
				SELECT principal_kind, principal_id FROM space_memberships
				WHERE space_id = ?
			`, message.SpaceID)
			if err != nil {
				return nil, fmt.Errorf("list dm recipients: %w", err)
			}
			for rows.Next() {
				principal := Principal{OrganizationID: message.Author.OrganizationID}
				if err := rows.Scan(&principal.Kind, &principal.ID); err != nil {
					rows.Close()
					return nil, fmt.Errorf("scan dm recipient: %w", err)
				}
				if principal.Kind != message.Author.Kind || principal.ID != message.Author.ID {
					reasons[principal] = InboxReasonDM
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, fmt.Errorf("iterate dm recipients: %w", err)
			}
			if err := rows.Close(); err != nil {
				return nil, fmt.Errorf("close dm recipients: %w", err)
			}
		}
	} else {
		rows, err := tx.QueryContext(ctx, `
			SELECT follows.principal_kind, follows.principal_id
			FROM principal_thread_follows follows
			WHERE follows.thread_root_message_id = ?
			  AND NOT EXISTS(
				SELECT 1 FROM principal_space_mutes mutes
				WHERE mutes.principal_kind = follows.principal_kind
				  AND mutes.principal_id = follows.principal_id
				  AND mutes.space_id = follows.space_id
			  )
		`, message.Target.ID)
		if err != nil {
			return nil, fmt.Errorf("list thread followers: %w", err)
		}
		for rows.Next() {
			principal := Principal{OrganizationID: message.Author.OrganizationID}
			if err := rows.Scan(&principal.Kind, &principal.ID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan thread follower: %w", err)
			}
			if principal.Kind != message.Author.Kind || principal.ID != message.Author.ID {
				reasons[principal] = InboxReasonThreadFollow
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate thread followers: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close thread followers: %w", err)
		}
	}
	for mention, eligible := range eligibleMentions {
		if !eligible || (message.Author.Kind == mention.Kind && message.Author.ID == mention.ID) {
			continue
		}
		if reasons[mention] != InboxReasonDM {
			reasons[mention] = InboxReasonMention
		}
	}
	recipients := make([]Principal, 0, len(reasons))
	for recipient := range reasons {
		recipients = append(recipients, recipient)
	}
	sort.Slice(recipients, func(left, right int) bool {
		if recipients[left].Kind != recipients[right].Kind {
			return recipients[left].Kind < recipients[right].Kind
		}
		return recipients[left].ID < recipients[right].ID
	})
	created := make([]EligibleInboxTrigger, 0, len(recipients))
	for _, recipient := range recipients {
		reason, err := requireGrant(ctx, tx, recipient, CapabilitySpaceRead, Scope{Kind: "space", ID: message.SpaceID}, now, "")
		if err != nil {
			return nil, err
		}
		if reason != "" {
			continue
		}
		itemID := uuid.NewString()
		row := tx.QueryRowContext(ctx, `
			INSERT INTO inbox_items(
				id, recipient_kind, recipient_id, space_id, target_kind, target_id, trigger_message_id,
				trigger_target_sequence, reason, state, created_at
			)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 'unread', ?)
			ON CONFLICT(recipient_kind, recipient_id, trigger_message_id) DO NOTHING
			RETURNING sequence, id, recipient_kind, recipient_id, space_id, target_kind, target_id, trigger_message_id,
			          trigger_target_sequence, reason, state, claimed_at, done_at, completion, created_at
		`, itemID, recipient.Kind, recipient.ID, message.SpaceID, message.Target.Kind, message.Target.ID,
			message.ID, message.TargetSequence, reasons[recipient], unixNano(now))
		item, err := scanInboxItem(row)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("project inbox attention: %w", err)
		}
		item.Recipient.OrganizationID = recipient.OrganizationID
		trigger := EligibleInboxTrigger{Item: item, Message: message}
		if recipient.Kind == PrincipalAgent {
			if _, err := ensureDeliveryTx(ctx, tx, trigger); err != nil {
				return nil, err
			}
		}
		created = append(created, trigger)
	}
	return created, nil
}

func upsertThreadFollow(ctx context.Context, tx *sql.Tx, principal Principal, spaceID, threadID, source string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO principal_thread_follows(principal_kind, principal_id, space_id, thread_root_message_id, followed_at, source)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(principal_kind, principal_id, thread_root_message_id) DO UPDATE SET
			followed_at = excluded.followed_at,
			source = excluded.source
	`, principal.Kind, principal.ID, spaceID, threadID, unixNano(now), source); err != nil {
		return fmt.Errorf("persist thread follow: %w", err)
	}
	return nil
}

func closeRemovedPrincipalInbox(ctx context.Context, tx *sql.Tx, principal Principal, spaceID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE inbox_items
		SET state = 'done', done_at = ?, completion = 'access_lost'
		WHERE recipient_kind = ? AND recipient_id = ? AND space_id = ? AND state != 'done'
	`, unixNano(now), principal.Kind, principal.ID, spaceID); err != nil {
		return fmt.Errorf("close removed principal inbox items: %w", err)
	}
	for _, statement := range []string{
		`DELETE FROM principal_space_mutes WHERE principal_kind = ? AND principal_id = ? AND space_id = ?`,
		`DELETE FROM principal_thread_follows WHERE principal_kind = ? AND principal_id = ? AND space_id = ?`,
		`DELETE FROM principal_target_cursors WHERE principal_kind = ? AND principal_id = ? AND space_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, principal.Kind, principal.ID, spaceID); err != nil {
			return fmt.Errorf("remove principal inbox projection: %w", err)
		}
	}
	return nil
}
