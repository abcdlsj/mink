package command

import (
	"context"
	"strings"
	"testing"
)

func noopRun(ctx context.Context, args []string) (string, error) { return "", nil }

// TestRegisterRejectsDuplicateName locks the Phase 1 fix: a second command
// claiming an already-registered name is rejected instead of silently
// overwriting the first. Silent overwrite is exactly how load order alone used
// to decide which of two implementations of the same verb won.
func TestRegisterRejectsDuplicateName(t *testing.T) {
	r := NewRegistry()

	first := NewFuncCmd("compact", "first", noopRun)
	if err := r.Register(first); err != nil {
		t.Fatalf("first registration must succeed, got %v", err)
	}

	second := NewFuncCmd("compact", "second", noopRun)
	err := r.Register(second)
	if err == nil {
		t.Fatalf("duplicate registration must be rejected")
	}
	if !strings.Contains(err.Error(), "compact") {
		t.Fatalf("error should name the colliding command, got %v", err)
	}

	// The original registration must remain authoritative — the rejected
	// duplicate must not have overwritten it.
	got := r.Get("compact")
	if got == nil {
		t.Fatalf("original command must still be registered")
	}
	if got.Desc() != "first" {
		t.Fatalf("original command must be preserved, got desc %q", got.Desc())
	}
}

// TestRegisterDistinctNamesSucceed guards against over-rejection: distinct
// names still register cleanly and both resolve.
func TestRegisterDistinctNamesSucceed(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(NewFuncCmd("session", "s", noopRun)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := r.Register(NewFuncCmd("compact", "c", noopRun)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Get("session") == nil || r.Get("compact") == nil {
		t.Fatalf("both distinct commands must resolve")
	}
	if n := len(r.All()); n != 2 {
		t.Fatalf("expected 2 registered commands, got %d", n)
	}
}
