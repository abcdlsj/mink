package knowledge

import (
	"context"
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
	backoffMax  time.Duration
	lastHealth  time.Time
	backoff     time.Duration
	startup     bool
	failed      bool

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
	return &Reconciler{store: database, now: time.Now, wake: 100 * time.Millisecond, healthWake: time.Minute, stepTimeout: 5 * time.Second, backoffMax: 5 * time.Second}
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
	for {
		if ctx.Err() != nil {
			return
		}
		stepContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.stepTimeout)
		progressed := r.step(stepContext)
		cancel()
		if progressed {
			r.backoff = 0
			continue
		}
		delay := r.wake
		if r.failed {
			delay = r.nextBackoff()
		} else {
			r.backoff = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}

func (r *Reconciler) step(ctx context.Context) bool {
	r.failed = false
	metadata, err := r.store.KnowledgeIndexMetadata(ctx)
	if err != nil {
		return r.fail()
	}
	if r.startup {
		if metadata.NextGeneration != 0 {
			if !r.discard(ctx, metadata.NextGeneration) {
				return false
			}
			r.startup = false
			return true
		}
		r.startup = false
	}
	if r.healthDue() {
		if !r.health(ctx) {
			return false
		}
		metadata, err = r.store.KnowledgeIndexMetadata(ctx)
		if err != nil {
			return r.fail()
		}
	}
	if metadata.NextGeneration != 0 {
		return r.reconcileNext(ctx, metadata)
	}
	if metadata.ActiveGeneration != 0 {
		return r.project(ctx, metadata.ActiveGeneration)
	}
	if _, err := r.store.StartKnowledgeRebuild(ctx, r.now()); err != nil {
		return r.fail()
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
			return r.fail()
		}
		if _, err := r.store.ActivateKnowledgeGeneration(ctx, metadata.NextGeneration); err != nil {
			return r.fail()
		}
		return true
	}
	if r.project(ctx, metadata.NextGeneration) {
		return true
	}
	if err := r.store.CompleteKnowledgeGeneration(ctx, metadata.NextGeneration); err != nil {
		return r.fail()
	}
	if r.project(ctx, metadata.NextGeneration) {
		return true
	}
	if _, err := r.store.ActivateKnowledgeGeneration(ctx, metadata.NextGeneration); err != nil {
		return r.fail()
	}
	return true
}

func (r *Reconciler) project(ctx context.Context, generation uint64) bool {
	dirty, found, err := r.store.NextKnowledgeDirtySource(ctx, generation)
	if err != nil {
		return r.fail()
	}
	if !found {
		return false
	}
	document, exists, err := r.store.ReadKnowledgeSourceDocument(ctx, dirty.Source)
	if err != nil {
		return r.fail()
	}
	if !exists {
		document.Source = dirty.Source
		document.Revision = dirty.Revision
	}
	_, err = r.store.ApplyKnowledgeProjection(ctx, generation, dirty.Sequence, document)
	if err != nil {
		r.health(ctx)
		return r.fail()
	}
	return true
}

func (r *Reconciler) discard(ctx context.Context, generation uint64) bool {
	if _, err := r.store.DiscardKnowledgeGeneration(ctx, generation); err != nil {
		return r.fail()
	}
	return true
}

func (r *Reconciler) healthDue() bool {
	return r.lastHealth.IsZero() || r.now().Sub(r.lastHealth) >= r.healthWake
}

func (r *Reconciler) health(ctx context.Context) bool {
	health, err := r.store.CheckKnowledgeIndexHealth(ctx)
	if err != nil {
		return r.fail()
	}
	if health != store.KnowledgeIndexCorrupt {
		r.lastHealth = r.now()
		return true
	}
	if err := r.store.RepairKnowledgeFTS(ctx); err != nil {
		return r.fail()
	}
	r.lastHealth = r.now()
	return true
}

func (r *Reconciler) fail() bool {
	r.failed = true
	return false
}

func (r *Reconciler) nextBackoff() time.Duration {
	if r.backoff == 0 {
		r.backoff = r.wake
		return r.backoff
	}
	r.backoff *= 2
	if r.backoffMax > 0 && r.backoff > r.backoffMax {
		r.backoff = r.backoffMax
	}
	return r.backoff
}
