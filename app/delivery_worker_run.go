package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/sumi/delivery"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/space"
)

// run executes one claimed Delivery end-to-end under its fence. The sequence is
// the durable ordering Iris froze:
//
//  1. re-read the origin user message from the Space (facts are the source of
//     truth; the volatile job payload is gone after a restart),
//  2. ensure the single assistant placeholder for this DeliveryID and fence-bind
//     its stable MessageID onto the Delivery (crash between append and bind is
//     recovered because EnsureDeliveryPlaceholder is idempotent by DeliveryID),
//  3. start a parallel lease-renew guard so a long turn keeps its lane,
//  4. run the turn through the shared pipeline, finalizing INTO the placeholder,
//  5. Complete or Fail the Delivery under the fence.
//
// Any stale-fence signal (a renew/bind that finds the lease was reclaimed) stops
// the worker from writing further Space state for this attempt: the new owner
// owns finalization.
func (w *deliveryWorker) run(ctx context.Context, d *delivery.Delivery, fence delivery.Fence) {
	a := w.app
	if a == nil || d == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if d.Kind == delivery.KindAsyncDelegate {
		w.runAsyncDelegate(ctx, d, fence)
		return
	}
	deliveries := a.store.Deliveries()

	origin, ok := a.loadOriginMessage(d.SpaceID, d.OriginMessageID)
	if !ok {
		// The origin fact is gone (space/message deleted or Space corrupted). There
		// is nothing to reply to; fail visibly so the Delivery leaves the claimable
		// set. But if a PRIOR attempt already bound a visible placeholder (this is a
		// reclaim after the origin vanished), that placeholder is still "pending" on
		// disk — on a headless server it is the sole durable projection, so it must
		// be flipped to failed under the live lease FIRST, then the Delivery Failed
		// (which clears the lease, so it must come second). With no bound
		// placeholder, only the Delivery is failed.
		now := w.now()
		if id := strings.TrimSpace(d.ResultMessageID); id != "" {
			if _, ferr := a.spaces.FailDeliveryMessage(d.SpaceID, id, d.ID, fence.OwnerID, fence.Version, now, "origin message not found"); ferr != nil {
				return
			}
		}
		_, _ = deliveries.Fail(d.ID, fence, "origin message not found", now)
		return
	}

	personas := a.fuzzyPersonaResolver()
	placeholder, _, err := a.spaces.EnsureDeliveryPlaceholder(d.SpaceID, d.ID, d.AgentID, d.ParentMessageID, personas.Info)
	if err != nil {
		_, _ = deliveries.Fail(d.ID, fence, "ensure placeholder: "+err.Error(), w.now())
		return
	}
	bound, _, err := deliveries.BindResultMessage(d.ID, fence, placeholder.ID, w.now())
	if err != nil {
		// Stale fence or result conflict: another owner won this attempt. Do NOT
		// write Space state; leave the placeholder for the live owner to finalize.
		return
	}
	resultMessageID := bound.ResultMessageID

	// Parallel lease guard: renews on a ticker while the turn runs, and flips a
	// shared stale flag the moment a renew is fenced out. The turn goroutine reads
	// that flag before writing any Space state.
	guard := &leaseGuard{}
	renewCtx, stopRenew := context.WithCancel(ctx)
	var renewWG sync.WaitGroup
	renewWG.Add(1)
	go func() {
		defer renewWG.Done()
		w.renewLoop(renewCtx, d.ID, fence, guard)
	}()

	source := a.deliverySource(d)
	target := w.routingTargetFor(d, origin)
	binding := &wakeBinding{
		deliveryID:      d.ID,
		resultMessageID: resultMessageID,
		fence:           fence,
		guard:           guard,
		now:             w.now,
	}
	result := a.channelWakePipeline().runChannelWakeBound(
		ctx,
		source,
		d.SpaceID,
		target,
		origin.Content,
		cloneAttachments(origin.Attachments),
		binding,
	)

	stopRenew()
	renewWG.Wait()

	if len(result.notices) > 0 {
		a.publishRoutingNotices(source, result.notices)
	}

	if guard.stale() {
		// A renew was fenced out mid-turn: the lease was reclaimed and a newer
		// owner is authoritative. Do not finalize over them.
		return
	}
	now := w.now()
	if result.err != nil {
		// A stale-write rejection from finalize is not a turn failure: a newer
		// owner already finalized this placeholder, so this superseded attempt
		// simply lost the race. Do not Fail the Delivery or touch the Space —
		// the newer owner is authoritative.
		if errors.Is(result.err, space.ErrStaleDeliveryWrite) {
			return
		}
		// Real turn failure. Project the failure onto the placeholder FIRST, under
		// the still-live lease: on a headless server (no desktop backend consuming
		// TurnError) the placeholder is the sole durable record of the failure, so
		// leaving it "pending" would surface a forever-spinning bubble after a
		// restart. FailDeliveryMessage is gated on the live-lease fence, so a newer
		// owner that has already reclaimed makes it a no-op (ErrStaleDeliveryWrite)
		// rather than overwriting their reply. Only after the Space projection do we
		// Fail the Delivery — which clears the lease, so it MUST come second (a
		// cleared lease would fail the live-lease gate on the Space write).
		if _, ferr := a.spaces.FailDeliveryMessage(d.SpaceID, resultMessageID, d.ID, fence.OwnerID, fence.Version, now, result.err.Error()); ferr == nil {
			_, _ = deliveries.Fail(d.ID, fence, result.err.Error(), now)
		}
		return
	}
	if _, err := deliveries.Complete(d.ID, fence, resultMessageID, now); err != nil {
		// Complete rejected (stale/terminal). Nothing more to do; a live owner or
		// operator action supersedes this attempt.
		return
	}
}

