package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/memory"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

func testMemoryStore(t *testing.T) (*memory.Store, *rtsqlite.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := rtsqlite.Open(dir+"/runtime.db", rtsqlite.OpenOptions{PoolSize: 1, Workspace: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return memory.New(dir+"/memory", db), db
}

func TestWriteMemoryDefaultsToAgentScope(t *testing.T) {
	mem, db := testMemoryStore(t)
	tool := NewWriteMemory(mem, db, "agent:debug")

	out, err := tool.Run(context.Background(), json.RawMessage(`{"title":"Debug playbook","body":"check logs first"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "scope=agent:agent:debug") {
		t.Fatalf("unexpected output: %s", out)
	}

	docs, err := mem.RecentByScope(context.Background(), memory.AgentScope("agent:debug"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Title != "Debug playbook" {
		t.Fatalf("unexpected docs: %#v", docs)
	}
}

func TestSearchMemoryDefaultsToContextScopes(t *testing.T) {
	mem, db := testMemoryStore(t)
	ctx := bus.WithSource(context.Background(), "cli:/tmp/ws")

	if _, err := mem.PutScoped(ctx, memory.ChannelScope("cli:/tmp/ws"), memory.Doc{
		Title: "Channel note",
		Body:  "incident checklist for current channel",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.PutScoped(ctx, memory.WorkspaceScope(db.WorkspaceID()), memory.Doc{
		Title: "Workspace note",
		Body:  "incident checklist for workspace",
	}); err != nil {
		t.Fatal(err)
	}

	tool := NewSearchMemory(mem, db, "agent:debug")
	out, err := tool.Run(ctx, json.RawMessage(`{"query":"incident checklist","limit":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "channel:cli:/tmp/ws") {
		t.Fatalf("expected channel scope in output: %s", out)
	}
	if !strings.Contains(out, "workspace:"+db.WorkspaceID()) {
		t.Fatalf("expected workspace scope in output: %s", out)
	}
}
