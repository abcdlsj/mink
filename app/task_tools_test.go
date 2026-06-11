package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
)

func TestTaskCreateToolRequiresCapabilityAndExecutableAssignee(t *testing.T) {
	a := newTaskToolTestApp(t)
	sp, msg := newTaskToolSpace(t, a)
	ctx := taskToolCtx("desktop:channel:"+sp.ID, "planner", msg.ID)

	args := map[string]string{
		"title":               "wire task tools",
		"assignee_id":         "dev",
		"expected_outcome":    "agent can create assigned tasks",
		"acceptance_criteria": "tests cover capability guard",
		"space_id":            sp.ID,
		"source_message_id":   msg.ID,
	}
	out, err := a.tools.Run(ctx, "task_create", mustJSON(t, args))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "assigned_to=dev") {
		t.Fatalf("output = %q", out)
	}
	tasks, err := a.Tasks().ListBySpace(sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks len = %d, want 1", len(tasks))
	}
	tk := tasks[0]
	if tk.CreatedBy != "planner" || tk.AssignedBy != "planner" || tk.WorkerID != "dev" {
		t.Fatalf("delegation metadata = %#v", tk)
	}
	if tk.ExpectedOutcome != args["expected_outcome"] || tk.AcceptanceCriteria != args["acceptance_criteria"] {
		t.Fatalf("task planning fields = %#v", tk)
	}

	_, err = a.tools.Run(taskToolCtx("desktop:channel:"+sp.ID, "viewer", msg.ID), "task_create", mustJSON(t, args))
	if err == nil || !strings.Contains(err.Error(), "lacks required capability") {
		t.Fatalf("expected capability error, got %v", err)
	}

	args["assignee_id"] = "viewer"
	_, err = a.tools.Run(ctx, "task_create", mustJSON(t, args))
	if err == nil || !strings.Contains(err.Error(), "lacks required capability: task.execute") {
		t.Fatalf("expected executable assignee error, got %v", err)
	}
}

func TestTaskAssignAndStatusToolsAreCapabilityGated(t *testing.T) {
	a := newTaskToolTestApp(t)
	sp, msg := newTaskToolSpace(t, a)
	tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:            sp.ID,
		TriggerMessageID:   msg.ID,
		InitiatorID:        "planner",
		CreatedBy:          "planner",
		WorkerID:           "dev",
		AssignedBy:         "planner",
		Title:              "ship work",
		ExpectedOutcome:    "done",
		AcceptanceCriteria: "reviewed",
		Source:             "desktop:channel:" + sp.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.tools.Run(taskToolCtx("desktop:channel:"+sp.ID, "viewer", msg.ID), "task_assign", mustJSON(t, map[string]string{
		"task_id":     tk.ID,
		"assignee_id": "dev",
	}))
	if err == nil || !strings.Contains(err.Error(), "lacks required capability") {
		t.Fatalf("expected assign capability error, got %v", err)
	}

	out, err := a.tools.Run(taskToolCtx("desktop:channel:"+sp.ID, "planner", msg.ID), "task_assign", mustJSON(t, map[string]string{
		"task_id":     tk.ID,
		"assignee_id": "dev2",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "assigned_to=dev2") {
		t.Fatalf("assign output = %q", out)
	}

	_, err = a.tools.Run(taskToolCtx("desktop:channel:"+sp.ID, "dev", msg.ID), "task_update_status", mustJSON(t, map[string]string{
		"task_id": tk.ID,
		"status":  "doing",
	}))
	if err == nil || !strings.Contains(err.Error(), "assigned to dev2") {
		t.Fatalf("expected assignment guard, got %v", err)
	}

	if _, err := a.tools.Run(taskToolCtx("desktop:channel:"+sp.ID, "dev2", msg.ID), "task_update_status", mustJSON(t, map[string]string{
		"task_id": tk.ID,
		"status":  "review",
	})); err != nil {
		t.Fatal(err)
	}

	_, err = a.tools.Run(taskToolCtx("desktop:channel:"+sp.ID, "dev2", msg.ID), "task_update_status", mustJSON(t, map[string]string{
		"task_id": tk.ID,
		"status":  "done",
	}))
	if err == nil || !strings.Contains(err.Error(), "task.review") {
		t.Fatalf("expected review capability error, got %v", err)
	}

	if _, err := a.tools.Run(taskToolCtx("desktop:channel:"+sp.ID, "reviewer", msg.ID), "task_update_status", mustJSON(t, map[string]string{
		"task_id": tk.ID,
		"status":  "done",
		"outcome": "approved",
	})); err != nil {
		t.Fatal(err)
	}
	got, err := a.Tasks().Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != taskpkg.StatusFinished || got.Outcome != "approved" {
		t.Fatalf("task = %#v", got)
	}
}

func newTaskToolTestApp(t *testing.T) *App {
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
	for id, caps := range map[string][]string{
		"planner":  {"task.assign"},
		"dev":      {"task.execute"},
		"dev2":     {"task.execute"},
		"reviewer": {"task.review"},
		"viewer":   nil,
	} {
		if _, err := a.Personas().Create(id, persona.Meta{Runtime: "stub", Capabilities: caps}, ""); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func newTaskToolSpace(t *testing.T, a *App) (*space.Space, space.Message) {
	t.Helper()
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "alpha", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := a.Spaces().AppendUserMessage(sp.ID, "please split work", nil)
	if err != nil {
		t.Fatal(err)
	}
	return sp, msg
}

func taskToolCtx(source, personaID, parentMessageID string) context.Context {
	ctx := command.WithSource(context.Background(), source)
	ctx = command.WithPersona(ctx, personaID)
	ctx = command.WithParentMessage(ctx, parentMessageID)
	return ctx
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
