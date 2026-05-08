package collab

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/config"
)

func TestWaitFallsBackToRunLog(t *testing.T) {
	dir := t.TempDir()
	a, err := app.New(config.Config{
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := &manager{
		app:   a,
		tasks: map[string]*task{},
		teams: map[string]map[string]string{},
	}
	a.Bus().Publish(bus.Event{
		Type:      bus.DelegateFinished,
		Source:    "cli",
		SessionID: "sess-1",
		TaskID:    "task-123",
		Output:    "done",
	})

	out, err := m.wait("task-123", 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if out != "done" {
		t.Fatalf("output = %q, want %q", out, "done")
	}
}
