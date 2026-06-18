package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
)

func newBackendWithApp(t *testing.T) (*Backend, *app.App) {
	t.Helper()
	dir := t.TempDir()
	a, err := app.New(config.Config{
		Runtime:   "stub",
		DataDir:   filepath.Join(dir, "sumi-data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return newBackend(a), a
}

func TestMemoryOverviewIncludesRecentItemsWithoutPaths(t *testing.T) {
	b, a := newBackendWithApp(t)
	dir := filepath.Join(a.Config().MemoryDir(), "persona", "coder")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pref-1.md"), []byte(`---
title: "Reply style"
kind: "preference"
summary: "User prefers concise Chinese replies."
updated_at: 2026-06-18T01:02:03Z
---

# Reply style

User prefers concise Chinese replies.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := b.MemoryOverview("coder", "", "")
	var found bool
	for _, sc := range got.Scopes {
		if strings.Contains(sc.Label+sc.Key, a.Config().MemoryDir()) || strings.Contains(sc.Label+sc.Key, a.Workspace()) {
			t.Fatalf("memory scope leaked path: %#v", sc)
		}
		if sc.Kind != "persona" || sc.Key != "coder" {
			continue
		}
		found = true
		if len(sc.Recent) != 1 {
			t.Fatalf("recent = %#v, want one item", sc.Recent)
		}
		doc := sc.Recent[0]
		if doc.ID != "pref-1" || doc.Title != "Reply style" || !strings.Contains(doc.Summary, "concise Chinese") {
			t.Fatalf("doc = %#v", doc)
		}
		if strings.Contains(doc.Title+doc.Summary+doc.ID, a.Config().MemoryDir()) {
			t.Fatalf("memory overview leaked path: %#v", doc)
		}
	}
	if !found {
		t.Fatalf("persona scope missing: %#v", got.Scopes)
	}
}

func TestSpaceRecentRunsReadsTaskStoreBySpaceID(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "alpha", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.Spaces().EnsureSpace(space.KindChannel, "beta", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: "msg-1", InitiatorID: "user", WorkerID: "coder", Title: "alpha task",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: other.ID, TriggerMessageID: "msg-2", InitiatorID: "user", WorkerID: "coder", Title: "beta task",
	}); err != nil {
		t.Fatal(err)
	}

	got, archived := b.spaceRecentRuns(sp)
	if archived != 0 {
		t.Fatalf("archived = %d, want 0", archived)
	}
	if len(got) != 1 {
		t.Fatalf("got %d runs for space alpha, want 1: %#v", len(got), got)
	}
	if got[0].Title != "alpha task" {
		t.Fatalf("title = %q, want alpha task", got[0].Title)
	}
	if got[0].AgentID != "coder" {
		t.Fatalf("AgentID = %q, want coder", got[0].AgentID)
	}
	if got[0].Lifecycle != "active" {
		t.Fatalf("Lifecycle = %q, want active", got[0].Lifecycle)
	}
}

func TestSpaceRecentRunsReturnsActiveRunsAndArchivedCount(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "alpha", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	active, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: "msg-1", InitiatorID: "user", WorkerID: "coder", Title: "active task",
	})
	if err != nil {
		t.Fatal(err)
	}
	archived, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: "msg-2", InitiatorID: "user", WorkerID: "coder", Title: "archived task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Update(active.ID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Update(archived.ID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusFinished}); err != nil {
		t.Fatal(err)
	}

	got, archivedCount := b.spaceRecentRuns(sp)
	if len(got) != 1 {
		t.Fatalf("got %d active runs, want 1: %#v", len(got), got)
	}
	if got[0].Title != "active task" {
		t.Fatalf("active title = %q", got[0].Title)
	}
	if archivedCount != 1 {
		t.Fatalf("archived = %d, want 1", archivedCount)
	}
}

func TestCapabilitiesDefaultTaskStatesAreActiveOnly(t *testing.T) {
	b, a := newBackendWithApp(t)
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "alpha", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	active, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: "msg-1", InitiatorID: "user", WorkerID: "coder", Title: "active task",
		State: taskpkg.TaskState{Checkpoint: "queued"},
	})
	if err != nil {
		t.Fatal(err)
	}
	archived, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: "msg-2", InitiatorID: "user", WorkerID: "coder", Title: "archived task",
		State: taskpkg.TaskState{Checkpoint: "done"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Update(active.ID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Update(archived.ID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusFinished}); err != nil {
		t.Fatal(err)
	}

	got := b.Capabilities()
	if len(got.Tasks) != 1 {
		t.Fatalf("capability tasks = %d, want 1: %#v", len(got.Tasks), got.Tasks)
	}
	if got.Tasks[0].Title != "active task" || got.Tasks[0].Lifecycle != "active" {
		t.Fatalf("task card = %#v", got.Tasks[0])
	}
	if got.ArchivedTaskStateCount != 1 {
		t.Fatalf("ArchivedTaskStateCount = %d, want 1", got.ArchivedTaskStateCount)
	}
}

