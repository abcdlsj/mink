package delivery

import (
	"testing"
	"time"
)

// fixed base instant; tests advance it explicitly rather than sleeping.
var t0 = time.Date(2026, 7, 17, 4, 0, 0, 0, time.UTC)

const leaseTTL = 30 * time.Second

func pendingDelivery() *Delivery {
	return &Delivery{
		ID:              "dlv-test",
		Kind:            KindChannelWake,
		SpaceID:         "sp-1",
		ParentMessageID: "parent-1",
		OriginMessageID: "origin-1",
		AgentID:         "agent-1",
		Status:          StatusPending,
		CreatedAt:       t0,
		UpdatedAt:       t0,
	}
}

func TestClaimGrantsLeaseAndIncrementsAttempt(t *testing.T) {
	d := pendingDelivery()
	fence, err := d.Claim("worker-a", t0, leaseTTL)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if d.Status != StatusLeased {
		t.Fatalf("status = %q, want leased", d.Status)
	}
	if d.Attempt != 1 {
		t.Fatalf("attempt = %d, want 1", d.Attempt)
	}
	// Version is derived from attempt (Delivery-level monotonic), not the lease.
	if d.Lease == nil || d.Lease.OwnerID != "worker-a" || d.Lease.Version != 1 {
		t.Fatalf("lease = %+v", d.Lease)
	}
	if !d.Lease.ExpiresAt.Equal(t0.Add(leaseTTL)) {
		t.Fatalf("expires = %v", d.Lease.ExpiresAt)
	}
	if fence.OwnerID != "worker-a" || fence.Version != 1 {
		t.Fatalf("fence = %+v", fence)
	}
}

func TestClaimRejectsActiveLease(t *testing.T) {
	d := pendingDelivery()
	if _, err := d.Claim("worker-a", t0, leaseTTL); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Claim("worker-b", t0.Add(5*time.Second), leaseTTL); err != ErrLeaseHeld {
		t.Fatalf("err = %v, want ErrLeaseHeld", err)
	}
}

