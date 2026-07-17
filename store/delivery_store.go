package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/delivery"
)

// DeliveryStore persists durable-delivery records as one JSON file per Delivery,
// following the same atomic-write pattern as task_store. All atomic CAS
// operations (Claim/Complete/Fail/Renew/Requeue) hold the Store mutex across the
// full load-mutate-save so a lease grant or fence check cannot race a concurrent
// worker. Clock values (now, leaseTTL) are passed in so tests drive a fake clock
// rather than sleeping.
type DeliveryStore struct {
	s *Store
}

// Deliveries returns the delivery-scoped view of the Store.
func (s *Store) Deliveries() *DeliveryStore {
	return &DeliveryStore{s: s}
}

func (ds *DeliveryStore) path(id string) string {
	return filepath.Join(ds.s.deliveriesDir, strings.TrimSpace(id)+".json")
}

func (ds *DeliveryStore) saveLocked(d *delivery.Delivery) error {
	if d == nil {
		return fmt.Errorf("delivery is nil")
	}
	if strings.TrimSpace(d.ID) == "" {
		return fmt.Errorf("delivery id is empty")
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(ds.path(d.ID), append(data, '\n'))
}

func (ds *DeliveryStore) loadLocked(id string) (*delivery.Delivery, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("delivery id is empty")
	}
	path := ds.path(id)
	if !fileExists(path) {
		return nil, nil
	}
	data, err := readFile(path)
	if err != nil {
		return nil, err
	}
	var d delivery.Delivery
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// allLocked lists every persisted Delivery. It is FAIL-CLOSE: a read or
// unmarshal error aborts the whole listing rather than silently skipping the
// bad file. For a durable idempotency store, skipping a corrupt record would
// hide an existing stable key and let CreateIfAbsent mint a duplicate Delivery,
// so a corrupt store must block writes until repaired, not fail open.
func (ds *DeliveryStore) allLocked() ([]*delivery.Delivery, error) {
	out := make([]*delivery.Delivery, 0)
	err := walkDirJSON(ds.s.deliveriesDir, func(path string) error {
		data, err := readFile(path)
		if err != nil {
			return fmt.Errorf("delivery store: read %s: %w", path, err)
		}
		var d delivery.Delivery
		if err := json.Unmarshal(data, &d); err != nil {
			return fmt.Errorf("delivery store: unmarshal %s: %w", path, err)
		}
		out = append(out, &d)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// scanLocked lists every persisted Delivery once and reports, for a candidate
// stable key and id, the matching stable-key record, whether that id is already
// taken by a DIFFERENT record, and the max Seq seen (for monotonic allocation).
func (ds *DeliveryStore) scanLocked(stableKey, id string) (match *delivery.Delivery, idTaken bool, maxSeq int64, err error) {
	all, err := ds.allLocked()
	if err != nil {
		return nil, false, 0, err
	}
	id = strings.TrimSpace(id)
	for _, d := range all {
		if d.Seq > maxSeq {
			maxSeq = d.Seq
		}
		if d.StableKey() == stableKey {
			match = d
		}
		if id != "" && d.ID == id {
			idTaken = true
		}
	}
	return match, idTaken, maxSeq, nil
}

// CreateIfAbsent resolves the stable key to an existing Delivery or creates a
// new pending one. created reports whether a new record was written. This is the
// idempotent enqueue: the same routed intent replayed after a crash returns the
// original Delivery instead of a duplicate.
//
// A caller-supplied ID is honored only if it is not already used by a record
// with a DIFFERENT stable key; otherwise ErrIDConflict is returned rather than
// clobbering that other fact. A store-assigned monotonic Seq is allocated under
// the same global lock so lane FIFO has an authoritative order independent of
// timestamp ties or directory-walk order.
func (ds *DeliveryStore) CreateIfAbsent(d *delivery.Delivery, now time.Time) (*delivery.Delivery, bool, error) {
	if d == nil {
		return nil, false, fmt.Errorf("delivery is nil")
	}
	if err := d.Validate(); err != nil {
		return nil, false, err
	}
	ds.s.mu.Lock()
	defer ds.s.mu.Unlock()
	existing, idTaken, maxSeq, err := ds.scanLocked(d.StableKey(), d.ID)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}
	// No stable-key match: this is a new record. A caller-supplied ID that
	// already belongs to a different-key record must not be overwritten.
	if idTaken {
		return nil, false, delivery.ErrIDConflict
	}
	created := *d
	if strings.TrimSpace(created.ID) == "" {
		created.ID = delivery.NewID()
	}
	created.Seq = maxSeq + 1
	created.Status = delivery.StatusPending
	created.Lease = nil
	created.Attempt = 0
	created.CreatedAt = now
	created.UpdatedAt = now
	if err := ds.saveLocked(&created); err != nil {
		return nil, false, err
	}
	return &created, true, nil
}

// Get loads a Delivery by ID; returns (nil, nil) if absent.
func (ds *DeliveryStore) Get(id string) (*delivery.Delivery, error) {
	ds.s.mu.Lock()
	defer ds.s.mu.Unlock()
	return ds.loadLocked(id)
}

// Claim atomically grants a lease over the identified Delivery.
func (ds *DeliveryStore) Claim(id, ownerID string, now time.Time, leaseTTL time.Duration) (*delivery.Delivery, delivery.Fence, error) {
	ds.s.mu.Lock()
	defer ds.s.mu.Unlock()
	d, err := ds.loadLocked(id)
	if err != nil {
		return nil, delivery.Fence{}, err
	}
	if d == nil {
		return nil, delivery.Fence{}, delivery.ErrNotFound
	}
	fence, err := d.Claim(ownerID, now, leaseTTL)
	if err != nil {
		return nil, delivery.Fence{}, err
	}
	if err := ds.saveLocked(d); err != nil {
		return nil, delivery.Fence{}, err
	}
	return d, fence, nil
}

// ClaimNextInLane enforces FIFO single-execution per (space,parent,agent) lane:
// if any Delivery in the lane holds an active (unexpired) lease, the lane is
// busy and ErrLaneBusy is returned; otherwise the oldest claimable Delivery in
// the lane is claimed. This keeps one Persona's replies under a parent ordered.
func (ds *DeliveryStore) ClaimNextInLane(spaceID, parentMessageID, agentID, ownerID string, now time.Time, leaseTTL time.Duration) (*delivery.Delivery, delivery.Fence, error) {
	laneKey := delivery.LaneKey(spaceID, parentMessageID, agentID)
	ds.s.mu.Lock()
	defer ds.s.mu.Unlock()
	all, err := ds.allLocked()
	if err != nil {
		return nil, delivery.Fence{}, err
	}
	lane := make([]*delivery.Delivery, 0)
	for _, d := range all {
		if d.LaneKey() != laneKey {
			continue
		}
		if d.Status == delivery.StatusLeased && now.Before(leaseExpiry(d)) {
			return nil, delivery.Fence{}, delivery.ErrLaneBusy
		}
		lane = append(lane, d)
	}
	// FIFO by the store-assigned monotonic Seq, not CreatedAt: timestamps can tie
	// under a fake clock or a single append batch, and directory-walk order is
	// not fact order. Requeue never changes Seq, so a requeued head keeps its
	// original position.
	sort.SliceStable(lane, func(i, j int) bool {
		return lane[i].Seq < lane[j].Seq
	})
	for _, d := range lane {
		fence, err := d.Claim(ownerID, now, leaseTTL)
		if err != nil {
			continue
		}
		if err := ds.saveLocked(d); err != nil {
			return nil, delivery.Fence{}, err
		}
		return d, fence, nil
	}
	return nil, delivery.Fence{}, delivery.ErrNotClaimable
}

// Renew extends the lease of a claimed Delivery under fence.
func (ds *DeliveryStore) Renew(id string, fence delivery.Fence, now time.Time, leaseTTL time.Duration) (*delivery.Delivery, error) {
	return ds.mutateLocked(id, func(d *delivery.Delivery) error {
		return d.Renew(fence, now, leaseTTL)
	})
}

// Complete finalizes a Delivery onto resultMessageID under fence (append-once).
func (ds *DeliveryStore) Complete(id string, fence delivery.Fence, resultMessageID string, now time.Time) (*delivery.Delivery, error) {
	return ds.mutateLocked(id, func(d *delivery.Delivery) error {
		return d.Complete(fence, resultMessageID, now)
	})
}

// Fail records a user-visible failure under fence; the Delivery stays requeuable.
func (ds *DeliveryStore) Fail(id string, fence delivery.Fence, errStr string, now time.Time) (*delivery.Delivery, error) {
	return ds.mutateLocked(id, func(d *delivery.Delivery) error {
		return d.Fail(fence, errStr, now)
	})
}

// Requeue is the explicit-Retry transition failed -> pending (same message reused).
func (ds *DeliveryStore) Requeue(id string, now time.Time) (*delivery.Delivery, error) {
	return ds.mutateLocked(id, func(d *delivery.Delivery) error {
		return d.Requeue(now)
	})
}

// Cancel moves a Delivery to the terminal canceled state (operator/system action).
func (ds *DeliveryStore) Cancel(id string, now time.Time) (*delivery.Delivery, error) {
	return ds.mutateLocked(id, func(d *delivery.Delivery) error {
		return d.Cancel(now)
	})
}

// BindResultMessage fence-binds the stable placeholder message id append-once
// under the live lease. Used by the worker at first claim to persist the visible
// assistant message a Delivery finalizes onto.
func (ds *DeliveryStore) BindResultMessage(id string, fence delivery.Fence, msgID string, now time.Time) (*delivery.Delivery, string, error) {
	ds.s.mu.Lock()
	defer ds.s.mu.Unlock()
	d, err := ds.loadLocked(id)
	if err != nil {
		return nil, "", err
	}
	if d == nil {
		return nil, "", delivery.ErrNotFound
	}
	bound, err := d.BindResultMessage(fence, msgID, now)
	if err != nil {
		return nil, "", err
	}
	if err := ds.saveLocked(d); err != nil {
		return nil, "", err
	}
	return d, bound, nil
}

// mutateLocked loads, applies fn, and saves a Delivery under the Store mutex.
// The domain transition itself enforces which states/fences are legal.
func (ds *DeliveryStore) mutateLocked(id string, fn func(*delivery.Delivery) error) (*delivery.Delivery, error) {
	ds.s.mu.Lock()
	defer ds.s.mu.Unlock()
	d, err := ds.loadLocked(id)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, delivery.ErrNotFound
	}
	if err := fn(d); err != nil {
		return nil, err
	}
	if err := ds.saveLocked(d); err != nil {
		return nil, err
	}
	return d, nil
}

// ListBySpace returns all deliveries for a Space (used by startup reconcile).
func (ds *DeliveryStore) ListBySpace(spaceID string) ([]*delivery.Delivery, error) {
	spaceID = strings.TrimSpace(spaceID)
	ds.s.mu.Lock()
	defer ds.s.mu.Unlock()
	all, err := ds.allLocked()
	if err != nil {
		return nil, err
	}
	out := make([]*delivery.Delivery, 0, len(all))
	for _, d := range all {
		if spaceID != "" && d.SpaceID != spaceID {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

// ListLeasedByOwner returns leased deliveries whose live lease is held by
// ownerID (the worker/lease holder), for lease recovery on that owner. This
// filters by Lease.OwnerID, NOT the target Delivery.AgentID — the two are
// distinct: AgentID is who the reply is from, ownerID is who currently executes.
func (ds *DeliveryStore) ListLeasedByOwner(ownerID string) ([]*delivery.Delivery, error) {
	ownerID = strings.TrimSpace(ownerID)
	ds.s.mu.Lock()
	defer ds.s.mu.Unlock()
	all, err := ds.allLocked()
	if err != nil {
		return nil, err
	}
	out := make([]*delivery.Delivery, 0)
	for _, d := range all {
		if d.Status != delivery.StatusLeased || d.Lease == nil {
			continue
		}
		if ownerID != "" && d.Lease.OwnerID != ownerID {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

// Lane identifies one (space, parent, agent) FIFO execution lane.
type Lane struct {
	SpaceID         string
	ParentMessageID string
	AgentID         string
}

// ClaimableLanes returns the distinct lanes that currently hold at least one
// claimable Delivery (pending, or leased with an expired lease) and NO active
// lease. It is the worker's fallback-sweep discovery primitive: a lane returned
// here is safe to attempt ClaimNextInLane on without hot-looping on ErrLaneBusy,
// and a lane with only terminal/failed members is omitted so the sweep does not
// spin on lanes that need an explicit Requeue. This is a read-only snapshot; an
// interleaving claim just turns a later ClaimNextInLane into ErrLaneBusy/
// ErrNotClaimable, which the worker already tolerates.
func (ds *DeliveryStore) ClaimableLanes(now time.Time) ([]Lane, error) {
	ds.s.mu.Lock()
	defer ds.s.mu.Unlock()
	all, err := ds.allLocked()
	if err != nil {
		return nil, err
	}
	type laneState struct {
		lane      Lane
		claimable bool
		busy      bool
	}
	byKey := map[string]*laneState{}
	order := make([]string, 0)
	for _, d := range all {
		key := d.LaneKey()
		st := byKey[key]
		if st == nil {
			st = &laneState{lane: Lane{SpaceID: d.SpaceID, ParentMessageID: d.ParentMessageID, AgentID: d.AgentID}}
			byKey[key] = st
			order = append(order, key)
		}
		if d.Status == delivery.StatusLeased && now.Before(leaseExpiry(d)) {
			st.busy = true
			continue
		}
		switch d.Status {
		case delivery.StatusPending:
			st.claimable = true
		case delivery.StatusLeased:
			// lease present but expired (guarded above) => reclaimable
			st.claimable = true
		}
	}
	out := make([]Lane, 0, len(order))
	for _, key := range order {
		st := byKey[key]
		if st.busy || !st.claimable {
			continue
		}
		out = append(out, st.lane)
	}
	return out, nil
}

func leaseExpiry(d *delivery.Delivery) time.Time {
	if d.Lease == nil {
		return time.Time{}
	}
	return d.Lease.ExpiresAt
}
