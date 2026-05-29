package collab

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
)

func newWorkerRunnerApp(t *testing.T) *manager {
	t.Helper()
	a := newTestApp(t, config.CollabConfig{})
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Personas().Create("reviewer", persona.Meta{Display: "Reviewer", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "ok " + turn.Input})
			return nil
		}), nil
	})
	return newManager(a)
}

func ensureChannelSpace(t *testing.T, m *manager, src string) *space.Space {
	t.Helper()
	target := space.MapSource(src)
	sp, err := m.app.Spaces().EnsureForSource(src, space.PersonaInfo{ID: target.Seed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.app.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	loaded, _ := m.app.Spaces().LoadSpace(sp.ID)
	return loaded
}

func TestResolveCollabWorkerPersonaAcceptsRegisteredID(t *testing.T) {
	m := newWorkerRunnerApp(t)
	p, err := m.resolveCollabWorkerPersona("desktop", "coder")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "coder" {
		t.Fatalf("id = %q", p.ID)
	}
}

func TestResolveCollabWorkerPersonaResolvesAliasViaTeamsToPersona(t *testing.T) {
	m := newWorkerRunnerApp(t)
	m.bind("desktop", "Reviewer", "reviewer")
	p, err := m.resolveCollabWorkerPersona("desktop", "Reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "reviewer" {
		t.Fatalf("id = %q, want reviewer", p.ID)
	}
}

func TestResolveCollabWorkerPersonaRejectsAliasBoundToRuntimeOnly(t *testing.T) {
	m := newWorkerRunnerApp(t)
	m.bind("desktop", "shellbot", "stub")
	_, err := m.resolveCollabWorkerPersona("desktop", "shellbot")
	if !errors.Is(err, ErrCollabAliasNotPersona) {
		t.Fatalf("err = %v, want ErrCollabAliasNotPersona", err)
	}
}

func TestResolveCollabWorkerPersonaRejectsUnknownAlias(t *testing.T) {
	m := newWorkerRunnerApp(t)
	_, err := m.resolveCollabWorkerPersona("desktop", "ghost")
	if !errors.Is(err, ErrCollabAliasUnknown) {
		t.Fatalf("err = %v, want ErrCollabAliasUnknown", err)
	}
}

func TestResolveCollabWorkerPersonaRejectsEmpty(t *testing.T) {
	m := newWorkerRunnerApp(t)
	_, err := m.resolveCollabWorkerPersona("desktop", "")
	if !errors.Is(err, ErrCollabAliasMissing) {
		t.Fatalf("err = %v, want ErrCollabAliasMissing", err)
	}
}

func TestRunWorkerSyncReturnsResultMessageIDAndOutcomeNotFullBody(t *testing.T) {
	m := newWorkerRunnerApp(t)
	sp := ensureChannelSpace(t, m, "desktop")
	in := workerRunInput{
		Source:           "desktop",
		ParentSpaceID:    sp.ID,
		TriggerMessageID: sp.Messages[len(sp.Messages)-1].ID,
		InitiatorID:      "user",
		WorkerID:         "coder",
		Runtime:          "stub",
		Title:            "do the thing",
		Input:            "do the thing",
	}
	out, err := m.runWorkerSync(context.Background(), in, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != taskpkg.StatusFinished {
		t.Fatalf("status = %v, want finished", out.Status)
	}
	if out.ResultMessageID == "" {
		t.Fatal("result message id missing")
	}
	if len([]rune(out.Outcome)) > taskpkg.MaxOutcomeLen {
		t.Fatalf("outcome leaked beyond limit: %d > %d", len([]rune(out.Outcome)), taskpkg.MaxOutcomeLen)
	}
	if !strings.Contains(out.Outcome, "ok do the thing") {
		t.Fatalf("outcome = %q, want short echo", out.Outcome)
	}
}

func TestRunWorkerSyncTimeoutCancelsTask(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			select {
			case <-block:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}), nil
	})
	m := newManager(a)
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	in := workerRunInput{
		Source:           "desktop",
		ParentSpaceID:    loaded.ID,
		TriggerMessageID: loaded.Messages[len(loaded.Messages)-1].ID,
		InitiatorID:      "user",
		WorkerID:         "coder",
		Runtime:          "stub",
		Title:            "long",
		Input:            "long",
	}
	out, err := m.runWorkerSync(context.Background(), in, 50*time.Millisecond)
	close(block)
	if !errors.Is(err, ErrCollabSpawnTimeoutCancel) {
		t.Fatalf("err = %v, want ErrCollabSpawnTimeoutCancel", err)
	}
	if out.Status != taskpkg.StatusCanceled {
		t.Fatalf("status = %v, want canceled", out.Status)
	}
}

func TestRunWorkerAsMentionDoesNotCreateTask(t *testing.T) {
	m := newWorkerRunnerApp(t)
	sp := ensureChannelSpace(t, m, "desktop")
	in := workerRunInput{
		Source:           "desktop",
		ParentSpaceID:    sp.ID,
		TriggerMessageID: sp.Messages[len(sp.Messages)-1].ID,
		InitiatorID:      "user",
		WorkerID:         "coder",
		Runtime:          "stub",
		Input:            "have a look",
	}
	resultID, err := m.runWorkerAsMention(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if resultID == "" {
		t.Fatal("mention should write a Space message and return its id")
	}
	tasks, err := m.app.Tasks().ListBySpace(sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("mention must NOT create a Task, got %d", len(tasks))
	}
	loaded, _ := m.app.Spaces().LoadSpace(sp.ID)
	workerCount := 0
	var workerMessageID string
	for _, mm := range loaded.Messages {
		if mm.AuthorID == "coder" {
			workerCount++
			workerMessageID = mm.ID
		}
	}
	if workerCount != 1 {
		t.Fatalf("worker message count = %d, want 1", workerCount)
	}
	if workerMessageID != resultID {
		t.Fatalf("returned id = %q, want %q", resultID, workerMessageID)
	}
}

func TestRunWorkerAsMentionRejectsEmptyAssistantOutput(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error { return nil }), nil
	})
	m := newManager(a)
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	in := workerRunInput{
		Source:           "desktop",
		ParentSpaceID:    loaded.ID,
		TriggerMessageID: loaded.Messages[len(loaded.Messages)-1].ID,
		InitiatorID:      "user",
		WorkerID:         "coder",
		Runtime:          "stub",
		Input:            "go",
	}
	if _, err := m.runWorkerAsMention(context.Background(), in); !errors.Is(err, ErrCollabWorkerWroteNothing) {
		t.Fatalf("err = %v, want ErrCollabWorkerWroteNothing", err)
	}
}

func TestRunWorkerAsTaskRejectsUnknownPersona(t *testing.T) {
	m := newWorkerRunnerApp(t)
	sp := ensureChannelSpace(t, m, "desktop")
	in := workerRunInput{
		Source:           "desktop",
		ParentSpaceID:    sp.ID,
		TriggerMessageID: sp.Messages[len(sp.Messages)-1].ID,
		InitiatorID:      "user",
		WorkerID:         "ghost",
		Runtime:          "stub",
		Title:            "x",
		Input:            "x",
	}
	if _, err := m.runWorkerAsTask(context.Background(), in); !errors.Is(err, ErrCollabPersonaNotFound) {
		t.Fatalf("err = %v, want ErrCollabPersonaNotFound", err)
	}
}
