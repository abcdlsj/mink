package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

func testStore(t *testing.T) (*Store, *rtsqlite.DB, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "memory")
	db, err := rtsqlite.Open(filepath.Join(t.TempDir(), "runtime.db"), rtsqlite.OpenOptions{PoolSize: 1, Workspace: "/tmp/ws-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(root, db), db, root
}

func TestPutScopedAndRecentByScope(t *testing.T) {
	store, db, root := testStore(t)
	ctx := context.Background()

	global := GlobalScope()
	agent := AgentScope("agent:debug")

	if _, err := store.PutScoped(ctx, global, Doc{
		ID:      "global-note",
		Title:   "Global rule",
		Kind:    "note",
		Summary: "always verify",
		Body:    "always verify before answering",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutScoped(ctx, agent, Doc{
		ID:      "agent-note",
		Title:   "Debug playbook",
		Kind:    "note",
		Summary: "check logs first",
		Body:    "check logs first when debugging",
	}); err != nil {
		t.Fatal(err)
	}

	docs, err := store.RecentByScope(ctx, agent, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ID != "agent-note" {
		t.Fatalf("unexpected scoped docs: %#v", docs)
	}
	if docs[0].Workspace != db.WorkspaceID() {
		t.Fatalf("expected workspace %q, got %#v", db.WorkspaceID(), docs[0])
	}

	path := filepath.Join(root, "workspaces", db.WorkspaceID(), agent.Dir(""), "agent-note.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !containsAll(text, "workspace_id: ws_", "scope_kind: agent", `scope_key: "agent:debug"`) {
		t.Fatalf("expected scope metadata in file, got %q", text)
	}
}

func TestSearchScoped(t *testing.T) {
	store, _, _ := testStore(t)
	ctx := context.Background()

	if _, err := store.PutScoped(ctx, GlobalScope(), Doc{
		ID:      "global",
		Title:   "Global guide",
		Kind:    "note",
		Summary: "triage incident",
		Body:    "global incident response checklist",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutScoped(ctx, WorkspaceScope("ws-a"), Doc{
		ID:      "workspace",
		Title:   "Workspace guide",
		Kind:    "note",
		Summary: "triage service",
		Body:    "workspace specific service checklist",
	}); err != nil {
		t.Fatal(err)
	}

	docs, err := store.SearchScoped(ctx, []Scope{WorkspaceScope("ws-a")}, "service checklist", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ID != "workspace" {
		t.Fatalf("unexpected scoped search results: %#v", docs)
	}
}

func TestWorkspaceScopedMemoryIsolation(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "memory")
	dbPath := filepath.Join(t.TempDir(), "runtime.db")

	dbA, err := rtsqlite.Open(dbPath, rtsqlite.OpenOptions{PoolSize: 1, Workspace: "/tmp/ws-a"})
	if err != nil {
		t.Fatal(err)
	}
	defer dbA.Close()

	dbB, err := rtsqlite.Open(dbPath, rtsqlite.OpenOptions{PoolSize: 1, Workspace: "/tmp/ws-b"})
	if err != nil {
		t.Fatal(err)
	}
	defer dbB.Close()

	storeA := New(root, dbA)
	storeB := New(root, dbB)

	if _, err := storeA.PutScoped(ctx, GlobalScope(), Doc{
		ID:    "ws-a",
		Title: "Workspace A",
		Body:  "shared root but isolated workspace a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := storeB.PutScoped(ctx, GlobalScope(), Doc{
		ID:    "ws-b",
		Title: "Workspace B",
		Body:  "shared root but isolated workspace b",
	}); err != nil {
		t.Fatal(err)
	}

	docsA, err := storeA.SearchScoped(ctx, []Scope{GlobalScope()}, "isolated workspace", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(docsA) != 1 || docsA[0].ID != "ws-a" {
		t.Fatalf("unexpected workspace a docs: %#v", docsA)
	}

	docsB, err := storeB.SearchScoped(ctx, []Scope{GlobalScope()}, "isolated workspace", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(docsB) != 1 || docsB[0].ID != "ws-b" {
		t.Fatalf("unexpected workspace b docs: %#v", docsB)
	}

	pathA := filepath.Join(root, "workspaces", dbA.WorkspaceID(), GlobalScope().Dir(""), "ws-a.md")
	if _, err := os.Stat(pathA); err != nil {
		t.Fatalf("expected workspace a file: %v", err)
	}
	pathB := filepath.Join(root, "workspaces", dbB.WorkspaceID(), GlobalScope().Dir(""), "ws-b.md")
	if _, err := os.Stat(pathB); err != nil {
		t.Fatalf("expected workspace b file: %v", err)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
