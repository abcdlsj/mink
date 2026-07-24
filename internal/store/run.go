package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	executiondomain "github.com/abcdlsj/sumi/internal/execution/domain"
)

func (s *Store) ListRuns(ctx context.Context, params ListRunsParams) (ListRunsResult, error) {
	if params.Limit == 0 || params.Limit > maxRunListLimit {
		return ListRunsResult{}, ErrInvalidRunLimit
	}
	tx, authentication, err := s.beginRunTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return ListRunsResult{}, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, runSelect+`
		WHERE runs.agent_id = ? AND runs.state IN ('queued', 'running') AND runs.sequence > ?
		ORDER BY runs.sequence
		LIMIT ?
	`, authentication.Principal.ID, params.AfterSequence, params.Limit)
	if err != nil {
		return ListRunsResult{}, fmt.Errorf("list runs: %w", err)
	}
	var runs []Run
	for rows.Next() {
		run, scanErr := scanRun(rows)
		if scanErr != nil {
			rows.Close()
			return ListRunsResult{}, fmt.Errorf("scan run: %w", scanErr)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ListRunsResult{}, fmt.Errorf("iterate runs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return ListRunsResult{}, fmt.Errorf("close runs: %w", err)
	}

	visible := runs[:0]
	var next uint64
	for _, run := range runs {
		item, itemErr := runInboxItem(ctx, tx, run)
		if itemErr != nil {
			return ListRunsResult{}, itemErr
		}
		item.Recipient.OrganizationID = authentication.Principal.OrganizationID
		allowed, accessErr := inboxItemReadable(ctx, tx, authentication.Principal, item, params.Now)
		if accessErr != nil {
			return ListRunsResult{}, accessErr
		}
		if !allowed {
			if err := cancelInaccessibleRun(ctx, tx, run, params.Now); err != nil {
				return ListRunsResult{}, err
			}
			continue
		}
		visible = append(visible, run)
		if run.Sequence > next {
			next = run.Sequence
		}
	}
	if err := tx.Commit(); err != nil {
		return ListRunsResult{}, fmt.Errorf("commit run list: %w", err)
	}
	return ListRunsResult{Runs: visible, NextSequence: next}, nil
}

func (s *Store) GetRun(ctx context.Context, params GetRunParams) (Run, error) {
	tx, authentication, err := s.beginRunTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	run, err := requireOwnedRun(ctx, tx, authentication.Principal.ID, params.RunID)
	if err != nil {
		return Run{}, err
	}
	item, err := runInboxItem(ctx, tx, run)
	if err != nil {
		return Run{}, err
	}
	item.Recipient.OrganizationID = authentication.Principal.OrganizationID
	if err := requireRunItemAccess(ctx, tx, authentication.Principal, item, params.Now); err != nil {
		return Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return Run{}, fmt.Errorf("commit run read: %w", err)
	}
	return run, nil
}

func (s *Store) ClaimRun(ctx context.Context, params ClaimRunParams) (Run, error) {
	fingerprint, err := runFingerprint(struct {
		RunID string `json:"run_id"`
	}{params.RunID})
	if err != nil {
		return Run{}, err
	}
	tx, authentication, err := s.beginRunTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	run, err := requireOwnedRun(ctx, tx, authentication.Principal.ID, params.RunID)
	if err != nil {
		return Run{}, err
	}
	var receipt runRequestReceipt
	if found, err := readRunRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationClaimRun, fingerprint, &receipt); err != nil {
		return Run{}, err
	} else if found {
		return commitRunReplay(tx, receipt.Run)
	}
	item, err := runInboxItem(ctx, tx, run)
	if err != nil {
		return Run{}, err
	}
	item.Recipient.OrganizationID = authentication.Principal.OrganizationID
	if err := requireRunItemAccess(ctx, tx, authentication.Principal, item, params.Now); err != nil {
		return Run{}, err
	}
	if item.State == InboxStateDone {
		return Run{}, ErrInboxAccessLost
	}
	if err := executiondomain.CanClaim(executionRun(run), params.Now); err != nil {
		return Run{}, err
	}
	var anotherActive bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE agent_id = ? AND state = 'running' AND id != ?)`, run.AgentID, run.ID).Scan(&anotherActive); err != nil {
		return Run{}, fmt.Errorf("check active agent run: %w", err)
	}
	if anotherActive {
		return Run{}, ErrRunAlreadyActive
	}
	basis, err := runCursor(ctx, tx, run)
	if err != nil {
		return Run{}, err
	}
	fence, err := allocateRunFence(ctx, tx, run.AgentID)
	if err != nil {
		return Run{}, err
	}
	expiresAt := params.Now.UTC().Add(runLeaseTTL)
	if _, err := tx.ExecContext(ctx, `
		UPDATE runs SET state = 'running', input_basis_target_sequence = ?, attempt = attempt + 1,
			lease_holder_computer_id = ?, lease_expires_at = ?, fence = ?, placement_desired_revision = ?,
			result_kind = '', result_id = '', usage_input_units = 0, usage_output_units = 0,
			error_code = '', started_at = COALESCE(started_at, ?), completed_at = NULL, cancelled_at = NULL
		WHERE id = ?
	`, basis, authentication.Proof.ComputerID(), unixNano(expiresAt), fence,
		authentication.Proof.PlacementDesiredRevision(), unixNano(params.Now), run.ID); err != nil {
		if isUniqueConstraint(err, "runs.agent_id") {
			return Run{}, ErrRunAlreadyActive
		}
		return Run{}, fmt.Errorf("claim run: %w", err)
	}
	if item.State == InboxStateUnread {
		if _, err := tx.ExecContext(ctx, `UPDATE inbox_items SET state = 'claimed', claimed_at = ? WHERE id = ? AND state = 'unread'`, unixNano(params.Now), item.ID); err != nil {
			return Run{}, fmt.Errorf("claim run inbox item: %w", err)
		}
	}
	run, err = runByID(ctx, tx, run.ID)
	if err != nil {
		return Run{}, err
	}
	receipt = runRequestReceipt{Run: run}
	if err := persistRunRequest(ctx, tx, params.RequestID, run.AgentID, operationClaimRun, fingerprint, run.ID, receipt, params.Now); err != nil {
		return Run{}, err
	}
	if err := appendRunAudit(ctx, tx, authentication, AuditRunClaim, run, params.RequestID, params.Now); err != nil {
		return Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return Run{}, fmt.Errorf("commit run claim: %w", err)
	}
	return run, nil
}

func (s *Store) RenewRun(ctx context.Context, params RenewRunParams) (Run, error) {
	fingerprint, err := runFingerprint(struct {
		RunID   string `json:"run_id"`
		Attempt uint64 `json:"attempt"`
		Fence   uint64 `json:"fence"`
	}{params.RunID, params.Attempt, params.Fence})
	if err != nil {
		return Run{}, err
	}
	tx, authentication, err := s.beginRunTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	run, err := requireOwnedRun(ctx, tx, authentication.Principal.ID, params.RunID)
	if err != nil {
		return Run{}, err
	}
	var receipt runRequestReceipt
	if found, err := readRunRequest(ctx, tx, params.RequestID, run.AgentID, operationRenewRun, fingerprint, &receipt); err != nil {
		return Run{}, err
	} else if found {
		return commitRunReplay(tx, receipt.Run)
	}
	if err := executiondomain.ValidateLease(executionRun(run), authentication.Proof.ComputerID(),
		authentication.Proof.PlacementDesiredRevision(), params.Attempt, params.Fence, params.Now); err != nil {
		return Run{}, err
	}
	expiresAt := params.Now.UTC().Add(runLeaseTTL)
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET lease_expires_at = ? WHERE id = ?`, unixNano(expiresAt), run.ID); err != nil {
		return Run{}, fmt.Errorf("renew run: %w", err)
	}
	run, err = runByID(ctx, tx, run.ID)
	if err != nil {
		return Run{}, err
	}
	receipt = runRequestReceipt{Run: run}
	if err := persistRunRequest(ctx, tx, params.RequestID, run.AgentID, operationRenewRun, fingerprint, run.ID, receipt, params.Now); err != nil {
		return Run{}, err
	}
	if err := appendRunAudit(ctx, tx, authentication, AuditRunRenew, run, params.RequestID, params.Now); err != nil {
		return Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return Run{}, fmt.Errorf("commit run renewal: %w", err)
	}
	return run, nil
}