// leaseGuard is the shared stop-signal between the renew loop and the turn. Once
// a renew is fenced out (ErrStaleLease), the guard is tripped and the turn must
// not write further Space/Delivery state.
type leaseGuard struct {
	mu      sync.Mutex
	tripped bool
}

func (g *leaseGuard) trip() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.tripped = true
	g.mu.Unlock()
}

func (g *leaseGuard) stale() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tripped
}

// renewLoop keeps the lease alive on a ticker until ctx is canceled (turn done)
// or a renew is rejected. A rejected renew trips the guard and stops the loop —
// the worker must not keep writing under a reclaimed lease.
func (w *deliveryWorker) renewLoop(ctx context.Context, id string, fence delivery.Fence, guard *leaseGuard) {
	ticker := time.NewTicker(deliveryRenewEvery)
	defer ticker.Stop()
	deliveries := w.app.store.Deliveries()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := deliveries.Renew(id, fence, w.now(), deliveryLeaseTTL); err != nil {
				guard.trip()
				return
			}
		}
	}
}

func (w *deliveryWorker) routingTargetFor(d *delivery.Delivery, origin space.Message) space.RoutingTarget {
	target := space.RoutingTarget{
		AgentID:         d.AgentID,
		OriginMessageID: d.OriginMessageID,
		Reason:          w.deliveryReason(d, origin),
	}
	// A parent message means this wake lives in a chain/thread. The chain's
	// remaining budget is recovered from persisted intents at fanout time (in
	// buildIntents), so the in-memory RoutingChain here only needs to carry the
	// root/parent identity for the collaboration brief and reply routing.
	if strings.TrimSpace(d.ParentMessageID) != "" {
		chainRoot := w.chainRootFor(origin, d)
		chain := space.NewRoutingChain(chainRoot, d.SpaceID, space.DefaultRoutingBudget)
		chain.ParentMessageID = d.ParentMessageID
		target.Chain = chain
	}
	return target
}

// chainRootFor recovers the chain root for a routed delivery from the origin
// message's persisted intents (the durable authority), falling back to the
// origin id itself for a first-round wake whose intents name it as the root.
func (w *deliveryWorker) chainRootFor(origin space.Message, d *delivery.Delivery) string {
	for _, it := range origin.RoutingIntents {
		if it.AgentID == d.AgentID && strings.TrimSpace(it.ChainRoot) != "" {
			return it.ChainRoot
		}
	}
	for _, it := range origin.RoutingIntents {
		if strings.TrimSpace(it.ChainRoot) != "" {
			return it.ChainRoot
		}
	}
	return d.OriginMessageID
}

func (w *deliveryWorker) deliveryReason(d *delivery.Delivery, origin space.Message) string {
	for _, it := range origin.RoutingIntents {
		if it.AgentID == d.AgentID && strings.TrimSpace(it.Reason) != "" {
			return it.Reason
		}
	}
	return ""
}

func (a *App) loadOriginMessage(spaceID, messageID string) (space.Message, bool) {
	if a == nil || a.spaces == nil {
		return space.Message{}, false
	}
	sp, err := a.spaces.LoadSpace(spaceID)
	if err != nil || sp == nil {
		return space.Message{}, false
	}
	for _, m := range sp.Messages {
		if m.ID == messageID {
			return m, true
		}
	}
	return space.Message{}, false
}

// deliverySource reconstructs the router source string for a delivery's Space so
// the wake resolves the same session/router target a live desktop message would.
// The volatile job payload (which carried the original source) is gone after a
// restart, so the source is rebuilt from the durable Space Kind+Key — the same
// shape MapSource/SourceUsesRouter recognize and Resolve round-trips back to this
// Space. Falls back to the space id if the Space cannot be loaded.
func (a *App) deliverySource(d *delivery.Delivery) string {
	if a == nil || d == nil {
		return ""
	}
	sp, err := a.spaces.LoadSpace(d.SpaceID)
	if err != nil || sp == nil {
		return "desktop:channel:" + d.SpaceID
	}
	return routerSourceForSpace(sp)
}

// routerSourceForSpace maps a Space back to the router-recognized source prefix
// for its kind. Only channel/thread wakes are routed (commit 2 scope), so the
// channel form is the primary path; the others are reconstructed for
// completeness so a mis-kinded delivery still resolves rather than silently
// mapping to the wrong space.
func routerSourceForSpace(sp *space.Space) string {
	key := strings.TrimSpace(sp.Key)
	if key == "" {
		key = sp.ID
	}
	switch sp.Kind {
	case space.KindChannel:
		return "desktop:channel:" + key
	case space.KindAgentDM:
		return "desktop:agent:" + key
	case space.KindDirectChat:
		return "desktop:direct:" + key
	default:
		return "desktop:channel:" + key
	}
}

func cloneAttachments(in []msg.Attachment) []msg.Attachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]msg.Attachment, len(in))
	copy(out, in)
	return out
}
