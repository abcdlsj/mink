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
	store, _, root := testStore(t)
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

	path := filepath.Join(agent.Dir(root), "agent-note.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !containsAll(text, "scope_kind: agent", `scope_key: "agent:debug"`) {
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

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
