package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAgentRuntimeCreateKeepsOneCurrentSession(t *testing.T) {
	fixture := openAgentRuntimeFixture(t)
	firstToken := runtimeTestToken(1)
	first := createRuntimeSession(t, fixture, firstToken, fixture.now)
	if first.AgentID != fixture.agentID || first.ComputerID != fixture.computer.ID || first.PlacementDesiredRevision != 1 {
		t.Fatalf("first session = %+v", first)
	}
	if _, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), firstToken, fixture.now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	secondToken := runtimeTestToken(2)
	createRuntimeSession(t, fixture, secondToken, fixture.now.Add(time.Second))
	assertRuntimeUnauthenticated(t, fixture.database, firstToken, fixture.now.Add(2*time.Second))
	if _, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), secondToken, fixture.now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertCurrentRuntimeCount(t, fixture.database, fixture.agentID, 1)

	tokens := make([]string, 12)
	errorsByIndex := make([]error, len(tokens))
	var wait sync.WaitGroup
	for index := range tokens {
		tokens[index] = runtimeTestToken(byte(index + 10))
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, errorsByIndex[index] = fixture.database.CreateAgentRuntimeSession(context.Background(), CreateAgentRuntimeSessionParams{
				ComputerID: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
				AgentID: fixture.agentID, PlacementDesiredRevision: 1, Token: tokens[index],
				Now: fixture.now.Add(3 * time.Second), ExpiresAt: fixture.now.Add(10*time.Minute + 3*time.Second),
			})
		}(index)
	}
	wait.Wait()
	for index, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("create %d: %v", index, err)
		}
	}
	valid := 0
	for _, token := range tokens {
		if _, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), token, fixture.now.Add(4*time.Second)); err == nil {
			valid++
		} else if !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
			t.Fatal(err)
		}
	}
	if valid != 1 {
		t.Fatalf("valid concurrent tokens = %d, want 1", valid)
	}
	assertCurrentRuntimeCount(t, fixture.database, fixture.agentID, 1)
}

func TestAgentRuntimeCreateValidatesComputerBeforeBinding(t *testing.T) {
	fixture := openAgentRuntimeFixture(t)
	params := CreateAgentRuntimeSessionParams{
		ComputerID: fixture.computer.ID, RegistrationKey: "wrong-registration-key",
		AgentID: uuid.NewString(), PlacementDesiredRevision: 99,
		Token: runtimeTestToken(3), Now: fixture.now, ExpiresAt: fixture.now.Add(10 * time.Minute),
	}
	if _, err := fixture.database.CreateAgentRuntimeSession(context.Background(), params); !errors.Is(err, ErrRegistrationKeyMismatch) {
		t.Fatalf("wrong key error = %v", err)
	}
	assertRuntimeRows(t, fixture.database, 0)

	params.RegistrationKey = fixture.registrationKey
	if _, err := fixture.database.CreateAgentRuntimeSession(context.Background(), params); !errors.Is(err, ErrAgentRuntimeBinding) {
		t.Fatalf("wrong binding error = %v", err)
	}
	assertRuntimeRows(t, fixture.database, 0)

	params.ComputerID = uuid.NewString()
	if _, err := fixture.database.CreateAgentRuntimeSession(context.Background(), params); !errors.Is(err, ErrComputerNotFound) {
		t.Fatalf("missing computer error = %v", err)
	}
	assertRuntimeRows(t, fixture.database, 0)
}

