package knowledge

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
)

func TestReconcilerBuildsOnceAndDrainsDurableDirtySources(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reconciler := New(db)
	reconciler.wake = time.Millisecond
	reconciler.backoffMax = 8 * time.Millisecond
	reconciler.Start(context.Background())
	defer reconciler.Close()

	waitKnowledgeState(t, db, store.KnowledgeIndexReady)
	entry, err := db.EnqueueKnowledgeDirtySource(context.Background(), store.KnowledgeSource{
		Kind: store.KnowledgeSourceMessage, ID: uuid.NewString(),
	}, [32]byte{1}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, stateErr := db.KnowledgeIndexState(context.Background())
		_, found, dirtyErr := db.NextKnowledgeDirtySource(context.Background())
		if stateErr == nil && dirtyErr == nil && state.AppliedSequence == entry.Sequence && !found {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("dirty source was not drained")
}

func TestStepUsesSingleProjectionLifecycle(t *testing.T) {
	source := store.KnowledgeSource{Kind: store.KnowledgeSourceMessage, ID: uuid.NewString()}
	dirty := store.KnowledgeDirtySource{Sequence: 7, Source: source, Revision: [32]byte{1}}
	document := store.KnowledgeSourceDocument{Source: source, Revision: dirty.Revision, Body: "body"}
	backend := &fakeKnowledgeStore{state: store.KnowledgeIndexState{Status: store.KnowledgeIndexDegraded}}
	reconciler := &Reconciler{store: backend, now: time.Now, wake: time.Millisecond}
	if !reconciler.step(context.Background()) || backend.rebuilds != 1 {
		t.Fatalf("degraded step did not rebuild: %+v", backend)
	}
	backend.state.Status = store.KnowledgeIndexReady
	backend.dirty, backend.document, backend.exists = dirty, document, true
	if !reconciler.step(context.Background()) || backend.applied != dirty.Sequence {
		t.Fatalf("ready step did not apply dirty source: %+v", backend)
	}
	backend.dirty = store.KnowledgeDirtySource{}
	backend.health = store.KnowledgeIndexCorrupt
	if !reconciler.step(context.Background()) || backend.rebuilds != 2 {
		t.Fatalf("corrupt step did not rebuild: %+v", backend)
	}
}

func TestPersistentStateFailuresUseBoundedBackoff(t *testing.T) {
	backend := &fakeKnowledgeStore{stateErr: errors.New("unavailable")}
	reconciler := &Reconciler{store: backend, now: time.Now, wake: time.Millisecond, backoffMax: 8 * time.Millisecond}
	for _, want := range []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond, 8 * time.Millisecond, 8 * time.Millisecond} {
		if reconciler.step(context.Background()) || !reconciler.failed {
			t.Fatal("persistent state failure reported progress or idle")
		}
		if got := reconciler.nextBackoff(); got != want {
			t.Fatalf("backoff = %s, want %s", got, want)
		}
	}
	backend.mu.Lock()
	calls := backend.stateCalls
	backend.mu.Unlock()
	if calls != 5 {
		t.Fatalf("state calls = %d, want 5", calls)
	}
	backend.stateErr = nil
	backend.state.Status = store.KnowledgeIndexReady
	if reconciler.step(context.Background()) || reconciler.failed {
		t.Fatal("healthy idle state was treated as progress or failure")
	}
}

func waitKnowledgeState(t *testing.T, db *store.Store, status string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, err := db.KnowledgeIndexState(context.Background())
		if err == nil && state.Status == status {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("knowledge state did not become %s", status)
}

type fakeKnowledgeStore struct {
	mu         sync.Mutex
	state      store.KnowledgeIndexState
	stateErr   error
	stateCalls int
	rebuilds   int
	dirty      store.KnowledgeDirtySource
	document   store.KnowledgeSourceDocument
	exists     bool
	applied    uint64
	health     store.KnowledgeIndexHealth
}

func (s *fakeKnowledgeStore) KnowledgeIndexState(context.Context) (store.KnowledgeIndexState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateCalls++
	return s.state, s.stateErr
}

func (s *fakeKnowledgeStore) RebuildKnowledgeIndex(context.Context) error {
	s.rebuilds++
	return nil
}

func (s *fakeKnowledgeStore) NextKnowledgeDirtySource(context.Context) (store.KnowledgeDirtySource, bool, error) {
	return s.dirty, s.dirty.Sequence != 0, nil
}

func (s *fakeKnowledgeStore) ReadKnowledgeSourceDocument(context.Context, store.KnowledgeSource) (store.KnowledgeSourceDocument, bool, error) {
	return s.document, s.exists, nil
}

func (s *fakeKnowledgeStore) ApplyKnowledgeProjection(_ context.Context, sequence uint64, _ store.KnowledgeSourceDocument) (bool, error) {
	s.applied = sequence
	s.dirty = store.KnowledgeDirtySource{}
	return true, nil
}

func (s *fakeKnowledgeStore) CheckKnowledgeIndexHealth(context.Context) (store.KnowledgeIndexHealth, error) {
	return s.health, nil
}
