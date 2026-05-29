package collab

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

func TestMentionToolWritesSpaceMessageButNoTask(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	if _, err := a.Personas().Create("reviewer", persona.Meta{Display: "Reviewer", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "looking at it"})
			return nil
		}), nil
	})
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	tool := mentionTool{m: newManager(a)}
	args := json.RawMessage(`{"agent_id":"reviewer","question":"have a look"}`)
	ctx := command.WithSource(context.Background(), "desktop")
	out, err := tool.Run(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "mentioned Reviewer") {
		t.Fatalf("return = %q, want short confirmation", out)
	}
	if strings.Contains(out, "looking at it") {
		t.Fatalf("mention return must not carry worker reply body, got %q", out)
	}
	tasks, _ := a.Tasks().ListBySpace(sp.ID)
	if len(tasks) != 0 {
		t.Fatalf("mention must NOT create a Task in Background Tasks, got %d", len(tasks))
	}
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	workerCount := 0
	for _, m := range loaded.Messages {
		if m.AuthorID == "reviewer" {
			workerCount++
		}
	}
	if workerCount != 1 {
		t.Fatalf("worker message count = %d, want 1", workerCount)
	}
}

func TestMentionToolRejectsAliasNotPersona(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error { return nil }), nil
	})
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	m := newManager(a)
	m.bind("desktop", "shellbot", "stub")
	tool := mentionTool{m: m}
	args := json.RawMessage(`{"agent_id":"shellbot","question":"go"}`)
	ctx := command.WithSource(context.Background(), "desktop")
	if _, err := tool.Run(ctx, args); !errors.Is(err, ErrCollabAliasNotPersona) {
		t.Fatalf("err = %v, want ErrCollabAliasNotPersona", err)
	}
	tasks, _ := a.Tasks().ListBySpace(sp.ID)
	if len(tasks) != 0 {
		t.Fatalf("rejected mention must NOT create task, got %d", len(tasks))
	}
}

func TestMentionToolEmptyAssistantOutputRejects(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	if _, err := a.Personas().Create("reviewer", persona.Meta{Display: "Reviewer", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error { return nil }), nil
	})
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	tool := mentionTool{m: newManager(a)}
	args := json.RawMessage(`{"agent_id":"reviewer","question":"go"}`)
	ctx := command.WithSource(context.Background(), "desktop")
	if _, err := tool.Run(ctx, args); !errors.Is(err, ErrCollabWorkerWroteNothing) {
		t.Fatalf("err = %v, want ErrCollabWorkerWroteNothing", err)
	}
}
