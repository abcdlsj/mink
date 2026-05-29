package space

import (
	"testing"
)

func TestNewRoutingChainDefaults(t *testing.T) {
	c := NewRoutingChain("root-1", "space-1", 0)
	if c.Budget != DefaultRoutingBudget {
		t.Errorf("budget should default to %d, got %d", DefaultRoutingBudget, c.Budget)
	}
	if c.RootMessageID != "root-1" || c.SpaceID != "space-1" {
		t.Errorf("ids not stamped: %+v", c)
	}
	if c.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
}

func TestRoutingChainCanWakeAndSpend(t *testing.T) {
	c := NewRoutingChain("root", "space", 3)
	for _, ag := range []string{"coder", "reviewer", "tshoot"} {
		ok, _ := c.CanWake(ag)
		if !ok {
			t.Errorf("agent %s should be wakable", ag)
		}
		c.Spend(ag)
	}
	// Budget should be exhausted now.
	if ok, why := c.CanWake("any"); ok || why != "budget_exhausted" {
		t.Errorf("after 3 spends: ok=%v why=%q (want false / budget_exhausted)", ok, why)
	}
	// Spend on exhausted chain is a no-op.
	c.Spend("another")
	if c.Budget != 0 {
		t.Errorf("Spend after exhaustion should be no-op, budget=%d", c.Budget)
	}
}

func TestRoutingChainSingleReplyPerAgent(t *testing.T) {
	c := NewRoutingChain("root", "space", 5)
	c.Spend("coder")
	if ok, why := c.CanWake("coder"); ok || why != "duplicate_skipped" {
		t.Errorf("second wake of same agent: ok=%v why=%q", ok, why)
	}
	// Repeat Spend on duplicate is also a no-op.
	c.Spend("coder")
	if c.Budget != 4 {
		t.Errorf("budget should remain 4 after duplicate spend, got %d", c.Budget)
	}
}

func TestRoutingChainNilSafe(t *testing.T) {
	var c *RoutingChain
	if ok, why := c.CanWake("x"); ok || why != "no_chain" {
		t.Errorf("nil chain: ok=%v why=%q (want false / no_chain)", ok, why)
	}
	c.Spend("x") // must not panic
	if c.AlreadyReplied("x") {
		t.Error("nil chain AlreadyReplied should be false")
	}
}

func TestChainTrackerIdempotent(t *testing.T) {
	tk := NewChainTracker()
	a := tk.Start("root", "space", 3)
	b := tk.Start("root", "space", 999) // same root, must return same instance
	if a != b {
		t.Error("Start with existing root should return the same chain")
	}
	if got := tk.Get("root"); got != a {
		t.Error("Get should return the live chain")
	}
}

func TestChainTrackerSeparateChainsDoNotInterfere(t *testing.T) {
	tk := NewChainTracker()
	a := tk.Start("root-A", "space-1", 3)
	b := tk.Start("root-B", "space-1", 3)
	a.Spend("coder")
	a.Spend("coder") // dup, no-op
	if okA, _ := a.CanWake("coder"); okA {
		t.Error("chain A should disallow second coder reply")
	}
	if okB, _ := b.CanWake("coder"); !okB {
		t.Error("chain B is independent and should allow coder reply")
	}
	if a.Budget == b.Budget {
		// a spent once for coder, b spent zero
		t.Errorf("budgets should differ: a=%d b=%d", a.Budget, b.Budget)
	}
}

func TestChainTrackerDrop(t *testing.T) {
	tk := NewChainTracker()
	tk.Start("root", "space", 3)
	tk.Drop("root")
	if got := tk.Get("root"); got != nil {
		t.Error("Drop should remove the chain")
	}
}
