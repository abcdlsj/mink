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

	collaborationdomain "github.com/abcdlsj/sumi/internal/collaboration/domain"
	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
	executiondomain "github.com/abcdlsj/sumi/internal/execution/domain"
	"github.com/google/uuid"
)

const (
	RunStateQueued    = "queued"
	RunStateRunning   = "running"
	RunStateSucceeded = "succeeded"
	RunStateFailed    = "failed"
	RunStateCancelled = "cancelled"

	RunOutcomeSucceeded = "succeeded"
	RunOutcomeFailed    = "failed"

	operationClaimRun    = "run.claim"
	operationRenewRun    = "run.renew"
	operationCancelRun   = "run.cancel"
	operationCompleteRun = "run.complete"

	maxRunListLimit = 200
	runLeaseTTL     = 60 * time.Second
)

type Run = executionapp.Run
type RunUsage = executionapp.RunUsage
type ListRunsParams = executionapp.ListRunsQuery
type ListRunsResult = executionapp.ListRunsResult
type GetRunParams = executionapp.GetRunQuery
type ClaimRunParams = executionapp.ClaimRunCommand
type RenewRunParams = executionapp.RenewRunCommand
type CancelRunParams = executionapp.CancelRunCommand
type CompleteRunParams = executionapp.CompleteRunCommand
type CompleteRunResult = executionapp.CompleteRunResult

type runRequestReceipt struct {
	Run Run `json:"run"`
}

type runCompleteRequestReceipt struct {
	OutboxEventID string                  `json:"outbox_event_id"`
	Run           Run                     `json:"run"`
	Result        inboxSendRequestReceipt `json:"result"`
}

