package space

import (
	"sync"
	"time"
)

const DefaultRoutingBudget = 3

type RoutingChain struct {
	RootMessageID   string
	ParentMessageID string
	SpaceID         string
	Budget          int
	StartedAt       time.Time

	repliedAgents map[string]bool
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
		repliedAgents: map[string]bool{},
	}
}

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

func (c *RoutingChain) AlreadyReplied(agentID string) bool {
	if c == nil {
		return false
	}
	return c.repliedAgents[agentID]
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
