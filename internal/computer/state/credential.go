package state

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type CredentialDeliveryKey struct {
	KeyID      string
	PrivateKey [32]byte
	PublicKey  [32]byte
	CreatedAt  time.Time
}

type CredentialBinding struct {
	Handle         string
	DeliveryID     string
	AgentID        string
	ComputerID     string
	CredentialKind string
	KeyID          string
	CreatedAt      time.Time
}

func (s *State) ActiveCredentialDeliveryKey(ctx context.Context) (CredentialDeliveryKey, bool, error) {
	return s.credentialDeliveryKey(ctx, `WHERE active = 1`)
}

func (s *State) CredentialDeliveryKey(ctx context.Context, keyID string) (CredentialDeliveryKey, bool, error) {
	return s.credentialDeliveryKey(ctx, `WHERE key_id = ?`, keyID)
}

func (s *State) credentialDeliveryKey(ctx context.Context, condition string, arguments ...any) (CredentialDeliveryKey, bool, error) {
	var key CredentialDeliveryKey
	var privateKey, publicKey []byte
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT key_id, private_key, public_key, created_at
		FROM credential_delivery_keys `+condition, arguments...).Scan(&key.KeyID, &privateKey, &publicKey, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CredentialDeliveryKey{}, false, nil
	}
	if err != nil {
		return CredentialDeliveryKey{}, false, fmt.Errorf("read credential delivery key: %w", err)
	}
	if len(privateKey) != len(key.PrivateKey) || len(publicKey) != len(key.PublicKey) {
		return CredentialDeliveryKey{}, false, errors.New("credential delivery key material is invalid")
	}
	copy(key.PrivateKey[:], privateKey)
	copy(key.PublicKey[:], publicKey)
	key.CreatedAt = fromUnixNano(createdAt)
	return key, true, nil
}

func (s *State) SaveCredentialDeliveryKey(ctx context.Context, key CredentialDeliveryKey) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin credential delivery key save: %w", err)
	}
	defer tx.Rollback()
	var existingPrivate, existingPublic []byte
	err = tx.QueryRowContext(ctx, `SELECT private_key, public_key FROM credential_delivery_keys WHERE key_id = ?`, key.KeyID).Scan(&existingPrivate, &existingPublic)
	if err == nil {
		if !bytes.Equal(existingPrivate, key.PrivateKey[:]) || !bytes.Equal(existingPublic, key.PublicKey[:]) {
			return errors.New("credential delivery key ID conflicts")
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit credential delivery key replay: %w", err)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read credential delivery key replay: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE credential_delivery_keys SET active = 0 WHERE active = 1`); err != nil {
		return fmt.Errorf("deactivate credential delivery key: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO credential_delivery_keys(key_id, private_key, public_key, active, created_at)
		VALUES(?, ?, ?, 1, ?)
	`, key.KeyID, key.PrivateKey[:], key.PublicKey[:], unixNano(key.CreatedAt)); err != nil {
		return fmt.Errorf("persist credential delivery key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit credential delivery key: %w", err)
	}
	return nil
}

func (s *State) SaveCredentialBinding(ctx context.Context, binding CredentialBinding) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO credential_bindings(handle, delivery_id, agent_id, computer_id, credential_kind, key_id, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, binding.Handle, binding.DeliveryID, binding.AgentID, binding.ComputerID, binding.CredentialKind, binding.KeyID, unixNano(binding.CreatedAt))
	if err == nil {
		return nil
	}
	existing, found, readErr := s.CredentialBinding(ctx, binding.Handle)
	if readErr != nil {
		return readErr
	}
	if found && existing.Handle == binding.Handle && existing.DeliveryID == binding.DeliveryID &&
		existing.AgentID == binding.AgentID && existing.ComputerID == binding.ComputerID &&
		existing.CredentialKind == binding.CredentialKind && existing.KeyID == binding.KeyID &&
		existing.CreatedAt.Equal(binding.CreatedAt) {
		return nil
	}
	return fmt.Errorf("persist credential binding: %w", err)
}

func (s *State) CredentialBinding(ctx context.Context, handle string) (CredentialBinding, bool, error) {
	return s.credentialBinding(ctx, `WHERE handle = ?`, handle)
}

func (s *State) CredentialBindingByDelivery(ctx context.Context, deliveryID string) (CredentialBinding, bool, error) {
	return s.credentialBinding(ctx, `WHERE delivery_id = ?`, deliveryID)
}

func (s *State) credentialBinding(ctx context.Context, condition string, arguments ...any) (CredentialBinding, bool, error) {
	var binding CredentialBinding
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT handle, delivery_id, agent_id, computer_id, credential_kind, key_id, created_at
		FROM credential_bindings `+condition, arguments...).Scan(&binding.Handle, &binding.DeliveryID, &binding.AgentID, &binding.ComputerID, &binding.CredentialKind, &binding.KeyID, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CredentialBinding{}, false, nil
	}
	if err != nil {
		return CredentialBinding{}, false, fmt.Errorf("read credential binding: %w", err)
	}
	binding.CreatedAt = fromUnixNano(createdAt)
	return binding, true, nil
}
