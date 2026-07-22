package knowledge

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/store"
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
}

func (s *reconcilerStore) KnowledgeIndexMetadata(context.Context) (store.KnowledgeIndexMetadata, error) {
	if s.metadataStarted != nil {
		close(s.metadataStarted)
		<-s.metadataRelease
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

func (s *reconcilerStore) record(call string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}
