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
	ctx := taskToolCtxWithInput("desktop:channel:"+sp.ID, "planner", msg.ID, "请创建任务给 dev")

	args := map[string]string{
		"title":               "wire task tools",
		"assignee_id":         "dev",
		"expected_outcome":    "agent can create assigned tasks",
		"acceptance_criteria": "tests cover capability guard",
		"space_id":            sp.ID,
		"source_message_id":   msg.ID,
		"authorization_text":  "请创建任务给 dev",
	}
	out, err := a.tools.Run(ctx, "task_create", mustJSON(t, args))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "task proposal prepared:") {
		t.Fatalf("output = %q", out)
	}
	tasks, err := a.Tasks().ListBySpace(sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks len = %d, want 0 before human commit", len(tasks))
	}
	pending := a.PendingTaskCreateProposals(10)
	if len(pending) != 1 {
		t.Fatalf("pending proposals = %#v", pending)
	}
	var payload TaskCreateProposalPayload
	if err := json.Unmarshal(pending[0].Proposal.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CreatedBy != "planner" || payload.AssignedBy != "planner" || payload.AssigneeID != "dev" {
		t.Fatalf("delegation metadata = %#v", payload)
	}
	if payload.ExpectedOutcome != args["expected_outcome"] || payload.AcceptanceCriteria != args["acceptance_criteria"] {
		t.Fatalf("task planning fields = %#v", payload)
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

func TestTaskCreateToolRejectsVagueConversationTasks(t *testing.T) {
	a := newTaskToolTestApp(t)
	sp, msg := newTaskToolSpace(t, a)
	ctx := taskToolCtxWithInput("desktop:channel:"+sp.ID, "planner", msg.ID, "请创建任务给 dev")

	_, err := a.tools.Run(ctx, "task_create", mustJSON(t, map[string]string{
		"title":               "查一下链接",
		"assignee_id":         "dev",
		"expected_outcome":    "完成",
		"acceptance_criteria": "看一下就行",
		"space_id":            sp.ID,
		"source_message_id":   msg.ID,
		"authorization_text":  "请创建任务给 dev",
	}))
	if err == nil || !strings.Contains(err.Error(), "simple Q&A") {
		t.Fatalf("expected vague task rejection, got %v", err)
	}
}

func TestTaskCreateToolRequiresExplicitCurrentUserTaskIntent(t *testing.T) {
	a := newTaskToolTestApp(t)
	sp, msg := newTaskToolSpace(t, a)
	args := map[string]string{
		"title":               "fix login regression",
		"assignee_id":         "dev",
		"expected_outcome":    "login regression is fixed and verified",
		"acceptance_criteria": "regression test covers the login path",
		"space_id":            sp.ID,
		"source_message_id":   msg.ID,
		"authorization_text":  "修复这个登录 bug",
	}
	_, err := a.tools.Run(taskToolCtxWithInput("desktop:channel:"+sp.ID, "planner", msg.ID, "修复这个登录 bug"), "task_create", mustJSON(t, args))
	if err == nil || !strings.Contains(err.Error(), "explicitly ask") {
		t.Fatalf("expected explicit task intent error, got %v", err)
	}

	args["authorization_text"] = "请创建任务给 dev"
	_, err = a.tools.Run(taskToolCtxWithInput("desktop:channel:"+sp.ID, "planner", msg.ID, "请创建任务给 dev"), "task_create", mustJSON(t, args))
	if err != nil {
		t.Fatalf("explicit task intent should allow task_create: %v", err)
	}
}

func TestTaskToolBlocksDefaultToProposeOnly(t *testing.T) {
	blocks := taskToolBlocks(&persona.Persona{ID: "planner", Capabilities: []string{"task.assign"}})
	for _, name := range []string{"task_assign", "task_update_status"} {
		if _, ok := blocks[name]; !ok {
			t.Fatalf("propose-only should block %s: %#v", name, blocks)
		}
	}
	if _, ok := blocks["task_create"]; ok {
		t.Fatalf("propose-only should keep task_create available for proposal prep: %#v", blocks)
	}

	if blocks := taskToolBlocks(&persona.Persona{ID: "planner", TaskPolicy: "auto_commit"}); blocks != nil {
		t.Fatalf("auto_commit should expose task tools, got %#v", blocks)
	}
}

func TestTaskCreateToolPreparesProposalWhenPolicyIsProposeOnly(t *testing.T) {
	a := newTaskToolTestApp(t)
	sp, msg := newTaskToolSpace(t, a)
	ctx := taskToolCtxWithInput("desktop:channel:"+sp.ID, "planner", msg.ID, "请创建任务给 dev")
	out, err := a.tools.Run(ctx, "task_create", mustJSON(t, map[string]string{
		"title":               "prepare only",
		"assignee_id":         "dev",
		"expected_outcome":    "proposal exists without task mutation",
		"acceptance_criteria": "no real task before human commit",
		"space_id":            sp.ID,
		"source_message_id":   msg.ID,
		"authorization_text":  "请创建任务给 dev",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "task proposal prepared:") {
		t.Fatalf("output = %q", out)
	}
	tasks, err := a.Tasks().ListBySpace(sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks len = %d, want 0 before commit", len(tasks))
	}
	if pending := a.PendingTaskCreateProposals(10); len(pending) != 1 {
		t.Fatalf("pending proposals = %#v", pending)
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

func taskToolCtxWithInput(source, personaID, parentMessageID, input string) context.Context {
	ctx := command.WithRunContext(context.Background(), command.RunContext{
		Source: source,
		Input:  input,
	})
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
