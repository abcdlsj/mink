package mink

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/abcdlsj/mink/bus"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
)

func TestPrepareFreshCLISourceResetsRecoveredBinding(t *testing.T) {
	ctx := context.Background()
	store := session.NewFileStore(t.TempDir())
	eventBus := bus.New()
	sm := session.NewManager(store, eventBus)

	rt, err := rtsqlite.Open(filepath.Join(t.TempDir(), "runtime.db"), rtsqlite.OpenOptions{})
	if err != nil {
		t.Fatalf("open runtime db: %v", err)
	}
	defer rt.Close()

	oldSession, err := sm.ResetSource(bus.AddrPlatformCLI)
	if err != nil {
		t.Fatalf("seed old session: %v", err)
	}
	if _, err := rt.StartRun(ctx, bus.AddrPlatformCLI, oldSession.ID(), bus.AddrAgentMain, "user_input", "hello"); err != nil {
		t.Fatalf("seed runtime binding: %v", err)
	}

	app := &App{sm: sm, rt: rt}
	if err := app.prepareFreshCLISource(ctx); err != nil {
		t.Fatalf("prepare fresh cli source: %v", err)
	}

	currentID, ok := sm.CurrentID(bus.AddrPlatformCLI)
	if !ok {
		t.Fatalf("expected current cli session")
	}
	if currentID == oldSession.ID() {
		t.Fatalf("expected new session, still using old session %s", currentID)
	}

	bindings, err := rt.SessionBindings(ctx)
	if err != nil {
		t.Fatalf("load session bindings: %v", err)
	}
	if _, ok := bindings[bus.AddrPlatformCLI]; ok {
		t.Fatalf("expected runtime binding for cli source to be cleared, got %#v", bindings[bus.AddrPlatformCLI])
	}
}
