package computerstate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

const (
	stateDirectory = "computer"
	databaseName   = "state.db"
	lockName       = "state.lock"
)

//go:embed schema.sql
var schema string

type State struct {
	db       *sql.DB
	dir      string
	lockFile *os.File
}

type PairingAttempt struct {
	ServerURL       string
	PairingToken    string
	RequestID       string
	RegistrationKey string
	Name            string
	OS              string
	Arch            string
	CreatedAt       time.Time
}

type Identity struct {
	ServerURL       string
	ComputerID      string
	RegistrationKey string
	PairedAt        time.Time
}

type RuntimeSession struct {
	AgentID             string
	ComputerID          string
	PlacementGeneration uint64
	Token               string
	ExpiresAt           time.Time
	UpdatedAt           time.Time
}

type MutationAttempt struct {
	RequestID         string
	Operation         string
	SubjectID         string
	PayloadHash       [sha256.Size]byte
	Status            string
	RunID             string
	LaunchID          string
	Fence             uint64
	ResponseLaunchID  string
	ResponseFence     uint64
	ResponseExpiresAt *time.Time
	CreatedAt         time.Time
	CompletedAt       *time.Time
}

type OutboxEvent struct {
	Sequence            uint64
	OutboxEventID       string
	RequestID           string
	AgentID             string
	PlacementGeneration uint64
	RunID               string
	LaunchID            string
	Fence               uint64
	Outcome             string
	Body                string
	State               string
	RejectionCode       string
	MentionedAgentIDs   []string
	CreatedAt           time.Time
	LastAttemptAt       *time.Time
	Attempts            uint64
}

func Open(dataRoot string) (*State, error) {
	if dataRoot == "" {
		return nil, errors.New("computer state data root is required")
	}
	dataDirectory := filepath.Join(dataRoot, "data")
	directory := filepath.Join(dataDirectory, stateDirectory)
	for _, path := range []string{dataRoot, dataDirectory, directory} {
		if err := ensureDirectory(path); err != nil {
			return nil, err
		}
	}
	lockFile, err := openSecureFile(filepath.Join(directory, lockName))
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("lock computer state: %w", err)
	}
	databasePath := filepath.Join(directory, databaseName)
	if err := inspectExistingStateFiles(directory, databaseName+"-wal", databaseName+"-shm"); err != nil {
		unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
		lockFile.Close()
		return nil, err
	}
	databaseFile, err := openSecureFile(databasePath)
	if err != nil {
		unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
		lockFile.Close()
		return nil, err
	}
	if err := databaseFile.Close(); err != nil {
		unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
		lockFile.Close()
		return nil, fmt.Errorf("close computer state database seed: %w", err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
		lockFile.Close()
		return nil, fmt.Errorf("open computer state: %w", err)
	}
	database.SetMaxOpenConns(1)
	state := &State{db: database, dir: directory, lockFile: lockFile}
	if err := state.configure(); err != nil {
		state.Close()
		return nil, err
	}
	if err := state.secureSQLiteFiles(); err != nil {
		state.Close()
		return nil, err
	}
	return state, nil
}