func (s *Store) CancelRun(ctx context.Context, params CancelRunParams) (Run, error) {
	fingerprint, err := runFingerprint(struct {
		RunID   string `json:"run_id"`
		Attempt uint64 `json:"attempt"`
		Fence   uint64 `json:"fence"`
	}{params.RunID, params.Attempt, params.Fence})
	if err != nil {
		return Run{}, err
	}
	tx, authentication, err := s.beginRunTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	run, err := requireOwnedRun(ctx, tx, authentication.Principal.ID, params.RunID)
	if err != nil {
		return Run{}, err
	}
	var receipt runRequestReceipt
	if found, err := readRunRequest(ctx, tx, params.RequestID, run.AgentID, operationCancelRun, fingerprint, &receipt); err != nil {
		return Run{}, err
	} else if found {
		return commitRunReplay(tx, receipt.Run)
	}
	item, err := runInboxItem(ctx, tx, run)
	if err != nil {
		return Run{}, err
	}
	switch run.State {
	case RunStateQueued:
		if params.Attempt != 0 || params.Fence != 0 {
			return Run{}, ErrRunLeaseStale
		}
	case RunStateRunning:
		if run.LeaseHolderComputerID != authentication.Proof.ComputerID() ||
			run.PlacementDesiredRevision != authentication.Proof.PlacementDesiredRevision() ||
			run.Attempt != params.Attempt || run.Fence != params.Fence {
			return Run{}, ErrRunLeaseStale
		}
	default:
		return Run{}, ErrRunNotRunning
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET state = 'cancelled', cancelled_at = ? WHERE id = ?`, unixNano(params.Now), run.ID); err != nil {
		return Run{}, fmt.Errorf("cancel run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE inbox_items SET state = 'done', done_at = ?, completion = 'cancelled'
		WHERE id = ? AND state != 'done'
	`, unixNano(params.Now), item.ID); err != nil {
		return Run{}, fmt.Errorf("cancel run inbox item: %w", err)
	}
	run, err = runByID(ctx, tx, run.ID)
	if err != nil {
		return Run{}, err
	}
	receipt = runRequestReceipt{Run: run}
	if err := persistRunRequest(ctx, tx, params.RequestID, run.AgentID, operationCancelRun, fingerprint, run.ID, receipt, params.Now); err != nil {
		return Run{}, err
	}
	if err := appendRunAudit(ctx, tx, authentication, AuditRunCancel, run, params.RequestID, params.Now); err != nil {
		return Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return Run{}, fmt.Errorf("commit run cancellation: %w", err)
	}
	return run, nil
}

func cancelInaccessibleRun(ctx context.Context, tx *sql.Tx, run Run, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET state = 'cancelled', cancelled_at = ? WHERE id = ? AND state IN ('queued', 'running')`, unixNano(now), run.ID); err != nil {
		return fmt.Errorf("cancel inaccessible run: %w", err)
	}
	return closeInboxItemAccessLost(ctx, tx, run.InboxItemID, now)
}

func appendRunAudit(ctx context.Context, tx *sql.Tx, authentication AgentRuntimeAuthentication, action string, run Run, requestID string, now time.Time) error {
	return appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: authentication.Principal.OrganizationID,
		Actor:          authentication.Principal,
		Action:         action,
		TargetKind:     "run",
		TargetID:       run.ID,
		ContextKind:    "space",
		ContextID:      run.SpaceID,
		RequestID:      requestID,
		Outcome:        "committed",
		Now:            now,
	})
}

func commitRunReplay[T any](tx *sql.Tx, value T) (T, error) {
	if err := tx.Commit(); err != nil {
		var zero T
		return zero, fmt.Errorf("commit run replay: %w", err)
	}
	return value, nil
}
