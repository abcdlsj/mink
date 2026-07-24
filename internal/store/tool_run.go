package store

import (
	"context"
	"database/sql"
	"time"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	executiondomain "github.com/abcdlsj/sumi/internal/execution/domain"
)

func requireToolRun(
	ctx context.Context,
	tx *sql.Tx,
	runtime AgentRuntimeAuthentication,
	proof *authorityapp.RunProof,
	now time.Time,
) (Run, bool, error) {
	if proof == nil {
		if runtime.Valid() {
			return Run{}, false, executiondomain.ErrRunLeaseStale
		}
		return Run{}, false, nil
	}
	if !runtime.Valid() || !proof.Valid() {
		return Run{}, false, ErrAgentRuntimeUnauthenticated
	}
	run, err := requireOwnedRun(ctx, tx, runtime.Principal.ID, proof.RunID)
	if err != nil {
		return Run{}, false, err
	}
	if err := executiondomain.ValidateLease(
		executionRun(run), runtime.Proof.ComputerID(), runtime.Proof.PlacementDesiredRevision(),
		proof.Attempt, proof.Fence, now,
	); err != nil {
		return Run{}, false, err
	}
	return run, true, nil
}
