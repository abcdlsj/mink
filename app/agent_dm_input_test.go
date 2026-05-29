package app

import (
	"context"
	"errors"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/space"
)

func TestAgentDMHandleInputUserMessageWritesSpaceBeforeRuntime(t *testing.T) {
	a, rec := newRoutingTestApp(t)
	seedPersona(t, a, "tshoot", "Tshoot")
	if _, err := a.HandleInput(context.Background(), "desktop:agent:tshoot", "hi there"); err != nil {
		t.Fatal(err)
	}
	if got := rec.Sources(); len(got) == 0 {
		t.Fatal("runtime should have run after Space write succeeded")
	}
	target := space.MapSource("desktop:agent:tshoot")
	sp, err := a.Spaces().EnsureSpace(target.Kind, target.Seed, space.PersonaInfo{ID: target.Seed, Display: "Tshoot"})
	if err != nil {
		t.Fatal(err)
	}
	userMessages := 0
	for _, m := range sp.Messages {
		if m.AuthorKind == space.ParticipantUser {
			userMessages++
		}
	}
	if userMessages != 1 {
		t.Fatalf("user message count in AgentDM Space = %d, want 1", userMessages)
	}
}

func TestAgentDMHandleInputRejectsUnknownPersonaWithoutTouchingSession(t *testing.T) {
	a, rec := newRoutingTestApp(t)
	_, err := a.HandleInput(context.Background(), "desktop:agent:ghost", "hi")
	if !errors.Is(err, ErrAgentDMPersonaNotFound) {
		t.Fatalf("err = %v, want ErrAgentDMPersonaNotFound", err)
	}
	if got := rec.Sources(); len(got) != 0 {
		t.Fatalf("runtime must NOT run when persona resolution fails, got %d calls: %v", len(got), got)
	}
}

func TestAgentDMHandleInputAsRejectsConflictBetweenSourceAndExplicitPersona(t *testing.T) {
	a, _ := newRoutingTestApp(t)
	seedPersona(t, a, "tshoot", "Tshoot")
	seedPersona(t, a, "coder", "Coder")
	_, err := a.HandleInputAs(context.Background(), "desktop:agent:tshoot", "coder", "hi")
	if !errors.Is(err, ErrAgentDMPersonaConflict) {
		t.Fatalf("err = %v, want ErrAgentDMPersonaConflict", err)
	}
}

func TestAgentDMHandleInputAsAcceptsMatchingExplicitPersona(t *testing.T) {
	a, rec := newRoutingTestApp(t)
	seedPersona(t, a, "tshoot", "Tshoot")
	if _, err := a.HandleInputAs(context.Background(), "desktop:agent:tshoot", "tshoot", "hi"); err != nil {
		t.Fatal(err)
	}
	if got := rec.Sources(); len(got) == 0 {
		t.Fatal("runtime should have run when source seed and explicit persona agree")
	}
}

func TestAgentDMHandleInputCLISeedDrivesRuntimePersona(t *testing.T) {
	a, _ := newRoutingTestApp(t)
	seedPersona(t, a, "tshoot", "Tshoot")
	var seenPersona *agent.Persona
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		seenPersona = env.Persona
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ack"})
			return nil
		}), nil
	})
	if _, err := a.HandleInput(context.Background(), "cli:agent:tshoot", "hi"); err != nil {
		t.Fatal(err)
	}
	if seenPersona == nil {
		t.Fatal("runtime env must carry persona derived from cli:agent:* source seed")
	}
	if seenPersona.ID != "tshoot" {
		t.Fatalf("persona id = %q, want tshoot", seenPersona.ID)
	}
}

func TestAgentDMAssistantMultiMessageAssemblesIntoSingleSpaceMessage(t *testing.T) {
	a, _ := newRoutingTestApp(t)
	seedPersona(t, a, "tshoot", "Tshoot")
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "first part."})
			turn.Session.Add(msg.Message{Role: "assistant", Content: "second part."})
			return nil
		}), nil
	})
	if _, err := a.HandleInput(context.Background(), "desktop:agent:tshoot", "hi"); err != nil {
		t.Fatal(err)
	}
	target := space.MapSource("desktop:agent:tshoot")
	sp, err := a.Spaces().EnsureSpace(target.Kind, target.Seed, space.PersonaInfo{ID: target.Seed, Display: "Tshoot"})
	if err != nil {
		t.Fatal(err)
	}
	agentMessages := 0
	var combined string
	for _, m := range sp.Messages {
		if m.AuthorKind == space.ParticipantAgent {
			agentMessages++
			combined = m.Content
		}
	}
	if agentMessages != 1 {
		t.Fatalf("agent message count = %d, want 1 (multi-message turn must assemble)", agentMessages)
	}
	if combined != "first part.\nsecond part." {
		t.Fatalf("combined content = %q, want assembled join", combined)
	}
}

func TestAgentDMAssistantToolOnlyTurnDoesNotWriteSpaceMessage(t *testing.T) {
	a, _ := newRoutingTestApp(t)
	seedPersona(t, a, "tshoot", "Tshoot")
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			return nil
		}), nil
	})
	if _, err := a.HandleInput(context.Background(), "desktop:agent:tshoot", "hi"); err != nil {
		t.Fatal(err)
	}
	target := space.MapSource("desktop:agent:tshoot")
	sp, err := a.Spaces().EnsureSpace(target.Kind, target.Seed, space.PersonaInfo{ID: target.Seed, Display: "Tshoot"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range sp.Messages {
		if m.AuthorKind == space.ParticipantAgent {
			t.Fatalf("tool-only / empty turn must not write agent message, got %+v", m)
		}
	}
}
