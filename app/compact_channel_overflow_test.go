package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

// TestChannelWakeOverflowPreflightFailsClosed: the channel/thread wake path runs
// the same overflow preflight the Direct/CLI path runs. When the pending origin
// turn cannot fit the window, the wake fails closed with a visible TurnError and
// the runtime is never invoked (no silent truncation).
func TestChannelWakeOverflowPreflightFailsClosed(t *testing.T) {
	a, _, spaceID, agentID := overflowApp(t, 200, 2)
	if _, err := a.Personas().Create(agentID, persona.Meta{Runtime: "stub"}, "# Bob"); err != nil {
		t.Fatal(err)
	}
	var ran bool
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			ran = true
			turn.Session.Add(msg.Message{Role: "assistant", Content: "should not happen"})
			return nil
		}), nil
	})

	events, cancel := a.Bus().Subscribe(64)
	defer cancel()

	seedSpace(t, a, spaceID, 4, func(i int) string { return "detail " + string(rune('a'+i)) })
	pending := strings.Repeat("x", 1000) // ~251 tok, over the ~180 budget on its own
	origin, err := a.Spaces().AppendUserMessage(spaceID, "@bob "+pending, []string{agentID})
	if err != nil {
		t.Fatal(err)
	}

	res := a.runChannelWake(context.Background(), "desktop:channel:"+spaceID, spaceID, space.RoutingTarget{
		AgentID:         agentID,
		OriginMessageID: origin.ID,
	}, pending, nil)

	if res.err == nil || !errors.Is(res.err, ErrContextOverflow) {
		t.Fatalf("wake should fail closed with ErrContextOverflow, got %v", res.err)
	}
	if ran {
		t.Fatalf("runtime ran despite overflow preflight failure")
	}

	sawError := false
	for draining := true; draining; {
		select {
		case ev := <-events:
			if ev.Type == bus.TurnError && strings.Contains(ev.Err, "exceeds model window") {
				sawError = true
			}
		default:
			draining = false
		}
	}
	if !sawError {
		t.Fatalf("expected a visible TurnError event surfacing the overflow")
	}
}

// TestChannelWakePendingBudgetCountsQuotedTranscript proves the channel wake
// preflight expands quoted_transcript attachments into the pending budget: they
// are fed into the same overflow preflight, so a short @mention carrying a huge
// transcript fails closed with a visible TurnError and never runs the runtime.
func TestChannelWakePendingBudgetCountsQuotedTranscript(t *testing.T) {
	a, _, spaceID, agentID := overflowApp(t, 200, 2)
	if _, err := a.Personas().Create(agentID, persona.Meta{Runtime: "stub"}, "# Bob"); err != nil {
		t.Fatal(err)
	}
	var ran bool
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			ran = true
			turn.Session.Add(msg.Message{Role: "assistant", Content: "should not happen"})
			return nil
		}), nil
	})

	events, cancel := a.Bus().Subscribe(64)
	defer cancel()

	seedSpace(t, a, spaceID, 2, func(i int) string { return "hi " + string(rune('a'+i)) })
	attachments := []msg.Attachment{{Kind: "quoted_transcript", Label: "old chat", Data: strings.Repeat("z", 5000)}}
	origin, err := a.Spaces().AppendUserMessageWithAttachmentsInThread(spaceID, "", "@bob go", []string{agentID}, attachments)
	if err != nil {
		t.Fatal(err)
	}

	res := a.runChannelWake(context.Background(), "desktop:channel:"+spaceID, spaceID, space.RoutingTarget{
		AgentID:         agentID,
		OriginMessageID: origin.ID,
	}, "go", attachments)

	if res.err == nil || !errors.Is(res.err, ErrContextOverflow) {
		t.Fatalf("wake with huge quoted transcript should fail closed, got %v", res.err)
	}
	if ran {
		t.Fatalf("runtime ran despite pending-budget overflow")
	}

	sawError := false
	for draining := true; draining; {
		select {
		case ev := <-events:
			if ev.Type == bus.TurnError && strings.Contains(ev.Err, "exceeds model window") {
				sawError = true
			}
		default:
			draining = false
		}
	}
	if !sawError {
		t.Fatalf("expected a visible TurnError surfacing the overflow")
	}
}

