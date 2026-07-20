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
	createdAt := time.Date(2026, 7, 20, 11, 30, 25, 0, time.UTC)
	lastSeenAt := createdAt.Add(123456789 * time.Nanosecond)
	computerID := uuid.NewString()
	agentID := uuid.NewString()
	requestID := uuid.NewString()
	fingerprint, err := agentPayloadFingerprint(CreateAgentParams{Name: "legacy-agent", Driver: "native"})
	if err != nil {
		t.Fatal(err)
	}
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
	replaceVersionThreeTablesWithLegacyTextSchema(t, database)
	if _, err := database.Exec(`
		INSERT INTO computers(id, registration_key_hash, name, os, arch, created_at, last_seen_at)
		VALUES(?, zeroblob(32), 'legacy-computer', 'linux', 'amd64', ?, ?)
	`, computerID, createdAt.Format(time.RFC3339), lastSeenAt.Format(time.RFC3339Nano)); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO agents(id, name, description, driver, created_at, updated_at)
		VALUES(?, 'legacy-agent', '', 'native', ?, ?)
	`, agentID, createdAt.Format(time.RFC3339Nano), lastSeenAt.Format(time.RFC3339Nano)); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO agent_create_requests(request_id, agent_id)
		VALUES(?, ?)
	`, requestID, agentID); err != nil {
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
	computers, err := store.ListComputers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(computers) != 1 || computers[0].ID != computerID || !computers[0].CreatedAt.Equal(createdAt) || !computers[0].LastSeenAt.Equal(lastSeenAt) {
		t.Fatalf("migrated computers = %+v", computers)
	}
	agents, err := store.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].ID != agentID || !agents[0].CreatedAt.Equal(createdAt) || !agents[0].UpdatedAt.Equal(lastSeenAt) {
		t.Fatalf("migrated agents = %+v", agents)
	}
	var storedComputerID string
	var keyHash []byte
	if err := store.db.QueryRow("SELECT id, registration_key_hash FROM computers").Scan(&storedComputerID, &keyHash); err != nil {
		t.Fatal(err)
	}
	if storedComputerID != computerID || !bytes.Equal(keyHash, make([]byte, 32)) {
		t.Fatalf("migrated computer identity = %q/%x", storedComputerID, keyHash)
	}
	var storedRequestID, storedAgentID string
	var storedFingerprint []byte
	if err := store.db.QueryRow("SELECT request_id, agent_id, payload_fingerprint FROM agent_create_requests").Scan(&storedRequestID, &storedAgentID, &storedFingerprint); err != nil {
		t.Fatal(err)
	}
	if storedRequestID != requestID || storedAgentID != agentID || !bytes.Equal(storedFingerprint, fingerprint[:]) {
		t.Fatalf("migrated agent receipt = %q/%q/%x", storedRequestID, storedAgentID, storedFingerprint)
	}
	for _, table := range []string{"computers", "agents"} {
		var createdType, updatedType string
		column := "updated_at"
		if table == "computers" {
			column = "last_seen_at"
		}
		if err := store.db.QueryRow("SELECT typeof(created_at), typeof("+column+") FROM "+table).Scan(&createdType, &updatedType); err != nil {
			t.Fatal(err)
		}
		if createdType != "integer" || updatedType != "integer" {
			t.Fatalf("%s timestamp types = %s/%s, want integer/integer", table, createdType, updatedType)
		}
	}
}

