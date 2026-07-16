package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/llm"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
)

// failingSummarizer is a Provider whose Chat always errors, used to prove the
// authoritative /compact surfaces summarize failure verbatim instead of faking
// success (the heuristic fallback is gone on purpose).
type failingSummarizer struct{}

func (failingSummarizer) Chat(ctx context.Context, msgs []msg.Message, tools []llm.Tool) (*llm.Response, error) {
	return nil, errors.New("summarize boom")
}

func (failingSummarizer) ChatStream(ctx context.Context, msgs []msg.Message, tools []llm.Tool) (<-chan llm.Chunk, error) {
	ch := make(chan llm.Chunk)
	close(ch)
	return ch, nil
}

func seedScratchSession(t *testing.T, a *App, source string, turns int) *session.Session {
	t.Helper()
	s, err := a.sessions.Current(source)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < turns; i++ {
		s.Add(msg.Message{Role: "user", Content: "scratch turn number " + string(rune('a'+i))})
		s.Add(msg.Message{Role: "assistant", Content: "scratch reply number " + string(rune('a'+i))})
	}
	return s
}

// TestManualCompactAuthoritativeReturnsReceiptAndEmitsOnce is the Phase 1 lock
// for the converged manual /compact (Iris ruling: option B). On a space-less
// source it must:
//   - return the terse success receipt "session compacted" (no summary echo),
//   - publish bus.SessionCompacted exactly once as a lifecycle contract,
//   - actually shrink history and set a session summary via the authoritative
//     LLM summarize path.
func TestManualCompactAuthoritativeReturnsReceiptAndEmitsOnce(t *testing.T) {
	a, _, _, _ := overflowApp(t, 200, 2)
	source := "scratch:manual-authoritative"

	if a.manualCompactSpaceBacked(source) {
		t.Fatalf("scratch source must not be treated as space-backed")
	}
	s := seedScratchSession(t, a, source, 6)
	before := len(s.Messages)

	var compacted int
	stop := a.Bus().OnPublish(func(ev bus.Event) {
		if ev.Type == bus.SessionCompacted {
			compacted++
		}
	})
	defer stop()

	ctx := command.WithSource(context.Background(), source)
	out, err := a.runCompactCommand(ctx, nil)
	if err != nil {
		t.Fatalf("authoritative manual compact should proceed, got err=%v", err)
	}
	if out != "session compacted" {
		t.Fatalf("expected terse receipt %q, got %q", "session compacted", out)
	}
	if compacted != 1 {
		t.Fatalf("SessionCompacted must publish exactly once, got %d", compacted)
	}
	if len(s.Messages) >= before {
		t.Fatalf("authoritative compact should shrink history: before=%d after=%d", before, len(s.Messages))
	}
	if s.Summary == "" {
		t.Fatalf("authoritative compact should set a session summary")
	}
	// The authoritative summary comes from the offline stub, not a heuristic.
	if !strings.Contains(s.Summary, "compacted-summary") {
		t.Fatalf("summary must come from the authoritative summarizer, got %q", s.Summary)
	}
}

// TestManualCompactAuthoritativeFoldsNote proves the plugin's note capability is
// preserved (Rule #1: no capability loss) and preserved correctly: an explicit
// user note is kept VERBATIM as a stable top-level `User note:` prefix ABOVE the
// authoritative provenance-wrapped summary — not paraphrased by the LLM and not
// buried inside the provenance block (which is framed as weak context). The
// persisted s.Summary starts with the note line so it survives as durable,
// authoritative context.
func TestManualCompactAuthoritativeFoldsNote(t *testing.T) {
	a, _, _, _ := overflowApp(t, 200, 2)
	source := "scratch:manual-note"
	s := seedScratchSession(t, a, source, 6)

	ctx := command.WithSource(context.Background(), source)
	out, err := a.runCompactCommand(ctx, []string{"remember", "the", "migration"})
	if err != nil {
		t.Fatalf("note compact should proceed, got err=%v", err)
	}
	if out != "session compacted" {
		t.Fatalf("expected terse receipt, got %q", out)
	}
	if !strings.HasPrefix(s.Summary, "User note: remember the migration\n") {
		t.Fatalf("note must be the verbatim top-level prefix of the summary, got %q", s.Summary)
	}
	// The note must sit ABOVE the provenance header, not inside it.
	noteIdx := strings.Index(s.Summary, "User note: remember the migration")
	provIdx := strings.Index(s.Summary, "Historical summary")
	if provIdx < 0 || noteIdx > provIdx {
		t.Fatalf("note must precede the provenance block, got %q", s.Summary)
	}
	if !strings.Contains(s.Summary, "compacted-summary") {
		t.Fatalf("authoritative summary body must still be present alongside the note, got %q", s.Summary)
	}
}

