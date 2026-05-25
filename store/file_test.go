package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
)

func TestRoundTripSession(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sumi-data")
	db, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	s := session.New("cli")
	s.Add(msg.Message{Role: "user", Content: "hello"})
	s.Add(msg.Message{Role: "assistant", Content: "ok", Usage: &msg.TokenUsage{Input: 10, Output: 5, Total: 15}})

	if err := db.SaveSession(s); err != nil {
		t.Fatal(err)
	}
	if err := db.SetCurrentSession("cli", s.ID); err != nil {
		t.Fatal(err)
	}

	id, err := db.CurrentSessionID("cli")
	if err != nil {
		t.Fatal(err)
	}
	if id != s.ID {
		t.Fatalf("got current %q", id)
	}

	loaded, err := db.LoadSession(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("got %d messages", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "hello" {
		t.Fatalf("got content %q", loaded.Messages[0].Content)
	}
	if loaded.Usage.Calls != 1 || loaded.Usage.Input != 10 || loaded.Usage.Output != 5 || loaded.Usage.Total != 15 {
		t.Fatalf("usage = %+v", loaded.Usage)
	}

	date, tag, ok := parseSessionID(s.ID)
	if !ok {
		t.Fatalf("invalid session id %q", s.ID)
	}
	want := filepath.Join(root, "sessions", tag, date, s.ID+".json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("session file missing: %v", err)
	}
}

func TestSessionIndexAndRunLogFacts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sumi-data")
	db, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	s := session.New("cli")
	if err := db.SaveSession(s); err != nil {
		t.Fatal(err)
	}
	if err := db.SetCurrentSession("cli", s.ID); err != nil {
		t.Fatal(err)
	}
	s.Add(msg.Message{Role: "user", Content: "hello"})
	if err := db.SaveSession(s); err != nil {
		t.Fatal(err)
	}

	idx, err := db.SessionIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx) != 1 {
		t.Fatalf("index len = %d, want 1", len(idx))
	}
	if idx[0].ID != s.ID || idx[0].Path == "" || idx[0].RunlogPath == "" || idx[0].Messages != 1 {
		t.Fatalf("bad index: %+v", idx[0])
	}

	evs, err := db.ReplaySession(s.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, ev := range evs {
		seen[ev.Type] = true
	}
	for _, typ := range []string{bus.SessionCreated, bus.SessionSwitched, bus.SessionSaved} {
		if !seen[typ] {
			t.Fatalf("missing %s in replay: %#v", typ, evs)
		}
	}
}
