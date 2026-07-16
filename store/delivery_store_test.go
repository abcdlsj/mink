package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/delivery"
)

var dtBase = time.Date(2026, 7, 17, 4, 0, 0, 0, time.UTC)

const dtTTL = 30 * time.Second

func seedDelivery(kind delivery.Kind, space, parent, origin, agent string) *delivery.Delivery {
	return &delivery.Delivery{
		Kind:            kind,
		SpaceID:         space,
		ParentMessageID: parent,
		OriginMessageID: origin,
		AgentID:         agent,
	}
}

func TestDeliveryStoreCreateIfAbsentIdempotent(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()

	first, created, err := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "a1"), dtBase)
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	if first.Status != delivery.StatusPending || first.Attempt != 0 || first.Lease != nil {
		t.Fatalf("new delivery not pending/clean: %+v", first)
	}
	// Same stable key -> same record, not created again.
	second, created2, err := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "a1"), dtBase.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("second create-if-absent reported created=true")
	}
	if second.ID != first.ID {
		t.Fatalf("distinct IDs for same stable key: %q vs %q", first.ID, second.ID)
	}
	// Different origin message -> different key -> new record.
	third, created3, err := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o2", "a1"), dtBase)
	if err != nil || !created3 {
		t.Fatalf("third create: created=%v err=%v", created3, err)
	}
	if third.ID == first.ID {
		t.Fatal("distinct stable keys collapsed to one record")
	}
}

func TestDeliveryStoreClaimPersistsLease(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	d, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "a1"), dtBase)

	claimed, fence, err := ds.Claim(d.ID, "worker-a", dtBase, dtTTL)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.Status != delivery.StatusLeased || claimed.Attempt != 1 {
		t.Fatalf("claimed = %+v", claimed)
	}
	// Reload from disk to confirm the lease is durable, not just in-memory.
	reloaded, err := ds.Get(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Lease == nil || reloaded.Lease.OwnerID != "worker-a" || reloaded.Lease.Version != fence.Version {
		t.Fatalf("reloaded lease = %+v", reloaded.Lease)
	}
}

func TestDeliveryStoreCompleteFencedStaleRejected(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	d, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "a1"), dtBase)
	_, fence, _ := ds.Claim(d.ID, "worker-a", dtBase, dtTTL)

	// Reclaim after expiry by another worker.
	_, _, err := ds.Claim(d.ID, "worker-b", dtBase.Add(dtTTL+time.Second), dtTTL)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	// Old owner's fence must be rejected.
	if _, err := ds.Complete(d.ID, fence, "msg-old", dtBase.Add(dtTTL+2*time.Second)); !errors.Is(err, delivery.ErrStaleLease) {
		t.Fatalf("stale complete err = %v, want ErrStaleLease", err)
	}
}

func TestDeliveryStoreLaneFIFOBlocksWhenBusy(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	// Two deliveries in the same lane (same space/parent/agent), distinct origins.
	d1, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "a1"), dtBase)
	_, _, _ = ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o2", "a1"), dtBase.Add(time.Second))

	// First lane claim picks the oldest (d1).
	got, _, err := ds.ClaimNextInLane("sp", "p1", "a1", "worker-a", dtBase.Add(2*time.Second), dtTTL)
	if err != nil {
		t.Fatalf("first lane claim: %v", err)
	}
	if got.ID != d1.ID {
		t.Fatalf("lane claim picked %q, want oldest %q", got.ID, d1.ID)
	}
	// Lane is now busy (d1 holds an active lease) -> second claim blocked.
	if _, _, err := ds.ClaimNextInLane("sp", "p1", "a1", "worker-b", dtBase.Add(3*time.Second), dtTTL); !errors.Is(err, delivery.ErrLaneBusy) {
		t.Fatalf("busy lane err = %v, want ErrLaneBusy", err)
	}
}

