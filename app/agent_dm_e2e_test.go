package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

func bootAgentDMApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if _, err := a.Personas().Create("tshoot", persona.Meta{Display: "Tshoot", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok " + turn.Input})
			return nil
		}), nil
	})
	return a
}

func loadAgentDMSpace(t *testing.T, a *App, persona string) *space.Space {
	t.Helper()
	sp, err := a.Spaces().EnsureSpace(space.KindAgentDM, persona, space.PersonaInfo{ID: persona, Display: persona})
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func TestE2EAgentDMSingleTurnNoDoubleMessage(t *testing.T) {
	a := bootAgentDMApp(t)
	if _, err := a.HandleInput(context.Background(), "desktop:agent:tshoot", "hi"); err != nil {
		t.Fatal(err)
	}
	sp := loadAgentDMSpace(t, a, "tshoot")
	user, agentMsgs := 0, 0
	for _, m := range sp.Messages {
		if m.AuthorKind == space.ParticipantUser {
			user++
		}
		if m.AuthorKind == space.ParticipantAgent {
			agentMsgs++
		}
	}
	if user != 1 || agentMsgs != 1 {
		t.Fatalf("Space message counts = user:%d agent:%d, want 1/1 (no double)", user, agentMsgs)
	}
}

func TestE2ECLISeedDrivesPCAndCLISharedAgentDMSpace(t *testing.T) {
	a := bootAgentDMApp(t)
	if _, err := a.HandleInput(context.Background(), "cli:agent:coder", "from cli"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.HandleInputAs(context.Background(), "desktop:agent:coder", "coder", "from pc"); err != nil {
		t.Fatal(err)
	}
	sp := loadAgentDMSpace(t, a, "coder")
	user := 0
	for _, m := range sp.Messages {
		if m.AuthorKind == space.ParticipantUser {
			user++
		}
	}
	if user != 2 {
		t.Fatalf("user message count after CLI + PC turns = %d, want 2 (cross-entry shared Space)", user)
	}
}

func TestE2EAgentDMHistoryReopenSeesEverything(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "sumi-data")

	first, err := New(config.Config{Runtime: "stub", DataDir: dataDir, Workspace: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Personas().Create("tshoot", persona.Meta{Display: "Tshoot", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	first.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ack " + turn.Input})
			return nil
		}), nil
	})
	if _, err := first.HandleInput(context.Background(), "desktop:agent:tshoot", "msg-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := first.HandleInput(context.Background(), "desktop:agent:tshoot", "msg-2"); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()

	second, err := New(config.Config{Runtime: "stub", DataDir: dataDir, Workspace: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })

	sp := loadAgentDMSpace(t, second, "tshoot")
	user := 0
	for _, m := range sp.Messages {
		if m.AuthorKind == space.ParticipantUser {
			user++
		}
	}
	if user != 2 {
		t.Fatalf("after reopen user msg count = %d, want 2 (history persisted)", user)
	}
}
