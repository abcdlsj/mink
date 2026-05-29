package collab

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
)

func mustRegisterPersona(t *testing.T, a *app.App, id, display string) {
	t.Helper()
	if _, err := a.Personas().Create(id, persona.Meta{Display: display, Runtime: "stub"}, ""); err != nil {
		t.Fatalf("create persona %s: %v", id, err)
	}
}

func mustEnsureChannelSpace(t *testing.T, a *app.App, source string) *space.Space {
	t.Helper()
	target := space.MapSource(source)
	sp, err := a.Spaces().EnsureForSource(source, space.PersonaInfo{ID: target.Seed})
	if err != nil {
		t.Fatalf("ensure space: %v", err)
	}
	return sp
}

func waitForTaskStatus(t *testing.T, a *app.App, taskID string, want taskpkg.Status, timeout time.Duration) *taskpkg.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tk, err := a.Tasks().Get(taskID)
		if err == nil && tk.Status == want {
			return tk
		}
		time.Sleep(10 * time.Millisecond)
	}
	tk, _ := a.Tasks().Get(taskID)
	t.Fatalf("task %s never reached status %q (last = %+v)", taskID, want, tk)
	return nil
}

func TestDelegateInSpaceWritesSingleWorkerMessage(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "audit complete"})
			return nil
		}), nil
	})
	mustRegisterPersona(t, a, "coder", "Coder")
	source := "desktop"
	sp := mustEnsureChannelSpace(t, a, source)
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick off the audit", nil); err != nil {
		t.Fatal(err)
	}

	m := newManager(a)
	ctx := command.WithPersona(command.WithSource(context.Background(), source), "user")

	id, ok, err := m.tryDelegateInSpace(ctx, source, "coder", "stub", "audit retry policy")
	if !ok {
		t.Fatal("expected space delegate path to engage")
	}
	if err != nil {
		t.Fatal(err)
	}
	tk := waitForTaskStatus(t, a, id, taskpkg.StatusFinished, 2*time.Second)
	if tk.WorkerID != "coder" {
		t.Fatalf("WorkerID = %q", tk.WorkerID)
	}
	if tk.SpaceID != sp.ID {
		t.Fatalf("SpaceID = %q, want %q", tk.SpaceID, sp.ID)
	}
	loaded, err := a.Spaces().LoadSpace(sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	workerCount := 0
	var workerMessageID string
	for _, m := range loaded.Messages {
		if m.AuthorID == "coder" {
			workerCount++
			workerMessageID = m.ID
		}
	}
	if workerCount != 1 {
		t.Fatalf("worker-authored message count = %d, want 1 (no double-write)", workerCount)
	}
	if tk.ResultMessageID != workerMessageID {
		t.Fatalf("ResultMessageID = %q, want %q", tk.ResultMessageID, workerMessageID)
	}
	if tk.Outcome == "" {
		t.Fatal("Outcome should be non-empty short summary")
	}
	if !strings.Contains(tk.Outcome, "audit complete") {
		t.Fatalf("Outcome = %q, want short summary containing reply", tk.Outcome)
	}
}

func TestDelegateInSpaceRejectsUnknownWorker(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	source := "desktop"
	sp := mustEnsureChannelSpace(t, a, source)
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "go", nil); err != nil {
		t.Fatal(err)
	}
	m := newManager(a)
	id, ok, err := m.tryDelegateInSpace(context.Background(), source, "ghost", "stub", "do it")
	if !ok {
		t.Fatal("space-anchored delegate must own the result; ok must be true")
	}
	if !errors.Is(err, ErrUnknownWorker) {
		t.Fatalf("err = %v, want ErrUnknownWorker", err)
	}
	if id != "" {
		t.Fatalf("task id = %q, want empty (no task created)", id)
	}
	tasks, err := a.Tasks().ListBySpace(sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("task store has %d tasks, want 0 (rejection must not create a task)", len(tasks))
	}
}

func TestDelegateInSpaceFallsBackWhenSourceHasNoSpace(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	mustRegisterPersona(t, a, "coder", "Coder")
	m := newManager(a)
	if _, ok, _ := m.tryDelegateInSpace(context.Background(), "subtask:legacy", "coder", "stub", "go"); ok {
		t.Fatal("subtask:* must not engage space delegate path")
	}
}

