package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/llm"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
)

// stubSummarizer is a deterministic, offline llm.Provider used to exercise the
// compaction path now that the heuristic fallback is gone (a real provider is
// mandatory; provider==nil is a hard error). Chat returns a fixed non-empty
// summary that never contains the provenance header, so tests can assert the
// checkpoint stores the raw summary.
type stubSummarizer struct {
	calls        int
	maxUserRunes int // largest user-message payload seen across calls
	// maxCallTokens is the largest FULL per-call input estimate seen: the whole
	// []msg.Message the summarizer received (system prompt + the Prior summary /
	// New messages wrapper), scored with the same estimateMessages heuristic the
	// budget is expressed in. Asserting on this — not just the user payload —
	// proves the wrapper and system overhead are inside the per-call budget too.
	maxCallTokens int
}

func (p *stubSummarizer) Chat(ctx context.Context, msgs []msg.Message, tools []llm.Tool) (*llm.Response, error) {
	p.calls++
	if n := estimateMessages(msgs); n > p.maxCallTokens {
		p.maxCallTokens = n
	}
	for _, m := range msgs {
		if m.Role == "user" {
			if n := len([]rune(m.Content)); n > p.maxUserRunes {
				p.maxUserRunes = n
			}
		}
	}
	return &llm.Response{Content: "compacted-summary"}, nil
}

func (p *stubSummarizer) ChatStream(ctx context.Context, msgs []msg.Message, tools []llm.Tool) (<-chan llm.Chunk, error) {
	ch := make(chan llm.Chunk)
	close(ch)
	return ch, nil
}

