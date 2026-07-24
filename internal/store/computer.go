package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	computerapp "github.com/abcdlsj/sumi/internal/computer/application"
	computerdomain "github.com/abcdlsj/sumi/internal/computer/domain"
	"github.com/google/uuid"
)

const (
	computerPairingHashDomain = "sumi-computer-pairing-v1\x00"
	computerPairingTTL        = 10 * time.Minute
	computerIdentityColumns   = `id, name, os, arch, created_at, last_seen_at, current_inventory_revision`
)

type Computer = computerapp.Computer

type CapabilityInventory = computerdomain.CapabilityInventory

type HeartbeatComputerParams = computerapp.HeartbeatCommand

type ComputerPairing = computerapp.Pairing

type CreateComputerPairingParams = computerapp.PreparePairingCommand

type PairComputerParams = computerapp.PairCommand

func (s *Store) CreateComputerPairing(ctx context.Context, params CreateComputerPairingParams) (ComputerPairing, error) {
	if params.Actor.Kind != "human" || params.Actor.ID == "" || params.Actor.OrganizationID == "" ||
		!validComputerPairingToken(params.Token) || params.Now.IsZero() || params.ExpiresAt.IsZero() {
		return ComputerPairing{}, ErrComputerPairingInvalid
	}
	tokenHash := computerPairingTokenHash(params.Token)
	fingerprint, err := computerPairingFingerprint(struct {
		ActorID   string `json:"actor_id"`
		TokenHash []byte `json:"token_hash"`
		ExpiresAt int64  `json:"expires_at"`
	}{params.Actor.ID, tokenHash[:], unixNano(params.ExpiresAt)})
	if err != nil {
		return ComputerPairing{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ComputerPairing{}, fmt.Errorf("begin computer pairing creation: %w", err)
	}
	defer tx.Rollback()
	reason, err := requireGrant(ctx, tx, params.Actor, CapabilityComputerPair, Scope{Kind: "organization", ID: params.Actor.OrganizationID}, params.Now, "")
	if err != nil {
		return ComputerPairing{}, err
	}
	if reason != "" {
		return ComputerPairing{}, commitDenied(ctx, tx, params.Actor, AuditComputerPairPrepare, "computer_pairing", "", params.RequestID, reason, params.Now)
	}
	var pairing ComputerPairing
	var humanID string
	var storedFingerprint []byte
	var expiresAt int64
	err = tx.QueryRowContext(ctx, `
		SELECT id, human_id, payload_fingerprint, expires_at
		FROM computer_pairings WHERE request_id = ?
	`, params.RequestID).Scan(&pairing.ID, &humanID, &storedFingerprint, &expiresAt)
	if err == nil {
		if humanID != params.Actor.ID || !bytes.Equal(storedFingerprint, fingerprint[:]) {
			return ComputerPairing{}, ErrComputerPairingConflict
		}
		pairing.ExpiresAt = timeFromUnixNano(expiresAt)
		if err := tx.Commit(); err != nil {
			return ComputerPairing{}, fmt.Errorf("commit computer pairing replay: %w", err)
		}
		return pairing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ComputerPairing{}, fmt.Errorf("read computer pairing request: %w", err)
	}
	if !params.ExpiresAt.After(params.Now) || params.ExpiresAt.Sub(params.Now) > computerPairingTTL {
		return ComputerPairing{}, ErrComputerPairingInvalid
	}
	pairing = ComputerPairing{ID: uuid.NewString(), ExpiresAt: params.ExpiresAt.UTC()}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO computer_pairings(id, request_id, human_id, token_hash, payload_fingerprint, created_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, pairing.ID, params.RequestID, params.Actor.ID, tokenHash[:], fingerprint[:], unixNano(params.Now), unixNano(params.ExpiresAt)); err != nil {
		if isUniqueConstraint(err, "computer_pairings.token_hash") {
			return ComputerPairing{}, ErrComputerPairingConflict
		}
		return ComputerPairing{}, fmt.Errorf("persist computer pairing: %w", err)
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: params.Actor.OrganizationID, Actor: params.Actor,
		Action: AuditComputerPairPrepare, TargetKind: "computer_pairing", TargetID: pairing.ID,
		RequestID: params.RequestID, Outcome: "committed", Now: params.Now,
	}); err != nil {
		return ComputerPairing{}, err
	}
	if err := tx.Commit(); err != nil {
		return ComputerPairing{}, fmt.Errorf("commit computer pairing creation: %w", err)
	}
	return pairing, nil
}