func TestDeliveryStoreLaneAdvancesAfterComplete(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	d1, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "a1"), dtBase)
	d2, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o2", "a1"), dtBase.Add(time.Second))

	claimed, fence, err := ds.ClaimNextInLane("sp", "p1", "a1", "worker-a", dtBase.Add(2*time.Second), dtTTL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ds.Complete(claimed.ID, fence, "msg-1", dtBase.Add(3*time.Second)); err != nil {
		t.Fatalf("complete d1: %v", err)
	}
	// Lane free now; next claim gets d2.
	next, _, err := ds.ClaimNextInLane("sp", "p1", "a1", "worker-b", dtBase.Add(4*time.Second), dtTTL)
	if err != nil {
		t.Fatalf("second lane claim: %v", err)
	}
	if next.ID != d2.ID {
		t.Fatalf("advanced to %q, want %q", next.ID, d2.ID)
	}
	_ = d1
}

func TestDeliveryStoreListBySpaceAndLeasedByAgent(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "spA", "p1", "o1", "a1"), dtBase)
	dB, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "spB", "p1", "o1", "a2"), dtBase)
	ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "spA", "p2", "o1", "a1"), dtBase)

	inA, err := ds.ListBySpace("spA")
	if err != nil {
		t.Fatal(err)
	}
	if len(inA) != 2 {
		t.Fatalf("ListBySpace(spA) = %d, want 2", len(inA))
	}
	// Claim dB (AgentID=a2) with a DISTINCT lease owner. ListLeasedByOwner must
	// filter by the lease holder (worker-x), NOT the target AgentID (a2).
	if _, _, err := ds.Claim(dB.ID, "worker-x", dtBase, dtTTL); err != nil {
		t.Fatal(err)
	}
	leased, err := ds.ListLeasedByOwner("worker-x")
	if err != nil {
		t.Fatal(err)
	}
	if len(leased) != 1 || leased[0].ID != dB.ID {
		t.Fatalf("ListLeasedByOwner(worker-x) = %+v", leased)
	}
	// Querying by the AgentID (a2) must return nothing — it is not the owner.
	byAgent, _ := ds.ListLeasedByOwner("a2")
	if len(byAgent) != 0 {
		t.Fatalf("ListLeasedByOwner(a2) = %d, want 0 (a2 is AgentID, not lease owner)", len(byAgent))
	}
	// A different worker owns nothing.
	other, _ := ds.ListLeasedByOwner("worker-y")
	if len(other) != 0 {
		t.Fatalf("ListLeasedByOwner(worker-y) = %d, want 0", len(other))
	}
}

func TestDeliveryStoreDistinctLanesClaimIndependently(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	// Same space+parent, different agents => different lanes.
	dA, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "agentA"), dtBase)
	dB, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "agentB"), dtBase)

	// Claim lane A; lane B must remain independently claimable.
	gotA, _, err := ds.ClaimNextInLane("sp", "p1", "agentA", "worker-a", dtBase.Add(time.Second), dtTTL)
	if err != nil {
		t.Fatalf("lane A claim: %v", err)
	}
	if gotA.ID != dA.ID {
		t.Fatalf("lane A claimed %q, want %q", gotA.ID, dA.ID)
	}
	gotB, _, err := ds.ClaimNextInLane("sp", "p1", "agentB", "worker-b", dtBase.Add(time.Second), dtTTL)
	if err != nil {
		t.Fatalf("lane B claim (should be independent of busy lane A): %v", err)
	}
	if gotB.ID != dB.ID {
		t.Fatalf("lane B claimed %q, want %q", gotB.ID, dB.ID)
	}
}

func TestDeliveryStoreCompleteSameFenceIdempotent(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	d, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "a1"), dtBase)
	_, fence, _ := ds.Claim(d.ID, "worker-a", dtBase, dtTTL)

	if _, err := ds.Complete(d.ID, fence, "msg-1", dtBase.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// Same-fence terminal transition re-applied: idempotent no-op, result frozen.
	again, err := ds.Complete(d.ID, fence, "msg-2", dtBase.Add(2*time.Second))
	if err != nil {
		t.Fatalf("repeat complete under same fence err = %v, want nil", err)
	}
	if again.Status != delivery.StatusCompleted || again.ResultMessageID != "msg-1" {
		t.Fatalf("terminal complete not immutable: %+v", again)
	}
	// Reload confirms the persisted record was not mutated by the repeat.
	reloaded, _ := ds.Get(d.ID)
	if reloaded.ResultMessageID != "msg-1" {
		t.Fatalf("persisted result = %q, want msg-1", reloaded.ResultMessageID)
	}
}

