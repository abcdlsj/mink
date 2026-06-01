package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

// TestEndToEndAgentDMTurnAutoTitlesViaPersistAssistantTurn is the
// production-shaped path: user sends a message into an AgentDM,
// inputFlow runs the runtime, persistAssistantTurn writes the
// assistant message and triggers MaybeAutoTitleAgentDM in a
// goroutine. We assert the Space.Title ends up updated, with no
// extra calls to MaybeAutoTitleAgentDM from the test harness.
func TestEndToEndAgentDMTurnAutoTitlesViaPersistAssistantTurn(t *testing.T) {
	a := newTitleTestApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ack: " + turn.Input})
			return nil
		}), nil
	})
	sp, err := a.Spaces().EnsureSpace(space.KindAgentDM, "coder-deadbeef", space.PersonaInfo{ID: "coder", Display: "Coder"})
	if err != nil {
		t.Fatal(err)
	}
	source := "desktop:agent:" + sp.ID

	events, cancel := a.Bus().Subscribe(64)
	defer cancel()

	if _, err := a.HandleInput(context.Background(), source, "audit retry policy"); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}

	// Wait for the async title goroutine to run and publish.
	deadline := time.After(2 * time.Second)
	titleSeen := false
	for !titleSeen {
		select {
		case ev := <-events:
			if ev.Type == bus.SpaceTitleChanged && ev.SpaceID == sp.ID && strings.TrimSpace(ev.Text) != "" {
				titleSeen = true
			}
		case <-deadline:
			loaded, _ := a.Spaces().LoadSpace(sp.ID)
			t.Fatalf("no SpaceTitleChanged event within 2s; final title = %q", loaded.Title)
		}
	}

	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	if loaded.Title == "" || looksLikeAgentDMMachineSeed(loaded.Title, "coder") {
		t.Fatalf("Title = %q, want a substantive title after the first turn", loaded.Title)
	}
	if !strings.Contains(loaded.Title, "audit") && !strings.Contains(loaded.Title, "retry") {
		t.Fatalf("Title = %q, expected to mention audit/retry from the user message", loaded.Title)
	}
}
