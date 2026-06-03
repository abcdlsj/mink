package space

import (
	"sync"
	"time"
)

const DefaultRoutingBudget = 3

type RoutingChain struct {
	mu              sync.Mutex
	RootMessageID   string
	ParentMessageID string
	SpaceID         string
	Budget          int
	StartedAt       time.Time
}

func NewRoutingChain(rootMessageID, spaceID string, budget int) *RoutingChain {
	if budget <= 0 {
		budget = DefaultRoutingBudget
	}
	return &RoutingChain{
		RootMessageID: rootMessageID,
		SpaceID:       spaceID,
		Budget:        budget,
		StartedAt:     time.Now(),
	}
}

func (c *RoutingChain) CanWake(agentID string) (bool, string) {
	if c == nil {
		return false, "no_chain"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.canWakeLocked(agentID)
}

func (c *RoutingChain) TrySpend(agentID string) (bool, string) {
	if c == nil {
		return false, "no_chain"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ok, reason := c.canWakeLocked(agentID)
	if !ok {
		return false, reason
	}
	c.Budget--
	return true, ""
}

func (c *RoutingChain) canWakeLocked(agentID string) (bool, string) {
	if c.Budget <= 0 {
		return false, "budget_exhausted"
	}
	return true, ""
}

func (c *RoutingChain) Spend(agentID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Budget <= 0 {
		return
	}
	c.Budget--
}

type ChainTracker struct {
	mu     sync.Mutex
	chains map[string]*RoutingChain
}

func NewChainTracker() *ChainTracker {
	return &ChainTracker{chains: map[string]*RoutingChain{}}
}

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

func (t *ChainTracker) Get(rootMessageID string) *RoutingChain {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.chains[rootMessageID]
}

func (t *ChainTracker) Drop(rootMessageID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.chains, rootMessageID)
}
