package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	"github.com/google/uuid"
)

const operationSendMessage = "message.send"

type SendMessageParams = collaborationapp.SendMessageCommand

type GetMessageParams = collaborationapp.GetMessageQuery

type GetThreadParams = collaborationapp.GetThreadQuery

type ListMessagesParams = collaborationapp.ListMessagesQuery

func (s *Store) SendMessage(ctx context.Context, params SendMessageParams) (Message, error) {
	if err := validateMessageBody(params.Body); err != nil {
		return Message{}, err
	}
	if err := validateMessageTarget(params.Target); err != nil {
		return Message{}, err
	}
	mentions, err := canonicalMentionPrincipals(params.MentionedPrincipals)
	if err != nil {
		return Message{}, err
	}
	fingerprint, err := collaborationFingerprint(struct {
		ActorKind  PrincipalKind `json:"actor_kind"`
		ActorID    string        `json:"actor_id"`
		TargetKind string        `json:"target_kind"`
		TargetID   string        `json:"target_id"`
		Body       string        `json:"body"`
		Mentions   []Principal   `json:"mentioned_principals,omitempty"`
	}{params.Actor.Kind, params.Actor.ID, string(params.Target.Kind), params.Target.ID, params.Body, mentions})
	if err != nil {
		return Message{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, fmt.Errorf("begin message send: %w", err)
	}
	defer tx.Rollback()
	actor, err := recheckMessageActor(ctx, tx, params.Actor, params.Runtime, params.Now)
	if err != nil {
		if params.Actor.Kind == PrincipalHuman && params.Actor.Valid() {
			return Message{}, denyCollaboration(ctx, tx, params.Actor, AuditMessageSend, "message", params.Target.ID,
				params.RequestID, "principal_inactive", params.Now, err)
		}
		return Message{}, err
	}
	params.Actor = actor
	if receipt, found, err := readCollaborationReceipt(ctx, tx, params.RequestID, operationSendMessage, fingerprint); err != nil {
		return Message{}, err
	} else if found {
		return commitMessageReplay(ctx, tx, receipt.ResultID)
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
	if err := collaborationSpace(space).ValidateMessageSend(); err != nil {
		return Message{}, denyCollaboration(ctx, tx, params.Actor, AuditMessageSend, "space", spaceID,
			params.RequestID, "space_archived", params.Now, err)
	}
	if err := validateMentionMembers(ctx, tx, spaceID, mentions); err != nil {
		return Message{}, denyCollaboration(ctx, tx, params.Actor, AuditMessageSend, "space", spaceID,
			params.RequestID, "mention_invalid", params.Now, err)
	}
	message, _, err := publishMessageTx(ctx, tx, params.Actor, params.Target, params.Body, mentions, params.RequestID, fingerprint, params.Now)
	if err != nil {
		return Message{}, err
	}
	if err := persistCollaborationReceipt(ctx, tx, params.RequestID, operationSendMessage, fingerprint, message.ID, params.Now); err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("commit message send: %w", err)
	}
	return message, nil
}

func publishMessageTx(ctx context.Context, tx *sql.Tx, actor Principal, target MessageTarget, body string, mentions []Principal, requestID string, fingerprint [sha256.Size]byte, now time.Time) (Message, []EligibleInboxTrigger, error) {
	spaceID, err := resolveSendTargetSpace(ctx, tx, target)
	if err != nil {
		return Message{}, nil, err
	}
	createdThread := false
	if target.Kind == MessageTargetThread {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO threads(id, space_id, created_at)
			VALUES(?, ?, ?)
			ON CONFLICT(id) DO NOTHING
		`, target.ID, spaceID, unixNano(now))
		if err != nil {
			return Message{}, nil, fmt.Errorf("persist thread fact: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return Message{}, nil, fmt.Errorf("read thread creation result: %w", err)
		}
		createdThread = affected == 1
	}
	sequence, err := allocateTargetSequence(ctx, tx, target)
	if err != nil {
		return Message{}, nil, err
	}
	messageID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO messages(
			id, request_id, payload_fingerprint, space_id, target_kind, target_id, target_sequence,
			author_kind, author_id, body, created_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, messageID, requestID, fingerprint[:], spaceID, target.Kind, target.ID, sequence,
		actor.Kind, actor.ID, body, unixNano(now)); err != nil {
		return Message{}, nil, fmt.Errorf("persist message: %w", err)
	}
	for ordinal, mention := range mentions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO message_mentions(message_id, principal_kind, principal_id, ordinal) VALUES(?, ?, ?, ?)`, messageID, mention.Kind, mention.ID, ordinal); err != nil {
			return Message{}, nil, fmt.Errorf("persist message mention: %w", err)
		}
	}
	if createdThread {
		if err := appendAuditEvent(ctx, tx, AppendAuditParams{
			OrganizationID: actor.OrganizationID, Actor: actor, Action: AuditThreadCreate,
			TargetKind: "thread", TargetID: target.ID, RequestID: requestID,
			ContextKind: "space", ContextID: spaceID, Outcome: "committed", Now: now,
		}); err != nil {
			return Message{}, nil, err
		}
	}
	contextKind, contextID := "space", spaceID
	if target.Kind == MessageTargetThread {
		contextKind, contextID = "thread", target.ID
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: actor.OrganizationID, Actor: actor, Action: AuditMessageSend,
		TargetKind: "message", TargetID: messageID, RequestID: requestID,
		ContextKind: contextKind, ContextID: contextID, Outcome: "committed", Now: now,
	}); err != nil {
		return Message{}, nil, err
	}
	message, err := scanMessage(tx.QueryRowContext(ctx, messageSelect+` WHERE id = ?`, messageID))
	if err != nil {
		return Message{}, nil, fmt.Errorf("read sent message: %w", err)
	}
	message.Author.OrganizationID = actor.OrganizationID
	message.MentionedPrincipals = append([]Principal(nil), mentions...)
	for index := range message.MentionedPrincipals {
		message.MentionedPrincipals[index].OrganizationID = actor.OrganizationID
	}
	if _, err := enqueueKnowledgeDirtySource(ctx, tx, KnowledgeSource{Kind: KnowledgeSourceMessage, ID: message.ID}, KnowledgeMessageRevision(message.ID, message.TargetSequence), now); err != nil {
		return Message{}, nil, err
	}
	created, err := projectMessageAttention(ctx, tx, message, mentions, now)
	if err != nil {
		return Message{}, nil, err
	}
	return message, created, nil
}

