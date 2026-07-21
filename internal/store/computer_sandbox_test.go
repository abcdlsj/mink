package store

import (
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
	keyHash := registrationKeyHash("migration-computer-key")
	if _, err := database.Exec(`
		INSERT INTO computers(id, registration_key_hash, name, os, arch, created_at, last_seen_at)
		VALUES(?, ?, 'migration-host', 'linux', 'amd64', ?, ?)
	`, computerID, keyHash[:], unixNano(createdAt), unixNano(createdAt)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO computer_pairings(
			id, request_id, human_id, token_hash, payload_fingerprint, created_at, expires_at,
			consumed_at, consume_request_id, consume_fingerprint, computer_id
		) VALUES(?, ?, ?, zeroblob(32), zeroblob(32), ?, ?, ?, ?, zeroblob(32), ?)
	`, pairingID, uuid.NewString(), bootstrap.Human.ID, unixNano(createdAt), unixNano(createdAt.Add(time.Minute)),
		unixNano(createdAt), requestID, computerID); err != nil {
		t.Fatal(err)
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
			updated, err := database.HeartbeatComputer(context.Background(), computer.ID, params.RegistrationKey, capability, params.Now.Add(time.Duration(index)*time.Millisecond))
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
	current, err := database.HeartbeatComputer(context.Background(), first.ID, params.RegistrationKey, UnknownSandboxCapability(), now.Add(time.Second))
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
	if _, err := database.HeartbeatComputer(context.Background(), computer.ID, "invalid-sandbox-key", invalid, now.Add(time.Hour)); err != ErrSandboxCapabilityInvalid {
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