func TestDeliveryStoreRequeueReuseMessage(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	d, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "a1"), dtBase)
	_, fence, _ := ds.Claim(d.ID, "worker-a", dtBase, dtTTL)

	failed, err := ds.Fail(d.ID, fence, "boom", dtBase.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != delivery.StatusFailed {
		t.Fatalf("status = %q, want failed", failed.Status)
	}
	attempt := failed.Attempt
	requeued, err := ds.Requeue(d.ID, dtBase.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if requeued.Status != delivery.StatusPending || requeued.Attempt != attempt {
		t.Fatalf("requeued = %+v (attempt should stay %d)", requeued, attempt)
	}
	// Re-claim after requeue bumps attempt.
	reclaimed, _, err := ds.Claim(d.ID, "worker-b", dtBase.Add(3*time.Second), dtTTL)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed.Attempt != attempt+1 {
		t.Fatalf("attempt after re-claim = %d, want %d", reclaimed.Attempt, attempt+1)
	}
}

func TestDeliveryStoreBindResultMessagePersistsAndFenced(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	d, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "a1"), dtBase)
	_, fence, _ := ds.Claim(d.ID, "worker-a", dtBase, dtTTL)

	_, bound, err := ds.BindResultMessage(d.ID, fence, "msg-1", dtBase.Add(time.Second))
	if err != nil || bound != "msg-1" {
		t.Fatalf("bind: bound=%q err=%v", bound, err)
	}
	// Durable: reload from disk shows the bound id.
	reloaded, _ := ds.Get(d.ID)
	if reloaded.ResultMessageID != "msg-1" {
		t.Fatalf("persisted result = %q, want msg-1", reloaded.ResultMessageID)
	}
	// Same id re-binds idempotently.
	if _, again, err := ds.BindResultMessage(d.ID, fence, "msg-1", dtBase.Add(2*time.Second)); err != nil || again != "msg-1" {
		t.Fatalf("same-id rebind: again=%q err=%v", again, err)
	}
	// A different non-empty id is a persisted-layer conflict, not a silent drop.
	if _, _, err := ds.BindResultMessage(d.ID, fence, "msg-2", dtBase.Add(3*time.Second)); !errors.Is(err, delivery.ErrResultConflict) {
		t.Fatalf("conflict bind err = %v, want ErrResultConflict", err)
	}
	// The persisted record still holds the original id after the rejected conflict.
	after, _ := ds.Get(d.ID)
	if after.ResultMessageID != "msg-1" {
		t.Fatalf("result mutated by rejected conflict: %q", after.ResultMessageID)
	}
	// A stale fence is rejected at the store layer too.
	stale := delivery.Fence{OwnerID: "worker-a", Version: 999}
	if _, _, err := ds.BindResultMessage(d.ID, stale, "msg-z", dtBase.Add(4*time.Second)); !errors.Is(err, delivery.ErrStaleLease) {
		t.Fatalf("stale bind err = %v, want ErrStaleLease", err)
	}
}

func TestDeliveryStoreCancelPersistsAndFencesOldOwner(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	d, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "a1"), dtBase)
	_, fence, _ := ds.Claim(d.ID, "worker-a", dtBase, dtTTL)

	canceled, err := ds.Cancel(d.ID, dtBase.Add(time.Second))
	if err != nil || canceled.Status != delivery.StatusCanceled {
		t.Fatalf("cancel: status=%q err=%v", canceled.Status, err)
	}
	// Durable across reload.
	reloaded, _ := ds.Get(d.ID)
	if reloaded.Status != delivery.StatusCanceled || reloaded.Lease != nil {
		t.Fatalf("persisted cancel = %+v", reloaded)
	}
	// The old owner's fence can no longer complete OR fail; canceled cannot be
	// requeued or re-claimed.
	if _, err := ds.Complete(d.ID, fence, "msg", dtBase.Add(2*time.Second)); !errors.Is(err, delivery.ErrTerminal) {
		t.Fatalf("complete after cancel err = %v, want ErrTerminal", err)
	}
	if _, err := ds.Fail(d.ID, fence, "boom", dtBase.Add(2*time.Second)); !errors.Is(err, delivery.ErrTerminal) {
		t.Fatalf("fail after cancel err = %v, want ErrTerminal", err)
	}
	if _, err := ds.Requeue(d.ID, dtBase.Add(2*time.Second)); !errors.Is(err, delivery.ErrNotClaimable) {
		t.Fatalf("requeue after cancel err = %v, want ErrNotClaimable", err)
	}
	if _, _, err := ds.Claim(d.ID, "worker-b", dtBase.Add(3*time.Second), dtTTL); !errors.Is(err, delivery.ErrNotClaimable) {
		t.Fatalf("claim after cancel err = %v, want ErrNotClaimable", err)
	}
}

