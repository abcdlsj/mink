package app

import (
	"path/filepath"
	"testing"

	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/space"
)

func TestInspectContextReportsFilteredReasons(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Spaces().AppendMessageWithRouting(ch.ID, space.Message{
		AuthorID:   "user",
		AuthorKind: space.ParticipantUser,
		Content:    "real question",
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Spaces().AppendMessageWithRouting(ch.ID, space.Message{
		AuthorID:   "user",
		AuthorKind: space.ParticipantUser,
		Content:    "Send failed: old transport error",
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Spaces().AppendMessageWithRouting(ch.ID, space.Message{
		AuthorID:   "sumi",
		AuthorKind: space.ParticipantSystem,
		Content:    "No agent picked this up.",
	}, nil, nil); err != nil {
		t.Fatal(err)
	}

	view, err := a.InspectContext(ContextInspectInput{
		SpaceID: ch.ID,
		Source:  "desktop:channel:" + ch.ID,
		AgentID: "bob",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.RawMessageCount != 3 || view.EligibleCount != 1 || view.SelectedCount != 1 {
		t.Fatalf("counts raw=%d eligible=%d selected=%d", view.RawMessageCount, view.EligibleCount, view.SelectedCount)
	}
	if got := filteredCount(view.FilteredCounts, "runtime_noise"); got != 1 {
		t.Fatalf("runtime_noise count = %d, want 1", got)
	}
	if got := filteredCount(view.FilteredCounts, "system"); got != 1 {
		t.Fatalf("system count = %d, want 1", got)
	}
	if len(view.Messages) != 1 || view.Messages[0].Content == "" {
		t.Fatalf("selected messages = %+v", view.Messages)
	}
}

func TestResetContextSeparatesSessionAndSummary(t *testing.T) {
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	s, err := a.NewSession("desktop")
	if err != nil {
		t.Fatal(err)
	}
	s.Summary = "old summary"
	s.Add(msg.Message{Role: "system", Content: "[Context Summary]\nold summary"})
	s.Add(msg.Message{Role: "user", Content: "keep me"})
	if err := a.SaveSession(s); err != nil {
		t.Fatal(err)
	}

	summaryRes, err := a.ResetContext(ContextResetInput{Source: "desktop", Action: "summary"})
	if err != nil {
		t.Fatal(err)
	}
	if !summaryRes.ClearedSummary || summaryRes.RemovedSummaryMessages != 1 {
		t.Fatalf("summary reset = %+v", summaryRes)
	}
	current, err := a.CurrentSession("desktop")
	if err != nil {
		t.Fatal(err)
	}
	if current.Summary != "" || len(current.Messages) != 1 || current.Messages[0].Content != "keep me" {
		t.Fatalf("current after summary reset = %+v", current)
	}

	sessionRes, err := a.ResetContext(ContextResetInput{Source: "desktop", Action: "runtime_session"})
	if err != nil {
		t.Fatal(err)
	}
	if sessionRes.PreviousSessionID == "" || sessionRes.SessionID == "" || sessionRes.SessionID == sessionRes.PreviousSessionID {
		t.Fatalf("session reset = %+v", sessionRes)
	}
	fresh, err := a.CurrentSession("desktop")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID != sessionRes.SessionID || len(fresh.Messages) != 0 {
		t.Fatalf("fresh session = %+v", fresh)
	}
}

func filteredCount(in []ContextFilteredCount, reason string) int {
	for _, c := range in {
		if c.Reason == reason {
			return c.Count
		}
	}
	return 0
}
