package app

import (
	"context"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/delivery"
	"github.com/abcdlsj/sumi/space"
)

// reconcileDeliveries rebuilds durable deliveries from the persisted routing
// facts at startup. It is the recovery half of the durability contract: the
// volatile wake goroutines/channels are gone after a restart, but every intent a
// message committed still lives in the Space file, so we re-derive the pending
// deliveries from those intents alone — never from the current mentions /
// listening / config, which may have changed since.
//
// Only channel/thread wakes are materialized here (commit 2 scope). async
// delegate deliveries (Task.ExecutionIntent) are deferred to commit 3.
// CreateIfAbsent is idempotent by StableKey, so an intent whose delivery already
// exists (still pending, or already completed/failed) is a no-op — reconcile
// never resurrects a terminal delivery or double-creates a live one.
func (a *App) reconcileDeliveries(ctx context.Context) error {
	if a == nil || a.spaces == nil || a.store == nil {
		return nil
	}
	spaces, err := a.spaces.ListSpaces()
	if err != nil {
		return err
	}
	deliveries := a.store.Deliveries()
	now := time.Now()
	for _, sp := range spaces {
		if sp == nil {
			continue
		}
		for _, m := range sp.Messages {
			if ctx != nil {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
			}
			for _, it := range m.RoutingIntents {
				d := deliveryFromIntent(sp.ID, m, it)
				if d == nil {
					continue
				}
				if _, _, err := deliveries.CreateIfAbsent(d, now); err != nil {
					// A single malformed intent must not abort recovery of the rest;
					// its delivery simply stays unmaterialized until the next reconcile.
					continue
				}
			}
		}
	}
	return nil
}

// createContinuationDeliveries materializes the downstream deliveries named by a
// just-finalized reply's persisted routing intents. It runs AFTER
// FinalizeDeliveryMessage has committed those intents, so a crash between the
// reply commit and this create is recovered by reconcileDeliveries reading the
// same intents. The worker is nudged once so a newly claimable lane is picked up
// promptly instead of waiting for the fallback scan.
func (a *App) createContinuationDeliveries(spaceID string, reply space.Message) []space.RoutingNotice {
	if a == nil || a.store == nil || len(reply.RoutingIntents) == 0 {
		return nil
	}
	deliveries := a.store.Deliveries()
	now := time.Now()
	created := false
	for _, it := range reply.RoutingIntents {
		d := deliveryFromIntent(spaceID, reply, it)
		if d == nil {
			continue
		}
		if _, fresh, err := deliveries.CreateIfAbsent(d, now); err == nil && fresh {
			created = true
		}
	}
	if created && a.worker != nil {
		a.worker.wake()
	}
	return nil
}

// deliveryFromIntent maps one persisted RoutingIntent on an origin message to the
// Delivery that should execute it. The origin message is the delivery's
// OriginMessageID; the intent's parent threads it; async_delegate is skipped
// (commit 3). Returns nil for intents outside commit 2 scope or missing an agent.
func deliveryFromIntent(spaceID string, origin space.Message, it space.RoutingIntent) *delivery.Delivery {
	agentID := strings.TrimSpace(it.AgentID)
	if agentID == "" || strings.TrimSpace(spaceID) == "" || strings.TrimSpace(origin.ID) == "" {
		return nil
	}
	parent := strings.TrimSpace(it.ParentMessageID)
	kind := delivery.KindChannelWake
	if parent != "" {
		kind = delivery.KindThreadWake
	}
	return &delivery.Delivery{
		Kind:            kind,
		SpaceID:         strings.TrimSpace(spaceID),
		ParentMessageID: parent,
		OriginMessageID: strings.TrimSpace(origin.ID),
		AgentID:         agentID,
	}
}
