package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
)

// checkpointTestApp builds a stub App (provider nil → deterministic heuristic
// summaries, no network) plus a channel Space pre-filled with `nOld` old user
// messages. It returns the app, the space id, the agent self-id, and the
// ordered old message IDs.
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

	if err := a.autoCompact(context.Background(), "desktop:channel:"+spaceID, "stub", s, view); err != nil {
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

// TestAutoCompactCheckpointStaleInvalidation is the stale regression. Editing a
// message inside the compacted prefix must invalidate the fingerprint, so the
// next projection falls back to a full rebuild and clears the summary/checkpoint
// rather than silently replaying an out-of-date summary.
func TestAutoCompactCheckpointStaleInvalidation(t *testing.T) {
	a, spaceID, agentID, ids := checkpointTestApp(t, 6)

	s := session.New("desktop:channel:" + spaceID + ":persona:" + agentID)
	view := viewFor(a, spaceID, agentID)
	view.Apply(s)
	if err := a.autoCompact(context.Background(), "desktop:channel:"+spaceID, "stub", s, view); err != nil {
		t.Fatal(err)
	}
	if s.Checkpoint == nil {
		t.Fatalf("compact did not write a checkpoint")
	}

	// Edit a message inside the folded prefix (ids[1]).
	if _, err := a.Spaces().UpdateMessage(spaceID, ids[1], func(m *space.Message) {
		m.Content = "EDITED prefix content"
	}); err != nil {
		t.Fatal(err)
	}

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
	if len(next.Messages) != 6 {
		t.Fatalf("full rebuild = %d messages, want all 6", len(next.Messages))
	}
	foundEdit := false
	for _, m := range next.Messages {
		if strings.Contains(m.Content, "EDITED prefix content") {
			foundEdit = true
		}
		if strings.HasPrefix(m.Content, "[Context Summary]") {
			t.Fatalf("full rebuild leaked a summary system message: %+v", m)
		}
	}
	if !foundEdit {
		t.Fatalf("edited content missing from full rebuild: %+v", next.Messages)
	}
}

// TestAutoCompactDraftNoCheckpoint confirms a turn with no Space (in-memory
// Draft) keeps legacy in-place compaction and writes no checkpoint.
func TestAutoCompactDraftNoCheckpoint(t *testing.T) {
	a, _, _, _ := checkpointTestApp(t, 0)

	s := session.New("cli:direct:draft")
	for i := 0; i < 6; i++ {
		s.Add(msg.Message{Role: "user", Content: "draft line " + string(rune('a'+i))})
	}
	if err := a.autoCompact(context.Background(), "cli:direct:draft", "stub", s, ContextView{}); err != nil {
		t.Fatal(err)
	}
	if s.Checkpoint != nil {
		t.Fatalf("draft turn must not write a checkpoint: %+v", s.Checkpoint)
	}
	if strings.TrimSpace(s.Summary) == "" {
		t.Fatalf("draft compact should still summarize in place")
	}
}
