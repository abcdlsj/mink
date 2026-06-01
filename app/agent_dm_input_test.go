package app

import (
	"context"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

// TestAgentDMMultiInstanceUserMessageRunsTurnAndAppendsAgentReply is the
// minimal regression for lsoooj's "agent 不回复" report. After the
// AgentDM multi-instance refactor (commit 2280d62 + 1843e37), sending
// a message into a Space-id-addressed AgentDM source must still:
//   - run the runtime turn for that persona
//   - persist the assistant reply into the same Space (not the
//     legacy persona-keyed singleton)
func TestAgentDMMultiInstanceUserMessageRunsTurnAndAppendsAgentReply(t *testing.T) {
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
	// Create a multi-instance AgentDM Space (seed = persona-uuid8,
	// matching what Backend.CreateAgentDM does).
	sp, err := a.Spaces().EnsureSpace(space.KindAgentDM, "coder-deadbeef", space.PersonaInfo{ID: "coder", Display: "Coder"})
	if err != nil {
		t.Fatal(err)
	}
	source := "desktop:agent:" + sp.ID
	if _, err := a.HandleInput(context.Background(), source, "audit retry policy"); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	// Allow async title goroutine to finish writing before cleanup.
	time.Sleep(50 * time.Millisecond)
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	users, agents := 0, 0
	var agentContent string
	for _, m := range loaded.Messages {
		switch m.AuthorKind {
		case space.ParticipantUser:
			users++
		case space.ParticipantAgent:
			agents++
			agentContent = m.Content
		}
	}
	if users != 1 {
		t.Fatalf("user message count = %d, want 1", users)
	}
	if agents != 1 {
		t.Fatalf("agent reply count = %d, want 1 — runtime turn did not run or assistant write missed the Space", agents)
	}
	if agentContent != "ack: audit retry policy" {
		t.Fatalf("agent content = %q, want ack of the user message", agentContent)
	}
}

// TestAgentDMMultiInstanceTwoSpacesIsolated checks that two Space-id
// AgentDMs for the same persona keep their messages separate. This
// guards against the legacy singleton fallback accidentally being
// used and merging two conversations.
func TestAgentDMMultiInstanceTwoSpacesIsolated(t *testing.T) {
	a := newTitleTestApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "echo:" + turn.Input})
			return nil
		}), nil
	})
	sp1, _ := a.Spaces().EnsureSpace(space.KindAgentDM, "coder-aaaaaaaa", space.PersonaInfo{ID: "coder", Display: "Coder"})
	sp2, _ := a.Spaces().EnsureSpace(space.KindAgentDM, "coder-bbbbbbbb", space.PersonaInfo{ID: "coder", Display: "Coder"})
	if _, err := a.HandleInput(context.Background(), "desktop:agent:"+sp1.ID, "first ask"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.HandleInput(context.Background(), "desktop:agent:"+sp2.ID, "second ask"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	loaded1, _ := a.Spaces().LoadSpace(sp1.ID)
	loaded2, _ := a.Spaces().LoadSpace(sp2.ID)
	for _, m := range loaded1.Messages {
		if m.Content == "second ask" || m.Content == "echo:second ask" {
			t.Fatalf("instance 1 leaked content from instance 2: %q", m.Content)
		}
	}
	for _, m := range loaded2.Messages {
		if m.Content == "first ask" || m.Content == "echo:first ask" {
			t.Fatalf("instance 2 leaked content from instance 1: %q", m.Content)
		}
	}
}
