package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
)

func TestRoundTripSession(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mink-data")
	db, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	s := session.New("cli")
	s.Add(msg.Message{Role: "user", Content: "hello"})

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
	if len(loaded.Messages) != 1 {
		t.Fatalf("got %d messages", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "hello" {
		t.Fatalf("got content %q", loaded.Messages[0].Content)
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
