package app

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/sumi/delivery"
)

const (
	// deliveryLeaseTTL is how long a claim's lease is valid before it may be
	// reclaimed by another worker. deliveryRenewEvery must be comfortably shorter
	// so an in-progress turn keeps its lease alive across a long model call.
	deliveryLeaseTTL   = 30 * time.Second
	deliveryRenewEvery = 10 * time.Second
	// workerFallbackScan bounds how long a claimable lane can sit unserved if a
	// hint was missed (e.g. lost on a crash before the worker started). It is a
	// safety net, not the primary trigger — enqueue/retry hint the worker directly.
	workerFallbackScan = 30 * time.Second
)

// wakeBinding threads a durable Delivery's identity into the shared channel-wake
// pipeline. When non-nil, the pipeline finalizes the agent reply INTO the
// pre-created assistant placeholder (resultMessageID) under the delivery fence,
// emits routed turn events carrying DeliveryID+MessageID so the desktop backend
// binds rather than appends, and never appends a second visible message. When
// nil, the pipeline keeps its original append-a-new-message behavior for the
// direct/test synchronous callers.
type wakeBinding struct {
	deliveryID      string
	resultMessageID string
	fence           delivery.Fence
	guard           *leaseGuard
	// now is the worker's clock, used at finalize time to evaluate live-lease
	// authority against the same clock the lease/renew path uses (fake clock in
	// tests). Nil defaults to time.Now.
	now func() time.Time
}

// deliveryWorker is the single process-level manager goroutine that drains
// claimable delivery lanes. It replaces the volatile per-lane goroutine/channel
// queues: routing intent and delivery records are durable, so a restart resumes
// execution from the store rather than losing in-flight wakes.
//
// The manager goroutine only CLAIMS (cheap, store-atomic) and dispatches; each
// claimed delivery runs its turn on its own runner goroutine, so a slow model
// call in one lane never blocks claiming in another. ClaimNextInLane makes a lane
// busy for the whole turn, so at most one runner per lane is ever live.
type deliveryWorker struct {
	app     *App
	ownerID string

	hint   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	now func() time.Time
}

func newDeliveryWorker(a *App, ownerID string) *deliveryWorker {
	return &deliveryWorker{
		app:     a,
		ownerID: strings.TrimSpace(ownerID),
		hint:    make(chan struct{}, 1),
		now:     time.Now,
	}
}

func (w *deliveryWorker) start(ctx context.Context) {
	if w == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.ctx, w.cancel = context.WithCancel(ctx)
	w.wg.Add(1)
	go w.loop()
}

func (w *deliveryWorker) stop() {
	if w == nil || w.cancel == nil {
		return
	}
	w.cancel()
	w.wg.Wait()
}

// wake nudges the manager goroutine to sweep now. It is a non-blocking
// coalescing signal: many enqueues collapse into one pending sweep.
func (w *deliveryWorker) wake() {
	if w == nil {
		return
	}
	select {
	case w.hint <- struct{}{}:
	default:
	}
}

func (w *deliveryWorker) loop() {
	defer w.wg.Done()
	ticker := time.NewTicker(workerFallbackScan)
	defer ticker.Stop()
	w.sweep()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-w.hint:
			w.sweep()
		case <-ticker.C:
			w.sweep()
		}
	}
}

// sweep discovers claimable lanes and claims the FIFO head of each, dispatching
// every successful claim to a runner goroutine. A lane that is busy (active
// lease elsewhere) or has only terminal/failed members is skipped — the
// discovery primitive already filters those, so this never hot-loops on
// ErrLaneBusy. Errors from a single lane are swallowed so one bad lane does not
// stall the rest; the next hint/tick retries.
func (w *deliveryWorker) sweep() {
	if w == nil || w.app == nil || w.app.store == nil {
		return
	}
	deliveries := w.app.store.Deliveries()
	lanes, err := deliveries.ClaimableLanes(w.now())
	if err != nil {
		return
	}
	for _, lane := range lanes {
		select {
		case <-w.ctx.Done():
			return
		default:
		}
		d, fence, err := deliveries.ClaimNextInLane(lane.SpaceID, lane.ParentMessageID, lane.AgentID, w.ownerID, w.now(), deliveryLeaseTTL)
		if err != nil {
			// ErrLaneBusy / ErrNotClaimable: nothing to do this pass.
			continue
		}
		w.dispatch(d, fence)
	}
}

func (w *deliveryWorker) dispatch(d *delivery.Delivery, fence delivery.Fence) {
	if d == nil {
		return
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.run(d, fence)
		// After a delivery finalizes its lane frees; re-sweep to pick up the next
		// FIFO member or any chained deliveries this turn created.
		w.wake()
	}()
}