func TestCapabilitiesIncludesAssignedTaskWithoutRuntimeState(t *testing.T) {
	b, a := newBackendWithApp(t)
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "alpha", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:            sp.ID,
		TriggerMessageID:   "msg-1",
		InitiatorID:        "user",
		CreatedBy:          "pmo",
		WorkerID:           "dev",
		AssignedBy:         "cto",
		Title:              "assigned task",
		ExpectedOutcome:    "patch",
		AcceptanceCriteria: "tests pass",
	})
	if err != nil {
		t.Fatal(err)
	}

	got := b.Capabilities()
	if len(got.Tasks) != 1 || got.Tasks[0].ID != tk.ID {
		t.Fatalf("capability tasks = %#v, want assigned task", got.Tasks)
	}
	if got.Tasks[0].CreatedBy != "pmo" || got.Tasks[0].Assignee != "dev" || got.Tasks[0].AssignedBy != "cto" {
		t.Fatalf("task delegation = %#v", got.Tasks[0])
	}
	if got.Tasks[0].ExpectedOutcome != "patch" || got.Tasks[0].AcceptanceCriteria != "tests pass" {
		t.Fatalf("task quality = %#v", got.Tasks[0])
	}
}

func TestUpdateTaskStatusMapsKanbanActions(t *testing.T) {
	b, a := newBackendWithApp(t)
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "alpha", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := a.Spaces().AppendUserMessage(sp.ID, "please audit", nil)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: msg.ID, InitiatorID: "user", WorkerID: "coder", Title: "audit",
	})
	if err != nil {
		t.Fatal(err)
	}

	review, err := b.UpdateTaskStatus(tk.ID, "review")
	if err != nil {
		t.Fatal(err)
	}
	if review.Status != "in_review" || review.Lifecycle != "active" {
		t.Fatalf("review run = %#v", review)
	}
	if review.ParentMessageID != msg.ID || review.TriggerMessageID != msg.ID {
		t.Fatalf("message anchors = parent %q trigger %q, want %q", review.ParentMessageID, review.TriggerMessageID, msg.ID)
	}

	done, err := b.UpdateTaskStatus(tk.ID, "done")
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != "finished" || done.Lifecycle != "archived" {
		t.Fatalf("done run = %#v", done)
	}
}

