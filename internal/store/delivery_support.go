package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func ensureDeliveryTx(ctx context.Context, tx *sql.Tx, trigger EligibleInboxTrigger) (Delivery, error) {
	if trigger.Item.AgentID == "" || trigger.Message.ID == "" || trigger.Item.TriggerMessageID != trigger.Message.ID ||
		trigger.Item.SpaceID != trigger.Message.SpaceID || trigger.Item.Target != trigger.Message.Target ||
		trigger.Item.TriggerTargetSequence != trigger.Message.TargetSequence {
		return Delivery{}, ErrRunIntegrity
	}
	deliveryID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deliveries(
			id, agent_id, inbox_item_id, trigger_message_id, space_id, target_kind,
			target_id, trigger_target_sequence, state, created_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, 'available', ?)
		ON CONFLICT(agent_id, trigger_message_id) DO NOTHING
	`, deliveryID, trigger.Item.AgentID, trigger.Item.ID, trigger.Message.ID, trigger.Message.SpaceID,
		trigger.Message.Target.Kind, trigger.Message.Target.ID, trigger.Message.TargetSequence,
		unixNano(trigger.Item.CreatedAt)); err != nil {
		return Delivery{}, fmt.Errorf("ensure delivery: %w", err)
	}
	delivery, err := deliveryByAgentMessage(ctx, tx, trigger.Item.AgentID, trigger.Message.ID)
	if err != nil {
		return Delivery{}, fmt.Errorf("read ensured delivery: %w", err)
	}
	if delivery.InboxItemID != trigger.Item.ID || delivery.SpaceID != trigger.Item.SpaceID ||
		delivery.Target != trigger.Item.Target || delivery.TriggerTargetSequence != trigger.Item.TriggerTargetSequence ||
		!delivery.CreatedAt.Equal(trigger.Item.CreatedAt) {
		return Delivery{}, ErrRunIntegrity
	}
	return delivery, nil
}

func (s *Store) beginRunTransaction(ctx context.Context, authentication AgentRuntimeAuthentication, now time.Time) (*sql.Tx, AgentRuntimeAuthentication, error) {
	tx, current, err := s.beginInboxTransaction(ctx, authentication, now)
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

func deliveryCursor(ctx context.Context, tx *sql.Tx, delivery Delivery) (uint64, error) {
	var spaceID string
	var seen uint64
	err := tx.QueryRowContext(ctx, `
		SELECT space_id, seen_up_to_target_sequence
		FROM agent_target_cursors
		WHERE agent_id = ? AND target_kind = ? AND target_id = ?
	`, delivery.AgentID, delivery.Target.Kind, delivery.Target.ID).Scan(&spaceID, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrDeliveryCursorUnavailable
	}
	if err != nil {
		return 0, fmt.Errorf("read delivery cursor: %w", err)
	}
	if spaceID != delivery.SpaceID || seen < delivery.TriggerTargetSequence {
		return 0, ErrDeliveryCursorUnavailable
	}
	return seen, nil
}

func replayRunAcceptRequest(ctx context.Context, tx *sql.Tx, requestID, agentID string, fingerprint [sha256.Size]byte, delivery Delivery) (Run, bool, error) {
	snapshot, found, err := readInboxRequestReceipt(ctx, tx, requestID, agentID, operationAcceptDelivery, fingerprint)
	if err != nil || !found {
		return Run{}, found, err
	}
	var receipt runAcceptRequestReceipt
	if err := decodeInboxRequestReceipt(snapshot, &receipt); err != nil {
		return Run{}, false, err
	}
	current, err := runByID(ctx, tx, receipt.Run.ID)
	if err != nil {
		return Run{}, false, ErrRunIntegrity
	}
	if receipt.Run.DeliveryID != delivery.ID || receipt.Run.AgentID != agentID || receipt.Run.State != RunStateAccepted ||
		receipt.Run.Outcome != "" || receipt.Run.ResultKind != "" || receipt.Run.ResultID != "" ||
		receipt.Run.StartedAt != nil || receipt.Run.CompletedAt != nil || current.DeliveryID != receipt.Run.DeliveryID ||
		current.AgentID != receipt.Run.AgentID || current.BasisTargetSequence != receipt.Run.BasisTargetSequence ||
		!current.AcceptedAt.Equal(receipt.Run.AcceptedAt) {
		return Run{}, false, ErrRunIntegrity
	}
	return receipt.Run, true, nil
}

func replayRunLaunchRequest(ctx context.Context, tx *sql.Tx, requestID, agentID, operation string, fingerprint [sha256.Size]byte, run Run, proof AgentRuntimeProof) (RunLaunch, bool, error) {
	snapshot, found, err := readInboxRequestReceipt(ctx, tx, requestID, agentID, operation, fingerprint)
	if err != nil || !found {
		return RunLaunch{}, found, err
	}
	var receipt runLaunchRequestReceipt
	if err := decodeInboxRequestReceipt(snapshot, &receipt); err != nil {
		return RunLaunch{}, false, err
	}
	current, err := runLaunchByID(ctx, tx, receipt.Launch.ID)
	if err != nil {
		return RunLaunch{}, false, ErrRunIntegrity
	}
	if receipt.Launch.RunID != run.ID || receipt.Launch.AgentID != agentID ||
		current.RunID != receipt.Launch.RunID || current.AgentID != receipt.Launch.AgentID ||
		current.HolderComputerID != receipt.Launch.HolderComputerID ||
		current.HolderPlacementGeneration != receipt.Launch.HolderPlacementGeneration ||
		current.Fence != receipt.Launch.Fence || !current.ClaimedAt.Equal(receipt.Launch.ClaimedAt) {
		return RunLaunch{}, false, ErrRunIntegrity
	}
	if !runLaunchHeldBy(receipt.Launch, proof) {
		return RunLaunch{}, false, ErrRunLaunchStale
	}
	return receipt.Launch, true, nil
}

func replayRunCompletion(ctx context.Context, tx *sql.Tx, params CompleteRunParams, fingerprint [sha256.Size]byte, run Run, item InboxItem, proof AgentRuntimeProof) (CompleteRunResult, bool, error) {
	receipt, found, err := readRunCompletionReceipt(ctx, tx, params.OutboxEventID, params.RequestID, params.RunID)
	if err != nil {
		return CompleteRunResult{}, false, err
	}
	if !found {
		if _, found, err := readInboxRequestReceipt(ctx, tx, params.RequestID, run.AgentID, operationCompleteRun, fingerprint); err != nil {
			return CompleteRunResult{}, false, err
		} else if found {
			return CompleteRunResult{}, false, ErrRunIntegrity
		}
		return CompleteRunResult{}, false, nil
	}
	if receipt.OutboxEventID != params.OutboxEventID || receipt.RequestID != params.RequestID ||
		receipt.RunID != params.RunID || receipt.LaunchID != params.LaunchID || receipt.Fence != params.Fence ||
		!bytes.Equal(receipt.Fingerprint[:], fingerprint[:]) {
		return CompleteRunResult{}, false, ErrRunCompletionConflict
	}
	launch, err := runLaunchByID(ctx, tx, receipt.LaunchID)
	if err != nil || launch.RunID != receipt.RunID || launch.AgentID != run.AgentID || launch.Fence != receipt.Fence {
		return CompleteRunResult{}, false, ErrRunIntegrity
	}
	if !runLaunchHeldBy(launch, proof) {
		return CompleteRunResult{}, false, ErrRunLaunchStale
	}
	snapshot, requestFound, err := readInboxRequestReceipt(ctx, tx, params.RequestID, run.AgentID, operationCompleteRun, fingerprint)
	if err != nil {
		return CompleteRunResult{}, false, err
	}
	if !requestFound {
		return CompleteRunResult{}, false, ErrRunIntegrity
	}
	var requestReceipt runCompleteRequestReceipt
	if err := decodeInboxRequestReceipt(snapshot, &requestReceipt); err != nil {
		return CompleteRunResult{}, false, err
	}
	if requestReceipt.OutboxEventID != receipt.OutboxEventID || requestReceipt.Run.ID != receipt.RunID ||
		requestReceipt.LaunchID != receipt.LaunchID || requestReceipt.Fence != receipt.Fence ||
		requestReceipt.Run.DeliveryID != run.DeliveryID || requestReceipt.Run.AgentID != run.AgentID ||
		requestReceipt.Run.BasisTargetSequence != run.BasisTargetSequence ||
		!requestReceipt.Run.AcceptedAt.Equal(run.AcceptedAt) || requestReceipt.Run.State != RunStateCompleted ||
		requestReceipt.Run.Outcome != run.Outcome || requestReceipt.Run.ResultKind != run.ResultKind ||
		requestReceipt.Run.ResultID != run.ResultID || requestReceipt.Run.StartedAt == nil || run.StartedAt == nil ||
		!requestReceipt.Run.StartedAt.Equal(*run.StartedAt) || requestReceipt.Run.CompletedAt == nil || run.CompletedAt == nil ||
		!requestReceipt.Run.CompletedAt.Equal(*run.CompletedAt) || requestReceipt.Result.Kind != receipt.ResultKind ||
		!requestReceipt.Result.CommittedAt.Equal(receipt.CommittedAt) {
		return CompleteRunResult{}, false, ErrRunIntegrity
	}
	result, err := rehydrateInboxResult(ctx, tx, params.RequestID, run.AgentID, requestReceipt.Result.Kind, requestReceipt.Result.Message, requestReceipt.Result.HeldDraft)
	if err != nil {
		return CompleteRunResult{}, false, err
	}
	result.CommittedAt = requestReceipt.Result.CommittedAt
	resultID, err := inboxSendResultID(result)
	if err != nil {
		return CompleteRunResult{}, false, err
	}
	if run.State != RunStateCompleted || run.ResultKind != result.Kind || run.ResultID != resultID ||
		run.ResultKind != receipt.ResultKind || run.ResultID != receipt.ResultID || run.CompletedAt == nil ||
		!run.CompletedAt.Equal(receipt.CommittedAt) || requestReceipt.Result.InboxItemID != item.ID {
		return CompleteRunResult{}, false, ErrRunIntegrity
	}
	return CompleteRunResult{
		Run: requestReceipt.Run, Kind: result.Kind, Message: result.Message,
		HeldDraft: result.HeldDraft, CommittedAt: receipt.CommittedAt,
	}, true, nil
}

func readRunCompletionReceipt(ctx context.Context, tx *sql.Tx, eventID, requestID, runID string) (runCompletionReceipt, bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT outbox_event_id, request_id, payload_fingerprint, run_id, launch_id,
		       fence, result_kind, result_id, committed_at
		FROM run_completion_receipts
		WHERE outbox_event_id = ? OR request_id = ? OR run_id = ?
	`, eventID, requestID, runID)
	if err != nil {
		return runCompletionReceipt{}, false, fmt.Errorf("read run completion receipt: %w", err)
	}
	defer rows.Close()
	var receipts []runCompletionReceipt
	for rows.Next() {
		var receipt runCompletionReceipt
		var storedFingerprint []byte
		var committedAt int64
		if err := rows.Scan(&receipt.OutboxEventID, &receipt.RequestID, &storedFingerprint,
			&receipt.RunID, &receipt.LaunchID, &receipt.Fence, &receipt.ResultKind,
			&receipt.ResultID, &committedAt); err != nil {
			return runCompletionReceipt{}, false, fmt.Errorf("scan run completion receipt: %w", err)
		}
		if len(storedFingerprint) != sha256.Size {
			return runCompletionReceipt{}, false, ErrRunIntegrity
		}
		copy(receipt.Fingerprint[:], storedFingerprint)
		receipt.CommittedAt = timeFromUnixNano(committedAt)
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return runCompletionReceipt{}, false, fmt.Errorf("iterate run completion receipts: %w", err)
	}
	if len(receipts) == 0 {
		return runCompletionReceipt{}, false, nil
	}
	if len(receipts) != 1 {
		return runCompletionReceipt{}, false, ErrRunCompletionConflict
	}
	return receipts[0], true, nil
}

