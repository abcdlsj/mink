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
	"time"

	"github.com/google/uuid"
)

const (
	computerPairingHashDomain = "sumi-computer-pairing-v1\x00"
	computerPairingTTL        = 10 * time.Minute
	computerColumns           = `id, name, os, arch, created_at, last_seen_at,
		sandbox_provider, sandbox_isolation, sandbox_workspace_access, sandbox_process_control,
		sandbox_filesystem_isolation, sandbox_network_isolation, sandbox_secret_materialization,
		sandbox_daemon_crash_cleanup, sandbox_declaration_revision`
)

type Computer struct {
	ID                         string
	Name                       string
	OS                         string
	Arch                       string
	CreatedAt                  time.Time
	LastSeenAt                 time.Time
	SandboxCapability          SandboxCapability
	SandboxDeclarationRevision uint64
}

type SandboxCapability struct {
	Provider              string
	Isolation             string
	WorkspaceAccess       string
	ProcessControl        string
	FilesystemIsolation   string
	NetworkIsolation      string
	SecretMaterialization string
	DaemonCrashCleanup    string
}

type RegisterComputerParams struct {
	RegistrationKey   string
	Name              string
	OS                string
	Arch              string
	SandboxCapability SandboxCapability
	Now               time.Time
}

type ComputerPairing struct {
	ID        string
	ExpiresAt time.Time
}

type CreateComputerPairingParams struct {
	RequestID string
	Actor     Principal
	Token     string
	ExpiresAt time.Time
	Now       time.Time
}

type PairComputerParams struct {
	RequestID         string
	PairingToken      string
	RegistrationKey   string
	Name              string
	OS                string
	Arch              string
	SandboxCapability SandboxCapability
	Now               time.Time
}

func UnknownSandboxCapability() SandboxCapability {
	return SandboxCapability{
		Provider: "unknown", Isolation: "unknown", WorkspaceAccess: "unknown", ProcessControl: "unknown",
		FilesystemIsolation: "unknown", NetworkIsolation: "unknown", SecretMaterialization: "unknown",
		DaemonCrashCleanup: "unknown",
	}
}

func TrustedLocalSandboxCapability() SandboxCapability {
	return SandboxCapability{
		Provider: "trusted_local", Isolation: "trusted_local", WorkspaceAccess: "direct_read_write",
		ProcessControl: "context_process_group", FilesystemIsolation: "none", NetworkIsolation: "none",
		SecretMaterialization: "ephemeral_environment", DaemonCrashCleanup: "none",
	}
}

func ValidSandboxCapability(capability SandboxCapability) bool {
	return capability == UnknownSandboxCapability() || capability == TrustedLocalSandboxCapability()
}