func ensureRunTx(ctx context.Context, tx *sql.Tx, trigger EligibleInboxTrigger) (Run, error) {
	if trigger.Item.Recipient.Kind != PrincipalAgent || trigger.Item.Recipient.ID == "" || trigger.Message.ID == "" ||
		trigger.Item.TriggerMessageID != trigger.Message.ID || trigger.Item.SpaceID != trigger.Message.SpaceID ||
		trigger.Item.Target != trigger.Message.Target || trigger.Item.TriggerTargetSequence != trigger.Message.TargetSequence {
		return Run{}, ErrRunIntegrity
	}
	runID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO runs(
			id, agent_id, inbox_item_id, trigger_message_id, space_id, target_kind,
			target_id, trigger_target_sequence, state, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, 'queued', ?)
		ON CONFLICT(agent_id, trigger_message_id) DO NOTHING
	`, runID, trigger.Item.Recipient.ID, trigger.Item.ID, trigger.Message.ID, trigger.Message.SpaceID,
		trigger.Message.Target.Kind, trigger.Message.Target.ID, trigger.Message.TargetSequence, unixNano(trigger.Item.CreatedAt)); err != nil {
		return Run{}, fmt.Errorf("ensure run: %w", err)
	}
	run, err := runByAgentMessage(ctx, tx, trigger.Item.Recipient.ID, trigger.Message.ID)
	if err != nil {
		return Run{}, fmt.Errorf("read ensured run: %w", err)
	}
	if run.InboxItemID != trigger.Item.ID || run.SpaceID != trigger.Item.SpaceID || run.Target != trigger.Item.Target ||
		run.TriggerTargetSequence != trigger.Item.TriggerTargetSequence || !run.CreatedAt.Equal(trigger.Item.CreatedAt) {
		return Run{}, ErrRunIntegrity
	}
	return run, nil
}

func (s *Store) beginRunTransaction(ctx context.Context, authentication AgentRuntimeAuthentication, now time.Time) (*sql.Tx, AgentRuntimeAuthentication, error) {
	tx, current, err := s.beginAgentInboxTransaction(ctx, authentication, now)
	if err != nil {
		return nil, AgentRuntimeAuthentication{}, err
	}
	reason, err := requireGrant(ctx, tx, current.Principal, CapabilityRunExecute, Scope{Kind: "agent", ID: current.Principal.ID}, now, "")
	if err != nil {
		tx.Rollback()
		return nil, AgentRuntimeAuthentication{}, err
	}
	if reason != "" {
		tx.Rollback()
		return nil, AgentRuntimeAuthentication{}, ErrPermissionDenied
	}
	return tx, current, nil
}

func requireRunItemAccess(ctx context.Context, tx *sql.Tx, principal Principal, item InboxItem, now time.Time) error {
	allowed, err := inboxItemReadable(ctx, tx, principal, item, now)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrInboxAccessLost
	}
	return nil
}

func requireRunReplyAccess(ctx context.Context, tx *sql.Tx, principal Principal, item InboxItem, now time.Time) (Space, error) {
	if err := requireRunItemAccess(ctx, tx, principal, item, now); err != nil {
		return Space{}, err
	}
	spaceID, err := resolveReadableTargetSpace(ctx, tx, item.Target)
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
	return loadMutationSpace(ctx, tx, principal, spaceID)
}

func runCursor(ctx context.Context, tx *sql.Tx, run Run) (uint64, error) {
	var spaceID string
	var seen uint64
	err := tx.QueryRowContext(ctx, `
		SELECT space_id, seen_up_to_target_sequence
		FROM principal_target_cursors
		WHERE principal_kind = 'agent' AND principal_id = ? AND target_kind = ? AND target_id = ?
	`, run.AgentID, run.Target.Kind, run.Target.ID).Scan(&spaceID, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrInboxBasisMismatch
	}
	if err != nil {
		return 0, fmt.Errorf("read run cursor: %w", err)
	}
	if spaceID != run.SpaceID || seen < run.TriggerTargetSequence {
		return 0, ErrInboxBasisMismatch
	}
	return seen, nil
}

func requireOwnedRun(ctx context.Context, tx *sql.Tx, agentID, runID string) (Run, error) {
	run, err := runByID(ctx, tx, runID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && run.AgentID != agentID) {
		return Run{}, ErrRunNotFound
	}
	return run, err
}

func runInboxItem(ctx context.Context, tx *sql.Tx, run Run) (InboxItem, error) {
	item, err := inboxItemByID(ctx, tx, run.InboxItemID)
	if err != nil {
		return InboxItem{}, err
	}
	if item.Recipient.Kind != PrincipalAgent || item.Recipient.ID != run.AgentID || item.TriggerMessageID != run.TriggerMessageID ||
		item.SpaceID != run.SpaceID || item.Target != run.Target || item.TriggerTargetSequence != run.TriggerTargetSequence {
		return InboxItem{}, ErrRunIntegrity
	}
	return item, nil
}

func allocateRunFence(ctx context.Context, tx *sql.Tx, agentID string) (uint64, error) {
	var fence uint64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO agent_run_fences(agent_id, current_fence) VALUES(?, 1)
		ON CONFLICT(agent_id) DO UPDATE SET current_fence = current_fence + 1
		RETURNING current_fence
	`, agentID).Scan(&fence); err != nil {
		return 0, fmt.Errorf("allocate run fence: %w", err)
	}
	return fence, nil
}

func readRunRequest(ctx context.Context, tx *sql.Tx, requestID, agentID, operation string, fingerprint [sha256.Size]byte, response any) (bool, error) {
	var storedAgentID, storedOperation string
	var storedFingerprint, snapshot []byte
	err := tx.QueryRowContext(ctx, `
		SELECT agent_id, operation, payload_fingerprint, response_snapshot
		FROM run_requests WHERE request_id = ?
	`, requestID).Scan(&storedAgentID, &storedOperation, &storedFingerprint, &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read run request receipt: %w", err)
	}
	if storedAgentID != agentID || storedOperation != operation || !bytes.Equal(storedFingerprint, fingerprint[:]) {
		return false, ErrRunRequestConflict
	}
	if err := json.Unmarshal(snapshot, response); err != nil {
		return false, ErrRunIntegrity
	}
	return true, nil
}

