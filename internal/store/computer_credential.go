package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
	"github.com/google/uuid"
)

const credentialDeliveryTTL = 10 * time.Minute

func (s *Store) EnqueueCredentialDelivery(ctx context.Context, command computerapp.EnqueueCredentialDeliveryCommand) (computerapp.CredentialDelivery, error) {
	if !validCredentialDeliveryCommand(command) {
		return computerapp.CredentialDelivery{}, computerapp.ErrCredentialDeliveryInvalid
	}
	fingerprint, err := credentialDeliveryFingerprint(command)
	if err != nil {
		return computerapp.CredentialDelivery{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return computerapp.CredentialDelivery{}, fmt.Errorf("begin credential delivery: %w", err)
	}
	defer tx.Rollback()
	if reason, err := requireGrant(ctx, tx, command.Actor, CapabilityAgentRuntimeConfigure, Scope{Kind: "agent", ID: command.AgentID}, command.Now, ""); err != nil {
		return computerapp.CredentialDelivery{}, err
	} else if reason != "" {
		return computerapp.CredentialDelivery{}, computerapp.ErrCredentialDeliveryDenied
	}
	stored, found, err := readCredentialDeliveryByRequest(ctx, tx, command.RequestID)
	if err != nil {
		return computerapp.CredentialDelivery{}, err
	}
	if found {
		var storedFingerprint []byte
		if err := tx.QueryRowContext(ctx, `SELECT payload_fingerprint FROM credential_deliveries WHERE request_id = ?`, command.RequestID).Scan(&storedFingerprint); err != nil {
			return computerapp.CredentialDelivery{}, fmt.Errorf("read credential delivery fingerprint: %w", err)
		}
		if !bytes.Equal(storedFingerprint, fingerprint[:]) {
			return computerapp.CredentialDelivery{}, computerapp.ErrCredentialDeliveryConflict
		}
		if err := tx.Commit(); err != nil {
			return computerapp.CredentialDelivery{}, fmt.Errorf("commit credential delivery replay: %w", err)
		}
		return stored, nil
	}
	if !command.ExpiresAt.After(command.Now) || command.ExpiresAt.Sub(command.Now) > credentialDeliveryTTL {
		return computerapp.CredentialDelivery{}, computerapp.ErrCredentialDeliveryInvalid
	}
	var capability bool
	capabilityQuery, capabilityArgs := credentialCapabilityQuery(command.ComputerID, command.CredentialKind, command.Sealed.KeyID)
	if err := tx.QueryRowContext(ctx, capabilityQuery, capabilityArgs...).Scan(&capability); err != nil {
		return computerapp.CredentialDelivery{}, fmt.Errorf("read credential delivery capability: %w", err)
	}
	if !capability {
		return computerapp.CredentialDelivery{}, computerapp.ErrCredentialDeliveryDenied
	}
	var agentExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agents WHERE id = ?)`, command.AgentID).Scan(&agentExists); err != nil {
		return computerapp.CredentialDelivery{}, fmt.Errorf("read credential delivery agent: %w", err)
	}
	if !agentExists {
		return computerapp.CredentialDelivery{}, computerapp.ErrCredentialDeliveryInvalid
	}
	delivery := computerapp.CredentialDelivery{
		ID: uuid.NewString(), RequestID: command.RequestID, ComputerID: command.ComputerID, AgentID: command.AgentID,
		CredentialKind: command.CredentialKind, Sealed: cloneSealedCredential(command.Sealed), State: computerapp.CredentialDeliveryQueued,
		ExpiresAt: command.ExpiresAt.UTC(), CreatedAt: command.Now.UTC(), UpdatedAt: command.Now.UTC(),
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO credential_deliveries(
			id, request_id, actor_human_id, payload_fingerprint, computer_id, agent_id, credential_kind,
			algorithm, key_id, ephemeral_public_key, nonce, ciphertext, state, expires_at, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'queued', ?, ?, ?)
	`, delivery.ID, delivery.RequestID, command.Actor.ID, fingerprint[:], delivery.ComputerID, delivery.AgentID,
		delivery.CredentialKind, delivery.Sealed.Algorithm, delivery.Sealed.KeyID, delivery.Sealed.EphemeralPublicKey,
		delivery.Sealed.Nonce, delivery.Sealed.Ciphertext, unixNano(delivery.ExpiresAt), unixNano(delivery.CreatedAt), unixNano(delivery.UpdatedAt)); err != nil {
		return computerapp.CredentialDelivery{}, fmt.Errorf("persist credential delivery: %w", err)
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: command.Actor.OrganizationID, Actor: command.Actor, Action: AuditAgentRuntimeConfigure,
		TargetKind: "agent", TargetID: command.AgentID, ContextKind: "computer", ContextID: command.ComputerID,
		RequestID: command.RequestID, Outcome: "committed", Now: command.Now,
	}); err != nil {
		return computerapp.CredentialDelivery{}, err
	}
	if err := tx.Commit(); err != nil {
		return computerapp.CredentialDelivery{}, fmt.Errorf("commit credential delivery: %w", err)
	}
	return delivery, nil
}

