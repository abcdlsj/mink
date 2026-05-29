package app

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
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

// agentTurn captures a per-persona scripted reply so wake-runtime
// tests can assert exact Space content.
type agentTurn struct {
	content   string
	reasoning string
	silent    bool // emit no assistant message at all
}

// scriptedRuntimeApp returns an App whose runtime emits a different
// scripted reply per persona id, plus a recorder of every Run call.
func scriptedRuntimeApp(t *testing.T, scripts map[string]agentTurn) (*App, *runtimeRecorder) {
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
		var personaID string
		if env != nil && env.Persona != nil {
			personaID = env.Persona.ID
		}
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			rec.record(turn)
			script, ok := scripts[personaID]
			if !ok || script.silent {
				return nil
			}
			turn.Session.Add(msg.Message{
				Role:      "assistant",
				Content:   script.content,
				Reasoning: script.reasoning,
				AgentID:   personaID,
			})
			return nil
		}), nil
	})
	return a, rec
}

// seedPersona inserts a persona into the registry so the resolver
// can find it during routing tests.
func seedPersona(t *testing.T, a *App, id, display string) {
	t.Helper()
	if _, err := a.personas.Create(id, persona.Meta{Display: display, Runtime: "stub"}, "stub soul"); err != nil {
		t.Fatalf("create persona %q: %v", id, err)
	}
}

// P2.5a isolation tests — Iris's three checks before P2.5b lands.

func TestChannelInputWithMentionDoesNotRunActivePersona(t *testing.T) {
	a, rec := newRoutingTestApp(t)
	if _, err := a.HandleInput(context.Background(), "desktop", "@coder look at this"); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	// In P2.5a the runtime never ran. P2.5b allows scripted wakes,
	// but with no persona registered "coder" cannot resolve, so the
	// router still produces zero wakes and runtime stays at 0.
	if got := rec.Sources(); len(got) != 0 {
		t.Errorf("active persona must not run for unresolved @; got %v", got)
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

// P2.5b wake-runtime tests.

func TestChannelWakeRunsResolvedAgentOnly(t *testing.T) {
	a, rec := scriptedRuntimeApp(t, map[string]agentTurn{
		"coder": {content: "coder reply"},
	})
	seedPersona(t, a, "coder", "Coder")
	seedPersona(t, a, "tshoot", "Tshoot")

	if _, err := a.HandleInput(context.Background(), "desktop", "@coder look"); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}

	// Exactly one runtime call, on a scratch source.
	got := rec.Sources()
	if len(got) != 1 {
		t.Fatalf("expected 1 runtime call, got %d (%v)", len(got), got)
	}
	if !strings.HasPrefix(got[0], "scratch:wake:") {
		t.Errorf("runtime should run on scratch source, got %q", got[0])
	}

	spaces, _ := a.store.ListSpaces()
	if len(spaces) != 1 {
		t.Fatalf("expected 1 space, got %d", len(spaces))
	}
	sp := spaces[0]
	if len(sp.Messages) != 2 {
		t.Fatalf("expected 2 messages (user + coder), got %d: %+v", len(sp.Messages), sp.Messages)
	}
	if sp.Messages[1].AuthorID != "coder" || sp.Messages[1].AuthorKind != space.ParticipantAgent {
		t.Errorf("agent message author wrong: %+v", sp.Messages[1])
	}
	if sp.Messages[1].Content != "coder reply" {
		t.Errorf("agent reply content = %q, want %q", sp.Messages[1].Content, "coder reply")
	}
}

func TestScratchSessionDoesNotLeakIntoStore(t *testing.T) {
	a, _ := scriptedRuntimeApp(t, map[string]agentTurn{
		"coder": {content: "ok"},
	})
	seedPersona(t, a, "coder", "Coder")

	if _, err := a.HandleInput(context.Background(), "desktop", "@coder hi"); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}

	sessions, err := a.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	for _, s := range sessions {
		if strings.HasPrefix(s.Source, "scratch:") {
			t.Errorf("scratch session leaked into ListSessions: %+v", s)
		}
	}
	storeSessions, _ := a.store.ListSessions()
	for _, s := range storeSessions {
		if strings.HasPrefix(s.Source, "scratch:") {
			t.Errorf("scratch session leaked onto disk: %+v", s)
		}
	}
}

func TestChannelWakeWithEmptyReplyDoesNotWriteSpace(t *testing.T) {
	a, _ := scriptedRuntimeApp(t, map[string]agentTurn{
		"coder": {silent: true},
	})
	seedPersona(t, a, "coder", "Coder")

	if _, err := a.HandleInput(context.Background(), "desktop", "@coder hi"); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	spaces, _ := a.store.ListSpaces()
	sp := spaces[0]
	if len(sp.Messages) != 1 {
		t.Errorf("silent agent should not produce a Space message; got %d messages", len(sp.Messages))
	}
}

