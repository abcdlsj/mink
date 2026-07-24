package knowledge

import (
	"context"
	"fmt"
	"sync"
	"time"

	knowledgeapp "github.com/abcdlsj/sumi/internal/knowledge/application"
	"github.com/abcdlsj/sumi/internal/observability"
)

type Reconciler struct {
	store        knowledgeStore
	now          func() time.Time
	wake         time.Duration
	healthWake   time.Duration
	stepTimeout  time.Duration
	backoffMax   time.Duration
	backoff      time.Duration
	lastHealth   time.Time
	failed       bool
	failures     uint
	failureErr   error
	failureEvent string
	lastFailure  string
	logger       *observability.Logger

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

type knowledgeStore interface {
	KnowledgeIndexState(context.Context) (knowledgeapp.IndexState, error)
	RebuildKnowledgeIndex(context.Context) error
	NextKnowledgeDirtySource(context.Context) (knowledgeapp.DirtySource, bool, error)
	ReadKnowledgeSourceDocument(context.Context, knowledgeapp.Source) (knowledgeapp.SourceDocument, bool, error)
	ApplyKnowledgeProjection(context.Context, uint64, knowledgeapp.SourceDocument) (bool, error)
	CheckKnowledgeIndexHealth(context.Context) (knowledgeapp.IndexHealth, error)
}

func New(database knowledgeStore) *Reconciler {
	return NewWithLogger(database, nil)
}

func NewWithLogger(database knowledgeStore, logger *observability.Logger) *Reconciler {
	return &Reconciler{
		store: database, now: time.Now, wake: 100 * time.Millisecond, healthWake: time.Minute,
		stepTimeout: 5 * time.Second, backoffMax: 5 * time.Second,
		logger: observability.CategoryLogger(logger, observability.ComponentServer, observability.CategoryKnowledge),
	}
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
			if r.failures > 0 {
				r.logger.Info("knowledge reconciler recovered", "event", "knowledge.reconciler.recovered", "failed_attempts", r.failures)
			}
			r.failures = 0
			r.lastFailure = ""
			r.backoff = 0
			continue
		}
		delay := r.wake
		if r.failed {
			r.failures++
			previousBackoff := r.backoff
			delay = r.nextBackoff()
			failure := fmt.Sprint(r.failureErr)
			fields := []any{"event", r.failureEvent, "err", r.failureErr, "attempt", r.failures, "retry_in", delay}
			if r.failures == 1 || delay != previousBackoff || failure != r.lastFailure {
				r.logger.Warn("knowledge reconciliation failed; retry scheduled", fields...)
			} else {
				r.logger.Debug("knowledge reconciliation remains unavailable", fields...)
			}
			r.lastFailure = failure
		} else {
			if r.failures > 0 {
				r.logger.Info("knowledge reconciler recovered", "event", "knowledge.reconciler.recovered", "failed_attempts", r.failures)
			}
			r.failures = 0
			r.lastFailure = ""
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
	if r.logger == nil {
		r.logger = observability.CategoryLogger(nil, observability.ComponentServer, observability.CategoryKnowledge)
	}
	r.failed = false
	r.failureErr = nil
	r.failureEvent = ""
	state, err := r.store.KnowledgeIndexState(ctx)
	if err != nil {
		return r.fail("knowledge.state.read.failed", err)
	}
	if state.Status != knowledgeapp.IndexReady {
		r.logger.Warn("knowledge index degraded; rebuilding", "event", "knowledge.index.rebuild.started", "status", state.Status, "applied_sequence", state.AppliedSequence)
		if err := r.store.RebuildKnowledgeIndex(ctx); err != nil {
			return r.fail("knowledge.index.rebuild.failed", err)
		}
		r.lastHealth = r.now()
		r.logger.Info("knowledge index rebuilt", "event", "knowledge.index.rebuild.completed", "reason", "degraded")
		return true
	}
	dirty, found, err := r.store.NextKnowledgeDirtySource(ctx)
	if err != nil {
		return r.fail("knowledge.dirty.read.failed", err)
	}
	if found {
		document, exists, err := r.store.ReadKnowledgeSourceDocument(ctx, dirty.Source)
		if err != nil {
			return r.fail("knowledge.source.read.failed", err)
		}
		if !exists {
			document.Source = dirty.Source
			document.Revision = dirty.Revision
		}
		_, err = r.store.ApplyKnowledgeProjection(ctx, dirty.Sequence, document)
		if err != nil {
			return r.fail("knowledge.projection.apply.failed", err)
		}
		r.logger.Debug("knowledge projection applied", "event", "knowledge.projection.applied", "sequence", dirty.Sequence, "source_kind", dirty.Source.Kind, "source_id", dirty.Source.ID, "source_version", dirty.Source.Version, "source_exists", exists)
		return true
	}
	if !r.lastHealth.IsZero() && r.now().Sub(r.lastHealth) < r.healthWake {
		return false
	}
	health, err := r.store.CheckKnowledgeIndexHealth(ctx)
	if err != nil {
		return r.fail("knowledge.health.check.failed", err)
	}
	if health == knowledgeapp.IndexCorrupt {
		r.logger.Warn("knowledge index corruption detected; rebuilding", "event", "knowledge.index.corrupt.detected")
		if err := r.store.RebuildKnowledgeIndex(ctx); err != nil {
			return r.fail("knowledge.index.rebuild.failed", err)
		}
		r.lastHealth = r.now()
		r.logger.Info("knowledge index rebuilt", "event", "knowledge.index.rebuild.completed", "reason", "corrupt")
		return true
	}
	r.lastHealth = r.now()
	return false
}

func (r *Reconciler) fail(event string, err error) bool {
	r.failed = true
	r.failureEvent = event
	if err == nil {
		err = fmt.Errorf("knowledge reconciliation failed")
	}
	r.failureErr = err
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
