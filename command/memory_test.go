package command

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/memory"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

func testMemoryCmd(t *testing.T) (*memoryCmd, *memory.Store, *rtsqlite.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := rtsqlite.Open(filepath.Join(dir, "runtime.db"), rtsqlite.OpenOptions{PoolSize: 1, Workspace: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := memory.New(filepath.Join(dir, "memory"), db)
	return &memoryCmd{mem: store, rt: db}, store, db
}

func TestMemoryCmdRecentDefaultsToWorkspace(t *testing.T) {
	cmd, store, db := testMemoryCmd(t)
	ctx := bus.WithSource(context.Background(), "platform:cli")

	if _, err := store.PutScoped(ctx, memory.WorkspaceScope(db.WorkspaceID()), memory.Doc{
		Title:   "Workspace note",
		Summary: "dispatcher rules",
		Body:    "dispatcher rules body",
	}); err != nil {
		t.Fatal(err)
	}

	out, err := cmd.Run(ctx, []string{"recent"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "workspace:"+db.WorkspaceID()) || !strings.Contains(out, "Workspace note") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestMemoryCmdSaveAndSearch(t *testing.T) {
	cmd, _, db := testMemoryCmd(t)
	ctx := bus.WithSource(context.Background(), "platform:cli")

	if _, err := cmd.Run(ctx, []string{"save", "workspace", "Debug", "playbook", "::", "check", "logs", "first"}); err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Run(ctx, []string{"search", "workspace", "logs"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "workspace:"+db.WorkspaceID()) || !strings.Contains(out, "Debug playbook") {
		t.Fatalf("unexpected output: %s", out)
	}
}