// Iris P0-3 store side + fail-close: a corrupt JSON file in the deliveries dir
// must abort listing so CreateIfAbsent cannot miss an existing stable key and
// mint a duplicate. It fails closed (error), it does NOT silently create.
func TestDeliveryStoreCorruptFileFailsClosed(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	if _, _, err := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "a1"), dtBase); err != nil {
		t.Fatal(err)
	}
	// Drop a corrupt record into the deliveries dir.
	corrupt := filepath.Join(s.deliveriesDir, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A create for a NEW stable key must fail closed rather than skip the corrupt
	// file (which would risk missing an existing key and duplicating).
	_, created, err := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o2", "a1"), dtBase.Add(time.Second))
	if err == nil {
		t.Fatal("CreateIfAbsent succeeded despite corrupt store; want fail-close error")
	}
	if created {
		t.Fatal("CreateIfAbsent reported created=true on a corrupt store")
	}
	// ListBySpace and ClaimNextInLane must also fail closed.
	if _, err := ds.ListBySpace("sp"); err == nil {
		t.Fatal("ListBySpace succeeded on corrupt store; want error")
	}
	if _, _, err := ds.ClaimNextInLane("sp", "p1", "a1", "worker-a", dtBase.Add(2*time.Second), dtTTL); err == nil {
		t.Fatal("ClaimNextInLane succeeded on corrupt store; want error")
	}
}

func TestDeliveryStoreCreateIfAbsentRejectsBlankStableKey(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	// Missing AgentID => degenerate stable key => rejected before any write.
	bad := seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "")
	if _, created, err := ds.CreateIfAbsent(bad, dtBase); !errors.Is(err, delivery.ErrInvalid) || created {
		t.Fatalf("blank-key create: created=%v err=%v, want ErrInvalid", created, err)
	}
}

// Iris P1-1: a caller-supplied ID that already belongs to a DIFFERENT stable key
// must not be overwritten by create-if-absent. Same ID + different key => hard
// conflict, and the original record is left intact.
func TestDeliveryStoreCreateIfAbsentRejectsForeignIDReuse(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()

	keyA := seedDelivery(delivery.KindChannelWake, "sp", "p1", "oA", "a1")
	keyA.ID = "shared-id"
	first, created, err := ds.CreateIfAbsent(keyA, dtBase)
	if err != nil || !created || first.ID != "shared-id" {
		t.Fatalf("seed key-A: id=%q created=%v err=%v", first.ID, created, err)
	}

	// Same ID, DIFFERENT stable key (distinct origin) -> must conflict.
	keyB := seedDelivery(delivery.KindChannelWake, "sp", "p1", "oB", "a1")
	keyB.ID = "shared-id"
	if _, created, err := ds.CreateIfAbsent(keyB, dtBase.Add(time.Second)); !errors.Is(err, delivery.ErrIDConflict) || created {
		t.Fatalf("foreign-id reuse: created=%v err=%v, want ErrIDConflict", created, err)
	}

	// The original key-A record is untouched (origin still oA).
	reloaded, _ := ds.Get("shared-id")
	if reloaded == nil || reloaded.OriginMessageID != "oA" {
		t.Fatalf("key-A record clobbered: %+v", reloaded)
	}

	// Re-presenting the SAME id with the SAME key is still idempotent (not a
	// conflict) — it resolves to the existing record.
	again, created, err := ds.CreateIfAbsent(keyA, dtBase.Add(2*time.Second))
	if err != nil || created || again.ID != "shared-id" {
		t.Fatalf("same-id same-key replay: id=%q created=%v err=%v", again.ID, created, err)
	}
}