func normalizeSandboxCapability(capability SandboxCapability) (SandboxCapability, bool) {
	if capability == (SandboxCapability{}) {
		return UnknownSandboxCapability(), true
	}
	return capability, ValidSandboxCapability(capability)
}

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
		INSERT INTO computer_pairings(
			id, request_id, human_id, token_hash, payload_fingerprint, created_at, expires_at
		) VALUES(?, ?, ?, ?, ?, ?, ?)
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
	capability, capabilityValid := normalizeSandboxCapability(params.SandboxCapability)
	if !validComputerPairingToken(params.PairingToken) || params.RequestID == "" || params.RegistrationKey == "" ||
		params.Now.IsZero() || !capabilityValid {
		return Computer{}, ErrComputerPairingInvalid
	}
	params.SandboxCapability = capability
	tokenHash := computerPairingTokenHash(params.PairingToken)
	keyHash := registrationKeyHash(params.RegistrationKey)
	fingerprint, err := computerPairingPayloadFingerprint(keyHash[:], params.Name, params.OS, params.Arch, params.SandboxCapability)
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
	var consumeFingerprint []byte
	err = tx.QueryRowContext(ctx, `
		SELECT id, human_id, expires_at, consumed_at, consume_request_id, consume_fingerprint, computer_id
		FROM computer_pairings WHERE token_hash = ?
	`, tokenHash[:]).Scan(&pairingID, &humanID, &expiresAt, &consumedAt, &consumeRequestID, &consumeFingerprint, &computerID)
	if errors.Is(err, sql.ErrNoRows) {
		var other bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM computer_pairings WHERE consume_request_id = ?)`, params.RequestID).Scan(&other); err != nil {
			return Computer{}, fmt.Errorf("check computer pairing request: %w", err)
		}
		if other {
			return Computer{}, ErrComputerPairingConflict
		}
		return Computer{}, ErrComputerPairingInvalid
	}
	if err != nil {
		return Computer{}, fmt.Errorf("read computer pairing: %w", err)
	}
	if consumedAt.Valid {
		if !consumeRequestID.Valid || consumeRequestID.String != params.RequestID || !bytes.Equal(consumeFingerprint, fingerprint[:]) || !computerID.Valid {
			return Computer{}, ErrComputerPairingConflict
		}
		computer, err := scanComputer(tx.QueryRowContext(ctx, `SELECT `+computerColumns+` FROM computers WHERE id = ?`, computerID.String))
		if err != nil {
			return Computer{}, ErrComputerPairingInvalid
		}
		receipt, revision, err := scanSandboxReceipt(tx.QueryRowContext(ctx, `
			SELECT sandbox_provider, sandbox_isolation, sandbox_workspace_access, sandbox_process_control,
				sandbox_filesystem_isolation, sandbox_network_isolation, sandbox_secret_materialization,
				sandbox_daemon_crash_cleanup, sandbox_declaration_revision
			FROM computer_pairing_sandbox_receipts WHERE pairing_id = ?
		`, pairingID))
		if err != nil {
			return Computer{}, ErrComputerPairingInvalid
		}
		computer.SandboxCapability = receipt
		computer.SandboxDeclarationRevision = revision
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
		CreatedAt: params.Now.UTC(), LastSeenAt: params.Now.UTC(), SandboxCapability: params.SandboxCapability,
		SandboxDeclarationRevision: 1,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO computers(
			id, registration_key_hash, name, os, arch, created_at, last_seen_at,
			sandbox_provider, sandbox_isolation, sandbox_workspace_access, sandbox_process_control,
			sandbox_filesystem_isolation, sandbox_network_isolation, sandbox_secret_materialization,
			sandbox_daemon_crash_cleanup, sandbox_declaration_revision
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, computer.ID, keyHash[:], computer.Name, computer.OS, computer.Arch, unixNano(computer.CreatedAt), unixNano(computer.LastSeenAt),
		computer.SandboxCapability.Provider, computer.SandboxCapability.Isolation, computer.SandboxCapability.WorkspaceAccess,
		computer.SandboxCapability.ProcessControl, computer.SandboxCapability.FilesystemIsolation,
		computer.SandboxCapability.NetworkIsolation, computer.SandboxCapability.SecretMaterialization,
		computer.SandboxCapability.DaemonCrashCleanup, computer.SandboxDeclarationRevision); err != nil {
		if isUniqueConstraint(err, "computers.registration_key_hash") {
			return Computer{}, ErrComputerPairingConflict
		}
		return Computer{}, fmt.Errorf("persist paired computer: %w", err)
	}
	if err := insertSandboxReceipt(ctx, tx, pairingID, computer); err != nil {
		return Computer{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE computer_pairings
		SET consumed_at = ?, consume_request_id = ?, consume_fingerprint = ?, computer_id = ?
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

func computerPairingPayloadFingerprint(registrationKeyHash []byte, name, operatingSystem, architecture string, capability SandboxCapability) ([sha256.Size]byte, error) {
	return computerPairingFingerprint(struct {
		RegistrationKeyHash []byte            `json:"registration_key_hash"`
		Name                string            `json:"name"`
		OS                  string            `json:"os"`
		Arch                string            `json:"arch"`
		SandboxCapability   SandboxCapability `json:"sandbox_capability"`
	}{registrationKeyHash, name, operatingSystem, architecture, capability})
}

func legacyComputerPairingPayloadFingerprint(registrationKeyHash []byte, name, operatingSystem, architecture string) ([sha256.Size]byte, error) {
	return computerPairingFingerprint(struct {
		RegistrationKeyHash []byte `json:"registration_key_hash"`
		Name                string `json:"name"`
		OS                  string `json:"os"`
		Arch                string `json:"arch"`
	}{registrationKeyHash, name, operatingSystem, architecture})
}

// Sandbox declaration revisions order Store commits, not client clocks or request start times.
func (s *Store) RecoverComputer(ctx context.Context, params RegisterComputerParams) (Computer, error) {
	capability, valid := normalizeSandboxCapability(params.SandboxCapability)
	if !valid {
		return Computer{}, ErrSandboxCapabilityInvalid
	}
	params.SandboxCapability = capability
	keyHash := registrationKeyHash(params.RegistrationKey)
	row := s.db.QueryRowContext(ctx, `
		UPDATE computers
		SET name = ?, os = ?, arch = ?, last_seen_at = max(last_seen_at, ?),
			sandbox_provider = ?, sandbox_isolation = ?, sandbox_workspace_access = ?, sandbox_process_control = ?,
			sandbox_filesystem_isolation = ?, sandbox_network_isolation = ?, sandbox_secret_materialization = ?,
			sandbox_daemon_crash_cleanup = ?, sandbox_declaration_revision = sandbox_declaration_revision + 1
		WHERE registration_key_hash = ?
		RETURNING `+computerColumns+`
	`, params.Name, params.OS, params.Arch, unixNano(params.Now),
		params.SandboxCapability.Provider, params.SandboxCapability.Isolation, params.SandboxCapability.WorkspaceAccess,
		params.SandboxCapability.ProcessControl, params.SandboxCapability.FilesystemIsolation,
		params.SandboxCapability.NetworkIsolation, params.SandboxCapability.SecretMaterialization,
		params.SandboxCapability.DaemonCrashCleanup, keyHash[:])
	computer, err := scanComputer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Computer{}, ErrComputerNotFound
	}
	if err != nil {
		return Computer{}, fmt.Errorf("recover computer: %w", err)
	}
	return computer, nil
}

func (s *Store) RegisterComputer(ctx context.Context, params RegisterComputerParams) (Computer, error) {
	capability, valid := normalizeSandboxCapability(params.SandboxCapability)
	if !valid {
		return Computer{}, ErrSandboxCapabilityInvalid
	}
	params.SandboxCapability = capability
	stamp := unixNano(params.Now)
	keyHash := registrationKeyHash(params.RegistrationKey)
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO computers(
			id, registration_key_hash, name, os, arch, created_at, last_seen_at,
			sandbox_provider, sandbox_isolation, sandbox_workspace_access, sandbox_process_control,
			sandbox_filesystem_isolation, sandbox_network_isolation, sandbox_secret_materialization,
			sandbox_daemon_crash_cleanup, sandbox_declaration_revision
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT(registration_key_hash) DO UPDATE SET
			name = excluded.name,
			os = excluded.os,
			arch = excluded.arch,
			last_seen_at = max(computers.last_seen_at, excluded.last_seen_at),
			sandbox_provider = excluded.sandbox_provider,
			sandbox_isolation = excluded.sandbox_isolation,
			sandbox_workspace_access = excluded.sandbox_workspace_access,
			sandbox_process_control = excluded.sandbox_process_control,
			sandbox_filesystem_isolation = excluded.sandbox_filesystem_isolation,
			sandbox_network_isolation = excluded.sandbox_network_isolation,
			sandbox_secret_materialization = excluded.sandbox_secret_materialization,
			sandbox_daemon_crash_cleanup = excluded.sandbox_daemon_crash_cleanup,
			sandbox_declaration_revision = computers.sandbox_declaration_revision + 1
		RETURNING `+computerColumns+`
	`, uuid.NewString(), keyHash[:], params.Name, params.OS, params.Arch, stamp, stamp,
		params.SandboxCapability.Provider, params.SandboxCapability.Isolation, params.SandboxCapability.WorkspaceAccess,
		params.SandboxCapability.ProcessControl, params.SandboxCapability.FilesystemIsolation,
		params.SandboxCapability.NetworkIsolation, params.SandboxCapability.SecretMaterialization,
		params.SandboxCapability.DaemonCrashCleanup)
	computer, err := scanComputer(row)
	if err != nil {
		return Computer{}, fmt.Errorf("register computer: %w", err)
	}
	return computer, nil
}

