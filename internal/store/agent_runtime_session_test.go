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
	f := openAgentRuntimeFixture(t)
	firstToken := rtToken(1)
	first := createRuntimeSession(t, f, firstToken, f.now)
	if first.AgentID != f.agentID || first.ComputerID != f.computer.ID || first.PlacementDesiredRevision != 1 {
		t.Fatalf("first session = %+v", first)
	}
	if _, err := f.database.AuthenticateAgentRuntimeSession(context.Background(), firstToken, f.now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	secondToken := rtToken(2)
	createRuntimeSession(t, f, secondToken, f.now.Add(time.Second))
	assertRuntimeUnauthenticated(t, f.database, firstToken, f.now.Add(2*time.Second))
	if _, err := f.database.AuthenticateAgentRuntimeSession(context.Background(), secondToken, f.now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertCurrentRuntimeCount(t, f.database, f.agentID, 1)

	tokens := make([]string, 12)
	errorsByIndex := make([]error, len(tokens))
	var wait sync.WaitGroup
	for index := range tokens {
		tokens[index] = rtToken(byte(index + 10))
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, errorsByIndex[index] = f.database.CreateAgentRuntimeSession(context.Background(), CreateAgentRuntimeSessionParams{
				ComputerID: f.computer.ID, RegistrationKey: f.registrationKey,
				AgentID: f.agentID, PlacementDesiredRevision: 1, Token: tokens[index],
				Now: f.now.Add(3 * time.Second), ExpiresAt: f.now.Add(10*time.Minute + 3*time.Second),
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
		if _, err := f.database.AuthenticateAgentRuntimeSession(context.Background(), token, f.now.Add(4*time.Second)); err == nil {
			valid++
		} else if !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
			t.Fatal(err)
		}
	}
	if valid != 1 {
		t.Fatalf("valid concurrent tokens = %d, want 1", valid)
	}
	assertCurrentRuntimeCount(t, f.database, f.agentID, 1)
}

func TestAgentRuntimeCreateValidatesComputerBeforeBinding(t *testing.T) {
	f := openAgentRuntimeFixture(t)
	params := CreateAgentRuntimeSessionParams{
		ComputerID: f.computer.ID, RegistrationKey: "wrong-registration-key",
		AgentID: uuid.NewString(), PlacementDesiredRevision: 99,
		Token: rtToken(3), Now: f.now, ExpiresAt: f.now.Add(10 * time.Minute),
	}
	if _, err := f.database.CreateAgentRuntimeSession(context.Background(), params); !errors.Is(err, ErrRegistrationKeyMismatch) {
		t.Fatalf("wrong key error = %v", err)
	}
	assertRuntimeRows(t, f.database, 0)

	params.RegistrationKey = f.registrationKey
	if _, err := f.database.CreateAgentRuntimeSession(context.Background(), params); !errors.Is(err, ErrAgentRuntimeBinding) {
		t.Fatalf("wrong binding error = %v", err)
	}
	assertRuntimeRows(t, f.database, 0)

	params.ComputerID = uuid.NewString()
	if _, err := f.database.CreateAgentRuntimeSession(context.Background(), params); !errors.Is(err, ErrComputerNotFound) {
		t.Fatalf("missing computer error = %v", err)
	}
	assertRuntimeRows(t, f.database, 0)
}

func TestAgentRuntimeTTLValidationDoesNotMutateSessions(t *testing.T) {
	f := openAgentRuntimeFixture(t)
	for index, lifetime := range []time.Duration{10*time.Minute + time.Nanosecond, 24 * time.Hour} {
		_, err := f.database.CreateAgentRuntimeSession(context.Background(), CreateAgentRuntimeSessionParams{
			ComputerID: f.computer.ID, RegistrationKey: f.registrationKey,
			AgentID: f.agentID, PlacementDesiredRevision: 1, Token: rtToken(byte(50 + index)),
			Now: f.now, ExpiresAt: f.now.Add(lifetime),
		})
		if !errors.Is(err, ErrAgentRuntimeInvalid) {
			t.Fatalf("create lifetime %s error = %v", lifetime, err)
		}
		assertRuntimeRows(t, f.database, 0)
	}

	currentToken := rtToken(52)
	createRuntimeSession(t, f, currentToken, f.now)
	authentication, err := f.database.AuthenticateAgentRuntimeSession(context.Background(), currentToken, f.now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	before := readAgentRuntimeRows(t, f.database)
	for index, lifetime := range []time.Duration{10*time.Minute + time.Nanosecond, 24 * time.Hour} {
		_, err := f.database.RenewAgentRuntimeSession(context.Background(), RenewAgentRuntimeSessionParams{
			Proof: authentication.Proof, ComputerID: f.computer.ID, RegistrationKey: f.registrationKey,
			Token: rtToken(byte(53 + index)), Now: f.now.Add(time.Second),
			ExpiresAt: f.now.Add(time.Second).Add(lifetime),
		})
		if !errors.Is(err, ErrAgentRuntimeInvalid) {
			t.Fatalf("renew lifetime %s error = %v", lifetime, err)
		}
		after := readAgentRuntimeRows(t, f.database)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("renew lifetime %s mutated rows: before=%+v after=%+v", lifetime, before, after)
		}
		if _, err := f.database.AuthenticateAgentRuntimeSession(context.Background(), currentToken, f.now.Add(2*time.Second)); err != nil {
			t.Fatalf("renew lifetime %s revoked current token: %v", lifetime, err)
		}
	}
}

func TestAgentRuntimeRenewAndRevokeRequireCurrentComputer(t *testing.T) {
	f := openAgentRuntimeFixture(t)
	firstToken := rtToken(4)
	createRuntimeSession(t, f, firstToken, f.now)
	authentication, err := f.database.AuthenticateAgentRuntimeSession(context.Background(), firstToken, f.now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	wrongKey := RenewAgentRuntimeSessionParams{
		Proof: authentication.Proof, ComputerID: f.computer.ID, RegistrationKey: "wrong-key",
		Token: rtToken(5), Now: f.now.Add(time.Second), ExpiresAt: f.now.Add(10*time.Minute + time.Second),
	}
	if _, err := f.database.RenewAgentRuntimeSession(context.Background(), wrongKey); !errors.Is(err, ErrRegistrationKeyMismatch) {
		t.Fatalf("wrong key renew error = %v", err)
	}
	assertCurrentRuntimeCount(t, f.database, f.agentID, 1)
	if _, err := f.database.AuthenticateAgentRuntimeSession(context.Background(), firstToken, f.now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}

	secondToken := rtToken(6)
	renewed, err := f.database.RenewAgentRuntimeSession(context.Background(), RenewAgentRuntimeSessionParams{
		Proof: authentication.Proof, ComputerID: f.computer.ID, RegistrationKey: f.registrationKey,
		Token: secondToken, Now: f.now.Add(2 * time.Second), ExpiresAt: f.now.Add(10*time.Minute + 2*time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if renewed.AgentID != f.agentID || renewed.ComputerID != f.computer.ID {
		t.Fatalf("renewed session = %+v", renewed)
	}
	assertRuntimeUnauthenticated(t, f.database, firstToken, f.now.Add(3*time.Second))
	if _, err := f.database.RenewAgentRuntimeSession(context.Background(), RenewAgentRuntimeSessionParams{
		Proof: authentication.Proof, ComputerID: f.computer.ID, RegistrationKey: f.registrationKey,
		Token: rtToken(7), Now: f.now.Add(3 * time.Second), ExpiresAt: f.now.Add(10*time.Minute + 3*time.Second),
	}); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
		t.Fatalf("renew replay error = %v", err)
	}
	assertCurrentRuntimeCount(t, f.database, f.agentID, 1)

	renewedAuthentication, err := f.database.AuthenticateAgentRuntimeSession(context.Background(), secondToken, f.now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.database.RevokeAgentRuntimeSession(context.Background(), RevokeAgentRuntimeSessionParams{
		Proof: renewedAuthentication.Proof, ComputerID: f.computer.ID,
		RegistrationKey: f.registrationKey, Now: f.now.Add(4 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	assertRuntimeUnauthenticated(t, f.database, secondToken, f.now.Add(5*time.Second))
	if err := f.database.RevokeAgentRuntimeSession(context.Background(), RevokeAgentRuntimeSessionParams{
		Proof: renewedAuthentication.Proof, ComputerID: f.computer.ID,
		RegistrationKey: f.registrationKey, Now: f.now.Add(5 * time.Second),
	}); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
		t.Fatalf("revoke replay error = %v", err)
	}
	assertCurrentRuntimeCount(t, f.database, f.agentID, 0)
}

func TestAgentRuntimeConcurrentRenewHasOneSuccessor(t *testing.T) {
	f := openAgentRuntimeFixture(t)
	firstToken := rtToken(40)
	createRuntimeSession(t, f, firstToken, f.now)
	authentication, err := f.database.AuthenticateAgentRuntimeSession(context.Background(), firstToken, f.now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	tokens := []string{rtToken(41), rtToken(42)}
	errorsByIndex := make([]error, len(tokens))
	var wait sync.WaitGroup
	for index := range tokens {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, errorsByIndex[index] = f.database.RenewAgentRuntimeSession(context.Background(), RenewAgentRuntimeSessionParams{
				Proof: authentication.Proof, ComputerID: f.computer.ID, RegistrationKey: f.registrationKey,
				Token: tokens[index], Now: f.now.Add(2 * time.Second), ExpiresAt: f.now.Add(10*time.Minute + 2*time.Second),
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
		if _, err := f.database.AuthenticateAgentRuntimeSession(context.Background(), token, f.now.Add(3*time.Second)); err == nil {
			valid++
		} else if !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
			t.Fatal(err)
		}
	}
	if valid != 1 {
		t.Fatalf("valid successor tokens = %d, want 1", valid)
	}
	assertCurrentRuntimeCount(t, f.database, f.agentID, 1)
}

func TestAgentRuntimeProofRecheckRejectsLifecycleChanges(t *testing.T) {
	tests := map[string]func(*testing.T, *agentRuntimeFixture, AgentRuntimeAuthentication){
		"create replacement": func(t *testing.T, f *agentRuntimeFixture, _ AgentRuntimeAuthentication) {
			createRuntimeSession(t, f, rtToken(9), f.now.Add(2*time.Second))
		},
		"revoke": func(t *testing.T, f *agentRuntimeFixture, authentication AgentRuntimeAuthentication) {
			if err := f.database.RevokeAgentRuntimeSession(context.Background(), RevokeAgentRuntimeSessionParams{
				Proof: authentication.Proof, ComputerID: f.computer.ID,
				RegistrationKey: f.registrationKey, Now: f.now.Add(2 * time.Second),
			}); err != nil {
				t.Fatal(err)
			}
		},
		"placement desired revision": func(t *testing.T, f *agentRuntimeFixture, _ AgentRuntimeAuthentication) {
			if _, err := f.database.db.Exec(`
				UPDATE agent_placements SET desired_revision = 2, state = 'ready', updated_at = ? WHERE agent_id = ?
			`, unixNano(f.now.Add(2*time.Second)), f.agentID); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			f := openAgentRuntimeFixture(t)
			token := rtToken(8)
			createRuntimeSession(t, f, token, f.now)
			authentication, err := f.database.AuthenticateAgentRuntimeSession(context.Background(), token, f.now.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			change(t, f, authentication)
			tx, err := f.database.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := requireAgentRuntimeSession(context.Background(), tx, authentication.Proof, f.now.Add(3*time.Second)); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
				t.Fatalf("recheck error = %v", err)
			}
		})
	}
}

func TestAgentRuntimeSessionSurvivesRestartAndExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	f := openAgentRuntimeFixtureAt(t, path)
	token := rtToken(20)
	createRuntimeSession(t, f, token, f.now)
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err := restarted.AuthenticateAgentRuntimeSession(context.Background(), token, f.now.Add(9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertRuntimeUnauthenticated(t, restarted, token, f.now.Add(10*time.Minute))
}

func TestAgentRuntimeAuthenticationTracksCurrentPlacement(t *testing.T) {
	tests := map[string]func(*testing.T, *agentRuntimeFixture){
		"pending": func(t *testing.T, f *agentRuntimeFixture) {
			if _, err := f.database.db.Exec("UPDATE agent_placements SET state = 'pending' WHERE agent_id = ?", f.agentID); err != nil {
				t.Fatal(err)
			}
		},
		"failed": func(t *testing.T, f *agentRuntimeFixture) {
			if _, err := f.database.db.Exec("UPDATE agent_placements SET state = 'failed', error_code = 'workspace_unavailable' WHERE agent_id = ?", f.agentID); err != nil {
				t.Fatal(err)
			}
		},
		"reassigned": func(t *testing.T, f *agentRuntimeFixture) {
			other := pairTestComputer(t, f.database, f.owner, "other-registration-key", testCapabilityInventory("test", true), f.now)
			if _, err := f.database.db.Exec(`
				UPDATE agent_placements
				SET computer_id = ?, desired_revision = 2, state = 'ready', updated_at = ?
				WHERE agent_id = ?
			`, other.ID, unixNano(f.now.Add(time.Second)), f.agentID); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			f := openAgentRuntimeFixture(t)
			token := rtToken(21)
			createRuntimeSession(t, f, token, f.now)
			change(t, f)
			assertRuntimeUnauthenticated(t, f.database, token, f.now.Add(2*time.Second))
			assertCurrentRuntimeCount(t, f.database, f.agentID, 1)
		})
	}
}

func TestAgentRuntimeDatabaseStoresOnlyDomainSeparatedHash(t *testing.T) {
	f := openAgentRuntimeFixture(t)
	token := rtToken(22)
	createRuntimeSession(t, f, token, f.now)
	var storedHash []byte
	if err := f.database.db.QueryRow("SELECT token_hash FROM agent_runtime_sessions").Scan(&storedHash); err != nil {
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
	if err := f.database.db.QueryRow(`
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
	f := openAgentRuntimeFixtureAt(t, filepath.Join(t.TempDir(), "server.db"))
	t.Cleanup(func() {
		if err := f.database.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}

func openAgentRuntimeFixtureAt(t *testing.T, path string) *agentRuntimeFixture {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 21, 1, 0, 0, 0, time.UTC)
	bs, err := db.EnsureAuthority(context.Background(), rtToken(250), now)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bs.Human.ID, OrganizationID: bs.Organization.ID}
	regKey := "computer-registration-key"
	comp := pairTestComputer(t, db, owner, regKey, testCapabilityInventory("test", true), now)
	agent, err := db.CreateAgent(context.Background(), testCreateAgentParams(owner, "runtime-"+uuid.NewString()[:8], now))
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	bindTestRuntimeCredential(t, db, agent.ID, comp.ID, "cred_unbound_"+agent.ID, "openai", now)
	configureTestRuntimeSpec(t, db, owner, agent.ID, now)
	p, err := db.SetAgentPlacement(context.Background(), SetAgentPlacementParams{
		RequestID: uuid.NewString(), Actor: owner, AgentID: agent.ID, ComputerID: comp.ID, Now: now,
	})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.AcknowledgeAgentPlacement(context.Background(), AcknowledgePlacementParams{
		ComputerID: comp.ID, RegistrationKey: regKey, AgentID: agent.ID,
		DesiredRevision: p.DesiredRevision, State: "ready", Now: now,
	}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return &agentRuntimeFixture{
		database: db, owner: owner, computer: comp, registrationKey: regKey, agentID: agent.ID, now: now,
	}
}

func createRuntimeSession(t *testing.T, f *agentRuntimeFixture, token string, now time.Time) AgentRuntimeSession {
	t.Helper()
	s, err := f.database.CreateAgentRuntimeSession(context.Background(), CreateAgentRuntimeSessionParams{
		ComputerID: f.computer.ID, RegistrationKey: f.registrationKey,
		AgentID: f.agentID, PlacementDesiredRevision: 1, Token: token,
		Now: now, ExpiresAt: now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func rtToken(value byte) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%032d", value))[:32])
}

func assertRuntimeUnauthenticated(t *testing.T, db *Store, token string, now time.Time) {
	t.Helper()
	if _, err := db.AuthenticateAgentRuntimeSession(context.Background(), token, now); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
		t.Fatalf("auth error = %v", err)
	}
}

func assertCurrentRuntimeCount(t *testing.T, db *Store, agentID string, want int) {
	t.Helper()
	var n int
	if err := db.db.QueryRow(`
		SELECT count(*) FROM agent_runtime_sessions WHERE agent_id = ? AND revoked_at IS NULL
	`, agentID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("session count = %d, want %d", n, want)
	}
}

func assertRuntimeRows(t *testing.T, db *Store, want int) {
	t.Helper()
	var n int
	if err := db.db.QueryRow("SELECT count(*) FROM agent_runtime_sessions").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("rows = %d, want %d", n, want)
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

func readAgentRuntimeRows(t *testing.T, db *Store) []agentRuntimeRow {
	t.Helper()
	rows, err := db.db.Query(`
		SELECT hex(token_hash), agent_id, computer_id, placement_desired_revision, created_at, expires_at, revoked_at
		FROM agent_runtime_sessions
		ORDER BY created_at, hex(token_hash)
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []agentRuntimeRow
	for rows.Next() {
		var r agentRuntimeRow
		if err := rows.Scan(&r.TokenHash, &r.AgentID, &r.ComputerID, &r.PlacementDesiredRevision, &r.CreatedAt, &r.ExpiresAt, &r.RevokedAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
