package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPairingAttemptSurvivesRestartAndCompletesAtomically(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "sumi")
	state, err := Open(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	attempt := PairingAttempt{
		ServerURL: "https://sumi.test", PairingToken: testSecret(1), RequestID: uuid.NewString(),
		RegistrationKey: "registration-secret", Name: "build-host", OS: "linux", Arch: "arm64", CreatedAt: now,
	}
	if err := state.SavePairingAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	if err := state.SavePairingAttempt(context.Background(), attempt); err != nil {
		t.Fatalf("exact pairing attempt replay: %v", err)
	}
	conflict := attempt
	conflict.RequestID = uuid.NewString()
	if err := state.SavePairingAttempt(context.Background(), conflict); err == nil {
		t.Fatal("conflicting pairing attempt replaced persisted attempt")
	}
	if _, err := Open(dataRoot); err == nil {
		t.Fatal("second state owner acquired the lock")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	state, err = Open(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	restored, found, err := state.PairingAttempt(context.Background())
	if err != nil || !found || !samePairingAttempt(restored, attempt) {
		t.Fatalf("restored pairing attempt = %+v, %v, %v", restored, found, err)
	}
	identity := Identity{
		ServerURL: attempt.ServerURL, ComputerID: uuid.NewString(), RegistrationKey: attempt.RegistrationKey, PairedAt: now.Add(time.Second),
	}
	if err := state.CompletePairing(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	if _, found, err := state.PairingAttempt(context.Background()); err != nil || found {
		t.Fatalf("pairing attempt after completion = %v, %v", found, err)
	}
	storedIdentity, found, err := state.Identity(context.Background())
	if err != nil || !found || !sameIdentity(storedIdentity, identity) {
		t.Fatalf("stored identity = %+v, %v, %v", storedIdentity, found, err)
	}
	if err := state.CompletePairing(context.Background(), identity); err != nil {
		t.Fatalf("exact identity completion replay: %v", err)
	}
	newAttempt := attempt
	newAttempt.RequestID = uuid.NewString()
	newAttempt.PairingToken = testSecret(2)
	if err := state.SavePairingAttempt(context.Background(), newAttempt); err == nil {
		t.Fatal("pairing attempt was accepted after identity existed")
	}
	conflictingIdentity := identity
	conflictingIdentity.ServerURL = "https://other-sumi.test"
	conflictingIdentity.ComputerID = uuid.NewString()
	conflictingIdentity.RegistrationKey = "other-registration-secret"
	if err := state.CompletePairing(context.Background(), conflictingIdentity); err == nil {
		t.Fatal("conflicting identity completion was accepted")
	}
	unchanged, found, err := state.Identity(context.Background())
	if err != nil || !found || !sameIdentity(unchanged, identity) {
		t.Fatalf("identity after conflicts = %+v, %v, %v", unchanged, found, err)
	}
	assertStateModes(t, dataRoot)
}

func TestPairingAttemptRejectsNonCanonicalToken(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "sumi"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	attempt := PairingAttempt{
		ServerURL: "https://sumi.test", PairingToken: "not-a-32-byte-secret", RequestID: uuid.NewString(),
		RegistrationKey: "registration-secret", Name: "host", OS: "linux", Arch: "arm64", CreatedAt: time.Now(),
	}
	if err := state.SavePairingAttempt(context.Background(), attempt); err == nil {
		t.Fatal("non-canonical pairing token was persisted")
	}
}

func TestStateSchemaMarker(t *testing.T) {
	t.Run("current schema reopens", func(t *testing.T) {
		dataRoot := filepath.Join(t.TempDir(), "sumi")
		state, err := Open(dataRoot)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := Open(dataRoot)
		if err != nil {
			t.Fatal(err)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("nonempty database without marker", func(t *testing.T) {
		dataRoot := filepath.Join(t.TempDir(), "sumi")
		directory := filepath.Join(dataRoot, "data", stateDirectory)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		databasePath := filepath.Join(directory, databaseName)
		if err := os.WriteFile(databasePath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		database, err := sql.Open("sqlite", databasePath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`CREATE TABLE computer_identity (singleton INTEGER PRIMARY KEY)`); err != nil {
			database.Close()
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dataRoot); err == nil || !strings.Contains(err.Error(), "schema is incompatible") {
			t.Fatalf("open without schema marker = %v", err)
		}
	})

	t.Run("changed marker", func(t *testing.T) {
		dataRoot := filepath.Join(t.TempDir(), "sumi")
		state, err := Open(dataRoot)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := state.db.Exec(`UPDATE state_metadata SET value = 'wrong' WHERE key = 'schema_version'`); err != nil {
			state.Close()
			t.Fatal(err)
		}
		if err := state.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dataRoot); err == nil || !strings.Contains(err.Error(), "schema is incompatible") {
			t.Fatalf("open with changed schema marker = %v", err)
		}
	})
}

func TestStateRejectsSymlinksAndLooseFiles(t *testing.T) {
	t.Run("state directory symlink", func(t *testing.T) {
		dataRoot := filepath.Join(t.TempDir(), "sumi")
		if err := os.MkdirAll(filepath.Join(dataRoot, "data"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), filepath.Join(dataRoot, "data", stateDirectory)); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dataRoot); err == nil {
			t.Fatal("state directory symlink was accepted")
		}
	})
	t.Run("database mode", func(t *testing.T) {
		dataRoot := filepath.Join(t.TempDir(), "sumi")
		directory := filepath.Join(dataRoot, "data", stateDirectory)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, databaseName), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dataRoot); err == nil {
			t.Fatal("loose database mode was accepted")
		}
	})
	t.Run("database symlink", func(t *testing.T) {
		dataRoot := filepath.Join(t.TempDir(), "sumi")
		directory := filepath.Join(dataRoot, "data", stateDirectory)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), filepath.Join(directory, databaseName)); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dataRoot); err == nil {
			t.Fatal("database symlink was accepted")
		}
	})
	t.Run("wal mode", func(t *testing.T) {
		dataRoot := filepath.Join(t.TempDir(), "sumi")
		directory := filepath.Join(dataRoot, "data", stateDirectory)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, databaseName+"-wal"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dataRoot); err == nil {
			t.Fatal("loose WAL mode was accepted")
		}
	})
}

