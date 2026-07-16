package app

import (
	"context"
	"errors"
	"testing"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/space"
)

// TestManualCompactSpaceBackedRefusesAndLeavesProjectionUnchanged proves that a
// manual /compact (or !compact) on a source whose conversation is a persisted
// Space is refused with ErrManualCompactSpaceBacked and performs NO in-place
// mutation. An in-place s.Compact here would be silently discarded by the next
// turn's ContextView.Apply (the checkpoint identity is not knowable at compact
// time), so faking a mutation the projection throws away is worse than refusing.
func TestManualCompactSpaceBackedRefusesAndLeavesProjectionUnchanged(t *testing.T) {
	a, _, spaceID, _ := overflowApp(t, 200, 2)
	source := "cli:channel:work"

	// Prime the session for this source with real projected history so we can
	// prove the refusal does not touch it.
	seedSpace(t, a, spaceID, 3, func(i int) string { return "history line number " + string(rune('a'+i)) })

	s, err := a.sessions.Current(source)
	if err != nil {
		t.Fatal(err)
	}
	view := a.BuildContextView(ContextViewInput{SpaceID: spaceID, Source: source, AgentID: "bob"})
	view.Apply(s)
	before := len(s.Messages)
	if before == 0 {
		t.Fatalf("precondition: expected projected history, got 0 messages")
	}

	ctx := command.WithSource(context.Background(), source)
	out, err := a.runCompactCommand(ctx, nil)
	if !errors.Is(err, ErrManualCompactSpaceBacked) {
		t.Fatalf("expected ErrManualCompactSpaceBacked, got out=%q err=%v", out, err)
	}
	if s.Checkpoint != nil {
		t.Fatalf("refused compact must not write a checkpoint, got %+v", s.Checkpoint)
	}
	if s.Summary != "" {
		t.Fatalf("refused compact must not set a summary, got %q", s.Summary)
	}
	if len(s.Messages) != before {
		t.Fatalf("refused compact mutated session: before=%d after=%d", before, len(s.Messages))
	}
}

// TestManualCompactSpaceBackedResolvedIDRefuses covers the resolved-Space-ID
// source form (cli:direct:<spaceID>): the guard must recognize the persisted
// Space behind the ID and refuse just the same.
func TestManualCompactSpaceBackedResolvedIDRefuses(t *testing.T) {
	a, _, _, _ := overflowApp(t, 200, 2)
	sp, err := a.Spaces().EnsureSpace(space.KindDirectChat, "resolved", space.PersonaInfo{ID: "assistant", Display: "Sumi"})
	if err != nil {
		t.Fatal(err)
	}
	if !a.manualCompactSpaceBacked("cli:direct:" + sp.ID) {
		t.Fatalf("expected resolved space-id source to be space-backed")
	}
}

// TestManualCompactSpacelessProceedsInPlace proves the legacy path is preserved:
// a source that maps to NO space (scratch:) still compacts in place, since there
// is no projection to anchor a checkpoint and nothing to silently discard the
// in-place result.
func TestManualCompactSpacelessProceedsInPlace(t *testing.T) {
	a, _, _, _ := overflowApp(t, 200, 2)
	source := "scratch:manual"

	if a.manualCompactSpaceBacked(source) {
		t.Fatalf("scratch source must not be treated as space-backed")
	}

	s, err := a.sessions.Current(source)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		s.Add(msg.Message{Role: "user", Content: "scratch turn number " + string(rune('a'+i))})
		s.Add(msg.Message{Role: "assistant", Content: "scratch reply number " + string(rune('a'+i))})
	}
	before := len(s.Messages)

	ctx := command.WithSource(context.Background(), source)
	out, err := a.runCompactCommand(ctx, nil)
	if err != nil {
		t.Fatalf("space-less manual compact should proceed, got err=%v", err)
	}
	if out == "" {
		t.Fatalf("expected a compact summary line, got empty output")
	}
	if len(s.Messages) >= before {
		t.Fatalf("in-place compact should shrink history: before=%d after=%d", before, len(s.Messages))
	}
	if s.Summary == "" {
		t.Fatalf("in-place compact should set a session summary")
	}
}