// checkpointTestApp builds a stub App with a deterministic offline summarizer
// provider plus a channel Space pre-filled with `nOld` old user messages. It
// returns the app, the space id, the agent self-id, and the ordered old message
// IDs.
func checkpointTestApp(t *testing.T, nOld int) (*App, string, string, []string) {
	t.Helper()
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
		Compact: config.CompactConfig{
			Auto:               true,
			TriggerMessages:    4,
			KeepRecentMessages: 2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.provider = &stubSummarizer{}
	t.Cleanup(func() { _ = a.Close() })

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, nOld)
	for i := 0; i < nOld; i++ {
		m, err := a.Spaces().AppendUserMessage(ch.ID, "old detail number "+string(rune('a'+i)), nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	return a, ch.ID, "bob", ids
}

func viewFor(a *App, spaceID, agentID string) ContextView {
	return a.BuildContextView(ContextViewInput{
		SpaceID: spaceID,
		Source:  "desktop:channel:" + spaceID,
		AgentID: agentID,
	})
}

// TestAutoCompactCheckpointDeterministicProjection is the duplicate regression.
// After a checkpoint compact, re-projecting the same Space (a later round) must
// replay [summary] + un-compacted suffix with every surviving message appearing
// exactly once, and repeated Apply calls must be idempotent (no runtime tail
// growth from round to round).
func TestAutoCompactCheckpointDeterministicProjection(t *testing.T) {
	a, spaceID, agentID, ids := checkpointTestApp(t, 6)

	s := session.New("desktop:channel:" + spaceID + ":persona:" + agentID)
	view := viewFor(a, spaceID, agentID)
	view.Apply(s)
	if len(s.Messages) != 6 {
		t.Fatalf("pre-compact projection = %d messages, want 6", len(s.Messages))
	}

	if err := a.autoCompact(context.Background(), "desktop:channel:"+spaceID, "stub", s, view, "", nil); err != nil {
		t.Fatal(err)
	}
	if s.Checkpoint == nil {
		t.Fatalf("compact did not write a checkpoint")
	}
	// keep=2 → prefix is the first 4 messages, boundary is ids[3].
	if s.Checkpoint.SummaryThroughMessageID != ids[3] {
		t.Fatalf("boundary = %q, want %q", s.Checkpoint.SummaryThroughMessageID, ids[3])
	}
	if strings.Contains(s.Checkpoint.Summary, "Historical summary") {
		t.Fatalf("checkpoint stores wrapped summary; must be raw: %q", s.Checkpoint.Summary)
	}

	assertReplay := func(tag string, sess *session.Session) {
		// Exactly one [Context Summary] system message, and it is first.
		if len(sess.Messages) != 3 {
			t.Fatalf("%s: projection = %d messages, want 3 (summary + 2 suffix): %+v", tag, len(sess.Messages), sess.Messages)
		}
		if sess.Messages[0].Role != "system" || !strings.HasPrefix(sess.Messages[0].Content, "[Context Summary]") {
			t.Fatalf("%s: first message is not the summary: %+v", tag, sess.Messages[0])
		}
		if strings.TrimSpace(sess.Summary) == "" {
			t.Fatalf("%s: session summary empty after replay", tag)
		}
		// The two suffix messages are the last two Space messages, once each.
		seen := map[string]int{}
		for _, m := range sess.Messages[1:] {
			seen[m.ID]++
		}
		for _, id := range ids[4:] {
			if seen[id] != 1 {
				t.Fatalf("%s: suffix message %q appears %d times, want 1: %+v", tag, id, seen[id], sess.Messages)
			}
		}
		// No folded prefix message leaks into the suffix.
		for _, id := range ids[:4] {
			if seen[id] != 0 {
				t.Fatalf("%s: folded prefix message %q leaked into suffix", tag, id)
			}
		}
	}

	assertReplay("same-turn", s)

	// Simulate the next round: fresh session carrying the persisted checkpoint,
	// fresh view from the unchanged Space.
	next := session.New(s.Source)
	next.Checkpoint = s.Checkpoint.Clone()
	view2 := viewFor(a, spaceID, agentID)
	view2.Apply(next)
	assertReplay("next-round", next)

	// Idempotent: applying again must not grow or duplicate.
	view3 := viewFor(a, spaceID, agentID)
	view3.Apply(next)
	assertReplay("re-apply", next)
}

// TestAutoCompactCheckpointNoRuntimeTailDuplication reproduces the exact turn
// lifecycle that motivated the deterministic-rebuild model: after a compact,
// runtime.Run adds the current user + assistant into the SAME session, and those
// two also land back in Space. The next round must rebuild from Space so the
// stale runtime tail is dropped and each Space message appears exactly once —
// not once from the residual session tail plus once from the Space suffix.
func TestAutoCompactCheckpointNoRuntimeTailDuplication(t *testing.T) {
	a, spaceID, agentID, _ := checkpointTestApp(t, 6)
	ctx := context.Background()
	source := "desktop:channel:" + spaceID

	s := session.New(source + ":persona:" + agentID)

	// --- Round 1: the turn that trips auto-compact. ---
	// The current user message is appended to Space first and excluded from this
	// turn's projection (runtime.Run re-adds it), mirroring the real flow.
	u1, err := a.Spaces().AppendUserMessage(spaceID, "CURRENT_USER_MSG turn one", nil)
	if err != nil {
		t.Fatal(err)
	}
	view1 := a.BuildContextView(ContextViewInput{SpaceID: spaceID, Source: source, AgentID: agentID, ExcludeMessageID: u1.ID})
	view1.Apply(s)
	if err := a.autoCompact(ctx, source, "stub", s, view1, "", nil); err != nil {
		t.Fatal(err)
	}
	if s.Checkpoint == nil {
		t.Fatalf("round 1 did not write a checkpoint")
	}

	// Runtime produces the current user + assistant into the session, with
	// explicit sentinel IDs so we can prove they get cleared next round.
	s.Add(msg.Message{ID: "runtime-user-1", Role: "user", Content: "CURRENT_USER_MSG turn one"})
	s.Add(msg.Message{ID: "runtime-asst-1", Role: "assistant", Content: "ASSISTANT_REPLY_MSG one"})
	// The assistant reply is then persisted back to Space (the user already was).
	if _, err := a.Spaces().AppendAgentMessage(spaceID, space.PersonaInfo{ID: agentID}, "ASSISTANT_REPLY_MSG one", "", nil, "", nil, nil); err != nil {
		t.Fatal(err)
	}

	// --- Round 2: the next turn. New user message, excluded as before. ---
	u2, err := a.Spaces().AppendUserMessage(spaceID, "CURRENT_USER_MSG turn two", nil)
	if err != nil {
		t.Fatal(err)
	}
	view2 := a.BuildContextView(ContextViewInput{SpaceID: spaceID, Source: source, AgentID: agentID, ExcludeMessageID: u2.ID})
	view2.Apply(s) // reuse the SAME session carrying the round-1 runtime tail.

	// Stale runtime tail must be gone.
	for _, m := range s.Messages {
		if m.ID == "runtime-user-1" || m.ID == "runtime-asst-1" {
			t.Fatalf("stale runtime tail survived rebuild: %+v", m)
		}
	}
	// Summary exactly once, first.
	summaryCount := 0
	for i, m := range s.Messages {
		if strings.HasPrefix(m.Content, "[Context Summary]") {
			summaryCount++
			if i != 0 {
				t.Fatalf("summary not first: index %d", i)
			}
		}
	}
	if summaryCount != 1 {
		t.Fatalf("summary appears %d times, want 1: %+v", summaryCount, s.Messages)
	}
	// Round-1 user and assistant each appear exactly once (from Space, not
	// doubled by a residual session tail).
	if n := countContaining(s.Messages, "CURRENT_USER_MSG turn one"); n != 1 {
		t.Fatalf("round-1 user appears %d times, want 1: %+v", n, s.Messages)
	}
	if n := countContaining(s.Messages, "ASSISTANT_REPLY_MSG one"); n != 1 {
		t.Fatalf("round-1 assistant appears %d times, want 1: %+v", n, s.Messages)
	}
	// The round-2 user is excluded this turn (runtime.Run will add it).
	if n := countContaining(s.Messages, "CURRENT_USER_MSG turn two"); n != 0 {
		t.Fatalf("round-2 user leaked into projection %d times, want 0", n)
	}
}

func countContaining(msgs []msg.Message, sub string) int {
	n := 0
	for _, m := range msgs {
		if strings.Contains(m.Content, sub) {
			n++
		}
	}
	return n
}

// TestAutoCompactCheckpointStaleInvalidation is the stale regression. A
// structural change to the compacted prefix — an in-place edit or a deletion of
// a message before the boundary — must invalidate the fingerprint, so the next
// projection falls back to a full rebuild and clears the summary/checkpoint
// rather than silently replaying an out-of-date summary.
func TestAutoCompactCheckpointStaleInvalidation(t *testing.T) {
	// mutate perturbs the compacted prefix and returns a substring that must be
	// present (edit) or absent (delete) in the resulting full rebuild.
	cases := []struct {
		name      string
		mutate    func(t *testing.T, a *App, spaceID string, ids []string)
		wantMsgs  int
		wantSub   string
		wantAbsnt bool // wantSub should be absent (delete) rather than present (edit)
	}{
		{
			name: "edit-prefix-message",
			mutate: func(t *testing.T, a *App, spaceID string, ids []string) {
				if _, err := a.Spaces().UpdateMessage(spaceID, ids[1], func(m *space.Message) {
					m.Content = "EDITED prefix content"
				}); err != nil {
					t.Fatal(err)
				}
			},
			wantMsgs: 6,
			wantSub:  "EDITED prefix content",
		},
		{
			name: "delete-message-before-boundary",
			mutate: func(t *testing.T, a *App, spaceID string, ids []string) {
				// ids[1] sits strictly before the boundary (ids[3]); deleting it
				// shortens the prefix and shifts the boundary, which the ID-based
				// fingerprint must reject (fail closed).
				if err := a.Spaces().DeleteMessage(spaceID, ids[1]); err != nil {
					t.Fatal(err)
				}
			},
			wantMsgs:  5,
			wantSub:   ids1Content,
			wantAbsnt: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, spaceID, agentID, ids := checkpointTestApp(t, 6)

			s := session.New("desktop:channel:" + spaceID + ":persona:" + agentID)
			view := viewFor(a, spaceID, agentID)
			view.Apply(s)
			if err := a.autoCompact(context.Background(), "desktop:channel:"+spaceID, "stub", s, view, "", nil); err != nil {
				t.Fatal(err)
			}
			if s.Checkpoint == nil {
				t.Fatalf("compact did not write a checkpoint")
			}
			if s.Checkpoint.SummaryThroughMessageID != ids[3] {
				t.Fatalf("boundary = %q, want ids[3]=%q", s.Checkpoint.SummaryThroughMessageID, ids[3])
			}

			tc.mutate(t, a, spaceID, ids)

			next := session.New(s.Source)
			next.Checkpoint = s.Checkpoint.Clone()
			view2 := viewFor(a, spaceID, agentID)
			view2.Apply(next)

			if strings.TrimSpace(next.Summary) != "" {
				t.Fatalf("stale checkpoint still summarized history: %q", next.Summary)
			}
			if next.Checkpoint != nil {
				t.Fatalf("stale checkpoint not cleared: %+v", next.Checkpoint)
			}
			if len(next.Messages) != tc.wantMsgs {
				t.Fatalf("full rebuild = %d messages, want %d: %+v", len(next.Messages), tc.wantMsgs, next.Messages)
			}
			for _, m := range next.Messages {
				if strings.HasPrefix(m.Content, "[Context Summary]") {
					t.Fatalf("full rebuild leaked a summary system message: %+v", m)
				}
			}
			present := countContaining(next.Messages, tc.wantSub) > 0
			if tc.wantAbsnt && present {
				t.Fatalf("deleted content %q still present in full rebuild: %+v", tc.wantSub, next.Messages)
			}
			if !tc.wantAbsnt && !present {
				t.Fatalf("expected content %q missing from full rebuild: %+v", tc.wantSub, next.Messages)
			}
		})
	}
}

// ids1Content is the content of the second seeded message (index 1), matching
// the pattern in checkpointTestApp: "old detail number " + rune('a'+1).
const ids1Content = "old detail number b"

// TestAutoCompactDraftNoCheckpoint confirms a turn with no Space (in-memory
// Draft) keeps legacy in-place compaction and writes no checkpoint.
func TestAutoCompactDraftNoCheckpoint(t *testing.T) {
	a, _, _, _ := checkpointTestApp(t, 0)

	s := session.New("cli:direct:draft")
	for i := 0; i < 6; i++ {
		s.Add(msg.Message{Role: "user", Content: "draft line " + string(rune('a'+i))})
	}
	if err := a.autoCompact(context.Background(), "cli:direct:draft", "stub", s, ContextView{}, "", nil); err != nil {
		t.Fatal(err)
	}
	if s.Checkpoint != nil {
		t.Fatalf("draft turn must not write a checkpoint: %+v", s.Checkpoint)
	}
	if strings.TrimSpace(s.Summary) == "" {
		t.Fatalf("draft compact should still summarize in place")
	}
}