func TestAgentRuntimeTTLValidationDoesNotMutateSessions(t *testing.T) {
	fixture := openAgentRuntimeFixture(t)
	for index, lifetime := range []time.Duration{10*time.Minute + time.Nanosecond, 24 * time.Hour} {
		_, err := fixture.database.CreateAgentRuntimeSession(context.Background(), CreateAgentRuntimeSessionParams{
			ComputerID: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
			AgentID: fixture.agentID, PlacementDesiredRevision: 1, Token: runtimeTestToken(byte(50 + index)),
			Now: fixture.now, ExpiresAt: fixture.now.Add(lifetime),
		})
		if !errors.Is(err, ErrAgentRuntimeInvalid) {
			t.Fatalf("create lifetime %s error = %v", lifetime, err)
		}
		assertRuntimeRows(t, fixture.database, 0)
	}

	currentToken := runtimeTestToken(52)
	createRuntimeSession(t, fixture, currentToken, fixture.now)
	authentication, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), currentToken, fixture.now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	before := readAgentRuntimeRows(t, fixture.database)
	for index, lifetime := range []time.Duration{10*time.Minute + time.Nanosecond, 24 * time.Hour} {
		_, err := fixture.database.RenewAgentRuntimeSession(context.Background(), RenewAgentRuntimeSessionParams{
			Proof: authentication.Proof, ComputerID: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
			Token: runtimeTestToken(byte(53 + index)), Now: fixture.now.Add(time.Second),
			ExpiresAt: fixture.now.Add(time.Second).Add(lifetime),
		})
		if !errors.Is(err, ErrAgentRuntimeInvalid) {
			t.Fatalf("renew lifetime %s error = %v", lifetime, err)
		}
		after := readAgentRuntimeRows(t, fixture.database)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("renew lifetime %s mutated rows: before=%+v after=%+v", lifetime, before, after)
		}
		if _, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), currentToken, fixture.now.Add(2*time.Second)); err != nil {
			t.Fatalf("renew lifetime %s revoked current token: %v", lifetime, err)
		}
	}
}

