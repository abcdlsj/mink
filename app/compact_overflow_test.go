package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
)

// overflowApp builds an App with a real (finite) model context window so the
// hard-overflow guard is active, a deterministic offline summarizer, and an
// empty channel Space. Budget = ContextWindow - MaxTokens - ReserveTokens; with
// contextWindow=200, MaxTokens=10, ReserveTokens=10 the usable window is 180
// tokens (~720 runes at runes/4+1).
func overflowApp(t *testing.T, contextWindow, keepRecent int) (*App, *stubSummarizer, string, string) {
	t.Helper()
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:     "native",
		DataDir:     filepath.Join(dir, "sumi-data"),
		Workspace:   dir,
		MaxTokens:   10,
		ActiveModel: "main",
		Default:     "main",
		Models: map[string]config.ModelConfig{
			"main": {Provider: "openai", Model: "test", APIKey: "test-key", MaxTokens: 10, ContextWindow: contextWindow},
		},
		Compact: config.CompactConfig{
			Auto:               true,
			KeepRecentMessages: keepRecent,
			ReserveTokens:      10,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	stub := &stubSummarizer{}
	a.provider = stub

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	return a, stub, ch.ID, "bob"
}

func seedSpace(t *testing.T, a *App, spaceID string, n int, content func(i int) string) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		m, err := a.Spaces().AppendUserMessage(spaceID, content(i), nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	return ids
}

// TestAutoCompactHardOverflowPendingAloneFailsClosed: when the pending user turn
// alone already exceeds the usable window, no amount of history compaction can
// help. The guard must fail closed with ErrContextOverflow before compacting,
// never truncate or fake success.
func TestAutoCompactHardOverflowPendingAloneFailsClosed(t *testing.T) {
	a, stub, spaceID, agentID := overflowApp(t, 200, 2)
	seedSpace(t, a, spaceID, 4, func(i int) string { return "short old detail " + string(rune('a'+i)) })

	view := a.BuildContextView(ContextViewInput{SpaceID: spaceID, Source: "desktop:channel:" + spaceID, AgentID: agentID})
	s := session.New("desktop:channel:" + spaceID + ":persona:" + agentID)
	view.Apply(s)

	// Budget ~180 tok; pending ~251 tok alone overflows.
	err := a.autoCompact(context.Background(), "desktop:channel:"+spaceID, "native", s, view, strings.Repeat("x", 1000), nil)
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("want ErrContextOverflow, got %v", err)
	}
	if !strings.Contains(err.Error(), "pending input") {
		t.Fatalf("error should identify the pending-alone case: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("summarizer must not run when pending alone overflows: calls=%d", stub.calls)
	}
	if s.Checkpoint != nil {
		t.Fatalf("no checkpoint should be written on fail-closed: %+v", s.Checkpoint)
	}
}

// TestAutoCompactHardOverflowUnresolvableFailsClosed: pending fits under the
// window on its own, but history compressed all the way to summary-only plus the
// pending turn still exceeds the window. The keep-recent loop shrinks to keep=0
// and then fails closed rather than looping forever or truncating.
func TestAutoCompactHardOverflowUnresolvableFailsClosed(t *testing.T) {
	a, _, spaceID, agentID := overflowApp(t, 200, 2)
	seedSpace(t, a, spaceID, 6, func(i int) string { return "old detail number " + string(rune('a'+i)) })

	view := a.BuildContextView(ContextViewInput{SpaceID: spaceID, Source: "desktop:channel:" + spaceID, AgentID: agentID})
	s := session.New("desktop:channel:" + spaceID + ":persona:" + agentID)
	view.Apply(s)

	// pending ~178 tok < budget 180, but summary (~35 tok) + pending exceeds 180.
	err := a.autoCompact(context.Background(), "desktop:channel:"+spaceID, "native", s, view, strings.Repeat("y", 710), nil)
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("want ErrContextOverflow after summary-only, got %v", err)
	}
}