// TestManualCompactEmptyNoteAddsNoNoise proves an empty/whitespace note does not
// inject a stray "User note:" line — the summary is exactly the authoritative
// provenance-wrapped body.
func TestManualCompactEmptyNoteAddsNoNoise(t *testing.T) {
	a, _, _, _ := overflowApp(t, 200, 2)
	source := "scratch:manual-empty-note"
	s := seedScratchSession(t, a, source, 6)

	ctx := command.WithSource(context.Background(), source)
	if _, err := a.runCompactCommand(ctx, []string{"   "}); err != nil {
		t.Fatalf("empty-note compact should proceed, got err=%v", err)
	}
	if strings.Contains(s.Summary, "User note:") {
		t.Fatalf("empty note must add no note line, got %q", s.Summary)
	}
}

// TestManualCompactEventTextEqualsPersistedSummary locks Iris's requirement that
// SessionCompacted.Text equals the final persisted s.Summary (note included), so
// event consumers see exactly what was stored.
func TestManualCompactEventTextEqualsPersistedSummary(t *testing.T) {
	a, _, _, _ := overflowApp(t, 200, 2)
	source := "scratch:manual-event-text"
	s := seedScratchSession(t, a, source, 6)

	var eventText string
	var count int
	stop := a.Bus().OnPublish(func(ev bus.Event) {
		if ev.Type == bus.SessionCompacted {
			count++
			eventText = ev.Text
		}
	})
	defer stop()

	ctx := command.WithSource(context.Background(), source)
	if _, err := a.runCompactCommand(ctx, []string{"keep", "the", "invariant"}); err != nil {
		t.Fatalf("compact should proceed, got err=%v", err)
	}
	if count != 1 {
		t.Fatalf("SessionCompacted must publish exactly once, got %d", count)
	}
	if eventText != s.Summary {
		t.Fatalf("SessionCompacted.Text must equal persisted summary:\n text=%q\n sum =%q", eventText, s.Summary)
	}
	if !strings.HasPrefix(eventText, "User note: keep the invariant\n") {
		t.Fatalf("event text must carry the verbatim note prefix, got %q", eventText)
	}
}

// TestManualCompactDispatchesToCoreReceipt locks the routing contract: dispatch
// of "/compact" through the real input flow resolves to the core authoritative
// command and returns the terse receipt. This is the observable proof that the
// core registration is the single authoritative /compact after the plugin
// heuristic was removed.
func TestManualCompactDispatchesToCoreReceipt(t *testing.T) {
	a, _, _, _ := overflowApp(t, 200, 2)
	source := "scratch:manual-dispatch"
	seedScratchSession(t, a, source, 6)

	ctx := command.WithSource(context.Background(), source)
	out, err := a.HandleInput(ctx, source, "/compact")
	if err != nil {
		t.Fatalf("/compact dispatch should succeed, got err=%v", err)
	}
	if strings.TrimSpace(out) != "session compacted" {
		t.Fatalf("dispatched /compact must return the core receipt, got %q", out)
	}
}

// TestManualCompactSummarizeFailureIsVisible proves a summarize failure is
// surfaced verbatim and does NOT publish SessionCompacted — no faked success,
// no silent truncation.
func TestManualCompactSummarizeFailureIsVisible(t *testing.T) {
	a, _, _, _ := overflowApp(t, 200, 2)
	a.provider = failingSummarizer{}
	source := "scratch:manual-fail"
	s := seedScratchSession(t, a, source, 6)
	before := len(s.Messages)

	var compacted int
	stop := a.Bus().OnPublish(func(ev bus.Event) {
		if ev.Type == bus.SessionCompacted {
			compacted++
		}
	})
	defer stop()

	ctx := command.WithSource(context.Background(), source)
	out, err := a.runCompactCommand(ctx, nil)
	if err == nil {
		t.Fatalf("summarize failure must surface an error, got out=%q err=nil", out)
	}
	if !strings.Contains(err.Error(), "summarize boom") {
		t.Fatalf("error must carry the underlying summarize failure, got %v", err)
	}
	if compacted != 0 {
		t.Fatalf("failed compact must not publish SessionCompacted, got %d", compacted)
	}
	if s.Summary != "" {
		t.Fatalf("failed compact must not set a summary, got %q", s.Summary)
	}
	if len(s.Messages) != before {
		t.Fatalf("failed compact must not mutate history: before=%d after=%d", before, len(s.Messages))
	}
}

// TestManualCompactSpaceBackedEmitsNoEvent complements the space-backed refusal
// test: the refusal must also publish NO SessionCompacted (the lifecycle event
// fires only on a real compaction).
func TestManualCompactSpaceBackedEmitsNoEvent(t *testing.T) {
	a, _, spaceID, _ := overflowApp(t, 200, 2)
	source := "cli:channel:work"
	seedSpace(t, a, spaceID, 3, func(i int) string { return "history line number " + string(rune('a'+i)) })

	var compacted int
	stop := a.Bus().OnPublish(func(ev bus.Event) {
		if ev.Type == bus.SessionCompacted {
			compacted++
		}
	})
	defer stop()

	ctx := command.WithSource(context.Background(), source)
	_, err := a.runCompactCommand(ctx, nil)
	if !errors.Is(err, ErrManualCompactSpaceBacked) {
		t.Fatalf("expected ErrManualCompactSpaceBacked, got %v", err)
	}
	if compacted != 0 {
		t.Fatalf("refused space-backed compact must not publish SessionCompacted, got %d", compacted)
	}
}
