package collab

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
)

func waitTaskFinishes(t *testing.T, a interface {
	Tasks() *taskpkg.Manager
}, id string, want taskpkg.Status, timeout time.Duration) *taskpkg.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tk, err := a.Tasks().Get(id)
		if err == nil && tk.Status == want {
			return tk
		}
		time.Sleep(10 * time.Millisecond)
	}
	tk, _ := a.Tasks().Get(id)
	t.Fatalf("task %s did not reach %s in %s, last = %+v", id, want, timeout, tk)
	return nil
}

func TestSpawnSpecialistBindOnlyDoesNotCreateTask(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	if _, err := a.Personas().Create("auditor", persona.Meta{Display: "Auditor", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	tool := specialistTool{m: newManager(a)}
	args := json.RawMessage(`{"role_name":"Auditor","role_description":"audit risk","agent_id":"auditor"}`)
	ctx := command.WithSource(context.Background(), "desktop")
	if _, err := tool.Run(ctx, args); err != nil {
		t.Fatal(err)
	}
	tasks, _ := a.Tasks().ListBySpace(sp.ID)
	if len(tasks) != 0 {
		t.Fatalf("bind-only specialist must NOT create task, got %d", len(tasks))
	}
}

func TestSpawnSpecialistWithTaskCreatesTaskInStore(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	if _, err := a.Personas().Create("auditor", persona.Meta{Display: "Auditor", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "first findings"})
			return nil
		}), nil
	})
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	tool := specialistTool{m: newManager(a)}
	args := json.RawMessage(`{"role_name":"Auditor","role_description":"audit risk","agent_id":"auditor","task":"audit retry policy"}`)
	ctx := command.WithSource(context.Background(), "desktop")
	if _, err := tool.Run(ctx, args); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tasks, _ := a.Tasks().ListBySpace(sp.ID)
		if len(tasks) == 1 && tasks[0].Status == taskpkg.StatusFinished {
			if tasks[0].WorkerID != "auditor" {
				t.Fatalf("worker = %q", tasks[0].WorkerID)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	tasks, _ := a.Tasks().ListBySpace(sp.ID)
	t.Fatalf("specialist with task did not finish: %+v", tasks)
}

func TestInviteAgentBindOnlyDoesNotCreateTask(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	if _, err := a.Personas().Create("scout", persona.Meta{Display: "Scout", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	tool := inviteTool{m: newManager(a)}
	args := json.RawMessage(`{"agent_id":"scout","role_name":"Scout"}`)
	ctx := command.WithSource(context.Background(), "desktop")
	if _, err := tool.Run(ctx, args); err != nil {
		t.Fatal(err)
	}
	tasks, _ := a.Tasks().ListBySpace(sp.ID)
	if len(tasks) != 0 {
		t.Fatalf("bind-only invite must NOT create task, got %d", len(tasks))
	}
}

func TestInviteAgentWithTaskCreatesTaskInStore(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	if _, err := a.Personas().Create("scout", persona.Meta{Display: "Scout", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	a.RegisterRuntime("stub", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			turn.Session.Add(msg.Message{Role: "assistant", Content: "first scout report"})
			return nil
		}), nil
	})
	target := space.MapSource("desktop")
	sp, _ := a.Spaces().EnsureForSource("desktop", space.PersonaInfo{ID: target.Seed})
	if _, err := a.Spaces().AppendUserMessage(sp.ID, "kick", nil); err != nil {
		t.Fatal(err)
	}
	tool := inviteTool{m: newManager(a)}
	args := json.RawMessage(`{"agent_id":"scout","role_name":"Scout","task":"check uplink"}`)
	ctx := command.WithSource(context.Background(), "desktop")
	if _, err := tool.Run(ctx, args); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tasks, _ := a.Tasks().ListBySpace(sp.ID)
		if len(tasks) == 1 && tasks[0].Status == taskpkg.StatusFinished {
			if tasks[0].WorkerID != "scout" {
				t.Fatalf("worker = %q", tasks[0].WorkerID)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("invite with task did not finish")
}