// TestAutoCompactHardOverflowCompactsUnderWindow: an over-window projection that
// IS resolvable by folding the prefix into a checkpoint. autoCompact must return
// nil, write a checkpoint, and leave the replayed session under the window —
// regardless of Compact.Auto's soft threshold (this is the hard trigger).
func TestAutoCompactHardOverflowCompactsUnderWindow(t *testing.T) {
	a, stub, spaceID, agentID := overflowApp(t, 200, 2)
	ids := seedSpace(t, a, spaceID, 6, func(i int) string {
		return strings.Repeat("detail ", 30) + string(rune('a'+i)) // ~210 runes ~53 tok each
	})

	view := a.BuildContextView(ContextViewInput{SpaceID: spaceID, Source: "desktop:channel:" + spaceID, AgentID: agentID})
	s := session.New("desktop:channel:" + spaceID + ":persona:" + agentID)
	view.Apply(s)

	if err := a.autoCompact(context.Background(), "desktop:channel:"+spaceID, "native", s, view, "small pending", nil); err != nil {
		t.Fatalf("resolvable overflow should not error: %v", err)
	}
	if stub.calls == 0 {
		t.Fatalf("summarizer should have run")
	}
	if s.Checkpoint == nil {
		t.Fatalf("resolvable hard overflow must write a checkpoint")
	}
	// keep=2 → prefix is first 4 messages, boundary is ids[3].
	if s.Checkpoint.SummaryThroughMessageID != ids[3] {
		t.Fatalf("boundary = %q, want ids[3]=%q", s.Checkpoint.SummaryThroughMessageID, ids[3])
	}
	budget := a.hardInputBudget()
	if got := estimateMessages(s.Messages) + estimateText("small pending"); got > budget {
		t.Fatalf("post-compact projection ~%d tok still exceeds budget %d", got, budget)
	}
}

// TestSummarizeChunkedKeepsEachCallUnderBudget: a prefix far larger than a single
// summarize call is folded across multiple provider calls, and every call's
// input stays under the summarize-call budget (the summarizer self-guards the
// window instead of feeding a raw over-window prefix to the provider).
func TestSummarizeChunkedKeepsEachCallUnderBudget(t *testing.T) {
	a, stub, _, _ := overflowApp(t, 200, 2)
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, "user: "+strings.Repeat("word ", 20)) // ~106 runes each
	}
	out, err := a.summarizeChunked(context.Background(), "", lines)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("chunked summary empty")
	}
	if stub.calls < 2 {
		t.Fatalf("expected the large prefix to fold across multiple calls, got %d", stub.calls)
	}
	budget := a.summarizeCallBudget()
	// Assert the FULL per-call input (system prompt + Prior/New wrapper + chunk),
	// not just the user payload, stays under budget.
	if stub.maxCallTokens > budget {
		t.Fatalf("a summarize call input ~%d tok exceeded per-call budget %d", stub.maxCallTokens, budget)
	}
}

// TestSummarizeChunkedSplitsOversizedLine: a single line larger than the per-call
// room is rune-split across sequential calls (layering, not truncation), and each
// call still stays under budget.
func TestSummarizeChunkedSplitsOversizedLine(t *testing.T) {
	a, stub, _, _ := overflowApp(t, 200, 2)
	giant := "user: " + strings.Repeat("z", 5000)
	out, err := a.summarizeChunked(context.Background(), "", []string{giant})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("split-line summary empty")
	}
	if stub.calls < 2 {
		t.Fatalf("oversized line was not split across calls: %d", stub.calls)
	}
	budget := a.summarizeCallBudget()
	if stub.maxCallTokens > budget {
		t.Fatalf("a split-line call input ~%d tok exceeded per-call budget %d", stub.maxCallTokens, budget)
	}
}

// TestSummarizeChunkedTinyRoomFailsClosed: when the running summary has eaten the
// per-call budget down so far that not even the smallest split piece fits (room
// below minSummarizeChunkRoom), summarizeChunked must fail closed immediately
// rather than spin re-splitting a 4-rune floor until the iteration guard trips.
// This is the P0-4 termination contract.
func TestSummarizeChunkedTinyRoomFailsClosed(t *testing.T) {
	// contextWindow=40 → budget = 40-10-10 = 20 tok. The system prompt alone
	// (~28 tok) plus the 64-tok wrapper margin already exceeds 20, so room is
	// deeply negative on the very first pass regardless of prior — the smallest
	// chunk can never fit. Must return ErrContextOverflow, not loop.
	a, stub, _, _ := overflowApp(t, 40, 2)
	_, err := a.summarizeChunked(context.Background(), "", []string{"user: " + strings.Repeat("z", 400)})
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("expected ErrContextOverflow on unfittable room, got %v", err)
	}
	// It must fail closed BEFORE burning the iteration budget on empty re-splits.
	if stub.calls != 0 {
		t.Fatalf("expected no provider calls when room cannot hold a chunk, got %d", stub.calls)
	}
}

