package store

import (
	"path/filepath"
	"testing"

	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
)

// TestRoundTripSessionCheckpoint asserts a ProjectionCheckpoint survives a
// SaveSession/LoadSession round-trip field-for-field, so a compact boundary
// persisted on one turn is still resolvable on later (post-restart) rounds.
func TestRoundTripSessionCheckpoint(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sumi-data")
	db, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	s := session.New("cli")
	s.Add(msg.Message{Role: "user", Content: "hello"})
	s.Checkpoint = &session.ProjectionCheckpoint{
		SpaceID:                 "space-1",
		ParentMessageID:         "parent-1",
		AgentID:                 "assistant",
		Profile:                 "direct",
		SummaryThroughMessageID: "msg-42",
		Summary:                 "raw provider summary text",
		PrefixFingerprint:       "deadbeef",
	}

	if err := db.SaveSession(s); err != nil {
		t.Fatal(err)
	}
	loaded, err := db.LoadSession(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Checkpoint == nil {
		t.Fatalf("checkpoint dropped on round-trip")
	}
	got := *loaded.Checkpoint
	want := *s.Checkpoint
	if got != want {
		t.Fatalf("checkpoint round-trip mismatch:\n got = %+v\nwant = %+v", got, want)
	}
}

// TestRoundTripSessionNoCheckpoint confirms a session without a checkpoint
// stays nil (the field is omitempty and must not materialize a zero value).
func TestRoundTripSessionNoCheckpoint(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sumi-data")
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
	loaded, err := db.LoadSession(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Checkpoint != nil {
		t.Fatalf("unexpected checkpoint: %+v", loaded.Checkpoint)
	}
}