func (s *Store) ListCredentialDeliveries(ctx context.Context, query computerapp.ListCredentialDeliveriesQuery) ([]computerapp.CredentialDelivery, error) {
	if query.Actor.Kind != "human" || query.AgentID == "" || query.Now.IsZero() {
		return nil, computerapp.ErrCredentialDeliveryInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin list credential deliveries: %w", err)
	}
	defer tx.Rollback()
	if reason, err := requireGrant(ctx, tx, query.Actor, CapabilityAgentRuntimeConfigure, Scope{Kind: "agent", ID: query.AgentID}, query.Now, ""); err != nil {
		return nil, err
	} else if reason != "" {
		return nil, computerapp.ErrCredentialDeliveryDenied
	}
	if err := expireCredentialDeliveries(ctx, tx, query.Now, query.ComputerID); err != nil {
		return nil, err
	}
	statement := credentialDeliverySelect + ` WHERE agent_id = ?`
	arguments := []any{query.AgentID}
	if query.ComputerID != "" {
		statement += ` AND computer_id = ?`
		arguments = append(arguments, query.ComputerID)
	}
	statement += ` ORDER BY created_at DESC, id DESC`
	rows, err := tx.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list credential deliveries: %w", err)
	}
	deliveries, err := scanCredentialDeliveries(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit list credential deliveries: %w", err)
	}
	return deliveries, nil
}

func (s *Store) ClaimCredentialDelivery(ctx context.Context, command computerapp.ClaimCredentialDeliveryCommand) (computerapp.CredentialDelivery, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return computerapp.CredentialDelivery{}, fmt.Errorf("begin claim credential delivery: %w", err)
	}
	defer tx.Rollback()
	if err := authenticateComputer(ctx, tx, command.ComputerID, command.RegistrationKey); err != nil {
		return computerapp.CredentialDelivery{}, err
	}
	if err := expireCredentialDeliveries(ctx, tx, command.Now, command.ComputerID); err != nil {
		return computerapp.CredentialDelivery{}, err
	}
	delivery, err := scanCredentialDelivery(tx.QueryRowContext(ctx, credentialDeliverySelect+`
		WHERE computer_id = ? AND state IN ('claimed', 'queued')
		ORDER BY CASE state WHEN 'claimed' THEN 0 ELSE 1 END, created_at, id LIMIT 1
	`, command.ComputerID))
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return computerapp.CredentialDelivery{}, fmt.Errorf("commit empty credential delivery claim: %w", err)
		}
		return computerapp.CredentialDelivery{}, computerapp.ErrNotFound
	}
	if err != nil {
		return computerapp.CredentialDelivery{}, fmt.Errorf("read credential delivery claim: %w", err)
	}
	if delivery.State == computerapp.CredentialDeliveryQueued {
		if _, err := tx.ExecContext(ctx, `UPDATE credential_deliveries SET state = 'claimed', updated_at = max(updated_at, ?) WHERE id = ? AND state = 'queued'`, unixNano(command.Now), delivery.ID); err != nil {
			return computerapp.CredentialDelivery{}, fmt.Errorf("persist credential delivery claim: %w", err)
		}
		delivery.State = computerapp.CredentialDeliveryClaimed
		delivery.UpdatedAt = command.Now.UTC()
	}
	if err := tx.Commit(); err != nil {
		return computerapp.CredentialDelivery{}, fmt.Errorf("commit credential delivery claim: %w", err)
	}
	return delivery, nil
}

