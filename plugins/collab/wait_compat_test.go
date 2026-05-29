package collab

import (
	"testing"
	"time"

	"github.com/abcdlsj/sumi/config"
	taskpkg "github.com/abcdlsj/sumi/task"
)

func TestWaitReadsTaskStoreFirst(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: "sp-1", TriggerMessageID: "msg-1", InitiatorID: "user", WorkerID: "coder", Title: "audit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Update(tk.ID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusFinished, Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	m := newManager(a)
	out, err := m.wait(tk.ID, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if out != "ok" {
		t.Fatalf("out = %q, want ok", out)
	}
}

func TestWaitReportsFailedTaskFromStore(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: "sp-1", TriggerMessageID: "msg-1", InitiatorID: "user", WorkerID: "coder", Title: "audit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks().Update(tk.ID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusFailed, Outcome: "boom"}); err != nil {
		t.Fatal(err)
	}
	m := newManager(a)
	if _, err := m.wait(tk.ID, 100*time.Millisecond); err == nil {
		t.Fatal("expected error when task store reports failed")
	}
}

func TestWaitFallsBackToLegacyForNonTaskStoreID(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	m := newManager(a)
	if _, err := m.wait("legacy-task-id", 10*time.Millisecond); err == nil {
		t.Fatal("expected error for unknown id")
	}
}