// TestSummarizeChunkedRoomBoundaryFailsClosed pins the EXACT fail-closed boundary
// at room=1 and room=2, not just deeply-negative room. Both are below
// minSummarizeChunkRoom(=3), so the guard must trip on the first iteration. This
// guards against a future edit that weakens the guard to `< 1`: at room=1/2 the
// smallest split piece still estimates 3 tok (splitLineToRoom floors at 4 runes,
// takeChunk charges runes/4+1+1 = 3), so it would never be accepted — the loop
// would re-split forever until the iteration cap and fail with the DIFFERENT
// "did not converge" error, all while calls stays 0. Asserting calls==0 +
// ErrContextOverflow alone cannot distinguish the two branches, so we also assert
// the guard-branch message to prove it failed on the room check, not the cap.
//
// Room is constructed deterministically from the pure summarizeChunkRoom formula:
// with prior="" and overflowApp's fixed reserve (MaxTokens 10 + ReserveTokens 10),
// budget = ContextWindow - 20, and room = budget - estimateText(prompt) - overhead.
// We pick ContextWindow so room lands exactly on 1 and 2, and assert that setup
// via summarizeChunkRoom before exercising the behavior.
func TestSummarizeChunkedRoomBoundaryFailsClosed(t *testing.T) {
	const reserve = 20 // overflowApp: max(MaxTokens,cfg.MaxTokens)=10 + ReserveTokens=10
	promptEst := estimateText(summarizeSystemPrompt)
	for _, tc := range []struct {
		name string
		room int
	}{
		{"room=1", 1},
		{"room=2", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := tc.room + promptEst + summarizeChunkFixedOverhead
			window := budget + reserve
			a, stub, _, _ := overflowApp(t, window, 2)

			// Ground-truth the setup: the room formula must yield exactly tc.room
			// for an empty prior, tying config to the code path under test.
			if got := summarizeChunkRoom(a.summarizeCallBudget(), ""); got != tc.room {
				t.Fatalf("setup: room=%d, want %d (budget=%d, promptEst=%d, overhead=%d)",
					got, tc.room, a.summarizeCallBudget(), promptEst, summarizeChunkFixedOverhead)
			}

			_, err := a.summarizeChunked(context.Background(), "", []string{"user: " + strings.Repeat("z", 400)})
			if !errors.Is(err, ErrContextOverflow) {
				t.Fatalf("room=%d: expected ErrContextOverflow, got %v", tc.room, err)
			}
			// Must be the room guard, not the iteration-cap "did not converge".
			if !strings.Contains(err.Error(), "running summary no longer leaves room") {
				t.Fatalf("room=%d: expected fail-closed on the room guard, got %v", tc.room, err)
			}
			if stub.calls != 0 {
				t.Fatalf("room=%d: expected 0 provider calls, got %d", tc.room, stub.calls)
			}
		})
	}
}