// Iris P1-2: lane FIFO must follow the store-assigned monotonic Seq, not
// CreatedAt, so equal timestamps (same fake-clock instant) still yield a
// deterministic append order. Enqueue three at the SAME instant and assert they
// are claimed in creation (Seq) order.
func TestDeliveryStoreLaneFIFOByEqualTimestampSeq(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	// All three share the exact same timestamp.
	d1, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "a1"), dtBase)
	d2, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o2", "a1"), dtBase)
	d3, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o3", "a1"), dtBase)

	// Seq must be strictly increasing in creation order.
	if !(d1.Seq < d2.Seq && d2.Seq < d3.Seq) {
		t.Fatalf("Seq not monotonic across equal timestamps: %d,%d,%d", d1.Seq, d2.Seq, d3.Seq)
	}

	want := []string{d1.ID, d2.ID, d3.ID}
	for i, wantID := range want {
		got, fence, err := ds.ClaimNextInLane("sp", "p1", "a1", "worker", dtBase.Add(time.Duration(i+1)*time.Second), dtTTL)
		if err != nil {
			t.Fatalf("lane claim %d: %v", i, err)
		}
		if got.ID != wantID {
			t.Fatalf("claim %d = %q, want %q (equal-timestamp FIFO must follow Seq)", i, got.ID, wantID)
		}
		if _, err := ds.Complete(got.ID, fence, "", dtBase.Add(time.Duration(i+1)*time.Second+100*time.Millisecond)); err != nil {
			t.Fatalf("complete %d: %v", i, err)
		}
	}
}

// Iris lane decision: a failed head must NOT block a later pending in the same
// lane, and must NOT be auto-claimed. The worker skips failed.
func TestDeliveryStoreLaneSkipsFailedHead(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	d1, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "a1"), dtBase)
	d2, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o2", "a1"), dtBase.Add(time.Second))

	// Claim + fail the head (d1).
	_, fence, err := ds.ClaimNextInLane("sp", "p1", "a1", "worker-a", dtBase.Add(2*time.Second), dtTTL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ds.Fail(d1.ID, fence, "boom", dtBase.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	// The lane must advance to the later pending d2, skipping the failed head.
	got, _, err := ds.ClaimNextInLane("sp", "p1", "a1", "worker-b", dtBase.Add(4*time.Second), dtTTL)
	if err != nil {
		t.Fatalf("lane claim past failed head: %v", err)
	}
	if got.ID != d2.ID {
		t.Fatalf("lane claimed %q, want later pending %q (failed head must be skipped)", got.ID, d2.ID)
	}
}

// Iris lane decision, requeue branch: after the failed head is requeued and the
// lane has no active lease, the requeued (older CreatedAt) delivery re-enters at
// its original FIFO position, so it is claimed before the later pending.
func TestDeliveryStoreLaneRequeuedHeadKeepsFIFOPosition(t *testing.T) {
	s := newStoreFor(t)
	ds := s.Deliveries()
	d1, _, _ := ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o1", "a1"), dtBase)
	ds.CreateIfAbsent(seedDelivery(delivery.KindChannelWake, "sp", "p1", "o2", "a1"), dtBase.Add(time.Second))

	_, fence, _ := ds.ClaimNextInLane("sp", "p1", "a1", "worker-a", dtBase.Add(2*time.Second), dtTTL)
	if _, err := ds.Fail(d1.ID, fence, "boom", dtBase.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Requeue the failed head; no active lease in the lane now.
	if _, err := ds.Requeue(d1.ID, dtBase.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	// FIFO uses CreatedAt (unchanged by requeue), so d1 (older) is claimed first.
	got, _, err := ds.ClaimNextInLane("sp", "p1", "a1", "worker-b", dtBase.Add(5*time.Second), dtTTL)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != d1.ID {
		t.Fatalf("requeued head claimed %q, want original FIFO head %q", got.ID, d1.ID)
	}
}