func (s *Store) GetMessage(ctx context.Context, params GetMessageParams) (Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, fmt.Errorf("begin message read: %w", err)
	}
	defer tx.Rollback()
	actor, err := recheckMessageActor(ctx, tx, params.Actor, params.Runtime, params.Now)
	if err != nil {
		return Message{}, err
	}
	params.Actor = actor
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
	mentions, err := messageMentions(ctx, tx, message.ID)
	if err != nil {
		return Message{}, err
	}
	for index := range mentions {
		mentions[index].OrganizationID = space.OrganizationID
	}
	message.MentionedPrincipals = mentions
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
	actor, err := recheckMessageActor(ctx, tx, params.Actor, params.Runtime, params.Now)
	if err != nil {
		return Thread{}, err
	}
	params.Actor = actor
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
	actor, err := recheckMessageActor(ctx, tx, params.Actor, params.Runtime, params.Now)
	if err != nil {
		return nil, err
	}
	params.Actor = actor
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
	if err := loadMessageMentions(ctx, tx, messages); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit message list: %w", err)
	}
	return messages, nil
}

func recheckMessageActor(ctx context.Context, tx *sql.Tx, actor Principal, runtime AgentRuntimeAuthentication, now time.Time) (Principal, error) {
	switch actor.Kind {
	case PrincipalHuman:
		if runtime.Valid() || now.IsZero() || validatePrincipalInOrganization(ctx, tx, actor, actor.OrganizationID) != nil {
			return Principal{}, ErrPermissionDenied
		}
		return actor, nil
	case PrincipalAgent:
		if !runtime.Valid() || runtime.Principal != actor {
			return Principal{}, ErrAgentRuntimeUnauthenticated
		}
		current, err := requireAgentRuntimeSession(ctx, tx, runtime.Proof, now)
		if err != nil || current.Principal != actor {
			if err != nil {
				return Principal{}, err
			}
			return Principal{}, ErrAgentRuntimeUnauthenticated
		}
		return current.Principal, nil
	default:
		return Principal{}, ErrPermissionDenied
	}
}

func resolveSendTargetSpace(ctx context.Context, tx *sql.Tx, target MessageTarget) (string, error) {
	if err := validateMessageTarget(target); err != nil {
		return "", err
	}
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
	}
	return "", ErrInvalidMessageTarget
}

func resolveReadableTargetSpace(ctx context.Context, tx *sql.Tx, target MessageTarget) (string, error) {
	if err := validateMessageTarget(target); err != nil {
		return "", err
	}
	if target.Kind == MessageTargetSpace {
		return target.ID, nil
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

func commitMessageReplay(ctx context.Context, tx *sql.Tx, messageID string) (Message, error) {
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
	mentions, err := messageMentions(ctx, tx, message.ID)
	if err != nil {
		return Message{}, err
	}
	for index := range mentions {
		mentions[index].OrganizationID = organizationID
	}
	message.MentionedPrincipals = mentions
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

func loadMessageMentions(ctx context.Context, tx *sql.Tx, messages []Message) error {
	for index := range messages {
		mentions, err := messageMentions(ctx, tx, messages[index].ID)
		if err != nil {
			return err
		}
		for mentionIndex := range mentions {
			mentions[mentionIndex].OrganizationID = messages[index].Author.OrganizationID
		}
		messages[index].MentionedPrincipals = mentions
	}
	return nil
}

func messageMentions(ctx context.Context, tx *sql.Tx, messageID string) ([]Principal, error) {
	rows, err := tx.QueryContext(ctx, `SELECT principal_kind, principal_id FROM message_mentions WHERE message_id = ? ORDER BY ordinal`, messageID)
	if err != nil {
		return nil, fmt.Errorf("list message mentions: %w", err)
	}
	defer rows.Close()
	var mentions []Principal
	for rows.Next() {
		var principal Principal
		if err := rows.Scan(&principal.Kind, &principal.ID); err != nil {
			return nil, fmt.Errorf("scan message mention: %w", err)
		}
		mentions = append(mentions, principal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate message mentions: %w", err)
	}
	return mentions, nil
}
