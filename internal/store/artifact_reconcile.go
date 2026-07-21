package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"
)

type ArtifactReconcileResult struct {
	Ready       int
	Missing     int
	Corrupt     int
	Quarantined int
	Deleted     int
}

func (s *ArtifactStore) Reconcile(ctx context.Context, now time.Time) (ArtifactReconcileResult, error) {
	if now.IsZero() {
		return ArtifactReconcileResult{}, ErrArtifactInvalid
	}
	rows, err := s.database.db.QueryContext(ctx, `SELECT digest, size FROM artifact_blobs`)
	if err != nil {
		return ArtifactReconcileResult{}, fmt.Errorf("list referenced artifact blobs: %w", err)
	}
	references := make(map[[sha256.Size]byte]int64)
	for rows.Next() {
		var raw []byte
		var size int64
		if err := rows.Scan(&raw, &size); err != nil {
			rows.Close()
			return ArtifactReconcileResult{}, err
		}
		if len(raw) != sha256.Size {
			rows.Close()
			return ArtifactReconcileResult{}, ErrArtifactIntegrity
		}
		var digest [sha256.Size]byte
		copy(digest[:], raw)
		references[digest] = size
	}
	if err := rows.Close(); err != nil {
		return ArtifactReconcileResult{}, err
	}
	states, quarantined, deleted, err := s.blobs.Reconcile(ctx, references, now, s.quarantineRetention)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ArtifactReconcileResult{}, ctxErr
		}
		return ArtifactReconcileResult{}, ErrArtifactBlobUnavailable
	}
	result := ArtifactReconcileResult{Quarantined: quarantined, Deleted: deleted}
	tx, err := s.database.db.BeginTx(ctx, nil)
	if err != nil {
		return ArtifactReconcileResult{}, fmt.Errorf("begin artifact inventory update: %w", err)
	}
	defer tx.Rollback()
	for digest := range references {
		state := states[digest]
		switch state {
		case "ready":
			result.Ready++
		case "missing":
			result.Missing++
		case "corrupt":
			result.Corrupt++
		default:
			return ArtifactReconcileResult{}, ErrArtifactIntegrity
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE artifact_blobs
			SET integrity_state = ?, checked_at = MAX(checked_at, ?)
			WHERE digest = ?
		`, state, unixNano(now), digest[:]); err != nil {
			return ArtifactReconcileResult{}, fmt.Errorf("update artifact integrity state: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return ArtifactReconcileResult{}, fmt.Errorf("commit artifact inventory update: %w", err)
	}
	return result, nil
}
