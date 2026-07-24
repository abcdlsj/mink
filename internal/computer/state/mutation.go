package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (s *State) BeginMutation(ctx context.Context, attempt MutationAttempt) (MutationAttempt, error) {
	if attempt.RequestID == "" {
		attempt.RequestID = uuid.NewString()
	}
	if !validID(attempt.RequestID) || attempt.Operation == "" || attempt.SubjectID == "" || attempt.CreatedAt.IsZero() {
		return MutationAttempt{}, errors.New("mutation attempt is invalid")
	}
	attempt.Status = MutationPending
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MutationAttempt{}, fmt.Errorf("begin mutation transaction: %w", err)
	}
	defer tx.Rollback()
	var existing MutationAttempt
	var payloadHash []byte
	var createdAt int64
	err = tx.QueryRowContext(ctx, `
		SELECT request_id, payload_hash, run_id, attempt, fence, created_at
		FROM mutation_attempts
		WHERE operation = ? AND subject_id = ? AND status = 'pending'
	`, attempt.Operation, attempt.SubjectID).Scan(&existing.RequestID, &payloadHash, &existing.RunID, &existing.Attempt, &existing.Fence, &createdAt)
	if err == nil {
		existing.Operation = attempt.Operation
		existing.SubjectID = attempt.SubjectID
		existing.Status = MutationPending
		existing.CreatedAt = fromUnixNano(createdAt)
		if len(payloadHash) != sha256.Size {
			return MutationAttempt{}, errors.New("pending mutation payload hash is invalid")
		}
		copy(existing.PayloadHash[:], payloadHash)
		if !samePendingMutation(existing, attempt) {
			return MutationAttempt{}, errors.New("pending mutation conflicts with requested attempt")
		}
		if err := tx.Commit(); err != nil {
			return MutationAttempt{}, fmt.Errorf("commit mutation replay: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return MutationAttempt{}, fmt.Errorf("read pending mutation: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO mutation_attempts(
			request_id, operation, subject_id, payload_hash, status, run_id, attempt, fence, created_at
		) VALUES(?, ?, ?, ?, 'pending', ?, ?, ?, ?)
	`, attempt.RequestID, attempt.Operation, attempt.SubjectID, attempt.PayloadHash[:], attempt.RunID, attempt.Attempt, attempt.Fence, unixNano(attempt.CreatedAt))
	if err != nil {
		return MutationAttempt{}, fmt.Errorf("begin mutation attempt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return MutationAttempt{}, fmt.Errorf("commit mutation attempt: %w", err)
	}
	if err := s.secureSQLiteFiles(); err != nil {
		return MutationAttempt{}, err
	}
	return attempt, nil
}

func (s *State) CompleteMutation(ctx context.Context, requestID string, status MutationStatus, responseAttempt, responseFence uint64, responseExpiresAt *time.Time, completedAt time.Time) error {
	if !validID(requestID) || (status != MutationSucceeded && status != MutationFailed) || completedAt.IsZero() {
		return errors.New("mutation completion is invalid")
	}
	var expiresAt any
	if responseExpiresAt != nil {
		expiresAt = unixNano(*responseExpiresAt)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE mutation_attempts
		SET status = ?, response_attempt = ?, response_fence = ?, response_expires_at = ?, completed_at = ?
		WHERE request_id = ? AND status = 'pending'
	`, status, responseAttempt, responseFence, expiresAt, unixNano(completedAt), requestID)
	if err != nil {
		return fmt.Errorf("complete mutation attempt: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("pending mutation attempt not found")
	}
	return s.secureSQLiteFiles()
}

func (s *State) MutationAttempts(ctx context.Context, operation MutationOperation, subjectID string) ([]MutationAttempt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT request_id, operation, subject_id, payload_hash, status, run_id, attempt, fence,
		       response_attempt, response_fence, response_expires_at, created_at, completed_at
		FROM mutation_attempts
		WHERE operation = ? AND subject_id = ?
		ORDER BY created_at, request_id
	`, operation, subjectID)
	if err != nil {
		return nil, fmt.Errorf("list mutation attempts: %w", err)
	}
	defer rows.Close()
	var attempts []MutationAttempt
	for rows.Next() {
		var attempt MutationAttempt
		var payloadHash []byte
		var responseExpiresAt, completedAt sql.NullInt64
		var createdAt int64
		if err := rows.Scan(&attempt.RequestID, &attempt.Operation, &attempt.SubjectID, &payloadHash, &attempt.Status, &attempt.RunID, &attempt.Attempt, &attempt.Fence, &attempt.ResponseAttempt, &attempt.ResponseFence, &responseExpiresAt, &createdAt, &completedAt); err != nil {
			return nil, fmt.Errorf("scan mutation attempt: %w", err)
		}
		if len(payloadHash) != sha256.Size {
			return nil, errors.New("mutation payload hash is invalid")
		}
		copy(attempt.PayloadHash[:], payloadHash)
		attempt.CreatedAt = fromUnixNano(createdAt)
		if responseExpiresAt.Valid {
			value := fromUnixNano(responseExpiresAt.Int64)
			attempt.ResponseExpiresAt = &value
		}
		if completedAt.Valid {
			value := fromUnixNano(completedAt.Int64)
			attempt.CompletedAt = &value
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mutation attempts: %w", err)
	}
	return attempts, nil
}

func samePendingMutation(left, right MutationAttempt) bool {
	return left.Operation == right.Operation && left.SubjectID == right.SubjectID &&
		left.PayloadHash == right.PayloadHash && left.RunID == right.RunID &&
		left.Attempt == right.Attempt && left.Fence == right.Fence
}