// TestAutoCompactExternalHardGuardIsConfigGated locks the P0-3 external-driver
// contract: the hard-overflow guard is enforceable for an external driver ONLY
// when the operator declares its usable input ceiling via
// config.ExternalInputBudgets. Without that config the guard stands down
// (UNGUARDED) even when Space history is injected — the external driver owns and
// reports its own overflow. It is never derived from the active (summarizer)
// model's window. A resume (no Space projection) skips compaction regardless.
func TestAutoCompactExternalHardGuardIsConfigGated(t *testing.T) {
	// Unconfigured external driver: injected Space history, pending far over the
	// active model's window — but there is no declared ceiling for "claude", so the
	// guard is UNGUARDED. autoCompact must NOT enforce, NOT compact, NOT checkpoint.
	a, stub, spaceID, agentID := overflowApp(t, 200, 2)
	seedSpace(t, a, spaceID, 4, func(i int) string { return "detail " + string(rune('a'+i)) })

	view := a.BuildContextView(ContextViewInput{SpaceID: spaceID, Source: "desktop:channel:" + spaceID, AgentID: agentID})
	s := session.New("desktop:channel:" + spaceID + ":persona:" + agentID)
	view.Apply(s)
	if err := a.autoCompact(context.Background(), "desktop:channel:"+spaceID, "claude", s, view, strings.Repeat("x", 1000), nil); err != nil {
		t.Fatalf("unconfigured external driver must be unguarded, got %v", err)
	}
	if s.Checkpoint != nil {
		t.Fatalf("unguarded external driver must not write a checkpoint: %+v", s.Checkpoint)
	}
	if stub.calls != 0 {
		t.Fatalf("unguarded external driver must not summarize: calls=%d", stub.calls)
	}

	// Configured external driver: declare a tiny usable input ceiling for "claude".
	// Now the same over-window pending turn must fail closed with ErrContextOverflow
	// — the guard is enforceable because the operator confirmed the ceiling. The
	// budget is the configured value directly, NOT the active model's derived window.
	b, bstub, spaceID2, agentID2 := overflowApp(t, 200, 2)
	if b.cfg.ExternalInputBudgets == nil {
		b.cfg.ExternalInputBudgets = map[string]int{}
	}
	b.cfg.ExternalInputBudgets["claude"] = 50 // ~50 tok ceiling
	seedSpace(t, b, spaceID2, 4, func(i int) string { return "detail " + string(rune('a'+i)) })

	view2 := b.BuildContextView(ContextViewInput{SpaceID: spaceID2, Source: "desktop:channel:" + spaceID2, AgentID: agentID2})
	s2 := session.New("desktop:channel:" + spaceID2 + ":persona:" + agentID2)
	view2.Apply(s2)
	// pending ~251 tok >> 50 tok ceiling → pending alone overflows, fail closed.
	err := b.autoCompact(context.Background(), "desktop:channel:"+spaceID2, "claude", s2, view2, strings.Repeat("x", 1000), nil)
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("configured external driver must enforce its declared ceiling, got %v", err)
	}
	if !strings.Contains(err.Error(), "pending input") {
		t.Fatalf("configured external overflow should identify the pending-alone case, got %v", err)
	}
	if bstub.calls != 0 {
		t.Fatalf("configured external overflow must fail before summarizing, calls=%d", bstub.calls)
	}

	// Resume: no Space projection (view.SpaceID empty) → guard disabled, no
	// compaction — independent of whether a budget is configured.
	s3 := session.New("cli:direct:resume")
	for i := 0; i < 6; i++ {
		s3.Add(msg.Message{Role: "user", Content: strings.Repeat("y", 500)})
	}
	before := len(s3.Messages)
	if err := b.autoCompact(context.Background(), "external:resume", "claude", s3, ContextView{}, strings.Repeat("x", 1000), nil); err != nil {
		t.Fatalf("resume must skip compaction, got %v", err)
	}
	if s3.Checkpoint != nil {
		t.Fatalf("resume must not write a checkpoint: %+v", s3.Checkpoint)
	}
	if len(s3.Messages) != before {
		t.Fatalf("resume must not compact in place: %d → %d", before, len(s3.Messages))
	}
	if strings.TrimSpace(s3.Summary) != "" {
		t.Fatalf("resume must not summarize: %q", s3.Summary)
	}
}

// TestHardBudgetStatusDeclaredButUnusableIsExplicitError is the evidence-2
// contract: a model that DOES declare a context window but whose
// MaxTokens+ReserveTokens reserve already swallows the whole window has no usable
// input budget. That is a misconfiguration, not an "unknown window", so
// hardBudgetStatus must return an explicit error (not a silent 0 that quietly
// disables the hard guard).
func TestHardBudgetStatusDeclaredButUnusableIsExplicitError(t *testing.T) {
	// contextWindow=20, MaxTokens=10, ReserveTokens=10 → reserve 20 >= window 20.
	a, _, _, _ := overflowApp(t, 20, 2)
	budget, enforceable, err := a.hardBudgetStatus()
	if err == nil {
		t.Fatalf("declared-but-unusable window must be an explicit error, got budget=%d enforceable=%v", budget, enforceable)
	}
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("want ErrContextOverflow, got %v", err)
	}
	if enforceable || budget != 0 {
		t.Fatalf("error case must report no enforceable budget, got budget=%d enforceable=%v", budget, enforceable)
	}

	// Contrast: an unknown window (ContextWindow<=0) is honestly unenforceable and
	// must NOT be an error — the guard stands down and the driver owns its context.
	b, _, _, _ := overflowApp(t, 0, 2)
	budget2, enforceable2, err2 := b.hardBudgetStatus()
	if err2 != nil {
		t.Fatalf("unknown window must not error, got %v", err2)
	}
	if enforceable2 || budget2 != 0 {
		t.Fatalf("unknown window must report no enforceable budget without error, got budget=%d enforceable=%v", budget2, enforceable2)
	}
}

