package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"time"

	execution "github.com/abcdlsj/sumi/internal/execution/domain"
)

func (s *Store) CompleteRun(ctx context.Context, params CompleteRunParams) (CompleteRunResult, error) {
	mentions, fingerprint, err := validateRunCompletionInput(params)
	if err != nil {
		return CompleteRunResult{}, err
	}
	tx, authentication, err := s.beginRunTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return CompleteRunResult{}, err
	}
	defer tx.Rollback()
	facts, err := loadRunCompletionFacts(ctx, tx, authentication.Principal, params.RunID, params.Now)
	if err != nil {
		return CompleteRunResult{}, err
	}
	if replay, found, err := replayRunCompletion(ctx, tx, params, fingerprint, facts.run, facts.item, authentication.Proof); err != nil {
		return CompleteRunResult{}, err
	} else if found {
		return commitInboxReplay(tx, replay)
	}
	launch, err := requireCompletableRunLaunch(ctx, tx, facts, authentication.Proof, params)
	if err != nil {
		return CompleteRunResult{}, err
	}
	if err := validateMentionMembers(ctx, tx, facts.space.ID, mentions); err != nil {
		return CompleteRunResult{}, err
	}
	result, err := sendOrHoldInboxReplyTx(ctx, tx, authentication.Principal, facts.item, "", facts.item.Target, facts.run.BasisTargetSequence, params.Body, mentions, params.RequestID, fingerprint, params.Now)
	if err != nil {
		return CompleteRunResult{}, err
	}
	resultID, err := inboxSendResultID(result)
	if err != nil {
		return CompleteRunResult{}, err
	}
	if err := persistRunCompletionState(ctx, tx, facts, launch, params, result.Kind, resultID); err != nil {
		return CompleteRunResult{}, err
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: authentication.Principal.OrganizationID,
		Actor:          authentication.Principal,
		Action:         AuditRunComplete,
		TargetKind:     "run",
		TargetID:       facts.run.ID,
		ContextKind:    string(facts.delivery.Target.Kind),
		ContextID:      facts.delivery.Target.ID,
		RequestID:      params.RequestID,
		Outcome:        "committed",
		Now:            params.Now,
	}); err != nil {
		return CompleteRunResult{}, err
	}
	run, err := runByID(ctx, tx, facts.run.ID)
	if err != nil {
		return CompleteRunResult{}, fmt.Errorf("read completed run: %w", err)
	}
	resultReceipt, err := newInboxSendRequestReceipt(facts.item.ID, result)
	if err != nil {
		return CompleteRunResult{}, err
	}
	requestReceipt := runCompleteRequestReceipt{
		OutboxEventID: params.OutboxEventID,
		Run:           run,
		LaunchID:      launch.ID,
		Fence:         launch.Fence,
		Result:        resultReceipt,
	}
	if err := persistInboxRequest(ctx, tx, params.RequestID, run.AgentID, operationCompleteRun, fingerprint, requestReceipt, params.Now); err != nil {
		return CompleteRunResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO run_completion_receipts(
			outbox_event_id, request_id, payload_fingerprint, run_id, launch_id,
			fence, result_kind, result_id, committed_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, params.OutboxEventID, params.RequestID, fingerprint[:], run.ID, launch.ID,
		launch.Fence, result.Kind, resultID, unixNano(params.Now)); err != nil {
		return CompleteRunResult{}, fmt.Errorf("persist run completion receipt: %w", err)
	}
	completed := CompleteRunResult{
		Run: run, Kind: result.Kind, Message: result.Message,
		HeldDraft: result.HeldDraft, CommittedAt: result.CommittedAt,
	}
	if err := tx.Commit(); err != nil {
		return CompleteRunResult{}, fmt.Errorf("commit run completion: %w", err)
	}
	return completed, nil
}

type runCompletionFacts struct {
	run      Run
	delivery Delivery
	item     InboxItem
	space    Space
}

func validateRunCompletionInput(params CompleteRunParams) ([]Principal, [sha256.Size]byte, error) {
	if err := execution.ValidateOutcome(params.Outcome); err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	if err := validateMessageBody(params.Body); err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	mentions, err := canonicalMentionPrincipals(params.MentionedPrincipals)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	fingerprint, err := inboxFingerprint(struct {
		OutboxEventID string      `json:"outbox_event_id"`
		RunID         string      `json:"run_id"`
		LaunchID      string      `json:"launch_id"`
		Fence         uint64      `json:"fence"`
		Outcome       string      `json:"outcome"`
		Body          string      `json:"body"`
		Mentions      []Principal `json:"mentioned_principals,omitempty"`
	}{params.OutboxEventID, params.RunID, params.LaunchID, params.Fence, string(params.Outcome), params.Body, mentions})
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	return mentions, fingerprint, nil
}

func loadRunCompletionFacts(ctx context.Context, tx *sql.Tx, principal Principal, runID string, now time.Time) (runCompletionFacts, error) {
	run, err := requireOwnedRun(ctx, tx, principal.ID, runID)
	if err != nil {
		return runCompletionFacts{}, err
	}
	delivery, item, _, err := requireOwnedDelivery(ctx, tx, principal.ID, run.DeliveryID)
	if err != nil {
		return runCompletionFacts{}, err
	}
	space, err := requireRunReplyAccess(ctx, tx, principal, item, now)
	if err != nil {
		return runCompletionFacts{}, err
	}
	return runCompletionFacts{run: run, delivery: delivery, item: item, space: space}, nil
}

func requireCompletableRunLaunch(ctx context.Context, tx *sql.Tx, facts runCompletionFacts, proof AgentRuntimeProof, params CompleteRunParams) (RunLaunch, error) {
	launch, found, err := currentRunLaunch(ctx, tx, facts.run.ID)
	if err != nil {
		return RunLaunch{}, err
	}
	if !found {
		return RunLaunch{}, execution.ErrRunLaunchStale
	}
	delivery := executionDelivery(facts.delivery)
	run := executionRun(facts.run)
	currentLaunch := executionLaunch(launch)
	if err := execution.CanComplete(execution.ActiveFacts{
		Delivery: &delivery,
		Run:      &run,
		Launch:   &currentLaunch,
	}, params.LaunchID, params.Fence, runLaunchHeldBy(launch, proof), params.Now); err != nil {
		return RunLaunch{}, err
	}
	if facts.space.ArchivedAt != nil {
		return RunLaunch{}, ErrSpaceArchived
	}
	return launch, nil
}

func persistRunCompletionState(ctx context.Context, tx *sql.Tx, facts runCompletionFacts, launch RunLaunch, params CompleteRunParams, resultKind, resultID string) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE runs
		SET state = 'completed', outcome = ?, result_kind = ?, result_id = ?, completed_at = ?
		WHERE id = ? AND state = 'running'
	`, params.Outcome, resultKind, resultID, unixNano(params.Now), facts.run.ID); err != nil {
		return fmt.Errorf("complete run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deliveries SET state = 'completed', completed_at = ? WHERE id = ? AND state = 'accepted'`, unixNano(params.Now), facts.delivery.ID); err != nil {
		return fmt.Errorf("complete delivery: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE run_launches
		SET closed_at = max(claimed_at, ?), close_reason = 'completed'
		WHERE id = ? AND closed_at IS NULL
	`, unixNano(params.Now), launch.ID); err != nil {
		return fmt.Errorf("close completed run launch: %w", err)
	}
	return nil
}
