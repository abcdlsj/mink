package desktop

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

type wakeRuntimeFunc func(context.Context, *agent.Turn) error

func (f wakeRuntimeFunc) Run(ctx context.Context, turn *agent.Turn) error { return f(ctx, turn) }

// overflowBackend builds a Backend over an App with a finite model context
// window so the hard-overflow guard is active. It mirrors app.overflowApp's
// config (contextWindow=200, MaxTokens=10, ReserveTokens=10 → ~180 usable
// tokens) but lives in the desktop package so the test can drive real bus
// events through Backend.trackTurnEvent.
func overflowBackend(t *testing.T) (*Backend, *app.App, string, string) {
	t.Helper()
	dir := t.TempDir()
	a, err := app.New(config.Config{
		Runtime:     "native",
		DataDir:     filepath.Join(dir, "sumi-data"),
		Workspace:   dir,
		MaxTokens:   10,
		ActiveModel: "main",
		Default:     "main",
		Models: map[string]config.ModelConfig{
			"main": {Provider: "openai", Model: "test", APIKey: "test-key", MaxTokens: 10, ContextWindow: 200},
		},
		Compact: config.CompactConfig{Auto: true, KeepRecentMessages: 2, ReserveTokens: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	return newBackend(a), a, ch.ID, "bob"
}

// TestWakeOverflowPersistsFailedMessageThroughBackendTracking is the P0-2
// contract at the Desktop backend layer. A channel/thread wake whose pending
// origin turn overflows the model window fails closed at the preflight, but the
// turn still follows one normal lifecycle on a single stream: TurnStarted ->
// TurnError. When those events pass through Backend.trackTurnEvent (exactly as
// the live Desktop event loop feeds them), the backend must land a pending
// agent message on TurnStarted and persist it as status=failed with the
// overflow error text on TurnError — so the failure is durable after reload,
// not a stream-less side-band. The stub runtime must never run.
func TestWakeOverflowPersistsFailedMessageThroughBackendTracking(t *testing.T) {
	b, a, spaceID, agentID := overflowBackend(t)

	if _, err := a.Personas().Create(agentID, persona.Meta{Runtime: "stub"}, "# Bob"); err != nil {
		t.Fatal(err)
	}
	var ran bool
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return wakeRuntimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			ran = true
			return nil
		}), nil
	})

	events, cancel := a.Bus().Subscribe(64)
	defer cancel()

	// Pending origin turn (~251 tok) alone exceeds the ~180 usable window.
	pending := strings.Repeat("x", 1000)
	origin, err := a.Spaces().AppendUserMessage(spaceID, "@bob "+pending, []string{agentID})
	if err != nil {
		t.Fatal(err)
	}

	source := "desktop:channel:" + spaceID
	// RetryChannelAgentReply drives runChannelWake, which emits TurnStarted on a
	// real stream BEFORE the overflow preflight, then TurnError on that same
	// stream when the guard fails closed. parentMessageID="" → top-level wake.
	if _, err := a.RetryChannelAgentReply(context.Background(), source, spaceID, agentID, "", origin.ID, pending); err == nil {
		t.Fatalf("wake must fail closed on pending-alone overflow")
	}
	if ran {
		t.Fatalf("stub runtime ran despite overflow preflight failure")
	}

	// Feed every published event through the backend's tracker, in order — this
	// is what the live Desktop event loop does. Only turn lifecycle events on a
	// non-empty stream/space are tracked.
	var tracked []bus.Event
	for draining := true; draining; {
		select {
		case ev := <-events:
			if ev.Type == bus.TurnStarted || ev.Type == bus.TurnError {
				tracked = append(tracked, ev)
			}
			b.trackTurnEvent(ev)
		default:
			draining = false
		}
	}

	// Event ordering + stream consistency: TurnStarted precedes TurnError, both
	// on the same non-empty stream, same agent.
	var started, errored *bus.Event
	for i := range tracked {
		switch tracked[i].Type {
		case bus.TurnStarted:
			if started == nil {
				started = &tracked[i]
			}
		case bus.TurnError:
			errored = &tracked[i]
		}
	}
	if started == nil || errored == nil {
		t.Fatalf("want both TurnStarted and TurnError, got %+v", tracked)
	}
	if strings.TrimSpace(started.StreamID) == "" {
		t.Fatalf("TurnStarted must carry a non-empty stream id")
	}
	if started.StreamID != errored.StreamID {
		t.Fatalf("TurnError stream %q != TurnStarted stream %q", errored.StreamID, started.StreamID)
	}
	if started.AgentID != agentID || errored.AgentID != agentID {
		t.Fatalf("events must be attributed to agent %q, got started=%q errored=%q", agentID, started.AgentID, errored.AgentID)
	}
	// Ordering by slice position (events drained in publish order).
	startedIdx, errIdx := -1, -1
	for i := range tracked {
		if tracked[i].Type == bus.TurnStarted && startedIdx == -1 {
			startedIdx = i
		}
		if tracked[i].Type == bus.TurnError {
			errIdx = i
		}
	}
	if !(startedIdx < errIdx) {
		t.Fatalf("TurnStarted must precede TurnError: startedIdx=%d errIdx=%d", startedIdx, errIdx)
	}

	// Read the Space back and assert the failed agent message is durable.
	sp, err := a.Spaces().LoadSpace(spaceID)
	if err != nil {
		t.Fatal(err)
	}
	var failed *space.Message
	for i := range sp.Messages {
		m := &sp.Messages[i]
		if m.AuthorKind == space.ParticipantAgent && m.AuthorID == agentID && m.Status == "failed" {
			failed = m
			break
		}
	}
	if failed == nil {
		t.Fatalf("expected a persisted status=failed agent message under %q, messages=%+v", agentID, sp.Messages)
	}
	if !strings.Contains(failed.Error, "exceeds model window") {
		t.Fatalf("failed message must carry the overflow error text, got %q", failed.Error)
	}
	if !strings.Contains(failed.Error, "pending input") {
		t.Fatalf("failed message should identify the pending-alone case, got %q", failed.Error)
	}
	if strings.TrimSpace(failed.ParentMessageID) != "" {
		t.Fatalf("top-level wake failure should be a top-level message, got parent %q", failed.ParentMessageID)
	}
}
