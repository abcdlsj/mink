package collab

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
)

// These tests LOCK the current collaboration entry-point contract. They do not
// change behavior: they pin the visible tool surface (so a later refactor cannot
// silently drop a capability) and the Space-mapped-source guard that keeps the
// single-agent / scratch path from triggering collaboration.

// scratchCtx returns a context whose source does NOT map to any Space
// (space.MapSource returns an empty Kind for "scratch:"/"subtask:" prefixes),
// so every collab tool must refuse with its Space-mapped-source guard.
func scratchCtx() context.Context {
	return command.WithSource(context.Background(), "scratch:none")
}

func mustArgs(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCollabToolSurfaceRegistersAllEntryPoints(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	if err := Plugin()(a); err != nil {
		t.Fatalf("apply collab plugin: %v", err)
	}
	// Rule #2: these are distinct collaboration capabilities. A refactor may merge
	// shared plumbing behind them, but none of these entry points may disappear.
	want := []string{
		"spawn",
		"delegate",
		"delegate_poll",
		"cancel_delegation",
		"invite_agent",
		"mention",
		"spawn_specialist",
	}
	got := map[string]bool{}
	for _, tl := range a.Tools() {
		got[tl.Name()] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("collab tool %q is not registered", name)
		}
	}
}

func TestMentionRequiresSpaceMappedSource(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	m := newManager(a)
	_, err := mentionTool{m: m}.Run(scratchCtx(), mustArgs(t, map[string]string{
		"agent_id": "reviewer",
		"question": "status?",
	}))
	if err == nil || !strings.Contains(err.Error(), "mention requires a Space-mapped source") {
		t.Fatalf("mention on non-space source err = %v, want Space-mapped-source guard", err)
	}
}

func TestMentionRejectsMissingArgs(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	m := newManager(a)
	cases := []map[string]string{
		{"question": "status?"},  // missing agent_id
		{"agent_id": "reviewer"}, // missing question
	}
	for _, args := range cases {
		_, err := mentionTool{m: m}.Run(scratchCtx(), mustArgs(t, args))
		if err == nil || !strings.Contains(err.Error(), "agent_id and question are required") {
			t.Fatalf("mention args=%v err = %v, want arg-validation error", args, err)
		}
	}
}

func TestDelegateRequiresSpaceMappedSource(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	m := newManager(a)
	_, err := delegateTool{m: m}.Run(scratchCtx(), mustArgs(t, map[string]string{
		"task":   "do the thing",
		"target": "reviewer",
	}))
	if err == nil || !strings.Contains(err.Error(), "delegate requires a Space-mapped source") {
		t.Fatalf("delegate on non-space source err = %v, want Space-mapped-source guard", err)
	}
}

func TestDelegateRejectsMissingTask(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	m := newManager(a)
	_, err := delegateTool{m: m}.Run(scratchCtx(), mustArgs(t, map[string]string{
		"target": "reviewer",
	}))
	if err == nil || !strings.Contains(err.Error(), "task is required") {
		t.Fatalf("delegate without task err = %v, want task-required error", err)
	}
}

func TestSpawnRequiresSpaceMappedSource(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	m := newManager(a)
	_, err := spawnTool{m: m}.Run(scratchCtx(), mustArgs(t, map[string]string{
		"task": "do the thing",
	}))
	if err == nil || !strings.Contains(err.Error(), "spawn requires a Space-mapped source") {
		t.Fatalf("spawn on non-space source err = %v, want Space-mapped-source guard", err)
	}
}

func TestInviteRejectsMissingAgentID(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	m := newManager(a)
	_, err := inviteTool{m: m}.Run(scratchCtx(), mustArgs(t, map[string]string{
		"role_name": "reviewer",
	}))
	if err == nil || !strings.Contains(err.Error(), "agent_id is required") {
		t.Fatalf("invite without agent_id err = %v, want agent_id-required error", err)
	}
}
