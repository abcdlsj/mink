package store

import (
	"path/filepath"
	"testing"

	"github.com/abcdlsj/sumi/task"
)

func newStoreFor(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestTaskStoreSaveLoadRoundTrip(t *testing.T) {
	s := newStoreFor(t)
	tk := &task.Task{
		ID:               "task-abc",
		SpaceID:          "space-1",
		TriggerMessageID: "msg-1",
		InitiatorID:      "user",
		WorkerID:         "coder",
		Title:            "audit retry policy",
		Status:           task.StatusQueued,
	}
	if err := s.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadTask("task-abc")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != "task-abc" || got.WorkerID != "coder" {
		t.Fatalf("loaded = %+v", got)
	}
}

func TestTaskStoreListBySpaceFilters(t *testing.T) {
	s := newStoreFor(t)
	a := &task.Task{ID: "task-a", SpaceID: "sp-a", InitiatorID: "u", WorkerID: "w", Title: "A", Status: task.StatusQueued}
	b := &task.Task{ID: "task-b", SpaceID: "sp-b", InitiatorID: "u", WorkerID: "w", Title: "B", Status: task.StatusQueued}
	for _, tk := range []*task.Task{a, b} {
		if err := s.SaveTask(tk); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListTasksBySpace("sp-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "task-a" {
		t.Fatalf("got = %+v", got)
	}
}

func TestRunStoreSaveLoadRoundTrip(t *testing.T) {
	s := newStoreFor(t)
	r := &task.Run{
		ID:     "run-xyz",
		TaskID: "task-1",
		Status: task.StatusRunning,
		KeySteps: []task.KeyStep{
			{Kind: task.KindRead, Title: "Read project files", OK: true},
		},
	}
	if err := s.SaveRun(r); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadRun("run-xyz")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.TaskID != "task-1" || len(got.KeySteps) != 1 {
		t.Fatalf("loaded = %+v", got)
	}
}

func TestListRunsByTaskFilters(t *testing.T) {
	s := newStoreFor(t)
	for _, taskID := range []string{"t-1", "t-1", "t-2"} {
		r := &task.Run{ID: "run-" + taskID + "-" + t.Name() + "-" + randomTag(), TaskID: taskID, Status: task.StatusRunning}
		if err := s.SaveRun(r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListRunsByTask("t-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
}

var seed int64

func randomTag() string {
	seed++
	return [...]string{"a", "b", "c", "d", "e", "f", "g"}[int(seed)%7]
}
