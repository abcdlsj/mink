package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	executiondomain "github.com/abcdlsj/sumi/internal/execution/domain"
)

func (s *Store) CompleteRun(ctx context.Context, params CompleteRunParams) (CompleteRunResult, error) {
	if err := executiondomain.ValidateOutcome(params.Outcome); err != nil {
		return CompleteRunResult{}, err
	}
	if params.Outcome == executiondomain.OutcomeSucceeded && params.ErrorCode != "" {
		return CompleteRunResult{}, ErrRunInvalidOutcome
	}
	if params.Outcome == executiondomain.OutcomeFailed && strings.TrimSpace(params.ErrorCode) == "" {
		return CompleteRunResult{}, ErrRunInvalidOutcome
	}
	if len(params.ErrorCode) > 255 {
		return CompleteRunResult{}, ErrRunInvalidOutcome
	}
	mentions, err := canonicalMentionPrincipals(params.MentionedPrincipals)
	if err != nil {
		return CompleteRunResult{}, err
	}
	fingerprint, err := runFingerprint(struct {
		OutboxEventID string      `json:"outbox_event_id"`
		RunID         string      `json:"run_id"`
		Attempt       uint64      `json:"attempt"`
		Fence         uint64      `json:"fence"`
		Outcome       string      `json:"outcome"`
		ErrorCode     string      `json:"error_code,omitempty"`
		Body          string      `json:"body"`
		Mentions      []Principal `json:"mentions,omitempty"`
		Usage         RunUsage    `json:"usage"`
	}{params.OutboxEventID, params.RunID, params.Attempt, params.Fence, string(params.Outcome), params.ErrorCode, params.Body, mentions, params.Usage})
	if err != nil {
		return CompleteRunResult{}, err
	}
	tx, authentication, err := s.beginRunTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return CompleteRunResult{}, err
	}
	defer tx.Rollback()
	run, err := requireOwnedRun(ctx, tx, authentication.Principal.ID, params.RunID)
	if err != nil {
		return CompleteRunResult{}, err
	}

	var receipt runCompleteRequestReceipt
	if found, err := readRunRequest(ctx, tx, params.RequestID, run.AgentID, operationCompleteRun, fingerprint, &receipt); err != nil {
		return CompleteRunResult{}, err
	} else if found {
		result, err := rehydrateRunCompletion(ctx, tx, params.RequestID, run.AgentID, receipt)
		if err != nil {
			return CompleteRunResult{}, err
		}
		return commitRunReplay(tx, result)
	}
	if err := rejectReusedCompletionEvent(ctx, tx, params.OutboxEventID, params.RequestID, fingerprint); err != nil {
		return CompleteRunResult{}, err
	}
	if err := executiondomain.ValidateLease(executionRun(run), authentication.Proof.ComputerID(),
		authentication.Proof.PlacementDesiredRevision(), params.Attempt, params.Fence, params.Now); err != nil {
		return CompleteRunResult{}, err
	}
	item, err := runInboxItem(ctx, tx, run)
	if err != nil {
		return CompleteRunResult{}, err
	}
	item.Recipient.OrganizationID = authentication.Principal.OrganizationID
	space, err := requireRunReplyAccess(ctx, tx, authentication.Principal, item, params.Now)
	if err != nil {
		return CompleteRunResult{}, err
	}
	if item.State != InboxStateClaimed {
		return CompleteRunResult{}, ErrInboxItemNotClaimed
	}
	if space.ArchivedAt != nil {
		return CompleteRunResult{}, ErrSpaceArchived
	}
	if err := validateMentionMembers(ctx, tx, space.ID, mentions); err != nil {
		return CompleteRunResult{}, err
	}
	sendResult, err := sendOrHoldInboxReplyTx(
		ctx, tx, authentication.Principal, item, "", item.Target, run.InputBasisTargetSequence,
		params.Body, mentions, params.RequestID, fingerprint, params.Now,
	)
	if err != nil {
		return CompleteRunResult{}, err
	}
	state := RunStateSucceeded
	if params.Outcome == executiondomain.OutcomeFailed {
		state = RunStateFailed
	}
	resultID := ""
	switch sendResult.Kind {
	case InboxResultMessage:
		resultID = sendResult.Message.ID
	case InboxResultHeldDraft:
		resultID = sendResult.HeldDraft.ID
	default:
		return CompleteRunResult{}, ErrRunIntegrity
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE runs SET state = ?, result_kind = ?, result_id = ?, usage_input_units = ?,
			usage_output_units = ?, error_code = ?, completed_at = ?
		WHERE id = ?
	`, state, sendResult.Kind, resultID, params.Usage.InputUnits, params.Usage.OutputUnits,
		params.ErrorCode, unixNano(params.Now), run.ID); err != nil {
		return CompleteRunResult{}, fmt.Errorf("complete run: %w", err)
	}
	run, err = runByID(ctx, tx, run.ID)
	if err != nil {
		return CompleteRunResult{}, err
	}
	result := CompleteRunResult{
		Run: run, Kind: sendResult.Kind, Message: sendResult.Message,
		HeldDraft: sendResult.HeldDraft, CommittedAt: params.Now.UTC(),
	}
	inboxReceipt, err := newInboxSendRequestReceipt(item.ID, sendResult)
	if err != nil {
		return CompleteRunResult{}, err
	}
	receipt = runCompleteRequestReceipt{OutboxEventID: params.OutboxEventID, Run: run, Result: inboxReceipt}
	if err := persistRunRequest(ctx, tx, params.RequestID, run.AgentID, operationCompleteRun, fingerprint, run.ID, receipt, params.Now); err != nil {
		return CompleteRunResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO run_completion_receipts(
			outbox_event_id, request_id, payload_fingerprint, run_id, attempt, fence,
			result_kind, result_id, committed_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, params.OutboxEventID, params.RequestID, fingerprint[:], run.ID, run.Attempt, run.Fence,
		run.ResultKind, run.ResultID, unixNano(params.Now)); err != nil {
		if isUniqueConstraint(err, "run_completion_receipts") {
			return CompleteRunResult{}, ErrRunCompletionConflict
		}
		return CompleteRunResult{}, fmt.Errorf("persist run completion receipt: %w", err)
	}
	if err := appendRunAudit(ctx, tx, authentication, AuditRunComplete, run, params.RequestID, params.Now); err != nil {
		return CompleteRunResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CompleteRunResult{}, fmt.Errorf("commit run completion: %w", err)
	}
	return result, nil
}

func rejectReusedCompletionEvent(ctx context.Context, tx *sql.Tx, outboxEventID, requestID string, fingerprint [32]byte) error {
	var storedRequestID string
	var storedFingerprint []byte
	err := tx.QueryRowContext(ctx, `
		SELECT request_id, payload_fingerprint FROM run_completion_receipts WHERE outbox_event_id = ?
	`, outboxEventID).Scan(&storedRequestID, &storedFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read run completion receipt: %w", err)
	}
	if storedRequestID != requestID || !bytes.Equal(storedFingerprint, fingerprint[:]) {
		return ErrRunCompletionConflict
	}
	return ErrRunIntegrity
}

func rehydrateRunCompletion(ctx context.Context, tx *sql.Tx, requestID, agentID string, receipt runCompleteRequestReceipt) (CompleteRunResult, error) {
	if receipt.OutboxEventID == "" || receipt.Run.ID == "" || receipt.Run.AgentID != agentID || receipt.Result.CommittedAt.IsZero() {
		return CompleteRunResult{}, ErrRunIntegrity
	}
	current, err := runByID(ctx, tx, receipt.Run.ID)
	if err != nil {
		return CompleteRunResult{}, ErrRunIntegrity
	}
	currentJSON, err := json.Marshal(current)
	if err != nil {
		return CompleteRunResult{}, ErrRunIntegrity
	}
	receiptJSON, err := json.Marshal(receipt.Run)
	if err != nil || !bytes.Equal(currentJSON, receiptJSON) {
		return CompleteRunResult{}, ErrRunIntegrity
	}
	var storedRequestID, resultKind, resultID string
	if err := tx.QueryRowContext(ctx, `
		SELECT request_id, result_kind, result_id FROM run_completion_receipts WHERE outbox_event_id = ?
	`, receipt.OutboxEventID).Scan(&storedRequestID, &resultKind, &resultID); err != nil {
		return CompleteRunResult{}, ErrRunIntegrity
	}
	if storedRequestID != requestID || resultKind != current.ResultKind || resultID != current.ResultID {
		return CompleteRunResult{}, ErrRunIntegrity
	}
	sendResult, err := rehydrateInboxResult(ctx, tx, requestID, agentID, receipt.Result.Kind, receipt.Result.Message, receipt.Result.HeldDraft)
	if err != nil {
		return CompleteRunResult{}, err
	}
	sendResult.CommittedAt = receipt.Result.CommittedAt
	return CompleteRunResult{
		Run: current, Kind: sendResult.Kind, Message: sendResult.Message,
		HeldDraft: sendResult.HeldDraft, CommittedAt: sendResult.CommittedAt,
	}, nil
}