func TestClaimRejectsCompleted(t *testing.T) {
	d := pendingDelivery()
	fence, _ := d.Claim("worker-a", t0, leaseTTL)
	if err := d.Complete(fence, "msg-1", t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Claim("worker-b", t0.Add(2*time.Second), leaseTTL); err != ErrNotClaimable {
		t.Fatalf("err = %v, want ErrNotClaimable", err)
	}
}

// P0-3: failed is NOT directly claimable; it must be explicitly Requeued first.
func TestClaimRejectsFailedWithoutRequeue(t *testing.T) {
	d := pendingDelivery()
	fence, _ := d.Claim("worker-a", t0, leaseTTL)
	if err := d.Fail(fence, "boom", t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Claim("worker-b", t0.Add(2*time.Second), leaseTTL); err != ErrNotClaimable {
		t.Fatalf("failed claim err = %v, want ErrNotClaimable", err)
	}
	// After explicit Requeue it becomes claimable again.
	if err := d.Requeue(t0.Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Claim("worker-b", t0.Add(4*time.Second), leaseTTL); err != nil {
		t.Fatalf("claim after requeue: %v", err)
	}
}

func TestLeaseExpiryReclaimable(t *testing.T) {
	d := pendingDelivery()
	first, _ := d.Claim("worker-a", t0, leaseTTL)
	second, err := d.Claim("worker-b", t0.Add(leaseTTL+time.Second), leaseTTL)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if second.Version <= first.Version {
		t.Fatalf("version did not advance: %d -> %d", first.Version, second.Version)
	}
	if d.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", d.Attempt)
	}
	// Old owner completion is now fenced.
	if err := d.Complete(first, "msg-old", t0.Add(leaseTTL+2*time.Second)); err != ErrStaleLease {
		t.Fatalf("stale complete err = %v, want ErrStaleLease", err)
	}
}

// P0-1: a same-owner retry must get a strictly higher version, so the first
// attempt's fence can never re-match the second lease.
func TestSameOwnerRetryOldFenceRejected(t *testing.T) {
	d := pendingDelivery()
	first, _ := d.Claim("worker-a", t0, leaseTTL)
	if err := d.Fail(first, "boom", t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := d.Requeue(t0.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	// Same owner re-claims. Version must advance beyond the first fence.
	second, err := d.Claim("worker-a", t0.Add(3*time.Second), leaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	if second.Version == first.Version {
		t.Fatalf("same-owner re-claim reused version %d — fencing broken", first.Version)
	}
	// The stale first fence must not be able to complete the new lease.
	if err := d.Complete(first, "msg-x", t0.Add(4*time.Second)); err != ErrStaleLease {
		t.Fatalf("stale first-fence complete err = %v, want ErrStaleLease", err)
	}
	// The current fence works.
	if err := d.Complete(second, "msg-ok", t0.Add(5*time.Second)); err != nil {
		t.Fatalf("current fence complete: %v", err)
	}
}

func TestRenewPreventsReclaim(t *testing.T) {
	d := pendingDelivery()
	fence, _ := d.Claim("worker-a", t0, leaseTTL)
	if err := d.Renew(fence, t0.Add(20*time.Second), leaseTTL); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if _, err := d.Claim("worker-b", t0.Add(35*time.Second), leaseTTL); err != ErrLeaseHeld {
		t.Fatalf("err = %v, want ErrLeaseHeld (renew should hold lane)", err)
	}
}

func TestRenewStaleFenceRejected(t *testing.T) {
	d := pendingDelivery()
	if _, err := d.Claim("worker-a", t0, leaseTTL); err != nil {
		t.Fatal(err)
	}
	stale := Fence{OwnerID: "worker-a", Version: 0}
	if err := d.Renew(stale, t0.Add(time.Second), leaseTTL); err != ErrStaleLease {
		t.Fatalf("err = %v, want ErrStaleLease", err)
	}
}

// P0-2: an expired lease can be neither renewed, completed, nor failed by its
// (now stale) owner — it must reclaim via a new Claim.
func TestExpiredLeaseRenewCompleteFailAllRejected(t *testing.T) {
	after := t0.Add(leaseTTL + time.Second)

	dR := pendingDelivery()
	fR, _ := dR.Claim("worker-a", t0, leaseTTL)
	if err := dR.Renew(fR, after, leaseTTL); err != ErrStaleLease {
		t.Fatalf("expired renew err = %v, want ErrStaleLease", err)
	}

	dC := pendingDelivery()
	fC, _ := dC.Claim("worker-a", t0, leaseTTL)
	if err := dC.Complete(fC, "msg", after); err != ErrStaleLease {
		t.Fatalf("expired complete err = %v, want ErrStaleLease", err)
	}

	dF := pendingDelivery()
	fF, _ := dF.Claim("worker-a", t0, leaseTTL)
	if err := dF.Fail(fF, "boom", after); err != ErrStaleLease {
		t.Fatalf("expired fail err = %v, want ErrStaleLease", err)
	}
}

func TestCompleteFenceRejectsStaleOwner(t *testing.T) {
	d := pendingDelivery()
	if _, err := d.Claim("worker-a", t0, leaseTTL); err != nil {
		t.Fatal(err)
	}
	stale := Fence{OwnerID: "worker-a", Version: 99}
	if err := d.Complete(stale, "msg-x", t0.Add(time.Second)); err != ErrStaleLease {
		t.Fatalf("err = %v, want ErrStaleLease", err)
	}
	if d.Status != StatusLeased {
		t.Fatalf("status = %q, want still leased", d.Status)
	}
}

// P1-4: after a new owner finalizes, an old owner's terminal-state replay is
// rejected; only the fence that actually finalized replays idempotently.
func TestCompleteIdempotentOnlyForFinalizingFence(t *testing.T) {
	d := pendingDelivery()
	first, _ := d.Claim("worker-a", t0, leaseTTL)
	// worker-a stalls; worker-b reclaims after expiry and completes.
	second, err := d.Claim("worker-b", t0.Add(leaseTTL+time.Second), leaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Complete(second, "msg-b", t0.Add(leaseTTL+2*time.Second)); err != nil {
		t.Fatal(err)
	}
	// The finalizing fence replays as an idempotent no-op, result frozen.
	if err := d.Complete(second, "msg-b2", t0.Add(leaseTTL+3*time.Second)); err != nil {
		t.Fatalf("finalizing-fence replay err = %v, want nil", err)
	}
	if d.ResultMessageID != "msg-b" {
		t.Fatalf("result = %q, want msg-b (immutable)", d.ResultMessageID)
	}
	// The old owner's fence must be rejected, not silently accepted.
	if err := d.Complete(first, "msg-a", t0.Add(leaseTTL+4*time.Second)); err != ErrStaleLease {
		t.Fatalf("old-owner replay err = %v, want ErrStaleLease", err)
	}
}

func TestFailThenRequeueNoAttemptBump(t *testing.T) {
	d := pendingDelivery()
	fence, _ := d.Claim("worker-a", t0, leaseTTL)
	if err := d.Fail(fence, "boom", t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if d.Status != StatusFailed || d.Error != "boom" {
		t.Fatalf("after fail = %+v", d)
	}
	attemptAfterFail := d.Attempt
	if err := d.Requeue(t0.Add(2 * time.Second)); err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	if d.Status != StatusPending || d.Error != "" || d.Lease != nil || d.LastFence != nil {
		t.Fatalf("after requeue = %+v", d)
	}
	if d.Attempt != attemptAfterFail {
		t.Fatalf("requeue bumped attempt %d -> %d", attemptAfterFail, d.Attempt)
	}
	// Only the next successful claim bumps attempt.
	if _, err := d.Claim("worker-b", t0.Add(3*time.Second), leaseTTL); err != nil {
		t.Fatal(err)
	}
	if d.Attempt != attemptAfterFail+1 {
		t.Fatalf("attempt after re-claim = %d, want %d", d.Attempt, attemptAfterFail+1)
	}
}

func TestRequeueRejectsCompleted(t *testing.T) {
	d := pendingDelivery()
	fence, _ := d.Claim("worker-a", t0, leaseTTL)
	if err := d.Complete(fence, "msg-1", t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := d.Requeue(t0.Add(2 * time.Second)); err != ErrAlreadyCompleted {
		t.Fatalf("err = %v, want ErrAlreadyCompleted", err)
	}
}

func TestCancelTransitions(t *testing.T) {
	// pending -> canceled
	dp := pendingDelivery()
	if err := dp.Cancel(t0); err != nil || dp.Status != StatusCanceled {
		t.Fatalf("cancel pending: err=%v status=%q", err, dp.Status)
	}
	// idempotent on canceled
	if err := dp.Cancel(t0.Add(time.Second)); err != nil {
		t.Fatalf("cancel idempotent err = %v", err)
	}
	// canceled is terminal: neither Requeue nor Claim may revive it.
	if err := dp.Requeue(t0.Add(2 * time.Second)); err != ErrNotClaimable {
		t.Fatalf("requeue canceled err = %v, want ErrNotClaimable", err)
	}
	if _, err := dp.Claim("worker-x", t0.Add(3*time.Second), leaseTTL); err != ErrNotClaimable {
		t.Fatalf("claim canceled err = %v, want ErrNotClaimable", err)
	}

	// leased -> canceled clears the lease; the old owner can no longer renew,
	// complete, OR fail — all three must be rejected, not just complete.
	dl := pendingDelivery()
	f, _ := dl.Claim("worker-a", t0, leaseTTL)
	if err := dl.Cancel(t0.Add(time.Second)); err != nil || dl.Status != StatusCanceled {
		t.Fatalf("cancel leased: err=%v status=%q", err, dl.Status)
	}
	if err := dl.Renew(f, t0.Add(2*time.Second), leaseTTL); err != ErrNotLeased {
		t.Fatalf("renew after cancel err = %v, want ErrNotLeased", err)
	}
	if err := dl.Complete(f, "msg", t0.Add(2*time.Second)); err != ErrTerminal {
		t.Fatalf("complete after cancel err = %v, want ErrTerminal", err)
	}
	if err := dl.Fail(f, "boom", t0.Add(2*time.Second)); err != ErrTerminal {
		t.Fatalf("fail after cancel err = %v, want ErrTerminal", err)
	}
	// completed cannot be canceled.
	dc := pendingDelivery()
	fc, _ := dc.Claim("worker-a", t0, leaseTTL)
	dc.Complete(fc, "msg", t0.Add(time.Second))
	if err := dc.Cancel(t0.Add(2 * time.Second)); err != ErrAlreadyCompleted {
		t.Fatalf("cancel completed err = %v, want ErrAlreadyCompleted", err)
	}
}

func TestBindResultMessageFenced(t *testing.T) {
	d := pendingDelivery()
	fence, _ := d.Claim("worker-a", t0, leaseTTL)
	got, err := d.BindResultMessage(fence, "msg-1", t0.Add(time.Second))
	if err != nil || got != "msg-1" {
		t.Fatalf("bind: got=%q err=%v", got, err)
	}
	// The same id re-binds idempotently.
	gotSame, err := d.BindResultMessage(fence, "msg-1", t0.Add(2*time.Second))
	if err != nil || gotSame != "msg-1" {
		t.Fatalf("same-id rebind: got=%q err=%v, want msg-1/nil", gotSame, err)
	}
	// An empty probe reads back the bound id without error.
	gotProbe, err := d.BindResultMessage(fence, "", t0.Add(2*time.Second))
	if err != nil || gotProbe != "msg-1" {
		t.Fatalf("probe: got=%q err=%v, want msg-1/nil", gotProbe, err)
	}
	// A DIFFERENT non-empty id is a hard conflict, not a silent drop.
	gotConflict, err := d.BindResultMessage(fence, "msg-2", t0.Add(2*time.Second))
	if err != ErrResultConflict || gotConflict != "msg-1" {
		t.Fatalf("conflict rebind: got=%q err=%v, want msg-1/ErrResultConflict", gotConflict, err)
	}
	// A stale fence is rejected.
	stale := Fence{OwnerID: "worker-a", Version: 99}
	if _, err := d.BindResultMessage(stale, "msg-x", t0.Add(3*time.Second)); err != ErrStaleLease {
		t.Fatalf("stale bind err = %v, want ErrStaleLease", err)
	}
	// Expired fence is rejected.
	if _, err := d.BindResultMessage(fence, "msg-y", t0.Add(leaseTTL+time.Second)); err != ErrStaleLease {
		t.Fatalf("expired bind err = %v, want ErrStaleLease", err)
	}
}

func TestValidateRejectsBlankStableKey(t *testing.T) {
	cases := map[string]*Delivery{
		"blank kind":    {SpaceID: "s", OriginMessageID: "o", AgentID: "a"},
		"blank space":   {Kind: KindChannelWake, OriginMessageID: "o", AgentID: "a"},
		"blank origin":  {Kind: KindChannelWake, SpaceID: "s", AgentID: "a"},
		"blank agent":   {Kind: KindChannelWake, SpaceID: "s", OriginMessageID: "o"},
		"whitespace":    {Kind: KindChannelWake, SpaceID: "  ", OriginMessageID: "o", AgentID: "a"},
	}
	for name, d := range cases {
		if err := d.Validate(); err != ErrInvalid {
			t.Fatalf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	ok := pendingDelivery()
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid delivery rejected: %v", err)
	}
}

func TestStableKeyAndLaneKey(t *testing.T) {
	d := pendingDelivery()
	other := pendingDelivery()
	other.OriginMessageID = "origin-2"
	if d.StableKey() == other.StableKey() {
		t.Fatal("stable keys collided across distinct origin messages")
	}
	if d.LaneKey() != other.LaneKey() {
		t.Fatalf("lane keys differ: %q vs %q", d.LaneKey(), other.LaneKey())
	}
}
