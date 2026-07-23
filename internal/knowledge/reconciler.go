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
	backoff     time.Duration
	lastHealth  time.Time
	failed      bool

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

type knowledgeStore interface {
	KnowledgeIndexState(context.Context) (store.KnowledgeIndexState, error)
	RebuildKnowledgeIndex(context.Context) error
	NextKnowledgeDirtySource(context.Context) (store.KnowledgeDirtySource, bool, error)
	ReadKnowledgeSourceDocument(context.Context, store.KnowledgeSource) (store.KnowledgeSourceDocument, bool, error)
	ApplyKnowledgeProjection(context.Context, uint64, store.KnowledgeSourceDocument) (bool, error)
	CheckKnowledgeIndexHealth(context.Context) (store.KnowledgeIndexHealth, error)
}

func New(database *store.Store) *Reconciler {
	return &Reconciler{store: database, now: time.Now, wake: 100 * time.Millisecond, healthWake: time.Minute, stepTimeout: 5 * time.Second, backoffMax: 5 * time.Second}
}

func (r *Reconciler) Start(ctx context.Context) {
	r.once.Do(func() {
		ctx, r.cancel = context.WithCancel(ctx)
		r.done = make(chan struct{})
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
	for ctx.Err() == nil {
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
	state, err := r.store.KnowledgeIndexState(ctx)
	if err != nil {
		return r.fail()
	}
	if state.Status != store.KnowledgeIndexReady {
		if r.store.RebuildKnowledgeIndex(ctx) != nil {
			return r.fail()
		}
		r.lastHealth = r.now()
		return true
	}
	dirty, found, err := r.store.NextKnowledgeDirtySource(ctx)
	if err != nil {
		return r.fail()
	}
	if found {
		document, exists, err := r.store.ReadKnowledgeSourceDocument(ctx, dirty.Source)
		if err != nil {
			return r.fail()
		}
		if !exists {
			document.Source = dirty.Source
			document.Revision = dirty.Revision
		}
		_, err = r.store.ApplyKnowledgeProjection(ctx, dirty.Sequence, document)
		if err != nil {
			return r.fail()
		}
		return true
	}
	if !r.lastHealth.IsZero() && r.now().Sub(r.lastHealth) < r.healthWake {
		return false
	}
	health, err := r.store.CheckKnowledgeIndexHealth(ctx)
	if err != nil {
		return r.fail()
	}
	if health == store.KnowledgeIndexCorrupt {
		if r.store.RebuildKnowledgeIndex(ctx) != nil {
			return r.fail()
		}
		r.lastHealth = r.now()
		return true
	}
	r.lastHealth = r.now()
	return false
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
