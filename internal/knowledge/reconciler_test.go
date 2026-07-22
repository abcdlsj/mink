package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
)

func TestReconcileNextZeroHighWaterCompletesAndActivates(t *testing.T) {
	database := &reconcilerStore{progress: store.KnowledgeGenerationProgress{Generation: 7}}
	reconciler := &Reconciler{store: database}
	if !reconciler.reconcileNext(context.Background(), store.KnowledgeIndexMetadata{NextGeneration: 7}) {
		t.Fatal("reconcile zero-high-water generation did not progress")
	}
	if want := []string{"build", "complete", "activate"}; !reflect.DeepEqual(database.calls, want) {
		t.Fatalf("calls = %v, want %v", database.calls, want)
	}
}

func TestStartDiscardsInheritedCompleteGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.EnqueueKnowledgeDirtySource(context.Background(), store.KnowledgeSource{Kind: store.KnowledgeSourceMessage, ID: uuid.NewString()}, [32]byte{1}, time.Now()); err != nil {
		t.Fatal(err)
	}
	pending, err := database.StartKnowledgeRebuild(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.BuildKnowledgeGenerationSnapshot(context.Background(), pending.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), pending.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	reconciler := New(database)
	reconciler.Start(context.Background())
	defer reconciler.Close()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		metadata, err := database.KnowledgeIndexMetadata(context.Background())
		if err == nil && metadata.ActiveGeneration > pending.NextGeneration && metadata.NextGeneration == 0 && metadata.Status == store.KnowledgeIndexReady {
			return
		}
		time.Sleep(time.Millisecond)
	}
	metadata, err := database.KnowledgeIndexMetadata(context.Background())
	t.Fatalf("inherited complete generation was not replaced: %+v, %v", metadata, err)
}

func TestStartDiscardsInheritedBuildingGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := database.StartKnowledgeRebuild(context.Background(), time.Now())
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	reconciler := New(database)
	reconciler.Start(context.Background())
	defer reconciler.Close()
	if metadata := waitForReadyGeneration(t, database); metadata.ActiveGeneration <= pending.NextGeneration {
		t.Fatalf("inherited building generation was not replaced: %+v", metadata)
	}
}

func TestStartRepairsCorruptActiveIndexAndRebuilds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	active := createReadyGeneration(t, database)
	direct, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := direct.Exec(`DROP TABLE knowledge_fts`); err != nil {
		direct.Close()
		t.Fatal(err)
	}
	if err := direct.Close(); err != nil {
		t.Fatal(err)
	}
	reconciler := New(database)
	reconciler.Start(context.Background())
	defer reconciler.Close()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		metadata, err := database.KnowledgeIndexMetadata(context.Background())
		if err == nil && metadata.ActiveGeneration > active && metadata.NextGeneration == 0 && metadata.Status == store.KnowledgeIndexReady {
			available, err := database.KnowledgeFTSAvailable(context.Background())
			if err != nil || !available {
				t.Fatalf("repaired FTS available = %t, %v", available, err)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	metadata, err := database.KnowledgeIndexMetadata(context.Background())
	t.Fatalf("corrupt active index was not rebuilt: %+v, %v", metadata, err)
}

func TestStartRepairsMissingActiveDerivedRowsWithoutDirtySources(t *testing.T) {
	for _, table := range []string{"knowledge_projection_rows", "knowledge_fts"} {
		t.Run(table, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.db")
			database, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			active := createReadyMessageGeneration(t, database)
			direct, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := direct.Exec(`DELETE FROM knowledge_dirty_sources`); err != nil {
				direct.Close()
				t.Fatal(err)
			}
			if _, err := direct.Exec(`DELETE FROM `+table+` WHERE generation = ?`, active); err != nil {
				direct.Close()
				t.Fatal(err)
			}
			if err := direct.Close(); err != nil {
				t.Fatal(err)
			}
			reconciler := New(database)
			reconciler.Start(context.Background())
			defer reconciler.Close()
			waitForGenerationAfter(t, database, active)
		})
	}
}

func TestStartRepairsCorruptIndexWithoutActiveGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	direct, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := direct.Exec(`DROP TABLE knowledge_fts`); err != nil {
		direct.Close()
		t.Fatal(err)
	}
	if err := direct.Close(); err != nil {
		t.Fatal(err)
	}
	reconciler := New(database)
	reconciler.Start(context.Background())
	defer reconciler.Close()
	metadata := waitForReadyGeneration(t, database)
	available, err := database.KnowledgeFTSAvailable(context.Background())
	if err != nil || !available || metadata.ActiveGeneration == 0 {
		t.Fatalf("corrupt index without active generation = %+v available=%t err=%v", metadata, available, err)
	}
}

func TestCloseAllowsImmediateStoreReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reconciler := New(database)
	reconciler.Start(context.Background())
	deadline := time.Now().Add(time.Second)
	ready := false
	for time.Now().Before(deadline) {
		metadata, err := database.KnowledgeIndexMetadata(context.Background())
		if err == nil && metadata.ActiveGeneration != 0 && metadata.Status == store.KnowledgeIndexReady {
			ready = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !ready {
		reconciler.Close()
		database.Close()
		t.Fatal("reconciler did not reach ready before Close")
	}
	reconciler.Close()
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
}

func createReadyGeneration(t *testing.T, database *store.Store) uint64 {
	t.Helper()
	pending, err := database.StartKnowledgeRebuild(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.BuildKnowledgeGenerationSnapshot(context.Background(), pending.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), pending.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateKnowledgeGeneration(context.Background(), pending.NextGeneration); err != nil {
		t.Fatal(err)
	}
	return pending.NextGeneration
}

func createReadyMessageGeneration(t *testing.T, database *store.Store) uint64 {
	t.Helper()
	bootstrap, err := database.EnsureAuthority(context.Background(), "knowledge-test-credential", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	space, err := database.CreateGroup(context.Background(), store.CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "knowledge health", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SendMessage(context.Background(), store.SendMessageParams{
		RequestID: uuid.NewString(), Actor: owner, Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: space.ID}, Body: "knowledge health source", Now: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	return createReadyGeneration(t, database)
}

func waitForReadyGeneration(t *testing.T, database *store.Store) store.KnowledgeIndexMetadata {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		metadata, err := database.KnowledgeIndexMetadata(context.Background())
		if err == nil && metadata.ActiveGeneration != 0 && metadata.NextGeneration == 0 && metadata.Status == store.KnowledgeIndexReady {
			return metadata
		}
		time.Sleep(time.Millisecond)
	}
	metadata, err := database.KnowledgeIndexMetadata(context.Background())
	t.Fatalf("knowledge generation was not ready: %+v, %v", metadata, err)
	return store.KnowledgeIndexMetadata{}
}

func waitForGenerationAfter(t *testing.T, database *store.Store, generation uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		metadata, err := database.KnowledgeIndexMetadata(context.Background())
		if err == nil && metadata.ActiveGeneration > generation && metadata.NextGeneration == 0 && metadata.Status == store.KnowledgeIndexReady {
			return
		}
		time.Sleep(time.Millisecond)
	}
	metadata, err := database.KnowledgeIndexMetadata(context.Background())
	t.Fatalf("knowledge generation did not advance after corruption: %+v, %v", metadata, err)
}

func TestReconcileNextDiscardsUnrecoverableSnapshot(t *testing.T) {
	database := &reconcilerStore{
		progress: store.KnowledgeGenerationProgress{Generation: 7},
		buildErr: errors.New("snapshot failed"),
	}
	reconciler := &Reconciler{store: database}
	if !reconciler.reconcileNext(context.Background(), store.KnowledgeIndexMetadata{ActiveGeneration: 3, NextGeneration: 7}) {
		t.Fatal("reconcile snapshot failure did not discard next generation")
	}
	if want := []string{"build", "discard"}; !reflect.DeepEqual(database.calls, want) {
		t.Fatalf("calls = %v, want %v", database.calls, want)
	}
}

func TestProjectAcknowledgesMissingSource(t *testing.T) {
	revision := [32]byte{1}
	dirty := store.KnowledgeDirtySource{Sequence: 4, Source: store.KnowledgeSource{Kind: store.KnowledgeSourceMessage, ID: "source"}, Revision: revision}
	database := &reconcilerStore{dirty: dirty, found: true}
	reconciler := &Reconciler{store: database}
	if !reconciler.project(context.Background(), 3) {
		t.Fatal("missing source was not acknowledged")
	}
	if database.appliedGeneration != 3 || database.appliedSequence != 4 || database.applied.Source != dirty.Source || database.applied.Revision != revision {
		t.Fatalf("applied projection = %+v generation=%d sequence=%d", database.applied, database.appliedGeneration, database.appliedSequence)
	}
}

func TestReconcileNextDoesNotDiscardOnActivationWindow(t *testing.T) {
	database := &reconcilerStore{
		progress:    store.KnowledgeGenerationProgress{Generation: 7, SnapshotHighWater: 2, AppliedSequence: 2},
		activateErr: errors.New("new dirty source"),
	}
	reconciler := &Reconciler{store: database}
	if reconciler.reconcileNext(context.Background(), store.KnowledgeIndexMetadata{ActiveGeneration: 3, NextGeneration: 7}) {
		t.Fatal("activation window reported progress")
	}
	if want := []string{"complete", "activate"}; !reflect.DeepEqual(database.calls, want) {
		t.Fatalf("calls = %v, want %v", database.calls, want)
	}
}

func TestCloseWaitsForCurrentPoll(t *testing.T) {
	database := &reconcilerStore{metadataStarted: make(chan struct{}), metadataRelease: make(chan struct{})}
	reconciler := &Reconciler{store: database, now: time.Now, wake: time.Hour, stepTimeout: time.Second}
	reconciler.Start(context.Background())
	select {
	case <-database.metadataStarted:
	case <-time.After(time.Second):
		t.Fatal("reconciler did not begin poll")
	}
	closed := make(chan struct{})
	go func() {
		reconciler.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned before current poll finished")
	case <-time.After(10 * time.Millisecond):
	}
	close(database.metadataRelease)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not wait for current poll")
	}
}

type reconcilerStore struct {
	mu sync.Mutex

	metadata store.KnowledgeIndexMetadata
	progress store.KnowledgeGenerationProgress
	dirty    store.KnowledgeDirtySource
	found    bool

	buildErr    error
	activateErr error
	calls       []string

	appliedGeneration uint64
	appliedSequence   uint64
	applied           store.KnowledgeSourceDocument

	metadataStarted chan struct{}
	metadataRelease chan struct{}
	metadataOnce    sync.Once
}

func (s *reconcilerStore) KnowledgeIndexMetadata(context.Context) (store.KnowledgeIndexMetadata, error) {
	if s.metadataStarted != nil {
		s.metadataOnce.Do(func() {
			close(s.metadataStarted)
			<-s.metadataRelease
		})
	}
	return s.metadata, nil
}

func (s *reconcilerStore) StartKnowledgeRebuild(context.Context, time.Time) (store.KnowledgeIndexMetadata, error) {
	s.record("start")
	return store.KnowledgeIndexMetadata{}, nil
}

func (s *reconcilerStore) KnowledgeGenerationProgress(context.Context, uint64) (store.KnowledgeGenerationProgress, error) {
	return s.progress, nil
}

func (s *reconcilerStore) BuildKnowledgeGenerationSnapshot(context.Context, uint64) error {
	s.record("build")
	return s.buildErr
}

func (s *reconcilerStore) NextKnowledgeDirtySource(context.Context, uint64) (store.KnowledgeDirtySource, bool, error) {
	return s.dirty, s.found, nil
}

func (s *reconcilerStore) ReadKnowledgeSourceDocument(context.Context, store.KnowledgeSource) (store.KnowledgeSourceDocument, bool, error) {
	return store.KnowledgeSourceDocument{}, false, nil
}

func (s *reconcilerStore) ApplyKnowledgeProjection(_ context.Context, generation, sequence uint64, document store.KnowledgeSourceDocument) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appliedGeneration = generation
	s.appliedSequence = sequence
	s.applied = document
	return true, nil
}

func (s *reconcilerStore) CompleteKnowledgeGeneration(context.Context, uint64) error {
	s.record("complete")
	return nil
}

func (s *reconcilerStore) ActivateKnowledgeGeneration(context.Context, uint64) (store.KnowledgeIndexMetadata, error) {
	s.record("activate")
	return store.KnowledgeIndexMetadata{}, s.activateErr
}

func (s *reconcilerStore) DiscardKnowledgeGeneration(context.Context, uint64) (store.KnowledgeIndexMetadata, error) {
	s.record("discard")
	return store.KnowledgeIndexMetadata{}, nil
}

func (s *reconcilerStore) CheckKnowledgeIndexHealth(context.Context) (store.KnowledgeIndexHealth, error) {
	return store.KnowledgeIndexHealthy, nil
}

func (s *reconcilerStore) RepairKnowledgeFTS(context.Context) error {
	s.record("repair")
	return nil
}

func (s *reconcilerStore) record(call string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}