func (s *State) Close() error {
	var closeErrors []error
	if s.db != nil {
		if _, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			closeErrors = append(closeErrors, err)
		}
		if err := s.db.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	if s.lockFile != nil {
		if err := unix.Flock(int(s.lockFile.Fd()), unix.LOCK_UN); err != nil {
			closeErrors = append(closeErrors, err)
		}
		if err := s.lockFile.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	return errors.Join(closeErrors...)
}

func (s *State) SavePairingAttempt(ctx context.Context, attempt PairingAttempt) error {
	if !validID(attempt.RequestID) || attempt.ServerURL == "" || !validSecret(attempt.PairingToken) || attempt.RegistrationKey == "" || attempt.CreatedAt.IsZero() {
		return errors.New("pairing attempt is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save pairing attempt: %w", err)
	}
	defer tx.Rollback()
	var identityExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM computer_identity WHERE singleton = 1)`).Scan(&identityExists); err != nil {
		return fmt.Errorf("check computer identity before pairing: %w", err)
	}
	if identityExists {
		return errors.New("computer identity already exists")
	}
	var existing PairingAttempt
	var createdAt int64
	err = tx.QueryRowContext(ctx, `
		SELECT server_url, pairing_token, request_id, registration_key, name, os, arch, created_at
		FROM pairing_attempt WHERE singleton = 1
	`).Scan(&existing.ServerURL, &existing.PairingToken, &existing.RequestID, &existing.RegistrationKey, &existing.Name, &existing.OS, &existing.Arch, &createdAt)
	if err == nil {
		existing.CreatedAt = fromUnixNano(createdAt)
		if !samePairingAttempt(existing, attempt) {
			return errors.New("pairing attempt conflicts with persisted attempt")
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit pairing attempt replay: %w", err)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read pairing attempt before save: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO pairing_attempt(singleton, server_url, pairing_token, request_id, registration_key, name, os, arch, created_at)
		VALUES(1, ?, ?, ?, ?, ?, ?, ?, ?)
	`, attempt.ServerURL, attempt.PairingToken, attempt.RequestID, attempt.RegistrationKey, attempt.Name, attempt.OS, attempt.Arch, unixNano(attempt.CreatedAt))
	if err != nil {
		return fmt.Errorf("save pairing attempt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pairing attempt: %w", err)
	}
	return s.secureSQLiteFiles()
}

func (s *State) PairingAttempt(ctx context.Context) (PairingAttempt, bool, error) {
	var attempt PairingAttempt
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT server_url, pairing_token, request_id, registration_key, name, os, arch, created_at
		FROM pairing_attempt WHERE singleton = 1
	`).Scan(&attempt.ServerURL, &attempt.PairingToken, &attempt.RequestID, &attempt.RegistrationKey, &attempt.Name, &attempt.OS, &attempt.Arch, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PairingAttempt{}, false, nil
	}
	if err != nil {
		return PairingAttempt{}, false, fmt.Errorf("read pairing attempt: %w", err)
	}
	attempt.CreatedAt = fromUnixNano(createdAt)
	return attempt, true, nil
}

func (s *State) CompletePairing(ctx context.Context, identity Identity) error {
	if !validID(identity.ComputerID) || identity.ServerURL == "" || identity.RegistrationKey == "" || identity.PairedAt.IsZero() {
		return errors.New("computer identity is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin complete pairing: %w", err)
	}
	defer tx.Rollback()
	var existing Identity
	var existingPairedAt int64
	err = tx.QueryRowContext(ctx, `
		SELECT server_url, computer_id, registration_key, paired_at
		FROM computer_identity WHERE singleton = 1
	`).Scan(&existing.ServerURL, &existing.ComputerID, &existing.RegistrationKey, &existingPairedAt)
	if err == nil {
		existing.PairedAt = fromUnixNano(existingPairedAt)
		if !sameIdentity(existing, identity) {
			return errors.New("computer identity does not match persisted identity")
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit pairing completion replay: %w", err)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read computer identity before pairing completion: %w", err)
	}
	var attemptServer, attemptKey string
	if err := tx.QueryRowContext(ctx, `SELECT server_url, registration_key FROM pairing_attempt WHERE singleton = 1`).Scan(&attemptServer, &attemptKey); err != nil {
		return fmt.Errorf("read pairing attempt for completion: %w", err)
	}
	if attemptServer != identity.ServerURL || attemptKey != identity.RegistrationKey {
		return errors.New("computer identity does not match pairing attempt")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO computer_identity(singleton, server_url, computer_id, registration_key, paired_at)
		VALUES(1, ?, ?, ?, ?)
	`, identity.ServerURL, identity.ComputerID, identity.RegistrationKey, unixNano(identity.PairedAt)); err != nil {
		return fmt.Errorf("save computer identity: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pairing_attempt WHERE singleton = 1`); err != nil {
		return fmt.Errorf("delete pairing attempt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pairing: %w", err)
	}
	return s.secureSQLiteFiles()
}

