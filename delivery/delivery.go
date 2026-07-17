package delivery

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Status is the durable-delivery execution lifecycle. It is intentionally
// distinct from task.Status: a Delivery tracks one recoverable execution
// attempt, not a user-visible commitment.
type Status string

const (
	StatusPending   Status = "pending"
	StatusLeased    Status = "leased"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

// Kind identifies which routed collaboration path produced the Delivery.
type Kind string

const (
	KindChannelWake   Kind = "channel_wake"
	KindThreadWake    Kind = "thread_wake"
	KindAsyncDelegate Kind = "async_delegate"
)

var (
	ErrAlreadyCompleted = errors.New("delivery: already completed")
	ErrTerminal         = errors.New("delivery: terminal state")
	ErrLeaseHeld        = errors.New("delivery: lease held by active owner")
	ErrStaleLease       = errors.New("delivery: stale lease fenced")
	ErrNotLeased        = errors.New("delivery: not in leased state")
	ErrNotClaimable     = errors.New("delivery: not claimable")
	ErrNotFound         = errors.New("delivery: not found")
	ErrLaneBusy         = errors.New("delivery: lane busy")
	ErrInvalid          = errors.New("delivery: invalid record")
	ErrResultConflict   = errors.New("delivery: result message already bound to a different id")
	ErrIDConflict       = errors.New("delivery: id already used by a different record")
)

// keySep is a control character that cannot appear in the id-shaped fields of a
// stable/lane key, so joined keys stay unambiguous.
const keySep = "\x1f"

// Lease is the temporary execution grant for a Delivery. Version fences a
// claim: only the holder of the current Version may renew, complete, or fail.
// A lease is NOT an assignment of responsibility — it is a short-lived
// execution right that may expire and be reclaimed by another worker.
type Lease struct {
	OwnerID   string    `json:"owner_id"`
	ExpiresAt time.Time `json:"expires_at"`
	Version   int64     `json:"version"`
}

// Fence is the token a worker must present to mutate a leased Delivery. A
// completion or failure carrying a Version older than the Delivery's current
// lease Version is rejected (ErrStaleLease), so a worker whose lease already
// expired and was reclaimed cannot finalize over the new owner.
type Fence struct {
	OwnerID string `json:"owner_id"`
	Version int64  `json:"version"`
}

// Delivery is one recoverable execution attempt for a routed collaboration.
// The stable key (Kind+SpaceID+ParentMessageID+OriginMessageID+AgentID) makes
// enqueue create-if-absent idempotent: the same mention/delegate replayed after
// a crash resolves to the same Delivery, so at most one visible reply results.
type Delivery struct {
	ID string `json:"id"`

	// Stable idempotency key components.
	Kind            Kind   `json:"kind"`
	SpaceID         string `json:"space_id"`
	ParentMessageID string `json:"parent_message_id,omitempty"`
	OriginMessageID string `json:"origin_message_id"`
	AgentID         string `json:"agent_id"`

	// TaskID links an async_delegate Delivery to its user-visible Task. The
	// Delivery references the Task, never the reverse.
	TaskID string `json:"task_id,omitempty"`

	// Seq is a store-assigned, persistent, strictly monotonic creation sequence
	// (assigned under the store's global lock in CreateIfAbsent). It is the
	// authoritative FIFO order for a lane: CreatedAt can tie under a fake clock
	// or a single append batch, and directory-walk order is not fact order, so
	// lane scheduling sorts by Seq. Requeue/retry never change it, so a requeued
	// head keeps its original FIFO position.
	Seq int64 `json:"seq"`

	Status  Status `json:"status"`
	Lease   *Lease `json:"lease,omitempty"`
	Attempt int    `json:"attempt"`

	// LastFence records the fence of the most recent terminal transition
	// (Complete/Fail). A terminal-state replay is idempotent ONLY when the
	// presented fence equals LastFence; any other fence is rejected, so a
	// reclaimed old owner cannot mask itself as the finalizer. Requeue clears it.
	LastFence *Fence `json:"last_fence,omitempty"`

	// ResultMessageID is the stable assistant placeholder message this Delivery
	// finalizes. It is assigned append-once on first claim and reused across
	// retries, so completed/failed/retried all project onto one visible message.
	ResultMessageID string `json:"result_message_id,omitempty"`
	Error           string `json:"error,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewID returns a delivery-tagged identifier, matching the task/run scheme.
func NewID() string {
	return "dlv-" + uuid.NewString()[:8]
}

// StableKey is the idempotency key used by CreateIfAbsent. Two enqueues that
// share these five fields resolve to the same Delivery.
func StableKey(kind Kind, spaceID, parentMessageID, originMessageID, agentID string) string {
	return strings.Join([]string{
		string(kind),
		strings.TrimSpace(spaceID),
		strings.TrimSpace(parentMessageID),
		strings.TrimSpace(originMessageID),
		strings.TrimSpace(agentID),
	}, keySep)
}

// StableKey returns the idempotency key for this Delivery.
func (d *Delivery) StableKey() string {
	return StableKey(d.Kind, d.SpaceID, d.ParentMessageID, d.OriginMessageID, d.AgentID)
}

// LaneKey identifies the FIFO/single-execution lane for a Delivery. All
// deliveries for the same (space, parent, agent) run one at a time and in
// order, so a single Persona's replies under one parent never interleave. It
// mirrors the pre-existing wakeQueueKey (app/channel_wake_pipeline.go).
func LaneKey(spaceID, parentMessageID, agentID string) string {
	return strings.Join([]string{
		strings.TrimSpace(spaceID),
		strings.TrimSpace(parentMessageID),
		strings.TrimSpace(agentID),
	}, keySep)
}

// LaneKey returns the lane key for this Delivery.
func (d *Delivery) LaneKey() string {
	return LaneKey(d.SpaceID, d.ParentMessageID, d.AgentID)
}

// Validate rejects records whose stable-key facts are blank. A Delivery with an
// empty Kind/SpaceID/OriginMessageID/AgentID would produce a degenerate stable
// key and let unrelated empty facts collide in the durable store, so such a
// record must never be persisted.
func (d *Delivery) Validate() error {
	if strings.TrimSpace(string(d.Kind)) == "" ||
		strings.TrimSpace(d.SpaceID) == "" ||
		strings.TrimSpace(d.OriginMessageID) == "" ||
		strings.TrimSpace(d.AgentID) == "" {
		return ErrInvalid
	}
	return nil
}

// Terminal reports whether the Delivery has reached an end state that no claim
// or requeue may revive (completed/canceled). failed is deliberately NOT
// terminal here: an explicit Retry requeues a failed Delivery.
func (s Status) Terminal() bool {
	return s == StatusCompleted || s == StatusCanceled
}

// leaseActive reports whether d holds a lease that has not expired as of now.
func (d *Delivery) leaseActive(now time.Time) bool {
	return d.Lease != nil && now.Before(d.Lease.ExpiresAt)
}

// claimable reports whether d may be claimed as of now. Only two states are
// claimable: pending, or leased with an expired lease (reclaim). failed is NOT
// claimable — it must be explicitly Requeued first — and terminal states never
// are. This keeps a failed Delivery out of the automatic worker/lane pipeline.
func (d *Delivery) claimable(now time.Time) bool {
	switch d.Status {
	case StatusPending:
		return true
	case StatusLeased:
		return !d.leaseActive(now)
	default:
		return false
	}
}

// fenceMatches reports whether the presented fence owns the current lease.
func (d *Delivery) fenceMatches(f Fence) bool {
	return d.Lease != nil &&
		d.Lease.OwnerID == f.OwnerID &&
		d.Lease.Version == f.Version
}

// OwnsLiveLease reports whether f is the authoritative current owner of this
// Delivery as of now: it holds the live lease AND that lease has not expired.
// It is the write-side authority predicate that gates a routed worker's Space
// finalize — evaluated inside the SAME store critical section as the Space byte
// write, so a superseded worker (whose fence was reclaimed by a newer Claim,
// which bumps the lease version) can never land content over the new owner. The
// strict expiry check matches Complete/Fail/Renew: an expired-but-unreclaimed
// lease is NOT authoritative and must re-Claim before writing.
func (d *Delivery) OwnsLiveLease(f Fence, now time.Time) bool {
	return d.fenceMatches(f) && d.leaseActive(now)
}

// Claim grants ownerID a fresh lease over a claimable Delivery and returns the
// fence the owner must present for subsequent renew/complete/fail. Attempt is
// incremented ONLY here — an explicit Requeue does not touch it, so one Retry
// records exactly one additional attempt (at its next successful claim).
func (d *Delivery) Claim(ownerID string, now time.Time, leaseTTL time.Duration) (Fence, error) {
	if !d.claimable(now) {
		switch d.Status {
		case StatusLeased:
			// still-active lease held by someone
			return Fence{}, ErrLeaseHeld
		default:
			// completed/canceled/failed: not claimable (failed needs Requeue)
			return Fence{}, ErrNotClaimable
		}
	}
	// Version is a Delivery-level monotonic value derived from the attempt count,
	// NOT from the (clearable) Lease. Fail()/Requeue() do not touch Attempt, so a
	// same-owner retry gets a strictly higher version and the previous attempt's
	// fence can never re-match a later lease.
	d.Attempt++
	version := int64(d.Attempt)
	d.Status = StatusLeased
	d.Lease = &Lease{
		OwnerID:   ownerID,
		ExpiresAt: now.Add(leaseTTL),
		Version:   version,
	}
	d.UpdatedAt = now
	return Fence{OwnerID: ownerID, Version: version}, nil
}

// Renew extends the current lease. The presented fence must match the live
// lease; a fence from a lease that was already reclaimed by another worker is
// rejected (ErrStaleLease). Renew keeps the same Version, so a long-running LLM
// turn holds its lane rather than being reclaimed mid-flight.
func (d *Delivery) Renew(f Fence, now time.Time, leaseTTL time.Duration) error {
	if d.Status != StatusLeased {
		return ErrNotLeased
	}
	if !d.fenceMatches(f) {
		return ErrStaleLease
	}
	// A lease that already expired cannot be renewed: the owner must re-Claim
	// (which bumps attempt/version) to reclaim. Renewing a dead lease would let a
	// stalled worker resurrect its grant past the reclaim window.
	if !d.leaseActive(now) {
		return ErrStaleLease
	}
	d.Lease.ExpiresAt = now.Add(leaseTTL)
	d.UpdatedAt = now
	return nil
}

// Complete finalizes a leased Delivery onto resultMessageID. A repeat complete
// on an already-completed Delivery is idempotent (append-once: nothing new is
// written). A complete presenting a fence that no longer owns the live lease is
// rejected (ErrStaleLease) — a reclaimed old owner cannot finalize over the new
// one.
func (d *Delivery) Complete(f Fence, resultMessageID string, now time.Time) error {
	switch d.Status {
	case StatusCompleted:
		// Idempotent ONLY for the fence that actually finalized it; any other
		// fence (e.g. a reclaimed old owner) is rejected.
		if d.fenceIsLastTerminal(f) {
			return nil
		}
		return ErrStaleLease
	case StatusCanceled:
		return ErrTerminal
	case StatusLeased:
		if !d.fenceMatches(f) {
			return ErrStaleLease
		}
		if !d.leaseActive(now) {
			// Expired lease cannot finalize; owner must reclaim via new Claim.
			return ErrStaleLease
		}
		d.Status = StatusCompleted
		if strings.TrimSpace(resultMessageID) != "" {
			d.ResultMessageID = resultMessageID
		}
		d.Error = ""
		d.Lease = nil
		d.recordTerminalFence(f)
		d.UpdatedAt = now
		return nil
	default:
		return ErrNotLeased
	}
}

// Fail moves a leased Delivery to the failed state with a user-visible error.
// failed is non-terminal: an explicit Requeue can revive it. Fence rules match
// Complete. Failing an already-completed Delivery is rejected.
func (d *Delivery) Fail(f Fence, errStr string, now time.Time) error {
	switch d.Status {
	case StatusCompleted:
		return ErrAlreadyCompleted
	case StatusCanceled:
		return ErrTerminal
	case StatusFailed:
		// Idempotent ONLY for the fence that recorded the failure.
		if d.fenceIsLastTerminal(f) {
			return nil
		}
		return ErrStaleLease
	case StatusLeased:
		if !d.fenceMatches(f) {
			return ErrStaleLease
		}
		if !d.leaseActive(now) {
			return ErrStaleLease
		}
		d.Status = StatusFailed
		d.Error = strings.TrimSpace(errStr)
		d.Lease = nil
		d.recordTerminalFence(f)
		d.UpdatedAt = now
		return nil
	default:
		return ErrNotLeased
	}
}

// Requeue is the explicit-Retry transition: failed -> pending. It clears only
// lease/fence/error and preserves ResultMessageID so the retry reuses the same
// visible message. Attempt is intentionally NOT incremented here (the next
// successful Claim does that). Completed/canceled cannot be requeued.
func (d *Delivery) Requeue(now time.Time) error {
	switch d.Status {
	case StatusFailed:
		d.Status = StatusPending
		d.Lease = nil
		d.Error = ""
		d.LastFence = nil
		d.UpdatedAt = now
		return nil
	case StatusPending:
		return nil
	case StatusCompleted:
		return ErrAlreadyCompleted
	default:
		return ErrNotClaimable
	}
}

// Cancel moves a non-completed Delivery to the terminal canceled state. It is
// valid from pending/leased/failed and is idempotent on already-canceled; a
// completed Delivery cannot be canceled. Unlike Complete/Fail this is an
// operator/system action, so it does not require the live lease fence — a
// canceled Delivery simply leaves the pipeline. It clears any lease so a
// lingering owner's later finalize is fenced by the terminal state.
func (d *Delivery) Cancel(now time.Time) error {
	switch d.Status {
	case StatusCanceled:
		return nil
	case StatusCompleted:
		return ErrAlreadyCompleted
	case StatusPending, StatusLeased, StatusFailed:
		d.Status = StatusCanceled
		d.Lease = nil
		d.LastFence = nil
		d.UpdatedAt = now
		return nil
	default:
		return ErrNotClaimable
	}
}

// BindResultMessage fence-binds the stable assistant placeholder id append-once.
// It requires the live, unexpired lease fence; the first non-empty id sticks and
// is returned on every later call that presents the SAME id. Presenting a
// DIFFERENT non-empty id once one is bound is a conflict (ErrResultConflict) —
// silently keeping the old id would let a second placeholder be dropped without
// the caller noticing. A stale/expired fence or a non-leased state is rejected,
// so only the current owner can establish the visible message a Delivery
// finalizes onto.
func (d *Delivery) BindResultMessage(f Fence, id string, now time.Time) (string, error) {
	if d.Status != StatusLeased {
		return "", ErrNotLeased
	}
	if !d.fenceMatches(f) {
		return "", ErrStaleLease
	}
	if !d.leaseActive(now) {
		return "", ErrStaleLease
	}
	id = strings.TrimSpace(id)
	if d.ResultMessageID == "" {
		if id != "" {
			d.ResultMessageID = id
			d.UpdatedAt = now
		}
		return d.ResultMessageID, nil
	}
	// Already bound: an empty probe reads it back; a matching id is a no-op; a
	// different non-empty id is a hard conflict rather than a silent drop.
	if id != "" && id != d.ResultMessageID {
		return d.ResultMessageID, ErrResultConflict
	}
	return d.ResultMessageID, nil
}

// recordTerminalFence stores the fence that drove a terminal transition so a
// later same-fence replay is idempotent while any other fence is rejected.
func (d *Delivery) recordTerminalFence(f Fence) {
	fc := f
	d.LastFence = &fc
}

// fenceIsLastTerminal reports whether f equals the fence of the last terminal
// transition. A zero/absent LastFence never matches, so unknown fences fail.
func (d *Delivery) fenceIsLastTerminal(f Fence) bool {
	return d.LastFence != nil && *d.LastFence == f
}
