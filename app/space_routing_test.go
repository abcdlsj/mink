package app

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
)

// runtimeRecorder counts Run invocations and the sources they
// carried. It writes a stub assistant reply so the legacy session
// path stays consistent.
type runtimeRecorder struct {
	mu      sync.Mutex
	sources []string
}

func (r *runtimeRecorder) record(turn *agent.Turn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources = append(r.sources, turn.Source)
}

func (r *runtimeRecorder) Sources() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sources...)
}

func newRoutingTestApp(t *testing.T) (*App, *runtimeRecorder) {
	t.Helper()
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	rec := &runtimeRecorder{}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			rec.record(turn)
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok"})
			return nil
		}), nil
	})
	return a, rec
}

// P2.5a isolation tests — Iris's three checks before P2.5b lands.

func TestChannelInputWithMentionDoesNotRunActivePersona(t *testing.T) {
	a, rec := newRoutingTestApp(t)
	if _, err := a.HandleInput(context.Background(), "desktop", "@coder look at this"); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	if got := rec.Sources(); len(got) != 0 {
		t.Errorf("active persona must not run on channel input; got runtime calls %v", got)
	}
	spaces, err := a.store.ListSpaces()
	if err != nil {
		t.Fatalf("ListSpaces: %v", err)
	}
	if len(spaces) != 1 {
		t.Fatalf("expected 1 space, got %d", len(spaces))
	}
	sp := spaces[0]
	if sp.Title != "default" {
		t.Errorf("space title = %q, want default", sp.Title)
	}
	if len(sp.Messages) != 1 || !strings.Contains(sp.Messages[0].Content, "@coder") {
		t.Errorf("space message not persisted correctly: %+v", sp.Messages)
	}
}

func TestChannelInputWithoutMentionDoesNotRunActivePersona(t *testing.T) {
	a, rec := newRoutingTestApp(t)
	if _, err := a.HandleInput(context.Background(), "desktop", "just a thought, no mention"); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	if got := rec.Sources(); len(got) != 0 {
		t.Errorf("no-mention channel input must not wake any agent; got %v", got)
	}
	spaces, _ := a.store.ListSpaces()
	if len(spaces) != 1 {
		t.Fatalf("expected 1 space, got %d", len(spaces))
	}
	sp := spaces[0]
	if len(sp.Messages) != 1 {
		t.Errorf("user message should still persist, got %d", len(sp.Messages))
	}
	for _, p := range sp.Participants {
		if p.Kind == "agent" {
			t.Errorf("no-mention input must not add agent participants, found %v", p)
		}
	}
}

func TestAgentDMStillUsesLegacyActivePersona(t *testing.T) {
	a, rec := newRoutingTestApp(t)
	if _, err := a.HandleInput(context.Background(), "desktop:agent:tshoot", "hi"); err != nil {
		t.Fatalf("DM HandleInput: %v", err)
	}
	got := rec.Sources()
	if len(got) == 0 {
		t.Fatalf("agent DM path must run the legacy runtime, got 0 calls")
	}
	for _, src := range got {
		if !strings.HasPrefix(src, "desktop:agent:tshoot") {
			t.Errorf("DM runtime call should carry agent source, got %q", src)
		}
	}
}

func TestSubtaskSourceStillUsesLegacyPath(t *testing.T) {
	a, rec := newRoutingTestApp(t)
	if _, err := a.HandleInput(context.Background(), "subtask:task-abc", "do it"); err != nil {
		t.Fatalf("subtask HandleInput: %v", err)
	}
	if got := rec.Sources(); len(got) == 0 {
		t.Errorf("subtask source must continue through legacy path; got 0 runtime calls")
	}
}
