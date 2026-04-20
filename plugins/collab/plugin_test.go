package collab

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
)

func TestWaitFallsBackToRunLog(t *testing.T) {
	dir := t.TempDir()
	a, err := app.New(config.Config{
		DataDir:   filepath.Join(dir, "mink-data"),
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
		Type:      taskFinished,
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