func inboxSendResultID(result SendInboxReplyResult) (string, error) {
	switch result.Kind {
	case InboxResultMessage:
		if result.Message == nil || result.HeldDraft != nil {
			return "", ErrRunIntegrity
		}
		return result.Message.ID, nil
	case InboxResultHeldDraft:
		if result.Message != nil || result.HeldDraft == nil {
			return "", ErrRunIntegrity
		}
		return result.HeldDraft.ID, nil
	default:
		return "", ErrRunIntegrity
	}
}

func allocateRunFence(ctx context.Context, tx *sql.Tx, agentID string) (uint64, error) {
	var fence uint64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO agent_run_fences(agent_id, current_fence)
		VALUES(?, 1)
		ON CONFLICT(agent_id) DO UPDATE SET current_fence = current_fence + 1
		RETURNING current_fence
	`, agentID).Scan(&fence); err != nil {
		return 0, fmt.Errorf("allocate agent run fence: %w", err)
	}
	return fence, nil
}

func runLaunchHeldBy(launch RunLaunch, proof AgentRuntimeProof) bool {
	return launch.AgentID == proof.AgentID() && launch.HolderComputerID == proof.ComputerID() &&
		launch.HolderPlacementGeneration == proof.PlacementGeneration()
}

func requireOwnedDelivery(ctx context.Context, tx *sql.Tx, agentID, deliveryID string) (Delivery, InboxItem, Message, error) {
	delivery, err := deliveryByID(ctx, tx, deliveryID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && delivery.AgentID != agentID) {
		return Delivery{}, InboxItem{}, Message{}, ErrDeliveryNotFound
	}
	if err != nil {
		return Delivery{}, InboxItem{}, Message{}, fmt.Errorf("read delivery: %w", err)
	}
	item, err := inboxItemByID(ctx, tx, delivery.InboxItemID)
	if err != nil {
		return Delivery{}, InboxItem{}, Message{}, ErrRunIntegrity
	}
	message, err := scanMessage(tx.QueryRowContext(ctx, messageSelect+` WHERE id = ?`, delivery.TriggerMessageID))
	if err != nil {
		return Delivery{}, InboxItem{}, Message{}, ErrRunIntegrity
	}
	if item.AgentID != delivery.AgentID || item.TriggerMessageID != delivery.TriggerMessageID ||
		item.SpaceID != delivery.SpaceID || item.Target != delivery.Target ||
		item.TriggerTargetSequence != delivery.TriggerTargetSequence || message.SpaceID != delivery.SpaceID ||
		message.Target != delivery.Target || message.TargetSequence != delivery.TriggerTargetSequence {
		return Delivery{}, InboxItem{}, Message{}, ErrRunIntegrity
	}
	return delivery, item, message, nil
}

func requireOwnedRun(ctx context.Context, tx *sql.Tx, agentID, runID string) (Run, error) {
	run, err := runByID(ctx, tx, runID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && run.AgentID != agentID) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("read run: %w", err)
	}
	return run, nil
}

func currentRunLaunch(ctx context.Context, tx *sql.Tx, runID string) (RunLaunch, bool, error) {
	launch, err := scanRunLaunch(tx.QueryRowContext(ctx, runLaunchSelect+` WHERE run_id = ? AND closed_at IS NULL`, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return RunLaunch{}, false, nil
	}
	if err != nil {
		return RunLaunch{}, false, fmt.Errorf("read current run launch: %w", err)
	}
	return launch, true, nil
}

func deliveryByID(ctx context.Context, tx *sql.Tx, deliveryID string) (Delivery, error) {
	return scanDelivery(tx.QueryRowContext(ctx, deliverySelect+` WHERE deliveries.id = ?`, deliveryID))
}

func deliveryByAgentMessage(ctx context.Context, tx *sql.Tx, agentID, messageID string) (Delivery, error) {
	return scanDelivery(tx.QueryRowContext(ctx, deliverySelect+` WHERE deliveries.agent_id = ? AND deliveries.trigger_message_id = ?`, agentID, messageID))
}

func runByID(ctx context.Context, tx *sql.Tx, runID string) (Run, error) {
	return scanRun(tx.QueryRowContext(ctx, runSelect+` WHERE id = ?`, runID))
}

func activeRunByAgent(ctx context.Context, tx *sql.Tx, agentID string) (Run, bool, error) {
	run, err := scanRun(tx.QueryRowContext(ctx, runSelect+` WHERE agent_id = ? AND state IN ('accepted', 'running')`, agentID))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, fmt.Errorf("read active run: %w", err)
	}
	return run, true, nil
}

func runLaunchByID(ctx context.Context, tx *sql.Tx, launchID string) (RunLaunch, error) {
	return scanRunLaunch(tx.QueryRowContext(ctx, runLaunchSelect+` WHERE id = ?`, launchID))
}

const deliverySelect = `
	SELECT deliveries.sequence, deliveries.id, deliveries.agent_id, deliveries.inbox_item_id,
	       deliveries.trigger_message_id, deliveries.space_id, deliveries.target_kind,
	       deliveries.target_id, deliveries.trigger_target_sequence, deliveries.state,
	       deliveries.created_at, deliveries.accepted_at, deliveries.completed_at
	FROM deliveries`

const runSelect = `
	SELECT id, delivery_id, agent_id, basis_target_sequence, state, outcome,
	       result_kind, result_id, accepted_at, started_at, completed_at
	FROM runs`

const runLaunchSelect = `
	SELECT id, run_id, agent_id, holder_computer_id, holder_placement_generation,
	       fence, claimed_at, expires_at, closed_at, close_reason
	FROM run_launches`

func scanDelivery(row scanner) (Delivery, error) {
	var delivery Delivery
	var createdAt int64
	var acceptedAt, completedAt sql.NullInt64
	if err := row.Scan(&delivery.Sequence, &delivery.ID, &delivery.AgentID, &delivery.InboxItemID,
		&delivery.TriggerMessageID, &delivery.SpaceID, &delivery.Target.Kind, &delivery.Target.ID,
		&delivery.TriggerTargetSequence, &delivery.State, &createdAt, &acceptedAt, &completedAt); err != nil {
		return Delivery{}, err
	}
	delivery.CreatedAt = timeFromUnixNano(createdAt)
	delivery.AcceptedAt = optionalTime(acceptedAt)
	delivery.CompletedAt = optionalTime(completedAt)
	return delivery, nil
}

func scanDeliveries(rows *sql.Rows) ([]Delivery, error) {
	var deliveries []Delivery
	for rows.Next() {
		delivery, err := scanDelivery(rows)
		if err != nil {
			return nil, fmt.Errorf("scan delivery: %w", err)
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deliveries: %w", err)
	}
	return deliveries, nil
}

func scanRun(row scanner) (Run, error) {
	var run Run
	var acceptedAt int64
	var startedAt, completedAt sql.NullInt64
	if err := row.Scan(&run.ID, &run.DeliveryID, &run.AgentID, &run.BasisTargetSequence,
		&run.State, &run.Outcome, &run.ResultKind, &run.ResultID, &acceptedAt,
		&startedAt, &completedAt); err != nil {
		return Run{}, err
	}
	run.AcceptedAt = timeFromUnixNano(acceptedAt)
	run.StartedAt = optionalTime(startedAt)
	run.CompletedAt = optionalTime(completedAt)
	return run, nil
}

func scanRunLaunch(row scanner) (RunLaunch, error) {
	var launch RunLaunch
	var claimedAt, expiresAt int64
	var closedAt sql.NullInt64
	if err := row.Scan(&launch.ID, &launch.RunID, &launch.AgentID, &launch.HolderComputerID,
		&launch.HolderPlacementGeneration, &launch.Fence, &claimedAt, &expiresAt,
		&closedAt, &launch.CloseReason); err != nil {
		return RunLaunch{}, err
	}
	launch.ClaimedAt = timeFromUnixNano(claimedAt)
	launch.ExpiresAt = timeFromUnixNano(expiresAt)
	launch.ClosedAt = optionalTime(closedAt)
	return launch, nil
}

func optionalTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	stamp := timeFromUnixNano(value.Int64)
	return &stamp
}
