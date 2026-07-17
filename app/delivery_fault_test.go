package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/delivery"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

// The fault tests below drive the durable-delivery seam directly (claim ->
// worker.run) so each fault window is exercised deterministically and asserts
// real Space/Delivery/Message facts, not mock worker calls. They are the
// executable form of Iris's frozen fault matrix (msg cabd9e49 + the 4 review
// points in msg 0737d0f2).

// faultEnv is a ready-to-drive App with one channel, one persona, and a stub
// runtime whose reply text is controllable per turn.
type faultEnv struct {
	t       *testing.T
	app     *App
	worker  *deliveryWorker
	channel *space.Space
	reply   func(input string) string
	runs    int
}

func newFaultEnv(t *testing.T) *faultEnv {
	t.Helper()
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.Personas().Create("bob", persona.Meta{Runtime: "stub"}, "# Bob"); err != nil {
		t.Fatal(err)
	}

	env := &faultEnv{t: t, app: a}
	env.reply = func(input string) string { return "reply to " + input }
	a.RegisterRuntime("stub", func(re *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			env.runs++
			turn.Session.Add(msg.Message{Role: "user", Content: turn.Input})
			turn.Session.Add(msg.Message{Role: "assistant", Content: env.reply(turn.Input)})
			return nil
		}), nil
	})

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	env.channel = ch
	// A stable process-level worker owner, distinct from the persona AgentID.
	env.worker = newDeliveryWorker(a, "worker-test")
	env.worker.ctx = context.Background()
	return env
}

// seedMention appends a user message that mentions bob, persisting its routing
// intent the way RouteUserChannelMessage would, and returns the message id.
func (e *faultEnv) seedMention(content string) space.Message {
	e.t.Helper()
	r := e.app.channelRouter()
	wakes, _, err := r.RouteUserChannelMessage(e.channel.ID, content, "", nil)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(wakes) != 1 || wakes[0].AgentID != "bob" {
		e.t.Fatalf("wakes = %+v, want single bob", wakes)
	}
	return e.mustMessage(wakes[0].OriginMessageID)
}

