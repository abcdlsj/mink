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
	taskpkg "github.com/abcdlsj/sumi/task"
)

func TestSpawnToolReturnsResultMessageIDNotFullBody(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{PollTimeoutMS: 5000})
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	full := strings.Repeat("BODY_LINE_ABCDEFGHIJKLMNOPQRSTUVWXYZ ", 64)
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: full})
			return nil
		}), nil
	})
	target := space.MapSource("desktop")
	sp, err := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	tool := spawnTool{m: newManager(a)}
	args := json.RawMessage(`{"task":"audit retry","runtime":"coder"}`)
	ctx := command.WithSource(context.Background(), "desktop")
	out, err := tool.Run(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "message=") || !strings.Contains(out, "outcome=") {
		t.Fatalf("spawn return missing pointer + outcome: %q", out)
	}
	if len([]rune(out)) > 60+taskpkg.MaxOutcomeLen {
		t.Fatalf("spawn return too long (%d runes), wanted bounded near MaxOutcomeLen+overhead", len([]rune(out)))
	}
	if strings.Count(out, "BODY_LINE_ABCDEFGHIJKLMNOPQRSTUVWXYZ") > 6 {
		t.Fatalf("spawn return appears to carry full body, not summary: %q", out)
	}
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	want := strings.TrimSpace(full)
	for _, m := range loaded.Messages {
		if m.AuthorID == "coder" && m.Content != want {
			t.Fatalf("Space worker message must carry full body, got %d chars (want %d)", len(m.Content), len(want))
		}
	}
}

func TestSpawnToolWritesSingleWorkerMessageInSpace(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{PollTimeoutMS: 5000})
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "audit done"})
			return nil
		}), nil
	})
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	tool := spawnTool{m: newManager(a)}
	args := json.RawMessage(`{"task":"audit","runtime":"coder"}`)
	ctx := command.WithSource(context.Background(), "desktop")
	if _, err := tool.Run(ctx, args); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	workerCount := 0
	for _, m := range loaded.Messages {
		if m.AuthorID == "coder" {
			workerCount++
		}
	}
	if workerCount != 1 {
		t.Fatalf("worker message count = %d, want 1 (exactly one source of truth)", workerCount)
	}
	tasks, _ := a.Tasks().ListBySpace(sp.ID)
	if len(tasks) != 1 {
		t.Fatalf("Background Tasks task count = %d, want 1", len(tasks))
	}
}

func TestSpawnToolTimeoutCancelsAndDoesNotLandLateMessage(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{PollTimeoutMS: 50})
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			select {
			case <-released:
				turn.Session.Add(msg.Message{Role: "assistant", Content: "late!"})
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}), nil
	})
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	tool := spawnTool{m: newManager(a)}
	args := json.RawMessage(`{"task":"long","runtime":"coder"}`)
	ctx := command.WithSource(context.Background(), "desktop")
	_, err := tool.Run(ctx, args)
	if !errors.Is(err, ErrCollabSpawnTimeoutCancel) {
		t.Fatalf("err = %v, want ErrCollabSpawnTimeoutCancel", err)
	}
	close(released)
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	for _, m := range loaded.Messages {
		if m.AuthorID == "coder" && strings.Contains(m.Content, "late!") {
			t.Fatalf("late worker message landed in Space after timeout cancel: %+v", m)
		}
	}
}

func TestSpawnToolRejectsWorkerNotPersona(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{PollTimeoutMS: 1000})
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	tool := spawnTool{m: newManager(a)}
	args := json.RawMessage(`{"task":"do","runtime":"stub"}`)
	ctx := command.WithSource(context.Background(), "desktop")
	if _, err := tool.Run(ctx, args); err == nil {
		t.Fatal("expected error: runtime alias does not resolve to persona")
	}
	tasks, _ := a.Tasks().ListBySpace(sp.ID)
	if len(tasks) != 0 {
		t.Fatalf("rejected spawn must NOT create a task, got %d", len(tasks))
	}
}

func TestSpawnToolShareContextClonesParentSession(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{PollTimeoutMS: 5000})
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	parentSession, err := a.NewSession("desktop")
	if err != nil {
		t.Fatal(err)
	}
	parentSession.Add(msg.Message{Role: "user", Content: "earlier context"})
	parentSession.Add(msg.Message{Role: "assistant", Content: "earlier reply"})
	if err := a.SaveSession(parentSession); err != nil {
		t.Fatal(err)
	}
	var observed int
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			observed = len(turn.Session.Messages)
			turn.Session.Add(msg.Message{Role: "assistant", Content: "audit done"})
			return nil
		}), nil
	})
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	tool := spawnTool{m: newManager(a)}
	args := json.RawMessage(`{"task":"do","runtime":"coder","share_context":true}`)
	ctx := command.WithSource(context.Background(), "desktop")
	if _, err := tool.Run(ctx, args); err != nil {
		t.Fatal(err)
	}
	if observed < 3 {
		t.Fatalf("share_context did not clone parent session: observed runtime turn had %d messages, want >=3 (parent ctx + new user)", observed)
	}
}
