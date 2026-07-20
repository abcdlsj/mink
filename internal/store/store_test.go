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
	"github.com/pressly/goose/v3"
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

func TestOpenUpgradesVersionOneDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := configure(database); err != nil {
		database.Close()
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := goose.UpTo(database, "migrations", 1); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.ListComputers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenUpgradesVersionThreeDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := configure(database); err != nil {
		database.Close()
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := goose.UpTo(database, "migrations", 3); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.ListAgentPlacements(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestComputerLastSeenNeverMovesBackward(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	future := time.Date(2030, 1, 1, 0, 0, 0, 123, time.UTC)
	params := RegisterComputerParams{
		RegistrationKey: "monotonic-computer-key",
		Name:            "Future host",
		OS:              "linux",
		Arch:            "amd64",
		Now:             future,
	}
	computer, err := store.RegisterComputer(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}

	earlier := future.Add(-time.Hour)
	params.Now = earlier
	registered, err := store.RegisterComputer(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if !registered.LastSeenAt.Equal(future) {
		t.Fatalf("earlier registration moved last_seen_at to %s", registered.LastSeenAt)
	}
	heartbeat, err := store.HeartbeatComputer(context.Background(), computer.ID, params.RegistrationKey, earlier)
	if err != nil {
		t.Fatal(err)
	}
	if !heartbeat.LastSeenAt.Equal(future) {
		t.Fatalf("earlier heartbeat moved last_seen_at to %s", heartbeat.LastSeenAt)
	}

	later := future.Add(time.Hour)
	heartbeat, err = store.HeartbeatComputer(context.Background(), computer.ID, params.RegistrationKey, later)
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

	params := CreateAgentParams{
		RequestID:   uuid.NewString(),
		Name:        "receipt-agent",
		Description: "original description",
		Driver:      "native",
		Now:         time.Now(),
	}
	agent, err := store.CreateAgent(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := agentPayloadFingerprint(params)
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

	if _, err := store.db.Exec("UPDATE agents SET description = ?, updated_at = ? WHERE id = ?", "current description", unixNano(params.Now.Add(time.Hour)), agent.ID); err != nil {
		t.Fatal(err)
	}
	replayed, err := store.CreateAgent(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Description != "current description" {
		t.Fatalf("idempotent replay returned %q", replayed.Description)
	}

	params.Description = "current description"
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