func (s *Store) CompleteCredentialDelivery(ctx context.Context, command computerapp.CompleteCredentialDeliveryCommand) (computerapp.CredentialDelivery, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return computerapp.CredentialDelivery{}, fmt.Errorf("begin complete credential delivery: %w", err)
	}
	defer tx.Rollback()
	if err := authenticateComputer(ctx, tx, command.ComputerID, command.RegistrationKey); err != nil {
		return computerapp.CredentialDelivery{}, err
	}
	if err := expireCredentialDeliveries(ctx, tx, command.Now, command.ComputerID); err != nil {
		return computerapp.CredentialDelivery{}, err
	}
	delivery, err := scanCredentialDelivery(tx.QueryRowContext(ctx, credentialDeliverySelect+` WHERE id = ?`, command.DeliveryID))
	if errors.Is(err, sql.ErrNoRows) {
		return computerapp.CredentialDelivery{}, computerapp.ErrNotFound
	}
	if err != nil {
		return computerapp.CredentialDelivery{}, fmt.Errorf("read credential delivery completion: %w", err)
	}
	if delivery.ComputerID != command.ComputerID {
		return computerapp.CredentialDelivery{}, computerapp.ErrCredentialDeliveryDenied
	}
	succeeded := command.BindingHandle != "" && command.ErrorCode == ""
	failed := command.BindingHandle == "" && command.ErrorCode != ""
	if !succeeded && !failed {
		return computerapp.CredentialDelivery{}, computerapp.ErrCredentialDeliveryInvalid
	}
	wantState := computerapp.CredentialDeliveryFailed
	if succeeded {
		wantState = computerapp.CredentialDeliverySucceeded
	}
	if delivery.State == wantState && delivery.BindingHandle == command.BindingHandle && delivery.ErrorCode == command.ErrorCode {
		if err := tx.Commit(); err != nil {
			return computerapp.CredentialDelivery{}, fmt.Errorf("commit credential delivery completion replay: %w", err)
		}
		return delivery, nil
	}
	if delivery.State != computerapp.CredentialDeliveryClaimed {
		return computerapp.CredentialDelivery{}, computerapp.ErrCredentialDeliveryConflict
	}
	if succeeded {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO credential_bindings(handle, delivery_id, computer_id, agent_id, credential_kind, key_id, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?)
		`, command.BindingHandle, delivery.ID, delivery.ComputerID, delivery.AgentID, delivery.CredentialKind, delivery.Sealed.KeyID, unixNano(command.Now)); err != nil {
			return computerapp.CredentialDelivery{}, computerapp.ErrCredentialDeliveryConflict
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE credential_deliveries SET state = ?, binding_handle = ?, error_code = ?, updated_at = max(updated_at, ?)
		WHERE id = ? AND state = 'claimed'
	`, wantState, command.BindingHandle, command.ErrorCode, unixNano(command.Now), delivery.ID); err != nil {
		return computerapp.CredentialDelivery{}, fmt.Errorf("persist credential delivery completion: %w", err)
	}
	delivery.State, delivery.BindingHandle, delivery.ErrorCode, delivery.UpdatedAt = wantState, command.BindingHandle, command.ErrorCode, command.Now.UTC()
	if err := tx.Commit(); err != nil {
		return computerapp.CredentialDelivery{}, fmt.Errorf("commit credential delivery completion: %w", err)
	}
	return delivery, nil
}

func validCredentialDeliveryCommand(command computerapp.EnqueueCredentialDeliveryCommand) bool {
	return command.RequestID != "" && command.Actor.Kind == "human" && command.ComputerID != "" && command.AgentID != "" &&
		validCredentialKind(command.CredentialKind) && command.Sealed.Algorithm == "x25519_xchacha20_poly1305" &&
		command.Sealed.KeyID != "" && len(command.Sealed.EphemeralPublicKey) == 32 && len(command.Sealed.Nonce) == 24 &&
		len(command.Sealed.Ciphertext) >= 17 && len(command.Sealed.Ciphertext) <= 65552 && !command.Now.IsZero()
}

func validCredentialKind(kind string) bool {
	return kind == "openai" || kind == "anthropic" || kind == "codex_adapter" || kind == "claude_adapter"
}

func credentialCapabilityQuery(computerID, kind, keyID string) (string, []any) {
	engine := "builtin"
	protocolCondition := ""
	switch kind {
	case "openai":
		protocolCondition = " AND engines.provider_openai_responses = 1"
	case "anthropic":
		protocolCondition = " AND engines.provider_anthropic_messages = 1"
	case "codex_adapter":
		engine = "codex_adapter"
	case "claude_adapter":
		engine = "claude_adapter"
	}
	return `
		SELECT EXISTS(
			SELECT 1 FROM computers
			JOIN computer_capability_inventories AS inventory
			  ON inventory.computer_id = computers.id AND inventory.revision = computers.current_inventory_revision
			JOIN computer_engine_capabilities AS engines
			  ON engines.computer_id = computers.id AND engines.inventory_revision = inventory.revision
			WHERE computers.id = ? AND inventory.credential_healthy = 1 AND inventory.credential_key_id = ?
			  AND engines.engine = ? AND engines.healthy = 1` + protocolCondition + `
		)
	`, []any{computerID, keyID, engine}
}