func TestChannelWakeAgentReplyMentionFansOut(t *testing.T) {
	a, _ := scriptedRuntimeApp(t, map[string]agentTurn{
		"coder":    {content: "looking, ping @reviewer for a second pair"},
		"reviewer": {content: "reviewer looked"},
	})
	seedPersona(t, a, "coder", "Coder")
	seedPersona(t, a, "reviewer", "Reviewer")

	if _, err := a.HandleInput(context.Background(), "desktop", "@coder look"); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}

	spaces, _ := a.store.ListSpaces()
	if len(spaces) != 1 {
		t.Fatalf("expected 1 space, got %d", len(spaces))
	}
	sp := spaces[0]
	// user message + coder reply + reviewer reply
	if len(sp.Messages) != 3 {
		t.Fatalf("expected 3 messages (user/coder/reviewer), got %d: %+v", len(sp.Messages), sp.Messages)
	}
	if sp.Messages[1].AuthorID != "coder" {
		t.Errorf("second message should be coder, got %q", sp.Messages[1].AuthorID)
	}
	if sp.Messages[2].AuthorID != "reviewer" || sp.Messages[2].AuthorKind != space.ParticipantAgent {
		t.Errorf("third message should be reviewer-authored, got %+v", sp.Messages[2])
	}
}

func TestChannelWakeAgentSelfMentionDoesNotRecurse(t *testing.T) {
	a, rec := scriptedRuntimeApp(t, map[string]agentTurn{
		"coder": {content: "thinking @coder more"},
	})
	seedPersona(t, a, "coder", "Coder")

	if _, err := a.HandleInput(context.Background(), "desktop", "@coder look"); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}

	if got := rec.Sources(); len(got) != 1 {
		t.Errorf("self-mention must not re-wake; runtime called %d times (%v)", len(got), got)
	}
}

func TestChannelWakeIndependentChainsDoNotInterfere(t *testing.T) {
	a, rec := scriptedRuntimeApp(t, map[string]agentTurn{
		"coder": {content: "ok"},
	})
	seedPersona(t, a, "coder", "Coder")

	if _, err := a.HandleInput(context.Background(), "desktop", "@coder a"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := a.HandleInput(context.Background(), "desktop", "@coder b"); err != nil {
		t.Fatalf("second: %v", err)
	}

	// Both messages should produce a coder wake — separate chains.
	if got := rec.Sources(); len(got) != 2 {
		t.Errorf("expected 2 runtime calls across two chains, got %d (%v)", len(got), got)
	}
	spaces, _ := a.store.ListSpaces()
	sp := spaces[0]
	if len(sp.Messages) != 4 {
		t.Errorf("expected 4 messages (user/coder/user/coder), got %d", len(sp.Messages))
	}
}

func TestScratchSourceDoesNotMapToChannel(t *testing.T) {
	if sourceIsChannel("scratch:wake:abc") {
		t.Error("scratch source must not be classified as channel")
	}
	target := space.MapSource("scratch:wake:abc")
	if target.Kind != "" {
		t.Errorf("scratch source should produce empty target, got %+v", target)
	}
}

// P2.5c — bus notices.

func TestRoutingNoticesAreEmittedOnBus(t *testing.T) {
	a, _ := scriptedRuntimeApp(t, map[string]agentTurn{
		"coder": {content: "ok"},
	})
	seedPersona(t, a, "coder", "Coder")

	events, unsub := a.bus.Subscribe(16)
	defer unsub()

	if _, err := a.HandleInput(context.Background(), "desktop", "no mention here"); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}

	if !drainForType(t, events, string(space.NoticeChannelNoTarget)) {
		t.Errorf("expected %q on bus, none observed", space.NoticeChannelNoTarget)
	}
}

func TestSelfMentionStaysQuietOnBus(t *testing.T) {
	a, _ := scriptedRuntimeApp(t, map[string]agentTurn{
		"coder": {content: "again @coder, just kidding"},
	})
	seedPersona(t, a, "coder", "Coder")

	events, unsub := a.bus.Subscribe(16)
	defer unsub()

	if _, err := a.HandleInput(context.Background(), "desktop", "@coder kick"); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}

	for {
		select {
		case ev := <-events:
			if ev.Type == string(space.NoticeDuplicateSkipped) ||
				ev.Type == string(space.NoticeBudgetExhausted) {
				t.Errorf("self-mention should be silent on the bus, got %q", ev.Type)
			}
		default:
			return
		}
	}
}

// drainForType pulls events from the bus channel until it finds the
// expected type or the channel runs dry. Returns true when the type
// was observed.
func drainForType(t *testing.T, ch <-chan bus.Event, want string) bool {
	t.Helper()
	for {
		select {
		case ev := <-ch:
			if ev.Type == want {
				return true
			}
		default:
			return false
		}
	}
}