func TestRenewMutationAttemptsUseDistinctRequestsAndExpiries(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "sumi"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	runID := uuid.NewString()
	runAttempt := uint64(3)
	hash := sha256.Sum256([]byte("renew-payload"))
	seen := make(map[string]struct{})
	for index := range 3 {
		createdAt := now.Add(time.Duration(index) * time.Minute)
		attempt, err := state.BeginMutation(ctx, MutationAttempt{
			Operation: "run.renew", SubjectID: runID, PayloadHash: hash, RunID: runID,
			Attempt: runAttempt, Fence: 7, CreatedAt: createdAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := seen[attempt.RequestID]; exists {
			t.Fatalf("renew request id reused: %s", attempt.RequestID)
		}
		seen[attempt.RequestID] = struct{}{}
		expiresAt := createdAt.Add(time.Minute)
		if err := state.CompleteMutation(ctx, attempt.RequestID, "succeeded", runAttempt, 7, &expiresAt, createdAt.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	attempts, err := state.MutationAttempts(ctx, "run.renew", runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 3 {
		t.Fatalf("renew attempts = %d, want 3", len(attempts))
	}
	for index, attempt := range attempts {
		wantExpiry := now.Add(time.Duration(index)*time.Minute + time.Minute)
		if attempt.Status != "succeeded" || attempt.ResponseExpiresAt == nil || !attempt.ResponseExpiresAt.Equal(wantExpiry) {
			t.Fatalf("renew attempt %d = %+v", index, attempt)
		}
	}
}

func TestRuntimeSessionCASRejectsStaleSaveAndDelete(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "sumi"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	now := time.Now()
	agentID := uuid.NewString()
	firstComputerID := uuid.NewString()
	secondComputerID := uuid.NewString()
	first := RuntimeSession{
		AgentID: agentID, ComputerID: firstComputerID, PlacementDesiredRevision: 1, Token: testSecret(3),
		ExpiresAt: now.Add(10 * time.Minute), UpdatedAt: now,
	}
	second := RuntimeSession{
		AgentID: agentID, ComputerID: secondComputerID, PlacementDesiredRevision: 2, Token: testSecret(4),
		ExpiresAt: now.Add(11 * time.Minute), UpdatedAt: now.Add(time.Minute),
	}
	if err := state.SaveRuntimeSession(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveRuntimeSession(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveRuntimeSession(ctx, first); err == nil {
		t.Fatal("desired revision 1 response replaced desired revision 2")
	}
	if err := state.DeleteRuntimeSession(ctx, agentID, firstComputerID, 1); err != nil {
		t.Fatal(err)
	}
	staleRenew := second
	staleRenew.Token = testSecret(5)
	staleRenew.ExpiresAt = second.ExpiresAt.Add(-time.Minute)
	staleRenew.UpdatedAt = second.UpdatedAt.Add(-time.Minute)
	if err := state.SaveRuntimeSession(ctx, staleRenew); err == nil {
		t.Fatal("stale same-revision renew response replaced current session")
	}
	sessions, err := state.RuntimeSessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].PlacementDesiredRevision != 2 || sessions[0].ComputerID != secondComputerID || sessions[0].Token != second.Token {
		t.Fatalf("runtime after stale operations = %+v, %v", sessions, err)
	}
	if err := state.DeleteRuntimeSession(ctx, agentID, secondComputerID, 2); err != nil {
		t.Fatal(err)
	}
	sessions, err = state.RuntimeSessions(ctx)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("runtime after exact delete = %+v, %v", sessions, err)
	}
}

func TestMutationPendingSingleFlightSurvivesRestart(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "sumi")
	state, err := Open(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runID := uuid.NewString()
	hash := sha256.Sum256([]byte("renew-payload"))
	candidate := MutationAttempt{
		Operation: "run.renew", SubjectID: runID, PayloadHash: hash, RunID: runID,
		Attempt: 2, Fence: 9, CreatedAt: time.Now(),
	}
	first, err := state.BeginMutation(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := state.BeginMutation(ctx, candidate)
	if err != nil || replayed.RequestID != first.RequestID {
		t.Fatalf("pending replay = %+v, %v", replayed, err)
	}
	if _, err := state.db.ExecContext(ctx, `
		INSERT INTO mutation_attempts(request_id, operation, subject_id, payload_hash, status, run_id, attempt, fence, created_at)
		VALUES(?, ?, ?, ?, 'pending', ?, ?, ?, ?)
	`, uuid.NewString(), candidate.Operation, candidate.SubjectID, candidate.PayloadHash[:], candidate.RunID, candidate.Attempt, candidate.Fence, time.Now().UnixNano()); err == nil {
		t.Fatal("database allowed a second pending mutation")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = Open(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	reopened, err := state.BeginMutation(ctx, candidate)
	if err != nil || reopened.RequestID != first.RequestID {
		t.Fatalf("reopened pending replay = %+v, %v", reopened, err)
	}
	conflict := candidate
	conflict.PayloadHash = sha256.Sum256([]byte("different-payload"))
	if _, err := state.BeginMutation(ctx, conflict); err == nil {
		t.Fatal("conflicting pending mutation was accepted")
	}
	expiresAt := time.Now().Add(time.Minute)
	if err := state.CompleteMutation(ctx, first.RequestID, "succeeded", candidate.Attempt, 9, &expiresAt, time.Now()); err != nil {
		t.Fatal(err)
	}
	next, err := state.BeginMutation(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if next.RequestID == first.RequestID {
		t.Fatalf("completed mutation request id was reused: %s", next.RequestID)
	}
}

func TestStaleOutboxFenceTombstoneAllowsNewFence(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "sumi"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	agentID := uuid.NewString()
	runID := uuid.NewString()
	mentionID := uuid.NewString()
	stale := OutboxEvent{
		OutboxEventID: uuid.NewString(), RequestID: uuid.NewString(), AgentID: agentID,
		PlacementDesiredRevision: 1, RunID: runID, Attempt: 1, Fence: 1,
		Outcome: "succeeded", Body: "sensitive stale body", MentionedAgentIDs: []string{mentionID}, CreatedAt: now,
	}
	if err := state.EnqueueOutbox(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if err := state.EnqueueOutbox(ctx, stale); err != nil {
		t.Fatalf("exact local enqueue replay: %v", err)
	}
	conflict := stale
	conflict.Body = "different body"
	if err := state.EnqueueOutbox(ctx, conflict); err == nil {
		t.Fatal("conflicting local enqueue replay was accepted")
	}
	if err := state.TombstoneOutbox(ctx, stale.OutboxEventID, "stale_fence"); err != nil {
		t.Fatal(err)
	}
	current := stale
	current.OutboxEventID = uuid.NewString()
	current.RequestID = uuid.NewString()
	current.Fence = 2
	current.Body = "current result"
	current.CreatedAt = now.Add(time.Minute)
	if err := state.EnqueueOutbox(ctx, current); err != nil {
		t.Fatal(err)
	}
	events, err := state.Outbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("outbox events = %d, want 2", len(events))
	}
	if events[0].State != "tombstone" || events[0].Body != "" || len(events[0].MentionedAgentIDs) != 0 || events[0].RejectionCode != "stale_fence" {
		t.Fatalf("stale tombstone retained sensitive data: %+v", events[0])
	}
	if events[1].State != "pending" || events[1].Fence != 2 || events[1].Body != current.Body || len(events[1].MentionedAgentIDs) != 1 {
		t.Fatalf("current outbox event = %+v", events[1])
	}
	if err := state.AckOutbox(ctx, current.OutboxEventID); err != nil {
		t.Fatal(err)
	}
	events, err = state.Outbox(ctx)
	if err != nil || len(events) != 1 || events[0].State != "tombstone" {
		t.Fatalf("outbox after ack = %+v, %v", events, err)
	}
}

func TestPendingOutboxIsBoundedAndHydratesMentions(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "sumi"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	agentID := uuid.NewString()
	runID := uuid.NewString()
	var events []OutboxEvent
	for index := 0; index < 3; index++ {
		event := OutboxEvent{
			OutboxEventID: uuid.NewString(), RequestID: uuid.NewString(), AgentID: agentID,
			PlacementDesiredRevision: 1, RunID: runID, Attempt: uint64(index + 1), Fence: uint64(index + 1),
			Outcome: "succeeded", Body: "result", MentionedAgentIDs: []string{uuid.NewString(), uuid.NewString()},
			CreatedAt: now.Add(time.Duration(index) * time.Second),
		}
		if err := state.EnqueueOutbox(ctx, event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := state.TombstoneOutbox(ctx, events[0].OutboxEventID, "stale_fence"); err != nil {
		t.Fatal(err)
	}
	pending, err := state.PendingOutbox(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].OutboxEventID != events[1].OutboxEventID || len(pending[0].MentionedAgentIDs) != 2 {
		t.Fatalf("pending outbox = %+v", pending)
	}
	for _, event := range events[:2] {
		found, err := state.HasOutboxCompletion(ctx, event.RunID, event.Attempt, event.Fence)
		if err != nil || !found {
			t.Fatalf("completion %q found = %t, %v", event.OutboxEventID, found, err)
		}
	}
	if _, err := state.PendingOutbox(ctx, 0); err == nil {
		t.Fatal("zero pending outbox limit was accepted")
	}
}

func TestEnqueueOutboxRejectsInvalidCompletionPayload(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "sumi"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	base := OutboxEvent{
		OutboxEventID: uuid.NewString(), RequestID: uuid.NewString(), AgentID: uuid.NewString(),
		PlacementDesiredRevision: 1, RunID: uuid.NewString(), Attempt: 1, Fence: 1,
		Outcome: "succeeded", Body: "valid completion", CreatedAt: time.Now(),
	}
	mention := uuid.NewString()
	tooManyMentions := make([]string, 65)
	for index := range tooManyMentions {
		tooManyMentions[index] = uuid.NewString()
	}
	cases := []struct {
		name  string
		apply func(*OutboxEvent)
	}{
		{"empty body", func(event *OutboxEvent) { event.Body = "" }},
		{"invalid utf8", func(event *OutboxEvent) { event.Body = string([]byte{0xff}) }},
		{"body too long", func(event *OutboxEvent) { event.Body = strings.Repeat("x", maxCompletionBodyRunes+1) }},
		{"too many mentions", func(event *OutboxEvent) { event.MentionedAgentIDs = tooManyMentions }},
		{"duplicate mentions", func(event *OutboxEvent) { event.MentionedAgentIDs = []string{mention, mention} }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			event := base
			event.OutboxEventID = uuid.NewString()
			event.RequestID = uuid.NewString()
			test.apply(&event)
			if err := state.EnqueueOutbox(context.Background(), event); err == nil {
				t.Fatal("invalid completion payload was persisted")
			}
			events, err := state.Outbox(context.Background())
			if err != nil || len(events) != 0 {
				t.Fatalf("outbox after invalid completion = %+v, %v", events, err)
			}
		})
	}
}

func assertStateModes(t *testing.T, dataRoot string) {
	t.Helper()
	for _, path := range []string{dataRoot, filepath.Join(dataRoot, "data"), filepath.Join(dataRoot, "data", stateDirectory)} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("state directory %s = %v, %v", path, info, err)
		}
	}
	directory := filepath.Join(dataRoot, "data", stateDirectory)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(directory, entry.Name()))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("state file %s = %v, %v", entry.Name(), info, err)
		}
	}
}

func testSecret(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}
