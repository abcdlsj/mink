package collab

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
	taskpkg "github.com/abcdlsj/sumi/task"
)

func TestCancelToolMarksTaskStoreCanceled(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID: "sp-1", TriggerMessageID: "msg-1", InitiatorID: "user", WorkerID: "coder", Title: "audit",
	})
	if err != nil {
		t.Fatal(err)
	}
	tool := cancelTool{m: newManager(a)}
	args, _ := json.Marshal(map[string]string{"task_id": tk.ID})
	ctx := command.WithSource(context.Background(), "desktop")
	if _, err := tool.Run(ctx, args); err != nil {
		t.Fatal(err)
	}
	got, err := a.Tasks().Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != taskpkg.StatusCanceled {
		t.Fatalf("status = %v, want canceled", got.Status)
	}
}

func TestCancelToolUnknownTaskReturnsError(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	tool := cancelTool{m: newManager(a)}
	args, _ := json.Marshal(map[string]string{"task_id": "does-not-exist"})
	ctx := command.WithSource(context.Background(), "desktop")
	if _, err := tool.Run(ctx, args); err == nil {
		t.Fatal("expected error for unknown task id")
	}
}