func TestAgentRuntimeRenewAndRevokeRequireCurrentComputer(t *testing.T) {
	fixture := openAgentRuntimeFixture(t)
	firstToken := runtimeTestToken(4)
	createRuntimeSession(t, fixture, firstToken, fixture.now)
	authentication, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), firstToken, fixture.now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	wrongKey := RenewAgentRuntimeSessionParams{
		Proof: authentication.Proof, ComputerID: fixture.computer.ID, RegistrationKey: "wrong-key",
		Token: runtimeTestToken(5), Now: fixture.now.Add(time.Second), ExpiresAt: fixture.now.Add(10*time.Minute + time.Second),
	}
	if _, err := fixture.database.RenewAgentRuntimeSession(context.Background(), wrongKey); !errors.Is(err, ErrRegistrationKeyMismatch) {
		t.Fatalf("wrong key renew error = %v", err)
	}
	assertCurrentRuntimeCount(t, fixture.database, fixture.agentID, 1)
	if _, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), firstToken, fixture.now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}

	secondToken := runtimeTestToken(6)
	renewed, err := fixture.database.RenewAgentRuntimeSession(context.Background(), RenewAgentRuntimeSessionParams{
		Proof: authentication.Proof, ComputerID: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
		Token: secondToken, Now: fixture.now.Add(2 * time.Second), ExpiresAt: fixture.now.Add(10*time.Minute + 2*time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if renewed.AgentID != fixture.agentID || renewed.ComputerID != fixture.computer.ID {
		t.Fatalf("renewed session = %+v", renewed)
	}
	assertRuntimeUnauthenticated(t, fixture.database, firstToken, fixture.now.Add(3*time.Second))
	if _, err := fixture.database.RenewAgentRuntimeSession(context.Background(), RenewAgentRuntimeSessionParams{
		Proof: authentication.Proof, ComputerID: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
		Token: runtimeTestToken(7), Now: fixture.now.Add(3 * time.Second), ExpiresAt: fixture.now.Add(10*time.Minute + 3*time.Second),
	}); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
		t.Fatalf("renew replay error = %v", err)
	}
	assertCurrentRuntimeCount(t, fixture.database, fixture.agentID, 1)

	renewedAuthentication, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), secondToken, fixture.now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.RevokeAgentRuntimeSession(context.Background(), RevokeAgentRuntimeSessionParams{
		Proof: renewedAuthentication.Proof, ComputerID: fixture.computer.ID,
		RegistrationKey: fixture.registrationKey, Now: fixture.now.Add(4 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	assertRuntimeUnauthenticated(t, fixture.database, secondToken, fixture.now.Add(5*time.Second))
	if err := fixture.database.RevokeAgentRuntimeSession(context.Background(), RevokeAgentRuntimeSessionParams{
		Proof: renewedAuthentication.Proof, ComputerID: fixture.computer.ID,
		RegistrationKey: fixture.registrationKey, Now: fixture.now.Add(5 * time.Second),
	}); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
		t.Fatalf("revoke replay error = %v", err)
	}
	assertCurrentRuntimeCount(t, fixture.database, fixture.agentID, 0)
}

func TestAgentRuntimeConcurrentRenewHasOneSuccessor(t *testing.T) {
	fixture := openAgentRuntimeFixture(t)
	firstToken := runtimeTestToken(40)
	createRuntimeSession(t, fixture, firstToken, fixture.now)
	authentication, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), firstToken, fixture.now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	tokens := []string{runtimeTestToken(41), runtimeTestToken(42)}
	errorsByIndex := make([]error, len(tokens))
	var wait sync.WaitGroup
	for index := range tokens {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, errorsByIndex[index] = fixture.database.RenewAgentRuntimeSession(context.Background(), RenewAgentRuntimeSessionParams{
				Proof: authentication.Proof, ComputerID: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
				Token: tokens[index], Now: fixture.now.Add(2 * time.Second), ExpiresAt: fixture.now.Add(10*time.Minute + 2*time.Second),
			})
		}(index)
	}
	wait.Wait()
	succeeded := 0
	for _, err := range errorsByIndex {
		if err == nil {
			succeeded++
			continue
		}
		if !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
			t.Fatalf("concurrent renewal error = %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful renewals = %d, want 1", succeeded)
	}
	valid := 0
	for _, token := range tokens {
		if _, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), token, fixture.now.Add(3*time.Second)); err == nil {
			valid++
		} else if !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
			t.Fatal(err)
		}
	}
	if valid != 1 {
		t.Fatalf("valid successor tokens = %d, want 1", valid)
	}
	assertCurrentRuntimeCount(t, fixture.database, fixture.agentID, 1)
}

func TestAgentRuntimeProofRecheckRejectsLifecycleChanges(t *testing.T) {
	tests := map[string]func(*testing.T, *agentRuntimeFixture, AgentRuntimeAuthentication){
		"create replacement": func(t *testing.T, fixture *agentRuntimeFixture, _ AgentRuntimeAuthentication) {
			createRuntimeSession(t, fixture, runtimeTestToken(9), fixture.now.Add(2*time.Second))
		},
		"revoke": func(t *testing.T, fixture *agentRuntimeFixture, authentication AgentRuntimeAuthentication) {
			if err := fixture.database.RevokeAgentRuntimeSession(context.Background(), RevokeAgentRuntimeSessionParams{
				Proof: authentication.Proof, ComputerID: fixture.computer.ID,
				RegistrationKey: fixture.registrationKey, Now: fixture.now.Add(2 * time.Second),
			}); err != nil {
				t.Fatal(err)
			}
		},
		"placement desired revision": func(t *testing.T, fixture *agentRuntimeFixture, _ AgentRuntimeAuthentication) {
			if _, err := fixture.database.db.Exec(`
				UPDATE agent_placements SET desired_revision = 2, state = 'ready', updated_at = ? WHERE agent_id = ?
			`, unixNano(fixture.now.Add(2*time.Second)), fixture.agentID); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := openAgentRuntimeFixture(t)
			token := runtimeTestToken(8)
			createRuntimeSession(t, fixture, token, fixture.now)
			authentication, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), token, fixture.now.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			change(t, fixture, authentication)
			tx, err := fixture.database.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := requireAgentRuntimeSession(context.Background(), tx, authentication.Proof, fixture.now.Add(3*time.Second)); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
				t.Fatalf("recheck error = %v", err)
			}
		})
	}
}