// TestAutoCompactSurfacesUnusableWindowError: when a Space-backed turn (view.SpaceID
// set) runs under a declared-but-unusable window, autoCompact must surface the
// config error rather than silently proceeding unguarded.
func TestAutoCompactSurfacesUnusableWindowError(t *testing.T) {
	a, stub, spaceID, agentID := overflowApp(t, 20, 2)
	seedSpace(t, a, spaceID, 4, func(i int) string { return "detail " + string(rune('a'+i)) })

	view := a.BuildContextView(ContextViewInput{SpaceID: spaceID, Source: "desktop:channel:" + spaceID, AgentID: agentID})
	s := session.New("desktop:channel:" + spaceID + ":persona:" + agentID)
	view.Apply(s)

	err := a.autoCompact(context.Background(), "desktop:channel:"+spaceID, "native", s, view, "ping", nil)
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("Space-backed turn under an unusable window must fail loud, got %v", err)
	}
	if !strings.Contains(err.Error(), "no usable input budget") {
		t.Fatalf("error should identify the unusable-budget config, got %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("must fail before summarizing, got calls=%d", stub.calls)
	}
}

// TestAutoCompactPendingBudgetCountsQuotedTranscript is the P0-1 contract: a turn
// whose RAW text is tiny but which carries a huge quoted_transcript attachment
// still overflows, because the attachment expands into the pending message text
// (agent.UserInputWithAttachments) that the runtime will actually send. Counting
// only the raw input would let this slip past the hard guard.
func TestAutoCompactPendingBudgetCountsQuotedTranscript(t *testing.T) {
	a, stub, spaceID, agentID := overflowApp(t, 200, 2)
	seedSpace(t, a, spaceID, 2, func(i int) string { return "hi " + string(rune('a'+i)) })

	view := a.BuildContextView(ContextViewInput{SpaceID: spaceID, Source: "desktop:channel:" + spaceID, AgentID: agentID})
	s := session.New("desktop:channel:" + spaceID + ":persona:" + agentID)
	view.Apply(s)

	// Raw text is ~1 tok; the quoted transcript expands ~1250 tok into the message,
	// far past the ~180 budget, so the pending turn alone overflows.
	attachments := []msg.Attachment{{
		Kind:  "quoted_transcript",
		Label: "old chat",
		Data:  strings.Repeat("z", 5000),
	}}
	err := a.autoCompact(context.Background(), "desktop:channel:"+spaceID, "native", s, view, "go", attachments)
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("huge quoted_transcript must overflow the pending budget, got %v", err)
	}
	if !strings.Contains(err.Error(), "pending input") {
		t.Fatalf("error should identify the pending-alone case, got %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("must fail closed before summarizing, got calls=%d", stub.calls)
	}

	// Control: the SAME short raw text with NO attachment fits comfortably and does
	// not trip the guard — proving it was the expanded transcript, not the text.
	s2 := session.New("desktop:channel:" + spaceID + ":persona:" + agentID)
	view.Apply(s2)
	if err := a.autoCompact(context.Background(), "desktop:channel:"+spaceID, "native", s2, view, "go", nil); err != nil {
		t.Fatalf("short text alone must not overflow: %v", err)
	}
}

// TestAutoCompactPendingBudgetChargesImages is the P0-1 image contract: images do
// not expand into text, so the char heuristic misses them; a turn carrying enough
// images is charged the per-image capacity budget and overflows the window.
func TestAutoCompactPendingBudgetChargesImages(t *testing.T) {
	a, stub, spaceID, agentID := overflowApp(t, 200, 2)
	seedSpace(t, a, spaceID, 2, func(i int) string { return "hi " + string(rune('a'+i)) })

	view := a.BuildContextView(ContextViewInput{SpaceID: spaceID, Source: "desktop:channel:" + spaceID, AgentID: agentID})
	s := session.New("desktop:channel:" + spaceID + ":persona:" + agentID)
	view.Apply(s)

	// Budget ~180 tok; a single image is charged perImageBudgetTokens=1024, so one
	// image alone already exceeds the window even with trivial text.
	attachments := []msg.Attachment{{Kind: "image", MIME: "image/png", Data: "…"}}
	err := a.autoCompact(context.Background(), "desktop:channel:"+spaceID, "native", s, view, "look", attachments)
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("image attachment budget must be counted against the window, got %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("must fail closed before summarizing, got calls=%d", stub.calls)
	}
}
