package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

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

func readInboxRequestReceipt(ctx context.Context, tx *sql.Tx, requestID, agentID, operation string, fingerprint [sha256.Size]byte) ([]byte, bool, error) {
	var storedAgentID, storedOperation string
	var storedFingerprint, snapshot []byte
	err := tx.QueryRowContext(ctx, `
		SELECT agent_id, operation, payload_fingerprint, response_snapshot
		FROM agent_requests WHERE request_id = ?
	`, requestID).Scan(&storedAgentID, &storedOperation, &storedFingerprint, &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read inbox request receipt: %w", err)
	}
	if storedAgentID != agentID || storedOperation != operation || !bytes.Equal(storedFingerprint, fingerprint[:]) {
		return nil, false, ErrInboxRequestConflict
	}
	return snapshot, true, nil
}

func replayInboxItemRequest(ctx context.Context, tx *sql.Tx, requestID, agentID, operation string, fingerprint [sha256.Size]byte, current InboxItem) (InboxItem, bool, error) {
	snapshot, found, err := readInboxRequestReceipt(ctx, tx, requestID, agentID, operation, fingerprint)
	if err != nil || !found {
		return InboxItem{}, found, err
	}
	var receipt inboxItemRequestReceipt
	if err := decodeInboxRequestReceipt(snapshot, &receipt); err != nil {
		return InboxItem{}, false, err
	}
	if err := validateInboxItemReceipt(ctx, tx, agentID, current, receipt.Item); err != nil {
		return InboxItem{}, false, err
	}
	return receipt.Item, true, nil
}

func replayInboxPreferenceRequest(ctx context.Context, tx *sql.Tx, requestID, agentID, operation string, fingerprint [sha256.Size]byte) (InboxPreferenceResult, bool, error) {
	snapshot, found, err := readInboxRequestReceipt(ctx, tx, requestID, agentID, operation, fingerprint)
	if err != nil || !found {
		return InboxPreferenceResult{}, found, err
	}
	var receipt inboxPreferenceRequestReceipt
	if err := decodeInboxRequestReceipt(snapshot, &receipt); err != nil || receipt.CommittedAt.IsZero() {
		return InboxPreferenceResult{}, false, ErrInboxIntegrity
	}
	return InboxPreferenceResult(receipt), true, nil
}

func replayInboxSendRequest(ctx context.Context, tx *sql.Tx, requestID, agentID, operation string, fingerprint [sha256.Size]byte, item InboxItem) (SendInboxReplyResult, bool, error) {
	snapshot, found, err := readInboxRequestReceipt(ctx, tx, requestID, agentID, operation, fingerprint)
	if err != nil || !found {
		return SendInboxReplyResult{}, found, err
	}
	var receipt inboxSendRequestReceipt
	if err := decodeInboxRequestReceipt(snapshot, &receipt); err != nil || receipt.InboxItemID != item.ID || receipt.CommittedAt.IsZero() {
		return SendInboxReplyResult{}, false, ErrInboxIntegrity
	}
	result, err := rehydrateInboxResult(ctx, tx, requestID, agentID, receipt.Kind, receipt.Message, receipt.HeldDraft)
	if err != nil {
		return SendInboxReplyResult{}, false, err
	}
	result.CommittedAt = receipt.CommittedAt
	return result, true, nil
}

func replayInboxResolveRequest(ctx context.Context, tx *sql.Tx, requestID, agentID, operation string, fingerprint [sha256.Size]byte, source HeldDraft, item InboxItem) (ResolveHeldDraftResult, bool, error) {
	snapshot, found, err := readInboxRequestReceipt(ctx, tx, requestID, agentID, operation, fingerprint)
	if err != nil || !found {
		return ResolveHeldDraftResult{}, found, err
	}
	var receipt inboxResolveRequestReceipt
	if err := decodeInboxRequestReceipt(snapshot, &receipt); err != nil || receipt.SourceDraftID != source.ID || receipt.CommittedAt.IsZero() {
		return ResolveHeldDraftResult{}, false, ErrInboxIntegrity
	}
	if receipt.Action != DraftResolutionRetry && receipt.Action != DraftResolutionCancel && receipt.Action != DraftResolutionRetarget {
		return ResolveHeldDraftResult{}, false, ErrInboxIntegrity
	}
	if err := validateInboxItemReceipt(ctx, tx, agentID, item, receipt.InboxItem); err != nil {
		return ResolveHeldDraftResult{}, false, err
	}
	result := ResolveHeldDraftResult{Action: receipt.Action, InboxItem: receipt.InboxItem, CommittedAt: receipt.CommittedAt}
	if receipt.Action == DraftResolutionCancel {
		if receipt.Kind != "" || receipt.Message != nil || receipt.HeldDraft != nil {
			return ResolveHeldDraftResult{}, false, ErrInboxIntegrity
		}
		return result, true, nil
	}
	rehydrated, err := rehydrateInboxResult(ctx, tx, requestID, agentID, receipt.Kind, receipt.Message, receipt.HeldDraft)
	if err != nil {
		return ResolveHeldDraftResult{}, false, err
	}
	result.Kind = rehydrated.Kind
	result.Message = rehydrated.Message
	result.HeldDraft = rehydrated.HeldDraft
	return result, true, nil
}