func TestAgentRuntimeSessionSurvivesRestartAndExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	fixture := openAgentRuntimeFixtureAt(t, path)
	token := runtimeTestToken(20)
	createRuntimeSession(t, fixture, token, fixture.now)
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err := restarted.AuthenticateAgentRuntimeSession(context.Background(), token, fixture.now.Add(9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertRuntimeUnauthenticated(t, restarted, token, fixture.now.Add(10*time.Minute))
}

func TestAgentRuntimeAuthenticationTracksCurrentPlacement(t *testing.T) {
	tests := map[string]func(*testing.T, *agentRuntimeFixture){
		"pending": func(t *testing.T, fixture *agentRuntimeFixture) {
			if _, err := fixture.database.db.Exec("UPDATE agent_placements SET state = 'pending' WHERE agent_id = ?", fixture.agentID); err != nil {
				t.Fatal(err)
			}
		},
		"failed": func(t *testing.T, fixture *agentRuntimeFixture) {
			if _, err := fixture.database.db.Exec("UPDATE agent_placements SET state = 'failed', error_code = 'workspace_unavailable' WHERE agent_id = ?", fixture.agentID); err != nil {
				t.Fatal(err)
			}
		},
		"reassigned": func(t *testing.T, fixture *agentRuntimeFixture) {
			other := pairTestComputer(t, fixture.database, fixture.owner, "other-registration-key", testCapabilityInventory("test", true), fixture.now)
			if _, err := fixture.database.db.Exec(`
				UPDATE agent_placements
				SET computer_id = ?, desired_revision = 2, state = 'ready', updated_at = ?
				WHERE agent_id = ?
			`, other.ID, unixNano(fixture.now.Add(time.Second)), fixture.agentID); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := openAgentRuntimeFixture(t)
			token := runtimeTestToken(21)
			createRuntimeSession(t, fixture, token, fixture.now)
			change(t, fixture)
			assertRuntimeUnauthenticated(t, fixture.database, token, fixture.now.Add(2*time.Second))
			assertCurrentRuntimeCount(t, fixture.database, fixture.agentID, 1)
		})
	}
}

func TestAgentRuntimeDatabaseStoresOnlyDomainSeparatedHash(t *testing.T) {
	fixture := openAgentRuntimeFixture(t)
	token := runtimeTestToken(22)
	createRuntimeSession(t, fixture, token, fixture.now)
	var storedHash []byte
	if err := fixture.database.db.QueryRow("SELECT token_hash FROM agent_runtime_sessions").Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	want := agentRuntimeTokenHash(token)
	if !bytes.Equal(storedHash, want[:]) {
		t.Fatalf("stored hash = %x, want %x", storedHash, want)
	}
	plain := sha256.Sum256([]byte(token))
	if bytes.Equal(storedHash, plain[:]) || bytes.Contains(storedHash, []byte(token)) {
		t.Fatal("runtime token was not domain-separated before persistence")
	}
	var tokenColumns int
	if err := fixture.database.db.QueryRow(`
		SELECT count(*) FROM pragma_table_info('agent_runtime_sessions') WHERE name = 'token'
	`).Scan(&tokenColumns); err != nil {
		t.Fatal(err)
	}
	if tokenColumns != 0 {
		t.Fatal("agent_runtime_sessions exposes a raw token column")
	}
}

type agentRuntimeFixture struct {
	database        *Store
	owner           Principal
	computer        Computer
	registrationKey string
	agentID         string
	now             time.Time
}