func (s *Store) HeartbeatComputer(ctx context.Context, id, registrationKey string, capability SandboxCapability, now time.Time) (Computer, error) {
	capability, valid := normalizeSandboxCapability(capability)
	if !valid {
		return Computer{}, ErrSandboxCapabilityInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Computer{}, fmt.Errorf("begin computer heartbeat: %w", err)
	}
	defer tx.Rollback()

	keyHash := registrationKeyHash(registrationKey)
	row := tx.QueryRowContext(ctx, `
		UPDATE computers
		SET last_seen_at = max(last_seen_at, ?),
			sandbox_provider = ?, sandbox_isolation = ?, sandbox_workspace_access = ?, sandbox_process_control = ?,
			sandbox_filesystem_isolation = ?, sandbox_network_isolation = ?, sandbox_secret_materialization = ?,
			sandbox_daemon_crash_cleanup = ?, sandbox_declaration_revision = sandbox_declaration_revision + 1
		WHERE id = ? AND registration_key_hash = ?
		RETURNING `+computerColumns+`
	`, unixNano(now), capability.Provider, capability.Isolation, capability.WorkspaceAccess, capability.ProcessControl,
		capability.FilesystemIsolation, capability.NetworkIsolation, capability.SecretMaterialization,
		capability.DaemonCrashCleanup, id, keyHash[:])
	computer, err := scanComputer(row)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return Computer{}, fmt.Errorf("commit computer heartbeat: %w", err)
		}
		return computer, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Computer{}, fmt.Errorf("heartbeat computer: %w", err)
	}

	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM computers WHERE id = ?)", id).Scan(&exists); err != nil {
		return Computer{}, fmt.Errorf("check computer identity: %w", err)
	}
	if !exists {
		return Computer{}, ErrComputerNotFound
	}
	return Computer{}, ErrRegistrationKeyMismatch
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

