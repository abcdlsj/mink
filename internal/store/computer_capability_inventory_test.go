package store

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	computerdomain "github.com/abcdlsj/sumi/internal/computer/domain"
	"github.com/google/uuid"
)

func TestComputerCapabilityInventoriesUseCommitOrderedMonotonicRevision(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	bootstrap, err := database.EnsureAuthority(context.Background(), "inventory-revision-owner-credential-abcdefghijklmnopqrstuvwxyz", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: PrincipalHuman, ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	registrationKey := "inventory-revision-key"
	computer := pairTestComputer(t, database, owner, registrationKey, testCapabilityInventory("initial", true), now)
	if computer.CapabilityInventory.Revision != 1 {
		t.Fatalf("initial revision = %d", computer.CapabilityInventory.Revision)
	}

	const declarations = 20
	type result struct {
		inventory CapabilityInventory
		revision  uint64
	}
	results := make(chan result, declarations)
	var group sync.WaitGroup
	for index := range declarations {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			declaration := testCapabilityInventory("revision-"+string(rune('a'+index)), index%2 == 0)
			updated, err := database.HeartbeatComputer(context.Background(), HeartbeatComputerParams{
				ComputerID: computer.ID, RegistrationKey: registrationKey,
				CapabilityInventory: declaration, Now: now.Add(time.Duration(index) * time.Millisecond),
			})
			if err != nil {
				t.Errorf("heartbeat %d: %v", index, err)
				return
			}
			results <- result{inventory: declaration, revision: updated.CapabilityInventory.Revision}
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
	final.inventory.Revision = final.revision
	final.inventory.DeclaredAt = current.CapabilityInventory.DeclaredAt
	if !reflect.DeepEqual(current.CapabilityInventory, final.inventory) {
		t.Fatalf("current inventory = %+v, final commit = %+v", current.CapabilityInventory, final)
	}
}

func TestComputerPairingReplaysOriginalInventoryAfterCurrentFactAdvances(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	bootstrap, err := database.EnsureAuthority(context.Background(), "inventory-pairing-owner-credential-abcdefghijklmnopqrstuvwxyz", now)
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
		RequestID: uuid.NewString(), PairingToken: token, RegistrationKey: "inventory-pairing-computer-key",
		Name: "Pairing host", OS: "macos", Arch: "arm64", CapabilityInventory: testCapabilityInventory("paired", true), Now: now,
	}
	first, err := database.PairComputer(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if first.CapabilityInventory.Revision != 1 {
		t.Fatalf("first inventory = %+v", first.CapabilityInventory)
	}
	current, err := database.HeartbeatComputer(context.Background(), HeartbeatComputerParams{
		ComputerID: first.ID, RegistrationKey: params.RegistrationKey,
		CapabilityInventory: testCapabilityInventory("current", false), Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if current.CapabilityInventory.Revision != 2 || current.CapabilityInventory.Engines[0].Version != "current" {
		t.Fatalf("current inventory = %+v", current.CapabilityInventory)
	}
	replayed, err := database.PairComputer(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.CapabilityInventory.Revision != 1 || replayed.CapabilityInventory.Engines[0].Version != "paired" {
		t.Fatalf("pair replay = %+v", replayed.CapabilityInventory)
	}
	params.CapabilityInventory = testCapabilityInventory("changed", true)
	if _, err := database.PairComputer(context.Background(), params); err != ErrComputerPairingConflict {
		t.Fatalf("changed inventory error = %v", err)
	}
}

func TestInvalidCapabilityInventoryDoesNotMutateComputer(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	bootstrap, err := database.EnsureAuthority(context.Background(), "invalid-inventory-owner-credential-abcdefghijklmnopqrstuvwxyz", now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: PrincipalHuman, ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	computer := pairTestComputer(t, database, owner, "invalid-inventory-key", testCapabilityInventory("valid", true), now)
	invalid := testCapabilityInventory("invalid", true)
	invalid.Engines = append(invalid.Engines, invalid.Engines[0])
	if _, err := database.HeartbeatComputer(context.Background(), HeartbeatComputerParams{
		ComputerID: computer.ID, RegistrationKey: "invalid-inventory-key",
		CapabilityInventory: invalid, Now: now.Add(time.Hour),
	}); err != ErrCapabilityInventoryInvalid {
		t.Fatalf("invalid heartbeat error = %v", err)
	}
	current, err := database.GetComputer(context.Background(), computer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.CapabilityInventory.Revision != 1 || !current.LastSeenAt.Equal(computer.LastSeenAt) {
		t.Fatalf("invalid inventory mutated computer: before %+v, after %+v", computer, current)
	}
}

func testCapabilityInventory(version string, healthy bool) CapabilityInventory {
	return computerdomain.TrustedLocalCapabilityInventory(computerdomain.EngineCapability{
		Kind: computerdomain.EngineBuiltin, Version: version, ProtocolVersion: 1,
		SupportsToolCalls: true, SupportsCancel: true, OpenAIResponses: true, Healthy: healthy,
	})
}
