package store

import (
	"context"
	"fmt"

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
	var previous *RunLaunch
	switch run.State {
	case RunStateAccepted:
		_, found, err := currentRunLaunch(ctx, tx, run.ID)
		if err != nil {
			return RunLaunch{}, err
		}
		if found {
			return RunLaunch{}, ErrRunIntegrity
		}
		previous = nil
	case RunStateRunning:
		launch, found, err := currentRunLaunch(ctx, tx, run.ID)
		if err != nil {
			return RunLaunch{}, err
		}
		if !found {
			return RunLaunch{}, ErrRunIntegrity
		}
		if launch.ExpiresAt.After(params.Now) {
			return RunLaunch{}, ErrRunLaunchActive
		}
		previous = &launch
	default:
		return RunLaunch{}, ErrRunNotAccepted
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
	`, launchID, run.ID, run.AgentID, authentication.Proof.computerID,
		authentication.Proof.placementGeneration, fence, unixNano(params.Now), unixNano(expiresAt)); err != nil {
		return RunLaunch{}, fmt.Errorf("persist run launch: %w", err)
	}
	if run.State == RunStateAccepted {
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
	if run.State != RunStateRunning {
		return RunLaunch{}, ErrRunNotRunning
	}
	launch, found, err := currentRunLaunch(ctx, tx, run.ID)
	if err != nil {
		return RunLaunch{}, err
	}
	if !found || launch.ID != params.LaunchID || launch.Fence != params.Fence || !runLaunchHeldBy(launch, authentication.Proof) {
		return RunLaunch{}, ErrRunLaunchStale
	}
	if !launch.ExpiresAt.After(params.Now) {
		return RunLaunch{}, ErrRunLaunchExpired
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