func credentialDeliveryFingerprint(command computerapp.EnqueueCredentialDeliveryCommand) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(struct {
		ActorID          string `json:"actor_id"`
		ComputerID       string `json:"computer_id"`
		AgentID          string `json:"agent_id"`
		CredentialKind   string `json:"credential_kind"`
		Algorithm        string `json:"algorithm"`
		KeyID            string `json:"key_id"`
		Ephemeral        []byte `json:"ephemeral_public_key"`
		Nonce            []byte `json:"nonce"`
		CiphertextDigest []byte `json:"ciphertext_digest"`
		ExpiresAt        int64  `json:"expires_at"`
	}{
		command.Actor.ID, command.ComputerID, command.AgentID, command.CredentialKind, command.Sealed.Algorithm,
		command.Sealed.KeyID, command.Sealed.EphemeralPublicKey, command.Sealed.Nonce,
		sha256Bytes(command.Sealed.Ciphertext), command.ExpiresAt.UTC().Unix(),
	})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode credential delivery fingerprint: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func sha256Bytes(value []byte) []byte {
	digest := sha256.Sum256(value)
	return digest[:]
}

const credentialDeliverySelect = `
	SELECT id, request_id, computer_id, agent_id, credential_kind, algorithm, key_id,
	       ephemeral_public_key, nonce, ciphertext, state, binding_handle, error_code,
	       expires_at, created_at, updated_at
	FROM credential_deliveries`

func readCredentialDeliveryByRequest(ctx context.Context, tx *sql.Tx, requestID string) (computerapp.CredentialDelivery, bool, error) {
	delivery, err := scanCredentialDelivery(tx.QueryRowContext(ctx, credentialDeliverySelect+` WHERE request_id = ?`, requestID))
	if errors.Is(err, sql.ErrNoRows) {
		return computerapp.CredentialDelivery{}, false, nil
	}
	if err != nil {
		return computerapp.CredentialDelivery{}, false, fmt.Errorf("read credential delivery request: %w", err)
	}
	return delivery, true, nil
}

func scanCredentialDelivery(row scanner) (computerapp.CredentialDelivery, error) {
	var delivery computerapp.CredentialDelivery
	var expiresAt, createdAt, updatedAt int64
	err := row.Scan(
		&delivery.ID, &delivery.RequestID, &delivery.ComputerID, &delivery.AgentID, &delivery.CredentialKind,
		&delivery.Sealed.Algorithm, &delivery.Sealed.KeyID, &delivery.Sealed.EphemeralPublicKey,
		&delivery.Sealed.Nonce, &delivery.Sealed.Ciphertext, &delivery.State, &delivery.BindingHandle,
		&delivery.ErrorCode, &expiresAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return computerapp.CredentialDelivery{}, err
	}
	delivery.ExpiresAt = timeFromUnixNano(expiresAt)
	delivery.CreatedAt = timeFromUnixNano(createdAt)
	delivery.UpdatedAt = timeFromUnixNano(updatedAt)
	return delivery, nil
}

func scanCredentialDeliveries(rows *sql.Rows) ([]computerapp.CredentialDelivery, error) {
	var deliveries []computerapp.CredentialDelivery
	for rows.Next() {
		delivery, err := scanCredentialDelivery(rows)
		if err != nil {
			return nil, fmt.Errorf("scan credential delivery: %w", err)
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate credential deliveries: %w", err)
	}
	return deliveries, nil
}

func expireCredentialDeliveries(ctx context.Context, tx *sql.Tx, now time.Time, computerID string) error {
	statement := `UPDATE credential_deliveries SET state = 'expired', error_code = 'expired', updated_at = max(updated_at, ?) WHERE state IN ('queued', 'claimed') AND expires_at <= ?`
	arguments := []any{unixNano(now), unixNano(now)}
	if computerID != "" {
		statement += ` AND computer_id = ?`
		arguments = append(arguments, computerID)
	}
	if _, err := tx.ExecContext(ctx, statement, arguments...); err != nil {
		return fmt.Errorf("expire credential deliveries: %w", err)
	}
	return nil
}

func cloneSealedCredential(value computerapp.SealedCredential) computerapp.SealedCredential {
	value.EphemeralPublicKey = append([]byte(nil), value.EphemeralPublicKey...)
	value.Nonce = append([]byte(nil), value.Nonce...)
	value.Ciphertext = append([]byte(nil), value.Ciphertext...)
	return value
}
