package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const operationSendMessage = "message.send"

type SendMessageParams struct {
	RequestID string
	Actor     Principal
	Target    MessageTarget
	Body      string
	Now       time.Time
}

type GetMessageParams struct {
	Actor     Principal
	MessageID string
	Now       time.Time
}

type GetThreadParams struct {
	Actor    Principal
	ThreadID string
	Now      time.Time
}

type ListMessagesParams struct {
	Actor         Principal
	Target        MessageTarget
	AfterSequence uint64
	Limit         uint32
	Now           time.Time
}

func (s *Store) SendMessage(ctx context.Context, params SendMessageParams) (Message, error) {
	if err := validateMessageBody(params.Body); err != nil {
		return Message{}, err
	}
	if params.Target.Kind != MessageTargetSpace && params.Target.Kind != MessageTargetThread {
		return Message{}, ErrInvalidMessageTarget
	}
	fingerprint, err := collaborationFingerprint(struct {
		ActorKind  string `json:"actor_kind"`
		ActorID    string `json:"actor_id"`
		TargetKind string `json:"target_kind"`
		TargetID   string `json:"target_id"`
		Body       string `json:"body"`
	}{params.Actor.Kind, params.Actor.ID, params.Target.Kind, params.Target.ID, params.Body})
	if err != nil {
		return Message{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, fmt.Errorf("begin message send: %w", err)
	}
	defer tx.Rollback()
	if receipt, found, err := readCollaborationReceipt(ctx, tx, params.RequestID, operationSendMessage, fingerprint); err != nil {
		return Message{}, err
	} else if found {
		return commitMessageReplay(tx, receipt.ResultID)
	}

	spaceID, err := resolveSendTargetSpace(ctx, tx, params.Target)
	if err != nil {
		return Message{}, denyCollaboration(ctx, tx, params.Actor, AuditMessageSend, "message", params.Target.ID,
			params.RequestID, "target_invalid", params.Now, err)
	}
	if err := requireCollaborationGrant(ctx, tx, params.Actor, CapabilityMessageSend,
		Scope{Kind: "space", ID: spaceID}, AuditMessageSend, "space", spaceID,
		params.RequestID, params.Now); err != nil {
		return Message{}, err
	}
	space, err := loadMutationSpace(ctx, tx, params.Actor, spaceID)
	if err != nil {
		return Message{}, denyCollaboration(ctx, tx, params.Actor, AuditMessageSend, "space", spaceID,
			params.RequestID, "space_unavailable", params.Now, err)
	}
	if space.ArchivedAt != nil {
		return Message{}, denyCollaboration(ctx, tx, params.Actor, AuditMessageSend, "space", spaceID,
			params.RequestID, "space_archived", params.Now, ErrSpaceArchived)
	}

	createdThread := false
	if params.Target.Kind == MessageTargetThread {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO threads(id, space_id, created_at)
			VALUES(?, ?, ?)
			ON CONFLICT(id) DO NOTHING
		`, params.Target.ID, spaceID, unixNano(params.Now))
		if err != nil {
			return Message{}, fmt.Errorf("persist thread fact: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil {
			return Message{}, fmt.Errorf("read thread creation result: %w", err)
		} else {
			createdThread = affected == 1
		}
	}
	sequence, err := allocateTargetSequence(ctx, tx, params.Target)
	if err != nil {
		return Message{}, err
	}
	messageID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO messages(
			id, request_id, payload_fingerprint, space_id, target_kind, target_id, target_sequence,
			author_kind, author_id, body, created_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, messageID, params.RequestID, fingerprint[:], spaceID, params.Target.Kind, params.Target.ID, sequence,
		params.Actor.Kind, params.Actor.ID, params.Body, unixNano(params.Now)); err != nil {
		return Message{}, fmt.Errorf("persist message: %w", err)
	}
	if err := persistCollaborationReceipt(ctx, tx, params.RequestID, operationSendMessage, fingerprint, messageID, params.Now); err != nil {
		return Message{}, err
	}
	if createdThread {
		if err := appendAuditEvent(ctx, tx, AppendAuditParams{
			OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: AuditThreadCreate,
			TargetKind: "thread", TargetID: params.Target.ID, RequestID: params.RequestID,
			Outcome: "committed", Now: params.Now,
		}); err != nil {
			return Message{}, err
		}
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: AuditMessageSend,
		TargetKind: "message", TargetID: messageID, RequestID: params.RequestID,
		Outcome: "committed", Now: params.Now,
	}); err != nil {
		return Message{}, err
	}
	message, err := scanMessage(tx.QueryRowContext(ctx, messageSelect+` WHERE id = ?`, messageID))
	if err != nil {
		return Message{}, fmt.Errorf("read sent message: %w", err)
	}
	message.Author.OrganizationID = params.Actor.OrganizationID
	if err := tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("commit message send: %w", err)
	}
	return message, nil
}

func (s *Store) GetMessage(ctx context.Context, params GetMessageParams) (Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, fmt.Errorf("begin message read: %w", err)
	}
	defer tx.Rollback()
	message, err := scanMessage(tx.QueryRowContext(ctx, messageSelect+` WHERE id = ?`, params.MessageID))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrMessageNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("read message: %w", err)
	}
	space, err := requireReadableSpace(ctx, tx, params.Actor, message.SpaceID, params.Now)
	if err != nil {
		return Message{}, err
	}
	message.Author.OrganizationID = space.OrganizationID
	if err := tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("commit message read: %w", err)
	}
	return message, nil
}

func (s *Store) GetThread(ctx context.Context, params GetThreadParams) (Thread, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Thread{}, fmt.Errorf("begin thread read: %w", err)
	}
	defer tx.Rollback()
	thread, err := scanThread(tx.QueryRowContext(ctx, `SELECT id, space_id, created_at FROM threads WHERE id = ?`, params.ThreadID))
	if errors.Is(err, sql.ErrNoRows) {
		return Thread{}, ErrThreadNotFound
	}
	if err != nil {
		return Thread{}, fmt.Errorf("read thread: %w", err)
	}
	if _, err := requireReadableSpace(ctx, tx, params.Actor, thread.SpaceID, params.Now); err != nil {
		return Thread{}, err
	}
	if err := tx.Commit(); err != nil {
		return Thread{}, fmt.Errorf("commit thread read: %w", err)
	}
	return thread, nil
}

func (s *Store) ListMessages(ctx context.Context, params ListMessagesParams) ([]Message, error) {
	if params.Limit == 0 || params.Limit > maxMessageListLimit {
		return nil, ErrInvalidMessageLimit
	}
	if params.Target.Kind != MessageTargetSpace && params.Target.Kind != MessageTargetThread {
		return nil, ErrInvalidMessageTarget
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin message list: %w", err)
	}
	defer tx.Rollback()
	spaceID, err := resolveReadableTargetSpace(ctx, tx, params.Target)
	if err != nil {
		return nil, err
	}
	space, err := requireReadableSpace(ctx, tx, params.Actor, spaceID, params.Now)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, messageSelect+`
		WHERE target_kind = ? AND target_id = ? AND target_sequence > ?
		ORDER BY target_sequence
		LIMIT ?
	`, params.Target.Kind, params.Target.ID, params.AfterSequence, params.Limit)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	var messages []Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan message: %w", err)
		}
		message.Author.OrganizationID = space.OrganizationID
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close messages: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit message list: %w", err)
	}
	return messages, nil
}

func resolveSendTargetSpace(ctx context.Context, tx *sql.Tx, target MessageTarget) (string, error) {
	switch target.Kind {
	case MessageTargetSpace:
		return target.ID, nil
	case MessageTargetThread:
		var spaceID string
		err := tx.QueryRowContext(ctx, `
			SELECT space_id
			FROM messages
			WHERE id = ? AND target_kind = 'space' AND target_id = space_id
		`, target.ID).Scan(&spaceID)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrInvalidMessageTarget
		}
		if err != nil {
			return "", fmt.Errorf("resolve thread root: %w", err)
		}
		return spaceID, nil
	default:
		return "", ErrInvalidMessageTarget
	}
}

func resolveReadableTargetSpace(ctx context.Context, tx *sql.Tx, target MessageTarget) (string, error) {
	if target.Kind == MessageTargetSpace {
		return target.ID, nil
	}
	if target.Kind != MessageTargetThread {
		return "", ErrInvalidMessageTarget
	}
	var spaceID string
	err := tx.QueryRowContext(ctx, `SELECT space_id FROM threads WHERE id = ?`, target.ID).Scan(&spaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrThreadNotFound
	}
	if err != nil {
		return "", fmt.Errorf("resolve readable thread: %w", err)
	}
	return spaceID, nil
}

func allocateTargetSequence(ctx context.Context, tx *sql.Tx, target MessageTarget) (uint64, error) {
	var sequence uint64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO message_target_sequences(target_kind, target_id, next_sequence)
		VALUES(?, ?, 2)
		ON CONFLICT(target_kind, target_id)
		DO UPDATE SET next_sequence = next_sequence + 1
		RETURNING next_sequence - 1
	`, target.Kind, target.ID).Scan(&sequence); err != nil {
		return 0, fmt.Errorf("allocate message target sequence: %w", err)
	}
	return sequence, nil
}

func commitMessageReplay(tx *sql.Tx, messageID string) (Message, error) {
	message, err := scanMessage(tx.QueryRow(messageSelect+` WHERE id = ?`, messageID))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrCollaborationIntegrity
	}
	if err != nil {
		return Message{}, fmt.Errorf("read message request result: %w", err)
	}
	var organizationID string
	if err := tx.QueryRow(`SELECT organization_id FROM spaces WHERE id = ?`, message.SpaceID).Scan(&organizationID); err != nil {
		return Message{}, fmt.Errorf("read replayed message organization: %w", err)
	}
	message.Author.OrganizationID = organizationID
	if err := tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("commit message request replay: %w", err)
	}
	return message, nil
}

func scanThread(row scanner) (Thread, error) {
	var thread Thread
	var createdAt int64
	if err := row.Scan(&thread.ID, &thread.SpaceID, &createdAt); err != nil {
		return Thread{}, err
	}
	thread.CreatedAt = timeFromUnixNano(createdAt)
	return thread, nil
}
