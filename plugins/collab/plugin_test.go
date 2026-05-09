package collab

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
)

type runtimeFunc func(context.Context, *agent.Turn) error

func (f runtimeFunc) Run(ctx context.Context, turn *agent.Turn) error { return f(ctx, turn) }

func newTestApp(t *testing.T, collab config.CollabConfig) *app.App {
	t.Helper()
	dir := t.TempDir()
	a, err := app.New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
		Collab:    collab,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestWaitFallsBackToRunLog(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	m := newManager(a)
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

func TestDelegateQueueBacksPressure(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{MaxConcurrent: 1, QueueDepth: 2})
	var inflight atomic.Int32
	var peak atomic.Int32
	release := make(chan struct{})
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			n := inflight.Add(1)
			for {
				cur := peak.Load()
				if n <= cur || peak.CompareAndSwap(cur, n) {
					break
				}
			}
			defer inflight.Add(-1)
			<-release
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok"})
			return nil
		}), nil
	})

	m := newManager(a)

	id1, err := m.delegate("cli", "stub", "a", false, false)
	if err != nil {
		t.Fatalf("delegate 1: %v", err)
	}
	id2, err := m.delegate("cli", "stub", "b", false, false)
	if err != nil {
		t.Fatalf("delegate 2: %v", err)
	}
	if _, err := m.delegate("cli", "stub", "c", false, false); err != errQueueFull {
		t.Fatalf("delegate 3: want errQueueFull, got %v", err)
	}

	close(release)

	for _, id := range []string{id1, id2} {
		out, err := m.wait(id, 2*time.Second)
		if err != nil {
			t.Fatalf("wait %s: %v", id, err)
		}
		if out != "ok" {
			t.Fatalf("wait %s = %q, want ok", id, out)
		}
	}
	if peak.Load() > 1 {
		t.Fatalf("peak concurrency = %d, want ≤ 1", peak.Load())
	}
}

func TestDelegateCancel(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{MaxConcurrent: 2, QueueDepth: 4})
	running := make(chan struct{})
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			close(running)
			<-ctx.Done()
			return ctx.Err()
		}), nil
	})

	m := newManager(a)

	id, err := m.delegate("cli", "stub", "slow", false, false)
	if err != nil {
		t.Fatal(err)
	}
	<-running
	if err := m.cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	_, err = m.wait(id, time.Second)
	if err == nil {
		t.Fatal("wait returned nil, want cancel error")
	}

	evs, err := a.ReplayTask(id, 16)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range evs {
		if ev.Type == bus.DelegateCanceled {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected DelegateCanceled event in runlog")
	}
}

func TestTeamsPersistAcrossRestart(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	a.RegisterRuntime("claude", func(env *agent.RuntimeEnv) (agent.Runtime, error) { return nil, nil })

	m1 := newManager(a)
	m1.bind("cli", "reviewer", "claude")

	data, err := os.ReadFile(a.Config().CollabTeamsPath())
	if err != nil {
		t.Fatalf("read teams file: %v", err)
	}
	var loaded map[string]map[string]string
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal teams: %v", err)
	}
	if loaded["cli"]["reviewer"] != "claude" {
		t.Fatalf("teams file = %v, want reviewer→claude", loaded)
	}

	m2 := newManager(a)
	if rt := m2.pickRuntime("cli", "reviewer", ""); rt != "claude" {
		t.Fatalf("pickRuntime after restart = %q, want claude", rt)
	}
}

func TestPickRuntimeFallsBackWhenMissing(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	m := newManager(a)
	rt := m.pickRuntime("cli", "nonexistent-runtime", "")
	if rt != a.Config().Runtime {
		t.Fatalf("pickRuntime = %q, want fallback to %q", rt, a.Config().Runtime)
	}
}

func TestBindIsConcurrencySafe(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	a.RegisterRuntime("claude", func(env *agent.RuntimeEnv) (agent.Runtime, error) { return nil, nil })
	m := newManager(a)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.bind("cli", "alias", "claude")
		}(i)
	}
	wg.Wait()
	if rt := m.pickRuntime("cli", "alias", ""); rt != "claude" {
		t.Fatalf("pickRuntime = %q", rt)
	}
}