func openAgentRuntimeFixture(t *testing.T) *agentRuntimeFixture {
	t.Helper()
	fixture := openAgentRuntimeFixtureAt(t, filepath.Join(t.TempDir(), "server.db"))
	t.Cleanup(func() {
		if err := fixture.database.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture
}

func openAgentRuntimeFixtureAt(t *testing.T, path string) *agentRuntimeFixture {
	t.Helper()
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 21, 1, 0, 0, 0, time.UTC)
	bootstrap, err := database.EnsureAuthority(context.Background(), runtimeTestToken(250), now)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	registrationKey := "computer-registration-key"
	computer := pairTestComputer(t, database, owner, registrationKey, testCapabilityInventory("test", true), now)
	agent, err := database.CreateAgent(context.Background(), testCreateAgentParams(owner, "runtime-"+uuid.NewString()[:8], now))
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	bindTestRuntimeCredential(t, database, agent.ID, computer.ID, "cred_unbound_"+agent.ID, "openai", now)
	configureTestRuntimeSpec(t, database, owner, agent.ID, now)
	placement, err := database.SetAgentPlacement(context.Background(), SetAgentPlacementParams{
		RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID, ComputerID: computer.ID, Now: now,
	})
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.AcknowledgeAgentPlacement(context.Background(), AcknowledgePlacementParams{
		ComputerID: computer.ID, RegistrationKey: registrationKey, AgentID: agent.ID,
		DesiredRevision: placement.DesiredRevision, State: "ready", Now: now,
	}); err != nil {
		database.Close()
		t.Fatal(err)
	}
	return &agentRuntimeFixture{
		database: database, owner: owner, computer: computer, registrationKey: registrationKey, agentID: agent.ID, now: now,
	}
}

func createRuntimeSession(t *testing.T, fixture *agentRuntimeFixture, token string, now time.Time) AgentRuntimeSession {
	t.Helper()
	session, err := fixture.database.CreateAgentRuntimeSession(context.Background(), CreateAgentRuntimeSessionParams{
		ComputerID: fixture.computer.ID, RegistrationKey: fixture.registrationKey,
		AgentID: fixture.agentID, PlacementDesiredRevision: 1, Token: token,
		Now: now, ExpiresAt: now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func runtimeTestToken(value byte) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%032d", value))[:32])
}

func assertRuntimeUnauthenticated(t *testing.T, database *Store, token string, now time.Time) {
	t.Helper()
	if _, err := database.AuthenticateAgentRuntimeSession(context.Background(), token, now); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
		t.Fatalf("authentication error = %v", err)
	}
}

func assertCurrentRuntimeCount(t *testing.T, database *Store, agentID string, want int) {
	t.Helper()
	var count int
	if err := database.db.QueryRow(`
		SELECT count(*) FROM agent_runtime_sessions WHERE agent_id = ? AND revoked_at IS NULL
	`, agentID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("current session count = %d, want %d", count, want)
	}
}

func assertRuntimeRows(t *testing.T, database *Store, want int) {
	t.Helper()
	var count int
	if err := database.db.QueryRow("SELECT count(*) FROM agent_runtime_sessions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("runtime session rows = %d, want %d", count, want)
	}
}

type agentRuntimeRow struct {
	TokenHash                string
	AgentID                  string
	ComputerID               string
	PlacementDesiredRevision uint64
	CreatedAt                int64
	ExpiresAt                int64
	RevokedAt                sql.NullInt64
}

func readAgentRuntimeRows(t *testing.T, database *Store) []agentRuntimeRow {
	t.Helper()
	rows, err := database.db.Query(`
		SELECT hex(token_hash), agent_id, computer_id, placement_desired_revision, created_at, expires_at, revoked_at
		FROM agent_runtime_sessions
		ORDER BY created_at, hex(token_hash)
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []agentRuntimeRow
	for rows.Next() {
		var row agentRuntimeRow
		if err := rows.Scan(&row.TokenHash, &row.AgentID, &row.ComputerID, &row.PlacementDesiredRevision, &row.CreatedAt, &row.ExpiresAt, &row.RevokedAt); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