func decodeInboxRequestReceipt(snapshot []byte, receipt any) error {
	if err := json.Unmarshal(snapshot, receipt); err != nil {
		return ErrInboxIntegrity
	}
	return nil
}

func newInboxSendRequestReceipt(itemID string, result SendInboxReplyResult) (inboxSendRequestReceipt, error) {
	receipt := inboxSendRequestReceipt{InboxItemID: itemID, Kind: result.Kind, CommittedAt: result.CommittedAt}
	switch result.Kind {
	case InboxResultMessage:
		if result.Message == nil || result.HeldDraft != nil {
			return inboxSendRequestReceipt{}, ErrInboxIntegrity
		}
		message := newInboxMessageReference(*result.Message)
		receipt.Message = &message
	case InboxResultHeldDraft:
		if result.Message != nil || result.HeldDraft == nil {
			return inboxSendRequestReceipt{}, ErrInboxIntegrity
		}
		draft := newInboxHeldDraftReceipt(*result.HeldDraft)
		receipt.HeldDraft = &draft
	default:
		return inboxSendRequestReceipt{}, ErrInboxIntegrity
	}
	if receipt.CommittedAt.IsZero() {
		return inboxSendRequestReceipt{}, ErrInboxIntegrity
	}
	return receipt, nil
}

func newInboxResolveRequestReceipt(sourceDraftID string, result ResolveHeldDraftResult) (inboxResolveRequestReceipt, error) {
	receipt := inboxResolveRequestReceipt{
		SourceDraftID: sourceDraftID,
		Action:        result.Action,
		Kind:          result.Kind,
		InboxItem:     result.InboxItem,
		CommittedAt:   result.CommittedAt,
	}
	if receipt.CommittedAt.IsZero() || receipt.InboxItem.ID == "" {
		return inboxResolveRequestReceipt{}, ErrInboxIntegrity
	}
	if result.Action == DraftResolutionCancel {
		if result.Kind != "" || result.Message != nil || result.HeldDraft != nil {
			return inboxResolveRequestReceipt{}, ErrInboxIntegrity
		}
		return receipt, nil
	}
	switch result.Kind {
	case InboxResultMessage:
		if result.Message == nil || result.HeldDraft != nil {
			return inboxResolveRequestReceipt{}, ErrInboxIntegrity
		}
		message := newInboxMessageReference(*result.Message)
		receipt.Message = &message
	case InboxResultHeldDraft:
		if result.Message != nil || result.HeldDraft == nil {
			return inboxResolveRequestReceipt{}, ErrInboxIntegrity
		}
		draft := newInboxHeldDraftReceipt(*result.HeldDraft)
		receipt.HeldDraft = &draft
	default:
		return inboxResolveRequestReceipt{}, ErrInboxIntegrity
	}
	return receipt, nil
}

func newInboxMessageReference(message Message) inboxMessageReference {
	return inboxMessageReference{
		ID:             message.ID,
		AgentID:        message.Author.ID,
		SpaceID:        message.SpaceID,
		Target:         message.Target,
		TargetSequence: message.TargetSequence,
		MentionCount:   len(message.MentionedAgentIDs),
		CreatedAt:      message.CreatedAt,
	}
}

func newInboxHeldDraftReceipt(draft HeldDraft) inboxHeldDraftReceipt {
	return inboxHeldDraftReceipt{
		Sequence:            draft.Sequence,
		ID:                  draft.ID,
		AgentID:             draft.AgentID,
		InboxItemID:         draft.InboxItemID,
		PredecessorDraftID:  draft.PredecessorDraftID,
		SpaceID:             draft.SpaceID,
		Target:              draft.Target,
		BasisTargetSequence: draft.BasisTargetSequence,
		MentionCount:        len(draft.MentionedAgentIDs),
		HeldReason:          draft.HeldReason,
		State:               draft.State,
		ResolutionAction:    draft.ResolutionAction,
		ResultKind:          draft.ResultKind,
		ResultID:            draft.ResultID,
		CreatedAt:           draft.CreatedAt,
		UpdatedAt:           draft.UpdatedAt,
	}
}

