package knowledge

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/abcdlsj/sumi/internal/store"
)

type Reconciler struct {
	store       knowledgeStore
	now         func() time.Time
	wake        time.Duration
	healthWake  time.Duration
	stepTimeout time.Duration
	lastHealth  time.Time
	startup     bool

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

type knowledgeStore interface {
	KnowledgeIndexMetadata(context.Context) (store.KnowledgeIndexMetadata, error)
	StartKnowledgeRebuild(context.Context, time.Time) (store.KnowledgeIndexMetadata, error)
	KnowledgeGenerationProgress(context.Context, uint64) (store.KnowledgeGenerationProgress, error)
	BuildKnowledgeGenerationSnapshot(context.Context, uint64) error
	NextKnowledgeDirtySource(context.Context, uint64) (store.KnowledgeDirtySource, bool, error)
	ReadKnowledgeSourceDocument(context.Context, store.KnowledgeSource) (store.KnowledgeSourceDocument, bool, error)
	ApplyKnowledgeProjection(context.Context, uint64, uint64, store.KnowledgeSourceDocument) (bool, error)
	CompleteKnowledgeGeneration(context.Context, uint64) error
	ActivateKnowledgeGeneration(context.Context, uint64) (store.KnowledgeIndexMetadata, error)
	DiscardKnowledgeGeneration(context.Context, uint64) (store.KnowledgeIndexMetadata, error)
	CheckKnowledgeIndexHealth(context.Context) (store.KnowledgeIndexHealth, error)
	RepairKnowledgeFTS(context.Context) error
}

func New(database *store.Store) *Reconciler {
	return &Reconciler{store: database, now: time.Now, wake: 100 * time.Millisecond, healthWake: time.Minute, stepTimeout: 5 * time.Second}
}

func (r *Reconciler) Start(ctx context.Context) {
	r.once.Do(func() {
		ctx, r.cancel = context.WithCancel(ctx)
		r.done = make(chan struct{})
		r.startup = true
		go func() {
			defer close(r.done)
			r.run(ctx)
		}()
	})
}

func (r *Reconciler) Close() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	<-r.done
}

func (r *Reconciler) run(ctx context.Context) {
	ticker := time.NewTicker(r.wake)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		stepContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.stepTimeout)
		progressed := r.step(stepContext)
		cancel()
		if progressed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Reconciler) step(ctx context.Context) bool {
	metadata, err := r.store.KnowledgeIndexMetadata(ctx)
	if err != nil {
		return false
	}
	if r.startup {
		r.startup = false
		if metadata.NextGeneration != 0 {
			return r.discard(ctx, metadata.NextGeneration)
		}
	}
	if r.healthDue() {
		if !r.health(ctx) {
			return false
		}
		metadata, err = r.store.KnowledgeIndexMetadata(ctx)
		if err != nil {
			return false
		}
	}
	if metadata.NextGeneration != 0 {
		return r.reconcileNext(ctx, metadata)
	}
	if metadata.ActiveGeneration != 0 {
		return r.project(ctx, metadata.ActiveGeneration)
	}
	if _, err := r.store.StartKnowledgeRebuild(ctx, r.now()); err != nil {
		return false
	}
	return true
}

func (r *Reconciler) reconcileNext(ctx context.Context, metadata store.KnowledgeIndexMetadata) bool {
	progress, err := r.store.KnowledgeGenerationProgress(ctx, metadata.NextGeneration)
	if err != nil {
		return r.discard(ctx, metadata.NextGeneration)
	}
	if progress.SnapshotHighWater == 0 {
		if err := r.store.BuildKnowledgeGenerationSnapshot(ctx, metadata.NextGeneration); err != nil {
			return r.discard(ctx, metadata.NextGeneration)
		}
		if err := r.store.CompleteKnowledgeGeneration(ctx, metadata.NextGeneration); err != nil {
			return false
		}
		if _, err := r.store.ActivateKnowledgeGeneration(ctx, metadata.NextGeneration); err != nil {
			return false
		}
		return true
	}
	if r.project(ctx, metadata.NextGeneration) {
		return true
	}
	if err := r.store.CompleteKnowledgeGeneration(ctx, metadata.NextGeneration); err != nil {
		return false
	}
	if r.project(ctx, metadata.NextGeneration) {
		return true
	}
	if _, err := r.store.ActivateKnowledgeGeneration(ctx, metadata.NextGeneration); err != nil {
		return false
	}
	return true
}

func (r *Reconciler) project(ctx context.Context, generation uint64) bool {
	dirty, found, err := r.store.NextKnowledgeDirtySource(ctx, generation)
	if err != nil || !found {
		return false
	}
	document, exists, err := r.store.ReadKnowledgeSourceDocument(ctx, dirty.Source)
	if err != nil && !errors.Is(err, context.Canceled) {
		return false
	}
	if !exists {
		document.Source = dirty.Source
		document.Revision = dirty.Revision
	}
	_, err = r.store.ApplyKnowledgeProjection(ctx, generation, dirty.Sequence, document)
	if err != nil {
		r.health(ctx)
	}
	return err == nil
}

func (r *Reconciler) discard(ctx context.Context, generation uint64) bool {
	_, err := r.store.DiscardKnowledgeGeneration(ctx, generation)
	return err == nil
}

func (r *Reconciler) healthDue() bool {
	return r.lastHealth.IsZero() || r.now().Sub(r.lastHealth) >= r.healthWake
}

func (r *Reconciler) health(ctx context.Context) bool {
	r.lastHealth = r.now()
	health, err := r.store.CheckKnowledgeIndexHealth(ctx)
	if err != nil {
		return true
	}
	if health != store.KnowledgeIndexCorrupt {
		return true
	}
	return r.store.RepairKnowledgeFTS(ctx) == nil
}
