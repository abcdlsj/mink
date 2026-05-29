package space

import (
	"sync"
	"time"
)

// DefaultRoutingBudget is the maximum number of agent replies a
// single user-initiated routing chain may produce. Per Iris and
// lsoooj this is "soft" — exhausting it surfaces a notice but is
// never an error. The number can be tuned later via config.
const DefaultRoutingBudget = 3

// RoutingChain tracks one user-initiated wake-up cascade.
// A chain begins when a user message with mentions is accepted and
// continues across whatever agent replies that message kicks off.
// The root message id is the natural identifier — same root, same
// chain — and the next user message starts a fresh chain regardless
// of the Space.
//
// Chain accounting is in-memory only. A daemon restart resets all
// chains; that's fine because chains are short-lived (one user
// turn) and can't outlive a process.
type RoutingChain struct {
	RootMessageID string
	SpaceID       string
	Budget        int
	StartedAt     time.Time

	repliedAgents map[string]bool
}

// NewRoutingChain produces a chain rooted at rootMessageID with the
// given budget. budget <= 0 falls back to DefaultRoutingBudget so
// callers cannot accidentally start a zero-reply chain.
func NewRoutingChain(rootMessageID, spaceID string, budget int) *RoutingChain {
	if budget <= 0 {
		budget = DefaultRoutingBudget
	}
	return &RoutingChain{
		RootMessageID: rootMessageID,
		SpaceID:       spaceID,
		Budget:        budget,
		StartedAt:     time.Now(),
		repliedAgents: map[string]bool{},
	}
}

// CanWake reports whether agentID is allowed to start a turn under
// this chain. It returns false (and a reason) for either of:
//
//   - the chain has no budget left;
//   - this agent has already replied once in this chain.
func (c *RoutingChain) CanWake(agentID string) (bool, string) {
	if c == nil {
		return false, "no_chain"
	}
	if c.Budget <= 0 {
		return false, "budget_exhausted"
	}
	if c.repliedAgents[agentID] {
		return false, "duplicate_skipped"
	}
	return true, ""
}

// Spend records that agentID is starting a reply under this chain.
// Callers should call Spend once they decide to actually wake the
// agent (i.e. after CanWake returned true). It deducts one budget
// unit and marks the agent as already-replied.
//
// Spend on an exhausted chain or a duplicate agent is a no-op so
// callers cannot accidentally overspend.
func (c *RoutingChain) Spend(agentID string) {
	if c == nil {
		return
	}
	if c.Budget <= 0 {
		return
	}
	if c.repliedAgents[agentID] {
		return
	}
	c.Budget--
	c.repliedAgents[agentID] = true
}

// AlreadyReplied reports whether agentID has spent a turn in this
// chain.
func (c *RoutingChain) AlreadyReplied(agentID string) bool {
	if c == nil {
		return false
	}
	return c.repliedAgents[agentID]
}

// ChainTracker is the in-memory registry of live routing chains.
// It is keyed by root message id; concurrent access is safe.
type ChainTracker struct {
	mu     sync.Mutex
	chains map[string]*RoutingChain
}

func NewChainTracker() *ChainTracker {
	return &ChainTracker{chains: map[string]*RoutingChain{}}
}

// Start creates a chain rooted at rootMessageID and registers it.
// If a chain already exists for that id, the existing one is
// returned unchanged (idempotent).
func (t *ChainTracker) Start(rootMessageID, spaceID string, budget int) *RoutingChain {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c, ok := t.chains[rootMessageID]; ok {
		return c
	}
	c := NewRoutingChain(rootMessageID, spaceID, budget)
	t.chains[rootMessageID] = c
	return c
}

// Get returns the chain for rootMessageID, or nil.
func (t *ChainTracker) Get(rootMessageID string) *RoutingChain {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.chains[rootMessageID]
}

// Drop removes a chain from the tracker (e.g. after a turn budget
// expires or its parent message is GC'd).
func (t *ChainTracker) Drop(rootMessageID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.chains, rootMessageID)
}