// externalWakeApp builds an App whose GLOBAL runtime is an external driver
// ("claude") while the summarizer model is a finite native model. A persona with
// an EMPTY runtime is created, so runtimeFactory falls back to cfg.Runtime — the
// actual consumer is external. This is the exact shape gap #1 guards: the wake
// preflight must budget against the effective (external) runtime, not native.
func externalWakeApp(t *testing.T) (*App, string, string) {
	t.Helper()
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:     "claude",
		DataDir:     filepath.Join(dir, "sumi-data"),
		Workspace:   dir,
		MaxTokens:   10,
		ActiveModel: "main",
		Default:     "main",
		Models: map[string]config.ModelConfig{
			"main": {Provider: "openai", Model: "test", APIKey: "test-key", MaxTokens: 10, ContextWindow: 200},
		},
		Compact: config.CompactConfig{Auto: false, KeepRecentMessages: 2, ReserveTokens: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	a.provider = &stubSummarizer{}
	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	// Persona has NO runtime override → consumer resolves to cfg.Runtime ("claude").
	if _, err := a.Personas().Create("bob", persona.Meta{}, "# Bob"); err != nil {
		t.Fatal(err)
	}
	return a, ch.ID, "bob"
}

// driveExternalWake seeds fat history + an over-window pending origin and runs
// the channel wake. It registers "claude" as the runtime the wake will actually
// build, recording whether it ran.
func driveExternalWake(t *testing.T, a *App, spaceID, agentID string) (channelWakeResult, *bool) {
	t.Helper()
	ran := new(bool)
	a.RegisterRuntime("claude", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			*ran = true
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ran"})
			return nil
		}), nil
	})
	seedSpace(t, a, spaceID, 4, func(i int) string { return "detail " + string(rune('a'+i)) })
	pending := strings.Repeat("x", 1000) // ~251 tok, over the ~180 NATIVE budget
	origin, err := a.Spaces().AppendUserMessage(spaceID, "@bob "+pending, []string{agentID})
	if err != nil {
		t.Fatal(err)
	}
	res := a.runChannelWake(context.Background(), "desktop:channel:"+spaceID, spaceID, space.RoutingTarget{
		AgentID:         agentID,
		OriginMessageID: origin.ID,
	}, pending, nil)
	return res, ran
}

// TestChannelWakeUsesEffectiveRuntimeForBudget proves the wake preflight budgets
// against the EFFECTIVE consumer runtime (persona empty → cfg.Runtime "claude"),
// never native. Two cases pinned to the external-budget contract:
//
//   - unconfigured external budget: the guard stands down (unguarded), so the
//     over-native-window turn is NOT failed closed and the external runtime runs.
//     Before the fix this wrongly failed closed on the native 180-tok budget.
//   - configured external budget below the pending size: the guard is enforceable
//     and the same turn fails closed with ErrContextOverflow, runtime never runs.
func TestChannelWakeUsesEffectiveRuntimeForBudget(t *testing.T) {
	t.Run("unconfigured external is unguarded and runs", func(t *testing.T) {
		a, spaceID, agentID := externalWakeApp(t)
		res, ran := driveExternalWake(t, a, spaceID, agentID)
		if res.err != nil {
			t.Fatalf("unconfigured external consumer must be unguarded, got err %v", res.err)
		}
		if !*ran {
			t.Fatalf("external runtime should have run when the guard stands down")
		}
	})

	t.Run("configured external budget fails closed", func(t *testing.T) {
		a, spaceID, agentID := externalWakeApp(t)
		if a.cfg.ExternalInputBudgets == nil {
			a.cfg.ExternalInputBudgets = map[string]int{}
		}
		a.cfg.ExternalInputBudgets["claude"] = 50 // pending ~251 tok >> 50
		res, ran := driveExternalWake(t, a, spaceID, agentID)
		if res.err == nil || !errors.Is(res.err, ErrContextOverflow) {
			t.Fatalf("configured external budget must fail closed, got %v", res.err)
		}
		if !strings.Contains(res.err.Error(), "pending input") {
			t.Fatalf("error should identify the pending-alone case, got %v", res.err)
		}
		if *ran {
			t.Fatalf("external runtime ran despite the configured-budget overflow")
		}
	})
}