func (s *State) SaveIdentity(ctx context.Context, identity Identity) error {
	if !validID(identity.ComputerID) || identity.ServerURL == "" || identity.RegistrationKey == "" || identity.PairedAt.IsZero() {
		return errors.New("computer identity is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save computer identity: %w", err)
	}
	defer tx.Rollback()
	var existing Identity
	var existingPairedAt int64
	err = tx.QueryRowContext(ctx, `
		SELECT server_url, computer_id, registration_key, paired_at
		FROM computer_identity WHERE singleton = 1
	`).Scan(&existing.ServerURL, &existing.ComputerID, &existing.RegistrationKey, &existingPairedAt)
	if err == nil {
		existing.PairedAt = fromUnixNano(existingPairedAt)
		if !sameIdentity(existing, identity) {
			return errors.New("computer identity does not match persisted identity")
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit computer identity replay: %w", err)
		}
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read computer identity server: %w", err)
	}
	var pairingExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pairing_attempt WHERE singleton = 1)`).Scan(&pairingExists); err != nil {
		return fmt.Errorf("check pairing attempt before identity import: %w", err)
	}
	if pairingExists {
		return errors.New("pairing attempt already exists")
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO computer_identity(singleton, server_url, computer_id, registration_key, paired_at)
		VALUES(1, ?, ?, ?, ?)
	`, identity.ServerURL, identity.ComputerID, identity.RegistrationKey, unixNano(identity.PairedAt))
	if err != nil {
		return fmt.Errorf("save computer identity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit computer identity: %w", err)
	}
	return s.secureSQLiteFiles()
}

func (s *State) Identity(ctx context.Context) (Identity, bool, error) {
	var identity Identity
	var pairedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT server_url, computer_id, registration_key, paired_at
		FROM computer_identity WHERE singleton = 1
	`).Scan(&identity.ServerURL, &identity.ComputerID, &identity.RegistrationKey, &pairedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, false, nil
	}
	if err != nil {
		return Identity{}, false, fmt.Errorf("read computer identity: %w", err)
	}
	identity.PairedAt = fromUnixNano(pairedAt)
	return identity, true, nil
}

func (s *State) SaveRuntimeSession(ctx context.Context, session RuntimeSession) error {
	if !validID(session.AgentID) || !validID(session.ComputerID) || session.PlacementGeneration == 0 || session.Token == "" || session.ExpiresAt.IsZero() || session.UpdatedAt.IsZero() {
		return errors.New("runtime session is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save runtime session: %w", err)
	}
	defer tx.Rollback()
	existing, found, err := readRuntimeSession(tx.QueryRowContext(ctx, `
		SELECT agent_id, computer_id, placement_generation, token, expires_at, updated_at
		FROM runtime_sessions WHERE agent_id = ?
	`, session.AgentID))
	if err != nil {
		return err
	}
	if found {
		if session.PlacementGeneration < existing.PlacementGeneration {
			return errors.New("runtime session placement generation is stale")
		}
		if session.PlacementGeneration == existing.PlacementGeneration {
			if session.ComputerID != existing.ComputerID {
				return errors.New("runtime session computer conflicts with current binding")
			}
			if session.UpdatedAt.Before(existing.UpdatedAt) || session.ExpiresAt.Before(existing.ExpiresAt) {
				return errors.New("runtime session response is stale")
			}
			if session.UpdatedAt.Equal(existing.UpdatedAt) && session.ExpiresAt.Equal(existing.ExpiresAt) && session.Token != existing.Token {
				return errors.New("runtime session response conflicts with current token")
			}
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE runtime_sessions
			SET computer_id = ?, placement_generation = ?, token = ?, expires_at = ?, updated_at = ?
			WHERE agent_id = ? AND placement_generation = ?
		`, session.ComputerID, session.PlacementGeneration, session.Token, unixNano(session.ExpiresAt), unixNano(session.UpdatedAt), session.AgentID, existing.PlacementGeneration)
		if err != nil {
			return fmt.Errorf("update runtime session: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil || updated != 1 {
			return errors.New("runtime session binding changed during update")
		}
	} else if _, err := tx.ExecContext(ctx, `
		INSERT INTO runtime_sessions(agent_id, computer_id, placement_generation, token, expires_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)
	`, session.AgentID, session.ComputerID, session.PlacementGeneration, session.Token, unixNano(session.ExpiresAt), unixNano(session.UpdatedAt)); err != nil {
		return fmt.Errorf("insert runtime session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit runtime session: %w", err)
	}
	return s.secureSQLiteFiles()
}

