package collab

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/config"
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
