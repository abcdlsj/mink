package state

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

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

func (s *State) ReplacePairingAttempt(ctx context.Context, expected, replacement PairingAttempt) error {
	if !validID(expected.RequestID) || expected.ServerURL == "" || !validSecret(expected.PairingToken) ||
		expected.RegistrationKey == "" || expected.CreatedAt.IsZero() {
		return errors.New("expected pairing attempt is invalid")
	}
	if !validID(replacement.RequestID) || replacement.ServerURL != expected.ServerURL || !validSecret(replacement.PairingToken) ||
		replacement.PairingToken == expected.PairingToken || replacement.RegistrationKey == "" || replacement.CreatedAt.IsZero() {
		return errors.New("replacement pairing attempt is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace pairing attempt: %w", err)
	}
	defer tx.Rollback()
	var identityExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM computer_identity WHERE singleton = 1)`).Scan(&identityExists); err != nil {
		return fmt.Errorf("check computer identity before pairing replacement: %w", err)
	}
	if identityExists {
		return errors.New("computer identity already exists")
	}
	var existing PairingAttempt
	var createdAt int64
	if err := tx.QueryRowContext(ctx, `
		SELECT server_url, pairing_token, request_id, registration_key, name, os, arch, created_at
		FROM pairing_attempt WHERE singleton = 1
	`).Scan(&existing.ServerURL, &existing.PairingToken, &existing.RequestID, &existing.RegistrationKey, &existing.Name, &existing.OS, &existing.Arch, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("pairing attempt not found")
		}
		return fmt.Errorf("read pairing attempt before replacement: %w", err)
	}
	existing.CreatedAt = fromUnixNano(createdAt)
	if !samePairingAttempt(existing, expected) {
		return errors.New("pairing attempt does not match expected attempt")
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE pairing_attempt
		SET server_url = ?, pairing_token = ?, request_id = ?, registration_key = ?, name = ?, os = ?, arch = ?, created_at = ?
		WHERE singleton = 1 AND request_id = ?
	`, replacement.ServerURL, replacement.PairingToken, replacement.RequestID, replacement.RegistrationKey,
		replacement.Name, replacement.OS, replacement.Arch, unixNano(replacement.CreatedAt), expected.RequestID)
	if err != nil {
		return fmt.Errorf("replace pairing attempt: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("pairing attempt changed during replacement")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pairing attempt replacement: %w", err)
	}
	return s.secureSQLiteFiles()
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