func persistRunRequest(ctx context.Context, tx *sql.Tx, requestID, agentID, operation string, fingerprint [sha256.Size]byte, runID string, response any, now time.Time) error {
	snapshot, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode run response snapshot: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO run_requests(request_id, agent_id, operation, payload_fingerprint, run_id, response_snapshot, committed_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, requestID, agentID, operation, fingerprint[:], runID, snapshot, unixNano(now)); err != nil {
		return fmt.Errorf("persist run request receipt: %w", err)
	}
	return nil
}

func runFingerprint(value any) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode run request: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func runByID(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, runID string) (Run, error) {
	return scanRun(queryer.QueryRowContext(ctx, runSelect+` WHERE runs.id = ?`, runID))
}

func runByAgentMessage(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, agentID, messageID string) (Run, error) {
	return scanRun(queryer.QueryRowContext(ctx, runSelect+` WHERE runs.agent_id = ? AND runs.trigger_message_id = ?`, agentID, messageID))
}

const runSelect = `
	SELECT runs.sequence, runs.id, runs.agent_id, runs.inbox_item_id, runs.trigger_message_id,
	       runs.space_id, runs.target_kind, runs.target_id, runs.trigger_target_sequence,
	       runs.input_basis_target_sequence, runs.state, runs.attempt, runs.lease_holder_computer_id,
	       runs.lease_expires_at, runs.fence, runs.placement_desired_revision, runs.result_kind,
	       runs.result_id, runs.usage_input_units, runs.usage_output_units, runs.error_code,
	       runs.created_at, runs.started_at, runs.completed_at, runs.cancelled_at
	FROM runs`

func scanRun(row scanner) (Run, error) {
	var run Run
	var targetKind string
	var holder sql.NullString
	var leaseExpiresAt, startedAt, completedAt, cancelledAt sql.NullInt64
	var createdAt int64
	if err := row.Scan(
		&run.Sequence, &run.ID, &run.AgentID, &run.InboxItemID, &run.TriggerMessageID,
		&run.SpaceID, &targetKind, &run.Target.ID, &run.TriggerTargetSequence,
		&run.InputBasisTargetSequence, &run.State, &run.Attempt, &holder, &leaseExpiresAt,
		&run.Fence, &run.PlacementDesiredRevision, &run.ResultKind, &run.ResultID,
		&run.Usage.InputUnits, &run.Usage.OutputUnits, &run.ErrorCode, &createdAt,
		&startedAt, &completedAt, &cancelledAt,
	); err != nil {
		return Run{}, err
	}
	run.Target.Kind = collaborationdomain.MessageTargetKind(targetKind)
	if holder.Valid {
		run.LeaseHolderComputerID = holder.String
	}
	if leaseExpiresAt.Valid {
		value := timeFromUnixNano(leaseExpiresAt.Int64)
		run.LeaseExpiresAt = &value
	}
	if startedAt.Valid {
		value := timeFromUnixNano(startedAt.Int64)
		run.StartedAt = &value
	}
	if completedAt.Valid {
		value := timeFromUnixNano(completedAt.Int64)
		run.CompletedAt = &value
	}
	if cancelledAt.Valid {
		value := timeFromUnixNano(cancelledAt.Int64)
		run.CancelledAt = &value
	}
	run.CreatedAt = timeFromUnixNano(createdAt)
	if err := executiondomain.ValidateRun(executionRun(run)); err != nil {
		return Run{}, err
	}
	return run, nil
}

func executionRun(run Run) executiondomain.Run {
	return executiondomain.Run{
		State: executiondomain.RunState(run.State), InputBasisTargetSequence: run.InputBasisTargetSequence,
		Attempt: run.Attempt, LeaseHolderComputerID: run.LeaseHolderComputerID, LeaseExpiresAt: run.LeaseExpiresAt,
		Fence: run.Fence, PlacementDesiredRevision: run.PlacementDesiredRevision,
		ResultKind: executiondomain.ResultKind(run.ResultKind), ResultID: run.ResultID, ErrorCode: run.ErrorCode,
		StartedAt: run.StartedAt, CompletedAt: run.CompletedAt, CancelledAt: run.CancelledAt,
	}
}