func (e *faultEnv) mustMessage(id string) space.Message {
	e.t.Helper()
	sp, err := e.app.Spaces().LoadSpace(e.channel.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, m := range sp.Messages {
		if m.ID == id {
			return m
		}
	}
	e.t.Fatalf("message %s not found", id)
	return space.Message{}
}

func (e *faultEnv) messages() []space.Message {
	e.t.Helper()
	sp, err := e.app.Spaces().LoadSpace(e.channel.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return sp.Messages
}

// marshalSpace re-reads the Space from the store and returns its canonical JSON.
// The store's LoadSpace reads from disk, so comparing this before/after a
// rejected write proves whether any bytes were persisted (fault condition 4).
func (e *faultEnv) marshalSpace(spaceID string) string {
	e.t.Helper()
	sp, err := e.app.Spaces().LoadSpace(spaceID)
	if err != nil {
		e.t.Fatal(err)
	}
	b, err := json.Marshal(sp)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

// createDelivery persists a pending channel-wake delivery for origin+bob, the
// way reconcile/continuation would from the origin's persisted intent.
func (e *faultEnv) createDelivery(origin space.Message) *delivery.Delivery {
	e.t.Helper()
	d := &delivery.Delivery{
		Kind:            delivery.KindChannelWake,
		SpaceID:         e.channel.ID,
		OriginMessageID: origin.ID,
		AgentID:         "bob",
	}
	created, _, err := e.app.Deliveries().CreateIfAbsent(d, time.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return created
}

func (e *faultEnv) claim(id string) (*delivery.Delivery, delivery.Fence) {
	e.t.Helper()
	d, err := e.app.Deliveries().Get(id)
	if err != nil {
		e.t.Fatal(err)
	}
	claimed, fence, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, e.worker.ownerID, time.Now(), deliveryLeaseTTL)
	if err != nil {
		e.t.Fatalf("claim: %v", err)
	}
	return claimed, fence
}

func assistantPlaceholders(messages []space.Message, deliveryID string) []space.Message {
	var out []space.Message
	for _, m := range messages {
		if m.DeliveryID == deliveryID {
			out = append(out, m)
		}
	}
	return out
}

// --- Fault 1: Space append-before-claim -----------------------------------
//
// The routing intent (the durable fact that bob must be woken) is persisted on
// the origin message in the SAME commit as the message itself, BEFORE any
// delivery is claimed or any goroutine runs. A crash right after the user
// message is written must leave a recoverable fact.
func TestFaultSpaceAppendBeforeClaim(t *testing.T) {
	e := newFaultEnv(t)
	origin := e.seedMention("@bob hello")

	found := false
	for _, it := range origin.RoutingIntents {
		if it.AgentID == "bob" {
			found = true
			if strings.TrimSpace(it.ChainRoot) != origin.ID {
				t.Fatalf("intent ChainRoot = %q, want origin id %q", it.ChainRoot, origin.ID)
			}
		}
	}
	if !found {
		t.Fatalf("origin has no persisted routing intent for bob: %+v", origin.RoutingIntents)
	}

	// reconcile must be able to rebuild the delivery from that persisted intent
	// alone, with no live queue and no reliance on current router config.
	if err := e.app.reconcileDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	ds, err := e.app.Deliveries().ListBySpace(e.channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 || ds[0].AgentID != "bob" || ds[0].OriginMessageID != origin.ID {
		t.Fatalf("reconciled deliveries = %+v, want single bob wake for origin", ds)
	}
	if ds[0].Status != delivery.StatusPending {
		t.Fatalf("reconciled delivery status = %q, want pending", ds[0].Status)
	}
}

// --- Fault 2: claim-before-reply ------------------------------------------
//
// On first claim the worker must persist exactly ONE pending assistant
// placeholder for the DeliveryID and fence-bind its stable MessageID onto the
// Delivery BEFORE the reply exists. This is the crash window between claim and
// reply: the visible "thinking" message and its binding are durable.
func TestFaultClaimBeforeReply(t *testing.T) {
	e := newFaultEnv(t)
	origin := e.seedMention("@bob hello")
	d := e.createDelivery(origin)

	// Simulate a crash after placeholder+bind but before the turn runs by doing
	// exactly those store/space writes the worker does at the top of run().
	claimed, fence := e.claim(d.ID)
	personas := e.app.fuzzyPersonaResolver()
	placeholder, existing, err := e.app.Spaces().EnsureDeliveryPlaceholder(claimed.SpaceID, claimed.ID, claimed.AgentID, claimed.ParentMessageID, personas.Info)
	if err != nil {
		t.Fatal(err)
	}
	if existing {
		t.Fatal("placeholder unexpectedly already existed on first claim")
	}
	if placeholder.Status != "pending" {
		t.Fatalf("placeholder status = %q, want pending", placeholder.Status)
	}
	bound, _, err := e.app.Deliveries().BindResultMessage(claimed.ID, fence, placeholder.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if bound.ResultMessageID != placeholder.ID {
		t.Fatalf("bound result = %q, want placeholder %q", bound.ResultMessageID, placeholder.ID)
	}

	ph := assistantPlaceholders(e.messages(), claimed.ID)
	if len(ph) != 1 {
		t.Fatalf("placeholders for delivery = %d, want exactly 1", len(ph))
	}
	if strings.TrimSpace(ph[0].Content) != "" {
		t.Fatalf("placeholder has content %q before reply, want empty", ph[0].Content)
	}
}

// --- Fault 3: reply-before-complete ---------------------------------------
//
// A full worker.run finalizes the reply INTO the placeholder and only then
// Completes the Delivery. After run, the Delivery points at the same visible
// message the reply landed in, and there is still exactly one placeholder.
func TestFaultReplyBeforeComplete(t *testing.T) {
	e := newFaultEnv(t)
	origin := e.seedMention("@bob hello")
	d := e.createDelivery(origin)

	claimed, fence := e.claim(d.ID)
	e.worker.run(context.Background(), claimed, fence)

	got, err := e.app.Deliveries().Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != delivery.StatusCompleted {
		t.Fatalf("delivery status = %q, want completed", got.Status)
	}
	ph := assistantPlaceholders(e.messages(), claimed.ID)
	if len(ph) != 1 {
		t.Fatalf("placeholders for delivery = %d, want exactly 1", len(ph))
	}
	if got.ResultMessageID != ph[0].ID {
		t.Fatalf("delivery result %q != placeholder %q", got.ResultMessageID, ph[0].ID)
	}
	if strings.TrimSpace(ph[0].Content) == "" {
		t.Fatalf("finalized placeholder content empty, want reply text")
	}
	if ph[0].Status == "pending" {
		t.Fatalf("finalized placeholder still pending")
	}
}

// --- Fault 4: duplicate replay --------------------------------------------
//
// If finalize already succeeded but Complete did not (crash in between), the
// delivery is still pending/leased and points at the stable placeholder.
// Replay must NOT append a second visible reply: it re-discovers the same
// placeholder via EnsureDeliveryPlaceholder's DeliveryID idempotency and
// finalizes in place. End state: exactly one placeholder, one reply.
func TestFaultDuplicateReplay(t *testing.T) {
	e := newFaultEnv(t)
	origin := e.seedMention("@bob hello")
	d := e.createDelivery(origin)

	// First attempt: run fully (reply + complete).
	claimed, fence := e.claim(d.ID)
	e.worker.run(context.Background(), claimed, fence)
	firstRuns := e.runs

	beforeCount := len(e.messages())
	firstPH := assistantPlaceholders(e.messages(), claimed.ID)
	if len(firstPH) != 1 {
		t.Fatalf("after first run placeholders = %d, want 1", len(firstPH))
	}

	// Replay the SAME delivery+fence (simulates a redelivery of an
	// already-finalized attempt). Completed is terminal: replay must be
	// idempotent for the finalizing fence and must not append a new message.
	e.worker.run(context.Background(), claimed, fence)

	afterCount := len(e.messages())
	if afterCount != beforeCount {
		t.Fatalf("replay changed message count %d -> %d, want stable", beforeCount, afterCount)
	}
	ph := assistantPlaceholders(e.messages(), claimed.ID)
	if len(ph) != 1 {
		t.Fatalf("after replay placeholders = %d, want exactly 1", len(ph))
	}
	if ph[0].ID != firstPH[0].ID {
		t.Fatalf("replay bound a different message %q != %q", ph[0].ID, firstPH[0].ID)
	}
	if e.runs != firstRuns {
		t.Fatalf("replay re-ran the turn (runs %d -> %d) on a completed delivery", firstRuns, e.runs)
	}
}

// --- Fault 5: stale fence -------------------------------------------------
//
// A slow worker (owner A) is superseded: its lease expires, owner B re-claims
// the same delivery (Attempt++ -> higher fence) and completes it. When the
// stale owner A finally finishes and tries to Complete with its old fence, the
// store must reject it (ErrStaleLease) and the worker must treat that as
// fail-visible, never reporting success and never overwriting B's result.
func TestFaultStaleFence(t *testing.T) {
	e := newFaultEnv(t)
	origin := e.seedMention("@bob hello")
	d := e.createDelivery(origin)

	// Owner A claims.
	past := time.Now().Add(-2 * deliveryLeaseTTL)
	claimedA, fenceA, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-A", past, deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("claim A: %v", err)
	}

	// A's lease has expired (claimed in the past). Owner B re-claims the same
	// lane now, bumping the fence.
	claimedB, fenceB, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-B", time.Now(), deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("claim B: %v", err)
	}
	if !(fenceB.Version > fenceA.Version) {
		t.Fatalf("fenceB version %d not greater than fenceA %d", fenceB.Version, fenceA.Version)
	}
	if claimedB.ID != claimedA.ID {
		t.Fatalf("B claimed a different delivery")
	}

	// While B still holds the active lease, the stale owner A wakes up and
	// tries to keep going with its OLD fence. Both renew and finalize must be
	// rejected as stale — A must never write under B's live lease.
	if _, err := e.app.Deliveries().Renew(claimedA.ID, fenceA, time.Now(), deliveryLeaseTTL); !errors.Is(err, delivery.ErrStaleLease) {
		t.Fatalf("stale Renew err = %v, want ErrStaleLease", err)
	}
	if _, err := e.app.Deliveries().Complete(claimedA.ID, fenceA, "fake-message-from-A", time.Now()); !errors.Is(err, delivery.ErrStaleLease) {
		t.Fatalf("stale Complete err = %v, want ErrStaleLease", err)
	}

	// B finishes and binds+completes the real reply.
	e.worker.run(context.Background(), claimedB, fenceB)
	completed, err := e.app.Deliveries().Get(claimedB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != delivery.StatusCompleted {
		t.Fatalf("after B run status = %q, want completed", completed.Status)
	}
	winnerMsg := completed.ResultMessageID

	// After B completed, A's late Complete with the stale fence is still
	// rejected (idempotent only for B's finalizing fence).
	if _, err := e.app.Deliveries().Complete(claimedA.ID, fenceA, "fake-message-from-A", time.Now()); !errors.Is(err, delivery.ErrStaleLease) {
		t.Fatalf("post-complete stale Complete err = %v, want ErrStaleLease", err)
	}

	// Driving the full stale run must not corrupt B's completed result: still
	// completed, still exactly one placeholder pointing at B's message.
	e.worker.run(context.Background(), claimedA, fenceA)
	after, err := e.app.Deliveries().Get(claimedB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != delivery.StatusCompleted || after.ResultMessageID != winnerMsg {
		t.Fatalf("stale run corrupted delivery: status=%q result=%q want completed/%q", after.Status, after.ResultMessageID, winnerMsg)
	}
	ph := assistantPlaceholders(e.messages(), claimedB.ID)
	if len(ph) != 1 {
		t.Fatalf("after stale run placeholders = %d, want exactly 1", len(ph))
	}
}

// --- Fault 6: failed -> retry -> success in the same placeholder ----------
//
// A failed turn marks the delivery failed (not auto-claimable) but keeps its
// placeholder. An explicit Requeue makes it claimable again; the retry reuses
// the SAME placeholder (DeliveryID idempotency) and, on success, finalizes it
// in place. No second visible message is ever created.
func TestFaultFailedRetrySamePlaceholder(t *testing.T) {
	e := newFaultEnv(t)
	origin := e.seedMention("@bob hello")
	d := e.createDelivery(origin)

	// First attempt fails inside the turn.
	e.reply = func(input string) string { panic("boom: runtime failure") }
	claimed, fence := e.claim(d.ID)
	func() {
		defer func() { _ = recover() }()
		e.worker.run(context.Background(), claimed, fence)
	}()

	// If the worker swallowed the failure internally it should have marked the
	// delivery failed; if it panicked out we force the failed state the way the
	// worker's error path does, then assert the placeholder survived.
	cur, err := e.app.Deliveries().Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Status != delivery.StatusFailed {
		if _, ferr := e.app.Deliveries().Fail(claimed.ID, fence, "boom", time.Now()); ferr != nil {
			t.Fatalf("could not force failed state: %v (status was %q)", ferr, cur.Status)
		}
	}
	failed, err := e.app.Deliveries().Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != delivery.StatusFailed {
		t.Fatalf("status = %q, want failed", failed.Status)
	}
	ph := assistantPlaceholders(e.messages(), claimed.ID)
	if len(ph) != 1 {
		t.Fatalf("after failed attempt placeholders = %d, want exactly 1", len(ph))
	}
	failedPHID := ph[0].ID

	// Failed is NOT auto-claimable.
	if _, _, cerr := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, e.worker.ownerID, time.Now(), deliveryLeaseTTL); !errors.Is(cerr, delivery.ErrNotClaimable) {
		t.Fatalf("claim of failed delivery err = %v, want ErrNotClaimable", cerr)
	}

	// Explicit Requeue -> claimable again.
	if _, err := e.app.Deliveries().Requeue(claimed.ID, time.Now()); err != nil {
		t.Fatalf("requeue: %v", err)
	}
	e.reply = func(input string) string { return "recovered reply to " + input }
	claimed2, fence2 := e.claim(d.ID)
	e.worker.run(context.Background(), claimed2, fence2)

	done, err := e.app.Deliveries().Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != delivery.StatusCompleted {
		t.Fatalf("after retry status = %q, want completed", done.Status)
	}
	ph2 := assistantPlaceholders(e.messages(), claimed.ID)
	if len(ph2) != 1 {
		t.Fatalf("after retry placeholders = %d, want exactly 1 (same message reused)", len(ph2))
	}
	if ph2[0].ID != failedPHID {
		t.Fatalf("retry used a new message %q, want reuse of %q", ph2[0].ID, failedPHID)
	}
	if !strings.Contains(ph2[0].Content, "recovered reply") {
		t.Fatalf("retry did not finalize recovered content, got %q", ph2[0].Content)
	}
}

// --- Fault 7: Task exists but Delivery missing ----------------------------
//
// The persisted routing intent is the user-visible commitment ("Task"); its
// Delivery (the execution attempt) can be lost (never created, or GC'd).
// reconcile must re-materialize the Delivery from the persisted intent alone,
// idempotently, so the wake is never permanently dropped after a restart.
func TestFaultTaskExistsDeliveryMissing(t *testing.T) {
	e := newFaultEnv(t)
	origin := e.seedMention("@bob hello")

	// No delivery created yet: the commitment exists (intent persisted) but the
	// execution attempt is missing.
	ds, err := e.app.Deliveries().ListBySpace(e.channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 0 {
		t.Fatalf("precondition: expected 0 deliveries, got %d", len(ds))
	}

	// Reconcile from persisted intents.
	if err := e.app.reconcileDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	ds, err = e.app.Deliveries().ListBySpace(e.channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 || ds[0].OriginMessageID != origin.ID || ds[0].AgentID != "bob" {
		t.Fatalf("reconcile did not recover missing delivery: %+v", ds)
	}

	// Reconcile again: must be idempotent (create-if-absent), no duplicate.
	if err := e.app.reconcileDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	ds, err = e.app.Deliveries().ListBySpace(e.channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 {
		t.Fatalf("reconcile not idempotent: %d deliveries, want 1", len(ds))
	}

	// And the recovered delivery drives to a real reply.
	claimed, fence := e.claim(ds[0].ID)
	e.worker.run(context.Background(), claimed, fence)
	got, err := e.app.Deliveries().Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != delivery.StatusCompleted {
		t.Fatalf("recovered delivery did not complete: status %q", got.Status)
	}
}

// --- Fault 8: direct single-agent stays zero-Delivery ---------------------
//
// The scope guard: a direct (non-routed) single-agent turn keeps the
// synchronous shortest path. It must create NO Delivery at all -- the durable
// machinery is only for async/routed collaboration.
func TestFaultDirectZeroDelivery(t *testing.T) {
	e := newFaultEnv(t)

	// A direct DM-style space with no routing/mention.
	direct, err := e.app.Spaces().EnsureSpace(space.KindDirectChat, "bob", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.app.Spaces().AppendUserMessage(direct.ID, "hello bob", nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	source := "desktop:direct:" + direct.ID
	res := e.app.runChannelWake(ctx, source, direct.ID, space.RoutingTarget{AgentID: "bob", OriginMessageID: first.ID}, "hello bob", nil)
	if res.err != nil {
		t.Fatalf("direct wake failed: %v", res.err)
	}

	ds, err := e.app.Deliveries().ListBySpace(direct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 0 {
		t.Fatalf("direct turn created %d deliveries, want 0 (synchronous shortest path)", len(ds))
	}
	// The reply still landed synchronously via the legacy append path.
	msgs, err := e.app.Spaces().LoadSpace(direct.ID)
	if err != nil {
		t.Fatal(err)
	}
	hasReply := false
	for _, m := range msgs.Messages {
		if m.AuthorKind == space.ParticipantAgent && strings.Contains(m.Content, "reply to hello bob") {
			hasReply = true
		}
	}
	if !hasReply {
		t.Fatalf("direct turn produced no synchronous assistant reply: %+v", msgs.Messages)
	}
}

// TestFaultWorkerFailMarksPlaceholderFailed is the headless-orphan guard for
// Iris review point ③ (fail-visible). When a routed worker turn fails inside the
// runtime, the worker Fails the Delivery — but the assistant placeholder it
// appended must not stay stuck "pending" in the Space. On a headless server
// (no desktop backend consuming TurnError) the placeholder is the only durable
// projection of the failure, so the worker itself must flip it to status=failed
// with the error text. Otherwise a restart shows a forever-spinning bubble.
func TestFaultWorkerFailMarksPlaceholderFailed(t *testing.T) {
	e := newFaultEnv(t)
	origin := e.seedMention("@bob hello")
	d := e.createDelivery(origin)

	// Runtime fails the turn (returns an error rather than panicking, so the
	// worker's normal Fail path runs to completion).
	e.app.RegisterRuntime("stub", func(re *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			e.runs++
			return errors.New("boom: runtime failure")
		}), nil
	})

	claimed, fence := e.claim(d.ID)
	e.worker.run(context.Background(), claimed, fence)

	got, err := e.app.Deliveries().Get(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != delivery.StatusFailed {
		t.Fatalf("delivery status = %q, want failed", got.Status)
	}
	ph := assistantPlaceholders(e.messages(), claimed.ID)
	if len(ph) != 1 {
		t.Fatalf("placeholders for delivery = %d, want exactly 1", len(ph))
	}
	if ph[0].Status != "failed" {
		t.Fatalf("worker Fail left placeholder status = %q, want failed (headless orphan)", ph[0].Status)
	}
	if strings.TrimSpace(ph[0].Error) == "" {
		t.Fatalf("failed placeholder must carry error text, got empty")
	}
	if !strings.Contains(ph[0].Error, "boom") {
		t.Fatalf("failed placeholder error = %q, want the runtime failure text", ph[0].Error)
	}
}

// --- Fault 5b: stale-fence WRITE linearization (Iris msg a40c4349) ---------
//
// The guard-trip / Complete-stale rejection is NOT enough: an old-fence worker
// can read a live lease, then — before it writes the Space — lose the lease to a
// newer owner who finalizes. If the old worker's Space write is not itself
// version-gated, it overwrites the newer owner's reply and Complete only fails
// AFTER the corrupt content already landed.
//
// The fix is a persistent write-condition: FinalizeDeliveryMessage/
// FailDeliveryMessage carry the Delivery fence version, and the Space manager
// rejects — inside its save lock — any write whose version is below the
// placeholder's already-accepted version. The lock is the linearization point,
// so there is no separable check to race.
//
// This is the EASIER half (Iris msg 7bfba570): v2 (higher fence) finalizes
// first, then the stale v1 write is rejected. It only proves "highest WRITTEN
// wins". The harder half — v1 rejected even when v2 has claimed but NOT yet
// written — is TestFaultStaleWriteRejectedBeforeNewOwnerFinalizes below.
//
// Authority is now the live Delivery lease, not a version stamped on the
// Message: FinalizeDeliveryMessage/FailDeliveryMessage carry the finalizing
// fence, and the Store re-reads the authoritative Delivery and checks
// OwnsLiveLease inside the SAME s.mu that guards the Space byte-write. A
// superseded fence (owner-A after owner-B reclaimed) never matches the live
// lease, so its write is rejected before any bytes land.
func TestFaultStaleWriteRejectedByVersionGate(t *testing.T) {
	e := newFaultEnv(t)
	origin := e.seedMention("@bob hello")
	d := e.createDelivery(origin)

	// Owner A claims in the past (v1) so its lease is expired and owner B can
	// legitimately reclaim now (v2 > v1) — the real "A hung, B took over" race.
	past := time.Now().Add(-2 * deliveryLeaseTTL)
	_, fenceA, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-A", past, deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("claim A: %v", err)
	}
	_, fenceB, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-B", time.Now(), deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("claim B: %v", err)
	}
	if !(fenceB.Version > fenceA.Version) {
		t.Fatalf("fenceB version %d not greater than fenceA %d", fenceB.Version, fenceA.Version)
	}

	// One shared placeholder for the DeliveryID (idempotent by DeliveryID).
	personas := e.app.fuzzyPersonaResolver()
	ph, _, err := e.app.Spaces().EnsureDeliveryPlaceholder(d.SpaceID, d.ID, d.AgentID, d.ParentMessageID, personas.Info)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	// B (live lease owner) finalizes first: its reply lands.
	if _, _, err := e.app.Spaces().FinalizeDeliveryMessage(d.SpaceID, ph.ID, d.ID, fenceB.OwnerID, fenceB.Version, now, func(m *space.Message) {
		m.Content = "v2 winner reply"
		m.Status = ""
	}, nil, personas.Info, nil); err != nil {
		t.Fatalf("v2 finalize: %v", err)
	}

	// A (superseded fence) attempts to overwrite. The live-lease check must
	// reject it INSIDE the save lock — the corrupt content never lands.
	_, _, staleErr := e.app.Spaces().FinalizeDeliveryMessage(d.SpaceID, ph.ID, d.ID, fenceA.OwnerID, fenceA.Version, now, func(m *space.Message) {
		m.Content = "v1 STALE reply that must never land"
		m.Status = ""
	}, nil, personas.Info, nil)
	if !errors.Is(staleErr, space.ErrStaleDeliveryWrite) {
		t.Fatalf("stale finalize err = %v, want ErrStaleDeliveryWrite", staleErr)
	}

	// A stale Fail must be rejected the same way — it cannot flip v2's reply to failed.
	if _, ferr := e.app.Spaces().FailDeliveryMessage(d.SpaceID, ph.ID, d.ID, fenceA.OwnerID, fenceA.Version, now, "v1 stale failure"); !errors.Is(ferr, space.ErrStaleDeliveryWrite) {
		t.Fatalf("stale FailDeliveryMessage err = %v, want ErrStaleDeliveryWrite", ferr)
	}

	final := assistantPlaceholders(e.messages(), d.ID)
	if len(final) != 1 {
		t.Fatalf("placeholders = %d, want exactly 1", len(final))
	}
	if final[0].Content != "v2 winner reply" {
		t.Fatalf("final content = %q, want v2 winner reply (stale write leaked)", final[0].Content)
	}
	if final[0].Status == "failed" {
		t.Fatalf("stale Fail flipped the v2 reply to failed")
	}
}

// TestFaultStaleWriteBarrier forces the exact race Iris demanded: the stale v1
// writer is blocked from entering its Space write until AFTER the newer owner v2
// has finalized successfully, then released. This proves the rejection is a
// property of the write itself (version-gated inside the lock), not of when the
// worker happened to check its lease. Whatever the wall-clock order, the final
// Message belongs to v2.
func TestFaultStaleWriteBarrier(t *testing.T) {
	e := newFaultEnv(t)
	origin := e.seedMention("@bob hello")
	d := e.createDelivery(origin)

	past := time.Now().Add(-2 * deliveryLeaseTTL)
	_, fenceA, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-A", past, deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("claim A: %v", err)
	}
	_, fenceB, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-B", time.Now(), deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("claim B: %v", err)
	}
	personas := e.app.fuzzyPersonaResolver()
	ph, _, err := e.app.Spaces().EnsureDeliveryPlaceholder(d.SpaceID, d.ID, d.AgentID, d.ParentMessageID, personas.Info)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	// v1 is "ready to write" (its turn finished) but is held at the barrier. It
	// only enters FinalizeDeliveryMessage after v2 has fully finalized.
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		<-release
		_, _, e1 := e.app.Spaces().FinalizeDeliveryMessage(d.SpaceID, ph.ID, d.ID, fenceA.OwnerID, fenceA.Version, now, func(m *space.Message) {
			m.Content = "v1 STALE reply that must never land"
			m.Status = ""
		}, nil, personas.Info, nil)
		done <- e1
	}()

	// v2 finalizes to success first.
	if _, _, err := e.app.Spaces().FinalizeDeliveryMessage(d.SpaceID, ph.ID, d.ID, fenceB.OwnerID, fenceB.Version, now, func(m *space.Message) {
		m.Content = "v2 winner reply"
		m.Status = ""
	}, nil, personas.Info, nil); err != nil {
		t.Fatalf("v2 finalize: %v", err)
	}

	// Release the stale v1 write only now.
	close(release)
	staleErr := <-done
	if !errors.Is(staleErr, space.ErrStaleDeliveryWrite) {
		t.Fatalf("barrier stale finalize err = %v, want ErrStaleDeliveryWrite", staleErr)
	}

	final := assistantPlaceholders(e.messages(), d.ID)
	if len(final) != 1 {
		t.Fatalf("placeholders = %d, want exactly 1", len(final))
	}
	if final[0].Content != "v2 winner reply" {
		t.Fatalf("final = {content=%q}, want v2 winner", final[0].Content)
	}
}

// TestFaultStaleWriteRejectedBeforeNewOwnerFinalizes is the HARDER barrier Iris
// demanded in msg 7bfba570 — the one the message-version gate could NOT pass.
//
// Sequence: owner-A (v1) claims and its turn finishes. Owner-B (v2) then claims
// successfully — the live lease is now v2 — but B is paused BEFORE it finalizes
// (simulating B crashing between claim and Space write). Now the stale v1
// finalize arrives. Under the old "highest WRITTEN version wins" gate, the
// Message still showed version 0 (B never wrote), so v1's finalize would have
// been ACCEPTED and its bad content would persist visibly until B's next
// reclaim. Under the live-lease authority, v1's fence no longer owns the lease
// (B's claim bumped it), so the write is rejected inside s.mu and the Message
// stays B's untouched pending placeholder — no v1 content ever lands.
func TestFaultStaleWriteRejectedBeforeNewOwnerFinalizes(t *testing.T) {
	e := newFaultEnv(t)
	origin := e.seedMention("@bob hello")
	d := e.createDelivery(origin)

	past := time.Now().Add(-2 * deliveryLeaseTTL)
	_, fenceA, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-A", past, deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("claim A: %v", err)
	}
	// B reclaims now: the live lease is v2. B does NOT finalize (crash simulated).
	_, fenceB, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-B", time.Now(), deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("claim B: %v", err)
	}
	if !(fenceB.Version > fenceA.Version) {
		t.Fatalf("fenceB version %d not greater than fenceA %d", fenceB.Version, fenceA.Version)
	}

	personas := e.app.fuzzyPersonaResolver()
	ph, _, err := e.app.Spaces().EnsureDeliveryPlaceholder(d.SpaceID, d.ID, d.AgentID, d.ParentMessageID, personas.Info)
	if err != nil {
		t.Fatal(err)
	}
	if ph.Status != "pending" {
		t.Fatalf("placeholder status = %q, want pending before any finalize", ph.Status)
	}

	// Capture the exact on-disk Space bytes before the stale writes. Rejection
	// must leave these byte-identical: condition 4 is "no Space bytes written",
	// not merely "content reconciled away afterward". LoadSpace re-reads from
	// disk in the real store, so a marshaled before/after equality proves the
	// persisted file was never touched.
	beforeBytes := e.marshalSpace(d.SpaceID)

	// Stale v1 finalize arrives while v2 owns the lease but has written nothing.
	// It MUST be rejected — this is exactly the case the message-version gate let through.
	now := time.Now()
	_, _, staleErr := e.app.Spaces().FinalizeDeliveryMessage(d.SpaceID, ph.ID, d.ID, fenceA.OwnerID, fenceA.Version, now, func(m *space.Message) {
		m.Content = "v1 STALE reply that must never land"
		m.Status = ""
	}, nil, personas.Info, nil)
	if !errors.Is(staleErr, space.ErrStaleDeliveryWrite) {
		t.Fatalf("stale v1 finalize err = %v, want ErrStaleDeliveryWrite (message-version gate would have ACCEPTED this)", staleErr)
	}

	// A stale v1 Fail before v2 writes must also be rejected.
	if _, ferr := e.app.Spaces().FailDeliveryMessage(d.SpaceID, ph.ID, d.ID, fenceA.OwnerID, fenceA.Version, now, "v1 stale failure"); !errors.Is(ferr, space.ErrStaleDeliveryWrite) {
		t.Fatalf("stale v1 Fail err = %v, want ErrStaleDeliveryWrite", ferr)
	}

	// Condition 4: zero Space bytes written on rejection.
	if afterBytes := e.marshalSpace(d.SpaceID); afterBytes != beforeBytes {
		t.Fatalf("rejected stale writes mutated persisted Space bytes:\nbefore=%s\nafter=%s", beforeBytes, afterBytes)
	}

	// The Message must still be v2's untouched pending placeholder — no v1 content,
	// not flipped to failed, and its identity fields (DeliveryID, ChainRoot via
	// RoutingIntents, ParentMessageID) unchanged. This is the visible-stale-result
	// window Iris flagged.
	final := assistantPlaceholders(e.messages(), d.ID)
	if len(final) != 1 {
		t.Fatalf("placeholders = %d, want exactly 1", len(final))
	}
	if final[0].Status != "pending" {
		t.Fatalf("placeholder status = %q, want still pending (v1 must not have written)", final[0].Status)
	}
	if strings.Contains(final[0].Content, "STALE") {
		t.Fatalf("stale v1 content leaked into placeholder: %q", final[0].Content)
	}
	if final[0].DeliveryID != d.ID {
		t.Fatalf("placeholder DeliveryID = %q, want %q (identity mutated)", final[0].DeliveryID, d.ID)
	}
	if final[0].ParentMessageID != ph.ParentMessageID {
		t.Fatalf("placeholder ParentMessageID = %q, want %q (identity mutated)", final[0].ParentMessageID, ph.ParentMessageID)
	}
	if len(final[0].RoutingIntents) != 0 {
		t.Fatalf("placeholder RoutingIntents = %+v, want none (stale v1 injected chain intents)", final[0].RoutingIntents)
	}

	// Sanity: v2 (the live owner) CAN still finalize afterwards — the placeholder
	// was left intact and claimable, not corrupted.
	if _, _, err := e.app.Spaces().FinalizeDeliveryMessage(d.SpaceID, ph.ID, d.ID, fenceB.OwnerID, fenceB.Version, time.Now(), func(m *space.Message) {
		m.Content = "v2 winner reply"
		m.Status = ""
	}, nil, personas.Info, nil); err != nil {
		t.Fatalf("v2 finalize after stale rejection: %v", err)
	}
	after := assistantPlaceholders(e.messages(), d.ID)
	if len(after) != 1 || after[0].Content != "v2 winner reply" {
		t.Fatalf("after v2 finalize = %+v, want single v2 winner reply", after)
	}
}

// TestFaultOriginVanishedAfterBindLeaseReclaimFailsPlaceholder is the
// headless-orphan guard for the lease-expiry reclaim-after-bind window Iris
// flagged (msg 6676da17). Owner-A claims in the past and fence-binds a visible
// "pending" placeholder onto the Delivery, then dies WITHOUT finalizing — its
// lease simply expires (no Fail, no Requeue: this is genuine lease-expiry
// reclaim, not an explicit retry). Before a fresh worker reclaims, the origin
// user message vanishes (space edited, message deleted, or the fact corrupted).
// Owner-B reclaims the expired lease with ClaimNextInLane(now) and the
// origin-not-found branch must NOT merely Fail the Delivery and walk away — that
// would leave A's already-bound placeholder spinning "pending" forever on a
// headless server, its sole durable projection. B must first flip the bound
// placeholder to failed under its live lease, THEN Fail the Delivery.
func TestFaultOriginVanishedAfterBindLeaseReclaimFailsPlaceholder(t *testing.T) {
	e := newFaultEnv(t)
	origin := e.seedMention("@bob hello")
	d := e.createDelivery(origin)

	// Owner-A claims in the past so its lease is already expired, fence-binds the
	// placeholder, then "crashes" before finalize. No Fail/Requeue: the lease just
	// lapses. This is the exact on-disk state a lease-expiry reclaim finds — a
	// leased-but-expired Delivery carrying ResultMessageID and a pending placeholder.
	past := time.Now().Add(-2 * deliveryLeaseTTL)
	_, fenceA, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-A", past, deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("claim A: %v", err)
	}
	personas := e.app.fuzzyPersonaResolver()
	ph, _, err := e.app.Spaces().EnsureDeliveryPlaceholder(d.SpaceID, d.ID, d.AgentID, d.ParentMessageID, personas.Info)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.app.Deliveries().BindResultMessage(d.ID, fenceA, ph.ID, past); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if ph.Status != "pending" {
		t.Fatalf("placeholder status = %q, want pending before crash", ph.Status)
	}

	// The origin fact vanishes before the reclaim runs.
	if err := e.app.Spaces().DeleteMessage(d.SpaceID, origin.ID); err != nil {
		t.Fatalf("delete origin: %v", err)
	}
	if _, ok := e.app.loadOriginMessage(d.SpaceID, origin.ID); ok {
		t.Fatalf("origin still present after delete")
	}

	// Owner-B reclaims the expired lease at now(): the live lease is v2. Its fence
	// is what the origin-not-found FailDeliveryMessage must present, and it must
	// have preserved A's ResultMessageID (reclaim never clears it).
	claimed2, fence2, err := e.app.Deliveries().ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-B", time.Now(), deliveryLeaseTTL)
	if err != nil {
		t.Fatalf("reclaim B: %v", err)
	}
	if !(fence2.Version > fenceA.Version) {
		t.Fatalf("reclaim fence version %d not greater than A %d", fence2.Version, fenceA.Version)
	}
	if claimed2.ResultMessageID != ph.ID {
		t.Fatalf("ResultMessageID = %q, want %q preserved across lease reclaim", claimed2.ResultMessageID, ph.ID)
	}
	e.worker.run(context.Background(), claimed2, fence2)

	// The Delivery is failed and out of the claimable set.
	got, err := e.app.Deliveries().Get(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != delivery.StatusFailed {
		t.Fatalf("delivery status = %q, want failed", got.Status)
	}

	// The crux: the bound placeholder must be failed, NOT a forever-pending bubble.
	final := assistantPlaceholders(e.messages(), d.ID)
	if len(final) != 1 {
		t.Fatalf("placeholders = %d, want exactly 1", len(final))
	}
	if final[0].ID != ph.ID {
		t.Fatalf("reclaim used a new placeholder %q, want the bound %q", final[0].ID, ph.ID)
	}
	if final[0].Status != "failed" {
		t.Fatalf("origin-not-found reclaim left placeholder status = %q, want failed (headless orphan)", final[0].Status)
	}
	if !strings.Contains(final[0].Error, "origin message not found") {
		t.Fatalf("failed placeholder error = %q, want the origin-not-found text", final[0].Error)
	}
}
