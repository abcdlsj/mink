package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/session"
)

func TestRunLogReplaySession(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "mink-data"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	base := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	sa := session.New("cli")
	sb := session.New("cli")
	evs := []bus.Event{
		{Type: bus.TurnStarted, Source: "cli", SessionID: sa.ID, Time: base},
		{Type: bus.ToolCallStarted, Source: "cli", SessionID: sa.ID, ToolCallID: "tool-1", Tool: "bash", Input: `{"cmd":"printf hi"}`, Time: base.Add(time.Second)},
		{Type: bus.TurnFinished, Source: "cli", SessionID: sb.ID, Time: base.Add(2 * time.Second)},
		{Type: bus.ToolCallFinished, Source: "cli", SessionID: sa.ID, ToolCallID: "tool-1", Tool: "bash", Output: "hi", Time: base.Add(3 * time.Second)},
	}
	for _, ev := range evs {
		if err := db.AppendEvent(ev); err != nil {
			t.Fatal(err)
		}
	}

	got, err := db.ReplaySession(sa.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	if got[0].Type != bus.TurnStarted {
		t.Fatalf("first event = %q", got[0].Type)
	}
	if got[1].ToolCallID != "tool-1" {
		t.Fatalf("tool call id = %q", got[1].ToolCallID)
	}
	if got[2].Type != bus.ToolCallFinished {
		t.Fatalf("last event = %q", got[2].Type)
	}
}

func TestRunLogUsesSessionPrefixPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mink-data")
	db, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	s := session.New("telegram:42")
	ev := bus.Event{
		Type:      bus.TurnStarted,
		Source:    s.Source,
		SessionID: s.ID,
		Time:      s.CreatedAt,
	}
	if err := db.AppendEvent(ev); err != nil {
		t.Fatal(err)
	}

	date, tag, ok := parseSessionID(s.ID)
	if !ok {
		t.Fatalf("invalid session id %q", s.ID)
	}
	want := filepath.Join(root, "runlog", tag, date, s.ID+".jsonl")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("runlog file missing: %v", err)
	}
}
