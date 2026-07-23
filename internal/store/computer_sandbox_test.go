package store

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"sync"
	"testing"
	"time"

	computerdomain "github.com/abcdlsj/sumi/internal/computer/domain"
	"github.com/google/uuid"
)

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
				capability = computerdomain.TrustedLocalSandboxCapability()
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
		Name: "Pairing host", OS: "macos", Arch: "arm64", SandboxCapability: computerdomain.TrustedLocalSandboxCapability(), Now: now,
	}
	first, err := database.PairComputer(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if first.SandboxDeclarationRevision != 1 || first.SandboxCapability != computerdomain.TrustedLocalSandboxCapability() {
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
		SandboxCapability: computerdomain.TrustedLocalSandboxCapability(), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	invalid := computerdomain.TrustedLocalSandboxCapability()
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