func TestDelegateInSpaceFallsBackWhenSpaceIsEmpty(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	mustRegisterPersona(t, a, "coder", "Coder")
	source := "desktop"
	mustEnsureChannelSpace(t, a, source)
	m := newManager(a)
	if _, ok, _ := m.tryDelegateInSpace(context.Background(), source, "coder", "stub", "go"); ok {
		t.Fatal("empty space (no trigger message) must fall back to legacy path")
	}
}

func TestDelegateInSpaceMarksEmptyOutputWhenWorkerReplyEmpty(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			return nil
		}), nil
	})
	mustRegisterPersona(t, a, "coder", "Coder")
	source := "desktop"
	sp := mustEnsureChannelSpace(t, a, source)
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "go", nil); err != nil {
		t.Fatal(err)
	}
	m := newManager(a)
	ctx := command.WithPersona(command.WithSource(context.Background(), source), "user")
	id, ok, err := m.tryDelegateInSpace(ctx, source, "coder", "stub", "do it")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected space delegate path to engage")
	}
	tk := waitForTaskStatus(t, a, id, taskpkg.StatusEmptyOutput, 2*time.Second)
	loaded, _ := a.Spaces().LoadSpace(sp.ID)
	for _, m := range loaded.Messages {
		if m.AuthorID == "coder" {
			t.Fatalf("empty output should not write any worker message, found %+v", m)
		}
	}
	if tk.ResultMessageID != "" {
		t.Fatalf("ResultMessageID = %q, want empty", tk.ResultMessageID)
	}
}

func TestKeyStepTitlesAreTemplateGeneratedNotOutput(t *testing.T) {
	cases := []struct {
		name        string
		toolName    string
		args        []byte
		wantPrefix  string
	}{
		{"bash", "bash", []byte(`{"cmd":"ls -la"}`), "Ran bash "},
		{"read", "read", []byte(`{"path":"main.go"}`), "Read "},
		{"write", "write", []byte(`{"path":"out.txt"}`), "Wrote "},
		{"delegate", "delegate", []byte(`{"target":"reviewer"}`), "Delegated to "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			step, ok := stepFromToolCall(msg.ToolCall{Name: c.toolName, Args: c.args}, time.Now())
			if !ok {
				t.Fatal("expected step")
			}
			if !strings.HasPrefix(step.Title, c.wantPrefix) {
				t.Fatalf("Title = %q, want template prefix %q", step.Title, c.wantPrefix)
			}
			if len([]rune(step.Title)) > taskpkg.MaxTitleLen {
				t.Fatalf("Title length %d > %d", len([]rune(step.Title)), taskpkg.MaxTitleLen)
			}
		})
	}
}

func TestKeyStepTitleTruncatesLongInputField(t *testing.T) {
	long := strings.Repeat("a", 200)
	step, ok := stepFromToolCall(msg.ToolCall{Name: "bash", Args: []byte(`{"cmd":"` + long + `"}`)}, time.Now())
	if !ok {
		t.Fatal("expected step")
	}
	if len([]rune(step.Title)) > taskpkg.MaxTitleLen {
		t.Fatalf("Title length %d > %d", len([]rune(step.Title)), taskpkg.MaxTitleLen)
	}
	if !strings.HasPrefix(step.Title, "Ran bash ") {
		t.Fatalf("Title lost its template prefix: %q", step.Title)
	}
}

func TestSummarizeAddedStepsRejectsToolOutputContent(t *testing.T) {
	added := []msg.Message{{
		Role: "assistant",
		ToolCalls: []msg.ToolCall{{
			Name: "read",
			Args: []byte(`{"path":"plan.md"}`),
		}},
	}, {
		Role: "tool",
		ToolResults: []msg.ToolResult{{
			ToolCallID: "tool-1",
			Content:    "PLAN: kill the moon. RAW SECRET DATA.",
		}},
	}}
	steps := summarizeAddedSteps(added, nil)
	for _, s := range steps {
		if strings.Contains(s.Title, "PLAN") || strings.Contains(s.Title, "SECRET") {
			t.Fatalf("KeyStep leaked tool output content into title: %q", s.Title)
		}
	}
}
