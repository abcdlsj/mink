package store

import (
	"context"
	"fmt"

	execution "github.com/abcdlsj/sumi/internal/execution/domain"
	"github.com/google/uuid"
)

func (s *Store) ClaimRun(ctx context.Context, params ClaimRunParams) (RunLaunch, error) {
	fingerprint, err := inboxFingerprint(struct {
		RunID string `json:"run_id"`
	}{params.RunID})
	if err != nil {
		return RunLaunch{}, err
	}
	tx, authentication, err := s.beginRunTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return RunLaunch{}, err
	}
	defer tx.Rollback()
	run, err := requireOwnedRun(ctx, tx, authentication.Principal.ID, params.RunID)
	if err != nil {
		return RunLaunch{}, err
	}
	if replay, found, err := replayRunLaunchRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationClaimRun, fingerprint, run, authentication.Proof); err != nil {
		return RunLaunch{}, err
	} else if found {
		return commitInboxReplay(tx, replay)
	}
	current, found, err := currentRunLaunch(ctx, tx, run.ID)
	if err != nil {
		return RunLaunch{}, err
	}
	var currentLaunch *RunLaunch
	if found {
		currentLaunch = &current
	}
	decision, err := execution.CanClaim(executionRun(run), executionLaunchOrNil(currentLaunch), params.Now)
	if err != nil {
		return RunLaunch{}, err
	}
	var previous *RunLaunch
	if decision.ReplacedLaunchID != "" {
		previous = currentLaunch
	}
	if previous != nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE run_launches
			SET closed_at = max(claimed_at, ?), close_reason = 'replaced'
			WHERE id = ? AND closed_at IS NULL
		`, unixNano(params.Now), previous.ID); err != nil {
			return RunLaunch{}, fmt.Errorf("close expired run launch: %w", err)
		}
	}
	fence, err := allocateRunFence(ctx, tx, run.AgentID)
	if err != nil {
		return RunLaunch{}, err
	}
	launchID := uuid.NewString()
	expiresAt := params.Now.Add(runLeaseTTL)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO run_launches(
			id, run_id, agent_id, holder_computer_id, holder_placement_generation,
			fence, claimed_at, expires_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, launchID, run.ID, run.AgentID, authentication.Proof.ComputerID(),
		authentication.Proof.PlacementGeneration(), fence, unixNano(params.Now), unixNano(expiresAt)); err != nil {
		return RunLaunch{}, fmt.Errorf("persist run launch: %w", err)
	}
	if decision.StartRun {
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET state = 'running', started_at = ? WHERE id = ? AND state = 'accepted'`, unixNano(params.Now), run.ID); err != nil {
			return RunLaunch{}, fmt.Errorf("start run: %w", err)
		}
	}
	launch, err := runLaunchByID(ctx, tx, launchID)
	if err != nil {
		return RunLaunch{}, fmt.Errorf("read claimed run launch: %w", err)
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: authentication.Principal.OrganizationID,
		Actor:          authentication.Principal,
		Action:         AuditRunLaunch,
		TargetKind:     "run",
		TargetID:       run.ID,
		ContextKind:    "computer",
		ContextID:      launch.HolderComputerID,
		RequestID:      params.RequestID,
		Outcome:        "committed",
		Now:            params.Now,
	}); err != nil {
		return RunLaunch{}, err
	}
	if err := persistInboxRequest(ctx, tx, params.RequestID, run.AgentID, operationClaimRun, fingerprint, runLaunchRequestReceipt{Launch: launch}, params.Now); err != nil {
		return RunLaunch{}, err
	}
	if err := tx.Commit(); err != nil {
		return RunLaunch{}, fmt.Errorf("commit run claim: %w", err)
	}
	return launch, nil
}

func (s *Store) RenewRun(ctx context.Context, params RenewRunParams) (RunLaunch, error) {
	fingerprint, err := inboxFingerprint(struct {
		RunID    string `json:"run_id"`
		LaunchID string `json:"launch_id"`
		Fence    uint64 `json:"fence"`
	}{params.RunID, params.LaunchID, params.Fence})
	if err != nil {
		return RunLaunch{}, err
	}
	tx, authentication, err := s.beginRunTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return RunLaunch{}, err
	}
	defer tx.Rollback()
	run, err := requireOwnedRun(ctx, tx, authentication.Principal.ID, params.RunID)
	if err != nil {
		return RunLaunch{}, err
	}
	if replay, found, err := replayRunLaunchRequest(ctx, tx, params.RequestID, authentication.Principal.ID, operationRenewRun, fingerprint, run, authentication.Proof); err != nil {
		return RunLaunch{}, err
	} else if found {
		return commitInboxReplay(tx, replay)
	}
	launch, found, err := currentRunLaunch(ctx, tx, run.ID)
	if err != nil {
		return RunLaunch{}, err
	}
	if !found {
		return RunLaunch{}, execution.ErrRunLaunchStale
	}
	if err := execution.CanRenew(executionRun(run), executionLaunch(launch), params.LaunchID, params.Fence, runLaunchHeldBy(launch, authentication.Proof), params.Now); err != nil {
		return RunLaunch{}, err
	}
	expiresAt := params.Now.Add(runLeaseTTL)
	if _, err := tx.ExecContext(ctx, `UPDATE run_launches SET expires_at = ? WHERE id = ? AND closed_at IS NULL`, unixNano(expiresAt), launch.ID); err != nil {
		return RunLaunch{}, fmt.Errorf("renew run launch: %w", err)
	}
	launch, err = runLaunchByID(ctx, tx, launch.ID)
	if err != nil {
		return RunLaunch{}, fmt.Errorf("read renewed run launch: %w", err)
	}
	if err := persistInboxRequest(ctx, tx, params.RequestID, run.AgentID, operationRenewRun, fingerprint, runLaunchRequestReceipt{Launch: launch}, params.Now); err != nil {
		return RunLaunch{}, err
	}
	if err := tx.Commit(); err != nil {
		return RunLaunch{}, fmt.Errorf("commit run renewal: %w", err)
	}
	return launch, nil
}
