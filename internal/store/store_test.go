package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

func TestServerIDPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	first := openServerID(t, path)
	second := openServerID(t, path)
	if first != second {
		t.Fatalf("server id changed from %q to %q", first, second)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("database mode = %o, want 600", got)
	}
}

func TestOpenRejectsExistingSchemaWithoutFinalMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("CREATE TABLE legacy_metadata (key TEXT PRIMARY KEY)"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(path); err == nil {
		store.Close()
		t.Fatal("Open succeeded for a schema without the final marker")
	}
}

func TestOpenRejectsPreviousFinalSchemaMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE system_metadata SET value = '3' WHERE key = 'schema_version'`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(path); err == nil {
		store.Close()
		t.Fatal("Open succeeded for the previous final schema marker")
	}
}

func TestComputerLastSeenNeverMovesBackward(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	future := time.Date(2030, 1, 1, 0, 0, 0, 123, time.UTC)
	bootstrap, err := store.EnsureAuthority(context.Background(), "last-seen-owner-credential-abcdefghijklmnopqrstuvwxyz", future)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: PrincipalHuman, ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	registrationKey := "monotonic-computer-key"
	inventory := testCapabilityInventory("test", true)
	computer := pairTestComputer(t, store, owner, registrationKey, inventory, future)

	earlier := future.Add(-time.Hour)
	heartbeat, err := store.HeartbeatComputer(context.Background(), HeartbeatComputerParams{
		ComputerID: computer.ID, RegistrationKey: registrationKey, CapabilityInventory: inventory, Now: earlier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !heartbeat.LastSeenAt.Equal(future) {
		t.Fatalf("earlier heartbeat moved last_seen_at to %s", heartbeat.LastSeenAt)
	}

	later := future.Add(time.Hour)
	heartbeat, err = store.HeartbeatComputer(context.Background(), HeartbeatComputerParams{
		ComputerID: computer.ID, RegistrationKey: registrationKey, CapabilityInventory: inventory, Now: later,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !heartbeat.LastSeenAt.Equal(later) {
		t.Fatalf("later heartbeat left last_seen_at at %s", heartbeat.LastSeenAt)
	}
}

func TestAgentRequestReceiptKeepsOriginalFingerprint(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bootstrap, err := store.EnsureAuthority(context.Background(), "agent-receipt-owner-credential-abcdefghijklmnopqrstuvwxyz", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}

	params := CreateAgentParams{
		RequestID: uuid.NewString(), Actor: owner, Handle: "receipt-agent", DisplayName: "Receipt Agent",
		Role: "tester", Mission: "Verify immutable request receipts", Instructions: "Preserve request identity.", Now: time.Now(),
	}
	agent, err := store.CreateAgent(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := agentCreateFingerprint(params)
	if err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := store.db.QueryRow("SELECT payload_fingerprint FROM agent_create_requests WHERE request_id = ?", params.RequestID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 32 || !bytes.Equal(stored, expected[:]) {
		t.Fatalf("stored fingerprint = %x, want %x", stored, expected)
	}

	updated, err := store.UpdateAgentProfile(context.Background(), UpdateAgentProfileParams{
		RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID, ExpectedRevision: 1,
		DisplayName: "Current Agent", Role: params.Role, Mission: params.Mission, Instructions: params.Instructions, Now: params.Now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.CreateAgent(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Profile.Revision != 1 || replayed.Profile.DisplayName != params.DisplayName {
		t.Fatalf("create replay = %+v, want immutable revision 1", replayed.Profile)
	}
	if updated.Profile.Revision != 2 || updated.Profile.DisplayName != "Current Agent" {
		t.Fatalf("updated profile = %+v", updated.Profile)
	}

	params.DisplayName = "Current Agent"
	_, err = store.CreateAgent(context.Background(), params)
	if !errors.Is(err, ErrAgentRequestConflict) {
		t.Fatalf("changed payload error = %v, want %v", err, ErrAgentRequestConflict)
	}
}

func openServerID(t *testing.T, path string) string {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.ServerID(context.Background())
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return id
}