func (s *State) DeleteRuntimeSession(ctx context.Context, agentID, computerID string, placementGeneration uint64) error {
	if !validID(agentID) || !validID(computerID) || placementGeneration == 0 {
		return errors.New("runtime session binding is invalid")
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM runtime_sessions
		WHERE agent_id = ? AND computer_id = ? AND placement_generation = ?
	`, agentID, computerID, placementGeneration); err != nil {
		return fmt.Errorf("delete runtime session: %w", err)
	}
	return s.secureSQLiteFiles()
}

func (s *State) RuntimeSessions(ctx context.Context) ([]RuntimeSession, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_id, computer_id, placement_generation, token, expires_at, updated_at
		FROM runtime_sessions ORDER BY agent_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list runtime sessions: %w", err)
	}
	defer rows.Close()
	var sessions []RuntimeSession
	for rows.Next() {
		var session RuntimeSession
		var expiresAt, updatedAt int64
		if err := rows.Scan(&session.AgentID, &session.ComputerID, &session.PlacementGeneration, &session.Token, &expiresAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan runtime session: %w", err)
		}
		session.ExpiresAt = fromUnixNano(expiresAt)
		session.UpdatedAt = fromUnixNano(updatedAt)
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtime sessions: %w", err)
	}
	return sessions, nil
}

func (s *State) BeginMutation(ctx context.Context, attempt MutationAttempt) (MutationAttempt, error) {
	if attempt.RequestID == "" {
		attempt.RequestID = uuid.NewString()
	}
	if !validID(attempt.RequestID) || attempt.Operation == "" || attempt.SubjectID == "" || attempt.CreatedAt.IsZero() {
		return MutationAttempt{}, errors.New("mutation attempt is invalid")
	}
	attempt.Status = "pending"
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MutationAttempt{}, fmt.Errorf("begin mutation transaction: %w", err)
	}
	defer tx.Rollback()
	var existing MutationAttempt
	var payloadHash []byte
	var createdAt int64
	err = tx.QueryRowContext(ctx, `
		SELECT request_id, payload_hash, run_id, launch_id, fence, created_at
		FROM mutation_attempts
		WHERE operation = ? AND subject_id = ? AND status = 'pending'
	`, attempt.Operation, attempt.SubjectID).Scan(&existing.RequestID, &payloadHash, &existing.RunID, &existing.LaunchID, &existing.Fence, &createdAt)
	if err == nil {
		existing.Operation = attempt.Operation
		existing.SubjectID = attempt.SubjectID
		existing.Status = "pending"
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
			request_id, operation, subject_id, payload_hash, status, run_id, launch_id, fence, created_at
		) VALUES(?, ?, ?, ?, 'pending', ?, ?, ?, ?)
	`, attempt.RequestID, attempt.Operation, attempt.SubjectID, attempt.PayloadHash[:], attempt.RunID, attempt.LaunchID, attempt.Fence, unixNano(attempt.CreatedAt))
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

func (s *State) CompleteMutation(ctx context.Context, requestID, status, responseLaunchID string, responseFence uint64, responseExpiresAt *time.Time, completedAt time.Time) error {
	if !validID(requestID) || (status != "succeeded" && status != "failed") || completedAt.IsZero() {
		return errors.New("mutation completion is invalid")
	}
	var expiresAt any
	if responseExpiresAt != nil {
		expiresAt = unixNano(*responseExpiresAt)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE mutation_attempts
		SET status = ?, response_launch_id = ?, response_fence = ?, response_expires_at = ?, completed_at = ?
		WHERE request_id = ? AND status = 'pending'
	`, status, responseLaunchID, responseFence, expiresAt, unixNano(completedAt), requestID)
	if err != nil {
		return fmt.Errorf("complete mutation attempt: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("pending mutation attempt not found")
	}
	return s.secureSQLiteFiles()
}

func (s *State) MutationAttempts(ctx context.Context, operation, subjectID string) ([]MutationAttempt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT request_id, operation, subject_id, payload_hash, status, run_id, launch_id, fence,
		       response_launch_id, response_fence, response_expires_at, created_at, completed_at
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
		if err := rows.Scan(&attempt.RequestID, &attempt.Operation, &attempt.SubjectID, &payloadHash, &attempt.Status, &attempt.RunID, &attempt.LaunchID, &attempt.Fence, &attempt.ResponseLaunchID, &attempt.ResponseFence, &responseExpiresAt, &createdAt, &completedAt); err != nil {
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

func (s *State) EnqueueOutbox(ctx context.Context, event OutboxEvent) error {
	if !validID(event.OutboxEventID) || !validID(event.RequestID) || !validID(event.AgentID) || !validID(event.RunID) || !validID(event.LaunchID) || event.PlacementGeneration == 0 || event.Fence == 0 || (event.Outcome != "succeeded" && event.Outcome != "failed") || event.CreatedAt.IsZero() {
		return errors.New("outbox event is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin enqueue outbox: %w", err)
	}
	defer tx.Rollback()
	existing, found, err := outboxCollision(ctx, tx, event)
	if err != nil {
		return err
	}
	if found {
		if !sameOutboxEvent(existing, event) {
			return errors.New("outbox event conflicts with persisted event")
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit outbox replay: %w", err)
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_events(
			outbox_event_id, request_id, agent_id, placement_generation, run_id, launch_id,
			fence, outcome, body, state, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)
	`, event.OutboxEventID, event.RequestID, event.AgentID, event.PlacementGeneration, event.RunID, event.LaunchID, event.Fence, event.Outcome, event.Body, unixNano(event.CreatedAt)); err != nil {
		return fmt.Errorf("persist outbox event: %w", err)
	}
	for ordinal, agentID := range event.MentionedAgentIDs {
		if !validID(agentID) {
			return errors.New("outbox mention agent id is invalid")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO outbox_mentions(outbox_event_id, ordinal, agent_id) VALUES(?, ?, ?)`, event.OutboxEventID, ordinal, agentID); err != nil {
			return fmt.Errorf("persist outbox mention: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit enqueue outbox: %w", err)
	}
	return s.secureSQLiteFiles()
}

func outboxCollision(ctx context.Context, tx *sql.Tx, candidate OutboxEvent) (OutboxEvent, bool, error) {
	var event OutboxEvent
	var createdAt int64
	err := tx.QueryRowContext(ctx, `
		SELECT sequence, outbox_event_id, request_id, agent_id, placement_generation, run_id, launch_id,
		       fence, outcome, body, state, rejection_code, created_at, attempts
		FROM outbox_events
		WHERE outbox_event_id = ? OR request_id = ? OR (run_id = ? AND launch_id = ? AND fence = ?)
		LIMIT 1
	`, candidate.OutboxEventID, candidate.RequestID, candidate.RunID, candidate.LaunchID, candidate.Fence).Scan(
		&event.Sequence, &event.OutboxEventID, &event.RequestID, &event.AgentID, &event.PlacementGeneration,
		&event.RunID, &event.LaunchID, &event.Fence, &event.Outcome, &event.Body, &event.State,
		&event.RejectionCode, &createdAt, &event.Attempts,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return OutboxEvent{}, false, nil
	}
	if err != nil {
		return OutboxEvent{}, false, fmt.Errorf("read outbox collision: %w", err)
	}
	event.CreatedAt = fromUnixNano(createdAt)
	rows, err := tx.QueryContext(ctx, `SELECT agent_id FROM outbox_mentions WHERE outbox_event_id = ? ORDER BY ordinal`, event.OutboxEventID)
	if err != nil {
		return OutboxEvent{}, false, fmt.Errorf("list outbox collision mentions: %w", err)
	}
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			rows.Close()
			return OutboxEvent{}, false, fmt.Errorf("scan outbox collision mention: %w", err)
		}
		event.MentionedAgentIDs = append(event.MentionedAgentIDs, agentID)
	}
	if err := rows.Close(); err != nil {
		return OutboxEvent{}, false, fmt.Errorf("close outbox collision mentions: %w", err)
	}
	return event, true, nil
}

func sameOutboxEvent(existing, candidate OutboxEvent) bool {
	if existing.OutboxEventID != candidate.OutboxEventID || existing.RequestID != candidate.RequestID ||
		existing.AgentID != candidate.AgentID || existing.PlacementGeneration != candidate.PlacementGeneration ||
		existing.RunID != candidate.RunID || existing.LaunchID != candidate.LaunchID || existing.Fence != candidate.Fence ||
		existing.Outcome != candidate.Outcome || existing.Body != candidate.Body || existing.State != "pending" ||
		existing.RejectionCode != "" || !existing.CreatedAt.Equal(candidate.CreatedAt) ||
		len(existing.MentionedAgentIDs) != len(candidate.MentionedAgentIDs) {
		return false
	}
	for index := range existing.MentionedAgentIDs {
		if existing.MentionedAgentIDs[index] != candidate.MentionedAgentIDs[index] {
			return false
		}
	}
	return true
}

func (s *State) TombstoneOutbox(ctx context.Context, eventID, rejectionCode string) error {
	if !validID(eventID) || rejectionCode == "" {
		return errors.New("outbox tombstone is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tombstone outbox: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM outbox_mentions WHERE outbox_event_id = ?`, eventID); err != nil {
		return fmt.Errorf("delete tombstone mentions: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE outbox_events SET state = 'tombstone', body = '', rejection_code = ?
		WHERE outbox_event_id = ? AND state = 'pending'
	`, rejectionCode, eventID)
	if err != nil {
		return fmt.Errorf("tombstone outbox event: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("pending outbox event not found")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tombstone outbox: %w", err)
	}
	return s.secureSQLiteFiles()
}

func (s *State) AckOutbox(ctx context.Context, eventID string) error {
	if !validID(eventID) {
		return errors.New("outbox event id is invalid")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM outbox_events WHERE outbox_event_id = ? AND state = 'pending'`, eventID)
	if err != nil {
		return fmt.Errorf("ack outbox event: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil || deleted != 1 {
		return errors.New("pending outbox event not found")
	}
	return s.secureSQLiteFiles()
}

func (s *State) Outbox(ctx context.Context) ([]OutboxEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sequence, outbox_event_id, request_id, agent_id, placement_generation, run_id, launch_id,
		       fence, outcome, body, state, rejection_code, created_at, last_attempt_at, attempts
		FROM outbox_events ORDER BY sequence
	`)
	if err != nil {
		return nil, fmt.Errorf("list outbox events: %w", err)
	}
	var events []OutboxEvent
	for rows.Next() {
		var event OutboxEvent
		var createdAt int64
		var lastAttemptAt sql.NullInt64
		if err := rows.Scan(&event.Sequence, &event.OutboxEventID, &event.RequestID, &event.AgentID, &event.PlacementGeneration, &event.RunID, &event.LaunchID, &event.Fence, &event.Outcome, &event.Body, &event.State, &event.RejectionCode, &createdAt, &lastAttemptAt, &event.Attempts); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		event.CreatedAt = fromUnixNano(createdAt)
		if lastAttemptAt.Valid {
			value := fromUnixNano(lastAttemptAt.Int64)
			event.LastAttemptAt = &value
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate outbox events: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close outbox events: %w", err)
	}
	for index := range events {
		mentions, err := s.outboxMentions(ctx, events[index].OutboxEventID)
		if err != nil {
			return nil, err
		}
		events[index].MentionedAgentIDs = mentions
	}
	return events, nil
}

func (s *State) outboxMentions(ctx context.Context, eventID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT agent_id FROM outbox_mentions WHERE outbox_event_id = ? ORDER BY ordinal`, eventID)
	if err != nil {
		return nil, fmt.Errorf("list outbox mentions: %w", err)
	}
	defer rows.Close()
	var mentions []string
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			return nil, fmt.Errorf("scan outbox mention: %w", err)
		}
		mentions = append(mentions, agentID)
	}
	return mentions, rows.Err()
}

func (s *State) configure() error {
	for _, statement := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA synchronous = FULL",
		"PRAGMA busy_timeout = 5000",
		schema,
	} {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("configure computer state: %w", err)
		}
	}
	return nil
}