func (s *Store) PairComputer(ctx context.Context, params PairComputerParams) (Computer, error) {
	inventory, valid := normalizeCapabilityInventory(params.CapabilityInventory)
	if !valid || !validComputerPairingToken(params.PairingToken) || params.RequestID == "" || params.RegistrationKey == "" || params.Now.IsZero() {
		return Computer{}, ErrComputerPairingInvalid
	}
	params.CapabilityInventory = inventory
	tokenHash := computerPairingTokenHash(params.PairingToken)
	keyHash := registrationKeyHash(params.RegistrationKey)
	fingerprint, err := computerPairingPayloadFingerprint(keyHash[:], params.Name, params.OS, params.Arch, inventory)
	if err != nil {
		return Computer{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Computer{}, fmt.Errorf("begin computer pairing: %w", err)
	}
	defer tx.Rollback()
	var pairingID, humanID string
	var expiresAt int64
	var consumedAt sql.NullInt64
	var consumeRequestID, computerID sql.NullString
	var inventoryRevision sql.NullInt64
	var consumeFingerprint []byte
	err = tx.QueryRowContext(ctx, `
		SELECT id, human_id, expires_at, consumed_at, consume_request_id, consume_fingerprint,
		       computer_id, computer_inventory_revision
		FROM computer_pairings WHERE token_hash = ?
	`, tokenHash[:]).Scan(&pairingID, &humanID, &expiresAt, &consumedAt, &consumeRequestID, &consumeFingerprint, &computerID, &inventoryRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return Computer{}, ErrComputerPairingInvalid
	}
	if err != nil {
		return Computer{}, fmt.Errorf("read computer pairing: %w", err)
	}
	if consumedAt.Valid {
		if !consumeRequestID.Valid || consumeRequestID.String != params.RequestID || !bytes.Equal(consumeFingerprint, fingerprint[:]) || !computerID.Valid || !inventoryRevision.Valid {
			return Computer{}, ErrComputerPairingConflict
		}
		computer, err := readComputerAtInventory(ctx, tx, computerID.String, uint64(inventoryRevision.Int64))
		if err != nil {
			return Computer{}, ErrComputerPairingInvalid
		}
		if err := tx.Commit(); err != nil {
			return Computer{}, fmt.Errorf("commit paired computer replay: %w", err)
		}
		return computer, nil
	}
	if !timeFromUnixNano(expiresAt).After(params.Now) {
		return Computer{}, ErrComputerPairingInvalid
	}
	var requestUsed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM computer_pairings WHERE consume_request_id = ?)`, params.RequestID).Scan(&requestUsed); err != nil {
		return Computer{}, fmt.Errorf("check computer pairing consume request: %w", err)
	}
	if requestUsed {
		return Computer{}, ErrComputerPairingConflict
	}
	computer := Computer{
		ID: uuid.NewString(), Name: params.Name, OS: params.OS, Arch: params.Arch,
		CreatedAt: params.Now.UTC(), LastSeenAt: params.Now.UTC(),
	}
	computer.CapabilityInventory = inventory
	computer.CapabilityInventory.Revision = 1
	computer.CapabilityInventory.DeclaredAt = params.Now.UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO computers(id, registration_key_hash, name, os, arch, created_at, last_seen_at, current_inventory_revision)
		VALUES(?, ?, ?, ?, ?, ?, ?, 1)
	`, computer.ID, keyHash[:], computer.Name, computer.OS, computer.Arch, unixNano(computer.CreatedAt), unixNano(computer.LastSeenAt)); err != nil {
		if isUniqueConstraint(err, "computers.registration_key_hash") {
			return Computer{}, ErrComputerPairingConflict
		}
		return Computer{}, fmt.Errorf("persist paired computer: %w", err)
	}
	if err := insertCapabilityInventory(ctx, tx, computer.ID, computer.CapabilityInventory); err != nil {
		return Computer{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE computer_pairings
		SET consumed_at = ?, consume_request_id = ?, consume_fingerprint = ?, computer_id = ?, computer_inventory_revision = 1
		WHERE id = ? AND consumed_at IS NULL AND expires_at > ?
	`, unixNano(params.Now), params.RequestID, fingerprint[:], computer.ID, pairingID, unixNano(params.Now))
	if err != nil {
		return Computer{}, fmt.Errorf("consume computer pairing: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return Computer{}, ErrComputerPairingInvalid
	}
	var organizationID string
	if err := tx.QueryRowContext(ctx, `SELECT organization_id FROM humans WHERE id = ? AND status = 'active'`, humanID).Scan(&organizationID); err != nil {
		return Computer{}, ErrComputerPairingInvalid
	}
	actor := Principal{Kind: "human", ID: humanID, OrganizationID: organizationID}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: organizationID, Actor: actor, Action: AuditComputerPair,
		TargetKind: "computer", TargetID: computer.ID, RequestID: params.RequestID,
		Outcome: "committed", Now: params.Now,
	}); err != nil {
		return Computer{}, err
	}
	if err := tx.Commit(); err != nil {
		return Computer{}, fmt.Errorf("commit computer pairing: %w", err)
	}
	return computer, nil
}

func (s *Store) HeartbeatComputer(ctx context.Context, params HeartbeatComputerParams) (Computer, error) {
	inventory, valid := normalizeCapabilityInventory(params.CapabilityInventory)
	if !valid {
		return Computer{}, ErrCapabilityInventoryInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Computer{}, fmt.Errorf("begin computer heartbeat: %w", err)
	}
	defer tx.Rollback()
	keyHash := registrationKeyHash(params.RegistrationKey)
	var currentRevision int64
	if err := tx.QueryRowContext(ctx, `
		SELECT current_inventory_revision FROM computers WHERE id = ? AND registration_key_hash = ?
	`, params.ComputerID, keyHash[:]).Scan(&currentRevision); errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if checkErr := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM computers WHERE id = ?)", params.ComputerID).Scan(&exists); checkErr != nil {
			return Computer{}, fmt.Errorf("check computer identity: %w", checkErr)
		}
		if !exists {
			return Computer{}, ErrComputerNotFound
		}
		return Computer{}, ErrRegistrationKeyMismatch
	} else if err != nil {
		return Computer{}, fmt.Errorf("authenticate computer heartbeat: %w", err)
	}
	nextRevision := uint64(currentRevision + 1)
	inventory.Revision = nextRevision
	inventory.DeclaredAt = params.Now.UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE computers SET last_seen_at = max(last_seen_at, ?), current_inventory_revision = ?
		WHERE id = ? AND current_inventory_revision = ?
	`, unixNano(params.Now), nextRevision, params.ComputerID, currentRevision); err != nil {
		return Computer{}, fmt.Errorf("advance heartbeat inventory revision: %w", err)
	}
	if err := insertCapabilityInventory(ctx, tx, params.ComputerID, inventory); err != nil {
		return Computer{}, err
	}
	computer, err := readComputerAtInventory(ctx, tx, params.ComputerID, nextRevision)
	if err != nil {
		return Computer{}, fmt.Errorf("read heartbeat computer: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Computer{}, fmt.Errorf("commit computer heartbeat: %w", err)
	}
	return computer, nil
}

func (s *Store) GetComputer(ctx context.Context, id string) (Computer, error) {
	computer, err := readCurrentComputer(ctx, s.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Computer{}, ErrComputerNotFound
	}
	if err != nil {
		return Computer{}, fmt.Errorf("get computer: %w", err)
	}
	return computer, nil
}

func (s *Store) ListComputers(ctx context.Context) ([]Computer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+computerIdentityColumns+` FROM computers ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list computers: %w", err)
	}
	var computers []Computer
	var revisions []uint64
	for rows.Next() {
		computer, revision, err := scanComputerIdentity(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan computer: %w", err)
		}
		computers = append(computers, computer)
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate computers: %w", err)
	}
	rows.Close()
	for index := range computers {
		inventory, err := readCapabilityInventory(ctx, s.db, computers[index].ID, revisions[index])
		if err != nil {
			return nil, fmt.Errorf("read computer capability inventory: %w", err)
		}
		computers[index].CapabilityInventory = inventory
	}
	return computers, nil
}

func readCurrentComputer(ctx context.Context, queryer capabilityInventoryQueryer, id string) (Computer, error) {
	computer, revision, err := scanComputerIdentity(queryer.QueryRowContext(ctx, `SELECT `+computerIdentityColumns+` FROM computers WHERE id = ?`, id))
	if err != nil {
		return Computer{}, err
	}
	inventory, err := readCapabilityInventory(ctx, queryer, id, revision)
	if err != nil {
		return Computer{}, err
	}
	computer.CapabilityInventory = inventory
	return computer, nil
}

func readComputerAtInventory(ctx context.Context, queryer capabilityInventoryQueryer, id string, revision uint64) (Computer, error) {
	computer, _, err := scanComputerIdentity(queryer.QueryRowContext(ctx, `SELECT `+computerIdentityColumns+` FROM computers WHERE id = ?`, id))
	if err != nil {
		return Computer{}, err
	}
	inventory, err := readCapabilityInventory(ctx, queryer, id, revision)
	if err != nil {
		return Computer{}, err
	}
	computer.CapabilityInventory = inventory
	return computer, nil
}

func scanComputerIdentity(row scanner) (Computer, uint64, error) {
	var computer Computer
	var createdAt, lastSeenAt, revision int64
	if err := row.Scan(&computer.ID, &computer.Name, &computer.OS, &computer.Arch, &createdAt, &lastSeenAt, &revision); err != nil {
		return Computer{}, 0, err
	}
	if revision < 1 {
		return Computer{}, 0, ErrCapabilityInventoryInvalid
	}
	computer.CreatedAt = timeFromUnixNano(createdAt)
	computer.LastSeenAt = timeFromUnixNano(lastSeenAt)
	return computer, uint64(revision), nil
}

type scanner interface {
	Scan(...any) error
}

func insertCapabilityInventory(ctx context.Context, tx *sql.Tx, computerID string, inventory CapabilityInventory) error {
	sandbox := inventory.Sandboxes[0]
	credential := inventory.CredentialDelivery
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO computer_capability_inventories(
			computer_id, revision, sandbox_provider, sandbox_isolation, sandbox_workspace_access,
			sandbox_process_control, sandbox_filesystem_isolation, sandbox_network_isolation,
			sandbox_secret_materialization, sandbox_daemon_crash_cleanup, credential_healthy,
			credential_algorithm, credential_store, credential_key_id, credential_public_key, declared_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, computerID, inventory.Revision, sandbox.Provider, sandbox.Isolation, sandbox.WorkspaceAccess,
		sandbox.ProcessControl, sandbox.FilesystemIsolation, sandbox.NetworkIsolation,
		sandbox.SecretMaterialization, sandbox.DaemonCrashCleanup, credential.Healthy,
		credential.Algorithm, credential.Store, credential.KeyID, credential.PublicKey, unixNano(inventory.DeclaredAt)); err != nil {
		return fmt.Errorf("persist computer capability inventory: %w", err)
	}
	for _, engine := range inventory.Engines {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO computer_engine_capabilities(
				computer_id, inventory_revision, engine, version, protocol_version,
				supports_tool_calls, supports_cancel, provider_openai_responses,
				provider_anthropic_messages, healthy
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, computerID, inventory.Revision, engine.Kind, engine.Version, engine.ProtocolVersion,
			engine.SupportsToolCalls, engine.SupportsCancel, engine.OpenAIResponses,
			engine.AnthropicMessages, engine.Healthy); err != nil {
			return fmt.Errorf("persist computer engine capability: %w", err)
		}
	}
	return nil
}

type capabilityInventoryQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readCapabilityInventory(ctx context.Context, queryer capabilityInventoryQueryer, computerID string, revision uint64) (CapabilityInventory, error) {
	var inventory CapabilityInventory
	var sandbox computerdomain.SandboxCapability
	var declaredAt int64
	err := queryer.QueryRowContext(ctx, `
		SELECT revision, sandbox_provider, sandbox_isolation, sandbox_workspace_access,
		       sandbox_process_control, sandbox_filesystem_isolation, sandbox_network_isolation,
		       sandbox_secret_materialization, sandbox_daemon_crash_cleanup, credential_healthy,
		       credential_algorithm, credential_store, credential_key_id, credential_public_key, declared_at
		FROM computer_capability_inventories WHERE computer_id = ? AND revision = ?
	`, computerID, revision).Scan(
		&inventory.Revision, &sandbox.Provider, &sandbox.Isolation, &sandbox.WorkspaceAccess,
		&sandbox.ProcessControl, &sandbox.FilesystemIsolation, &sandbox.NetworkIsolation,
		&sandbox.SecretMaterialization, &sandbox.DaemonCrashCleanup, &inventory.CredentialDelivery.Healthy,
		&inventory.CredentialDelivery.Algorithm, &inventory.CredentialDelivery.Store,
		&inventory.CredentialDelivery.KeyID, &inventory.CredentialDelivery.PublicKey, &declaredAt,
	)
	if err != nil {
		return CapabilityInventory{}, err
	}
	inventory.Sandboxes = []computerdomain.SandboxCapability{sandbox}
	inventory.DeclaredAt = timeFromUnixNano(declaredAt)
	rows, err := queryer.QueryContext(ctx, `
		SELECT engine, version, protocol_version, supports_tool_calls, supports_cancel,
		       provider_openai_responses, provider_anthropic_messages, healthy
		FROM computer_engine_capabilities
		WHERE computer_id = ? AND inventory_revision = ? ORDER BY engine
	`, computerID, revision)
	if err != nil {
		return CapabilityInventory{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var engine computerdomain.EngineCapability
		if err := rows.Scan(&engine.Kind, &engine.Version, &engine.ProtocolVersion, &engine.SupportsToolCalls,
			&engine.SupportsCancel, &engine.OpenAIResponses, &engine.AnthropicMessages, &engine.Healthy); err != nil {
			return CapabilityInventory{}, err
		}
		inventory.Engines = append(inventory.Engines, engine)
	}
	if err := rows.Err(); err != nil {
		return CapabilityInventory{}, err
	}
	declaration := inventory
	declaration.Revision = 0
	declaration.DeclaredAt = time.Time{}
	if !declaration.ValidDeclaration() {
		return CapabilityInventory{}, ErrCapabilityInventoryInvalid
	}
	return inventory, nil
}

func normalizeCapabilityInventory(inventory CapabilityInventory) (CapabilityInventory, bool) {
	if !inventory.ValidDeclaration() {
		return CapabilityInventory{}, false
	}
	inventory.Engines = append([]computerdomain.EngineCapability(nil), inventory.Engines...)
	inventory.Sandboxes = append([]computerdomain.SandboxCapability(nil), inventory.Sandboxes...)
	sort.Slice(inventory.Engines, func(i, j int) bool { return inventory.Engines[i].Kind < inventory.Engines[j].Kind })
	return inventory, true
}

func computerPairingPayloadFingerprint(registrationKeyHash []byte, name string, operatingSystem computerdomain.OperatingSystem, architecture computerdomain.Architecture, inventory CapabilityInventory) ([sha256.Size]byte, error) {
	return computerPairingFingerprint(struct {
		RegistrationKeyHash []byte                         `json:"registration_key_hash"`
		Name                string                         `json:"name"`
		OS                  computerdomain.OperatingSystem `json:"os"`
		Arch                computerdomain.Architecture    `json:"arch"`
		Inventory           CapabilityInventory            `json:"capability_inventory"`
	}{registrationKeyHash, name, operatingSystem, architecture, inventory})
}

func computerPairingFingerprint(value any) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode computer pairing: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func computerPairingTokenHash(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(computerPairingHashDomain + token))
}

func validComputerPairingToken(token string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == token
}

func registrationKeyHash(key string) [sha256.Size]byte {
	return sha256.Sum256([]byte(key))
}