func (s *Store) GetComputer(ctx context.Context, id string) (Computer, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+computerColumns+`
		FROM computers
		WHERE id = ?
	`, id)
	computer, err := scanComputer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Computer{}, ErrComputerNotFound
	}
	if err != nil {
		return Computer{}, fmt.Errorf("get computer: %w", err)
	}
	return computer, nil
}

func (s *Store) ListComputers(ctx context.Context) ([]Computer, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+computerColumns+`
		FROM computers
		ORDER BY name, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list computers: %w", err)
	}
	defer rows.Close()

	var computers []Computer
	for rows.Next() {
		computer, err := scanComputer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan computer: %w", err)
		}
		computers = append(computers, computer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate computers: %w", err)
	}
	return computers, nil
}

type scanner interface {
	Scan(...any) error
}

func scanComputer(row scanner) (Computer, error) {
	var computer Computer
	var createdAt, lastSeenAt, revision int64
	if err := row.Scan(
		&computer.ID, &computer.Name, &computer.OS, &computer.Arch, &createdAt, &lastSeenAt,
		&computer.SandboxCapability.Provider, &computer.SandboxCapability.Isolation,
		&computer.SandboxCapability.WorkspaceAccess, &computer.SandboxCapability.ProcessControl,
		&computer.SandboxCapability.FilesystemIsolation, &computer.SandboxCapability.NetworkIsolation,
		&computer.SandboxCapability.SecretMaterialization, &computer.SandboxCapability.DaemonCrashCleanup,
		&revision,
	); err != nil {
		return Computer{}, err
	}
	if revision < 0 || !ValidSandboxCapability(computer.SandboxCapability) {
		return Computer{}, ErrSandboxCapabilityInvalid
	}
	computer.CreatedAt = timeFromUnixNano(createdAt)
	computer.LastSeenAt = timeFromUnixNano(lastSeenAt)
	computer.SandboxDeclarationRevision = uint64(revision)
	return computer, nil
}

func insertSandboxReceipt(ctx context.Context, tx *sql.Tx, pairingID string, computer Computer) error {
	capability := computer.SandboxCapability
	_, err := tx.ExecContext(ctx, `
		INSERT INTO computer_pairing_sandbox_receipts(
			pairing_id, sandbox_provider, sandbox_isolation, sandbox_workspace_access, sandbox_process_control,
			sandbox_filesystem_isolation, sandbox_network_isolation, sandbox_secret_materialization,
			sandbox_daemon_crash_cleanup, sandbox_declaration_revision
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, pairingID, capability.Provider, capability.Isolation, capability.WorkspaceAccess, capability.ProcessControl,
		capability.FilesystemIsolation, capability.NetworkIsolation, capability.SecretMaterialization,
		capability.DaemonCrashCleanup, computer.SandboxDeclarationRevision)
	if err != nil {
		return fmt.Errorf("persist computer pairing sandbox receipt: %w", err)
	}
	return nil
}

func scanSandboxReceipt(row scanner) (SandboxCapability, uint64, error) {
	var capability SandboxCapability
	var revision int64
	if err := row.Scan(
		&capability.Provider, &capability.Isolation, &capability.WorkspaceAccess, &capability.ProcessControl,
		&capability.FilesystemIsolation, &capability.NetworkIsolation, &capability.SecretMaterialization,
		&capability.DaemonCrashCleanup, &revision,
	); err != nil {
		return SandboxCapability{}, 0, err
	}
	if revision < 0 || !ValidSandboxCapability(capability) {
		return SandboxCapability{}, 0, ErrSandboxCapabilityInvalid
	}
	return capability, uint64(revision), nil
}
