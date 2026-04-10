package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/abcdlsj/mink/msg"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

func TestSQLiteStoreSaveLoadAndListByWorkspace(t *testing.T) {
	db, err := rtsqlite.Open(filepath.Join(t.TempDir(), "runtime.db"), rtsqlite.OpenOptions{PoolSize: 1})
	if err != nil {
		t.Fatalf("open runtime db: %v", err)
	}
	defer db.Close()

	storeA := NewSQLiteStore(db, "/tmp/ws-a")
	storeB := NewSQLiteStore(db, "/tmp/ws-b")

	snapA := &Snapshot{
		ID:        "sess-a",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Entries: []Entry{
			{
				ID:      "u1",
				Message: message("user", "hello from a"),
			},
		},
	}
	snapB := &Snapshot{
		ID:        "sess-b",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Entries: []Entry{
			{
				ID:      "u2",
				Message: message("user", "hello from b"),
			},
		},
	}

	if err := storeA.Save(snapA.ID, snapA); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := storeB.Save(snapB.ID, snapB); err != nil {
		t.Fatalf("save b: %v", err)
	}

	gotA, err := storeA.Load("sess-a")
	if err != nil {
		t.Fatalf("load a: %v", err)
	}
	if len(gotA.Entries) != 1 || gotA.Entries[0].Message.Content != "hello from a" {
		t.Fatalf("unexpected load a snapshot: %#v", gotA)
	}

	if _, err := storeA.Load("sess-b"); err == nil {
		t.Fatalf("expected workspace-scoped load miss")
	}

	listA, err := storeA.List()
	if err != nil {
		t.Fatalf("list a: %v", err)
	}
	if len(listA) != 1 || listA[0] != "sess-a" {
		t.Fatalf("unexpected list a: %#v", listA)
	}

	listB, err := storeB.List()
	if err != nil {
		t.Fatalf("list b: %v", err)
	}
	if len(listB) != 1 || listB[0] != "sess-b" {
		t.Fatalf("unexpected list b: %#v", listB)
	}
}

func message(role, content string) (m msg.Message) {
	m.Role = role
	m.Content = content
	return m
}
