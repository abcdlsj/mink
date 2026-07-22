package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func TestComputerSandboxMigrationBackfillsUnknownCapabilityAndPairingReceipt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := configure(database); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(database, "migrations", 13); err != nil {
		t.Fatal(err)
	}
	ownerStore := &Store{db: database}
	bootstrap, err := ownerStore.EnsureAuthority(context.Background(), "sandbox-migration-owner-credential-abcdefghijklmnopqrstuvwxyz", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	computerID := uuid.NewString()
	pairingID := uuid.NewString()
	requestID := uuid.NewString()
	createdAt := time.Now().UTC()
	registrationKey := "migration-computer-key"
	name := "migration-host"
	operatingSystem := "linux"
	architecture := "amd64"
	keyHash := registrationKeyHash(registrationKey)
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	tokenHash := computerPairingTokenHash(token)
	legacyFingerprint, err := legacyComputerPairingPayloadFingerprint(keyHash[:], name, operatingSystem, architecture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO computers(id, registration_key_hash, name, os, arch, created_at, last_seen_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, computerID, keyHash[:], name, operatingSystem, architecture, unixNano(createdAt), unixNano(createdAt)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO computer_pairings(
			id, request_id, human_id, token_hash, payload_fingerprint, created_at, expires_at,
			consumed_at, consume_request_id, consume_fingerprint, computer_id
		) VALUES(?, ?, ?, ?, zeroblob(32), ?, ?, ?, ?, ?, ?)
	`, pairingID, uuid.NewString(), bootstrap.Human.ID, tokenHash[:], unixNano(createdAt), unixNano(createdAt.Add(time.Minute)),
		unixNano(createdAt), requestID, legacyFingerprint[:], computerID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE computers SET name = 'migrated-host', os = 'macos', arch = 'arm64' WHERE id = ?`, computerID); err != nil {
		t.Fatal(err)
	}
	replayParams := PairComputerParams{
		RequestID: requestID, PairingToken: token, RegistrationKey: registrationKey,
		Name: name, OS: operatingSystem, Arch: architecture, Now: createdAt.Add(time.Second),
	}
	if _, err := ownerStore.PairComputer(context.Background(), replayParams); err != ErrComputerPairingInvalid {
		t.Fatalf("unmigrated v13 replay error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	computer, err := upgraded.GetComputer(context.Background(), computerID)
	if err != nil {
		t.Fatal(err)
	}
	if computer.SandboxCapability != UnknownSandboxCapability() || computer.SandboxDeclarationRevision != 0 {
		t.Fatalf("migrated capability = %+v revision %d", computer.SandboxCapability, computer.SandboxDeclarationRevision)
	}
	receipt, revision, err := scanSandboxReceipt(upgraded.db.QueryRow(`
		SELECT sandbox_provider, sandbox_isolation, sandbox_workspace_access, sandbox_process_control,
			sandbox_filesystem_isolation, sandbox_network_isolation, sandbox_secret_materialization,
			sandbox_daemon_crash_cleanup, sandbox_declaration_revision
		FROM computer_pairing_sandbox_receipts WHERE pairing_id = ?
	`, pairingID))
	if err != nil || receipt != UnknownSandboxCapability() || revision != 0 {
		t.Fatalf("migrated pairing receipt = %+v revision %d, %v", receipt, revision, err)
	}
	replayed, err := upgraded.PairComputer(context.Background(), replayParams)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != computerID || replayed.SandboxCapability != UnknownSandboxCapability() || replayed.SandboxDeclarationRevision != 0 {
		t.Fatalf("cross-version replay = %+v", replayed)
	}
	changedRequests := []struct {
		name   string
		change func(*PairComputerParams)
	}{
		{"registration key", func(params *PairComputerParams) { params.RegistrationKey = "changed-migration-key" }},
		{"name", func(params *PairComputerParams) { params.Name = "migrated-host" }},
		{"operating system", func(params *PairComputerParams) { params.OS = "macos" }},
		{"architecture", func(params *PairComputerParams) { params.Arch = "arm64" }},
		{"capability", func(params *PairComputerParams) { params.SandboxCapability = TrustedLocalSandboxCapability() }},
	}
	for _, test := range changedRequests {
		t.Run(test.name, func(t *testing.T) {
			changed := replayParams
			test.change(&changed)
			if _, err := upgraded.PairComputer(context.Background(), changed); err != ErrComputerPairingConflict {
				t.Fatalf("changed cross-version replay error = %v", err)
			}
		})
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	computer, err = reopened.GetComputer(context.Background(), computerID)
	if err != nil || computer.SandboxCapability != UnknownSandboxCapability() || computer.SandboxDeclarationRevision != 0 {
		t.Fatalf("reopened migrated capability = %+v revision %d, %v", computer.SandboxCapability, computer.SandboxDeclarationRevision, err)
	}
}

func TestComputerSandboxMigrationDownAndUpRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := configure(database); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(database, "migrations", 14); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := (&Store{db: database}).EnsureAuthority(context.Background(), "sandbox-round-trip-owner-credential-abcdefghijklmnopqrstuvwxyz", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	computerID := uuid.NewString()
	registrationKey := "round-trip-registration-key"
	keyHash := registrationKeyHash(registrationKey)
	name := "round-trip-host"
	operatingSystem := "linux"
	architecture := "amd64"
	if _, err := database.Exec(`
		INSERT INTO computers(id, registration_key_hash, name, os, arch, created_at, last_seen_at)
		VALUES(?, ?, ?, ?, ?, 1, 1)
	`, computerID, keyHash[:], name, operatingSystem, architecture); err != nil {
		t.Fatal(err)
	}
	legacyFingerprint, err := legacyComputerPairingPayloadFingerprint(keyHash[:], name, operatingSystem, architecture)
	if err != nil {
		t.Fatal(err)
	}
	pairingToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	tokenHash := computerPairingTokenHash(pairingToken)
	pairingID := uuid.NewString()
	consumeRequestID := uuid.NewString()
	if _, err := database.Exec(`
		INSERT INTO computer_pairings(
			id, request_id, human_id, token_hash, payload_fingerprint, created_at, expires_at,
			consumed_at, consume_request_id, consume_fingerprint, computer_id
		) VALUES(?, ?, ?, ?, zeroblob(32), 1, 2, 1, ?, ?, ?)
	`, pairingID, uuid.NewString(), bootstrap.Human.ID, tokenHash[:], consumeRequestID, legacyFingerprint[:], computerID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO computer_pairing_sandbox_receipts(
			pairing_id, sandbox_provider, sandbox_isolation, sandbox_workspace_access,
			sandbox_process_control, sandbox_filesystem_isolation, sandbox_network_isolation,
			sandbox_secret_materialization, sandbox_daemon_crash_cleanup, sandbox_declaration_revision
		) VALUES(?, 'unknown', 'unknown', 'unknown', 'unknown', 'unknown', 'unknown', 'unknown', 'unknown', 0)
	`, pairingID); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Store{db: database}).RecoverComputer(context.Background(), RegisterComputerParams{
		RegistrationKey: registrationKey, Name: "round-trip-migrated-host", OS: "macos", Arch: "arm64",
		SandboxCapability: UnknownSandboxCapability(), Now: time.Unix(0, 3),
	}); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownTo(database, "migrations", 13); err != nil {
		t.Fatal(err)
	}
	var capabilityColumns int
	if err := database.QueryRow(`
		SELECT count(*) FROM pragma_table_info('computers') WHERE name LIKE 'sandbox_%'
	`).Scan(&capabilityColumns); err != nil {
		t.Fatal(err)
	}
	if capabilityColumns != 0 {
		t.Fatalf("sandbox columns after down = %d", capabilityColumns)
	}
	var fingerprint []byte
	if err := database.QueryRow(`SELECT consume_fingerprint FROM computer_pairings WHERE id = ?`, pairingID).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fingerprint, legacyFingerprint[:]) {
		t.Fatalf("consume fingerprint after down = %x", fingerprint)
	}
	replayParams := PairComputerParams{
		RequestID: consumeRequestID, PairingToken: pairingToken, RegistrationKey: registrationKey,
		Name: name, OS: operatingSystem, Arch: architecture, Now: time.Unix(0, 2),
	}
	replayedComputerID, err := replayLegacyConsumedPairing(database, replayParams)
	if err != nil || replayedComputerID != computerID {
		t.Fatalf("v13 replay after down = %q, %v", replayedComputerID, err)
	}
	changedRequests := []struct {
		name   string
		change func(*PairComputerParams)
	}{
		{"registration key", func(params *PairComputerParams) { params.RegistrationKey = "changed-round-trip-key" }},
		{"name", func(params *PairComputerParams) { params.Name = "round-trip-migrated-host" }},
		{"operating system", func(params *PairComputerParams) { params.OS = "macos" }},
		{"architecture", func(params *PairComputerParams) { params.Arch = "arm64" }},
	}
	for _, test := range changedRequests {
		t.Run("down "+test.name, func(t *testing.T) {
			changed := replayParams
			test.change(&changed)
			if _, err := replayLegacyConsumedPairing(database, changed); err != ErrComputerPairingConflict {
				t.Fatalf("changed v13 replay error = %v", err)
			}
		})
	}
	var receiptTables int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'computer_pairing_sandbox_receipts'`).Scan(&receiptTables); err != nil {
		t.Fatal(err)
	}
	if receiptTables != 0 {
		t.Fatalf("sandbox receipt table after down = %d", receiptTables)
	}
	if err := goose.UpTo(database, "migrations", 14); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`
		SELECT count(*) FROM pragma_table_info('computers') WHERE name LIKE 'sandbox_%'
	`).Scan(&capabilityColumns); err != nil {
		t.Fatal(err)
	}
	if capabilityColumns != 9 {
		t.Fatalf("sandbox columns after re-up = %d", capabilityColumns)
	}
	if err := database.QueryRow(`SELECT consume_fingerprint FROM computer_pairings WHERE id = ?`, pairingID).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fingerprint, legacyFingerprint[:]) {
		t.Fatalf("consume fingerprint after re-up = %x", fingerprint)
	}
	var receipts int
	if err := database.QueryRow(`SELECT count(*) FROM computer_pairing_sandbox_receipts WHERE pairing_id = ?`, pairingID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("sandbox receipts after re-up = %d", receipts)
	}
	replayed, err := (&Store{db: database}).PairComputer(context.Background(), replayParams)
	if err != nil || replayed.ID != computerID || replayed.SandboxCapability != UnknownSandboxCapability() || replayed.SandboxDeclarationRevision != 0 {
		t.Fatalf("v14 replay after re-up = %+v, %v", replayed, err)
	}
}

func replayLegacyConsumedPairing(database *sql.DB, params PairComputerParams) (string, error) {
	tokenHash := computerPairingTokenHash(params.PairingToken)
	keyHash := registrationKeyHash(params.RegistrationKey)
	fingerprint, err := legacyComputerPairingPayloadFingerprint(keyHash[:], params.Name, params.OS, params.Arch)
	if err != nil {
		return "", err
	}
	var consumeRequestID, computerID string
	var consumeFingerprint []byte
	if err := database.QueryRow(`
		SELECT consume_request_id, consume_fingerprint, computer_id
		FROM computer_pairings WHERE token_hash = ? AND consumed_at IS NOT NULL
	`, tokenHash[:]).Scan(&consumeRequestID, &consumeFingerprint, &computerID); err != nil {
		return "", err
	}
	if consumeRequestID != params.RequestID || !bytes.Equal(consumeFingerprint, fingerprint[:]) {
		return "", ErrComputerPairingConflict
	}
	return computerID, nil
}

func TestComputerSandboxMigrationFailsClosedAndRollsBackCorruptConsumedPairing(t *testing.T) {
	for _, corruption := range []string{"missing computer", "damaged registration key hash"} {
		t.Run(corruption, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.db")
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if err := configure(database); err != nil {
				t.Fatal(err)
			}
			goose.SetBaseFS(migrations)
			if err := goose.SetDialect("sqlite3"); err != nil {
				t.Fatal(err)
			}
			if err := goose.UpTo(database, "migrations", 13); err != nil {
				t.Fatal(err)
			}
			bootstrap, err := (&Store{db: database}).EnsureAuthority(context.Background(), "sandbox-corruption-owner-credential-abcdefghijklmnopqrstuvwxyz", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			computerID := uuid.NewString()
			keyHash := registrationKeyHash("corrupt-migration-key")
			if corruption != "missing computer" {
				if _, err := database.Exec(`
					INSERT INTO computers(id, registration_key_hash, name, os, arch, created_at, last_seen_at)
					VALUES(?, ?, 'corrupt-host', 'linux', 'amd64', 1, 1)
				`, computerID, keyHash[:]); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := database.Exec("PRAGMA foreign_keys = OFF"); err != nil {
				t.Fatal(err)
			}
			pairingID := uuid.NewString()
			requestID := uuid.NewString()
			legacyFingerprint := make([]byte, 32)
			if _, err := database.Exec(`
				INSERT INTO computer_pairings(
					id, request_id, human_id, token_hash, payload_fingerprint, created_at, expires_at,
					consumed_at, consume_request_id, consume_fingerprint, computer_id
				) VALUES(?, ?, ?, zeroblob(32), zeroblob(32), 1, 2, 1, ?, ?, ?)
			`, pairingID, uuid.NewString(), bootstrap.Human.ID, requestID, legacyFingerprint, computerID); err != nil {
				t.Fatal(err)
			}
			if corruption == "damaged registration key hash" {
				if _, err := database.Exec("PRAGMA ignore_check_constraints = ON"); err != nil {
					t.Fatal(err)
				}
				if _, err := database.Exec("UPDATE computers SET registration_key_hash = x'01' WHERE id = ?", computerID); err != nil {
					t.Fatal(err)
				}
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}

			if upgraded, err := Open(path); err == nil {
				upgraded.Close()
				t.Fatal("corrupt consumed pairing migration succeeded")
			}
			rolledBack, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer rolledBack.Close()
			var capabilityColumns, receiptTables int
			if err := rolledBack.QueryRow(`SELECT count(*) FROM pragma_table_info('computers') WHERE name LIKE 'sandbox_%'`).Scan(&capabilityColumns); err != nil {
				t.Fatal(err)
			}
			if err := rolledBack.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'computer_pairing_sandbox_receipts'`).Scan(&receiptTables); err != nil {
				t.Fatal(err)
			}
			var fingerprint []byte
			if err := rolledBack.QueryRow(`SELECT consume_fingerprint FROM computer_pairings WHERE id = ?`, pairingID).Scan(&fingerprint); err != nil {
				t.Fatal(err)
			}
			if capabilityColumns != 0 || receiptTables != 0 || !bytes.Equal(fingerprint, legacyFingerprint) {
				t.Fatalf("failed migration left partial facts: columns=%d receipts=%d fingerprint=%x", capabilityColumns, receiptTables, fingerprint)
			}
		})
	}
}

func TestComputerSandboxDeclarationsUseCommitOrderedMonotonicRevision(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	params := RegisterComputerParams{
		RegistrationKey: "sandbox-revision-key", Name: "Revision host", OS: "linux", Arch: "amd64",
		SandboxCapability: UnknownSandboxCapability(), Now: time.Now(),
	}
	computer, err := database.RegisterComputer(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if computer.SandboxDeclarationRevision != 1 {
		t.Fatalf("initial revision = %d", computer.SandboxDeclarationRevision)
	}

	const declarations = 20
	type result struct {
		capability SandboxCapability
		revision   uint64
	}
	results := make(chan result, declarations)
	var group sync.WaitGroup
	for index := range declarations {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			capability := UnknownSandboxCapability()
			if index%2 == 0 {
				capability = TrustedLocalSandboxCapability()
			}
			updated, err := database.HeartbeatComputer(context.Background(), HeartbeatComputerParams{
				ComputerID: computer.ID, RegistrationKey: params.RegistrationKey,
				SandboxCapability: capability, Now: params.Now.Add(time.Duration(index) * time.Millisecond),
			})
			if err != nil {
				t.Errorf("heartbeat %d: %v", index, err)
				return
			}
			results <- result{capability: capability, revision: updated.SandboxDeclarationRevision}
		}(index)
	}
	group.Wait()
	close(results)
	seen := make(map[uint64]struct{}, declarations)
	var final result
	for declaration := range results {
		seen[declaration.revision] = struct{}{}
		if declaration.revision > final.revision {
			final = declaration
		}
	}
	if len(seen) != declarations || final.revision != declarations+1 {
		t.Fatalf("revisions = %v, final = %d", seen, final.revision)
	}
	current, err := database.GetComputer(context.Background(), computer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.SandboxDeclarationRevision != final.revision || current.SandboxCapability != final.capability {
		t.Fatalf("current declaration = %+v revision %d, final commit = %+v", current.SandboxCapability, current.SandboxDeclarationRevision, final)
	}
}

func TestComputerPairingReplaysOriginalSandboxDeclarationAfterCurrentFactAdvances(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	bootstrap, err := database.EnsureAuthority(context.Background(), "sandbox-pairing-owner-credential-abcdefghijklmnopqrstuvwxyz", now)
	if err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	_, err = database.CreateComputerPairing(context.Background(), CreateComputerPairingParams{
		RequestID: uuid.NewString(), Actor: Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID},
		Token: token, ExpiresAt: now.Add(time.Minute), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	params := PairComputerParams{
		RequestID: uuid.NewString(), PairingToken: token, RegistrationKey: "sandbox-pairing-computer-key",
		Name: "Pairing host", OS: "macos", Arch: "arm64", SandboxCapability: TrustedLocalSandboxCapability(), Now: now,
	}
	first, err := database.PairComputer(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if first.SandboxDeclarationRevision != 1 || first.SandboxCapability != TrustedLocalSandboxCapability() {
		t.Fatalf("first declaration = %+v revision %d", first.SandboxCapability, first.SandboxDeclarationRevision)
	}
	current, err := database.HeartbeatComputer(context.Background(), HeartbeatComputerParams{
		ComputerID: first.ID, RegistrationKey: params.RegistrationKey,
		SandboxCapability: UnknownSandboxCapability(), Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if current.SandboxDeclarationRevision != 2 || current.SandboxCapability != UnknownSandboxCapability() {
		t.Fatalf("current declaration = %+v revision %d", current.SandboxCapability, current.SandboxDeclarationRevision)
	}
	replayed, err := database.PairComputer(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.SandboxDeclarationRevision != first.SandboxDeclarationRevision || replayed.SandboxCapability != first.SandboxCapability {
		t.Fatalf("pair replay = %+v revision %d, first = %+v revision %d", replayed.SandboxCapability, replayed.SandboxDeclarationRevision, first.SandboxCapability, first.SandboxDeclarationRevision)
	}
	params.SandboxCapability = UnknownSandboxCapability()
	if _, err := database.PairComputer(context.Background(), params); err != ErrComputerPairingConflict {
		t.Fatalf("changed declaration error = %v", err)
	}
}

func TestInvalidSandboxDeclarationDoesNotMutateComputer(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	computer, err := database.RegisterComputer(context.Background(), RegisterComputerParams{
		RegistrationKey: "invalid-sandbox-key", Name: "Invalid host", OS: "linux", Arch: "amd64",
		SandboxCapability: TrustedLocalSandboxCapability(), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	invalid := TrustedLocalSandboxCapability()
	invalid.NetworkIsolation = "unknown"
	if _, err := database.HeartbeatComputer(context.Background(), HeartbeatComputerParams{
		ComputerID: computer.ID, RegistrationKey: "invalid-sandbox-key",
		SandboxCapability: invalid, Now: now.Add(time.Hour),
	}); err != ErrSandboxCapabilityInvalid {
		t.Fatalf("invalid heartbeat error = %v", err)
	}
	current, err := database.GetComputer(context.Background(), computer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.SandboxDeclarationRevision != computer.SandboxDeclarationRevision || !current.LastSeenAt.Equal(computer.LastSeenAt) || current.SandboxCapability != computer.SandboxCapability {
		t.Fatalf("invalid declaration mutated computer: before %+v, after %+v", computer, current)
	}
}