func TestOpenUpgradesIntegerVersionThreeDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	createdAt := time.Date(2026, 7, 20, 11, 30, 25, 123456789, time.UTC)
	computerID := uuid.NewString()
	agentID := uuid.NewString()
	requestID := uuid.NewString()
	fingerprint := bytes.Repeat([]byte{0x5a}, 32)
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
	if _, err := database.Exec(`
		INSERT INTO computers(id, registration_key_hash, name, os, arch, created_at, last_seen_at)
		VALUES(?, zeroblob(32), 'integer-computer', 'linux', 'amd64', ?, ?)
	`, computerID, createdAt.UnixNano(), createdAt.UnixNano()); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO agents(id, name, description, driver, created_at, updated_at)
		VALUES(?, 'integer-agent', '', 'native', ?, ?)
	`, agentID, createdAt.UnixNano(), createdAt.UnixNano()); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO agent_create_requests(request_id, agent_id, payload_fingerprint)
		VALUES(?, ?, ?)
	`, requestID, agentID, fingerprint); err != nil {
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
	computer, err := store.GetComputer(context.Background(), computerID)
	if err != nil {
		t.Fatal(err)
	}
	if !computer.CreatedAt.Equal(createdAt) || !computer.LastSeenAt.Equal(createdAt) {
		t.Fatalf("migrated integer computer = %+v", computer)
	}
	agent, err := store.GetAgent(context.Background(), agentID)
	if err != nil {
		t.Fatal(err)
	}
	if !agent.CreatedAt.Equal(createdAt) || !agent.UpdatedAt.Equal(createdAt) {
		t.Fatalf("migrated integer agent = %+v", agent)
	}
	var storedRequestID, storedAgentID string
	var storedFingerprint []byte
	if err := store.db.QueryRow("SELECT request_id, agent_id, payload_fingerprint FROM agent_create_requests").Scan(&storedRequestID, &storedAgentID, &storedFingerprint); err != nil {
		t.Fatal(err)
	}
	if storedRequestID != requestID || storedAgentID != agentID || !bytes.Equal(storedFingerprint, fingerprint) {
		t.Fatalf("migrated integer receipt = %q/%q/%x", storedRequestID, storedAgentID, storedFingerprint)
	}
}

func TestOpenRejectsInvalidVersionThreeTimestampWithoutMutation(t *testing.T) {
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
	replaceVersionThreeTablesWithLegacyTextSchema(t, database)
	computerID := uuid.NewString()
	if _, err := database.Exec(`
		INSERT INTO computers(id, registration_key_hash, name, os, arch, created_at, last_seen_at)
		VALUES(?, zeroblob(32), 'invalid-time-computer', 'linux', 'amd64', 'not-a-time', '2026-07-20T11:30:25Z')
	`, computerID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	if store, err := Open(path); err == nil {
		store.Close()
		t.Fatal("Open succeeded with an invalid v3 timestamp")
	}
	database, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var version int
	if err := database.QueryRow("SELECT max(version_id) FROM goose_db_version WHERE is_applied = 1").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("migration version = %d, want 3", version)
	}
	var createdAt, createdType string
	if err := database.QueryRow("SELECT created_at, typeof(created_at) FROM computers WHERE id = ?", computerID).Scan(&createdAt, &createdType); err != nil {
		t.Fatal(err)
	}
	if createdAt != "not-a-time" || createdType != "text" {
		t.Fatalf("rolled-back timestamp = %q/%q", createdAt, createdType)
	}
	var stagingTables int
	if err := database.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN ('computers_v4', 'agents_v4', 'agent_create_requests_v4', 'agent_placements')").Scan(&stagingTables); err != nil {
		t.Fatal(err)
	}
	if stagingTables != 0 {
		t.Fatalf("migration left %d staging tables", stagingTables)
	}
}

func replaceVersionThreeTablesWithLegacyTextSchema(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`
		DROP TABLE agent_create_requests;
		DROP TABLE agents;
		DROP TABLE computers;
		CREATE TABLE computers (
			id TEXT PRIMARY KEY CHECK (length(id) = 36),
			registration_key_hash BLOB NOT NULL UNIQUE CHECK (length(registration_key_hash) = 32),
			name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
			os TEXT NOT NULL CHECK (os IN ('macos', 'linux')),
			arch TEXT NOT NULL CHECK (arch IN ('arm64', 'amd64')),
			created_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL
		);
		CREATE TABLE agents (
			id TEXT PRIMARY KEY CHECK (length(id) = 36),
			name TEXT NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 32),
			description TEXT NOT NULL CHECK (length(description) <= 1000),
			driver TEXT NOT NULL CHECK (driver IN ('native', 'codex', 'claude')),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE agent_create_requests (
			request_id TEXT PRIMARY KEY CHECK (length(request_id) = 36),
			agent_id TEXT NOT NULL UNIQUE REFERENCES agents(id) ON DELETE RESTRICT
		);
	`); err != nil {
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
