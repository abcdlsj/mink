package computerstate

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
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
	now := time.Unix(1_800_000_000, 0).UTC()
	attempt := PairingAttempt{
		ServerURL: "https://sumi.test", PairingToken: "pairing-secret", RequestID: uuid.NewString(),
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
	if err != nil || !found || restored != attempt {
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
	if err != nil || !found || storedIdentity != identity {
		t.Fatalf("stored identity = %+v, %v, %v", storedIdentity, found, err)
	}
	assertStateModes(t, dataRoot)
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
	launchID := uuid.NewString()
	hash := sha256.Sum256([]byte("renew-payload"))
	seen := make(map[string]struct{})
	for index := range 3 {
		createdAt := now.Add(time.Duration(index) * time.Minute)
		attempt, err := state.BeginMutation(ctx, MutationAttempt{
			Operation: "run.renew", SubjectID: runID, PayloadHash: hash, RunID: runID,
			LaunchID: launchID, Fence: 7, CreatedAt: createdAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := seen[attempt.RequestID]; exists {
			t.Fatalf("renew request id reused: %s", attempt.RequestID)
		}
		seen[attempt.RequestID] = struct{}{}
		expiresAt := createdAt.Add(time.Minute)
		if err := state.CompleteMutation(ctx, attempt.RequestID, "succeeded", launchID, 7, &expiresAt, createdAt.Add(time.Second)); err != nil {
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
	launchID := uuid.NewString()
	mentionID := uuid.NewString()
	stale := OutboxEvent{
		OutboxEventID: uuid.NewString(), RequestID: uuid.NewString(), AgentID: agentID,
		PlacementGeneration: 1, RunID: runID, LaunchID: launchID, Fence: 1,
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
