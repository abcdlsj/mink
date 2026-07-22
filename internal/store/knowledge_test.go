package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func TestKnowledgeFTSAvailableAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	available, err := database.KnowledgeFTSAvailable(context.Background())
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if !available {
		database.Close()
		t.Fatal("knowledge FTS is unavailable")
	}
	if _, err := database.db.Exec(`INSERT INTO knowledge_fts(source_kind, source_id, source_version, generation, revision, body) VALUES('message', ?, 0, 1, zeroblob(32), 'knowledge capability probe')`, uuid.NewString()); err != nil {
		database.Close()
		t.Fatal(err)
	}
	var matches int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_fts WHERE knowledge_fts MATCH 'capability'`).Scan(&matches); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if matches != 1 {
		database.Close()
		t.Fatalf("knowledge FTS matches = %d, want 1", matches)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	available, err = reopened.KnowledgeFTSAvailable(context.Background())
	if err != nil || !available {
		t.Fatalf("knowledge FTS after reopen = %t, %v", available, err)
	}
}

func TestKnowledgeDirtySourcesUseGlobalCommittedSequence(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	now := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC)
	revision := KnowledgeWorkRevision(KnowledgeWorkFields{Goal: "index"})
	sources := []KnowledgeSource{
		{Kind: KnowledgeSourceMessage, ID: uuid.NewString()},
		{Kind: KnowledgeSourceWork, ID: uuid.NewString()},
		{Kind: KnowledgeSourceArtifactVersion, ID: uuid.NewString(), Version: 1},
	}
	for index, source := range sources {
		entry, err := database.EnqueueKnowledgeDirtySource(context.Background(), source, revision, now.Add(time.Duration(index)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if entry.Sequence != uint64(index+1) {
			t.Fatalf("sequence = %d, want %d", entry.Sequence, index+1)
		}
	}
}

func TestKnowledgeDirtySourceRollbackLeavesNoVisibleEntry(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	tx, err := database.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = enqueueKnowledgeDirtySource(context.Background(), tx, KnowledgeSource{Kind: KnowledgeSourceMessage, ID: uuid.NewString()}, KnowledgeWorkRevision(KnowledgeWorkFields{Goal: "rollback"}), time.Now())
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_dirty_sources`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("visible dirty sources = %d, want 0", count)
	}
}

func TestKnowledgeArtifactVersionKeysDoNotCollide(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	id := uuid.NewString()
	revision := KnowledgeWorkRevision(KnowledgeWorkFields{Goal: "artifact"})
	first, err := database.EnqueueKnowledgeDirtySource(context.Background(), KnowledgeSource{Kind: KnowledgeSourceArtifactVersion, ID: id, Version: 1}, revision, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.EnqueueKnowledgeDirtySource(context.Background(), KnowledgeSource{Kind: KnowledgeSourceArtifactVersion, ID: id, Version: 2}, revision, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if first.Source == second.Source || first.Sequence == second.Sequence {
		t.Fatalf("artifact entries collided: %+v / %+v", first, second)
	}
}

func TestKnowledgeWorkRevisionIsStructuredAndOrdered(t *testing.T) {
	base := KnowledgeWorkFields{Goal: "same", AcceptanceCriteria: []string{"one", "two"}, Constraints: []string{"limit"}, BlockingReason: "blocked", Result: "result"}
	cases := []KnowledgeWorkFields{
		{Goal: "", AcceptanceCriteria: []string{"one", "two"}, Constraints: []string{"limit"}, BlockingReason: "same", Result: "result"},
		{Goal: "same", AcceptanceCriteria: []string{"two", "one"}, Constraints: []string{"limit"}, BlockingReason: "blocked", Result: "result"},
		{Goal: "same", AcceptanceCriteria: []string{"one", "two", ""}, Constraints: []string{"limit"}, BlockingReason: "blocked", Result: "result"},
	}
	for _, changed := range cases {
		if KnowledgeWorkRevision(base) == KnowledgeWorkRevision(changed) {
			t.Fatalf("work revision did not change for %+v", changed)
		}
	}
}

func TestKnowledgeSourceRevisionsBindTheirAuthoritativeFields(t *testing.T) {
	messageID := uuid.NewString()
	if KnowledgeMessageRevision(messageID, 1) == KnowledgeMessageRevision(messageID, 2) {
		t.Fatal("message revision ignored target sequence")
	}
	artifactID := uuid.NewString()
	var digest [32]byte
	digest[0] = 1
	if KnowledgeArtifactVersionRevision(artifactID, 1, digest) == KnowledgeArtifactVersionRevision(artifactID, 2, digest) {
		t.Fatal("artifact revision ignored version")
	}
	digest[0] = 2
	if KnowledgeArtifactVersionRevision(artifactID, 2, [32]byte{1}) == KnowledgeArtifactVersionRevision(artifactID, 2, digest) {
		t.Fatal("artifact revision ignored content digest")
	}
}

func TestKnowledgeGenerationRequiresCompletePendingGeneration(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	now := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC)
	initial, err := database.KnowledgeIndexMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if initial.Status != KnowledgeIndexDegraded || initial.ActiveGeneration != 0 || initial.NextGeneration != 0 {
		t.Fatalf("initial metadata = %+v", initial)
	}
	rebuilding, err := database.StartKnowledgeRebuild(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilding.Status != KnowledgeIndexRebuilding || rebuilding.NextGeneration == 0 {
		t.Fatalf("rebuilding metadata = %+v", rebuilding)
	}
	if _, err := database.ActivateKnowledgeGeneration(context.Background(), rebuilding.NextGeneration); err == nil {
		t.Fatal("activated incomplete knowledge generation")
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), rebuilding.NextGeneration); err != nil {
		t.Fatal(err)
	}
	ready, err := database.ActivateKnowledgeGeneration(context.Background(), rebuilding.NextGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != KnowledgeIndexReady || ready.ActiveGeneration != rebuilding.NextGeneration || ready.NextGeneration != 0 {
		t.Fatalf("ready metadata = %+v", ready)
	}
}

func TestKnowledgeMigrationDownUpRoundTrip(t *testing.T) {
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
	if err := goose.Up(database, "migrations"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Down(database, "migrations"); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := database.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name = 'knowledge_fts')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("knowledge FTS survived migration down")
	}
	if err := goose.Up(database, "migrations"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	available, err := reopened.KnowledgeFTSAvailable(context.Background())
	if err != nil || !available {
		t.Fatalf("knowledge FTS after migration round-trip = %t, %v", available, err)
	}
}

func openKnowledgeStore(t *testing.T) *Store {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func TestKnowledgeSourceValidation(t *testing.T) {
	err := validateKnowledgeSource(KnowledgeSource{Kind: KnowledgeSourceMessage, ID: uuid.NewString(), Version: 1})
	if err == nil {
		t.Fatal("versioned message source was accepted")
	}
}