func TestCreateAndAssignTaskExposeDelegationMetadata(t *testing.T) {
	b, a := newBackendWithApp(t)
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "alpha", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := a.Spaces().AppendUserMessage(sp.ID, "ship this", nil)
	if err != nil {
		t.Fatal(err)
	}

	created, err := b.CreateTask(CreateTaskRequest{
		SpaceID:            sp.ID,
		SourceMessageID:    msg.ID,
		SourceThreadID:     msg.ID,
		CreatedBy:          "cto",
		Assignee:           "dev",
		AssignedBy:         "pmo",
		Title:              "implement delegation",
		Outcome:            "DEV ships the patch",
		AcceptanceCriteria: "green tests and visible assignee",
		Source:             "desktop:test",
		ExplicitTaskIntent: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatedBy != "cto" || created.Assignee != "dev" || created.AssigneeID != "dev" || created.AssignedBy != "pmo" {
		t.Fatalf("created delegation = %#v", created)
	}
	if created.ExpectedOutcome != "DEV ships the patch" || created.AcceptanceCriteria != "green tests and visible assignee" {
		t.Fatalf("created quality = %#v", created)
	}
	if created.TriggerMessageID != msg.ID || created.SourceThreadID != msg.ID || created.ParentMessageID != msg.ID {
		t.Fatalf("created source = %#v", created)
	}

	assigned, err := b.AssignTask(AssignTaskRequest{
		TaskID:             created.ID,
		AssigneeID:         "reviewer",
		AssignedBy:         "cto",
		ExpectedOutcome:    "review the implementation",
		AcceptanceCriteria: "review complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Assignee != "reviewer" || assigned.WorkerID != "reviewer" || assigned.AssignedBy != "cto" {
		t.Fatalf("assigned delegation = %#v", assigned)
	}
	if assigned.ExpectedOutcome != "review the implementation" || assigned.AcceptanceCriteria != "review complete" {
		t.Fatalf("assigned quality = %#v", assigned)
	}
}

func TestCreateTaskRejectsVagueConversationCandidate(t *testing.T) {
	b, a := newBackendWithApp(t)
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "alpha", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := a.Spaces().AppendUserMessage(sp.ID, "查一下这个链接", nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = b.CreateTask(CreateTaskRequest{
		SpaceID:            sp.ID,
		SourceMessageID:    msg.ID,
		CreatedBy:          "user",
		Assignee:           "dev",
		AssignedBy:         "user",
		Title:              "查一下链接",
		ExpectedOutcome:    "完成",
		AcceptanceCriteria: "看一下就行",
		Source:             "desktop:test",
		ExplicitTaskIntent: true,
	})
	if err == nil || !strings.Contains(err.Error(), "concrete deliverable") {
		t.Fatalf("expected vague candidate rejection, got %v", err)
	}
}

func TestCreateTaskRequiresExplicitTaskIntent(t *testing.T) {
	b, a := newBackendWithApp(t)
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "alpha", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := a.Spaces().AppendUserMessage(sp.ID, "修复这个 bug", nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = b.CreateTask(CreateTaskRequest{
		SpaceID:            sp.ID,
		SourceMessageID:    msg.ID,
		CreatedBy:          "user",
		Assignee:           "dev",
		AssignedBy:         "user",
		Title:              "fix bug",
		ExpectedOutcome:    "bug is fixed and verified",
		AcceptanceCriteria: "test covers the fixed behavior",
		Source:             "desktop:test",
	})
	if err == nil || !strings.Contains(err.Error(), "explicit user task intent") {
		t.Fatalf("expected explicit task intent error, got %v", err)
	}
}

func TestGetRunDetailReturnsKeyStepsButNotResultBody(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "alpha", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: "msg-1", InitiatorID: "user", WorkerID: "coder", Title: "audit",
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.Tasks().StartRun(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().FinishRun(r.ID, taskpkg.FinishRunInput{
		Status: taskpkg.StatusFinished,
		KeySteps: []taskpkg.KeyStep{
			{Kind: taskpkg.KindRead, Title: "Read plan.md", OK: true},
			{Kind: taskpkg.KindRun, Title: "Ran tests", OK: true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Update(tk.ID, taskpkg.UpdateTaskInput{
		Status:          taskpkg.StatusFinished,
		ResultMessageID: "msg-99",
		Outcome:         "all green",
	}); err != nil {
		t.Fatal(err)
	}

	detail := b.GetRunDetail(tk.ID)
	if detail.TaskID != tk.ID {
		t.Fatalf("TaskID = %q", detail.TaskID)
	}
	if detail.WorkerID != "coder" || detail.WorkerName != "Coder" {
		t.Fatalf("worker = %q/%q", detail.WorkerID, detail.WorkerName)
	}
	if detail.ResultMessageID != "msg-99" {
		t.Fatalf("ResultMessageID = %q", detail.ResultMessageID)
	}
	if detail.Outcome != "all green" {
		t.Fatalf("Outcome = %q", detail.Outcome)
	}
	if len(detail.KeySteps) != 2 {
		t.Fatalf("KeySteps = %d, want 2", len(detail.KeySteps))
	}
}

func TestGetRunDetailEmptyOutputUsesNoOutputUIToken(t *testing.T) {
	b, a := newBackendWithApp(t)
	if _, err := a.Personas().Create("coder", persona.Meta{Display: "Coder", Runtime: "stub"}, ""); err != nil {
		t.Fatal(err)
	}
	sp, err := a.Spaces().EnsureSpace(space.KindChannel, "alpha", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: sp.ID, TriggerMessageID: "msg-1", InitiatorID: "user", WorkerID: "coder", Title: "audit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Update(tk.ID, taskpkg.UpdateTaskInput{
		Status: taskpkg.StatusEmptyOutput,
	}); err != nil {
		t.Fatal(err)
	}
	detail := b.GetRunDetail(tk.ID)
	if detail.Status != "no_output" {
		t.Fatalf("status token = %q, want no_output", detail.Status)
	}
}

func TestGetRunDetailMissingTaskReturnsZero(t *testing.T) {
	b, _ := newBackendWithApp(t)
	if got := b.GetRunDetail("task-does-not-exist"); got.TaskID != "" {
		t.Fatalf("expected zero RunDetail, got %#v", got)
	}
}