func rehydrateInboxResult(ctx context.Context, tx *sql.Tx, requestID, agentID, kind string, messageReference *inboxMessageReference, draftReceipt *inboxHeldDraftReceipt) (SendInboxReplyResult, error) {
	switch kind {
	case InboxResultMessage:
		if messageReference == nil || draftReceipt != nil {
			return SendInboxReplyResult{}, ErrInboxIntegrity
		}
		message, err := rehydrateInboxMessage(ctx, tx, requestID, agentID, *messageReference)
		if err != nil {
			return SendInboxReplyResult{}, err
		}
		return SendInboxReplyResult{Kind: kind, Message: &message}, nil
	case InboxResultHeldDraft:
		if messageReference != nil || draftReceipt == nil {
			return SendInboxReplyResult{}, ErrInboxIntegrity
		}
		draft, err := rehydrateInboxHeldDraft(ctx, tx, agentID, *draftReceipt)
		if err != nil {
			return SendInboxReplyResult{}, err
		}
		return SendInboxReplyResult{Kind: kind, HeldDraft: &draft}, nil
	default:
		return SendInboxReplyResult{}, ErrInboxIntegrity
	}
}

func rehydrateInboxMessage(ctx context.Context, tx *sql.Tx, requestID, agentID string, reference inboxMessageReference) (Message, error) {
	message, err := scanMessage(tx.QueryRowContext(ctx, messageSelect+` WHERE id = ?`, reference.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrInboxIntegrity
	}
	if err != nil {
		return Message{}, fmt.Errorf("read inbox message result: %w", err)
	}
	if message.ID != reference.ID || message.RequestID != requestID || message.Author.Kind != "agent" || message.Author.ID != agentID ||
		reference.AgentID != agentID || message.SpaceID != reference.SpaceID || message.Target != reference.Target ||
		message.TargetSequence != reference.TargetSequence || !message.CreatedAt.Equal(reference.CreatedAt) {
		return Message{}, ErrInboxIntegrity
	}
	var organizationID string
	if err := tx.QueryRowContext(ctx, `SELECT organization_id FROM spaces WHERE id = ?`, message.SpaceID).Scan(&organizationID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Message{}, ErrInboxIntegrity
		}
		return Message{}, fmt.Errorf("read inbox message organization: %w", err)
	}
	mentions, err := messageMentions(ctx, tx, message.ID)
	if err != nil {
		return Message{}, err
	}
	if len(mentions) != reference.MentionCount {
		return Message{}, ErrInboxIntegrity
	}
	message.Author.OrganizationID = organizationID
	message.MentionedAgentIDs = mentions
	return message, nil
}

func rehydrateInboxHeldDraft(ctx context.Context, tx *sql.Tx, agentID string, receipt inboxHeldDraftReceipt) (HeldDraft, error) {
	draft, err := scanHeldDraft(tx.QueryRowContext(ctx, heldDraftSelect+` WHERE id = ?`, receipt.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return HeldDraft{}, ErrInboxIntegrity
	}
	if err != nil {
		return HeldDraft{}, fmt.Errorf("read inbox held draft result: %w", err)
	}
	if draft.Sequence != receipt.Sequence || draft.ID != receipt.ID || draft.AgentID != agentID || receipt.AgentID != agentID ||
		draft.InboxItemID != receipt.InboxItemID || draft.PredecessorDraftID != receipt.PredecessorDraftID ||
		draft.SpaceID != receipt.SpaceID || draft.Target != receipt.Target || draft.BasisTargetSequence != receipt.BasisTargetSequence ||
		draft.HeldReason != receipt.HeldReason || !draft.CreatedAt.Equal(receipt.CreatedAt) {
		return HeldDraft{}, ErrInboxIntegrity
	}
	mentions, err := heldDraftMentions(ctx, tx, draft.ID)
	if err != nil {
		return HeldDraft{}, err
	}
	if len(mentions) != receipt.MentionCount {
		return HeldDraft{}, ErrInboxIntegrity
	}
	draft.State = receipt.State
	draft.ResolutionAction = receipt.ResolutionAction
	draft.ResultKind = receipt.ResultKind
	draft.ResultID = receipt.ResultID
	draft.UpdatedAt = receipt.UpdatedAt
	draft.MentionedAgentIDs = mentions
	return draft, nil
}

func validateInboxItemReceipt(ctx context.Context, tx *sql.Tx, agentID string, current, snapshot InboxItem) error {
	if current.ID != snapshot.ID || current.AgentID != agentID || snapshot.AgentID != agentID ||
		current.Sequence != snapshot.Sequence || current.SpaceID != snapshot.SpaceID || current.Target != snapshot.Target ||
		current.TriggerMessageID != snapshot.TriggerMessageID || current.TriggerTargetSequence != snapshot.TriggerTargetSequence ||
		current.Reason != snapshot.Reason || !current.CreatedAt.Equal(snapshot.CreatedAt) {
		return ErrInboxIntegrity
	}
	message, err := scanMessage(tx.QueryRowContext(ctx, messageSelect+` WHERE id = ?`, snapshot.TriggerMessageID))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInboxIntegrity
	}
	if err != nil {
		return fmt.Errorf("read inbox trigger message: %w", err)
	}
	if message.ID != snapshot.TriggerMessageID || message.SpaceID != snapshot.SpaceID || message.Target != snapshot.Target || message.TargetSequence != snapshot.TriggerTargetSequence {
		return ErrInboxIntegrity
	}
	return nil
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
