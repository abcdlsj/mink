package collab

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/bus"
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

func TestCancelToolPublishesDelegateCanceledWithMetadata(t *testing.T) {
	a := newTestApp(t, config.CollabConfig{})
	tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:          "sp-99",
		TriggerMessageID: "msg-trigger",
		InitiatorID:      "user",
		WorkerID:         "coder",
		Title:            "audit",
		Source:           "desktop",
	})
	if err != nil {
		t.Fatal(err)
	}

	events, cancelSub := a.Bus().Subscribe(64)
	captured := []bus.Event{}
	var mu sync.Mutex
	doneCh := make(chan struct{})
	go func() {
		for ev := range events {
			mu.Lock()
			captured = append(captured, ev)
			mu.Unlock()
		}
		close(doneCh)
	}()

	tool := cancelTool{m: newManager(a)}
	args, _ := json.Marshal(map[string]string{"task_id": tk.ID})
	ctx := command.WithSource(context.Background(), "desktop")
	if _, err := tool.Run(ctx, args); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	cancelSub()
	<-doneCh

	mu.Lock()
	defer mu.Unlock()
	var found *bus.Event
	for i := range captured {
		if captured[i].Type == bus.DelegateCanceled {
			found = &captured[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected DelegateCanceled event after cancel_delegation, got events: %+v", captured)
	}
	if found.TaskID != tk.ID {
		t.Fatalf("TaskID = %q, want %q", found.TaskID, tk.ID)
	}
	if found.SpaceID != "sp-99" {
		t.Fatalf("SpaceID = %q, want sp-99", found.SpaceID)
	}
	if found.ParentMessageID != "msg-trigger" {
		t.Fatalf("ParentMessageID = %q, want msg-trigger (the trigger)", found.ParentMessageID)
	}
	if found.AgentID != "coder" {
		t.Fatalf("AgentID = %q, want coder", found.AgentID)
	}
}