func (s *State) secureSQLiteFiles() error {
	for _, name := range []string{databaseName, databaseName + "-wal", databaseName + "-shm", lockName} {
		path := filepath.Join(s.dir, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect computer state file: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("computer state file %s is not regular", name)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("secure computer state file: %w", err)
		}
	}
	return nil
}

func ensureDirectory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create computer state directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect computer state directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("computer state path is not a real directory")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure computer state directory: %w", err)
	}
	return nil
}

func openSecureFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("computer state file is not regular")
		}
		if info.Mode().Perm() != 0o600 {
			return nil, fmt.Errorf("computer state file mode is %o, want 600", info.Mode().Perm())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect computer state file: %w", err)
	}
	descriptor, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open computer state file: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), path)
	after, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("inspect open computer state file: %w", err)
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(after, current) || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || current.Mode().Perm() != 0o600 {
		file.Close()
		return nil, errors.New("computer state file changed while opening")
	}
	return file, nil
}

func inspectExistingStateFiles(directory string, names ...string) error {
	for _, name := range names {
		info, err := os.Lstat(filepath.Join(directory, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect computer state file: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("computer state file %s is not regular", name)
		}
		if info.Mode().Perm() != 0o600 {
			return fmt.Errorf("computer state file %s mode is %o, want 600", name, info.Mode().Perm())
		}
	}
	return nil
}

func validID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func validSecret(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func samePairingAttempt(left, right PairingAttempt) bool {
	return left.ServerURL == right.ServerURL && left.PairingToken == right.PairingToken &&
		left.RequestID == right.RequestID && left.RegistrationKey == right.RegistrationKey &&
		left.Name == right.Name && left.OS == right.OS && left.Arch == right.Arch &&
		left.CreatedAt.Equal(right.CreatedAt)
}

func sameIdentity(left, right Identity) bool {
	return left.ServerURL == right.ServerURL && left.ComputerID == right.ComputerID &&
		left.RegistrationKey == right.RegistrationKey && left.PairedAt.Equal(right.PairedAt)
}

func readRuntimeSession(row interface{ Scan(...any) error }) (RuntimeSession, bool, error) {
	var session RuntimeSession
	var expiresAt, updatedAt int64
	err := row.Scan(&session.AgentID, &session.ComputerID, &session.PlacementGeneration, &session.Token, &expiresAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeSession{}, false, nil
	}
	if err != nil {
		return RuntimeSession{}, false, fmt.Errorf("read runtime session: %w", err)
	}
	session.ExpiresAt = fromUnixNano(expiresAt)
	session.UpdatedAt = fromUnixNano(updatedAt)
	return session, true, nil
}

func samePendingMutation(left, right MutationAttempt) bool {
	return left.Operation == right.Operation && left.SubjectID == right.SubjectID &&
		left.PayloadHash == right.PayloadHash && left.RunID == right.RunID &&
		left.LaunchID == right.LaunchID && left.Fence == right.Fence
}

func unixNano(value time.Time) int64 {
	return value.UTC().UnixNano()
}

func fromUnixNano(value int64) time.Time {
	return time.Unix(0, value).UTC()
}
